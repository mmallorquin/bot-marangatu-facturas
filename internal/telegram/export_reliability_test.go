package telegram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/mmallorquin/bot-marangatu-facturas/internal/store"
)

type cancelAfterACKClient struct {
	method string
	cancel context.CancelFunc
}

func (c cancelAfterACKClient) Do(req *http.Request) (*http.Response, error) {
	response, err := http.DefaultClient.Do(req)
	if err == nil && strings.HasSuffix(req.URL.Path, "/"+c.method) {
		// The Telegram client closes the response after decoding its success.
		response.Body = &cancelAfterACKBody{ReadCloser: response.Body, cancel: c.cancel}
	}
	return response, err
}

type cancelAfterACKBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *cancelAfterACKBody) Close() error {
	err := b.ReadCloser.Close()
	b.cancel()
	return err
}

func cancelContextAfterTelegramACK(t *testing.T, h *harness, method string, cancel context.CancelFunc) {
	t.Helper()
	server := httptest.NewServer(h.telegram)
	t.Cleanup(server.Close)
	b, err := bot.New(testToken, bot.WithServerURL(server.URL), bot.WithSkipGetMe(),
		bot.WithHTTPClient(time.Minute, cancelAfterACKClient{method: method, cancel: cancel}))
	if err != nil {
		t.Fatal(err)
	}
	h.bot = b
}

// failTelegramMethod replaces only the external transport: handlers and SQLite remain real.
func failTelegramMethod(t *testing.T, h *harness, method string, fail *atomic.Bool) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/"+method) && fail.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"ok":false,"error_code":503,"description":"temporarily unavailable"}`))
			return
		}
		h.telegram.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	b, err := bot.New(testToken, bot.WithServerURL(server.URL), bot.WithSkipGetMe())
	if err != nil {
		t.Fatal(err)
	}
	h.bot = b
}

func setupReliableExport(t *testing.T, h *harness) {
	t.Helper()
	h.saveOneInvoice(t)
	h.sendText("/ruc 80024627-6")
	h.sendText("/imputar iva")
	h.sendText("/registro 955")
	h.sendText("/exportar 09/2026")
}

func callbackDataIn(t *testing.T, markup, prefix string) string {
	t.Helper()
	var keyboard models.InlineKeyboardMarkup
	if err := json.Unmarshal([]byte(markup), &keyboard); err != nil {
		t.Fatal(err)
	}
	for _, row := range keyboard.InlineKeyboard {
		for _, button := range row {
			if strings.HasPrefix(button.CallbackData, prefix) {
				return button.CallbackData
			}
		}
	}
	t.Fatalf("no callback %q in %s", prefix, markup)
	return ""
}

func TestZIPConfirmationDeliversOnceEvenWhenPressedConcurrently(t *testing.T) {
	h := newHarness(t)
	setupReliableExport(t, h)
	data := callbackDataIn(t, h.telegram.lastSent(t).markup, "x:z:")
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); h.pressRaw(data) }()
	}
	wg.Wait()
	if docs := h.telegram.byMethod("sendDocument"); len(docs) != 1 || docs[0].fileName != "80024627_REG_092026_V0001.zip" {
		t.Fatalf("a single confirmation must deliver one V0001: %+v", docs)
	}
	h.pressRaw(data)
	if docs := h.telegram.byMethod("sendDocument"); len(docs) != 1 {
		t.Fatalf("replaying a delivered confirmation sent %d documents", len(docs))
	}
}

func TestFailedZIPRemainsReminderEligibleAndRetriesSameVersion(t *testing.T) {
	h := newHarness(t)
	setupReliableExport(t, h)
	data := callbackDataIn(t, h.telegram.lastSent(t).markup, "x:z:")
	var fail atomic.Bool
	fail.Store(true)
	failTelegramMethod(t, h, "sendDocument", &fail)
	h.pressRaw(data)
	candidates, err := h.store.ReminderCandidates(context.Background(), "2026-09")
	if err != nil || len(candidates) != 1 {
		t.Fatalf("an undelivered ZIP must remain eligible: %+v, %v", candidates, err)
	}
	fail.Store(false)
	h.pressRaw(data)
	docs := h.telegram.byMethod("sendDocument")
	if len(docs) != 1 || docs[0].fileName != "80024627_REG_092026_V0001.zip" {
		t.Fatalf("retry must reuse the reserved version: %+v", docs)
	}
	if candidates, err := h.store.ReminderCandidates(context.Background(), "2026-09"); err != nil || len(candidates) != 0 {
		t.Fatalf("confirmed delivery must suppress the reminder: %+v, %v", candidates, err)
	}
}

