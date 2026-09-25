package benchmark

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mmallorquin/bot-marangatu-facturas/internal/invoice"
	"github.com/mmallorquin/bot-marangatu-facturas/internal/reader"
)

var (
	deepseek = Setup{Model: "deepseek/deepseek-v4.1-flash"}
	gemini   = Setup{Model: "google/gemini-3.1-flash-lite", Effort: "low"}
	haiku    = Setup{Model: "anthropic/claude-haiku-4.5", Effort: "none"}
)

// validInvoice pasa todas las validaciones.
func validInvoice(total int64) invoice.Invoice {
	return invoice.Invoice{
		IsInvoice: true, IssuerRUC: "80000519-8", Timbrado: "12345678", Number: "001-001-0001234",
		Date: "2026-09-20", Condition: invoice.ConditionCash, Currency: invoice.CurrencyPYG,
		Taxed10: total, VAT10: total / 11, Total: total,
	}
}

func run(photo string, setup Setup, inv invoice.Invoice, cost float64, seconds int) Run {
	return Run{
		Photo:    photo,
		Setup:    setup,
		Result:   reader.Result{Invoice: inv, CostUSD: cost, Usage: reader.Usage{ReasoningTokens: 100}},
		Duration: time.Duration(seconds) * time.Second,
	}
}

func sampleRuns() []Run {
	misread := validInvoice(150_000)
	misread.Total = 160_000 // leyó mal el total → no suma y no coincide con la mayoría

	return []Run{
		run("a.jpg", deepseek, validInvoice(150_000), 0.001, 5),
		run("a.jpg", gemini, validInvoice(150_000), 0.002, 3),
		run("a.jpg", haiku, misread, 0.003, 4),
		run("b.jpg", deepseek, invoice.Invoice{IsInvoice: false}, 0.001, 10),
		run("b.jpg", gemini, invoice.Invoice{IsInvoice: false}, 0.002, 2),
		{Photo: "b.jpg", Setup: haiku, Err: errors.New("timeout"), Duration: 90 * time.Second},
	}
}

func TestSummarizeGroupsBySetupInOrderOfAppearance(t *testing.T) {
	// Act
	summaries := Summarize(sampleRuns())

	// Assert
	if len(summaries) != 3 {
		t.Fatalf("se esperaban 3 configuraciones, hay %d", len(summaries))
	}
	got := []Setup{summaries[0].Setup, summaries[1].Setup, summaries[2].Setup}
	if !slices.Equal(got, []Setup{deepseek, gemini, haiku}) {
		t.Errorf("orden = %v", got)
	}
}

func TestSummarizeCountsOutcomesCostAndTime(t *testing.T) {
	// Act
	byModel := map[string]Summary{}
	for _, s := range Summarize(sampleRuns()) {
		byModel[s.Setup.Model] = s
	}

	// Assert
	ds := byModel[deepseek.Model]
	if ds.Photos != 2 || ds.Clean != 1 || ds.NotInvoice != 1 || ds.WithIssues != 0 || ds.Errors != 0 {
		t.Errorf("conteos de DeepSeek = %+v", ds)
	}
	if ds.TotalCostUSD != 0.002 || ds.AvgSeconds != 7.5 || ds.MaxSeconds != 10 || ds.AvgReasoningTokens != 100 {
		t.Errorf("costo/tiempo de DeepSeek = %+v", ds)
	}

	hk := byModel[haiku.Model]
	if hk.WithIssues != 1 || hk.Errors != 1 || hk.Clean != 0 {
		t.Errorf("conteos de Haiku = %+v", hk)
	}
}

func TestSummarizeMeasuresAgreementWithTheMajority(t *testing.T) {
	byModel := map[string]Summary{}
	for _, s := range Summarize(sampleRuns()) {
		byModel[s.Setup.Model] = s
	}

	// En a.jpg DeepSeek y Gemini coinciden (mayoría); Haiku leyó otro total.
	// En b.jpg no hay facturas, así que no cuenta para el acuerdo.
	if byModel[deepseek.Model].Agrees != 1 || byModel[gemini.Model].Agrees != 1 || byModel[haiku.Model].Agrees != 0 {
		t.Errorf("acuerdos = deepseek %d, gemini %d, haiku %d",
			byModel[deepseek.Model].Agrees, byModel[gemini.Model].Agrees, byModel[haiku.Model].Agrees)
	}
}

func TestSetupLabel(t *testing.T) {
	if deepseek.Label() != "deepseek/deepseek-v4.1-flash (por defecto)" {
		t.Errorf("Label() = %q", deepseek.Label())
	}
	if haiku.Label() != "anthropic/claude-haiku-4.5 (none)" {
		t.Errorf("Label() = %q", haiku.Label())
	}
}

func TestFormatTablesShowsSummaryAndPerPhotoMatrixWithoutInvoiceData(t *testing.T) {
	runs := sampleRuns()

	text := FormatTables(Summarize(runs), runs)

	for _, want := range []string{"| Configuración |", "a.jpg", "b.jpg", "✅", "⚠️", "🤔", "❌"} {
		if !strings.Contains(text, want) {
			t.Errorf("falta %q en:\n%s", want, text)
		}
	}
	for _, private := range []string{"80000519-8", "12345678", "150000", "150.000"} {
		if strings.Contains(text, private) {
			t.Errorf("la tabla no debe mostrar datos de la factura (%q):\n%s", private, text)
		}
	}
}

func TestLoadPhotosReturnsSupportedImagesSorted(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	for _, name := range []string{"b.jpg", "a.PNG", "c.webp", "notas.txt", "d.jpeg"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// Act
	photos, err := LoadPhotos(dir)

	// Assert
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	var names, mimes []string
	for _, p := range photos {
		names = append(names, p.Name)
		mimes = append(mimes, p.Image.MimeType)
	}
	if !slices.Equal(names, []string{"a.PNG", "b.jpg", "c.webp", "d.jpeg"}) {
		t.Errorf("fotos = %v", names)
	}
	if !slices.Equal(mimes, []string{"image/png", "image/jpeg", "image/webp", "image/jpeg"}) {
		t.Errorf("tipos = %v", mimes)
	}
}

func TestLoadPhotosFailsOnMissingFolder(t *testing.T) {
	if _, err := LoadPhotos(filepath.Join(t.TempDir(), "no-existe")); err == nil {
		t.Error("se esperaba un error")
	}
}
