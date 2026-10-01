package telegram

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mmallorquin/bot-marangatu-facturas/internal/openrouter"
	"github.com/mmallorquin/bot-marangatu-facturas/internal/store"
)

func TestWelcomeAllowsPhotoBeforeTaxSetup(t *testing.T) {
	h := newHarness(t)
	h.sendText("/start")
	if messages := h.telegram.byMethod("sendMessage"); len(messages) != 1 {
		t.Fatalf("welcome must be one photo-first message, got %d", len(messages))
	}
	cs, err := h.store.Settings(context.Background(), testChatID)
	if err != nil || cs.AwaitingRUC {
		t.Fatalf("welcome must not capture ordinary text as RUC: %+v, %v", cs, err)
	}
	h.sendPhoto()
	if !strings.Contains(h.telegram.lastSent(t).text, "0001234") {
		t.Fatal("a first photo should reach review without tax setup")
	}
}

func TestExplicitExportResumesExactPeriodAfterGuidedSetup(t *testing.T) {
	h := newHarness(t)
	h.saveInvoiceOf(1, "2026-08-10", "001-001-0000001")
	h.sendText("/exportar 08/2026")
	h.sendText("80024627-6")
	if !strings.Contains(h.telegram.lastSent(t).markup, "i:t:") {
		t.Fatal("export setup must accept RUC without requiring another command")
	}
	h.pressRaw("i:ok:1")
	h.pressRaw("reg:955:31c37163ec1d34a3a76e9ea0e5394089")
	preview := h.telegram.lastSent(t)
	if !strings.Contains(preview.text, "Previa — Agosto 2026") || !strings.Contains(preview.markup, "x:z:2026-08:") {
		t.Fatalf("setup must resume the requested August export: %+v", preview)
	}
	h.pressRaw(callbackDataIn(t, preview.markup, "x:z:"))
	if docs := h.telegram.byMethod("sendDocument"); len(docs) != 1 || docs[0].fileName != "80024627_REG_082026_V0001.zip" {
		t.Fatalf("wrong export after setup: %+v", docs)
	}
}

func TestExportMenuOffersPeriodsInParaguayTime(t *testing.T) {
	h := newHarness(t)
	h.handle = NewHandler(Deps{Logger: discardLogger(), Token: testToken, Store: h.store, Reader: h.reader,
		Now: func() time.Time { return time.Date(2026, 10, 1, 1, 0, 0, 0, time.UTC) }})
	h.saveOneInvoice(t)
	h.sendText("/ruc 80024627-6")
	h.sendText("/imputar iva")
	h.sendText("/registro 955")
	h.sendText("/exportar")
	menu := h.telegram.lastSent(t)
	if !strings.Contains(menu.markup, "ep:use:2026-09") || !strings.Contains(menu.markup, "ep:view:2026-08") {
		t.Fatalf("the UTC October boundary must still offer September locally: %+v", menu)
	}
	h.pressRaw(callbackDataIn(t, menu.markup, "ep:use:"))
	if !strings.Contains(h.telegram.lastSent(t).text, "Previa — Septiembre 2026") {
		t.Fatal("period button should open the selected preview")
	}
}

func TestCancellingExportSetupDoesNotResumeOldPeriod(t *testing.T) {
	h := newHarness(t)
	h.saveOneInvoice(t)
	h.sendText("/exportar 09/2026")
	h.sendText("/cancelar")
	h.sendText("/ruc 80024627-6")
	h.sendText("/imputar iva")
	h.sendText("/registro 955")
	for _, message := range h.telegram.byMethod("sendMessage") {
		if strings.Contains(message.markup, "x:z:") {
			t.Fatal("cancelled setup was silently resumed")
		}
	}
}

func TestOldSetupCancelCannotCancelANewerExport(t *testing.T) {
	h := newHarness(t)
	h.saveOneInvoice(t)
	h.sendText("/exportar 08/2026")
	oldCancel := callbackDataIn(t, h.telegram.lastSent(t).markup, "ep:cancel")
	h.sendText("/cancelar")
	h.sendText("/exportar 09/2026")
	h.pressRaw(oldCancel)
	cs, err := h.store.Settings(context.Background(), testChatID)
	if err != nil || !cs.AwaitingRUC || cs.PendingExport != "2026-09" {
		t.Fatalf("old cancel cleared the newer September setup: %+v, %v", cs, err)
	}
	h.sendText("80024627-6")
	h.pressRaw("i:ok:1")
	h.pressRaw("reg:955:31c37163ec1d34a3a76e9ea0e5394089")
	if !strings.Contains(h.telegram.lastSent(t).text, "Previa — Septiembre 2026") {
		t.Fatal("new setup did not resume September")
	}
}

