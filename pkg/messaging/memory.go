package messaging

import (
	"context"
	"log/slog"
	"sync"

	"trivita-fhir/pkg/core"
)

// InMemory dispatch đồng bộ trong process. Lỗi handler được log, không làm hỏng luồng chính.
type InMemory struct {
	mu           sync.RWMutex
	handlers     map[string][]Handler
	*memChannels // Channels và Presence trong process
}

func NewInMemory() *InMemory {
	return &InMemory{handlers: map[string][]Handler{}, memChannels: newMemChannels()}
}

func (b *InMemory) Subscribe(topic string, h Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers[topic] = append(b.handlers[topic], h)
}

// SubscribeBroadcast: broker trong process chỉ có một instance nên giống Subscribe.
func (b *InMemory) SubscribeBroadcast(topic string, h Handler) { b.Subscribe(topic, h) }

func (b *InMemory) Publish(ctx context.Context, e core.Event) error {
	env, err := NewEnvelope(e)
	if err != nil {
		return err
	}
	b.mu.RLock()
	hs := append([]Handler(nil), b.handlers[env.Type]...)
	b.mu.RUnlock()
	for _, h := range hs {
		if err := h(ctx, env); err != nil {
			slog.ErrorContext(ctx, "handler lỗi", "event_type", env.Type, "event_id", env.ID, "error", err)
		}
	}
	return nil
}

func (b *InMemory) Ping(context.Context) error  { return nil }
func (b *InMemory) Start(context.Context) error { return nil }
func (b *InMemory) Stop(context.Context) error  { return nil }
