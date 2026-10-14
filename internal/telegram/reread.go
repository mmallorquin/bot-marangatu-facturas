package telegram

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/mmallorquin/bot-marangatu-facturas/internal/invoice"
	"github.com/mmallorquin/bot-marangatu-facturas/internal/reader"
	"github.com/mmallorquin/bot-marangatu-facturas/internal/store"
)

// Ver foto y Releer usan la referencia a la foto que guarda el borrador (file_id de Telegram).
const (
	// maxRereads limita el costo: cada relectura es una llamada más al modelo.
	maxRereads = 2

	rereadAllFields = "todo"
)

// identityFields son los campos que identifican la factura: los que la relectura compara.
var identityFields = []string{invoice.FieldIssuerRUC, invoice.FieldTimbrado, invoice.FieldNumber, invoice.FieldDate, invoice.FieldCDC}

// sourceKeyboardRow son los botones para volver a la foto.
func sourceKeyboardRow(id int64) []models.InlineKeyboardButton {
	return []models.InlineKeyboardButton{
		button("📷 Ver foto", callback{action: actionPhoto, id: id}),
		button("🔁 Releer", callback{action: actionReread, id: id}),
	}
}

// sendSourcePhoto reenvía la foto o el PDF original citando el mensaje de la factura.
func (h *handler) sendSourcePhoto(ctx context.Context, b *bot.Bot, press buttonPress, rec store.Record) {
	if rec.Source.FileID == "" {
		h.answer(ctx, b, press.queryID, NoSourceAlert, true)
		return
	}
	reply := &models.ReplyParameters{MessageID: press.messageID, AllowSendingWithoutReply: true}
	var err error
	if rec.Source.MimeType == pdfMimeType {
		_, err = b.SendDocument(ctx, &bot.SendDocumentParams{
			ChatID: press.chatID, Document: &models.InputFileString{Data: rec.Source.FileID}, ReplyParameters: reply,
		})
	} else {
		_, err = b.SendPhoto(ctx, &bot.SendPhotoParams{
			ChatID: press.chatID, Photo: &models.InputFileString{Data: rec.Source.FileID}, ReplyParameters: reply,
		})
	}
	if err != nil {
		h.logger.Warn("no se pudo reenviar la foto", "chat_id", press.chatID, "error", Redact(err, h.deps.Token))
		h.answer(ctx, b, press.queryID, PhotoGoneAlert, true)
		return
	}
	h.track(ctx, press.chatID, store.Event{Kind: store.EventViewPhoto})
	h.answer(ctx, b, press.queryID, "", false)
}

// handleRereadCallback corre fuera de withChat, como una lectura nueva: la descarga y la IA
// tardan segundos y no deben trabar el resto del chat. Toma el bloqueo solo para leer y responder.
func (h *handler) handleRereadCallback(ctx context.Context, b *bot.Bot, query *models.CallbackQuery, msg *models.Message) {
	c, err := parseCallback(query.Data)
	if err != nil {
		h.answer(ctx, b, query.ID, NoLongerEditableAlert, false)
		return
	}
	chatID := msg.Chat.ID
	press := buttonPress{queryID: query.ID, chatID: chatID, messageID: msg.ID, callback: c}

	ch := h.deps.Operations.chat(chatID)
	ch.mu.Lock()
	ctx = operationContext(ctx, ch)
	rec, err := h.deps.Store.Get(ctx, chatID, c.id)
	switch {
	case errors.Is(err, store.ErrNotFound) || (err == nil && rec.Status != store.StatusDraft):
		ch.mu.Unlock()
		h.answer(ctx, b, press.queryID, NoLongerEditableAlert, false)
		h.editKeyboard(ctx, b, press, noKeyboard())
		return
	case err != nil:
		ch.mu.Unlock()
		h.logger.Error("no se pudo leer la factura", "chat_id", chatID, "error", err)
		h.answer(ctx, b, press.queryID, StoreErrorMessage, true)
		return
	case rec.Source.FileID == "":
		ch.mu.Unlock()
		h.answer(ctx, b, press.queryID, NoSourceAlert, true)
		return
	case rec.Rereads >= maxRereads:
		ch.mu.Unlock()
		h.answer(ctx, b, press.queryID, RereadLimitAlert, true)
		return
	}
	op := currentOperation(ctx)
	ctx, cancel := context.WithCancel(ctx)
	ch.reads[op] = cancel
	h.answer(ctx, b, press.queryID, RereadingAnswer, false)
	ch.mu.Unlock()
	defer func() {
		cancel()
		ch.mu.Lock()
		delete(ch.reads, op)
		ch.mu.Unlock()
	}()
	h.reread(ctx, b, press, rec)
}

