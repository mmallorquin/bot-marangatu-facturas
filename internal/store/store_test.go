package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/mmallorquin/bot-marangatu-facturas/internal/invoice"
)

const (
	chatA int64 = 111
	chatB int64 = 222
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "datos", "facturas.db"))
	if err != nil {
		t.Fatalf("no se pudo abrir la base: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func sampleInvoice(number string, total int64) invoice.Invoice {
	return invoice.Invoice{
		IsInvoice: true, Type: invoice.TypeFactura, IssuerRUC: "80000519-8", IssuerName: "Comercial",
		Timbrado: "12345678", Number: number, Date: "2026-09-20", Condition: invoice.ConditionCash,
		Currency: invoice.CurrencyPYG, Taxed10: total, VAT10: total / 11, Total: total,
	}
}

func newDraft(inv invoice.Invoice) Draft {
	return Draft{Invoice: inv, Model: "google/gemini-3.1-flash-lite", CostUSD: 0.0009}
}

func TestCreateDraftAndGet(t *testing.T) {
	// Arrange
	s := openTestStore(t)
	ctx := context.Background()
	inv := sampleInvoice("001-001-0000001", 110_000)

	// Act
	id, err := s.CreateDraft(ctx, chatA, newDraft(inv))
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	rec, err := s.Get(ctx, chatA, id)

	// Assert
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if rec.Status != StatusDraft || rec.Invoice.Number != inv.Number || rec.Original.Total != inv.Total ||
		rec.Model != "google/gemini-3.1-flash-lite" || rec.Corrected {
		t.Errorf("registro = %+v", rec)
	}
}

func TestGetDoesNotReturnInvoicesFromOtherChats(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.CreateDraft(context.Background(), chatA, newDraft(sampleInvoice("001-001-0000001", 110_000)))

	_, err := s.Get(context.Background(), chatB, id)

	if !errors.Is(err, ErrNotFound) {
		t.Errorf("error = %v, se esperaba ErrNotFound", err)
	}
}

func TestUpdateInvoiceMarksAsCorrectedAndKeepsOriginal(t *testing.T) {
	// Arrange
	s := openTestStore(t)
	ctx := context.Background()
	original := sampleInvoice("001-001-0000001", 110_000)
	id, _ := s.CreateDraft(ctx, chatA, newDraft(original))

	// Act
	if err := s.UpdateInvoice(ctx, chatA, id, sampleInvoice("001-001-0000001", 220_000)); err != nil {
		t.Fatalf("UpdateInvoice: %v", err)
	}

	// Assert
	rec, _ := s.Get(ctx, chatA, id)
	if rec.Invoice.Total != 220_000 || rec.Original.Total != 110_000 || !rec.Corrected {
		t.Errorf("registro = %+v", rec)
	}
}

func TestSaveAndDiscardOnlyWorkOnDrafts(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	saved, _ := s.CreateDraft(ctx, chatA, newDraft(sampleInvoice("001-001-0000001", 110_000)))
	discarded, _ := s.CreateDraft(ctx, chatA, newDraft(sampleInvoice("001-001-0000002", 110_000)))

	if err := s.Save(ctx, chatA, saved); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := s.Discard(ctx, chatA, discarded); err != nil {
		t.Fatalf("Discard: %v", err)
	}

	for _, id := range []int64{saved, discarded} {
		if err := s.Save(ctx, chatA, id); !errors.Is(err, ErrNotDraft) {
			t.Errorf("Save(%d) = %v, se esperaba ErrNotDraft", id, err)
		}
		if err := s.UpdateInvoice(ctx, chatA, id, sampleInvoice("x", 1)); !errors.Is(err, ErrNotDraft) {
			t.Errorf("UpdateInvoice(%d) = %v, se esperaba ErrNotDraft", id, err)
		}
	}
	if rec, _ := s.Get(ctx, chatA, discarded); rec.Status != StatusDiscarded {
		t.Errorf("estado = %q, se esperaba descartada", rec.Status)
	}
}

func TestSaveRejectsDuplicatesInTheSameChatOnly(t *testing.T) {
	// Arrange
	s := openTestStore(t)
	ctx := context.Background()
	inv := sampleInvoice("001-001-0000001", 110_000)
	first, _ := s.CreateDraft(ctx, chatA, newDraft(inv))
	second, _ := s.CreateDraft(ctx, chatA, newDraft(inv))
	otherChat, _ := s.CreateDraft(ctx, chatB, newDraft(inv))

	// Act
	errFirst := s.Save(ctx, chatA, first)
	errSecond := s.Save(ctx, chatA, second)
	errOther := s.Save(ctx, chatB, otherChat)

	// Assert
	if errFirst != nil || errOther != nil {
		t.Fatalf("errores inesperados: %v, %v", errFirst, errOther)
	}
	if !errors.Is(errSecond, ErrDuplicate) {
		t.Errorf("error = %v, se esperaba ErrDuplicate", errSecond)
	}
}

func TestSaveValidatesCurrentInvoiceAfterCallerRead(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	id, err := s.CreateDraft(ctx, chatA, newDraft(sampleInvoice("001-001-0000001", 150_000)))
	if err != nil {
		t.Fatal(err)
	}

	// El usuario confirmó una lectura válida, pero otra corrección terminó
	// antes del guardado. La confirmación anterior no autoriza datos inválidos.
	previous, err := s.Get(ctx, chatA, id)
	if err != nil {
		t.Fatal(err)
	}
	if issues := invoice.Validate(previous.Invoice); len(issues) != 0 {
		t.Fatalf("lectura inicial inválida: %v", issues)
	}
	changed := previous.Invoice
	changed.Total = 1
	if err := s.UpdateInvoice(ctx, chatA, id, changed); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAwaiting(ctx, chatA, id, invoice.FieldTotal); err != nil {
		t.Fatal(err)
	}

	if err := s.Save(ctx, chatA, id); !errors.Is(err, ErrInvalidInvoice) {
		t.Fatalf("Save = %v; debe rechazar un total de 1 cuando las columnas suman 150000", err)
	}
	current, err := s.Get(ctx, chatA, id)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != StatusDraft || current.Invoice.Total != 1 || current.Original.Total != 150_000 {
		t.Fatalf("la validación alteró el borrador o su original: %+v", current)
	}
	if pending, found, err := s.Awaiting(ctx, chatA); err != nil || !found || pending.ID != id {
		t.Fatalf("la validación perdió la corrección pendiente: %+v, %v, %v", pending, found, err)
	}
	if saved, err := s.SavedRecords(ctx, chatA, "2026-09"); err != nil || len(saved) != 0 {
		t.Fatalf("la factura inválida entró a la exportación: %+v, %v", saved, err)
	}

	if err := s.UpdateInvoice(ctx, chatA, id, previous.Invoice); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(ctx, chatA, id); err != nil {
		t.Fatalf("la factura corregida no se pudo guardar: %v", err)
	}
}

func TestAwaitingCorrectionIsOnePerChat(t *testing.T) {
	// Arrange
	s := openTestStore(t)
	ctx := context.Background()
	id1, _ := s.CreateDraft(ctx, chatA, newDraft(sampleInvoice("001-001-0000001", 110_000)))
	id2, _ := s.CreateDraft(ctx, chatA, newDraft(sampleInvoice("001-001-0000002", 110_000)))

	// Act
	_ = s.SetAwaiting(ctx, chatA, id1, invoice.FieldTotal)
	_ = s.SetAwaiting(ctx, chatA, id2, invoice.FieldDate)
	pending, found, err := s.Awaiting(ctx, chatA)

	// Assert
	if err != nil || !found || pending.ID != id2 || pending.Field != invoice.FieldDate {
		t.Errorf("Awaiting = %+v, %v, %v", pending, found, err)
	}
	if _, found, _ := s.Awaiting(ctx, chatB); found {
		t.Error("el chat B no debería tener correcciones pendientes")
	}

	if err := s.ClearAwaiting(ctx, chatA); err != nil {
		t.Fatalf("ClearAwaiting: %v", err)
	}
	if _, found, _ := s.Awaiting(ctx, chatA); found {
		t.Error("no debería quedar nada pendiente")
	}
}

func TestSetAwaitingFailsForUnknownDraft(t *testing.T) {
	s := openTestStore(t)

	err := s.SetAwaiting(context.Background(), chatA, 999, invoice.FieldTotal)

	if !errors.Is(err, ErrNotFound) {
		t.Errorf("error = %v, se esperaba ErrNotFound", err)
	}
}

func TestMonthSummaryAddsOnlySavedInvoicesOfThatMonthAndChat(t *testing.T) {
	// Arrange
	s := openTestStore(t)
	ctx := context.Background()
	save := func(chat int64, inv invoice.Invoice) {
		id, _ := s.CreateDraft(ctx, chat, newDraft(inv))
		if err := s.Save(ctx, chat, id); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}
	save(chatA, sampleInvoice("001-001-0000001", 110_000))
	save(chatA, sampleInvoice("001-001-0000002", 220_000))
	august := sampleInvoice("001-001-0000003", 330_000)
	august.Date = "2026-08-31"
	save(chatA, august)
	save(chatB, sampleInvoice("001-001-0000004", 440_000))
	_, _ = s.CreateDraft(ctx, chatA, newDraft(sampleInvoice("001-001-0000005", 550_000))) // borrador

	// Act
	sum, err := s.MonthSummary(ctx, chatA, "2026-09")

	// Assert
	if err != nil {
		t.Fatalf("MonthSummary: %v", err)
	}
	want := Summary{Count: 2, Taxed10: 330_000, VAT10: 30_000, Total: 330_000}
	if sum != want {
		t.Errorf("resumen = %+v, se esperaba %+v", sum, want)
	}
}

func TestOpenCreatesFolderAndCanBeReopened(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a", "b", "facturas.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	id, _ := s.CreateDraft(context.Background(), chatA, newDraft(sampleInvoice("001-001-0000001", 110_000)))
	_ = s.Close()

	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("reabrir: %v", err)
	}
	defer reopened.Close()
	if _, err := reopened.Get(context.Background(), chatA, id); err != nil {
		t.Errorf("los datos no persistieron: %v", err)
	}
}

