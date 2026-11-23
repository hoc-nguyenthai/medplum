package fhirsub_test

import (
	"context"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirsub"
)

func (e *env) subscription(id string) *fhir.Subscription {
	e.t.Helper()
	r, err := e.mem.ReadResource(context.Background(), "Subscription", id)
	if err != nil {
		e.t.Fatal(err)
	}
	return r.(*fhir.Subscription)
}

// autoDisableCfg: một worker để thứ tự giao theo đúng thứ tự sự kiện; 400 không thử lại nên mỗi sự kiện bỏ cuộc ngay.
func autoDisableCfg(triggers ...fhirsub.Trigger) fhirsub.Config {
	cfg := fastCfg()
	cfg.Workers = 1
	cfg.AutoDisable = triggers
	return cfg
}

func TestAutoDisable(t *testing.T) {
	t.Run("đủ ngưỡng thì tắt và không giao nữa", func(t *testing.T) {
		h := newHook(t, 400, 400, 400)
		e := newEnv(t, autoDisableCfg(fhirsub.Trigger{MaxConsecutiveFailures: 3, TimeWindowSeconds: 60}))
		e.subscribe("s", "Observation", h.srv.URL)
		for i := 1; i <= 3; i++ {
			e.create(obs)
			eventually(t, "bỏ cuộc", func() bool { return e.mgr.Stats().Failed == int64(i) })
		}
		eventually(t, "Subscription bị tắt", func() bool { return e.subscription("s").Status == "off" })
		want := "Automatically disabled after 3 consecutive failed events in the last 60 seconds"
		if got := e.subscription("s").Error; got != want {
			t.Errorf("error = %q, muốn %q", got, want)
		}
		e.create(obs)
		stays(t, "giao cho Subscription đã tắt", func() bool { return h.count() != 3 })
	})
	t.Run("giao thành công xóa bộ đếm", func(t *testing.T) {
		h := newHook(t, 400, 400, 200, 400, 400)
		e := newEnv(t, autoDisableCfg(fhirsub.Trigger{MaxConsecutiveFailures: 3, TimeWindowSeconds: 60}))
		e.subscribe("s", "Observation", h.srv.URL)
		for i := 1; i <= 5; i++ {
			e.create(obs)
			eventually(t, "giao xong", func() bool { return h.count() == i })
		}
		eventually(t, "4 lần bỏ cuộc", func() bool { return e.mgr.Stats().Failed == 4 })
		if s := e.subscription("s"); s.Status != "active" || s.Error != "" {
			t.Errorf("không được tắt: status=%s error=%q", s.Status, s.Error)
		}
	})
	t.Run("không cấu hình trigger thì không tắt", func(t *testing.T) {
		h := newHook(t, 400, 400, 400, 400)
		e := newEnv(t, autoDisableCfg())
		e.subscribe("s", "Observation", h.srv.URL)
		for i := 1; i <= 4; i++ {
			e.create(obs)
			eventually(t, "bỏ cuộc", func() bool { return e.mgr.Stats().Failed == int64(i) })
		}
		if s := e.subscription("s"); s.Status != "active" {
			t.Errorf("status = %s", s.Status)
		}
	})
}

func TestFailureTrackers(t *testing.T) {
	t.Run("bộ nhớ", func(t *testing.T) {
		testFailureTracker(t, func(now func() time.Time) fhirsub.FailureTracker { return fhirsub.NewMemoryFailureTracker(now) })
	})
	t.Run("redis", func(t *testing.T) {
		c := goredis.NewClient(&goredis.Options{Addr: redisAddr(t)})
		t.Cleanup(func() { _ = c.Close() })
		testFailureTracker(t, func(now func() time.Time) fhirsub.FailureTracker { return fhirsub.NewRedisFailureTracker(c, "", now) })
	})
}

