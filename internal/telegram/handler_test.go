package telegram

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/mmallorquin/bot-marangatu-facturas/internal/invoice"
	"github.com/mmallorquin/bot-marangatu-facturas/internal/reader"
	"github.com/mmallorquin/bot-marangatu-facturas/internal/store"
)

const (
	testToken     = "123456:ABC-token"
	testChatID    = int64(42)
	fakeImageData = "bytes-de-la-foto"
)

// outgoing es un pedido que el bot le hizo a Telegram.
type outgoing struct {
	method string // sendMessage, editMessageText, editMessageReplyMarkup, answerCallbackQuery
	text   string
	markup string // reply_markup en JSON
	alert  bool
}

// fakeTelegram simula la API de Telegram: mensajes, ediciones, botones, getFile y descarga.
type fakeTelegram struct {
	mu              sync.Mutex
	requests        []outgoing
	requestedFileID string
	failGetFile     bool
}

func (f *fakeTelegram) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_ = r.ParseMultipartForm(1 << 20)
	method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]

	switch {
	case strings.HasPrefix(r.URL.Path, "/file/bot"):
		_, _ = io.WriteString(w, fakeImageData)
		return
	case method == "getFile":
		f.requestedFileID = r.FormValue("file_id")
		if f.failGetFile {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"ok":false,"error_code":400,"description":"file not found"}`)
			return
		}
		_, _ = io.WriteString(w, `{"ok":true,"result":{"file_id":"x","file_path":"photos/factura.jpg"}}`)
		return
	case method == "answerCallbackQuery":
		f.requests = append(f.requests, outgoing{method: method, text: r.FormValue("text"), alert: r.FormValue("show_alert") == "true"})
		_, _ = io.WriteString(w, `{"ok":true,"result":true}`)
		return
	}

	f.requests = append(f.requests, outgoing{method: method, text: r.FormValue("text"), markup: r.FormValue("reply_markup")})
	_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":10,"chat":{"id":42}}}`)
}

// byMethod devuelve los pedidos de un tipo, en orden.
func (f *fakeTelegram) byMethod(method string) []outgoing {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []outgoing
	for _, r := range f.requests {
		if r.method == method {
			out = append(out, r)
		}
	}
	return out
}

func (f *fakeTelegram) lastSent(t *testing.T) outgoing {
	t.Helper()
	sent := f.byMethod("sendMessage")
	if len(sent) == 0 {
		t.Fatal("el bot no envió ningún mensaje")
	}
	return sent[len(sent)-1]
}

// fakeReader devuelve un resultado fijo y guarda las imágenes recibidas.
type fakeReader struct {
	result   reader.Result
	err      error
	received []reader.Image
}

func (f *fakeReader) Read(_ context.Context, img reader.Image) (reader.Result, error) {
	f.received = append(f.received, img)
	return f.result, f.err
}

// harness arma el bot con Telegram falso, base SQLite real y lector falso.
type harness struct {
	telegram *fakeTelegram
	bot      *bot.Bot
	store    *store.Store
	reader   *fakeReader
	handle   bot.HandlerFunc
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	fake := &fakeTelegram{}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)

	b, err := bot.New(testToken, bot.WithServerURL(server.URL), bot.WithSkipGetMe())
	if err != nil {
		t.Fatalf("no se pudo crear el bot: %v", err)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "facturas.db"))
	if err != nil {
		t.Fatalf("no se pudo abrir la base: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	rdr := &fakeReader{result: reader.Result{Invoice: sampleInvoice(), Model: "modelo-test", CostUSD: 0.001}}
	handle := NewHandler(Deps{
		Logger: discardLogger(),
		Token:  testToken,
		Reader: rdr,
		Store:  st,
		Now:    func() time.Time { return time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC) },
	})
	return &harness{telegram: fake, bot: b, store: st, reader: rdr, handle: handle}
}

func (h *harness) send(update *models.Update) {
	h.handle(context.Background(), h.bot, update)
}

func (h *harness) sendText(text string) {
	h.send(&models.Update{Message: &models.Message{Chat: models.Chat{ID: testChatID}, Text: text}})
}

