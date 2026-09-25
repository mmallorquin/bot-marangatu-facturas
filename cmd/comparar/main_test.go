package main

import (
	"slices"
	"testing"

	"github.com/mmallorquin/bot-marangatu-facturas/internal/benchmark"
)

func TestParseSetupsCombinesModelsAndEfforts(t *testing.T) {
	// Act
	setups, err := parseSetups(" a/modelo , b/modelo ", "defecto, none")

	// Assert
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	want := []benchmark.Setup{
		{Model: "a/modelo", Effort: ""}, {Model: "a/modelo", Effort: "none"},
		{Model: "b/modelo", Effort: ""}, {Model: "b/modelo", Effort: "none"},
	}
	if !slices.Equal(setups, want) {
		t.Errorf("setups = %v, se esperaba %v", setups, want)
	}
}

func TestParseSetupsRejectsInvalidInput(t *testing.T) {
	cases := map[string][2]string{
		"sin modelos":           {"", "defecto"},
		"razonamiento inválido": {"a/modelo", "muchísimo"},
	}

	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseSetups(args[0], args[1]); err == nil {
				t.Error("se esperaba un error")
			}
		})
	}
}
