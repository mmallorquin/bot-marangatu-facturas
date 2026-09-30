package telegram

import (
	"strings"
	"testing"
	"time"
)

// saveInvoiceOf lee y guarda una factura con esa fecha y número.
// id es el número de borrador: la base es nueva en cada test, así que la n-ésima foto es el borrador n.
func (h *harness) saveInvoiceOf(id int64, date, number string) {
	h.reader.result.Invoice.Date = date
	h.reader.result.Invoice.Number = number
	h.sendPhoto()
	h.press(callback{action: actionSave, id: id})
}

func TestAnnualExportIncludesTheWholeYear(t *testing.T) {
	// Arrange: facturas de marzo y septiembre, e imputación al IRP.
	h := newHarness(t)
	h.saveInvoiceOf(1, "2026-03-10", "001-001-0000001")
	h.saveInvoiceOf(2, "2026-09-20", "001-001-0000002")
	h.sendText("/ruc 80024627-6")
	h.sendText("/imputar irp")
	imputeReply := h.telegram.lastSent(t).text

	// Act
	h.sendText("/exportar 2026")
	preview := h.telegram.lastSent(t)
	h.pressRaw("x:z:2026")
	h.sendText("/exportar 2026")
	h.pressRaw("x:z:2026")
	h.sendText("/exportar 09/2026")
	monthlyPreview := h.telegram.lastSent(t).text
	h.pressRaw("x:z:2026-09")

	// Assert
	if !strings.Contains(imputeReply, "/exportar 2026") {
		t.Errorf("/imputar irp debería explicar el archivo anual: %q", imputeReply)
	}
	for _, want := range []string{"Previa — 2026", "2 comprobantes", "Archivo anual", "/exportar 09/2026"} {
		if !strings.Contains(preview.text, want) {
			t.Errorf("falta %q en la previa anual:\n%s", want, preview.text)
		}
	}
	if !strings.Contains(preview.markup, "x:z:2026\"") {
		t.Errorf("el botón ZIP debería ser anual: %s", preview.markup)
	}
	if !strings.Contains(monthlyPreview, "1 comprobante") || !strings.Contains(monthlyPreview, "/exportar 2026") {
		t.Errorf("la previa mensual con IRP debería indicar cómo pedir el anual:\n%s", monthlyPreview)
	}
	docs := h.telegram.byMethod("sendDocument")
	var names []string
	for _, d := range docs {
		names = append(names, d.fileName)
	}
	want := []string{"80024627_REG_2026_V0001.zip", "80024627_REG_2026_V0002.zip", "80024627_REG_092026_V0001.zip"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("archivos = %v, se esperaba %v", names, want)
	}
}

func TestMonthlyExportWithoutIRPHasNoAnnualHint(t *testing.T) {
	h := newHarness(t)
	h.saveOneInvoice(t)
	h.sendText("/ruc 80024627-6")
	h.sendText("/imputar iva")

	h.sendText("/exportar")

	if text := h.telegram.lastSent(t).text; strings.Contains(text, "anual") {
		t.Errorf("sin IRP no hace falta hablar del anual:\n%s", text)
	}
}

func TestAnnualSummary(t *testing.T) {
	h := newHarness(t)
	h.saveInvoiceOf(1, "2026-03-10", "001-001-0000001")
	h.saveInvoiceOf(2, "2026-09-20", "001-001-0000002")

	h.sendText("/resumen 2026")

	if text := h.telegram.lastSent(t).text; !strings.Contains(text, "2026: 2 facturas") || !strings.Contains(text, "300.000") {
		t.Errorf("resumen anual = %q", text)
	}
}

func TestAnnualYearToFileSuggestsLastYearUntilFebruary(t *testing.T) {
	cases := map[time.Month]string{time.January: "2026", time.February: "2026", time.March: "2027", time.December: "2027"}
	for month, want := range cases {
		if got := annualYearToFile(time.Date(2027, month, 15, 0, 0, 0, 0, time.UTC)); got != want {
			t.Errorf("%s 2027: sugiere %s, se esperaba %s", month, got, want)
		}
	}
}

func TestAnnualHintIsOnlyForIRPWithoutIVAOrIRE(t *testing.T) {
	h := newHarness(t)
	h.saveOneInvoice(t)
	h.sendText("/ruc 80024627-6")

	h.sendText("/imputar iva irp")
	imputeReply := h.telegram.lastSent(t).text
	h.sendText("/exportar")
	preview := h.telegram.lastSent(t).text

	if strings.Contains(imputeReply, "anual") || strings.Contains(preview, "anual") {
		t.Errorf("quien liquida IVA registra mes a mes, no hace falta el aviso anual:\n%s\n---\n%s", imputeReply, preview)
	}
}
