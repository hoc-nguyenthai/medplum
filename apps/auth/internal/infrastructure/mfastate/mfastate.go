// Package mfastate cài application.MFAState: Memory (một instance) và Redis (nhiều instance). Tiền tố khóa Redis là `auth:mfa:` (L-M2); dấu `{}` quanh
// userID là hash tag để mọi khóa của một user cùng slot khi dùng cụm Redis (script đặt chỗ cần hai khóa cùng slot).
package mfastate

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

func offerKey(userID, loginID string) string {
	return fmt.Sprintf("auth:mfa:{%s}:totp-enroll:%s", norm(userID), norm(loginID))
}
func loginKey(userID, loginID string) string {
	return fmt.Sprintf("auth:mfa:{%s}:login:%s", norm(userID), norm(loginID))
}
func userKey(userID string) string { return fmt.Sprintf("auth:mfa:{%s}:user", norm(userID)) }
func totpKey(userID, token string) string {
	return fmt.Sprintf("auth:mfa:{%s}:totp:%s", norm(userID), token)
}
func hitKey(key string) string { return "auth:mfa:hit:" + norm(key) }

// ---- Memory

type counter struct {
	n   int
	exp time.Time
}

// Memory là MFAState trong bộ nhớ. CHỈ đúng với một instance: nhiều instance mà dùng Memory thì hạn mức và chống dùng lại nhân lên theo số instance.
type Memory struct {
	mu       sync.Mutex
	now      func() time.Time
	offers   map[string]counterStr
	counters map[string]*counter
	used     map[string]time.Time
}

type counterStr struct {
	v   string
	exp time.Time
}

// NewMemory dựng Memory; now nil là time.Now.
func NewMemory(now func() time.Time) *Memory {
	if now == nil {
		now = time.Now
	}
	return &Memory{now: now, offers: map[string]counterStr{}, counters: map[string]*counter{}, used: map[string]time.Time{}}
}

// live trả bộ đếm còn hạn (xóa nếu đã hết hạn). Gọi khi đang giữ khóa.
func (m *Memory) live(key string) *counter {
	c := m.counters[key]
	if c != nil && !m.now().Before(c.exp) {
		delete(m.counters, key)
		return nil
	}
	return c
}

func (m *Memory) incr(key string, window time.Duration) int {
	c := m.live(key)
	if c == nil {
		c = &counter{exp: m.now().Add(window)}
		m.counters[key] = c
	}
	c.n++
	return c.n
}

func (m *Memory) OfferGetOrCreate(_ context.Context, userID, loginID, candidate string, ttl time.Duration) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := offerKey(userID, loginID)
	if o, ok := m.offers[k]; ok && m.now().Before(o.exp) {
		return o.v, nil
	}
	m.offers[k] = counterStr{candidate, m.now().Add(ttl)}
	return candidate, nil
}

func (m *Memory) OfferGet(_ context.Context, userID, loginID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if o, ok := m.offers[offerKey(userID, loginID)]; ok && m.now().Before(o.exp) {
		return o.v, nil
	}
	return "", nil
}

func (m *Memory) OfferDelete(_ context.Context, userID, loginID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.offers, offerKey(userID, loginID))
	return nil
}

func (m *Memory) ReserveAttempt(_ context.Context, userID, loginID string, loginLimit, userLimit int, window time.Duration) (application.AttemptResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	lk, uk := loginKey(userID, loginID), userKey(userID)
	l, u := m.live(lk), m.live(uk)
	ln, un := 0, 0
	if l != nil {
		ln = l.n
	}
	if u != nil {
		un = u.n
	}
	if ln >= loginLimit {
		return application.AttemptResult{LoginCount: ln, RetryAfter: l.exp.Sub(m.now())}, nil
	}
	if un >= userLimit {
		return application.AttemptResult{LoginCount: ln, RetryAfter: u.exp.Sub(m.now())}, nil
	}
	n := m.incr(lk, window)
	m.incr(uk, window)
	return application.AttemptResult{Allowed: true, LoginCount: n}, nil
}

func (m *Memory) ReleaseAttempt(_ context.Context, userID, loginID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.counters, loginKey(userID, loginID))
	uk := userKey(userID)
	if u := m.live(uk); u != nil {
		if u.n <= 1 {
			delete(m.counters, uk)
		} else {
			u.n--
		}
	}
	return nil
}

func (m *Memory) ClaimTOTP(_ context.Context, userID, token string, ttl time.Duration) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := totpKey(userID, token)
	if exp, ok := m.used[k]; ok && m.now().Before(exp) {
		return false, nil
	}
	m.used[k] = m.now().Add(ttl)
	return true, nil
}

func (m *Memory) Hit(_ context.Context, key string, limit int, window time.Duration) (int, time.Duration, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := hitKey(key)
	if c := m.live(k); c != nil && c.n >= limit {
		return c.n, c.exp.Sub(m.now()), false, nil
	}
	return m.incr(k, window), 0, true, nil
}

// ---- Redis

const reserveScript = `
local l = tonumber(redis.call('GET', KEYS[1]) or '0')
local u = tonumber(redis.call('GET', KEYS[2]) or '0')
if l >= tonumber(ARGV[1]) then return {0, l, u, redis.call('PTTL', KEYS[1])} end
if u >= tonumber(ARGV[2]) then return {0, l, u, redis.call('PTTL', KEYS[2])} end
l = redis.call('INCR', KEYS[1])
if l == 1 then redis.call('PEXPIRE', KEYS[1], ARGV[3]) end
u = redis.call('INCR', KEYS[2])
if u == 1 then redis.call('PEXPIRE', KEYS[2], ARGV[3]) end
return {1, l, u, 0}`

