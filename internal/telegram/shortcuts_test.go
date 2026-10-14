package telegram

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mmallorquin/bot-marangatu-facturas/internal/invoice"
	"github.com/mmallorquin/bot-marangatu-facturas/internal/store"
)

func TestReadResultQuotesThePhotoMessage(t *testing.T) {
	h := newHarness(t)

	h.sendPhoto()

	if got := h.telegram.lastSent(t); !strings.Contains(got.replyTo, `"message_id":77`) {
		t.Errorf("la lectura debería citar el mensaje de la foto: %+v", got)
	}
}

func TestWrongRUCCheckDigitOffersOneTapFix(t *testing.T) {
	// Arrange: la IA leyó mal el dígito verificador.
	h := newHarness(t)
	inv := sampleInvoice()
	inv.IssuerRUC = "80000519-1"
	h.reader.result.Invoice = inv

	// Act
	h.sendPhoto()
	read := h.telegram.lastSent(t)
	h.press(callback{action: actionSave, id: 1})
	h.press(callback{action: actionUseRUC, id: 1})
	edits := h.telegram.byMethod("editMessageText")
	fixed := edits[len(edits)-1]
	h.press(callback{action: actionSave, id: 1})

	// Assert
	if !strings.Contains(read.text, "debería ser 8") || !strings.Contains(read.markup, "Usar RUC 80000519-8") || !strings.Contains(read.markup, `"r:1"`) {
		t.Errorf("debería avisar y ofrecer el RUC corregido: %+v", read)
	}
	if !strings.Contains(fixed.text, "RUC: 80000519-8") || !strings.Contains(fixed.text, "coherentes") || strings.Contains(fixed.markup, `"r:1"`) {
		t.Errorf("después del atajo la factura cierra y el botón desaparece: %+v", fixed)
	}
	rec, _ := h.store.Get(context.Background(), testChatID, 1)
	if rec.Status != store.StatusSaved || rec.Invoice.IssuerRUC != "80000519-8" || !rec.Corrected || rec.Original.IssuerRUC != "80000519-1" {
		t.Errorf("registro = %+v", rec)
	}
	now := time.Now()
	m, _ := h.store.Metrics(context.Background(), store.MetricsQuery{Since: now.Add(-time.Hour), Now: now})
	if len(m.ReadIssueFields) != 1 || m.ReadIssueFields[0].Field != invoice.FieldIssuerRUC ||
		len(m.BlockedFields) != 1 || m.BlockedFields[0].Field != invoice.FieldIssuerRUC {
		t.Errorf("métricas por campo: leídos %+v, bloquearon %+v", m.ReadIssueFields, m.BlockedFields)
	}
}

func TestBadCDCWarnsButDoesNotBlockAndCanBeRemovedOrTyped(t *testing.T) {
	// Arrange: CDC incompleto.
	h := newHarness(t)
	inv := sampleInvoice()
	inv.CDC = "0180000519800100100012341"
	h.reader.result.Invoice = inv

	// Act: se puede guardar igual; pero primero probamos los dos caminos de corrección.
	h.sendPhoto()
	read := h.telegram.lastSent(t)
	h.press(callback{action: actionNoCDC, id: 1})
	edits := h.telegram.byMethod("editMessageText")
	cleared := edits[len(edits)-1]

	// Assert
	if !strings.Contains(read.text, "44 dígitos") || !strings.Contains(read.markup, `"q:1"`) || !strings.Contains(read.text, "factura electrónica") {
		t.Errorf("debería avisar del CDC, ofrecer quitarlo y tratarla como electrónica mientras tanto: %+v", read)
	}
	if !strings.Contains(cleared.text, CDCClearedNote) || strings.Contains(cleared.text, "44 dígitos") || strings.Contains(cleared.markup, `"q:1"`) {
		t.Errorf("sin CDC no hay aviso ni botón: %+v", cleared)
	}
	rec, _ := h.store.Get(context.Background(), testChatID, 1)
	if rec.Invoice.CDC != "" {
		t.Errorf("CDC = %q", rec.Invoice.CDC)
	}

	// Act: corregir el CDC escribiéndolo.
	h.press(callback{action: actionEdit, id: 1})
	fields := h.telegram.byMethod("editMessageReplyMarkup")
	h.press(callback{action: actionField, id: 1, field: invoice.FieldCDC})
	asked := h.telegram.lastSent(t).text
	h.sendText("0180 0005 1980 0100 1000 1234 1202 6092 0123 4567 8901")
	typed := h.telegram.lastSent(t).text

	// Assert
	if !strings.Contains(fields[len(fields)-1].markup, `"f:1:cdc"`) {
		t.Errorf("el CDC debería estar entre los campos corregibles: %s", fields[len(fields)-1].markup)
	}
	if !strings.Contains(asked, "44 dígitos") {
		t.Errorf("pista del CDC: %q", asked)
	}
	if !strings.Contains(typed, "factura electrónica") || !strings.Contains(typed, "coherentes") {
		t.Errorf("con el CDC corregido vuelve a ser electrónica y cierra:\n%s", typed)
	}
}

func TestBadCDCStillAllowsSaving(t *testing.T) {
	h := newHarness(t)
	inv := sampleInvoice()
	inv.CDC = "0123"
	h.reader.result.Invoice = inv

	h.sendPhoto()
	h.press(callback{action: actionSave, id: 1})

	if rec, _ := h.store.Get(context.Background(), testChatID, 1); rec.Status != store.StatusSaved {
		t.Errorf("un CDC mal leído no bloquea el guardado: %s", rec.Status)
	}
}
