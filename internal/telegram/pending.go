package telegram

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

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

	readyDrafts, review, duplicates, err := h.classifyPending(ctx, chatID, drafts)
	if err != nil {
		h.logger.Error("no se pudieron comprobar los pendientes", "chat_id", chatID, "error", err)
		h.send(ctx, b, chatID, StoreErrorMessage, nil)
		return
	}
	ready := len(readyDrafts)
	text := fmt.Sprintf("📥 Tenés %d %s sin guardar: %d %s y %d %s algo para revisar.",
		len(drafts), plural(len(drafts), "factura", "facturas"),
		ready, plural(ready, "cierra", "cierran"),
		review, plural(review, "tiene", "tienen"))
	if duplicates > 0 {
		text += fmt.Sprintf("\n⚠️ %d %s ya %s; no se volverán a guardar. %s desde su mensaje.", duplicates,
			plural(duplicates, "duplicada", "duplicadas"), plural(duplicates, "estaba guardada", "estaban guardadas"), plural(duplicates, "Descartala", "Descartalas"))
	}
	if ready == 0 {
		if review > 0 {
			text += "\n\nCorregilas desde su mensaje, más arriba."
		}
		h.send(ctx, b, chatID, text, nil)
		return
	}
	keyboard := &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{
		{Text: fmt.Sprintf("✅ Guardar las %d que cierran", ready), CallbackData: pendingSaveCallback + ":" + pendingRevision(readyDrafts)},
	}}}
	h.send(ctx, b, chatID, text, keyboard)
}

func (h *handler) classifyPending(ctx context.Context, chatID int64, drafts []store.Record) (ready []store.Record, review, duplicates int, err error) {
	for _, rec := range drafts {
		if !invoice.Clean(rec.Invoice) {
			review++
			continue
		}
		alreadySaved, err := h.deps.Store.IsSaved(ctx, chatID, rec.Invoice)
		if err != nil {
			return nil, 0, 0, err
		}
		if alreadySaved {
			duplicates++
			continue
		}
		ready = append(ready, rec)
	}
	return ready, review, duplicates, nil
}

// pendingRevision binds the button to the exact ready IDs and data shown.
// New arrivals or corrections require a new confirmation instead of expanding a batch.
func pendingRevision(drafts []store.Record) string {
	var ready []struct {
		ID      int64
		Invoice invoice.Invoice
	}
	for _, rec := range drafts {
		if invoice.Clean(rec.Invoice) {
			ready = append(ready, struct {
				ID      int64
				Invoice invoice.Invoice
			}{rec.ID, rec.Invoice})
		}
	}
	data, _ := json.Marshal(ready)
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum[:16])
}

func (h *handler) handlePendingCallback(ctx context.Context, b *bot.Bot, query *models.CallbackQuery, msg *models.Message) {
	parts := strings.Split(query.Data, ":")
	if len(parts) != 3 || parts[0]+":"+parts[1] != pendingSaveCallback || parts[2] == "" {
		h.answer(ctx, b, query.ID, "Este botón ya no está disponible. Usá /pendientes de nuevo.", true)
		return
	}
	h.saveAllReady(ctx, b, buttonPress{queryID: query.ID, chatID: msg.Chat.ID, messageID: msg.ID}, parts[2])
}

// saveAllReady guarda los borradores sin problemas; los demás quedan para revisar.
func (h *handler) saveAllReady(ctx context.Context, b *bot.Bot, press buttonPress, revision string) {
	drafts, err := h.deps.Store.Drafts(ctx, press.chatID)
	if err != nil {
		h.logger.Error("no se pudieron leer los pendientes", "chat_id", press.chatID, "error", err)
		h.answer(ctx, b, press.queryID, StoreErrorMessage, true)
		return
	}
	readyDrafts, review, duplicates, err := h.classifyPending(ctx, press.chatID, drafts)
	if err != nil {
		h.logger.Error("no se pudieron comprobar los pendientes", "chat_id", press.chatID, "error", err)
		h.answer(ctx, b, press.queryID, StoreErrorMessage, true)
		return
	}
	if revision != pendingRevision(readyDrafts) {
		h.editText(ctx, b, press, "Los pendientes cambiaron. Revisá el nuevo resumen antes de guardar.", noKeyboard())
		h.showPending(ctx, b, press.chatID)
		h.answer(ctx, b, press.queryID, "Los pendientes cambiaron; confirmá el nuevo resumen.", true)
		return
	}

	saved := 0
	for _, rec := range readyDrafts {
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
