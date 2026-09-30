package store

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/mmallorquin/bot-marangatu-facturas/internal/invoice"
)

var metricsNow = time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

// logAt registra un evento en una fecha fija.
func logAt(t *testing.T, s *Store, chatID int64, at time.Time, e Event) {
	t.Helper()
	_, err := s.db.Exec(`
		INSERT INTO events (chat_id, kind, detail, seconds, cost_usd, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		chatID, e.Kind, e.Detail, e.Seconds, e.CostUSD, at.UTC().Format(time.RFC3339))
	if err != nil {
		t.Fatal(err)
	}
}

func lastWeek(t *testing.T, s *Store, exclude ...int64) Metrics {
	t.Helper()
	m, err := s.Metrics(context.Background(), MetricsQuery{Since: metricsNow.AddDate(0, 0, -7), Now: metricsNow, Exclude: exclude})
	if err != nil {
		t.Fatalf("Metrics: %v", err)
	}
	return m
}

func TestMetricsCountsUsersAndFunnel(t *testing.T) {
	// Arrange
	s := openTestStore(t)
	old := metricsNow.AddDate(0, 0, -30)
	yesterday := metricsNow.AddDate(0, 0, -1)
	logAt(t, s, chatA, old, Event{Kind: EventStart}) // usuario viejo que volvió
	logAt(t, s, chatA, yesterday, Event{Kind: EventPhoto})
	logAt(t, s, chatA, yesterday, Event{Kind: EventRead, Detail: EventDetailOK, Seconds: 2, CostUSD: 0.001})
	logAt(t, s, chatA, yesterday, Event{Kind: EventSaved, Detail: EventDetailClean})
	logAt(t, s, chatA, metricsNow, Event{Kind: EventPhoto})
	logAt(t, s, chatA, metricsNow, Event{Kind: EventRead, Detail: EventDetailIssues, Seconds: 4, CostUSD: 0.001})
	logAt(t, s, chatA, metricsNow, Event{Kind: EventCorrection, Detail: invoice.FieldTotal})
	logAt(t, s, chatA, metricsNow, Event{Kind: EventSaved, Detail: EventDetailCorrected})
	logAt(t, s, chatA, metricsNow, Event{Kind: EventExportZIP})
	logAt(t, s, chatB, metricsNow, Event{Kind: EventPhoto}) // usuario nuevo
	logAt(t, s, chatB, metricsNow, Event{Kind: EventNotInvoice, Seconds: 10, CostUSD: 0.002})

	// Act
	m := lastWeek(t, s)

	// Assert
	if m.Users != 2 || m.NewUsers != 1 || m.ReturningUsers != 1 || m.AllTimeUsers != 2 {
		t.Errorf("usuarios = %+v", m)
	}
	if m.UsersSaved != 1 || m.UsersExported != 1 {
		t.Errorf("activación: guardaron %d, exportaron %d", m.UsersSaved, m.UsersExported)
	}
	if m.Photos != 3 || m.ReadOK != 1 || m.ReadIssues != 1 || m.NotInvoice != 1 || m.Saved != 2 || m.SavedClean != 1 {
		t.Errorf("embudo = %+v", m)
	}
	if m.CostUSD < 0.00399 || m.CostUSD > 0.00401 {
		t.Errorf("costo = %v", m.CostUSD)
	}
	if m.AvgSeconds != 16.0/3 || m.P90Seconds != 10 {
		t.Errorf("tiempos = %.2f promedio, %.2f p90", m.AvgSeconds, m.P90Seconds)
	}
	if len(m.PerUser) != 2 || m.PerUser[0].Photos+m.PerUser[1].Photos != 3 {
		t.Errorf("por usuario = %+v", m.PerUser)
	}
	for _, u := range m.PerUser {
		if u.ChatID == chatA && (u.Days != 2 || u.Saved != 2 || u.ZIPs != 1 || !u.FirstSeen.Equal(old)) {
			t.Errorf("usuario A = %+v", u)
		}
	}
}

func TestMetricsExcludesChats(t *testing.T) {
	// Arrange
	s := openTestStore(t)
	logAt(t, s, chatA, metricsNow, Event{Kind: EventPhoto})
	logAt(t, s, chatB, metricsNow, Event{Kind: EventPhoto})
	if _, err := s.CreateDraft(context.Background(), chatB, newDraft(sampleInvoice("001-001-0000001", 110_000))); err != nil {
		t.Fatal(err)
	}

	// Act
	m := lastWeek(t, s, chatB)

	// Assert
	if m.Users != 1 || m.Photos != 1 || m.AllTimeUsers != 1 || len(m.PerUser) != 1 || m.PerUser[0].ChatID != chatA {
		t.Errorf("no excluyó el chat B: %+v", m)
	}
}

func TestMetricsComparesWhatTheAIReadWithWhatWasSaved(t *testing.T) {
	// Arrange
	s := openTestStore(t)
	ctx := context.Background()
	read := sampleInvoice("1-1-1234", 110_000) // el número se normaliza: no cuenta como corrección
	id, _ := s.CreateDraft(ctx, chatA, newDraft(read))
	fixed := read
	fixed.Number = invoice.NormalizeNumber(read.Number)
	fixed.Date = "2026-09-21"
	if err := s.UpdateInvoice(ctx, chatA, id, fixed); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(ctx, chatA, id); err != nil {
		t.Fatal(err)
	}
	clean, _ := s.CreateDraft(ctx, chatA, newDraft(sampleInvoice("001-001-0000002", 55_000)))
	if err := s.Save(ctx, chatA, clean); err != nil {
		t.Fatal(err)
	}

	// Act
	m, err := s.Metrics(ctx, MetricsQuery{Since: time.Now().Add(-time.Hour), Now: time.Now()})

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	want := []FieldAccuracy{{Field: invoice.FieldDate, Corrected: 1}}
	if m.SavedCompared != 2 || len(m.FieldAccuracy) != 1 || m.FieldAccuracy[0] != want[0] {
		t.Errorf("precisión = %d comparadas, %+v", m.SavedCompared, m.FieldAccuracy)
	}
}

func TestMetricsCountsAbandonedDrafts(t *testing.T) {
	// Arrange
	s := openTestStore(t)
	ctx := context.Background()
	oldID, _ := s.CreateDraft(ctx, chatA, newDraft(sampleInvoice("001-001-0000001", 110_000)))
	_, _ = s.CreateDraft(ctx, chatA, newDraft(sampleInvoice("001-001-0000002", 110_000)))
	twoDaysAgo := metricsNow.Add(-48 * time.Hour).Format(time.RFC3339)
	if _, err := s.db.Exec(`UPDATE invoices SET created_at = ? WHERE id = ?`, twoDaysAgo, oldID); err != nil {
		t.Fatal(err)
	}

	// Act: el borrador de hace 2 días está abandonado; el recién creado todavía no.
	m, err := s.Metrics(ctx, MetricsQuery{Since: metricsNow.AddDate(0, 0, -7), Now: metricsNow})

	// Assert
	if err != nil || m.Abandoned != 1 {
		t.Errorf("abandonados = %d, error = %v", m.Abandoned, err)
	}
}

func TestEventsNeverStoreInvoiceData(t *testing.T) {
	// Arrange
	s := openTestStore(t)

	// Act
	if err := s.LogEvent(context.Background(), chatA, Event{Kind: EventCorrection, Detail: invoice.FieldTotal}); err != nil {
		t.Fatal(err)
	}

	// Assert: la tabla solo tiene columnas de uso.
	rows, err := s.db.Query(`SELECT name FROM pragma_table_info('events')`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var name string
		_ = rows.Scan(&name)
		columns = append(columns, name)
	}
	got, _ := json.Marshal(columns)
	if string(got) != `["id","chat_id","kind","detail","seconds","cost_usd","created_at"]` {
		t.Errorf("columnas = %s", got)
	}
}

func TestAverageAndP90(t *testing.T) {
	avg, p90 := averageAndP90([]float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10})
	if avg != 5.5 || p90 != 9 {
		t.Errorf("promedio %.1f, p90 %.1f", avg, p90)
	}
	if avg, p90 := averageAndP90(nil); avg != 0 || p90 != 0 {
		t.Error("sin datos debería dar cero")
	}
}

func TestOpenReadOnlyCannotWrite(t *testing.T) {
	// Arrange
	path := t.TempDir() + "/facturas.db"
	rw, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = rw.Close()

	// Act
	ro, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()

	// Assert
	if err := ro.LogEvent(context.Background(), chatA, Event{Kind: EventStart}); err == nil {
		t.Error("una base de solo lectura no debería aceptar escrituras")
	}
	if _, err := OpenReadOnly(t.TempDir() + "/no-existe.db"); err == nil {
		t.Error("debería fallar si la base no existe")
	}
}
