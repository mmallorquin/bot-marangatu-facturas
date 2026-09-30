package telegram

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/mmallorquin/bot-marangatu-facturas/internal/invoice"
	"github.com/mmallorquin/bot-marangatu-facturas/internal/marangatu"
	"github.com/mmallorquin/bot-marangatu-facturas/internal/store"
)

// Comandos de configuración y exportación.
const (
	rucCommand     = "/ruc"
	imputeCommand  = "/imputar"
	exportCommand  = "/exportar"
	askRUCMessage  = "Primero decime tu RUC para armar el archivo, por ejemplo:\n/ruc 1234567-8"
	askImputations = "¿A qué impuestos imputás tus compras? Escribí uno o varios, por ejemplo:\n" +
		"/imputar iva\n/imputar iva irp\n\n(IVA, IRE o IRP-RSP)"
	uploadHint = "Subilo en Marangatu, en la importación del Registro de Comprobantes (RG 90)."

	exportCallbackPrefix = "x"
	exportActionCSV      = "c"
	exportActionExcel    = "e"
	exportActionZIP      = "z"
	exportActionCancel   = "n"
	exportPreviewLimit   = 10
)

type exportCallback struct {
	action string
	period string
}

func (c exportCallback) encode() string {
	return fmt.Sprintf("%s:%s:%s", exportCallbackPrefix, c.action, c.period)
}

func parseExportCallback(data string) (exportCallback, error) {
	parts := strings.Split(data, ":")
	if len(parts) != 3 || parts[0] != exportCallbackPrefix ||
		!slices.Contains([]string{exportActionCSV, exportActionExcel, exportActionZIP, exportActionCancel}, parts[1]) {
		return exportCallback{}, errInvalidCallback
	}
	if _, err := time.Parse("2006-01", parts[2]); err != nil {
		return exportCallback{}, errInvalidCallback
	}
	return exportCallback{action: parts[1], period: parts[2]}, nil
}

func exportPreviewKeyboard(period string) *models.InlineKeyboardMarkup {
	button := func(text, action string) models.InlineKeyboardButton {
		return models.InlineKeyboardButton{Text: text, CallbackData: exportCallback{action: action, period: period}.encode()}
	}
	return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{
		{button("📄 Descargar CSV", exportActionCSV), button("📊 Descargar Excel", exportActionExcel)},
		{button("✅ Generar ZIP", exportActionZIP), button("❌ Cancelar", exportActionCancel)},
	}}
}

// Palabras aceptadas en /imputar.
var imputationWords = map[string]func(*store.Imputations){
	"iva":     func(i *store.Imputations) { i.IVA = true },
	"ire":     func(i *store.Imputations) { i.IRE = true },
	"irp":     func(i *store.Imputations) { i.IRP = true },
	"irp-rsp": func(i *store.Imputations) { i.IRP = true },
	"rsp":     func(i *store.Imputations) { i.IRP = true },
}

// parseImputations lee "/imputar iva, irp" → {IVA: true, IRP: true}.
func parseImputations(text string) (store.Imputations, error) {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return r == ' ' || r == ',' })
	if len(words) < 2 {
		return store.Imputations{}, errors.New("indicá al menos un impuesto: iva, ire o irp")
	}

	var imp store.Imputations
	for _, word := range words[1:] {
		apply, ok := imputationWords[word]
		if !ok {
			return store.Imputations{}, fmt.Errorf("no conozco el impuesto %q: usá iva, ire o irp", word)
		}
		apply(&imp)
	}
	return imp, nil
}

func formatImputations(imp store.Imputations) string {
	var names []string
	if imp.IVA {
		names = append(names, "IVA")
	}
	if imp.IRE {
		names = append(names, "IRE")
	}
	if imp.IRP {
		names = append(names, "IRP-RSP")
	}
	if len(names) == 0 {
		return "ninguno"
	}
	return strings.Join(names, ", ")
}

