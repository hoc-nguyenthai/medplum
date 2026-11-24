package config_test

import (
	"strings"
	"testing"
	"time"

	"trivita-fhir/apps/fhir-downloader/internal/config"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

// base là cấu hình tối thiểu hợp lệ ở local.
func base() map[string]string {
	return map[string]string{"FHIR_BINARY_STORAGE": "file:/tmp/blobs"}
}

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
	return with(base(), "ENVIRONMENT", "production", "DATABASE_URL", "postgres://u:p@db/fhir", "REDIS_URL", "redis://redis:6379/0",
		"FHIR_BASE_URL", "https://fhir.example.org/fhir/R4")
}

func TestDefaultsAreValidForLocal(t *testing.T) {
	s, err := config.FromEnv(env(base()))
	if err != nil {
		t.Fatal(err)
	}
	if s.Addr != ":8009" || s.Environment != "local" || s.BrokerGroup != "fhir-downloader" || s.AutoDownloadTimeout != 30*time.Second || s.AutoDownloadAllowPrivate {
		t.Errorf("mặc định: %+v", s)
	}
	if s.MaxBytes() != 256<<20 {
		t.Errorf("trần mặc định phải theo FHIR_STORAGE_MAX_UPLOAD_BYTES: %d", s.MaxBytes())
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
		{"thiếu kho", with(base(), "FHIR_BINARY_STORAGE", ""), "FHIR_BINARY_STORAGE: bắt buộc"},
		{"kho lạ", with(base(), "FHIR_BINARY_STORAGE", "gcs:bucket"), "FHIR_BINARY_STORAGE: chỉ hỗ trợ"},
		{"file thiếu thư mục", with(base(), "FHIR_BINARY_STORAGE", "file:"), "thiếu thư mục"},
		{"s3 thiếu mọi thứ", with(base(), "FHIR_BINARY_STORAGE", "s3:bucket/p"), "FHIR_S3_REGION"},
		{"s3 endpoint có đường dẫn", with(base(), "FHIR_BINARY_STORAGE", "s3:b", "FHIR_S3_REGION", "r", "FHIR_S3_ACCESS_KEY_ID", "a", "FHIR_S3_SECRET_ACCESS_KEY", "s", "FHIR_S3_ENDPOINT", "http://h/x"), "FHIR_S3_ENDPOINT"},
		{"Redis sai dạng", with(base(), "REDIS_URL", "http://x"), "REDIS_URL: phải dạng"},
		{"group trùng fhir-server", with(base(), "BROKER_GROUP", "trivita"), "BROKER_GROUP"},
		{"group rỗng", with(base(), "BROKER_GROUP", " "), "BROKER_GROUP"},
		{"base URL sai", with(base(), "FHIR_BASE_URL", "ftp://x"), "FHIR_BASE_URL"},
		{"storage base URL sai", with(base(), "FHIR_STORAGE_BASE_URL", "x"), "FHIR_STORAGE_BASE_URL"},
		{"hạn tải 0", with(base(), "FHIR_AUTO_DOWNLOAD_TIMEOUT", "0s"), "FHIR_AUTO_DOWNLOAD_TIMEOUT"},
		{"trần tải âm", with(base(), "FHIR_AUTO_DOWNLOAD_MAX_BYTES", "-1"), "FHIR_AUTO_DOWNLOAD_MAX_BYTES"},
		{"readiness 0", with(base(), "READINESS_TIMEOUT", "0s"), "READINESS_TIMEOUT"},
		{"shutdown 0", with(base(), "SHUTDOWN_TIMEOUT", "0s"), "SHUTDOWN_TIMEOUT"},
		{"require schema với sqlite", with(base(), "DATABASE_REQUIRE_SCHEMA", "true"), "DATABASE_REQUIRE_SCHEMA"},
		{"production dùng SQLite", with(prod(), "DATABASE_URL", "sqlite://./x.db"), "không dùng SQLite"},
		{"production cho phép địa chỉ nội bộ", with(prod(), "FHIR_AUTO_DOWNLOAD_ALLOW_PRIVATE", "true"), "FHIR_AUTO_DOWNLOAD_ALLOW_PRIVATE"},
		{"production S3 qua http", with(prod(), "FHIR_BINARY_STORAGE", "s3:b", "FHIR_S3_REGION", "r", "FHIR_S3_ACCESS_KEY_ID", "a", "FHIR_S3_SECRET_ACCESS_KEY", "s", "FHIR_S3_ENDPOINT", "http://s3:9000"), "https"},
		{"boolean sai", with(base(), "DATABASE_ECHO", "maybe"), "DATABASE_ECHO"},
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
	_, err := config.FromEnv(env(map[string]string{"REDIS_URL": "http://x", "BROKER_GROUP": "trivita"}))
	if err == nil {
		t.Fatal("muốn lỗi")
	}
	for _, want := range []string{"FHIR_BINARY_STORAGE", "REDIS_URL", "BROKER_GROUP"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("thiếu %s trong %v", want, err)
		}
	}
}

func TestDownloadLimitsAndPrefixes(t *testing.T) {
	s, err := config.FromEnv(env(with(base(), "FHIR_AUTO_DOWNLOAD_MAX_BYTES", "1000", "FHIR_BASE_URL", "https://fhir.example.org/fhir/R4/",
		"FHIR_AUTO_DOWNLOAD_ALLOWED_PREFIXES", " https://a/ , ,https://b/x ")))
	if err != nil {
		t.Fatal(err)
	}
	if s.MaxBytes() != 1000 {
		t.Errorf("MaxBytes: %d", s.MaxBytes())
	}
	if got := config.Prefixes(s.AutoDownloadAllowedPrefixes); len(got) != 2 || got[0] != "https://a/" || got[1] != "https://b/x" {
		t.Errorf("tách tiền tố: %q", got)
	}
	own := s.OwnPrefixes()
	if len(own) != 2 || own[0] != "https://fhir.example.org/fhir/R4/" || own[1] != "https://fhir.example.org/storage" {
		t.Errorf("tiền tố của chính hệ thống: %q", own)
	}
	if s2, _ := config.FromEnv(env(with(base(), "FHIR_STORAGE_BASE_URL", "https://files.example.org/s/"))); s2.OwnPrefixes()[1] != "https://files.example.org/s" {
		t.Errorf("FHIR_STORAGE_BASE_URL: %q", s2.OwnPrefixes())
	}
}
