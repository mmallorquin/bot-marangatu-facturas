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
	"github.com/mmallorquin/bot-marangatu-facturas/internal/store"
)

// Tiempo máximo para descargar y leer una factura.
const processTimeout = 2 * time.Minute

// InvoiceStore es lo que el bot necesita para guardar facturas (lo implementa store.Store).
type InvoiceStore interface {
	CreateDraft(ctx context.Context, chatID int64, d store.Draft) (int64, error)
	Get(ctx context.Context, chatID, id int64) (store.Record, error)
	UpdateInvoice(ctx context.Context, chatID, id int64, inv invoice.Invoice) error
	Save(ctx context.Context, chatID, id int64) error
	Discard(ctx context.Context, chatID, id int64) error
	SetAwaiting(ctx context.Context, chatID, id int64, field string) error
	Awaiting(ctx context.Context, chatID int64) (store.Pending, bool, error)
	ClearAwaiting(ctx context.Context, chatID int64) error
	MonthSummary(ctx context.Context, chatID int64, period string) (store.Summary, error)
	SavedInvoices(ctx context.Context, chatID int64, period string) ([]invoice.Invoice, error)
	Settings(ctx context.Context, chatID int64) (store.ChatSettings, error)
	SetRUC(ctx context.Context, chatID int64, ruc string) error
	SetImputations(ctx context.Context, chatID int64, imp store.Imputations) error
	NextExportSeq(ctx context.Context, chatID int64, period string) (int, error)
}

// Deps son las dependencias del handler.
type Deps struct {
	Logger *slog.Logger
	Token  string // solo para ocultarlo en los logs de error
	Reader reader.Reader
	Store  InvoiceStore
	Now    func() time.Time // reloj; en tests se fija una fecha
}

// NewHandler devuelve el handler que responde a mensajes y botones.
func NewHandler(deps Deps) bot.HandlerFunc {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	h := &handler{deps: deps, logger: deps.Logger}
	return h.handle
}

type handler struct {
	deps   Deps
	logger *slog.Logger
}

func (h *handler) handle(ctx context.Context, b *bot.Bot, update *models.Update) {
	switch {
	case update.CallbackQuery != nil:
		h.handleCallback(ctx, b, update.CallbackQuery)
	case update.Message != nil:
		h.handleMessage(ctx, b, update.Message)
	}
}

func (h *handler) handleMessage(ctx context.Context, b *bot.Bot, msg *models.Message) {
	chatID := msg.Chat.ID
	file, kind := imageFileOf(msg)
	switch kind {
	case notAnImage:
		h.handleText(ctx, b, chatID, msg.Text)
	case imageUnsupported:
		h.send(ctx, b, chatID, UnsupportedFormatMessage, nil)
	case imageTooLarge:
		h.send(ctx, b, chatID, TooLargeMessage, nil)
	case imageSupported:
		h.send(ctx, b, chatID, ReadingMessage, nil)
		h.processImage(ctx, b, chatID, file)
	}
}

func (h *handler) handleText(ctx context.Context, b *bot.Bot, chatID int64, text string) {
	switch commandOf(text) {
	case summaryCommand:
		h.sendSummary(ctx, b, chatID, text)
		return
	case cancelCommand:
		h.cancelCorrection(ctx, b, chatID)
		return
	case startCommand:
		h.send(ctx, b, chatID, WelcomeMessage, nil)
		return
	case rucCommand:
		h.setRUC(ctx, b, chatID, text)
		return
	case imputeCommand:
		h.setImputations(ctx, b, chatID, text)
		return
	case exportCommand:
		h.exportMonth(ctx, b, chatID, text)
		return
	}

	pending, found, err := h.deps.Store.Awaiting(ctx, chatID)
	if err != nil {
		h.logger.Error("no se pudo leer la corrección pendiente", "chat_id", chatID, "error", err)
	}
	if !found {
		h.send(ctx, b, chatID, ReplyForText(text), nil)
		return
	}
	h.applyCorrection(ctx, b, chatID, pending, text)
}

