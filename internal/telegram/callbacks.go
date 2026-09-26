package telegram

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-telegram/bot/models"

	"github.com/mmallorquin/bot-marangatu-facturas/internal/invoice"
	"github.com/mmallorquin/bot-marangatu-facturas/internal/store"
)

// Acciones de los botones. Son de una letra porque Telegram limita el dato a 64 bytes.
const (
	actionSave    = "g" // guardar
	actionDiscard = "d" // descartar
	actionEdit    = "e" // mostrar los campos a corregir
	actionField   = "f" // corregir un campo
	actionBack    = "v" // volver a los botones principales
)

const fieldsPerRow = 2

var errInvalidCallback = errors.New("botón inválido")

// callback es la acción de un botón sobre una factura.
type callback struct {
	action string
	id     int64
	field  string // solo para actionField
}

func (c callback) encode() string {
	if c.action == actionField {
		return fmt.Sprintf("%s:%d:%s", c.action, c.id, c.field)
	}
	return fmt.Sprintf("%s:%d", c.action, c.id)
}

func parseCallback(data string) (callback, error) {
	parts := strings.Split(data, ":")
	if len(parts) < 2 {
		return callback{}, errInvalidCallback
	}
	id, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return callback{}, errInvalidCallback
	}

	c := callback{action: parts[0], id: id}
	switch {
	case c.action == actionField && len(parts) == 3 && slices.Contains(invoice.EditableFields, parts[2]):
		c.field = parts[2]
		return c, nil
	case len(parts) == 2 && slices.Contains([]string{actionSave, actionDiscard, actionEdit, actionBack}, c.action):
		return c, nil
	default:
		return callback{}, errInvalidCallback
	}
}

func button(text string, c callback) models.InlineKeyboardButton {
	return models.InlineKeyboardButton{Text: text, CallbackData: c.encode()}
}

// mainKeyboard son los botones que acompañan a cada factura leída.
func mainKeyboard(id int64) *models.InlineKeyboardMarkup {
	return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{
		button("✅ Guardar", callback{action: actionSave, id: id}),
		button("✏️ Corregir", callback{action: actionEdit, id: id}),
		button("🗑️ Descartar", callback{action: actionDiscard, id: id}),
	}}}
}

// discardOnlyKeyboard se muestra cuando la factura ya estaba guardada.
func discardOnlyKeyboard(id int64) *models.InlineKeyboardMarkup {
	return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{
		button("🗑️ Descartar", callback{action: actionDiscard, id: id}),
	}}}
}

// noKeyboard quita los botones de un mensaje.
func noKeyboard() *models.InlineKeyboardMarkup {
	return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{}}
}

// fieldsKeyboard muestra un botón por campo corregible, de a dos por fila, y "Volver".
func fieldsKeyboard(id int64) *models.InlineKeyboardMarkup {
	var rows [][]models.InlineKeyboardButton
	for chunk := range slices.Chunk(invoice.EditableFields, fieldsPerRow) {
		var row []models.InlineKeyboardButton
		for _, field := range chunk {
			row = append(row, button(capitalize(fieldLabel(field)), callback{action: actionField, id: id, field: field}))
		}
		rows = append(rows, row)
	}
	rows = append(rows, []models.InlineKeyboardButton{button("← Volver", callback{action: actionBack, id: id})})
	return &models.InlineKeyboardMarkup{InlineKeyboard: rows}
}

var monthNames = []string{
	"Enero", "Febrero", "Marzo", "Abril", "Mayo", "Junio",
	"Julio", "Agosto", "Septiembre", "Octubre", "Noviembre", "Diciembre",
}

// Formatos aceptados en "/resumen <período>".
var periodLayouts = []string{"2006-01", "1/2006", "01/2006"}

// parsePeriod lee el período de "/resumen [AAAA-MM | MM/AAAA]"; sin argumento, el mes actual.
func parsePeriod(text string, now time.Time) (string, error) {
	fields := strings.Fields(text)
	if len(fields) < 2 {
		return now.Format("2006-01"), nil
	}
	for _, layout := range periodLayouts {
		if parsed, err := time.Parse(layout, fields[1]); err == nil {
			return parsed.Format("2006-01"), nil
		}
	}
	return "", fmt.Errorf("no entiendo el período %q: usá /resumen 09/2026", fields[1])
}

// periodTitle convierte "2026-09" en "Septiembre 2026".
func periodTitle(period string) string {
	parsed, err := time.Parse("2006-01", period)
	if err != nil {
		return period
	}
	return fmt.Sprintf("%s %d", monthNames[parsed.Month()-1], parsed.Year())
}

// FormatMonthSummary arma el mensaje de /resumen.
func FormatMonthSummary(period string, sum store.Summary) string {
	title := periodTitle(period)
	if sum.Count == 0 {
		return fmt.Sprintf("📊 %s\n\nNo guardaste facturas de este mes todavía.", title)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "📊 %s: %d facturas guardadas\n\n", title, sum.Count)
	if sum.Exempt != 0 {
		fmt.Fprintf(&b, "Exentas: %s\n", formatGs(sum.Exempt))
	}
	if sum.Taxed5 != 0 || sum.VAT5 != 0 {
		fmt.Fprintf(&b, "Gravada 5 %%: %s · IVA 5 %%: %s\n", formatGs(sum.Taxed5), formatGs(sum.VAT5))
	}
	if sum.Taxed10 != 0 || sum.VAT10 != 0 {
		fmt.Fprintf(&b, "Gravada 10 %%: %s · IVA 10 %%: %s\n", formatGs(sum.Taxed10), formatGs(sum.VAT10))
	}
	fmt.Fprintf(&b, "Total: %s Gs", formatGs(sum.Total))
	return b.String()
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	return strings.ToUpper(string(r[0])) + string(r[1:])
}