func TestDeleteSavedRemovesItFromSummariesAndAllowsSavingAgain(t *testing.T) {
	// Arrange
	s := openTestStore(t)
	ctx := context.Background()
	inv := sampleInvoice("001-001-0000001", 110_000)
	id, _ := s.CreateDraft(ctx, chatA, newDraft(inv))
	if err := s.Save(ctx, chatA, id); err != nil {
		t.Fatal(err)
	}

	// Act
	otherChat := s.DeleteSaved(ctx, chatB, id)
	err := s.DeleteSaved(ctx, chatA, id)
	again := s.DeleteSaved(ctx, chatA, id)

	// Assert
	if !errors.Is(otherChat, ErrNotFound) {
		t.Errorf("otro chat no puede borrarla: %v", otherChat)
	}
	if err != nil {
		t.Fatalf("DeleteSaved: %v", err)
	}
	if !errors.Is(again, ErrNotSaved) {
		t.Errorf("borrar dos veces = %v", again)
	}
	if saved, _ := s.SavedRecords(ctx, chatA, "2026-09"); len(saved) != 0 {
		t.Errorf("sigue en la lista: %+v", saved)
	}
	id2, _ := s.CreateDraft(ctx, chatA, newDraft(inv))
	if err := s.Save(ctx, chatA, id2); err != nil {
		t.Errorf("debería poder volver a guardarla: %v", err)
	}
	if saved, _ := s.SavedRecords(ctx, chatA, "2026"); len(saved) != 1 || saved[0].ID != id2 {
		t.Errorf("SavedRecords = %+v", saved)
	}
}

