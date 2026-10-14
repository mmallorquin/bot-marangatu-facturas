package telegram

import (
	"context"
	"strings"
	"testing"

	"github.com/go-telegram/bot/models"

	"github.com/mmallorquin/bot-marangatu-facturas/internal/invoice"
	"github.com/mmallorquin/bot-marangatu-facturas/internal/store"
)

func TestAutoSaveSavesCleanInvoicesAndCanBeUndone(t *testing.T) {
	// Arrange
	h := newHarness(t)
	h.sendText("/autoguardar si")

	// Act: llega una factura que cierra.
	h.sendPhoto()
	auto := h.telegram.lastSent(t)

	// Assert: se guardó sola, con el botón de deshacer.
	if !strings.Contains(auto.text, AutoSavedNote) || !strings.Contains(auto.markup, `"u:1"`) {
		t.Fatalf("debería guardarse sola con Deshacer: %+v", auto)
	}
	if rec, _ := h.store.Get(context.Background(), testChatID, 1); rec.Status != store.StatusSaved {
		t.Fatalf("estado = %s", rec.Status)
	}

	// Act: deshacer.
	h.press(callback{action: actionUndo, id: 1})

	// Assert: vuelve a borrador con los botones de siempre, y se puede guardar de nuevo.
	edits := h.telegram.byMethod("editMessageText")
	undone := edits[len(edits)-1]
	if !strings.Contains(undone.text, UndoneNote) || !strings.Contains(undone.markup, `"g:1"`) {
		t.Errorf("después de deshacer: %+v", undone)
	}
	h.press(callback{action: actionSave, id: 1})
	if rec, _ := h.store.Get(context.Background(), testChatID, 1); rec.Status != store.StatusSaved {
		t.Errorf("debería poder guardarse de nuevo: %s", rec.Status)
	}
}

func TestAutoSaveLeavesInvoicesWithProblemsAndDuplicatesForReview(t *testing.T) {
	h := newHarness(t)
	h.sendText("/autoguardar si")
	h.sendPhoto() // se guarda sola

	// Una con el IVA mal y la misma factura otra vez: ninguna se guarda sola.
	wrong := sampleInvoice()
	wrong.Number = "001-001-0000777"
	wrong.VAT10 = 1
	h.reader.result.Invoice = wrong
	h.sendPhoto()
	withProblem := h.telegram.lastSent(t)
	h.reader.result.Invoice = sampleInvoice()
	h.sendPhoto()
	duplicate := h.telegram.lastSent(t)

	for name, msg := range map[string]outgoing{"con problema": withProblem, "duplicada": duplicate} {
		if strings.Contains(msg.text, AutoSavedNote) || !strings.Contains(msg.markup, `"g:`) {
			t.Errorf("%s: debería esperar a que el usuario la revise: %+v", name, msg)
		}
	}
}

func TestAutoSaveCommand(t *testing.T) {
	h := newHarness(t)

	h.sendText("/autoguardar")
	initial := h.telegram.lastSent(t).text
	h.sendText("/autoguardar sí")
	h.sendText("/autoguardar")
	enabled := h.telegram.lastSent(t).text
	h.sendText("/autoguardar quizás")
	invalid := h.telegram.lastSent(t).text
	h.sendText("/autoguardar no")
	h.sendPhoto()

	if !strings.Contains(initial, "desactivado") || !strings.Contains(enabled, "activado: las facturas") {
		t.Errorf("estado: %q / %q", initial, enabled)
	}
	if !strings.Contains(invalid, "/autoguardar si o /autoguardar no") {
		t.Errorf("valor inválido: %q", invalid)
	}
	if strings.Contains(h.telegram.lastSent(t).text, AutoSavedNote) {
		t.Error("desactivado no debería guardar solo")
	}
}

func TestAutoSaveIsOnByDefaultForANewChat(t *testing.T) {
	// El harness lo apaga para el chat de prueba; otro chat recibe el valor por defecto.
	h := newHarness(t)
	const other = int64(4242)

	h.send(&models.Update{Message: &models.Message{ID: 90, Chat: models.Chat{ID: other, Type: models.ChatTypePrivate}, Photo: photoUpdate().Message.Photo}})

	sent := h.telegram.byMethod("sendMessage")
	last := sent[len(sent)-1]
	if !strings.Contains(last.text, AutoSavedNote) || !strings.Contains(last.markup, `"u:1"`) || !strings.Contains(last.markup, `"o:1"`) {
		t.Errorf("una factura que cierra se guarda sola, con Deshacer y Ver foto: %+v", last)
	}
	if !strings.Contains(last.replyTo, `"message_id":90`) {
		t.Errorf("y cita la foto: %q", last.replyTo)
	}
	if rec, _ := h.store.Get(context.Background(), other, 1); rec.Status != store.StatusSaved {
		t.Errorf("estado = %s", rec.Status)
	}
}

func TestEveryProcessMessageQuotesThePhoto(t *testing.T) {
	h := newHarness(t)
	h.sendPhoto()
	h.press(callback{action: actionField, id: 1, field: invoice.FieldTotal})
	h.sendText("abc")
	h.sendText("150.000")

	for _, m := range h.telegram.byMethod("sendMessage") {
		if !strings.Contains(m.replyTo, `"message_id":77`) {
			t.Errorf("mensaje sin cita a la foto: %q", m.text)
		}
	}
}
