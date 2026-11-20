package telegram

import (
	"context"
	"errors"
	"fmt"
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
	rec, err := h.deps.Store.Get(ctx, press.chatID, c.id)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		h.logger.Error("no se pudo leer la factura", "chat_id", press.chatID, "error", err)
		h.answer(ctx, b, press.queryID, StoreErrorMessage, true)
		return
	}
	// Ver la foto y volver a la lectura valen mientras la factura exista, guardada o pendiente.
	// El resto de los botones edita el borrador, así que una guardada o descartada ya no los acepta.
	viewing := c.action == actionPhoto || c.action == actionShow
	if err != nil || rec.Status == store.StatusDiscarded || rec.Status == store.StatusDeleted {
		h.answer(ctx, b, press.queryID, NoLongerEditableAlert, false)
		h.editKeyboard(ctx, b, press, noKeyboard())
		return
	}
	if !viewing && c.revision != invoiceRevision(rec) {
		h.refreshInvoice(ctx, b, press, rec)
		return
	}
	if c.action == actionUndo {
		h.undoAutoSave(ctx, b, press)
		return
	}
	if !viewing && rec.Status != store.StatusDraft {
		h.answer(ctx, b, press.queryID, NoLongerEditableAlert, false)
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
		pending, found, err := h.deps.Store.Awaiting(ctx, press.chatID)
		if err == nil && found && pending.ID == rec.ID {
			err = h.deps.Store.ClearAwaiting(ctx, press.chatID)
		}
		if err != nil {
			h.answer(ctx, b, press.queryID, StoreErrorMessage, true)
			return
		}
		h.editKeyboard(ctx, b, press, reviewKeyboard(c.id, rec.Invoice, rec.Source.FileID != ""))
		h.answer(ctx, b, press.queryID, "", false)
	case actionUseRUC:
		h.applyShortcut(ctx, b, press, rec, invoice.FieldIssuerRUC, RUCAppliedAnswer, "")
	case actionNoCDC:
		h.applyShortcut(ctx, b, press, rec, invoice.FieldCDC, "", CDCClearedNote)
	case actionPhoto:
		h.sendSourcePhoto(ctx, b, press, rec)
	case actionShow:
		h.showReading(ctx, b, press, rec)
	case actionLater:
		h.answer(ctx, b, press.queryID, "", false)
		h.offerPendingAfter(ctx, b, press.chatID, rec.ID)
	case actionAdopt:
		h.adoptReread(ctx, b, press, rec)
	case actionField:
		h.askForValue(ctx, b, press, rec)
	}
}

// applyShortcut aplica una corrección de un toque (RUC sugerido o quitar el CDC) y vuelve a mostrar la factura.
// El valor se recalcula acá, a partir del borrador actual: el botón no lleva datos de la factura.
func (h *handler) applyShortcut(ctx context.Context, b *bot.Bot, press buttonPress, rec store.Record, field, answer, note string) {
	value := ""
	if field == invoice.FieldIssuerRUC {
		suggested, ok := invoice.SuggestRUC(rec.Invoice.IssuerRUC)
		if !ok {
			h.answer(ctx, b, press.queryID, "El RUC ya está bien.", false)
			h.editKeyboard(ctx, b, press, reviewKeyboard(rec.ID, rec.Invoice, rec.Source.FileID != ""))
			return
		}
		value = suggested
	}
	edited, err := invoice.Edit(rec.Invoice, field, value)
	if err == nil {
		err = h.deps.Store.UpdateInvoice(ctx, press.chatID, rec.ID, edited)
	}
	if err != nil {
		h.logger.Error("no se pudo aplicar el atajo", "chat_id", press.chatID, "campo", field, "error", err)
		h.answer(ctx, b, press.queryID, StoreErrorMessage, true)
		return
	}
	h.track(ctx, press.chatID, store.Event{Kind: store.EventCorrection, Detail: field})
	h.answer(ctx, b, press.queryID, answer, false)
	if h.saveIfClean(ctx, press.chatID, rec.ID, edited) {
		h.editText(ctx, b, press, FormatInvoice(edited, nil)+"\n\n"+AutoSavedNote, savedKeyboard(rec.ID, rec.Source.FileID != ""))
		h.offerNextPending(ctx, b, press.chatID)
		return
	}
	text, _ := h.invoiceMessage(ctx, press.chatID, edited)
	if note != "" {
		text += "\n\n" + note
	}
	h.editText(ctx, b, press, text, reviewKeyboard(rec.ID, edited, rec.Source.FileID != ""))
}

