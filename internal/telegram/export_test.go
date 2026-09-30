package telegram

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/mmallorquin/bot-marangatu-facturas/internal/marangatu"
	"github.com/mmallorquin/bot-marangatu-facturas/internal/store"
)

func TestParseImputations(t *testing.T) {
	cases := map[string]store.Imputations{
		"/imputar iva":         {IVA: true},
		"/imputar IVA, IRP":    {IVA: true, IRP: true},
		"/imputar ire irp-rsp": {IRE: true, IRP: true},
		"/imputar iva,ire,rsp": {IVA: true, IRE: true, IRP: true},
	}
	for text, want := range cases {
		got, err := parseImputations(text)
		if err != nil || got != want {
			t.Errorf("parseImputations(%q) = %+v, %v; se esperaba %+v", text, got, err, want)
		}
	}
	for _, bad := range []string{"/imputar", "/imputar renta", "/imputar iva ganancias"} {
		if _, err := parseImputations(bad); err == nil {
			t.Errorf("parseImputations(%q) debería fallar", bad)
		}
	}
}

func TestExportCaptionListsSkippedInvoices(t *testing.T) {
	export := marangatu.Export{Rows: 3, Skipped: []marangatu.Skipped{
		{Number: "001-001-0000009", Reason: marangatu.ReasonElectronic},
	}}

	caption := exportCaption("2026-09", export, false)

	for _, want := range []string{"3 comprobantes", "Septiembre 2026", "001-001-0000009", marangatu.ReasonElectronic} {
		if !strings.Contains(caption, want) {
			t.Errorf("falta %q en:\n%s", want, caption)
		}
	}
}

func TestFormatExportPreviewLimitsTheChatListAndExplainsSkippedInvoices(t *testing.T) {
	preview := marangatu.Preview{Period: "2026-09", Total: 1_650_000}
	for i := 1; i <= 11; i++ {
		inv := sampleInvoice()
		inv.Number = fmt.Sprintf("001-001-%07d", i)
		inv.IssuerName = fmt.Sprintf("Proveedor %d", i)
		preview.Invoices = append(preview.Invoices, inv)
	}
	preview.Skipped = []marangatu.Skipped{{Number: "001-001-0000099", Reason: marangatu.ReasonElectronic}}

	text := formatExportPreview("2026-09", preview, store.Imputations{IVA: true, IRP: true})

	for _, want := range []string{
		"Previa", "Septiembre 2026", "11 comprobantes", "1.650.000 Gs", "IVA, IRP-RSP",
		"Proveedor 1", "001-001-0000010", "1 más", "001-001-0000099", marangatu.ReasonElectronic,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("falta %q en:\n%s", want, text)
		}
	}
	if strings.Contains(text, "Proveedor 11") {
		t.Errorf("la previa del chat debería limitarse a 10 filas:\n%s", text)
	}
}

// saveOneInvoice lee una foto y la guarda.
func (h *harness) saveOneInvoice(t *testing.T) {
	t.Helper()
	h.sendPhoto()
	h.press(callback{action: actionSave, id: 1})
}

func TestExportAsksForRUCAndImputationsFirst(t *testing.T) {
	h := newHarness(t)
	h.saveOneInvoice(t)

	h.sendText("/exportar")
	askRUC := h.telegram.lastSent(t).text
	h.sendText("/ruc 800246276")
	h.sendText("/exportar")
	askImputations := h.telegram.lastSent(t).text

	if !strings.Contains(askRUC, "/ruc") {
		t.Errorf("no pidió el RUC: %q", askRUC)
	}
	if !strings.Contains(askImputations, "/imputar") {
		t.Errorf("no pidió las imputaciones: %q", askImputations)
	}
	if len(h.telegram.byMethod("sendDocument")) != 0 {
		t.Error("no debería enviar el archivo sin configuración")
	}
}

func TestRUCCommandValidatesAndNormalizes(t *testing.T) {
	h := newHarness(t)

	h.sendText("/ruc 80024627-1")
	invalid := h.telegram.lastSent(t).text
	h.sendText("/ruc 800246276")
	valid := h.telegram.lastSent(t).text

	if !strings.Contains(invalid, "❌") {
		t.Errorf("debería rechazar el RUC inválido: %q", invalid)
	}
	if !strings.Contains(valid, "80024627-6") {
		t.Errorf("respuesta = %q", valid)
	}
	cs, _ := h.store.Settings(context.Background(), testChatID)
	if cs.RUC != "80024627-6" {
		t.Errorf("RUC guardado = %q", cs.RUC)
	}
}

