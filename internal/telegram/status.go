package telegram

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/mmallorquin/bot-marangatu-facturas/internal/store"
)

// /estado cuenta los últimos archivos recibidos y deja reintentar los que fallaron.
const (
	statusCommand = "/estado"

	// statusJobsLimit son los archivos más recientes que se cuentan y numeran.
	statusJobsLimit = 50
	// maxRetryButtons acota los botones de un mensaje (Telegram admite 100).
	maxRetryButtons = 10

	NoFilesMessage = "Todavía no recibí archivos. Mandame una foto o PDF de tu factura 📸"
)

// numberedJob es un archivo con su número en la lista ("Archivo 7").
type numberedJob struct {
	n   int
	job store.Job
}

func (h *handler) showStatus(ctx context.Context, b *bot.Bot, chatID int64) {
	jobs, err := h.deps.Store.Jobs(ctx, chatID, statusJobsLimit)
	if err != nil {
		h.logger.Error("no se pudieron leer los archivos", "chat_id", chatID, "error", err)
		h.send(ctx, b, chatID, StoreErrorMessage, nil)
		return
	}
	entries := attentionEntries(jobs, needsAttention)
	h.send(ctx, b, chatID, FormatJobStatus(jobs, entries), retryKeyboard(entries))
}

func needsAttention(j store.Job) bool {
	switch j.State {
	case store.JobFailedDownload, store.JobFailedRead, store.JobUncertain:
		return true
	}
	return false
}

// attentionEntries numera los archivos en orden de llegada y deja los que cumplen keep.
func attentionEntries(jobs []store.Job, keep func(store.Job) bool) []numberedJob {
	var entries []numberedJob
	for i, j := range jobs {
		if keep(j) {
			entries = append(entries, numberedJob{n: i + 1, job: j})
		}
	}
	return entries
}

// FormatJobStatus resume cuántos archivos están listos, en espera o con error.
func FormatJobStatus(jobs []store.Job, entries []numberedJob) string {
	var ready, notInvoice, waiting, failed int
	for _, j := range jobs {
		switch {
		case j.State == store.JobDone && j.InvoiceID > 0:
			ready++
		case j.State == store.JobDone:
			notInvoice++
		case j.State == store.JobQueued || j.State == store.JobDownloading || j.State == store.JobReading:
			waiting++
		case needsAttention(j):
			failed++
		}
	}
	total := ready + notInvoice + waiting + failed
	if total == 0 {
		return NoFilesMessage
	}
	var parts []string
	add := func(n int, text string) {
		if n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, text))
		}
	}
	add(ready, plural(ready, "listo", "listos")+" para revisar")
	add(notInvoice, plural(notInvoice, "no era una factura", "no eran facturas"))
	add(waiting, "en espera")
	add(failed, "con error")
	text := fmt.Sprintf("📥 Recibí %d %s: %s.", total, plural(total, "archivo", "archivos"), joinWithY(parts))
	if ready > 0 {
		text += " Revisalos en /facturas."
	}
	if len(entries) > 0 {
		text += "\n\n" + attentionLines(entries)
	}
	return text
}

func joinWithY(parts []string) string {
	if len(parts) < 2 {
		return strings.Join(parts, "")
	}
	return strings.Join(parts[:len(parts)-1], ", ") + " y " + parts[len(parts)-1]
}

// attentionLines describe cada archivo con error; los que exceden el límite se cuentan.
func attentionLines(entries []numberedJob) string {
	var lines []string
	for i, e := range entries {
		if i == maxRetryButtons {
			lines = append(lines, fmt.Sprintf("… y %d más con error.", len(entries)-maxRetryButtons))
			break
		}
		lines = append(lines, fmt.Sprintf("Archivo %d: %s", e.n, attentionReason(e.job.State)))
	}
	return strings.Join(lines, "\n")
}

func attentionReason(state store.JobState) string {
	switch state {
	case store.JobFailedDownload:
		return "no pude descargarlo."
	case store.JobFailedRead:
		return "no pude leerlo."
	default:
		return "se cortó mientras lo leía y no sé si se llegó a cobrar."
	}
}

// retryKeyboard ofrece un botón por archivo con error; nil si no hay ninguno.
func retryKeyboard(entries []numberedJob) *models.InlineKeyboardMarkup {
	if len(entries) == 0 {
		return nil
	}
	var rows [][]models.InlineKeyboardButton
	for i, e := range entries {
		if i == maxRetryButtons {
			break
		}
		label := "🔁 Reintentar este archivo"
		if len(entries) > 1 {
			label = fmt.Sprintf("🔁 Reintentar archivo %d", e.n)
		}
		rows = append(rows, []models.InlineKeyboardButton{{
			Text: label, CallbackData: retryJobCallback + ":" + strconv.FormatInt(e.job.ID, 10),
		}})
	}
	return &models.InlineKeyboardMarkup{InlineKeyboard: rows}
}
