package invoice

import "testing"

// Vectores generados con la implementación de referencia de la DNIT (dv.js).
func TestCheckDigitMatchesReferenceImplementation(t *testing.T) {
	cases := map[string]int{
		"80000519": 8,
		"80024627": 6,
		"1234567":  9,
		"4567890":  1,
		"80012345": 0,
		"5006300":  6,
		"12345678": 9,
		"3456789A": 3, // RUC de extranjero que termina en letra
		"80100001": 7,
		"444444":   2,
	}

	for base, want := range cases {
		t.Run(base, func(t *testing.T) {
			if got := CheckDigit(base); got != want {
				t.Errorf("CheckDigit(%q) = %d, se esperaba %d", base, got, want)
			}
		})
	}
}

func TestValidateRUC(t *testing.T) {
	cases := []struct {
		ruc     string
		isValid bool
	}{
		{"80000519-8", true},
		{"1234567-9", true},
		{" 80024627-6 ", true},
		{"80000519-7", false}, // dígito verificador incorrecto
		{"80000519", false},   // falta el dígito verificador
		{"8000-0519-8", false},
		{"", false},
		{"ABCDEFG-1", false},
	}

	for _, tc := range cases {
		t.Run(tc.ruc, func(t *testing.T) {
			err := ValidateRUC(tc.ruc)

			if tc.isValid && err != nil {
				t.Errorf("ValidateRUC(%q) = %v, se esperaba válido", tc.ruc, err)
			}
			if !tc.isValid && err == nil {
				t.Errorf("ValidateRUC(%q) = nil, se esperaba un error", tc.ruc)
			}
		})
	}
}