func TestDeleteSavedRejectsDrafts(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	id, _ := s.CreateDraft(ctx, chatA, newDraft(sampleInvoice("001-001-0000001", 110_000)))

	if err := s.DeleteSaved(ctx, chatA, id); !errors.Is(err, ErrNotSaved) {
		t.Errorf("un borrador no se borra con DeleteSaved: %v", err)
	}
}

func TestExportDeliveryMigrationKeepsLegacyExportsAndNewReservationsDistinct(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	old, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = old.Exec(`CREATE TABLE exports (chat_id INTEGER NOT NULL, period TEXT NOT NULL,
		seq INTEGER NOT NULL, PRIMARY KEY (chat_id, period));
		INSERT INTO exports (chat_id, period, seq) VALUES (111, '2026-08', 2);`)
	_ = old.Close()
	if err != nil {
		t.Fatal(err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	var seq int
	var delivered string
	if err := s.db.QueryRow(`SELECT seq, delivered_at FROM exports WHERE chat_id = 111 AND period = '2026-08'`).Scan(&seq, &delivered); err != nil {
		t.Fatal(err)
	}
	if seq != 2 || delivered == "" {
		t.Fatalf("exportación anterior perdió su estado: seq=%d, delivered=%q", seq, delivered)
	}
	if _, err := s.NextExportSeq(context.Background(), chatA, "2026-09"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT seq, delivered_at FROM exports WHERE chat_id = 111 AND period = '2026-09'`).Scan(&seq, &delivered); err != nil {
		t.Fatal(err)
	}
	if seq != 1 || delivered != "" {
		t.Fatalf("reabrir convirtió una reserva en entrega: seq=%d, delivered=%q", seq, delivered)
	}
}

func TestDeleteChatClearsExportRequestsOnlyForThatChat(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if _, err := s.db.Exec(`INSERT INTO export_requests (chat_id, period, request_key, seq) VALUES
		(111, '2026-09', 'first', 1), (222, '2026-09', 'other', 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteChat(ctx, chatA); err != nil {
		t.Fatal(err)
	}
	var deleted, kept int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM export_requests WHERE chat_id = 111`).Scan(&deleted); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM export_requests WHERE chat_id = 222`).Scan(&kept); err != nil {
		t.Fatal(err)
	}
	if deleted != 0 || kept != 1 {
		t.Fatalf("borrado de solicitudes: chat borrado=%d, otro chat=%d", deleted, kept)
	}
}
