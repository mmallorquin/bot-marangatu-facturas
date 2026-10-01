package telegram

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/mmallorquin/bot-marangatu-facturas/internal/marangatu"
	"github.com/mmallorquin/bot-marangatu-facturas/internal/store"
)

// /facturas lista las facturas guardadas y permite borrar las que se guardaron por error.
const (
	listCommand = "/facturas"

	listCallbackPrefix = "l"
	listActionAsk      = "a" // pedir confirmación
	listActionConfirm  = "s" // sí, borrar
	listActionCancel   = "n" // no borrar

	listLimit = 10
)

// listCallback es un botón de /facturas: l:<acción>:<id>:<período>.
type listCallback struct {
	action string
	id     int64
	period string // para volver a mostrar la lista después de borrar
}

func (c listCallback) encode() string {
	return fmt.Sprintf("%s:%s:%d:%s", listCallbackPrefix, c.action, c.id, c.period)
}

func parseListCallback(data string) (listCallback, error) {
	parts := strings.Split(data, ":")
	if len(parts) != 4 || parts[0] != listCallbackPrefix ||
		!slices.Contains([]string{listActionAsk, listActionConfirm, listActionCancel}, parts[1]) {
		return listCallback{}, errInvalidCallback
	}
	id, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return listCallback{}, errInvalidCallback
	}
	if _, err := marangatu.ParsePeriod(parts[3]); err != nil {
		return listCallback{}, errInvalidCallback
	}
	return listCallback{action: parts[1], id: id, period: parts[3]}, nil
}

func (h *handler) listInvoices(ctx context.Context, b *bot.Bot, chatID int64, text string) {
	period, err := parsePeriod(text, h.deps.Now().In(reminderLocation))
	if err != nil {
		h.send(ctx, b, chatID, "❌ "+err.Error(), nil)
		return
	}
	view := historyCallback{action: "l", period: period}
	if len(strings.Fields(text)) > 1 {
		view.tab = "s"
	}
	message, keyboard, err := h.historyList(ctx, chatID, view)
	if err != nil {
		h.logger.Error("no se pudieron listar las facturas", "chat_id", chatID, "error", err)
		h.send(ctx, b, chatID, StoreErrorMessage, nil)
		return
	}
	h.send(ctx, b, chatID, message, keyboard)
}

// invoiceList conserva el retorno a las guardadas después del borrado.
func (h *handler) invoiceList(ctx context.Context, chatID int64, period string) (string, *models.InlineKeyboardMarkup, error) {
	return h.historyList(ctx, chatID, historyCallback{action: "l", tab: "s", period: period})
}

func (h *handler) handleListCallback(ctx context.Context, b *bot.Bot, query *models.CallbackQuery, msg *models.Message) {
	c, err := parseListCallback(query.Data)
	if err != nil {
		h.answer(ctx, b, query.ID, NoLongerEditableAlert, false)
		return
	}
	press := buttonPress{queryID: query.ID, chatID: msg.Chat.ID, messageID: msg.ID}

	switch c.action {
	case listActionAsk:
		h.askDelete(ctx, b, press, c)
	case listActionConfirm:
		h.confirmDelete(ctx, b, press, c)
	case listActionCancel:
		h.editText(ctx, b, press, "Listo, no borré nada.", noKeyboard())
		h.answer(ctx, b, press.queryID, "", false)
	}
}

func (h *handler) askDelete(ctx context.Context, b *bot.Bot, press buttonPress, c listCallback) {
	rec, err := h.deps.Store.Get(ctx, press.chatID, c.id)
	if err != nil || rec.Status != store.StatusSaved {
		h.answer(ctx, b, press.queryID, NotSavedAnymoreAlert, true)
		return
	}
	inv := rec.Invoice
	question := fmt.Sprintf("🗑️ ¿Borrar esta factura?\n\n%s · %s · %s · %s Gs\n\nDeja de aparecer en /resumen y en /exportar.",
		displayDate(inv.Date), previewIssuer(inv.IssuerName), inv.Number, formatGs(inv.Total))
	keyboard := &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{
		{Text: "Sí, borrar", CallbackData: listCallback{action: listActionConfirm, id: c.id, period: c.period}.encode()},
		{Text: "No", CallbackData: listCallback{action: listActionCancel, id: c.id, period: c.period}.encode()},
	}}}
	h.send(ctx, b, press.chatID, question, keyboard)
	h.answer(ctx, b, press.queryID, "", false)
}

func (h *handler) confirmDelete(ctx context.Context, b *bot.Bot, press buttonPress, c listCallback) {
	err := h.deps.Store.DeleteSaved(ctx, press.chatID, c.id)
	switch {
	case errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrNotSaved):
		h.editText(ctx, b, press, NotSavedAnymoreAlert, noKeyboard())
		h.answer(ctx, b, press.queryID, "", false)
		return
	case err != nil:
		h.logger.Error("no se pudo borrar la factura", "chat_id", press.chatID, "error", err)
		h.answer(ctx, b, press.queryID, StoreErrorMessage, true)
		return
	}
	h.track(ctx, press.chatID, store.Event{Kind: store.EventDeleted})

	// El mensaje de confirmación pasa a ser la lista actualizada.
	list, keyboard, err := h.invoiceList(ctx, press.chatID, c.period)
	if err != nil {
		list, keyboard = DeletedMessage, noKeyboard()
	} else {
		list = DeletedMessage + "\n\n" + list
	}
	if keyboard == nil {
		keyboard = noKeyboard()
	}
	h.editText(ctx, b, press, list, keyboard)
	h.answer(ctx, b, press.queryID, "Borrada", false)
}
