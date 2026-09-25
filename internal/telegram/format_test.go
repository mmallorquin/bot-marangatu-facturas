package telegram

import (
	"strings"
	"testing"

	"github.com/mmallorquin/bot-marangatu-facturas/internal/invoice"
	"github.com/mmallorquin/bot-marangatu-facturas/internal/reader"
)

func sampleInvoice() invoice.Invoice {
	return invoice.Invoice{
		IsInvoice:  true,
		Type:       invoice.TypeFactura,
		IssuerRUC:  "80000519-8",
		IssuerName: "Comercial Ejemplo S.A.",
		Timbrado:   "12345678",
		Number:     "001-001-0001234",
		Date:       "2026-09-20",
		Condition:  invoice.ConditionCash,
		Currency:   invoice.CurrencyPYG,
		Taxed10:    150_000,
		VAT10:      13_636,
		Total:      150_000,
	}
}

func TestFormatInvoiceShowsDataInParaguayanFormat(t *testing.T) {
	// Act
	text := FormatInvoice(reader.Result{Invoice: sampleInvoice()}, nil)

	// Assert
	for _, want := range []string{
		"Comercial Ejemplo S.A.", "80000519-8", "001-001-0001234", "12345678",
		"20/09/2026", "Contado", "Gravada 10 %: 150.000", "IVA 10 %: 13.636", "Total: 150.000 Gs", "✅",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("falta %q en:\n%s", want, text)
		}
	}
	if strings.Contains(text, "Exentas") || strings.Contains(text, "5 %") {
		t.Errorf("no debería mostrar columnas en cero:\n%s", text)
	}
}

func TestFormatInvoiceListsIssuesAndUncertainFields(t *testing.T) {
	// Arrange
	inv := sampleInvoice()
	inv.UncertainFields = []string{invoice.FieldTimbrado}
	issues := []invoice.Issue{{Field: invoice.FieldTotal, Message: "el total no suma"}}

	// Act
	text := FormatInvoice(reader.Result{Invoice: inv}, issues)

	// Assert
	for _, want := range []string{"⚠️", "el total no suma", "timbrado"} {
		if !strings.Contains(text, want) {
			t.Errorf("falta %q en:\n%s", want, text)
		}
	}
	if strings.Contains(text, "✅") {
		t.Errorf("no debería mostrar ✅ si hay problemas:\n%s", text)
	}
}

func TestFormatInvoiceWhenImageIsNotAnInvoice(t *testing.T) {
	text := FormatInvoice(reader.Result{Invoice: invoice.Invoice{IsInvoice: false}}, nil)

	if text != NotInvoiceMessage {
		t.Errorf("FormatInvoice() = %q, se esperaba NotInvoiceMessage", text)
	}
}

func TestFormatGuaranies(t *testing.T) {
	cases := map[int64]string{0: "0", 999: "999", 1000: "1.000", 150000: "150.000", 1234567: "1.234.567", -5000: "-5.000"}

	for n, want := range cases {
		if got := formatGs(n); got != want {
			t.Errorf("formatGs(%d) = %q, se esperaba %q", n, got, want)
		}
	}
}

func TestFormatInvoiceSuggestsRetakingWhenThereAreManyProblems(t *testing.T) {
	// Arrange: 2 problemas de validación + 1 campo dudoso = 3
	inv := sampleInvoice()
	inv.UncertainFields = []string{invoice.FieldDate}
	issues := []invoice.Issue{{Field: invoice.FieldTotal, Message: "a"}, {Field: invoice.FieldVAT10, Message: "b"}}

	// Act
	text := FormatInvoice(reader.Result{Invoice: inv}, issues)

	// Assert
	if !strings.Contains(text, RetakeTip) {
		t.Errorf("se esperaba el consejo de sacar otra foto:\n%s", text)
	}
}

func TestFormatInvoiceDoesNotSuggestRetakingForFewProblems(t *testing.T) {
	issues := []invoice.Issue{{Field: invoice.FieldTotal, Message: "a"}}

	text := FormatInvoice(reader.Result{Invoice: sampleInvoice()}, issues)

	if strings.Contains(text, RetakeTip) {
		t.Errorf("con un solo problema no hace falta otra foto:\n%s", text)
	}
}
