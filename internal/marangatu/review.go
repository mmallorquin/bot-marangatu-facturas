package marangatu

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"strconv"

	"github.com/xuri/excelize/v2"

	"github.com/mmallorquin/bot-marangatu-facturas/internal/invoice"
)

// Preview es exactamente el conjunto de comprobantes que entrará en el ZIP.
type Preview struct {
	Period   string
	Invoices []invoice.Invoice
	Skipped  []Skipped
	Total    int64
	settings Settings
}

// ReviewFile es un archivo legible por una persona; no se importa en Marangatu.
type ReviewFile struct {
	FileName string
	Data     []byte
}

var reviewHeaders = []string{
	"Tipo de registro", "Tipo de identificación", "RUC del proveedor", "Razón social",
	"Tipo de comprobante", "Fecha", "Timbrado", "Número", "Gravado 10%", "Gravado 5%",
	"Exento", "Total", "Condición", "Moneda extranjera", "Imputa IVA", "Imputa IRE",
	"Imputa IRP-RSP", "No imputa", "Comprobante asociado", "Timbrado asociado",
}

// PreviewPurchases filtra con las mismas reglas del ZIP oficial y calcula su total.
func PreviewPurchases(invoices []invoice.Invoice, s Settings, period string) (Preview, error) {
	preview := Preview{Period: period, settings: s}
	if err := s.Validate(); err != nil {
		return preview, err
	}
	if _, err := ParsePeriod(period); err != nil {
		return preview, err
	}
	for _, inv := range invoices {
		if reason := skipReason(inv); reason != "" {
			preview.Skipped = append(preview.Skipped, Skipped{Number: inv.Number, Reason: reason})
			continue
		}
		preview.Invoices = append(preview.Invoices, inv)
		preview.Total += inv.Total
	}
	if len(preview.Invoices) == 0 {
		return preview, ErrNothingToExport
	}
	if len(preview.Invoices) > maxRows {
		return preview, fmt.Errorf("%w: %d (máximo %d)", ErrTooManyRows, len(preview.Invoices), maxRows)
	}
	return preview, nil
}

// CSV genera una tabla con encabezados y los mismos 20 campos del archivo oficial.
func (p Preview) CSV() (ReviewFile, error) {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	w.UseCRLF = true
	if err := w.Write(reviewHeaders); err != nil {
		return ReviewFile{}, fmt.Errorf("escribiendo encabezados CSV: %w", err)
	}
	for _, inv := range p.Invoices {
		if err := w.Write(purchaseFields(inv, p.settings)); err != nil {
			return ReviewFile{}, fmt.Errorf("escribiendo CSV: %w", err)
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return ReviewFile{}, fmt.Errorf("cerrando CSV: %w", err)
	}
	return ReviewFile{FileName: p.reviewBaseName() + ".csv", Data: buf.Bytes()}, nil
}

// XLSX genera la misma tabla que CSV en un libro de Excel.
func (p Preview) XLSX() (ReviewFile, error) {
	book := excelize.NewFile()
	defer book.Close()
	const sheet = "Comprobantes"
	if err := book.SetSheetName("Sheet1", sheet); err != nil {
		return ReviewFile{}, fmt.Errorf("nombrando hoja Excel: %w", err)
	}
	rows := make([][]string, 0, len(p.Invoices)+1)
	rows = append(rows, reviewHeaders)
	for _, inv := range p.Invoices {
		rows = append(rows, purchaseFields(inv, p.settings))
	}
	for rowIndex, row := range rows {
		for columnIndex, value := range row {
			cell, err := excelize.CoordinatesToCellName(columnIndex+1, rowIndex+1)
			if err != nil {
				return ReviewFile{}, fmt.Errorf("ubicando celda Excel: %w", err)
			}
			cellValue := any(value)
			if rowIndex > 0 && columnIndex >= 8 && columnIndex <= 11 {
				if number, err := strconv.ParseInt(value, 10, 64); err == nil {
					cellValue = number
				}
			}
			if err := book.SetCellValue(sheet, cell, cellValue); err != nil {
				return ReviewFile{}, fmt.Errorf("escribiendo Excel: %w", err)
			}
		}
	}
	headerStyle, err := book.NewStyle(&excelize.Style{
		Font: &excelize.Font{Bold: true, Color: "FFFFFF"},
		Fill: excelize.Fill{Type: "pattern", Color: []string{"1F4E78"}, Pattern: 1},
	})
	if err != nil {
		return ReviewFile{}, fmt.Errorf("creando estilo Excel: %w", err)
	}
	if err := book.SetCellStyle(sheet, "A1", "T1", headerStyle); err != nil {
		return ReviewFile{}, fmt.Errorf("aplicando estilo Excel: %w", err)
	}
	if err := book.SetColWidth(sheet, "A", "T", 18); err != nil {
		return ReviewFile{}, fmt.Errorf("ajustando Excel: %w", err)
	}
	data, err := book.WriteToBuffer()
	if err != nil {
		return ReviewFile{}, fmt.Errorf("generando Excel: %w", err)
	}
	return ReviewFile{FileName: p.reviewBaseName() + ".xlsx", Data: data.Bytes()}, nil
}

func (p Preview) reviewBaseName() string {
	period, _ := ParsePeriod(p.Period) // ya fue validado por PreviewPurchases
	return fmt.Sprintf("%s_PREVIA_%s", rucBase(p.settings.RUC), period.fileToken())
}
