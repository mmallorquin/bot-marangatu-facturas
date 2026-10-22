package telegram

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/mmallorquin/bot-marangatu-facturas/internal/invoice"
	"github.com/mmallorquin/bot-marangatu-facturas/internal/store"
)

// The candidate is part of the shown data: an older Adopt must not adopt a newer reread.
func invoiceRevision(rec store.Record) string {
	data, _ := json.Marshal(struct {
		Status  store.Status
		Invoice invoice.Invoice
		Reread  *invoice.Invoice
		Rereads int
	}{rec.Status, rec.Invoice, rec.Reread, rec.Rereads})
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum[:8])
}

func splitRevision(data string) (string, string) {
	at := strings.LastIndexByte(data, ':')
	if at < 0 || len(data[at+1:]) != 16 {
		return data, ""
	}
	if _, err := hex.DecodeString(data[at+1:]); err != nil {
		return data, ""
	}
	return data[:at], data[at+1:]
}

// Bind at the shared sending boundaries, including keyboards attached to the source photo.
func (h *handler) bindKeyboard(ctx context.Context, chatID int64, kb *models.InlineKeyboardMarkup) *models.InlineKeyboardMarkup {
	if kb == nil {
		return nil
	}
	out := &models.InlineKeyboardMarkup{}
	for _, row := range kb.InlineKeyboard {
		copied := append([]models.InlineKeyboardButton(nil), row...)
		for i := range copied {
			data, _ := splitRevision(copied[i].CallbackData)
			var id int64
			if c, err := parseCallback(data); err == nil && c.action != actionPhoto && c.action != actionShow {
				id = c.id
			} else if c, err := parseHistoryCallback(data, h.deps.Now()); err == nil && c.action == "e" {
				id = c.id
			} else if c, err := parseListCallback(data); err == nil && c.action == listActionConfirm {
				id = c.id
			}
			if id == 0 {
				continue
			}
			rec, err := h.deps.Store.Get(ctx, chatID, id)
			if err != nil {
				copied[i].CallbackData = "expired"
				continue
			}
			copied[i].CallbackData = data + ":" + invoiceRevision(rec)
		}
		out.InlineKeyboard = append(out.InlineKeyboard, copied)
	}
	return out
}

func (h *handler) refreshInvoice(ctx context.Context, b *bot.Bot, press buttonPress, rec store.Record) {
	if rec.Status != store.StatusDraft && rec.Status != store.StatusSaved {
		h.editKeyboard(ctx, b, press, noKeyboard())
		h.answer(ctx, b, press.queryID, NoLongerEditableAlert, false)
		return
	}
	text, _ := h.invoiceMessage(ctx, press.chatID, rec.Invoice)
	keyboard := reviewKeyboard(rec.ID, rec.Invoice, rec.Source.FileID != "")
	if rec.Status == store.StatusSaved {
		text = FormatInvoice(rec.Invoice, h.reviewIssues(rec.Invoice)) + "\n\n" + SavedNote
		keyboard = savedKeyboard(rec.ID, rec.Source.FileID != "")
	}
	h.sendReply(ctx, b, press.chatID, rec.Source.MessageID, text, keyboard)
	h.answer(ctx, b, press.queryID, "La factura cambió o el botón es antiguo. Revisá los datos y confirmá de nuevo.", true)
}

// A future date requires review, not a new fiscal prohibition on explicit saving.
func (h *handler) reviewIssues(inv invoice.Invoice) []invoice.Issue {
	issues := invoice.Validate(inv)
	if inv.Date > h.deps.Now().In(reminderLocation).Format("2006-01-02") {
		if _, err := time.Parse("2006-01-02", inv.Date); err == nil {
			issues = append(issues, invoice.Issue{Field: invoice.FieldDate, Message: "fecha futura: revisá la foto; si es correcta, podés guardar individualmente"})
		}
	}
	return issues
}
