package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// SetPendingExport conserva una intención del chat, no una autorización para enviar el ZIP.
func (s *Store) SetPendingExport(ctx context.Context, chatID int64, period string) error {
	if len(period) > 40 {
		return errors.New("período pendiente demasiado largo")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO chat_settings (chat_id, pending_export) VALUES (?, ?)
		ON CONFLICT (chat_id) DO UPDATE SET pending_export = excluded.pending_export`, chatID, period)
	return err
}

// CreateExportPreview vincula la solicitud a los datos que vio el usuario.
func (s *Store) CreateExportPreview(ctx context.Context, chatID int64, period, key, revision string) error {
	if revision == "" {
		return errors.New("la previa debe identificar sus datos")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO export_requests (chat_id, period, request_key, seq, revision)
		VALUES (?, ?, ?, 0, ?)`, chatID, period, key, revision)
	return err
}

// Presentation solo registra lo que el usuario afirma haber presentado en Marangatu.
type Presentation struct {
	Revision    string
	Seq         int
	ConfirmedAt string
}

func (s *Store) ExportPresentation(ctx context.Context, chatID int64, period string) (Presentation, error) {
	var p Presentation
	err := s.db.QueryRowContext(ctx, `SELECT revision, seq, confirmed_at FROM presentations WHERE chat_id = ? AND period = ?`, chatID, period).
		Scan(&p.Revision, &p.Seq, &p.ConfirmedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Presentation{}, nil
	}
	return p, err
}

// ExportDeliveredRevision rechaza solicitudes viejas, ajenas, borradas o sin entrega confirmada.
func (s *Store) ExportDeliveredRevision(ctx context.Context, chatID int64, period, key string) (string, error) {
	var revision string
	err := s.db.QueryRowContext(ctx, `SELECT revision FROM export_requests
		WHERE chat_id = ? AND period = ? AND request_key = ? AND cancelled = 0 AND seq > 0
		AND delivered_at != '' AND revision != ''`, chatID, period, key).Scan(&revision)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrExportExpired
	}
	return revision, err
}

// MarkUserPresented exige una entrega real; repetir la misma confirmación es idempotente.
func (s *Store) MarkUserPresented(ctx context.Context, chatID int64, period, key string) error {
	result, err := s.db.ExecContext(ctx, `INSERT INTO presentations (chat_id, period, revision, seq, confirmed_at)
		SELECT chat_id, period, revision, seq, ? FROM export_requests
		WHERE chat_id = ? AND period = ? AND request_key = ? AND cancelled = 0
		AND seq > 0 AND delivered_at != '' AND revision != ''
		ON CONFLICT (chat_id, period) DO UPDATE SET revision = excluded.revision, seq = excluded.seq,
		confirmed_at = CASE WHEN presentations.revision = excluded.revision AND presentations.seq = excluded.seq
		THEN presentations.confirmed_at ELSE excluded.confirmed_at END`, timestamp(), chatID, period, key)
	if err != nil {
		return fmt.Errorf("registrando la presentación manual: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrExportExpired
	}
	return nil
}
