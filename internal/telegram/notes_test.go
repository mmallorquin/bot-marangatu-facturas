package telegram

import (
	"strings"
	"testing"
)

func TestDuplicateIsWarnedAsSoonAsItIsRead(t *testing.T) {
	// Arrange: la factura ya está guardada.
	h := newHarness(t)
	h.saveOneInvoice(t)

	// Act: llega la misma factura otra vez.
	h.sendPhoto()

	// Assert: el aviso aparece al leerla, antes de tocar Guardar.
	if got := h.telegram.lastSent(t).text; !strings.Contains(got, AlreadySavedNote) {
		t.Errorf("debería avisar del duplicado al leer:\n%s", got)
	}
}

func TestFirstReadHasNoDuplicateWarning(t *testing.T) {
	h := newHarness(t)

	h.sendPhoto()

	if got := h.telegram.lastSent(t).text; strings.Contains(got, AlreadySavedNote) || strings.Contains(got, ElectronicNote) {
		t.Errorf("una factura nueva y no electrónica no lleva avisos:\n%s", got)
	}
}

func TestElectronicInvoiceIsExplainedWhenRead(t *testing.T) {
	h := newHarness(t)
	inv := sampleInvoice()
	inv.CDC = "01800005198001001000123422026092012345678901"
	h.reader.result.Invoice = inv

	h.sendPhoto()

	if got := h.telegram.lastSent(t).text; !strings.Contains(got, ElectronicNote) {
		t.Errorf("debería explicar que la electrónica no va en el ZIP:\n%s", got)
	}
}
