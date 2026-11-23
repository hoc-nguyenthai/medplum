package bootstrap_test

import (
	"net/http"
	"strings"
	"testing"

	"trivita-fhir/apps/fhir-server/internal/config"
)

func TestSecurityHeadersAndCORSOnFHIR(t *testing.T) {
	_, srv := newServer(t, settings(func(s *config.Settings) {
		s.AllowedOrigins = "http://localhost:3000"
		s.BaseURL = "https://fhir.test/fhir/R4"
	}))
	do := doer(t, srv)

	r := do("GET", "/fhir/R4/Patient", readSecret, "Origin", "http://localhost:3000")
	for k, v := range map[string]string{
		"X-Content-Type-Options": "nosniff", "X-Frame-Options": "DENY", "Referrer-Policy": "no-referrer",
		"Cache-Control":               "no-store, no-cache, must-revalidate",
		"Access-Control-Allow-Origin": "http://localhost:3000", "Access-Control-Allow-Credentials": "true",
	} {
		if got := r.header.Get(k); got != v {
			t.Errorf("%s = %q, muốn %q", k, got, v)
		}
	}
	if r.header.Get("Strict-Transport-Security") == "" || !strings.Contains(r.header.Get("Content-Security-Policy"), "default-src 'none'") {
		t.Errorf("thiếu HSTS hoặc CSP: %v", r.header)
	}
	// header bảo mật cũng có trên lỗi 401 (không token)
	if r := do("GET", "/fhir/R4/Patient", "", "Origin", "http://evil.test"); r.status != 401 || r.header.Get("X-Frame-Options") != "DENY" || r.header.Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("401: %d %v", r.status, r.header)
	}
	// preflight không cần token; origin lạ không được header CORS
	if r := do("OPTIONS", "/fhir/R4/Patient", "", "Origin", "http://localhost:3000", "Access-Control-Request-Method", "POST"); r.status != 204 || r.header.Get("Access-Control-Allow-Origin") != "http://localhost:3000" {
		t.Errorf("preflight: %d %v", r.status, r.header)
	}
	if r := do("OPTIONS", "/fhir/R4/Patient", "", "Origin", "http://evil.test", "Access-Control-Request-Method", "POST"); r.header.Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("origin lạ được CORS: %v", r.header)
	}
	// CORS chỉ dưới /fhir/R4; Swagger không bị CSP chặn
	if r := do("GET", "/healthz", "", "Origin", "http://localhost:3000"); r.header.Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("CORS ngoài /fhir/R4: %v", r.header)
	}
	if r := do("GET", "/swagger/doc.json", ""); r.status != 200 || r.header.Get("Content-Security-Policy") != "" {
		t.Errorf("swagger: %d CSP=%q", r.status, r.header.Get("Content-Security-Policy"))
	}
}

func TestNoCORSWithoutAllowedOrigins(t *testing.T) {
	_, srv := newServer(t, settings())
	r := doer(t, srv)("GET", "/fhir/R4/Patient", readSecret, "Origin", "http://localhost:3000")
	if r.header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("mặc định không origin nào: %v", r.header)
	}
}

func TestRateLimitPerIP(t *testing.T) {
	_, srv := newServer(t, settings(func(s *config.Settings) { s.RateLimit, s.RateBurst = 0.001, 3 }))
	do := doer(t, srv)
	for i := 0; i < 3; i++ {
		if r := do("GET", "/fhir/R4/Patient", readSecret); r.status != 200 {
			t.Fatalf("yêu cầu %d: %d", i, r.status)
		}
	}
	r := do("GET", "/fhir/R4/Patient", readSecret)
	if r.status != http.StatusTooManyRequests || r.header.Get("Retry-After") == "" || r.json(t)["resourceType"] != "OperationOutcome" {
		t.Fatalf("muốn 429 kèm Retry-After và OperationOutcome: %d %v %s", r.status, r.header, r.body)
	}
	// hạn mức đứng trước xác thực: dò token sai cũng bị chặn, và 429 vẫn mang header bảo mật
	if r := do("GET", "/fhir/R4/Patient", "sai"); r.status != 429 || r.header.Get("X-Frame-Options") != "DENY" {
		t.Errorf("token sai: %d %v", r.status, r.header)
	}
	// health, readiness không bị giới hạn
	for i := 0; i < 5; i++ {
		if r := do("GET", "/healthz", ""); r.status != 200 {
			t.Fatalf("healthz bị giới hạn: %d", r.status)
		}
	}
}

func TestRateLimitIgnoresSpoofedForwardedFor(t *testing.T) {
	_, srv := newServer(t, settings(func(s *config.Settings) { s.RateLimit, s.RateBurst = 0.001, 1 }))
	do := doer(t, srv)
	do("GET", "/fhir/R4/Patient", readSecret, "X-Forwarded-For", "1.1.1.1")
	if r := do("GET", "/fhir/R4/Patient", readSecret, "X-Forwarded-For", "2.2.2.2"); r.status != 429 {
		t.Fatalf("không khai proxy tin cậy thì X-Forwarded-For không được tách hạn mức: %d", r.status)
	}
}

func TestRateLimitUsesForwardedForFromTrustedProxy(t *testing.T) {
	_, srv := newServer(t, settings(func(s *config.Settings) {
		s.RateLimit, s.RateBurst, s.TrustedProxies = 0.001, 1, "127.0.0.0/8"
	}))
	do := doer(t, srv)
	if r := do("GET", "/fhir/R4/Patient", readSecret, "X-Forwarded-For", "1.1.1.1"); r.status != 200 {
		t.Fatal(r.status)
	}
	if r := do("GET", "/fhir/R4/Patient", readSecret, "X-Forwarded-For", "2.2.2.2"); r.status != 200 {
		t.Fatalf("IP khác phải có hạn mức riêng: %d", r.status)
	}
	if r := do("GET", "/fhir/R4/Patient", readSecret, "X-Forwarded-For", "1.1.1.1"); r.status != 429 {
		t.Fatalf("cùng IP phải bị chặn: %d", r.status)
	}
}
