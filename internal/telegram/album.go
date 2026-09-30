package telegram

import (
	"sync"
	"time"
)

// Un álbum llega como varios mensajes con el mismo media_group_id, casi al mismo tiempo.
// Se recuerda cada álbum un rato para avisar "leyendo" una sola vez.
const albumMemory = 2 * time.Minute

// albums recuerda qué álbumes ya recibieron el aviso de "leyendo".
type albums struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

// first indica si es la primera foto que llega de ese álbum.
func (a *albums) first(groupID string, now time.Time) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.seen == nil {
		a.seen = map[string]time.Time{}
	}
	for id, at := range a.seen {
		if now.Sub(at) > albumMemory {
			delete(a.seen, id)
		}
	}
	if _, ok := a.seen[groupID]; ok {
		return false
	}
	a.seen[groupID] = now
	return true
}
