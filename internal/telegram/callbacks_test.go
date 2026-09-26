package telegram

import (
	"strings"
	"testing"
	"time"

	"github.com/mmallorquin/bot-marangatu-facturas/internal/invoice"
	"github.com/mmallorquin/bot-marangatu-facturas/internal/store"
)

func TestCallbackRoundTrip(t *testing.T) {
	cases := []callback{
		{action: actionSave, id: 42},
		{action: actionDiscard, id: 7},
		{action: actionEdit, id: 1},
		{action: actionBack, id: 1},
		{action: actionField, id: 99, field: invoice.FieldTotal},
	}

	for _, want := range cases {
		t.Run(want.encode(), func(t *testing.T) {
			// Act
			got, err := parseCallback(want.encode())

			// Assert
			if err != nil || got != want {
				t.Errorf("parseCallback(%q) = %+v, %v; se esperaba %+v", want.encode(), got, err, want)
			}
		})
	}
}

func TestCallbackDataFitsTelegramLimit(t *testing.T) {
	const telegramMaxCallbackBytes = 64
	for _, field := range invoice.EditableFields {
		data := callback{action: actionField, id: 1<<62 - 1, field: field}.encode()
		if len(data) > telegramMaxCallbackBytes {
			t.Errorf("%q ocupa %d bytes", data, len(data))
		}
	}
}

func TestParseCallbackRejectsGarbage(t *testing.T) {
	for _, data := range []string{"", "x", "g:", "g:abc", "z:1", "f:1", "f:1:campo_inventado", "g:1:extra"} {
		if _, err := parseCallback(data); err == nil {
			t.Errorf("parseCallback(%q) debería fallar", data)
		}
	}
}

func TestFieldsKeyboardHasEveryEditableFieldAndBack(t *testing.T) {
	kb := fieldsKeyboard(5)

	var data []string
	for _, row := range kb.InlineKeyboard {
		for _, button := range row {
			data = append(data, button.CallbackData)
		}
	}
	joined := strings.Join(data, " ")
	for _, field := range invoice.EditableFields {
		if !strings.Contains(joined, "f:5:"+field) {
			t.Errorf("falta el botón para %q", field)
		}
	}
	if !strings.Contains(joined, "v:5") {
		t.Error("falta el botón Volver")
	}
}

func TestParsePeriod(t *testing.T) {
	now := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	cases := map[string]string{
		"/resumen":         "2026-09",
		"/resumen 2026-08": "2026-08",
		"/resumen 8/2026":  "2026-08",
		"/resumen 08/2026": "2026-08",
	}

	for text, want := range cases {
		got, err := parsePeriod(text, now)
		if err != nil || got != want {
			t.Errorf("parsePeriod(%q) = %q, %v; se esperaba %q", text, got, err, want)
		}
	}
	if _, err := parsePeriod("/resumen ayer", now); err == nil {
		t.Error("se esperaba un error con un período inválido")
	}
}

func TestFormatMonthSummary(t *testing.T) {
	sum := store.Summary{Count: 2, Taxed10: 330_000, VAT10: 30_000, Total: 330_000}

	text := FormatMonthSummary("2026-09", sum)

	for _, want := range []string{"Septiembre 2026", "2 facturas", "IVA 10 %: 30.000", "Total: 330.000 Gs"} {
		if !strings.Contains(text, want) {
			t.Errorf("falta %q en:\n%s", want, text)
		}
	}
	if empty := FormatMonthSummary("2026-09", store.Summary{}); !strings.Contains(empty, "No guardaste facturas") {
		t.Errorf("resumen vacío = %q", empty)
	}
}
