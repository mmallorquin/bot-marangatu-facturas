package marangatu

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/mmallorquin/bot-marangatu-facturas/internal/invoice"
)

var settings = Settings{RUC: "80024627-6", ImputeIVA: true, ImputeIRP: true}

func factura(number string) invoice.Invoice {
	return invoice.Invoice{
		IsInvoice: true, Type: invoice.TypeFactura, IssuerRUC: "80000519-8", IssuerName: "Comercial Ejemplo S.A.",
		Timbrado: "12345678", Number: number, Date: "2026-09-20", Condition: invoice.ConditionCash,
		Currency: invoice.CurrencyPYG, Exempt: 5_000, Taxed5: 21_000, Taxed10: 150_000, VAT5: 1_000, VAT10: 13_636, Total: 176_000,
	}
}

// unzip devuelve el nombre y el contenido del único archivo del ZIP.
func unzip(t *testing.T, data []byte) (string, string) {
	t.Helper()
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("ZIP inválido: %v", err)
	}
	if len(r.File) != 1 {
		t.Fatalf("el ZIP debe tener un solo archivo, tiene %d", len(r.File))
	}
	f, _ := r.File[0].Open()
	defer f.Close()
	content, _ := io.ReadAll(f)
	return r.File[0].Name, string(content)
}

func TestBuildPurchasesFollowsTheOfficialFormat(t *testing.T) {
	// Act
	export, err := BuildPurchases([]invoice.Invoice{factura("001-001-0001234")}, settings, "2026-09", "V0001")

	// Assert
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if export.FileName != "80024627_REG_092026_V0001.zip" {
		t.Errorf("nombre = %q", export.FileName)
	}
	name, content := unzip(t, export.Zip)
	if name != "80024627_REG_092026_V0001.txt" {
		t.Errorf("archivo dentro del ZIP = %q", name)
	}
	// 20 campos separados por tabulación, en el orden de la especificación de la DNIT.
	want := strings.Join([]string{
		"2", "11", "80000519", "Comercial Ejemplo S.A.", "109", "20/09/2026", "12345678", "001-001-0001234",
		"150000", "21000", "5000", "176000", "1", "N", "S", "N", "S", "N", "", "",
	}, "\t") + "\r\n"
	if content != want {
		t.Errorf("contenido:\n%q\nse esperaba:\n%q", content, want)
	}
	if export.Rows != 1 || len(export.Skipped) != 0 {
		t.Errorf("filas = %d, omitidas = %+v", export.Rows, export.Skipped)
	}
}

func TestBuildPurchasesMapsConditionAndImputations(t *testing.T) {
	inv := factura("001-001-0000001")
	inv.Condition = invoice.ConditionCredit
	s := Settings{RUC: "80024627-6", ImputeIRE: true}

	export, err := BuildPurchases([]invoice.Invoice{inv}, s, "2026-09", "V0001")
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}

	_, content := unzip(t, export.Zip)
	fields := strings.Split(strings.TrimSuffix(content, "\r\n"), "\t")
	got := strings.Join(fields[12:18], ",")
	if got != "2,N,N,S,N,N" {
		t.Errorf("condición e imputaciones = %s, se esperaba 2,N,N,S,N,N", got)
	}
}

func TestBuildPurchasesRegistersTicketsWithTotalOnly(t *testing.T) {
	ticket := factura("")
	ticket.Type = invoice.TypeTicket

	export, err := BuildPurchases([]invoice.Invoice{ticket}, settings, "2026-09", "V0001")
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}

	_, content := unzip(t, export.Zip)
	fields := strings.Split(strings.TrimSuffix(content, "\r\n"), "\t")
	if fields[4] != "112" || strings.Join(fields[8:12], ",") != "0,0,0,176000" {
		t.Errorf("ticket = %v", fields)
	}
}

