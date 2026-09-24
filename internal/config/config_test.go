package config

import (
	"errors"
	"testing"
)

func envFrom(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func TestLoadReadsTokenFromEnvironment(t *testing.T) {
	// Arrange
	getenv := envFrom(map[string]string{TokenEnvVar: "123456:ABC-token"})

	// Act
	cfg, err := Load(getenv)

	// Assert
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if cfg.TelegramBotToken != "123456:ABC-token" {
		t.Errorf("token = %q, se esperaba %q", cfg.TelegramBotToken, "123456:ABC-token")
	}
}

func TestLoadTrimsWhitespaceAroundToken(t *testing.T) {
	cfg, err := Load(envFrom(map[string]string{TokenEnvVar: "  123456:ABC-token \n"}))

	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if cfg.TelegramBotToken != "123456:ABC-token" {
		t.Errorf("token = %q, se esperaba sin espacios", cfg.TelegramBotToken)
	}
}

func TestLoadFailsWhenTokenIsMissing(t *testing.T) {
	cases := map[string]map[string]string{
		"variable ausente": {},
		"variable vacía":   {TokenEnvVar: ""},
		"solo espacios":    {TokenEnvVar: "   "},
	}

	for name, env := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Load(envFrom(env))

			if !errors.Is(err, ErrMissingToken) {
				t.Errorf("error = %v, se esperaba ErrMissingToken", err)
			}
		})
	}
}
