package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"trivita-fhir/apps/ws-gateway/internal/presentation/httpapi"
)

var wsHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ws")) })

func get(e *echo.Echo, path string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	req.RemoteAddr = "192.0.2.10:1234"
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func TestNewRequiresWebSocketHandler(t *testing.T) {
	if _, err := httpapi.New(httpapi.Deps{}); err == nil {
		t.Fatal("thiếu handler websocket phải lỗi")
	}
}

func TestHealthEndpoints(t *testing.T) {
	ready := true
	e, err := httpapi.New(httpapi.Deps{WebSocket: wsHandler, Ready: func(context.Context) (bool, map[string]bool) {
		return ready, map[string]bool{"redis": ready}
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/healthz", "/health/live", "/health/startup"} {
		if r := get(e, p, nil); r.Code != 200 || !strings.Contains(r.Body.String(), `"ok"`) {
			t.Errorf("%s: %d %s", p, r.Code, r.Body)
		}
	}
	r := get(e, "/health/ready", nil)
	var body httpapi.ReadyResponse
	_ = json.Unmarshal(r.Body.Bytes(), &body)
	if r.Code != 200 || !body.Ready || !body.Checks["redis"] {
		t.Errorf("ready: %d %s", r.Code, r.Body)
	}
	ready = false
	if r := get(e, "/health/ready", nil); r.Code != http.StatusServiceUnavailable {
		t.Errorf("không sẵn sàng phải 503: %d", r.Code)
	}
	// không có hàm Ready: coi như sẵn sàng
	e2, _ := httpapi.New(httpapi.Deps{WebSocket: wsHandler})
	if r := get(e2, "/health/ready", nil); r.Code != 200 {
		t.Errorf("không có Ready: %d", r.Code)
	}
}

func TestRoutesAndErrorFormat(t *testing.T) {
	e, _ := httpapi.New(httpapi.Deps{WebSocket: wsHandler})
	if r := get(e, httpapi.WebSocketPath, nil); r.Code != 200 || r.Body.String() != "ws" {
		t.Errorf("websocket route: %d %s", r.Code, r.Body)
	}
	r := get(e, "/khong-co", nil)
	var body map[string]string
	if r.Code != 404 || json.Unmarshal(r.Body.Bytes(), &body) != nil || body["error"] == "" {
		t.Errorf("404 phải là JSON {error}: %d %s", r.Code, r.Body)
	}
	// lỗi 5xx không lộ chi tiết nội bộ
	e.GET("/boom", func(echo.Context) error { return echo.NewHTTPError(500, "chi tiết nội bộ bí mật") })
	e.GET("/panic", func(echo.Context) error { panic("bí mật khi panic") })
	for _, p := range []string{"/boom", "/panic"} {
		r := get(e, p, nil)
		if r.Code != 500 || strings.Contains(r.Body.String(), "bí mật") {
			t.Errorf("%s: %d %s", p, r.Code, r.Body)
		}
	}
}

func TestSecurityHeadersAreSet(t *testing.T) {
	e, _ := httpapi.New(httpapi.Deps{WebSocket: wsHandler})
	r := get(e, "/healthz", nil)
	if r.Header().Get("X-Content-Type-Options") != "nosniff" || r.Header().Get("X-Frame-Options") != "DENY" || r.Header().Get("X-Request-Id") == "" {
		t.Errorf("header: %v", r.Header())
	}
}

// Hạn mức áp cho endpoint websocket (theo IP của IPExtractor), không áp cho health.
func TestRateLimitAppliesToWebSocketOnly(t *testing.T) {
	e, _ := httpapi.New(httpapi.Deps{WebSocket: wsHandler, RateLimit: 0.001, RateBurst: 2})
	for i := 0; i < 2; i++ {
		if r := get(e, httpapi.WebSocketPath, nil); r.Code != 200 {
			t.Fatalf("trong hạn mức: %d", r.Code)
		}
	}
	r := get(e, httpapi.WebSocketPath, nil)
	if r.Code != http.StatusTooManyRequests || r.Header().Get("Retry-After") == "" {
		t.Fatalf("vượt hạn mức: %d %v", r.Code, r.Header())
	}
	for _, p := range []string{"/healthz", "/health/ready"} {
		if r := get(e, p, nil); r.Code == http.StatusTooManyRequests {
			t.Errorf("%s không được bị giới hạn", p)
		}
	}
}

// Mặc định KHÔNG tin X-Forwarded-For: gửi header giả không né được hạn mức; có IPExtractor tin proxy thì mỗi IP một bộ đếm.
func TestRateLimitIdentityFollowsIPExtractor(t *testing.T) {
	e, _ := httpapi.New(httpapi.Deps{WebSocket: wsHandler, RateLimit: 0.001, RateBurst: 1})
	get(e, httpapi.WebSocketPath, nil)
	if r := get(e, httpapi.WebSocketPath, map[string]string{"X-Forwarded-For": "9.9.9.9"}); r.Code != http.StatusTooManyRequests {
		t.Errorf("XFF giả né được hạn mức: %d", r.Code)
	}
	trusting, _ := httpapi.New(httpapi.Deps{WebSocket: wsHandler, RateLimit: 0.001, RateBurst: 1,
		IPExtractor: func(r *http.Request) string { return r.Header.Get("X-Forwarded-For") }})
	for _, ip := range []string{"1.1.1.1", "2.2.2.2"} {
		if r := get(trusting, httpapi.WebSocketPath, map[string]string{"X-Forwarded-For": ip}); r.Code != 200 {
			t.Errorf("IP %s có bộ đếm riêng: %d", ip, r.Code)
		}
	}
	if r := get(trusting, httpapi.WebSocketPath, map[string]string{"X-Forwarded-For": "1.1.1.1"}); r.Code != http.StatusTooManyRequests {
		t.Errorf("cùng IP lần hai phải 429: %d", r.Code)
	}
}
