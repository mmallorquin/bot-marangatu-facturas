package telegram

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mmallorquin/bot-marangatu-facturas/internal/invoice"
	"github.com/mmallorquin/bot-marangatu-facturas/internal/reader"
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
	invoice.FieldIssuerRUC: "RUC",
	"razon_social_emisor":  "razón social",
	invoice.FieldTimbrado:  "timbrado",
	invoice.FieldNumber:    "número",
	invoice.FieldDate:      "fecha",
	invoice.FieldCondition: "condición",
	invoice.FieldCurrency:  "moneda",
	invoice.FieldExempt:    "exentas",
	invoice.FieldTaxed5:    "gravada 5 %",
	invoice.FieldTaxed10:   "gravada 10 %",
	invoice.FieldVAT5:      "IVA 5 %",
	invoice.FieldVAT10:     "IVA 10 %",
	invoice.FieldTotal:     "total",
	invoice.FieldCDC:       "CDC",
}

// FormatInvoice arma el mensaje que el bot le muestra al usuario.
func FormatInvoice(res reader.Result, issues []invoice.Issue) string {
	inv := res.Invoice
	if !inv.IsInvoice {
		return NotInvoiceMessage
	}

	var b strings.Builder
	fmt.Fprintf(&b, "🧾 %s %s\n", labelOr(typeLabels, inv.Type, "Comprobante"), inv.Number)
	fmt.Fprintf(&b, "Emisor: %s\n", inv.IssuerName)
	fmt.Fprintf(&b, "RUC: %s · Timbrado: %s\n", inv.IssuerRUC, inv.Timbrado)
	fmt.Fprintf(&b, "Fecha: %s · %s\n\n", displayDate(inv.Date), labelOr(conditionLabels, inv.Condition, inv.Condition))
	b.WriteString(formatAmounts(inv))
	b.WriteString("\n")
	b.WriteString(formatReview(inv.UncertainFields, issues))
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
		currency = inv.Currency
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
	for _, issue := range issues {
		fmt.Fprintf(&b, "• %s\n", issue.Message)
	}
	if len(uncertain) > 0 {
		labels := make([]string, 0, len(uncertain))
		for _, field := range uncertain {
			labels = append(labels, labelOr(fieldLabels, field, field))
		}
		fmt.Fprintf(&b, "• No se lee bien: %s\n", strings.Join(labels, ", "))
	}
	if len(uncertain)+len(issues) >= retakeThreshold {
		b.WriteString("\n" + RetakeTip)
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// displayDate convierte AAAA-MM-DD a DD/MM/AAAA; si no puede, devuelve el texto original.
func displayDate(date string) string {
	parsed, err := time.Parse("2006-01-02", date)
	if err != nil {
		return date
	}
	return parsed.Format("02/01/2006")
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
