package telegram

import "strings"

const tokenPlaceholder = "<token>"

// Redact devuelve el mensaje del error sin el token.
// Telegram pone el token en la URL de cada request, así que los errores
// de red lo incluirían en los logs si no lo ocultamos.
func Redact(err error, token string) string {
	if err == nil {
		return ""
	}
	if token == "" {
		return err.Error()
	}
	return strings.ReplaceAll(err.Error(), token, tokenPlaceholder)
}
