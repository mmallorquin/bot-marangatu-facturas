package telegram

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/mmallorquin/bot-marangatu-facturas/internal/invoice"
	"github.com/mmallorquin/bot-marangatu-facturas/internal/reader"
)

// Tiempo máximo para descargar y leer una factura.
const processTimeout = 2 * time.Minute

// NewHandler devuelve el handler que responde a cada mensaje del bot.
// Recibe el token solo para ocultarlo en los logs de error.
func NewHandler(logger *slog.Logger, token string, rdr reader.Reader) bot.HandlerFunc {
	h := &handler{logger: logger, token: token, reader: rdr}
	return h.handle
}

type handler struct {
	logger *slog.Logger
	token  string
	reader reader.Reader
}

func (h *handler) handle(ctx context.Context, b *bot.Bot, update *models.Update) {
	msg := update.Message
	if msg == nil {
		return
	}

	file, kind := imageFileOf(msg)
	switch kind {
	case notAnImage:
		h.send(ctx, b, msg.Chat.ID, ReplyForText(msg.Text))
	case imageUnsupported:
		h.send(ctx, b, msg.Chat.ID, UnsupportedFormatMessage)
	case imageTooLarge:
		h.send(ctx, b, msg.Chat.ID, TooLargeMessage)
	case imageSupported:
		h.send(ctx, b, msg.Chat.ID, ReadingMessage)
		h.send(ctx, b, msg.Chat.ID, h.readInvoice(ctx, b, msg.Chat.ID, file))
	}
}

// readInvoice descarga la imagen, la lee con IA, la valida y devuelve la respuesta al usuario.
func (h *handler) readInvoice(ctx context.Context, b *bot.Bot, chatID int64, file imageFile) string {
	ctx, cancel := context.WithTimeout(ctx, processTimeout)
	defer cancel()

	data, err := downloadFile(ctx, b, file.fileID)
	if err != nil {
		h.logger.Error("no se pudo descargar la imagen", "chat_id", chatID, "error", Redact(err, h.token))
		return ReadErrorMessage
	}

	start := time.Now()
	result, err := h.reader.Read(ctx, reader.Image{Data: data, MimeType: file.mimeType})
	if err != nil {
		h.logger.Error("no se pudo leer la factura", "chat_id", chatID, "error", err)
		return ReadErrorMessage
	}

	issues := invoice.Validate(result.Invoice)
	h.logger.Info("factura leída",
		"chat_id", chatID,
		"modelo", result.Model,
		"costo_usd", result.CostUSD,
		"segundos", time.Since(start).Seconds(),
		"es_comprobante", result.Invoice.IsInvoice,
		"problemas", len(issues),
	)
	return FormatInvoice(result, issues)
}

func (h *handler) send(ctx context.Context, b *bot.Bot, chatID int64, text string) {
	_, err := b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: text})
	if err != nil {
		h.logger.Error("no se pudo responder", "chat_id", chatID, "error", Redact(err, h.token))
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
