package telegram

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-telegram/bot"

	"github.com/mmallorquin/bot-marangatu-facturas/internal/store"
)

// Recordatorios de exportar: el bot avisa a quien tiene facturas guardadas de un período
// terminado y todavía no generó el ZIP. Se manda una sola vez por chat y período.
const (
	remindersCommand = "/recordatorios"

	// Aviso de exportación, no vencimiento oficial: desde el día 3 a las 9:00.
	monthlyReminderDay = 3
	// El anual (IRP-RSP) se presenta hasta febrero; se recuerda desde el 15 de enero.
	annualReminderDay = 15
	reminderHour      = 9

	// Cada cuánto se revisa si hay recordatorios para mandar.
	reminderCheckEvery = 30 * time.Minute
)

// reminderLocation es la hora de Paraguay; si el sistema no la tiene, se usa UTC-3.
var reminderLocation = func() *time.Location {
	if loc, err := time.LoadLocation("America/Asuncion"); err == nil {
		return loc
	}
	return time.FixedZone("PYT", -3*60*60)
}()

// RunReminders revisa cada media hora si hay recordatorios para mandar, hasta que ctx se cancela.
func RunReminders(ctx context.Context, b *bot.Bot, deps Deps) {
	h := &handler{deps: deps, logger: deps.Logger, albums: &albums{}}
	if h.deps.Now == nil {
		h.deps.Now = time.Now
	}
	ticker := time.NewTicker(reminderCheckEvery)
	defer ticker.Stop()
	for {
		h.sendDueReminders(ctx, b, h.deps.Now())
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// duePeriods devuelve qué recordatorios corresponden en este momento: el mensual (el mes
// anterior) desde el día 3 y el anual (el año anterior) desde el 15 de enero.
func duePeriods(now time.Time) (monthly, annual string) {
	local := now.In(reminderLocation)
	if local.Hour() < reminderHour {
		return "", ""
	}
	if local.Day() >= monthlyReminderDay {
		monthly = local.AddDate(0, 0, -local.Day()).Format("2006-01") // último día del mes anterior
	}
	if local.Month() == time.January && local.Day() >= annualReminderDay {
		annual = local.AddDate(-1, 0, 0).Format(yearLayout)
	}
	return monthly, annual
}

func (h *handler) sendDueReminders(ctx context.Context, b *bot.Bot, now time.Time) {
	monthly, annual := duePeriods(now)
	if monthly != "" {
		h.remind(ctx, b, monthly, func(c store.ReminderCandidate) bool {
			return c.Registration == store.RegistrationMonthly
		})
	}
	if annual != "" {
		h.remind(ctx, b, annual, func(c store.ReminderCandidate) bool {
			return c.Registration == store.RegistrationAnnual
		})
	}
}

// remind avisa a los chats del período que cumplen applies.
func (h *handler) remind(ctx context.Context, b *bot.Bot, period string, applies func(store.ReminderCandidate) bool) {
	candidates, err := h.deps.Store.ReminderCandidates(ctx, period)
	if err != nil {
		h.logger.Error("no se pudieron buscar los recordatorios", "periodo", period, "error", err)
		return
	}
	for _, c := range candidates {
		if !applies(c) {
			continue
		}
		// Se anota antes de mandar: si el usuario bloqueó el bot, no se reintenta cada media hora.
		if err := h.deps.Store.MarkReminded(ctx, c.ChatID, period); err != nil {
			h.logger.Error("no se pudo anotar el recordatorio", "chat_id", c.ChatID, "error", err)
			continue
		}
		h.send(ctx, b, c.ChatID, reminderMessage(period, c.Invoices), nil)
		detail := "mensual"
		if isAnnual(period) {
			detail = "anual"
		}
		h.track(ctx, c.ChatID, store.Event{Kind: store.EventReminder, Detail: detail})
	}
}

func reminderMessage(period string, invoices int) string {
	argument := period
	if !isAnnual(period) {
		argument = period[5:] + "/" + period[:4] // 2026-09 → 09/2026
	}
	return fmt.Sprintf("📅 Tenés %d %s de %s y todavía no generaste el ZIP para Marangatu.\n\n"+
		"Cuando quieras: /exportar %s\n\nPara no recibir este aviso: /recordatorios no",
		invoices, plural(invoices, "factura guardada", "facturas guardadas"), periodTitle(period), argument)
}

func (h *handler) setReminders(ctx context.Context, b *bot.Bot, chatID int64, text string) {
	fields := strings.Fields(strings.ToLower(text))
	if len(fields) < 2 {
		cs, err := h.deps.Store.Settings(ctx, chatID)
		if err != nil {
			h.send(ctx, b, chatID, StoreErrorMessage, nil)
			return
		}
		state := "desactivados."
		if cs.Reminders {
			state = "activados: te aviso según tu /registro si tenés facturas sin exportar. No es un aviso de vencimiento oficial."
			if !cs.Registration.Valid() {
				state += " Elegí 955 o 956 con /registro para recibir avisos."
			}
		}
		h.send(ctx, b, chatID, "Los recordatorios están "+state+"\n\nPara cambiarlo: /recordatorios si o /recordatorios no", nil)
		return
	}

	var on bool
	switch {
	case containsWord(onWords, fields[1]):
		on = true
	case containsWord(offWords, fields[1]):
	default:
		h.send(ctx, b, chatID, "Escribí /recordatorios si o /recordatorios no", nil)
		return
	}
	if err := h.deps.Store.SetReminders(ctx, chatID, on); err != nil {
		h.logger.Error("no se pudieron cambiar los recordatorios", "chat_id", chatID, "error", err)
		h.send(ctx, b, chatID, StoreErrorMessage, nil)
		return
	}
	if on {
		h.send(ctx, b, chatID, "✅ Listo: los avisos de exportación están activados según tu /registro. Si todavía no elegiste 955 o 956, hacelo con /registro. No son avisos de vencimiento oficial.", nil)
		return
	}
	h.send(ctx, b, chatID, "✅ Listo: no te mando más recordatorios.", nil)
}
