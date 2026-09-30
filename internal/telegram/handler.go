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

// Tiempo máximo para registrar un evento de uso.
const trackTimeout = 5 * time.Second

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
	SavedRecords(ctx context.Context, chatID int64, period string) ([]store.SavedRecord, error)
	DeleteSaved(ctx context.Context, chatID, id int64) error
	IsSaved(ctx context.Context, chatID int64, inv invoice.Invoice) (bool, error)
	Drafts(ctx context.Context, chatID int64) ([]store.Record, error)
	Unsave(ctx context.Context, chatID, id int64) error
	SetAutoSave(ctx context.Context, chatID int64, on bool) error
	Settings(ctx context.Context, chatID int64) (store.ChatSettings, error)
	SetRUC(ctx context.Context, chatID int64, ruc string) error
	SetImputations(ctx context.Context, chatID int64, imp store.Imputations) error
	NextExportSeq(ctx context.Context, chatID int64, period string) (int, error)
	LogEvent(ctx context.Context, chatID int64, e store.Event) error
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
	h := &handler{deps: deps, logger: deps.Logger, albums: &albums{}}
	return h.handle
}

type handler struct {
	deps   Deps
	logger *slog.Logger
	albums *albums
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
		h.track(ctx, chatID, store.Event{Kind: store.EventPhotoRejected, Detail: store.EventDetailFormat})
		h.send(ctx, b, chatID, UnsupportedFormatMessage, nil)
	case imageTooLarge:
		h.track(ctx, chatID, store.Event{Kind: store.EventPhotoRejected, Detail: store.EventDetailSize})
		h.send(ctx, b, chatID, TooLargeMessage, nil)
	case imageSupported:
		detail := store.EventDetailImage
		if file.mimeType == pdfMimeType {
			detail = store.EventDetailPDF
		}
		h.track(ctx, chatID, store.Event{Kind: store.EventPhoto, Detail: detail})
		switch {
		case msg.MediaGroupID == "":
			h.send(ctx, b, chatID, ReadingMessage, nil)
		case h.albums.first(msg.MediaGroupID, time.Now()):
			h.send(ctx, b, chatID, ReadingAlbumMessage, nil)
		}
		h.processImage(ctx, b, chatID, file)
	}
}

func (h *handler) handleText(ctx context.Context, b *bot.Bot, chatID int64, text string) {
	if kind, ok := commandEvents[commandOf(text)]; ok {
		h.track(ctx, chatID, store.Event{Kind: kind})
	}
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
	case listCommand:
		h.listInvoices(ctx, b, chatID, text)
		return
	case pendingCommand:
		h.showPending(ctx, b, chatID)
		return
	case autoSaveCommand:
		h.setAutoSave(ctx, b, chatID, text)
		return
	}

	pending, found, err := h.deps.Store.Awaiting(ctx, chatID)
	if err != nil {
		h.logger.Error("no se pudo leer la corrección pendiente", "chat_id", chatID, "error", err)
	}
	if !found {
		h.track(ctx, chatID, store.Event{Kind: store.EventUnknownText})
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
		h.track(ctx, chatID, store.Event{Kind: store.EventReadError, Detail: store.EventDetailDownload})
		h.send(ctx, b, chatID, ReadErrorMessage, nil)
		return
	}

	start := time.Now()
	result, err := h.deps.Reader.Read(ctx, reader.Image{Data: data, MimeType: file.mimeType})
	if err != nil {
		h.logger.Error("no se pudo leer la factura", "chat_id", chatID, "error", err)
		h.track(ctx, chatID, store.Event{
			Kind: store.EventReadError, Detail: store.EventDetailAI, Seconds: time.Since(start).Seconds(),
		})
		h.send(ctx, b, chatID, ReadErrorMessage, nil)
		return
	}

	inv := result.Invoice
	// Se conserva el resultado del lector intacto para guardarlo como original.
	inv.Number = invoice.NormalizeNumber(inv.Number)
	issues := invoice.Validate(inv)
	seconds := time.Since(start).Seconds()
	h.track(ctx, chatID, readEvent(inv, issues, seconds, result.CostUSD))
	h.logger.Info("factura leída",
		"chat_id", chatID,
		"modelo", result.Model,
		"costo_usd", result.CostUSD,
		"tokens_entrada", result.Usage.InputTokens,
		"tokens_salida", result.Usage.OutputTokens,
		"tokens_razonamiento", result.Usage.ReasoningTokens,
		"segundos", seconds,
		"es_comprobante", inv.IsInvoice,
		"problemas", len(issues),
	)
	if !inv.IsInvoice {
		h.send(ctx, b, chatID, NotInvoiceMessage, nil)
		return
	}

	id, err := h.deps.Store.CreateDraft(ctx, chatID, store.Draft{Invoice: result.Invoice, Model: result.Model, CostUSD: result.CostUSD})
	if err != nil {
		h.logger.Error("no se pudo crear el borrador", "chat_id", chatID, "error", err)
		h.send(ctx, b, chatID, FormatInvoice(inv, issues)+"\n\n"+StoreErrorMessage, nil)
		return
	}
	text, duplicate := h.invoiceMessage(ctx, chatID, inv, issues)
	if !duplicate && invoice.Clean(inv) && h.autoSaves(ctx, chatID) {
		if err := h.deps.Store.Save(ctx, chatID, id); err == nil {
			h.track(ctx, chatID, store.Event{Kind: store.EventSaved, Detail: store.EventDetailClean})
			h.send(ctx, b, chatID, text+"\n\n"+AutoSavedNote, undoKeyboard(id))
			return
		}
		// Si falla el guardado automático, queda como borrador con los botones de siempre.
	}
	h.send(ctx, b, chatID, text, mainKeyboard(id))
}

