package telegram

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-telegram/bot"

	"github.com/mmallorquin/bot-marangatu-facturas/internal/reader"
	"github.com/mmallorquin/bot-marangatu-facturas/internal/store"
)

// flakyStore falla como una base ocupada en las transiciones que se le piden.
type flakyStore struct {
	*store.Store
	failFrom    store.JobState
	recoverFail error
}

var errBusy = errors.New("database is locked")

func (f flakyStore) AdvanceJob(ctx context.Context, chatID, id int64, from, to store.JobState) error {
	if from == f.failFrom && to != store.JobFailedDownload && to != store.JobFailedRead {
		return errBusy
	}
	return f.Store.AdvanceJob(ctx, chatID, id, from, to)
}

func (f flakyStore) RecoverJobs(ctx context.Context) ([]store.Job, []store.Job, error) {
	if f.recoverFail != nil {
		return nil, nil, f.recoverFail
	}
	return f.Store.RecoverJobs(ctx)
}

// stallingTelegram simula una API lenta: getFile y/o sendMessage no responden hasta liberarse.
type stallingTelegram struct {
	stallGetFile, stallSend bool
	reached                 chan string // método que quedó esperando
	unblock                 chan struct{}
	once                    sync.Once
	sends                   atomic.Int32
}

func newStallingTelegram() *stallingTelegram {
	return &stallingTelegram{reached: make(chan string, 8), unblock: make(chan struct{})}
}

func (s *stallingTelegram) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	switch {
	case method == "getFile" && s.stallGetFile:
		s.reached <- method
		select {
		case <-r.Context().Done():
		case <-s.unblock:
		}
		return
	case method == "sendMessage":
		s.sends.Add(1)
		if s.stallSend {
			s.reached <- method
			select {
			case <-r.Context().Done():
				return
			case <-s.unblock:
			}
		}
	case method == "getFile":
		_, _ = io.WriteString(w, `{"ok":true,"result":{"file_id":"x","file_path":"photos/factura.jpg"}}`)
		return
	case strings.HasPrefix(r.URL.Path, "/file/bot"):
		_, _ = io.WriteString(w, fakeImageData)
		return
	}
	_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":10,"chat":{"id":42}}}`)
}

// release deja responder a los pedidos que esperan.
func (s *stallingTelegram) release() { s.once.Do(func() { close(s.unblock) }) }

