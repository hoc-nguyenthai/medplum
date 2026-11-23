package messaging

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"trivita-fhir/pkg/core"
)

// RedisOptions tùy chỉnh RedisBroker; trường để trống dùng giá trị mặc định.
type RedisOptions struct {
	Group  string        // mặc định "trivita"
	Prefix string        // mặc định "trivita:events"
	Block  time.Duration // thời gian chờ mỗi lần đọc, mặc định 1s

	// Xử lý lại message lỗi: message handler báo lỗi (hoặc consumer chết giữa chừng) nằm trong PEL; cứ mỗi ClaimInterval
	// instance này nhận lại những message idle ≥ ClaimIdle để chạy lại. Sau MaxDeliveries lần giao mà vẫn lỗi, message
	// được chuyển sang stream `{stream}:dead` và ACK (poison message không chặn hàng đợi).
	ClaimIdle     time.Duration // mặc định 30s
	ClaimInterval time.Duration // mặc định 10s
	MaxDeliveries int           // mặc định 5
}

// Redis dùng Redis Streams + consumer group: giao at-least-once, có ACK.
// Mỗi topic = 1 stream `{prefix}:{topic}`; các instance cùng group chia nhau xử lý.
type Redis struct {
	client   redis.UniversalClient
	group    string
	prefix   string
	block    time.Duration
	consumer string

	claimIdle, claimEvery time.Duration
	maxDeliveries         int

	mu       sync.RWMutex
	handlers map[string][]Handler
	bcast    map[string][]Handler // SubscribeBroadcast: mọi instance nhận mọi event

	chMu   sync.Mutex
	chSubs map[int]func() // SubscribeChannel: hủy đăng ký còn sống (Stop hủy hết)
	chNext int

	cancel context.CancelFunc
	done   chan struct{}
	bdone  chan struct{}
}

// NewRedis mở client từ URL redis://host:6379/0.
func NewRedis(url string, opts RedisOptions) (*Redis, error) {
	ro, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("messaging: REDIS_URL không hợp lệ: %w", err)
	}
	return NewRedisFromClient(redis.NewClient(ro), opts), nil
}

// NewRedisFromClient dùng client có sẵn.
func NewRedisFromClient(c redis.UniversalClient, opts RedisOptions) *Redis {
	if opts.Group == "" {
		opts.Group = "trivita"
	}
	if opts.Prefix == "" {
		opts.Prefix = "trivita:events"
	}
	if opts.Block <= 0 {
		opts.Block = time.Second
	}
	if opts.ClaimIdle <= 0 {
		opts.ClaimIdle = 30 * time.Second
	}
	if opts.ClaimInterval <= 0 {
		opts.ClaimInterval = 10 * time.Second
	}
	if opts.MaxDeliveries <= 0 {
		opts.MaxDeliveries = 5
	}
	return &Redis{
		claimIdle: opts.ClaimIdle, claimEvery: opts.ClaimInterval, maxDeliveries: opts.MaxDeliveries,
		client:   c,
		group:    opts.Group,
		prefix:   opts.Prefix,
		block:    opts.Block,
		consumer: opts.Group + "-" + core.NewID()[:8],
		handlers: map[string][]Handler{},
		bcast:    map[string][]Handler{},
		chSubs:   map[int]func(){},
	}
}

func (b *Redis) stream(topic string) string { return b.prefix + ":" + topic }

func (b *Redis) Subscribe(topic string, h Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers[topic] = append(b.handlers[topic], h)
}

// SubscribeBroadcast đăng ký handler nhận MỌI event của topic trên instance này (đọc stream trực tiếp bằng XREAD, không
// qua consumer group, không ACK, chỉ event phát sau Start). Xem Broadcaster.
func (b *Redis) SubscribeBroadcast(topic string, h Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.bcast[topic] = append(b.bcast[topic], h)
}

func (b *Redis) Publish(ctx context.Context, e core.Event) error {
	env, err := NewEnvelope(e)
	if err != nil {
		return err
	}
	raw, err := jsonMarshal(env)
	if err != nil {
		return err
	}
	return b.client.XAdd(ctx, &redis.XAddArgs{Stream: b.stream(env.Type), Values: map[string]any{"data": raw}}).Err()
}

// Client trả client go-redis đang dùng (cho thành phần khác dùng chung pool, ví dụ pkg/fhircast). Redis.Stop đóng client này,
// nên thành phần mượn phải được đóng TRƯỚC Stop.
func (b *Redis) Client() redis.UniversalClient { return b.client }

func (b *Redis) Ping(ctx context.Context) error {
	return b.client.Ping(ctx).Err()
}

func (b *Redis) topics() []string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	topics := make([]string, 0, len(b.handlers))
	for t := range b.handlers {
		topics = append(topics, t)
	}
	return topics
}

