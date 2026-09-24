package telegram

import (
	"testing"

	"github.com/go-telegram/bot/models"
)

func TestReplyFor(t *testing.T) {
	cases := []struct {
		name string
		msg  *models.Message
		want string
	}{
		{"comando /start", &models.Message{Text: "/start"}, WelcomeMessage},
		{"/start con mención al bot", &models.Message{Text: "/start@FacturasBot"}, WelcomeMessage},
		{"foto comprimida", &models.Message{Photo: []models.PhotoSize{{FileID: "abc"}}}, PhotoReceivedMessage},
		{"imagen enviada como archivo", &models.Message{Document: &models.Document{MimeType: "image/jpeg"}}, PhotoReceivedMessage},
		{"PDF enviado como archivo", &models.Message{Document: &models.Document{MimeType: "application/pdf"}}, HelpMessage},
		{"texto cualquiera", &models.Message{Text: "hola"}, HelpMessage},
		{"comando desconocido", &models.Message{Text: "/otro"}, HelpMessage},
		{"mensaje vacío", &models.Message{}, HelpMessage},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			got := ReplyFor(tc.msg)

			// Assert
			if got != tc.want {
				t.Errorf("ReplyFor() = %q, se esperaba %q", got, tc.want)
			}
		})
	}
}
