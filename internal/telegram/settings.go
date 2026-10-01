package telegram

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/mmallorquin/bot-marangatu-facturas/internal/store"
)

const (
	settingsCommand        = "/ajustes"
	settingsCallbackPrefix = "c"
)

func (h *handler) showSettings(ctx context.Context, b *bot.Bot, chatID int64) {
	cs, err := h.deps.Store.Settings(ctx, chatID)
	if err != nil {
		h.send(ctx, b, chatID, StoreErrorMessage, nil)
		return
	}
	text, keyboard := settingsView(cs)
	h.send(ctx, b, chatID, text, keyboard)
}

func settingsView(cs store.ChatSettings) (string, *models.InlineKeyboardMarkup) {
	ruc := cs.RUC
	if ruc == "" {
		ruc = "sin configurar"
	}
	imputations := formatImputations(cs.Imputations)
	if cs.Imputations == (store.Imputations{}) {
		imputations = "sin configurar"
	}
	state := func(on bool) string {
		if on {
			return "activado"
		}
		return "desactivado"
	}
	text := fmt.Sprintf("⚙️ Ajustes\n\nRUC: %s\nImpuestos: %s\nRegistro: %s\nGuardado automático: %s\nRecordatorios: %s\n\nElegí qué querés cambiar.", ruc, imputations, registrationLabel(cs.Registration), state(cs.AutoSave), state(cs.Reminders))
	toggle := func(label, field string, on bool) models.InlineKeyboardButton {
		value, action := "1", "Activar "
		if on {
			value, action = "0", "Desactivar "
		}
		return models.InlineKeyboardButton{Text: action + label, CallbackData: "c:" + field + ":" + value}
	}
	keyboard := &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{
		{{Text: "RUC", CallbackData: "c:ruc"}, {Text: "Impuestos", CallbackData: "c:impute"}},
		{{Text: "Registro 955 / 956", CallbackData: "c:registration"}},
		{toggle("guardado automático", "auto", cs.AutoSave)},
		{toggle("recordatorios", "rem", cs.Reminders)},
		{{Text: "🗑️ Borrar mis datos", CallbackData: "c:delete"}},
	}}
	return text, keyboard
}

func (h *handler) handleSettingsCallback(ctx context.Context, b *bot.Bot, query *models.CallbackQuery, msg *models.Message) {
	parts := strings.Split(query.Data, ":")
	if len(parts) < 2 || parts[0] != settingsCallbackPrefix {
		h.answer(ctx, b, query.ID, NoLongerEditableAlert, true)
		return
	}
	if len(parts) == 3 && (parts[1] == "auto" || parts[1] == "rem") && (parts[2] == "0" || parts[2] == "1") {
		value := "no"
		if parts[2] == "1" {
			value = "si"
		}
		if parts[1] == "auto" {
			h.setAutoSave(ctx, b, msg.Chat.ID, autoSaveCommand+" "+value)
		} else {
			h.setReminders(ctx, b, msg.Chat.ID, remindersCommand+" "+value)
		}
		h.showSettings(ctx, b, msg.Chat.ID)
		h.answer(ctx, b, query.ID, "", false)
		return
	}
	if len(parts) != 2 {
		h.answer(ctx, b, query.ID, NoLongerEditableAlert, true)
		return
	}
	switch parts[1] {
	case "ruc":
		h.beginRUCSetup(ctx, b, msg.Chat.ID)
	case "impute":
		h.setImputations(ctx, b, msg.Chat.ID, imputeCommand)
	case "registration":
		h.setRegistration(ctx, b, msg.Chat.ID, registrationCommand)
	case "delete":
		h.askDeleteData(ctx, b, msg.Chat.ID)
	default:
		h.answer(ctx, b, query.ID, NoLongerEditableAlert, true)
		return
	}
	h.answer(ctx, b, query.ID, "", false)
}
