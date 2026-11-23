package mfastate

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

var _ application.MFAState = (*Memory)(nil)
var _ application.MFAState = (*Redis)(nil)

type harness struct {
	name    string
	state   application.MFAState
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

func TestOfferIsStablePerUserAndLoginAndExpires(t *testing.T) {
	each(t, func(t *testing.T, h harness) {
		a, _ := h.state.OfferGetOrCreate(ctx, "U1", "L1", "SECRET-A", 20*time.Minute)
		b, _ := h.state.OfferGetOrCreate(ctx, "U1", "L1", "SECRET-B", 20*time.Minute)
		if a != "SECRET-A" || b != "SECRET-A" {
			t.Fatalf("cùng (user, login) phải nhận cùng một secret: %q %q", a, b)
		}
		// mỗi login một secret riêng, và user khác nhau không chung
		if c, _ := h.state.OfferGetOrCreate(ctx, "U1", "L2", "SECRET-C", time.Minute); c != "SECRET-C" {
			t.Errorf("login khác: %q", c)
		}
		if d, _ := h.state.OfferGetOrCreate(ctx, "U2", "L1", "SECRET-D", time.Minute); d != "SECRET-D" {
			t.Errorf("user khác: %q", d)
		}
		// id không phân biệt hoa thường (khóa dựng từ id chữ thường)
		if e, _ := h.state.OfferGet(ctx, "u1", "l1"); e != "SECRET-A" {
			t.Errorf("khóa phải chuẩn hóa chữ thường: %q", e)
		}
		h.advance(19 * time.Minute)
		if g, _ := h.state.OfferGet(ctx, "U1", "L1"); g != "SECRET-A" {
			t.Errorf("còn hạn: %q", g)
		}
		h.advance(2 * time.Minute)
		if g, _ := h.state.OfferGet(ctx, "U1", "L1"); g != "" {
			t.Errorf("hết hạn sau 20 phút: %q", g)
		}
		if n, _ := h.state.OfferGetOrCreate(ctx, "U1", "L1", "SECRET-NEW", time.Minute); n != "SECRET-NEW" {
			t.Errorf("hết hạn thì tạo mới: %q", n)
		}
		_ = h.state.OfferDelete(ctx, "U1", "L1")
		if g, _ := h.state.OfferGet(ctx, "U1", "L1"); g != "" {
			t.Error("OfferDelete phải xóa")
		}
	})
}

func TestOfferConcurrentCallersAgreeOnOneSecret(t *testing.T) {
	each(t, func(t *testing.T, h harness) {
		var wg sync.WaitGroup
		got := make([]string, 16)
		for i := range got {
			wg.Add(1)
			go func() {
				defer wg.Done()
				got[i], _ = h.state.OfferGetOrCreate(ctx, "U", "L", string(rune('A'+i)), time.Minute)
			}()
		}
		wg.Wait()
		for _, g := range got {
			if g != got[0] || g == "" {
				t.Fatalf("mọi lời gọi phải nhận cùng một secret: %v", got)
			}
		}
	})
}

func TestReserveAttemptLimitsPerLoginAndPerUser(t *testing.T) {
	each(t, func(t *testing.T, h harness) {
		const win = 10 * time.Minute
		for i := 1; i <= 5; i++ {
			r, err := h.state.ReserveAttempt(ctx, "U", "L1", 5, 20, win)
			if err != nil || !r.Allowed || r.LoginCount != i {
				t.Fatalf("lượt %d: %+v %v", i, r, err)
			}
		}
		r, _ := h.state.ReserveAttempt(ctx, "U", "L1", 5, 20, win)
		if r.Allowed || r.RetryAfter <= 0 || r.RetryAfter > win || r.LoginCount != 5 {
			t.Fatalf("lượt thứ 6 của login phải bị chặn: %+v", r)
		}
		// login khác của cùng user vẫn đặt được (đếm riêng) cho tới hạn mức của user
		for i := 0; i < 15; i++ {
			l := string(rune('a' + i))
			if r, _ := h.state.ReserveAttempt(ctx, "U", l, 5, 20, win); !r.Allowed {
				t.Fatalf("login %s lượt đầu của user (đã dùng %d) phải được", l, 5+i)
			}
		}
		if r, _ := h.state.ReserveAttempt(ctx, "U", "login-moi", 5, 20, win); r.Allowed || r.RetryAfter <= 0 {
			t.Fatalf("lượt thứ 21 của user phải bị chặn: %+v", r)
		}
		// user khác không bị ảnh hưởng
		if r, _ := h.state.ReserveAttempt(ctx, "OTHER", "L1", 5, 20, win); !r.Allowed || r.LoginCount != 1 {
			t.Errorf("user khác: %+v", r)
		}
		// cửa sổ cố định: hết sau 10 phút kể từ lượt ĐẦU, không trượt theo lượt sau
		h.advance(win + time.Second)
		if r, _ := h.state.ReserveAttempt(ctx, "U", "L1", 5, 20, win); !r.Allowed || r.LoginCount != 1 {
			t.Errorf("hết cửa sổ phải đếm lại từ 1: %+v", r)
		}
	})
}

func TestReserveAttemptWindowIsFixedNotSliding(t *testing.T) {
	each(t, func(t *testing.T, h harness) {
		const win = 10 * time.Minute
		h.state.ReserveAttempt(ctx, "U", "L", 3, 20, win)
		h.advance(6 * time.Minute)
		h.state.ReserveAttempt(ctx, "U", "L", 3, 20, win)
		h.state.ReserveAttempt(ctx, "U", "L", 3, 20, win)
		r, _ := h.state.ReserveAttempt(ctx, "U", "L", 3, 20, win)
		if r.Allowed || r.RetryAfter > 4*time.Minute+time.Second {
			t.Fatalf("hạn đặt lại tính từ lượt đầu (còn ~4 phút): %+v", r)
		}
		h.advance(4*time.Minute + time.Second)
		if r, _ := h.state.ReserveAttempt(ctx, "U", "L", 3, 20, win); !r.Allowed || r.LoginCount != 1 {
			t.Errorf("sau lượt đầu 10 phút phải đếm lại: %+v", r)
		}
	})
}

// Cửa sổ của bộ đếm USER cũng cố định (tính từ lượt đầu của user, không trượt theo lượt của login khác).
func TestReserveAttemptUserWindowIsFixedNotSliding(t *testing.T) {
	each(t, func(t *testing.T, h harness) {
		const win = 10 * time.Minute
		h.state.ReserveAttempt(ctx, "U", "L1", 5, 3, win)
		h.advance(6 * time.Minute)
		h.state.ReserveAttempt(ctx, "U", "L2", 5, 3, win)
		h.state.ReserveAttempt(ctx, "U", "L3", 5, 3, win)
		r, _ := h.state.ReserveAttempt(ctx, "U", "L4", 5, 3, win)
		if r.Allowed || r.RetryAfter > 4*time.Minute+time.Second {
			t.Fatalf("hạn đặt lại của user tính từ lượt đầu (còn ~4 phút): %+v", r)
		}
		h.advance(4*time.Minute + time.Second)
		if r, _ := h.state.ReserveAttempt(ctx, "U", "L5", 5, 3, win); !r.Allowed {
			t.Errorf("sau lượt đầu 10 phút user phải đếm lại: %+v", r)
		}
	})
}

func TestReserveAttemptIsAtomicUnderConcurrency(t *testing.T) {
	each(t, func(t *testing.T, h harness) {
		var ok atomic.Int32
		var wg sync.WaitGroup
		for i := 0; i < 40; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if r, err := h.state.ReserveAttempt(ctx, "U", "L", 5, 20, time.Minute); err == nil && r.Allowed {
					ok.Add(1)
				}
			}()
		}
		wg.Wait()
		if ok.Load() != 5 {
			t.Fatalf("đúng 5 lượt được đặt, được %d", ok.Load())
		}
	})
}

