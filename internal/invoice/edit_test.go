package invoice

import (
	"errors"
	"slices"
	"testing"
)

func TestEditNormalizesWhatTheUserTypes(t *testing.T) {
	cases := []struct {
		field string
		raw   string
		check func(Invoice) bool
	}{
		{FieldTotal, "150.000", func(i Invoice) bool { return i.Total == 150_000 }},
		{FieldTotal, " 1.234.567 Gs ", func(i Invoice) bool { return i.Total == 1_234_567 }},
		{FieldVAT10, "13,636", func(i Invoice) bool { return i.VAT10 == 13_636 }},
		{FieldExempt, "0", func(i Invoice) bool { return i.Exempt == 0 }},
		{FieldTaxed5, "₲ 21000", func(i Invoice) bool { return i.Taxed5 == 21_000 }},
		{FieldDate, "20/09/2026", func(i Invoice) bool { return i.Date == "2026-09-20" }},
		{FieldDate, "5/9/2026", func(i Invoice) bool { return i.Date == "2026-09-05" }},
		{FieldDate, "2026-09-20", func(i Invoice) bool { return i.Date == "2026-09-20" }},
		{FieldNumber, "1-1-1234", func(i Invoice) bool { return i.Number == "001-001-0001234" }},
		{FieldNumber, "001-002-0000456", func(i Invoice) bool { return i.Number == "001-002-0000456" }},
		{FieldIssuerRUC, "800005198", func(i Invoice) bool { return i.IssuerRUC == "80000519-8" }},
		{FieldIssuerRUC, " 3456789a-3 ", func(i Invoice) bool { return i.IssuerRUC == "3456789A-3" }},
		{FieldTimbrado, "1234 5678", func(i Invoice) bool { return i.Timbrado == "12345678" }},
		{FieldCondition, "Crédito", func(i Invoice) bool { return i.Condition == ConditionCredit }},
		{FieldCondition, "CONTADO", func(i Invoice) bool { return i.Condition == ConditionCash }},
		{FieldIssuerName, "  Comercial XYZ S.A. ", func(i Invoice) bool { return i.IssuerName == "Comercial XYZ S.A." }},
	}

	for _, tc := range cases {
		t.Run(tc.field+"="+tc.raw, func(t *testing.T) {
			// Act
			got, err := Edit(validInvoice(), tc.field, tc.raw)

			// Assert
			if err != nil {
				t.Fatalf("error inesperado: %v", err)
			}
			if !tc.check(got) {
				t.Errorf("valor no normalizado: %+v", got)
			}
		})
	}
}

func TestEditRejectsInvalidValues(t *testing.T) {
	cases := []struct {
		field string
		raw   string
	}{
		{FieldTotal, "mucho"},
		{FieldTotal, ""},
		{FieldTotal, "-5000"},
		{FieldDate, "ayer"},
		{FieldDate, "31/02/2026"},
		{FieldCondition, "fiado"},
		{FieldIssuerName, "   "},
		{"campo_inventado", "x"},
	}

	for _, tc := range cases {
		t.Run(tc.field+"="+tc.raw, func(t *testing.T) {
			if _, err := Edit(validInvoice(), tc.field, tc.raw); err == nil {
				t.Error("se esperaba un error")
			}
		})
	}
}

func TestEditUnknownFieldReturnsSentinelError(t *testing.T) {
	_, err := Edit(validInvoice(), "campo_inventado", "x")

	if !errors.Is(err, ErrNotEditable) {
		t.Errorf("error = %v, se esperaba ErrNotEditable", err)
	}
}

func TestEditRemovesFieldFromUncertainWithoutMutatingOriginal(t *testing.T) {
	// Arrange
	original := validInvoice()
	original.UncertainFields = []string{FieldTotal, FieldDate}

	// Act
	edited, err := Edit(original, FieldTotal, "180.000")

	// Assert
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if !slices.Equal(edited.UncertainFields, []string{FieldDate}) {
		t.Errorf("dudosos después de editar = %v", edited.UncertainFields)
	}
	if !slices.Equal(original.UncertainFields, []string{FieldTotal, FieldDate}) || original.Total != 176_000 {
		t.Errorf("se modificó la factura original: %+v", original)
	}
}

func TestEditableFieldsCanAllBeEdited(t *testing.T) {
	for _, field := range EditableFields {
		if _, err := Edit(validInvoice(), field, "x"); errors.Is(err, ErrNotEditable) {
			t.Errorf("%q está en EditableFields pero Edit no lo soporta", field)
		}
	}
}
