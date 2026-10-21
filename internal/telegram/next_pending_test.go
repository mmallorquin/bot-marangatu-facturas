package telegram

import (
	"context"
	"strings"
	"testing"

	"github.com/go-telegram/bot/models"

	"github.com/mmallorquin/bot-marangatu-facturas/internal/store"
)

// sendSecondPhoto manda otra foto, con otro número de factura, en el mensaje 78.
func (h *harness) sendSecondPhoto() {
	h.reader.result.Invoice.Number = "001-001-0005678"
	h.send(&models.Update{Message: &models.Message{ID: 78, Chat: models.Chat{ID: testChatID, Type: models.ChatTypePrivate}, Photo: photoUpdate().Message.Photo}})
}

func nextPendingMessages(h *harness) []outgoing {
	var found []outgoing
	for _, m := range h.telegram.byMethod("sendMessage") {
		if strings.Contains(m.text, "Siguiente pendiente") {
			found = append(found, m)
		}
	}
	return found
}

func TestSavingShowsTheNextPendingInvoiceCitingItsPhoto(t *testing.T) {
	// Arrange: dos pendientes (el chat de prueba no autoguarda).
	h := newHarness(t)
	h.sendPhoto()
	h.sendSecondPhoto()

	// Act: guardar la primera.
	h.press(callback{action: actionSave, id: 1})

	// Assert: aparece la segunda, con sus botones, citando su foto.
	next := nextPendingMessages(h)
	if len(next) != 1 || !strings.Contains(next[0].text, "(queda 1)") || !strings.Contains(next[0].text, "001-001-0005678") ||
		!strings.Contains(next[0].markup, `"g:2:`) || !strings.Contains(next[0].replyTo, `"message_id":78`) {
		t.Fatalf("siguiente pendiente: %+v", next)
	}
	if rec, _ := h.store.Get(context.Background(), testChatID, 1); rec.Status != store.StatusSaved {
		t.Errorf("la primera quedó %s", rec.Status)
	}

	// Al guardar la última no hay nada más que mostrar.
	h.press(callback{action: actionSave, id: 2})
	if got := nextPendingMessages(h); len(got) != 1 {
		t.Errorf("sin pendientes no debería ofrecer otra: %d mensajes", len(got))
	}
}

func TestDiscardingAlsoShowsTheNextPendingInvoice(t *testing.T) {
	h := newHarness(t)
	h.sendPhoto()
	h.sendSecondPhoto()

	h.press(callback{action: actionDiscard, id: 2})

	next := nextPendingMessages(h)
	if len(next) != 1 || !strings.Contains(next[0].markup, `"g:1:`) || !strings.Contains(next[0].replyTo, `"message_id":77`) {
		t.Errorf("siguiente pendiente tras descartar: %+v", next)
	}
}
