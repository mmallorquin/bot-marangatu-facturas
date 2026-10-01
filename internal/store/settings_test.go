package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
)

func TestSettingsStartEmptyAndArePerChat(t *testing.T) {
	// Arrange
	s := openTestStore(t)
	ctx := context.Background()

	// Act
	empty, err := s.Settings(ctx, chatA)
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	if err := s.SetRUC(ctx, chatA, "80024627-6"); err != nil {
		t.Fatalf("SetRUC: %v", err)
	}
	if err := s.SetImputations(ctx, chatA, Imputations{IVA: true, IRP: true}); err != nil {
		t.Fatalf("SetImputations: %v", err)
	}

	// Assert
	if empty != (ChatSettings{Reminders: true}) {
		t.Errorf("configuración inicial = %+v, se esperaba la de por defecto", empty)
	}
	got, _ := s.Settings(ctx, chatA)
	want := ChatSettings{RUC: "80024627-6", Imputations: Imputations{IVA: true, IRP: true}, Reminders: true}
	if got != want {
		t.Errorf("configuración = %+v, se esperaba %+v", got, want)
	}
	if other, _ := s.Settings(ctx, chatB); other != (ChatSettings{Reminders: true}) {
		t.Errorf("el chat B no debería tener configuración: %+v", other)
	}
}

