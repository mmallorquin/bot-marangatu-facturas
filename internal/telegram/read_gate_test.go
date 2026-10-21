package telegram

import (
	"context"
	"testing"
	"time"
)

// grab pide un lugar en otra goroutine y avisa cuando lo recibe.
func grab(g *readGate, chatID int64) <-chan func() {
	got := make(chan func(), 1)
	go func() {
		if release, err := g.acquire(context.Background(), chatID); err == nil {
			got <- release
		}
	}()
	return got
}

func waitQueued(t *testing.T, g *readGate, n int) {
	t.Helper()
	waitFor(t, func() bool { return g.queued() == n })
}

func TestReadGateGivesFreedSlotToTheChatWithFewerReads(t *testing.T) {
	g := newReadGate(2)
	a1, a2 := grab(g, 1), grab(g, 1)
	releaseA := <-a1
	<-a2
	a3 := grab(g, 1)
	waitQueued(t, g, 1)
	b1 := grab(g, 2)
	waitQueued(t, g, 2)

	releaseA()

	select {
	case <-b1:
	case <-a3:
		t.Fatal("el chat A volvió a quedarse con el lugar mientras B esperaba")
	case <-time.After(time.Second):
		t.Fatal("nadie recibió el lugar liberado")
	}
}

func TestReadGateLetsOneChatUseEverySlotWhenAlone(t *testing.T) {
	g := newReadGate(2)
	<-grab(g, 1)
	select {
	case <-grab(g, 1):
	case <-time.After(time.Second):
		t.Fatal("el único chat con trabajo no pudo usar el segundo lugar")
	}
}

func TestReadGateCancelledWaiterLeavesNothingTaken(t *testing.T) {
	g := newReadGate(1)
	release, _ := g.acquire(context.Background(), 1)
	ctx, cancel := context.WithCancel(context.Background())
	failed := make(chan error, 1)
	go func() { _, err := g.acquire(ctx, 2); failed <- err }()
	waitQueued(t, g, 1)

	cancel()
	if err := <-failed; err == nil {
		t.Fatal("la espera cancelada devolvió un lugar")
	}
	release()

	select {
	case <-grab(g, 3):
	case <-time.After(time.Second):
		t.Fatal("el lugar quedó tomado por la espera cancelada")
	}
}
