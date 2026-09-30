package telegram

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/go-telegram/bot/models"

	"github.com/mmallorquin/bot-marangatu-facturas/internal/invoice"
)

// TestFullUserJourney recorre el flujo completo de un usuario en Telegram, de punta a punta:
// bienvenida, foto con errores, corrección, guardado, duplicado, PDF electrónico, resumen,
// lista, configuración, exportación mensual y anual, y borrado. Usa la base SQLite real y
// Telegram y OpenRouter simulados.
func TestFullUserJourney(t *testing.T) {
	h := newHarness(t)
	expect := func(step, got string, wants ...string) {
		t.Helper()
		for _, want := range wants {
			if !strings.Contains(got, want) {
				t.Errorf("%s: falta %q en:\n%s", step, want, got)
			}
		}
	}
	lastText := func() string { return h.telegram.lastSent(t).text }

	// 1. Bienvenida y ayuda.
	h.sendText("/start")
	expect("/start", lastText(), "Mandame una foto", "/facturas", "/exportar 2026")
	h.sendText("/ayuda")
	expect("/ayuda", lastText(), "/resumen", "/exportar")

	// 2. Foto con el IVA mal leído: se avisa y no se puede guardar.
	misread := sampleInvoice()
	misread.VAT10 = 1_000
	h.reader.result.Invoice = misread
	h.sendPhoto()
	expect("lectura con error", lastText(), "Factura 001-001-0001234", "debería ser cerca de 13.636, pero dice 1.000")
	h.press(callback{action: actionSave, id: 1})
	if alerts := h.telegram.byMethod("answerCallbackQuery"); !alerts[len(alerts)-1].alert {
		t.Error("guardar con errores debería mostrar una alerta")
	}

	// 3. Corrección: valor inválido, después el correcto, y guardado.
	h.press(callback{action: actionField, id: 1, field: invoice.FieldVAT10})
	h.sendText("trece mil")
	expect("corrección inválida", lastText(), "no es válido", "/cancelar")
	h.sendText("13.636")
	expect("corrección válida", lastText(), "IVA 10 %: 13.636", "coherentes")
	h.press(callback{action: actionSave, id: 1})
	edits := h.telegram.byMethod("editMessageText")
	expect("guardada", edits[len(edits)-1].text, "Guardada")

	// 4. La misma factura otra vez: se detecta el duplicado y se descarta.
	h.reader.result.Invoice = sampleInvoice()
	h.sendPhoto()
	h.press(callback{action: actionSave, id: 2})
	edits = h.telegram.byMethod("editMessageText")
	expect("duplicada", edits[len(edits)-1].text, "Ya tenías guardada")
	h.press(callback{action: actionDiscard, id: 2})

	// 5. Una imagen que no es factura y un archivo que no se puede leer.
	h.reader.result.Invoice = invoice.Invoice{}
	h.sendPhoto()
	expect("no factura", lastText(), "No parece una factura")
	h.send(&models.Update{Message: &models.Message{Chat: models.Chat{ID: testChatID},
		Document: &models.Document{FileID: "w", MimeType: "application/msword"}}})
	expect("Word", lastText(), "PDF")

	// 6. Factura electrónica en PDF: se guarda, pero no va en el ZIP.
	electronic := sampleInvoice()
	electronic.Number = "001-001-0009999"
	electronic.CDC = "01800005198001001000999922026092012345678901"
	h.reader.result.Invoice = electronic
	h.send(&models.Update{Message: &models.Message{Chat: models.Chat{ID: testChatID},
		Document: &models.Document{FileID: "p", MimeType: "application/pdf"}}})
	h.press(callback{action: actionSave, id: 3})

	// 7. Resumen y lista del mes.
	h.sendText("/resumen")
	expect("/resumen", lastText(), "Septiembre 2026: 2 facturas guardadas", "Total: 300.000 Gs")
	h.sendText("/facturas")
	expect("/facturas", lastText(), "0009999", "0001234")

	// 8. Exportar pide la configuración primero.
	h.sendText("/exportar")
	expect("exportar sin RUC", lastText(), "/ruc")
	h.sendText("/ruc 80024627-6")
	h.sendText("/exportar")
	expect("exportar sin imputar", lastText(), "/imputar")
	h.sendText("/imputar irp")
	expect("/imputar irp", lastText(), "IRP-RSP", "/exportar 2026")

	// 9. Exportación mensual: previa, CSV y ZIP (la electrónica queda afuera).
	h.sendText("/exportar")
	expect("previa mensual", lastText(), "1 comprobante listo para Marangatu", "001-001-0009999 — es electrónica", "/exportar 2026")
	h.pressRaw("x:c:2026-09")
	h.pressRaw("x:z:2026-09")

	// 10. Exportación anual.
	h.sendText("/exportar 2026")
	expect("previa anual", lastText(), "Previa — 2026", "Archivo anual")
	h.pressRaw("x:z:2026")

	docs := h.telegram.byMethod("sendDocument")
	var names []string
	for _, d := range docs {
		names = append(names, d.fileName)
	}
	want := "80024627_PREVIA_092026.csv,80024627_REG_092026_V0001.zip,80024627_REG_2026_V0001.zip"
	if strings.Join(names, ",") != want {
		t.Fatalf("archivos = %v, se esperaba %s", names, want)
	}
	expect("caption del ZIP", docs[1].text, "1 comprobante de Septiembre 2026 listo")
	r, err := zip.NewReader(bytes.NewReader(docs[2].fileData), int64(len(docs[2].fileData)))
	if err != nil || len(r.File) != 1 || r.File[0].Name != "80024627_REG_2026_V0001.txt" {
		t.Fatalf("ZIP anual inválido: %v", err)
	}
	f, _ := r.File[0].Open()
	content, _ := io.ReadAll(f)
	if rows := strings.Count(string(content), "\r\n"); rows != 1 || !strings.Contains(string(content), "001-001-0001234") {
		t.Errorf("contenido del anual (%d filas):\n%q", rows, content)
	}

	// 11. Borrar una factura desde /facturas.
	h.sendText("/facturas")
	h.pressRaw("l:a:1:2026-09")
	h.pressRaw("l:s:1:2026-09")
	h.sendText("/resumen")
	expect("resumen tras borrar", lastText(), "1 factura guardada", "150.000 Gs")
}
