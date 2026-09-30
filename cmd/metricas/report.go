package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/mmallorquin/bot-marangatu-facturas/internal/store"
)

// formatReport arma el reporte en texto. Nunca incluye datos de las facturas.
func formatReport(m store.Metrics, days, excluded int, perUser bool) string {
	var b strings.Builder
	period := "todo el historial"
	if days > 0 {
		period = fmt.Sprintf("últimos %d días", days)
	}
	fmt.Fprintf(&b, "📊 Métricas del bot — %s", period)
	if excluded > 0 {
		fmt.Fprintf(&b, " (sin %d chat excluidos)", excluded)
	}
	b.WriteString("\n")

	section(&b, "👥 Usuarios")
	line(&b, "Activos", "%d (%d nuevos, %d volvieron otro día)", m.Users, m.NewUsers, m.ReturningUsers)
	line(&b, "Guardaron una factura", "%d %s", m.UsersSaved, pct(m.UsersSaved, m.Users))
	line(&b, "Bajaron un ZIP", "%d %s", m.UsersExported, pct(m.UsersExported, m.Users))
	line(&b, "Total histórico", "%d", m.AllTimeUsers)

	read := m.ReadOK + m.ReadIssues + m.NotInvoice + m.ReadErrors
	section(&b, "📸 Embudo")
	line(&b, "Fotos y PDF recibidos", "%d, %d en PDF (+%d rechazados por formato o tamaño)", m.Photos, m.PDFs, m.Rejected)
	line(&b, "Leídas sin avisos", "%d %s", m.ReadOK, pct(m.ReadOK, read))
	line(&b, "Leídas con avisos", "%d %s", m.ReadIssues, pct(m.ReadIssues, read))
	line(&b, "No era factura", "%d %s", m.NotInvoice, pct(m.NotInvoice, read))
	line(&b, "Errores de lectura", "%d %s", m.ReadErrors, pct(m.ReadErrors, read))
	line(&b, "Guardadas", "%d", m.Saved)
	line(&b, "Descartadas", "%d", m.Discarded)
	line(&b, "Borradas tras guardar", "%d", m.Deleted)
	line(&b, "Abandonadas (+24 h)", "%d", m.Abandoned)
	line(&b, "Duplicadas", "%d", m.Duplicates)
	line(&b, "Guardar bloqueado", "%d (datos que no cierran)", m.Blocked)

	section(&b, "🎯 Calidad de la lectura")
	line(&b, "Guardadas sin corregir", "%d de %d %s", m.SavedClean, m.Saved, pct(m.SavedClean, m.Saved))
	if len(m.FieldAccuracy) == 0 {
		line(&b, "Campos mal leídos", "ninguno")
	}
	for _, f := range m.FieldAccuracy {
		line(&b, "  "+f.Field, "corregido en %d de %d guardadas %s", f.Corrected, m.SavedCompared, pct(f.Corrected, m.SavedCompared))
	}
	line(&b, "Correcciones hechas", "%s", formatCorrections(m.Corrections))
	line(&b, "Correcciones inválidas", "%d", m.BadCorrections)

	section(&b, "💸 Costo y velocidad")
	line(&b, "Costo OpenRouter", "USD %.4f", m.CostUSD)
	if m.Saved > 0 {
		line(&b, "Costo por guardada", "USD %.4f", m.CostUSD/float64(m.Saved))
	}
	line(&b, "Lectura", "%.1f s promedio · %.1f s p90", m.AvgSeconds, m.P90Seconds)
	line(&b, "Foto → guardada (mediana)", "%s", humanDuration(m.MedianToSave))

	section(&b, "🧭 Funciones")
	line(&b, "/resumen", "%d", m.Summaries)
	line(&b, "/facturas", "%d", m.Lists)
	line(&b, "/exportar", "%d", m.ExportPreviews)
	line(&b, "CSV o Excel", "%d", m.ExportReviews)
	line(&b, "ZIP", "%d", m.ExportZIPs)
	line(&b, "Textos no entendidos", "%d", m.UnknownText)

	if perUser {
		section(&b, "🙋 Por usuario")
		b.WriteString("chat_id        primer uso  último uso  días  fotos  guardadas  zip  costo\n")
		for _, u := range m.PerUser {
			fmt.Fprintf(&b, "%-14d %-11s %-11s %4d  %5d  %9d  %3d  %.4f\n",
				u.ChatID, u.FirstSeen.Local().Format("02/01/2006"), u.LastSeen.Local().Format("02/01/2006"),
				u.Days, u.Photos, u.Saved, u.ZIPs, u.CostUSD)
		}
	}
	return b.String()
}

// formatCorrections resume las correcciones por campo, incluidas las de facturas que después se descartaron.
func formatCorrections(counts []store.FieldCount) string {
	if len(counts) == 0 {
		return "0"
	}
	total := 0
	parts := make([]string, len(counts))
	for i, c := range counts {
		total += c.Count
		parts[i] = fmt.Sprintf("%s %d", c.Field, c.Count)
	}
	return fmt.Sprintf("%d (%s)", total, strings.Join(parts, ", "))
}

func section(b *strings.Builder, title string) {
	fmt.Fprintf(b, "\n%s\n", title)
}

func line(b *strings.Builder, label, format string, args ...any) {
	fmt.Fprintf(b, "  %-26s %s\n", label+":", strings.TrimSpace(fmt.Sprintf(format, args...)))
}

// pct devuelve "(45 %)", o "" si no hay base para calcularlo.
func pct(part, total int) string {
	if total == 0 {
		return ""
	}
	return fmt.Sprintf("(%.0f %%)", 100*float64(part)/float64(total))
}

func humanDuration(d time.Duration) string {
	switch {
	case d == 0:
		return "-"
	case d < time.Minute:
		return fmt.Sprintf("%.0f s", d.Seconds())
	case d < time.Hour:
		return fmt.Sprintf("%.0f min", d.Minutes())
	default:
		return fmt.Sprintf("%.1f h", d.Hours())
	}
}
