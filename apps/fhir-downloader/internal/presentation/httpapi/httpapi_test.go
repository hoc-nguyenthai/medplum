package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"trivita-fhir/apps/fhir-downloader/internal/presentation/httpapi"
)

func get(t *testing.T, h http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(method, path, nil))
	return w
}

func TestLivenessProbesNeverTouchDependencies(t *testing.T) {
	called := false
	h := httpapi.New(httpapi.Deps{Ready: func(context.Context) (bool, map[string]bool) { called = true; return false, nil }})
	for _, p := range []string{"/healthz", "/health/live", "/health/startup"} {
		w := get(t, h, "GET", p)
		var body httpapi.HealthResponse
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil || body.Status != "ok" {
			t.Errorf("%s: %d %s", p, w.Code, w.Body)
		}
	}
	if called {
		t.Error("liveness không được kiểm phụ thuộc (K8s không được restart pod vì lỗi của Redis hay DB)")
	}
}

func TestReadyFollowsDependencies(t *testing.T) {
	ok := true
	h := httpapi.New(httpapi.Deps{Ready: func(context.Context) (bool, map[string]bool) {
		return ok, map[string]bool{"database": ok, "broker": true}
	}})
	if w := get(t, h, "GET", "/health/ready"); w.Code != 200 {
		t.Fatalf("sẵn sàng: %d", w.Code)
	}
	ok = false
	w := get(t, h, "GET", "/health/ready")
	var body httpapi.ReadyResponse
	if w.Code != 503 || json.Unmarshal(w.Body.Bytes(), &body) != nil || body.Ready || body.Checks["database"] {
		t.Fatalf("không sẵn sàng: %d %s", w.Code, w.Body)
	}
	if w := get(t, httpapi.New(httpapi.Deps{}), "GET", "/health/ready"); w.Code != 200 {
		t.Errorf("không có Ready: %d", w.Code)
	}
}

func TestNoBusinessRoutesAndSecurityHeaders(t *testing.T) {
	h := httpapi.New(httpapi.Deps{})
	for _, c := range []struct{ method, path string }{{"GET", "/"}, {"POST", "/fhir/R4/Patient"}, {"GET", "/storage/x"}, {"POST", "/healthz"}} {
		w := get(t, h, c.method, c.path)
		if w.Code != 404 && w.Code != 405 {
			t.Errorf("%s %s: %d, muốn 404 hoặc 405 (service không có route nghiệp vụ)", c.method, c.path, w.Code)
		}
	}
	w := get(t, h, "GET", "/healthz")
	if w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("X-Frame-Options") == "" {
		t.Errorf("thiếu header bảo mật: %v", w.Header())
	}
}

func TestPanicBecomesPlain500(t *testing.T) {
	h := httpapi.New(httpapi.Deps{Ready: func(context.Context) (bool, map[string]bool) { panic("lộ chi tiết nội bộ") }})
	w := get(t, h, "GET", "/health/ready")
	if w.Code != 500 || w.Body.String() == "" || strings.Contains(w.Body.String(), "lộ chi tiết") {
		t.Fatalf("panic phải thành 500 không lộ chi tiết: %d %s", w.Code, w.Body)
	}
}

// Ghim hợp đồng probe (docs/api-contract.md): thân JSON đúng từng byte, Content-Type và đủ header bảo mật ở MỌI phản hồi (kể cả 404), 404 JSON cho mọi thứ khác.
func TestProbeContractIsPinned(t *testing.T) {
	wantHeaders := map[string]string{
		"Content-Type":            "application/json",
		"Cache-Control":           "no-store, no-cache, must-revalidate",
		"Pragma":                  "no-cache",
		"Content-Security-Policy": "default-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none';",
		"Referrer-Policy":         "no-referrer",
		"X-Content-Type-Options":  "nosniff",
		"X-Frame-Options":         "DENY",
		"X-Xss-Protection":        "1; mode=block",
	}
	ready := true
	checks := map[string]bool{"broker": true, "database": true}
	h := httpapi.New(httpapi.Deps{Ready: func(context.Context) (bool, map[string]bool) { return ready, checks }})
	cases := []struct {
		method, path string
		code         int
		body         string
	}{
		{"GET", "/healthz", 200, `{"status":"ok"}` + "\n"},
		{"GET", "/health/live", 200, `{"status":"ok"}` + "\n"},
		{"GET", "/health/startup", 200, `{"status":"ok"}` + "\n"},
		{"GET", "/health/ready", 200, `{"ready":true,"checks":{"broker":true,"database":true}}` + "\n"},
		{"POST", "/healthz", 404, `{"error":"Not Found"}` + "\n"},
		{"OPTIONS", "/health/ready", 404, `{"error":"Not Found"}` + "\n"},
		{"GET", "/fhir/R4/Patient", 404, `{"error":"Not Found"}` + "\n"},
	}
	for _, c := range cases {
		w := get(t, h, c.method, c.path)
		if w.Code != c.code || w.Body.String() != c.body {
			t.Errorf("%s %s: %d %q, muốn %d %q", c.method, c.path, w.Code, w.Body.String(), c.code, c.body)
		}
		for k, v := range wantHeaders {
			if got := w.Header().Get(k); got != v {
				t.Errorf("%s %s: header %s = %q, muốn %q", c.method, c.path, k, got, v)
			}
		}
		if w.Header().Get("Permissions-Policy") == "" {
			t.Errorf("%s %s: thiếu Permissions-Policy", c.method, c.path)
		}
	}
	ready, checks = false, map[string]bool{"broker": true, "database": false}
	if w := get(t, h, "GET", "/health/ready"); w.Code != 503 || w.Body.String() != `{"ready":false,"checks":{"broker":true,"database":false}}`+"\n" {
		t.Errorf("database hỏng (broker còn sống): %d %q", w.Code, w.Body.String())
	}
	ready, checks = false, map[string]bool{"broker": false, "database": true}
	if w := get(t, h, "GET", "/health/ready"); w.Code != 503 || w.Body.String() != `{"ready":false,"checks":{"broker":false,"database":true}}`+"\n" {
		t.Errorf("Redis hỏng (database còn sống): %d %q", w.Code, w.Body.String())
	}
	ready, checks = false, map[string]bool{"draining": true}
	if w := get(t, h, "GET", "/health/ready"); w.Code != 503 || w.Body.String() != `{"ready":false,"checks":{"draining":true}}`+"\n" {
		t.Errorf("đang tắt: %d %q", w.Code, w.Body.String())
	}
	if w := get(t, httpapi.New(httpapi.Deps{}), "GET", "/health/ready"); w.Code != 200 || w.Body.String() != `{"ready":true,"checks":{}}`+"\n" {
		t.Errorf("không có Ready: %d %q", w.Code, w.Body.String())
	}
}
