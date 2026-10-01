package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

var (
	ErrExportExpired   = errors.New("la confirmación de exportación ya no está disponible")
	ErrExportCancelled = errors.New("la exportación fue cancelada")
)

// CreateExportRequest registers a preview without consuming a file version.
// Deleting a chat also deletes these confirmations, invalidating old buttons.
func (s *Store) CreateExportRequest(ctx context.Context, chatID int64, period, key string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO export_requests (chat_id, period, request_key, seq) VALUES (?, ?, ?, 0)`, chatID, period, key)
	if err != nil {
		return fmt.Errorf("registrando la previa: %w", err)
	}
	return nil
}

// ExportRequestStatus checks a confirmation without reserving a file version.
// Every action, including review downloads and preview refreshes, checks this.
func (s *Store) ExportRequestStatus(ctx context.Context, chatID int64, period, key string) (bool, error) {
	var deliveredAt string
	var cancelled bool
	err := s.db.QueryRowContext(ctx, `SELECT delivered_at, cancelled FROM export_requests WHERE chat_id = ? AND period = ? AND request_key = ?`, chatID, period, key).
		Scan(&deliveredAt, &cancelled)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrExportExpired
	}
	if err != nil {
		return false, fmt.Errorf("leyendo la confirmación: %w", err)
	}
	if cancelled {
		return false, ErrExportCancelled
	}
	return deliveredAt != "", nil
}

// ReserveExport assigns a version once per confirmation. A retry after a failed
// upload reuses that version; reserving does not count as delivering the ZIP.
func (s *Store) ReserveExport(ctx context.Context, chatID int64, period, key string) (seq int, delivered bool, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback()
	var deliveredAt string
	var cancelled bool
	err = tx.QueryRowContext(ctx, `SELECT seq, delivered_at, cancelled FROM export_requests WHERE chat_id = ? AND period = ? AND request_key = ?`, chatID, period, key).
		Scan(&seq, &deliveredAt, &cancelled)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, ErrExportExpired
	}
	if err != nil {
		return 0, false, fmt.Errorf("leyendo la confirmación: %w", err)
	}
	if cancelled {
		return 0, false, ErrExportCancelled
	}
	if seq == 0 {
		err = tx.QueryRowContext(ctx, `INSERT INTO exports (chat_id, period, seq) VALUES (?, ?, 1)
			ON CONFLICT (chat_id, period) DO UPDATE SET seq = seq + 1 RETURNING seq`, chatID, period).Scan(&seq)
		if err == nil {
			_, err = tx.ExecContext(ctx, `UPDATE export_requests SET seq = ? WHERE chat_id = ? AND period = ? AND request_key = ?`, seq, chatID, period, key)
		}
		if err != nil {
			return 0, false, fmt.Errorf("reservando la exportación: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, false, err
	}
	return seq, deliveredAt != "", nil
}

// MarkExportDelivered records only an upload acknowledged by Telegram.
func (s *Store) MarkExportDelivered(ctx context.Context, chatID int64, period, key string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := timestamp()
	result, err := tx.ExecContext(ctx, `UPDATE export_requests SET delivered_at = ?
		WHERE chat_id = ? AND period = ? AND request_key = ? AND cancelled = 0 AND seq > 0`, now, chatID, period, key)
	if err != nil {
		return fmt.Errorf("confirmando la entrega: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrExportExpired
	}
	if _, err := tx.ExecContext(ctx, `UPDATE exports SET delivered_at = ? WHERE chat_id = ? AND period = ?`, now, chatID, period); err != nil {
		return fmt.Errorf("anotando la exportación entregada: %w", err)
	}
	return tx.Commit()
}

func (s *Store) CancelExport(ctx context.Context, chatID int64, period, key string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE export_requests SET cancelled = 1 WHERE chat_id = ? AND period = ? AND request_key = ? AND delivered_at = ''`, chatID, period, key)
	if err != nil {
		return fmt.Errorf("cancelando la exportación: %w", err)
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
