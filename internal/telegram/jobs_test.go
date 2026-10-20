package telegram

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"

	"github.com/mmallorquin/bot-marangatu-facturas/internal/reader"
	"github.com/mmallorquin/bot-marangatu-facturas/internal/store"
)

const otherChatID = int64(43)

// waitFor espera a que una condición se cumpla (las lecturas corren en otras goroutines).
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("la condición no se cumplió a tiempo")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// gatedReader no lee hasta que el test le da permiso, e ignora la cancelación como un proveedor lento.
type gatedReader struct {
	calls   atomic.Int32
	entered chan struct{}
	release chan struct{}
}

func newGatedReader() *gatedReader {
	return &gatedReader{entered: make(chan struct{}, 64), release: make(chan struct{}, 64)}
}

func (r *gatedReader) Read(context.Context, reader.Image) (reader.Result, error) {
	r.calls.Add(1)
	r.entered <- struct{}{}
	<-r.release
	return reader.Result{Invoice: sampleInvoice()}, nil
}

// withHandler reemplaza el handler del harness y lo devuelve para probar la recuperación.
func (h *harness) withHandler(deps Deps) *handler {
	deps.Logger = discardLogger()
	deps.Store = h.store
	hd := newHandler(deps)
	h.handle = hd.handle
	return hd
}

func (h *harness) jobs(t *testing.T, chatID int64) []store.Job {
	t.Helper()
	jobs, err := h.store.Jobs(context.Background(), chatID, 100)
	if err != nil {
		t.Fatal(err)
	}
	return jobs
}

func (h *harness) jobsIn(t *testing.T, chatID int64, state store.JobState) int {
	count := 0
	for _, j := range h.jobs(t, chatID) {
		if j.State == state {
			count++
		}
	}
	return count
}

func (h *harness) enqueue(t *testing.T, chatID int64, messageID int, state store.JobState) store.Job {
	t.Helper()
	unique := "u" + string(rune('a'+messageID))
	job, _, err := h.store.EnqueueJob(context.Background(), store.Job{
		ChatID: chatID, MessageID: messageID, FileUniqueID: unique, FileID: "file-" + unique,
		MimeType: "image/jpeg", Origin: store.JobOriginMessage,
	})
	if err != nil {
		t.Fatal(err)
	}
	path := map[store.JobState][]store.JobState{
		store.JobQueued:         {},
		store.JobDownloading:    {store.JobDownloading},
		store.JobReading:        {store.JobDownloading, store.JobReading},
		store.JobFailedDownload: {store.JobDownloading, store.JobFailedDownload},
	}[state]
	from := store.JobQueued
	for _, next := range path {
		if err := h.store.AdvanceJob(context.Background(), chatID, job.ID, from, next); err != nil {
			t.Fatal(err)
		}
		from = next
	}
	job.State = state
	return job
}

func chatPhoto(chatID int64, messageID int) *models.Update {
	return &models.Update{Message: &models.Message{
		ID: messageID, Chat: models.Chat{ID: chatID, Type: models.ChatTypePrivate},
		Photo: []models.PhotoSize{{FileID: "grande", FileUniqueID: "unico", Width: 1280, Height: 1706}},
	}}
}

func TestSameUpdateDeliveredTwiceIsOneJobAndOneRead(t *testing.T) {
	h := newHarness(t)
	update := photoUpdate()

	h.send(update)
	h.send(update)

	if len(h.reader.received) != 1 {
		t.Fatalf("lecturas = %d, se esperaba 1", len(h.reader.received))
	}
	jobs := h.jobs(t, testChatID)
	if len(jobs) != 1 || jobs[0].State != store.JobDone || jobs[0].InvoiceID != 1 {
		t.Fatalf("trabajos = %+v", jobs)
	}
	if reading := len(h.telegram.byMethod("sendMessage")); reading != 2 { // "Leyendo…" y la factura
		t.Errorf("mensajes = %d: la entrega repetida respondió de nuevo", reading)
	}
}

func TestJobKeepsTelegramReferenceAndOrigin(t *testing.T) {
	h := newHarness(t)
	update := chatPhoto(testChatID, 9)
	update.Message.MediaGroupID = "album-1"

	h.send(update)

	job := h.jobs(t, testChatID)[0]
	if job.FileID != "grande" || job.FileUniqueID != "unico" || job.MessageID != 9 ||
		job.Kind != store.JobKindPhoto || job.Origin != store.JobOriginAlbum {
		t.Errorf("trabajo = %+v", job)
	}
}