func (h *harness) sendPhoto() {
	h.send(&models.Update{Message: &models.Message{
		Chat: models.Chat{ID: testChatID},
		Photo: []models.PhotoSize{
			{FileID: "chica", Width: 90, Height: 120},
			{FileID: "grande", Width: 1280, Height: 1706},
		},
	}})
}

func (h *harness) press(c callback) {
	h.send(&models.Update{CallbackQuery: &models.CallbackQuery{
		ID:   "cb",
		Data: c.encode(),
		Message: models.MaybeInaccessibleMessage{
			Type:    models.MaybeInaccessibleMessageTypeMessage,
			Message: &models.Message{ID: 10, Chat: models.Chat{ID: testChatID}},
		},
	}})
}

// firstDraftID lee la foto y devuelve el ID del borrador creado.
func (h *harness) firstDraftID(t *testing.T) int64 {
	t.Helper()
	h.sendPhoto()
	return 1 // la base es nueva en cada test: el primer borrador es el 1
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestPhotoIsReadAndShownWithButtons(t *testing.T) {
	// Arrange
	h := newHarness(t)

	// Act
	h.sendPhoto()

	// Assert
	if h.telegram.requestedFileID != "grande" {
		t.Errorf("se descargó %q, se esperaba la foto de mayor resolución", h.telegram.requestedFileID)
	}
	if len(h.reader.received) != 1 || string(h.reader.received[0].Data) != fakeImageData {
		t.Fatalf("el lector no recibió la foto: %+v", h.reader.received)
	}
	sent := h.telegram.byMethod("sendMessage")
	if len(sent) != 2 || sent[0].text != ReadingMessage {
		t.Fatalf("mensajes = %+v", sent)
	}
	if !strings.Contains(sent[1].text, "80000519-8") || !strings.Contains(sent[1].markup, `"g:1"`) {
		t.Errorf("la factura no se mostró con botones: %+v", sent[1])
	}
	if rec, err := h.store.Get(context.Background(), testChatID, 1); err != nil || rec.Status != store.StatusDraft {
		t.Errorf("no se creó el borrador: %+v, %v", rec, err)
	}
}

func TestNonInvoiceImageDoesNotCreateDraft(t *testing.T) {
	h := newHarness(t)
	h.reader.result = reader.Result{Invoice: invoice.Invoice{IsInvoice: false}}

	h.sendPhoto()

	if got := h.telegram.lastSent(t); got.text != NotInvoiceMessage || got.markup != "" {
		t.Errorf("respuesta = %+v", got)
	}
	if _, err := h.store.Get(context.Background(), testChatID, 1); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("no debería haber borrador: %v", err)
	}
}

func TestSaveButtonSavesAValidInvoice(t *testing.T) {
	// Arrange
	h := newHarness(t)
	id := h.firstDraftID(t)

	// Act
	h.press(callback{action: actionSave, id: id})

	// Assert
	edits := h.telegram.byMethod("editMessageText")
	if len(edits) != 1 || !strings.Contains(edits[0].text, SavedNote) {
		t.Fatalf("ediciones = %+v", edits)
	}
	if rec, _ := h.store.Get(context.Background(), testChatID, id); rec.Status != store.StatusSaved {
		t.Errorf("estado = %q, se esperaba guardada", rec.Status)
	}
	if answers := h.telegram.byMethod("answerCallbackQuery"); len(answers) != 1 {
		t.Errorf("siempre hay que responder al botón: %+v", answers)
	}
}

func TestSaveButtonRefusesInvoicesWithProblems(t *testing.T) {
	// Arrange
	h := newHarness(t)
	broken := sampleInvoice()
	broken.Total = 999
	h.reader.result = reader.Result{Invoice: broken}
	id := h.firstDraftID(t)

	// Act
	h.press(callback{action: actionSave, id: id})

	// Assert
	answers := h.telegram.byMethod("answerCallbackQuery")
	if len(answers) != 1 || !answers[0].alert || answers[0].text != FixBeforeSavingAlert {
		t.Errorf("respuesta al botón = %+v", answers)
	}
	if rec, _ := h.store.Get(context.Background(), testChatID, id); rec.Status != store.StatusDraft {
		t.Errorf("no debería guardarse: estado %q", rec.Status)
	}
}

