package invoice

import (
	"slices"
	"testing"
)

func issueFields(issues []Issue) []string {
	var fields []string
	for _, issue := range issues {
		fields = append(fields, issue.Field)
	}
	return fields
}

func TestInvoiceWithoutTotalNeedsReview(t *testing.T) {
	// El modelo deja en 0 lo que no pudo leer: con todo en 0 las sumas cierran igual.
	inv := validInvoice()
	inv.Exempt, inv.Taxed5, inv.Taxed10, inv.VAT5, inv.VAT10, inv.Total = 0, 0, 0, 0, 0, 0

	if fields := issueFields(Validate(inv)); !slices.Contains(fields, FieldTotal) {
		t.Errorf("una factura sin total no cierra: %v", fields)
	}
}

func TestUnreadableCDCMustBeResolvedBeforeSaving(t *testing.T) {
	// Una electrónica con el CDC ilegible llega con el CDC vacío y marcado como dudoso:
	// guardada así entraría al archivo como una factura en papel.
	inv := validInvoice()
	inv.UncertainFields = []string{FieldCDC}
	if fields := issueFields(Validate(inv)); !slices.Contains(fields, FieldCDC) {
		t.Errorf("CDC dudoso y vacío debía bloquear: %v", fields)
	}

	// El usuario resuelve con un toque o una palabra.
	resolved, err := Edit(inv, FieldCDC, "no")
	if err != nil || len(Validate(resolved)) != 0 {
		t.Errorf("al indicar que no tiene CDC la factura cierra: %v, %v", Validate(resolved), err)
	}
}

func TestMalformedCDCMustBeResolvedBeforeSaving(t *testing.T) {
	// Un CDC que no tiene 44 dígitos dejaba una factura en papel fuera del archivo por "electrónica".
	inv := validInvoice()
	inv.CDC = "123"

	if fields := issueFields(Validate(inv)); !slices.Contains(fields, FieldCDC) {
		t.Errorf("un CDC incompleto debía bloquear: %v", fields)
	}
}

func TestTicketWithOnlyTheTotalIsValid(t *testing.T) {
	// El archivo de Marangatu descarta el desglose de los tickets: no hay que inventarlo.
	inv := validInvoice()
	inv.Type = TypeTicket
	inv.Exempt, inv.Taxed5, inv.Taxed10, inv.VAT5, inv.VAT10, inv.Total = 0, 0, 0, 0, 0, 50_000

	if issues := Validate(inv); len(issues) != 0 {
		t.Errorf("ticket con solo el total: %v", issues)
	}
	inv.Type = TypeFactura
	if len(Validate(inv)) == 0 {
		t.Error("una factura sí necesita su desglose")
	}
}

func TestTypeCanBeCorrectedAndOtherAsksForReview(t *testing.T) {
	inv := validInvoice()
	inv.Type = TypeOther
	if fields := issueFields(Warnings(inv)); !slices.Contains(fields, FieldType) {
		t.Errorf("el tipo \"otro\" no entra al archivo y debía avisarse: %v", fields)
	}

	for raw, want := range map[string]string{
		"Factura": TypeFactura, " ticket ": TypeTicket, "nota de crédito": TypeNotaCredito,
		"nota de debito": TypeNotaDebito, "autofactura": TypeAutofactura,
	} {
		edited, err := Edit(inv, FieldType, raw)
		if err != nil || edited.Type != want {
			t.Errorf("Edit(tipo, %q) = %q, %v; se esperaba %q", raw, edited.Type, err, want)
		}
	}
	if _, err := Edit(inv, FieldType, "remito"); err == nil {
		t.Error("un tipo desconocido se rechaza")
	}
	if !slices.Contains(EditableFields, FieldType) {
		t.Error("el tipo debe figurar entre los campos corregibles")
	}
}

func TestCurrencyIsNormalizedToPYG(t *testing.T) {
	for _, raw := range []string{"PYG", "pyg", " Gs ", "GS.", "₲", "guaraníes", "Guaranies"} {
		if got := NormalizeCurrency(raw); got != CurrencyPYG {
			t.Errorf("NormalizeCurrency(%q) = %q", raw, got)
		}
	}
	if got := NormalizeCurrency("usd"); got != "USD" {
		t.Errorf("otra moneda se conserva en mayúsculas: %q", got)
	}
}