func (h *handler) reread(ctx context.Context, b *bot.Bot, press buttonPress, rec store.Record) {
	ctx, cancel := context.WithTimeout(ctx, processTimeout)
	defer cancel()
	select {
	case h.deps.Operations.reads <- struct{}{}:
		defer func() { <-h.deps.Operations.reads }()
	case <-ctx.Done():
		h.finishRead(ctx, func(ctx context.Context) { h.sendReply(ctx, b, press.chatID, press.messageID, ReadErrorMessage, nil) })
		return
	}

	data, err := downloadFile(ctx, b, rec.Source.FileID)
	if err != nil {
		h.finishRead(ctx, func(ctx context.Context) {
			h.logger.Warn("no se pudo volver a bajar la foto", "chat_id", press.chatID, "error", Redact(err, h.deps.Token))
			h.track(ctx, press.chatID, store.Event{Kind: store.EventReadError, Detail: store.EventDetailDownload})
			h.sendReply(ctx, b, press.chatID, press.messageID, PhotoGoneMessage, nil)
		})
		return
	}

	start := time.Now()
	result, err := h.deps.Reader.Read(ctx, reader.Image{Data: data, MimeType: rec.Source.MimeType, Focus: reader.FocusIdentity})
	if err != nil {
		h.finishRead(ctx, func(ctx context.Context) {
			h.logger.Error("no se pudo releer la factura", "chat_id", press.chatID, "error", err)
			h.track(ctx, press.chatID, store.Event{Kind: store.EventReadError, Detail: store.EventDetailAI, Seconds: time.Since(start).Seconds()})
			h.sendReply(ctx, b, press.chatID, press.messageID, readFailureMessage(err), nil)
		})
		return
	}
	h.finishRead(ctx, func(ctx context.Context) { h.showReread(ctx, b, press, result, time.Since(start).Seconds()) })
}

// showReread compara la relectura con el borrador actual (que el usuario pudo haber corregido
// mientras tanto) y ofrece adoptar, campo por campo, lo que difiere.
func (h *handler) showReread(ctx context.Context, b *bot.Bot, press buttonPress, result reader.Result, seconds float64) {
	current, err := h.deps.Store.Get(ctx, press.chatID, press.callback.id)
	if err != nil || current.Status != store.StatusDraft {
		return
	}
	reread := result.Invoice
	reread.Number = invoice.NormalizeNumber(reread.Number)
	if err := h.deps.Store.SaveReread(ctx, press.chatID, current.ID, reread); err != nil {
		h.logger.Error("no se pudo guardar la relectura", "chat_id", press.chatID, "error", err)
		h.sendReply(ctx, b, press.chatID, press.messageID, StoreErrorMessage, nil)
		return
	}
	h.track(ctx, press.chatID, store.Event{Kind: store.EventReread, Seconds: seconds, CostUSD: result.CostUSD})

	diffs := identityDiffs(current.Invoice, reread)
	attempt := fmt.Sprintf("%d de %d", current.Rereads+1, maxRereads)
	if !reread.IsInvoice {
		h.sendReply(ctx, b, press.chatID, press.messageID, "🔁 Relectura "+attempt+": esta vez el modelo no reconoció una factura. "+RetakeTip, nil)
		return
	}
	if len(diffs) == 0 {
		h.sendReply(ctx, b, press.chatID, press.messageID, fmt.Sprintf(RereadSameMessage, attempt), nil)
		return
	}

	var text strings.Builder
	fmt.Fprintf(&text, "🔁 Relectura %s. Leyó distinto:\n", attempt)
	var rows [][]models.InlineKeyboardButton
	for _, d := range diffs {
		fmt.Fprintf(&text, "• %s: %s → %s\n", capitalize(fieldLabel(d.Field)), orEmpty(d.Current), orEmpty(d.Reread))
		rows = append(rows, []models.InlineKeyboardButton{
			button("Usar "+fieldLabel(d.Field)+" de la relectura", callback{action: actionAdopt, id: current.ID, field: d.Field}),
		})
	}
	if len(diffs) > 1 {
		rows = append(rows, []models.InlineKeyboardButton{button("Usar todo lo de la relectura", callback{action: actionAdopt, id: current.ID, field: rereadAllFields})})
	}
	text.WriteString("\nMirá la factura y tocá lo que corresponda. Si ninguna es correcta, corregí el campo a mano.")
	h.sendReply(ctx, b, press.chatID, press.messageID, text.String(), &models.InlineKeyboardMarkup{InlineKeyboard: rows})
}

