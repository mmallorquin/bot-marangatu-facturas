package marangatu

import (
	"errors"
	"testing"

	"github.com/mmallorquin/bot-marangatu-facturas/internal/invoice"
)

func TestParsePeriod(t *testing.T) {
	for period, want := range map[string]string{"2026-09": "092026", "2026": "2026"} {
		p, err := ParsePeriod(period)
		if err != nil || p.fileToken() != want || p.Annual != (period == "2026") {
			t.Errorf("ParsePeriod(%q) = %+v, %v", period, p, err)
		}
	}
	for _, bad := range []string{"26", "2026-13", "2026-9", "09/2026", "", "20260"} {
		if _, err := ParsePeriod(bad); !errors.Is(err, ErrInvalidPeriod) {
			t.Errorf("ParsePeriod(%q) debería fallar, error = %v", bad, err)
		}
	}
}

func TestBuildPurchasesAnnualUsesTheYearInTheFileName(t *testing.T) {
	// Arrange: facturas de dos meses del mismo año.
	march := factura("001-001-0000001")
	march.Date = "2026-03-10"
	invoices := []invoice.Invoice{march, factura("001-001-0000002")}

	// Act
	export, err := BuildPurchases(invoices, settings, "2026", "V0001")
	preview, _ := PreviewPurchases(invoices, settings, "2026")
	csv, _ := preview.CSV()

	// Assert
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if export.FileName != "80024627_REG_2026_V0001.zip" || export.Rows != 2 {
		t.Errorf("nombre = %q, filas = %d", export.FileName, export.Rows)
	}
	if name, _ := unzip(t, export.Zip); name != "80024627_REG_2026_V0001.txt" {
		t.Errorf("archivo dentro del ZIP = %q", name)
	}
	if csv.FileName != "80024627_PREVIA_2026.csv" {
		t.Errorf("previa = %q", csv.FileName)
	}
}