func TestSaveButtonWarnsAboutDuplicates(t *testing.T) {
	h := newHarness(t)
	h.sendPhoto()
	h.sendPhoto()
	h.press(callback{action: actionSave, id: 1})

	h.press(callback{action: actionSave, id: 2})

	edits := h.telegram.byMethod("editMessageText")
	if len(edits) != 2 || !strings.Contains(edits[1].text, DuplicateNote) || !strings.Contains(edits[1].markup, `"d:2"`) {
		t.Errorf("ediciones = %+v", edits)
	}
}

func TestDiscardButton(t *testing.T) {
	h := newHarness(t)
	id := h.firstDraftID(t)

	h.press(callback{action: actionDiscard, id: id})

	if edits := h.telegram.byMethod("editMessageText"); len(edits) != 1 || edits[0].text != DiscardedMessage {
		t.Errorf("ediciones = %+v", edits)
	}
	if rec, _ := h.store.Get(context.Background(), testChatID, id); rec.Status != store.StatusDiscarded {
		t.Errorf("estado = %q", rec.Status)
	}
}

func TestEditAndBackButtonsSwitchKeyboards(t *testing.T) {
	h := newHarness(t)
	id := h.firstDraftID(t)

	h.press(callback{action: actionEdit, id: id})
	h.press(callback{action: actionBack, id: id})

	edits := h.telegram.byMethod("editMessageReplyMarkup")
	if len(edits) != 2 || !strings.Contains(edits[0].markup, `"f:1:total"`) || !strings.Contains(edits[1].markup, `"g:1"`) {
		t.Errorf("teclados = %+v", edits)
	}
}

func TestCorrectingAFieldUpdatesTheInvoice(t *testing.T) {
	// Arrange
	h := newHarness(t)
	id := h.firstDraftID(t)

	// Act
	h.press(callback{action: actionField, id: id, field: invoice.FieldIssuerName})
	asked := h.telegram.lastSent(t)
	h.sendText("Nueva Razón S.A.")

	// Assert
	if !strings.Contains(asked.text, "razón social") {
		t.Errorf("no pidió el valor: %q", asked.text)
	}
	updated := h.telegram.lastSent(t)
	if !strings.Contains(updated.text, "Nueva Razón S.A.") || !strings.Contains(updated.markup, `"g:1"`) {
		t.Errorf("no mostró la factura corregida con botones: %+v", updated)
	}
	rec, _ := h.store.Get(context.Background(), testChatID, id)
	if rec.Invoice.IssuerName != "Nueva Razón S.A." || !rec.Corrected || rec.Original.IssuerName == "Nueva Razón S.A." {
		t.Errorf("registro = %+v", rec)
	}
	if _, pending, _ := h.store.Awaiting(context.Background(), testChatID); pending {
		t.Error("la corrección debería haber terminado")
	}
}

func TestInvalidCorrectionKeepsWaiting(t *testing.T) {
	h := newHarness(t)
	id := h.firstDraftID(t)
	h.press(callback{action: actionField, id: id, field: invoice.FieldTotal})

	h.sendText("muchísimo")

	if got := h.telegram.lastSent(t); !strings.Contains(got.text, "no es válido") {
		t.Errorf("respuesta = %q", got.text)
	}
	if _, pending, _ := h.store.Awaiting(context.Background(), testChatID); !pending {
		t.Error("debería seguir esperando el valor")
	}
}

func TestCancelCommandStopsCorrection(t *testing.T) {
	h := newHarness(t)
	id := h.firstDraftID(t)
	h.press(callback{action: actionField, id: id, field: invoice.FieldTotal})

	h.sendText("/cancelar")

	if got := h.telegram.lastSent(t); got.text != CancelledMessage {
		t.Errorf("respuesta = %q", got.text)
	}
	if _, pending, _ := h.store.Awaiting(context.Background(), testChatID); pending {
		t.Error("no debería quedar la corrección pendiente")
	}
}

