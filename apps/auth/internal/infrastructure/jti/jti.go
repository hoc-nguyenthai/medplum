// Package jti nhớ các `jti` của client assertion đã dùng để chặn phát lại. Memory chỉ đúng với MỘT instance; nhiều instance phải dùng Redis,
// nếu không một assertion bị lộ phát lại được trên instance khác trong thời hạn của nó (tối đa 5 phút).
package jti

import (
	"context"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// Memory là JTIStore trong bộ nhớ, có trần số mục (đầy thì từ chối dùng mới, fail đóng).
type Memory struct {
	mu   sync.Mutex
	seen map[string]time.Time
	max  int
	now  func() time.Time
}

// NewMemory dựng Memory với trần max mục (<=0: 100000).
func NewMemory(max int) *Memory {
	if max <= 0 {
		max = 100000
	}
	return &Memory{seen: map[string]time.Time{}, max: max, now: time.Now}
}

// ErrFull báo bộ nhớ jti đã đầy toàn mục còn hạn.
type ErrFull struct{}

func (ErrFull) Error() string { return "jti: bộ nhớ đầy" }

// UseOnce ghi key trong ttl; trả false nếu key đã ghi và còn hạn.
func (m *Memory) UseOnce(_ context.Context, key string, ttl time.Duration) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	if exp, ok := m.seen[key]; ok && now.Before(exp) {
		return false, nil
	}
	if len(m.seen) >= m.max {
		for k, exp := range m.seen {
			if !now.Before(exp) {
				delete(m.seen, k)
			}
		}
		if len(m.seen) >= m.max {
			return false, ErrFull{}
		}
	}
	m.seen[key] = now.Add(ttl)
	return true, nil
}

// Redis là JTIStore dùng chung nhiều instance (SET NX EX).
type Redis struct {
	c      *redis.Client
	prefix string
}

// NewRedis dựng Redis từ URL (redis://...).
func NewRedis(url string) (*Redis, error) {
	opt, err := redis.ParseURL(url)
	if err != nil {
		return nil, err
	}
	return &Redis{c: redis.NewClient(opt), prefix: "auth:jti:"}, nil
}

// UseOnce ghi key bằng SET NX; lỗi Redis là lỗi (fail đóng).
func (r *Redis) UseOnce(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	if ttl < time.Second {
		ttl = time.Second
	}
	return r.c.SetNX(ctx, r.prefix+key, 1, ttl).Result()
}

// Ping kiểm kết nối.
func (r *Redis) Ping(ctx context.Context) error { return r.c.Ping(ctx).Err() }

// Close đóng kết nối.
func (r *Redis) Close() error { return r.c.Close() }
