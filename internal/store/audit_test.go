package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mmallorquin/bot-marangatu-facturas/internal/invoice"
)

func saveInvoice(t *testing.T, s *Store, chatID int64, inv invoice.Invoice) int64 {
	t.Helper()
	id, err := s.CreateDraft(context.Background(), chatID, newDraft(inv))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Save(context.Background(), chatID, id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestReminderComesBackWhenInvoicesAreSavedAfterTheLastExport(t *testing.T) {
	// Arrange: exportó el día 20 y después guardó otra factura del mismo mes.
	s := openTestStore(t)
	ctx := context.Background()
	const chat, period = int64(1), "2026-09"
	saveInvoice(t, s, chat, sampleInvoice("001-001-0000001", 110_000))
	if _, err := s.db.Exec(`INSERT INTO exports (chat_id, period, seq, delivered_at) VALUES (?, ?, 1, '2026-09-20T12:00:00Z')`, chat, period); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE invoices SET updated_at = '2026-09-19T12:00:00Z'`); err != nil {
		t.Fatal(err)
	}

	// Assert: todo lo guardado ya está en un ZIP, no hay aviso.
	if got, err := s.ReminderCandidates(ctx, period); err != nil || len(got) != 0 {
		t.Fatalf("con todo exportado no se avisa: %+v, %v", got, err)
	}

	// Act: guarda otra después de exportar.
	later := saveInvoice(t, s, chat, sampleInvoice("001-001-0000002", 110_000))
	if _, err := s.db.Exec(`UPDATE invoices SET updated_at = '2026-09-25T12:00:00Z' WHERE id = ?`, later); err != nil {
		t.Fatal(err)
	}

	// Assert
	if got, err := s.ReminderCandidates(ctx, period); err != nil || len(got) != 1 || got[0].ChatID != chat {
		t.Errorf("hay una factura que no está en ningún ZIP y debía avisarse: %+v, %v", got, err)
	}
}

func TestSameReceiptReadAsTicketAndAsInvoiceIsADuplicate(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	inv := sampleInvoice("001-001-0000001", 110_000)
	saveInvoice(t, s, 1, inv)

	inv.Type = invoice.TypeTicket
	id, err := s.CreateDraft(ctx, 1, newDraft(inv))
	if err != nil {
		t.Fatal(err)
	}

	if err := s.Save(ctx, 1, id); !errors.Is(err, ErrDuplicate) {
		t.Errorf("el mismo comprobante leído como ticket es un duplicado: %v", err)
	}
}

func TestIdentityDistinguishesTimbradoAndCreditNotes(t *testing.T) {
	s := openTestStore(t)
	inv := sampleInvoice("001-001-0000001", 110_000)
	saveInvoice(t, s, 1, inv)

	renewed := inv
	renewed.Timbrado = "87654321" // el emisor renovó timbrado y la numeración reinició
	saveInvoice(t, s, 1, renewed)

	credit := inv
	credit.Type = invoice.TypeNotaCredito // una nota de crédito puede repetir el número de una factura
	saveInvoice(t, s, 1, credit)
}

func TestSummaryTellsApartWhatDoesNotGoIntoTheFile(t *testing.T) {
	s := openTestStore(t)
	saveInvoice(t, s, 1, sampleInvoice("001-001-0000001", 110_000))
	credit := sampleInvoice("001-001-0000002", 110_000)
	credit.Type = invoice.TypeNotaCredito
	saveInvoice(t, s, 1, credit)

	sum, err := s.MonthSummary(context.Background(), 1, "2026-09")

	if err != nil || sum.Count != 2 || sum.Excluded != 1 || sum.ExcludedTotal != 110_000 {
		t.Errorf("resumen = %+v, %v: la nota de crédito no entra al archivo", sum, err)
	}
}

func TestWritesRefuseAnInvoiceOfAnotherChat(t *testing.T) {
	// Los handlers leen antes con Get, que ya filtra por chat; esto cubre las escrituras por sí solas.
	s := openTestStore(t)
	ctx := context.Background()
	const owner, other = int64(1), int64(2)
	draft, err := s.CreateDraft(ctx, owner, newDraft(sampleInvoice("001-001-0000001", 110_000)))
	if err != nil {
		t.Fatal(err)
	}
	saved := saveInvoice(t, s, owner, sampleInvoice("001-001-0000002", 110_000))
	edited := sampleInvoice("001-001-0000009", 220_000)

	writes := map[string]error{
		"Save":          s.Save(ctx, other, draft),
		"Discard":       s.Discard(ctx, other, draft),
		"UpdateInvoice": s.UpdateInvoice(ctx, other, draft, edited),
		"SaveReread":    s.SaveReread(ctx, other, draft, edited),
		"SetAwaiting":   s.SetAwaiting(ctx, other, draft, invoice.FieldTotal),
		"Unsave":        s.Unsave(ctx, other, saved),
		"DeleteSaved":   s.DeleteSaved(ctx, other, saved),
	}
	for name, err := range writes {
		if err == nil {
			t.Errorf("%s aceptó una factura de otro chat", name)
		}
	}
	if rec, _ := s.Get(ctx, owner, draft); rec.Status != StatusDraft || rec.Invoice.Number != "001-001-0000001" {
		t.Errorf("el borrador del dueño cambió: %+v", rec)
	}
	if rec, _ := s.Get(ctx, owner, saved); rec.Status != StatusSaved {
		t.Errorf("la guardada del dueño cambió: %s", rec.Status)
	}
}

func TestMetricsCountDaysInParaguayTime(t *testing.T) {
	// 20:30 y 21:15 de Asunción del mismo día caen en dos días distintos en UTC.
	s := openTestStore(t)
	ctx := context.Background()
	for _, at := range []string{"2026-11-19T23:30:00Z", "2026-11-20T00:15:00Z"} {
		if _, err := s.db.Exec(`INSERT INTO events (chat_id, kind, detail, seconds, cost_usd, created_at) VALUES (1, ?, '', 0, 0, ?)`, EventPhoto, at); err != nil {
			t.Fatal(err)
		}
	}

	now := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	m, err := s.Metrics(ctx, MetricsQuery{Since: now.AddDate(0, 0, -7), Now: now})

	if err != nil || m.ReturningUsers != 0 {
		t.Errorf("usar el bot dos veces la misma noche no es volver otro día: %+v, %v", m.ReturningUsers, err)
	}
}