func TestCancelledZIPConfirmationCannotBeReplayed(t *testing.T) {
	h := newHarness(t)
	setupReliableExport(t, h)
	markup := h.telegram.lastSent(t).markup
	h.pressRaw(callbackDataIn(t, markup, "x:n:"))
	h.pressRaw(callbackDataIn(t, markup, "x:z:"))
	if docs := h.telegram.byMethod("sendDocument"); len(docs) != 0 {
		t.Fatalf("cancelled confirmation delivered %d documents", len(docs))
	}
}

func TestCancelledExportCannotSendReviewFiles(t *testing.T) {
	for _, prefix := range []string{"x:c:", "x:e:"} {
		t.Run(prefix, func(t *testing.T) {
			h := newHarness(t)
			setupReliableExport(t, h)
			markup := h.telegram.lastSent(t).markup
			h.pressRaw(callbackDataIn(t, markup, "x:n:"))
			h.pressRaw(callbackDataIn(t, markup, prefix))
			if docs := h.telegram.byMethod("sendDocument"); len(docs) != 0 {
				t.Fatalf("cancelled request sent %d review files", len(docs))
			}
		})
	}
}

func TestCancelledExportCannotRefreshAfterItsDataChanges(t *testing.T) {
	h := newHarness(t)
	setupReliableExport(t, h)
	markup := h.telegram.lastSent(t).markup
	h.pressRaw(callbackDataIn(t, markup, "x:n:"))
	before := len(h.telegram.byMethod("editMessageText"))
	h.sendText("/imputar iva irp")
	h.pressRaw(callbackDataIn(t, markup, "x:z:"))
	if after := len(h.telegram.byMethod("editMessageText")); after != before {
		t.Fatalf("cancelled preview was revived after its data changed: %d new edits", after-before)
	}
}

func TestDeletedExportCannotSendReviewAfterIdenticalDataIsRecreated(t *testing.T) {
	h := newHarness(t)
	setupReliableExport(t, h)
	data := callbackDataIn(t, h.telegram.lastSent(t).markup, "x:c:")
	if _, err := h.store.DeleteChat(context.Background(), testChatID); err != nil {
		t.Fatal(err)
	}
	h.saveInvoiceOf(2, "2026-09-20", "001-001-0001234")
	h.sendText("/ruc 80024627-6")
	h.sendText("/imputar iva")
	h.sendText("/registro 955")
	h.pressRaw(data)
	if docs := h.telegram.byMethod("sendDocument"); len(docs) != 0 {
		t.Fatalf("pre-deletion preview sent %d review files after its data was recreated", len(docs))
	}
}

func TestLegacyExportCannotCreateANewConfirmation(t *testing.T) {
	h := newHarness(t)
	setupReliableExport(t, h)
	before := len(h.telegram.byMethod("editMessageText"))
	h.pressRaw("x:z:2026-09")
	if docs := h.telegram.byMethod("sendDocument"); len(docs) != 0 {
		t.Fatalf("legacy callback sent %d files", len(docs))
	}
	if after := len(h.telegram.byMethod("editMessageText")); after != before {
		t.Fatalf("legacy callback created a new confirmation: %d new edits", after-before)
	}
}

func TestPendingConfirmationDoesNotSaveLaterArrivals(t *testing.T) {
	h := newHarness(t)
	h.sendPhoto()
	h.sendText("/pendientes")
	data := callbackDataIn(t, h.telegram.lastSent(t).markup, pendingSaveCallback)
	inv := sampleInvoice()
	inv.Number = "001-001-0009999"
	h.reader.result.Invoice = inv
	h.sendPhoto()
	h.pressRaw(data)
	later, err := h.store.Get(context.Background(), testChatID, 2)
	if err != nil || later.Status != store.StatusDraft {
		t.Fatalf("a later invoice must not be covered by the old confirmation: %+v, %v", later, err)
	}
}

