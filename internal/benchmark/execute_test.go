package benchmark

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mmallorquin/bot-marangatu-facturas/internal/reader"
)

// countingReader cuenta cuántas lecturas corren a la vez.
type countingReader struct {
	running, peak *atomic.Int32
	setup         Setup
}

func (r countingReader) Read(ctx context.Context, img reader.Image) (reader.Result, error) {
	now := r.running.Add(1)
	defer r.running.Add(-1)
	for {
		peak := r.peak.Load()
		if now <= peak || r.peak.CompareAndSwap(peak, now) {
			break
		}
	}
	time.Sleep(10 * time.Millisecond)
	return reader.Result{Model: r.setup.Model + "|" + string(img.Data)}, nil
}

func TestExecuteRunsEveryPhotoWithEverySetupInStableOrder(t *testing.T) {
	// Arrange
	photos := []Photo{
		{Name: "a.jpg", Image: reader.Image{Data: []byte("a")}},
		{Name: "b.jpg", Image: reader.Image{Data: []byte("b")}},
		{Name: "c.jpg", Image: reader.Image{Data: []byte("c")}},
	}
	setups := []Setup{deepseek, gemini}
	var running, peak atomic.Int32
	newReader := func(s Setup) reader.Reader { return countingReader{&running, &peak, s} }

	var mu sync.Mutex
	var progressCalls int
	progress := func(Run) { mu.Lock(); progressCalls++; mu.Unlock() }

	// Act
	runs := Execute(context.Background(), photos, setups, newReader, 2, progress)

	// Assert
	want := []string{
		"a.jpg " + deepseek.Model + "|a", "a.jpg " + gemini.Model + "|a",
		"b.jpg " + deepseek.Model + "|b", "b.jpg " + gemini.Model + "|b",
		"c.jpg " + deepseek.Model + "|c", "c.jpg " + gemini.Model + "|c",
	}
	if len(runs) != len(want) {
		t.Fatalf("se esperaban %d corridas, hubo %d", len(want), len(runs))
	}
	for i, r := range runs {
		if got := r.Photo + " " + r.Result.Model; got != want[i] {
			t.Errorf("corrida %d = %q, se esperaba %q", i, got, want[i])
		}
	}
	if peak.Load() > 2 {
		t.Errorf("se esperaban como máximo 2 lecturas a la vez, hubo %d", peak.Load())
	}
	if progressCalls != len(want) {
		t.Errorf("progress se llamó %d veces", progressCalls)
	}
}
