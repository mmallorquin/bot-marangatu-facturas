package telegram

import (
	"context"
	"errors"
	"log/slog"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// NewHandler devuelve el handler que responde a cada mensaje del bot.
// Recibe el token solo para ocultarlo en los logs de error.
func NewHandler(logger *slog.Logger, token string) bot.HandlerFunc {
	return func(ctx context.Context, b *bot.Bot, update *models.Update) {
		msg := update.Message
		if msg == nil {
			return
		}

		reply := ReplyFor(msg)
		if reply == PhotoReceivedMessage {
			logger.Info("imagen recibida", "chat_id", msg.Chat.ID)
		}

		_, err := b.SendMessage(ctx, &bot.SendMessageParams{ChatID: msg.Chat.ID, Text: reply})
		if err != nil {
			logger.Error("no se pudo responder", "chat_id", msg.Chat.ID, "error", Redact(err, token))
		}
	}
}

// NewErrorsHandler reemplaza el log de errores de la librería, que imprime el token.
func NewErrorsHandler(logger *slog.Logger, token string) bot.ErrorsHandler {
	return func(err error) {
		if errors.Is(err, context.Canceled) {
			return // apagado normal con Ctrl+C
		}
		logger.Error("error de Telegram", "error", Redact(err, token))
	}
}
