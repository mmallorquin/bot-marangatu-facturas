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
	SetAwaitingRUC(ctx context.Context, chatID int64, on bool, setupKey ...string) error
	DeleteChat(ctx context.Context, chatID int64) (int, error)
	SetReminders(ctx context.Context, chatID int64, on bool) error
	ReminderCandidates(ctx context.Context, period string) ([]store.ReminderCandidate, error)
	MarkReminded(ctx context.Context, chatID int64, period string) error
	Settings(ctx context.Context, chatID int64) (store.ChatSettings, error)
	SetRUC(ctx context.Context, chatID int64, ruc string) error
	SetImputations(ctx context.Context, chatID int64, imp store.Imputations) error
	SetRegistration(ctx context.Context, chatID int64, ruc string, registration store.Registration) error
	SetPendingExport(ctx context.Context, chatID int64, period string) error
	NextExportSeq(ctx context.Context, chatID int64, period string) (int, error)
	CreateExportRequest(ctx context.Context, chatID int64, period, key string) error
	CreateExportPreview(ctx context.Context, chatID int64, period, key, revision string) error
	ExportDeliveredRevision(ctx context.Context, chatID int64, period, key string) (string, error)
	ExportPresentation(ctx context.Context, chatID int64, period string) (store.Presentation, error)
	MarkUserPresented(ctx context.Context, chatID int64, period, key string) error
	ExportRequestStatus(ctx context.Context, chatID int64, period, key string) (bool, error)
	ReserveExport(ctx context.Context, chatID int64, period, key string) (int, bool, error)
	MarkExportDelivered(ctx context.Context, chatID int64, period, key string) error
	CancelExport(ctx context.Context, chatID int64, period, key string) error
	LogEvent(ctx context.Context, chatID int64, e store.Event) error
}

// Deps son las dependencias del handler.
type Deps struct {
	Logger     *slog.Logger
	Token      string // solo para ocultarlo en los logs de error
	Reader     reader.Reader
	Store      InvoiceStore
	Now        func() time.Time // reloj; en tests se fija una fecha
	Operations *ChatOperations  // compartido con los recordatorios
}

// NewHandler devuelve el handler que responde a mensajes y botones.
func NewHandler(deps Deps) bot.HandlerFunc {
	return newHandler(deps).handle
}

func newHandler(deps Deps) *handler {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.Operations == nil {
		deps.Operations = NewChatOperations()
	}
	return &handler{deps: deps, logger: deps.Logger, albums: &albums{}}
}

type handler struct {
	deps   Deps
	logger *slog.Logger
	albums *albums
}

func (h *handler) handle(ctx context.Context, b *bot.Bot, update *models.Update) {
	if update == nil {
		return
	}
	switch {
	case update.CallbackQuery != nil:
		query := update.CallbackQuery
		msg := query.Message.Message
		if msg == nil || msg.Chat.Type != models.ChatTypePrivate || msg.Chat.ID <= 0 ||
			(query.From.ID != 0 && query.From.ID != msg.Chat.ID) {
			h.answer(ctx, b, query.ID, "Usá el bot desde tu chat privado.", true)
			return
		}
		h.withChat(ctx, msg.Chat.ID, func(ctx context.Context) { h.handleCallback(ctx, b, query) })
	case update.Message != nil:
		msg := update.Message
		if msg.Chat.Type != models.ChatTypePrivate || msg.Chat.ID <= 0 {
			if msg.Chat.ID != 0 {
				h.send(ctx, b, msg.Chat.ID, "Para cuidar tus facturas, usá el bot desde tu chat privado.", nil)
			}
			return
		}
		file, kind := imageFileOf(msg)
		if kind == imageSupported {
			h.handleImageMessage(ctx, b, msg, file)
			return
		}
		h.withChat(ctx, msg.Chat.ID, func(ctx context.Context) { h.handleMessage(ctx, b, msg) })
	}
}

func (h *handler) handleMessage(ctx context.Context, b *bot.Bot, msg *models.Message) {
	chatID := msg.Chat.ID
	_, kind := imageFileOf(msg)
	switch kind {
	case notAnImage:
		h.handleText(ctx, b, chatID, msg.Text)
	case imageUnsupported:
		h.track(ctx, chatID, store.Event{Kind: store.EventPhotoRejected, Detail: store.EventDetailFormat})
		h.send(ctx, b, chatID, UnsupportedFormatMessage, nil)
	case imageTooLarge:
		h.track(ctx, chatID, store.Event{Kind: store.EventPhotoRejected, Detail: store.EventDetailSize})
		h.send(ctx, b, chatID, TooLargeMessage, nil)
	}
}