func TestRestartProcessesQueuedAndInterruptedDownloads(t *testing.T) {
	h := newHarness(t)
	hd := h.withHandler(Deps{Reader: h.reader})
	h.enqueue(t, testChatID, 1, store.JobQueued)
	h.enqueue(t, testChatID, 2, store.JobDownloading)
	h.enqueue(t, testChatID, 3, store.JobQueued)

	done, err := hd.recoverJobs(context.Background(), h.bot)
	done.Wait()

	if err != nil || len(h.reader.received) != 3 {
		t.Fatalf("lecturas = %d, %v", len(h.reader.received), err)
	}
	for _, job := range h.jobs(t, testChatID) {
		if job.State != store.JobDone || job.InvoiceID == 0 {
			t.Errorf("trabajo sin terminar: %+v", job)
		}
	}
	if drafts, _ := h.store.Drafts(context.Background(), testChatID); len(drafts) != 3 {
		t.Errorf("borradores = %d", len(drafts))
	}
}

func TestRestartMarksReadingJobUncertainAndAsksBeforeRetrying(t *testing.T) {
	h := newHarness(t)
	hd := h.withHandler(Deps{Reader: h.reader})
	job := h.enqueue(t, testChatID, 1, store.JobReading)

	done, err := hd.recoverJobs(context.Background(), h.bot)
	done.Wait()

	if err != nil || len(h.reader.received) != 0 {
		t.Fatalf("no debía leer: lecturas = %d, %v", len(h.reader.received), err)
	}
	if got := h.jobs(t, testChatID)[0].State; got != store.JobUncertain {
		t.Fatalf("estado = %s", got)
	}
	sent := h.telegram.byMethod("sendMessage")
	if len(sent) != 1 || !strings.Contains(sent[0].text, "Archivo 1") || !strings.Contains(sent[0].markup, "Reintentar este archivo") {
		t.Fatalf("aviso = %+v", sent)
	}
	// Reiniciar de nuevo no vuelve a avisar ni a leer.
	done, _ = hd.recoverJobs(context.Background(), h.bot)
	done.Wait()
	if len(h.telegram.byMethod("sendMessage")) != 1 || len(h.reader.received) != 0 {
		t.Error("el segundo arranque avisó o leyó otra vez")
	}

	h.pressRaw(retryJobCallback + ":" + itoa(job.ID))

	if len(h.reader.received) != 1 || h.jobs(t, testChatID)[0].State != store.JobDone {
		t.Errorf("el reintento no leyó el archivo: lecturas = %d", len(h.reader.received))
	}
	h.pressRaw(retryJobCallback + ":" + itoa(job.ID))
	if len(h.reader.received) != 1 {
		t.Error("un segundo toque leyó de nuevo")
	}
}

func TestDownloadFailureIsRecordedAndCanBeRetried(t *testing.T) {
	h := newHarness(t)
	h.telegram.failGetFile = true

	h.sendPhoto()

	job := h.jobs(t, testChatID)[0]
	if job.State != store.JobFailedDownload || len(h.reader.received) != 0 {
		t.Fatalf("trabajo = %+v, lecturas = %d", job, len(h.reader.received))
	}
	last := h.telegram.lastSent(t)
	if !strings.Contains(last.text, "descargar") || !strings.Contains(last.markup, retryJobCallback+":") {
		t.Errorf("aviso = %+v", last)
	}

	h.telegram.failGetFile = false
	h.pressRaw(retryJobCallback + ":" + itoa(job.ID))

	if h.jobs(t, testChatID)[0].State != store.JobDone || len(h.reader.received) != 1 {
		t.Errorf("el reintento no terminó: %+v", h.jobs(t, testChatID))
	}
}

func TestQueueWaitDoesNotConsumeTheReadTimeout(t *testing.T) {
	h := newHarness(t)
	rdr := newGatedReader()
	h.withHandler(Deps{Reader: rdr, ReadTimeout: 150 * time.Millisecond})
	var sending sync.WaitGroup
	for range 3 {
		sending.Add(1)
		go func() { defer sending.Done(); h.sendPhoto() }()
	}
	<-rdr.entered
	<-rdr.entered
	waitFor(t, func() bool { return h.jobsIn(t, testChatID, store.JobQueued) == 1 })

	time.Sleep(400 * time.Millisecond) // el tercero espera más que el plazo de lectura
	close(rdr.release)
	sending.Wait()

	if rdr.calls.Load() != 3 {
		t.Fatalf("lecturas = %d, el que esperó turno no se leyó", rdr.calls.Load())
	}
	for _, job := range h.jobs(t, testChatID) {
		if job.State != store.JobDone {
			t.Errorf("trabajo %d en %s", job.ID, job.State)
		}
	}
	for _, m := range h.telegram.byMethod("sendMessage") {
		if m.text == ReadErrorMessage || strings.Contains(m.text, "descargar") {
			t.Errorf("la espera de turno se informó como error: %q", m.text)
		}
	}
}

