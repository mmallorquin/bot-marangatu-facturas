package telegram

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/mmallorquin/bot-marangatu-facturas/internal/invoice"
	"github.com/mmallorquin/bot-marangatu-facturas/internal/store"
)

// Configuración guiada: la primera vez, el bot pide el RUC (se escribe sin comando) y
// ofrece botones para elegir los impuestos, en vez de pedir /ruc e /imputar.
const (
	askRUCOnboarding = "Para armar el archivo de Marangatu necesito tu RUC. Escribilo acá, por ejemplo 1234567-8.\n\n" +
		"Si preferís probar primero, mandame una foto de una factura y el RUC lo cargás después con /ruc."
	askImputationButtons = "¿A qué impuestos imputás tus compras? Tocá los que correspondan y después ✅ Listo.\n" +
		"(También podés escribir /imputar iva irp)"

	imputeCallbackPrefix = "i"
	imputeActionToggle   = "t"
	imputeActionDone     = "ok"
)

// Cada impuesto es un bit de la máscara que viaja en el botón: 1 IVA, 2 IRE, 4 IRP-RSP.
var imputeOptions = []struct {
	bit   int
	label string
}{
	{1, "IVA"}, {2, "IRE"}, {4, "IRP-RSP"},
}

func maskOf(imp store.Imputations) int {
	mask := 0
	if imp.IVA {
		mask |= 1
	}
	if imp.IRE {
		mask |= 2
	}
	if imp.IRP {
		mask |= 4
	}
	return mask
}

func imputationsOf(mask int) store.Imputations {
	return store.Imputations{IVA: mask&1 != 0, IRE: mask&2 != 0, IRP: mask&4 != 0}
}

// imputeKeyboard muestra un botón por impuesto (marcado o no) y "Listo".
func imputeKeyboard(mask int) *models.InlineKeyboardMarkup {
	var row []models.InlineKeyboardButton
	for _, opt := range imputeOptions {
		check := "☐"
		if mask&opt.bit != 0 {
			check = "☑️"
		}
		row = append(row, models.InlineKeyboardButton{
			Text:         check + " " + opt.label,
			CallbackData: fmt.Sprintf("%s:%s:%d", imputeCallbackPrefix, imputeActionToggle, mask^opt.bit),
		})
	}
	done := models.InlineKeyboardButton{
		Text: "✅ Listo", CallbackData: fmt.Sprintf("%s:%s:%d", imputeCallbackPrefix, imputeActionDone, mask),
	}
	return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{row, {done}}}
}

// askImputations muestra los botones de impuestos, con lo que el chat ya tenga elegido.
func (h *handler) askImputations(ctx context.Context, b *bot.Bot, chatID int64) {
	mask := 0
	if cs, err := h.deps.Store.Settings(ctx, chatID); err == nil {
		mask = maskOf(cs.Imputations)
	}
	h.send(ctx, b, chatID, askImputationButtons, imputeKeyboard(mask))
}

func (h *handler) handleImputeCallback(ctx context.Context, b *bot.Bot, query *models.CallbackQuery, msg *models.Message) {
	parts := strings.Split(query.Data, ":")
	mask, err := strconv.Atoi(parts[len(parts)-1])
	if len(parts) != 3 || err != nil || mask < 0 || mask > 7 {
		h.answer(ctx, b, query.ID, NoLongerEditableAlert, false)
		return
	}
	press := buttonPress{queryID: query.ID, chatID: msg.Chat.ID, messageID: msg.ID}

	switch parts[1] {
	case imputeActionToggle:
		h.editKeyboard(ctx, b, press, imputeKeyboard(mask))
		h.answer(ctx, b, press.queryID, "", false)
	case imputeActionDone:
		if mask == 0 {
			h.answer(ctx, b, press.queryID, "Elegí al menos un impuesto.", true)
			return
		}
		imp := imputationsOf(mask)
		if err := h.deps.Store.SetImputations(ctx, press.chatID, imp); err != nil {
			h.logger.Error("no se pudieron guardar las imputaciones", "chat_id", press.chatID, "error", err)
			h.answer(ctx, b, press.queryID, StoreErrorMessage, true)
			return
		}
		h.track(ctx, press.chatID, store.Event{Kind: store.EventImpute})
		message, keyboard := h.imputationReply(ctx, press.chatID, imp)
		h.editText(ctx, b, press, message, keyboard)
		h.answer(ctx, b, press.queryID, "", false)
	default:
		h.answer(ctx, b, press.queryID, NoLongerEditableAlert, false)
	}
}

