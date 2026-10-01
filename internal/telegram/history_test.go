package telegram

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/go-telegram/bot/models"
	"github.com/mmallorquin/bot-marangatu-facturas/internal/invoice"
	"github.com/mmallorquin/bot-marangatu-facturas/internal/store"
)

// Catches a list that hides draft IDs, leaving the user dependent on old messages.
func TestMyInvoicesRecoversDraftDetailAndExistingReviewActions(t *testing.T) {
	h := newHarness(t)
	h.sendPhoto()
	h.sendText("/facturas")
	list := h.telegram.lastSent(t)
	if !strings.Contains(list.text, "1 pendiente") || !strings.Contains(list.markup, `"h:o:p:2026-09:0:1"`) || !strings.Contains(list.markup, "Guardadas") {
		t.Fatalf("pendiente inaccesible desde Mis facturas: %+v", list)
	}
	h.pressRaw("h:o:p:2026-09:0:1")
	detail := lastEdited(t, h)
	if !strings.Contains(detail.text, "001-001-0001234") || !strings.Contains(detail.markup, `"g:1"`) || !strings.Contains(detail.markup, `"e:1"`) {
		t.Fatalf("detalle sin revisión: %+v", detail)
	}
	h.press(callback{action: actionSave, id: 1})
	rec, err := h.store.Get(context.Background(), testChatID, 1)
	if err != nil || rec.Status != store.StatusSaved {
		t.Fatalf("guardar desde detalle: %+v, %v", rec, err)
	}
}

// Catches truncation when more than twenty saved records belong to the same month.
func TestMyInvoicesPagesReachEverySavedInvoiceInOneMonth(t *testing.T) {
	h := newHarness(t)
	for id := int64(1); id <= 23; id++ {
		h.saveInvoiceOf(id, "2026-09-10", fmt.Sprintf("001-001-%07d", id))
	}
	h.sendText("/facturas")
	first := h.telegram.lastSent(t)
	if !strings.Contains(first.text, "23 facturas guardadas") || !strings.Contains(first.markup, `"h:l:s:2026-09:1"`) {
		t.Fatalf("primera página: %+v", first)
	}
	h.pressRaw("h:l:s:2026-09:1")
	second := lastEdited(t, h)
	if !strings.Contains(second.markup, `"h:l:s:2026-09:2"`) {
		t.Fatalf("segunda página: %+v", second)
	}
	h.pressRaw("h:l:s:2026-09:2")
	third := lastEdited(t, h)
	if !strings.Contains(third.text, "0000001") || !strings.Contains(third.markup, `"h:o:s:2026-09:2:1"`) || strings.Contains(third.markup, `"h:l:s:2026-09:3"`) {
		t.Fatalf("última página: %+v", third)
	}
}

// Catches re-OCR or duplicate creation when correcting an already saved invoice.
func TestSavedDetailCorrectionReusesIDWithoutReadingAgain(t *testing.T) {
	h := newHarness(t)
	h.saveInvoiceOf(1, "2026-09-10", "001-001-0000001")
	h.pressRaw("h:o:s:2026-09:0:1")
	detail := lastEdited(t, h)
	if !strings.Contains(detail.markup, `"h:e:s:2026-09:0:1"`) || !strings.Contains(detail.markup, `"l:a:1:2026-09"`) {
		t.Fatalf("detalle guardado: %+v", detail)
	}
	h.pressRaw("h:e:s:2026-09:0:1")
	rec, _ := h.store.Get(context.Background(), testChatID, 1)
	if rec.Status != store.StatusDraft {
		t.Fatalf("debe volver a pendiente antes de corregir: %+v", rec)
	}
	h.press(callback{action: actionField, id: 1, field: invoice.FieldIssuerName})
	h.sendText("Proveedor corregido")
	h.press(callback{action: actionSave, id: 1})
	rec, _ = h.store.Get(context.Background(), testChatID, 1)
	saved, _ := h.store.SavedRecords(context.Background(), testChatID, "2026-09")
	// saveInvoiceOf reads the initial photo once; reopening/correcting must add no reads.
	if rec.Status != store.StatusSaved || rec.Invoice.IssuerName != "Proveedor corregido" || len(saved) != 1 || len(h.reader.received) != 1 {
		t.Fatalf("corrección no conserva registro: %+v, guardadas %d, lecturas %d", rec, len(saved), len(h.reader.received))
	}
}

// Catches a saved detail that treats the record itself as a second duplicate.
func TestSavedDetailDoesNotWarnThatItsOwnRecordIsDuplicated(t *testing.T) {
	h := newHarness(t)
	h.saveInvoiceOf(1, "2026-09-10", "001-001-0000001")
	h.pressRaw("h:o:s:2026-09:0:1")
	detail := lastEdited(t, h)
	if strings.Contains(detail.text, AlreadySavedNote) {
		t.Fatalf("detalle pide descartar su propio registro: %s", detail.text)
	}
}

// Catches a legacy explicit period command that unexpectedly opens all drafts.
func TestExplicitHistoryPeriodOpensSavedTabEvenWithDrafts(t *testing.T) {
	h := newHarness(t)
	h.saveInvoiceOf(1, "2026-08-10", "001-001-0000001")
	h.sendPhoto()
	h.sendText("/facturas 08/2026")
	list := h.telegram.lastSent(t)
	if !strings.Contains(list.markup, `"h:o:s:2026-08:0:1"`) || strings.Contains(list.markup, `"h:o:p:2026-08:0:2"`) {
		t.Fatalf("comando de período no abre guardadas: %+v", list)
	}
}

