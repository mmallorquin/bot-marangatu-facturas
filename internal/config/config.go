// Package config carga y valida la configuración del bot desde variables de entorno.
package config

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Variables de entorno.
const (
	TokenEnvVar        = "TELEGRAM_BOT_TOKEN"
	OpenRouterKeyVar   = "OPENROUTER_API_KEY"
	OpenRouterModelVar = "OPENROUTER_MODEL"
	OpenRouterZDRVar   = "OPENROUTER_ZDR"
)

// DefaultModel es el modelo que se usa si no se configura OPENROUTER_MODEL.
// Lee imágenes, soporta respuestas en JSON y tiene proveedores con retención cero.
const DefaultModel = "deepseek/deepseek-v4.1-flash"

var (
	ErrMissingToken         = errors.New("falta el token del bot de Telegram")
	ErrMissingOpenRouterKey = errors.New("falta la API key de OpenRouter")
	ErrInvalidZDR           = errors.New("valor inválido para OPENROUTER_ZDR")
)

// Config es la configuración del bot. Se pasa por valor para que nadie la modifique.
type Config struct {
	TelegramBotToken string
	OpenRouterAPIKey string
	OpenRouterModel  string
	OpenRouterZDR    bool // true: solo proveedores que no guardan las facturas
}

// Load arma la configuración usando getenv (normalmente os.Getenv).
// Recibir la función en vez de leer el entorno directamente permite testearla.
func Load(getenv func(string) string) (Config, error) {
	read := func(key string) string { return strings.TrimSpace(getenv(key)) }

	token := read(TokenEnvVar)
	if token == "" {
		return Config{}, missing(ErrMissingToken, TokenEnvVar, "el token de @BotFather")
	}

	apiKey := read(OpenRouterKeyVar)
	if apiKey == "" {
		return Config{}, missing(ErrMissingOpenRouterKey, OpenRouterKeyVar, "tu key de openrouter.ai/keys")
	}

	model := read(OpenRouterModelVar)
	if model == "" {
		model = DefaultModel
	}

	zdr, err := parseBoolOrDefault(read(OpenRouterZDRVar), true)
	if err != nil {
		return Config{}, fmt.Errorf("%w: usá true o false", ErrInvalidZDR)
	}

	return Config{
		TelegramBotToken: token,
		OpenRouterAPIKey: apiKey,
		OpenRouterModel:  model,
		OpenRouterZDR:    zdr,
	}, nil
}

func missing(err error, envVar, hint string) error {
	return fmt.Errorf("%w: copiá .env.example a .env y completá %s con %s", err, envVar, hint)
}

func parseBoolOrDefault(value string, fallback bool) (bool, error) {
	if value == "" {
		return fallback, nil
	}
	return strconv.ParseBool(value)
}
