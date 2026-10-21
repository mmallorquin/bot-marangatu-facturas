package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	_ "modernc.org/sqlite" // para preparar una base dañada

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
	t.Setenv(config.DatabasePathVar, filepath.Join(t.TempDir(), "facturas.db"))
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

func TestRunRegistersTelegramCommandMenu(t *testing.T) {
	setValidEnv(t, "123456:ABC-token")
	commandsPayload := make(chan string, 1)
	menuPayload := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseMultipartForm(1 << 20)
		switch {
		case strings.HasSuffix(r.URL.Path, "/setMyCommands"):
			commandsPayload <- r.FormValue("commands")
			_, _ = io.WriteString(w, `{"ok":true,"result":true}`)
		case strings.HasSuffix(r.URL.Path, "/setChatMenuButton"):
			menuPayload <- r.FormValue("menu_button")
			_, _ = io.WriteString(w, `{"ok":true,"result":true}`)
		default:
			_, _ = io.WriteString(w, `{"ok":true,"result":[]}`)
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if err := run(ctx, discardLogger(), bot.WithServerURL(server.URL), bot.WithSkipGetMe()); err != nil {
		t.Fatalf("run: %v", err)
	}
	var commands []models.BotCommand
	select {
	case payload := <-commandsPayload:
		if err := json.Unmarshal([]byte(payload), &commands); err != nil {
			t.Fatalf("comandos inválidos: %v", err)
		}
	default:
		t.Fatal("no registró los comandos para elegirlos en Telegram")
	}
	if len(commands) != 5 {
		t.Fatalf("el menú principal debe tener cinco entradas: %+v", commands)
	}
	for _, name := range []string{"facturas", "exportar", "estado", "ajustes", "ayuda"} {
		found := false
		for _, command := range commands {
			if command.Command == name && command.Description != "" {
				found = true
			}
		}
		if !found {
			t.Errorf("falta el comando %s con descripción en el menú: %+v", name, commands)
		}
	}
	select {
	case payload := <-menuPayload:
		var menu struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(payload), &menu); err != nil || menu.Type != "commands" {
			t.Errorf("botón de menú = %q, %v", payload, err)
		}
	default:
		t.Error("no habilitó el botón de menú de comandos")
	}
}

func TestRunFailsWhenPendingFilesCannotBeRecovered(t *testing.T) {
	// Arrange: una tabla jobs de una versión incompatible hace fallar la recuperación, no la apertura.
	setValidEnv(t, "123456:ABC-token")
	db, err := sql.Open("sqlite", os.Getenv(config.DatabasePathVar))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE jobs (state TEXT, updated_at TEXT)`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"ok":true,"result":[]}`)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Act
	err = run(ctx, discardLogger(), bot.WithServerURL(server.URL), bot.WithSkipGetMe())

	// Assert
	if err == nil || ctx.Err() != nil {
		t.Fatalf("error = %v: el arranque debía fallar sin esperar al bot", err)
	}
}
