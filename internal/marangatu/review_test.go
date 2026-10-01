package marangatu

import (
	"bytes"
	"encoding/csv"
	"testing"

	"github.com/xuri/excelize/v2"

	"github.com/mmallorquin/bot-marangatu-facturas/internal/invoice"
)

var wantReviewHeaders = []string{
	"Tipo de registro", "Tipo de identificación", "RUC del proveedor", "Razón social",
	"Tipo de comprobante", "Fecha", "Timbrado", "Número", "Gravado 10%", "Gravado 5%",
	"Exento", "Total", "Condición", "Moneda extranjera", "Imputa IVA", "Imputa IRE",
	"Imputa IRP-RSP", "No imputa", "Comprobante asociado", "Timbrado asociado",
}

func TestPreviewPurchasesUsesTheSameRowsAsTheOfficialExport(t *testing.T) {
	electronic := factura("001-001-0000002")
	electronic.CDC = "01800005198001001000123412026092012345678901"

	preview, err := PreviewPurchases([]invoice.Invoice{factura("001-001-0000001"), electronic}, settings, "2026-09")

	if err != nil {
		t.Fatalf("PreviewPurchases: %v", err)
	}
	if len(preview.Invoices) != 1 || preview.Total != 176_000 {
		t.Errorf("facturas = %d, total = %d", len(preview.Invoices), preview.Total)
	}
	if len(preview.Skipped) != 1 || preview.Skipped[0].Reason != ReasonElectronic {
		t.Errorf("omitidas = %+v", preview.Skipped)
	}
}

func TestReviewCSVShowsTheExactImportFieldsWithHeaders(t *testing.T) {
	inv := factura("001-001-0001234")
	inv.IssuerName = "Comercial Ejemplo, S.A."
	preview, err := PreviewPurchases([]invoice.Invoice{inv}, settings, "2026-09")
	if err != nil {
		t.Fatalf("PreviewPurchases: %v", err)
	}

	file, err := preview.CSV()
	if err != nil {
		t.Fatalf("CSV: %v", err)
	}
	if file.FileName != "80024627_PREVIA_092026.csv" {
		t.Errorf("nombre = %q", file.FileName)
	}
	records, err := csv.NewReader(bytes.NewReader(file.Data)).ReadAll()
	if err != nil {
		t.Fatalf("CSV inválido: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("filas = %d", len(records))
	}
	if got := records[0]; len(got) != 20 {
		t.Fatalf("encabezados = %d", len(got))
	} else {
		for i, want := range wantReviewHeaders {
			if got[i] != want {
				t.Errorf("encabezado %d = %q, se esperaba %q", i+1, got[i], want)
			}
		}
	}
	want := []string{
		"2", "11", "80000519", "Comercial Ejemplo, S.A.", "109", "20/09/2026", "12345678", "001-001-0001234",
		"150000", "21000", "5000", "176000", "1", "N", "S", "N", "S", "N", "", "",
	}
	for i := range want {
		if records[1][i] != want[i] {
			t.Errorf("campo %d = %q, se esperaba %q", i+1, records[1][i], want[i])
		}
	}
}

func TestReviewXLSXShowsTheSameFieldsAsCSV(t *testing.T) {
	preview, err := PreviewPurchases([]invoice.Invoice{factura("001-001-0001234")}, settings, "2026-09")
	if err != nil {
		t.Fatalf("PreviewPurchases: %v", err)
	}

	file, err := preview.XLSX()
	if err != nil {
		t.Fatalf("XLSX: %v", err)
	}
	if file.FileName != "80024627_PREVIA_092026.xlsx" {
		t.Errorf("nombre = %q", file.FileName)
	}
	book, err := excelize.OpenReader(bytes.NewReader(file.Data))
	if err != nil {
		t.Fatalf("XLSX inválido: %v", err)
	}
	defer book.Close()
	rows, err := book.GetRows("Comprobantes")
	if err != nil {
		t.Fatalf("leyendo XLSX: %v", err)
	}
	if len(rows) != 2 || len(rows[0]) != 20 || len(rows[1]) < 18 {
		t.Fatalf("dimensiones = %d filas; encabezados=%d, datos visibles=%d", len(rows), len(rows[0]), len(rows[1]))
	}
	if rows[0][3] != "Razón social" || rows[1][3] != "Comercial Ejemplo S.A." || rows[1][11] != "176000" {
		t.Errorf("contenido XLSX = %+v", rows)
	}
	for _, cell := range []string{"S2", "T2"} {
		if value, err := book.GetCellValue("Comprobantes", cell); err != nil || value != "" {
			t.Errorf("%s = %q, %v; se esperaba vacío", cell, value, err)
		}
	}
}

func TestCSVNeutralizesFormulaNamesWithoutChangingOfficialFields(t *testing.T) {
	for _, name := range []string{"=1+1", "+1+1", "-1+1", "@SUM(1)", "  =1+1"} {
		t.Run(name, func(t *testing.T) {
			inv := factura("001-001-0001234")
			inv.IssuerName = name
			preview, err := PreviewPurchases([]invoice.Invoice{inv}, settings, "2026-09")
			if err != nil {
				t.Fatal(err)
			}
			file, err := preview.CSV()
			if err != nil {
				t.Fatal(err)
			}
			rows, err := csv.NewReader(bytes.NewReader(file.Data)).ReadAll()
			if err != nil {
				t.Fatal(err)
			}
			official := purchaseFields(inv, settings)[3]
			if rows[1][3] != "'"+official {
				t.Fatalf("unsafe field %q", rows[1][3])
			}
			if purchaseFields(inv, settings)[3] != official {
				t.Fatal("official field changed")
			}
		})
	}
}