// saveIfClean guarda sola una factura recién corregida que ya cierra, si el chat tiene el guardado
// automático: así corregir un dato no exige además tocar Guardar. Devuelve si la guardó.
func (h *handler) saveIfClean(ctx context.Context, chatID, id int64, inv invoice.Invoice) bool {
	if !invoice.Clean(inv) || len(h.reviewIssues(inv)) > 0 || !h.autoSaves(ctx, chatID) {
		return false
	}
	if err := h.deps.Store.Save(ctx, chatID, id); err != nil {
		return false // duplicada o falla de la base: queda pendiente con sus botones
	}
	h.track(ctx, chatID, store.Event{Kind: store.EventSaved, Detail: store.EventDetailCorrected})
	return true
}

func (h *handler) saveInvoice(ctx context.Context, b *bot.Bot, press buttonPress, rec store.Record) {
	if issues := invoice.Validate(rec.Invoice); len(issues) > 0 {
		h.track(ctx, press.chatID, store.Event{Kind: store.EventSaveBlocked})
		for _, issue := range issues {
			h.track(ctx, press.chatID, store.Event{Kind: store.EventBlockedField, Detail: issue.Field})
		}
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
		h.editText(ctx, b, press, FormatInvoice(rec.Invoice, nil)+"\n\n"+DuplicateNote, discardOnlyKeyboard(rec.ID, rec.Source.FileID != ""))
		h.answer(ctx, b, press.queryID, "", false)
	case err != nil:
		h.logger.Error("no se pudo guardar la factura", "chat_id", press.chatID, "error", err)
		h.answer(ctx, b, press.queryID, StoreErrorMessage, true)
	default:
		h.logger.Info("factura guardada", "chat_id", press.chatID, "id", rec.ID, "corregida", rec.Corrected)
		h.track(ctx, press.chatID, store.Event{Kind: store.EventSaved, Detail: savedDetail(rec.Corrected)})
		h.editText(ctx, b, press, FormatInvoice(rec.Invoice, nil)+"\n\n"+SavedNote, savedKeyboard(rec.ID, rec.Source.FileID != ""))
		h.answer(ctx, b, press.queryID, SavedAnswer, false)
		h.offerNextPending(ctx, b, press.chatID)
	}
}

// offerNextPending muestra la pendiente más antigua que queda, con su foto y sus botones,
// para seguir revisando sin buscarla en el chat. Si no queda ninguna, no dice nada.
func (h *handler) offerNextPending(ctx context.Context, b *bot.Bot, chatID int64) {
	h.offerPending(ctx, b, chatID, func(drafts []store.Record) store.Record { return drafts[0] })
}

// offerPendingID es offerNextPending empezando por la pendiente first, si sigue pendiente.
func (h *handler) offerPendingID(ctx context.Context, b *bot.Bot, chatID, first int64) {
	h.offerPending(ctx, b, chatID, func(drafts []store.Record) store.Record {
		for _, d := range drafts {
			if d.ID == first {
				return d
			}
		}
		return drafts[0]
	})
}

// offerPendingAfter muestra la pendiente que sigue a skipped; desde la última vuelve a la primera.
func (h *handler) offerPendingAfter(ctx context.Context, b *bot.Bot, chatID, skipped int64) {
	h.offerPending(ctx, b, chatID, func(drafts []store.Record) store.Record {
		for _, d := range drafts {
			if d.ID > skipped {
				return d
			}
		}
		return drafts[0]
	})
}

