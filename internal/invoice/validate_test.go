package invoice

import (
	"reflect"
	"slices"
	"strconv"
	"testing"
)

// validInvoice es una factura correcta: 150.000 Gs gravados al 10 % y 21.000 al 5 %.
func validInvoice() Invoice {
	return Invoice{
		IsInvoice:  true,
		Type:       TypeFactura,
		IssuerRUC:  "80000519-8",
		IssuerName: "Comercial Ejemplo S.A.",
		Timbrado:   "12345678",
		Number:     "001-001-0001234",
		Date:       "2026-09-20",
		Condition:  ConditionCash,
		Currency:   CurrencyPYG,
		Exempt:     5_000,
		Taxed5:     21_000,
		Taxed10:    150_000,
		VAT5:       1_000,
		VAT10:      13_636,
		Total:      176_000,
	}
}

func fieldsWithIssues(issues []Issue) []string {
	fields := make([]string, 0, len(issues))
	for _, issue := range issues {
		fields = append(fields, issue.Field)
	}
	return fields
}

func TestValidateAcceptsAConsistentInvoice(t *testing.T) {
	issues := Validate(validInvoice())

	if len(issues) != 0 {
		t.Errorf("se esperaba sin problemas, se obtuvo %+v", issues)
	}
}

func TestValidateDetectsEachInconsistentField(t *testing.T) {
	cases := []struct {
		name   string
		modify func(inv Invoice) Invoice
		field  string
	}{
		{"RUC con dígito incorrecto", func(inv Invoice) Invoice { inv.IssuerRUC = "80000519-7"; return inv }, FieldIssuerRUC},
		{"timbrado corto", func(inv Invoice) Invoice { inv.Timbrado = "1234"; return inv }, FieldTimbrado},
		{"número sin formato", func(inv Invoice) Invoice { inv.Number = "1234"; return inv }, FieldNumber},
		{"fecha inválida", func(inv Invoice) Invoice { inv.Date = "2026-13-40"; return inv }, FieldDate},
		{"condición desconocida", func(inv Invoice) Invoice { inv.Condition = "fiado"; return inv }, FieldCondition},
		{"moneda extranjera", func(inv Invoice) Invoice { inv.Currency = "USD"; return inv }, FieldCurrency},
		{"monto negativo", func(inv Invoice) Invoice { inv.Exempt = -1; inv.Total -= 5_001; return inv }, FieldExempt},
		{"total que no suma", func(inv Invoice) Invoice { inv.Total = 180_000; return inv }, FieldTotal},
		{"IVA 10 % incorrecto", func(inv Invoice) Invoice { inv.VAT10 = 15_000; return inv }, FieldVAT10},
		{"IVA 5 % incorrecto", func(inv Invoice) Invoice { inv.VAT5 = 2_100; return inv }, FieldVAT5},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			inv := tc.modify(validInvoice())

			// Act
			issues := Validate(inv)

			// Assert
			if !slices.Contains(fieldsWithIssues(issues), tc.field) {
				t.Errorf("se esperaba un problema en %q, se obtuvo %+v", tc.field, issues)
			}
		})
	}
}

func TestValidateToleratesSmallVATRounding(t *testing.T) {
	inv := validInvoice()
	inv.VAT10 = 13_637 // 150.000 / 11 = 13.636,36 → redondeo hacia arriba

	if issues := Validate(inv); len(issues) != 0 {
		t.Errorf("el redondeo de 1 Gs no debería ser un problema: %+v", issues)
	}
}

func TestValidateAcceptsAValidCDC(t *testing.T) {
	inv := validInvoice()
	inv.CDC = "01800005198001001000123412026092012345678901"

	if issues := Validate(inv); len(issues) != 0 {
		t.Errorf("CDC de 44 dígitos debería ser válido: %+v", issues)
	}
}

func TestValidateReportsWhenImageIsNotAnInvoice(t *testing.T) {
	issues := Validate(Invoice{IsInvoice: false})

	if !slices.Equal(fieldsWithIssues(issues), []string{FieldIsInvoice}) {
		t.Errorf("se esperaba solo el problema de no-comprobante, se obtuvo %+v", issues)
	}
}

func TestValidateDoesNotMutateInput(t *testing.T) {
	inv := validInvoice()
	inv.IssuerRUC = "  80000519-8  "
	inv.UncertainFields = []string{FieldTotal}
	before := inv
	before.UncertainFields = slices.Clone(inv.UncertainFields)

	Validate(inv)

	if !reflect.DeepEqual(inv, before) {
		t.Errorf("Validate modificó la factura de entrada")
	}
}

func TestCDCWithWrongLengthIsAWarningNotABlock(t *testing.T) {
	inv := validInvoice()
	inv.CDC = "0123"

	if issues := Validate(inv); len(issues) != 0 {
		t.Errorf("un CDC mal leído no bloquea: %+v", issues)
	}
	warnings := Warnings(inv)
	if len(warnings) != 1 || warnings[0].Field != FieldCDC {
		t.Errorf("debería avisar del CDC: %+v", warnings)
	}
	if Clean(inv) {
		t.Error("con avisos no se guarda sola")
	}
}

func TestSuggestRUC(t *testing.T) {
	cases := map[string]struct {
		ruc  string
		want string
		ok   bool
	}{
		"dígito mal":       {"80024627-1", "80024627-6", true},
		"dígito bien":      {"80024627-6", "", false},
		"base con letra":   {"1234567A-0", "1234567A-" + strconv.Itoa(CheckDigit("1234567A")), true},
		"sin formato":      {"80024627", "", false},
		"con espacios":     {" 80024627-1 ", "80024627-6", true},
		"dígito ya cierra": {"80000519-8", "", false},
	}
	for name, tc := range cases {
		got, ok := SuggestRUC(tc.ruc)
		if ok != tc.ok || got != tc.want {
			t.Errorf("%s: SuggestRUC(%q) = %q, %v; se esperaba %q, %v", name, tc.ruc, got, ok, tc.want, tc.ok)
		}
	}
}
