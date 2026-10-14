package invoice

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// CDC es el Código de Control de una factura electrónica (SIFEN): 44 dígitos que incluyen el RUC
// del emisor, el número del comprobante y la fecha. Sirve para cruzar esos datos con la lectura.
//
// Estructura según el Manual Técnico de SIFEN (coincide en varias fuentes; no se pudo verificar
// contra el documento oficial de la DNIT desde el entorno donde se escribió esto):
// tipo de documento (2), RUC del emisor sin DV con ceros a la izquierda (8), DV del RUC (1),
// establecimiento (3), punto de expedición (3), número (7), tipo de contribuyente (1),
// fecha AAAAMMDD (8), tipo de emisión (1), código de seguridad (9) y DV del CDC (1).
type CDC struct {
	DocumentType  string
	RUCBase       string // sin ceros a la izquierda
	RUCCheckDigit int
	Establishment string
	Point         string
	Serial        string
	TaxpayerType  string
	Date          string // AAAA-MM-DD
	EmissionType  string
	Security      string
	CheckDigit    int
	digits        string
}

const cdcLength = 44

var ErrInvalidCDC = errors.New("CDC inválido")

// ParseCDC separa los campos del CDC. No verifica el dígito verificador: para eso está CheckDigitOK.
func ParseCDC(raw string) (CDC, error) {
	digits := whitespaceNoise.Replace(strings.TrimSpace(raw))
	if len(digits) != cdcLength || !onlyDigits.MatchString(digits) {
		return CDC{}, fmt.Errorf("%w: tiene que tener 44 dígitos", ErrInvalidCDC)
	}
	date, err := time.Parse("20060102", digits[25:33])
	if err != nil {
		return CDC{}, fmt.Errorf("%w: la fecha %q no es válida", ErrInvalidCDC, digits[25:33])
	}
	rucBase := strings.TrimLeft(digits[2:10], "0")
	if rucBase == "" {
		return CDC{}, fmt.Errorf("%w: el RUC está vacío", ErrInvalidCDC)
	}
	return CDC{
		DocumentType:  digits[0:2],
		RUCBase:       rucBase,
		RUCCheckDigit: int(digits[10] - '0'),
		Establishment: digits[11:14],
		Point:         digits[14:17],
		Serial:        digits[17:24],
		TaxpayerType:  digits[24:25],
		Date:          date.Format(dateLayout),
		EmissionType:  digits[33:34],
		Security:      digits[34:43],
		CheckDigit:    int(digits[43] - '0'),
		digits:        digits,
	}, nil
}

// CheckDigitOK verifica el último dígito con módulo 11 sobre los 43 anteriores, el mismo
// algoritmo del RUC. Hasta confirmarlo con el manual oficial, un DV que no cierra es un aviso.
func (c CDC) CheckDigitOK() bool {
	return CheckDigit(c.digits[:cdcLength-1]) == c.CheckDigit
}

// RUC devuelve el RUC del emisor como figura en el CDC: base-DV.
func (c CDC) RUC() string {
	return c.RUCBase + "-" + strconv.Itoa(c.RUCCheckDigit)
}

// Number devuelve el número del comprobante en el formato 001-001-0001234.
func (c CDC) Number() string {
	return c.Establishment + "-" + c.Point + "-" + c.Serial
}

// ReconcileRUCWithCDC corrige el dígito verificador del RUC cuando el CDC, que trae el RUC
// de forma independiente, confirma la misma base con otro dígito. Es el único caso en que
// el bot corrige el RUC solo: dos lecturas independientes coinciden en la base.
// Devuelve la factura (corregida o no) y si hubo corrección.
func ReconcileRUCWithCDC(inv Invoice) (Invoice, bool) {
	cdc, err := ParseCDC(inv.CDC)
	if err != nil || !cdc.CheckDigitOK() || cdc.RUCCheckDigit != CheckDigit(cdc.RUCBase) {
		return inv, false
	}
	match := rucPattern.FindStringSubmatch(strings.ToUpper(strings.TrimSpace(inv.IssuerRUC)))
	if match == nil || strings.TrimLeft(match[1], "0") != cdc.RUCBase || int(match[2][0]-'0') == cdc.RUCCheckDigit {
		return inv, false
	}
	inv.IssuerRUC = cdc.RUC()
	return inv, true
}
