package telegram

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf16"

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

	exportCallbackPrefix  = "x"
	exportActionCSV       = "c"
	exportActionExcel     = "e"
	exportActionZIP       = "z"
	exportActionCancel    = "n"
	exportPreviewLimit    = 10
	deliveryRecordTimeout = 5 * time.Second
)

type exportCallback struct {
	action    string
	period    string
	revision  string
	requestID string
}

func (c exportCallback) encode() string {
	return fmt.Sprintf("%s:%s:%s:%s:%s", exportCallbackPrefix, c.action, c.period, c.revision, c.requestID)
}

func parseExportCallback(data string) (exportCallback, error) {
	parts := strings.Split(data, ":")
	if (len(parts) < 3 || len(parts) > 5) || parts[0] != exportCallbackPrefix ||
		!slices.Contains([]string{exportActionCSV, exportActionExcel, exportActionZIP, exportActionCancel}, parts[1]) {
		return exportCallback{}, errInvalidCallback
	}
	if _, err := marangatu.ParsePeriod(parts[2]); err != nil {
		return exportCallback{}, errInvalidCallback
	}
	c := exportCallback{action: parts[1], period: parts[2]}
	if len(parts) >= 4 {
		c.revision = parts[3]
	}
	if len(parts) == 5 {
		c.requestID = parts[4]
	}
	return c, nil
}

