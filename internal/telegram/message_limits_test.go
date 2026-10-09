package telegram

import (
	"context"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/mmallorquin/bot-marangatu-facturas/internal/store"
)

// Catches oversized OCR replies losing all review buttons on Telegram rejection.
func TestOversizedInvoiceReplyPreservesReviewButtonsAndFullRecord(t *testing.T) {
	h := newHarness(t)
	name := strings.Repeat("🧾", 3000)
	h.reader.result.Invoice.IssuerName = name
	h.sendPhoto()
	message := h.telegram.lastSent(t)
	if units := len(utf16.Encode([]rune(message.text))); units > 4096 {
		t.Fatalf("mensaje excede Telegram: %d", units)
	}
	if !strings.Contains(message.text, "Emisor: "+strings.Repeat("🧾", 79)+"…") || !strings.Contains(message.markup, `"e:1"`) || !strings.Contains(message.markup, `"g:1"`) {
		t.Fatalf("mensaje no conserva aviso y revisión: %+v", message)
	}
	rec, err := h.store.Get(context.Background(), testChatID, 1)
	if err != nil || rec.Invoice.IssuerName != name {
		t.Fatalf("se cortaron los datos originales: %+v, %v", rec, err)
	}
}

func TestOversizedIssuerPreservesWarningsButtonsAndManualSave(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
	}{
		{name: "5000 letters", text: strings.Repeat("Proveedor ", 500)},
		{name: "3000 emoji", text: strings.Repeat("🧾", 3000)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.reader.result.Invoice.IssuerName = tc.text
			h.reader.result.Invoice.UncertainFields = []string{"total"}
			h.sendPhoto()
			message := h.telegram.lastSent(t)
			if units := len(utf16.Encode([]rune(message.text))); units > 4096 {
				t.Fatalf("mensaje excede Telegram: %d", units)
			}
			if !strings.Contains(message.text, "No se lee bien: total") {
				t.Fatalf("se perdió el aviso del dato dudoso: %q", message.text)
			}
			if !strings.Contains(message.markup, `"e:1"`) || !strings.Contains(message.markup, `"g:1"`) {
				t.Fatalf("faltan botones de revisión: %+v", message)
			}
			if strings.Contains(message.text, AutoSavedNote) {
				t.Fatalf("un dato dudoso no debe guardarse automáticamente: %q", message.text)
			}
			rec, err := h.store.Get(context.Background(), testChatID, 1)
			if err != nil || rec.Invoice.IssuerName != tc.text || len(rec.Invoice.UncertainFields) != 1 || rec.Invoice.UncertainFields[0] != "total" {
				t.Fatalf("se modificó el borrador original: %+v, %v", rec, err)
			}

			h.press(callback{action: actionSave, id: 1})
			rec, err = h.store.Get(context.Background(), testChatID, 1)
			if err != nil || rec.Status != store.StatusSaved || len(rec.Invoice.UncertainFields) != 1 {
				t.Fatalf("el guardado manual intencional no se conservó: %+v, %v", rec, err)
			}
		})
	}
}

func TestManyLongWarningsAndIdentifiersKeepReviewAndTotalsVisible(t *testing.T) {
	h := newHarness(t)
	inv := h.reader.result.Invoice
	inv.Number = strings.Repeat("N", 5000)
	inv.IssuerName = strings.Repeat("🧾", 3000)
	inv.IssuerRUC = strings.Repeat("R", 5000)
	inv.Timbrado = strings.Repeat("T", 5000)
	inv.Date = strings.Repeat("D", 5000)
	inv.Condition = strings.Repeat("C", 5000)
	inv.Currency = strings.Repeat("M", 5000)
	inv.UncertainFields = make([]string, 100)
	for i := range inv.UncertainFields {
		inv.UncertainFields[i] = strings.Repeat("campo🧾", 100)
	}
	h.reader.result.Invoice = inv
	h.sendPhoto()

	message := h.telegram.lastSent(t)
	if units := len(utf16.Encode([]rune(message.text))); units > 4096 {
		t.Fatalf("mensaje excede Telegram: %d", units)
	}
	for _, want := range []string{"Hay más avisos", "Total: 150.000", "Emisor:", "RUC:"} {
		if !strings.Contains(message.text, want) {
			t.Errorf("falta contenido visible %q:\n%s", want, message.text)
		}
	}
	if !strings.Contains(message.markup, `"e:1"`) || !strings.Contains(message.markup, `"g:1"`) {
		t.Fatalf("faltan botones de revisión: %+v", message)
	}
	rec, err := h.store.Get(context.Background(), testChatID, 1)
	if err != nil || rec.Original.Number != inv.Number || rec.Original.IssuerName != inv.IssuerName || rec.Original.IssuerRUC != inv.IssuerRUC || rec.Original.Timbrado != inv.Timbrado || rec.Original.Date != inv.Date || rec.Original.Condition != inv.Condition || rec.Original.Currency != inv.Currency || len(rec.Original.UncertainFields) != len(inv.UncertainFields) || rec.Original.UncertainFields[0] != inv.UncertainFields[0] {
		t.Fatalf("se alteró la lectura original: err=%v, id=%d status=%s", err, rec.ID, rec.Status)
	}
}

// Catches oversized historical details becoming inaccessible while the list is usable.
func TestOversizedHistoryDetailPreservesCorrectionButtonsAndFullRecord(t *testing.T) {
	h := newHarness(t)
	inv := sampleInvoice()
	inv.IssuerName = strings.Repeat("🧾", 3000)
	id, err := h.store.CreateDraft(context.Background(), testChatID, store.Draft{Invoice: inv})
	if err != nil {
		t.Fatal(err)
	}
	if id != 1 {
		t.Fatalf("fixture id = %d", id)
	}
	h.pressRaw("h:o:p:2026-09:0:1")
	message := lastEdited(t, h)
	if units := len(utf16.Encode([]rune(message.text))); units > 4096 {
		t.Fatalf("detalle excede Telegram: %d", units)
	}
	if !strings.Contains(message.text, "Emisor: "+strings.Repeat("🧾", 79)+"…") || !strings.Contains(message.markup, `"e:1"`) || !strings.Contains(message.markup, `"h:l:p:2026-09:0"`) {
		t.Fatalf("detalle sin corrección/retorno: %+v", message)
	}
	rec, err := h.store.Get(context.Background(), testChatID, 1)
	if err != nil || rec.Invoice.IssuerName != inv.IssuerName {
		t.Fatalf("se cortó el registro: %+v, %v", rec, err)
	}
}
