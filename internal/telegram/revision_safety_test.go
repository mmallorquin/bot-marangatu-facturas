package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"
	"github.com/mmallorquin/bot-marangatu-facturas/internal/invoice"
	"github.com/mmallorquin/bot-marangatu-facturas/internal/store"
)

// Use the callback actually shown, never derive its revision from the database.
func shownCallback(t *testing.T, markup, prefix string) string {
	t.Helper()
	var kb models.InlineKeyboardMarkup
	if err := json.Unmarshal([]byte(markup), &kb); err != nil {
		t.Fatal(err)
	}
	for _, row := range kb.InlineKeyboard {
		for _, b := range row {
			if b.CallbackData == prefix || strings.HasPrefix(b.CallbackData, prefix+":") {
				return b.CallbackData
			}
		}
	}
	t.Fatalf("callback %q missing in %s", prefix, markup)
	return ""
}

func TestOldMutationButtonsCannotChangeCurrentDraft(t *testing.T) {
	for _, action := range []string{actionDiscard, actionUseRUC, actionNoCDC, actionField, actionReread} {
		t.Run(action, func(t *testing.T) {
			h := newHarness(t)
			h.reader.result.Invoice.IssuerRUC = "80000519-1"
			h.reader.result.Invoice.CDC = "123"
			h.sendPhoto()
			if action == actionField {
				h.press(callback{action: actionEdit, id: 1})
			}
			c := callback{action: action, id: 1}
			if action == actionField {
				c.field = invoice.FieldTotal
			}
			data, ok := h.emittedCallback(c)
			if !ok {
				t.Fatal("button not emitted")
			}
			changed := sampleInvoice()
			changed.CDC = "456"
			if err := h.store.UpdateInvoice(context.Background(), testChatID, 1, changed); err != nil {
				t.Fatal(err)
			}
			before, _ := h.store.Get(context.Background(), testChatID, 1)
			h.pressRaw(data)
			after, _ := h.store.Get(context.Background(), testChatID, 1)
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("stale %s changed record", action)
			}
			if _, found, _ := h.store.Awaiting(context.Background(), testChatID); found {
				t.Fatal("stale field started correction")
			}
			if len(h.reader.received) != 1 {
				t.Fatal("stale reread called paid reader")
			}
		})
	}
}

func TestOldAdoptCannotApplyNewRereadCandidate(t *testing.T) {
	h := newHarness(t)
	h.sendPhoto()
	h.reader.result.Invoice.Number = "001-001-0001111"
	h.press(callback{action: actionReread, id: 1})
	old := shownCallback(t, h.telegram.lastSent(t).markup, "a:1:numero")
	h.reader.result.Invoice.Number = "001-001-0002222"
	h.press(callback{action: actionShow, id: 1})
	h.press(callback{action: actionReread, id: 1})
	h.pressRaw(old)
	rec, err := h.store.Get(context.Background(), testChatID, 1)
	if err != nil || rec.Invoice.Number != "001-001-0001234" || rec.Reread.Number != "001-001-0002222" {
		t.Fatalf("old button adopted new candidate: %+v %v", rec, err)
	}
}

func TestOldSavedMutationButtonsNeedNewConfirmation(t *testing.T) {
	for _, action := range []string{"undo", "history", "delete"} {
		for _, legacy := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/legacy=%v", action, legacy), func(t *testing.T) {
				h := newHarness(t)
				h.sendPhoto()
				h.press(callback{action: actionSave, id: 1})
				prefix := "u:1"
				markup := lastEdited(t, h).markup
				switch action {
				case "history":
					h.pressRaw("h:o:s:2026-09:0:1")
					prefix = "h:e:s:2026-09:0:1"
					markup = lastEdited(t, h).markup
				case "delete":
					h.pressRaw("l:a:1:2026-09")
					prefix = "l:s:1:2026-09"
					markup = h.telegram.lastSent(t).markup
				}
				old := shownCallback(t, markup, prefix)
				if legacy {
					old = prefix
				}
				if err := h.store.Unsave(context.Background(), testChatID, 1); err != nil {
					t.Fatal(err)
				}
				changed := sampleInvoice()
				changed.IssuerName = "Nuevo emisor"
				if err := h.store.UpdateInvoice(context.Background(), testChatID, 1, changed); err != nil {
					t.Fatal(err)
				}
				if err := h.store.Save(context.Background(), testChatID, 1); err != nil {
					t.Fatal(err)
				}
				before, _ := h.store.Get(context.Background(), testChatID, 1)
				h.pressRaw(old)
				after, _ := h.store.Get(context.Background(), testChatID, 1)
				if !reflect.DeepEqual(before, after) {
					t.Fatal("old saved action mutated current invoice")
				}
				if action == "delete" {
					fresh := shownCallback(t, h.telegram.lastSent(t).markup, prefix)
					h.pressRaw(fresh)
					after, _ = h.store.Get(context.Background(), testChatID, 1)
					if after.Status != store.StatusDeleted {
						t.Fatal("fresh delete failed")
					}
				}
			})
		}
	}
}

