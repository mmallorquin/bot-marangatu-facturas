package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// JobState es el estado de un archivo recibido. El camino normal es
// en_cola → descargando → leyendo → lista; el resto son finales hasta que el usuario reintenta.
type JobState string

const (
	JobQueued         JobState = "en_cola"
	JobDownloading    JobState = "descargando"
	JobReading        JobState = "leyendo"
	JobDone           JobState = "lista"
	JobFailedDownload JobState = "error_descarga"
	JobFailedRead     JobState = "error_lectura"
	JobUncertain      JobState = "incierta"  // se cortó durante una lectura: no se sabe si el proveedor cobró
	JobCancelled      JobState = "cancelada" // reservado: hoy, borrar los datos elimina los trabajos
)

const (
	JobKindPhoto = "foto"
	JobKindPDF   = "pdf"

	JobOriginMessage = "mensaje" // archivo mandado solo
	JobOriginAlbum   = "album"   // parte de un álbum

	pdfMimeType = "application/pdf"

	// JobRetention es cuánto se conserva la referencia de un archivo ya terminado.
	JobRetention = 30 * 24 * time.Hour
)

// ErrJobGone indica que el trabajo ya no existe o no está en el estado esperado
// (por ejemplo, porque el usuario borró sus datos mientras se leía).
var ErrJobGone = errors.New("el archivo ya no está en la cola")

// retryableStates son los estados desde los que el usuario puede pedir otro intento.
var retryableStates = []JobState{JobFailedDownload, JobFailedRead, JobUncertain}

// Job es un archivo recibido de un chat. Guarda la referencia de Telegram, nunca los bytes.
type Job struct {
	ID           int64
	ChatID       int64
	MessageID    int
	FileUniqueID string
	FileID       string
	Kind         string
	MimeType     string
	Origin       string
	State        JobState
	InvoiceID    int64 // 0 mientras no hay borrador (o si el archivo no era una factura)
}

const jobColumns = `id, chat_id, message_id, file_unique_id, file_id, kind, mime_type, origin, state, invoice_id`

type scanner interface{ Scan(dest ...any) error }

func scanJob(row scanner) (Job, error) {
	var j Job
	err := row.Scan(&j.ID, &j.ChatID, &j.MessageID, &j.FileUniqueID, &j.FileID, &j.Kind, &j.MimeType, &j.Origin, &j.State, &j.InvoiceID)
	return j, err
}

// EnqueueJob anota un archivo recibido. Si la misma entrega ya estaba anotada
// (mismo chat, mensaje y archivo) devuelve el trabajo existente y created=false.
func (s *Store) EnqueueJob(ctx context.Context, j Job) (job Job, created bool, err error) {
	j.Kind = JobKindPhoto
	if j.MimeType == pdfMimeType {
		j.Kind = JobKindPDF
	}
	now := timestamp()
	res, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO jobs (chat_id, message_id, file_unique_id, file_id, kind, mime_type, origin, state, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		j.ChatID, j.MessageID, j.FileUniqueID, j.FileID, j.Kind, j.MimeType, j.Origin, JobQueued, now, now)
	if err != nil {
		return Job{}, false, fmt.Errorf("anotando el archivo: %w", err)
	}
	inserted, _ := res.RowsAffected()
	job, err = scanJob(s.db.QueryRowContext(ctx, `SELECT `+jobColumns+`
		FROM jobs WHERE chat_id = ? AND message_id = ? AND file_unique_id = ?`, j.ChatID, j.MessageID, j.FileUniqueID))
	if err != nil {
		return Job{}, false, fmt.Errorf("leyendo el archivo anotado: %w", err)
	}
	return job, inserted == 1, nil
}

