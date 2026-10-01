package telegram

import (
	"context"
	"errors"
	"strings"

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
	if strings.HasPrefix(query.Data, "ep:") {
		h.handleExportFlowCallback(ctx, b, query, msg)
		return
	}
	if strings.HasPrefix(query.Data, "tp:") {
		h.handlePresentationCallback(ctx, b, query, msg)
		return
	}
	if strings.HasPrefix(query.Data, historyCallbackPrefix+":") {
		h.handleHistoryCallback(ctx, b, query, msg)
		return
	}
	if strings.HasPrefix(query.Data, settingsCallbackPrefix+":") {
		h.handleSettingsCallback(ctx, b, query, msg)
		return
	}
	if strings.HasPrefix(query.Data, exportCallbackPrefix+":") {
		h.handleExportCallback(ctx, b, query, msg)
		return
	}
	if strings.HasPrefix(query.Data, listCallbackPrefix+":") {
		h.handleListCallback(ctx, b, query, msg)
		return
	}
	if strings.HasPrefix(query.Data, imputeCallbackPrefix+":") {
		h.handleImputeCallback(ctx, b, query, msg)
		return
	}
	if strings.HasPrefix(query.Data, registrationCallbackPrefix+":") {
		h.handleRegistrationCallback(ctx, b, query, msg)
		return
	}
	if strings.HasPrefix(query.Data, "z:") {
		h.handleDeleteData(ctx, b, buttonPress{queryID: query.ID, chatID: msg.Chat.ID, messageID: msg.ID}, query.Data)
		return
	}
	if strings.HasPrefix(query.Data, pendingSaveCallback) {
		h.handlePendingCallback(ctx, b, query, msg)
		return
	}
	c, err := parseCallback(query.Data)
	if err != nil {
		h.answer(ctx, b, query.ID, NoLongerEditableAlert, false)
		return
	}
	press := buttonPress{queryID: query.ID, chatID: msg.Chat.ID, messageID: msg.ID, callback: c}
	if c.action == actionUndo {
		h.undoAutoSave(ctx, b, press)
		return
	}

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
		h.track(ctx, press.chatID, store.Event{Kind: store.EventSaveBlocked})
		h.answer(ctx, b, press.queryID, FixBeforeSavingAlert, true)
		return
	}

	err := h.deps.Store.Save(ctx, press.chatID, rec.ID)
	switch {
	case errors.Is(err, store.ErrInvalidInvoice):
		h.track(ctx, press.chatID, store.Event{Kind: store.EventSaveBlocked})
		h.answer(ctx, b, press.queryID, FixBeforeSavingAlert, true)
	case errors.Is(err, store.ErrDuplicate):
		h.track(ctx, press.chatID, store.Event{Kind: store.EventDuplicate})
		h.editText(ctx, b, press, FormatInvoice(rec.Invoice, nil)+"\n\n"+DuplicateNote, discardOnlyKeyboard(rec.ID))
		h.answer(ctx, b, press.queryID, "", false)
	case err != nil:
		h.logger.Error("no se pudo guardar la factura", "chat_id", press.chatID, "error", err)
		h.answer(ctx, b, press.queryID, StoreErrorMessage, true)
	default:
		h.logger.Info("factura guardada", "chat_id", press.chatID, "id", rec.ID, "corregida", rec.Corrected)
		h.track(ctx, press.chatID, store.Event{Kind: store.EventSaved, Detail: savedDetail(rec.Corrected)})
		h.editText(ctx, b, press, FormatInvoice(rec.Invoice, nil)+"\n\n"+SavedNote, undoKeyboard(rec.ID))
		h.answer(ctx, b, press.queryID, SavedAnswer, false)
	}
}

// undoAutoSave vuelve a borrador una factura guardada y muestra los botones de siempre.
func (h *handler) undoAutoSave(ctx context.Context, b *bot.Bot, press buttonPress) {
	err := h.deps.Store.Unsave(ctx, press.chatID, press.callback.id)
	if err != nil {
		if !errors.Is(err, store.ErrNotSaved) {
			h.logger.Error("no se pudo deshacer el guardado", "chat_id", press.chatID, "error", err)
		}
		h.answer(ctx, b, press.queryID, NotSavedAnymoreAlert, false)
		h.editKeyboard(ctx, b, press, noKeyboard())
		return
	}
	h.track(ctx, press.chatID, store.Event{Kind: store.EventUndo})
	rec, err := h.deps.Store.Get(ctx, press.chatID, press.callback.id)
	if err != nil {
		h.answer(ctx, b, press.queryID, StoreErrorMessage, true)
		return
	}
	h.editText(ctx, b, press, FormatInvoice(rec.Invoice, invoice.Validate(rec.Invoice))+"\n\n"+UndoneNote, mainKeyboard(rec.ID))
	h.answer(ctx, b, press.queryID, "", false)
}

// savedDetail es el detalle del evento de guardado: si el usuario corrigió algo antes.
func savedDetail(corrected bool) string {
	if corrected {
		return store.EventDetailCorrected
	}
	return store.EventDetailClean
}

func (h *handler) discardInvoice(ctx context.Context, b *bot.Bot, press buttonPress) {
	if err := h.deps.Store.Discard(ctx, press.chatID, press.callback.id); err != nil {
		h.logger.Error("no se pudo descartar la factura", "chat_id", press.chatID, "error", err)
		h.answer(ctx, b, press.queryID, StoreErrorMessage, true)
		return
	}
	h.track(ctx, press.chatID, store.Event{Kind: store.EventDiscarded})
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
		ChatID: press.chatID, MessageID: press.messageID, Text: telegramMessageText(text), ReplyMarkup: keyboard,
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
