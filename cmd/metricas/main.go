// Comando metricas: muestra cómo se usa el bot, leyendo la base sin modificarla.
// Es solo para quien administra el servidor: el bot no muestra nada de esto en Telegram.
//
//	go run ./cmd/metricas -dias 7 -excluir 123456789 -usuarios
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mmallorquin/bot-marangatu-facturas/internal/config"
	"github.com/mmallorquin/bot-marangatu-facturas/internal/store"
)

func main() {
	if err := run(os.Args[1:], time.Now()); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string, now time.Time) error {
	flags := flag.NewFlagSet("metricas", flag.ContinueOnError)
	dbPath := flags.String("db", defaultDatabasePath(), "archivo SQLite del bot")
	days := flags.Int("dias", 7, "últimos N días; 0 = todo el historial")
	exclude := flags.String("excluir", "", "chat_id separados por coma que no cuentan (tus pruebas)")
	perUser := flags.Bool("usuarios", false, "agregar el detalle por usuario (muestra chat_id)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *days < 0 {
		return fmt.Errorf("-dias no puede ser negativo")
	}
	excluded, err := parseChatIDs(*exclude)
	if err != nil {
		return fmt.Errorf("-excluir: %w", err)
	}

	db, err := store.OpenReadOnly(*dbPath)
	if err != nil {
		return err
	}
	defer db.Close()

	var since time.Time // cero = desde el principio
	if *days > 0 {
		since = now.AddDate(0, 0, -*days)
	}
	m, err := db.Metrics(context.Background(), store.MetricsQuery{Since: since, Now: now, Exclude: excluded})
	if err != nil {
		return err
	}
	fmt.Print(formatReport(m, *days, len(excluded), *perUser))
	return nil
}

func defaultDatabasePath() string {
	if path := strings.TrimSpace(os.Getenv(config.DatabasePathVar)); path != "" {
		return path
	}
	return config.DefaultDatabasePath
}

func parseChatIDs(value string) ([]int64, error) {
	var ids []int64
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item == "" {
			continue
		}
		id, err := strconv.ParseInt(item, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%q no es un chat_id", item)
		}
		ids = append(ids, id)
	}
	return ids, nil
}
