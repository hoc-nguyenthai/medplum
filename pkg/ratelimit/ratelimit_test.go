package ratelimit

import (
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func count(s Store, id string, n int) (ok int) {
	for i := 0; i < n; i++ {
		if a, _ := s.Allow(id); a {
			ok++
		}
	}
	return
}

func TestNormalize(t *testing.T) {
	for _, c := range []struct {
		rps  float64
		b, w int
	}{{10, 0, 20}, {0.1, 0, 1}, {10, 3, 3}, {10, -1, 20}} {
		if g := Normalize(c.rps, c.b); g != c.w {
			t.Errorf("Normalize(%v,%d)=%d, muốn %d", c.rps, c.b, g, c.w)
		}
	}
}

func TestMemoryBurstThenRefillAndPerKey(t *testing.T) {
	s := NewMemory(10, 3)
	now := time.Unix(1000, 0)
	s.now = func() time.Time { return now }
	if g := count(s, "a", 10); g != 3 {
		t.Fatalf("burst: qua %d, muốn 3", g)
	}
	if g := count(s, "b", 10); g != 3 {
		t.Fatalf("khóa khác phải có đợt riêng: %d", g)
	}
	now = now.Add(200 * time.Millisecond) // 10/s * 0.2s = 2 token
	if g := count(s, "a", 10); g != 2 {
		t.Fatalf("nạp lại: qua %d, muốn 2", g)
	}
	now = now.Add(10 * time.Minute)
	count(s, "c", 1)
	if len(s.m) != 1 {
		t.Errorf("khóa cũ phải bị dọn, còn %d", len(s.m))
	}
}

func newRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	cli := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = cli.Close() })
	return mr, cli
}

// Hai "replica" (hai Redis store cùng client/prefix) chia sẻ một đợt; bộ nhớ thì mỗi bên một đợt.
func TestRedisSharedAcrossReplicas(t *testing.T) {
	_, cli := newRedis(t)
	r1, r2 := NewRedis(cli, "t:", 1, 4), NewRedis(cli, "t:", 1, 4)
	got := count(r1, "ip", 3) + count(r2, "ip", 3)
	if got != 4 {
		t.Fatalf("tổng hai replica qua %d, muốn đúng burst 4", got)
	}
	if g := count(r1, "other", 10); g != 4 {
		t.Errorf("khóa khác: %d", g)
	}
	m1, m2 := NewMemory(1, 4), NewMemory(1, 4)
	if g := count(m1, "ip", 6) + count(m2, "ip", 6); g != 8 {
		t.Errorf("đối chứng: bộ nhớ hai replica phải gấp đôi (8), được %d", g)
	}
}

func TestRedisRefillsAndPrefixesSeparate(t *testing.T) {
	mr, cli := newRedis(t)
	r := NewRedis(cli, "a:", 20, 2)
	if count(r, "ip", 5) != 2 {
		t.Fatal("burst")
	}
	if count(NewRedis(cli, "b:", 20, 2), "ip", 5) != 2 {
		t.Error("prefix khác phải có bộ đếm riêng")
	}
	time.Sleep(120 * time.Millisecond) // 20/s -> >=2 token (giờ lấy từ TIME của miniredis = giờ thật)
	if g := count(r, "ip", 5); g < 1 {
		t.Errorf("sau khi chờ phải nạp lại, qua %d", g)
	}
	if ttl := mr.TTL("a:ip"); ttl <= 0 {
		t.Errorf("khóa phải có TTL, được %v", ttl)
	}
}

func TestRedisFailureFallsBackToMemory(t *testing.T) {
	mr, cli := newRedis(t)
	r := NewRedis(cli, "t:", 1, 3)
	r.fallback.now = func() time.Time { return time.Unix(1000, 0) }
	mr.Close()
	start := time.Now()
	if g := count(r, "ip", 10); g != 3 {
		t.Fatalf("Redis chết: rơi về bộ nhớ, qua %d, muốn 3 (không chặn hết, không bỏ giới hạn)", g)
	}
	if r.Errors() != 1 {
		t.Errorf("breaker: chỉ thử Redis một lần rồi bỏ qua, Errors=%d", r.Errors())
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("Redis chết không được làm chậm mỗi yêu cầu: %v", d)
	}
}