func (h *handler) setRUC(ctx context.Context, b *bot.Bot, chatID int64, text string) {
	fields := strings.Fields(text)
	if len(fields) < 2 {
		cs, err := h.deps.Store.Settings(ctx, chatID)
		if err != nil || cs.RUC == "" {
			h.send(ctx, b, chatID, askRUCMessage, nil)
			return
		}
		h.send(ctx, b, chatID, "Tu RUC es "+cs.RUC+". Para cambiarlo: /ruc 1234567-8", nil)
		return
	}

	ruc := invoice.NormalizeRUC(strings.Join(fields[1:], ""))
	if err := invoice.ValidateRUC(ruc); err != nil {
		h.send(ctx, b, chatID, "❌ "+err.Error(), nil)
		return
	}
	if err := h.deps.Store.SetRUC(ctx, chatID, ruc); err != nil {
		h.logger.Error("no se pudo guardar el RUC", "chat_id", chatID, "error", err)
		h.send(ctx, b, chatID, StoreErrorMessage, nil)
		return
	}
	h.send(ctx, b, chatID, "✅ RUC guardado: "+ruc, nil)
}

func (h *handler) setImputations(ctx context.Context, b *bot.Bot, chatID int64, text string) {
	imp, err := parseImputations(text)
	if err != nil {
		h.send(ctx, b, chatID, "❌ "+err.Error()+"\n\n"+askImputations, nil)
		return
	}
	if err := h.deps.Store.SetImputations(ctx, chatID, imp); err != nil {
		h.logger.Error("no se pudieron guardar las imputaciones", "chat_id", chatID, "error", err)
		h.send(ctx, b, chatID, StoreErrorMessage, nil)
		return
	}
	h.send(ctx, b, chatID, "✅ Tus compras se van a imputar a: "+formatImputations(imp), nil)
}

// exportMonth muestra una previa; el ZIP se arma recién cuando el usuario lo confirma.
func (h *handler) exportMonth(ctx context.Context, b *bot.Bot, chatID int64, text string) {
	period, err := parsePeriod(text, h.deps.Now())
	if err != nil {
		h.send(ctx, b, chatID, "❌ "+err.Error(), nil)
		return
	}
	cs, err := h.deps.Store.Settings(ctx, chatID)
	if err != nil {
		h.logger.Error("no se pudo leer la configuración", "chat_id", chatID, "error", err)
		h.send(ctx, b, chatID, StoreErrorMessage, nil)
		return
	}
	switch {
	case cs.RUC == "":
		h.send(ctx, b, chatID, askRUCMessage, nil)
		return
	case cs.Imputations == (store.Imputations{}):
		h.send(ctx, b, chatID, askImputations, nil)
		return
	}

	invoices, err := h.deps.Store.SavedInvoices(ctx, chatID, period)
	if err != nil {
		h.logger.Error("no se pudieron leer las facturas", "chat_id", chatID, "error", err)
		h.send(ctx, b, chatID, StoreErrorMessage, nil)
		return
	}
	if len(invoices) == 0 {
		h.send(ctx, b, chatID, fmt.Sprintf("No hay facturas guardadas de %s.", periodTitle(period)), nil)
		return
	}

	settings := marangatu.Settings{
		RUC: cs.RUC, ImputeIVA: cs.Imputations.IVA, ImputeIRE: cs.Imputations.IRE, ImputeIRP: cs.Imputations.IRP,
	}
	preview, err := marangatu.PreviewPurchases(invoices, settings, period)
	if errors.Is(err, marangatu.ErrNothingToExport) {
		noun := "comprobantes guardados"
		if len(invoices) == 1 {
			noun = "comprobante guardado"
		}
		message := fmt.Sprintf("📋 %s\n\nTenés %d %s, pero ninguno entra en el ZIP.\n\n%s\n\nSiguen guardados y aparecen en /resumen.",
			periodTitle(period), len(invoices), noun, formatSkipped(preview.Skipped))
		h.send(ctx, b, chatID, message, nil)
		return
	}
	if err != nil {
		h.send(ctx, b, chatID, "❌ "+err.Error(), nil)
		return
	}
	h.send(ctx, b, chatID, formatExportPreview(period, preview, cs.Imputations), exportPreviewKeyboard(period))
}

