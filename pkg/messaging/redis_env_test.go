package messaging_test

import (
	"context"
	"os"
	"testing"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
)

// Đặt MESSAGING_TEST_REDIS_ADDR (host:port) thì các test Redis chạy trên Redis THẬT thay vì miniredis. Dùng một instance
// riêng cho test: mỗi test FLUSHDB trước và sau khi chạy.
const redisEnv = "MESSAGING_TEST_REDIS_ADDR"

type testRedis struct {
	addr string
	c    *goredis.Client
	mr   *miniredis.Miniredis
}

func newRedis(t *testing.T) *testRedis {
	t.Helper()
	if addr := os.Getenv(redisEnv); addr != "" {
		c := goredis.NewClient(&goredis.Options{Addr: addr})
		ctx := context.Background()
		if err := c.FlushDB(ctx).Err(); err != nil {
			t.Fatalf("Redis test %s: %v", addr, err)
		}
		t.Cleanup(func() { _ = c.FlushDB(ctx).Err(); _ = c.Close() })
		return &testRedis{addr: addr, c: c}
	}
	mr := miniredis.RunT(t)
	return &testRedis{addr: mr.Addr(), mr: mr}
}

func (r *testRedis) Addr() string { return r.addr }

// Stream trả các entry của stream với Values dạng phẳng [k1, v1, k2, v2...] giống miniredis.
func (r *testRedis) Stream(key string) ([]miniredis.StreamEntry, error) {
	if r.mr != nil {
		return r.mr.Stream(key)
	}
	msgs, err := r.c.XRange(context.Background(), key, "-", "+").Result()
	if err != nil {
		return nil, err
	}
	out := make([]miniredis.StreamEntry, 0, len(msgs))
	for _, m := range msgs {
		e := miniredis.StreamEntry{ID: m.ID}
		for k, v := range m.Values {
			e.Values = append(e.Values, k, v.(string))
		}
		out = append(out, e)
	}
	return out, nil
}
