package telegram

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/mmallorquin/bot-marangatu-facturas/internal/invoice"
	"github.com/mmallorquin/bot-marangatu-facturas/internal/store"
)

// /pendientes junta las facturas leídas que todavía no se guardaron ni descartaron,
// por ejemplo después de mandar un álbum, y permite guardar de una vez las que cierran.
const (
	pendingCommand      = "/pendientes"
	pendingSaveCallback = "p:s"
)

func (h *handler) showPending(ctx context.Context, b *bot.Bot, chatID int64) {
	drafts, err := h.deps.Store.Drafts(ctx, chatID)
	if err != nil {
		h.logger.Error("no se pudieron leer los pendientes", "chat_id", chatID, "error", err)
		h.send(ctx, b, chatID, StoreErrorMessage, nil)
		return
	}
	if len(drafts) == 0 {
		h.send(ctx, b, chatID, "✅ No tenés facturas pendientes de guardar.", nil)
		return
	}

	ready := 0
	for _, rec := range drafts {
		if invoice.Clean(rec.Invoice) {
			ready++
		}
	}
	text := fmt.Sprintf("📥 Tenés %d %s sin guardar: %d %s y %d %s algo para revisar.",
		len(drafts), plural(len(drafts), "factura", "facturas"),
		ready, plural(ready, "cierra", "cierran"),
		len(drafts)-ready, plural(len(drafts)-ready, "tiene", "tienen"))
	if ready == 0 {
		h.send(ctx, b, chatID, text+"\n\nCorregilas desde su mensaje, más arriba.", nil)
		return
	}
	keyboard := &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{
		{Text: fmt.Sprintf("✅ Guardar las %d que cierran", ready), CallbackData: pendingSaveCallback},
	}}}
	h.send(ctx, b, chatID, text, keyboard)
}

// saveAllReady guarda los borradores sin problemas; los demás quedan para revisar.
func (h *handler) saveAllReady(ctx context.Context, b *bot.Bot, press buttonPress) {
	drafts, err := h.deps.Store.Drafts(ctx, press.chatID)
	if err != nil {
		h.logger.Error("no se pudieron leer los pendientes", "chat_id", press.chatID, "error", err)
		h.answer(ctx, b, press.queryID, StoreErrorMessage, true)
		return
	}

	saved, duplicates, review := 0, 0, 0
	for _, rec := range drafts {
		if !invoice.Clean(rec.Invoice) {
			review++
			continue
		}
		err := h.deps.Store.Save(ctx, press.chatID, rec.ID)
		switch {
		case errors.Is(err, store.ErrDuplicate):
			duplicates++
			h.track(ctx, press.chatID, store.Event{Kind: store.EventDuplicate})
		case err != nil:
			h.logger.Error("no se pudo guardar la factura", "chat_id", press.chatID, "id", rec.ID, "error", err)
			review++
		default:
			saved++
			h.track(ctx, press.chatID, store.Event{Kind: store.EventSaved, Detail: savedDetail(rec.Corrected)})
		}
	}

	text := fmt.Sprintf("💾 Guardé %d %s.", saved, plural(saved, "factura", "facturas"))
	if duplicates > 0 {
		text += fmt.Sprintf("\n⚠️ %d ya %s guardada%s antes: quedaron sin guardar, descartalas desde su mensaje.",
			duplicates, plural(duplicates, "estaba", "estaban"), plural(duplicates, "", "s"))
	}
	if review > 0 {
		text += fmt.Sprintf("\n✏️ %d %s algo para revisar: corregilas desde su mensaje, más arriba.",
			review, plural(review, "tiene", "tienen"))
	}
	h.editText(ctx, b, press, text, noKeyboard())
	h.answer(ctx, b, press.queryID, "", false)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
