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
		{Command: "facturas", Description: "Mis facturas: pendientes, guardadas y totales"},
		{Command: "exportar", Description: "Exportar: elegir período y revisar archivos"},
		{Command: "ajustes", Description: "Ajustes: RUC, impuestos y preferencias"},
		{Command: "ayuda", Description: "Ayuda: cómo usar el bot y presentar en Marangatu"},
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