func TestExportShowsPreviewAndOnlyGeneratesZipAfterConfirmation(t *testing.T) {
	// Arrange
	h := newHarness(t)
	h.saveOneInvoice(t)
	h.sendText("/ruc 80024627-6")
	h.sendText("/imputar iva irp")

	// Act
	h.sendText("/exportar")

	// Assert: primero muestra exactamente qué se va a exportar.
	preview := h.telegram.lastSent(t)
	for _, want := range []string{
		"Previa", "Septiembre 2026", "1 comprobante", "Comercial Ejemplo S.A.",
		"80000519-8", "001-001-0001234", "150.000", "IVA, IRP-RSP",
	} {
		if !strings.Contains(preview.text, want) {
			t.Errorf("falta %q en la previa:\n%s", want, preview.text)
		}
	}
	for _, callbackData := range []string{"x:c:2026-09", "x:e:2026-09", "x:z:2026-09", "x:n:2026-09"} {
		if !strings.Contains(preview.markup, callbackData) {
			t.Errorf("falta el botón %q en %s", callbackData, preview.markup)
		}
	}
	if docs := h.telegram.byMethod("sendDocument"); len(docs) != 0 {
		t.Fatalf("la previa no debe enviar archivos: %+v", docs)
	}

	// Act: descargar los formatos de revisión no consume V0001.
	h.pressRaw("x:c:2026-09")
	h.pressRaw("x:e:2026-09")
	h.pressRaw("x:z:2026-09")

	// Assert: CSV, Excel y finalmente el ZIP oficial V0001.
	docs := h.telegram.byMethod("sendDocument")
	if len(docs) != 3 {
		t.Fatalf("se esperaban CSV, Excel y ZIP; hubo %d", len(docs))
	}
	if docs[0].fileName != "80024627_PREVIA_092026.csv" {
		t.Errorf("CSV = %q", docs[0].fileName)
	}
	if docs[1].fileName != "80024627_PREVIA_092026.xlsx" {
		t.Errorf("Excel = %q", docs[1].fileName)
	}
	if docs[2].fileName != "80024627_REG_092026_V0001.zip" || !strings.Contains(docs[2].text, "1 comprobante") {
		t.Errorf("ZIP = %q, texto = %q", docs[2].fileName, docs[2].text)
	}

	// El ZIP conserva el formato oficial.
	r, err := zip.NewReader(bytes.NewReader(docs[2].fileData), int64(len(docs[2].fileData)))
	if err != nil || len(r.File) != 1 {
		t.Fatalf("ZIP inválido: %v", err)
	}
	f, _ := r.File[0].Open()
	content, _ := io.ReadAll(f)
	if !strings.HasPrefix(string(content), "2\t11\t80000519\t") {
		t.Errorf("contenido = %q", content)
	}

	// Una nueva confirmación recién consume V0002.
	h.sendText("/exportar")
	h.pressRaw("x:z:2026-09")
	if second := h.telegram.byMethod("sendDocument"); len(second) != 4 || second[3].fileName != "80024627_REG_092026_V0002.zip" {
		t.Errorf("la segunda exportación debería ser V0002: %+v", second)
	}
}

func TestExportPreviewCanBeCancelledWithoutSendingAFile(t *testing.T) {
	h := newHarness(t)
	h.saveOneInvoice(t)
	h.sendText("/ruc 80024627-6")
	h.sendText("/imputar iva")
	h.sendText("/exportar")

	h.pressRaw("x:n:2026-09")

	if docs := h.telegram.byMethod("sendDocument"); len(docs) != 0 {
		t.Errorf("cancelar no debería enviar archivos: %+v", docs)
	}
	if edits := h.telegram.byMethod("editMessageText"); len(edits) == 0 || !strings.Contains(edits[len(edits)-1].text, "cancelada") {
		t.Errorf("ediciones = %+v", edits)
	}
}

func TestExportWithoutSavedInvoices(t *testing.T) {
	h := newHarness(t)
	h.sendText("/ruc 80024627-6")
	h.sendText("/imputar ire")

	h.sendText("/exportar 08/2026")

	if got := h.telegram.lastSent(t).text; !strings.Contains(got, "Agosto 2026") || !strings.Contains(got, "No hay facturas guardadas") {
		t.Errorf("respuesta = %q", got)
	}
}

func TestExportExplainsWhenEveryInvoiceWasSkipped(t *testing.T) {
	h := newHarness(t)
	electronic := sampleInvoice()
	electronic.CDC = "01800005198001001000123412026092012345678901"
	h.reader.result.Invoice = electronic
	h.saveOneInvoice(t)
	h.sendText("/ruc 80024627-6")
	h.sendText("/imputar iva")

	h.sendText("/exportar")

	got := h.telegram.lastSent(t).text
	if !strings.Contains(got, marangatu.ReasonElectronic) || len(h.telegram.byMethod("sendDocument")) != 0 {
		t.Errorf("respuesta = %q", got)
	}
	for _, explanation := range []string{"1 comprobante guardado", "ZIP", "/resumen"} {
		if !strings.Contains(got, explanation) {
			t.Errorf("no explica %q en la respuesta: %q", explanation, got)
		}
	}
	summary, err := h.store.MonthSummary(context.Background(), testChatID, "2026-09")
	if err != nil || summary.Count != 1 {
		t.Errorf("la factura electrónica debe seguir guardada: %+v, %v", summary, err)
	}
}
