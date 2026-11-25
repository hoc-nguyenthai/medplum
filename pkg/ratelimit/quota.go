package ratelimit

import (
	"context"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

// Status là tình trạng hạn mức điểm của một khóa trong cửa sổ hiện tại.
type Status struct {
	Limit     int           // tổng điểm cho phép mỗi cửa sổ
	Remaining int           // điểm còn lại (không âm)
	Reset     time.Duration // thời gian còn lại tới khi cửa sổ làm mới
	Allowed   bool          // Consume: yêu cầu có được qua không (Peek luôn true)
}

// Quota là hạn mức theo ĐIỂM trong cửa sổ cố định (khác Store: token bucket đếm yêu cầu). Mỗi thao tác tốn `cost` điểm; hết điểm thì bị từ chối tới hết cửa sổ.
// limit và window do người gọi truyền mỗi lần nên một kho phục vụ được nhiều hạn mức (theo người dùng, theo project) với các khóa khác nhau.
type Quota interface {
	// Consume trừ cost điểm của key nếu còn đủ (cost lớn hơn limit được hạ về limit: thao tác nặng vẫn chạy được một lần mỗi cửa sổ). Từ chối thì không trừ.
	Consume(key string, cost, limit int, window time.Duration) (Status, error)
	// Peek đọc tình trạng của key mà không trừ điểm.
	Peek(key string, limit int, window time.Duration) (Status, error)
}

type quotaEntry struct {
	used  int
	start time.Time
}

// MemoryQuota là Quota trong bộ nhớ một tiến trình (N replica thì tổng hạn mức gấp N lần).
type MemoryQuota struct {
	now func() time.Time
	mu  sync.Mutex
	m   map[string]*quotaEntry
	gc  time.Time
}

// NewMemoryQuota tạo kho bộ nhớ.
func NewMemoryQuota() *MemoryQuota {
	return &MemoryQuota{now: time.Now, m: map[string]*quotaEntry{}}
}

func (q *MemoryQuota) current(key string, window time.Duration, now time.Time) *quotaEntry {
	if now.Sub(q.gc) > window {
		for k, e := range q.m {
			if now.Sub(e.start) >= window {
				delete(q.m, k)
			}
		}
		q.gc = now
	}
	e := q.m[key]
	if e == nil || now.Sub(e.start) >= window {
		e = &quotaEntry{start: now}
		q.m[key] = e
	}
	return e
}

func status(limit, used int, reset time.Duration, allowed bool) Status {
	rem := limit - used
	if rem < 0 {
		rem = 0
	}
	return Status{Limit: limit, Remaining: rem, Reset: reset, Allowed: allowed}
}

// Consume thực hiện Quota.
func (q *MemoryQuota) Consume(key string, cost, limit int, window time.Duration) (Status, error) {
	if cost > limit {
		cost = limit
	}
	now := q.now()
	q.mu.Lock()
	defer q.mu.Unlock()
	e := q.current(key, window, now)
	reset := window - now.Sub(e.start)
	if e.used+cost > limit {
		return status(limit, e.used, reset, false), nil
	}
	e.used += cost
	return status(limit, e.used, reset, true), nil
}

// Peek thực hiện Quota.
func (q *MemoryQuota) Peek(key string, limit int, window time.Duration) (Status, error) {
	now := q.now()
	q.mu.Lock()
	defer q.mu.Unlock()
	e := q.m[key]
	if e == nil || now.Sub(e.start) >= window {
		return status(limit, 0, window, true), nil
	}
	return status(limit, e.used, window-now.Sub(e.start), true), nil
}

// KEYS[1]=khóa; ARGV: cost, limit, window(ms), consume(1|0). Trả {allowed, used, pttl(ms)}.
const quotaScript = `
local cost = tonumber(ARGV[1])
local limit = tonumber(ARGV[2])
local window = tonumber(ARGV[3])
local consume = ARGV[4] == '1'
local used = tonumber(redis.call('GET', KEYS[1]) or '0')
local ttl = redis.call('PTTL', KEYS[1])
if ttl < 0 then ttl = window end
if not consume then return {1, used, ttl} end
if cost > limit then cost = limit end
if used + cost > limit then return {0, used, ttl} end
used = redis.call('INCRBY', KEYS[1], cost)
if used == cost then
  redis.call('PEXPIRE', KEYS[1], window)
  ttl = window
end
return {1, used, ttl}
`

// RedisQuota là Quota dùng chung mọi replica (một script Lua nguyên tử). Redis lỗi hoặc quá 200ms: dùng MemoryQuota của replica này trong 5 giây rồi thử lại
// Redis (fail-open có giới hạn, giống Redis của Store), log tối đa mỗi 30 giây.
type RedisQuota struct {
	cli      redis.Scripter
	prefix   string
	script   *redis.Script
	timeout  time.Duration
	fallback *MemoryQuota

	errs      atomic.Int64
	downUntil atomic.Int64
	lastWarn  atomic.Int64
}

// NewRedisQuota tạo kho dùng chung; prefix tách các bộ đếm (ví dụ "fhir:quota:").
func NewRedisQuota(cli redis.Scripter, prefix string) *RedisQuota {
	return &RedisQuota{cli: cli, prefix: prefix, script: redis.NewScript(quotaScript), timeout: 200 * time.Millisecond, fallback: NewMemoryQuota()}
}

func (q *RedisQuota) run(key string, cost, limit int, window time.Duration, consume bool) (Status, error) {
	ctx, cancel := context.WithTimeout(context.Background(), q.timeout)
	defer cancel()
	flag := "0"
	if consume {
		flag = "1"
	}
	res, err := q.script.Run(ctx, q.cli, []string{q.prefix + key}, strconv.Itoa(cost), strconv.Itoa(limit), window.Milliseconds(), flag).Int64Slice()
	if err != nil || len(res) != 3 {
		return Status{}, err
	}
	return status(limit, int(res[1]), time.Duration(res[2])*time.Millisecond, res[0] == 1), nil
}

func (q *RedisQuota) call(key string, cost, limit int, window time.Duration, consume bool) (Status, error) {
	if time.Now().UnixNano() >= q.downUntil.Load() {
		if st, err := q.run(key, cost, limit, window, consume); err == nil && st.Limit == limit {
			return st, nil
		} else if err != nil {
			q.errs.Add(1)
			q.downUntil.Store(time.Now().Add(breakerPause).UnixNano())
			now := time.Now().Unix()
			if last := q.lastWarn.Load(); now-last >= 30 && q.lastWarn.CompareAndSwap(last, now) {
				slog.Warn("ratelimit: Redis lỗi, dùng hạn mức điểm trong bộ nhớ của replica này", "err", err)
			}
		}
	}
	if consume {
		return q.fallback.Consume(key, cost, limit, window)
	}
	return q.fallback.Peek(key, limit, window)
}

// Consume thực hiện Quota.
func (q *RedisQuota) Consume(key string, cost, limit int, window time.Duration) (Status, error) {
	return q.call(key, cost, limit, window, true)
}

// Peek thực hiện Quota.
func (q *RedisQuota) Peek(key string, limit int, window time.Duration) (Status, error) {
	return q.call(key, 0, limit, window, false)
}

// Errors là số lần phải rơi về bộ nhớ vì Redis lỗi.
func (q *RedisQuota) Errors() int64 { return q.errs.Load() }
