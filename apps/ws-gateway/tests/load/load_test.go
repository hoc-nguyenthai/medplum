// Package load_test đo tải của ws-gateway THẬT (một binary + Redis thật qua testcontainers): mở N socket, bind vào S Subscription,
// đo bộ nhớ (RSS) và độ trễ fan-out. Dùng để định cỡ tài nguyên và FHIR_WS_MAX_CONNECTIONS, không phải để kiểm đúng sai.
package load_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/coder/websocket"

	"trivita-fhir/pkg/fhirsub"
	"trivita-fhir/pkg/messaging"
	"trivita-fhir/pkg/testkit"
)

// Chạy:
//
//	make bench-ws                       # N=1000
//	WS_LOAD_CONNS=5000 make bench-ws    # kết quả in bằng t.Log (go test -v)
//
// Số đo phụ thuộc máy chạy; đo lại trên cấu hình triển khai thật trước khi chốt requests/limits.

func rssKB(t *testing.T, pid int) int {
	t.Helper()
	out, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		t.Fatalf("ps: %v", err)
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(out)))
	return n
}

func forgeBindToken(key, sub string) string {
	raw, _ := json.Marshal(map[string]any{"v": 1, "sub": sub, "proj": "load", "exp": time.Now().Add(time.Hour).Unix(), "jti": "load"})
	m := hmac.New(sha256.New, []byte(key))
	m.Write([]byte("fhir-ws-binding|"))
	m.Write(raw)
	return base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	i := int(float64(len(sorted)-1) * p)
	return sorted[i]
}

