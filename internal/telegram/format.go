package telegram

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/mmallorquin/bot-marangatu-facturas/internal/invoice"
)

// Con esta cantidad de problemas conviene sacar otra foto en vez de corregir a mano.
const retakeThreshold = 3

var typeLabels = map[string]string{
	invoice.TypeFactura:     "Factura",
	invoice.TypeNotaCredito: "Nota de crédito",
	invoice.TypeNotaDebito:  "Nota de débito",
	invoice.TypeAutofactura: "Autofactura",
	invoice.TypeTicket:      "Ticket",
	invoice.TypeOther:       "Comprobante",
}

var conditionLabels = map[string]string{
	invoice.ConditionCash:   "Contado",
	invoice.ConditionCredit: "Crédito",
}

var fieldLabels = map[string]string{
	invoice.FieldIssuerRUC:  "RUC",
	invoice.FieldIssuerName: "razón social",
	invoice.FieldTimbrado:   "timbrado",
	invoice.FieldNumber:     "número",
	invoice.FieldDate:       "fecha",
	invoice.FieldCondition:  "condición",
	invoice.FieldCurrency:   "moneda",
	invoice.FieldExempt:     "exentas",
	invoice.FieldTaxed5:     "gravada 5 %",
	invoice.FieldTaxed10:    "gravada 10 %",
	invoice.FieldVAT5:       "IVA 5 %",
	invoice.FieldVAT10:      "IVA 10 %",
	invoice.FieldTotal:      "total",
	invoice.FieldCDC:        "CDC",
}

const maxIssuerDisplayUTF16 = 160

const (
	maxReviewDisplayUTF16 = 900
	maxNumberDisplayUTF16 = 64
	maxRUCDisplayUTF16    = 48
	maxTimbradoDisplay    = 32
	maxDateDisplayUTF16   = 24
	maxShortDisplayUTF16  = 32
)

// FormatInvoice arma el mensaje que el bot le muestra al usuario.
func FormatInvoice(inv invoice.Invoice, issues []invoice.Issue) string {
	if !inv.IsInvoice {
		return NotInvoiceMessage
	}

	var b strings.Builder
	b.WriteString(formatReview(inv.UncertainFields, issues))
	b.WriteString("\n\n")
	fmt.Fprintf(&b, "🧾 %s %s\n", truncateUTF16(labelOr(typeLabels, inv.Type, "Comprobante"), maxShortDisplayUTF16), truncateUTF16(inv.Number, maxNumberDisplayUTF16))
	fmt.Fprintf(&b, "Emisor: %s\n", truncateUTF16(inv.IssuerName, maxIssuerDisplayUTF16))
	fmt.Fprintf(&b, "RUC: %s · Timbrado: %s\n", truncateUTF16(inv.IssuerRUC, maxRUCDisplayUTF16), truncateUTF16(inv.Timbrado, maxTimbradoDisplay))
	fmt.Fprintf(&b, "Fecha: %s · %s\n\n", truncateUTF16(displayDate(inv.Date), maxDateDisplayUTF16), truncateUTF16(labelOr(conditionLabels, inv.Condition, inv.Condition), maxShortDisplayUTF16))
	b.WriteString(formatAmounts(inv))
	return b.String()
}

func truncateUTF16(value string, maxUnits int) string {
	units := 0
	for _, r := range value {
		units += utf16.RuneLen(r)
	}
	if units <= maxUnits {
		return value
	}
	const suffix = "…"
	remaining := maxUnits - utf16.RuneLen([]rune(suffix)[0])
	var b strings.Builder
	for _, r := range value {
		runeUnits := utf16.RuneLen(r)
		if runeUnits > remaining {
			break
		}
		b.WriteRune(r)
		remaining -= runeUnits
	}
	b.WriteString(suffix)
	return b.String()
}

func formatAmounts(inv invoice.Invoice) string {
	var b strings.Builder
	if inv.Exempt != 0 {
		fmt.Fprintf(&b, "Exentas: %s\n", formatGs(inv.Exempt))
	}
	if inv.Taxed5 != 0 || inv.VAT5 != 0 {
		fmt.Fprintf(&b, "Gravada 5 %%: %s · IVA 5 %%: %s\n", formatGs(inv.Taxed5), formatGs(inv.VAT5))
	}
	if inv.Taxed10 != 0 || inv.VAT10 != 0 {
		fmt.Fprintf(&b, "Gravada 10 %%: %s · IVA 10 %%: %s\n", formatGs(inv.Taxed10), formatGs(inv.VAT10))
	}
	currency := "Gs"
	if inv.Currency != invoice.CurrencyPYG {
		currency = truncateUTF16(inv.Currency, maxShortDisplayUTF16)
	}
	fmt.Fprintf(&b, "Total: %s %s\n", formatGs(inv.Total), currency)
	return b.String()
}

func formatReview(uncertain []string, issues []invoice.Issue) string {
	if len(uncertain) == 0 && len(issues) == 0 {
		return "✅ Los datos son coherentes."
	}

	var b strings.Builder
	b.WriteString("⚠️ Revisá:\n")
	omitted := false
	appendLine := func(line string) bool {
		separator := "• "
		if b.Len() > len("⚠️ Revisá:\n") {
			separator = "\n• "
		}
		if utf16Length(b.String()+separator+line) > maxReviewDisplayUTF16-utf16Length("\n• Hay más avisos; revisá los campos antes de guardar.") {
			return false
		}
		b.WriteString(separator)
		b.WriteString(line)
		return true
	}
	for _, issue := range issues {
		if !appendLine(truncateUTF16(issue.Message, 240)) {
			omitted = true
			break
		}
		if utf16Length(issue.Message) > 240 {
			omitted = true
		}
	}
	for _, field := range uncertain {
		label := fieldLabel(field)
		if !appendLine("No se lee bien: " + truncateUTF16(label, 64)) {
			omitted = true
			break
		}
		if utf16Length(label) > 64 {
			omitted = true
		}
	}
	if len(uncertain)+len(issues) >= retakeThreshold {
		if !appendLine(RetakeTip) {
			omitted = true
		}
	}
	if omitted {
		b.WriteString("\n• Hay más avisos; revisá los campos antes de guardar.")
	}
	return b.String()
}

func utf16Length(value string) int {
	units := 0
	for _, r := range value {
		units += utf16.RuneLen(r)
	}
	return units
}

// displayDate convierte AAAA-MM-DD a DD/MM/AAAA; si no puede, devuelve el texto original.
func displayDate(date string) string {
	parsed, err := time.Parse("2006-01-02", date)
	if err != nil {
		return date
	}
	return parsed.Format("02/01/2006")
}

// fieldLabel es el nombre legible de un campo ("iva_10" → "IVA 10 %").
func fieldLabel(field string) string {
	return labelOr(fieldLabels, field, field)
}

func labelOr(labels map[string]string, key, fallback string) string {
	if label, ok := labels[key]; ok {
		return label
	}
	return fallback
}

// formatGs escribe un monto con punto de miles, como en Paraguay: 150000 → "150.000".
func formatGs(n int64) string {
	digits := strconv.FormatInt(n, 10)
	sign := ""
	if n < 0 {
		sign, digits = "-", digits[1:]
	}

	var b strings.Builder
	for i, d := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteRune(d)
	}
	return sign + b.String()
}
