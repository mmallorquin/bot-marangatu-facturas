package telegram

import (
	"errors"
	"strings"
	"testing"
)

func TestRedactRemovesTokenFromErrorMessage(t *testing.T) {
	// Arrange
	token := "123456:ABC-secreto"
	err := errors.New(`Post "https://api.telegram.org/bot123456:ABC-secreto/getUpdates": timeout`)

	// Act
	got := Redact(err, token)

	// Assert
	if strings.Contains(got, token) {
		t.Errorf("el mensaje todavía contiene el token: %q", got)
	}
	if !strings.Contains(got, "bot<token>/getUpdates") {
		t.Errorf("se esperaba el marcador <token>, se obtuvo %q", got)
	}
}

func TestRedactHandlesNilErrorAndEmptyToken(t *testing.T) {
	if got := Redact(nil, "x"); got != "" {
		t.Errorf("Redact(nil) = %q, se esperaba vacío", got)
	}
	if got := Redact(errors.New("falla"), ""); got != "falla" {
		t.Errorf("Redact con token vacío = %q, se esperaba el mensaje original", got)
	}
}
