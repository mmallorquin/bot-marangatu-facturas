// Package store guarda las facturas en una base SQLite local.
// Cada factura pertenece a un chat: nunca se leen ni modifican facturas de otro chat.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // driver SQLite en Go puro (sin cgo)

	"github.com/mmallorquin/bot-marangatu-facturas/internal/invoice"
)

// Status es el estado de una factura.
type Status string

const (
	StatusDraft     Status = "borrador"   // leída, esperando que el usuario la confirme
	StatusSaved     Status = "guardada"   // confirmada: entra en el resumen y la exportación
	StatusDiscarded Status = "descartada" // el usuario la descartó
	StatusDeleted   Status = "borrada"    // estaba guardada y el usuario la sacó con /facturas
)

const (
	dirPermissions = 0o700 // la base tiene datos fiscales: solo el dueño puede leerla
	busyTimeoutMs  = 5000
)

var (
	ErrNotFound       = errors.New("factura no encontrada")
	ErrNotDraft       = errors.New("la factura ya fue guardada o descartada")
	ErrDuplicate      = errors.New("esa factura ya está guardada")
	ErrNotSaved       = errors.New("la factura no está guardada")
	ErrInvalidInvoice = errors.New("la factura tiene datos inválidos")
)

const schema = `
CREATE TABLE IF NOT EXISTS invoices (
	id             INTEGER PRIMARY KEY AUTOINCREMENT,
	chat_id        INTEGER NOT NULL,
	status         TEXT    NOT NULL,
	invoice_json   TEXT    NOT NULL, -- datos actuales (con correcciones)
	original_json  TEXT    NOT NULL, -- lo que leyó la IA, para medir precisión
	model          TEXT    NOT NULL,
	cost_usd       REAL    NOT NULL,
	corrected      INTEGER NOT NULL DEFAULT 0,
	awaiting_field TEXT,             -- campo que el usuario está corrigiendo
	dedup_key      TEXT,             -- tipo|RUC|timbrado|número, se completa al guardar
	period         TEXT,             -- AAAA-MM de la fecha de la factura, al guardar
	created_at     TEXT    NOT NULL,
	updated_at     TEXT    NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS invoices_saved_unique
	ON invoices (chat_id, dedup_key) WHERE status = 'guardada';
CREATE INDEX IF NOT EXISTS invoices_chat_period ON invoices (chat_id, status, period);

CREATE TABLE IF NOT EXISTS chat_settings (
	chat_id    INTEGER PRIMARY KEY,
	ruc        TEXT    NOT NULL DEFAULT '', -- RUC del contribuyente que informa
	impute_iva INTEGER NOT NULL DEFAULT 0,
	impute_ire INTEGER NOT NULL DEFAULT 0,
	impute_irp INTEGER NOT NULL DEFAULT 0  -- IRP-RSP
);

CREATE TABLE IF NOT EXISTS reminders (
	chat_id INTEGER NOT NULL,
	period  TEXT    NOT NULL, -- AAAA-MM o AAAA recordado
	sent_at TEXT    NOT NULL,
	PRIMARY KEY (chat_id, period)
);

CREATE TABLE IF NOT EXISTS exports (
	chat_id INTEGER NOT NULL,
	period  TEXT    NOT NULL, -- AAAA-MM
	seq     INTEGER NOT NULL, -- número de archivo del período: V0001, V0002...
	PRIMARY KEY (chat_id, period)
);

-- Una solicitud identifica un botón de exportación y permite reintentar
-- una entrega fallida sin reservar otra secuencia ni repetir una entrega exitosa.
CREATE TABLE IF NOT EXISTS export_requests (
	chat_id      INTEGER NOT NULL,
	period       TEXT    NOT NULL,
	request_key  TEXT    NOT NULL,
	seq          INTEGER NOT NULL,
	delivered_at TEXT    NOT NULL DEFAULT '',
	cancelled    INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (chat_id, period, request_key)
);

-- Declaración manual del usuario: no es una consulta ni una validación de DNIT.
CREATE TABLE IF NOT EXISTS presentations (
	chat_id INTEGER NOT NULL,
	period TEXT NOT NULL,
	revision TEXT NOT NULL,
	seq INTEGER NOT NULL,
	confirmed_at TEXT NOT NULL,
	PRIMARY KEY (chat_id, period)
);

-- Uso del bot para las métricas de la beta. Sin datos de las facturas.
CREATE TABLE IF NOT EXISTS events (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	chat_id    INTEGER NOT NULL,
	kind       TEXT    NOT NULL,
	detail     TEXT    NOT NULL DEFAULT '',
	seconds    REAL    NOT NULL DEFAULT 0, -- duración de la lectura
	cost_usd   REAL    NOT NULL DEFAULT 0, -- costo de la lectura en OpenRouter
	created_at TEXT    NOT NULL
);
CREATE INDEX IF NOT EXISTS events_created ON events (created_at);
`