// formatExportPreview resume en el chat las primeras filas del mismo conjunto que entrará en el ZIP.
func formatExportPreview(period string, preview marangatu.Preview, imp store.Imputations) string {
	noun := "comprobantes"
	if len(preview.Invoices) == 1 {
		noun = "comprobante"
	}

	var text strings.Builder
	fmt.Fprintf(&text, "📋 Previa — %s\n\n", periodTitle(period))
	fmt.Fprintf(&text, "%d %s listos para Marangatu\n", len(preview.Invoices), noun)
	fmt.Fprintf(&text, "Total: %s Gs\n", formatGs(preview.Total))
	fmt.Fprintf(&text, "Imputación: %s\n", formatImputations(imp))

	for index, inv := range preview.Invoices[:min(len(preview.Invoices), exportPreviewLimit)] {
		fmt.Fprintf(&text, "\n%d. %s · %s · %s · %s · %s Gs",
			index+1, displayDate(inv.Date), previewIssuer(inv.IssuerName), inv.IssuerRUC, inv.Number, formatGs(inv.Total))
	}
	if remaining := len(preview.Invoices) - exportPreviewLimit; remaining > 0 {
		fmt.Fprintf(&text, "\n\n… y %d más. Descargá CSV o Excel para ver todo.", remaining)
	}
	if skipped := formatSkipped(preview.Skipped); skipped != "" {
		text.WriteString("\n\n" + skipped)
	}
	return text.String()
}

func previewIssuer(name string) string {
	const maxRunes = 40
	clean := strings.Join(strings.Fields(name), " ")
	runes := []rune(clean)
	if len(runes) > maxRunes {
		return string(runes[:maxRunes-1]) + "…"
	}
	return clean
}

type preparedExport struct {
	settings marangatu.Settings
	invoices []invoice.Invoice
	preview  marangatu.Preview
}

func (h *handler) prepareExport(ctx context.Context, chatID int64, period string) (preparedExport, error) {
	cs, err := h.deps.Store.Settings(ctx, chatID)
	if err != nil {
		return preparedExport{}, err
	}
	settings := marangatu.Settings{
		RUC: cs.RUC, ImputeIVA: cs.Imputations.IVA, ImputeIRE: cs.Imputations.IRE, ImputeIRP: cs.Imputations.IRP,
	}
	invoices, err := h.deps.Store.SavedInvoices(ctx, chatID, period)
	if err != nil {
		return preparedExport{}, err
	}
	preview, err := marangatu.PreviewPurchases(invoices, settings, period)
	if err != nil {
		return preparedExport{}, err
	}
	return preparedExport{settings: settings, invoices: invoices, preview: preview}, nil
}

func (h *handler) handleExportCallback(ctx context.Context, b *bot.Bot, query *models.CallbackQuery, msg *models.Message) {
	callback, err := parseExportCallback(query.Data)
	if err != nil {
		h.answer(ctx, b, query.ID, NoLongerEditableAlert, false)
		return
	}
	press := buttonPress{queryID: query.ID, chatID: msg.Chat.ID, messageID: msg.ID}
	if callback.action == exportActionCancel {
		h.editText(ctx, b, press, "❌ Exportación cancelada.", noKeyboard())
		h.answer(ctx, b, query.ID, "", false)
		return
	}

	prepared, err := h.prepareExport(ctx, press.chatID, callback.period)
	if err != nil {
		h.logger.Error("no se pudo reconstruir la exportación", "chat_id", press.chatID, "periodo", callback.period, "error", err)
		h.answer(ctx, b, query.ID, "La previa ya no está disponible. Usá /exportar de nuevo.", true)
		return
	}

	switch callback.action {
	case exportActionCSV:
		file, err := prepared.preview.CSV()
		if err == nil {
			err = h.sendReviewFile(ctx, b, press.chatID, file, "CSV")
		}
		if err != nil {
			h.exportCallbackError(ctx, b, query.ID, press.chatID, err)
			return
		}
		h.track(ctx, press.chatID, store.Event{Kind: store.EventExportReview, Detail: store.EventDetailCSV})
		h.answer(ctx, b, query.ID, "CSV listo", false)
	case exportActionExcel:
		file, err := prepared.preview.XLSX()
		if err == nil {
			err = h.sendReviewFile(ctx, b, press.chatID, file, "Excel")
		}
		if err != nil {
			h.exportCallbackError(ctx, b, query.ID, press.chatID, err)
			return
		}
		h.track(ctx, press.chatID, store.Event{Kind: store.EventExportReview, Detail: store.EventDetailExcel})
		h.answer(ctx, b, query.ID, "Excel listo", false)
	case exportActionZIP:
		h.sendConfirmedZIP(ctx, b, press, callback.period, prepared)
	}
}