func TestFutureDateUsesParaguayDayAndAllowsToday(t *testing.T) {
	for _, date := range []string{"2026-09-24", "2026-09-25"} {
		t.Run(date, func(t *testing.T) {
			h := newHarness(t)
			h.handle = NewHandler(Deps{Logger: discardLogger(), Token: testToken, Store: h.store, Reader: h.reader, Now: func() time.Time { return time.Date(2026, 9, 25, 1, 0, 0, 0, time.UTC) }})
			h.reader.result.Invoice.Date = date
			if err := h.store.SetAutoSave(context.Background(), testChatID, true); err != nil {
				t.Fatal(err)
			}
			h.sendPhoto()
			rec, _ := h.store.Get(context.Background(), testChatID, 1)
			want := store.StatusSaved
			if date == "2026-09-25" {
				want = store.StatusDraft
			}
			if rec.Status != want {
				t.Fatalf("Paraguay date %s: %s want %s", date, rec.Status, want)
			}
			if want == store.StatusDraft && !strings.Contains(h.telegram.lastSent(t).text, "fecha futura") {
				t.Fatal("future warning missing")
			}
		})
	}
}

func TestStaleAndLegacySaveCannotSaveChangedInvoice(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{true: "legacy", false: "stale"}[legacy], func(t *testing.T) {
			h := newHarness(t)
			h.sendPhoto()
			data := shownCallback(t, h.telegram.lastSent(t).markup, "g:1")
			if legacy {
				data = "g:1"
			}
			inv := sampleInvoice()
			inv.IssuerName = "Emisor corregido"
			if err := h.store.UpdateInvoice(context.Background(), testChatID, 1, inv); err != nil {
				t.Fatal(err)
			}
			h.pressRaw(data)
			rec, err := h.store.Get(context.Background(), testChatID, 1)
			if err != nil || rec.Status != store.StatusDraft {
				t.Fatalf("old button saved unseen invoice: %+v %v", rec, err)
			}
			if !strings.Contains(h.telegram.lastSent(t).text, "Emisor corregido") {
				t.Fatal("missing current invoice")
			}
			h.pressRaw(shownCallback(t, h.telegram.lastSent(t).markup, "g:1"))
			rec, err = h.store.Get(context.Background(), testChatID, 1)
			if err != nil || rec.Status != store.StatusSaved {
				t.Fatalf("fresh confirmation failed: %v %v", rec.Status, err)
			}
		})
	}
}

func TestReviewRequiredBeforeAutomaticOrBulkSave(t *testing.T) {
	for _, tc := range []struct{ name, value string }{{"issuer", "   "}, {"future", "2026-09-26"}} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			if tc.name == "issuer" {
				h.reader.result.Invoice.IssuerName = tc.value
			} else {
				h.reader.result.Invoice.Date = tc.value
			}
			if err := h.store.SetAutoSave(context.Background(), testChatID, true); err != nil {
				t.Fatal(err)
			}
			h.sendPhoto()
			save := shownCallback(t, h.telegram.lastSent(t).markup, "g:1")
			rec, err := h.store.Get(context.Background(), testChatID, 1)
			if err != nil || rec.Status != store.StatusDraft {
				t.Fatalf("unsafe automatic save: %v %v", rec.Status, err)
			}
			h.sendText("/pendientes")
			if strings.Contains(h.telegram.lastSent(t).markup, "p:s:") {
				t.Fatal("unsafe bulk save offered")
			}
			h.pressRaw(save)
			rec, err = h.store.Get(context.Background(), testChatID, 1)
			want := store.StatusDraft
			if tc.name == "future" {
				want = store.StatusSaved
			}
			if err != nil || rec.Status != want {
				t.Fatalf("explicit save: got %v want %v (%v)", rec.Status, want, err)
			}
			if tc.name == "issuer" && len(invoice.Validate(rec.Invoice)) == 0 {
				t.Fatal("blank issuer accepted")
			}
		})
	}
}

