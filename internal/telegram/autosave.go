package telegram

import (
	"context"
	"strings"

	"github.com/go-telegram/bot"
)

// /autoguardar si|no: guardar sin preguntar las facturas que cierran.
const autoSaveCommand = "/autoguardar"

var (
	onWords  = []string{"si", "sí", "on", "activar"}
	offWords = []string{"no", "off", "desactivar"}
)

func (h *handler) setAutoSave(ctx context.Context, b *bot.Bot, chatID int64, text string) {
	fields := strings.Fields(strings.ToLower(text))
	if len(fields) < 2 {
		cs, err := h.deps.Store.Settings(ctx, chatID)
		if err != nil {
			h.send(ctx, b, chatID, StoreErrorMessage, nil)
			return
		}
		state := "desactivado: cada factura espera que toques Guardar."
		if cs.AutoSave {
			state = "activado: las facturas que cierran se guardan solas."
		}
		h.send(ctx, b, chatID, "El guardado automático está "+state+"\n\nPara cambiarlo: /autoguardar si o /autoguardar no", nil)
		return
	}

	var on bool
	switch {
	case containsWord(onWords, fields[1]):
		on = true
	case containsWord(offWords, fields[1]):
	default:
		h.send(ctx, b, chatID, "Escribí /autoguardar si o /autoguardar no", nil)
		return
	}
	if err := h.deps.Store.SetAutoSave(ctx, chatID, on); err != nil {
		h.logger.Error("no se pudo cambiar el guardado automático", "chat_id", chatID, "error", err)
		h.send(ctx, b, chatID, StoreErrorMessage, nil)
		return
	}
	if on {
		h.send(ctx, b, chatID, "✅ Listo: las facturas que cierran se guardan solas, con un botón para deshacer. "+
			"Las que tienen algo para revisar te las sigo mostrando para que las corrijas.", nil)
		return
	}
	h.send(ctx, b, chatID, "✅ Listo: cada factura espera que toques Guardar.", nil)
}

func containsWord(words []string, w string) bool {
	for _, candidate := range words {
		if candidate == w {
			return true
		}
	}
	return false
}