func (s *stallingTelegram) bot(t *testing.T) *bot.Bot {
	t.Helper()
	server := httptest.NewServer(s)
	t.Cleanup(server.Close)
	t.Cleanup(s.release) // corre antes que server.Close
	b, err := bot.New(testToken, bot.WithServerURL(server.URL), bot.WithSkipGetMe())
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// signalReader avisa que empezó a leer y espera la cancelación, como una llamada en vuelo.
type signalReader struct{ entered chan struct{} }

func (r signalReader) Read(ctx context.Context, _ reader.Image) (reader.Result, error) {
	r.entered <- struct{}{}
	<-ctx.Done()
	return reader.Result{}, ctx.Err()
}

type panicReader struct{}

func (panicReader) Read(context.Context, reader.Image) (reader.Result, error) { panic("proveedor roto") }

func TestShutdownDuringDownloadLeavesJobUntouchedAndSilent(t *testing.T) {
	h := newHarness(t)
	api := newStallingTelegram()
	api.stallGetFile = true
	hd := h.withHandler(Deps{Reader: h.reader})
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	go func() { hd.handle(ctx, api.bot(t), photoUpdate()); close(finished) }()
	<-api.reached
	sentBefore := api.sends.Load()

	cancel() // SIGTERM
	<-finished

	if got := h.jobs(t, testChatID)[0].State; got != store.JobDownloading {
		t.Errorf("estado = %s, debía quedar descargando para retomarse", got)
	}
	if api.sends.Load() != sentBefore {
		t.Error("se avisó al usuario de un error que fue un apagado")
	}
}

func TestShutdownDuringReadLeavesJobReadingAndSilent(t *testing.T) {
	h := newHarness(t)
	rdr := signalReader{entered: make(chan struct{}, 1)}
	hd := h.withHandler(Deps{Reader: rdr})
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	go func() { hd.handle(ctx, h.bot, photoUpdate()); close(finished) }()
	<-rdr.entered
	messages := len(h.telegram.byMethod("sendMessage"))

	cancel()
	<-finished

	if got := h.jobs(t, testChatID)[0].State; got != store.JobReading {
		t.Errorf("estado = %s, debía quedar leyendo para volverse incierto", got)
	}
	if len(h.telegram.byMethod("sendMessage")) != messages {
		t.Error("se respondió un error que fue un apagado")
	}
	if m := h.metrics(t); m.ReadErrors != 0 {
		t.Errorf("el apagado se registró como error de lectura: %d", m.ReadErrors)
	}
}

func TestFailedTransitionDoesNotLeaveJobStuckAndSilent(t *testing.T) {
	for _, from := range []store.JobState{store.JobQueued, store.JobDownloading} {
		t.Run(string(from), func(t *testing.T) {
			h := newHarness(t)
			h.withHandler(Deps{Reader: h.reader, Store: flakyStore{Store: h.store, failFrom: from}})

			h.sendPhoto()

			if got := h.jobs(t, testChatID)[0].State; got != store.JobFailedDownload {
				t.Fatalf("estado = %s", got)
			}
			last := h.telegram.lastSent(t)
			if last.text != DownloadErrorMessage || !strings.Contains(last.markup, retryJobCallback+":") {
				t.Errorf("respuesta = %+v", last)
			}
			if len(h.reader.received) != 0 {
				t.Error("leyó pese a no poder registrar el avance")
			}
		})
	}
}

func TestPanickingReaderMarksJobFailedAndReplies(t *testing.T) {
	h := newHarness(t)
	h.withHandler(Deps{Reader: panicReader{}})

	h.sendPhoto()

	if got := h.jobs(t, testChatID)[0].State; got != store.JobFailedRead {
		t.Fatalf("estado = %s", got)
	}
	last := h.telegram.lastSent(t)
	if last.text != ReadErrorMessage || !strings.Contains(last.markup, retryJobCallback+":") {
		t.Errorf("respuesta = %+v", last)
	}
	// El bot sigue atendiendo: el trabajo puede reintentarse.
	h.withHandler(Deps{Reader: h.reader})
	h.pressRaw(retryJobCallback + ":" + itoa(h.jobs(t, testChatID)[0].ID))
	if h.jobs(t, testChatID)[0].State != store.JobDone {
		t.Error("el reintento tras el pánico no terminó")
	}
}

func TestRecoveryDoesNotWaitForUncertainNotifications(t *testing.T) {
	h := newHarness(t)
	api := newStallingTelegram()
	api.stallSend = true
	hd := h.withHandler(Deps{Reader: h.reader})
	h.enqueue(t, testChatID, 1, store.JobReading)
	returned := make(chan *sync.WaitGroup, 1)

	go func() { wg, _ := hd.recoverJobs(context.Background(), api.bot(t)); returned <- wg }()

	var notifications *sync.WaitGroup
	select {
	case notifications = <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("la recuperación quedó esperando el aviso a Telegram")
	}
	<-api.reached
	api.release()
	notifications.Wait()
	if api.sends.Load() != 1 {
		t.Errorf("avisos = %d", api.sends.Load())
	}
}

func TestRecoverJobsPropagatesDatabaseFailure(t *testing.T) {
	h := newHarness(t)
	failing := flakyStore{Store: h.store, recoverFail: errBusy}

	err := RecoverJobs(context.Background(), h.bot, Deps{Logger: discardLogger(), Store: failing, Reader: h.reader})

	if !errors.Is(err, errBusy) {
		t.Fatalf("error = %v", err)
	}
}
