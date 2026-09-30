package telegram

import (
	"context"
	"strings"
	"testing"

	"github.com/mmallorquin/bot-marangatu-facturas/internal/store"
)

func TestPhotoWithExtraLeadingZeroCanBeReviewedAndSaved(t *testing.T) {
	h := newHarness(t)
	h.reader.result.Invoice.Number = "001-005-00009821"
	id := h.firstDraftID(t)
	if sent := h.telegram.lastSent(t); !strings.Contains(sent.text, "001-005-0009821") || strings.Contains(sent.text, "el número debe") {
		t.Fatalf("previa = %s", sent.text)
	}
	h.press(callback{action: actionSave, id: id})
	rec, err := h.store.Get(context.Background(), testChatID, id)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != store.StatusSaved || rec.Invoice.Number != "001-005-0009821" || rec.Original.Number != "001-005-00009821" {
		t.Fatalf("estado/número/original = %s / %q / %q", rec.Status, rec.Invoice.Number, rec.Original.Number)
	}
}

func TestPhotoWithEightSignificantDigitsCannotBeSaved(t *testing.T) {
	h := newHarness(t)
	h.reader.result.Invoice.Number = "001-005-12345678"
	id := h.firstDraftID(t)
	h.press(callback{action: actionSave, id: id})
	rec, err := h.store.Get(context.Background(), testChatID, id)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != store.StatusDraft || rec.Invoice.Number != "001-005-12345678" {
		t.Fatalf("se guardó o recortó un número significativo: %+v", rec)
	}
}
