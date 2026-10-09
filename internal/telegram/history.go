package telegram

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/mmallorquin/bot-marangatu-facturas/internal/invoice"
	"github.com/mmallorquin/bot-marangatu-facturas/internal/marangatu"
	"github.com/mmallorquin/bot-marangatu-facturas/internal/store"
)

const historyCallbackPrefix = "h"

// h:l:<p|s>:<period>:<page>, or h:<o|e>:<p|s>:<period>:<page>:<id>.
// Pending includes all drafts, including invoices whose date still needs correction.
type historyCallback struct {
	action string
	tab    string
	period string
	page   int
	id     int64
}

func (c historyCallback) encode() string {
	data := fmt.Sprintf("h:%s:%s:%s:%d", c.action, c.tab, c.period, c.page)
	if c.action != "l" {
		data += fmt.Sprintf(":%d", c.id)
	}
	return data
}

func parseHistoryCallback(data string, now time.Time) (historyCallback, error) {
	parts := strings.Split(data, ":")
	if len(parts) < 5 || parts[0] != historyCallbackPrefix || (parts[2] != "p" && parts[2] != "s") {
		return historyCallback{}, errInvalidCallback
	}
	c := historyCallback{action: parts[1], tab: parts[2], period: parts[3]}
	if !validHistoryPeriod(c.period, now) {
		return historyCallback{}, errInvalidCallback
	}
	page, err := strconv.Atoi(parts[4])
	if err != nil || page < 0 || page > 9999 {
		return historyCallback{}, errInvalidCallback
	}
	c.page = page
	if c.action == "l" && len(parts) == 5 {
		return c, nil
	}
	if (c.action != "o" && c.action != "e") || len(parts) != 6 || (c.action == "e" && c.tab != "s") {
		return historyCallback{}, errInvalidCallback
	}
	c.id, err = strconv.ParseInt(parts[5], 10, 64)
	if err != nil || c.id <= 0 {
		return historyCallback{}, errInvalidCallback
	}
	return c, nil
}

func validHistoryPeriod(period string, _ time.Time) bool {
	// Match the periods already accepted by invoice storage. An OCR date error
	// must not make a saved invoice inaccessible for correction.
	_, err := marangatu.ParsePeriod(period)
	return err == nil
}

func historyButton(label string, c historyCallback) models.InlineKeyboardButton {
	return models.InlineKeyboardButton{Text: label, CallbackData: c.encode()}
}

