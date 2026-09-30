package telegram

import (
	"context"
	"strings"
	"testing"
	"time"
)

func asuncion(year int, month time.Month, day, hour int) time.Time {
	return time.Date(year, month, day, hour, 0, 0, 0, reminderLocation)
}

func TestDuePeriods(t *testing.T) {
	cases := []struct {
		name            string
		now             time.Time
		monthly, annual string
	}{
		{"antes del día 3", asuncion(2026, 10, 2, 12), "", ""},
		{"día 3 temprano", asuncion(2026, 10, 3, 8), "", ""},
		{"día 3 a las 9", asuncion(2026, 10, 3, 9), "2026-09", ""},
		{"fin de mes", asuncion(2026, 10, 31, 20), "2026-09", ""},
		{"enero antes del 15", asuncion(2027, 1, 10, 10), "2026-12", ""},
		{"enero desde el 15", asuncion(2027, 1, 15, 10), "2026-12", "2026"},
	}
	for _, tc := range cases {
		monthly, annual := duePeriods(tc.now)
		if monthly != tc.monthly || annual != tc.annual {
			t.Errorf("%s: mensual %q anual %q, se esperaba %q %q", tc.name, monthly, annual, tc.monthly, tc.annual)
		}
	}
}

func TestMonthlyReminderIsSentOnceAndNotAfterExporting(t *testing.T) {
	// Arrange: una factura guardada de septiembre.
	h := newHarness(t)
	h.saveOneInvoice(t)
	handler := &handler{deps: Deps{Logger: discardLogger(), Store: h.store}, logger: discardLogger(), albums: &albums{}}
	october := asuncion(2026, 10, 3, 10)

	// Act: dos revisiones seguidas.
	handler.sendDueReminders(context.Background(), h.bot, october)
	handler.sendDueReminders(context.Background(), h.bot, october.Add(time.Hour))

	// Assert: un solo aviso, con el comando listo para usar.
	var reminders []string
	for _, m := range h.telegram.byMethod("sendMessage") {
		if strings.HasPrefix(m.text, "📅") {
			reminders = append(reminders, m.text)
		}
	}
	if len(reminders) != 1 || !strings.Contains(reminders[0], "1 factura guardada de Septiembre 2026") ||
		!strings.Contains(reminders[0], "/exportar 09/2026") {
		t.Errorf("recordatorios = %q", reminders)
	}
}

func TestNoReminderAfterExportingOrWhenDisabled(t *testing.T) {
	cases := map[string]func(h *harness){
		"ya exportó": func(h *harness) {
			h.sendText("/ruc 80024627-6")
			h.sendText("/imputar iva")
			h.sendText("/exportar 09/2026")
			h.pressRaw("x:z:2026-09")
		},
		"desactivado": func(h *harness) { h.sendText("/recordatorios no") },
		"presenta anual": func(h *harness) {
			h.sendText("/imputar irp") // el mensual no aplica: recibe el anual en enero
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.saveOneInvoice(t)
			setup(h)
			handler := &handler{deps: Deps{Logger: discardLogger(), Store: h.store}, logger: discardLogger(), albums: &albums{}}

			handler.sendDueReminders(context.Background(), h.bot, asuncion(2026, 10, 5, 10))

			for _, m := range h.telegram.byMethod("sendMessage") {
				if strings.HasPrefix(m.text, "📅") {
					t.Errorf("no debería recordar: %q", m.text)
				}
			}
		})
	}
}

func TestAnnualReminderForIRPOnlyInJanuary(t *testing.T) {
	h := newHarness(t)
	h.saveOneInvoice(t)
	h.sendText("/imputar irp")
	handler := &handler{deps: Deps{Logger: discardLogger(), Store: h.store}, logger: discardLogger(), albums: &albums{}}

	handler.sendDueReminders(context.Background(), h.bot, asuncion(2027, 1, 15, 10))

	if got := h.telegram.lastSent(t).text; !strings.Contains(got, "de 2026") || !strings.Contains(got, "/exportar 2026") {
		t.Errorf("recordatorio anual = %q", got)
	}
}

func TestRemindersCommand(t *testing.T) {
	h := newHarness(t)

	h.sendText("/recordatorios")
	initial := h.telegram.lastSent(t).text
	h.sendText("/recordatorios no")
	h.sendText("/recordatorios")
	disabled := h.telegram.lastSent(t).text

	if !strings.Contains(initial, "activados") || !strings.Contains(disabled, "desactivados") {
		t.Errorf("estados: %q / %q", initial, disabled)
	}
}
