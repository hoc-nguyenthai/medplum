package messaging

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// ChannelHandler xử lý một thông điệp của kênh. Chạy tuần tự theo từng đăng ký (không song song với chính nó); lỗi hoặc panic
// phải do handler tự xử lý, panic chỉ được ghi log.
type ChannelHandler func(payload []byte)

// Channels là Broker hỗ trợ kênh pub/sub ĐỘNG theo tên, không bền: thông điệp chỉ tới những đăng ký đang sống lúc phát (không
// replay, không ACK, mất khi người nghe rớt hoặc khởi động lại: at-most-once). Khác Broadcaster ở chỗ đăng ký được thêm và bỏ
// bất cứ lúc nào, cho các việc như "chỉ nghe kênh của những Subscription websocket đang có kết nối".
type Channels interface {
	// PublishChannel gửi payload tới mọi đăng ký hiện có của kênh, trên mọi instance.
	PublishChannel(ctx context.Context, channel string, payload []byte) error
	// SubscribeChannel đăng ký nghe kênh. Khi trả về nil lỗi thì đăng ký đã có hiệu lực (thông điệp phát sau đó sẽ tới h).
	// ctx chỉ dùng cho bước thiết lập, không gắn với vòng đời đăng ký. cancel hủy đăng ký và chờ h đang chạy xong; gọi nhiều
	// lần an toàn.
	SubscribeChannel(ctx context.Context, channel string, h ChannelHandler) (cancel func(), err error)
}

// Presence là sổ hiện diện có TTL: "member đang giữ key". Dùng để bên phát biết có ai nghe một kênh hay không mà không phải
// hỏi từng người nghe. Mỗi mục tự hết hạn sau ttl nếu không được Mark lại (người nghe chết không để lại mục treo).
type Presence interface {
	// Mark ghi (hoặc gia hạn) member vào key, hết hạn sau ttl.
	Mark(ctx context.Context, key, member string, ttl time.Duration) error
	// Unmark gỡ member khỏi key (không có thì thôi).
	Unmark(ctx context.Context, key, member string) error
	// Present cho biết key còn ít nhất một member chưa hết hạn.
	Present(ctx context.Context, key string) (bool, error)
}

func callChannel(h ChannelHandler, payload []byte) {
	defer func() {
		if p := recover(); p != nil {
			slog.Error("messaging: handler kênh panic", "panic", p)
		}
	}()
	h(payload)
}

// ---------------------------------------------------------------- bộ nhớ

const memChannelBuffer = 256

type memSub struct {
	in   chan []byte
	stop chan struct{}
	done chan struct{}
}

type memChannels struct {
	mu   sync.Mutex
	subs map[string]map[*memSub]struct{}

	pmu      sync.Mutex
	presence map[string]map[string]time.Time
	now      func() time.Time
}

func newMemChannels() *memChannels {
	return &memChannels{subs: map[string]map[*memSub]struct{}{}, presence: map[string]map[string]time.Time{}, now: time.Now}
}

// PublishChannel giao tới đăng ký trong process; đăng ký chậm (hàng đợi đầy) bị bỏ thông điệp, đúng ngữ nghĩa không bền.
func (m *memChannels) PublishChannel(_ context.Context, channel string, payload []byte) error {
	m.mu.Lock()
	targets := make([]*memSub, 0, len(m.subs[channel]))
	for s := range m.subs[channel] {
		targets = append(targets, s)
	}
	m.mu.Unlock()
	for _, s := range targets {
		select {
		case s.in <- append([]byte(nil), payload...):
		default:
			slog.Warn("messaging: hàng đợi kênh đầy, bỏ thông điệp", "channel", channel)
		}
	}
	return nil
}

func (m *memChannels) SubscribeChannel(_ context.Context, channel string, h ChannelHandler) (func(), error) {
	s := &memSub{in: make(chan []byte, memChannelBuffer), stop: make(chan struct{}), done: make(chan struct{})}
	m.mu.Lock()
	if m.subs[channel] == nil {
		m.subs[channel] = map[*memSub]struct{}{}
	}
	m.subs[channel][s] = struct{}{}
	m.mu.Unlock()
	go func() {
		defer close(s.done)
		for {
			select {
			case p := <-s.in:
				callChannel(h, p)
			case <-s.stop:
				return
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			m.mu.Lock()
			delete(m.subs[channel], s)
			if len(m.subs[channel]) == 0 {
				delete(m.subs, channel)
			}
			m.mu.Unlock()
			close(s.stop)
		})
		<-s.done
	}, nil
}

func (m *memChannels) Mark(_ context.Context, key, member string, ttl time.Duration) error {
	m.pmu.Lock()
	defer m.pmu.Unlock()
	if m.presence[key] == nil {
		m.presence[key] = map[string]time.Time{}
	}
	m.presence[key][member] = m.now().Add(ttl)
	return nil
}

func (m *memChannels) Unmark(_ context.Context, key, member string) error {
	m.pmu.Lock()
	defer m.pmu.Unlock()
	delete(m.presence[key], member)
	if len(m.presence[key]) == 0 {
		delete(m.presence, key)
	}
	return nil
}

func (m *memChannels) Present(_ context.Context, key string) (bool, error) {
	m.pmu.Lock()
	defer m.pmu.Unlock()
	now := m.now()
	for member, exp := range m.presence[key] {
		if exp.After(now) {
			return true, nil
		}
		delete(m.presence[key], member)
	}
	delete(m.presence, key)
	return false, nil
}