func TestReleaseAttemptReturnsExactlyOneUserSlot(t *testing.T) {
	each(t, func(t *testing.T, h harness) {
		const win = time.Minute
		// login A sai 2 lần; login B đang thử (lượt thứ 3 của user) rồi thành công => trả đúng 1 lượt của user, xóa bộ đếm của login B
		h.state.ReserveAttempt(ctx, "U", "A", 5, 3, win)
		h.state.ReserveAttempt(ctx, "U", "A", 5, 3, win)
		h.state.ReserveAttempt(ctx, "U", "B", 5, 3, win)
		if r, _ := h.state.ReserveAttempt(ctx, "U", "C", 5, 3, win); r.Allowed {
			t.Fatal("user đã đạt 3 lượt")
		}
		if err := h.state.ReleaseAttempt(ctx, "U", "B"); err != nil {
			t.Fatal(err)
		}
		if r, _ := h.state.ReserveAttempt(ctx, "U", "C", 5, 3, win); !r.Allowed {
			t.Fatal("sau khi trả một lượt, login C phải đặt được")
		}
		// chỉ trả ĐÚNG MỘT lượt: user lại đầy (A:2 + C:1 = 3), login D bị chặn
		if r, _ := h.state.ReserveAttempt(ctx, "U", "D", 5, 3, win); r.Allowed {
			t.Fatal("release chỉ được trả một lượt của user, không xóa cả bộ đếm")
		}
		// các lần sai của login A KHÔNG bị xóa bởi việc login B thành công
		if r, _ := h.state.ReserveAttempt(ctx, "U", "A", 5, 99, win); r.LoginCount != 3 {
			t.Errorf("bộ đếm login A phải giữ nguyên (2 lần sai + lượt này): %+v", r)
		}
		// bộ đếm login B đã bị xóa
		if r, _ := h.state.ReserveAttempt(ctx, "U", "B", 5, 99, win); r.LoginCount != 1 {
			t.Errorf("bộ đếm login B phải bị xóa: %+v", r)
		}
		// release khi không có gì để trả không lỗi và không âm
		if err := h.state.ReleaseAttempt(ctx, "NOBODY", "L"); err != nil {
			t.Error(err)
		}
	})
}

