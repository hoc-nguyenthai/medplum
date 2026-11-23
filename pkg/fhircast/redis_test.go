package fhircast

import (
	"context"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
)

// Đặt FHIRCAST_TEST_REDIS_ADDR (host:port) thì test chạy trên Redis THẬT thay vì miniredis (instance riêng cho test:
// mỗi test FLUSHDB trước và sau).
const redisEnv = "FHIRCAST_TEST_REDIS_ADDR"

type testRedis struct {
	c  *goredis.Client
	mr *miniredis.Miniredis
}

func newRedis(t *testing.T) *testRedis {
	t.Helper()
	if addr := os.Getenv(redisEnv); addr != "" {
		c := goredis.NewClient(&goredis.Options{Addr: addr})
		if err := c.FlushDB(context.Background()).Err(); err != nil {
			t.Fatalf("Redis test %s: %v", addr, err)
		}
		t.Cleanup(func() { _ = c.FlushDB(context.Background()).Err(); _ = c.Close() })
		return &testRedis{c: c}
	}
	mr := miniredis.RunT(t)
	c := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = c.Close() })
	return &testRedis{c: c, mr: mr}
}

// ---- hợp đồng Store dùng chung cho bộ nhớ và Redis (không phụ thuộc thời gian trôi)

func storeContract(t *testing.T, mk func(StoreOptions) Store) {
	ctx := context.Background()
	t.Run("subscription", func(t *testing.T) {
		st := mk(StoreOptions{})
		sub := Subscription{ProjectID: "p", Topic: "t", Events: []string{"Patient-open"}, Version: STU2, UserSubject: "u"}
		if _, ok, err := st.GetSubscription(ctx, "e"); ok || err != nil {
			t.Fatal("chưa có")
		}
		if err := st.PutSubscription(ctx, "e", sub, time.Minute); err != nil {
			t.Fatal(err)
		}
		got, ok, _ := st.GetSubscription(ctx, "e")
		if !ok || got.ProjectID != "p" || got.Topic != "t" || got.Version != STU2 || got.UserSubject != "u" || len(got.Events) != 1 {
			t.Fatalf("đọc lại: %+v", got)
		}
		_ = st.DeleteSubscription(ctx, "e")
		if _, ok, _ := st.GetSubscription(ctx, "e"); ok {
			t.Error("đã xóa")
		}
	})
	t.Run("cas", func(t *testing.T) {
		st := mk(StoreOptions{})
		if ok, err := st.CompareAndSetContext(ctx, "p", "t", "v1", ctxOf("Patient", "v2")); ok || err != nil {
			t.Error("không có ngữ cảnh thì không thiết lập")
		}
		if _, ok, _ := st.GetContext(ctx, "p", "t"); ok {
			t.Error("không được tạo ngữ cảnh")
		}
		_ = st.SetContext(ctx, "p", "t", ctxOf("Patient", "v1"))
		if ok, _ := st.CompareAndSetContext(ctx, "p", "t", "v1", ctxOf("Patient", "v2")); !ok {
			t.Fatal("phải thay")
		}
		if ok, _ := st.CompareAndSetContext(ctx, "p", "t", "v1", ctxOf("Patient", "v3")); ok {
			t.Error("đã đi tiếp")
		}
		if cc, _, _ := st.GetContext(ctx, "p", "t"); cc.VersionID != "v2" || cc.Type != "Patient" {
			t.Errorf("sau CAS: %+v", cc)
		}
	})
	t.Run("cas race exactly one wins", func(t *testing.T) { // F-9
		st := mk(StoreOptions{})
		_ = st.SetContext(ctx, "p", "t", ctxOf("DiagnosticReport", "v0"))
		var wins atomic.Int32
		var wg sync.WaitGroup
		for i := 0; i < 30; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				if ok, err := st.CompareAndSetContext(ctx, "p", "t", "v0", ctxOf("DiagnosticReport", "w"+strings.Repeat("x", i))); ok {
					wins.Add(1)
				} else if err != nil {
					t.Error(err)
				}
			}(i)
		}
		wg.Wait()
		if wins.Load() != 1 {
			t.Errorf("%d người thắng", wins.Load())
		}
	})
	t.Run("isolation", func(t *testing.T) { // F-3
		st := mk(StoreOptions{})
		_ = st.SetContext(ctx, "A", "same", ctxOf("Patient", "a"))
		_ = st.SetContext(ctx, "B", "same", ctxOf("Patient", "b"))
		a, _, _ := st.GetContext(ctx, "A", "same")
		b, _, _ := st.GetContext(ctx, "B", "same")
		if a.VersionID != "a" || b.VersionID != "b" {
			t.Errorf("%v %v", a.VersionID, b.VersionID)
		}
		_ = st.SetContext(ctx, "A", "x:t:y", ctxOf("Patient", "c"))
		if _, ok, _ := st.GetContext(ctx, "A:t:x", "y"); ok {
			t.Error("va chạm khóa")
		}
		_ = st.ClearContext(ctx, "A", "same")
		if _, ok, _ := st.GetContext(ctx, "A", "same"); ok {
			t.Error("close phải xóa")
		}
		if _, ok, _ := st.GetContext(ctx, "B", "same"); !ok {
			t.Error("xóa của A không được ảnh hưởng B")
		}
	})
	t.Run("report contexts", func(t *testing.T) {
		st := mk(StoreOptions{MaxReportContexts: 2})
		for _, id := range []string{"r1", "r2"} {
			if err := st.SaveReportContext(ctx, "p", "t", id, ctxOf("DiagnosticReport", id)); err != nil {
				t.Fatal(err)
			}
		}
		if err := st.SaveReportContext(ctx, "p", "t", "r3", ctxOf("DiagnosticReport", "r3")); err != ErrTooManyContexts {
			t.Errorf("trần: %v", err)
		}
		if err := st.SaveReportContext(ctx, "p", "t", "r1", ctxOf("DiagnosticReport", "r1b")); err != nil {
			t.Errorf("ghi lại: %v", err)
		}
		if cc, ok, _ := st.GetReportContext(ctx, "p", "t", "r1"); !ok || cc.VersionID != "r1b" {
			t.Errorf("r1: %+v", cc)
		}
		_ = st.DeleteReportContext(ctx, "p", "t", "r1")
		if _, ok, _ := st.GetReportContext(ctx, "p", "t", "r1"); ok {
			t.Error("r1 phải bị xóa")
		}
		if _, ok, _ := st.GetReportContext(ctx, "other", "t", "r2"); ok {
			t.Error("project khác không thấy")
		}
		_ = st.DeleteReportContexts(ctx, "p", "t")
		if _, ok, _ := st.GetReportContext(ctx, "p", "t", "r2"); ok {
			t.Error("xóa hết")
		}
	})
	t.Run("size limit", func(t *testing.T) { // F-7
		st := mk(StoreOptions{MaxContextBytes: 200})
		big := CurrentContext{Type: "Patient", VersionID: "v", Context: []map[string]any{{"k": strings.Repeat("x", 500)}}}
		if err := st.SetContext(ctx, "p", "t", big); err != ErrContextTooLarge {
			t.Errorf("Set: %v", err)
		}
		_ = st.SetContext(ctx, "p", "t", ctxOf("Patient", "v"))
		if ok, err := st.CompareAndSetContext(ctx, "p", "t", "v", big); ok || err != ErrContextTooLarge {
			t.Errorf("CAS: %v %v", ok, err)
		}
		if err := st.SaveReportContext(ctx, "p", "t", "r", big); err != ErrContextTooLarge {
			t.Errorf("Save: %v", err)
		}
	})
	t.Run("user topic", func(t *testing.T) { // ca 37
		st := mk(StoreOptions{})
		a, err := st.UserTopic(ctx, "u1")
		b, _ := st.UserTopic(ctx, "u1")
		o, _ := st.UserTopic(ctx, "u2")
		if err != nil || a == "" || a != b || a == o {
			t.Errorf("%q %q %q %v", a, b, o, err)
		}
	})
}