// fieldDiff es un campo de identidad en el que la relectura difiere del borrador.
type fieldDiff struct {
	Field, Current, Reread string
}

func identityDiffs(current, reread invoice.Invoice) []fieldDiff {
	values := func(inv invoice.Invoice) map[string]string {
		return map[string]string{
			invoice.FieldIssuerRUC: strings.ToUpper(strings.TrimSpace(inv.IssuerRUC)),
			invoice.FieldTimbrado:  strings.TrimSpace(inv.Timbrado),
			invoice.FieldNumber:    invoice.NormalizeNumber(inv.Number),
			invoice.FieldDate:      inv.Date,
			invoice.FieldCDC:       strings.TrimSpace(inv.CDC),
		}
	}
	a, b := values(current), values(reread)
	var diffs []fieldDiff
	for _, field := range identityFields {
		if a[field] != b[field] {
			diffs = append(diffs, fieldDiff{Field: field, Current: display(field, a[field]), Reread: display(field, b[field])})
		}
	}
	return diffs
}

func display(field, value string) string {
	if field == invoice.FieldDate {
		return displayDate(value)
	}
	return value
}

func orEmpty(value string) string {
	if value == "" {
		return "(vacío)"
	}
	return value
}

// adoptReread aplica al borrador uno o todos los campos en que la relectura difiere.
func (h *handler) adoptReread(ctx context.Context, b *bot.Bot, press buttonPress, rec store.Record) {
	if rec.Reread == nil {
		h.answer(ctx, b, press.queryID, NoRereadAlert, true)
		return
	}
	edited := rec.Invoice
	for _, d := range identityDiffs(rec.Invoice, *rec.Reread) {
		if press.callback.field != rereadAllFields && press.callback.field != d.Field {
			continue
		}
		value := rawIdentityValue(*rec.Reread, d.Field)
		next, err := invoice.Edit(edited, d.Field, value)
		if err != nil {
			h.answer(ctx, b, press.queryID, "❌ "+err.Error(), true)
			return
		}
		edited = next
		h.track(ctx, press.chatID, store.Event{Kind: store.EventCorrection, Detail: d.Field})
	}
	if err := h.deps.Store.UpdateInvoice(ctx, press.chatID, rec.ID, edited); err != nil {
		h.logger.Error("no se pudo aplicar la relectura", "chat_id", press.chatID, "error", err)
		h.answer(ctx, b, press.queryID, StoreErrorMessage, true)
		return
	}
	text, _ := h.invoiceMessage(ctx, press.chatID, edited, invoice.Validate(edited))
	h.editText(ctx, b, press, text, reviewKeyboard(rec.ID, edited, rec.Source.FileID != ""))
	h.answer(ctx, b, press.queryID, RereadAppliedAnswer, false)
}

func rawIdentityValue(inv invoice.Invoice, field string) string {
	switch field {
	case invoice.FieldIssuerRUC:
		return inv.IssuerRUC
	case invoice.FieldTimbrado:
		return inv.Timbrado
	case invoice.FieldNumber:
		return inv.Number
	case invoice.FieldDate:
		return inv.Date
	case invoice.FieldCDC:
		if inv.CDC == "" {
			return "no"
		}
		return inv.CDC
	}
	return ""
}
