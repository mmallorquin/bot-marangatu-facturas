package telegram

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/mmallorquin/bot-marangatu-facturas/internal/reader"
	"github.com/mmallorquin/bot-marangatu-facturas/internal/store"
)

// Cada archivo recibido es un trabajo anotado en SQLite antes de descargarlo:
// en_cola → descargando → leyendo → lista, o un final con error. Al reiniciar, lo que no
// empezó a leerse sigue solo; lo que estaba leyéndose queda incierto y lo decide el usuario.
const (
	retryJobCallback = "jr"

	DownloadErrorMessage = "😕 No pude descargar el archivo desde Telegram. Probá de nuevo o mandalo otra vez."
	ReadTimeoutMessage   = "⏳ La lectura tardó demasiado y no sé si se llegó a cobrar. " +
		"Mirá /pendientes y, si la factura no está, tocá Reintentar."
	RetryingAnswer = "⏳ Reintentando…"
	NoRetryAlert   = "Ese archivo ya no se puede reintentar."

	// notifyTimeout es el plazo para avisar a un chat tras el arranque.
	notifyTimeout = 10 * time.Second
)

func jobOf(msg *models.Message, file imageFile) store.Job {
	origin := store.JobOriginMessage
	if msg.MediaGroupID != "" {
		origin = store.JobOriginAlbum
	}
	unique := file.fileUniqueID
	if unique == "" {
		unique = file.fileID
	}
	return store.Job{
		ChatID: msg.Chat.ID, MessageID: msg.ID, FileUniqueID: unique, FileID: file.fileID,
		MimeType: file.mimeType, Origin: origin,
	}
}

// sourceOf es la referencia a la foto que se guarda con el borrador.
func sourceOf(job store.Job, rotation int) store.Source {
	return store.Source{FileID: job.FileID, MimeType: job.MimeType, MessageID: job.MessageID, Rotation: rotation}
}

func (h *handler) readTimeout() time.Duration {
	if h.deps.ReadTimeout > 0 {
		return h.deps.ReadTimeout
	}
	return processTimeout
}

// processJob espera un lector libre, descarga el archivo, lo lee con IA y muestra el resultado.
// El plazo de lectura empieza recién con el lector asignado: esperar turno no cuenta.
// Si ctx se cancela (el usuario borró sus datos o el bot se apaga) el trabajo se abandona sin avisar:
// en el primer caso ya no existe; en el segundo se retoma al arrancar.
func (h *handler) processJob(ctx context.Context, b *bot.Bot, job store.Job) {
	defer h.closeBatch(ctx, b, job.ChatID) // corre último, después de responder por este archivo
	release, err := h.deps.Operations.gate.acquire(ctx, job.ChatID)
	if err != nil {
		return
	}
	defer release()
	state := store.JobQueued
	defer h.recoverJobPanic(ctx, b, job, &state)
	if !h.advanceOrFail(ctx, b, job, state, store.JobDownloading) {
		return
	}
	state = store.JobDownloading
	readCtx, cancel := context.WithTimeout(ctx, h.readTimeout())
	defer cancel()

	data, err := downloadFile(readCtx, b, job.FileID)
	if err != nil {
		if ctx.Err() != nil {
			return // apagado o borrado: no es un error del archivo
		}
		h.finishRead(ctx, func(ctx context.Context) {
			h.logger.Error("no se pudo descargar el archivo", "chat_id", job.ChatID, "error", Redact(err, h.deps.Token))
			h.reportDownloadFailure(ctx, b, job, state)
		})
		return
	}
	if !h.advanceOrFail(ctx, b, job, state, store.JobReading) {
		return
	}
	state = store.JobReading

	start := time.Now()
	result, err := h.deps.Reader.Read(readCtx, reader.Image{Data: data, MimeType: job.MimeType})
	if err != nil {
		if ctx.Err() != nil {
			return // la lectura queda en leyendo y, al arrancar, como incierta
		}
		h.finishRead(ctx, func(ctx context.Context) {
			h.reportReadFailure(ctx, b, job, err, time.Since(start).Seconds(), errors.Is(readCtx.Err(), context.DeadlineExceeded))
		})
		return
	}
	h.finishRead(ctx, func(ctx context.Context) {
		h.showReadInvoice(ctx, b, job, result, time.Since(start).Seconds())
	})
}

