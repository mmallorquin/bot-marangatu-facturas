package telegram

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"

	"github.com/mmallorquin/bot-marangatu-facturas/internal/invoice"
	"github.com/mmallorquin/bot-marangatu-facturas/internal/store"
)

func (h *harness) metrics(t *testing.T) store.Metrics {
	t.Helper()
	now := time.Now()
	m, err := h.store.Metrics(context.Background(), store.MetricsQuery{Since: now.Add(-time.Hour), Now: now})
	if err != nil {
		t.Fatalf("Metrics: %v", err)
	}
	return m
}

func TestUsageIsRecordedWithoutChangingReplies(t *testing.T) {
	// Arrange
	h := newHarness(t)

	// Act: una foto corregida (una vez con un valor inválido), guardada y exportada.
	h.sendText("/start")
	h.sendPhoto()
	h.press(callback{action: actionField, id: 1, field: invoice.FieldTotal})
	h.sendText("muchísimo")
	h.sendText("/cancelar")
	h.press(callback{action: actionField, id: 1, field: invoice.FieldIssuerName})
	h.sendText("Otra Razón S.A.")
	h.press(callback{action: actionSave, id: 1})
	h.sendText("hola")
	h.sendText("/ruc 80024627-6")
	h.sendText("/imputar iva")
	h.sendText("/registro 955")
	h.sendText("/exportar 09/2026")
	h.pressExport(t, "x:c:2026-09")
	h.pressExport(t, "x:z:2026-09")

	// Assert
	m := h.metrics(t)
	if m.Users != 1 || m.UsersSaved != 1 || m.UsersExported != 1 {
		t.Errorf("usuarios = %d, guardaron = %d, exportaron = %d", m.Users, m.UsersSaved, m.UsersExported)
	}
	if m.Photos != 1 || m.ReadOK != 1 || m.Saved != 1 || m.SavedClean != 0 {
		t.Errorf("embudo = fotos %d, ok %d, guardadas %d, sin corregir %d", m.Photos, m.ReadOK, m.Saved, m.SavedClean)
	}
	if !slices.Equal(m.Corrections, []store.FieldCount{{Field: invoice.FieldIssuerName, Count: 1}}) || m.BadCorrections != 1 {
		t.Errorf("correcciones = %+v, inválidas = %d", m.Corrections, m.BadCorrections)
	}
	if m.SavedCompared != 1 || len(m.FieldAccuracy) != 1 || m.FieldAccuracy[0].Field != invoice.FieldIssuerName {
		t.Errorf("precisión = %d comparadas, %+v", m.SavedCompared, m.FieldAccuracy)
	}
	if m.ExportPreviews != 1 || m.ExportReviews != 1 || m.ExportZIPs != 1 || m.UnknownText != 1 {
		t.Errorf("funciones = previa %d, revisión %d, zip %d, no entendidos %d",
			m.ExportPreviews, m.ExportReviews, m.ExportZIPs, m.UnknownText)
	}
	if m.CostUSD != 0.001 {
		t.Errorf("costo = %v", m.CostUSD)
	}
}

func TestFailedReadsAndRejectedFilesAreRecorded(t *testing.T) {
	// Arrange
	h := newHarness(t)
	h.reader.result.Invoice = invoice.Invoice{IsInvoice: false}

	// Act
	h.sendPhoto()
	h.telegram.failGetFile = true
	h.sendPhoto()

	// Assert
	m := h.metrics(t)
	if m.Photos != 2 || m.NotInvoice != 1 || m.ReadErrors != 1 {
		t.Errorf("fotos %d, no factura %d, errores %d", m.Photos, m.NotInvoice, m.ReadErrors)
	}
}

func TestReadTimeoutIsStillRecorded(t *testing.T) {
	// Arrange
	h := newHarness(t)
	h.reader.err = context.DeadlineExceeded
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // el contexto del pedido ya venció

	// Act
	h.handle(ctx, h.bot, photoUpdate())

	// Assert
	if m := h.metrics(t); m.ReadErrors != 1 {
		t.Errorf("errores de lectura = %d, se esperaba 1", m.ReadErrors)
	}
}

func TestPDFIsReadAndCountedAsPDF(t *testing.T) {
	// Arrange
	h := newHarness(t)

	// Act
	h.send(&models.Update{Message: &models.Message{
		Chat:     models.Chat{ID: testChatID, Type: models.ChatTypePrivate},
		Document: &models.Document{FileID: "pdf", MimeType: "application/pdf", FileSize: 200_000},
	}})

	// Assert
	if len(h.reader.received) != 1 || h.reader.received[0].MimeType != "application/pdf" {
		t.Fatalf("el lector debería recibir el PDF: %+v", h.reader.received)
	}
	if got := h.telegram.lastSent(t); !strings.Contains(got.markup, `"g:1"`) {
		t.Errorf("debería mostrar la factura con botones: %+v", got)
	}
	if m := h.metrics(t); m.Photos != 1 || m.PDFs != 1 {
		t.Errorf("archivos = %d, PDF = %d", m.Photos, m.PDFs)
	}
}
