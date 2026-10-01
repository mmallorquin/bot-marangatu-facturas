package telegram

import (
	"context"
	"strings"
	"testing"
)

// Catches missing settings routing and toggles that use stale assumed values.
func TestSettingsShowsCurrentConfigurationAndExplicitToggles(t *testing.T) {
	h := newHarness(t)
	h.sendText("/ajustes")
	settings := h.telegram.lastSent(t)
	for _, data := range []string{`"c:ruc"`, `"c:impute"`, `"c:registration"`, `"c:auto:1"`, `"c:rem:0"`, `"c:delete"`} {
		if !strings.Contains(settings.markup, data) {
			t.Fatalf("falta acción %s: %+v", data, settings)
		}
	}
	h.pressRaw("c:auto:1")
	h.pressRaw("c:rem:0")
	cs, _ := h.store.Settings(context.Background(), testChatID)
	if !cs.AutoSave || cs.Reminders {
		t.Fatalf("ajustes no aplicados: %+v", cs)
	}
	h.sendText("/ajustes")
	settings = h.telegram.lastSent(t)
	if !strings.Contains(settings.markup, `"c:auto:0"`) || !strings.Contains(settings.markup, `"c:rem:1"`) {
		t.Fatalf("ajustes no reflejan estado: %+v", settings)
	}
}

// Catches deletion that bypasses the existing one-use confirmation protection.
func TestSettingsDeleteAsksConfirmationBeforeRemovingData(t *testing.T) {
	h := newHarness(t)
	h.saveInvoiceOf(1, "2026-09-10", "001-001-0000001")
	h.pressRaw("c:delete")
	question := h.telegram.lastSent(t)
	if !strings.Contains(question.markup, "z:s:") || !strings.Contains(question.text, "¿Borrar todos tus datos") {
		t.Fatalf("confirmación ausente: %+v", question)
	}
	if _, err := h.store.Get(context.Background(), testChatID, 1); err != nil {
		t.Fatalf("se borró sin confirmar: %v", err)
	}
}

// Catches a settings RUC button that only prints the legacy command instructions.
func TestSettingsRUCBeginsTypedSetup(t *testing.T) {
	h := newHarness(t)
	h.pressRaw("c:ruc")
	cs, _ := h.store.Settings(context.Background(), testChatID)
	if !cs.AwaitingRUC {
		t.Fatal("Ajustes debe esperar RUC escrito sin comando")
	}
	h.sendText("80024627-6")
	cs, _ = h.store.Settings(context.Background(), testChatID)
	if cs.RUC != "80024627-6" || cs.AwaitingRUC {
		t.Fatalf("RUC no configurado: %+v", cs)
	}
}
