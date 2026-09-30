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

	listLimit          = 20
	deleteButtonsInRow = 4
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
	period, err := parsePeriod(text, h.deps.Now())
	if err != nil {
		h.send(ctx, b, chatID, "❌ "+err.Error(), nil)
		return
	}
	message, keyboard, err := h.invoiceList(ctx, chatID, period)
	if err != nil {
		h.logger.Error("no se pudieron listar las facturas", "chat_id", chatID, "error", err)
		h.send(ctx, b, chatID, StoreErrorMessage, nil)
		return
	}
	h.send(ctx, b, chatID, message, keyboard)
}

// invoiceList arma el mensaje con las facturas guardadas del período, las más recientes primero.
func (h *handler) invoiceList(ctx context.Context, chatID int64, period string) (string, *models.InlineKeyboardMarkup, error) {
	saved, err := h.deps.Store.SavedRecords(ctx, chatID, period)
	if err != nil {
		return "", nil, err
	}
	if len(saved) == 0 {
		return fmt.Sprintf("📂 No hay facturas guardadas de %s.", periodTitle(period)), nil, nil
	}
	slices.Reverse(saved)
	shown := saved[:min(len(saved), listLimit)]

	var text strings.Builder
	noun := "facturas guardadas"
	if len(saved) == 1 {
		noun = "factura guardada"
	}
	fmt.Fprintf(&text, "📂 %s: %d %s\n", periodTitle(period), len(saved), noun)
	var buttons []models.InlineKeyboardButton
	for i, rec := range shown {
		inv := rec.Invoice
		fmt.Fprintf(&text, "\n%d. %s · %s · %s · %s Gs",
			i+1, displayDate(inv.Date), previewIssuer(inv.IssuerName), inv.Number, formatGs(inv.Total))
		buttons = append(buttons, models.InlineKeyboardButton{
			Text:         fmt.Sprintf("🗑️ %d", i+1),
			CallbackData: listCallback{action: listActionAsk, id: rec.ID, period: period}.encode(),
		})
	}
	if hidden := len(saved) - len(shown); hidden > 0 {
		fmt.Fprintf(&text, "\n\n… y %d más antiguas. Para verlas, pedí un mes: /facturas 09/2026", hidden)
	}
	text.WriteString("\n\nPara sacar una factura guardada por error, tocá 🗑️ con su número.")
	return text.String(), &models.InlineKeyboardMarkup{InlineKeyboard: slices.Collect(slices.Chunk(buttons, deleteButtonsInRow))}, nil
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