func exportPreviewKeyboard(period, revision, requestID string) *models.InlineKeyboardMarkup {
	button := func(text, action string) models.InlineKeyboardButton {
		return models.InlineKeyboardButton{Text: text, CallbackData: exportCallback{action: action, period: period, revision: revision, requestID: requestID}.encode()}
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
	h.saveRUC(ctx, b, chatID, ruc)
}

func (h *handler) setImputations(ctx context.Context, b *bot.Bot, chatID int64, text string) {
	if len(strings.Fields(text)) < 2 {
		h.askImputations(ctx, b, chatID)
		return
	}
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
	message, keyboard := h.imputationReply(ctx, chatID, imp)
	h.send(ctx, b, chatID, message, keyboard)
	h.resumeConfiguredExport(ctx, b, chatID)
}

// exportMonth muestra una previa; el ZIP se arma recién cuando el usuario lo confirma.
func (h *handler) exportMonth(ctx context.Context, b *bot.Bot, chatID int64, text string) {
	now := h.deps.Now().In(reminderLocation)
	period, err := parsePeriod(text, now)
	if err != nil {
		h.send(ctx, b, chatID, "❌ "+err.Error(), nil)
		return
	}
	if len(strings.Fields(text)) == 1 {
		period = "select:" + period + ":" + annualYearToFile(now)
	}
	h.startExport(ctx, b, chatID, period)
}

func (h *handler) showExportPreview(ctx context.Context, b *bot.Bot, chatID int64, period string, cs store.ChatSettings) {
	invoices, err := h.deps.Store.SavedInvoices(ctx, chatID, period)
	if err != nil {
		h.logger.Error("no se pudieron leer las facturas", "chat_id", chatID, "error", err)
		h.send(ctx, b, chatID, StoreErrorMessage, nil)
		return
	}
	if len(invoices) == 0 {
		h.send(ctx, b, chatID, fmt.Sprintf("No hay facturas guardadas de %s.\n\n%s", periodTitle(period), noMovementHint), nil)
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
		message := fmt.Sprintf("📋 %s\n\nTenés %d %s, pero ninguno entra en el ZIP.\n\n%s\n\nSiguen guardados y aparecen en /resumen.\n\n%s\n%s",
			periodTitle(period), len(invoices), noun, formatSkipped(preview.Skipped), presentationHint, retentionHint)
		h.send(ctx, b, chatID, message, nil)
		return
	}
	if err != nil {
		h.send(ctx, b, chatID, "❌ "+err.Error(), nil)
		return
	}
	keyboard, err := h.newExportKeyboard(ctx, chatID, period, exportRevision(settings, preview, cs.Registration))
	if err != nil {
		h.logger.Error("no se pudo registrar la previa", "chat_id", chatID, "error", err)
		h.send(ctx, b, chatID, StoreErrorMessage, nil)
		return
	}
	revision := exportRevision(settings, preview, cs.Registration)
	h.send(ctx, b, chatID, formatExportPreview(period, preview, cs.Imputations, cs.Registration)+h.presentationNote(ctx, chatID, period, revision), keyboard)
}

func (h *handler) newExportKeyboard(ctx context.Context, chatID int64, period, revision string) (*models.InlineKeyboardMarkup, error) {
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	requestID := fmt.Sprintf("%x", nonce)
	if err := h.deps.Store.CreateExportPreview(ctx, chatID, period, requestID, revision); err != nil {
		return nil, err
	}
	return exportPreviewKeyboard(period, revision, requestID), nil
}

// formatExportPreview resume en el chat las primeras filas del mismo conjunto que entrará en el ZIP.
func formatExportPreview(period string, preview marangatu.Preview, imp store.Imputations, registration store.Registration) string {
	noun := "comprobantes"
	if len(preview.Invoices) == 1 {
		noun = "comprobante"
	}

	var text strings.Builder
	fmt.Fprintf(&text, "📋 Previa — %s\n\n", periodTitle(period))
	fmt.Fprintf(&text, "%d %s %s para Marangatu\n", len(preview.Invoices), noun, readyWord(len(preview.Invoices)))
	fmt.Fprintf(&text, "Total: %s Gs\n", formatGs(preview.Total))
	fmt.Fprintf(&text, "Imputación: %s\n", formatImputations(imp))
	fmt.Fprintf(&text, "Registro configurado: %s\n", registrationLabel(registration))

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
	if note := periodNote(period, registration == store.RegistrationAnnual); note != "" {
		text.WriteString("\n\n" + note)
	}
	if !registration.Valid() {
		text.WriteString("\n\nAntes de generar el ZIP, elegí tu obligación con /registro. CSV y Excel siguen disponibles para revisar.")
	} else if warning := wrongRegistrationPeriod(registration, period); warning != "" {
		text.WriteString("\n\n" + warning)
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
	settings     marangatu.Settings
	invoices     []invoice.Invoice
	preview      marangatu.Preview
	registration store.Registration
}

// exportRevision binds every action to the data and settings shown in its preview.
func exportRevision(settings marangatu.Settings, preview marangatu.Preview, registration store.Registration) string {
	data, _ := json.Marshal(struct {
		Settings     marangatu.Settings
		Preview      marangatu.Preview
		Registration store.Registration
	}{settings, preview, registration})
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum[:16])
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
	return preparedExport{settings: settings, invoices: invoices, preview: preview, registration: cs.Registration}, nil
}

func (h *handler) handleExportCallback(ctx context.Context, b *bot.Bot, query *models.CallbackQuery, msg *models.Message) {
	callback, err := parseExportCallback(query.Data)
	if err != nil {
		h.answer(ctx, b, query.ID, NoLongerEditableAlert, false)
		return
	}
	press := buttonPress{queryID: query.ID, chatID: msg.Chat.ID, messageID: msg.ID}
	delivered, err := h.deps.Store.ExportRequestStatus(ctx, press.chatID, callback.period, callback.requestID)
	if delivered || errors.Is(err, store.ErrExportExpired) || errors.Is(err, store.ErrExportCancelled) {
		h.editKeyboard(ctx, b, press, noKeyboard())
		h.answer(ctx, b, query.ID, "Esta exportación ya terminó o no está disponible. Usá /exportar de nuevo.", true)
		return
	}
	if err != nil {
		h.exportCallbackError(ctx, b, query.ID, press.chatID, err)
		return
	}
	if callback.action == exportActionCancel {
		if err := h.deps.Store.CancelExport(ctx, press.chatID, callback.period, callback.requestID); err != nil {
			h.answer(ctx, b, query.ID, "Esta exportación ya terminó o no está disponible. Usá /exportar de nuevo.", true)
			return
		}
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

	revision := exportRevision(prepared.settings, prepared.preview, prepared.registration)
	if callback.revision != revision {
		imp := store.Imputations{IVA: prepared.settings.ImputeIVA, IRE: prepared.settings.ImputeIRE, IRP: prepared.settings.ImputeIRP}
		keyboard, err := h.newExportKeyboard(ctx, press.chatID, callback.period, revision)
		if err != nil {
			h.exportCallbackError(ctx, b, query.ID, press.chatID, err)
			return
		}
		if err := h.deps.Store.CancelExport(ctx, press.chatID, callback.period, callback.requestID); err != nil {
			h.exportCallbackError(ctx, b, query.ID, press.chatID, err)
			return
		}
		h.editText(ctx, b, press, formatExportPreview(callback.period, prepared.preview, imp, prepared.registration)+h.presentationNote(ctx, press.chatID, callback.period, revision), keyboard)
		h.answer(ctx, b, query.ID, "La previa cambió. Revisala y confirmá de nuevo.", true)
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
		if !prepared.registration.Valid() {
			h.send(ctx, b, press.chatID, "Antes de generar el ZIP, elegí tu obligación con /registro.\n\n"+registrationPrompt, registrationKeyboard(prepared.settings.RUC))
			h.answer(ctx, b, query.ID, "Falta elegir el registro 955 o 956 con /registro.", true)
			return
		}
		if warning := wrongRegistrationPeriod(prepared.registration, callback.period); warning != "" {
			h.answer(ctx, b, query.ID, warning, true)
			return
		}
		h.sendConfirmedZIP(ctx, b, press, callback.period, callback.requestID, prepared)
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

func (h *handler) sendConfirmedZIP(ctx context.Context, b *bot.Bot, press buttonPress, period, requestID string, prepared preparedExport) {
	seq, delivered, err := h.deps.Store.ReserveExport(ctx, press.chatID, period, requestID)
	if errors.Is(err, store.ErrExportExpired) || errors.Is(err, store.ErrExportCancelled) {
		h.answer(ctx, b, press.queryID, "Esta exportación ya terminó o no está disponible. Usá /exportar de nuevo.", true)
		return
	}
	if err != nil {
		h.exportCallbackError(ctx, b, press.queryID, press.chatID, err)
		return
	}
	if delivered {
		h.editKeyboard(ctx, b, press, noKeyboard())
		h.answer(ctx, b, press.queryID, "Este ZIP ya fue enviado.", false)
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
		Caption:  exportCaption(period, export, prepared.registration == store.RegistrationAnnual),
	})
	if err != nil {
		h.exportCallbackError(ctx, b, press.queryID, press.chatID, err)
		return
	}
	// Telegram already acknowledged delivery. Record that acknowledgement even
	// if the caller cancelled; the shared chat lock still prevents a deletion race.
	recordCtx, cancelRecord := context.WithTimeout(context.WithoutCancel(ctx), deliveryRecordTimeout)
	defer cancelRecord()
	if err := h.deps.Store.MarkExportDelivered(recordCtx, press.chatID, period, requestID); err != nil {
		h.logger.Error("el ZIP llegó pero no se pudo registrar su entrega", "chat_id", press.chatID, "periodo", period, "error", err)
		h.editKeyboard(ctx, b, press, noKeyboard())
		h.answer(ctx, b, press.queryID, "ZIP enviado. No se pudo registrar la entrega; revisá el archivo recibido.", true)
		return
	}
	h.track(ctx, press.chatID, store.Event{Kind: store.EventExportZIP})
	h.editKeyboard(ctx, b, press, noKeyboard())
	h.answer(ctx, b, press.queryID, "ZIP listo", false)
	h.send(ctx, b, press.chatID, "📤 ZIP entregado. Esto todavía no confirma la presentación del período en Marangatu. Cuando obtengas el Talón, podés marcarlo acá.",
		presentationKeyboard("ask", period, requestID, "Ya presenté en Marangatu"))
	h.logger.Info("exportación enviada", "chat_id", press.chatID, "periodo", period, "filas", export.Rows, "omitidas", len(export.Skipped))
}

func (h *handler) exportCallbackError(ctx context.Context, b *bot.Bot, queryID string, chatID int64, err error) {
	h.logger.Error("no se pudo generar o enviar el archivo", "chat_id", chatID, "error", Redact(err, h.deps.Token))
	h.answer(ctx, b, queryID, StoreErrorMessage, true)
}

// exportCaption explica qué hay en el archivo y qué quedó afuera.
func exportCaption(period string, export marangatu.Export, mayFileAnnually bool) string {
	noun := "comprobantes"
	if export.Rows == 1 {
		noun = "comprobante"
	}
	caption := fmt.Sprintf("📤 %d %s de %s %s para Marangatu.\n%s\n\n%s\n%s", export.Rows, noun, periodTitle(period), readyWord(export.Rows), uploadHint, presentationHint, retentionHint)
	if note := periodNote(period, mayFileAnnually); note != "" {
		caption += "\n\n" + note
	}
	if len(export.Skipped) > 0 {
		// Telegram limita el texto de un documento a 1024 caracteres. Las
		// instrucciones fiscales siempre quedan; acotamos solo el detalle opcional.
		caption += "\n\nQuedaron afuera:"
		for index, skipped := range export.Skipped {
			line := fmt.Sprintf("\n• %s — %s", skipped.Number, skipped.Reason)
			remaining := len(export.Skipped) - index - 1
			suffix := ""
			if remaining > 0 {
				suffix = fmt.Sprintf("\n… y %d más; revisá la previa.", remaining)
			}
			if len(utf16.Encode([]rune(caption+line+suffix))) > 1024 {
				caption += fmt.Sprintf("\n… y %d más; revisá la previa.", remaining+1)
				break
			}
			caption += line
		}
	}
	return caption
}

func readyWord(n int) string {
	if n == 1 {
		return "listo"
	}
	return "listos"
}

// El registro anual del IRP-RSP se presenta hasta febrero del año siguiente: en enero y febrero
// el archivo que corresponde es el del año anterior.
const lastMonthForPreviousYear = time.February

// annualYearToFile es el año cuyo archivo anual conviene sugerir en esta fecha.
func annualYearToFile(now time.Time) string {
	if now.Month() <= lastMonthForPreviousYear {
		return now.AddDate(-1, 0, 0).Format(yearLayout)
	}
	return now.Format(yearLayout)
}

// annualHint explica cómo generar el archivo para el registro anual confirmado.
func annualHint(year string) string {
	return fmt.Sprintf("ℹ️ Para tu registro anual (956), usá /exportar %s para el archivo del año.", year)
}

// periodNote aclara qué tipo de archivo es: el anual siempre, el mensual solo a quien puede presentar anual.
func periodNote(period string, mayFileAnnually bool) string {
	if isAnnual(period) {
		return fmt.Sprintf("ℹ️ Archivo anual (%s), para la obligación 956. Para revisar un mes usá /exportar 09/%s.", period, period)
	}
	if mayFileAnnually {
		return annualHint(period[:len(yearLayout)])
	}
	return ""
}

func formatSkipped(skipped []marangatu.Skipped) string {
	if len(skipped) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Quedaron afuera:\n")
	// Las excluidas también se acotan: un mes con muchas electrónicas no
	// debe impedir que Telegram entregue la previa o las instrucciones fiscales.
	for _, s := range skipped[:min(len(skipped), exportPreviewLimit)] {
		fmt.Fprintf(&b, "• %s — %s\n", s.Number, s.Reason)
	}
	if remaining := len(skipped) - exportPreviewLimit; remaining > 0 {
		fmt.Fprintf(&b, "… y %d más excluidos.\n", remaining)
	}
	return strings.TrimSuffix(b.String(), "\n")
}