// Draft es una factura recién leída.
type Draft struct {
	Invoice invoice.Invoice
	Model   string
	CostUSD float64
}

// Record es una factura guardada en la base.
type Record struct {
	ID        int64
	ChatID    int64
	Status    Status
	Invoice   invoice.Invoice // con las correcciones del usuario
	Original  invoice.Invoice // como la leyó la IA
	Model     string
	CostUSD   float64
	Corrected bool
}

// Pending es la corrección que el chat está esperando.
type Pending struct {
	ID    int64
	Field string
}

// Summary suma las facturas guardadas de un mes.
type Summary struct {
	Count   int
	Exempt  int64
	Taxed5  int64
	Taxed10 int64
	VAT5    int64
	VAT10   int64
	Total   int64
}

// Store es el repositorio de facturas.
type Store struct {
	db *sql.DB
}

// Open abre (o crea) la base en path, creando la carpeta si hace falta.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), dirPermissions); err != nil {
		return nil, fmt.Errorf("creando la carpeta de la base: %w", err)
	}
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(%d)&_pragma=journal_mode(WAL)", path, busyTimeoutMs)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("abriendo la base: %w", err)
	}
	db.SetMaxOpenConns(1) // SQLite escribe de a uno; evita errores de "database is locked"

	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("creando las tablas: %w", err)
	}
	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

// addedColumns son columnas que se agregaron después de crear la tabla. Las bases existentes
// las reciben al abrirse, sin perder datos.
var addedColumns = []struct{ table, column, definition string }{
	{"chat_settings", "registration", "TEXT NOT NULL DEFAULT '' CHECK (registration IN ('', '955', '956'))"},
	{"chat_settings", "auto_save", "INTEGER NOT NULL DEFAULT 0"},    // guardar solo las que cierran
	{"chat_settings", "reminders", "INTEGER NOT NULL DEFAULT 1"},    // recordatorio de exportar
	{"chat_settings", "awaiting_ruc", "INTEGER NOT NULL DEFAULT 0"}, // configuración guiada: el próximo texto es el RUC
	{"exports", "delivered_at", "TEXT NOT NULL DEFAULT ''"},         // reservar el número no confirma la entrega
	{"chat_settings", "pending_export", "TEXT NOT NULL DEFAULT ''"}, // retomar el período tras configurar
	{"chat_settings", "ruc_setup_key", "TEXT NOT NULL DEFAULT ''"},  // invalidar Cancelar de configuraciones anteriores
	{"export_requests", "revision", "TEXT NOT NULL DEFAULT ''"},     // datos entregados, para la marca manual
}

