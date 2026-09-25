package benchmark

import (
	"context"
	"sync"
	"time"

	"github.com/mmallorquin/bot-marangatu-facturas/internal/reader"
)

// readTimeout es el máximo por lectura, igual que en el bot.
const readTimeout = 2 * time.Minute

// Execute lee cada foto con cada configuración, con hasta `parallel` lecturas a la vez.
// Devuelve las corridas en orden estable: foto por foto, configuración por configuración.
// progress se llama al terminar cada corrida (puede ser nil).
func Execute(
	ctx context.Context,
	photos []Photo,
	setups []Setup,
	newReader func(Setup) reader.Reader,
	parallel int,
	progress func(Run),
) []Run {
	readers := make([]reader.Reader, len(setups))
	for i, s := range setups {
		readers[i] = newReader(s)
	}

	runs := make([]Run, len(photos)*len(setups))
	slots := make(chan struct{}, max(parallel, 1))
	var wg sync.WaitGroup

	for p, photo := range photos {
		for s, setup := range setups {
			wg.Add(1)
			go func(index int, photo Photo, setup Setup, rdr reader.Reader) {
				defer wg.Done()
				slots <- struct{}{}
				defer func() { <-slots }()

				runs[index] = readOnce(ctx, photo, setup, rdr)
				if progress != nil {
					progress(runs[index])
				}
			}(p*len(setups)+s, photo, setup, readers[s])
		}
	}
	wg.Wait()
	return runs
}

func readOnce(ctx context.Context, photo Photo, setup Setup, rdr reader.Reader) Run {
	ctx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()

	start := time.Now()
	result, err := rdr.Read(ctx, photo.Image)
	return Run{Photo: photo.Name, Setup: setup, Result: result, Err: err, Duration: time.Since(start)}
}
