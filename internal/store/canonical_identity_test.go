package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mmallorquin/bot-marangatu-facturas/internal/invoice"
)

// Esquema anterior real de invoices, sin la columna aditiva de identidad.
const legacyInvoiceSchema = `CREATE TABLE invoices (
	id INTEGER PRIMARY KEY AUTOINCREMENT, chat_id INTEGER NOT NULL, status TEXT NOT NULL,
	invoice_json TEXT NOT NULL, original_json TEXT NOT NULL, model TEXT NOT NULL,
	cost_usd REAL NOT NULL, corrected INTEGER NOT NULL DEFAULT 0, awaiting_field TEXT,
	dedup_key TEXT, period TEXT, created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE UNIQUE INDEX invoices_saved_unique ON invoices (chat_id, dedup_key) WHERE status = 'guardada';`

func insertLegacySaved(t *testing.T, db *sql.DB, chatID int64, inv invoice.Invoice) int64 {
	t.Helper()
	data, err := json.Marshal(inv)
	if err != nil {
		t.Fatal(err)
	}
	// La versión anterior normalizaba número, pero dejaba el RUC textual en la clave.
	key := fmt.Sprintf("%s|%s|%s|%s", inv.Type, inv.IssuerRUC, inv.Timbrado, invoice.NormalizeNumber(inv.Number))
	res, err := db.Exec(`INSERT INTO invoices (chat_id,status,invoice_json,original_json,model,cost_usd,dedup_key,period,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)`, chatID, StatusSaved, string(data), string(data), "synthetic", 0, key, periodOf(inv.Date), "2026-09-20T12:00:00Z", "2026-09-20T12:00:05Z")
	if err != nil {
		t.Fatal(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestCanonicalIdentityRejectsRUCVariantsAndPreservesOriginal(t *testing.T) {
	for _, ruc := range []string{" 80000519-8 ", "800005198", "\t80000519-8\n"} {
		t.Run(ruc, func(t *testing.T) {
			s := openTestStore(t)
			ctx := context.Background()
			inv := sampleInvoice("1-1-1", 110000)
			inv.IssuerRUC = ruc
			id, err := s.CreateDraft(ctx, chatA, newDraft(inv))
			if err != nil {
				t.Fatal(err)
			}
			if err := s.Save(ctx, chatA, id); err != nil {
				t.Fatal(err)
			}
			rec, err := s.Get(ctx, chatA, id)
			if err != nil {
				t.Fatal(err)
			}
			if rec.Invoice.IssuerRUC != "80000519-8" || rec.Invoice.Number != "001-001-0000001" || rec.Original.IssuerRUC != ruc || rec.Original.Number != "1-1-1" || rec.Corrected {
				t.Fatal("la normalización debe preservar lectura y correcciones originales")
			}
			canonical := sampleInvoice("001-001-0000001", 110000)
			if saved, err := s.IsSaved(ctx, chatA, canonical); err != nil || !saved {
				t.Fatalf("IsSaved = %v, %v", saved, err)
			}
			second, err := s.CreateDraft(ctx, chatA, newDraft(canonical))
			if err != nil {
				t.Fatal(err)
			}
			if err := s.Save(ctx, chatA, second); !errors.Is(err, ErrDuplicate) {
				t.Fatalf("Save = %v", err)
			}
			other, err := s.CreateDraft(ctx, chatB, newDraft(canonical))
			if err != nil {
				t.Fatal(err)
			}
			if err := s.Save(ctx, chatB, other); err != nil {
				t.Fatal("se mezclaron identidades de chats")
			}
			if err := s.Unsave(ctx, chatA, id); err != nil {
				t.Fatal(err)
			}
			if err := s.Save(ctx, chatA, second); err != nil {
				t.Fatalf("deshacer debe liberar identidad: %v", err)
			}
		})
	}
}

func TestCanonicalIdentityIgnoresRUCLetterCase(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	inv := sampleInvoice("001-001-0000001", 110000)
	inv.IssuerRUC = fmt.Sprintf("12345a-%d", invoice.CheckDigit("12345A"))
	id, err := s.CreateDraft(ctx, chatA, newDraft(inv))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Save(ctx, chatA, id); err != nil {
		t.Fatal(err)
	}
	inv.IssuerRUC = strings.ToUpper(inv.IssuerRUC)
	if saved, err := s.IsSaved(ctx, chatA, inv); err != nil || !saved {
		t.Fatalf("IsSaved = %v, %v", saved, err)
	}
}

func TestCanonicalMigrationPreservesCollisionsAndRecovery(t *testing.T) {
	for _, differentMonths := range []bool{false, true} {
		t.Run(fmt.Sprintf("differentMonths=%t", differentMonths), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "legacy.db")
			old, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := old.Exec(legacyInvoiceSchema); err != nil {
				t.Fatal(err)
			}
			first := sampleInvoice("001-001-0000001", 110000)
			second := first
			second.IssuerRUC = " " + first.IssuerRUC + " "
			if differentMonths {
				second.Date = "2026-08-31"
			}
			id1 := insertLegacySaved(t, old, chatA, first)
			id2 := insertLegacySaved(t, old, chatA, second)
			if err := old.Close(); err != nil {
				t.Fatal(err)
			}
			s, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			for _, entry := range []struct {
				id  int64
				inv invoice.Invoice
			}{{id1, first}, {id2, second}} {
				var status, data, original, rawKey, canonicalKey string
				if err := s.db.QueryRow(`SELECT status,invoice_json,original_json,dedup_key,canonical_dedup_key FROM invoices WHERE id=?`, entry.id).Scan(&status, &data, &original, &rawKey, &canonicalKey); err != nil {
					t.Fatal(err)
				}
				want, _ := json.Marshal(entry.inv)
				if status != string(StatusSaved) || data != string(want) || original != string(want) || canonicalKey != dedupKey(first) || !strings.Contains(rawKey, entry.inv.IssuerRUC) {
					t.Fatal("la migración alteró datos/estado originales")
				}
			}
			if list, err := s.SavedRecords(ctx, chatA, "2026"); err != nil || len(list) != 2 {
				t.Fatalf("historial = %d, %v", len(list), err)
			}
			if sum, err := s.MonthSummary(ctx, chatA, "2026"); err != nil || !sum.HasDuplicates || sum.Count != 2 {
				t.Fatalf("resumen debe permitir recuperación: %+v, %v", sum, err)
			}
			if _, err := s.SavedInvoices(ctx, chatA, "2026"); !errors.Is(err, ErrDuplicateInvoices) {
				t.Fatalf("export anual = %v", err)
			}
			if differentMonths {
				if list, err := s.SavedInvoices(ctx, chatA, "2026-09"); err != nil || len(list) != 1 {
					t.Fatalf("período sin copias seleccionadas = %d, %v", len(list), err)
				}
			}
			if saved, err := s.IsSaved(ctx, chatA, first); err != nil || !saved {
				t.Fatalf("legacy IsSaved = %v, %v", saved, err)
			}
			// Segunda apertura: la migración debe ser idempotente.
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			s, err = Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if err := s.DeleteSaved(ctx, chatB, id2); !errors.Is(err, ErrNotFound) {
				t.Fatalf("aislamiento al quitar = %v", err)
			}
			if err := s.DeleteSaved(ctx, chatA, id2); err != nil {
				t.Fatal(err)
			}
			if list, err := s.SavedInvoices(ctx, chatA, "2026"); err != nil || len(list) != 1 {
				t.Fatalf("export tras quitar copia = %d, %v", len(list), err)
			}
			if rec, err := s.Get(ctx, chatA, id2); err != nil || rec.Status != StatusDeleted || rec.Original.IssuerRUC != second.IssuerRUC {
				t.Fatal("quitar copia debe ser lógico y preservar original")
			}
		})
	}
}

func TestCanonicalMigrationBackfillsRowsCreatedAfterRollback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollback.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	inv := sampleInvoice("001-001-0000001", 110000)
	inv.IssuerRUC = " 80000519-8 "
	// Simula el INSERT de la versión anterior que desconoce la columna nueva.
	insertLegacySaved(t, s.db, chatA, inv)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	inv.IssuerRUC = "80000519-8"
	if saved, err := s.IsSaved(ctx, chatA, inv); err != nil || !saved {
		t.Fatalf("fila de rollback = %v, %v", saved, err)
	}
	id, err := s.CreateDraft(ctx, chatA, newDraft(inv))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Save(ctx, chatA, id); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("Save tras rollback = %v", err)
	}
}