func migrate(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("iniciando las migraciones: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, c := range addedColumns {
		var exists bool
		err := tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM pragma_table_info(?) WHERE name = ?)`, c.table, c.column).Scan(&exists)
		if err != nil {
			return fmt.Errorf("revisando la columna %s.%s: %w", c.table, c.column, err)
		}
		if exists {
			continue
		}
		if _, err := tx.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", c.table, c.column, c.definition)); err != nil {
			return fmt.Errorf("agregando la columna %s.%s: %w", c.table, c.column, err)
		}
		if c.table == "exports" && c.column == "delivered_at" {
			// La versión anterior consideraba toda exportación como entregada.
			// Conservamos esa interpretación sin inventar una fecha de entrega.
			if _, err := tx.Exec(`UPDATE exports SET delivered_at = 'legacy'`); err != nil {
				return fmt.Errorf("preservando las exportaciones anteriores: %w", err)
			}
		}
	}
	return tx.Commit()
}

// OpenReadOnly abre una base existente sin poder modificarla, para consultarla
// mientras el bot sigue corriendo.
func OpenReadOnly(path string) (*Store, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("abriendo la base: %w", err)
	}
	dsn := fmt.Sprintf("file:%s?mode=ro&_pragma=busy_timeout(%d)", path, busyTimeoutMs)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("abriendo la base: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("abriendo la base: %w", err)
	}
	return &Store{db: db}, nil
}

// Close cierra la base.
func (s *Store) Close() error {
	return s.db.Close()
}

// CreateDraft guarda una factura recién leída y devuelve su ID.
func (s *Store) CreateDraft(ctx context.Context, chatID int64, d Draft) (int64, error) {
	original, err := json.Marshal(d.Invoice)
	if err != nil {
		return 0, fmt.Errorf("serializando la factura: %w", err)
	}
	d.Invoice.Number = invoice.NormalizeNumber(d.Invoice.Number)
	data, err := json.Marshal(d.Invoice)
	if err != nil {
		return 0, fmt.Errorf("serializando la factura normalizada: %w", err)
	}
	now := timestamp()
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO invoices (chat_id, status, invoice_json, original_json, model, cost_usd, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		chatID, StatusDraft, data, original, d.Model, d.CostUSD, now, now)
	if err != nil {
		return 0, fmt.Errorf("guardando el borrador: %w", err)
	}
	return res.LastInsertId()
}

// Get devuelve una factura del chat.
func (s *Store) Get(ctx context.Context, chatID, id int64) (Record, error) {
	var (
		rec                     Record
		invoiceJSON, originJSON string
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT id, chat_id, status, invoice_json, original_json, model, cost_usd, corrected
		FROM invoices WHERE id = ? AND chat_id = ?`, id, chatID).
		Scan(&rec.ID, &rec.ChatID, &rec.Status, &invoiceJSON, &originJSON, &rec.Model, &rec.CostUSD, &rec.Corrected)
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, ErrNotFound
	}
	if err != nil {
		return Record{}, fmt.Errorf("leyendo la factura: %w", err)
	}
	if err := json.Unmarshal([]byte(invoiceJSON), &rec.Invoice); err != nil {
		return Record{}, fmt.Errorf("leyendo los datos de la factura: %w", err)
	}
	if err := json.Unmarshal([]byte(originJSON), &rec.Original); err != nil {
		return Record{}, fmt.Errorf("leyendo la lectura original: %w", err)
	}
	// También permite revisar borradores creados antes de la normalización.
	rec.Invoice.Number = invoice.NormalizeNumber(rec.Invoice.Number)
	return rec, nil
}

