package telegram

import (
	"context"
	"encoding/binary"
	"hash/crc32"
	"strings"
	"sync"
	"testing"

	"github.com/go-telegram/bot/models"

	"github.com/mmallorquin/bot-marangatu-facturas/internal/invoice"
	"github.com/mmallorquin/bot-marangatu-facturas/internal/reader"
	"github.com/mmallorquin/bot-marangatu-facturas/internal/store"
)

const wrongRUC = "80000519-1" // el dígito verificador correcto es 8

// sequenceReader devuelve un resultado por llamada, en orden; el último se repite.
type sequenceReader struct {
	mu       sync.Mutex
	results  []invoice.Invoice
	received []reader.Image
}

func (r *sequenceReader) Read(_ context.Context, img reader.Image) (reader.Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := min(len(r.received), len(r.results)-1)
	r.received = append(r.received, img)
	return reader.Result{Invoice: r.results[n], CostUSD: 0.001}, nil
}

func withRUC(ruc string) invoice.Invoice {
	inv := sampleInvoice()
	inv.IssuerRUC = ruc
	return inv
}

func (h *harness) autoSaveOn(t *testing.T) {
	t.Helper()
	if err := h.store.SetAutoSave(context.Background(), testChatID, true); err != nil {
		t.Fatal(err)
	}
}

func (h *harness) status(t *testing.T, id int64) store.Status {
	t.Helper()
	rec, err := h.store.Get(context.Background(), testChatID, id)
	if err != nil {
		t.Fatal(err)
	}
	return rec.Status
}

func TestBadRUCGetsASecondLookBeforeBotheringTheUser(t *testing.T) {
	// Arrange: la primera lectura trae mal el RUC; la segunda, enfocada en la identidad, lo lee bien.
	h := newHarness(t)
	h.autoSaveOn(t)
	r := &sequenceReader{results: []invoice.Invoice{withRUC(wrongRUC), sampleInvoice()}}
	h.withHandler(Deps{Reader: r})

	// Act
	h.sendPhoto()

	// Assert
	if len(r.received) != 2 || r.received[0].Focus == reader.FocusIdentity || r.received[1].Focus != reader.FocusIdentity {
		t.Fatalf("lecturas = %+v: se esperaba una normal y una enfocada en la identidad", r.received)
	}
	if h.status(t, 1) != store.StatusSaved {
		t.Errorf("con la segunda lectura limpia la factura debía guardarse sola")
	}
	if last := h.telegram.lastSent(t); !strings.Contains(last.text, "80000519-8") || strings.Contains(last.text, "Revisá") {
		t.Errorf("el usuario no debía ver el RUC mal leído: %q", last.text)
	}
}

func TestSecondLookIsKeptOnlyWhenItReadsBetter(t *testing.T) {
	h := newHarness(t)
	second := withRUC(wrongRUC)
	second.Timbrado = "1" // la segunda lectura sale peor
	r := &sequenceReader{results: []invoice.Invoice{withRUC(wrongRUC), second}}
	h.withHandler(Deps{Reader: r})

	h.sendPhoto()

	rec, _ := h.store.Get(context.Background(), testChatID, 1)
	if len(r.received) != 2 || rec.Invoice.Timbrado != "12345678" || rec.CostUSD != 0.002 {
		t.Errorf("debía quedar la primera lectura y sumar el costo de las dos: lecturas=%d timbrado=%q costo=%v",
			len(r.received), rec.Invoice.Timbrado, rec.CostUSD)
	}
}

func TestCleanReadIsNotReadTwice(t *testing.T) {
	h := newHarness(t)

	h.sendPhoto()

	if len(h.reader.received) != 1 {
		t.Errorf("lecturas = %d: una factura que cierra se lee una sola vez", len(h.reader.received))
	}
}

