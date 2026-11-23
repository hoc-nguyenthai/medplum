// Package e2e_test chạy ws-gateway như một service ĐỘC LẬP: dựng image từ Dockerfile của repo rồi chạy cùng Redis thật bằng
// testcontainers, nói chuyện với nó chỉ qua mạng (HTTP, WebSocket, Redis), không import mã bên trong service. Vì dựng bằng
// Dockerfile nên các test này kiểm luôn cả image (người dùng không phải root, HEALTHCHECK, từ chối cấu hình sai, tắt êm).
package e2e_test

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/golang-jwt/jwt/v5"

	"trivita-fhir/pkg/messaging"
	"trivita-fhir/pkg/testkit"
)

const (
	jwtIssuer   = "https://auth.e2e.test"
	jwtAudience = "fhir"
	publicWS    = "ws://gateway.e2e.test:8000" // FHIRCAST_PUBLIC_URL giả; test thay phần host bằng địa chỉ thật
)

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// image dựng image ws-gateway từ Dockerfile RIÊNG: context là gốc repo, cùng cách `make docker APP=ws-gateway`.
func image(t *testing.T) string {
	t.Helper()
	testkit.RequireDocker(t)
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}").Output()
	if err != nil {
		t.Fatalf("không tìm được thư mục module: %v", err)
	}
	root := strings.TrimSpace(string(out))
	return testkit.BuildImage(t, testkit.BuildSpec{
		Name: "ws-gateway", Context: root, Dockerfile: filepath.Join(root, "apps/ws-gateway/Dockerfile"),
	})
}

type stack struct {
	gw     *testkit.Service
	redis  *testkit.RedisInstance
	key    string // FHIR_WS_SIGNING_KEY
	secret string // FHIRCAST_JWT_HMAC_SECRET
}

// newStack dựng Redis và gateway cùng một mạng Docker; mut chỉnh biến môi trường của gateway.
func newStack(t *testing.T, mut func(env map[string]string)) *stack {
	t.Helper()
	img := image(t)
	net := testkit.NewNetwork(t)
	rds := testkit.StartRedis(t, testkit.OnNetwork(net, "redis"))
	s := &stack{redis: rds, key: randHex(24), secret: randHex(24)}
	env := map[string]string{
		"ENVIRONMENT": "local", "LOG_JSON": "true", "REDIS_URL": "redis://redis:6379/0", "FHIR_WS_SIGNING_KEY": s.key,
		"FHIR_WS_RATE_LIMIT": "0", "SHUTDOWN_DRAIN_DELAY": "0s", "FHIR_WS_CLOSE_TIMEOUT": "2s",
		"FHIRCAST_ENABLED": "true", "FHIRCAST_PUBLIC_URL": publicWS, "FHIRCAST_JWT_ISSUER": jwtIssuer,
		"FHIRCAST_JWT_AUDIENCE": jwtAudience, "FHIRCAST_JWT_HMAC_SECRET": s.secret, "FHIRCAST_HEARTBEAT": "1s",
	}
	if mut != nil {
		mut(env)
	}
	s.gw = testkit.RunService(t, testkit.ServiceSpec{Image: img, Port: "8000/tcp", Health: "/health/ready", Env: env, Network: net, Alias: "gateway", ReadOnly: true})
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("---- log ws-gateway ----\n%s", s.gw.Logs(t))
		}
	})
	return s
}

// jwtToken là access token HS256 như auth cấp (claim project và scope).
func (s *stack) jwtToken(t *testing.T, project, sub, scope string) string {
	t.Helper()
	c := jwt.MapClaims{"iss": jwtIssuer, "aud": jwtAudience, "sub": sub, "scope": scope, "exp": time.Now().Add(time.Hour).Unix()}
	if project != "" {
		c["fhir_project"] = project
	}
	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString([]byte(s.secret))
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

// bindToken là token bind websocket do fhir-server cấp (HMAC-SHA256 với tiền tố miền chữ ký), ký bằng key.
func bindToken(key, sub string) string {
	raw, _ := json.Marshal(map[string]any{"v": 1, "sub": sub, "proj": "e2e", "exp": time.Now().Add(time.Hour).Unix(), "jti": randHex(4)})
	m := hmac.New(sha256.New, []byte(key))
	m.Write([]byte("fhir-ws-binding|"))
	m.Write(raw)
	return base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func bindFrame(token string) []byte {
	b, _ := json.Marshal(map[string]any{"type": "bind-with-token", "payload": map[string]any{"token": token}})
	return b
}

// redisURL là địa chỉ Redis từ máy chạy test (để đóng vai fhir-server phát sự kiện).
func (s *stack) publisher(t *testing.T) *messaging.Redis {
	t.Helper()
	b, err := messaging.NewRedis(s.redis.URL+"/0", messaging.RedisOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Stop(context.Background()) })
	return b
}

// call gọi HTTP của gateway và trả mã trạng thái cùng thân JSON (nếu có).
func call(t *testing.T, method, url, token string, body any) (int, map[string]any) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = strings.NewReader(string(b))
	}
	req, _ := http.NewRequest(method, url, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return resp.StatusCode, m
}

func dial(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("dial %s: %v", url, err)
	}
	t.Cleanup(func() { _ = c.CloseNow() })
	return c
}

func readText(t *testing.T, c *websocket.Conn) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, b, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("đọc websocket: %v", err)
	}
	return string(b)
}

// readUntil đọc tới khi gặp khung chứa substr (bỏ qua heartbeat).
func readUntil(t *testing.T, c *websocket.Conn, substr string) string {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if m := readText(t, c); strings.Contains(m, substr) {
			return m
		}
	}
	t.Fatalf("không nhận được khung chứa %q", substr)
	return ""
}

func closeStatus(t *testing.T, c *websocket.Conn) websocket.StatusCode {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for {
		if _, _, err := c.Read(ctx); err != nil {
			return websocket.CloseStatus(err)
		}
	}
}

// eventually thử lại tới khi cond đúng hoặc hết hạn.
func eventually(t *testing.T, what string, within time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("hết %v mà chưa thấy: %s", within, what)
}

func status(url string) int {
	resp, err := http.Get(url)
	if err != nil {
		return 0
	}
	resp.Body.Close()
	return resp.StatusCode
}