func (h *handler) autoSaves(ctx context.Context, chatID int64) bool {
	cs, err := h.deps.Store.Settings(ctx, chatID)
	return err == nil && cs.AutoSave
}

// invoiceMessage es la factura con sus avisos (si ya estaba guardada o si es electrónica)
// e indica si es un duplicado.
func (h *handler) invoiceMessage(ctx context.Context, chatID int64, inv invoice.Invoice, issues []invoice.Issue) (string, bool) {
	text := FormatInvoice(inv, issues)
	saved, err := h.deps.Store.IsSaved(ctx, chatID, inv)
	if err != nil {
		h.logger.Warn("no se pudo buscar duplicados", "chat_id", chatID, "error", err)
	}
	if saved {
		text += "\n\n" + AlreadySavedNote
	}
	if inv.CDC != "" {
		text += "\n\n" + ElectronicNote
	}
	return text, saved
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
		h.track(ctx, chatID, store.Event{Kind: store.EventBadCorrection, Detail: pending.Field})
		h.send(ctx, b, chatID, "❌ "+err.Error()+"\n"+retryOrCancelHint, nil)
		return
	}
	if err := h.deps.Store.UpdateInvoice(ctx, chatID, rec.ID, edited); err != nil {
		h.logger.Error("no se pudo guardar la corrección", "chat_id", chatID, "error", err)
		h.send(ctx, b, chatID, StoreErrorMessage, nil)
		return
	}
	h.track(ctx, chatID, store.Event{Kind: store.EventCorrection, Detail: pending.Field})
	if err := h.deps.Store.ClearAwaiting(ctx, chatID); err != nil {
		h.logger.Error("no se pudo cerrar la corrección", "chat_id", chatID, "error", err)
	}
	message, _ := h.invoiceMessage(ctx, chatID, edited, invoice.Validate(edited))
	h.send(ctx, b, chatID, message, mainKeyboard(rec.ID))
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

// commandEvents es el evento que registra cada comando.
var commandEvents = map[string]string{
	startCommand:    store.EventStart,
	summaryCommand:  store.EventSummary,
	cancelCommand:   store.EventCancel,
	rucCommand:      store.EventRUC,
	imputeCommand:   store.EventImpute,
	exportCommand:   store.EventExportPreview,
	listCommand:     store.EventList,
	pendingCommand:  store.EventPending,
	autoSaveCommand: store.EventAutoSave,
}

// readEvent describe el resultado de una lectura, sin datos de la factura.
func readEvent(inv invoice.Invoice, issues []invoice.Issue, seconds, cost float64) store.Event {
	e := store.Event{Kind: store.EventRead, Detail: store.EventDetailOK, Seconds: seconds, CostUSD: cost}
	switch {
	case !inv.IsInvoice:
		e.Kind, e.Detail = store.EventNotInvoice, ""
	case len(issues) > 0 || len(inv.UncertainFields) > 0:
		e.Detail = store.EventDetailIssues
	}
	return e
}

// track registra un evento para las métricas. Si falla, el usuario no se entera.
// Usa su propio plazo: una lectura que venció por timeout también tiene que quedar registrada.
func (h *handler) track(ctx context.Context, chatID int64, e store.Event) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), trackTimeout)
	defer cancel()
	if err := h.deps.Store.LogEvent(ctx, chatID, e); err != nil {
		h.logger.Warn("no se pudo registrar la métrica", "evento", e.Kind, "error", err)
	}
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