func TestBadRUCIsFixedFromTheSameIssuerSavedBefore(t *testing.T) {
	// Arrange: el usuario ya guardó una factura de este emisor con el RUC correcto.
	h := newHarness(t)
	h.autoSaveOn(t)
	h.sendPhoto()
	inv := withRUC("80000518-1") // mal leído: un dígito de la base y el verificador
	inv.Number = "001-001-0009999"
	inv.IssuerName = "  comercial ejemplo s.a. "
	h.reader.result.Invoice = inv

	// Act
	h.sendPhoto()

	// Assert: sin segunda lectura paga, corregido y guardado, con el aviso de qué se hizo.
	if len(h.reader.received) != 2 {
		t.Errorf("lecturas = %d: el RUC conocido evita la segunda lectura", len(h.reader.received))
	}
	rec, _ := h.store.Get(context.Background(), testChatID, 2)
	if rec.Invoice.IssuerRUC != "80000519-8" || rec.Status != store.StatusSaved {
		t.Errorf("factura = %s %s", rec.Invoice.IssuerRUC, rec.Status)
	}
	if last := h.telegram.lastSent(t); !strings.Contains(last.text, RUCFromHistoryNote) {
		t.Errorf("debía avisar que usó el RUC de facturas anteriores: %q", last.text)
	}
}

func TestProblemFieldGetsItsOwnFixButton(t *testing.T) {
	h := newHarness(t)
	h.reader.result.Invoice = withRUC(wrongRUC)

	h.sendPhoto()

	card := h.telegram.lastSent(t)
	if !strings.Contains(card.markup, "Corregir RUC") || !strings.Contains(card.markup, `"f:1:ruc_emisor:`) {
		t.Errorf("la tarjeta debía traer un botón directo para corregir el RUC: %s", card.markup)
	}
}

func TestCorrectionPromptShowsWhatWasRead(t *testing.T) {
	h := newHarness(t)
	h.reader.result.Invoice = withRUC(wrongRUC)
	h.sendPhoto()

	h.press(callback{action: actionField, id: 1, field: invoice.FieldIssuerRUC})

	prompt := h.telegram.lastSent(t)
	if !strings.Contains(prompt.text, "Leí: "+wrongRUC) || !strings.Contains(prompt.text, "001-001-0001234") {
		t.Errorf("el pedido debía decir qué factura es y qué se leyó: %q", prompt.text)
	}
}

func TestTypedCorrectionSavesAndMovesOnWhenTheInvoiceCloses(t *testing.T) {
	// Arrange: dos pendientes con el RUC mal y guardado automático.
	h := newHarness(t)
	h.autoSaveOn(t)
	h.reader.result.Invoice = withRUC(wrongRUC)
	h.sendPhoto()
	h.sendSecondPhoto()

	// Act: un toque y escribir el RUC.
	h.press(callback{action: actionField, id: 1, field: invoice.FieldIssuerRUC})
	h.sendText("80000519-8")

	// Assert: quedó guardada sin tocar Guardar y aparece la siguiente.
	if h.status(t, 1) != store.StatusSaved {
		t.Fatalf("la factura corregida debía guardarse sola")
	}
	sent := h.telegram.byMethod("sendMessage")
	saved, next := sent[len(sent)-2], sent[len(sent)-1]
	if !strings.Contains(saved.text, "RUC: 80000519-8") || !strings.Contains(saved.text, AutoSavedNote) || !strings.Contains(saved.markup, "Deshacer") {
		t.Errorf("confirmación = %+v", saved)
	}
	if !strings.Contains(next.text, "Siguiente pendiente") || !strings.Contains(next.text, "001-001-0005678") {
		t.Errorf("debía ofrecer la siguiente pendiente: %q", next.text)
	}
}

func TestOneTapRUCFixSavesWhenTheInvoiceCloses(t *testing.T) {
	h := newHarness(t)
	h.autoSaveOn(t)
	h.reader.result.Invoice = withRUC(wrongRUC)
	h.sendPhoto()

	h.press(callback{action: actionUseRUC, id: 1})

	if h.status(t, 1) != store.StatusSaved {
		t.Errorf("con el RUC sugerido la factura cierra y debía guardarse sola")
	}
}

func TestCorrectionWithoutAutoSaveStillAsksToSave(t *testing.T) {
	h := newHarness(t) // el arnés apaga el guardado automático
	h.reader.result.Invoice = withRUC(wrongRUC)
	h.sendPhoto()

	h.press(callback{action: actionField, id: 1, field: invoice.FieldIssuerRUC})
	h.sendText("80000519-8")

	if h.status(t, 1) != store.StatusDraft || !strings.Contains(h.telegram.lastSent(t).markup, "Guardar") {
		t.Errorf("sin guardado automático la factura corregida espera el Guardar")
	}
}

