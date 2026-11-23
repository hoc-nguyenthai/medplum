package fhircast

import (
	"context"
	"fmt"
	"sync"

	"github.com/redis/go-redis/v9"
)

// RedisBus là Bus trên Redis Pub/Sub với MỘT PubSub (một kết nối) cho cả instance; đăng ký và hủy kênh Redis theo đếm
// tham chiếu. go-redis tự nối lại và đăng ký lại khi mất kết nối; thông điệp phát trong lúc đứt bị mất (không bền).
type RedisBus struct {
	client redis.UniversalClient
	ps     *redis.PubSub

	mu     sync.Mutex
	chans  map[string]*busChan
	closed bool
	done   chan struct{}
}

type busChan struct {
	subs  map[*sub]struct{}
	ready chan struct{} // đóng khi Redis xác nhận SUBSCRIBE
	once  sync.Once
}

// NewRedisBus tạo bus; Close để giải phóng kết nối.
func NewRedisBus(c redis.UniversalClient) *RedisBus {
	b := &RedisBus{client: c, chans: map[string]*busChan{}, done: make(chan struct{})}
	b.ps = c.Subscribe(context.Background())
	msgs := b.ps.ChannelWithSubscriptions(redis.WithChannelSize(1024))
	go func() {
		defer close(b.done)
		for m := range msgs {
			switch v := m.(type) {
			case *redis.Subscription:
				if v.Kind == "subscribe" {
					b.mu.Lock()
					if ch := b.chans[v.Channel]; ch != nil {
						ch.once.Do(func() { close(ch.ready) })
					}
					b.mu.Unlock()
				}
			case *redis.Message:
				b.mu.Lock()
				var targets []*sub
				if ch := b.chans[v.Channel]; ch != nil {
					for s := range ch.subs {
						targets = append(targets, s)
					}
				}
				b.mu.Unlock()
				for _, s := range targets {
					s.offer(v.Channel, []byte(v.Payload))
				}
			}
		}
	}()
	return b
}

func (b *RedisBus) Publish(ctx context.Context, channel string, payload []byte) error {
	return b.client.Publish(ctx, channel, payload).Err()
}

// Subscribe: xem Bus. Chờ Redis xác nhận đăng ký kênh rồi mới trả về.
func (b *RedisBus) Subscribe(ctx context.Context, channel string, h Handler) (func(), error) {
	s := newSub(h)
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		s.close()
		return nil, fmt.Errorf("fhircast: bus đã đóng")
	}
	ch := b.chans[channel]
	first := ch == nil
	if first {
		ch = &busChan{subs: map[*sub]struct{}{}, ready: make(chan struct{})}
		b.chans[channel] = ch
	}
	ch.subs[s] = struct{}{}
	b.mu.Unlock()

	cancel := func() {
		b.mu.Lock()
		last := false
		if c := b.chans[channel]; c != nil {
			delete(c.subs, s)
			if len(c.subs) == 0 {
				delete(b.chans, channel)
				last = true
			}
		}
		closed := b.closed
		b.mu.Unlock()
		if last && !closed {
			_ = b.ps.Unsubscribe(context.Background(), channel)
		}
		s.close()
	}
	if first {
		if err := b.ps.Subscribe(ctx, channel); err != nil {
			cancel()
			return nil, fmt.Errorf("fhircast: đăng ký kênh: %w", err)
		}
	}
	select {
	case <-ch.ready:
		return cancel, nil
	case <-ctx.Done():
		cancel()
		return nil, ctx.Err()
	}
}

// Close hủy mọi đăng ký và đóng kết nối.
func (b *RedisBus) Close() error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	var all []*sub
	for _, c := range b.chans {
		for s := range c.subs {
			all = append(all, s)
		}
	}
	b.chans = map[string]*busChan{}
	b.mu.Unlock()
	err := b.ps.Close()
	<-b.done
	for _, s := range all {
		s.close()
	}
	return err
}
