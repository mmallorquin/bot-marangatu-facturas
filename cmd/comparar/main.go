// Comando comparar: lee las fotos de una carpeta con varios modelos y niveles de
// razonamiento, y muestra una tabla con resultados, costo y tiempo.
//
//	go run ./cmd/comparar -modelos deepseek/deepseek-v4.1-flash,google/gemini-3.1-flash-lite -razonamiento defecto,none
//
// Ojo: cada foto × configuración es una llamada paga a OpenRouter.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"slices"
	"strings"
	"sync/atomic"
	"syscall"

	"github.com/joho/godotenv"

	"github.com/mmallorquin/bot-marangatu-facturas/internal/benchmark"
	"github.com/mmallorquin/bot-marangatu-facturas/internal/config"
	"github.com/mmallorquin/bot-marangatu-facturas/internal/openrouter"
	"github.com/mmallorquin/bot-marangatu-facturas/internal/reader"
)

const (
	defaultModels = config.DefaultModel + ",google/gemini-3.1-flash-lite,anthropic/claude-haiku-4.5"
	// defaultEffortName es la palabra que representa "no enviar razonamiento".
	defaultEffortName = "defecto"
)

var validEfforts = []string{defaultEffortName, "none", "minimal", "low", "medium", "high"}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	models := flag.String("modelos", defaultModels, "modelos de OpenRouter separados por coma")
	efforts := flag.String("razonamiento", defaultEffortName, "niveles separados por coma: "+strings.Join(validEfforts, ", "))
	dir := flag.String("carpeta", "facturas", "carpeta con las fotos")
	parallel := flag.Int("paralelo", 4, "lecturas simultáneas")
	zdr := flag.Bool("zdr", true, "usar solo proveedores con retención cero")
	flag.Parse()

	if err := godotenv.Load(); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	apiKey := strings.TrimSpace(os.Getenv(config.OpenRouterKeyVar))
	if apiKey == "" {
		return fmt.Errorf("%w: completá %s en .env", config.ErrMissingOpenRouterKey, config.OpenRouterKeyVar)
	}

	setups, err := parseSetups(*models, *efforts)
	if err != nil {
		return err
	}
	photos, err := benchmark.LoadPhotos(*dir)
	if err != nil {
		return err
	}
	if len(photos) == 0 {
		return fmt.Errorf("no hay fotos JPG, PNG o WEBP en %s", *dir)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	total := len(photos) * len(setups)
	fmt.Fprintf(os.Stderr, "Leyendo %d fotos × %d configuraciones = %d llamadas a OpenRouter\n\n", len(photos), len(setups), total)

	newReader := func(s benchmark.Setup) reader.Reader {
		return openrouter.New(openrouter.Options{APIKey: apiKey, Model: s.Model, ZDR: *zdr, ReasoningEffort: s.Effort})
	}
	runs := benchmark.Execute(ctx, photos, setups, newReader, *parallel, progressPrinter(total))

	fmt.Println(benchmark.FormatTables(benchmark.Summarize(runs), runs))
	return nil
}

// parseSetups combina cada modelo con cada nivel de razonamiento.
func parseSetups(models, efforts string) ([]benchmark.Setup, error) {
	modelList := splitList(models)
	if len(modelList) == 0 {
		return nil, errors.New("indicá al menos un modelo con -modelos")
	}

	var setups []benchmark.Setup
	for _, model := range modelList {
		for _, effort := range splitList(efforts) {
			if !slices.Contains(validEfforts, effort) {
				return nil, fmt.Errorf("razonamiento %q inválido: usá %s", effort, strings.Join(validEfforts, ", "))
			}
			if effort == defaultEffortName {
				effort = ""
			}
			setups = append(setups, benchmark.Setup{Model: model, Effort: effort})
		}
	}
	return setups, nil
}

func splitList(s string) []string {
	var items []string
	for _, item := range strings.Split(s, ",") {
		if item = strings.TrimSpace(item); item != "" {
			items = append(items, item)
		}
	}
	return items
}

// progressPrinter muestra una línea por corrida terminada (sin datos de la factura).
func progressPrinter(total int) func(benchmark.Run) {
	var done atomic.Int32
	return func(r benchmark.Run) {
		n := done.Add(1)
		status := fmt.Sprintf("$%.4f", r.Result.CostUSD)
		if r.Err != nil {
			status = "ERROR: " + r.Err.Error()
		}
		fmt.Fprintf(os.Stderr, "[%d/%d] %s · %s · %.1fs · %s\n", n, total, r.Photo, r.Setup.Label(), r.Duration.Seconds(), status)
	}
}