// Start tạo consumer group cho mọi topic đã Subscribe rồi chạy vòng consume ở nền.
func (b *Redis) Start(ctx context.Context) error {
	topics := b.topics()
	for _, t := range topics {
		err := b.client.XGroupCreateMkStream(ctx, b.stream(t), b.group, "0").Err()
		if err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
			return fmt.Errorf("messaging: tạo consumer group %s: %w", t, err)
		}
	}
	b.mu.RLock()
	btopics := make([]string, 0, len(b.bcast))
	for t := range b.bcast {
		btopics = append(btopics, t)
	}
	b.mu.RUnlock()
	if len(topics) == 0 && len(btopics) == 0 {
		return nil
	}
	// vị trí bắt đầu của luồng broadcast phải chốt TRƯỚC khi Start trả về: event phát sau đó đều được nhận
	last := map[string]string{}
	for _, t := range btopics {
		last[t] = "0-0"
		if r, err := b.client.XRevRangeN(ctx, b.stream(t), "+", "-", 1).Result(); err == nil && len(r) > 0 {
			last[t] = r[0].ID
		}
	}
	loopCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	b.cancel = cancel
	b.done = make(chan struct{})
	if len(topics) > 0 {
		go b.consume(loopCtx, topics)
	} else {
		close(b.done)
	}
	b.bdone = make(chan struct{})
	if len(btopics) > 0 {
		go b.consumeBroadcast(loopCtx, last)
	} else {
		close(b.bdone)
	}
	return nil
}

// Stop dừng consumer, chờ handler đang chạy rồi đóng client.
func (b *Redis) Stop(ctx context.Context) error {
	b.closeChannels()
	if b.cancel != nil {
		b.cancel()
		for _, ch := range []chan struct{}{b.done, b.bdone} {
			select {
			case <-ch:
			case <-ctx.Done():
			}
		}
	}
	return b.client.Close()
}

// consumeBroadcast đọc các stream bằng XREAD từ vị trí đã chốt và gọi handler broadcast; lỗi handler chỉ được log.
func (b *Redis) consumeBroadcast(ctx context.Context, last map[string]string) {
	defer close(b.bdone)
	for ctx.Err() == nil {
		streams := make([]string, 0, 2*len(last))
		topics := make([]string, 0, len(last))
		for t := range last {
			topics = append(topics, t)
			streams = append(streams, b.stream(t))
		}
		for _, t := range topics {
			streams = append(streams, last[t])
		}
		res, err := b.client.XRead(ctx, &redis.XReadArgs{Streams: streams, Count: 50, Block: b.block}).Result()
		if err != nil {
			if errors.Is(err, redis.Nil) || ctx.Err() != nil {
				continue
			}
			slog.ErrorContext(ctx, "lỗi đọc Redis stream (broadcast), thử lại sau 1s", "error", err)
			select {
			case <-time.After(time.Second):
			case <-ctx.Done():
			}
			continue
		}
		for _, s := range res {
			topic := strings.TrimPrefix(s.Stream, b.prefix+":")
			for _, m := range s.Messages {
				last[topic] = m.ID
				raw, _ := m.Values["data"].(string)
				var env Envelope
				if err := jsonUnmarshal([]byte(raw), &env); err != nil {
					slog.ErrorContext(ctx, "message hỏng, bỏ qua", "topic", topic, "id", m.ID, "error", err)
					continue
				}
				b.mu.RLock()
				hs := append([]Handler(nil), b.bcast[topic]...)
				b.mu.RUnlock()
				for _, h := range hs {
					if err := safeCall(ctx, h, env); err != nil {
						slog.ErrorContext(ctx, "handler broadcast lỗi", "event_type", env.Type, "event_id", env.ID, "error", err)
					}
				}
			}
		}
	}
}

func (b *Redis) consume(ctx context.Context, topics []string) {
	defer close(b.done)
	streams := make([]string, 0, 2*len(topics))
	for _, t := range topics {
		streams = append(streams, b.stream(t))
	}
	for range topics {
		streams = append(streams, ">")
	}
	lastClaim := time.Now()
	for ctx.Err() == nil {
		if time.Since(lastClaim) >= b.claimEvery {
			b.reclaim(ctx, topics)
			lastClaim = time.Now()
		}
		res, err := b.client.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group: b.group, Consumer: b.consumer, Streams: streams, Count: 10, Block: b.block,
		}).Result()
		if err != nil {
			if errors.Is(err, redis.Nil) || ctx.Err() != nil {
				continue
			}
			slog.ErrorContext(ctx, "lỗi đọc Redis stream, thử lại sau 1s", "error", err)
			select {
			case <-time.After(time.Second):
			case <-ctx.Done():
			}
			continue
		}
		for _, s := range res {
			topic := strings.TrimPrefix(s.Stream, b.prefix+":")
			for _, m := range s.Messages {
				if b.dispatch(ctx, topic, m) {
					b.ack(ctx, s.Stream, m.ID)
				}
			}
		}
	}
}