// reportDownloadFailure cierra el trabajo como error de descarga y ofrece reintentarlo.
func (h *handler) reportDownloadFailure(ctx context.Context, b *bot.Bot, job store.Job, from store.JobState) {
	h.failJobQuietly(ctx, job, from, store.JobFailedDownload)
	h.track(ctx, job.ChatID, store.Event{Kind: store.EventReadError, Detail: store.EventDetailDownload})
	h.sendReply(ctx, b, job.ChatID, job.MessageID, DownloadErrorMessage, retryKeyboard([]numberedJob{{job: job}}))
}

// reportReadFailure cierra el trabajo tras un fallo del lector. Si venció el plazo, el proveedor
// pudo haber cobrado: queda incierto y se pide decidir en vez de dar por fallida la lectura.
func (h *handler) reportReadFailure(ctx context.Context, b *bot.Bot, job store.Job, err error, seconds float64, timedOut bool) {
	h.logger.Error("no se pudo leer la factura", "chat_id", job.ChatID, "error", err)
	h.track(ctx, job.ChatID, store.Event{Kind: store.EventReadError, Detail: store.EventDetailAI, Seconds: seconds})
	if timedOut {
		h.failJobQuietly(ctx, job, store.JobReading, store.JobUncertain)
		h.sendReply(ctx, b, job.ChatID, job.MessageID, ReadTimeoutMessage, retryKeyboard([]numberedJob{{job: job}}))
		return
	}
	h.failJobQuietly(ctx, job, store.JobReading, store.JobFailedRead)
	h.sendReply(ctx, b, job.ChatID, job.MessageID, readFailureMessage(err), nil)
}

// advanceOrFail mueve el trabajo de estado. Si la base falla con el trabajo vivo, lo cierra como
// error de descarga con botón de reintento para que no quede trabado y mudo.
func (h *handler) advanceOrFail(ctx context.Context, b *bot.Bot, job store.Job, from, to store.JobState) bool {
	err := h.deps.Store.AdvanceJob(ctx, job.ChatID, job.ID, from, to)
	if err == nil {
		return true
	}
	if errors.Is(err, store.ErrJobGone) || ctx.Err() != nil {
		return false
	}
	h.logger.Error("no se pudo actualizar el archivo", "chat_id", job.ChatID, "job", job.ID, "error", err)
	h.finishRead(ctx, func(ctx context.Context) { h.reportDownloadFailure(ctx, b, job, from) })
	return false
}

// recoverJobPanic evita que un lector que falla con pánico deje el trabajo en leyendo sin respuesta.
func (h *handler) recoverJobPanic(ctx context.Context, b *bot.Bot, job store.Job, state *store.JobState) {
	r := recover()
	if r == nil {
		return
	}
	h.logger.Error("pánico al procesar el archivo", "chat_id", job.ChatID, "job", job.ID, "panic", r)
	if ctx.Err() != nil {
		return
	}
	h.finishRead(ctx, func(ctx context.Context) {
		h.failJobQuietly(ctx, job, *state, store.JobFailedRead)
		h.sendReply(ctx, b, job.ChatID, job.MessageID, ReadErrorMessage, retryKeyboard([]numberedJob{{job: job}}))
	})
}

// failJobQuietly cierra el trabajo con error; si falla, solo queda en el log.
func (h *handler) failJobQuietly(ctx context.Context, job store.Job, from, to store.JobState) {
	if err := h.deps.Store.AdvanceJob(ctx, job.ChatID, job.ID, from, to); err != nil && !errors.Is(err, store.ErrJobGone) {
		h.logger.Error("no se pudo cerrar el archivo con error", "chat_id", job.ChatID, "job", job.ID, "error", err)
	}
}

// resumeJob procesa un trabajo que no llegó por un mensaje nuevo (arranque o reintento ya anotado).
func (h *handler) resumeJob(ctx context.Context, b *bot.Bot, job store.Job) {
	c := h.deps.Operations.chat(job.ChatID)
	c.mu.Lock()
	ctx, done := beginRead(ctx, c)
	c.mu.Unlock()
	defer done()
	h.processJob(ctx, b, job)
}

