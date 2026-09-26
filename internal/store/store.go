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
)

const (
	dirPermissions = 0o700 // la base tiene datos fiscales: solo el dueño puede leerla
	busyTimeoutMs  = 5000
)

var (
	ErrNotFound  = errors.New("factura no encontrada")
	ErrNotDraft  = errors.New("la factura ya fue guardada o descartada")
	ErrDuplicate = errors.New("esa factura ya está guardada")
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
	return &Store{db: db}, nil
}

// Close cierra la base.
func (s *Store) Close() error {
	return s.db.Close()
}

// CreateDraft guarda una factura recién leída y devuelve su ID.
func (s *Store) CreateDraft(ctx context.Context, chatID int64, d Draft) (int64, error) {
	data, err := json.Marshal(d.Invoice)
	if err != nil {
		return 0, fmt.Errorf("serializando la factura: %w", err)
	}
	now := timestamp()
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO invoices (chat_id, status, invoice_json, original_json, model, cost_usd, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		chatID, StatusDraft, data, data, d.Model, d.CostUSD, now, now)
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

// Save confirma un borrador. Devuelve ErrDuplicate si el chat ya guardó la misma factura.
func (s *Store) Save(ctx context.Context, chatID, id int64) error {
	return s.inDraftTx(ctx, chatID, id, func(tx *sql.Tx) error {
		inv, err := currentInvoice(ctx, tx, id)
		if err != nil {
			return err
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
			UPDATE invoices SET status = ?, dedup_key = ?, period = ?, awaiting_field = NULL, updated_at = ?
			WHERE id = ?`, StatusSaved, key, periodOf(inv.Date), timestamp(), id)
		return err
	})
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
	rows, err := s.db.QueryContext(ctx, `
		SELECT invoice_json FROM invoices WHERE chat_id = ? AND status = ? AND period = ?`,
		chatID, StatusSaved, period)
	if err != nil {
		return Summary{}, fmt.Errorf("leyendo el resumen: %w", err)
	}
	defer rows.Close()

	var sum Summary
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return Summary{}, fmt.Errorf("leyendo el resumen: %w", err)
		}
		var inv invoice.Invoice
		if err := json.Unmarshal([]byte(data), &inv); err != nil {
			return Summary{}, fmt.Errorf("leyendo una factura del resumen: %w", err)
		}
		sum = addInvoice(sum, inv)
	}
	return sum, rows.Err()
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
