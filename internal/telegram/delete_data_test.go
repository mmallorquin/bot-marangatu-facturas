package telegram

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"

	"github.com/mmallorquin/bot-marangatu-facturas/internal/store"
)

func TestDeleteMyDataAsksFirstAndOnlyDeletesThisChat(t *testing.T) {
	// Arrange: este chat tiene facturas y configuración; otro chat también tiene una factura.
	h := newHarness(t)
	h.saveOneInvoice(t)
	h.sendPhoto() // un borrador
	h.sendText("/ruc 80024627-6")
	h.send(&models.Update{Message: &models.Message{Chat: models.Chat{ID: 999}, Photo: photoUpdate().Message.Photo}})

	// Act: pedir, cancelar, pedir y confirmar.
	h.sendText("/borrar_mis_datos")
	question := h.telegram.lastSent(t)
	h.pressRaw(deleteDataCancel)
	if cs, _ := h.store.Settings(context.Background(), testChatID); cs.RUC == "" {
		t.Fatal("cancelar no debería borrar nada")
	}
	h.pressRaw(deleteDataConfirm)
	edits := h.telegram.byMethod("editMessageText")
	done := edits[len(edits)-1].text

	// Assert
	if !strings.Contains(question.text, "No se puede deshacer") || !strings.Contains(question.markup, deleteDataConfirm) {
		t.Errorf("confirmación: %+v", question)
	}
	if !strings.Contains(done, "borré 2 facturas") {
		t.Errorf("resultado: %q", done)
	}
	ctx := context.Background()
	if cs, _ := h.store.Settings(ctx, testChatID); cs.RUC != "" {
		t.Errorf("la configuración debería haberse borrado: %+v", cs)
	}
	if saved, _ := h.store.SavedInvoices(ctx, testChatID, "2026"); len(saved) != 0 {
		t.Errorf("quedaron facturas: %d", len(saved))
	}
	now := time.Now()
	m, _ := h.store.Metrics(ctx, store.MetricsQuery{Since: now.Add(-time.Hour), Now: now})
	if m.Users != 1 || len(m.PerUser) != 1 || m.PerUser[0].ChatID != 999 {
		t.Errorf("solo deberían quedar los eventos del otro chat: %+v", m.PerUser)
	}
	if drafts, _ := h.store.Drafts(ctx, 999); len(drafts) != 1 {
		t.Errorf("el otro chat no se toca: %d borradores", len(drafts))
	}
}