func TestNextPendingComesWithItsPhoto(t *testing.T) {
	h := newHarness(t)
	h.sendPhoto()
	h.sendSecondPhoto()

	h.press(callback{action: actionSave, id: 1})

	photos := h.telegram.byMethod("sendPhoto")
	next := nextPendingMessages(h)
	if len(photos) != 1 || photos[0].fileName != "grande" || !strings.Contains(photos[0].text, "001-001-0005678") {
		t.Fatalf("la siguiente pendiente debía llegar con su foto: %+v", photos)
	}
	if len(next) != 1 || !strings.Contains(next[0].replyTo, `"message_id":12`) { // 12 = la foto recién mandada
		t.Errorf("la tarjeta debía citar la foto recién mandada: %+v", next)
	}
}

func TestPendingCommandShowsTheNextInvoiceToReview(t *testing.T) {
	h := newHarness(t)
	h.reader.result.Invoice = withRUC(wrongRUC)
	h.sendPhoto()

	h.sendText("/pendientes")

	last := h.telegram.lastSent(t)
	if strings.Contains(last.text, "más arriba") || !strings.Contains(last.markup, "Corregir RUC") {
		t.Errorf("/pendientes debía mostrar la factura a revisar con sus botones: %+v", last)
	}
}

func TestLaterButtonShowsAnotherPendingInvoice(t *testing.T) {
	h := newHarness(t)
	h.reader.result.Invoice = withRUC(wrongRUC)
	h.sendPhoto()
	h.sendSecondPhoto()
	h.sendText("/pendientes")
	first := h.telegram.lastSent(t)
	if !strings.Contains(first.text, "001-001-0001234") || !strings.Contains(first.markup, "Después") {
		t.Fatalf("la primera pendiente debía traer el botón Después: %+v", first)
	}

	h.press(callback{action: actionLater, id: 1})

	if last := h.telegram.lastSent(t); !strings.Contains(last.text, "001-001-0005678") {
		t.Errorf("Después debía mostrar la otra pendiente: %q", last.text)
	}
	if h.status(t, 1) != store.StatusDraft {
		t.Errorf("Después no cambia la factura")
	}
}

func TestViewPhotoFallsBackToDocumentForImagesSentAsFiles(t *testing.T) {
	h := newHarness(t)
	h.telegram.failSendPhoto = true
	h.send(&models.Update{Message: &models.Message{
		ID: 80, Chat: models.Chat{ID: testChatID, Type: models.ChatTypePrivate},
		Document: &models.Document{FileID: "archivo", FileUniqueID: "u-archivo", MimeType: "image/jpeg", FileSize: 1000},
	}})

	h.press(callback{action: actionPhoto, id: 1})

	docs := h.telegram.byMethod("sendDocument")
	if len(docs) != 1 || docs[0].fileName != "archivo" {
		t.Errorf("si no se puede mandar como foto, se manda como documento: %+v", docs)
	}
}

// hugePNGHeader es el comienzo de un PNG que declara 12000×12000 píxeles: alcanza para leer su tamaño.
func hugePNGHeader() []byte {
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], 12000)
	binary.BigEndian.PutUint32(ihdr[4:], 12000)
	ihdr[8], ihdr[9] = 8, 2 // 8 bits, RGB
	chunk := append([]byte("IHDR"), ihdr...)
	out := []byte("\x89PNG\r\n\x1a\n")
	out = binary.BigEndian.AppendUint32(out, 13)
	out = append(out, chunk...)
	return binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(chunk))
}

func TestRotateRefusesImagesTooLargeToDecode(t *testing.T) {
	if _, err := rotateImage(hugePNGHeader(), 90); err == nil || !strings.Contains(err.Error(), "demasiado grande") {
		t.Errorf("una imagen de 144 MP no se decodifica: err = %v", err)
	}
}

func TestAdoptingARereadSavesWhenTheInvoiceCloses(t *testing.T) {
	// Arrange: ni la lectura ni su segunda mirada leen bien el RUC; la relectura pedida sí.
	h := newHarness(t)
	h.autoSaveOn(t)
	r := &sequenceReader{results: []invoice.Invoice{withRUC(wrongRUC), withRUC(wrongRUC), sampleInvoice()}}
	h.withHandler(Deps{Reader: r})
	h.sendPhoto()
	h.press(callback{action: actionReread, id: 1})

	// Act
	h.press(callback{action: actionAdopt, id: 1, field: invoice.FieldIssuerRUC})

	// Assert
	if h.status(t, 1) != store.StatusSaved {
		t.Errorf("al adoptar el RUC de la relectura la factura cierra y debía guardarse sola")
	}
}