func TestCanonicalMigrationRollsBackOnUnreadableSavedData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "corrupt.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(legacyInvoiceSchema); err != nil {
		t.Fatal(err)
	}
	id := insertLegacySaved(t, db, chatA, sampleInvoice("001-001-0000001", 110000))
	if _, err := db.Exec(`UPDATE invoices SET invoice_json = ? WHERE id = ?`, `{"numero":`, id); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if s, err := Open(path); err == nil {
		s.Close()
		t.Fatal("migración debe rechazar datos ilegibles")
	} else if !strings.Contains(err.Error(), "factura 1") || strings.Contains(err.Error(), "numero") {
		t.Fatalf("diagnóstico no seguro: %v", err)
	}
	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var exists bool
	if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM pragma_table_info('invoices') WHERE name='canonical_dedup_key')`).Scan(&exists); err != nil || exists {
		t.Fatalf("ALTER no se revirtió = %v, %v", exists, err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM invoices WHERE status='guardada'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("fila preservada = %d, %v", count, err)
	}
}

func TestCanonicalMigrationRefreshesIdentityChangedByAnOlderVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old_correction.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	before := sampleInvoice("001-001-0000001", 110000)
	id, err := s.CreateDraft(ctx, chatA, newDraft(before))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Save(ctx, chatA, id); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	// La versión anterior deshace/corrige/guarda sin conocer la columna canónica.
	after := before
	after.Number = "001-001-0000002"
	data, err := json.Marshal(after)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE invoices SET invoice_json=?, dedup_key=?, corrected=1 WHERE id=?`, string(data), dedupKey(after), id); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if found, err := s.IsSaved(ctx, chatA, after); err != nil || !found {
		t.Fatalf("identidad corregida no actualizada: %v, %v", found, err)
	}
	if found, err := s.IsSaved(ctx, chatA, before); err != nil || found {
		t.Fatalf("identidad anterior sigue ocupada: %v, %v", found, err)
	}
	if rec, err := s.Get(ctx, chatA, id); err != nil || rec.Original.Number != before.Number || rec.Invoice.Number != after.Number || !rec.Corrected {
		t.Fatal("reapertura alteró la corrección anterior")
	}
	second, err := s.CreateDraft(ctx, chatA, newDraft(after))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Save(ctx, chatA, second); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("Save tras corrección antigua: %v", err)
	}
}

