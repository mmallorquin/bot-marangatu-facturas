// Package config carga y valida la configuración del bot desde variables de entorno.
package config

import (
	"errors"
	"fmt"
	"strings"
)

// TokenEnvVar es la variable de entorno con el token que entrega @BotFather.
const TokenEnvVar = "TELEGRAM_BOT_TOKEN"

// ErrMissingToken indica que no se configuró el token del bot.
var ErrMissingToken = errors.New("falta el token del bot de Telegram")

// Config es la configuración del bot. Se pasa por valor para que nadie la modifique.
type Config struct {
	TelegramBotToken string
}

// Load arma la configuración usando getenv (normalmente os.Getenv).
// Recibir la función en vez de leer el entorno directamente permite testearla.
func Load(getenv func(string) string) (Config, error) {
	token := strings.TrimSpace(getenv(TokenEnvVar))
	if token == "" {
		return Config{}, fmt.Errorf(
			"%w: copiá .env.example a .env y completá %s con el token de @BotFather",
			ErrMissingToken, TokenEnvVar,
		)
	}
	return Config{TelegramBotToken: token}, nil
}
