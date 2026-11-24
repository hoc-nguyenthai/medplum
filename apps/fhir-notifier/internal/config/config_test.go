package config_test

import (
	"strings"
	"testing"
	"time"

	"trivita-fhir/apps/fhir-notifier/internal/config"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func with(m map[string]string, kv ...string) map[string]string {
	out := map[string]string{}
	for k, v := range m {
		out[k] = v
	}
	for i := 0; i+1 < len(kv); i += 2 {
		out[kv[i]] = kv[i+1]
	}
	return out
}

func prod() map[string]string {
	return map[string]string{"ENVIRONMENT": "production", "DATABASE_URL": "postgres://u:p@db/fhir", "REDIS_URL": "redis://redis:6379/0"}
}

func TestDefaultsAreValidForLocal(t *testing.T) {
	s, err := config.FromEnv(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if s.Addr != ":8010" || s.BrokerGroup != "fhir-notifier" || s.WebhookWorkers != 4 || s.WebhookMaxAttempts != 5 || s.WebhookTimeout != 10*time.Second || s.WSIssuer() || s.WebhookAllowPrivate {
		t.Errorf("mặc định: %+v", s)
	}
}

func TestProductionBaselineIsValid(t *testing.T) {
	if _, err := config.FromEnv(env(prod())); err != nil {
		t.Fatalf("cấu hình production tối thiểu phải hợp lệ: %v", err)
	}
}

func TestInvalidConfigIsRejectedWithTheVariableName(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"Redis sai dạng", map[string]string{"REDIS_URL": "http://x"}, "REDIS_URL: phải dạng"},
		{"group trùng fhir-server", map[string]string{"BROKER_GROUP": "trivita"}, "BROKER_GROUP"},
		{"group rỗng", map[string]string{"BROKER_GROUP": " "}, "BROKER_GROUP"},
		{"workers 0", map[string]string{"FHIR_WEBHOOK_WORKERS": "0"}, "FHIR_WEBHOOK_WORKERS"},
		{"workers quá lớn", map[string]string{"FHIR_WEBHOOK_WORKERS": "1000"}, "FHIR_WEBHOOK_WORKERS"},
		{"số lần thử 0", map[string]string{"FHIR_WEBHOOK_MAX_ATTEMPTS": "0"}, "FHIR_WEBHOOK_MAX_ATTEMPTS"},
		{"số lần thử quá lớn", map[string]string{"FHIR_WEBHOOK_MAX_ATTEMPTS": "99"}, "FHIR_WEBHOOK_MAX_ATTEMPTS"},
		{"hạn 0", map[string]string{"FHIR_WEBHOOK_TIMEOUT": "0s"}, "FHIR_WEBHOOK_TIMEOUT"},
		{"tự tắt không phải JSON", map[string]string{"FHIR_SUBSCRIPTION_AUTO_DISABLE": "abc"}, "FHIR_SUBSCRIPTION_AUTO_DISABLE"},
		{"tự tắt ngưỡng 0", map[string]string{"FHIR_SUBSCRIPTION_AUTO_DISABLE": `[{"maxConsecutiveFailures":0,"timeWindowSeconds":60}]`}, "số nguyên dương"},
		{"tự tắt khóa lạ", map[string]string{"FHIR_SUBSCRIPTION_AUTO_DISABLE": `[{"maxConsecutiveFailures":1,"timeWindowSeconds":60,"x":1}]`}, "FHIR_SUBSCRIPTION_AUTO_DISABLE"},
		{"websocket embedded", map[string]string{"FHIR_WS_MODE": "embedded"}, "FHIR_WS_MODE=embedded"},
		{"websocket lạ", map[string]string{"FHIR_WS_MODE": "x"}, "FHIR_WS_MODE"},
		{"readiness 0", map[string]string{"READINESS_TIMEOUT": "0s"}, "READINESS_TIMEOUT"},
		{"shutdown 0", map[string]string{"SHUTDOWN_TIMEOUT": "0s"}, "SHUTDOWN_TIMEOUT"},
		{"require schema với sqlite", map[string]string{"DATABASE_REQUIRE_SCHEMA": "true"}, "DATABASE_REQUIRE_SCHEMA"},
		{"production dùng SQLite", with(prod(), "DATABASE_URL", "sqlite://./x.db"), "không dùng SQLite"},
		{"production cho phép địa chỉ nội bộ", with(prod(), "FHIR_WEBHOOK_ALLOW_PRIVATE", "true"), "FHIR_WEBHOOK_ALLOW_PRIVATE"},
		{"boolean sai", map[string]string{"DATABASE_ECHO": "maybe"}, "DATABASE_ECHO"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := config.FromEnv(env(c.env))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("muốn lỗi nêu %q, được %v", c.want, err)
			}
		})
	}
}

func TestAllErrorsAreReportedTogether(t *testing.T) {
	_, err := config.FromEnv(env(map[string]string{"REDIS_URL": "http://x", "BROKER_GROUP": "trivita", "FHIR_WEBHOOK_WORKERS": "0"}))
	if err == nil {
		t.Fatal("muốn lỗi")
	}
	for _, want := range []string{"REDIS_URL", "BROKER_GROUP", "FHIR_WEBHOOK_WORKERS"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("thiếu %s trong %v", want, err)
		}
	}
}

func TestSharedEnvFileWithFhirServerIsAccepted(t *testing.T) {
	// cùng tệp môi trường với fhir-server: các biến của fhir-server mà notifier không đọc không gây lỗi; biến chung được hiểu
	s, err := config.FromEnv(env(map[string]string{
		"FHIR_SUBSCRIPTIONS": "true", "FHIR_SUBSCRIPTION_DELIVERY": "false", "BROKER_DRIVER": "redis", "FHIR_WS_MODE": "issuer", "FHIR_WS_SIGNING_KEY": strings.Repeat("w", 40),
		"FHIR_WEBHOOK_WORKERS": "8", "FHIR_SUBSCRIPTION_AUTO_DISABLE": `[{"maxConsecutiveFailures":3,"timeWindowSeconds":60}]`,
	}))
	if err != nil {
		t.Fatal(err)
	}
	trig, err := s.AutoDisableTriggers()
	if err != nil || len(trig) != 1 || trig[0].MaxConsecutiveFailures != 3 || !s.WSIssuer() || s.WebhookWorkers != 8 {
		t.Errorf("%+v %v %v", s, trig, err)
	}
}