const releaseScript = `
redis.call('DEL', KEYS[1])
local u = tonumber(redis.call('GET', KEYS[2]) or '0')
if u <= 1 then redis.call('DEL', KEYS[2]) else redis.call('DECR', KEYS[2]) end
return 1`

const hitScript = `
local c = tonumber(redis.call('GET', KEYS[1]) or '0')
if c >= tonumber(ARGV[1]) then return {0, c, redis.call('PTTL', KEYS[1])} end
c = redis.call('INCR', KEYS[1])
if c == 1 then redis.call('PEXPIRE', KEYS[1], ARGV[2]) end
return {1, c, 0}`

// Redis là MFAState dùng chung nhiều instance.
type Redis struct {
	c       redis.UniversalClient
	reserve *redis.Script
	release *redis.Script
	hit     *redis.Script
}

// NewRedis dựng Redis trên client có sẵn (không sở hữu client).
func NewRedis(c redis.UniversalClient) *Redis {
	return &Redis{c: c, reserve: redis.NewScript(reserveScript), release: redis.NewScript(releaseScript), hit: redis.NewScript(hitScript)}
}

func (r *Redis) OfferGetOrCreate(ctx context.Context, userID, loginID, candidate string, ttl time.Duration) (string, error) {
	k := offerKey(userID, loginID)
	for i := 0; i < 3; i++ { // hết hạn đúng giữa SETNX và GET thì thử lại
		if err := r.c.SetNX(ctx, k, candidate, ttl).Err(); err != nil {
			return "", err
		}
		v, err := r.c.Get(ctx, k).Result()
		if err == nil {
			return v, nil
		}
		if err != redis.Nil {
			return "", err
		}
	}
	return "", fmt.Errorf("mfastate: không đọc được offer")
}

func (r *Redis) OfferGet(ctx context.Context, userID, loginID string) (string, error) {
	v, err := r.c.Get(ctx, offerKey(userID, loginID)).Result()
	if err == redis.Nil {
		return "", nil
	}
	return v, err
}

func (r *Redis) OfferDelete(ctx context.Context, userID, loginID string) error {
	return r.c.Del(ctx, offerKey(userID, loginID)).Err()
}

func (r *Redis) ReserveAttempt(ctx context.Context, userID, loginID string, loginLimit, userLimit int, window time.Duration) (application.AttemptResult, error) {
	res, err := r.reserve.Run(ctx, r.c, []string{loginKey(userID, loginID), userKey(userID)}, loginLimit, userLimit, window.Milliseconds()).Int64Slice()
	if err != nil {
		return application.AttemptResult{}, err
	}
	if len(res) != 4 {
		return application.AttemptResult{}, fmt.Errorf("mfastate: kết quả script không đúng")
	}
	if res[0] != 1 {
		return application.AttemptResult{LoginCount: int(res[1]), RetryAfter: time.Duration(max64(res[3], 0)) * time.Millisecond}, nil
	}
	return application.AttemptResult{Allowed: true, LoginCount: int(res[1])}, nil
}

func (r *Redis) ReleaseAttempt(ctx context.Context, userID, loginID string) error {
	return r.release.Run(ctx, r.c, []string{loginKey(userID, loginID), userKey(userID)}).Err()
}

func (r *Redis) ClaimTOTP(ctx context.Context, userID, token string, ttl time.Duration) (bool, error) {
	return r.c.SetNX(ctx, totpKey(userID, token), 1, ttl).Result()
}

func (r *Redis) Hit(ctx context.Context, key string, limit int, window time.Duration) (int, time.Duration, bool, error) {
	res, err := r.hit.Run(ctx, r.c, []string{hitKey(key)}, limit, window.Milliseconds()).Int64Slice()
	if err != nil {
		return 0, 0, false, err
	}
	if len(res) != 3 {
		return 0, 0, false, fmt.Errorf("mfastate: kết quả script không đúng")
	}
	return int(res[1]), time.Duration(max64(res[2], 0)) * time.Millisecond, res[0] == 1, nil
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// callTimeout là hạn mỗi lệnh Redis: hạn phải đặt trên client vì deadline của context không áp cho bước khởi tạo kết nối của go-redis.
const callTimeout = 2 * time.Second

// OpenRedis dựng Redis từ URL (redis:// hoặc rediss://), không kết nối ngay. Trả kèm hàm đóng client. Không thử lại: đăng nhập MFA phải fail đóng nhanh khi
// kho trạng thái hỏng thay vì treo.
func OpenRedis(rawURL string) (*Redis, func() error, error) {
	opt, err := redis.ParseURL(rawURL)
	if err != nil {
		return nil, nil, fmt.Errorf("mfastate: URL Redis không hợp lệ: %w", err)
	}
	opt.DialTimeout, opt.ReadTimeout, opt.WriteTimeout = callTimeout, callTimeout, callTimeout
	opt.ContextTimeoutEnabled = true
	opt.MaxRetries = -1
	c := redis.NewClient(opt)
	return NewRedis(c), c.Close, nil
}