func TestSummaryCommandShowsSavedInvoicesOfTheMonth(t *testing.T) {
	h := newHarness(t)
	h.sendPhoto()
	h.press(callback{action: actionSave, id: 1})

	h.sendText("/resumen")

	if got := h.telegram.lastSent(t); !strings.Contains(got.text, "Septiembre 2026: 1 facturas") {
		t.Errorf("resumen = %q", got.text)
	}
}

func TestButtonsOnAlreadySavedInvoiceAreRemoved(t *testing.T) {
	h := newHarness(t)
	id := h.firstDraftID(t)
	h.press(callback{action: actionSave, id: id})

	h.press(callback{action: actionEdit, id: id})

	answers := h.telegram.byMethod("answerCallbackQuery")
	if len(answers) != 2 || answers[1].text != NoLongerEditableAlert {
		t.Errorf("respuestas = %+v", answers)
	}
}

func TestReaderErrorIsReportedWithoutDraft(t *testing.T) {
	h := newHarness(t)
	h.reader.err = errors.New("OpenRouter caído")

	h.sendPhoto()

	if got := h.telegram.lastSent(t); got.text != ReadErrorMessage {
		t.Errorf("respuesta = %q", got.text)
	}
}

func TestDownloadErrorSkipsTheReader(t *testing.T) {
	h := newHarness(t)
	h.telegram.failGetFile = true

	h.sendPhoto()

	if len(h.reader.received) != 0 || h.telegram.lastSent(t).text != ReadErrorMessage {
		t.Errorf("lector llamado %d veces", len(h.reader.received))
	}
}

func TestUnsupportedFilesAreRejectedWithoutCallingTheReader(t *testing.T) {
	cases := map[string]struct {
		doc  *models.Document
		want string
	}{
		"PDF":        {&models.Document{FileID: "d", MimeType: "application/pdf"}, UnsupportedFormatMessage},
		"muy grande": {&models.Document{FileID: "d", MimeType: "image/jpeg", FileSize: maxImageBytes + 1}, TooLargeMessage},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)

			h.send(&models.Update{Message: &models.Message{Chat: models.Chat{ID: testChatID}, Document: tc.doc}})

			if len(h.reader.received) != 0 || h.telegram.lastSent(t).text != tc.want {
				t.Errorf("lector llamado %d veces, respuesta %q", len(h.reader.received), h.telegram.lastSent(t).text)
			}
		})
	}
}

func TestTextWithoutPendingCorrectionGetsHelp(t *testing.T) {
	h := newHarness(t)

	h.sendText("hola")

	if got := h.telegram.lastSent(t); got.text != HelpMessage {
		t.Errorf("respuesta = %q", got.text)
	}
}

func TestUpdatesWithoutMessageOrCallbackAreIgnored(t *testing.T) {
	h := newHarness(t)

	h.send(&models.Update{})

	if len(h.telegram.requests) != 0 {
		t.Errorf("no se esperaban pedidos a Telegram: %+v", h.telegram.requests)
	}
}

func TestErrorsHandlerLogsWithoutToken(t *testing.T) {
	// Arrange
	var logs strings.Builder
	handleError := NewErrorsHandler(slog.New(slog.NewTextHandler(&logs, nil)), testToken)

	// Act
	handleError(errorWithToken(testToken))

	// Assert
	if strings.Contains(logs.String(), testToken) || !strings.Contains(logs.String(), "<token>") {
		t.Errorf("el token no se enmascaró: %s", logs.String())
	}
}

func TestErrorsHandlerIgnoresNormalShutdown(t *testing.T) {
	var logs strings.Builder
	handleError := NewErrorsHandler(slog.New(slog.NewTextHandler(&logs, nil)), "x")

	handleError(context.Canceled)

	if logs.Len() != 0 {
		t.Errorf("no se esperaba log al apagar el bot, se obtuvo: %s", logs.String())
	}
}

type errorWithToken string

func (e errorWithToken) Error() string {
	return "https://api.telegram.org/bot" + string(e) + "/getUpdates falló"
}
