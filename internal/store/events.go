package store

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mmallorquin/bot-marangatu-facturas/internal/invoice"
)

// Tipos de evento. Sirven para medir cómo se usa el bot durante la beta.
const (
	EventStart           = "start"
	EventPhoto           = "foto"           // llegó una imagen o PDF que el bot puede leer; detalle: imagen | pdf
	EventPhotoRejected   = "foto_rechazada" // detalle: formato | tamano
	EventRead            = "lectura"        // detalle: ok | avisos
	EventNotInvoice      = "no_factura"     // la IA dijo que la imagen no es un comprobante
	EventReadError       = "lectura_error"  // detalle: descarga | ia
	EventSaved           = "guardada"       // detalle: corregida | sin_corregir
	EventDuplicate       = "duplicada"      // quiso guardar una factura que ya tenía
	EventSaveBlocked     = "guardar_bloqueado"
	EventDiscarded       = "descartada"
	EventUndo            = "deshecha" // tocó Deshacer en una factura guardada automáticamente
	EventAutoSave        = "autoguardar"
	EventReminder        = "recordatorio" // el bot recordó exportar; detalle: mensual | anual
	EventReminderSetting = "recordatorios"
	EventList            = "facturas"            // usó /facturas
	EventPending         = "pendientes"          // usó /pendientes
	EventDeleted         = "borrada"             // sacó una factura ya guardada
	EventCorrection      = "correccion"          // detalle: campo corregido
	EventBadCorrection   = "correccion_invalida" // detalle: campo
	EventCancel          = "cancelar"
	EventSummary         = "resumen"
	EventRUC             = "ruc"
	EventImpute          = "imputar"
	EventExportPreview   = "exportar_previa"
	EventExportReview    = "exportar_revision" // detalle: csv | excel
	EventExportZIP       = "exportar_zip"
	EventUnknownText     = "texto_no_entendido"
	EventDetailCorrected = "corregida"
	EventDetailClean     = "sin_corregir"
	EventDetailIssues    = "avisos"
	EventDetailOK        = "ok"
	EventDetailDownload  = "descarga"
	EventDetailAI        = "ia"
	EventDetailFormat    = "formato"
	EventDetailSize      = "tamano"
	EventDetailCSV       = "csv"
	EventDetailExcel     = "excel"
	EventDetailImage     = "imagen"
	EventDetailPDF       = "pdf"
)

// Un borrador sin guardar ni descartar después de este tiempo se cuenta como abandonado.
const abandonedAfter = 24 * time.Hour

// Event es algo que hizo un usuario. Nunca lleva datos de la factura:
// solo el tipo, un detalle corto de una lista fija, la duración y el costo de la lectura.
type Event struct {
	Kind    string
	Detail  string
	Seconds float64
	CostUSD float64
}

// LogEvent registra un evento del chat.
func (s *Store) LogEvent(ctx context.Context, chatID int64, e Event) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO events (chat_id, kind, detail, seconds, cost_usd, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		chatID, e.Kind, e.Detail, e.Seconds, e.CostUSD, timestamp())
	if err != nil {
		return fmt.Errorf("registrando el evento: %w", err)
	}
	return nil
}

// FieldCount es cuántas veces se corrigió un campo.
type FieldCount struct {
	Field string
	Count int
}

// FieldAccuracy es cuántas facturas guardadas tuvieron que corregir un campo.
type FieldAccuracy struct {
	Field     string
	Corrected int
}

// UserActivity resume lo que hizo un chat en el período.
type UserActivity struct {
	ChatID    int64
	FirstSeen time.Time // primer evento de todos
	LastSeen  time.Time
	Days      int // días distintos con uso en el período
	Photos    int
	Saved     int
	ZIPs      int
	CostUSD   float64
}

