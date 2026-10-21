package telegram

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/mmallorquin/bot-marangatu-facturas/internal/invoice"
	"github.com/mmallorquin/bot-marangatu-facturas/internal/reader"
)

// scriptedReader devuelve un resultado distinto por llamada, en orden, y no termina hasta que el test lo suelta.
type scriptedReader struct {
	results []invoice.Invoice
	calls   atomic.Int32
	release chan struct{}
}

func newScriptedReader(results ...invoice.Invoice) *scriptedReader {
	return &scriptedReader{results: results, release: make(chan struct{}, len(results))}
}

func (r *scriptedReader) Read(context.Context, reader.Image) (reader.Result, error) {
	n := r.calls.Add(1) - 1
	<-r.release
	return reader.Result{Invoice: r.results[n]}, nil
}

func doubtfulInvoice(number string) invoice.Invoice {
	inv := sampleInvoice()
	inv.Number = number
	inv.UncertainFields = []string{invoice.FieldIssuerName}
	return inv
}

func cleanInvoice(number string) invoice.Invoice {
	inv := sampleInvoice()
	inv.Number = number
	return inv
}

// sendBatch manda varias fotos de un álbum a la vez y las suelta cuando todas están anotadas.
func (h *harness) sendBatch(t *testing.T, r *scriptedReader, photos int) {
	t.Helper()
	var wg sync.WaitGroup
	for range photos {
		wg.Go(func() { h.sendAlbumPhoto("tanda") })
	}
	waitFor(t, func() bool { return len(h.jobs(t, testChatID)) == photos && r.calls.Load() == readSlots })
	for range photos {
		r.release <- struct{}{}
	}
	wg.Wait()
}

func sentTexts(h *harness) []string {
	var texts []string
	for _, m := range h.telegram.byMethod("sendMessage") {
		texts = append(texts, m.text)
	}
	return texts
}

func TestBatchShowsCleanInvoicesFirstAndLeavesReviewForTheEnd(t *testing.T) {
	// Arrange
	h := newHarness(t)
	if err := h.store.SetAutoSave(context.Background(), testChatID, true); err != nil {
		t.Fatal(err)
	}
	r := newScriptedReader(cleanInvoice("001-001-0000001"), doubtfulInvoice("001-001-0000002"), cleanInvoice("001-001-0000003"))
	h.withHandler(Deps{Reader: r})

	// Act
	h.sendBatch(t, r, 3)

	// Assert
	texts := sentTexts(h)
	if len(texts) != 5 { // "Leyendo…", dos guardadas, el cierre y la que hay que revisar
		t.Fatalf("mensajes = %d:\n%s", len(texts), strings.Join(texts, "\n---\n"))
	}
	for _, text := range texts[1:3] {
		if !strings.HasPrefix(text, "✅ Guardada:") || strings.Contains(text, "0000002") {
			t.Errorf("una factura limpia de la tanda debía confirmarse en una línea: %q", text)
		}
	}
	if !strings.Contains(texts[3], "3 archivos") || !strings.Contains(texts[3], "2 guardadas") || !strings.Contains(texts[3], "1 para revisar") {
		t.Errorf("cierre de la tanda = %q", texts[3])
	}
	last := h.telegram.lastSent(t)
	if !strings.HasPrefix(last.text, "📥 Siguiente pendiente") || !strings.Contains(last.text, "0000002") || !strings.Contains(last.markup, "Guardar") {
		t.Errorf("la dudosa debía mostrarse al final, con sus botones: %+v", last)
	}
}

func TestBatchWithoutDoubtsClosesWithASummaryOnly(t *testing.T) {
	h := newHarness(t)
	if err := h.store.SetAutoSave(context.Background(), testChatID, true); err != nil {
		t.Fatal(err)
	}
	r := newScriptedReader(cleanInvoice("001-001-0000001"), cleanInvoice("001-001-0000002"))
	h.withHandler(Deps{Reader: r})

	h.sendBatch(t, r, 2)

	texts := sentTexts(h)
	if len(texts) != 4 || !strings.Contains(texts[3], "2 guardadas") || strings.Contains(texts[3], "revisar") {
		t.Errorf("mensajes:\n%s", strings.Join(texts, "\n---\n"))
	}
}

func TestBatchWithoutAutoSaveStillShowsCleanCardsRightAway(t *testing.T) {
	h := newHarness(t) // el arnés apaga el guardado automático
	r := newScriptedReader(doubtfulInvoice("001-001-0000001"), cleanInvoice("001-001-0000002"))
	h.withHandler(Deps{Reader: r})

	h.sendBatch(t, r, 2)

	texts := sentTexts(h)
	if len(texts) != 4 { // "Leyendo…", la limpia con sus botones, el cierre y la dudosa
		t.Fatalf("mensajes:\n%s", strings.Join(texts, "\n---\n"))
	}
	if !strings.Contains(texts[1], "0000002") || strings.HasPrefix(texts[1], "✅ Guardada:") {
		t.Errorf("sin guardado automático la limpia se muestra entera para confirmarla: %q", texts[1])
	}
	if !strings.Contains(texts[3], "0000001") {
		t.Errorf("la dudosa debía quedar al final: %q", texts[3])
	}
}

func TestBatchClosesEvenWhenTheLastFileIsNotAnInvoice(t *testing.T) {
	h := newHarness(t)
	if err := h.store.SetAutoSave(context.Background(), testChatID, true); err != nil {
		t.Fatal(err)
	}
	r := newScriptedReader(doubtfulInvoice("001-001-0000001"), invoice.Invoice{})
	h.withHandler(Deps{Reader: r})

	h.sendBatch(t, r, 2)

	last := h.telegram.lastSent(t)
	if !strings.Contains(last.text, "0000001") || !strings.Contains(last.markup, "Guardar") {
		t.Errorf("la dudosa debía mostrarse al cerrar la tanda aunque el otro archivo no fuera factura: %+v", last)
	}
}
