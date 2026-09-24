package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot"

	"github.com/mmallorquin/bot-marangatu-facturas/internal/config"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// setValidEnv deja las variables obligatorias configuradas.
func setValidEnv(t *testing.T, token string) {
	t.Helper()
	t.Setenv(config.TokenEnvVar, token)
	t.Setenv(config.OpenRouterKeyVar, "sk-or-test")
}

func TestRunFailsWithClearErrorWhenTokenIsMissing(t *testing.T) {
	// Arrange
	t.Setenv(config.TokenEnvVar, "")

	// Act
	err := run(context.Background(), discardLogger())

	// Assert
	if !errors.Is(err, config.ErrMissingToken) {
		t.Errorf("error = %v, se esperaba ErrMissingToken", err)
	}
}

func TestRunHidesTokenWhenTelegramRejectsIt(t *testing.T) {
	// Arrange: Telegram responde que el token no es válido
	const token = "123456:ABC-invalido"
	setValidEnv(t, token)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"ok":false,"error_code":401,"description":"Unauthorized"}`)
	}))
	defer server.Close()

	// Act
	err := run(context.Background(), discardLogger(), bot.WithServerURL(server.URL))

	// Assert
	if err == nil {
		t.Fatal("se esperaba un error por token inválido")
	}
	if strings.Contains(err.Error(), token) {
		t.Errorf("el error expone el token: %v", err)
	}
}

func TestRunStopsCleanlyWhenContextIsCancelled(t *testing.T) {
	// Arrange
	setValidEnv(t, "123456:ABC-token")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"ok":true,"result":[]}`)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	// Act
	err := run(ctx, discardLogger(), bot.WithServerURL(server.URL), bot.WithSkipGetMe())

	// Assert
	if err != nil {
		t.Errorf("error inesperado al detener el bot: %v", err)
	}
}