func (h *handler) historyList(ctx context.Context, chatID int64, c historyCallback) (string, *models.InlineKeyboardMarkup, error) {
	now := h.deps.Now().In(reminderLocation)
	if !validHistoryPeriod(c.period, now) {
		return "", nil, errInvalidCallback
	}
	drafts, err := h.deps.Store.Drafts(ctx, chatID)
	if err != nil {
		return "", nil, err
	}
	saved, err := h.deps.Store.SavedRecords(ctx, chatID, c.period)
	if err != nil {
		return "", nil, err
	}
	sum, err := h.deps.Store.MonthSummary(ctx, chatID, c.period)
	if err != nil {
		return "", nil, err
	}
	if c.tab == "" {
		c.tab = "s"
		if len(drafts) > 0 {
			c.tab = "p"
		}
	}
	var records []store.SavedRecord
	if c.tab == "p" {
		for _, rec := range drafts {
			records = append(records, store.SavedRecord{ID: rec.ID, Invoice: rec.Invoice})
		}
	} else {
		records = saved
	}
	slices.Reverse(records)
	pages := max(1, (len(records)+listLimit-1)/listLimit)
	c.page = min(c.page, pages-1)
	start, end := c.page*listLimit, min((c.page+1)*listLimit, len(records))
	var text strings.Builder
	fmt.Fprintf(&text, "📂 Mis facturas · %s\n\n%d %s · %d %s\nTotal guardado: %s Gs · IVA: %s Gs\n",
		periodTitle(c.period), len(drafts), plural(len(drafts), "pendiente", "pendientes"),
		len(saved), plural(len(saved), "factura guardada", "facturas guardadas"), formatGs(sum.Total), formatGs(sum.VAT5+sum.VAT10))
	if sum.HasDuplicates {
		text.WriteString("\n⚠️ Los totales incluyen duplicados. Abrí las copias y quitá la sobrante antes de exportar.\n")
	}
	if c.tab == "p" {
		text.WriteString("\n📥 Pendientes de todos los períodos. Abrí una para revisar, corregir o guardar.\n")
	} else {
		text.WriteString("\n💾 Guardadas del período. Abrí una para ver sus datos.\n")
	}
	rows := [][]models.InlineKeyboardButton{{
		historyButton(fmt.Sprintf("📥 Pendientes (%d)", len(drafts)), historyCallback{action: "l", tab: "p", period: c.period}),
		historyButton(fmt.Sprintf("💾 Guardadas (%d)", len(saved)), historyCallback{action: "l", tab: "s", period: c.period}),
	}}
	if len(records) == 0 {
		if c.tab == "p" {
			text.WriteString("\n✅ No tenés facturas pendientes de guardar.")
		} else {
			fmt.Fprintf(&text, "\nNo hay facturas guardadas de %s.", periodTitle(c.period))
		}
	}
	for index, rec := range records[start:end] {
		inv, number := rec.Invoice, start+index+1
		marker := ""
		if c.tab == "p" && !invoice.Clean(inv) {
			marker = " · ⚠️ Revisar"
		}
		fmt.Fprintf(&text, "\n%d. %s · %s · %s · %s Gs%s", number, historyValue(displayDate(inv.Date), 24), previewIssuer(inv.IssuerName), historyValue(inv.Number, 30), formatGs(inv.Total), marker)
		open := c
		open.action, open.id = "o", rec.ID
		rows = append(rows, []models.InlineKeyboardButton{historyButton(fmt.Sprintf("Abrir %d · %s", number, previewIssuer(inv.IssuerName)), open)})
	}
	if pages > 1 {
		fmt.Fprintf(&text, "\n\nPágina %d de %d", c.page+1, pages)
		var row []models.InlineKeyboardButton
		if c.page > 0 {
			previous := c
			previous.action, previous.page = "l", c.page-1
			row = append(row, historyButton("← Anteriores", previous))
		}
		if c.page+1 < pages {
			next := c
			next.action, next.page = "l", c.page+1
			row = append(row, historyButton("Más →", next))
		}
		rows = append(rows, row)
	}
	rows = append(rows, historyPeriodRows(c.period, now)...)
	return text.String(), &models.InlineKeyboardMarkup{InlineKeyboard: rows}, nil
}

// Invalid OCR data still belongs in the pending list, but must not hide its buttons.
func historyValue(value string, limit int) string {
	runes := []rune(strings.Join(strings.Fields(value), " "))
	if len(runes) > limit {
		return string(runes[:limit-1]) + "…"
	}
	return string(runes)
}

func historyPeriodRows(period string, now time.Time) [][]models.InlineKeyboardButton {
	parsed, _ := marangatu.ParsePeriod(period)
	step, layout, unit := 0, "2006-01", "Mes"
	if parsed.Annual {
		step, layout, unit = 1, "2006", "Año"
	}
	previous, next := parsed.Start.AddDate(-step, -(1-step), 0), parsed.Start.AddDate(step, 1-step, 0)
	var row []models.InlineKeyboardButton
	for _, target := range []struct{ label, period string }{
		{"← " + unit, previous.Format(layout)}, {unit + " →", next.Format(layout)},
	} {
		if validHistoryPeriod(target.period, now) {
			row = append(row, historyButton(target.label, historyCallback{action: "l", tab: "s", period: target.period}))
		}
	}
	rows := [][]models.InlineKeyboardButton{row}
	if parsed.Annual {
		month := fmt.Sprintf("%04d-%02d", parsed.Start.Year(), now.Month())
		rows = append(rows, []models.InlineKeyboardButton{historyButton("Ver un mes", historyCallback{action: "l", tab: "s", period: month}), historyButton("Mes actual", historyCallback{action: "l", tab: "s", period: now.Format("2006-01")})})
	} else {
		rows = append(rows, []models.InlineKeyboardButton{historyButton("Ver todo el año", historyCallback{action: "l", tab: "s", period: period[:4]}), historyButton("Mes actual", historyCallback{action: "l", tab: "s", period: now.Format("2006-01")})})
	}
	return rows
}