func TestBuildPurchasesSkipsWhatCannotBeImported(t *testing.T) {
	// Arrange
	electronic := factura("001-001-0000002")
	electronic.CDC = "01800005198001001000123412026092012345678901"
	creditNote := factura("001-001-0000003")
	creditNote.Type = invoice.TypeNotaCredito
	old := factura("001-001-0000004")
	old.Date = "2020-12-31"
	oldCredit := factura("001-001-0000005")
	oldCredit.Date = "2020-12-31"
	oldCredit.Condition = invoice.ConditionCredit

	invoices := []invoice.Invoice{factura("001-001-0000001"), electronic, creditNote, old, oldCredit}

	// Act
	export, err := BuildPurchases(invoices, settings, "2026-09", "V0001")

	// Assert
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if export.Rows != 2 {
		t.Errorf("filas = %d, se esperaban 2 (la factura común y la de crédito vieja)", export.Rows)
	}
	reasons := map[string]string{}
	for _, s := range export.Skipped {
		reasons[s.Number] = s.Reason
	}
	if reasons["001-001-0000002"] != ReasonElectronic || reasons["001-001-0000003"] != ReasonUnsupportedType ||
		reasons["001-001-0000004"] != ReasonTooOld {
		t.Errorf("omitidas = %+v", export.Skipped)
	}
}

func TestBuildPurchasesRemovesTabsAndNewlinesFromNames(t *testing.T) {
	inv := factura("001-001-0000001")
	inv.IssuerName = "Comercial\tEjemplo\nS.A."

	export, _ := BuildPurchases([]invoice.Invoice{inv}, settings, "2026-09", "V0001")

	_, content := unzip(t, export.Zip)
	if fields := strings.Split(strings.TrimSuffix(content, "\r\n"), "\t"); len(fields) != fieldCount || fields[3] != "Comercial Ejemplo S.A." {
		t.Errorf("campos = %q", fields)
	}
}

func TestBuildPurchasesErrors(t *testing.T) {
	electronic := factura("001-001-0000002")
	electronic.CDC = "01800005198001001000123412026092012345678901"

	cases := map[string]struct {
		invoices []invoice.Invoice
		settings Settings
		period   string
		fileID   string
		want     error
	}{
		"sin RUC":             {[]invoice.Invoice{factura("1")}, Settings{ImputeIVA: true}, "2026-09", "V0001", ErrInvalidSettings},
		"RUC inválido":        {[]invoice.Invoice{factura("1")}, Settings{RUC: "80024627-1", ImputeIVA: true}, "2026-09", "V0001", ErrInvalidSettings},
		"sin imputaciones":    {[]invoice.Invoice{factura("1")}, Settings{RUC: "80024627-6"}, "2026-09", "V0001", ErrInvalidSettings},
		"período inválido":    {[]invoice.Invoice{factura("1")}, settings, "septiembre", "V0001", ErrInvalidPeriod},
		"identificador largo": {[]invoice.Invoice{factura("1")}, settings, "2026-09", "V00001", ErrInvalidFileID},
		"nada para exportar":  {[]invoice.Invoice{electronic}, settings, "2026-09", "V0001", ErrNothingToExport},
		"sin facturas":        {nil, settings, "2026-09", "V0001", ErrNothingToExport},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := BuildPurchases(tc.invoices, tc.settings, tc.period, tc.fileID)

			if !errors.Is(err, tc.want) {
				t.Errorf("error = %v, se esperaba %v", err, tc.want)
			}
		})
	}
}

func TestBuildPurchasesRejectsMoreRowsThanAllowed(t *testing.T) {
	invoices := make([]invoice.Invoice, maxRows+1)
	for i := range invoices {
		invoices[i] = factura("001-001-0000001")
	}

	_, err := BuildPurchases(invoices, settings, "2026-09", "V0001")

	if !errors.Is(err, ErrTooManyRows) {
		t.Errorf("error = %v, se esperaba ErrTooManyRows", err)
	}
}

func TestFileID(t *testing.T) {
	cases := map[int]string{1: "V0001", 42: "V0042", 9999: "V9999"}
	for seq, want := range cases {
		if got := FileID(seq); got != want {
			t.Errorf("FileID(%d) = %q, se esperaba %q", seq, got, want)
		}
	}
}
