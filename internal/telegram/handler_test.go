package telegram

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/mmallorquin/bot-marangatu-facturas/internal/reader"
)

const (
	testToken     = "123456:ABC-token"
	fakeImageData = "bytes-de-la-foto"
)

// fakeTelegram simula la API de Telegram: responde mensajes, getFile y la descarga del archivo.
type fakeTelegram struct {
	mu              sync.Mutex
	sent            []string
	calls           []string
	requestedFileID string
	failGetFile     bool
}

func (f *fakeTelegram) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, r.URL.Path)
	_ = r.ParseMultipartForm(1 << 20)

	switch {
	case strings.HasPrefix(r.URL.Path, "/file/bot"):
		_, _ = io.WriteString(w, fakeImageData)
		return
	case strings.HasSuffix(r.URL.Path, "/getFile"):
		f.requestedFileID = r.FormValue("file_id")
		if f.failGetFile {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"ok":false,"error_code":400,"description":"file not found"}`)
			return
		}
		_, _ = io.WriteString(w, `{"ok":true,"result":{"file_id":"x","file_path":"photos/factura.jpg"}}`)
		return
	case strings.HasSuffix(r.URL.Path, "/sendMessage"):
		f.sent = append(f.sent, r.FormValue("text"))
	}
	_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":1,"chat":{"id":42}}}`)
}

// fakeReader devuelve un resultado fijo y guarda la imagen recibida.
type fakeReader struct {
	result   reader.Result
	err      error
	received []reader.Image
}

func (f *fakeReader) Read(_ context.Context, img reader.Image) (reader.Result, error) {
	f.received = append(f.received, img)
	return f.result, f.err
}

func newTestBot(t *testing.T, fake *fakeTelegram) *bot.Bot {
	t.Helper()
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)

	b, err := bot.New(testToken, bot.WithServerURL(server.URL), bot.WithSkipGetMe())
	if err != nil {
		t.Fatalf("no se pudo crear el bot: %v", err)
	}
	return b
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func photoUpdate() *models.Update {
	return &models.Update{Message: &models.Message{
		Chat:  models.Chat{ID: 42},
		Photo: []models.PhotoSize{{FileID: "chica", Width: 90, Height: 120}, {FileID: "grande", Width: 1280, Height: 1706}},
	}}
}

func TestHandlerReadsThePhotoAndRepliesWithTheInvoice(t *testing.T) {
	// Arrange
	fake := &fakeTelegram{}
	rdr := &fakeReader{result: reader.Result{Invoice: sampleInvoice()}}
	handler := NewHandler(discardLogger(), testToken, rdr)

	// Act
	handler(context.Background(), newTestBot(t, fake), photoUpdate())

	// Assert
	if fake.requestedFileID != "grande" {
		t.Errorf("se descargó %q, se esperaba la foto de mayor resolución", fake.requestedFileID)
	}
	if len(rdr.received) != 1 || string(rdr.received[0].Data) != fakeImageData || rdr.received[0].MimeType != "image/jpeg" {
		t.Fatalf("el lector no recibió la foto descargada: %+v", rdr.received)
	}
	if len(fake.sent) != 2 || fake.sent[0] != ReadingMessage || !strings.Contains(fake.sent[1], "80000519-8") {
		t.Errorf("mensajes enviados = %q", fake.sent)
	}
}

func TestHandlerRepliesWithErrorWhenReaderFails(t *testing.T) {
	fake := &fakeTelegram{}
	rdr := &fakeReader{err: errors.New("OpenRouter caído")}
	handler := NewHandler(discardLogger(), testToken, rdr)

	handler(context.Background(), newTestBot(t, fake), photoUpdate())

	if len(fake.sent) != 2 || fake.sent[1] != ReadErrorMessage {
		t.Errorf("mensajes enviados = %q, se esperaba el aviso de error", fake.sent)
	}
}

func TestHandlerRepliesWithErrorWhenDownloadFails(t *testing.T) {
	fake := &fakeTelegram{failGetFile: true}
	rdr := &fakeReader{}
	handler := NewHandler(discardLogger(), testToken, rdr)

	handler(context.Background(), newTestBot(t, fake), photoUpdate())

	if len(rdr.received) != 0 {
		t.Error("no se debería llamar a la IA si falló la descarga")
	}
	if len(fake.sent) != 2 || fake.sent[1] != ReadErrorMessage {
		t.Errorf("mensajes enviados = %q", fake.sent)
	}
}

func TestHandlerRejectsUnsupportedFilesWithoutCallingTheReader(t *testing.T) {
	cases := map[string]struct {
		doc  *models.Document
		want string
	}{
		"PDF":        {&models.Document{FileID: "d", MimeType: "application/pdf"}, UnsupportedFormatMessage},
		"muy grande": {&models.Document{FileID: "d", MimeType: "image/jpeg", FileSize: maxImageBytes + 1}, TooLargeMessage},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			fake := &fakeTelegram{}
			rdr := &fakeReader{}
			update := &models.Update{Message: &models.Message{Chat: models.Chat{ID: 42}, Document: tc.doc}}

			NewHandler(discardLogger(), testToken, rdr)(context.Background(), newTestBot(t, fake), update)

			if len(rdr.received) != 0 || len(fake.sent) != 1 || fake.sent[0] != tc.want {
				t.Errorf("lector llamado %d veces, mensajes = %q", len(rdr.received), fake.sent)
			}
		})
	}
}

func TestHandlerRepliesToTextWithHelp(t *testing.T) {
	fake := &fakeTelegram{}
	update := &models.Update{Message: &models.Message{Chat: models.Chat{ID: 42}, Text: "hola"}}

	NewHandler(discardLogger(), testToken, &fakeReader{})(context.Background(), newTestBot(t, fake), update)

	if len(fake.sent) != 1 || fake.sent[0] != HelpMessage {
		t.Errorf("mensajes enviados = %q", fake.sent)
	}
}

func TestHandlerIgnoresUpdatesWithoutMessage(t *testing.T) {
	fake := &fakeTelegram{}

	NewHandler(discardLogger(), testToken, &fakeReader{})(context.Background(), newTestBot(t, fake), &models.Update{})

	if len(fake.calls) != 0 {
		t.Errorf("no se esperaban llamadas a Telegram, hubo %v", fake.calls)
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