// Catches UTC defaults that select the wrong month before midnight in Paraguay.
func TestHistoryUsesAsuncionClockAndNavigatesPreviousPeriods(t *testing.T) {
	h := newHarness(t)
	h.handle = NewHandler(Deps{Logger: discardLogger(), Reader: h.reader, Store: h.store, Now: func() time.Time { return time.Date(2026, 10, 1, 1, 0, 0, 0, time.UTC) }})
	h.sendText("/facturas")
	first := h.telegram.lastSent(t)
	if !strings.Contains(first.text, "Septiembre 2026") || !strings.Contains(first.markup, `"h:l:s:2026-08:0"`) || !strings.Contains(first.markup, `"h:l:s:2026:0"`) {
		t.Fatalf("mes/navegación: %+v", first)
	}
	h.pressRaw("h:l:s:2025:0")
	year := lastEdited(t, h)
	if !strings.Contains(year.text, "2025") || !strings.Contains(year.markup, `"h:l:s:2024:0"`) {
		t.Fatalf("año anterior: %+v", year)
	}
}

// Catches authorization mistakes in new detail/correction callback branches.
func TestHistoryCannotOpenOrUnsaveAnotherChatsInvoice(t *testing.T) {
	h := newHarness(t)
	h.saveInvoiceOf(1, "2026-09-10", "001-001-0000001")
	before := len(h.telegram.byMethod("editMessageText"))
	for _, data := range []string{"h:o:s:2026-09:0:1", "h:e:s:2026-09:0:1"} {
		h.send(&models.Update{CallbackQuery: &models.CallbackQuery{ID: "other", From: models.User{ID: 999}, Data: data, Message: models.MaybeInaccessibleMessage{Type: models.MaybeInaccessibleMessageTypeMessage, Message: &models.Message{ID: 30, Chat: models.Chat{ID: 999, Type: models.ChatTypePrivate}}}}})
	}
	rec, _ := h.store.Get(context.Background(), testChatID, 1)
	if rec.Status != store.StatusSaved || len(h.telegram.byMethod("editMessageText")) != before {
		t.Fatalf("acceso cruzado: %+v", rec)
	}
}

// Catches invalid OCR strings that make the recoverable pending list unsendable.
func TestHistoryPendingPageFitsTelegramWithMalformedInvoiceFields(t *testing.T) {
	h := newHarness(t)
	for n := 0; n < 10; n++ {
		inv := sampleInvoice()
		inv.Date, inv.Number = strings.Repeat("🧾", 500), strings.Repeat("9", 1000)
		if _, err := h.store.CreateDraft(context.Background(), testChatID, store.Draft{Invoice: inv}); err != nil {
			t.Fatal(err)
		}
	}
	h.sendText("/facturas")
	text := h.telegram.lastSent(t).text
	if units := len(utf16.Encode([]rune(text))); units > 4096 {
		t.Fatalf("lista pendiente supera límite: %d unidades", units)
	}
}

func TestHistoryRejectsMalformedNavigationWithoutChangingInvoice(t *testing.T) {
	h := newHarness(t)
	h.saveInvoiceOf(1, "2026-09-10", "001-001-0000001")
	before := len(h.telegram.byMethod("editMessageText"))
	for _, data := range []string{
		"h:l:s:2026-13:0", "h:l:s:19999:0", "h:l:s:2028-00:0", "h:l:s:2026-09:-1", "h:l:s:2026-09:10000",
		"h:e:p:2026-09:0:1", "h:e:s:2026-09:0:0", "h:e:s:2026-09:0:-1", "h:o:s:2026-09:0:1:extra",
	} {
		h.pressRaw(data)
	}
	rec, _ := h.store.Get(context.Background(), testChatID, 1)
	if rec.Status != store.StatusSaved || len(h.telegram.byMethod("editMessageText")) != before {
		t.Fatalf("callback inválido cambió factura: %+v", rec)
	}
}

// A plausible OCR date error can still be calendar-valid. History must let its owner fix it.
func TestHistoryCanRecoverSavedInvoiceWithFarFutureDate(t *testing.T) {
	h := newHarness(t)
	h.saveInvoiceOf(1, "2030-09-10", "001-001-0000001")
	h.sendText("/facturas 2030")
	view := h.telegram.lastSent(t)
	if !strings.Contains(view.markup, "h:o:s:2030:0:1") {
		t.Fatalf("valid saved date became inaccessible: %+v", view)
	}
	h.pressRaw(callbackDataIn(t, view.markup, "h:o:"))
	detail := lastEdited(t, h)
	if !strings.Contains(detail.markup, "h:e:s:2030:0:1") {
		t.Fatal("stored invoice must remain correctable")
	}
}

func lastEdited(t *testing.T, h *harness) outgoing {
	t.Helper()
	edits := h.telegram.byMethod("editMessageText")
	if len(edits) == 0 {
		t.Fatal("no se editó el mensaje")
	}
	return edits[len(edits)-1]
}