func TestStoreContractMemory(t *testing.T) {
	storeContract(t, func(o StoreOptions) Store { return NewMemoryStore(o) })
}

func TestStoreContractRedis(t *testing.T) {
	storeContract(t, func(o StoreOptions) Store {
		r := newRedis(t)
		return NewRedisStore(r.c, "", o)
	})
}

func TestRedisStoreEveryKeyHasTTL(t *testing.T) { // F-7
	r := newRedis(t)
	ctx := context.Background()
	st := NewRedisStore(r.c, "", StoreOptions{})
	_ = st.PutSubscription(ctx, "e", Subscription{ProjectID: "p", Topic: "t", Events: []string{"x"}, Version: STU3}, 0)
	_ = st.SetContext(ctx, "p", "t", ctxOf("Patient", "v"))
	_ = st.SaveReportContext(ctx, "p", "t", "r", ctxOf("DiagnosticReport", "v"))
	_, _ = st.UserTopic(ctx, "u")
	keys, _ := r.c.Keys(ctx, "*").Result()
	if len(keys) != 4 {
		t.Fatalf("khóa: %v", keys)
	}
	for _, k := range keys {
		if ttl := r.c.TTL(ctx, k).Val(); ttl <= 0 {
			t.Errorf("khóa %s không có hạn (%v)", k, ttl)
		}
		if strings.Contains(k, "topic-secret") || strings.Contains(k, ":t:t:") {
			t.Errorf("khóa chứa topic thô: %s", k)
		}
	}
	if ttl := r.c.TTL(ctx, "fhircast:ep:e").Val(); ttl > DefaultLease {
		t.Errorf("hạn đăng ký %v vượt lease", ttl)
	}
	_ = st.PutSubscription(ctx, "e2", Subscription{ProjectID: "p", Topic: "t", Events: []string{"x"}, Version: STU3}, 100*time.Hour)
	if ttl := r.c.TTL(ctx, "fhircast:ep:e2").Val(); ttl > DefaultLease {
		t.Errorf("xin quá lease không bị kẹp: %v", ttl)
	}
}

