package invoice

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// rucBaseMax es el factor máximo del módulo 11 que usa la DNIT.
const rucBaseMax = 11

// El RUC es la cédula (dígitos, a veces con una letra final) + "-" + dígito verificador.
var rucPattern = regexp.MustCompile(`^(\d+[A-Z]?)-(\d)$`)

// CheckDigit calcula el dígito verificador de un RUC con el algoritmo oficial
// de la DNIT (módulo 11). Las letras se reemplazan por su código ASCII.
func CheckDigit(base string) int {
	digits := asciiDigits(strings.ToUpper(base))

	total, factor := 0, 2
	for i := len(digits) - 1; i >= 0; i-- {
		if factor > rucBaseMax {
			factor = 2
		}
		total += int(digits[i]-'0') * factor
		factor++
	}

	remainder := total % 11
	if remainder > 1 {
		return 11 - remainder
	}
	return 0
}

// asciiDigits deja los dígitos y reemplaza cada otro carácter por su código ASCII ("A" → "65").
func asciiDigits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
			continue
		}
		b.WriteString(strconv.Itoa(int(r)))
	}
	return b.String()
}

// ValidateRUC verifica el formato "base-dv" y que el dígito verificador sea correcto.
func ValidateRUC(ruc string) error {
	match := rucPattern.FindStringSubmatch(strings.ToUpper(strings.TrimSpace(ruc)))
	if match == nil {
		return fmt.Errorf("el RUC %q no tiene el formato 1234567-8", ruc)
	}

	base, got := match[1], int(match[2][0]-'0')
	if want := CheckDigit(base); got != want {
		return fmt.Errorf("el dígito verificador del RUC %q debería ser %d", ruc, want)
	}
	return nil
}
