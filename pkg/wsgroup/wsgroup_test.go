package wsgroup_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"trivita-fhir/pkg/wsgroup"
)

func echoServe(ctx context.Context, c *websocket.Conn) {
	for {
		typ, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		if c.Write(ctx, typ, data) != nil {
			return
		}
	}
}

func start(t *testing.T, cfg wsgroup.Config, serve wsgroup.ServeFunc) (*wsgroup.Group, *httptest.Server) {
	t.Helper()
	g := wsgroup.New(cfg)
	srv := httptest.NewServer(g.Handler(serve))
	t.Cleanup(func() {
		srv.CloseClientConnections()
		srv.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = g.Close(ctx)
	})
	return g, srv
}

func dial(t *testing.T, srv *httptest.Server, hdr http.Header) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), &websocket.DialOptions{HTTPHeader: hdr})
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("hết hạn chờ: %s", what)
}

func TestServesAndTracksConnections(t *testing.T) {
	g, srv := start(t, wsgroup.Config{}, echoServe)
	c, _, err := dial(t, srv, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := c.Write(ctx, websocket.MessageText, []byte("foo")); err != nil {
		t.Fatal(err)
	}
	_, got, err := c.Read(ctx)
	if err != nil || string(got) != "foo" {
		t.Fatalf("echo: %q %v", got, err)
	}
	if g.Conns() != 1 {
		t.Errorf("Conns = %d", g.Conns())
	}
	_ = c.Close(websocket.StatusNormalClosure, "")
	waitFor(t, "kết nối được gỡ khi client đóng", func() bool { return g.Conns() == 0 })
}

// W-1: vượt MaxConns thì 503; kết nối bị từ chối không giữ tài nguyên (không có goroutine tồn).
func TestMaxConnsRejectsWith503AndLeaksNothing(t *testing.T) {
	g, srv := start(t, wsgroup.Config{MaxConns: 2}, echoServe)
	var open []*websocket.Conn
	for range 2 {
		c, _, err := dial(t, srv, nil)
		if err != nil {
			t.Fatal(err)
		}
		open = append(open, c)
	}
	waitFor(t, "2 kết nối", func() bool { return g.Conns() == 2 })
	before := runtime.NumGoroutine()
	for range 50 {
		_, resp, err := dial(t, srv, nil)
		if err == nil {
			t.Fatal("kết nối vượt hạn mức phải bị từ chối")
		}
		if resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("muốn 503, được %v", resp)
		}
	}
	waitFor(t, "goroutine không tăng sau 50 kết nối bị từ chối", func() bool { return runtime.NumGoroutine() <= before+3 })
	if g.Conns() != 2 {
		t.Errorf("Conns = %d", g.Conns())
	}
	// có chỗ trống thì nhận lại
	_ = open[0].Close(websocket.StatusNormalClosure, "")
	waitFor(t, "chỗ trống", func() bool { return g.Conns() == 1 })
	c, _, err := dial(t, srv, nil)
	if err != nil {
		t.Fatalf("còn chỗ mà vẫn từ chối: %v", err)
	}
	_ = c.CloseNow()
}

// W-2: Origin.
func TestOriginPolicy(t *testing.T) {
	_, srv := start(t, wsgroup.Config{OriginPatterns: []string{"app.example.org"}}, echoServe)
	for name, c := range map[string]struct {
		origin string
		ok     bool
	}{
		"không có Origin (client không phải trình duyệt)": {"", true},
		"Origin được phép":                                {"https://app.example.org", true},
		"Origin lạ":                                       {"https://evil.example.com", false},
	} {
		hdr := http.Header{}
		if c.origin != "" {
			hdr.Set("Origin", c.origin)
		}
		conn, resp, err := dial(t, srv, hdr)
		if c.ok && err != nil {
			t.Errorf("%s: %v", name, err)
		}
		if !c.ok && (err == nil || resp == nil || resp.StatusCode != http.StatusForbidden) {
			t.Errorf("%s: muốn 403, được %v %v", name, resp, err)
		}
		if conn != nil {
			_ = conn.CloseNow()
		}
	}
	// rỗng: chỉ cùng host; Origin khác host bị từ chối
	_, srv2 := start(t, wsgroup.Config{}, echoServe)
	hdr := http.Header{"Origin": {"https://evil.example.com"}}
	if _, resp, err := dial(t, srv2, hdr); err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Errorf("không khai OriginPatterns thì chỉ cùng host: %v %v", resp, err)
	}
}