// UpdateInvoice reemplaza los datos de un borrador y lo marca como corregido.
func (s *Store) UpdateInvoice(ctx context.Context, chatID, id int64, inv invoice.Invoice) error {
	data, err := json.Marshal(inv)
	if err != nil {
		return fmt.Errorf("serializando la factura: %w", err)
	}
	return s.inDraftTx(ctx, chatID, id, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			UPDATE invoices SET invoice_json = ?, corrected = 1, updated_at = ? WHERE id = ?`,
			data, timestamp(), id)
		return err
	})
}

// Save confirma un borrador validando sus datos actuales dentro de la transacción.
// Devuelve ErrInvalidInvoice si son incoherentes y ErrDuplicate si ya está guardada.
func (s *Store) Save(ctx context.Context, chatID, id int64) error {
	return s.inDraftTx(ctx, chatID, id, func(tx *sql.Tx) error {
		inv, err := currentInvoice(ctx, tx, id)
		if err != nil {
			return err
		}
		inv.Number = invoice.NormalizeNumber(inv.Number)
		if len(invoice.Validate(inv)) != 0 {
			return ErrInvalidInvoice
		}
		data, err := json.Marshal(inv)
		if err != nil {
			return fmt.Errorf("serializando la factura normalizada: %w", err)
		}
		key := dedupKey(inv)

		var exists bool
		err = tx.QueryRowContext(ctx, `
			SELECT EXISTS (SELECT 1 FROM invoices WHERE chat_id = ? AND status = ? AND dedup_key = ?)`,
			chatID, StatusSaved, key).Scan(&exists)
		if err != nil {
			return fmt.Errorf("buscando duplicados: %w", err)
		}
		if exists {
			return ErrDuplicate
		}

		_, err = tx.ExecContext(ctx, `
			UPDATE invoices SET status = ?, invoice_json = ?, dedup_key = ?, period = ?, awaiting_field = NULL, updated_at = ?
			WHERE id = ?`, StatusSaved, data, key, periodOf(inv.Date), timestamp(), id)
		return err
	})
}

// Drafts devuelve los borradores del chat (leídos, sin guardar ni descartar), del más viejo al más nuevo.
func (s *Store) Drafts(ctx context.Context, chatID int64) ([]Record, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM invoices WHERE chat_id = ? AND status = ? ORDER BY id`, chatID, StatusDraft)
	if err != nil {
		return nil, fmt.Errorf("leyendo los pendientes: %w", err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("leyendo los pendientes: %w", err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("leyendo los pendientes: %w", err)
	}

	drafts := make([]Record, 0, len(ids))
	for _, id := range ids {
		rec, err := s.Get(ctx, chatID, id)
		if err != nil {
			return nil, err
		}
		drafts = append(drafts, rec)
	}
	return drafts, nil
}

// Unsave vuelve una factura guardada a borrador (el "Deshacer" del guardado automático).
func (s *Store) Unsave(ctx context.Context, chatID, id int64) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE invoices SET status = ?, dedup_key = NULL, period = NULL, updated_at = ?
		WHERE id = ? AND chat_id = ? AND status = ?`,
		StatusDraft, timestamp(), id, chatID, StatusSaved)
	if err != nil {
		return fmt.Errorf("deshaciendo el guardado: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil || n == 1 {
		return err
	}
	return ErrNotSaved
}

// DeleteChat borra todo lo del chat: facturas, configuración, exportaciones, recordatorios
// y eventos de uso. Devuelve cuántas facturas (de cualquier estado) se borraron.
func (s *Store) DeleteChat(ctx context.Context, chatID int64) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("iniciando la transacción: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, `DELETE FROM invoices WHERE chat_id = ?`, chatID)
	if err != nil {
		return 0, fmt.Errorf("borrando las facturas: %w", err)
	}
	deleted, _ := res.RowsAffected()
	for _, table := range []string{"chat_settings", "exports", "export_requests", "presentations", "reminders", "events"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE chat_id = ?", chatID); err != nil {
			return 0, fmt.Errorf("borrando %s: %w", table, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("confirmando el borrado: %w", err)
	}
	return int(deleted), nil
}

// IsSaved indica si el chat ya guardó esta misma factura (mismo tipo, RUC, timbrado y número).
func (s *Store) IsSaved(ctx context.Context, chatID int64, inv invoice.Invoice) (bool, error) {
	inv.Number = invoice.NormalizeNumber(inv.Number)
	var exists bool
	err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM invoices WHERE chat_id = ? AND status = ? AND dedup_key = ?)`,
		chatID, StatusSaved, dedupKey(inv)).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("buscando duplicados: %w", err)
	}
	return exists, nil
}

// DeleteSaved saca una factura guardada del chat: deja de aparecer en /resumen y en la exportación.
// No se elimina de la base (sigue contando en las métricas) y la misma factura se puede volver a guardar.
func (s *Store) DeleteSaved(ctx context.Context, chatID, id int64) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE invoices SET status = ?, updated_at = ? WHERE id = ? AND chat_id = ? AND status = ?`,
		StatusDeleted, timestamp(), id, chatID, StatusSaved)
	if err != nil {
		return fmt.Errorf("borrando la factura: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil || n == 1 {
		return err
	}
	var exists bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM invoices WHERE id = ? AND chat_id = ?)`,
		id, chatID).Scan(&exists); err != nil {
		return fmt.Errorf("buscando la factura: %w", err)
	}
	if !exists {
		return ErrNotFound
	}
	return ErrNotSaved
}

