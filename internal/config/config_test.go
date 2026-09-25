package config

import (
	"errors"
	"strings"
	"testing"
)

func envFrom(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

// validEnv tiene las variables obligatorias; cada test cambia lo que necesita.
func validEnv() map[string]string {
	return map[string]string{
		TokenEnvVar:      "123456:ABC-token",
		OpenRouterKeyVar: "sk-or-test",
	}
}

func TestLoadReadsRequiredValuesAndDefaults(t *testing.T) {
	// Act
	cfg, err := Load(envFrom(validEnv()))

	// Assert
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	want := Config{
		TelegramBotToken: "123456:ABC-token",
		OpenRouterAPIKey: "sk-or-test",
		OpenRouterModel:  DefaultModel,
		OpenRouterZDR:    true,
		ReasoningEffort:  DefaultReasoningEffort,
	}
	if cfg != want {
		t.Errorf("Load() = %+v, se esperaba %+v", cfg, want)
	}
}

func TestLoadTrimsWhitespace(t *testing.T) {
	env := validEnv()
	env[TokenEnvVar] = "  123456:ABC-token \n"
	env[OpenRouterKeyVar] = " sk-or-test "
	env[OpenRouterModelVar] = " google/gemini-3.1-flash-lite "

	cfg, err := Load(envFrom(env))

	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if cfg.TelegramBotToken != "123456:ABC-token" || cfg.OpenRouterAPIKey != "sk-or-test" ||
		cfg.OpenRouterModel != "google/gemini-3.1-flash-lite" {
		t.Errorf("no se quitaron los espacios: %+v", cfg)
	}
}

func TestLoadAllowsDisablingZDR(t *testing.T) {
	for _, value := range []string{"false", "0", "FALSE"} {
		env := validEnv()
		env[OpenRouterZDRVar] = value

		cfg, err := Load(envFrom(env))

		if err != nil || cfg.OpenRouterZDR {
			t.Errorf("%s=%q: zdr=%v err=%v, se esperaba zdr=false", OpenRouterZDRVar, value, cfg.OpenRouterZDR, err)
		}
	}
}

func TestLoadAcceptsValidReasoningEfforts(t *testing.T) {
	for _, effort := range []string{"none", "minimal", "low", "medium", "high", "LOW"} {
		env := validEnv()
		env[ReasoningVar] = effort

		cfg, err := Load(envFrom(env))

		if err != nil || cfg.ReasoningEffort != strings.ToLower(effort) {
			t.Errorf("%s=%q: effort=%q err=%v", ReasoningVar, effort, cfg.ReasoningEffort, err)
		}
	}
}

func TestLoadFailsOnMissingOrInvalidValues(t *testing.T) {
	cases := []struct {
		name    string
		key     string
		value   string
		wantErr error
	}{
		{"token ausente", TokenEnvVar, "", ErrMissingToken},
		{"token con espacios", TokenEnvVar, "   ", ErrMissingToken},
		{"key de OpenRouter ausente", OpenRouterKeyVar, "", ErrMissingOpenRouterKey},
		{"ZDR inválido", OpenRouterZDRVar, "tal vez", ErrInvalidZDR},
		{"razonamiento inválido", ReasoningVar, "muchísimo", ErrInvalidReasoning},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := validEnv()
			env[tc.key] = tc.value

			_, err := Load(envFrom(env))

			if !errors.Is(err, tc.wantErr) {
				t.Errorf("error = %v, se esperaba %v", err, tc.wantErr)
			}
		})
	}
}