func TestRedisStoreCASKeepsTTL(t *testing.T) { // SET trong CAS mà không đặt hạn sẽ xóa hạn: PHI tồn vô hạn
	r := newRedis(t)
	ctx := context.Background()
	st := NewRedisStore(r.c, "", StoreOptions{ContextTTL: time.Hour})
	_ = st.SetContext(ctx, "p", "t", ctxOf("DiagnosticReport", "v0"))
	if ok, err := st.CompareAndSetContext(ctx, "p", "t", "v0", ctxOf("DiagnosticReport", "v1")); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if ttl := r.c.TTL(ctx, st.ctxKey("p", "t")).Val(); ttl <= 0 || ttl > time.Hour {
		t.Errorf("sau CAS khóa phải có hạn <= 1 giờ, có %v", ttl)
	}
}

func TestRedisStoreExpiry(t *testing.T) { // F-10 (cần quay đồng hồ: chỉ miniredis)
	r := newRedis(t)
	if r.mr == nil {
		t.Skip("cần miniredis để quay đồng hồ")
	}
	ctx := context.Background()
	st := NewRedisStore(r.c, "", StoreOptions{ContextTTL: time.Hour})
	_ = st.PutSubscription(ctx, "e", Subscription{ProjectID: "p", Topic: "t", Events: []string{"x"}, Version: STU3}, 0)
	_ = st.SetContext(ctx, "p", "t", ctxOf("Patient", "v"))
	_ = st.SaveReportContext(ctx, "p", "t", "r", ctxOf("DiagnosticReport", "v"))
	r.mr.FastForward(59 * time.Minute)
	_ = st.SetContext(ctx, "p", "t", ctxOf("Patient", "v2")) // làm mới
	r.mr.FastForward(2 * time.Minute)
	if _, ok, _ := st.GetContext(ctx, "p", "t"); !ok {
		t.Error("ghi phải làm mới hạn")
	}
	if _, ok, _ := st.GetReportContext(ctx, "p", "t", "r"); ok {
		t.Error("ngữ cảnh đã lưu phải hết hạn")
	}
	if _, ok, _ := st.GetSubscription(ctx, "e"); ok {
		t.Error("đăng ký phải hết hạn đúng lease")
	}
	r.mr.FastForward(2 * time.Hour)
	if _, ok, _ := st.GetContext(ctx, "p", "t"); ok {
		t.Error("ngữ cảnh phải biến mất sau TTL")
	}
}

