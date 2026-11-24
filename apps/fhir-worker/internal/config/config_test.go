package config_test

import (
	"strings"
	"testing"
	"time"

	"trivita-fhir/apps/fhir-worker/internal/config"
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

func key() string { return strings.Repeat("k", 40) }

func prod() map[string]string {
	return map[string]string{"ENVIRONMENT": "production", "DATABASE_URL": "postgres://u:p@db/fhir", "FHIR_BASE_URL": "https://fhir.example.org/fhir/R4"}
}

func prodJWT() map[string]string {
	return with(prod(), "FHIR_JWT_ISSUER", "https://id.example.org", "FHIR_JOB_SIGNING_KEY", key())
}

func TestDefaultsAreValidForLocal(t *testing.T) {
	s, err := config.FromEnv(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if s.Addr != ":8011" || s.JobConcurrency != 2 || s.JobMaintenanceInterval != 5*time.Second || s.JobRecoveryGrace != 2*time.Second || s.JobLease != 5*time.Minute || s.JobCancelPoll != 2*time.Second ||
		s.JWTEnabled() || s.StorageEnabled() || s.Publishes() || s.JobKey() != nil {
		t.Errorf("mặc định: %+v", s)
	}
}

func TestProductionBaselines(t *testing.T) {
	if _, err := config.FromEnv(env(prod())); err != nil {
		t.Fatalf("production token tĩnh phải hợp lệ: %v", err)
	}
	s, err := config.FromEnv(env(prodJWT()))
	if err != nil {
		t.Fatalf("production JWT phải hợp lệ: %v", err)
	}
	if string(s.JobKey()) != key() {
		t.Errorf("khóa ký job: %q", s.JobKey())
	}
}

func TestInvalidConfigIsRejectedWithTheVariableName(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"concurrency 0", map[string]string{"FHIR_JOB_CONCURRENCY": "0"}, "FHIR_JOB_CONCURRENCY"},
		{"concurrency quá lớn", map[string]string{"FHIR_JOB_CONCURRENCY": "1000"}, "FHIR_JOB_CONCURRENCY"},
		{"lease 0", map[string]string{"FHIR_JOB_LEASE": "0s"}, "FHIR_JOB_LEASE"},
		{"chu kỳ thăm dò hủy 0", map[string]string{"FHIR_JOB_CANCEL_POLL": "0s"}, "FHIR_JOB_CANCEL_POLL"},
		{"chu kỳ thăm dò hủy âm", map[string]string{"FHIR_JOB_CANCEL_POLL": "-1s"}, "FHIR_JOB_CANCEL_POLL"},
		{"chu kỳ nhận job 0", map[string]string{"FHIR_JOB_MAINTENANCE_INTERVAL": "0s"}, "FHIR_JOB_MAINTENANCE_INTERVAL"},
		{"grace âm", map[string]string{"FHIR_JOB_RECOVERY_GRACE": "-1s"}, "FHIR_JOB_RECOVERY_GRACE"},
		{"checkpoint 0", map[string]string{"FHIR_BATCH_CHECKPOINT_ENTRIES": "0"}, "FHIR_BATCH_CHECKPOINT_ENTRIES"},
		{"base URL sai", map[string]string{"FHIR_BASE_URL": "ftp://x"}, "FHIR_BASE_URL"},
		{"storage base URL sai", map[string]string{"FHIR_STORAGE_BASE_URL": "x"}, "FHIR_STORAGE_BASE_URL"},
		{"kho lạ", map[string]string{"FHIR_BINARY_STORAGE": "gcs:b"}, "FHIR_BINARY_STORAGE: chỉ hỗ trợ"},
		{"file thiếu thư mục", map[string]string{"FHIR_BINARY_STORAGE": "file:"}, "thiếu thư mục"},
		{"s3 thiếu mọi thứ", map[string]string{"FHIR_BINARY_STORAGE": "s3:b"}, "FHIR_S3_REGION"},
		{"JWT thiếu khóa ký job", map[string]string{"FHIR_JWT_ISSUER": "https://id"}, "FHIR_JOB_SIGNING_KEY: bắt buộc"},
		{"khóa ký job ngắn", map[string]string{"FHIR_JWT_ISSUER": "https://id", "FHIR_JOB_SIGNING_KEY": "short"}, "FHIR_JOB_SIGNING_KEY: tối thiểu"},
		{"dev HMAC ngắn", map[string]string{"FHIR_JWT_ISSUER": "https://id", "FHIR_JWT_HMAC_SECRET": "short"}, "FHIR_JWT_HMAC_SECRET: tối thiểu"},
		{"khóa ký job khi không bật JWT", map[string]string{"FHIR_JOB_SIGNING_KEY": key()}, "chỉ dùng khi fhir-server bật JWT"},
		{"production JWT dùng HMAC", with(prod(), "FHIR_JWT_ISSUER", "https://id", "FHIR_JWT_HMAC_SECRET", key()), "FHIR_JOB_SIGNING_KEY: bắt buộc ở staging/production"},
		{"production SQLite", with(prod(), "DATABASE_URL", "sqlite://./x.db"), "không dùng SQLite"},
		{"phát sự kiện nhưng broker bộ nhớ", map[string]string{"FHIR_SUBSCRIPTIONS": "true"}, "BROKER_DRIVER: cần redis"},
		{"tự tải nhưng broker bộ nhớ", map[string]string{"FHIR_AUTO_DOWNLOAD": "true"}, "BROKER_DRIVER: cần redis"},
		{"Redis sai dạng", map[string]string{"FHIR_SUBSCRIPTIONS": "true", "BROKER_DRIVER": "redis", "REDIS_URL": "http://x"}, "REDIS_URL"},
		{"driver lạ", map[string]string{"BROKER_DRIVER": "kafka"}, "BROKER_DRIVER"},
		{"readiness 0", map[string]string{"READINESS_TIMEOUT": "0s"}, "READINESS_TIMEOUT"},
		{"require schema với sqlite", map[string]string{"DATABASE_REQUIRE_SCHEMA": "true"}, "DATABASE_REQUIRE_SCHEMA"},
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

func TestDevHMACDerivesTheSameJobKeyAsFhirServer(t *testing.T) {
	s, err := config.FromEnv(env(map[string]string{"FHIR_JWT_ISSUER": "https://id", "FHIR_JWT_HMAC_SECRET": key()}))
	if err != nil {
		t.Fatal(err)
	}
	// fhir-server: sha256("fhir-job-seal|" + FHIR_JWT_HMAC_SECRET) (apps/fhir-server/internal/bootstrap/container.go)
	got := s.JobKey()
	if len(got) != 32 {
		t.Fatalf("khóa suy ra phải dài 32 byte: %d", len(got))
	}
	s2, _ := config.FromEnv(env(map[string]string{"FHIR_JWT_ISSUER": "https://id", "FHIR_JWT_HMAC_SECRET": key()}))
	if string(s2.JobKey()) != string(got) {
		t.Error("khóa suy ra phải ổn định")
	}
	s3, _ := config.FromEnv(env(map[string]string{"FHIR_JWT_ISSUER": "https://id", "FHIR_JWT_HMAC_SECRET": strings.Repeat("z", 40)}))
	if string(s3.JobKey()) == string(got) {
		t.Error("secret khác phải cho khóa khác")
	}
}

func TestAllErrorsAreReportedTogetherAndPublishRulesFollowFhirServer(t *testing.T) {
	_, err := config.FromEnv(env(map[string]string{"FHIR_JOB_CONCURRENCY": "0", "FHIR_JOB_LEASE": "0s", "FHIR_SUBSCRIPTIONS": "true"}))
	if err == nil {
		t.Fatal("muốn lỗi")
	}
	for _, want := range []string{"FHIR_JOB_CONCURRENCY", "FHIR_JOB_LEASE", "BROKER_DRIVER"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("thiếu %s trong %v", want, err)
		}
	}
	s, err := config.FromEnv(env(map[string]string{"FHIR_AUTO_DOWNLOAD": "true", "BROKER_DRIVER": "redis"}))
	if err != nil || !s.Publishes() {
		t.Errorf("tự tải bật với Redis phải hợp lệ và phát sự kiện: %v %v", s.Publishes(), err)
	}
}

func TestSharedEnvFileWithFhirServerIsAccepted(t *testing.T) {
	// cùng tệp môi trường với fhir-server: các biến fhir-worker không đọc không gây lỗi
	_, err := config.FromEnv(env(map[string]string{
		"FHIR_JOB_EXECUTION": "false", "FHIR_SUBSCRIPTION_DELIVERY": "false", "FHIR_WS_MODE": "issuer", "FHIR_ASYNC_BATCH": "true", "FHIR_RATE_LIMIT": "20",
		"FHIR_JWKS_URL": "https://id/jwks", "FHIR_JWT_AUDIENCE": "fhir", "FHIR_BASE64_BINARY_MAX_BYTES": "2048", "FHIR_UPDATE_AS_CREATE": "true",
	}))
	if err != nil {
		t.Fatal(err)
	}
}
