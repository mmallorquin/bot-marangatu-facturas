package telegram

import (
	"context"
	"crypto/rand"
	"fmt"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// /borrar_mis_datos borra todo lo del usuario, con confirmación: facturas, configuración,
// exportaciones, recordatorios y métricas de uso.
const (
	deleteDataCommand = "/borrar_mis_datos"
	deleteDataConfirm = "z:s"
	deleteDataCancel  = "z:n"
)

func (h *handler) askDeleteData(ctx context.Context, b *bot.Bot, chatID int64) {
	c := currentOperation(ctx).chat
	c.deleteToken = rand.Text()
	keyboard := &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{
		{Text: "Sí, borrar todo", CallbackData: deleteDataConfirm + ":" + c.deleteToken},
		{Text: "No", CallbackData: deleteDataCancel + ":" + c.deleteToken},
	}}}
	if err := h.send(ctx, b, chatID, "⚠️ ¿Borrar todos tus datos del bot?\n\n"+
		"Se borran tus facturas (guardadas y pendientes), tu RUC, tus impuestos y el historial de exportaciones. "+
		"No se puede deshacer. Los ZIP que ya descargaste o subiste a Marangatu no se tocan.", keyboard); err != nil {
		c.deleteToken = ""
	}
}

func (h *handler) handleDeleteData(ctx context.Context, b *bot.Bot, press buttonPress, data string) {
	c := currentOperation(ctx).chat
	parts := strings.Split(data, ":")
	if len(parts) != 3 || (parts[0]+":"+parts[1] != deleteDataConfirm && parts[0]+":"+parts[1] != deleteDataCancel) ||
		c.deleteToken == "" || parts[2] != c.deleteToken {
		h.answer(ctx, b, press.queryID, "Esta confirmación venció. Pedí /borrar_mis_datos otra vez.", true)
		return
	}
	c.deleteToken = "" // cancelar o confirmar consume la misma pregunta
	if parts[1] == "n" {
		h.editText(ctx, b, press, "Listo, no borré nada.", noKeyboard())
		h.answer(ctx, b, press.queryID, "", false)
		return
	}
	deleted, err := h.deps.Store.DeleteChat(ctx, press.chatID)
	if err != nil {
		h.logger.Error("no se pudieron borrar los datos", "chat_id", press.chatID, "error", err)
		h.answer(ctx, b, press.queryID, StoreErrorMessage, true)
		return
	}
	invalidateReads(ctx)
	h.logger.Info("datos del chat borrados", "chat_id", press.chatID, "facturas", deleted)
	h.editText(ctx, b, press, fmt.Sprintf("🗑️ Listo: borré %d %s y tu configuración. "+
		"Si volvés a escribir, empezás de cero.", deleted, plural(deleted, "factura", "facturas")), noKeyboard())
	h.answer(ctx, b, press.queryID, "", false)
}