func TestRedisStoreCorruptValuesReadAsNone(t *testing.T) { // ca 41
	r := newRedis(t)
	ctx := context.Background()
	st := NewRedisStore(r.c, "", StoreOptions{})
	for i, raw := range []string{`not json`, `{"projectId":"p","topic":"t"}`, `{"projectId":"p","topic":"t","events":[42],"version":"STU3"}`, `{"projectId":"p","topic":"t","events":[],"version":"STU4"}`} {
		ep := string(rune('a' + i))
		r.c.Set(ctx, "fhircast:ep:"+ep, raw, time.Minute)
		if _, ok, err := st.GetSubscription(ctx, ep); ok || err != nil {
			t.Errorf("%q: ok=%v err=%v", raw, ok, err)
		}
	}
	r.c.Set(ctx, st.ctxKey("p", "t"), "garbage", time.Minute)
	if _, ok, err := st.GetContext(ctx, "p", "t"); ok || err != nil {
		t.Errorf("ngữ cảnh hỏng: ok=%v err=%v", ok, err)
	}
	if ok, err := st.CompareAndSetContext(ctx, "p", "t", "v", ctxOf("Patient", "w")); ok || err != nil {
		t.Errorf("CAS trên dữ liệu hỏng: %v %v", ok, err)
	}
}

func TestRedisStoreErrorsPropagate(t *testing.T) {
	r := newRedis(t)
	ctx := context.Background()
	st := NewRedisStore(r.c, "", StoreOptions{})
	_ = r.c.Close()
	if _, _, err := st.GetSubscription(ctx, "e"); err == nil {
		t.Error("GetSubscription phải lỗi khi Redis hỏng")
	}
	if err := st.PutSubscription(ctx, "e", Subscription{Version: STU3}, 0); err == nil {
		t.Error("PutSubscription phải lỗi")
	}
	if _, err := st.CompareAndSetContext(ctx, "p", "t", "v", ctxOf("Patient", "w")); err == nil {
		t.Error("CAS phải lỗi")
	}
	if _, err := st.UserTopic(ctx, "u"); err == nil {
		t.Error("UserTopic phải lỗi")
	}
}

// ---- Bus

type busKit struct {
	bus   Bus
	count func(channel string) int // số đăng ký phía Redis của kênh (-1: không đo được)
}