// Metrics resume el uso del bot desde una fecha. Son solo conteos: no identifican usuarios.
type Metrics struct {
	// Usuarios
	Users          int // chats con algún evento en el período
	NewUsers       int // su primer evento es del período
	ReturningUsers int // usaron el bot en 2 o más días distintos del período
	AllTimeUsers   int
	UsersSaved     int // guardaron al menos una factura
	UsersExported  int // bajaron al menos un ZIP

	// Embudo de una foto
	Photos     int
	PDFs       int // de Photos, cuántos fueron PDF
	Rejected   int
	ReadOK     int // factura leída sin avisos
	ReadIssues int // factura leída con avisos de validación
	NotInvoice int
	ReadErrors int
	Saved      int
	SavedClean int // guardadas sin ninguna corrección
	Discarded  int
	Deleted    int // guardadas que después se borraron con /facturas
	Duplicates int
	Blocked    int // intentos de guardar con datos que no cierran
	Abandoned  int // borradores del período sin guardar ni descartar después de 24 h

	// Correcciones
	Corrections    []FieldCount // de más a menos corregido
	BadCorrections int

	// Precisión real de la IA: lo que leyó contra lo que el usuario guardó
	SavedCompared int             // facturas guardadas del período que se pudieron comparar
	FieldAccuracy []FieldAccuracy // solo campos corregidos al menos una vez, de más a menos
	MedianToSave  time.Duration   // desde que llegó la foto hasta que se guardó

	// Otros comandos y fricción
	Summaries      int
	Lists          int
	ExportPreviews int
	ExportReviews  int
	ExportZIPs     int
	UnknownText    int

	// Costo y velocidad de las lecturas
	CostUSD    float64
	AvgSeconds float64
	P90Seconds float64

	PerUser []UserActivity // del más reciente al más antiguo
}

// MetricsQuery elige qué eventos entran en las métricas.
type MetricsQuery struct {
	Since   time.Time
	Now     time.Time // define qué borradores ya están abandonados
	Exclude []int64   // chats que no cuentan, por ejemplo los de tus propias pruebas
}

