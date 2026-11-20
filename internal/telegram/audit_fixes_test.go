package telegram

import (
	"context"
	"fmt"
	"github.com/mmallorquin/bot-marangatu-facturas/internal/invoice"
	"strings"
	"testing"

	"github.com/mmallorquin/bot-marangatu-facturas/internal/reader"
	"github.com/mmallorquin/bot-marangatu-facturas/internal/store"
)

func TestDailyFileLimitRejectsBeforeAnyPaidRead(t *testing.T) {
	// Arrange: cupo de 2 archivos por día para este chat.
	h := newHarness(t)
	h.withHandler(Deps{Reader: h.reader, DailyFileLimit: 2})

	// Act
	for range 3 {
		h.sendPhoto()
	}

	// Assert: el tercero no se lee ni se anota, y el usuario sabe por qué.
	if len(h.reader.received) != 2 || len(h.jobs(t, testChatID)) != 2 {
		t.Errorf("lecturas = %d, archivos = %d: el tercero no debía leerse", len(h.reader.received), len(h.jobs(t, testChatID)))
	}
	if last := h.telegram.lastSent(t); !strings.Contains(last.text, "2 archivos") || !strings.Contains(last.text, "mañana") {
		t.Errorf("aviso de cupo = %q", last.text)
	}
}

func TestDailyFileLimitIsPerChat(t *testing.T) {
	h := newHarness(t)
	h.withHandler(Deps{Reader: h.reader, DailyFileLimit: 1})
	h.sendPhoto()
	h.sendPhoto()

	h.send(chatPhoto(otherChatID, 500))

	if len(h.reader.received) != 2 {
		t.Errorf("lecturas = %d: el cupo de un chat no frena a otro", len(h.reader.received))
	}
}

// slowProviderReader falla como el cliente de OpenRouter cuando vence su propio plazo HTTP.
type slowProviderReader struct{}

func (slowProviderReader) Read(context.Context, reader.Image) (reader.Result, error) {
	return reader.Result{}, fmt.Errorf("OpenRouter: %w", context.DeadlineExceeded)
}

func TestProviderTimeoutLeavesTheFileUncertainWithRetry(t *testing.T) {
	// El plazo del cliente HTTP (90 s) vence antes que el del trabajo (120 s): el pedido salió y pudo cobrarse.
	h := newHarness(t)
	h.withHandler(Deps{Reader: slowProviderReader{}})

	h.sendPhoto()

	if got := h.jobs(t, testChatID)[0].State; got != store.JobUncertain {
		t.Errorf("estado = %s: un plazo vencido en el proveedor puede haberse cobrado", got)
	}
	if last := h.telegram.lastSent(t); last.text != ReadTimeoutMessage || !strings.Contains(last.markup, retryJobCallback+":") {
		t.Errorf("respuesta = %+v", last)
	}
}

func TestFailedReadOffersRetryInsteadOfResending(t *testing.T) {
	h := newHarness(t)
	h.reader.err = fmt.Errorf("proveedor caído")

	h.sendPhoto()

	if last := h.telegram.lastSent(t); !strings.Contains(last.markup, retryJobCallback+":") {
		t.Errorf("un error de lectura debía traer el botón Reintentar: %+v", last)
	}
}

func TestTappingRereadWhileOneIsRunningDoesNotPayAgain(t *testing.T) {
	// Arrange: una lectura lenta en curso.
	h := newHarness(t)
	r := newGatedReader()
	h.withHandler(Deps{Reader: r})
	r.release <- struct{}{}
	h.sendPhoto()
	<-r.entered
	data, ok := h.emittedCallback(callback{action: actionReread, id: 1})
	if !ok {
		t.Fatal("la tarjeta no trae el botón Releer")
	}

	// Act: primer toque (queda releyendo) y cuatro toques más.
	done := make(chan struct{})
	go func() { h.pressRaw(data); close(done) }()
	<-r.entered
	for range 4 {
		h.pressRaw(data)
	}
	answers := h.telegram.byMethod("answerCallbackQuery")
	r.release <- struct{}{}
	<-done

	// Assert
	if calls := r.calls.Load(); calls != 2 {
		t.Errorf("llamadas pagas = %d: la lectura inicial y una sola relectura", calls)
	}
	if last := answers[len(answers)-1]; !strings.Contains(last.text, "Ya la estoy releyendo") {
		t.Errorf("respuesta al toque repetido = %q", last.text)
	}
}

func TestReaderSlotIsFreedBeforeWaitingForTheChat(t *testing.T) {
	// Arrange: una lectura en curso y, mientras tanto, el chat queda ocupado
	// (por ejemplo, subiendo un ZIP a Telegram).
	h := newHarness(t)
	r := newGatedReader()
	hd := h.withHandler(Deps{Reader: r})
	chat := hd.deps.Operations.chat(testChatID)
	done := make(chan struct{})
	go func() { h.sendPhoto(); close(done) }()
	<-r.entered
	chat.mu.Lock()

	// Act: la lectura termina, pero su respuesta tiene que esperar al chat.
	r.release <- struct{}{}

	// Assert: el lector queda libre para otros chats aunque la respuesta siga esperando.
	gate := hd.deps.Operations.gate
	waitFor(t, func() bool {
		gate.mu.Lock()
		defer gate.mu.Unlock()
		return gate.free == readSlots
	})
	chat.mu.Unlock()
	<-done
}

func TestSummaryExplainsWhatDoesNotGoIntoTheZIP(t *testing.T) {
	text := FormatMonthSummary("2026-09", store.Summary{Count: 2, Taxed10: 220_000, VAT10: 20_000, Total: 220_000, Excluded: 1, ExcludedTotal: 110_000})

	for _, want := range []string{"1 no entra al ZIP", "110.000 Gs. Entran al ZIP: 110.000 Gs"} {
		if !strings.Contains(text, want) {
			t.Errorf("falta %q en:\n%s", want, text)
		}
	}
	if clean := FormatMonthSummary("2026-09", store.Summary{Count: 1, Total: 110_000}); strings.Contains(clean, "ZIP") {
		t.Errorf("sin exclusiones no hay aclaración:\n%s", clean)
	}
}

func TestUnknownTypeOffersToCorrectIt(t *testing.T) {
	// "otro" queda fuera del archivo para siempre si no se puede corregir.
	h := newHarness(t)
	h.reader.result.Invoice.Type = invoice.TypeOther
	h.sendPhoto()
	card := h.telegram.lastSent(t)

	h.press(callback{action: actionField, id: 1, field: invoice.FieldType})
	h.sendText("factura")

	if !strings.Contains(card.markup, "Corregir tipo") {
		t.Errorf("la tarjeta debía ofrecer corregir el tipo: %s", card.markup)
	}
	if rec, _ := h.store.Get(context.Background(), testChatID, 1); rec.Invoice.Type != invoice.TypeFactura {
		t.Errorf("tipo = %q", rec.Invoice.Type)
	}
}

func TestCurrencyWrittenAsGsIsAccepted(t *testing.T) {
	h := newHarness(t)
	h.autoSaveOn(t)
	h.reader.result.Invoice.Currency = "Gs"

	h.sendPhoto()

	if h.status(t, 1) != store.StatusSaved {
		t.Errorf("\"Gs\" es guaraníes: la factura debía cerrar y guardarse")
	}
}
