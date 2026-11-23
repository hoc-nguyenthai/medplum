package config_test

import (
	"strings"
	"testing"
	"time"

	"trivita-fhir/apps/ws-gateway/internal/config"
)

var key32 = strings.Repeat("k", 40)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func with(over map[string]string) map[string]string {
	m := map[string]string{"FHIR_WS_SIGNING_KEY": key32}
	for k, v := range over {
		m[k] = v
	}
	return m
}

func TestDefaultsAreValidForLocal(t *testing.T) {
	s, err := config.FromEnv(env(with(nil)))
	if err != nil {
		t.Fatal(err)
	}
	if s.Addr != ":8007" || s.WSMaxConnections != 1000 || s.WSMaxBindingsPerUser != 20 || s.WSHeartbeat != 30*time.Second ||
		s.WSCloseTimeout != 5*time.Second || s.WSPresenceTTL != 30*time.Second || s.RedisURL != config.DefaultRedisURL || s.RateLimit != 0 {
		t.Errorf("%+v", s)
	}
}

func TestValidationErrors(t *testing.T) {
	prod := map[string]string{"ENVIRONMENT": "production", "REDIS_URL": "redis://r:6379/0"}
	for name, c := range map[string]struct {
		env  map[string]string
		want string
	}{
		"thiếu khóa": {map[string]string{}, "FHIR_WS_SIGNING_KEY: bắt buộc"},
		"khóa ngắn":  {map[string]string{"FHIR_WS_SIGNING_KEY": "short"}, "tối thiểu 32"},
		"khóa cũ ngắn": {with(map[string]string{"FHIR_WS_SIGNING_KEY_PREVIOUS": "short"}),
			"mỗi khóa tối thiểu 32"},
		"quá nhiều khóa cũ": {with(map[string]string{"FHIR_WS_SIGNING_KEY_PREVIOUS": strings.Join([]string{strings.Repeat("a", 32), strings.Repeat("b", 32), strings.Repeat("c", 32), strings.Repeat("d", 32)}, ",")}),
			"tối đa 3"},
		"production dùng Redis mặc định": {with(map[string]string{"ENVIRONMENT": "production"}), "REDIS_URL"},
		"staging dùng Redis mặc định":    {with(map[string]string{"ENVIRONMENT": "staging"}), "REDIS_URL"},
		"giới hạn sai":                   {with(map[string]string{"FHIR_WS_MAX_CONNECTIONS": "0"}), "phải > 0"},
		"hạn mức bind sai":               {with(map[string]string{"FHIR_WS_MAX_BINDINGS_PER_USER": "0"}), "phải > 0"},
		"heartbeat sai":                  {with(map[string]string{"FHIR_WS_HEARTBEAT": "0s"}), "phải > 0"},
		"giới hạn IP âm":                 {with(map[string]string{"FHIR_WS_MAX_CONNS_PER_IP": "-1"}), "MAX_CONNS_PER_IP"},
		"TTL quá ngắn":                   {with(map[string]string{"FHIR_WS_PRESENCE_TTL": "1s"}), "PRESENCE_TTL"},
		"close lớn hơn shutdown":         {with(map[string]string{"FHIR_WS_CLOSE_TIMEOUT": "30s", "SHUTDOWN_TIMEOUT": "10s"}), "CLOSE_TIMEOUT"},
		"origin là URL":                  {with(map[string]string{"FHIR_WS_ALLOWED_ORIGINS": "https://app.example.org"}), "mẫu host"},
		"origin có đường dẫn":            {with(map[string]string{"FHIR_WS_ALLOWED_ORIGINS": "app.example.org/x"}), "mẫu host"},
		"origin sao ở production":        {with(map[string]string{"FHIR_WS_ALLOWED_ORIGINS": "*", "ENVIRONMENT": "production", "REDIS_URL": "redis://r:6379/0"}), `"*" không được dùng`},
		"proxy sai":                      {with(map[string]string{"FHIR_WS_TRUSTED_PROXIES": "không-phải-cidr"}), "không phải CIDR"},
		"rate âm":                        {with(map[string]string{"FHIR_WS_RATE_LIMIT": "-1"}), "RATE_LIMIT"},
		"bool hỏng":                      {with(map[string]string{"DEBUG": "maybe"}), "DEBUG"},
	} {
		_, err := config.FromEnv(env(c.env))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, muốn chứa %q", name, err, c.want)
		}
	}
	// các cấu hình hợp lệ tương ứng
	for name, e := range map[string]map[string]string{
		"production đầy đủ": with(prod),
		"origin mẫu host":   with(map[string]string{"FHIR_WS_ALLOWED_ORIGINS": "app.example.org, *.example.org"}),
		"origin * ở local":  with(map[string]string{"FHIR_WS_ALLOWED_ORIGINS": "*"}),
		"proxy hợp lệ":      with(map[string]string{"FHIR_WS_TRUSTED_PROXIES": "10.0.0.0/8, 192.168.0.0/16"}),
	} {
		if _, err := config.FromEnv(env(e)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestRateLimitDefaults(t *testing.T) {
	prod := with(map[string]string{"ENVIRONMENT": "production", "REDIS_URL": "redis://r:6379/0"})
	if s, err := config.FromEnv(env(prod)); err != nil || s.RateLimit != config.DefaultProdRateLimit {
		t.Errorf("production không đặt: %v %v", s.RateLimit, err)
	}
	off := with(map[string]string{"ENVIRONMENT": "production", "REDIS_URL": "redis://r:6379/0", "FHIR_WS_RATE_LIMIT": "0"})
	if s, err := config.FromEnv(env(off)); err != nil || s.RateLimit != 0 {
		t.Errorf("production đặt rõ 0 phải tắt: %v %v", s.RateLimit, err)
	}
	if s, err := config.FromEnv(env(with(map[string]string{"FHIR_WS_RATE_LIMIT": "3.5"}))); err != nil || s.RateLimit != 3.5 {
		t.Errorf("local đặt 3.5: %v %v", s.RateLimit, err)
	}
}

func TestListHelpers(t *testing.T) {
	s, err := config.FromEnv(env(with(map[string]string{
		"FHIR_WS_SIGNING_KEY_PREVIOUS": " " + strings.Repeat("a", 32) + " , " + strings.Repeat("b", 32) + ",,",
		"FHIR_WS_ALLOWED_ORIGINS":      " a.example.org ,, *.b.example.org ",
		"FHIR_WS_TRUSTED_PROXIES":      "10.0.0.0/8,",
	})))
	if err != nil {
		t.Fatal(err)
	}
	if p := s.PreviousKeys(); len(p) != 2 || p[0] != strings.Repeat("a", 32) {
		t.Errorf("PreviousKeys = %v", p)
	}
	if o := s.OriginPatterns(); len(o) != 2 || o[0] != "a.example.org" || o[1] != "*.b.example.org" {
		t.Errorf("OriginPatterns = %v", o)
	}
	if n, err := s.TrustedProxyNets(); err != nil || len(n) != 1 {
		t.Errorf("TrustedProxyNets = %v %v", n, err)
	}
	if s.IsProd() {
		t.Error("local không phải prod")
	}
}
