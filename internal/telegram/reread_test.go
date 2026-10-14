package telegram

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"

	"github.com/mmallorquin/bot-marangatu-facturas/internal/invoice"
	"github.com/mmallorquin/bot-marangatu-facturas/internal/reader"
	"github.com/mmallorquin/bot-marangatu-facturas/internal/store"
)

func lastEdit(t *testing.T, h *harness) outgoing {
	t.Helper()
	edits := h.telegram.byMethod("editMessageText")
	if len(edits) == 0 {
		t.Fatal("no hubo ediciones de mensaje")
	}
	return edits[len(edits)-1]
}

func TestViewPhotoResendsTheOriginalByFileID(t *testing.T) {
	// Arrange
	h := newHarness(t)
	h.sendPhoto()
	read := h.telegram.lastSent(t)

	// Act
	h.press(callback{action: actionPhoto, id: 1})

	// Assert: la foto va por file_id (sin volver a bajarla) y cita el mensaje de la factura.
	if !strings.Contains(read.markup, `"o:1"`) || !strings.Contains(read.markup, `"b:1"`) {
		t.Errorf("faltan Ver foto / Releer: %s", read.markup)
	}
	photos := h.telegram.byMethod("sendPhoto")
	if len(photos) != 1 || photos[0].fileName != "grande" || !strings.Contains(photos[0].replyTo, `"message_id":10`) {
		t.Errorf("reenvío de la foto: %+v", photos)
	}
	if h.telegram.requestedFileID != "grande" || len(h.reader.received) != 1 {
		t.Error("ver la foto no debería descargarla ni leerla de nuevo")
	}
}

func TestViewPDFResendsItAsDocument(t *testing.T) {
	h := newHarness(t)
	h.send(&models.Update{Message: &models.Message{ID: 78, Chat: models.Chat{ID: testChatID, Type: models.ChatTypePrivate},
		Document: &models.Document{FileID: "pdf-1", MimeType: "application/pdf", FileSize: 1000}}})

	h.press(callback{action: actionPhoto, id: 1})

	docs := h.telegram.byMethod("sendDocument")
	if len(docs) != 1 || docs[0].fileName != "pdf-1" {
		t.Errorf("reenvío del PDF: %+v", docs)
	}
}

func TestRereadOffersTheFieldsThatDifferAndAppliesThem(t *testing.T) {
	// Arrange: primera lectura con el DV del RUC y el número mal; la relectura los lee bien.
	h := newHarness(t)
	wrong := sampleInvoice()
	wrong.IssuerRUC = "80000519-1"
	wrong.Number = "001-001-0001239"
	h.reader.result.Invoice = wrong
	h.sendPhoto()
	h.reader.result.Invoice = sampleInvoice()

	// Act
	h.press(callback{action: actionReread, id: 1})
	offer := h.telegram.lastSent(t)

	// Assert: segunda lectura enfocada, mensaje con las diferencias y un botón por campo.
	if len(h.reader.received) != 2 || h.reader.received[1].Focus != reader.FocusIdentity {
		t.Fatalf("la relectura debería ir enfocada: %+v", h.reader.received)
	}
	for _, want := range []string{"Relectura 1 de 2", "80000519-1 → 80000519-8", "0001239 → 001-001-0001234", `"a:1:ruc_emisor"`, `"a:1:numero"`, `"a:1:todo"`} {
		if !strings.Contains(offer.text+offer.markup, want) {
			t.Errorf("falta %q en la oferta: %+v", want, offer)
		}
	}
	if !strings.Contains(offer.replyTo, `"message_id":10`) {
		t.Errorf("la oferta cita el mensaje de la factura: %q", offer.replyTo)
	}

	// Act: tomar solo el RUC.
	h.press(callback{action: actionAdopt, id: 1, field: invoice.FieldIssuerRUC})
	applied := lastEdit(t, h)
	rec, _ := h.store.Get(context.Background(), testChatID, 1)

	// Assert
	if rec.Invoice.IssuerRUC != "80000519-8" || rec.Invoice.Number != "001-001-0001239" || !rec.Corrected || rec.Rereads != 1 {
		t.Errorf("registro = %+v", rec)
	}
	if !strings.Contains(applied.text, "RUC: 80000519-8") || !strings.Contains(applied.markup, `"g:1"`) {
		t.Errorf("después de aplicar vuelve la factura con sus botones: %+v", applied)
	}
	now := time.Now()
	m, _ := h.store.Metrics(context.Background(), store.MetricsQuery{Since: now.Add(-time.Hour), Now: now})
	if m.Rereads != 1 || m.CostUSD < 0.0019 {
		t.Errorf("métricas: relecturas %d, costo %.4f", m.Rereads, m.CostUSD)
	}
}

func TestRereadAdoptAllAndLimit(t *testing.T) {
	h := newHarness(t)
	wrong := sampleInvoice()
	wrong.IssuerRUC = "80000519-1"
	wrong.Timbrado = "12345670"
	h.reader.result.Invoice = wrong
	h.sendPhoto()
	h.reader.result.Invoice = sampleInvoice()

	h.press(callback{action: actionReread, id: 1})
	h.press(callback{action: actionAdopt, id: 1, field: rereadAllFields})
	rec, _ := h.store.Get(context.Background(), testChatID, 1)
	if rec.Invoice.IssuerRUC != "80000519-8" || rec.Invoice.Timbrado != "12345678" {
		t.Errorf("\"todo\" aplica todos los campos: %+v", rec.Invoice)
	}

	h.press(callback{action: actionReread, id: 1}) // segunda: ahora coincide
	same := h.telegram.lastSent(t).text
	h.press(callback{action: actionReread, id: 1}) // tercera: tope
	answers := h.telegram.byMethod("answerCallbackQuery")
	limit := answers[len(answers)-1]

	if !strings.Contains(same, "leyó lo mismo") {
		t.Errorf("segunda relectura: %q", same)
	}
	if !limit.alert || !strings.Contains(limit.text, "dos veces") || len(h.reader.received) != 3 {
		t.Errorf("la tercera relectura se rechaza sin llamar al modelo: %+v, lecturas %d", limit, len(h.reader.received))
	}
}

func TestRereadWithoutSourceOrWhenTelegramLostThePhoto(t *testing.T) {
	// Un borrador creado antes de guardar la referencia a la foto.
	h := newHarness(t)
	if _, err := h.store.CreateDraft(context.Background(), testChatID, store.Draft{Invoice: sampleInvoice(), Model: "m"}); err != nil {
		t.Fatal(err)
	}
	h.press(callback{action: actionReread, id: 1})
	h.press(callback{action: actionPhoto, id: 1})
	answers := h.telegram.byMethod("answerCallbackQuery")
	if len(answers) != 2 || !strings.Contains(answers[0].text, "No tengo la foto") || !strings.Contains(answers[1].text, "No tengo la foto") {
		t.Errorf("sin referencia a la foto: %+v", answers)
	}

	// Telegram ya no tiene el archivo.
	h2 := newHarness(t)
	h2.sendPhoto()
	h2.telegram.failGetFile = true
	h2.press(callback{action: actionReread, id: 1})
	if got := h2.telegram.lastSent(t).text; !strings.Contains(got, "ya no la tiene") {
		t.Errorf("foto perdida: %q", got)
	}
	if len(h2.reader.received) != 1 {
		t.Error("sin foto no se llama al modelo")
	}
}
