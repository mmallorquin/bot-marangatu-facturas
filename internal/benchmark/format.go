package benchmark

import (
	"fmt"
	"strings"
)

// FormatTables arma dos tablas Markdown: el resumen por configuración y una matriz
// foto × configuración. No incluye ningún dato de las facturas.
func FormatTables(summaries []Summary, runs []Run) string {
	var b strings.Builder
	b.WriteString("## Resumen\n\n")
	b.WriteString("| Configuración | ✅ Limpias | ⚠️ Con avisos | 🤔 No factura | ❌ Errores | Coinciden | Costo total | Costo/foto | Seg. prom. | Seg. máx. | Tokens razon. |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|---|---|---|\n")
	for _, s := range summaries {
		fmt.Fprintf(&b, "| %s | %d | %d | %d | %d | %d | $%.4f | $%.4f | %.1f | %.1f | %d |\n",
			s.Setup.Label(), s.Clean, s.WithIssues, s.NotInvoice, s.Errors, s.Agrees,
			s.TotalCostUSD, s.TotalCostUSD/float64(s.Photos), s.AvgSeconds, s.MaxSeconds, s.AvgReasoningTokens)
	}

	b.WriteString("\n## Por foto\n\n")
	b.WriteString("✅ sin problemas · ⚠️N avisos · 🤔 no es factura · ❌ error\n\n")
	b.WriteString("| Foto |")
	for _, s := range summaries {
		fmt.Fprintf(&b, " %s |", s.Setup.Label())
	}
	b.WriteString("\n|---|" + strings.Repeat("---|", len(summaries)) + "\n")

	cells := map[string]map[Setup]string{}
	var photos []string
	for _, r := range runs {
		if cells[r.Photo] == nil {
			cells[r.Photo] = map[Setup]string{}
			photos = append(photos, r.Photo)
		}
		cells[r.Photo][r.Setup] = cell(r)
	}
	for _, photo := range photos {
		fmt.Fprintf(&b, "| %s |", photo)
		for _, s := range summaries {
			fmt.Fprintf(&b, " %s |", cells[photo][s.Setup])
		}
		b.WriteString("\n")
	}
	return b.String()
}

func cell(r Run) string {
	kind, problems := classify(r)
	switch kind {
	case outcomeClean:
		return "✅"
	case outcomeIssues:
		return fmt.Sprintf("⚠️%d", problems)
	case outcomeNotInvoice:
		return "🤔"
	default:
		return "❌"
	}
}
