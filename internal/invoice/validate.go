package invoice

import (
	"fmt"
	"regexp"
	"time"
)

// Nombres de campo usados en los problemas de validación (coinciden con el JSON).
const (
	FieldIsInvoice = "es_comprobante"
	FieldIssuerRUC = "ruc_emisor"
	FieldTimbrado  = "timbrado"
	FieldNumber    = "numero"
	FieldDate      = "fecha"
	FieldCondition = "condicion"
	FieldCurrency  = "moneda"
	FieldExempt    = "exentas"
	FieldTaxed5    = "gravada_5"
	FieldTaxed10   = "gravada_10"
	FieldVAT5      = "iva_5"
	FieldVAT10     = "iva_10"
	FieldTotal     = "total"
	FieldCDC       = "cdc"
)

const (
	dateLayout = "2006-01-02"

	// En Paraguay los montos gravados incluyen IVA: IVA 10 % = gravada/11, IVA 5 % = gravada/21.
	vat10Divisor = 11
	vat5Divisor  = 21

	// Tolerancia por redondeos: 1 % del IVA esperado, y nunca menos de vatMinToleranceGs.
	vatTolerancePercent = 1
	vatMinToleranceGs   = 2

	// La suma de columnas puede diferir del total en 1 Gs por redondeo.
	totalToleranceGs = 1
)

var (
	timbradoPattern = regexp.MustCompile(`^\d{8}$`)
	numberPattern   = regexp.MustCompile(`^\d{3}-\d{3}-\d{7}$`)
	cdcPattern      = regexp.MustCompile(`^\d{44}$`)
)

// Issue es un dato que no pasó la validación.
type Issue struct {
	Field   string
	Message string
}

// Validate revisa que la factura sea coherente. No modifica la factura recibida.
// Devuelve una lista vacía si todo está bien.
func Validate(inv Invoice) []Issue {
	if !inv.IsInvoice {
		return []Issue{{FieldIsInvoice, "la imagen no parece ser una factura"}}
	}

	var issues []Issue
	issues = append(issues, validateIdentity(inv)...)
	issues = append(issues, validateAmounts(inv)...)
	return issues
}

func validateIdentity(inv Invoice) []Issue {
	var issues []Issue
	if err := ValidateRUC(inv.IssuerRUC); err != nil {
		issues = append(issues, Issue{FieldIssuerRUC, err.Error()})
	}
	if !timbradoPattern.MatchString(inv.Timbrado) {
		issues = append(issues, Issue{FieldTimbrado, "el timbrado debe tener 8 dígitos"})
	}
	if !numberPattern.MatchString(inv.Number) {
		issues = append(issues, Issue{FieldNumber, "el número debe tener el formato 001-001-0000001"})
	}
	if _, err := time.Parse(dateLayout, inv.Date); err != nil {
		issues = append(issues, Issue{FieldDate, "la fecha no es válida"})
	}
	if inv.Condition != ConditionCash && inv.Condition != ConditionCredit {
		issues = append(issues, Issue{FieldCondition, "la condición debe ser contado o crédito"})
	}
	if inv.Currency != CurrencyPYG {
		issues = append(issues, Issue{FieldCurrency, "por ahora solo se soportan facturas en guaraníes"})
	}
	if inv.CDC != "" && !cdcPattern.MatchString(inv.CDC) {
		issues = append(issues, Issue{FieldCDC, "el CDC debe tener 44 dígitos"})
	}
	return issues
}

func validateAmounts(inv Invoice) []Issue {
	var issues []Issue
	amounts := []struct {
		field string
		value int64
	}{
		{FieldExempt, inv.Exempt}, {FieldTaxed5, inv.Taxed5}, {FieldTaxed10, inv.Taxed10},
		{FieldVAT5, inv.VAT5}, {FieldVAT10, inv.VAT10}, {FieldTotal, inv.Total},
	}
	for _, a := range amounts {
		if a.value < 0 {
			issues = append(issues, Issue{a.field, "el monto no puede ser negativo"})
		}
	}

	sum := inv.Exempt + inv.Taxed5 + inv.Taxed10
	if abs(sum-inv.Total) > totalToleranceGs {
		issues = append(issues, Issue{FieldTotal,
			fmt.Sprintf("exentas + gravadas suman %d, pero el total dice %d", sum, inv.Total)})
	}
	if issue, ok := checkVAT(FieldVAT10, inv.Taxed10, inv.VAT10, vat10Divisor); !ok {
		issues = append(issues, issue)
	}
	if issue, ok := checkVAT(FieldVAT5, inv.Taxed5, inv.VAT5, vat5Divisor); !ok {
		issues = append(issues, issue)
	}
	return issues
}

func checkVAT(field string, taxed, vat, divisor int64) (Issue, bool) {
	expected := roundDiv(taxed, divisor)
	tolerance := max(expected*vatTolerancePercent/100, vatMinToleranceGs)
	if abs(vat-expected) <= tolerance {
		return Issue{}, true
	}
	return Issue{field, fmt.Sprintf("el IVA debería ser cerca de %d, pero dice %d", expected, vat)}, false
}

// roundDiv divide redondeando al entero más cercano (para montos no negativos).
func roundDiv(n, d int64) int64 {
	return (n + d/2) / d
}

func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}
