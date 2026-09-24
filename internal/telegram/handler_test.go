package telegram

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// fakeTelegram simula la API de Telegram y guarda los mensajes que el bot envía.
type fakeTelegram struct {
	mu    sync.Mutex
	sent  []string
	calls []string
}

func (f *fakeTelegram) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, r.URL.Path)
	if strings.HasSuffix(r.URL.Path, "/sendMessage") {
		_ = r.ParseMultipartForm(1 << 20)
		f.sent = append(f.sent, r.FormValue("text"))
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":1,"chat":{"id":42}}}`)
}

func newTestBot(t *testing.T, fake *fakeTelegram) *bot.Bot {
	t.Helper()
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)

	b, err := bot.New("123456:ABC-token", bot.WithServerURL(server.URL), bot.WithSkipGetMe())
	if err != nil {
		t.Fatalf("no se pudo crear el bot: %v", err)
	}
	return b
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestHandlerRepliesToThePhotoInTheSameChat(t *testing.T) {
	// Arrange
	fake := &fakeTelegram{}
	b := newTestBot(t, fake)
	handler := NewHandler(discardLogger(), "123456:ABC-token")
	update := &models.Update{Message: &models.Message{
		Chat:  models.Chat{ID: 42},
		Photo: []models.PhotoSize{{FileID: "abc"}},
	}}

	// Act
	handler(context.Background(), b, update)

	// Assert
	if len(fake.sent) != 1 || fake.sent[0] != PhotoReceivedMessage {
		t.Errorf("mensajes enviados = %q, se esperaba [%q]", fake.sent, PhotoReceivedMessage)
	}
}

func TestHandlerIgnoresUpdatesWithoutMessage(t *testing.T) {
	fake := &fakeTelegram{}
	b := newTestBot(t, fake)
	handler := NewHandler(discardLogger(), "123456:ABC-token")

	handler(context.Background(), b, &models.Update{})

	if len(fake.calls) != 0 {
		t.Errorf("no se esperaban llamadas a Telegram, hubo %v", fake.calls)
	}
}

func TestErrorsHandlerLogsWithoutToken(t *testing.T) {
	// Arrange
	var logs strings.Builder
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	handleError := NewErrorsHandler(logger, "123456:ABC-token")

	// Act
	handleError(io.ErrUnexpectedEOF)
	handleError(errorWithToken("123456:ABC-token"))

	// Assert
	if strings.Contains(logs.String(), "123456:ABC-token") {
		t.Errorf("el log contiene el token: %s", logs.String())
	}
	if !strings.Contains(logs.String(), "<token>") {
		t.Errorf("se esperaba el token enmascarado en el log: %s", logs.String())
	}
}

type errorWithToken string

func (e errorWithToken) Error() string {
	return "https://api.telegram.org/bot" + string(e) + "/getUpdates falló"
}

func TestErrorsHandlerIgnoresNormalShutdown(t *testing.T) {
	var logs strings.Builder
	handleError := NewErrorsHandler(slog.New(slog.NewTextHandler(&logs, nil)), "x")

	handleError(context.Canceled)

	if logs.Len() != 0 {
		t.Errorf("no se esperaba log al apagar el bot, se obtuvo: %s", logs.String())
	}
}
