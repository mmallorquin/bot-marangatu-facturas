// Package telegram contiene la lógica del bot de Telegram.
package telegram

import (
	"strings"

	"github.com/go-telegram/bot/models"
)

const (
	WelcomeMessage = "👋 ¡Hola! Soy el bot de facturas para Marangatu.\n\n" +
		"Mandame una foto de tu factura y la voy a registrar por vos."
	PhotoReceivedMessage = "📸 Factura recibida ✅\n\n" +
		"Muy pronto voy a poder leerla y cargarla en tu planilla para Marangatu."
	HelpMessage = "Por ahora solo entiendo fotos 📸 Mandame una foto de la factura."
)

const startCommand = "/start"

// ReplyFor decide qué responderle a un mensaje. Es una función pura: no envía nada.
func ReplyFor(msg *models.Message) string {
	switch {
	case isStartCommand(msg.Text):
		return WelcomeMessage
	case isImage(msg):
		return PhotoReceivedMessage
	default:
		return HelpMessage
	}
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

// isImage detecta fotos comprimidas o imágenes enviadas como archivo
// (estas últimas conservan mejor calidad para leer la factura).
func isImage(msg *models.Message) bool {
	if len(msg.Photo) > 0 {
		return true
	}
	return msg.Document != nil && strings.HasPrefix(msg.Document.MimeType, "image/")
}