func (h *handler) sendReviewFile(ctx context.Context, b *bot.Bot, chatID int64, file marangatu.ReviewFile, format string) error {
	_, err := b.SendDocument(ctx, &bot.SendDocumentParams{
		ChatID:   chatID,
		Document: &models.InputFileUpload{Filename: file.FileName, Data: bytes.NewReader(file.Data)},
		Caption:  "🔎 Archivo " + format + " para revisar. No lo subas a Marangatu; el archivo oficial es el ZIP.",
	})
	return err
}

func (h *handler) sendConfirmedZIP(ctx context.Context, b *bot.Bot, press buttonPress, period string, prepared preparedExport) {
	seq, err := h.deps.Store.NextExportSeq(ctx, press.chatID, period)
	if err != nil {
		h.exportCallbackError(ctx, b, press.queryID, press.chatID, err)
		return
	}
	export, err := marangatu.BuildPurchases(prepared.invoices, prepared.settings, period, marangatu.FileID(seq))
	if err != nil {
		h.exportCallbackError(ctx, b, press.queryID, press.chatID, err)
		return
	}

	_, err = b.SendDocument(ctx, &bot.SendDocumentParams{
		ChatID:   press.chatID,
		Document: &models.InputFileUpload{Filename: export.FileName, Data: bytes.NewReader(export.Zip)},
		Caption:  exportCaption(period, export),
	})
	if err != nil {
		h.exportCallbackError(ctx, b, press.queryID, press.chatID, err)
		return
	}
	h.track(ctx, press.chatID, store.Event{Kind: store.EventExportZIP})
	h.editKeyboard(ctx, b, press, noKeyboard())
	h.answer(ctx, b, press.queryID, "ZIP listo", false)
	h.logger.Info("exportación enviada", "chat_id", press.chatID, "periodo", period, "filas", export.Rows, "omitidas", len(export.Skipped))
}

func (h *handler) exportCallbackError(ctx context.Context, b *bot.Bot, queryID string, chatID int64, err error) {
	h.logger.Error("no se pudo generar o enviar el archivo", "chat_id", chatID, "error", Redact(err, h.deps.Token))
	h.answer(ctx, b, queryID, StoreErrorMessage, true)
}

// exportCaption explica qué hay en el archivo y qué quedó afuera.
func exportCaption(period string, export marangatu.Export) string {
	noun := "comprobantes"
	if export.Rows == 1 {
		noun = "comprobante"
	}
	caption := fmt.Sprintf("📤 %d %s de %s listos para Marangatu.\n%s", export.Rows, noun, periodTitle(period), uploadHint)
	if len(export.Skipped) > 0 {
		caption += "\n\n" + formatSkipped(export.Skipped)
	}
	return caption
}

func formatSkipped(skipped []marangatu.Skipped) string {
	if len(skipped) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Quedaron afuera:\n")
	for _, s := range skipped {
		fmt.Fprintf(&b, "• %s: %s\n", s.Number, s.Reason)
	}
	return strings.TrimSuffix(b.String(), "\n")
}
