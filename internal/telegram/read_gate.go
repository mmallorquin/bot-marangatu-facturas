package telegram

import (
	"context"
	"sync"
)

// readSlots son los lectores simultáneos de todo el bot. No se amplía hasta medir (B04).
const readSlots = 2

// readGate reparte los lectores entre chats. Cuando se libera un lugar, lo recibe el chat
// que menos lecturas tiene en curso (a igualdad, el que esperó más): un chat con muchos
// archivos en cola no deja sin turno a otro que llega después, y si nadie más espera
// puede usar los dos lugares.
type readGate struct {
	mu      sync.Mutex
	free    int
	active  map[int64]int // lecturas en curso por chat
	waiting []*gateWaiter
}

type gateWaiter struct {
	chatID  int64
	granted chan struct{}
}

func newReadGate(slots int) *readGate {
	return &readGate{free: slots, active: make(map[int64]int)}
}

// acquire espera un lugar para el chat y devuelve la función que lo libera.
// Si ctx se cancela antes de recibirlo, no deja nada tomado.
func (g *readGate) acquire(ctx context.Context, chatID int64) (func(), error) {
	w := &gateWaiter{chatID: chatID, granted: make(chan struct{})}
	g.mu.Lock()
	g.waiting = append(g.waiting, w)
	g.grantLocked()
	g.mu.Unlock()

	select {
	case <-w.granted:
		return func() { g.release(chatID) }, nil
	case <-ctx.Done():
		g.mu.Lock()
		stillWaiting := g.removeLocked(w)
		g.mu.Unlock()
		if !stillWaiting { // el lugar se concedió justo antes de cancelar
			g.release(chatID)
		}
		return nil, ctx.Err()
	}
}

func (g *readGate) release(chatID int64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.free++
	if g.active[chatID]--; g.active[chatID] <= 0 {
		delete(g.active, chatID)
	}
	g.grantLocked()
}

// queued es la cantidad de lecturas esperando un lugar.
func (g *readGate) queued() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.waiting)
}

func (g *readGate) grantLocked() {
	for g.free > 0 && len(g.waiting) > 0 {
		next := 0
		for i, w := range g.waiting {
			if g.active[w.chatID] < g.active[g.waiting[next].chatID] {
				next = i
			}
		}
		w := g.waiting[next]
		g.waiting = append(g.waiting[:next:next], g.waiting[next+1:]...)
		g.free--
		g.active[w.chatID]++
		close(w.granted)
	}
}

// removeLocked saca a w de la espera; false si ya no estaba.
func (g *readGate) removeLocked(w *gateWaiter) bool {
	for i, other := range g.waiting {
		if other == w {
			g.waiting = append(g.waiting[:i:i], g.waiting[i+1:]...)
			return true
		}
	}
	return false
}
