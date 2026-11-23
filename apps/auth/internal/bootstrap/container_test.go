package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"trivita-fhir/apps/auth/internal/config"
)

func settings(t *testing.T, mut func(*config.Settings)) config.Settings {
	t.Helper()
	s := config.Default()
	s.DatabaseURL = "sqlite://" + filepath.Join(t.TempDir(), "auth.db")
	s.Addr = "127.0.0.1:0"
	s.ShutdownDrainDelay = 0
	s.ShutdownTimeout = 2 * time.Second
	s.BcryptCost = 4 // cost tối thiểu: test không cần băm chậm
	if mut != nil {
		mut(&s)
	}
	return s
}

func TestNewAndReadiness(t *testing.T) {
	c, err := New(context.Background(), settings(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Stop(context.Background())
	ready, checks := c.Readiness(context.Background())
	if !ready || !checks["database"] || !checks["accepting_traffic"] {
		t.Fatalf("phải sẵn sàng: %v %v", ready, checks)
	}
	c.BeginShutdown()
	ready, checks = c.Readiness(context.Background())
	if ready || checks["accepting_traffic"] || !checks["database"] {
		t.Fatalf("khi tắt phải 503 nhưng DB vẫn ổn: %v %v", ready, checks)
	}
}

func TestPendingMigrationRefusesToStart(t *testing.T) {
	s := settings(t, func(s *config.Settings) { s.DatabaseAutoMigrate = false })
	_, err := New(context.Background(), s)
	if err == nil || !strings.Contains(err.Error(), "migrate") {
		t.Fatalf("phải từ chối khởi động trên schema chưa migrate: %v", err)
	}
	var out bytes.Buffer
	if err := Migrate(context.Background(), s, "status", &out); err != nil || !strings.Contains(out.String(), "chưa áp dụng") {
		t.Fatalf("status: %v %q", err, out.String())
	}
	if err := Migrate(context.Background(), s, "up", &out); err != nil {
		t.Fatal(err)
	}
	c, err := New(context.Background(), s)
	if err != nil {
		t.Fatalf("sau migrate up phải khởi động được: %v", err)
	}
	_ = c.Stop(context.Background())
	out.Reset()
	if err := Migrate(context.Background(), s, "status", &out); err != nil || !strings.Contains(out.String(), ": 0") {
		t.Fatalf("status sau up: %v %q", err, out.String())
	}
	if err := Migrate(context.Background(), s, "down", &out); err != nil {
		t.Fatalf("down: %v", err)
	}
	if err := Migrate(context.Background(), s, "bogus", &out); err == nil {
		t.Fatal("lệnh lạ phải lỗi")
	}
}

func TestBadDatabaseURL(t *testing.T) {
	s := settings(t, func(s *config.Settings) { s.DatabaseURL = "mysql://nope" })
	if _, err := New(context.Background(), s); err == nil {
		t.Fatal("URL database không hỗ trợ phải lỗi")
	}
}

// Chạy server thật trên cổng ngẫu nhiên, gọi probe và swagger, rồi tắt êm bằng cách hủy context.
func TestServeEndToEnd(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	addrCh := make(chan net.Addr, 1)
	done := make(chan error, 1)
	flushed := false
	go func() {
		done <- Serve(ctx, settings(t, nil), ServeOptions{OnListen: func(a net.Addr) { addrCh <- a }, Flush: func() { flushed = true }})
	}()
	var base string
	select {
	case a := <-addrCh:
		base = "http://" + a.String()
	case err := <-done:
		t.Fatalf("Serve dừng sớm: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("hết giờ chờ server")
	}
	get := func(path string) (int, string) {
		resp, err := http.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var b bytes.Buffer
		_, _ = b.ReadFrom(resp.Body)
		return resp.StatusCode, b.String()
	}
	if code, _ := get("/healthz"); code != 200 {
		t.Errorf("/healthz = %d", code)
	}
	code, body := get("/health/ready")
	var r struct {
		Ready  bool
		Checks map[string]bool
	}
	_ = json.Unmarshal([]byte(body), &r)
	if code != 200 || !r.Ready || !r.Checks["database"] {
		t.Errorf("/health/ready = %d %s", code, body)
	}
	if code, _ := get("/swagger/doc.json"); code != 200 {
		t.Errorf("swagger local phải bật: %d", code)
	}
	if code, _ := get("/auth/login"); code != 405 {
		t.Errorf("/auth/login chỉ nhận POST: %d", code)
	}
	if code, _ := get("/auth/khong-co"); code != 404 {
		t.Errorf("route không có: %d", code)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("tắt êm phải không lỗi: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve không dừng")
	}
	if !flushed {
		t.Error("Flush phải được gọi")
	}
}

func TestSwaggerOffInProduction(t *testing.T) {
	s := settings(t, func(s *config.Settings) { s.Environment = "production" })
	c, err := New(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Stop(context.Background())
	e, err := c.Handler(nil)
	if err != nil {
		t.Fatal(err)
	}
	ts := httpTestServer(t, e)
	resp, err := http.Get(ts + "/swagger/doc.json")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("production phải 404, được %d", resp.StatusCode)
	}
}

func TestServeListenError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	err = Serve(context.Background(), settings(t, func(s *config.Settings) { s.Addr = ln.Addr().String() }), ServeOptions{})
	if err == nil {
		t.Fatal("cổng đã bị chiếm phải lỗi")
	}
}
