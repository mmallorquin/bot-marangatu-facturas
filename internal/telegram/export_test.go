package telegram

import (
	"archive/zip"
	"bytes"
	"context"
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

	caption := exportCaption("2026-09", export)

	for _, want := range []string{"3 comprobantes", "Septiembre 2026", "001-001-0000009", marangatu.ReasonElectronic} {
		if !strings.Contains(caption, want) {
			t.Errorf("falta %q en:\n%s", want, caption)
		}
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

func TestExportSendsTheZipFileForMarangatu(t *testing.T) {
	// Arrange
	h := newHarness(t)
	h.saveOneInvoice(t)
	h.sendText("/ruc 80024627-6")
	h.sendText("/imputar iva irp")

	// Act
	h.sendText("/exportar")

	// Assert
	docs := h.telegram.byMethod("sendDocument")
	if len(docs) != 1 {
		t.Fatalf("se esperaba un archivo, hubo %d", len(docs))
	}
	if docs[0].fileName != "80024627_REG_092026_V0001.zip" || !strings.Contains(docs[0].text, "1 comprobante") {
		t.Errorf("archivo = %q, texto = %q", docs[0].fileName, docs[0].text)
	}
	r, err := zip.NewReader(bytes.NewReader(docs[0].fileData), int64(len(docs[0].fileData)))
	if err != nil || len(r.File) != 1 {
		t.Fatalf("ZIP inválido: %v", err)
	}
	f, _ := r.File[0].Open()
	content, _ := io.ReadAll(f)
	if !strings.HasPrefix(string(content), "2\t11\t80000519\t") {
		t.Errorf("contenido = %q", content)
	}

	h.sendText("/exportar")
	if second := h.telegram.byMethod("sendDocument"); len(second) != 2 || second[1].fileName != "80024627_REG_092026_V0002.zip" {
		t.Errorf("la segunda exportación debería ser V0002: %+v", second)
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
}