// Hai replica dùng chung Redis cộng dồn thất bại của cùng một Subscription.
func TestRedisFailureTrackerIsShared(t *testing.T) {
	ctx := context.Background()
	c := goredis.NewClient(&goredis.Options{Addr: redisAddr(t)})
	t.Cleanup(func() { _ = c.Close() })
	a, b := fhirsub.NewRedisFailureTracker(c, "", nil), fhirsub.NewRedisFailureTracker(c, "", nil)
	triggers := []fhirsub.Trigger{{MaxConsecutiveFailures: 3, TimeWindowSeconds: 60}}
	for i, tr := range []fhirsub.FailureTracker{a, b} {
		if f, _, err := tr.Record(ctx, "s", triggers); err != nil || f != nil {
			t.Fatalf("lần %d: %v %v", i+1, f, err)
		}
	}
	if f, n, err := a.Record(ctx, "s", triggers); err != nil || f == nil || n != 3 {
		t.Fatalf("muốn kích hoạt ở lỗi thứ 3 (cộng từ hai replica), được %v %d %v", f, n, err)
	}
	if err := b.Clear(ctx, "s"); err != nil {
		t.Fatal(err)
	}
	if f, n, _ := a.Record(ctx, "s", triggers); f != nil || n != 0 {
		t.Fatalf("Clear ở replica khác không có hiệu lực: %v %d", f, n)
	}
	if ttl := c.TTL(ctx, "trivita:sub:failures:s").Val(); ttl <= 0 || ttl > time.Minute {
		t.Errorf("khóa phải có hạn bằng cửa sổ lớn nhất, TTL = %v", ttl)
	}
}

func testFailureTracker(t *testing.T, newTracker func(now func() time.Time) fhirsub.FailureTracker) {
	ctx := context.Background()
	now := time.Unix(1_000_000, 0)
	tr := newTracker(func() time.Time { return now })
	short := fhirsub.Trigger{MaxConsecutiveFailures: 3, TimeWindowSeconds: 10}
	long := fhirsub.Trigger{MaxConsecutiveFailures: 5, TimeWindowSeconds: 100}
	triggers := []fhirsub.Trigger{short, long}

	record := func() (*fhirsub.Trigger, int) {
		t.Helper()
		f, n, err := tr.Record(ctx, "s", triggers)
		if err != nil {
			t.Fatal(err)
		}
		return f, n
	}

	// Ba lỗi cách nhau 20 giây: cửa sổ 10 giây không bao giờ đủ 3, cửa sổ 100 giây đếm dần.
	for i := 0; i < 4; i++ {
		if f, _ := record(); f != nil {
			t.Fatalf("lần %d: không được kích hoạt, được %+v", i+1, *f)
		}
		now = now.Add(20 * time.Second)
	}
	f, n := record()
	if f == nil || *f != long || n != 5 {
		t.Fatalf("muốn trigger dài với 5 lỗi, được %v %d", f, n)
	}

	// Lỗi cũ hơn cửa sổ lớn nhất bị tỉa.
	_ = tr.Clear(ctx, "s")
	record()
	record()
	now = now.Add(101 * time.Second)
	if f, _ := record(); f != nil {
		t.Fatalf("lỗi ngoài cửa sổ vẫn được đếm: %+v", *f)
	}

	// Ba lỗi dồn trong cửa sổ ngắn thì trigger ngắn kích hoạt trước.
	_ = tr.Clear(ctx, "s")
	record()
	record()
	f, n = record()
	if f == nil || *f != short || n != 3 {
		t.Fatalf("muốn trigger ngắn với 3 lỗi, được %v %d", f, n)
	}

	// Subscription khác có bộ đếm riêng.
	if f, _, _ := tr.Record(ctx, "other", []fhirsub.Trigger{{MaxConsecutiveFailures: 2, TimeWindowSeconds: 10}}); f != nil {
		t.Fatal("bộ đếm bị dùng chung giữa Subscription")
	}
}