// ack dùng ctx không bị hủy: handler đã xong, đừng để lần tắt máy khiến message bị giao lại.
func (b *Redis) ack(ctx context.Context, stream string, ids ...string) {
	ackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	if err := b.client.XAck(ackCtx, stream, b.group, ids...).Err(); err != nil {
		slog.ErrorContext(ctx, "XACK lỗi", "stream", stream, "ids", ids, "error", err)
	}
}

// reclaim chạy lại các message trong PEL đã idle quá ClaimIdle (kể cả của consumer đã chết); message bị giao quá
// MaxDeliveries lần chuyển sang stream dead-letter.
func (b *Redis) reclaim(ctx context.Context, topics []string) {
	for _, t := range topics {
		if ctx.Err() != nil {
			return
		}
		stream := b.stream(t)
		pend, err := b.client.XPendingExt(ctx, &redis.XPendingExtArgs{
			Stream: stream, Group: b.group, Idle: b.claimIdle, Start: "-", End: "+", Count: 20,
		}).Result()
		if err != nil {
			if ctx.Err() == nil {
				slog.ErrorContext(ctx, "XPENDING lỗi", "stream", stream, "error", err)
			}
			continue
		}
		var retry []string
		for _, p := range pend {
			if p.RetryCount >= int64(b.maxDeliveries) {
				b.deadLetter(ctx, stream, p.ID, p.RetryCount)
			} else {
				retry = append(retry, p.ID)
			}
		}
		if len(retry) == 0 {
			continue
		}
		msgs, err := b.client.XClaim(ctx, &redis.XClaimArgs{
			Stream: stream, Group: b.group, Consumer: b.consumer, MinIdle: b.claimIdle, Messages: retry,
		}).Result()
		if err != nil {
			if ctx.Err() == nil {
				slog.ErrorContext(ctx, "XCLAIM lỗi", "stream", stream, "error", err)
			}
			continue
		}
		seen := map[string]bool{}
		for _, m := range msgs {
			seen[m.ID] = true
			if b.dispatch(ctx, t, m) {
				b.ack(ctx, stream, m.ID)
			}
		}
		// message đã bị xóa khỏi stream (XTRIM/XDEL) mà vẫn nằm trong PEL: không còn gì để xử lý, ACK để dọn
		var gone []string
		for _, id := range retry {
			if !seen[id] {
				if r, err := b.client.XRange(ctx, stream, id, id).Result(); err == nil && len(r) == 0 {
					gone = append(gone, id)
				}
			}
		}
		if len(gone) > 0 {
			b.ack(ctx, stream, gone...)
		}
	}
}

// deadLetter chép message sang `{stream}:dead` rồi ACK.
func (b *Redis) deadLetter(ctx context.Context, stream, id string, deliveries int64) {
	r, err := b.client.XRange(ctx, stream, id, id).Result()
	if err != nil {
		slog.ErrorContext(ctx, "đọc message dead-letter lỗi", "stream", stream, "id", id, "error", err)
		return
	}
	if len(r) > 0 {
		values := map[string]any{"original_id": id, "deliveries": deliveries}
		for k, v := range r[0].Values {
			values[k] = v
		}
		if err := b.client.XAdd(ctx, &redis.XAddArgs{Stream: stream + ":dead", Values: values}).Err(); err != nil {
			slog.ErrorContext(ctx, "ghi dead-letter lỗi", "stream", stream, "id", id, "error", err)
			return // chưa ACK: lần sau thử lại
		}
		slog.ErrorContext(ctx, "message vượt số lần giao tối đa, chuyển vào dead-letter", "stream", stream, "id", id, "deliveries", deliveries)
	}
	b.ack(ctx, stream, id)
}

// dispatch trả về true khi mọi handler thành công (được ACK).
func (b *Redis) dispatch(ctx context.Context, topic string, m redis.XMessage) bool {
	raw, _ := m.Values["data"].(string)
	var env Envelope
	if err := jsonUnmarshal([]byte(raw), &env); err != nil {
		slog.ErrorContext(ctx, "message hỏng, bỏ qua", "topic", topic, "id", m.ID, "error", err)
		return true // không thể xử lý lại được: ACK để khỏi kẹt
	}
	b.mu.RLock()
	hs := append([]Handler(nil), b.handlers[topic]...)
	b.mu.RUnlock()
	for _, h := range hs {
		if err := safeCall(ctx, h, env); err != nil {
			// Không ACK: message nằm trong PEL để xử lý lại (reclaim sẽ chạy lại sau ClaimIdle).
			slog.ErrorContext(ctx, "handler lỗi", "event_type", env.Type, "event_id", env.ID, "error", err)
			return false
		}
	}
	return true
}

// safeCall chạy handler và biến panic thành error để vòng consume không chết.
func safeCall(ctx context.Context, h Handler, env Envelope) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("handler panic: %v", r)
		}
	}()
	return h(ctx, env)
}
