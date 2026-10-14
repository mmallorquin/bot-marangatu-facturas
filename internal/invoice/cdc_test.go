package invoice

import (
	"errors"
	"strings"
	"testing"
)

// buildCDC arma un CDC coherente con la factura, con el DV calculado por el mismo módulo 11.
// No hay un CDC real verificado a mano en el repo: estos vectores prueban la consistencia interna.
func buildCDC(rucBase string, rucDV int, number, date string) string {
	parts := strings.Split(number, "-")
	body := "01" + strings.Repeat("0", 8-len(rucBase)) + rucBase + string(rune('0'+rucDV)) +
		parts[0] + parts[1] + parts[2] + "2" + strings.ReplaceAll(date, "-", "") + "1" + "123456789"
	return body + string(rune('0'+CheckDigit(body)))
}

func TestParseCDCSplitsTheFields(t *testing.T) {
	raw := buildCDC("80000519", 8, "001-001-0001234", "2026-09-20")

	cdc, err := ParseCDC("0180 " + raw[4:]) // con un espacio, como se copia del KuDE

	if err != nil {
		t.Fatalf("ParseCDC: %v", err)
	}
	if cdc.DocumentType != "01" || cdc.RUC() != "80000519-8" || cdc.Number() != "001-001-0001234" ||
		cdc.Date != "2026-09-20" || cdc.TaxpayerType != "2" || cdc.EmissionType != "1" || cdc.Security != "123456789" {
		t.Errorf("campos = %+v", cdc)
	}
	if !cdc.CheckDigitOK() {
		t.Error("el DV calculado debería cerrar")
	}
	altered := raw[:43] + string(rune('0'+(cdc.CheckDigit+1)%10))
	if parsed, _ := ParseCDC(altered); parsed.CheckDigitOK() {
		t.Error("un DV cambiado no debería cerrar")
	}
	for _, bad := range []string{"", "0123", raw + "1", strings.Replace(raw, raw[25:33], "20261340", 1)} {
		if _, err := ParseCDC(bad); !errors.Is(err, ErrInvalidCDC) {
			t.Errorf("ParseCDC(%q) debería fallar: %v", bad, err)
		}
	}
}

func TestWarningsCrossCheckTheCDC(t *testing.T) {
	inv := validInvoice() // RUC 80000519-8, número 001-001-0001234, fecha 2026-09-20
	inv.CDC = buildCDC("80000519", 8, "001-001-0001234", "2026-09-20")
	if w := Warnings(inv); len(w) != 0 {
		t.Fatalf("todo coincide, no debería avisar: %+v", w)
	}

	cases := map[string]struct {
		modify func(*Invoice)
		field  string
	}{
		"RUC distinto":    {func(i *Invoice) { i.IssuerRUC = "80024627-6" }, FieldIssuerRUC},
		"número distinto": {func(i *Invoice) { i.Number = "001-001-0009999" }, FieldNumber},
		"fecha distinta":  {func(i *Invoice) { i.Date = "2026-09-21" }, FieldDate},
		"DV del CDC mal": {func(i *Invoice) {
			i.CDC = i.CDC[:43] + "0"
			if i.CDC[43] == inv.CDC[43] {
				i.CDC = i.CDC[:43] + "1"
			}
		}, FieldCDC},
	}
	for name, tc := range cases {
		changed := inv
		tc.modify(&changed)
		if w := Warnings(changed); len(w) != 1 || w[0].Field != tc.field {
			t.Errorf("%s: avisos = %+v, se esperaba uno en %q", name, w, tc.field)
		}
		if len(Validate(changed)) != 0 {
			t.Errorf("%s: el cruce con el CDC avisa, no bloquea", name)
		}
	}
}

func TestReconcileRUCWithCDCFixesOnlyTheCheckDigit(t *testing.T) {
	inv := validInvoice()
	inv.CDC = buildCDC("80000519", 8, "001-001-0001234", "2026-09-20")

	wrongDV := inv
	wrongDV.IssuerRUC = "80000519-1"
	fixed, ok := ReconcileRUCWithCDC(wrongDV)
	if !ok || fixed.IssuerRUC != "80000519-8" {
		t.Errorf("con la misma base, el CDC confirma el DV: %q, %v", fixed.IssuerRUC, ok)
	}

	wrongBase := inv
	wrongBase.IssuerRUC = "80000513-8" // un dígito de la base distinto: no se toca
	if _, ok := ReconcileRUCWithCDC(wrongBase); ok {
		t.Error("si la base difiere, el bot no decide")
	}

	alreadyOK := inv
	if _, ok := ReconcileRUCWithCDC(alreadyOK); ok {
		t.Error("sin diferencia no hay corrección")
	}

	badCDC := wrongDV
	badCDC.CDC = badCDC.CDC[:43] + "9"
	if badCDC.CDC == wrongDV.CDC {
		badCDC.CDC = badCDC.CDC[:43] + "8"
	}
	if _, ok := ReconcileRUCWithCDC(badCDC); ok {
		t.Error("un CDC cuyo DV no cierra no sirve para confirmar nada")
	}

	noCDC := wrongDV
	noCDC.CDC = ""
	if _, ok := ReconcileRUCWithCDC(noCDC); ok {
		t.Error("sin CDC no hay corrección automática")
	}
}