// W-5: khung lớn hơn giới hạn thì thư viện đóng 1009.
func TestOversizedFrameClosesWith1009(t *testing.T) {
	_, srv := start(t, wsgroup.Config{ReadLimit: 1024}, echoServe)
	c, _, err := dial(t, srv, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = c.Write(ctx, websocket.MessageText, []byte(strings.Repeat("x", 4096)))
	_, _, err = c.Read(ctx)
	if websocket.CloseStatus(err) != websocket.StatusMessageTooBig {
		t.Fatalf("muốn 1009, được %v", err)
	}
}

// Handler panic: đóng 1011, tiến trình và các kết nối khác không bị ảnh hưởng.
func TestHandlerPanicClosesWith1011(t *testing.T) {
	g, srv := start(t, wsgroup.Config{}, func(ctx context.Context, c *websocket.Conn) {
		_, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		if string(data) == "boom" {
			panic("lỗi cố ý")
		}
		_ = c.Write(ctx, websocket.MessageText, data)
		echoServe(ctx, c)
	})
	good, _, err := dial(t, srv, nil)
	if err != nil {
		t.Fatal(err)
	}
	bad, _, err := dial(t, srv, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = good.Write(ctx, websocket.MessageText, []byte("hi"))
	_, got, err := good.Read(ctx)
	if err != nil || string(got) != "hi" {
		t.Fatalf("kết nối tốt: %q %v", got, err)
	}
	_ = bad.Write(ctx, websocket.MessageText, []byte("boom"))
	_, _, err = bad.Read(ctx)
	if websocket.CloseStatus(err) != websocket.StatusInternalError {
		t.Fatalf("muốn 1011, được %v", err)
	}
	waitFor(t, "kết nối panic được gỡ", func() bool { return g.Conns() == 1 })
	_ = good.Write(ctx, websocket.MessageText, []byte("again"))
	if _, got, err = good.Read(ctx); err != nil || string(got) != "again" {
		t.Errorf("kết nối tốt sau panic: %q %v", got, err)
	}
}

// W-7: đóng êm. 1001 cho socket đang mở, 503 cho upgrade mới, trả ngay khi không còn socket.
func TestCloseSends1001AndRefusesNewUpgrades(t *testing.T) {
	g, srv := start(t, wsgroup.Config{}, echoServe)
	c, _, err := dial(t, srv, nil)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "kết nối", func() bool { return g.Conns() == 1 })
	readErr := make(chan error, 1)
	go func() { _, _, err := c.Read(context.Background()); readErr <- err }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := g.Close(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-readErr:
		if websocket.CloseStatus(err) != websocket.StatusGoingAway {
			t.Errorf("muốn 1001, được %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("client không nhận khung đóng")
	}
	if _, resp, err := dial(t, srv, nil); err == nil || resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("upgrade khi đã đóng phải 503: %v %v", resp, err)
	}
	if g.Conns() != 0 {
		t.Errorf("Conns = %d", g.Conns())
	}
}

func TestCloseIsPromptWithoutConnectionsAndIdempotent(t *testing.T) {
	g := wsgroup.New(wsgroup.Config{})
	start := time.Now()
	for range 2 {
		if err := g.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("đóng nhóm rỗng mất %v", d)
	}
}

// Client im lặng (không đọc nên không bao giờ đáp khung đóng) bị cắt khi hết CloseTimeout, không phải chờ cả trần của ctx.
func TestSilentClientIsCutAfterCloseTimeout(t *testing.T) {
	g, srv := start(t, wsgroup.Config{CloseTimeout: 200 * time.Millisecond}, func(ctx context.Context, c *websocket.Conn) {
		<-ctx.Done()
	})
	c, _, err := dial(t, srv, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	waitFor(t, "kết nối", func() bool { return g.Conns() == 1 })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	start := time.Now()
	if err := g.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("client im lặng giữ Close %v (CloseTimeout = 200ms)", d)
	}
	if g.Conns() != 0 {
		t.Errorf("Conns = %d", g.Conns())
	}
}

// Close trả lỗi khi handler không chịu thoát trước ctx.
func TestCloseReturnsContextErrorWhenHandlerStuck(t *testing.T) {
	release := make(chan struct{})
	g, srv := start(t, wsgroup.Config{CloseTimeout: 50 * time.Millisecond}, func(ctx context.Context, c *websocket.Conn) {
		<-release // bỏ qua ctx.Done(): handler hỏng
	})
	t.Cleanup(func() { close(release) })
	c, _, err := dial(t, srv, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	waitFor(t, "kết nối", func() bool { return g.Conns() == 1 })
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := g.Close(ctx); err == nil {
		t.Error("handler kẹt thì Close phải trả lỗi ctx")
	}
}

// W-1: giới hạn theo IP. 429 khi một IP vượt hạn mức, IP khác không bị ảnh hưởng, đóng kết nối trả lại chỗ.
func TestMaxConnsPerIP(t *testing.T) {
	clientIP := func(r *http.Request) string { return r.Header.Get("X-Test-IP") }
	g, srv := start(t, wsgroup.Config{MaxConnsPerIP: 2, ClientIP: clientIP}, echoServe)
	as := func(ip string) http.Header { return http.Header{"X-Test-Ip": {ip}} }
	var a []*websocket.Conn
	for range 2 {
		c, _, err := dial(t, srv, as("1.1.1.1"))
		if err != nil {
			t.Fatal(err)
		}
		a = append(a, c)
	}
	waitFor(t, "2 kết nối", func() bool { return g.Conns() == 2 })
	_, resp, err := dial(t, srv, as("1.1.1.1"))
	if err == nil || resp == nil || resp.StatusCode != http.StatusTooManyRequests || resp.Header.Get("Retry-After") == "" {
		t.Fatalf("IP vượt hạn mức phải 429 kèm Retry-After: %v %v", resp, err)
	}
	other, _, err := dial(t, srv, as("2.2.2.2"))
	if err != nil {
		t.Fatalf("IP khác không được bị ảnh hưởng: %v", err)
	}
	_ = other.CloseNow()
	_ = a[0].Close(websocket.StatusNormalClosure, "")
	waitFor(t, "chỗ của IP được trả lại", func() bool {
		c, _, err := dial(t, srv, as("1.1.1.1"))
		if err == nil {
			_ = c.CloseNow()
		}
		return err == nil
	})
}

// Không khai MaxConnsPerIP thì không giới hạn theo IP; mặc định lấy IP từ RemoteAddr (cùng máy: cùng IP).
func TestMaxConnsPerIPDefaultsToRemoteAddr(t *testing.T) {
	_, srv := start(t, wsgroup.Config{MaxConnsPerIP: 1}, echoServe)
	c, _, err := dial(t, srv, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	if _, resp, err := dial(t, srv, nil); err == nil || resp == nil || resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("cùng RemoteAddr phải bị 429: %v %v", resp, err)
	}
	_, srv2 := start(t, wsgroup.Config{}, echoServe)
	for range 5 {
		c, _, err := dial(t, srv2, nil)
		if err != nil {
			t.Fatalf("không khai thì không giới hạn: %v", err)
		}
		defer c.CloseNow()
	}
}

// Chỗ được đặt trước Accept: nhiều upgrade đồng thời không vượt được MaxConns (kiểm tra bằng số kết nối thực sự mở).
func TestConcurrentUpgradesNeverExceedMaxConns(t *testing.T) {
	const max = 10
	g, srv := start(t, wsgroup.Config{MaxConns: max}, echoServe)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var open []*websocket.Conn
	start := make(chan struct{})
	for range 120 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if c, _, err := dial(t, srv, nil); err == nil {
				mu.Lock()
				open = append(open, c)
				mu.Unlock()
			}
		}()
	}
	close(start)
	wg.Wait()
	mu.Lock()
	n := len(open)
	mu.Unlock()
	if n != max {
		t.Errorf("mở được %d kết nối, muốn đúng %d", n, max)
	}
	if g.Conns() > max {
		t.Errorf("Conns = %d > %d", g.Conns(), max)
	}
}
