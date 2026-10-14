package telegram

import (
	"context"
	"errors"
	"fmt"
	"strconv"
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

// sourceKeyboardRow son los botones para volver a la foto.
func sourceKeyboardRow(id int64) []models.InlineKeyboardButton {
	return []models.InlineKeyboardButton{
		photoButton(id),
		button("🔁 Releer", callback{action: actionReread, id: id}),
	}
}

// photoCaption identifica de qué factura es la foto reenviada.
func photoCaption(inv invoice.Invoice) string {
	parts := []string{"📷 Foto de la factura"}
	if inv.Number != "" {
		parts = append(parts, historyValue(inv.Number, maxNumberDisplayUTF16))
	}
	if inv.IssuerName != "" {
		parts = append(parts, previewIssuer(inv.IssuerName))
	}
	if inv.Date != "" {
		parts = append(parts, displayDate(inv.Date))
	}
	return strings.Join(parts, " · ")
}

// sendSourcePhoto reenvía la foto o el PDF original citando el mensaje desde el que se pidió,
// con un epígrafe que dice de qué factura es y un botón para volver a la lectura.
func (h *handler) sendSourcePhoto(ctx context.Context, b *bot.Bot, press buttonPress, rec store.Record) {
	if rec.Source.FileID == "" {
		h.answer(ctx, b, press.queryID, NoSourceAlert, true)
		return
	}
	reply := &models.ReplyParameters{MessageID: press.messageID, AllowSendingWithoutReply: true}
	caption := photoCaption(rec.Invoice)
	keyboard := photoKeyboard(rec.ID, rec.Status == store.StatusDraft)
	var err error
	if rec.Source.MimeType == pdfMimeType {
		_, err = b.SendDocument(ctx, &bot.SendDocumentParams{
			ChatID: press.chatID, Document: &models.InputFileString{Data: rec.Source.FileID},
			Caption: caption, ReplyMarkup: keyboard, ReplyParameters: reply,
		})
	} else {
		_, err = b.SendPhoto(ctx, &bot.SendPhotoParams{
			ChatID: press.chatID, Photo: &models.InputFileString{Data: rec.Source.FileID},
			Caption: caption, ReplyMarkup: keyboard, ReplyParameters: reply,
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

// showReading vuelve a mandar la lectura de la factura, citando la foto original, con los botones
// que corresponden a su estado. Es el camino de vuelta desde la foto reenviada.
func (h *handler) showReading(ctx context.Context, b *bot.Bot, press buttonPress, rec store.Record) {
	replyTo := photoOf(rec, press)
	hasSource := rec.Source.FileID != ""
	var (
		text     string
		keyboard *models.InlineKeyboardMarkup
	)
	if rec.Status == store.StatusSaved {
		text = FormatInvoice(rec.Invoice, nil)
		if rec.Invoice.CDC != "" {
			text += "\n\n" + ElectronicNote
		}
		text += "\n\n" + SavedNote
		keyboard = savedKeyboard(rec.ID, hasSource)
	} else {
		text, _ = h.invoiceMessage(ctx, press.chatID, rec.Invoice, invoice.Validate(rec.Invoice))
		keyboard = reviewKeyboard(rec.ID, rec.Invoice, hasSource)
	}
	h.sendReply(ctx, b, press.chatID, replyTo, text, keyboard)
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

// photoOf es el mensaje a citar en las respuestas de la relectura: la foto si se guardó, si no la factura.
func photoOf(rec store.Record, press buttonPress) int {
	if rec.Source.MessageID > 0 {
		return rec.Source.MessageID
	}
	return press.messageID
}

func (h *handler) reread(ctx context.Context, b *bot.Bot, press buttonPress, rec store.Record) {
	ctx, cancel := context.WithTimeout(ctx, processTimeout)
	defer cancel()
	select {
	case h.deps.Operations.reads <- struct{}{}:
		defer func() { <-h.deps.Operations.reads }()
	case <-ctx.Done():
		h.finishRead(ctx, func(ctx context.Context) {
			h.sendReply(ctx, b, press.chatID, photoOf(rec, press), ReadErrorMessage, nil)
		})
		return
	}

	data, err := downloadFile(ctx, b, rec.Source.FileID)
	if err != nil {
		h.finishRead(ctx, func(ctx context.Context) {
			h.logger.Warn("no se pudo volver a bajar la foto", "chat_id", press.chatID, "error", Redact(err, h.deps.Token))
			h.track(ctx, press.chatID, store.Event{Kind: store.EventReadError, Detail: store.EventDetailDownload})
			h.sendReply(ctx, b, press.chatID, photoOf(rec, press), PhotoGoneMessage, nil)
		})
		return
	}

	start := time.Now()
	result, err := h.deps.Reader.Read(ctx, reader.Image{Data: data, MimeType: rec.Source.MimeType, Focus: reader.FocusIdentity})
	if err != nil {
		h.finishRead(ctx, func(ctx context.Context) {
			h.logger.Error("no se pudo releer la factura", "chat_id", press.chatID, "error", err)
			h.track(ctx, press.chatID, store.Event{Kind: store.EventReadError, Detail: store.EventDetailAI, Seconds: time.Since(start).Seconds()})
			h.sendReply(ctx, b, press.chatID, photoOf(rec, press), readFailureMessage(err), nil)
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
	replyTo := photoOf(current, press)
	reread := result.Invoice
	reread.Number = invoice.NormalizeNumber(reread.Number)
	if err := h.deps.Store.SaveReread(ctx, press.chatID, current.ID, reread); err != nil {
		h.logger.Error("no se pudo guardar la relectura", "chat_id", press.chatID, "error", err)
		h.sendReply(ctx, b, press.chatID, replyTo, StoreErrorMessage, nil)
		return
	}
	h.track(ctx, press.chatID, store.Event{Kind: store.EventReread, Seconds: seconds, CostUSD: result.CostUSD})

	diffs := readingDiffs(current.Invoice, reread)
	attempt := fmt.Sprintf("%d de %d", current.Rereads+1, maxRereads)
	if !reread.IsInvoice {
		h.sendReply(ctx, b, press.chatID, replyTo, "🔁 Relectura "+attempt+": esta vez el modelo no reconoció una factura. "+RetakeTip, nil)
		return
	}
	if len(diffs) == 0 {
		h.sendReply(ctx, b, press.chatID, replyTo, fmt.Sprintf(RereadSameMessage, attempt), nil)
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
	h.sendReply(ctx, b, press.chatID, replyTo, text.String(), &models.InlineKeyboardMarkup{InlineKeyboard: rows})
}

// fieldDiff es un campo en el que la relectura difiere del borrador.
type fieldDiff struct {
	Field, Current, Reread string
}

// readingDiffs compara todos los campos corregibles: la relectura vuelve a leer la imagen entera.
func readingDiffs(current, reread invoice.Invoice) []fieldDiff {
	var diffs []fieldDiff
	for _, field := range invoice.EditableFields {
		a, b := comparableValue(current, field), comparableValue(reread, field)
		if a != b {
			diffs = append(diffs, fieldDiff{Field: field, Current: display(field, a), Reread: display(field, b)})
		}
	}
	return diffs
}

// comparableValue es el valor del campo normalizado para comparar y para volver a cargarlo con Edit.
func comparableValue(inv invoice.Invoice, field string) string {
	switch field {
	case invoice.FieldIssuerName:
		return strings.TrimSpace(inv.IssuerName)
	case invoice.FieldIssuerRUC:
		return strings.ToUpper(strings.TrimSpace(inv.IssuerRUC))
	case invoice.FieldTimbrado:
		return strings.TrimSpace(inv.Timbrado)
	case invoice.FieldNumber:
		return invoice.NormalizeNumber(inv.Number)
	case invoice.FieldDate:
		return inv.Date
	case invoice.FieldCondition:
		return inv.Condition
	case invoice.FieldExempt:
		return strconv.FormatInt(inv.Exempt, 10)
	case invoice.FieldTaxed5:
		return strconv.FormatInt(inv.Taxed5, 10)
	case invoice.FieldTaxed10:
		return strconv.FormatInt(inv.Taxed10, 10)
	case invoice.FieldVAT5:
		return strconv.FormatInt(inv.VAT5, 10)
	case invoice.FieldVAT10:
		return strconv.FormatInt(inv.VAT10, 10)
	case invoice.FieldTotal:
		return strconv.FormatInt(inv.Total, 10)
	case invoice.FieldCDC:
		return strings.TrimSpace(inv.CDC)
	}
	return ""
}

// display muestra el valor como lo ve el usuario en la factura.
func display(field, value string) string {
	switch field {
	case invoice.FieldDate:
		return displayDate(value)
	case invoice.FieldCondition:
		return labelOr(conditionLabels, value, value)
	case invoice.FieldExempt, invoice.FieldTaxed5, invoice.FieldTaxed10, invoice.FieldVAT5, invoice.FieldVAT10, invoice.FieldTotal:
		if n, err := strconv.ParseInt(value, 10, 64); err == nil {
			return formatGs(n)
		}
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
	for _, d := range readingDiffs(rec.Invoice, *rec.Reread) {
		if press.callback.field != rereadAllFields && press.callback.field != d.Field {
			continue
		}
		value := comparableValue(*rec.Reread, d.Field)
		if value == "" && (d.Field == invoice.FieldIssuerName || d.Field == invoice.FieldCDC) {
			if d.Field == invoice.FieldIssuerName {
				continue // una razón social vacía no reemplaza a una leída
			}
			value = "no"
		}
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
