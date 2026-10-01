package telegram

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestRegistrationOffersButtonsAndIsIndependentOfTaxes(t *testing.T) {
	h := newHarness(t)
	h.sendText("/ruc 80024627-6")
	h.sendText("/imputar irp")
	h.sendText("/registro")
	prompt := h.telegram.lastSent(t)
	for _, want := range []string{"955", "956", "No sé", "reg:956:31c37163ec1d34a3a76e9ea0e5394089"} {
		if !strings.Contains(prompt.markup, want) {
			t.Errorf("falta opción %q: %+v", want, prompt)
		}
	}
	h.pressRaw("reg:956:31c37163ec1d34a3a76e9ea0e5394089")
	h.sendText("/imputar iva irp")
	h.sendText("/registro")
	if text := h.telegram.lastSent(t).text; !strings.Contains(text, "configurado: 956") {
		t.Fatalf("cambiar impuestos no debe cambiar el registro: %s", text)
	}
}

func TestRegistrationButtonsFitTelegramWithLongAcceptedRUC(t *testing.T) {
	h := newHarness(t)
	h.sendText("/ruc " + strings.Repeat("0", 60) + "-0")
	h.sendText("/registro")
	var markup struct {
		Rows [][]struct {
			Data string `json:"callback_data"`
		} `json:"inline_keyboard"`
	}
	if err := json.Unmarshal([]byte(h.telegram.lastSent(t).markup), &markup); err != nil {
		t.Fatal(err)
	}
	if len(markup.Rows) != 2 {
		t.Fatalf("el registro debe ofrecer sus opciones: %+v", markup)
	}
	for _, row := range markup.Rows {
		for _, button := range row {
			if len(button.Data) > 64 {
				t.Fatalf("un RUC aceptado genera un botón que Telegram rechaza: %d bytes", len(button.Data))
			}
		}
	}
	h.pressRaw(markup.Rows[0][1].Data)
	h.sendText("/registro")
	if text := h.telegram.lastSent(t).text; !strings.Contains(text, "configurado: 956") {
		t.Errorf("el botón compacto debe seguir ligado al RUC: %s", text)
	}
}

func TestRegistrationChangeInvalidatesOldExportPreview(t *testing.T) {
	h := newHarness(t)
	h.saveOneInvoice(t)
	h.sendText("/ruc 80024627-6")
	h.sendText("/imputar irp")
	h.sendText("/registro 955")
	h.sendText("/exportar 09/2026")
	old := h.exportButton(t, "x:z:2026-09")
	h.sendText("/registro 956")
	h.pressRaw(old)
	if docs := h.telegram.byMethod("sendDocument"); len(docs) != 0 {
		t.Fatal("un botón anterior al cambio de registro no debe exportar")
	}
	edits := h.telegram.byMethod("editMessageText")
	if len(edits) == 0 || !strings.Contains(edits[len(edits)-1].text, "956") {
		t.Fatalf("debe refrescar la previa con la configuración actual: %+v", edits)
	}
	var markup struct {
		Rows [][]struct {
			Data string `json:"callback_data"`
		} `json:"inline_keyboard"`
	}
	if err := json.Unmarshal([]byte(edits[len(edits)-1].markup), &markup); err != nil {
		t.Fatal(err)
	}
	if len(markup.Rows) != 2 || len(markup.Rows[1]) != 2 {
		t.Fatalf("previa refrescada inválida: %+v", markup)
	}
	h.pressRaw(markup.Rows[1][0].Data)
	if docs := h.telegram.byMethod("sendDocument"); len(docs) != 0 {
		t.Fatal("reconfirmar la previa no puede saltar la validación del período")
	}
}