// offerPending manda la foto de la pendiente elegida y, debajo, su lectura con los botones:
// en una tanda la foto original queda muy arriba en el chat y hay que verla para corregir.
func (h *handler) offerPending(ctx context.Context, b *bot.Bot, chatID int64, pick func([]store.Record) store.Record) {
	drafts, err := h.deps.Store.Drafts(ctx, chatID)
	if err != nil {
		h.logger.Error("no se pudieron leer los pendientes", "chat_id", chatID, "error", err)
		return
	}
	if len(drafts) == 0 {
		return
	}
	next := pick(drafts)
	replyTo := next.Source.MessageID
	if next.Source.FileID != "" {
		if photoID, err := h.sendSource(ctx, b, chatID, next, 0, nil); err == nil {
			replyTo = photoID
		} else {
			h.logger.Warn("no se pudo mandar la foto de la pendiente", "chat_id", chatID, "error", Redact(err, h.deps.Token))
		}
	}
	text, _ := h.invoiceMessage(ctx, chatID, next.Invoice)
	header := fmt.Sprintf(NextPendingHeader, plural(len(drafts), "queda", "quedan"), len(drafts))
	keyboard := reviewKeyboard(next.ID, next.Invoice, next.Source.FileID != "")
	if len(drafts) > 1 {
		keyboard.InlineKeyboard = append(keyboard.InlineKeyboard, []models.InlineKeyboardButton{
			button("⏭ Después", callback{action: actionLater, id: next.ID}),
		})
	}
	h.sendReply(ctx, b, chatID, replyTo, header+text, keyboard)
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
	h.editText(ctx, b, press, FormatInvoice(rec.Invoice, h.reviewIssues(rec.Invoice))+"\n\n"+UndoneNote, reviewKeyboard(rec.ID, rec.Invoice, rec.Source.FileID != ""))
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
	h.offerNextPending(ctx, b, press.chatID)
}

func (h *handler) askForValue(ctx context.Context, b *bot.Bot, press buttonPress, rec store.Record) {
	field := press.callback.field
	previous, found, err := h.deps.Store.Awaiting(ctx, press.chatID)
	if err != nil {
		h.answer(ctx, b, press.queryID, StoreErrorMessage, true)
		return
	}
	if err := h.deps.Store.SetAwaiting(ctx, press.chatID, press.callback.id, field); err != nil {
		h.logger.Error("no se pudo iniciar la corrección", "chat_id", press.chatID, "error", err)
		h.answer(ctx, b, press.queryID, StoreErrorMessage, true)
		return
	}
	text := fmt.Sprintf("🧾 Factura #%d · %s · %s\n\n", rec.ID, historyValue(rec.Invoice.Number, maxNumberDisplayUTF16), previewIssuer(rec.Invoice.IssuerName))
	if found && (previous.ID != rec.ID || previous.Field != field) {
		text += "Dejé la corrección anterior sin aplicar. Ahora corregís esta factura.\n\n"
	}
	if read := comparableValue(rec.Invoice, field); read != "" {
		text += "Leí: " + truncateUTF16(display(field, read), maxIssuerDisplayUTF16) + "\n"
	}
	h.sendReply(ctx, b, press.chatID, rec.Source.MessageID, text+askValueMessage(field), nil)
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
	keyboard = h.bindKeyboard(ctx, press.chatID, keyboard)
	_, err := b.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID: press.chatID, MessageID: press.messageID, Text: telegramMessageText(text), ReplyMarkup: keyboard,
	})
	if err != nil {
		h.logger.Error("no se pudo editar el mensaje", "chat_id", press.chatID, "error", Redact(err, h.deps.Token))
	}
}

func (h *handler) editKeyboard(ctx context.Context, b *bot.Bot, press buttonPress, keyboard *models.InlineKeyboardMarkup) {
	keyboard = h.bindKeyboard(ctx, press.chatID, keyboard)
	_, err := b.EditMessageReplyMarkup(ctx, &bot.EditMessageReplyMarkupParams{
		ChatID: press.chatID, MessageID: press.messageID, ReplyMarkup: keyboard,
	})
	if err != nil {
		h.logger.Error("no se pudo cambiar los botones", "chat_id", press.chatID, "error", Redact(err, h.deps.Token))
	}
}
