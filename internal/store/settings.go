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

// ChatSettings es la configuración de un chat.
type ChatSettings struct {
	RUC         string
	Imputations Imputations
	AutoSave    bool // guardar sin preguntar las facturas que cierran
	Reminders   bool // recordar exportar cuando termina el período
	AwaitingRUC bool // configuración guiada: el próximo texto es el RUC
}

// defaultSettings es la configuración de un chat que todavía no configuró nada.
var defaultSettings = ChatSettings{Reminders: true}

// Settings devuelve la configuración del chat (la de por defecto si todavía no configuró nada).
func (s *Store) Settings(ctx context.Context, chatID int64) (ChatSettings, error) {
	var cs ChatSettings
	err := s.db.QueryRowContext(ctx, `
		SELECT ruc, impute_iva, impute_ire, impute_irp, auto_save, reminders, awaiting_ruc
		FROM chat_settings WHERE chat_id = ?`, chatID).
		Scan(&cs.RUC, &cs.Imputations.IVA, &cs.Imputations.IRE, &cs.Imputations.IRP, &cs.AutoSave, &cs.Reminders, &cs.AwaitingRUC)
	if errors.Is(err, sql.ErrNoRows) {
		return defaultSettings, nil
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

// SetAutoSave activa o desactiva el guardado automático.
func (s *Store) SetAutoSave(ctx context.Context, chatID int64, on bool) error {
	return s.setFlag(ctx, chatID, "auto_save", on)
}

// SetReminders activa o desactiva los recordatorios de exportar.
func (s *Store) SetReminders(ctx context.Context, chatID int64, on bool) error {
	return s.setFlag(ctx, chatID, "reminders", on)
}

// SetAwaitingRUC marca que el próximo texto del chat es su RUC (configuración guiada).
func (s *Store) SetAwaitingRUC(ctx context.Context, chatID int64, on bool) error {
	return s.setFlag(ctx, chatID, "awaiting_ruc", on)
}

// setFlag cambia una columna booleana de chat_settings; column viene siempre de este paquete.
func (s *Store) setFlag(ctx context.Context, chatID int64, column string, on bool) error {
	_, err := s.db.ExecContext(ctx, fmt.Sprintf(`
		INSERT INTO chat_settings (chat_id, %[1]s) VALUES (?, ?)
		ON CONFLICT (chat_id) DO UPDATE SET %[1]s = excluded.%[1]s`, column), chatID, on)
	if err != nil {
		return fmt.Errorf("guardando la configuración: %w", err)
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

// SavedInvoices devuelve las facturas guardadas del chat en el período: un mes (AAAA-MM)
// o un año entero (AAAA). Van mes por mes y, dentro de cada mes, en el orden en que se leyeron.
func (s *Store) SavedInvoices(ctx context.Context, chatID int64, period string) ([]invoice.Invoice, error) {
	saved, err := s.SavedRecords(ctx, chatID, period)
	if err != nil {
		return nil, err
	}
	invoices := make([]invoice.Invoice, len(saved))
	for i, rec := range saved {
		invoices[i] = rec.Invoice
	}
	return invoices, nil
}

// SavedRecord es una factura guardada con su ID, para poder borrarla.
type SavedRecord struct {
	ID      int64
	Invoice invoice.Invoice
}

// SavedRecords es como SavedInvoices pero con el ID de cada factura.
func (s *Store) SavedRecords(ctx context.Context, chatID int64, period string) ([]SavedRecord, error) {
	first, last := period, period
	if len(period) == len("2006") {
		first, last = period+"-01", period+"-12"
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, invoice_json FROM invoices
		WHERE chat_id = ? AND status = ? AND period BETWEEN ? AND ? ORDER BY period, id`,
		chatID, StatusSaved, first, last)
	if err != nil {
		return nil, fmt.Errorf("leyendo las facturas: %w", err)
	}
	defer rows.Close()

	var saved []SavedRecord
	for rows.Next() {
		var (
			rec  SavedRecord
			data string
		)
		if err := rows.Scan(&rec.ID, &data); err != nil {
			return nil, fmt.Errorf("leyendo las facturas: %w", err)
		}
		if err := json.Unmarshal([]byte(data), &rec.Invoice); err != nil {
			return nil, fmt.Errorf("leyendo una factura: %w", err)
		}
		saved = append(saved, rec)
	}
	return saved, rows.Err()
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
