package store

import (
	"context"
	"errors"
	"testing"
)

func TestDraftNormalizesNumberButPreservesOriginal(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	id, err := s.CreateDraft(ctx, chatA, newDraft(sampleInvoice("001-005-00009821", 110_000)))
	if err != nil {
		t.Fatal(err)
	}
	rec, err := s.Get(ctx, chatA, id)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Invoice.Number != "001-005-0009821" || rec.Original.Number != "001-005-00009821" {
		t.Fatalf("número actual/original = %q / %q", rec.Invoice.Number, rec.Original.Number)
	}
	if rec.Corrected {
		t.Fatal("normalizar no es una corrección manual")
	}
}

func TestSaveCanonicalizesLegacyDraftAndPreventsDuplicates(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	id, err := s.CreateDraft(ctx, chatA, newDraft(sampleInvoice("001-005-00009821", 110_000)))
	if err != nil {
		t.Fatal(err)
	}
	// Simula un borrador creado por la versión anterior, sin normalización.
	if _, err := s.db.Exec(`UPDATE invoices SET invoice_json = original_json WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	rec, err := s.Get(ctx, chatA, id)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Invoice.Number != "001-005-0009821" {
		t.Fatalf("lectura del borrador antiguo = %q", rec.Invoice.Number)
	}
	if err := s.Save(ctx, chatA, id); err != nil {
		t.Fatal(err)
	}
	invoices, err := s.SavedInvoices(ctx, chatA, "2026-09")
	if err != nil {
		t.Fatal(err)
	}
	if len(invoices) != 1 || invoices[0].Number != "001-005-0009821" {
		t.Fatalf("facturas guardadas = %+v", invoices)
	}
	duplicate, err := s.CreateDraft(ctx, chatA, newDraft(sampleInvoice("001-005-0009821", 110_000)))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Save(ctx, chatA, duplicate); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicado = %v", err)
	}
	rec, err = s.Get(ctx, chatA, id)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Original.Number != "001-005-00009821" {
		t.Fatal("se perdió la lectura original")
	}
}
