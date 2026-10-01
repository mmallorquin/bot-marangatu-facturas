package telegram

import (
	"strings"
	"testing"

	"github.com/go-telegram/bot/models"
)

func TestListWithoutInvoices(t *testing.T) {
	h := newHarness(t)

	h.sendText("/facturas")

	if got := h.telegram.lastSent(t).text; !strings.Contains(got, "No hay facturas guardadas de Septiembre 2026") {
		t.Errorf("respuesta = %q", got)
	}
}

func TestListShowsSavedInvoicesNewestFirstWithDeleteButtons(t *testing.T) {
	// Arrange
	h := newHarness(t)
	h.saveInvoiceOf(1, "2026-09-10", "001-001-0000001")
	h.saveInvoiceOf(2, "2026-09-20", "001-001-0000002")

	// Act
	h.sendText("/facturas")

	// Assert
	list := h.telegram.lastSent(t)
	first, second := strings.Index(list.text, "0000002"), strings.Index(list.text, "0000001")
	if !strings.Contains(list.text, "2 facturas guardadas") || first < 0 || second < first {
		t.Errorf("lista:\n%s", list.text)
	}
	for _, data := range []string{`"l:a:2:2026-09"`, `"l:a:1:2026-09"`} {
		if !strings.Contains(list.markup, data) {
			t.Errorf("falta el botón %s en %s", data, list.markup)
		}
	}
}

func TestDeletingAnInvoiceAsksFirstAndRemovesItFromSummary(t *testing.T) {
	// Arrange
	h := newHarness(t)
	h.saveInvoiceOf(1, "2026-09-10", "001-001-0000001")
	h.saveInvoiceOf(2, "2026-09-20", "001-001-0000002")
	h.sendText("/facturas")

	// Act: tocar 🗑️, cancelar, volver a tocar y confirmar.
	h.pressRaw("l:a:1:2026-09")
	question := h.telegram.lastSent(t).text
	h.pressRaw("l:n:1:2026-09")
	h.sendText("/resumen")
	afterCancel := h.telegram.lastSent(t).text
	h.pressRaw("l:a:1:2026-09")
	h.pressRaw("l:s:1:2026-09")
	edited := h.telegram.byMethod("editMessageText")
	h.sendText("/resumen")
	afterDelete := h.telegram.lastSent(t).text

	// Assert
	if !strings.Contains(question, "¿Borrar esta factura?") || !strings.Contains(question, "001-001-0000001") {
		t.Errorf("confirmación = %q", question)
	}
	if !strings.Contains(afterCancel, "2 facturas") {
		t.Errorf("cancelar no debería borrar: %q", afterCancel)
	}
	last := edited[len(edited)-1].text
	if !strings.Contains(last, "Factura borrada") || !strings.Contains(last, "1 factura guardada") || strings.Contains(last, "0000001") {
		t.Errorf("la lista actualizada debería tener solo la otra factura:\n%s", last)
	}
	if !strings.Contains(afterDelete, "1 factura guardada") {
		t.Errorf("resumen después de borrar = %q", afterDelete)
	}
	if m := h.metrics(t); m.Deleted != 1 || m.Lists != 1 {
		t.Errorf("métricas: borradas %d, /facturas %d", m.Deleted, m.Lists)
	}
}

func TestCannotDeleteAnotherChatsInvoice(t *testing.T) {
	// Arrange
	h := newHarness(t)
	h.saveInvoiceOf(1, "2026-09-10", "001-001-0000001")

	// Act: otro chat aprieta un botón con el ID de esta factura.
	h.send(&models.Update{CallbackQuery: &models.CallbackQuery{
		ID:   "otro",
		Data: "l:s:1:2026-09",
		Message: models.MaybeInaccessibleMessage{
			Type:    models.MaybeInaccessibleMessageTypeMessage,
			Message: &models.Message{ID: 30, Chat: models.Chat{ID: 999, Type: models.ChatTypePrivate}},
		},
	}})
	h.sendText("/resumen")

	// Assert
	if got := h.telegram.lastSent(t).text; !strings.Contains(got, "1 factura guardada") {
		t.Errorf("la factura no debería haberse borrado: %q", got)
	}
}

func TestListCallbackRejectsGarbage(t *testing.T) {
	for _, data := range []string{"l:a:1", "l:x:1:2026-09", "l:a:uno:2026-09", "l:a:1:septiembre"} {
		if _, err := parseListCallback(data); err == nil {
			t.Errorf("debería rechazar %q", data)
		}
	}
	c := listCallback{action: listActionConfirm, id: 9_223_372_036_854_775_807, period: "2026-09"}
	if len(c.encode()) > 64 {
		t.Errorf("el callback supera el límite de Telegram: %q", c.encode())
	}
}
