package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mmallorquin/bot-marangatu-facturas/internal/invoice"
	"github.com/mmallorquin/bot-marangatu-facturas/internal/store"
)

// captureStdout devuelve lo que fn imprime.
func captureStdout(t *testing.T, fn func() error) string {
	t.Helper()
	r, w, _ := os.Pipe()
	stdout := os.Stdout
	os.Stdout = w
	err := fn()
	os.Stdout = stdout
	_ = w.Close()
	out, _ := io.ReadAll(r)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return string(out)
}

func TestReportReadsTheDatabaseWithoutInvoiceData(t *testing.T) {
	// Arrange
	path := filepath.Join(t.TempDir(), "facturas.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	inv := invoice.Invoice{
		IsInvoice: true, Type: invoice.TypeFactura, IssuerRUC: "80000519-8", IssuerName: "Comercial Secreta S.A.",
		Timbrado: "12345678", Number: "001-001-0000001", Date: "2026-09-20", Condition: invoice.ConditionCash,
		Currency: invoice.CurrencyPYG, Taxed10: 110_000, VAT10: 10_000, Total: 110_000,
	}
	id, _ := db.CreateDraft(ctx, 42, store.Draft{Invoice: inv, Model: "m", CostUSD: 0.001})
	_ = db.Save(ctx, 42, id)
	for _, e := range []store.Event{
		{Kind: store.EventPhoto},
		{Kind: store.EventRead, Detail: store.EventDetailOK, Seconds: 3, CostUSD: 0.001},
		{Kind: store.EventSaved, Detail: store.EventDetailClean},
	} {
		_ = db.LogEvent(ctx, 42, e)
	}
	_ = db.LogEvent(ctx, 99, store.Event{Kind: store.EventPhoto})
	_ = db.Close()

	// Act
	out := captureStdout(t, func() error {
		return run([]string{"-db", path, "-excluir", "99", "-usuarios"}, time.Now())
	})

	// Assert
	for _, want := range []string{"últimos 7 días", "sin 1 chat excluidos", "Activos:", "1 (1 nuevos", "Guardadas sin corregir:", "(100 %)", "42 "} {
		if !strings.Contains(out, want) {
			t.Errorf("falta %q en el reporte:\n%s", want, out)
		}
	}
	for _, secret := range []string{"Comercial Secreta", "80000519", "110.000", "110000"} {
		if strings.Contains(out, secret) {
			t.Errorf("el reporte no debe incluir datos de facturas (%q):\n%s", secret, out)
		}
	}
}

func TestRunRejectsBadFlags(t *testing.T) {
	if err := run([]string{"-excluir", "marco"}, time.Now()); err == nil {
		t.Error("debería rechazar un chat_id inválido")
	}
	if err := run([]string{"-dias", "-1"}, time.Now()); err == nil {
		t.Error("debería rechazar días negativos")
	}
	if err := run([]string{"-db", filepath.Join(t.TempDir(), "no.db")}, time.Now()); err == nil {
		t.Error("debería fallar si la base no existe")
	}
}
