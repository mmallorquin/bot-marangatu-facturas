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
	numberParts     = regexp.MustCompile(`^(\d{1,3})-(\d{1,3})-(\d+)$`)
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
		edited.IssuerRUC = NormalizeRUC(raw)
	case FieldTimbrado:
		edited.Timbrado = whitespaceNoise.Replace(raw)
	case FieldNumber:
		edited.Number = NormalizeNumber(raw)
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

// NormalizeRUC pasa a mayúsculas y agrega el guion antes del dígito verificador si falta.
func NormalizeRUC(raw string) string {
	ruc := strings.ToUpper(whitespaceNoise.Replace(raw))
	if !strings.Contains(ruc, "-") && len(ruc) >= 2 {
		ruc = ruc[:len(ruc)-1] + "-" + ruc[len(ruc)-1:]
	}
	return ruc
}

// NormalizeNumber completa con ceros y elimina solo ceros sobrantes del correlativo.
// Si quedan más de 7 dígitos significativos, conserva el número para su revisión.
func NormalizeNumber(raw string) string {
	number := whitespaceNoise.Replace(strings.TrimSpace(raw))
	parts := numberParts.FindStringSubmatch(number)
	if parts == nil {
		return number
	}
	serial := strings.TrimLeft(parts[3], "0")
	if len(serial) > 7 {
		return number
	}
	return fmt.Sprintf("%03s-%03s-%07s", parts[1], parts[2], serial)
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

// parseAmount acepta montos enteros en PYG, sin separador o con grupos de miles
// de tres dígitos. Los puntos y las comas solo se aceptan como separadores de
// miles; nunca se interpreta ni se trunca una parte decimal.
func parseAmount(raw string) (int64, error) {
	invalid := func() (int64, error) {
		return 0, fmt.Errorf("el monto %q no es válido: escribí un entero en guaraníes, por ejemplo 150000 o 150.000; no se aceptan decimales ni agrupaciones incorrectas", raw)
	}
	value := strings.TrimSpace(raw)
	if value == "" {
		return invalid()
	}

	prefixLength := currencyPrefixLength(value)
	hasCurrency := prefixLength > 0
	if hasCurrency {
		value = value[prefixLength:]
		if strings.HasPrefix(value, " ") {
			value = value[1:]
		}
	}

	suffixLength := currencySuffixLength(value)
	if suffixLength > 0 {
		if hasCurrency {
			return invalid()
		}
		hasCurrency = true
		value = value[:len(value)-suffixLength]
		if strings.HasSuffix(value, " ") {
			value = value[:len(value)-1]
		}
	}
	if hasCurrency && (strings.HasPrefix(value, " ") || strings.HasSuffix(value, " ")) {
		return invalid()
	}

	digits := value
	if strings.ContainsAny(value, ".,") {
		separator := value[strings.IndexAny(value, ".,")]
		groups := strings.Split(value, string(separator))
		if len(groups) < 2 || len(groups[0]) < 1 || len(groups[0]) > 3 || !onlyDigits.MatchString(groups[0]) {
			return invalid()
		}
		for _, group := range groups[1:] {
			if len(group) != 3 || !onlyDigits.MatchString(group) {
				return invalid()
			}
		}
		digits = strings.Join(groups, "")
	} else if !onlyDigits.MatchString(digits) {
		return invalid()
	}

	amount, err := strconv.ParseInt(digits, 10, 64)
	if err != nil {
		if errors.Is(err, strconv.ErrRange) {
			return 0, fmt.Errorf("el monto %q supera el máximo permitido para guaraníes enteros", raw)
		}
		return invalid()
	}
	return amount, nil
}

func currencyPrefixLength(value string) int {
	if strings.HasPrefix(value, "₲") {
		return len("₲")
	}
	lower := strings.ToLower(value)
	if strings.HasPrefix(lower, "gs.") {
		return len("gs.")
	}
	if strings.HasPrefix(lower, "gs") {
		return len("gs")
	}
	return 0
}

func currencySuffixLength(value string) int {
	if strings.HasSuffix(value, "₲") {
		return len("₲")
	}
	lower := strings.ToLower(value)
	if strings.HasSuffix(lower, "gs.") {
		return len("gs.")
	}
	if strings.HasSuffix(lower, "gs") {
		return len("gs")
	}
	return 0
}
