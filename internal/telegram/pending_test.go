package telegram

import (
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"
)

func (h *harness) sendAlbumPhoto(groupID string) {
	update := photoUpdate()
	update.Message.MediaGroupID = groupID
	h.send(update)
}

func TestAlbumSaysReadingOnceAndReadsEveryPhoto(t *testing.T) {
	h := newHarness(t)

	for range 3 {
		h.sendAlbumPhoto("album-1")
	}

	reading := 0
	for _, m := range h.telegram.byMethod("sendMessage") {
		if strings.HasPrefix(m.text, "⏳") {
			reading++
		}
	}
	if reading != 1 || len(h.reader.received) != 3 {
		t.Errorf("avisos de lectura = %d (se esperaba 1), fotos leídas = %d", reading, len(h.reader.received))
	}
}

func TestAlbumsAreForgottenAfterAWhile(t *testing.T) {
	a := &albums{}
	now := time.Now()
	if !a.first("x", now) || a.first("x", now) {
		t.Fatal("la primera foto del álbum avisa y las siguientes no")
	}
	if !a.first("x", now.Add(albumMemory+time.Second)) {
		t.Error("pasado un rato, el mismo id se trata como un álbum nuevo")
	}
}

func TestPendingSavesOnlyTheInvoicesThatAreReady(t *testing.T) {
	// Arrange: tres facturas leídas. Una tiene el IVA mal y otra ya estaba guardada.
	h := newHarness(t)
	h.saveInvoiceOf(1, "2026-09-01", "001-001-0000001")
	for i, number := range []string{"001-001-0000002", "001-001-0000003", "001-001-0000001"} {
		inv := sampleInvoice()
		inv.Number = number
		if i == 1 {
			inv.VAT10 = 1
		}
		h.reader.result.Invoice = inv
		h.sendAlbumPhoto("album")
	}

	// Act
	h.sendText("/pendientes")
	summary := h.telegram.lastSent(t)
	h.send(&models.Update{CallbackQuery: &models.CallbackQuery{
		ID: "cb", Data: pendingSaveCallback,
		Message: models.MaybeInaccessibleMessage{
			Type: models.MaybeInaccessibleMessageTypeMessage, Message: &models.Message{ID: 40, Chat: models.Chat{ID: testChatID}},
		},
	}})
	edits := h.telegram.byMethod("editMessageText")
	result := edits[len(edits)-1].text
	h.sendText("/pendientes")
	after := h.telegram.lastSent(t).text

	// Assert
	if !strings.Contains(summary.text, "3 facturas sin guardar: 2 cierran y 1 tiene algo para revisar") ||
		!strings.Contains(summary.markup, pendingSaveCallback) {
		t.Errorf("resumen de pendientes:\n%s\n%s", summary.text, summary.markup)
	}
	if !strings.Contains(result, "Guardé 1 factura") || !strings.Contains(result, "1 ya estaba guardada") ||
		!strings.Contains(result, "1 tiene algo para revisar") {
		t.Errorf("resultado:\n%s", result)
	}
	if !strings.Contains(after, "2 facturas sin guardar: 1 cierra") {
		t.Errorf("después quedan la duplicada y la que hay que revisar:\n%s", after)
	}
}

func TestPendingWithNothingToSave(t *testing.T) {
	h := newHarness(t)

	h.sendText("/pendientes")

	if got := h.telegram.lastSent(t).text; !strings.Contains(got, "No tenés facturas pendientes") {
		t.Errorf("respuesta = %q", got)
	}
}