func TestRegistrationNeedsRUCAndUnknownOptionDoesNotChoose(t *testing.T) {
	h := newHarness(t)
	h.sendText("/registro 955")
	if text := h.telegram.lastSent(t).text; !strings.Contains(text, "/ruc") {
		t.Errorf("sin RUC debe pedirlo: %s", text)
	}
	h.sendText("/ruc 80024627-6")
	h.sendText("/registro")
	h.pressRaw("reg:help:31c37163ec1d34a3a76e9ea0e5394089")
	if text := h.telegram.lastSent(t).text; !strings.Contains(text, "Marangatu") || !strings.Contains(text, "obligaciones") {
		t.Errorf("No sé debe explicar dónde consultar: %s", text)
	}
	h.sendText("/registro")
	if text := h.telegram.lastSent(t).text; !strings.Contains(text, "sin configurar") {
		t.Errorf("No sé no puede adivinar el registro: %s", text)
	}
}

func TestRegistrationResetsOnRUCChangeAndRejectsOldButtons(t *testing.T) {
	h := newHarness(t)
	h.sendText("/ruc 80024627-6")
	h.sendText("/registro 956")
	h.sendText("/ruc 80024627-6")
	h.sendText("/registro")
	if text := h.telegram.lastSent(t).text; !strings.Contains(text, "configurado: 956") {
		t.Errorf("el mismo RUC debe conservar el registro: %s", text)
	}
	h.sendText("/ruc 80000519-8")
	h.pressRaw("reg:956:31c37163ec1d34a3a76e9ea0e5394089")
	h.sendText("/registro")
	if text := h.telegram.lastSent(t).text; !strings.Contains(text, "sin configurar") {
		t.Errorf("un botón del RUC anterior no puede configurar el nuevo: %s", text)
	}
}

func TestRegistrationRejectsMalformedChoicesWithoutChangingSelection(t *testing.T) {
	h := newHarness(t)
	h.sendText("/ruc 80024627-6")
	h.sendText("/registro 955")
	for _, data := range []string{"reg:", "reg:957:80024627-6", "reg:956:80024627-6:extra", "reg:956:"} {
		h.pressRaw(data)
	}
	h.sendText("/registro 957")
	h.sendText("/registro 956 extra")
	h.sendText("/registro")
	if text := h.telegram.lastSent(t).text; !strings.Contains(text, "configurado: 955") {
		t.Errorf("entradas inválidas no deben modificar la elección: %s", text)
	}
}

func TestRegistrationUnknownAllowsReviewButBlocksZip(t *testing.T) {
	h := newHarness(t)
	h.saveOneInvoice(t)
	h.sendText("/ruc 80024627-6")
	h.sendText("/imputar irp")
	h.sendText("/exportar 09/2026")
	h.pressExport(t, "x:c:2026-09")
	h.pressExport(t, "x:e:2026-09")
	h.pressExport(t, "x:z:2026-09")
	docs := h.telegram.byMethod("sendDocument")
	if len(docs) != 2 || !strings.HasSuffix(docs[0].fileName, ".csv") || !strings.HasSuffix(docs[1].fileName, ".xlsx") {
		t.Fatalf("sin elegir registro solo se permiten archivos de revisión: %+v", docs)
	}
	if text := h.telegram.lastSent(t).text; !strings.Contains(text, "/registro") {
		t.Errorf("el ZIP debe pedir la elección explícita: %s", text)
	}
	h.sendText("/registro 955")
	h.sendText("/exportar 09/2026")
	h.pressExport(t, "x:z:2026-09")
	docs = h.telegram.byMethod("sendDocument")
	if len(docs) != 3 || docs[2].fileName != "80024627_REG_092026_V0001.zip" {
		t.Errorf("el intento bloqueado no debe consumir numeración: %+v", docs)
	}
}

