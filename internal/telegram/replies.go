// Package telegram contiene la lógica del bot de Telegram.
package telegram

import (
	"slices"
	"strings"

	"github.com/go-telegram/bot/models"

	"github.com/mmallorquin/bot-marangatu-facturas/internal/invoice"
)

const (
	WelcomeMessage = "👋 ¡Hola! Soy el bot de facturas para Marangatu.\n\n" +
		"Mandame una foto de tu factura: la leo, la revisás y la guardás.\n\n" +
		"/resumen — facturas guardadas este mes (o /resumen 08/2026)\n" +
		"/exportar — archivo del mes para importar en Marangatu\n" +
		"/ruc — tu RUC (lo pide el archivo de Marangatu)\n" +
		"/imputar — a qué impuestos imputás tus compras (iva, ire, irp)\n" +
		"/cancelar — cancelar una corrección"
	HelpMessage              = "Mandame una foto de la factura 📸 y la leo por vos."
	ReadingMessage           = "⏳ Leyendo tu factura…"
	ReadErrorMessage         = "😕 No pude leer la factura en este momento. Probá de nuevo en un rato."
	NotInvoiceMessage        = "🤔 No parece una factura. Mandame una foto donde se vea el comprobante completo."
	UnsupportedFormatMessage = "Por ahora solo leo imágenes JPG, PNG o WEBP. Mandá la factura como foto 📸"
	TooLargeMessage          = "La imagen es muy pesada (máximo 10 MB). Mandala como foto normal 📸"
	RetakeTip                = "📸 Tip: sacá la foto de nuevo con buena luz, de frente y con la factura completa."

	SavedNote             = "💾 Guardada."
	DuplicateNote         = "⚠️ Ya tenías guardada esta factura."
	DiscardedMessage      = "🗑️ Factura descartada."
	FixBeforeSavingAlert  = "⚠️ Corregí los datos marcados antes de guardar."
	NoLongerEditableAlert = "Esta factura ya fue guardada o descartada."
	CancelledMessage      = "Listo, cancelé la corrección."
	NothingToCancel       = "No había ninguna corrección pendiente."
	StoreErrorMessage     = "😕 No pude guardar los cambios. Probá de nuevo."
	SavedAnswer           = "✅ Guardada"
	retryOrCancelHint     = "Probá de nuevo o escribí /cancelar."
)

// Comandos del bot.
const (
	summaryCommand = "/resumen"
	cancelCommand  = "/cancelar"
)

// valueHints ayuda a escribir cada campo en el formato correcto.
var valueHints = map[string]string{
	invoice.FieldIssuerRUC: "ej. 80012345-6",
	invoice.FieldTimbrado:  "8 dígitos",
	invoice.FieldNumber:    "ej. 001-001-0001234",
	invoice.FieldDate:      "DD/MM/AAAA",
	invoice.FieldCondition: "contado o crédito",
}

const amountHint = "solo números, ej. 150.000"

const (
	startCommand = "/start"

	// Telegram permite descargar hasta 20 MB; una foto de factura no necesita más de 10.
	maxImageBytes = 10 << 20

	// Telegram convierte las fotos comprimidas siempre a JPEG.
	photoMimeType = "image/jpeg"
)

// Formatos que aceptan los modelos con visión.
var supportedMimeTypes = []string{"image/jpeg", "image/png", "image/webp"}

// ReplyForText decide qué responder a un mensaje de texto que no es un comando del flujo.
func ReplyForText(text string) string {
	if commandOf(text) == startCommand {
		return WelcomeMessage
	}
	return HelpMessage
}

// commandOf devuelve el comando de un mensaje ("/start@NombreDelBot x" → "/start"), o "".
func commandOf(text string) string {
	fields := strings.Fields(text)
	if len(fields) == 0 || !strings.HasPrefix(fields[0], "/") {
		return ""
	}
	command, _, _ := strings.Cut(fields[0], "@")
	return strings.ToLower(command)
}

// askValueMessage pide el valor correcto de un campo.
func askValueMessage(field string) string {
	hint, ok := valueHints[field]
	if !ok {
		hint = amountHint
		if field == invoice.FieldIssuerName {
			hint = "como figura en la factura"
		}
	}
	return "✏️ Escribí el valor correcto para " + fieldLabel(field) + " (" + hint + "):"
}

type imageKind int

const (
	notAnImage imageKind = iota
	imageSupported
	imageUnsupported
	imageTooLarge
)

// imageFile identifica el archivo de Telegram a descargar.
type imageFile struct {
	fileID   string
	mimeType string
}

// imageFileOf detecta si el mensaje trae una imagen que podemos leer.
func imageFileOf(msg *models.Message) (imageFile, imageKind) {
	if len(msg.Photo) > 0 {
		return imageFile{fileID: largestPhoto(msg.Photo).FileID, mimeType: photoMimeType}, imageSupported
	}

	doc := msg.Document
	switch {
	case doc == nil:
		return imageFile{}, notAnImage
	case !slices.Contains(supportedMimeTypes, doc.MimeType):
		return imageFile{}, imageUnsupported
	case doc.FileSize > maxImageBytes:
		return imageFile{}, imageTooLarge
	default:
		return imageFile{fileID: doc.FileID, mimeType: doc.MimeType}, imageSupported
	}
}

// largestPhoto elige la versión de mayor resolución (mejor para leer números chicos).
func largestPhoto(sizes []models.PhotoSize) models.PhotoSize {
	return slices.MaxFunc(sizes, func(a, b models.PhotoSize) int {
		return a.Width*a.Height - b.Width*b.Height
	})
}
