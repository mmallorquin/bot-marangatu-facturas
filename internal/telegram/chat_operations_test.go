package telegram

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"

	"github.com/mmallorquin/bot-marangatu-facturas/internal/reader"
	"github.com/mmallorquin/bot-marangatu-facturas/internal/store"
)

// Without the private-chat gate these uploads create shared invoice records.
func TestUploadsOutsidePrivateChatDoNotReadOrPersist(t *testing.T) {
	for _, chatType := range []models.ChatType{"", models.ChatTypeGroup, models.ChatTypeSupergroup, models.ChatTypeChannel} {
		name := string(chatType)
		if name == "" {
			name = "missing"
		}
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			u := photoUpdate()
			u.Message.Chat.Type = chatType
			h.send(u)
			if len(h.reader.received) != 0 {
				t.Fatal("an upload outside a private chat reached the invoice reader")
			}
			drafts, err := h.store.Drafts(context.Background(), testChatID)
			if err != nil || len(drafts) != 0 {
				t.Fatalf("shared drafts persisted: %d, %v", len(drafts), err)
			}
			if h.metrics(t).Users != 0 {
				t.Fatal("rejected group upload created usage data")
			}
		})
	}
}

func TestPrivateCallbackRejectsAnotherUser(t *testing.T) {
	h := newHarness(t)
	h.sendPhoto()
	h.send(&models.Update{CallbackQuery: &models.CallbackQuery{
		ID: "foreign-user", From: models.User{ID: 999}, Data: "g:1",
		Message: models.MaybeInaccessibleMessage{Type: models.MaybeInaccessibleMessageTypeMessage,
			Message: &models.Message{ID: 10, Chat: models.Chat{ID: testChatID, Type: models.ChatTypePrivate}}},
	}})
	rec, err := h.store.Get(context.Background(), testChatID, 1)
	if err != nil || rec.Status != store.StatusDraft {
		t.Fatalf("another user saved a private invoice: %+v, %v", rec, err)
	}
}

// Even a provider ignoring cancellation must not recreate data after deletion.
type delayedReader struct {
	entered chan struct{}
	release chan struct{}
}

func (r *delayedReader) Read(context.Context, reader.Image) (reader.Result, error) {
	close(r.entered)
	<-r.release
	return reader.Result{Invoice: sampleInvoice()}, nil
}

func TestDeletingDataInvalidatesInFlightReadAndItsMetrics(t *testing.T) {
	h := newHarness(t)
	rdr := &delayedReader{entered: make(chan struct{}), release: make(chan struct{})}
	h.handle = NewHandler(Deps{Logger: discardLogger(), Store: h.store, Reader: rdr})
	done := make(chan struct{})
	go func() { h.sendPhoto(); close(done) }()
	<-rdr.entered
	h.sendText("/borrar_mis_datos")
	confirm := deleteButtonData(t, h.telegram.lastSent(t), "z:s")
	deleted := make(chan struct{})
	go func() { h.pressRaw(confirm); close(deleted) }()
	select {
	case <-deleted:
	case <-time.After(time.Second):
		close(rdr.release)
		<-done
		t.Fatal("deletion waited for the external invoice reader")
	}
	messages := len(h.telegram.byMethod("sendMessage"))
	close(rdr.release)
	<-done
	drafts, err := h.store.Drafts(context.Background(), testChatID)
	if err != nil || len(drafts) != 0 {
		t.Fatalf("read resurrected drafts after deletion: %d, %v", len(drafts), err)
	}
	if m := h.metrics(t); m.Users != 0 {
		t.Fatalf("read resurrected usage after deletion: %+v", m)
	}
	if len(h.telegram.byMethod("sendMessage")) != messages {
		t.Fatal("an obsolete read replied after deletion")
	}
}

