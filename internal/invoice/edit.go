package invoice

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// FieldIssuerName es la razón social del emisor.
const FieldIssuerName = "razon_social_emisor"

// EditableFields son los campos que el usuario puede corregir, en el orden en que se muestran.
var EditableFields = []string{
	FieldIssuerName, FieldIssuerRUC, FieldTimbrado, FieldNumber, FieldDate, FieldCondition,
	FieldExempt, FieldTaxed5, FieldTaxed10, FieldVAT5, FieldVAT10, FieldTotal,
}

// ErrNotEditable indica que el campo no se puede corregir.
var ErrNotEditable = errors.New("ese campo no se puede corregir")

// Formatos de fecha que acepta al corregir (el primero es el que se guarda).
var dateInputLayouts = []string{dateLayout, "2/1/2006", "2-1-2006"}

var (
	onlyDigits      = regexp.MustCompile(`^\d+$`)
	numberParts     = regexp.MustCompile(`^(\d{1,3})-(\d{1,3})-(\d{1,7})$`)
	amountNoise     = strings.NewReplacer(".", "", ",", "", " ", "", "Gs", "", "gs", "", "GS", "", "₲", "")
	whitespaceNoise = strings.NewReplacer(" ", "", "\t", "")
)

// Edit devuelve una copia de inv con el campo corregido a partir de lo que escribió el usuario.
// No modifica inv. El campo corregido deja de figurar como dudoso.
func Edit(inv Invoice, field, raw string) (Invoice, error) {
	raw = strings.TrimSpace(raw)
	edited := inv
	edited.UncertainFields = slices.DeleteFunc(slices.Clone(inv.UncertainFields),
		func(f string) bool { return f == field })

	var err error
	switch field {
	case FieldIssuerName:
		edited.IssuerName, err = parseName(raw)
	case FieldIssuerRUC:
		edited.IssuerRUC = normalizeRUC(raw)
	case FieldTimbrado:
		edited.Timbrado = whitespaceNoise.Replace(raw)
	case FieldNumber:
		edited.Number = normalizeNumber(raw)
	case FieldDate:
		edited.Date, err = parseDate(raw)
	case FieldCondition:
		edited.Condition, err = parseCondition(raw)
	case FieldExempt:
		edited.Exempt, err = parseAmount(raw)
	case FieldTaxed5:
		edited.Taxed5, err = parseAmount(raw)
	case FieldTaxed10:
		edited.Taxed10, err = parseAmount(raw)
	case FieldVAT5:
		edited.VAT5, err = parseAmount(raw)
	case FieldVAT10:
		edited.VAT10, err = parseAmount(raw)
	case FieldTotal:
		edited.Total, err = parseAmount(raw)
	default:
		return inv, fmt.Errorf("%w: %q", ErrNotEditable, field)
	}
	if err != nil {
		return inv, err
	}
	return edited, nil
}

func parseName(raw string) (string, error) {
	if raw == "" {
		return "", errors.New("la razón social no puede quedar vacía")
	}
	return raw, nil
}

// normalizeRUC pasa a mayúsculas y agrega el guion antes del dígito verificador si falta.
func normalizeRUC(raw string) string {
	ruc := strings.ToUpper(whitespaceNoise.Replace(raw))
	if !strings.Contains(ruc, "-") && len(ruc) >= 2 {
		ruc = ruc[:len(ruc)-1] + "-" + ruc[len(ruc)-1:]
	}
	return ruc
}

// normalizeNumber completa con ceros: 1-1-1234 → 001-001-0001234.
func normalizeNumber(raw string) string {
	number := whitespaceNoise.Replace(raw)
	parts := numberParts.FindStringSubmatch(number)
	if parts == nil {
		return number
	}
	return fmt.Sprintf("%03s-%03s-%07s", parts[1], parts[2], parts[3])
}

func parseDate(raw string) (string, error) {
	for _, layout := range dateInputLayouts {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed.Format(dateLayout), nil
		}
	}
	return "", fmt.Errorf("la fecha %q no es válida: usá DD/MM/AAAA", raw)
}

func parseCondition(raw string) (string, error) {
	switch strings.ToLower(strings.ReplaceAll(raw, "é", "e")) {
	case ConditionCash:
		return ConditionCash, nil
	case ConditionCredit:
		return ConditionCredit, nil
	}
	return "", fmt.Errorf("la condición %q no es válida: escribí contado o crédito", raw)
}

// parseAmount acepta montos como "150.000", "150,000", "₲ 150000" o "150.000 Gs".
func parseAmount(raw string) (int64, error) {
	digits := amountNoise.Replace(raw)
	if !onlyDigits.MatchString(digits) {
		return 0, fmt.Errorf("el monto %q no es válido: escribí solo números, por ejemplo 150.000", raw)
	}
	return strconv.ParseInt(digits, 10, 64)
}
