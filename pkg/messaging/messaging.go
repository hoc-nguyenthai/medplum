// Package messaging định nghĩa port Broker và adapter: in-memory | redis (Streams).
package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"trivita-fhir/pkg/core"
)

// Envelope là dạng event truyền qua broker (serialize được, không phụ thuộc kiểu domain).
type Envelope struct {
	ID         string          `json:"id"`
	Type       string          `json:"type"`
	OccurredAt time.Time       `json:"occurred_at"`
	Payload    json.RawMessage `json:"payload"`
}

// NewEnvelope đóng gói một domain event; payload là JSON của chính event.
func NewEnvelope(e core.Event) (Envelope, error) {
	payload, err := json.Marshal(e)
	if err != nil {
		return Envelope{}, fmt.Errorf("messaging: mã hóa event %s: %w", e.EventType(), err)
	}
	return Envelope{ID: e.EventID(), Type: e.EventType(), OccurredAt: e.OccurredAtTime(), Payload: payload}, nil
}

// Decode giải mã payload vào v.
func (e Envelope) Decode(v any) error { return json.Unmarshal(e.Payload, v) }

// Handler xử lý một event. Phải idempotent vì broker có thể giao lại.
type Handler func(ctx context.Context, env Envelope) error

// Broker là port message broker. Topic mặc định = EventType() của domain event.
type Broker interface {
	Publish(ctx context.Context, e core.Event) error
	// Subscribe phải gọi trước Start.
	Subscribe(topic string, h Handler)
	// Ping kiểm tra broker còn dùng được (cho readiness).
	Ping(ctx context.Context) error
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
}

// Broadcaster là Broker hỗ trợ giao MỌI event tới MỌI instance (không chia nhau theo consumer group), dùng cho việc
// mỗi instance phải tự xử lý như đẩy tới kết nối websocket đang giữ. Không bền: instance chỉ nhận event phát sau khi
// Start (không đọc lại event cũ, không ACK); event phát khi instance đang tắt sẽ bị bỏ qua. Subscribe vẫn là
// at-least-once theo nhóm. SubscribeBroadcast phải gọi trước Start.
type Broadcaster interface {
	SubscribeBroadcast(topic string, h Handler)
}

// Config cấu hình factory New.
type Config struct {
	Driver   string // "memory" (mặc định) | "redis"
	RedisURL string
	Group    string // consumer group (redis); mặc định "trivita"
	// Concurrency (redis) là số message xử lý song song trong một instance; mặc định 1 (lần lượt).
	Concurrency int
	// ClaimIdle (redis) là thời gian một message nằm trong PEL không được ACK thì instance khác nhận lại; mặc định 30s. Phải LỚN HƠN thời gian xử lý lâu nhất của
	// một handler, nếu không message đang xử lý bị xử lý trùng ở instance khác.
	ClaimIdle time.Duration
	// ClaimInterval (redis) là chu kỳ instance này quét PEL để nhận lại message idle; mặc định 10s.
	ClaimInterval time.Duration
}

// New tạo broker theo Config.
func New(cfg Config) (Broker, error) {
	switch cfg.Driver {
	case "", "memory":
		return NewInMemory(), nil
	case "redis":
		if cfg.RedisURL == "" {
			return nil, errors.New("messaging: RedisURL là bắt buộc khi driver=redis")
		}
		return NewRedis(cfg.RedisURL, RedisOptions{Group: cfg.Group, Concurrency: cfg.Concurrency, ClaimIdle: cfg.ClaimIdle, ClaimInterval: cfg.ClaimInterval})
	default:
		return nil, fmt.Errorf("messaging: driver không được hỗ trợ: %q", cfg.Driver)
	}
}
