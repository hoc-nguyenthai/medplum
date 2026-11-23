package config_test

import (
	"strings"
	"testing"

	"trivita-fhir/apps/fhir-server/internal/config"
)

func TestHTTPSecuritySettings(t *testing.T) {
	// ENVIRONMENT=local mặc định cho phép origin của web dev; hạn mức tắt
	s, err := config.FromEnv(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := s.AllowedOriginList(); len(got) != 1 || got[0] != config.DevAllowedOrigin || s.RateLimit != 0 {
		t.Fatalf("mặc định: %v rate=%v", got, s.RateLimit)
	}
	s, err = config.FromEnv(env(map[string]string{
		"FHIR_ALLOWED_ORIGINS": "https://App.Example.org, https://b.example.org:8443", "FHIR_TRUSTED_PROXIES": "10.0.0.0/8, 192.168.0.0/16",
		"FHIR_RATE_LIMIT": "5.5", "FHIR_RATE_BURST": "20", "ENVIRONMENT": "production", "DATABASE_URL": "postgres://u:p@db/fhir",
	}))
	if err != nil {
		t.Fatal(err)
	}
	origins, _ := s.AllowedOriginList()
	nets, _ := s.TrustedProxyNets()
	if strings.Join(origins, ",") != "https://app.example.org,https://b.example.org:8443" || len(nets) != 2 || s.RateLimit != 5.5 || s.RateBurst != 20 {
		t.Fatalf("%v %v %v %v", origins, nets, s.RateLimit, s.RateBurst)
	}
	// production không có origin mặc định
	if s, _ = config.FromEnv(env(map[string]string{"ENVIRONMENT": "production"})); s.AllowedOrigins != "" {
		t.Errorf("production không được có origin mặc định: %q", s.AllowedOrigins)
	}
}

func TestHTTPSecuritySettingsRejected(t *testing.T) {
	for name, vars := range map[string]map[string]string{
		"origin thiếu scheme":   {"FHIR_ALLOWED_ORIGINS": "localhost:3000"},
		"* ở production":        {"ENVIRONMENT": "production", "FHIR_ALLOWED_ORIGINS": "*"},
		"http ở production":     {"ENVIRONMENT": "staging", "FHIR_ALLOWED_ORIGINS": "http://app.example.org"},
		"proxy không phải CIDR": {"FHIR_TRUSTED_PROXIES": "10.0.0.1"},
		"hạn mức âm":            {"FHIR_RATE_LIMIT": "-1"},
		"burst âm":              {"FHIR_RATE_BURST": "-1"},
		"hạn mức không phải số": {"FHIR_RATE_LIMIT": "nhanh"},
	} {
		if _, err := config.FromEnv(env(vars)); err == nil {
			t.Errorf("%s: phải bị từ chối", name)
		}
	}
	// "*" vẫn dùng được ở dev
	if _, err := config.FromEnv(env(map[string]string{"FHIR_ALLOWED_ORIGINS": "*"})); err != nil {
		t.Errorf("* ở local: %v", err)
	}
}

func TestRateLimitDefaultsOnInProduction(t *testing.T) {
	prod := map[string]string{"ENVIRONMENT": "production", "DATABASE_URL": "postgres://u:p@db/fhir"}
	with := func(k, v string) map[string]string {
		m := map[string]string{}
		for a, b := range prod {
			m[a] = b
		}
		m[k] = v
		return m
	}
	s, err := config.FromEnv(env(prod))
	if err != nil || s.RateLimit != config.DefaultProdRateLimit {
		t.Fatalf("production không đặt: %v %v", s.RateLimit, err)
	}
	if s, _ = config.FromEnv(env(with("ENVIRONMENT", "staging"))); s.RateLimit != config.DefaultProdRateLimit {
		t.Errorf("staging: %v", s.RateLimit)
	}
	if s, _ = config.FromEnv(env(with("FHIR_RATE_LIMIT", "0"))); s.RateLimit != 0 {
		t.Errorf("đặt rõ 0 phải tắt: %v", s.RateLimit)
	}
	if s, _ = config.FromEnv(env(with("FHIR_RATE_LIMIT", "3"))); s.RateLimit != 3 {
		t.Errorf("giá trị đặt phải thắng: %v", s.RateLimit)
	}
	if s, _ = config.FromEnv(env(map[string]string{"ENVIRONMENT": "development"})); s.RateLimit != 0 {
		t.Errorf("ngoài staging/production vẫn tắt: %v", s.RateLimit)
	}
}
