package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	"trivita-fhir/apps/auth/internal/domain"
)

func do(t *testing.T, e *echo.Echo, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func newEcho(t *testing.T, dep Deps) *echo.Echo {
	t.Helper()
	e, err := New(dep)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestLiveProbes(t *testing.T) {
	e := newEcho(t, Deps{})
	for _, p := range []string{"/healthz", "/health/live", "/health/startup"} {
		rec := do(t, e, "GET", p)
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"status":"ok"`) {
			t.Errorf("%s: %d %s", p, rec.Code, rec.Body)
		}
	}
}

func TestReady(t *testing.T) {
	e := newEcho(t, Deps{})
	if rec := do(t, e, "GET", "/health/ready"); rec.Code != 200 {
		t.Errorf("không có Ready func phải 200, được %d", rec.Code)
	}
	for _, c := range []struct {
		ready bool
		want  int
	}{{true, 200}, {false, 503}} {
		e := newEcho(t, Deps{Ready: func(context.Context) (bool, map[string]bool) {
			return c.ready, map[string]bool{"database": c.ready}
		}})
		rec := do(t, e, "GET", "/health/ready")
		var body ReadyResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if rec.Code != c.want || body.Ready != c.ready || body.Checks["database"] != c.ready {
			t.Errorf("ready=%v: %d %+v", c.ready, rec.Code, body)
		}
	}
}

func TestSwaggerGating(t *testing.T) {
	on := newEcho(t, Deps{Swagger: true})
	for _, p := range []string{"/swagger/index.html", "/swagger/doc.json"} {
		if rec := do(t, on, "GET", p); rec.Code != 200 {
			t.Errorf("%s bật phải 200, được %d", p, rec.Code)
		}
	}
	off := newEcho(t, Deps{})
	rec := do(t, off, "GET", "/swagger/index.html")
	if rec.Code != 404 || !strings.Contains(rec.Body.String(), `"error"`) {
		t.Errorf("tắt phải 404 JSON, được %d %s", rec.Code, rec.Body)
	}
}

func TestErrorFormats(t *testing.T) {
	e := newEcho(t, Deps{})
	e.GET("/auth/boom", func(echo.Context) error { return echo.NewHTTPError(http.StatusBadRequest, "Missing login") })
	e.GET("/auth/crash", func(echo.Context) error { return errors.New("chi tiết nội bộ bí mật") })
	e.GET("/auth/teapot", func(echo.Context) error { return echo.NewHTTPError(http.StatusTeapot, map[string]string{"x": "y"}) })
	e.GET("/other/boom", func(echo.Context) error { return echo.NewHTTPError(http.StatusForbidden, "no") })
	e.GET("/other/crash", func(echo.Context) error { return errors.New("bí mật") })

	rec := do(t, e, "GET", "/auth/boom")
	if rec.Code != 400 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/fhir+json") {
		t.Fatalf("auth: %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	var oo struct {
		ResourceType string `json:"resourceType"`
		Issue        []struct{ Severity, Code, Diagnostics string }
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &oo); err != nil {
		t.Fatal(err)
	}
	if oo.ResourceType != "OperationOutcome" || len(oo.Issue) != 1 || oo.Issue[0].Code != "invalid" || oo.Issue[0].Diagnostics != "Missing login" {
		t.Errorf("outcome sai: %+v", oo)
	}

	rec = do(t, e, "GET", "/auth/crash")
	if rec.Code != 500 || strings.Contains(rec.Body.String(), "bí mật") {
		t.Errorf("lỗi 5xx không được lộ chi tiết: %d %s", rec.Code, rec.Body)
	}
	if rec = do(t, e, "GET", "/auth/teapot"); rec.Code != http.StatusTeapot || !strings.Contains(rec.Body.String(), "I'm a teapot") {
		t.Errorf("message không phải chuỗi dùng status text: %d %s", rec.Code, rec.Body)
	}
	rec = do(t, e, "GET", "/other/boom")
	if rec.Code != 403 || strings.TrimSpace(rec.Body.String()) != `{"error":"no"}` {
		t.Errorf("ngoài /auth phải JSON thường: %d %s", rec.Code, rec.Body)
	}
	if rec = do(t, e, "GET", "/other/crash"); rec.Code != 500 || strings.Contains(rec.Body.String(), "bí mật") {
		t.Errorf("lỗi 5xx ngoài /auth không được lộ chi tiết: %d %s", rec.Code, rec.Body)
	}
	if rec = do(t, e, "GET", "/auth/none"); rec.Code != 404 || !strings.Contains(rec.Body.String(), "not-found") {
		t.Errorf("404 dưới /auth phải là OperationOutcome: %d %s", rec.Code, rec.Body)
	}
}

func TestInstrumentHook(t *testing.T) {
	called := false
	if _, err := New(Deps{Instrument: func(*echo.Echo) error { called = true; return nil }}); err != nil || !called {
		t.Fatalf("Instrument không được gọi: %v", err)
	}
	if _, err := New(Deps{Instrument: func(*echo.Echo) error { return errors.New("x") }}); err == nil {
		t.Fatal("lỗi Instrument phải được trả về")
	}
}

func TestCodeFor(t *testing.T) {
	for status, want := range map[int]string{400: "invalid", 401: "login", 403: "forbidden", 404: "not-found", 429: "throttled", 500: "exception", 418: "exception"} {
		if got := codeFor(status); got != want {
			t.Errorf("codeFor(%d)=%s, mong %s", status, got, want)
		}
	}
}

// ---- header bảo mật và CORS

func doWith(t *testing.T, e *echo.Echo, method, path, body string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func TestSecurityHeadersOnEveryKindOfResponse(t *testing.T) {
	e := newEcho(t, Deps{BaseURL: "https://auth.example.org"})
	e.GET("/auth/boom", func(echo.Context) error { return echo.NewHTTPError(http.StatusBadRequest, "x") })
	e.GET("/auth/crash", func(echo.Context) error { return errors.New("bí mật") })
	for name, path := range map[string]string{"200": "/health/live", "404": "/khong-co", "404 dưới /auth": "/auth/khong-co", "lỗi 400": "/auth/boom", "lỗi 500": "/auth/crash"} {
		rec := doWith(t, e, "GET", path, "", nil)
		for h, want := range map[string]string{
			"Cache-Control": "no-store, no-cache, must-revalidate", "X-Content-Type-Options": "nosniff", "X-Frame-Options": "DENY",
			"Referrer-Policy": "no-referrer", "Pragma": "no-cache",
		} {
			if got := rec.Header().Get(h); got != want {
				t.Errorf("%s (%s): %s = %q", name, path, h, got)
			}
		}
		if rec.Header().Get("Strict-Transport-Security") == "" || !strings.HasPrefix(rec.Header().Get("Content-Security-Policy"), "default-src 'none'") {
			t.Errorf("%s: thiếu HSTS hoặc CSP", name)
		}
	}
	// base URL http: không HSTS
	e = newEcho(t, Deps{BaseURL: "http://localhost:8006"})
	if doWith(t, e, "GET", "/health/live", "", nil).Header().Get("Strict-Transport-Security") != "" {
		t.Error("không HSTS với http")
	}
}

func TestSwaggerIsNotBrokenByCSP(t *testing.T) {
	e := newEcho(t, Deps{Swagger: true, BaseURL: "http://localhost:8006"})
	if csp := doWith(t, e, "GET", "/swagger/index.html", "", nil).Header().Get("Content-Security-Policy"); csp != "" {
		t.Errorf("CSP chặn mọi thứ sẽ làm hỏng giao diện Swagger: %q", csp)
	}
	if doWith(t, e, "GET", "/health/live", "", nil).Header().Get("Content-Security-Policy") == "" {
		t.Error("đường dẫn khác vẫn có CSP")
	}
	// Swagger tắt: /swagger/ không phải ngoại lệ (404 có đủ header)
	off := newEcho(t, Deps{})
	if doWith(t, off, "GET", "/swagger/index.html", "", nil).Header().Get("Content-Security-Policy") == "" {
		t.Error("Swagger tắt thì không có ngoại lệ")
	}
}

func TestCORSThroughEcho(t *testing.T) {
	e := newEcho(t, Deps{AllowedOrigins: []string{"http://localhost:3000"}})
	e.POST("/auth/login", func(c echo.Context) error { return c.String(200, "đã vào handler") })
	pre := map[string]string{"Origin": "http://localhost:3000", "Access-Control-Request-Method": "POST", "Access-Control-Request-Headers": "content-type"}

	// preflight tới route chỉ có POST: Echo sẽ trả 405 cho OPTIONS nếu CORS không chặn trước
	rec := doWith(t, e, "OPTIONS", "/auth/login", "", pre)
	if rec.Code != 204 || rec.Header().Get("Access-Control-Allow-Origin") != "http://localhost:3000" ||
		rec.Header().Get("Access-Control-Allow-Credentials") != "true" || rec.Header().Get("Access-Control-Allow-Headers") != "content-type" {
		t.Fatalf("preflight: %d %v", rec.Code, rec.Header())
	}
	if rec.Body.Len() != 0 {
		t.Error("preflight không có body")
	}
	// preflight tới route không tồn tại dưới tiền tố cho phép cũng được trả lời tại middleware
	if rec := doWith(t, e, "OPTIONS", "/oauth2/token", "", pre); rec.Code != 204 || rec.Header().Get("Access-Control-Allow-Origin") == "" {
		t.Errorf("%d %v", rec.Code, rec.Header())
	}
	// request thật
	rec = doWith(t, e, "POST", "/auth/login", "", map[string]string{"Origin": "http://localhost:3000"})
	if rec.Code != 200 || rec.Header().Get("Access-Control-Allow-Origin") != "http://localhost:3000" ||
		!strings.Contains(rec.Header().Get("Access-Control-Expose-Headers"), "X-Request-Id") || !strings.Contains(rec.Header().Get("Vary"), "Origin") {
		t.Errorf("%d %v", rec.Code, rec.Header())
	}
	// header lộ ra có giá trị thật: X-Request-Id do middleware RequestID đặt
	if rec.Header().Get("X-Request-Id") == "" {
		t.Error("X-Request-Id phải có để được lộ ra")
	}
	// origin khác, và đường dẫn ngoài tiền tố
	for name, c := range map[string]struct{ origin, path string }{
		"origin lạ": {"http://evil.example.org", "/auth/login"}, "cổng khác": {"http://localhost:3001", "/auth/login"},
		"ngoài tiền tố": {"http://localhost:3000", "/health/live"},
	} {
		rec := doWith(t, e, "OPTIONS", c.path, "", map[string]string{"Origin": c.origin, "Access-Control-Request-Method": "POST"})
		if rec.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Errorf("%s: không được cho phép: %v", name, rec.Header())
		}
	}
	// không cấu hình origin: không ai được
	none := newEcho(t, Deps{})
	none.POST("/auth/login", func(c echo.Context) error { return c.String(200, "ok") })
	if got := doWith(t, none, "POST", "/auth/login", "", map[string]string{"Origin": "http://localhost:3000"}).Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("mặc định không cho origin nào: %q", got)
	}
}

func TestOversizedBodyIsOperationOutcome(t *testing.T) {
	e := newEcho(t, Deps{})
	e.POST("/auth/login", func(c echo.Context) error {
		_, err := io.ReadAll(c.Request().Body)
		return err
	})
	big := strings.Repeat("a", 2<<20) // 2 MB, vượt giới hạn 1 MB
	req := httptest.NewRequest("POST", "/auth/login", strings.NewReader(big))
	req.ContentLength = int64(len(big))
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	var oo struct {
		ResourceType string
		Issue        []struct {
			Code        string
			Diagnostics string
		}
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &oo); err != nil {
		t.Fatalf("%v: %s", err, rec.Body)
	}
	if rec.Code != 413 || oo.ResourceType != "OperationOutcome" || oo.Issue[0].Diagnostics != "Request body too large" || oo.Issue[0].Code != "too-long" {
		t.Errorf("%d %s", rec.Code, rec.Body)
	}
	if !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/fhir+json") {
		t.Errorf("%s", rec.Header().Get("Content-Type"))
	}
}

func TestCodeForNewStatuses(t *testing.T) {
	for status, want := range map[int]string{413: "too-long", 415: "not-supported", 409: "conflict"} {
		if got := codeFor(status); got != want {
			t.Errorf("%d: %s", status, got)
		}
	}
}

func TestTooManyRequestsErrorRoundsUpSecondsAndSetsRetryAfter(t *testing.T) {
	e := echo.New()
	for path, d := range map[string]time.Duration{"/a": 1500 * time.Millisecond, "/b": 2 * time.Second, "/c": 1 * time.Millisecond, "/d": 0, "/e": -time.Second, "/f": -5 * time.Second} {
		d := d
		e.GET(path, func(c echo.Context) error { return writeError(c, domain.TooManyRequests(d)) })
	}
	for path, want := range map[string]string{"/a": "2", "/b": "2", "/c": "1", "/d": "0", "/e": "0", "/f": "0"} {
		rec := do(t, e, "GET", path)
		var body struct {
			Extension []struct {
				URL   string `json:"url"`
				Value int    `json:"valueUnsignedInt"`
			} `json:"extension"`
			Issue []struct {
				Code string `json:"code"`
			} `json:"issue"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if rec.Code != 429 || rec.Header().Get("Retry-After") != want || len(body.Extension) != 1 || strconv.Itoa(body.Extension[0].Value) != want ||
			body.Extension[0].URL != "https://medplum.com/fhir/StructureDefinition/rate-limit-reset" || body.Issue[0].Code != "throttled" {
			t.Errorf("%s: %d Retry-After=%q %s", path, rec.Code, rec.Header().Get("Retry-After"), rec.Body.String())
		}
	}
}
