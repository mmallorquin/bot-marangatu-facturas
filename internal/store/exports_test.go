package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestExportRetryReusesVersionAndDeliverySurvivesRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "facturas.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CreateExportRequest(ctx, chatA, "2026-09", "first-preview"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		seq, delivered, err := s.ReserveExport(ctx, chatA, "2026-09", "first-preview")
		if err != nil || seq != 1 || delivered {
			t.Fatalf("retry of undelivered request = %d, %t, %v", seq, delivered, err)
		}
	}
	if err := s.MarkExportDelivered(ctx, chatA, "2026-09", "first-preview"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if seq, delivered, err := s.ReserveExport(ctx, chatA, "2026-09", "first-preview"); err != nil || seq != 1 || !delivered {
		t.Fatalf("delivered request after restart = %d, %t, %v", seq, delivered, err)
	}
	if err := s.CreateExportRequest(ctx, chatA, "2026-09", "new-preview"); err != nil {
		t.Fatal(err)
	}
	if seq, delivered, err := s.ReserveExport(ctx, chatA, "2026-09", "new-preview"); err != nil || seq != 2 || delivered {
		t.Fatalf("an intentional new confirmation = %d, %t, %v", seq, delivered, err)
	}
}

func TestCancelledOrDeletedExportRequestCannotReserveVersion(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	if err := s.CreateExportRequest(ctx, chatA, "2026-09", "cancelled"); err != nil {
		t.Fatal(err)
	}
	if err := s.CancelExport(ctx, chatA, "2026-09", "cancelled"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ReserveExport(ctx, chatA, "2026-09", "cancelled"); !errors.Is(err, ErrExportCancelled) {
		t.Fatalf("cancelled request = %v", err)
	}
	if err := s.CreateExportRequest(ctx, chatA, "2026-09", "deleted"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteChat(ctx, chatA); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ReserveExport(ctx, chatA, "2026-09", "deleted"); !errors.Is(err, ErrExportExpired) {
		t.Fatalf("pre-delete request = %v", err)
	}
}