func TestMetricsIdentityNormalizationIsNotAHumanCorrection(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	inv := sampleInvoice("1-1-1", 110000)
	inv.IssuerRUC = " 80000519-8 "
	id, err := s.CreateDraft(ctx, chatA, newDraft(inv))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Save(ctx, chatA, id); err != nil {
		t.Fatal(err)
	}
	m, err := s.Metrics(ctx, MetricsQuery{Since: time.Now().Add(-time.Hour), Now: time.Now()})
	if err != nil || m.SavedCompared != 1 || len(m.FieldAccuracy) != 0 {
		t.Fatalf("comparación de identidad = %+v, %v", m.FieldAccuracy, err)
	}
}

func TestMetricsTimeToSaveStartsAtDraftCreation(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	id, err := s.CreateDraft(ctx, chatA, newDraft(sampleInvoice("001-001-0000001", 110000)))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Save(ctx, chatA, id); err != nil {
		t.Fatal(err)
	}
	received := metricsNow.Add(-time.Minute)
	created := received.Add(40 * time.Second)
	saved := created.Add(5 * time.Second)
	if _, err := s.db.Exec(`UPDATE invoices SET created_at=?,updated_at=? WHERE id=?`, created.Format(time.RFC3339), saved.Format(time.RFC3339), id); err != nil {
		t.Fatal(err)
	}
	logAt(t, s, chatA, received, Event{Kind: EventPhoto})
	m := lastWeek(t, s)
	if m.MedianToSave != 5*time.Second {
		t.Fatalf("tiempo desde borrador = %v", m.MedianToSave)
	}
}
