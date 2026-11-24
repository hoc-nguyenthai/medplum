package loginguard

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"trivita-fhir/apps/auth/internal/application"
)

type harness struct {
	name    string
	guard   application.LoginGuard
	advance func(time.Duration)
}

// Cùng một bộ test chạy trên Memory và Redis (miniredis chạy được Lua): hai bản cài phải cùng hành vi.
func harnesses(t *testing.T) []harness {
	t.Helper()
	var clock atomic.Int64
	clock.Store(time.Now().UnixNano())
	mem := NewMemory(func() time.Time { return time.Unix(0, clock.Load()) })
	mr := miniredis.RunT(t)
	cl := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = cl.Close() })
	return []harness{
		{"memory", mem, func(d time.Duration) { clock.Add(int64(d)) }},
		{"redis", NewRedis(cl), func(d time.Duration) { mr.FastForward(d) }},
	}
}

func each(t *testing.T, fn func(t *testing.T, h harness)) {
	for _, h := range harnesses(t) {
		t.Run(h.name, func(t *testing.T) { fn(t, h) })
	}
}

var ctx = context.Background()

const (
	limit   = 3
	window  = 10 * time.Minute
	lockFor = 15 * time.Minute
)

func reserve(t *testing.T, h harness, user string) application.LockResult {
	t.Helper()
	r, err := h.guard.Reserve(ctx, user, limit, window, lockFor)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestAllowsLimitAttemptsThenLocksOnTheNext(t *testing.T) {
	each(t, func(t *testing.T, h harness) {
		for i := 1; i <= limit; i++ {
			if r := reserve(t, h, "U1"); !r.Allowed {
				t.Fatalf("lượt %d phải được phép: %+v", i, r)
			}
		}
		r := reserve(t, h, "U1")
		if r.Allowed || !r.Locked || r.RetryAfter != lockFor {
			t.Fatalf("lượt %d phải khóa: %+v", limit+1, r)
		}
		// đang khóa: từ chối, không phải "vừa khóa", thời gian còn lại giảm dần và KHÔNG bị kéo dài bởi các lượt thử tiếp
		h.advance(5 * time.Minute)
		r = reserve(t, h, "U1")
		if r.Allowed || r.Locked || r.RetryAfter <= 9*time.Minute || r.RetryAfter > 10*time.Minute {
			t.Fatalf("đang khóa: %+v", r)
		}
		h.advance(10*time.Minute + time.Second)
		if r := reserve(t, h, "U1"); !r.Allowed {
			t.Fatalf("hết khóa phải được thử lại: %+v", r)
		}
	})
}

func TestUsersAreIndependentAndCaseInsensitive(t *testing.T) {
	each(t, func(t *testing.T, h harness) {
		for i := 0; i <= limit; i++ {
			reserve(t, h, "U1")
		}
		if r := reserve(t, h, "U2"); !r.Allowed {
			t.Fatalf("user khác không bị khóa: %+v", r)
		}
		if r := reserve(t, h, "u1"); r.Allowed {
			t.Fatalf("userID không phân biệt hoa thường: %+v", r)
		}
	})
}

func TestReleaseClearsFailuresButNotALock(t *testing.T) {
	each(t, func(t *testing.T, h harness) {
		reserve(t, h, "U1")
		reserve(t, h, "U1")
		if err := h.guard.Release(ctx, "U1"); err != nil {
			t.Fatal(err)
		}
		for i := 1; i <= limit; i++ { // bộ đếm đã về 0: lại được đủ limit lượt
			if r := reserve(t, h, "U1"); !r.Allowed {
				t.Fatalf("sau Release lượt %d phải được phép: %+v", i, r)
			}
		}
		reserve(t, h, "U1") // khóa
		if err := h.guard.Release(ctx, "U1"); err != nil {
			t.Fatal(err)
		}
		if r := reserve(t, h, "U1"); r.Allowed {
			t.Fatalf("Release không được mở khóa: %+v", r)
		}
	})
}

func TestUnlockClearsLockAndFailures(t *testing.T) {
	each(t, func(t *testing.T, h harness) {
		for i := 0; i <= limit; i++ {
			reserve(t, h, "U1")
		}
		if err := h.guard.Unlock(ctx, "U1"); err != nil {
			t.Fatal(err)
		}
		for i := 1; i <= limit; i++ {
			if r := reserve(t, h, "U1"); !r.Allowed {
				t.Fatalf("sau Unlock lượt %d phải được phép: %+v", i, r)
			}
		}
	})
}

func TestFailureWindowExpires(t *testing.T) {
	each(t, func(t *testing.T, h harness) {
		for i := 0; i < limit; i++ {
			reserve(t, h, "U1")
		}
		h.advance(window + time.Second) // cửa sổ đếm hết hạn: bộ đếm về 0 trước khi chạm ngưỡng
		for i := 1; i <= limit; i++ {
			if r := reserve(t, h, "U1"); !r.Allowed {
				t.Fatalf("cửa sổ mới lượt %d phải được phép: %+v", i, r)
			}
		}
	})
}

// Các lượt thử song song không vượt hạn mức: đúng `limit` lượt được phép dù 50 goroutine cùng đặt chỗ.
func TestConcurrentReservationsNeverExceedTheLimit(t *testing.T) {
	each(t, func(t *testing.T, h harness) {
		var allowed atomic.Int32
		var wg sync.WaitGroup
		for i := 0; i < 50; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if r, err := h.guard.Reserve(ctx, "U1", limit, window, lockFor); err == nil && r.Allowed {
					allowed.Add(1)
				}
			}()
		}
		wg.Wait()
		if allowed.Load() != limit {
			t.Fatalf("được phép %d lượt, đúng ra %d", allowed.Load(), limit)
		}
	})
}

func TestOpenRedisRejectsBadURL(t *testing.T) {
	if _, _, err := OpenRedis("http://x"); err == nil {
		t.Fatal("URL sai phải lỗi")
	}
	g, closeFn, err := OpenRedis("redis://127.0.0.1:1/0")
	if err != nil {
		t.Fatal(err)
	}
	defer closeFn()
	if _, err := g.Reserve(ctx, "U", 1, time.Minute, time.Minute); err == nil {
		t.Fatal("Redis không có thật phải trả lỗi (người gọi quyết định xử lý)")
	}
}

// Cửa sổ đếm dài hơn thời gian khóa: sau khi hết khóa bộ đếm phải bắt đầu lại từ 0 (không được khóa lại ngay mà chưa có lượt thử nào).
func TestCounterRestartsAfterTheLockEvenWhenTheWindowOutlastsIt(t *testing.T) {
	each(t, func(t *testing.T, h harness) {
		const longWindow, shortLock = 60 * time.Minute, 5 * time.Minute
		for i := 0; i <= limit; i++ {
			if _, err := h.guard.Reserve(ctx, "U1", limit, longWindow, shortLock); err != nil {
				t.Fatal(err)
			}
		}
		h.advance(shortLock + time.Second)
		for i := 1; i <= limit; i++ {
			r, err := h.guard.Reserve(ctx, "U1", limit, longWindow, shortLock)
			if err != nil || !r.Allowed {
				t.Fatalf("sau khi hết khóa lượt %d phải được phép: %+v %v", i, r, err)
			}
		}
	})
}