const readyToUseMessage = "Todo listo 🎉 Mandame las fotos o PDF de tus facturas y usá /exportar para revisarlas."

// imputationReply no deduce la obligación de los impuestos elegidos.
func (h *handler) imputationReply(ctx context.Context, chatID int64, imp store.Imputations) (string, *models.InlineKeyboardMarkup) {
	message := "✅ Tus compras se van a imputar a: " + formatImputations(imp)
	cs, err := h.deps.Store.Settings(ctx, chatID)
	if err != nil {
		return message + "\n\n" + StoreErrorMessage, noKeyboard()
	}
	if cs.RUC == "" {
		return message + "\n\n" + askRUCMessage, noKeyboard()
	}
	if !cs.Registration.Valid() {
		return message + "\n\n" + registrationPrompt, registrationKeyboard(cs.RUC)
	}
	message += "\nRegistro configurado: " + registrationLabel(cs.Registration)
	if cs.Registration == store.RegistrationAnnual {
		message += "\n\n" + annualHint(annualYearToFile(h.deps.Now().In(reminderLocation)))
	}
	return message + "\n\n" + readyToUseMessage, noKeyboard()
}

// welcome muestra la bienvenida y, si falta el RUC, empieza la configuración guiada.
func (h *handler) welcome(ctx context.Context, b *bot.Bot, chatID int64) {
	h.send(ctx, b, chatID, WelcomeMessage, nil)
	cs, err := h.deps.Store.Settings(ctx, chatID)
	if err != nil {
		return
	}
	if cs.RUC != "" {
		if cs.Imputations == (store.Imputations{}) {
			h.askImputations(ctx, b, chatID)
		} else if !cs.Registration.Valid() {
			h.askRegistration(ctx, b, chatID, cs)
		}
		return
	}
	if err := h.deps.Store.SetAwaitingRUC(ctx, chatID, true); err != nil {
		h.logger.Error("no se pudo iniciar la configuración", "chat_id", chatID, "error", err)
		return
	}
	h.send(ctx, b, chatID, askRUCOnboarding, nil)
}

// tryOnboardingRUC toma el texto como RUC si el chat está en la configuración guiada.
// Devuelve false si no estaba esperando el RUC.
func (h *handler) tryOnboardingRUC(ctx context.Context, b *bot.Bot, chatID int64, text string) bool {
	cs, err := h.deps.Store.Settings(ctx, chatID)
	if err != nil || !cs.AwaitingRUC || !strings.ContainsAny(text, "0123456789") {
		return false // un "hola" no es un intento de RUC: recibe la ayuda de siempre
	}
	ruc := invoice.NormalizeRUC(strings.ReplaceAll(text, " ", ""))
	if err := invoice.ValidateRUC(ruc); err != nil {
		h.send(ctx, b, chatID, "❌ "+err.Error()+"\n\nEscribí tu RUC de nuevo (ej. 1234567-8) o /cancelar para cargarlo después.", nil)
		return true
	}
	h.track(ctx, chatID, store.Event{Kind: store.EventRUC})
	h.saveRUC(ctx, b, chatID, ruc)
	return true
}

// stopOnboarding sale de la configuración guiada; devuelve true si el chat estaba en ella.
func (h *handler) stopOnboarding(ctx context.Context, chatID int64) bool {
	cs, err := h.deps.Store.Settings(ctx, chatID)
	if err != nil || !cs.AwaitingRUC {
		return false
	}
	return h.deps.Store.SetAwaitingRUC(ctx, chatID, false) == nil
}

// saveRUC guarda el RUC, termina la configuración guiada y, si faltan los impuestos, los pide.
func (h *handler) saveRUC(ctx context.Context, b *bot.Bot, chatID int64, ruc string) {
	if err := h.deps.Store.SetRUC(ctx, chatID, ruc); err != nil {
		h.logger.Error("no se pudo guardar el RUC", "chat_id", chatID, "error", err)
		h.send(ctx, b, chatID, StoreErrorMessage, nil)
		return
	}
	if err := h.deps.Store.SetAwaitingRUC(ctx, chatID, false); err != nil {
		h.logger.Error("no se pudo cerrar la configuración", "chat_id", chatID, "error", err)
	}
	h.send(ctx, b, chatID, "✅ RUC guardado: "+ruc, nil)
	if cs, err := h.deps.Store.Settings(ctx, chatID); err == nil {
		if cs.Imputations == (store.Imputations{}) {
			h.askImputations(ctx, b, chatID)
		} else if !cs.Registration.Valid() {
			h.askRegistration(ctx, b, chatID, cs)
		}
	}
}
