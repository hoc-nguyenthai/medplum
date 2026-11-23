package fhircast

import (
	"context"
	"log/slog"
	"sync"
)

// Handler xử lý một thông điệp kênh. Chạy tuần tự theo từng đăng ký; panic chỉ được log.
type Handler func(payload []byte)

// Bus là kênh pub/sub động theo tên, không bền (at-most-once): thông điệp chỉ tới đăng ký đang sống lúc phát. Mỗi instance
// giữ MỘT kết nối dùng chung cho mọi kênh (Medplum mở một kết nối Redis cho mỗi socket).
type Bus interface {
	Publish(ctx context.Context, channel string, payload []byte) error
	// Subscribe đăng ký nghe kênh; khi trả nil lỗi thì đăng ký đã có hiệu lực (thông điệp phát sau đó sẽ tới h). cancel hủy
	// đăng ký, chờ h đang chạy xong, gọi nhiều lần an toàn. Nhiều đăng ký cùng kênh dùng chung một đăng ký phía Redis (đếm
	// tham chiếu).
	Subscribe(ctx context.Context, channel string, h Handler) (cancel func(), err error)
}

// Channel là tên kênh của (project, topic): chứa project nên topic tự chọn không thể trùng kênh của project khác.
func Channel(project, topic string) string { return "fhircast:ch:" + project + ":" + TopicHash(topic) }

const subBuffer = 256

// sub là một đăng ký: hàng đợi riêng nên handler chậm của người này không chặn người khác; đầy thì bỏ (không bền).
type sub struct {
	in   chan []byte
	stop chan struct{}
	done chan struct{}
	once sync.Once
}

func newSub(h Handler) *sub {
	s := &sub{in: make(chan []byte, subBuffer), stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(s.done)
		for {
			select {
			case p := <-s.in:
				call(h, p)
			case <-s.stop:
				return
			}
		}
	}()
	return s
}

func (s *sub) offer(channel string, p []byte) {
	select {
	case s.in <- p:
	default:
		slog.Warn("fhircast: hàng đợi đăng ký đầy, bỏ thông điệp", "channel", channel)
	}
}

func (s *sub) close() {
	s.once.Do(func() { close(s.stop) })
	<-s.done
}

func call(h Handler, p []byte) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("fhircast: handler panic", "panic", r)
		}
	}()
	h(p)
}

// MemoryBus là Bus trong tiến trình (dev/test, một instance).
type MemoryBus struct {
	mu   sync.Mutex
	subs map[string]map[*sub]struct{}
}

func NewMemoryBus() *MemoryBus { return &MemoryBus{subs: map[string]map[*sub]struct{}{}} }

func (b *MemoryBus) Publish(_ context.Context, channel string, payload []byte) error {
	b.mu.Lock()
	targets := make([]*sub, 0, len(b.subs[channel]))
	for s := range b.subs[channel] {
		targets = append(targets, s)
	}
	b.mu.Unlock()
	for _, s := range targets {
		s.offer(channel, append([]byte(nil), payload...))
	}
	return nil
}

func (b *MemoryBus) Subscribe(_ context.Context, channel string, h Handler) (func(), error) {
	s := newSub(h)
	b.mu.Lock()
	if b.subs[channel] == nil {
		b.subs[channel] = map[*sub]struct{}{}
	}
	b.subs[channel][s] = struct{}{}
	b.mu.Unlock()
	return func() {
		b.mu.Lock()
		delete(b.subs[channel], s)
		if len(b.subs[channel]) == 0 {
			delete(b.subs, channel)
		}
		b.mu.Unlock()
		s.close()
	}, nil
}
