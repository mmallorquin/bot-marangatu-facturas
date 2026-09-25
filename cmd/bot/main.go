// Comando bot: arranca el bot de Telegram y queda escuchando mensajes.
package main

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/go-telegram/bot"
	"github.com/joho/godotenv"

	"github.com/mmallorquin/bot-marangatu-facturas/internal/config"
	"github.com/mmallorquin/bot-marangatu-facturas/internal/openrouter"
	"github.com/mmallorquin/bot-marangatu-facturas/internal/telegram"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, logger); err != nil {
		logger.Error("el bot se detuvo", "error", err)
		os.Exit(1)
	}
}

// run arranca el bot y bloquea hasta que ctx se cancela.
// extraOpts permite a los tests apuntar el bot a un servidor falso.
func run(ctx context.Context, logger *slog.Logger, extraOpts ...bot.Option) error {
	// El .env es opcional: en un servidor las variables pueden venir del entorno.
	if err := godotenv.Load(); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}

	invoiceReader := openrouter.New(openrouter.Options{
		APIKey: cfg.OpenRouterAPIKey,
		Model:  cfg.OpenRouterModel,
		ZDR:    cfg.OpenRouterZDR,

		ReasoningEffort: cfg.ReasoningEffort,
	})

	token := cfg.TelegramBotToken
	opts := append([]bot.Option{
		bot.WithDefaultHandler(telegram.NewHandler(logger, token, invoiceReader)),
		bot.WithErrorsHandler(telegram.NewErrorsHandler(logger, token)),
	}, extraOpts...)

	b, err := bot.New(token, opts...)
	if err != nil {
		return errors.New(telegram.Redact(err, token))
	}

	logger.Info("bot iniciado, esperando facturas (Ctrl+C para detener)",
		"modelo", cfg.OpenRouterModel, "zdr", cfg.OpenRouterZDR, "razonamiento", cfg.ReasoningEffort)
	b.Start(ctx)
	logger.Info("bot detenido")
	return nil
}