// Discard descarta un borrador.
func (s *Store) Discard(ctx context.Context, chatID, id int64) error {
	return s.inDraftTx(ctx, chatID, id, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			UPDATE invoices SET status = ?, awaiting_field = NULL, updated_at = ? WHERE id = ?`,
			StatusDiscarded, timestamp(), id)
		return err
	})
}

// SetAwaiting marca que el chat va a escribir el valor de un campo de ese borrador.
// Cada chat espera como máximo una corrección a la vez.
func (s *Store) SetAwaiting(ctx context.Context, chatID, id int64, field string) error {
	return s.inDraftTx(ctx, chatID, id, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE invoices SET awaiting_field = NULL WHERE chat_id = ?`, chatID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE invoices SET awaiting_field = ? WHERE id = ?`, field, id)
		return err
	})
}

// Awaiting devuelve la corrección pendiente del chat, si hay una.
func (s *Store) Awaiting(ctx context.Context, chatID int64) (Pending, bool, error) {
	var p Pending
	err := s.db.QueryRowContext(ctx, `
		SELECT id, awaiting_field FROM invoices
		WHERE chat_id = ? AND status = ? AND awaiting_field IS NOT NULL LIMIT 1`,
		chatID, StatusDraft).Scan(&p.ID, &p.Field)
	if errors.Is(err, sql.ErrNoRows) {
		return Pending{}, false, nil
	}
	if err != nil {
		return Pending{}, false, fmt.Errorf("buscando correcciones pendientes: %w", err)
	}
	return p, true, nil
}

// ClearAwaiting cancela la corrección pendiente del chat.
func (s *Store) ClearAwaiting(ctx context.Context, chatID int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE invoices SET awaiting_field = NULL WHERE chat_id = ?`, chatID)
	if err != nil {
		return fmt.Errorf("cancelando la corrección: %w", err)
	}
	return nil
}

// MonthSummary suma las facturas guardadas del chat en el período AAAA-MM.
func (s *Store) MonthSummary(ctx context.Context, chatID int64, period string) (Summary, error) {
	invoices, err := s.SavedInvoices(ctx, chatID, period)
	if err != nil {
		return Summary{}, err
	}
	var sum Summary
	for _, inv := range invoices {
		sum = addInvoice(sum, inv)
	}
	return sum, nil
}

func addInvoice(sum Summary, inv invoice.Invoice) Summary {
	return Summary{
		Count:   sum.Count + 1,
		Exempt:  sum.Exempt + inv.Exempt,
		Taxed5:  sum.Taxed5 + inv.Taxed5,
		Taxed10: sum.Taxed10 + inv.Taxed10,
		VAT5:    sum.VAT5 + inv.VAT5,
		VAT10:   sum.VAT10 + inv.VAT10,
		Total:   sum.Total + inv.Total,
	}
}

// inDraftTx ejecuta fn dentro de una transacción, solo si id es un borrador del chat.
func (s *Store) inDraftTx(ctx context.Context, chatID, id int64, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("iniciando la transacción: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var status Status
	err = tx.QueryRowContext(ctx, `SELECT status FROM invoices WHERE id = ? AND chat_id = ?`, id, chatID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("leyendo el estado: %w", err)
	}
	if status != StatusDraft {
		return ErrNotDraft
	}

	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func currentInvoice(ctx context.Context, tx *sql.Tx, id int64) (invoice.Invoice, error) {
	var data string
	if err := tx.QueryRowContext(ctx, `SELECT invoice_json FROM invoices WHERE id = ?`, id).Scan(&data); err != nil {
		return invoice.Invoice{}, fmt.Errorf("leyendo la factura: %w", err)
	}
	var inv invoice.Invoice
	if err := json.Unmarshal([]byte(data), &inv); err != nil {
		return invoice.Invoice{}, fmt.Errorf("leyendo los datos de la factura: %w", err)
	}
	return inv, nil
}

// dedupKey identifica una factura: el mismo emisor no repite tipo, timbrado y número.
func dedupKey(inv invoice.Invoice) string {
	return fmt.Sprintf("%s|%s|%s|%s", inv.Type, inv.IssuerRUC, inv.Timbrado, inv.Number)
}

// periodOf devuelve AAAA-MM de una fecha AAAA-MM-DD.
func periodOf(date string) string {
	const periodLength = len("2006-01")
	if len(date) < periodLength {
		return ""
	}
	return date[:periodLength]
}

func timestamp() string {
	return time.Now().UTC().Format(time.RFC3339)
}
