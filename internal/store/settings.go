package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/mmallorquin/bot-marangatu-facturas/internal/invoice"
)

// Imputations son los impuestos a los que se imputan las compras.
type Imputations struct {
	IVA bool
	IRE bool
	IRP bool // IRP-RSP
}

// ChatSettings es la configuración fiscal de un chat.
type ChatSettings struct {
	RUC         string
	Imputations Imputations
}

// Settings devuelve la configuración del chat (vacía si todavía no configuró nada).
func (s *Store) Settings(ctx context.Context, chatID int64) (ChatSettings, error) {
	var cs ChatSettings
	err := s.db.QueryRowContext(ctx, `
		SELECT ruc, impute_iva, impute_ire, impute_irp FROM chat_settings WHERE chat_id = ?`, chatID).
		Scan(&cs.RUC, &cs.Imputations.IVA, &cs.Imputations.IRE, &cs.Imputations.IRP)
	if errors.Is(err, sql.ErrNoRows) {
		return ChatSettings{}, nil
	}
	if err != nil {
		return ChatSettings{}, fmt.Errorf("leyendo la configuración: %w", err)
	}
	return cs, nil
}

// SetRUC guarda el RUC del contribuyente sin tocar las imputaciones.
func (s *Store) SetRUC(ctx context.Context, chatID int64, ruc string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO chat_settings (chat_id, ruc) VALUES (?, ?)
		ON CONFLICT (chat_id) DO UPDATE SET ruc = excluded.ruc`, chatID, ruc)
	if err != nil {
		return fmt.Errorf("guardando el RUC: %w", err)
	}
	return nil
}

// SetImputations guarda los impuestos a imputar sin tocar el RUC.
func (s *Store) SetImputations(ctx context.Context, chatID int64, imp Imputations) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO chat_settings (chat_id, impute_iva, impute_ire, impute_irp) VALUES (?, ?, ?, ?)
		ON CONFLICT (chat_id) DO UPDATE SET
			impute_iva = excluded.impute_iva, impute_ire = excluded.impute_ire, impute_irp = excluded.impute_irp`,
		chatID, imp.IVA, imp.IRE, imp.IRP)
	if err != nil {
		return fmt.Errorf("guardando las imputaciones: %w", err)
	}
	return nil
}

// SavedInvoices devuelve las facturas guardadas del chat en el período AAAA-MM, en el orden en que se leyeron.
func (s *Store) SavedInvoices(ctx context.Context, chatID int64, period string) ([]invoice.Invoice, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT invoice_json FROM invoices WHERE chat_id = ? AND status = ? AND period = ? ORDER BY id`,
		chatID, StatusSaved, period)
	if err != nil {
		return nil, fmt.Errorf("leyendo las facturas: %w", err)
	}
	defer rows.Close()

	var invoices []invoice.Invoice
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, fmt.Errorf("leyendo las facturas: %w", err)
		}
		var inv invoice.Invoice
		if err := json.Unmarshal([]byte(data), &inv); err != nil {
			return nil, fmt.Errorf("leyendo una factura: %w", err)
		}
		invoices = append(invoices, inv)
	}
	return invoices, rows.Err()
}

// NextExportSeq devuelve el siguiente número de archivo del período (1, 2, 3...).
// Marangatu pide que cada archivo que se sube tenga un identificador distinto.
func (s *Store) NextExportSeq(ctx context.Context, chatID int64, period string) (int, error) {
	var seq int
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO exports (chat_id, period, seq) VALUES (?, ?, 1)
		ON CONFLICT (chat_id, period) DO UPDATE SET seq = seq + 1
		RETURNING seq`, chatID, period).Scan(&seq)
	if err != nil {
		return 0, fmt.Errorf("numerando la exportación: %w", err)
	}
	return seq, nil
}
