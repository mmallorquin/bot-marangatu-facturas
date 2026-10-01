package telegram

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/mmallorquin/bot-marangatu-facturas/internal/marangatu"
	"github.com/mmallorquin/bot-marangatu-facturas/internal/reader"
	"github.com/mmallorquin/bot-marangatu-facturas/internal/store"
)

const exportFlowPrefix = "ep"

// startExport conserva el período antes de pedir configuración; nunca genera un ZIP solo.
func (h *handler) startExport(ctx context.Context, b *bot.Bot, chatID int64, intent string) {
	cs, err := h.deps.Store.Settings(ctx, chatID)
	if err != nil {
		h.send(ctx, b, chatID, StoreErrorMessage, nil)
		return
	}
	if cs.RUC == "" || cs.Imputations == (store.Imputations{}) || !cs.Registration.Valid() {
		if err := h.deps.Store.SetPendingExport(ctx, chatID, intent); err != nil {
			h.send(ctx, b, chatID, StoreErrorMessage, nil)
			return
		}
		h.askExportSetup(ctx, b, chatID, cs)
		return
	}
	h.completeExportIntent(ctx, b, chatID, intent, cs)
}

func (h *handler) askExportSetup(ctx context.Context, b *bot.Bot, chatID int64, cs store.ChatSettings) {
	switch {
	case cs.RUC == "":
		h.beginRUCSetup(ctx, b, chatID)
	case cs.Imputations == (store.Imputations{}):
		h.askImputations(ctx, b, chatID)
	default:
		h.askRegistration(ctx, b, chatID, cs)
	}
}

func (h *handler) beginRUCSetup(ctx context.Context, b *bot.Bot, chatID int64) {
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		h.send(ctx, b, chatID, StoreErrorMessage, nil)
		return
	}
	key := fmt.Sprintf("%x", nonce)
	if err := h.deps.Store.ClearAwaiting(ctx, chatID); err != nil {
		h.send(ctx, b, chatID, StoreErrorMessage, nil)
		return
	}
	if err := h.deps.Store.SetAwaitingRUC(ctx, chatID, true, key); err != nil {
		h.send(ctx, b, chatID, StoreErrorMessage, nil)
		return
	}
	h.send(ctx, b, chatID, askRUCOnboarding, &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{
		{Text: "Cancelar", CallbackData: exportFlowPrefix + ":cancel:" + key},
	}}})
}

// resumePendingExport devuelve true cuando ya se hizo cargo de la siguiente pantalla.
func (h *handler) resumePendingExport(ctx context.Context, b *bot.Bot, chatID int64) bool {
	cs, err := h.deps.Store.Settings(ctx, chatID)
	if err != nil || cs.PendingExport == "" {
		return false
	}
	if cs.RUC == "" || cs.Imputations == (store.Imputations{}) || !cs.Registration.Valid() {
		h.askExportSetup(ctx, b, chatID, cs)
		return true
	}
	intent := cs.PendingExport
	if err := h.deps.Store.SetPendingExport(ctx, chatID, ""); err != nil {
		h.send(ctx, b, chatID, StoreErrorMessage, nil)
		return true
	}
	h.completeExportIntent(ctx, b, chatID, intent, cs)
	return true
}

// La respuesta de imputación ya ofrece el siguiente ajuste faltante; no lo duplicamos.
func (h *handler) resumeConfiguredExport(ctx context.Context, b *bot.Bot, chatID int64) {
	cs, err := h.deps.Store.Settings(ctx, chatID)
	if err == nil && cs.RUC != "" && cs.Registration.Valid() && cs.Imputations != (store.Imputations{}) {
		h.resumePendingExport(ctx, b, chatID)
	}
}

func (h *handler) completeExportIntent(ctx context.Context, b *bot.Bot, chatID int64, intent string, cs store.ChatSettings) {
	if strings.HasPrefix(intent, "select:") {
		parts := strings.Split(intent, ":")
		if len(parts) != 3 {
			h.send(ctx, b, chatID, "Elegí Exportar de nuevo.", nil)
			return
		}
		period := parts[1]
		if cs.Registration == store.RegistrationAnnual {
			period = parts[2]
		}
		if _, err := marangatu.ParsePeriod(period); err != nil {
			h.send(ctx, b, chatID, "Elegí Exportar de nuevo.", nil)
			return
		}
		h.sendPeriodPicker(ctx, b, chatID, period)
		return
	}
	if _, err := marangatu.ParsePeriod(intent); err != nil {
		h.send(ctx, b, chatID, "Elegí Exportar de nuevo.", nil)
		return
	}
	h.showExportPreview(ctx, b, chatID, intent, cs)
}

func periodPicker(period string) *models.InlineKeyboardMarkup {
	layout := "2006-01"
	if isAnnual(period) {
		layout = yearLayout
	}
	date, _ := time.Parse(layout, period)
	previous, next := date.AddDate(0, -1, 0), date.AddDate(0, 1, 0)
	if isAnnual(period) {
		previous, next = date.AddDate(-1, 0, 0), date.AddDate(1, 0, 0)
	}
	row := []models.InlineKeyboardButton{}
	if previous.Year() >= 1 {
		row = append(row, models.InlineKeyboardButton{Text: "← " + periodTitle(previous.Format(layout)), CallbackData: exportFlowPrefix + ":view:" + previous.Format(layout)})
	}
	if next.Year() <= 9999 {
		row = append(row, models.InlineKeyboardButton{Text: periodTitle(next.Format(layout)) + " →", CallbackData: exportFlowPrefix + ":view:" + next.Format(layout)})
	}
	return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{
		{{Text: "Revisar " + periodTitle(period), CallbackData: exportFlowPrefix + ":use:" + period}},
		row,
	}}
}