// AdvanceJob mueve un trabajo de un estado a otro. Falla con ErrJobGone si el trabajo no
// está en el estado from: así un trabajo borrado o ya movido no se vuelve a tocar.
func (s *Store) AdvanceJob(ctx context.Context, chatID, id int64, from, to JobState) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE jobs SET state = ?, updated_at = ? WHERE id = ? AND chat_id = ? AND state = ?`,
		to, timestamp(), id, chatID, from)
	if err != nil {
		return fmt.Errorf("actualizando el archivo: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrJobGone
	}
	return nil
}

// finishJob cierra un trabajo en lectura enlazándolo con su borrador, dentro de la misma transacción.
func finishJob(ctx context.Context, tx *sql.Tx, chatID, id, invoiceID int64) error {
	res, err := tx.ExecContext(ctx, `
		UPDATE jobs SET state = ?, invoice_id = ?, updated_at = ? WHERE id = ? AND chat_id = ? AND state = ?`,
		JobDone, invoiceID, timestamp(), id, chatID, JobReading)
	if err != nil {
		return fmt.Errorf("cerrando el archivo: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrJobGone
	}
	return nil
}

// RetryJob vuelve a poner en cola un archivo con error o incierto. Falla con ErrJobGone
// si no es de ese chat o ya no está en un estado reintentable (un doble toque no lo duplica).
func (s *Store) RetryJob(ctx context.Context, chatID, id int64) (Job, error) {
	res, err := s.db.ExecContext(ctx, `
		UPDATE jobs SET state = ?, updated_at = ?
		WHERE id = ? AND chat_id = ? AND state IN (?, ?, ?)`,
		JobQueued, timestamp(), id, chatID, retryableStates[0], retryableStates[1], retryableStates[2])
	if err != nil {
		return Job{}, fmt.Errorf("reencolando el archivo: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Job{}, ErrJobGone
	}
	job, err := scanJob(s.db.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id = ?`, id))
	if err != nil {
		return Job{}, fmt.Errorf("leyendo el archivo: %w", err)
	}
	return job, nil
}

// Jobs devuelve los últimos limit archivos del chat, del más viejo al más nuevo.
func (s *Store) Jobs(ctx context.Context, chatID int64, limit int) ([]Job, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+jobColumns+` FROM (
			SELECT * FROM jobs WHERE chat_id = ? ORDER BY id DESC LIMIT ?
		) ORDER BY id`, chatID, limit)
	if err != nil {
		return nil, fmt.Errorf("leyendo los archivos: %w", err)
	}
	return collectJobs(rows)
}

func collectJobs(rows *sql.Rows) ([]Job, error) {
	defer rows.Close()
	var jobs []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, fmt.Errorf("leyendo un archivo: %w", err)
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

// RecoverJobs se llama al arrancar. Lo que estaba descargando vuelve a la cola; lo que estaba
// leyendo queda incierto (una llamada paga pudo haber salido) y nunca se reintenta solo.
// Devuelve los trabajos a procesar y los que quedaron inciertos en esta recuperación.
func (s *Store) RecoverJobs(ctx context.Context) (requeued, uncertain []Job, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("iniciando la recuperación: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.QueryContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE state = ? ORDER BY id`, JobReading)
	if err != nil {
		return nil, nil, fmt.Errorf("buscando lecturas cortadas: %w", err)
	}
	if uncertain, err = collectJobs(rows); err != nil {
		return nil, nil, err
	}
	now := timestamp()
	if _, err := tx.ExecContext(ctx, `UPDATE jobs SET state = ?, updated_at = ? WHERE state = ?`, JobUncertain, now, JobReading); err != nil {
		return nil, nil, fmt.Errorf("marcando lecturas inciertas: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE jobs SET state = ?, updated_at = ? WHERE state = ?`, JobQueued, now, JobDownloading); err != nil {
		return nil, nil, fmt.Errorf("reencolando descargas: %w", err)
	}
	rows, err = tx.QueryContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE state = ? ORDER BY id`, JobQueued)
	if err != nil {
		return nil, nil, fmt.Errorf("leyendo la cola: %w", err)
	}
	if requeued, err = collectJobs(rows); err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, fmt.Errorf("confirmando la recuperación: %w", err)
	}
	for i := range uncertain {
		uncertain[i].State = JobUncertain
	}
	return requeued, uncertain, nil
}

// PruneJobs olvida las referencias de archivos ya terminados hace más de maxAge.
func (s *Store) PruneJobs(ctx context.Context, maxAge time.Duration) error {
	cutoff := time.Now().UTC().Add(-maxAge).Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx, `DELETE FROM jobs WHERE updated_at < ? AND state NOT IN (?, ?, ?)`,
		cutoff, JobQueued, JobDownloading, JobReading)
	if err != nil {
		return fmt.Errorf("limpiando archivos viejos: %w", err)
	}
	return nil
}
