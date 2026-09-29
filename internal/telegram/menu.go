package telegram

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// ConfigureMenu publica los comandos y activa su botón de menú en Telegram.
func ConfigureMenu(ctx context.Context, b *bot.Bot) error {
	_, commandsErr := b.SetMyCommands(ctx, &bot.SetMyCommandsParams{Commands: []models.BotCommand{
		{Command: "resumen", Description: "Ver las facturas y el total de este mes"},
		{Command: "exportar", Description: "Revisar las facturas y generar el ZIP"},
		{Command: "ruc", Description: "Consultar o configurar tu RUC"},
		{Command: "imputar", Description: "Elegir impuestos: IVA, IRE o IRP"},
		{Command: "cancelar", Description: "Cancelar una corrección pendiente"},
		{Command: "start", Description: "Ver cómo usar el bot"},
	}})
	if commandsErr != nil {
		commandsErr = fmt.Errorf("publicando comandos: %w", commandsErr)
	}
	_, menuErr := b.SetChatMenuButton(ctx, &bot.SetChatMenuButtonParams{
		MenuButton: models.MenuButtonCommands{Type: models.MenuButtonTypeCommands},
	})
	if menuErr != nil {
		menuErr = fmt.Errorf("activando botón de menú: %w", menuErr)
	}
	return errors.Join(commandsErr, menuErr)
}