func (h *handler) handleImageMessage(ctx context.Context, b *bot.Bot, msg *models.Message, file imageFile) {
	c := h.deps.Operations.chat(msg.Chat.ID)
	c.mu.Lock()
	ctx = operationContext(ctx, c)
	op := currentOperation(ctx)
	ctx, cancel := context.WithCancel(ctx)
	c.reads[op] = cancel
	detail := store.EventDetailImage
	if file.mimeType == pdfMimeType {
		detail = store.EventDetailPDF
	}
	h.track(ctx, msg.Chat.ID, store.Event{Kind: store.EventPhoto, Detail: detail})
	switch {
	case msg.MediaGroupID == "":
		h.send(ctx, b, msg.Chat.ID, ReadingMessage, nil)
	case h.albums.first(msg.MediaGroupID, time.Now()):
		h.send(ctx, b, msg.Chat.ID, ReadingAlbumMessage, nil)
	}
	c.mu.Unlock()
	defer func() {
		cancel()
		c.mu.Lock()
		delete(c.reads, op)
		c.mu.Unlock()
	}()
	h.processImage(ctx, b, msg.Chat.ID, file)
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
		h.welcome(ctx, b, chatID)
		return
	case helpCommand, "/help":
		h.send(ctx, b, chatID, UsageMessage, nil)
		return
	case settingsCommand:
		h.showSettings(ctx, b, chatID)
		return
	case rucCommand:
		h.setRUC(ctx, b, chatID, text)
		return
	case imputeCommand:
		h.setImputations(ctx, b, chatID, text)
		return
	case registrationCommand:
		h.setRegistration(ctx, b, chatID, text)
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
	case remindersCommand:
		h.setReminders(ctx, b, chatID, text)
		return
	case deleteDataCommand:
		h.askDeleteData(ctx, b, chatID)
		return
	}

	pending, found, err := h.deps.Store.Awaiting(ctx, chatID)
	if err != nil {
		h.logger.Error("no se pudo leer la corrección pendiente", "chat_id", chatID, "error", err)
	}
	if !found {
		if commandOf(text) == "" && h.tryOnboardingRUC(ctx, b, chatID, text) {
			return
		}
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
	select {
	case h.deps.Operations.reads <- struct{}{}:
		defer func() { <-h.deps.Operations.reads }()
	case <-ctx.Done():
		h.finishRead(ctx, func(ctx context.Context) {
			h.track(ctx, chatID, store.Event{Kind: store.EventReadError, Detail: store.EventDetailDownload})
			h.send(ctx, b, chatID, ReadErrorMessage, nil)
		})
		return
	}

	data, err := downloadFile(ctx, b, file.fileID)
	if err != nil {
		h.finishRead(ctx, func(ctx context.Context) {
			h.logger.Error("no se pudo descargar la imagen", "chat_id", chatID, "error", Redact(err, h.deps.Token))
			h.track(ctx, chatID, store.Event{Kind: store.EventReadError, Detail: store.EventDetailDownload})
			h.send(ctx, b, chatID, ReadErrorMessage, nil)
		})
		return
	}

	start := time.Now()
	result, err := h.deps.Reader.Read(ctx, reader.Image{Data: data, MimeType: file.mimeType})
	if err != nil {
		h.finishRead(ctx, func(ctx context.Context) {
			h.logger.Error("no se pudo leer la factura", "chat_id", chatID, "error", err)
			h.track(ctx, chatID, store.Event{
				Kind: store.EventReadError, Detail: store.EventDetailAI, Seconds: time.Since(start).Seconds(),
			})
			h.send(ctx, b, chatID, readFailureMessage(err), nil)
		})
		return
	}
	h.finishRead(ctx, func(ctx context.Context) { h.showReadInvoice(ctx, b, chatID, result, time.Since(start).Seconds()) })
}

// finishRead confirma que el usuario no borró datos durante la lectura y usa un
// plazo nuevo para guardar/responder, aunque haya vencido el plazo de la IA.
func (h *handler) finishRead(ctx context.Context, finish func(context.Context)) {
	op := currentOperation(ctx)
	op.chat.mu.Lock()
	defer op.chat.mu.Unlock()
	if op.generation != op.chat.generation {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	finish(ctx)
}

func (h *handler) showReadInvoice(ctx context.Context, b *bot.Bot, chatID int64, result reader.Result, seconds float64) {
	inv := result.Invoice
	// Se conserva el resultado del lector intacto para guardarlo como original.
	inv.Number = invoice.NormalizeNumber(inv.Number)
	issues := invoice.Validate(inv)
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
	stoppedSetup := h.stopOnboarding(ctx, chatID)
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
	case stoppedSetup:
		h.send(ctx, b, chatID, "Listo, cargás tu RUC después con /ruc 1234567-8.", nil)
	default:
		h.send(ctx, b, chatID, NothingToCancel, nil)
	}
}

func (h *handler) sendSummary(ctx context.Context, b *bot.Bot, chatID int64, text string) {
	period, err := parsePeriod(text, h.deps.Now().In(reminderLocation))
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
	startCommand:     store.EventStart,
	summaryCommand:   store.EventSummary,
	cancelCommand:    store.EventCancel,
	rucCommand:       store.EventRUC,
	imputeCommand:    store.EventImpute,
	exportCommand:    store.EventExportPreview,
	listCommand:      store.EventList,
	pendingCommand:   store.EventPending,
	autoSaveCommand:  store.EventAutoSave,
	remindersCommand: store.EventReminderSetting,
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
	if op := currentOperation(ctx); op != nil && op.generation != op.chat.generation {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), trackTimeout)
	defer cancel()
	if err := h.deps.Store.LogEvent(ctx, chatID, e); err != nil {
		h.logger.Warn("no se pudo registrar la métrica", "evento", e.Kind, "error", err)
	}
}

// send envía un mensaje; keyboard puede ser nil.
func (h *handler) send(ctx context.Context, b *bot.Bot, chatID int64, text string, keyboard *models.InlineKeyboardMarkup) error {
	params := &bot.SendMessageParams{ChatID: chatID, Text: telegramMessageText(text)}
	if keyboard != nil {
		params.ReplyMarkup = keyboard
	}
	_, err := b.SendMessage(ctx, params)
	if err != nil {
		h.logger.Error("no se pudo responder", "chat_id", chatID, "error", Redact(err, h.deps.Token))
	}
	return err
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