func (h *handler) handleHistoryCallback(ctx context.Context, b *bot.Bot, query *models.CallbackQuery, msg *models.Message) {
	c, err := parseHistoryCallback(query.Data, h.deps.Now().In(reminderLocation))
	if err != nil {
		h.answer(ctx, b, query.ID, "Este botón no está disponible. Abrí Mis facturas de nuevo.", true)
		return
	}
	press := buttonPress{queryID: query.ID, chatID: msg.Chat.ID, messageID: msg.ID}
	if c.action == "l" {
		text, keyboard, err := h.historyList(ctx, press.chatID, c)
		if err != nil {
			h.answer(ctx, b, query.ID, StoreErrorMessage, true)
			return
		}
		h.editText(ctx, b, press, text, keyboard)
		h.answer(ctx, b, query.ID, "", false)
		return
	}
	rec, err := h.deps.Store.Get(ctx, press.chatID, c.id)
	expected := store.StatusSaved
	if c.tab == "p" {
		expected = store.StatusDraft
	}
	if err != nil || rec.Status != expected {
		h.answer(ctx, b, query.ID, "La factura cambió. Abrí Mis facturas de nuevo.", true)
		return
	}
	if c.action == "e" {
		if err := h.deps.Store.Unsave(ctx, press.chatID, c.id); err != nil {
			h.answer(ctx, b, query.ID, StoreErrorMessage, true)
			return
		}
		h.track(ctx, press.chatID, store.Event{Kind: store.EventUndo})
		c.tab = "p"
		text := FormatInvoice(rec.Invoice, invoice.Validate(rec.Invoice)) + "\n\n✏️ La factura quedó pendiente. Corregí los datos y tocá Guardar de nuevo."
		keyboard := fieldsKeyboard(c.id)
		back := c
		back.action, back.id = "l", 0
		keyboard.InlineKeyboard = append(keyboard.InlineKeyboard, []models.InlineKeyboardButton{historyButton("← Mis facturas", back)})
		h.editText(ctx, b, press, text, keyboard)
		h.answer(ctx, b, query.ID, "", false)
		return
	}
	text := FormatInvoice(rec.Invoice, invoice.Validate(rec.Invoice))
	keyboard := mainKeyboard(c.id)
	if c.tab == "s" {
		if rec.Invoice.CDC != "" {
			text += "\n\n" + ElectronicNote
		}
		text += "\n\n" + SavedNote
		edit := c
		edit.action = "e"
		keyboard = &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{
			historyButton("✏️ Corregir", edit),
			{Text: "🗑️ Borrar", CallbackData: listCallback{action: listActionAsk, id: c.id, period: c.period}.encode()},
		}}}
	} else {
		text, _ = h.invoiceMessage(ctx, press.chatID, rec.Invoice, invoice.Validate(rec.Invoice))
	}
	back := c
	back.action, back.id = "l", 0
	keyboard.InlineKeyboard = append(keyboard.InlineKeyboard, []models.InlineKeyboardButton{historyButton("← Mis facturas", back)})
	h.editText(ctx, b, press, text, keyboard)
	h.answer(ctx, b, query.ID, "", false)
}