func TestWSLoadFanOutAndMemory(t *testing.T) {
	if os.Getenv("TRIVITA_E2E_LOAD") != "1" {
		t.Skip("đặt TRIVITA_E2E_LOAD=1 (make bench-ws) để đo tải")
	}
	n := 1000
	if v, _ := strconv.Atoi(os.Getenv("WS_LOAD_CONNS")); v > 0 {
		n = v
	}
	const subs = 10
	testkit.RequireDocker(t)
	bin := buildGateway(t)
	redisAddr, key := testkit.StartRedis(t).Addr, randomHex(24)
	gPort := freePort(t)
	gwHTTP := fmt.Sprintf("http://127.0.0.1:%d", gPort)
	gwWS := fmt.Sprintf("ws://127.0.0.1:%d/ws/subscriptions-r4", gPort)
	p := startGateway(t, bin, map[string]string{
		"HTTP_ADDR": fmt.Sprintf("127.0.0.1:%d", gPort), "ENVIRONMENT": "local", "REDIS_URL": "redis://" + redisAddr + "/0",
		"FHIR_WS_SIGNING_KEY": key, "FHIR_WS_MAX_CONNECTIONS": strconv.Itoa(n + 100), "SHUTDOWN_DRAIN_DELAY": "0s", "LOG_JSON": "true",
		"FHIR_WS_RATE_LIMIT": "0", "FHIR_WS_HEARTBEAT": "30s",
	})
	waitReady(t, gwHTTP+"/health/ready")
	pid := p.Process.Pid
	base := rssKB(t, pid)

	// 1. mở và bind N socket (50 luồng song song)
	type conn struct {
		c    *websocket.Conn
		recv atomic.Int64 // UnixNano lúc nhận event-notification cuối
		got  atomic.Int32
	}
	conns := make([]*conn, n)
	var wg sync.WaitGroup
	var failed atomic.Int32
	sem := make(chan struct{}, 50)
	openStart := time.Now()
	for i := 0; i < n; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			c, _, err := websocket.Dial(ctx, gwWS, nil)
			if err != nil {
				failed.Add(1)
				return
			}
			cn := &conn{c: c}
			if err := c.Write(ctx, websocket.MessageText, bindFrame(forgeBindToken(key, fmt.Sprintf("load-sub-%d", i%subs)))); err != nil {
				failed.Add(1)
				return
			}
			if _, data, err := c.Read(ctx); err != nil || !strings.Contains(string(data), `"handshake"`) {
				failed.Add(1)
				return
			}
			conns[i] = cn
			go func() {
				for {
					_, data, err := c.Read(context.Background())
					if err != nil {
						return
					}
					if strings.Contains(string(data), "event-notification") {
						cn.recv.Store(time.Now().UnixNano())
						cn.got.Add(1)
					}
				}
			}()
		}()
	}
	wg.Wait()
	openDur := time.Since(openStart)
	t.Cleanup(func() {
		for _, c := range conns {
			if c != nil {
				_ = c.c.CloseNow()
			}
		}
	})
	if f := failed.Load(); f != 0 {
		t.Fatalf("%d/%d kết nối thất bại (giới hạn file descriptor? thử ulimit -n)", f, n)
	}
	time.Sleep(500 * time.Millisecond) // để gateway ổn định (GC, goroutine)
	loaded := rssKB(t, pid)

	// 2. fan-out: mỗi Subscription nhận 1 event, mỗi socket nhận đúng 1 thông báo (3 vòng)
	pub, err := messaging.NewRedis("redis://"+redisAddr+"/0", messaging.RedisOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pub.Stop(context.Background()) })
	resource := json.RawMessage(`{"resourceType":"Observation","id":"o","status":"final","code":{"text":"load"},"meta":{"project":"load"}}`)
	var rounds []time.Duration
	var all []time.Duration
	for round := 1; round <= 3; round++ {
		for _, c := range conns {
			c.recv.Store(0)
		}
		start := time.Now()
		for s := 0; s < subs; s++ {
			id := fmt.Sprintf("load-sub-%d", s)
			raw, _ := json.Marshal(fhirsub.WSMessage{Sub: id, Event: fhirsub.ChangeEvent{Interaction: "create", ResourceType: "Observation", ID: "o", Resource: resource}})
			if err := pub.PublishChannel(context.Background(), fhirsub.WSChannel(id), raw); err != nil {
				t.Fatal(err)
			}
		}
		deadline := time.Now().Add(30 * time.Second)
		for {
			done := 0
			for _, c := range conns {
				if int(c.got.Load()) >= round {
					done++
				}
			}
			if done == n {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("vòng %d: chỉ %d/%d socket nhận được event", round, done, n)
			}
			time.Sleep(2 * time.Millisecond)
		}
		var lat []time.Duration
		for _, c := range conns {
			lat = append(lat, time.Duration(c.recv.Load()-start.UnixNano()))
		}
		sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
		rounds = append(rounds, lat[len(lat)-1])
		all = append(all, lat...)
		t.Logf("vòng %d: %d thông báo, độ trễ p50=%v p99=%v max=%v", round, n, percentile(lat, 0.5).Round(time.Millisecond), percentile(lat, 0.99).Round(time.Millisecond), lat[len(lat)-1].Round(time.Millisecond))
	}
	sort.Slice(all, func(i, j int) bool { return all[i] < all[j] })
	after := rssKB(t, pid)

	t.Logf("=== %d socket, %d Subscription, gateway 1 tiến trình ===", n, subs)
	t.Logf("mở %d socket trong %v (%.0f socket/giây)", n, openDur.Round(time.Millisecond), float64(n)/openDur.Seconds())
	t.Logf("RSS: nền %d MB, sau %d socket %d MB, sau 3 vòng fan-out %d MB", base/1024, n, loaded/1024, after/1024)
	t.Logf("bộ nhớ mỗi socket ≈ %.1f KB (đo từ RSS)", float64(loaded-base)/float64(n))
	t.Logf("fan-out toàn bộ: p50=%v p99=%v (trên %d lượt giao)", percentile(all, 0.5).Round(time.Millisecond), percentile(all, 0.99).Round(time.Millisecond), len(all))
	_ = rounds
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func bindFrame(token string) []byte {
	b, _ := json.Marshal(map[string]any{"type": "bind-with-token", "payload": map[string]any{"token": token}})
	return b
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// buildGateway biên dịch binary ws-gateway (đo RSS của tiến trình, không phải của container) vào thư mục tạm.
func buildGateway(t *testing.T) string {
	t.Helper()
	root, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}").Output()
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "ws-gateway")
	cmd := exec.Command("go", "build", "-o", bin, "./apps/ws-gateway/cmd/ws-gateway")
	cmd.Dir = strings.TrimSpace(string(root))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build ws-gateway: %v\n%s", err, out)
	}
	return bin
}

func startGateway(t *testing.T, bin string, env map[string]string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(bin)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME")}
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
		if t.Failed() {
			t.Logf("---- log ws-gateway ----\n%s", out.String())
		}
	})
	return cmd
}

func waitReady(t *testing.T, url string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if res, err := http.Get(url); err == nil {
			res.Body.Close()
			if res.StatusCode == 200 {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("%s không sẵn sàng sau 30 giây", url)
}