func TestCancelCorrectionShowsInvoiceStillPending(t *testing.T) {
	h := newHarness(t)
	h.sendPhoto()
	h.pressRaw(shownCallback(t, h.telegram.lastSent(t).markup, "e:1"))
	edits := h.telegram.byMethod("editMessageReplyMarkup")
	h.pressRaw(shownCallback(t, edits[len(edits)-1].markup, "f:1:numero"))
	prompt := h.telegram.lastSent(t).text
	if !strings.Contains(prompt, "001-001-0001234") || !strings.Contains(prompt, "Comercial Ejemplo") {
		t.Fatalf("ambiguous correction: %s", prompt)
	}
	h.sendText("/cancelar")
	msg := h.telegram.lastSent(t)
	if !strings.Contains(msg.text, "pendiente") {
		t.Fatalf("missing final state: %s", msg.text)
	}
	save := shownCallback(t, msg.markup, "g:1")
	if _, found, err := h.store.Awaiting(context.Background(), testChatID); err != nil || found {
		t.Fatalf("correction still active: %v %v", found, err)
	}
	rec, err := h.store.Get(context.Background(), testChatID, 1)
	if err != nil || rec.Status != store.StatusDraft {
		t.Fatalf("cancel saved: %v %v", rec.Status, err)
	}
	h.pressRaw(save)
	rec, err = h.store.Get(context.Background(), testChatID, 1)
	if err != nil || rec.Status != store.StatusSaved {
		t.Fatalf("cannot continue: %v %v", rec.Status, err)
	}
}

func TestBackEndsCorrectionWithoutChangingInvoice(t *testing.T) {
	h := newHarness(t)
	h.sendPhoto()
	h.press(callback{action: actionField, id: 1, field: invoice.FieldIssuerName})
	h.press(callback{action: actionBack, id: 1})
	h.sendText("Esto ya no es una corrección")
	rec, _ := h.store.Get(context.Background(), testChatID, 1)
	if rec.Invoice.IssuerName != "Comercial Ejemplo S.A." {
		t.Fatal("Back left hidden correction active")
	}
	if _, found, _ := h.store.Awaiting(context.Background(), testChatID); found {
		t.Fatal("Back left awaiting active")
	}
}

func TestBulkSaveNeedsNewConfirmationAfterReread(t *testing.T) {
	h := newHarness(t)
	h.sendPhoto()
	h.sendText("/pendientes")
	old := callbackDataIn(t, h.telegram.lastSent(t).markup, "p:s:")
	candidate := sampleInvoice()
	candidate.Total = 170000
	if err := h.store.SaveReread(context.Background(), testChatID, 1, candidate); err != nil {
		t.Fatal(err)
	}
	h.pressRaw(old)
	rec, _ := h.store.Get(context.Background(), testChatID, 1)
	if rec.Status != store.StatusDraft {
		t.Fatal("old bulk save ignored newer reread")
	}
}

func TestSwitchCorrectionIdentifiesNewTarget(t *testing.T) {
	h := newHarness(t)
	h.sendPhoto()
	h.pressRaw(shownCallback(t, h.telegram.lastSent(t).markup, "e:1"))
	edits := h.telegram.byMethod("editMessageReplyMarkup")
	h.pressRaw(shownCallback(t, edits[len(edits)-1].markup, "f:1:numero"))
	h.reader.result.Invoice.Number = "001-001-0004321"
	h.sendPhoto()
	h.pressRaw(shownCallback(t, h.telegram.lastSent(t).markup, "e:2"))
	edits = h.telegram.byMethod("editMessageReplyMarkup")
	h.pressRaw(shownCallback(t, edits[len(edits)-1].markup, "f:2:numero"))
	text := h.telegram.lastSent(t).text
	if !strings.Contains(text, "001-001-0004321") || !strings.Contains(text, "anterior") {
		t.Fatalf("silent switch: %s", text)
	}
	h.sendText("001-001-0009999")
	a, _ := h.store.Get(context.Background(), testChatID, 1)
	b, _ := h.store.Get(context.Background(), testChatID, 2)
	if a.Invoice.Number != "001-001-0001234" || b.Invoice.Number != "001-001-0009999" {
		t.Fatalf("wrong correction target: %s %s", a.Invoice.Number, b.Invoice.Number)
	}
}
