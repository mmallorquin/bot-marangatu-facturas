package main

import (
	"strings"
	"testing"
	"time"

	"github.com/mmallorquin/bot-marangatu-facturas/internal/store"
)

func TestReportDoesNotClaimAccuracyOrPhotoToSaveTime(t *testing.T) {
	text := formatReport(store.Metrics{Saved: 1, SavedClean: 1, SavedCompared: 1, MedianToSave: 5 * time.Second}, 7, 0, false)
	for _, want := range []string{"Borrador → guardada (mediana)", "5 s", "excluye descarga y lectura de IA", "Diferencias registradas", "ninguna", "no mide la exactitud"} {
		if !strings.Contains(text, want) {
			t.Errorf("falta %q en reporte", want)
		}
	}
	for _, forbidden := range []string{"Foto → guardada", "Campos mal leídos"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("reporte afirma medición no disponible: %q", forbidden)
		}
	}
}