func busContract(t *testing.T, mk func(t *testing.T) busKit) {
	ctx := context.Background()
	recv := func(ch chan string) string {
		select {
		case s := <-ch:
			return s
		case <-time.After(2 * time.Second):
			return "<timeout>"
		}
	}
	none := func(t *testing.T, ch chan string) {
		t.Helper()
		select {
		case s := <-ch:
			t.Fatalf("không được nhận: %q", s)
		case <-time.After(150 * time.Millisecond):
		}
	}
	collect := func() (Handler, chan string) {
		ch := make(chan string, 64)
		return func(p []byte) { ch <- string(p) }, ch
	}
	// UNSUBSCRIBE được gửi bất đồng bộ: chờ tới khi Redis xử lý xong thay vì đọc ngay.
	waitCount := func(t *testing.T, k busKit, ch string, want int, msg string) {
		t.Helper()
		if k.count(ch) < 0 {
			return
		}
		deadline := time.Now().Add(2 * time.Second)
		for k.count(ch) != want && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if n := k.count(ch); n != want {
			t.Errorf("%s: muốn %d, có %d", msg, want, n)
		}
	}
	t.Run("publish reaches subscribers right after Subscribe returns", func(t *testing.T) {
		k := mk(t)
		h, ch := collect()
		cancel, err := k.bus.Subscribe(ctx, "c1", h)
		if err != nil {
			t.Fatal(err)
		}
		defer cancel()
		for i := 0; i < 20; i++ { // phát NGAY sau Subscribe: không được lọt
			if err := k.bus.Publish(ctx, "c1", []byte("m")); err != nil {
				t.Fatal(err)
			}
			if got := recv(ch); got != "m" {
				t.Fatalf("lần %d: %q", i, got)
			}
		}
	})
	t.Run("channels are isolated", func(t *testing.T) {
		k := mk(t)
		h1, c1 := collect()
		h2, c2 := collect()
		x1, _ := k.bus.Subscribe(ctx, "a", h1)
		x2, _ := k.bus.Subscribe(ctx, "b", h2)
		defer x1()
		defer x2()
		_ = k.bus.Publish(ctx, "a", []byte("for-a"))
		if recv(c1) != "for-a" {
			t.Fatal("a phải nhận")
		}
		none(t, c2)
	})
	t.Run("refcount shares one subscription and cancel keeps others", func(t *testing.T) {
		k := mk(t)
		h1, c1 := collect()
		h2, c2 := collect()
		x1, _ := k.bus.Subscribe(ctx, "rc", h1)
		x2, _ := k.bus.Subscribe(ctx, "rc", h2)
		if n := k.count("rc"); n >= 0 && n != 1 {
			t.Errorf("hai đăng ký cùng kênh phải dùng chung 1 đăng ký Redis, có %d", n)
		}
		_ = k.bus.Publish(ctx, "rc", []byte("both"))
		if recv(c1) != "both" || recv(c2) != "both" {
			t.Fatal("cả hai phải nhận")
		}
		x1()
		x1() // gọi lại an toàn
		_ = k.bus.Publish(ctx, "rc", []byte("only2"))
		if recv(c2) != "only2" {
			t.Fatal("người còn lại vẫn phải nhận")
		}
		none(t, c1)
		waitCount(t, k, "rc", 1, "còn 1 đăng ký")
		x2()
		waitCount(t, k, "rc", 0, "hủy hết thì phải gỡ đăng ký Redis")
		// đăng ký lại sau khi hủy hết vẫn chạy
		h3, c3 := collect()
		x3, err := k.bus.Subscribe(ctx, "rc", h3)
		if err != nil {
			t.Fatal(err)
		}
		defer x3()
		_ = k.bus.Publish(ctx, "rc", []byte("again"))
		if recv(c3) != "again" {
			t.Fatal("đăng ký lại phải nhận")
		}
	})
	t.Run("panic in handler keeps subscription alive", func(t *testing.T) {
		k := mk(t)
		var n atomic.Int32
		got := make(chan string, 4)
		x, _ := k.bus.Subscribe(ctx, "pn", func(p []byte) {
			if n.Add(1) == 1 {
				panic("boom")
			}
			got <- string(p)
		})
		defer x()
		_ = k.bus.Publish(ctx, "pn", []byte("1"))
		_ = k.bus.Publish(ctx, "pn", []byte("2"))
		if recv(got) != "2" {
			t.Fatal("sau panic vẫn phải nhận")
		}
	})
	t.Run("slow subscriber does not block another", func(t *testing.T) {
		k := mk(t)
		block := make(chan struct{})
		slow, _ := k.bus.Subscribe(ctx, "sl", func([]byte) { <-block })
		hf, cf := collect()
		fast, _ := k.bus.Subscribe(ctx, "sl", hf)
		defer func() { close(block); slow(); fast() }()
		for i := 0; i < 5; i++ {
			_ = k.bus.Publish(ctx, "sl", []byte("x"))
		}
		for i := 0; i < 5; i++ {
			if recv(cf) != "x" {
				t.Fatal("người nhanh phải nhận đủ")
			}
		}
	})
	t.Run("cancel waits for running handler", func(t *testing.T) {
		k := mk(t)
		started, release := make(chan struct{}), make(chan struct{})
		var finished atomic.Bool
		x, _ := k.bus.Subscribe(ctx, "cw", func([]byte) {
			close(started)
			<-release
			finished.Store(true)
		})
		_ = k.bus.Publish(ctx, "cw", []byte("x"))
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("handler không được gọi")
		}
		done := make(chan struct{})
		go func() { x(); close(done) }()
		select {
		case <-done:
			t.Fatal("cancel trả về khi handler còn chạy")
		case <-time.After(100 * time.Millisecond):
		}
		close(release)
		<-done
		if !finished.Load() {
			t.Error("handler chưa xong")
		}
	})
}

