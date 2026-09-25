package telegram

import (
	"testing"

	"github.com/go-telegram/bot/models"
)

func TestReplyForText(t *testing.T) {
	cases := map[string]string{
		"/start":             WelcomeMessage,
		"/start@FacturasBot": WelcomeMessage,
		"hola":               HelpMessage,
		"/otro":              HelpMessage,
		"":                   HelpMessage,
	}

	for text, want := range cases {
		t.Run(text, func(t *testing.T) {
			if got := ReplyForText(text); got != want {
				t.Errorf("ReplyForText(%q) = %q, se esperaba %q", text, got, want)
			}
		})
	}
}

func TestImageFileOf(t *testing.T) {
	cases := []struct {
		name       string
		msg        *models.Message
		wantFileID string
		wantMime   string
		wantKind   imageKind
	}{
		{
			name: "foto: elige la de mayor resolución",
			msg: &models.Message{Photo: []models.PhotoSize{
				{FileID: "chica", Width: 90, Height: 120}, {FileID: "grande", Width: 1280, Height: 1706}, {FileID: "mediana", Width: 320, Height: 426},
			}},
			wantFileID: "grande", wantMime: "image/jpeg", wantKind: imageSupported,
		},
		{
			name:       "imagen PNG como archivo",
			msg:        &models.Message{Document: &models.Document{FileID: "doc", MimeType: "image/png"}},
			wantFileID: "doc", wantMime: "image/png", wantKind: imageSupported,
		},
		{
			name:     "imagen HEIC como archivo",
			msg:      &models.Message{Document: &models.Document{FileID: "doc", MimeType: "image/heic"}},
			wantKind: imageUnsupported,
		},
		{
			name:     "PDF",
			msg:      &models.Message{Document: &models.Document{FileID: "doc", MimeType: "application/pdf"}},
			wantKind: imageUnsupported,
		},
		{
			name:     "imagen demasiado grande",
			msg:      &models.Message{Document: &models.Document{FileID: "doc", MimeType: "image/jpeg", FileSize: maxImageBytes + 1}},
			wantKind: imageTooLarge,
		},
		{
			name:     "texto",
			msg:      &models.Message{Text: "hola"},
			wantKind: notAnImage,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			file, kind := imageFileOf(tc.msg)

			// Assert
			if kind != tc.wantKind {
				t.Fatalf("kind = %v, se esperaba %v", kind, tc.wantKind)
			}
			if kind == imageSupported && (file.fileID != tc.wantFileID || file.mimeType != tc.wantMime) {
				t.Errorf("archivo = %+v, se esperaba %s (%s)", file, tc.wantFileID, tc.wantMime)
			}
		})
	}
}
