// Package ratelimit là bộ giới hạn tốc độ theo khóa (thường là IP) dùng chung cho các service.
//
// Hai kho cùng giao diện Store (khớp echo middleware.RateLimiterStore, nhưng gói này không import echo):
//   - Memory: token bucket trong bộ nhớ một tiến trình. N replica thì tổng hạn mức gấp N lần.
//   - Redis: token bucket dùng chung mọi replica (một script Lua nguyên tử, giờ lấy từ Redis để các replica lệch đồng hồ
//     không ảnh hưởng). Khi Redis lỗi, tự rơi về Memory của replica đó (fail-open có giới hạn) thay vì chặn hay bỏ giới hạn hẳn.
package ratelimit

import (
	"context"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
	"golang.org/x/time/rate"
)

// breakerPause là thời gian bỏ qua Redis sau một lần lỗi.
const breakerPause = 5 * time.Second

// Store quyết định một yêu cầu của khóa id có được qua không.
type Store interface {
	Allow(id string) (bool, error)
}

// Normalize trả burst hiệu lực: <=0 thì gấp đôi rps, tối thiểu 1.
func Normalize(rps float64, burst int) int {
	if burst <= 0 {
		burst = int(2 * rps)
	}
	if burst < 1 {
		burst = 1
	}
	return burst
}

type entry struct {
	lim  *rate.Limiter
	seen time.Time
}

// Memory là token bucket trong bộ nhớ; khóa không dùng quá ttl bị dọn.
type Memory struct {
	rps   float64
	burst int
	ttl   time.Duration
	now   func() time.Time

	mu      sync.Mutex
	m       map[string]*entry
	cleaned time.Time
}

// NewMemory tạo kho bộ nhớ với rps yêu cầu/giây và cỡ đợt burst (xem Normalize).
func NewMemory(rps float64, burst int) *Memory {
	return &Memory{rps: rps, burst: Normalize(rps, burst), ttl: 3 * time.Minute, now: time.Now, m: map[string]*entry{}}
}

// Allow thực hiện Store.
func (s *Memory) Allow(id string) (bool, error) {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if now.Sub(s.cleaned) > s.ttl {
		for k, e := range s.m {
			if now.Sub(e.seen) > s.ttl {
				delete(s.m, k)
			}
		}
		s.cleaned = now
	}
	e := s.m[id]
	if e == nil {
		e = &entry{lim: rate.NewLimiter(rate.Limit(s.rps), s.burst)}
		s.m[id] = e
	}
	e.seen = now
	return e.lim.AllowN(now, 1), nil
}

// script: KEYS[1]=khóa; ARGV: rps, burst, ttl(ms). Trả 1 nếu cho qua, 0 nếu chặn.
const script = `
local t = redis.call('TIME')
local now = t[1] * 1000 + math.floor(t[2] / 1000)
local rps = tonumber(ARGV[1])
local burst = tonumber(ARGV[2])
local h = redis.call('HMGET', KEYS[1], 'tokens', 'ts')
local tokens = tonumber(h[1])
local ts = tonumber(h[2])
if tokens == nil then
  tokens = burst
  ts = now
end
local delta = math.max(0, now - ts)
tokens = math.min(burst, tokens + delta * rps / 1000)
local ok = 0
if tokens >= 1 then
  tokens = tokens - 1
  ok = 1
end
redis.call('HSET', KEYS[1], 'tokens', tostring(tokens), 'ts', tostring(now))
redis.call('PEXPIRE', KEYS[1], ARGV[3])
return ok
`

// Redis là token bucket dùng chung nhiều replica.
type Redis struct {
	cli      redis.Scripter
	prefix   string
	rps      float64
	burst    int
	timeout  time.Duration
	script   *redis.Script
	fallback *Memory

	errs      atomic.Int64
	downUntil atomic.Int64 // unix nano: sau lỗi, bỏ qua Redis đến lúc này để không trả 200ms mỗi yêu cầu
	lastWarn  atomic.Int64
}

// NewRedis tạo kho dùng chung. prefix tách các bộ đếm (ví dụ "fhir:rl:" hoặc "auth:rl:login:"); mỗi bộ cần prefix riêng.
func NewRedis(cli redis.Scripter, prefix string, rps float64, burst int) *Redis {
	return &Redis{
		cli: cli, prefix: prefix, rps: rps, burst: Normalize(rps, burst), timeout: 200 * time.Millisecond,
		script: redis.NewScript(script), fallback: NewMemory(rps, burst),
	}
}

// Allow thực hiện Store. Redis lỗi (hoặc quá 200ms): dùng bộ đếm bộ nhớ của replica này trong 5 giây rồi thử lại Redis, ghi log tối đa mỗi 30 giây.
func (s *Redis) Allow(id string) (bool, error) {
	if time.Now().UnixNano() < s.downUntil.Load() {
		return s.fallback.Allow(id)
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
	defer cancel()
	ttl := time.Duration(float64(s.burst)/s.rps*float64(time.Second)) + 5*time.Second
	v, err := s.script.Run(ctx, s.cli, []string{s.prefix + id}, strconv.FormatFloat(s.rps, 'f', -1, 64), s.burst, ttl.Milliseconds()).Int()
	if err != nil {
		s.errs.Add(1)
		s.downUntil.Store(time.Now().Add(breakerPause).UnixNano())
		now := time.Now().Unix()
		if last := s.lastWarn.Load(); now-last >= 30 && s.lastWarn.CompareAndSwap(last, now) {
			slog.Warn("ratelimit: Redis lỗi, dùng bộ đếm bộ nhớ của replica này", "err", err)
		}
		return s.fallback.Allow(id)
	}
	return v == 1, nil
}

// Errors là số lần phải rơi về bộ nhớ vì Redis lỗi.
func (s *Redis) Errors() int64 { return s.errs.Load() }
