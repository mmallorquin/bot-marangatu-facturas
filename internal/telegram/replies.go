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
		"Mandame una foto de tu factura (o el PDF): la leo, la revisás y la guardás. Podés mandar varias juntas.\n\n" +
		"/resumen — facturas guardadas este mes (o /resumen 08/2026)\n" +
		"/facturas — ver las facturas guardadas y borrar las que sobran\n" +
		"/pendientes — guardar de una vez las leídas que cierran\n" +
		"/autoguardar si — guardar solas las facturas que cierran\n" +
		"/recordatorios no — no recibir avisos de exportación\n" +
		"/borrar_mis_datos — borrar todo lo que guardaste en el bot\n" +
		"/exportar — revisar el período según tu /registro\n" +
		"/exportar 2026 — revisar el año (ZIP para registro 956)\n" +
		"/ruc — tu RUC (lo pide el archivo de Marangatu)\n" +
		"/imputar — a qué impuestos imputás tus compras (iva, ire, irp)\n" +
		"/registro — tu obligación 955 mensual o 956 anual\n" +
		"/cancelar — cancelar una corrección\n\n" +
		"Tocá Menú junto al campo de mensaje para elegir un comando.\n\n" +
		"El bot no presenta tus registros ante la DNIT.\n" + presentationHint + "\n" + retentionHint
	HelpMessage              = "Mandame una foto de la factura 📸 o el PDF 📄 y la leo por vos. Para ver las opciones, tocá Menú junto al campo de mensaje."
	ReadingMessage           = "⏳ Leyendo tu factura…"
	ReadingAlbumMessage      = "⏳ Leyendo tus facturas… Cuando terminen, con /pendientes guardás de una vez las que cierran."
	ReadErrorMessage         = "😕 No pude leer la factura en este momento. Probá de nuevo en un rato."
	NotInvoiceMessage        = "🤔 No parece una factura. Mandame una foto donde se vea el comprobante completo."
	UnsupportedFormatMessage = "Por ahora leo fotos e imágenes JPG, PNG o WEBP, y archivos PDF. Mandá la factura como foto 📸 o como PDF 📄"
	TooLargeMessage          = "El archivo es muy pesado (máximo 10 MB). Mandá la factura como foto normal 📸"
	RetakeTip                = "📸 Tip: sacá la foto de nuevo con buena luz, de frente y con la factura completa."

	SavedNote        = "💾 Guardada."
	DuplicateNote    = "⚠️ Ya tenías guardada esta factura."
	AutoSavedNote    = "💾 Guardada automáticamente. Si algo no está bien, tocá Deshacer."
	UndoneNote       = "↩️ Listo, no está guardada. Corregila, guardala o descartala."
	AlreadySavedNote = "⚠️ Ya tenés guardada esta factura (mismo RUC, timbrado y número). Si es otra, corregí el número; si no, descartala."
	ElectronicNote   = "ℹ️ Es una factura electrónica (tiene CDC): no va en el ZIP. Guardala igual para verla en tu /resumen. " +
		"Obtenela en Marangatu y revisá su imputación: si no se imputó automáticamente, debés hacerlo allí."
	DiscardedMessage      = "🗑️ Factura descartada."
	FixBeforeSavingAlert  = "⚠️ Corregí los datos marcados antes de guardar."
	NoLongerEditableAlert = "Esta factura ya fue guardada o descartada."
	NotSavedAnymoreAlert  = "Esa factura ya no está guardada."
	DeletedMessage        = "🗑️ Factura borrada."
	CancelledMessage      = "Listo, cancelé la corrección."
	NothingToCancel       = "No había ninguna corrección pendiente."
	StoreErrorMessage     = "😕 No pude guardar los cambios. Probá de nuevo."
	SavedAnswer           = "✅ Guardada"
	retryOrCancelHint     = "Probá de nuevo o escribí /cancelar."

	// RG DNIT 12/2024: importar o generar el ZIP no confirma la presentación.
	presentationHint = "⚠️ Importar el ZIP no confirma la presentación. Revisá los registros y su imputación en Marangatu; " +
		"después debés confirmar el período para obtener el Talón de Presentación."
	retentionHint  = "Conservá los comprobantes físicos por el plazo de prescripción del impuesto."
	noMovementHint = "Que el bot no tenga facturas no significa que no hubo operaciones. " +
		"Si realmente no tuviste movimiento y te corresponde presentar, confirmalo como «sin movimiento» en Marangatu."
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

// Formatos que aceptan los modelos con visión. El PDF es común en facturas que llegan por email.
var supportedMimeTypes = []string{"image/jpeg", "image/png", "image/webp", pdfMimeType}

const pdfMimeType = "application/pdf"

// ReplyForText decide qué responder a un mensaje de texto que no es un comando del flujo.
func ReplyForText(text string) string {
	switch commandOf(text) {
	case startCommand, "/ayuda", "/help":
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