func (h *handler) sendPeriodPicker(ctx context.Context, b *bot.Bot, chatID int64, period string) {
	h.send(ctx, b, chatID, "📅 Elegí el período que querés exportar.\n\n"+periodTitle(period), periodPicker(period))
}

func (h *handler) handleExportFlowCallback(ctx context.Context, b *bot.Bot, query *models.CallbackQuery, msg *models.Message) {
	parts := strings.Split(query.Data, ":")
	if len(parts) == 3 && parts[1] == "cancel" {
		cs, err := h.deps.Store.Settings(ctx, msg.Chat.ID)
		if err != nil || !cs.AwaitingRUC || cs.RUCSetupKey == "" || cs.RUCSetupKey != parts[2] {
			h.answer(ctx, b, query.ID, "Esta configuración ya terminó. Usá el menú de nuevo.", true)
			return
		}
		h.stopOnboarding(ctx, msg.Chat.ID)
		h.editText(ctx, b, buttonPress{chatID: msg.Chat.ID, messageID: msg.ID}, "Configuración cancelada. Tus facturas siguen guardadas.", noKeyboard())
		h.answer(ctx, b, query.ID, "", false)
		return
	}
	if len(parts) != 3 || (parts[1] != "view" && parts[1] != "use") {
		h.answer(ctx, b, query.ID, NoLongerEditableAlert, true)
		return
	}
	if _, err := marangatu.ParsePeriod(parts[2]); err != nil {
		h.answer(ctx, b, query.ID, NoLongerEditableAlert, true)
		return
	}
	h.answer(ctx, b, query.ID, "", false)
	if parts[1] == "view" {
		h.editText(ctx, b, buttonPress{chatID: msg.Chat.ID, messageID: msg.ID}, "📅 Elegí el período que querés exportar.\n\n"+periodTitle(parts[2]), periodPicker(parts[2]))
		return
	}
	h.startExport(ctx, b, msg.Chat.ID, parts[2])
}

func readFailureMessage(err error) string {
	var failure reader.ClassifiedError
	if errors.As(err, &failure) {
		return failure.UserMessage()
	}
	return ReadErrorMessage
}

func (h *handler) presentationNote(ctx context.Context, chatID int64, period, revision string) string {
	state, err := h.deps.Store.ExportPresentation(ctx, chatID, period)
	if err != nil || state.ConfirmedAt == "" {
		return ""
	}
	if state.Revision != revision {
		return "\n\n⚠️ Marcaste un ZIP anterior como presentado; las facturas o los ajustes cambiaron. Revisá si corresponde rectificar en Marangatu."
	}
	return fmt.Sprintf("\n\n✅ Presentación marcada por vos para el ZIP V%04d. El bot no verifica la DNIT.", state.Seq)
}

func presentationKeyboard(action, period, requestID, text string) *models.InlineKeyboardMarkup {
	return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{
		{Text: text, CallbackData: "tp:" + action + ":" + period + ":" + requestID},
	}}}
}

func (h *handler) handlePresentationCallback(ctx context.Context, b *bot.Bot, query *models.CallbackQuery, msg *models.Message) {
	parts := strings.Split(query.Data, ":")
	if len(parts) != 4 || (parts[1] != "ask" && parts[1] != "yes") {
		h.answer(ctx, b, query.ID, NoLongerEditableAlert, true)
		return
	}
	if _, err := marangatu.ParsePeriod(parts[2]); err != nil {
		h.answer(ctx, b, query.ID, NoLongerEditableAlert, true)
		return
	}
	revision, err := h.deps.Store.ExportDeliveredRevision(ctx, msg.Chat.ID, parts[2], parts[3])
	if err != nil {
		h.answer(ctx, b, query.ID, "Ese ZIP no está disponible o no se confirmó su entrega.", true)
		return
	}
	prepared, err := h.prepareExport(ctx, msg.Chat.ID, parts[2])
	if err != nil || exportRevision(prepared.settings, prepared.preview, prepared.registration) != revision {
		h.answer(ctx, b, query.ID, "Los datos cambiaron desde ese ZIP. Revisá Exportar de nuevo.", true)
		return
	}
	if parts[1] == "ask" {
		h.send(ctx, b, msg.Chat.ID, "¿Confirmaste este período en Marangatu y obtuviste el Talón de Presentación?\n\nEl bot no verifica la DNIT: guarda únicamente tu confirmación manual.",
			presentationKeyboard("yes", parts[2], parts[3], "Sí, obtuve el Talón"))
		h.answer(ctx, b, query.ID, "", false)
		return
	}
	if err := h.deps.Store.MarkUserPresented(ctx, msg.Chat.ID, parts[2], parts[3]); err != nil {
		h.answer(ctx, b, query.ID, StoreErrorMessage, true)
		return
	}
	h.editText(ctx, b, buttonPress{chatID: msg.Chat.ID, messageID: msg.ID}, "✅ Presentación marcada por vos. Conservá el Talón; el bot no verifica la DNIT.", noKeyboard())
	h.answer(ctx, b, query.ID, "Confirmación manual guardada", false)
}
