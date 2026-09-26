package telegram

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

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
)

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

// exportMonth arma el archivo para Marangatu y lo envía como documento.
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

	seq, err := h.deps.Store.NextExportSeq(ctx, chatID, period)
	if err != nil {
		h.logger.Error("no se pudo numerar la exportación", "chat_id", chatID, "error", err)
		h.send(ctx, b, chatID, StoreErrorMessage, nil)
		return
	}
	settings := marangatu.Settings{
		RUC: cs.RUC, ImputeIVA: cs.Imputations.IVA, ImputeIRE: cs.Imputations.IRE, ImputeIRP: cs.Imputations.IRP,
	}
	export, err := marangatu.BuildPurchases(invoices, settings, period, marangatu.FileID(seq))
	if errors.Is(err, marangatu.ErrNothingToExport) {
		h.send(ctx, b, chatID, "No hay facturas para importar de "+periodTitle(period)+".\n"+formatSkipped(export.Skipped), nil)
		return
	}
	if err != nil {
		h.send(ctx, b, chatID, "❌ "+err.Error(), nil)
		return
	}

	_, err = b.SendDocument(ctx, &bot.SendDocumentParams{
		ChatID:   chatID,
		Document: &models.InputFileUpload{Filename: export.FileName, Data: bytes.NewReader(export.Zip)},
		Caption:  exportCaption(period, export),
	})
	if err != nil {
		h.logger.Error("no se pudo enviar el archivo", "chat_id", chatID, "error", Redact(err, h.deps.Token))
		h.send(ctx, b, chatID, StoreErrorMessage, nil)
		return
	}
	h.logger.Info("exportación enviada", "chat_id", chatID, "periodo", period, "filas", export.Rows, "omitidas", len(export.Skipped))
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
