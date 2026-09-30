package store

import (
	"context"
	"fmt"
	"testing"
)

func TestSettingsStartEmptyAndArePerChat(t *testing.T) {
	// Arrange
	s := openTestStore(t)
	ctx := context.Background()

	// Act
	empty, err := s.Settings(ctx, chatA)
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	if err := s.SetRUC(ctx, chatA, "80024627-6"); err != nil {
		t.Fatalf("SetRUC: %v", err)
	}
	if err := s.SetImputations(ctx, chatA, Imputations{IVA: true, IRP: true}); err != nil {
		t.Fatalf("SetImputations: %v", err)
	}

	// Assert
	if empty != (ChatSettings{}) {
		t.Errorf("configuración inicial = %+v, se esperaba vacía", empty)
	}
	got, _ := s.Settings(ctx, chatA)
	want := ChatSettings{RUC: "80024627-6", Imputations: Imputations{IVA: true, IRP: true}}
	if got != want {
		t.Errorf("configuración = %+v, se esperaba %+v", got, want)
	}
	if other, _ := s.Settings(ctx, chatB); other != (ChatSettings{}) {
		t.Errorf("el chat B no debería tener configuración: %+v", other)
	}
}

func TestSetRUCKeepsImputationsAndViceVersa(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	_ = s.SetImputations(ctx, chatA, Imputations{IRE: true})
	_ = s.SetRUC(ctx, chatA, "80024627-6")
	_ = s.SetRUC(ctx, chatA, "80000519-8")

	got, _ := s.Settings(ctx, chatA)
	if got.RUC != "80000519-8" || got.Imputations != (Imputations{IRE: true}) {
		t.Errorf("configuración = %+v", got)
	}
}

func TestSavedInvoicesReturnsOnlySavedOfThatMonthInOrder(t *testing.T) {
	// Arrange
	s := openTestStore(t)
	ctx := context.Background()
	for _, number := range []string{"001-001-0000001", "001-001-0000002"} {
		id, _ := s.CreateDraft(ctx, chatA, newDraft(sampleInvoice(number, 110_000)))
		_ = s.Save(ctx, chatA, id)
	}
	_, _ = s.CreateDraft(ctx, chatA, newDraft(sampleInvoice("001-001-0000003", 110_000))) // borrador

	// Act
	invoices, err := s.SavedInvoices(ctx, chatA, "2026-09")

	// Assert
	if err != nil {
		t.Fatalf("SavedInvoices: %v", err)
	}
	if len(invoices) != 2 || invoices[0].Number != "001-001-0000001" || invoices[1].Number != "001-001-0000002" {
		t.Errorf("facturas = %+v", invoices)
	}
	if none, _ := s.SavedInvoices(ctx, chatB, "2026-09"); len(none) != 0 {
		t.Errorf("el chat B no debería tener facturas: %+v", none)
	}
}

func TestNextExportSeqCountsPerChatAndPeriod(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	first, _ := s.NextExportSeq(ctx, chatA, "2026-09")
	second, _ := s.NextExportSeq(ctx, chatA, "2026-09")
	otherMonth, _ := s.NextExportSeq(ctx, chatA, "2026-08")
	otherChat, err := s.NextExportSeq(ctx, chatB, "2026-09")

	if err != nil || first != 1 || second != 2 || otherMonth != 1 || otherChat != 1 {
		t.Errorf("secuencias = %d, %d, %d, %d (err %v)", first, second, otherMonth, otherChat, err)
	}
}

func TestSavedInvoicesForAYearIncludesEveryMonthOfThatYear(t *testing.T) {
	// Arrange
	s := openTestStore(t)
	ctx := context.Background()
	for i, date := range []string{"2026-09-20", "2025-12-31", "2026-01-05", "2027-01-01"} {
		inv := sampleInvoice(fmt.Sprintf("001-001-%07d", i+1), 110_000)
		inv.Date = date
		id, _ := s.CreateDraft(ctx, chatA, newDraft(inv))
		if err := s.Save(ctx, chatA, id); err != nil {
			t.Fatal(err)
		}
	}

	// Act
	year, err := s.SavedInvoices(ctx, chatA, "2026")
	month, _ := s.SavedInvoices(ctx, chatA, "2026-09")

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if len(year) != 2 || year[0].Date != "2026-01-05" || year[1].Date != "2026-09-20" {
		t.Errorf("año 2026 = %+v", year)
	}
	if len(month) != 1 {
		t.Errorf("septiembre = %d facturas", len(month))
	}
}
