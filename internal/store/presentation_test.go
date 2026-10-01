package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestPresentationRequiresDeliveryAndStaysIsolatedAfterRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "facturas.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	for _, chat := range []int64{chatA, chatB} {
		if err := s.CreateExportPreview(ctx, chat, "2026-09", "preview", "revision"); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.MarkUserPresented(ctx, chatA, "2026-09", "preview"); !errors.Is(err, ErrExportExpired) {
		t.Fatalf("undelivered preview cannot be presented: %v", err)
	}
	if _, _, err := s.ReserveExport(ctx, chatA, "2026-09", "preview"); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkExportDelivered(ctx, chatA, "2026-09", "preview"); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkUserPresented(ctx, chatA, "2026-09", "preview"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPendingExport(ctx, chatB, "2026-08"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	state, err := s.ExportPresentation(ctx, chatA, "2026-09")
	if err != nil || state.Revision != "revision" || state.Seq != 1 || state.ConfirmedAt == "" {
		t.Fatalf("presentation should survive restart: %+v, %v", state, err)
	}
	other, err := s.ExportPresentation(ctx, chatB, "2026-09")
	if err != nil || other.ConfirmedAt != "" {
		t.Fatalf("another chat inherited confirmation: %+v %v", other, err)
	}
	cs, err := s.Settings(ctx, chatB)
	if err != nil || cs.PendingExport != "2026-08" {
		t.Fatalf("export setup period lost: %+v, %v", cs, err)
	}
	if _, err := s.DeleteChat(ctx, chatA); err != nil {
		t.Fatal(err)
	}
	state, err = s.ExportPresentation(ctx, chatA, "2026-09")
	if err != nil || state.ConfirmedAt != "" {
		t.Fatalf("delete must clear presentation: %+v, %v", state, err)
	}
	if _, err := s.ExportDeliveredRevision(ctx, chatA, "2026-09", "preview"); !errors.Is(err, ErrExportExpired) {
		t.Fatalf("deleted presentation callback remains valid: %v", err)
	}
}

func TestLegacyAndCancelledExportsCannotBeMarkedPresented(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	if err := s.CreateExportRequest(ctx, chatA, "2026-09", "legacy"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ReserveExport(ctx, chatA, "2026-09", "legacy"); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkExportDelivered(ctx, chatA, "2026-09", "legacy"); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkUserPresented(ctx, chatA, "2026-09", "legacy"); !errors.Is(err, ErrExportExpired) {
		t.Fatalf("unbound legacy delivery must not be marked: %v", err)
	}
	if err := s.CreateExportPreview(ctx, chatA, "2026-09", "cancelled", "revision"); err != nil {
		t.Fatal(err)
	}
	if err := s.CancelExport(ctx, chatA, "2026-09", "cancelled"); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkUserPresented(ctx, chatA, "2026-09", "cancelled"); !errors.Is(err, ErrExportExpired) {
		t.Fatalf("cancelled delivery must not be marked: %v", err)
	}
}