func TestDeliveredZIPOffersExplicitUserPresentationConfirmation(t *testing.T) {
	h := newHarness(t)
	setupReliableExport(t, h)
	h.pressRaw(callbackDataIn(t, h.telegram.lastSent(t).markup, "x:z:"))
	message := h.telegram.lastSent(t)
	if !strings.Contains(message.markup, "tp:ask:2026-09:") {
		t.Fatalf("successful ZIP delivery should offer a separate manual presentation step: %+v", message)
	}
	h.pressRaw(callbackDataIn(t, message.markup, "tp:ask:"))
	question := h.telegram.lastSent(t)
	if !strings.Contains(question.text, "Talón") || !strings.Contains(question.text, "no verifica") {
		t.Fatalf("confirmation must require the user's Marangatu check: %+v", question)
	}
	h.pressRaw(callbackDataIn(t, question.markup, "tp:yes:"))
	h.sendText("/exportar 09/2026")
	if !strings.Contains(h.telegram.lastSent(t).text, "Presentación marcada por vos") {
		t.Fatal("preview must distinguish user-marked presentation from ZIP delivery")
	}
	cs, _ := h.store.Settings(context.Background(), testChatID)
	if cs.Registration != store.RegistrationMonthly {
		t.Fatal("presentation confirmation must not change tax configuration")
	}
}

func TestPresentationCannotConfirmChangedOrDeletedZIP(t *testing.T) {
	for _, change := range []string{"settings", "delete"} {
		t.Run(change, func(t *testing.T) {
			h := newHarness(t)
			setupReliableExport(t, h)
			h.pressRaw(callbackDataIn(t, h.telegram.lastSent(t).markup, "x:z:"))
			h.pressRaw(callbackDataIn(t, h.telegram.lastSent(t).markup, "tp:ask:"))
			confirm := callbackDataIn(t, h.telegram.lastSent(t).markup, "tp:yes:")
			if change == "settings" {
				h.sendText("/imputar iva irp")
			} else {
				if _, err := h.store.DeleteChat(context.Background(), testChatID); err != nil {
					t.Fatal(err)
				}
			}
			h.pressRaw(confirm)
			state, err := h.store.ExportPresentation(context.Background(), testChatID, "2026-09")
			if err != nil || state.ConfirmedAt != "" {
				t.Fatalf("stale ZIP marked presented: %+v %v", state, err)
			}
		})
	}
}

func TestChangingSettingsWarnsAboutPreviousManualPresentation(t *testing.T) {
	h := newHarness(t)
	setupReliableExport(t, h)
	h.pressRaw(callbackDataIn(t, h.telegram.lastSent(t).markup, "x:z:"))
	h.pressRaw(callbackDataIn(t, h.telegram.lastSent(t).markup, "tp:ask:"))
	h.pressRaw(callbackDataIn(t, h.telegram.lastSent(t).markup, "tp:yes:"))
	h.sendText("/imputar iva irp")
	h.sendText("/exportar 09/2026")
	if !strings.Contains(h.telegram.lastSent(t).text, "las facturas o los ajustes cambiaron") {
		t.Fatal("an old user declaration must not cover changed data")
	}
}

func TestReaderQuotaFailureExplainsAdminActionWithoutLeakingProviderText(t *testing.T) {
	h := newHarness(t)
	h.reader.err = &openrouter.APIError{StatusCode: 402, Message: "secret-provider-details"}
	h.sendPhoto()
	message := h.telegram.lastSent(t).text
	if !strings.Contains(message, "saldo") || !strings.Contains(message, "administrador") || strings.Contains(message, "secret-provider-details") {
		t.Fatalf("quota failure must explain a safe next action: %q", message)
	}
	if drafts, err := h.store.Drafts(context.Background(), testChatID); err != nil || len(drafts) != 0 {
		t.Fatalf("a failed extraction created invoice data: %v %v", drafts, err)
	}
}