func TestRegistrationBlocksWrongZipPeriodButKeepsReview(t *testing.T) {
	for _, tc := range []struct{ registration, wrongPeriod, callback, rightPeriod, file string }{
		{"956", "09/2026", "2026-09", "2026", "80024627_REG_2026_V0001.zip"},
		{"955", "2026", "2026", "09/2026", "80024627_REG_092026_V0001.zip"},
	} {
		t.Run(tc.registration, func(t *testing.T) {
			h := newHarness(t)
			h.saveOneInvoice(t)
			h.sendText("/ruc 80024627-6")
			h.sendText("/imputar irp")
			h.sendText("/registro " + tc.registration)
			h.sendText("/exportar " + tc.wrongPeriod)
			h.pressExport(t, "x:c:"+tc.callback)
			h.pressExport(t, "x:z:"+tc.callback)
			if docs := h.telegram.byMethod("sendDocument"); len(docs) != 1 || !strings.HasSuffix(docs[0].fileName, ".csv") {
				t.Fatalf("período incompatible debe permitir CSV, no ZIP: %+v", docs)
			}
			answers := h.telegram.byMethod("answerCallbackQuery")
			if !answers[len(answers)-1].alert || !strings.Contains(answers[len(answers)-1].text, tc.registration) {
				t.Errorf("el bloqueo debe explicar la obligación: %+v", answers)
			}
			h.sendText("/exportar " + tc.rightPeriod)
			callback := "2026"
			if tc.registration == "955" {
				callback = "2026-09"
			}
			h.pressExport(t, "x:z:"+callback)
			docs := h.telegram.byMethod("sendDocument")
			if len(docs) != 2 || docs[1].fileName != tc.file {
				t.Errorf("exportación del período correcto: %+v", docs)
			}
		})
	}
}

func TestRegistrationDefaultExportUsesAnnualPeriodAndLocalYear(t *testing.T) {
	h := newHarness(t)
	h.saveOneInvoice(t)
	h.sendText("/ruc 80024627-6")
	h.sendText("/imputar irp")
	h.sendText("/registro 956")
	for _, now := range []time.Time{
		asuncion(2027, time.January, 15, 10), asuncion(2027, time.February, 15, 10),
		// En UTC ya es marzo; en Paraguay todavía es febrero.
		time.Date(2027, time.March, 1, 1, 0, 0, 0, time.UTC),
	} {
		h.handle = NewHandler(Deps{Logger: discardLogger(), Store: h.store, Reader: h.reader, Now: func() time.Time { return now }})
		h.sendText("/exportar")
		if text := h.telegram.lastSent(t).text; !strings.Contains(text, "Previa — 2026") {
			t.Errorf("%s: debe sugerir el ejercicio anterior completo: %s", now, text)
		}
	}
}

func TestRegistrationReminderRoutingUsesExplicitChoice(t *testing.T) {
	for _, tc := range []struct{ registration, taxes, want string }{
		{"", "irp", ""}, {"955", "irp", "/exportar 12/2026"},
		{"956", "iva irp", "/exportar 2026"},
	} {
		t.Run(tc.registration+tc.taxes, func(t *testing.T) {
			h := newHarness(t)
			h.saveInvoiceOf(1, "2026-12-20", "001-001-0001234")
			h.sendText("/ruc 80024627-6")
			h.sendText("/imputar " + tc.taxes)
			if tc.registration != "" {
				h.sendText("/registro " + tc.registration)
			}
			handler := &handler{deps: Deps{Logger: discardLogger(), Store: h.store}, logger: discardLogger(), albums: &albums{}}
			handler.sendDueReminders(context.Background(), h.bot, asuncion(2027, time.January, 15, 10))
			var reminders []string
			for _, sent := range h.telegram.byMethod("sendMessage") {
				if strings.HasPrefix(sent.text, "📅") {
					reminders = append(reminders, sent.text)
				}
			}
			if tc.want == "" {
				if len(reminders) != 0 {
					t.Fatalf("sin registro elegido no se debe adivinar un aviso: %v", reminders)
				}
			} else if len(reminders) != 1 || !strings.Contains(reminders[0], tc.want) {
				t.Errorf("aviso debe seguir el registro, no /imputar: %v", reminders)
			}
		})
	}
}