func TestDeleteConfirmationIsSingleUseAndSupersededByNewQuestion(t *testing.T) {
	h := newHarness(t)
	h.sendPhoto()
	h.sendText("/borrar_mis_datos")
	old := deleteButtonData(t, h.telegram.lastSent(t), "z:s")
	h.sendText("/borrar_mis_datos")
	current := deleteButtonData(t, h.telegram.lastSent(t), "z:s")
	h.pressRaw(old)
	if drafts, _ := h.store.Drafts(context.Background(), testChatID); len(drafts) != 1 {
		t.Fatal("superseded confirmation deleted current data")
	}
	h.pressRaw(current)
	if drafts, _ := h.store.Drafts(context.Background(), testChatID); len(drafts) != 0 {
		t.Fatal("current confirmation did not delete data")
	}
	h.sendPhoto()
	h.pressRaw(current)
	if drafts, _ := h.store.Drafts(context.Background(), testChatID); len(drafts) != 1 {
		t.Fatal("replayed confirmation deleted new data")
	}
}

func TestCancelledDeleteConfirmationCannotBeUsedAgain(t *testing.T) {
	h := newHarness(t)
	h.sendPhoto()
	h.sendText("/borrar_mis_datos")
	question := h.telegram.lastSent(t)
	h.pressRaw(deleteButtonData(t, question, "z:n"))
	h.pressRaw(deleteButtonData(t, question, "z:s"))
	if drafts, _ := h.store.Drafts(context.Background(), testChatID); len(drafts) != 1 {
		t.Fatal("cancelled confirmation deleted data")
	}
}

func deleteButtonData(t *testing.T, sent outgoing, prefix string) string {
	t.Helper()
	var keyboard models.InlineKeyboardMarkup
	if err := json.Unmarshal([]byte(sent.markup), &keyboard); err != nil {
		t.Fatal(err)
	}
	for _, row := range keyboard.InlineKeyboard {
		for _, button := range row {
			if len(button.CallbackData) >= len(prefix) && button.CallbackData[:len(prefix)] == prefix {
				return button.CallbackData
			}
		}
	}
	t.Fatalf("missing button %s in %s", prefix, sent.markup)
	return ""
}

type concurrentReader struct {
	mu      sync.Mutex
	active  int
	peak    int
	entered chan struct{}
	release chan struct{}
}

func (r *concurrentReader) Read(ctx context.Context, _ reader.Image) (reader.Result, error) {
	r.mu.Lock()
	r.active++
	if r.active > r.peak {
		r.peak = r.active
	}
	r.mu.Unlock()
	r.entered <- struct{}{}
	select {
	case <-ctx.Done():
	case <-r.release:
	}
	r.mu.Lock()
	r.active--
	r.mu.Unlock()
	return reader.Result{Invoice: sampleInvoice()}, ctx.Err()
}

func TestInvoiceReadsHaveBoundedConcurrency(t *testing.T) {
	h := newHarness(t)
	rdr := &concurrentReader{entered: make(chan struct{}, 4), release: make(chan struct{})}
	h.handle = NewHandler(Deps{Logger: discardLogger(), Store: h.store, Reader: rdr})
	var completed sync.WaitGroup
	for range 4 {
		completed.Add(1)
		go func() { defer completed.Done(); h.sendPhoto() }()
	}
	<-rdr.entered
	<-rdr.entered
	select {
	case <-rdr.entered:
		close(rdr.release)
		completed.Wait()
		t.Fatal("more than two invoice reads ran concurrently")
	case <-time.After(100 * time.Millisecond):
	}
	close(rdr.release)
	completed.Wait()
	rdr.mu.Lock()
	defer rdr.mu.Unlock()
	if rdr.peak > 2 {
		t.Fatalf("peak concurrent readers = %d", rdr.peak)
	}
	if drafts, _ := h.store.Drafts(context.Background(), testChatID); len(drafts) != 4 {
		t.Fatalf("queued invoice reads were lost: %d", len(drafts))
	}
}

func TestManualSaveOffersUndo(t *testing.T) {
	h := newHarness(t)
	h.sendPhoto()
	h.press(callback{action: actionSave, id: 1})
	edits := h.telegram.byMethod("editMessageText")
	if len(edits) != 1 {
		t.Fatalf("save confirmation = %+v", edits)
	}
	_ = deleteButtonData(t, edits[0], "u:1")
	h.press(callback{action: actionUndo, id: 1})
	if rec, _ := h.store.Get(context.Background(), testChatID, 1); rec.Status != store.StatusDraft {
		t.Fatal("manual save could not be undone")
	}
}
