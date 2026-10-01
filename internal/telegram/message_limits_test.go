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
	if !strings.Contains(message.text, "Mensaje abreviado") || !strings.Contains(message.markup, `"e:1"`) || !strings.Contains(message.markup, `"g:1"`) {
		t.Fatalf("mensaje no conserva aviso y revisión: %+v", message)
	}
	rec, err := h.store.Get(context.Background(), testChatID, 1)
	if err != nil || rec.Invoice.IssuerName != name {
		t.Fatalf("se cortaron los datos originales: %+v, %v", rec, err)
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
	if !strings.Contains(message.text, "Mensaje abreviado") || !strings.Contains(message.markup, `"e:1"`) || !strings.Contains(message.markup, `"h:l:p:2026-09:0"`) {
		t.Fatalf("detalle sin corrección/retorno: %+v", message)
	}
	rec, err := h.store.Get(context.Background(), testChatID, 1)
	if err != nil || rec.Invoice.IssuerName != inv.IssuerName {
		t.Fatalf("se cortó el registro: %+v, %v", rec, err)
	}
}