// Metrics calcula las métricas del período.
func (s *Store) Metrics(ctx context.Context, q MetricsQuery) (Metrics, error) {
	from := q.Since.UTC().Format(time.RFC3339)
	scope := newScope(q.Exclude)
	var m Metrics

	if err := s.countUsers(ctx, scope, from, &m); err != nil {
		return Metrics{}, err
	}
	if err := s.countEvents(ctx, scope, from, &m); err != nil {
		return Metrics{}, err
	}
	if err := s.readTimes(ctx, scope, from, &m); err != nil {
		return Metrics{}, err
	}

	if err := s.compareSaved(ctx, scope, from, &m); err != nil {
		return Metrics{}, err
	}
	if err := s.perUser(ctx, scope, from, &m); err != nil {
		return Metrics{}, err
	}

	abandonedBefore := q.Now.Add(-abandonedAfter).UTC().Format(time.RFC3339)
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM invoices WHERE status = ? AND created_at >= ? AND created_at < ?`+scope.and,
		scope.args(StatusDraft, from, abandonedBefore)...).Scan(&m.Abandoned)
	if err != nil {
		return Metrics{}, fmt.Errorf("contando borradores abandonados: %w", err)
	}
	return m, nil
}

// scope deja afuera los chats excluidos. Las consultas de eventos leen de "e" en vez de "events".
type scope struct {
	with    string // WITH e AS (...)
	and     string // AND chat_id NOT IN (...), para otras tablas
	exclude []any
}

func newScope(exclude []int64) scope {
	if len(exclude) == 0 {
		return scope{with: "WITH e AS (SELECT * FROM events) "}
	}
	marks := strings.TrimSuffix(strings.Repeat("?, ", len(exclude)), ", ")
	ids := make([]any, len(exclude))
	for i, id := range exclude {
		ids[i] = id
	}
	return scope{
		with:    "WITH e AS (SELECT * FROM events WHERE chat_id NOT IN (" + marks + ")) ",
		and:     " AND chat_id NOT IN (" + marks + ")",
		exclude: ids,
	}
}

// withArgs arma los argumentos de una consulta que empieza con sc.with.
func (sc scope) withArgs(args ...any) []any { return append(slices.Clone(sc.exclude), args...) }

// args arma los argumentos de una consulta que termina con sc.and.
func (sc scope) args(args ...any) []any { return append(args, sc.exclude...) }

func (s *Store) countUsers(ctx context.Context, sc scope, from string, m *Metrics) error {
	err := s.db.QueryRowContext(ctx, sc.with+`
		SELECT
			(SELECT COUNT(DISTINCT chat_id) FROM e WHERE created_at >= ?),
			(SELECT COUNT(*) FROM (SELECT MIN(created_at) AS first FROM e GROUP BY chat_id) WHERE first >= ?),
			(SELECT COUNT(*) FROM (
				SELECT chat_id FROM e WHERE created_at >= ?
				GROUP BY chat_id HAVING COUNT(DISTINCT substr(created_at, 1, 10)) >= 2)),
			(SELECT COUNT(DISTINCT chat_id) FROM e),
			(SELECT COUNT(DISTINCT chat_id) FROM e WHERE created_at >= ? AND kind = ?),
			(SELECT COUNT(DISTINCT chat_id) FROM e WHERE created_at >= ? AND kind = ?)`,
		sc.withArgs(from, from, from, from, EventSaved, from, EventExportZIP)...).
		Scan(&m.Users, &m.NewUsers, &m.ReturningUsers, &m.AllTimeUsers, &m.UsersSaved, &m.UsersExported)
	if err != nil {
		return fmt.Errorf("contando usuarios: %w", err)
	}
	return nil
}

func (s *Store) countEvents(ctx context.Context, sc scope, from string, m *Metrics) error {
	rows, err := s.db.QueryContext(ctx, sc.with+`
		SELECT kind, detail, COUNT(*), SUM(cost_usd) FROM e WHERE created_at >= ? GROUP BY kind, detail`,
		sc.withArgs(from)...)
	if err != nil {
		return fmt.Errorf("contando eventos: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			kind, detail string
			count        int
			cost         float64
		)
		if err := rows.Scan(&kind, &detail, &count, &cost); err != nil {
			return fmt.Errorf("contando eventos: %w", err)
		}
		m.CostUSD += cost
		addEvent(m, kind, detail, count)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("contando eventos: %w", err)
	}
	slices.SortFunc(m.Corrections, func(a, b FieldCount) int {
		if a.Count != b.Count {
			return b.Count - a.Count
		}
		return strings.Compare(a.Field, b.Field)
	})
	return nil
}

func addEvent(m *Metrics, kind, detail string, count int) {
	switch kind {
	case EventPhoto:
		m.Photos += count
		if detail == EventDetailPDF {
			m.PDFs += count
		}
	case EventPhotoRejected:
		m.Rejected += count
	case EventRead:
		if detail == EventDetailIssues {
			m.ReadIssues += count
		} else {
			m.ReadOK += count
		}
	case EventNotInvoice:
		m.NotInvoice += count
	case EventReadError:
		m.ReadErrors += count
	case EventSaved:
		m.Saved += count
		if detail == EventDetailClean {
			m.SavedClean += count
		}
	case EventDiscarded:
		m.Discarded += count
	case EventDeleted:
		m.Deleted += count
	case EventList:
		m.Lists += count
	case EventDuplicate:
		m.Duplicates += count
	case EventSaveBlocked:
		m.Blocked += count
	case EventCorrection:
		m.Corrections = append(m.Corrections, FieldCount{Field: detail, Count: count})
	case EventBadCorrection:
		m.BadCorrections += count
	case EventSummary:
		m.Summaries += count
	case EventExportPreview:
		m.ExportPreviews += count
	case EventExportReview:
		m.ExportReviews += count
	case EventExportZIP:
		m.ExportZIPs += count
	case EventUnknownText:
		m.UnknownText += count
	}
}

// readTimes calcula el promedio y el percentil 90 de lo que tarda una lectura.
func (s *Store) readTimes(ctx context.Context, sc scope, from string, m *Metrics) error {
	rows, err := s.db.QueryContext(ctx, sc.with+`
		SELECT seconds FROM e WHERE created_at >= ? AND kind IN (?, ?) ORDER BY seconds`,
		sc.withArgs(from, EventRead, EventNotInvoice)...)
	if err != nil {
		return fmt.Errorf("leyendo tiempos: %w", err)
	}
	defer rows.Close()

	var times []float64
	for rows.Next() {
		var seconds float64
		if err := rows.Scan(&seconds); err != nil {
			return fmt.Errorf("leyendo tiempos: %w", err)
		}
		times = append(times, seconds)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("leyendo tiempos: %w", err)
	}
	m.AvgSeconds, m.P90Seconds = averageAndP90(times)
	return nil
}

// averageAndP90 recibe los tiempos ordenados de menor a mayor.
func averageAndP90(sorted []float64) (float64, float64) {
	if len(sorted) == 0 {
		return 0, 0
	}
	var sum float64
	for _, t := range sorted {
		sum += t
	}
	p90 := sorted[(len(sorted)*9+9)/10-1] // rango más cercano: ceil(0,9 × n)
	return sum / float64(len(sorted)), p90
}

// compareSaved compara, campo por campo, lo que leyó la IA con lo que el usuario guardó.
func (s *Store) compareSaved(ctx context.Context, sc scope, from string, m *Metrics) error {
	rows, err := s.db.QueryContext(ctx, `
		SELECT original_json, invoice_json, created_at, updated_at FROM invoices
		WHERE status = ? AND created_at >= ?`+sc.and, sc.args(StatusSaved, from)...)
	if err != nil {
		return fmt.Errorf("comparando facturas guardadas: %w", err)
	}
	defer rows.Close()

	corrected := map[string]int{}
	var toSave []time.Duration
	for rows.Next() {
		var original, final, created, updated string
		if err := rows.Scan(&original, &final, &created, &updated); err != nil {
			return fmt.Errorf("comparando facturas guardadas: %w", err)
		}
		changed, err := changedFields(original, final)
		if err != nil {
			continue // una fila ilegible no debe impedir el reporte
		}
		m.SavedCompared++
		for _, field := range changed {
			corrected[field]++
		}
		if d, ok := elapsed(created, updated); ok {
			toSave = append(toSave, d)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("comparando facturas guardadas: %w", err)
	}

	for _, field := range invoice.EditableFields {
		if n := corrected[field]; n > 0 {
			m.FieldAccuracy = append(m.FieldAccuracy, FieldAccuracy{Field: field, Corrected: n})
		}
	}
	slices.SortStableFunc(m.FieldAccuracy, func(a, b FieldAccuracy) int { return b.Corrected - a.Corrected })
	m.MedianToSave = median(toSave)
	return nil
}

// changedFields devuelve los campos corregibles que difieren entre la lectura y la versión guardada.
func changedFields(originalJSON, finalJSON string) ([]string, error) {
	var original, final invoice.Invoice
	if err := json.Unmarshal([]byte(originalJSON), &original); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(finalJSON), &final); err != nil {
		return nil, err
	}
	// El número se normaliza al leer: eso no es una corrección del usuario.
	original.Number = invoice.NormalizeNumber(original.Number)
	final.Number = invoice.NormalizeNumber(final.Number)

	before, err := fieldValues(original)
	if err != nil {
		return nil, err
	}
	after, err := fieldValues(final)
	if err != nil {
		return nil, err
	}
	var changed []string
	for _, field := range invoice.EditableFields {
		if before[field] != after[field] {
			changed = append(changed, field)
		}
	}
	return changed, nil
}

// fieldValues indexa los campos de la factura por su nombre JSON.
func fieldValues(inv invoice.Invoice) (map[string]string, error) {
	data, err := json.Marshal(inv)
	if err != nil {
		return nil, err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	values := make(map[string]string, len(raw))
	for k, v := range raw {
		values[k] = string(v)
	}
	return values, nil
}

func elapsed(from, to string) (time.Duration, bool) {
	start, err1 := time.Parse(time.RFC3339, from)
	end, err2 := time.Parse(time.RFC3339, to)
	if err1 != nil || err2 != nil || end.Before(start) {
		return 0, false
	}
	return end.Sub(start), true
}

func median(values []time.Duration) time.Duration {
	if len(values) == 0 {
		return 0
	}
	sorted := slices.Clone(values)
	slices.Sort(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return (sorted[mid-1] + sorted[mid]) / 2
}

// perUser arma una fila por chat activo en el período.
func (s *Store) perUser(ctx context.Context, sc scope, from string, m *Metrics) error {
	rows, err := s.db.QueryContext(ctx, sc.with+`
		SELECT
			p.chat_id,
			(SELECT MIN(created_at) FROM e AS a WHERE a.chat_id = p.chat_id),
			MAX(p.created_at),
			COUNT(DISTINCT substr(p.created_at, 1, 10)),
			SUM(p.kind = ?),
			SUM(p.kind = ?),
			SUM(p.kind = ?),
			SUM(p.cost_usd)
		FROM e AS p WHERE p.created_at >= ?
		GROUP BY p.chat_id ORDER BY MAX(p.created_at) DESC`,
		sc.withArgs(EventPhoto, EventSaved, EventExportZIP, from)...)
	if err != nil {
		return fmt.Errorf("armando el detalle por usuario: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			u           UserActivity
			first, last string
		)
		if err := rows.Scan(&u.ChatID, &first, &last, &u.Days, &u.Photos, &u.Saved, &u.ZIPs, &u.CostUSD); err != nil {
			return fmt.Errorf("armando el detalle por usuario: %w", err)
		}
		u.FirstSeen, _ = time.Parse(time.RFC3339, first)
		u.LastSeen, _ = time.Parse(time.RFC3339, last)
		m.PerUser = append(m.PerUser, u)
	}
	return rows.Err()
}