// processImage descarga la imagen, la lee con IA, la valida y la muestra con botones.
func (h *handler) processImage(ctx context.Context, b *bot.Bot, chatID int64, file imageFile) {
	ctx, cancel := context.WithTimeout(ctx, processTimeout)
	defer cancel()

	data, err := downloadFile(ctx, b, file.fileID)
	if err != nil {
		h.logger.Error("no se pudo descargar la imagen", "chat_id", chatID, "error", Redact(err, h.deps.Token))
		h.send(ctx, b, chatID, ReadErrorMessage, nil)
		return
	}

	start := time.Now()
	result, err := h.deps.Reader.Read(ctx, reader.Image{Data: data, MimeType: file.mimeType})
	if err != nil {
		h.logger.Error("no se pudo leer la factura", "chat_id", chatID, "error", err)
		h.send(ctx, b, chatID, ReadErrorMessage, nil)
		return
	}

	inv := result.Invoice
	issues := invoice.Validate(inv)
	h.logger.Info("factura leída",
		"chat_id", chatID,
		"modelo", result.Model,
		"costo_usd", result.CostUSD,
		"tokens_entrada", result.Usage.InputTokens,
		"tokens_salida", result.Usage.OutputTokens,
		"tokens_razonamiento", result.Usage.ReasoningTokens,
		"segundos", time.Since(start).Seconds(),
		"es_comprobante", inv.IsInvoice,
		"problemas", len(issues),
	)
	if !inv.IsInvoice {
		h.send(ctx, b, chatID, NotInvoiceMessage, nil)
		return
	}

	id, err := h.deps.Store.CreateDraft(ctx, chatID, store.Draft{Invoice: inv, Model: result.Model, CostUSD: result.CostUSD})
	if err != nil {
		h.logger.Error("no se pudo crear el borrador", "chat_id", chatID, "error", err)
		h.send(ctx, b, chatID, FormatInvoice(inv, issues)+"\n\n"+StoreErrorMessage, nil)
		return
	}
	h.send(ctx, b, chatID, FormatInvoice(inv, issues), mainKeyboard(id))
}

func (h *handler) applyCorrection(ctx context.Context, b *bot.Bot, chatID int64, pending store.Pending, text string) {
	rec, err := h.deps.Store.Get(ctx, chatID, pending.ID)
	if err != nil || rec.Status != store.StatusDraft {
		_ = h.deps.Store.ClearAwaiting(ctx, chatID)
		h.send(ctx, b, chatID, HelpMessage, nil)
		return
	}

	edited, err := invoice.Edit(rec.Invoice, pending.Field, text)
	if err != nil {
		h.send(ctx, b, chatID, "❌ "+err.Error()+"\n"+retryOrCancelHint, nil)
		return
	}
	if err := h.deps.Store.UpdateInvoice(ctx, chatID, rec.ID, edited); err != nil {
		h.logger.Error("no se pudo guardar la corrección", "chat_id", chatID, "error", err)
		h.send(ctx, b, chatID, StoreErrorMessage, nil)
		return
	}
	if err := h.deps.Store.ClearAwaiting(ctx, chatID); err != nil {
		h.logger.Error("no se pudo cerrar la corrección", "chat_id", chatID, "error", err)
	}
	h.send(ctx, b, chatID, FormatInvoice(edited, invoice.Validate(edited)), mainKeyboard(rec.ID))
}

func (h *handler) cancelCorrection(ctx context.Context, b *bot.Bot, chatID int64) {
	_, found, err := h.deps.Store.Awaiting(ctx, chatID)
	if err == nil && found {
		err = h.deps.Store.ClearAwaiting(ctx, chatID)
	}
	switch {
	case err != nil:
		h.logger.Error("no se pudo cancelar la corrección", "chat_id", chatID, "error", err)
		h.send(ctx, b, chatID, StoreErrorMessage, nil)
	case found:
		h.send(ctx, b, chatID, CancelledMessage, nil)
	default:
		h.send(ctx, b, chatID, NothingToCancel, nil)
	}
}

func (h *handler) sendSummary(ctx context.Context, b *bot.Bot, chatID int64, text string) {
	period, err := parsePeriod(text, h.deps.Now())
	if err != nil {
		h.send(ctx, b, chatID, "❌ "+err.Error(), nil)
		return
	}
	sum, err := h.deps.Store.MonthSummary(ctx, chatID, period)
	if err != nil {
		h.logger.Error("no se pudo armar el resumen", "chat_id", chatID, "error", err)
		h.send(ctx, b, chatID, StoreErrorMessage, nil)
		return
	}
	h.send(ctx, b, chatID, FormatMonthSummary(period, sum), nil)
}

// send envía un mensaje; keyboard puede ser nil.
func (h *handler) send(ctx context.Context, b *bot.Bot, chatID int64, text string, keyboard *models.InlineKeyboardMarkup) {
	params := &bot.SendMessageParams{ChatID: chatID, Text: text}
	if keyboard != nil {
		params.ReplyMarkup = keyboard
	}
	if _, err := b.SendMessage(ctx, params); err != nil {
		h.logger.Error("no se pudo responder", "chat_id", chatID, "error", Redact(err, h.deps.Token))
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
