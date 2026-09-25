// Package benchmark compara modelos y niveles de razonamiento leyendo las mismas fotos.
// Nunca muestra datos de las facturas: solo conteos, costo y tiempo.
package benchmark

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mmallorquin/bot-marangatu-facturas/internal/invoice"
	"github.com/mmallorquin/bot-marangatu-facturas/internal/reader"
)

// mayoría mínima para considerar que hay consenso sobre una foto.
const minVotesForConsensus = 2

var mimeByExtension = map[string]string{
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".png":  "image/png",
	".webp": "image/webp",
}

// Setup es una combinación de modelo y nivel de razonamiento a probar.
type Setup struct {
	Model  string
	Effort string // vacío = valor por defecto del modelo
}

// Label identifica la configuración en las tablas.
func (s Setup) Label() string {
	effort := s.Effort
	if effort == "" {
		effort = "por defecto"
	}
	return fmt.Sprintf("%s (%s)", s.Model, effort)
}

// Photo es una imagen a leer.
type Photo struct {
	Name  string
	Image reader.Image
}

// Run es el resultado de leer una foto con una configuración.
type Run struct {
	Photo    string
	Setup    Setup
	Result   reader.Result
	Err      error
	Duration time.Duration
}

// Summary resume todas las corridas de una configuración.
type Summary struct {
	Setup              Setup
	Photos             int
	Clean              int // factura sin problemas de validación ni campos dudosos
	WithIssues         int
	NotInvoice         int
	Errors             int
	Agrees             int // fotos donde RUC, número y total coinciden con la mayoría
	TotalCostUSD       float64
	AvgSeconds         float64
	MaxSeconds         float64
	AvgReasoningTokens int
}

// LoadPhotos lee las imágenes soportadas de dir, ordenadas por nombre.
func LoadPhotos(dir string) ([]Photo, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("leyendo la carpeta %s: %w", dir, err)
	}

	var photos []Photo
	for _, entry := range entries {
		mime, ok := mimeByExtension[strings.ToLower(filepath.Ext(entry.Name()))]
		if entry.IsDir() || !ok {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("leyendo %s: %w", entry.Name(), err)
		}
		photos = append(photos, Photo{Name: entry.Name(), Image: reader.Image{Data: data, MimeType: mime}})
	}
	return photos, nil
}

// outcome clasifica una corrida.
type outcome int

const (
	outcomeClean outcome = iota
	outcomeIssues
	outcomeNotInvoice
	outcomeError
)

func classify(r Run) (outcome, int) {
	if r.Err != nil {
		return outcomeError, 0
	}
	if !r.Result.Invoice.IsInvoice {
		return outcomeNotInvoice, 0
	}
	problems := len(invoice.Validate(r.Result.Invoice)) + len(r.Result.Invoice.UncertainFields)
	if problems > 0 {
		return outcomeIssues, problems
	}
	return outcomeClean, 0
}

// Summarize agrupa las corridas por configuración, en el orden en que aparecen.
func Summarize(runs []Run) []Summary {
	majority := consensus(runs)

	var order []Setup
	bySetup := map[Setup]*Summary{}
	reasoningRuns := map[Setup]int{}
	for _, r := range runs {
		s, ok := bySetup[r.Setup]
		if !ok {
			s = &Summary{Setup: r.Setup}
			bySetup[r.Setup] = s
			order = append(order, r.Setup)
		}
		accumulate(s, r, majority)
		if r.Err == nil {
			reasoningRuns[r.Setup]++
		}
	}

	summaries := make([]Summary, 0, len(order))
	for _, setup := range order {
		s := *bySetup[setup]
		s.AvgSeconds /= float64(s.Photos)
		if n := reasoningRuns[setup]; n > 0 {
			s.AvgReasoningTokens /= n
		}
		summaries = append(summaries, s)
	}
	return summaries
}

// accumulate suma una corrida al resumen (AvgSeconds y AvgReasoningTokens quedan como sumas).
func accumulate(s *Summary, r Run, majority map[string]string) {
	s.Photos++
	s.TotalCostUSD += r.Result.CostUSD
	seconds := r.Duration.Seconds()
	s.AvgSeconds += seconds
	s.MaxSeconds = max(s.MaxSeconds, seconds)
	s.AvgReasoningTokens += r.Result.Usage.ReasoningTokens

	kind, _ := classify(r)
	switch kind {
	case outcomeClean:
		s.Clean++
	case outcomeIssues:
		s.WithIssues++
	case outcomeNotInvoice:
		s.NotInvoice++
	case outcomeError:
		s.Errors++
	}

	if key, ok := keyFields(r); ok && majority[r.Photo] == key {
		s.Agrees++
	}
}

// keyFields es la "huella" de una lectura para comparar modelos entre sí.
func keyFields(r Run) (string, bool) {
	if r.Err != nil || !r.Result.Invoice.IsInvoice {
		return "", false
	}
	inv := r.Result.Invoice
	return fmt.Sprintf("%s|%s|%d", inv.IssuerRUC, inv.Number, inv.Total), true
}

// consensus devuelve, por foto, la lectura en la que coincide la mayoría de los modelos.
func consensus(runs []Run) map[string]string {
	votes := map[string]map[string]int{}
	for _, r := range runs {
		key, ok := keyFields(r)
		if !ok {
			continue
		}
		if votes[r.Photo] == nil {
			votes[r.Photo] = map[string]int{}
		}
		votes[r.Photo][key]++
	}

	majority := map[string]string{}
	for photo, counts := range votes {
		best, bestVotes := "", 0
		for key, n := range counts {
			if n > bestVotes {
				best, bestVotes = key, n
			}
		}
		if bestVotes >= minVotesForConsensus {
			majority[photo] = best
		}
	}
	return majority
}