func TestPendingDoesNotOfferToSaveAnAlreadySavedInvoice(t *testing.T) {
	h := newHarness(t)
	h.saveOneInvoice(t)
	h.sendPhoto()
	h.sendText("/pendientes")
	view := h.telegram.lastSent(t)
	if strings.Contains(view.markup, pendingSaveCallback) || !strings.Contains(view.text, "duplicada") || strings.Contains(view.text, "Corregilas") {
		t.Fatalf("saved duplicates must be explained, not counted as ready: %+v", view)
	}
}

func TestFailedReminderIsRetriedUntilTelegramConfirmsDelivery(t *testing.T) {
	h := newHarness(t)
	h.saveOneInvoice(t)
	h.sendText("/ruc 80024627-6")
	h.sendText("/registro 955")
	var fail atomic.Bool
	fail.Store(true)
	failTelegramMethod(t, h, "sendMessage", &fail)
	handler := newHandler(Deps{Logger: discardLogger(), Store: h.store})
	handler.sendDueReminders(context.Background(), h.bot, asuncion(2026, time.October, 3, 10))
	if candidates, err := h.store.ReminderCandidates(context.Background(), "2026-09"); err != nil || len(candidates) != 1 {
		t.Fatalf("failed notification must remain eligible: %+v, %v", candidates, err)
	}
	fail.Store(false)
	handler.sendDueReminders(context.Background(), h.bot, asuncion(2026, time.October, 3, 11))
	if candidates, err := h.store.ReminderCandidates(context.Background(), "2026-09"); err != nil || len(candidates) != 0 {
		t.Fatalf("delivered notification must be recorded: %+v, %v", candidates, err)
	}
}

func TestZIPDeliveryIsRecordedWhenCallerCancelsAfterTelegramACK(t *testing.T) {
	h := newHarness(t)
	setupReliableExport(t, h)
	data := callbackDataIn(t, h.telegram.lastSent(t).markup, "x:z:")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cancelContextAfterTelegramACK(t, h, "sendDocument", cancel)
	h.handle(ctx, h.bot, &models.Update{CallbackQuery: &models.CallbackQuery{
		ID: "cancel-after-ack", Data: data, From: models.User{ID: testChatID},
		Message: models.MaybeInaccessibleMessage{Type: models.MaybeInaccessibleMessageTypeMessage,
			Message: &models.Message{ID: 20, Chat: models.Chat{ID: testChatID, Type: models.ChatTypePrivate}}},
	}})
	if ctx.Err() != context.Canceled {
		t.Fatal("fixture did not cancel the callback context after the acknowledgement")
	}
	h.pressRaw(data)
	if docs := h.telegram.byMethod("sendDocument"); len(docs) != 1 {
		t.Fatalf("acknowledged ZIP must not be replayed after caller cancellation: delivered %d documents", len(docs))
	}
	if candidates, err := h.store.ReminderCandidates(context.Background(), "2026-09"); err != nil || len(candidates) != 0 {
		t.Fatalf("acknowledged delivery must suppress reminders: %+v, %v", candidates, err)
	}
}

func TestReminderDeliveryIsRecordedWhenCallerCancelsAfterTelegramACK(t *testing.T) {
	h := newHarness(t)
	h.saveOneInvoice(t)
	h.sendText("/ruc 80024627-6")
	h.sendText("/registro 955")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cancelContextAfterTelegramACK(t, h, "sendMessage", cancel)
	handler := newHandler(Deps{Logger: discardLogger(), Store: h.store})
	handler.sendDueReminders(ctx, h.bot, asuncion(2026, time.October, 3, 10))
	if ctx.Err() != context.Canceled {
		t.Fatal("fixture did not cancel the reminder context after the acknowledgement")
	}
	handler.sendDueReminders(context.Background(), h.bot, asuncion(2026, time.October, 3, 11))
	reminders := 0
	for _, sent := range h.telegram.byMethod("sendMessage") {
		if strings.HasPrefix(sent.text, "📅") {
			reminders++
		}
	}
	if reminders != 1 {
		t.Fatalf("acknowledged reminder must not repeat after caller cancellation: delivered %d", reminders)
	}
}
