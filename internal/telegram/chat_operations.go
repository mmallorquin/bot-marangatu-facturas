package telegram

import (
	"context"
	"sync"
)

// ChatOperations serializa cambios por chat y limita las lecturas externas.
// El handler y los recordatorios comparten esta instancia.
type ChatOperations struct {
	mu    sync.Mutex
	chats map[int64]*chatOperation
	reads chan struct{}
}

type chatOperation struct {
	mu          sync.Mutex
	generation  uint64
	reads       map[*readOperation]context.CancelFunc
	deleteToken string
}

type readOperation struct {
	chat       *chatOperation
	generation uint64
}

type chatContextKey struct{}

func NewChatOperations() *ChatOperations {
	return &ChatOperations{chats: make(map[int64]*chatOperation), reads: make(chan struct{}, 2)}
}

func (ops *ChatOperations) chat(chatID int64) *chatOperation {
	ops.mu.Lock()
	defer ops.mu.Unlock()
	c := ops.chats[chatID]
	if c == nil {
		c = &chatOperation{reads: make(map[*readOperation]context.CancelFunc)}
		ops.chats[chatID] = c
	}
	return c
}

func operationContext(ctx context.Context, c *chatOperation) context.Context {
	return context.WithValue(ctx, chatContextKey{}, &readOperation{chat: c, generation: c.generation})
}

func currentOperation(ctx context.Context) *readOperation {
	op, _ := ctx.Value(chatContextKey{}).(*readOperation)
	return op
}

// withChat protege una operación corta: los accesos a SQLite y sus respuestas.
// Las descargas y la IA liberan este bloqueo mientras esperan servicios externos.
func (h *handler) withChat(ctx context.Context, chatID int64, run func(context.Context)) {
	c := h.deps.Operations.chat(chatID)
	c.mu.Lock()
	defer c.mu.Unlock()
	run(operationContext(ctx, c))
}

// invalidateReads se llama dentro de withChat tras un borrado exitoso.
func invalidateReads(ctx context.Context) {
	op := currentOperation(ctx)
	op.chat.generation++
	for _, cancel := range op.chat.reads {
		cancel()
	}
	op.chat.deleteToken = ""
}
