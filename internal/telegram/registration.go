package telegram

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/mmallorquin/bot-marangatu-facturas/internal/store"
)

const (
	registrationCommand        = "/registro"
	registrationCallbackPrefix = "reg"
	registrationPrompt         = "¿Qué obligación de registro tenés activa en tu RUC? Elegí la que figura en Marangatu, no la deduzcas de /imputar.\n" +
		"También podés escribir /registro 955 o /registro 956."
	registrationHelp = "Entrá a tu cuenta de Marangatu y consultá las obligaciones activas de tu RUC: " +
		"955 es registro mensual de comprobantes; 956 es registro anual. Si no encontrás ninguna o tenés dudas, consultá a tu contador o a la DNIT. " +
		"El bot no consulta tu cuenta ni necesita tu contraseña. No elige una obligación por vos."
)

func registrationLabel(registration store.Registration) string {
	switch registration {
	case store.RegistrationMonthly:
		return "955 — mensual"
	case store.RegistrationAnnual:
		return "956 — anual"
	default:
		return "sin configurar"
	}
}

func registrationKeyboard(ruc string) *models.InlineKeyboardMarkup {
	revision := registrationRUCRevision(ruc)
	option := func(label, action string) models.InlineKeyboardButton {
		return models.InlineKeyboardButton{Text: label, CallbackData: registrationCallbackPrefix + ":" + action + ":" + revision}
	}
	return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{
		{option("955 — Mensual", "955"), option("956 — Anual", "956")},
		{option("No sé", "help")},
	}}
}

// Mantiene el callback por debajo de los 64 bytes de Telegram, sin depender
// del largo del RUC, y permite detectar botones de una configuración anterior.
func registrationRUCRevision(ruc string) string {
	sum := sha256.Sum256([]byte(ruc))
	return fmt.Sprintf("%x", sum[:16])
}

func (h *handler) askRegistration(ctx context.Context, b *bot.Bot, chatID int64, cs store.ChatSettings) {
	h.send(ctx, b, chatID, "Registro configurado: "+registrationLabel(cs.Registration)+"\n\n"+registrationPrompt, registrationKeyboard(cs.RUC))
}

func (h *handler) setRegistration(ctx context.Context, b *bot.Bot, chatID int64, text string) {
	cs, err := h.deps.Store.Settings(ctx, chatID)
	if err != nil {
		h.send(ctx, b, chatID, StoreErrorMessage, nil)
		return
	}
	if cs.RUC == "" {
		h.send(ctx, b, chatID, askRUCMessage, nil)
		return
	}
	fields := strings.Fields(text)
	if len(fields) == 1 {
		h.askRegistration(ctx, b, chatID, cs)
		return
	}
	if len(fields) != 2 || !store.Registration(fields[1]).Valid() {
		h.send(ctx, b, chatID, "❌ Usá /registro 955 o /registro 956.\n\n"+registrationPrompt, registrationKeyboard(cs.RUC))
		return
	}
	registration := store.Registration(fields[1])
	if err := h.deps.Store.SetRegistration(ctx, chatID, cs.RUC, registration); err != nil {
		h.send(ctx, b, chatID, StoreErrorMessage, nil)
		return
	}
	h.send(ctx, b, chatID, h.registrationSaved(registration), nil)
}

func (h *handler) registrationSaved(registration store.Registration) string {
	text := "✅ Registro configurado: " + registrationLabel(registration) + ". No modifica tus impuestos ni tus facturas."
	if registration == store.RegistrationAnnual {
		return text + "\n\n" + annualHint(annualYearToFile(h.deps.Now().In(reminderLocation)))
	}
	return text + "\n\nUsá /exportar para revisar el mes, o /exportar MM/AAAA para otro mes."
}

func (h *handler) handleRegistrationCallback(ctx context.Context, b *bot.Bot, query *models.CallbackQuery, msg *models.Message) {
	parts := strings.Split(query.Data, ":")
	if len(parts) != 3 || (parts[1] != "help" && !store.Registration(parts[1]).Valid()) {
		h.answer(ctx, b, query.ID, NoLongerEditableAlert, true)
		return
	}
	cs, err := h.deps.Store.Settings(ctx, msg.Chat.ID)
	if err != nil {
		h.answer(ctx, b, query.ID, StoreErrorMessage, true)
		return
	}
	if cs.RUC == "" || parts[2] != registrationRUCRevision(cs.RUC) {
		h.answer(ctx, b, query.ID, "Tu RUC cambió. Usá /registro de nuevo.", true)
		return
	}
	if parts[1] == "help" {
		h.send(ctx, b, msg.Chat.ID, registrationHelp, registrationKeyboard(cs.RUC))
		h.answer(ctx, b, query.ID, "", false)
		return
	}
	registration := store.Registration(parts[1])
	if err := h.deps.Store.SetRegistration(ctx, msg.Chat.ID, cs.RUC, registration); err != nil {
		text := StoreErrorMessage
		if errors.Is(err, store.ErrRegistrationRUCChanged) {
			text = "Tu RUC cambió. Usá /registro de nuevo."
		}
		h.answer(ctx, b, query.ID, text, true)
		return
	}
	press := buttonPress{queryID: query.ID, chatID: msg.Chat.ID, messageID: msg.ID}
	h.editText(ctx, b, press, h.registrationSaved(registration), noKeyboard())
	h.answer(ctx, b, query.ID, "Registro guardado", false)
}

func wrongRegistrationPeriod(registration store.Registration, period string) string {
	if registration == store.RegistrationAnnual && !isAnnual(period) {
		return fmt.Sprintf("Tu registro es 956 anual. Para el ZIP usá /exportar %s. CSV y Excel sirven para revisar el mes.", period[:4])
	}
	if registration == store.RegistrationMonthly && isAnnual(period) {
		return fmt.Sprintf("Tu registro es 955 mensual. Para el ZIP elegí un mes: /exportar MM/%s. CSV y Excel sirven para revisar el año.", period)
	}
	return ""
}