func TestSecondChatGetsAReaderWhileFirstChatHasManyQueued(t *testing.T) {
	h := newHarness(t)
	rdr := newGatedReader()
	hd := h.withHandler(Deps{Reader: rdr})
	var sending sync.WaitGroup
	for range 10 {
		sending.Add(1)
		go func() { defer sending.Done(); h.sendPhoto() }()
	}
	<-rdr.entered
	<-rdr.entered
	waitFor(t, func() bool { return hd.deps.Operations.gate.queued() == 8 })
	sending.Add(1)
	go func() { defer sending.Done(); h.send(chatPhoto(otherChatID, 1)) }()
	waitFor(t, func() bool { return hd.deps.Operations.gate.queued() == 9 })

	rdr.release <- struct{}{} // termina una lectura del chat A

	waitFor(t, func() bool { return h.jobsIn(t, otherChatID, store.JobReading) == 1 })
	if queued := h.jobsIn(t, testChatID, store.JobQueued); queued != 8 {
		t.Errorf("el chat A siguió con su cola (%d en cola) mientras B esperaba", queued)
	}
	close(rdr.release)
	sending.Wait()
	if h.jobsIn(t, testChatID, store.JobDone) != 10 || h.jobsIn(t, otherChatID, store.JobDone) != 1 {
		t.Error("no terminaron todos los archivos")
	}
}

func TestDeletingDataCancelsWaitingJobsAndInFlightReads(t *testing.T) {
	h := newHarness(t)
	rdr := newGatedReader()
	h.withHandler(Deps{Reader: rdr})
	var sending sync.WaitGroup
	for range 4 {
		sending.Add(1)
		go func() { defer sending.Done(); h.sendPhoto() }()
	}
	<-rdr.entered
	<-rdr.entered
	waitFor(t, func() bool { return h.jobsIn(t, testChatID, store.JobQueued) == 2 })

	h.sendText("/borrar_mis_datos")
	h.pressRaw(deleteButtonData(t, h.telegram.lastSent(t), "z:s"))
	messages := len(h.telegram.byMethod("sendMessage"))
	close(rdr.release)
	sending.Wait()

	if jobs := h.jobs(t, testChatID); len(jobs) != 0 {
		t.Errorf("quedaron trabajos tras borrar: %+v", jobs)
	}
	if drafts, _ := h.store.Drafts(context.Background(), testChatID); len(drafts) != 0 {
		t.Errorf("una lectura en curso escribió %d borradores tras el borrado", len(drafts))
	}
	if rdr.calls.Load() != 2 {
		t.Errorf("lecturas = %d: los archivos en espera no debían leerse", rdr.calls.Load())
	}
	if len(h.telegram.byMethod("sendMessage")) != messages {
		t.Error("una lectura obsoleta respondió tras el borrado")
	}
}

func TestStatusSummarizesFilesAndOffersRetryForFailures(t *testing.T) {
	h := newHarness(t)
	h.sendPhoto() // listo
	failed := h.enqueue(t, testChatID, 90, store.JobFailedDownload)
	h.enqueue(t, testChatID, 91, store.JobQueued)
	uncertain := h.enqueue(t, testChatID, 92, store.JobReading)
	_ = h.store.AdvanceJob(context.Background(), testChatID, uncertain.ID, store.JobReading, store.JobUncertain)

	h.sendText(statusCommand)

	last := h.telegram.lastSent(t)
	for _, want := range []string{"Recibí 4 archivos", "1 listo para revisar", "1 en espera", "2 con error",
		"Archivo 2: no pude descargarlo", "Archivo 4:"} {
		if !strings.Contains(last.text, want) {
			t.Errorf("falta %q en %q", want, last.text)
		}
	}
	for _, id := range []int64{failed.ID, uncertain.ID} {
		if !strings.Contains(last.markup, retryJobCallback+":"+itoa(id)) {
			t.Errorf("falta el botón de reintento del archivo %d en %s", id, last.markup)
		}
	}
}

func TestStatusWithoutFiles(t *testing.T) {
	h := newHarness(t)

	h.sendText(statusCommand)

	if last := h.telegram.lastSent(t); !strings.Contains(last.text, "Todavía no recibí archivos") || last.markup != "" {
		t.Errorf("respuesta = %+v", last)
	}
}

func TestRetryIgnoresFilesOfAnotherChat(t *testing.T) {
	h := newHarness(t)
	foreign := h.enqueue(t, otherChatID, 1, store.JobFailedDownload)

	h.pressRaw(retryJobCallback + ":" + itoa(foreign.ID))

	if h.jobs(t, otherChatID)[0].State != store.JobFailedDownload || len(h.reader.received) != 0 {
		t.Error("un chat reintentó el archivo de otro")
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
