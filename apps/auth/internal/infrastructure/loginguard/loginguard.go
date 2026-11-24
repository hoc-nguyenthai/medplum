// Package loginguard cài application.LoginGuard: Memory (một instance) và Redis (nhiều instance). Khóa Redis có tiền tố `auth:lock:`; dấu `{}` quanh userID
// là hash tag để hai khóa của một user cùng slot khi dùng cụm Redis (script đụng cả hai).
package loginguard

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"trivita-fhir/apps/auth/internal/application"
)

func norm(s string) string { return strings.ToLower(s) }

func lockKey(userID string) string  { return fmt.Sprintf("auth:lock:{%s}:locked", norm(userID)) }
func countKey(userID string) string { return fmt.Sprintf("auth:lock:{%s}:fails", norm(userID)) }

// ---- Memory

type entry struct {
	n   int
	exp time.Time
}

// Memory là LoginGuard trong bộ nhớ. CHỈ đúng với một instance: nhiều instance mà dùng Memory thì hạn mức nhân lên theo số instance, và lệnh CLI `user unlock`
// (tiến trình khác) không mở được khóa.
type Memory struct {
	mu     sync.Mutex
	now    func() time.Time
	counts map[string]*entry
	locks  map[string]time.Time
}

// NewMemory dựng Memory; now nil là time.Now.
func NewMemory(now func() time.Time) *Memory {
	if now == nil {
		now = time.Now
	}
	return &Memory{now: now, counts: map[string]*entry{}, locks: map[string]time.Time{}}
}

func (m *Memory) Reserve(_ context.Context, userID string, limit int, window, lockFor time.Duration) (application.LockResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := norm(userID)
	now := m.now()
	if exp, ok := m.locks[k]; ok {
		if now.Before(exp) {
			return application.LockResult{RetryAfter: exp.Sub(now)}, nil
		}
		delete(m.locks, k)
	}
	c := m.counts[k]
	if c == nil || !now.Before(c.exp) {
		c = &entry{exp: now.Add(window)}
		m.counts[k] = c
	}
	c.n++
	if c.n > limit {
		delete(m.counts, k)
		m.locks[k] = now.Add(lockFor)
		return application.LockResult{Locked: true, RetryAfter: lockFor}, nil
	}
	return application.LockResult{Allowed: true}, nil
}

func (m *Memory) Release(_ context.Context, userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.counts, norm(userID))
	return nil
}

func (m *Memory) Unlock(_ context.Context, userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.counts, norm(userID))
	delete(m.locks, norm(userID))
	return nil
}

// ---- Redis

// Trả {1, 0, 0} được phép; {0, 0, ttl} đang khóa; {0, 1, lockMs} vừa bị khóa bởi lượt này.
const reserveScript = `
local ttl = redis.call('PTTL', KEYS[1])
if ttl > 0 then return {0, 0, ttl} end
local c = redis.call('INCR', KEYS[2])
if c == 1 then redis.call('PEXPIRE', KEYS[2], ARGV[2]) end
if c > tonumber(ARGV[1]) then
  redis.call('SET', KEYS[1], 1, 'PX', ARGV[3])
  redis.call('DEL', KEYS[2])
  return {0, 1, tonumber(ARGV[3])}
end
return {1, 0, 0}`

// Redis là LoginGuard dùng chung nhiều instance.
type Redis struct {
	c       redis.UniversalClient
	reserve *redis.Script
}

// NewRedis dựng Redis trên client có sẵn (không sở hữu client).
func NewRedis(c redis.UniversalClient) *Redis {
	return &Redis{c: c, reserve: redis.NewScript(reserveScript)}
}

func (r *Redis) Reserve(ctx context.Context, userID string, limit int, window, lockFor time.Duration) (application.LockResult, error) {
	res, err := r.reserve.Run(ctx, r.c, []string{lockKey(userID), countKey(userID)}, limit, window.Milliseconds(), lockFor.Milliseconds()).Int64Slice()
	if err != nil {
		return application.LockResult{}, err
	}
	if len(res) != 3 {
		return application.LockResult{}, fmt.Errorf("loginguard: kết quả script không đúng")
	}
	if res[0] == 1 {
		return application.LockResult{Allowed: true}, nil
	}
	return application.LockResult{Locked: res[1] == 1, RetryAfter: time.Duration(res[2]) * time.Millisecond}, nil
}

func (r *Redis) Release(ctx context.Context, userID string) error {
	return r.c.Del(ctx, countKey(userID)).Err()
}

func (r *Redis) Unlock(ctx context.Context, userID string) error {
	return r.c.Del(ctx, lockKey(userID), countKey(userID)).Err()
}

// callTimeout là hạn mỗi lệnh Redis (đặt trên client vì deadline của context không áp cho bước khởi tạo kết nối của go-redis).
const callTimeout = 2 * time.Second

// OpenRedis dựng Redis từ URL (redis:// hoặc rediss://), không kết nối ngay. Trả kèm hàm đóng client. Không thử lại: lỗi Redis được người gọi xử lý nhanh
// (đăng nhập cho qua và ghi log) thay vì treo.
func OpenRedis(rawURL string) (*Redis, func() error, error) {
	opt, err := redis.ParseURL(rawURL)
	if err != nil {
		return nil, nil, fmt.Errorf("loginguard: URL Redis không hợp lệ: %w", err)
	}
	opt.DialTimeout, opt.ReadTimeout, opt.WriteTimeout = callTimeout, callTimeout, callTimeout
	opt.ContextTimeoutEnabled = true
	opt.MaxRetries = -1
	c := redis.NewClient(opt)
	return NewRedis(c), c.Close, nil
}