// handleRetryJobCallback vuelve a poner en cola un archivo con error o incierto y lo procesa.
func (h *handler) handleRetryJobCallback(ctx context.Context, b *bot.Bot, query *models.CallbackQuery, msg *models.Message) {
	id, err := strconv.ParseInt(strings.TrimPrefix(query.Data, retryJobCallback+":"), 10, 64)
	if err != nil {
		h.answer(ctx, b, query.ID, NoRetryAlert, true)
		return
	}
	c := h.deps.Operations.chat(msg.Chat.ID)
	c.mu.Lock()
	job, err := h.deps.Store.RetryJob(ctx, msg.Chat.ID, id)
	switch {
	case errors.Is(err, store.ErrJobGone):
		c.mu.Unlock()
		h.answer(ctx, b, query.ID, NoRetryAlert, true)
		return
	case err != nil:
		c.mu.Unlock()
		h.logger.Error("no se pudo reintentar el archivo", "chat_id", msg.Chat.ID, "error", err)
		h.answer(ctx, b, query.ID, StoreErrorMessage, true)
		return
	}
	ctx, done := beginRead(ctx, c)
	c.mu.Unlock()
	defer done()
	h.answer(ctx, b, query.ID, RetryingAnswer, false) // fuera del bloqueo: es una llamada a Telegram
	h.processJob(ctx, b, job)
}

// RecoverJobs retoma al arrancar los archivos que quedaron a medias. Debe llamarse una vez,
// antes de recibir mensajes nuevos.
func RecoverJobs(ctx context.Context, b *bot.Bot, deps Deps) error {
	_, err := newHandler(deps).recoverJobs(ctx, b)
	return err
}

// recoverJobs devuelve el grupo que espera a los trabajos retomados.
func (h *handler) recoverJobs(ctx context.Context, b *bot.Bot) (*sync.WaitGroup, error) {
	var running sync.WaitGroup
	if err := h.deps.Store.PruneJobs(ctx, store.JobRetention); err != nil {
		h.logger.Warn("no se pudieron limpiar los archivos viejos", "error", err)
	}
	requeued, uncertain, err := h.deps.Store.RecoverJobs(ctx)
	if err != nil {
		return &running, fmt.Errorf("recuperando los archivos pendientes: %w", err)
	}
	for _, job := range requeued {
		running.Go(func() { h.resumeJob(ctx, b, job) })
	}
	running.Go(func() { h.notifyUncertain(ctx, b, uncertain) }) // sin demorar el arranque
	h.logger.Info("archivos recuperados", "reencolados", len(requeued), "inciertos", len(uncertain))
	return &running, nil
}

// notifyUncertain avisa una sola vez a cada chat con lecturas cortadas por el reinicio.
func (h *handler) notifyUncertain(ctx context.Context, b *bot.Bot, uncertain []store.Job) {
	byChat := map[int64][]store.Job{}
	var chats []int64
	for _, job := range uncertain {
		if _, seen := byChat[job.ChatID]; !seen {
			chats = append(chats, job.ChatID)
		}
		byChat[job.ChatID] = append(byChat[job.ChatID], job)
	}
	for _, chatID := range chats {
		h.notifyChatUncertain(ctx, b, chatID, byChat[chatID])
	}
}

func (h *handler) notifyChatUncertain(ctx context.Context, b *bot.Bot, chatID int64, uncertain []store.Job) {
	recent, err := h.deps.Store.Jobs(ctx, chatID, statusJobsLimit)
	if err != nil {
		h.logger.Error("no se pudieron leer los archivos del chat", "chat_id", chatID, "error", err)
		return
	}
	entries := attentionEntries(recent, func(j store.Job) bool {
		return j.State == store.JobUncertain && containsJob(uncertain, j.ID)
	})
	ctx, cancel := context.WithTimeout(ctx, notifyTimeout)
	defer cancel()
	text := fmt.Sprintf("⚠️ Se reinició el bot mientras leía %d %s. No sé si la lectura llegó a completarse "+
		"(puede que ya se haya cobrado), así que no reintento solo. Mirá /pendientes y, si falta alguno, tocá Reintentar.\n\n%s",
		len(uncertain), plural(len(uncertain), "archivo", "archivos"), attentionLines(entries))
	h.sendReply(ctx, b, chatID, 0, strings.TrimSpace(text), retryKeyboard(entries))
}

func containsJob(jobs []store.Job, id int64) bool {
	for _, j := range jobs {
		if j.ID == id {
			return true
		}
	}
	return false
}