func TestRegistrationPersistsPerChatAndPreservesData(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "facturas.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if err := s.SetRUC(ctx, chatA, "80024627-6"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetImputations(ctx, chatA, Imputations{IRP: true}); err != nil {
		t.Fatal(err)
	}
	id, err := s.CreateDraft(ctx, chatA, newDraft(sampleInvoice("001-001-0000001", 110_000)))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Save(ctx, chatA, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.NextExportSeq(ctx, chatA, "2026"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRegistration(ctx, chatA, "80024627-6", RegistrationAnnual); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s = reopened
	settings, err := s.Settings(ctx, chatA)
	if err != nil || settings.Registration != RegistrationAnnual || settings.RUC != "80024627-6" || !settings.Imputations.IRP {
		t.Fatalf("registro persistido: %+v, %v", settings, err)
	}
	other, err := s.Settings(ctx, chatB)
	if err != nil || other.Registration != "" {
		t.Errorf("la selección no puede pasar a otro chat: %+v, %v", other, err)
	}
	if invoices, err := s.SavedInvoices(ctx, chatA, "2026"); err != nil || len(invoices) != 1 || invoices[0].Total != 110_000 {
		t.Errorf("facturas preservadas: %+v, %v", invoices, err)
	}
	if seq, err := s.NextExportSeq(ctx, chatA, "2026"); err != nil || seq != 2 {
		t.Errorf("exportaciones preservadas: %d, %v", seq, err)
	}
}

func TestRegistrationStorageRejectsStaleRUCEvenWithoutHandler(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	if err := s.SetRUC(ctx, chatA, "80024627-6"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRegistration(ctx, chatA, "80024627-6", RegistrationAnnual); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRUC(ctx, chatA, "80000519-8"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRegistration(ctx, chatA, "80024627-6", RegistrationMonthly); !errors.Is(err, ErrRegistrationRUCChanged) {
		t.Errorf("un RUC viejo debe rechazarse en la base: %v", err)
	}
	if err := s.SetRegistration(ctx, chatA, "80000519-8", Registration("957")); err == nil {
		t.Error("una obligación no soportada debe rechazarse en la base")
	}
	if cs, err := s.Settings(ctx, chatA); err != nil || cs.Registration != "" {
		t.Errorf("no se debe restaurar una selección vieja o inválida: %+v, %v", cs, err)
	}
}

func TestSetRUCKeepsImputationsAndViceVersa(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	_ = s.SetImputations(ctx, chatA, Imputations{IRE: true})
	_ = s.SetRUC(ctx, chatA, "80024627-6")
	_ = s.SetRUC(ctx, chatA, "80000519-8")

	got, _ := s.Settings(ctx, chatA)
	if got.RUC != "80000519-8" || got.Imputations != (Imputations{IRE: true}) {
		t.Errorf("configuración = %+v", got)
	}
}

func TestSavedInvoicesReturnsOnlySavedOfThatMonthInOrder(t *testing.T) {
	// Arrange
	s := openTestStore(t)
	ctx := context.Background()
	for _, number := range []string{"001-001-0000001", "001-001-0000002"} {
		id, _ := s.CreateDraft(ctx, chatA, newDraft(sampleInvoice(number, 110_000)))
		_ = s.Save(ctx, chatA, id)
	}
	_, _ = s.CreateDraft(ctx, chatA, newDraft(sampleInvoice("001-001-0000003", 110_000))) // borrador

	// Act
	invoices, err := s.SavedInvoices(ctx, chatA, "2026-09")

	// Assert
	if err != nil {
		t.Fatalf("SavedInvoices: %v", err)
	}
	if len(invoices) != 2 || invoices[0].Number != "001-001-0000001" || invoices[1].Number != "001-001-0000002" {
		t.Errorf("facturas = %+v", invoices)
	}
	if none, _ := s.SavedInvoices(ctx, chatB, "2026-09"); len(none) != 0 {
		t.Errorf("el chat B no debería tener facturas: %+v", none)
	}
}

func TestNextExportSeqCountsPerChatAndPeriod(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	first, _ := s.NextExportSeq(ctx, chatA, "2026-09")
	second, _ := s.NextExportSeq(ctx, chatA, "2026-09")
	otherMonth, _ := s.NextExportSeq(ctx, chatA, "2026-08")
	otherChat, err := s.NextExportSeq(ctx, chatB, "2026-09")

	if err != nil || first != 1 || second != 2 || otherMonth != 1 || otherChat != 1 {
		t.Errorf("secuencias = %d, %d, %d, %d (err %v)", first, second, otherMonth, otherChat, err)
	}
}

func TestSavedInvoicesForAYearIncludesEveryMonthOfThatYear(t *testing.T) {
	// Arrange
	s := openTestStore(t)
	ctx := context.Background()
	for i, date := range []string{"2026-09-20", "2025-12-31", "2026-01-05", "2027-01-01"} {
		inv := sampleInvoice(fmt.Sprintf("001-001-%07d", i+1), 110_000)
		inv.Date = date
		id, _ := s.CreateDraft(ctx, chatA, newDraft(inv))
		if err := s.Save(ctx, chatA, id); err != nil {
			t.Fatal(err)
		}
	}

	// Act
	year, err := s.SavedInvoices(ctx, chatA, "2026")
	month, _ := s.SavedInvoices(ctx, chatA, "2026-09")

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if len(year) != 2 || year[0].Date != "2026-01-05" || year[1].Date != "2026-09-20" {
		t.Errorf("año 2026 = %+v", year)
	}
	if len(month) != 1 {
		t.Errorf("septiembre = %d facturas", len(month))
	}
}

func TestFlagsArePerChatAndKeepTheRest(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	_ = s.SetRUC(ctx, chatA, "80024627-6")

	_ = s.SetAutoSave(ctx, chatA, true)
	_ = s.SetReminders(ctx, chatA, false)
	_ = s.SetAwaitingRUC(ctx, chatA, true)

	got, _ := s.Settings(ctx, chatA)
	if got.RUC != "80024627-6" || !got.AutoSave || got.Reminders || !got.AwaitingRUC {
		t.Errorf("configuración = %+v", got)
	}
	if other, _ := s.Settings(ctx, chatB); other.AutoSave || !other.Reminders {
		t.Errorf("el chat B mantiene los valores por defecto: %+v", other)
	}
}

func TestOpenMigratesADatabaseCreatedBeforeTheNewColumns(t *testing.T) {
	// Arrange: una base con chat_settings como era antes.
	path := filepath.Join(t.TempDir(), "vieja.db")
	old, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = old.Exec(`CREATE TABLE chat_settings (chat_id INTEGER PRIMARY KEY, ruc TEXT NOT NULL DEFAULT '',
		impute_iva INTEGER NOT NULL DEFAULT 0, impute_ire INTEGER NOT NULL DEFAULT 0, impute_irp INTEGER NOT NULL DEFAULT 0);
		INSERT INTO chat_settings (chat_id, ruc, impute_iva) VALUES (111, '80024627-6', 1);`)
	_ = old.Close()
	if err != nil {
		t.Fatal(err)
	}

	// Act
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	// Assert: los datos siguen y las columnas nuevas tienen su valor por defecto.
	got, err := s.Settings(context.Background(), chatA)
	if err != nil || got.RUC != "80024627-6" || !got.Imputations.IVA || got.AutoSave || !got.Reminders {
		t.Errorf("configuración migrada = %+v, error = %v", got, err)
	}
	var registration string
	if err := s.db.QueryRow(`SELECT registration FROM chat_settings WHERE chat_id = 111`).Scan(&registration); err != nil || registration != "" {
		t.Errorf("la migración debe dejar el registro sin elegir, no deducirlo: %q, %v", registration, err)
	}
}