func TestBusMemory(t *testing.T) {
	busContract(t, func(*testing.T) busKit {
		return busKit{bus: NewMemoryBus(), count: func(string) int { return -1 }}
	})
}

func TestBusRedis(t *testing.T) {
	busContract(t, func(t *testing.T) busKit {
		r := newRedis(t)
		b := NewRedisBus(r.c)
		t.Cleanup(func() { _ = b.Close() })
		return busKit{bus: b, count: func(ch string) int {
			m, err := r.c.PubSubNumSub(context.Background(), ch).Result()
			if err != nil {
				return -1
			}
			return int(m[ch])
		}}
	})
}

func TestRedisBusCrossInstance(t *testing.T) { // hai instance (hai bus) chung Redis
	r := newRedis(t)
	a, b := NewRedisBus(r.c), NewRedisBus(r.c)
	defer a.Close()
	defer b.Close()
	got := make(chan string, 1)
	x, err := b.Subscribe(context.Background(), "x", func(p []byte) { got <- string(p) })
	if err != nil {
		t.Fatal(err)
	}
	defer x()
	if err := a.Publish(context.Background(), "x", []byte("hi")); err != nil {
		t.Fatal(err)
	}
	select {
	case s := <-got:
		if s != "hi" {
			t.Errorf("%q", s)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("instance kia không nhận")
	}
}

func TestRedisBusSubscribeAfterCloseAndFailure(t *testing.T) {
	r := newRedis(t)
	b := NewRedisBus(r.c)
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Subscribe(context.Background(), "x", func([]byte) {}); err == nil {
		t.Error("Subscribe sau Close phải lỗi")
	}
	if err := b.Close(); err != nil {
		t.Errorf("Close lần hai: %v", err)
	}
	if got := Channel("p", "a:b"); strings.Contains(got, "a:b") || !strings.HasPrefix(got, "fhircast:ch:p:") {
		t.Errorf("tên kênh: %s", got)
	}
	if Channel("p", "t") == Channel("q", "t") {
		t.Error("kênh phải theo project")
	}
}

// delayProxy chuyển tiếp TCP tới target và làm chậm chiều client->server, để lệnh SUBSCRIBE của bus tới Redis muộn hơn một
// PUBLISH gửi qua đường khác. Nếu Subscribe trả về trước khi Redis xác nhận thì PUBLISH đó bị lọt.
func delayProxy(t *testing.T, target string, delay time.Duration) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			cl, err := ln.Accept()
			if err != nil {
				return
			}
			srv, err := net.Dial("tcp", target)
			if err != nil {
				_ = cl.Close()
				continue
			}
			go func() { _, _ = io.Copy(cl, srv); _ = cl.Close() }()
			go func() {
				buf := make([]byte, 4096)
				for {
					n, err := cl.Read(buf)
					if n > 0 {
						time.Sleep(delay)
						if _, werr := srv.Write(buf[:n]); werr != nil {
							return
						}
					}
					if err != nil {
						_ = srv.Close()
						return
					}
				}
			}()
		}
	}()
	return ln.Addr().String()
}

func TestRedisBusSubscribeWaitsForRedisConfirmation(t *testing.T) {
	r := newRedis(t)
	addr := r.c.Options().Addr
	slow := goredis.NewClient(&goredis.Options{Addr: delayProxy(t, addr, 300*time.Millisecond)})
	t.Cleanup(func() { _ = slow.Close() })
	bus := NewRedisBus(slow)
	t.Cleanup(func() { _ = bus.Close() })

	got := make(chan string, 1)
	cancel, err := bus.Subscribe(context.Background(), "c-confirm", func(p []byte) { got <- string(p) })
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	// Publish ngay sau khi Subscribe trả về, qua client nhanh: phải tới nơi vì Redis đã xác nhận đăng ký.
	if err := r.c.Publish(context.Background(), "c-confirm", "right-after").Err(); err != nil {
		t.Fatal(err)
	}
	select {
	case s := <-got:
		if s != "right-after" {
			t.Errorf("%q", s)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("thông điệp phát ngay sau Subscribe bị lọt: Subscribe đã trả về trước khi Redis xác nhận")
	}
}
