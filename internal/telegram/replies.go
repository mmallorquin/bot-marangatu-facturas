// Package telegram contiene la lógica del bot de Telegram.
package telegram

import (
	"slices"
	"strings"

	"github.com/go-telegram/bot/models"
)

const (
	WelcomeMessage = "👋 ¡Hola! Soy el bot de facturas para Marangatu.\n\n" +
		"Mandame una foto de tu factura y te devuelvo los datos listos para registrar."
	HelpMessage              = "Mandame una foto de la factura 📸 y la leo por vos."
	ReadingMessage           = "⏳ Leyendo tu factura…"
	ReadErrorMessage         = "😕 No pude leer la factura en este momento. Probá de nuevo en un rato."
	NotInvoiceMessage        = "🤔 No parece una factura. Mandame una foto donde se vea el comprobante completo."
	UnsupportedFormatMessage = "Por ahora solo leo imágenes JPG, PNG o WEBP. Mandá la factura como foto 📸"
	TooLargeMessage          = "La imagen es muy pesada (máximo 10 MB). Mandala como foto normal 📸"
)

const (
	startCommand = "/start"

	// Telegram permite descargar hasta 20 MB; una foto de factura no necesita más de 10.
	maxImageBytes = 10 << 20

	// Telegram convierte las fotos comprimidas siempre a JPEG.
	photoMimeType = "image/jpeg"
)

// Formatos que aceptan los modelos con visión.
var supportedMimeTypes = []string{"image/jpeg", "image/png", "image/webp"}

// ReplyForText decide qué responder a un mensaje de texto.
func ReplyForText(text string) string {
	if isStartCommand(text) {
		return WelcomeMessage
	}
	return HelpMessage
}

// isStartCommand acepta "/start" y "/start@NombreDelBot" (así llega en grupos).
func isStartCommand(text string) bool {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return false
	}
	command := fields[0]
	return command == startCommand || strings.HasPrefix(command, startCommand+"@")
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