func TestClaimTOTPIsSingleUsePerUserAndExpires(t *testing.T) {
	each(t, func(t *testing.T, h harness) {
		ttl := 4 * 30 * time.Second
		if ok, err := h.state.ClaimTOTP(ctx, "U", "123456", ttl); err != nil || !ok {
			t.Fatalf("lần đầu: %v %v", ok, err)
		}
		if ok, _ := h.state.ClaimTOTP(ctx, "U", "123456", ttl); ok {
			t.Error("dùng lại cùng mã phải bị từ chối")
		}
		if ok, _ := h.state.ClaimTOTP(ctx, "OTHER", "123456", ttl); !ok {
			t.Error("user khác dùng cùng mã là hợp lệ (mã chỉ chống dùng lại theo từng user)")
		}
		if ok, _ := h.state.ClaimTOTP(ctx, "U", "654321", ttl); !ok {
			t.Error("mã khác phải dùng được")
		}
		h.advance(ttl + time.Second)
		if ok, _ := h.state.ClaimTOTP(ctx, "U", "123456", ttl); !ok {
			t.Error("sau khi hết hạn nhớ, mã được chấp nhận lại (lúc đó TOTP đã ngoài cửa sổ)")
		}
	})
}

func TestHitFixedWindowCounter(t *testing.T) {
	each(t, func(t *testing.T, h harness) {
		for i := 1; i <= 3; i++ {
			n, _, ok, err := h.state.Hit(ctx, "send:L1", 3, 20*time.Minute)
			if err != nil || !ok || n != i {
				t.Fatalf("lần %d: %d %v %v", i, n, ok, err)
			}
		}
		n, retry, ok, _ := h.state.Hit(ctx, "send:L1", 3, 20*time.Minute)
		if ok || n != 3 || retry <= 0 || retry > 20*time.Minute {
			t.Fatalf("lần thứ 4 phải bị chặn và không tăng: n=%d retry=%v ok=%v", n, retry, ok)
		}
		if _, _, ok, _ := h.state.Hit(ctx, "send:L2", 3, time.Minute); !ok {
			t.Error("khóa khác độc lập")
		}
		if _, _, ok, _ := h.state.Hit(ctx, "SEND:l1", 3, time.Minute); ok {
			t.Error("khóa không phân biệt hoa thường")
		}
		h.advance(21 * time.Minute)
		if n, _, ok, _ := h.state.Hit(ctx, "send:L1", 3, 20*time.Minute); !ok || n != 1 {
			t.Errorf("hết cửa sổ: %d %v", n, ok)
		}
	})
}

func TestRedisFailsClosedWhenUnavailable(t *testing.T) {
	mr := miniredis.RunT(t)
	cl := redis.NewClient(&redis.Options{Addr: mr.Addr(), DialTimeout: 200 * time.Millisecond, MaxRetries: -1})
	r := NewRedis(cl)
	mr.Close()
	if _, err := r.ReserveAttempt(ctx, "U", "L", 5, 20, time.Minute); err == nil {
		t.Error("ReserveAttempt phải lỗi khi Redis hỏng (không cho qua hạn mức)")
	}
	if _, err := r.ClaimTOTP(ctx, "U", "123456", time.Minute); err == nil {
		t.Error("ClaimTOTP phải lỗi khi Redis hỏng (không cho dùng lại mã)")
	}
	if _, err := r.OfferGetOrCreate(ctx, "U", "L", "S", time.Minute); err == nil {
		t.Error("OfferGetOrCreate phải lỗi")
	}
	if _, _, _, err := r.Hit(ctx, "k", 1, time.Minute); err == nil {
		t.Error("Hit phải lỗi")
	}
}

func TestOpenRedisValidation(t *testing.T) {
	if _, _, err := OpenRedis("không phải url"); err == nil {
		t.Error("URL sai phải lỗi")
	}
	r, closeFn, err := OpenRedis("redis://127.0.0.1:1/0")
	if err != nil || r == nil {
		t.Fatalf("không kết nối ngay: %v", err)
	}
	start := time.Now()
	if _, err := r.ClaimTOTP(ctx, "U", "1", time.Second); err == nil || time.Since(start) > 5*time.Second {
		t.Errorf("Redis không với tới phải lỗi nhanh: %v sau %v", err, time.Since(start))
	}
	if err := closeFn(); err != nil {
		t.Error(err)
	}
}
