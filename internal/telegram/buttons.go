package telegram

import (
	"context"
	"errors"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/mmallorquin/bot-marangatu-facturas/internal/invoice"
	"github.com/mmallorquin/bot-marangatu-facturas/internal/store"
)

// buttonPress es un botón apretado sobre el mensaje de una factura.
type buttonPress struct {
	queryID   string
	chatID    int64
	messageID int
	callback  callback
}

// handleCallback responde a los botones ✅ ✏️ 🗑️ y a los de cada campo.
func (h *handler) handleCallback(ctx context.Context, b *bot.Bot, query *models.CallbackQuery) {
	msg := query.Message.Message
	if msg == nil { // mensaje muy viejo o inaccesible
		h.answer(ctx, b, query.ID, NoLongerEditableAlert, false)
		return
	}
	c, err := parseCallback(query.Data)
	if err != nil {
		h.answer(ctx, b, query.ID, NoLongerEditableAlert, false)
		return
	}
	press := buttonPress{queryID: query.ID, chatID: msg.Chat.ID, messageID: msg.ID, callback: c}

	rec, err := h.deps.Store.Get(ctx, press.chatID, c.id)
	if errors.Is(err, store.ErrNotFound) || (err == nil && rec.Status != store.StatusDraft) {
		h.answer(ctx, b, press.queryID, NoLongerEditableAlert, false)
		h.editKeyboard(ctx, b, press, noKeyboard())
		return
	}
	if err != nil {
		h.logger.Error("no se pudo leer la factura", "chat_id", press.chatID, "error", err)
		h.answer(ctx, b, press.queryID, StoreErrorMessage, true)
		return
	}

	switch c.action {
	case actionSave:
		h.saveInvoice(ctx, b, press, rec)
	case actionDiscard:
		h.discardInvoice(ctx, b, press)
	case actionEdit:
		h.editKeyboard(ctx, b, press, fieldsKeyboard(c.id))
		h.answer(ctx, b, press.queryID, "", false)
	case actionBack:
		h.editKeyboard(ctx, b, press, mainKeyboard(c.id))
		h.answer(ctx, b, press.queryID, "", false)
	case actionField:
		h.askForValue(ctx, b, press)
	}
}

func (h *handler) saveInvoice(ctx context.Context, b *bot.Bot, press buttonPress, rec store.Record) {
	if issues := invoice.Validate(rec.Invoice); len(issues) > 0 {
		h.answer(ctx, b, press.queryID, FixBeforeSavingAlert, true)
		return
	}

	err := h.deps.Store.Save(ctx, press.chatID, rec.ID)
	switch {
	case errors.Is(err, store.ErrDuplicate):
		h.editText(ctx, b, press, FormatInvoice(rec.Invoice, nil)+"\n\n"+DuplicateNote, discardOnlyKeyboard(rec.ID))
		h.answer(ctx, b, press.queryID, "", false)
	case err != nil:
		h.logger.Error("no se pudo guardar la factura", "chat_id", press.chatID, "error", err)
		h.answer(ctx, b, press.queryID, StoreErrorMessage, true)
	default:
		h.logger.Info("factura guardada", "chat_id", press.chatID, "id", rec.ID, "corregida", rec.Corrected)
		h.editText(ctx, b, press, FormatInvoice(rec.Invoice, nil)+"\n\n"+SavedNote, noKeyboard())
		h.answer(ctx, b, press.queryID, SavedAnswer, false)
	}
}

func (h *handler) discardInvoice(ctx context.Context, b *bot.Bot, press buttonPress) {
	if err := h.deps.Store.Discard(ctx, press.chatID, press.callback.id); err != nil {
		h.logger.Error("no se pudo descartar la factura", "chat_id", press.chatID, "error", err)
		h.answer(ctx, b, press.queryID, StoreErrorMessage, true)
		return
	}
	h.editText(ctx, b, press, DiscardedMessage, noKeyboard())
	h.answer(ctx, b, press.queryID, "", false)
}

func (h *handler) askForValue(ctx context.Context, b *bot.Bot, press buttonPress) {
	field := press.callback.field
	if err := h.deps.Store.SetAwaiting(ctx, press.chatID, press.callback.id, field); err != nil {
		h.logger.Error("no se pudo iniciar la corrección", "chat_id", press.chatID, "error", err)
		h.answer(ctx, b, press.queryID, StoreErrorMessage, true)
		return
	}
	h.send(ctx, b, press.chatID, askValueMessage(field), nil)
	h.answer(ctx, b, press.queryID, "", false)
}

// answer cierra el "cargando" del botón; con alert muestra una ventana en vez de un aviso corto.
func (h *handler) answer(ctx context.Context, b *bot.Bot, queryID, text string, alert bool) {
	_, err := b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{CallbackQueryID: queryID, Text: text, ShowAlert: alert})
	if err != nil {
		h.logger.Error("no se pudo responder al botón", "error", Redact(err, h.deps.Token))
	}
}

func (h *handler) editText(ctx context.Context, b *bot.Bot, press buttonPress, text string, keyboard *models.InlineKeyboardMarkup) {
	_, err := b.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID: press.chatID, MessageID: press.messageID, Text: text, ReplyMarkup: keyboard,
	})
	if err != nil {
		h.logger.Error("no se pudo editar el mensaje", "chat_id", press.chatID, "error", Redact(err, h.deps.Token))
	}
}

func (h *handler) editKeyboard(ctx context.Context, b *bot.Bot, press buttonPress, keyboard *models.InlineKeyboardMarkup) {
	_, err := b.EditMessageReplyMarkup(ctx, &bot.EditMessageReplyMarkupParams{
		ChatID: press.chatID, MessageID: press.messageID, ReplyMarkup: keyboard,
	})
	if err != nil {
		h.logger.Error("no se pudo cambiar los botones", "chat_id", press.chatID, "error", Redact(err, h.deps.Token))
	}
}
