package store

import (
	"context"
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
