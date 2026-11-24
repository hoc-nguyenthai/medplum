package config_test

import (
	"strings"
	"testing"

	"trivita-fhir/apps/dicomweb/internal/config"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

// base là cấu hình tối thiểu hợp lệ ở local.
func base() map[string]string {
	return map[string]string{
		"FHIR_JWT_ISSUER": "https://id.test", "FHIR_JWT_AUDIENCE": "fhir", "FHIR_JWT_HMAC_SECRET": strings.Repeat("k", 32),
		"FHIR_BINARY_STORAGE": "file:/tmp/dicom",
	}
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
	return with(base(), "ENVIRONMENT", "production", "FHIR_JWT_HMAC_SECRET", "", "FHIR_JWKS_URL", "https://id.example.org/jwks.json",
		"DATABASE_URL", "postgres://u:p@db/fhir", "DICOMWEB_BASE_URL", "https://dicom.example.org")
}

func TestDefaultsAreValidForLocal(t *testing.T) {
	s, err := config.FromEnv(env(base()))
	if err != nil {
		t.Fatal(err)
	}
	if s.Addr != ":8008" || s.Environment != "local" || s.RateLimit != 0 || s.AllowedOrigins != config.DevAllowedOrigin {
		t.Errorf("mặc định: %+v", s)
	}
	if s.MaxInstanceBytes != 1<<30 || s.MaxRequestBytes != 4<<30 || s.MaxHeaderBytes != 1<<20 {
		t.Errorf("trần tải lên: %d %d %d", s.MaxInstanceBytes, s.MaxRequestBytes, s.MaxHeaderBytes)
	}
}

func TestInvalidConfigIsRejectedWithTheVariableName(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"thiếu issuer", with(base(), "FHIR_JWT_ISSUER", ""), "FHIR_JWT_ISSUER"},
		{"thiếu audience", with(base(), "FHIR_JWT_AUDIENCE", ""), "FHIR_JWT_AUDIENCE"},
		{"không có khóa", with(base(), "FHIR_JWT_HMAC_SECRET", ""), "FHIR_JWKS_URL hoặc FHIR_JWT_HMAC_SECRET"},
		{"cả hai khóa", with(base(), "FHIR_JWKS_URL", "https://id.test/jwks"), "FHIR_JWKS_URL hoặc FHIR_JWT_HMAC_SECRET"},
		{"HMAC ngắn", with(base(), "FHIR_JWT_HMAC_SECRET", "short"), "FHIR_JWT_HMAC_SECRET: tối thiểu"},
		{"thiếu kho", with(base(), "FHIR_BINARY_STORAGE", ""), "FHIR_BINARY_STORAGE: bắt buộc"},
		{"kho lạ", with(base(), "FHIR_BINARY_STORAGE", "azure:x"), "FHIR_BINARY_STORAGE: chỉ hỗ trợ"},
		{"file thiếu thư mục", with(base(), "FHIR_BINARY_STORAGE", "file:"), "thiếu thư mục"},
		{"s3 thiếu vùng", with(base(), "FHIR_BINARY_STORAGE", "s3:b", "FHIR_S3_ACCESS_KEY_ID", "a", "FHIR_S3_SECRET_ACCESS_KEY", "b"), "FHIR_S3_REGION"},
		{"s3 thiếu khóa", with(base(), "FHIR_BINARY_STORAGE", "s3:b", "FHIR_S3_REGION", "r"), "FHIR_S3_ACCESS_KEY_ID"},
		{"s3 endpoint có đường dẫn", with(base(), "FHIR_BINARY_STORAGE", "s3:b", "FHIR_S3_REGION", "r", "FHIR_S3_ACCESS_KEY_ID", "a", "FHIR_S3_SECRET_ACCESS_KEY", "b", "FHIR_S3_ENDPOINT", "http://h/x"), "FHIR_S3_ENDPOINT"},
		{"base url sai", with(base(), "DICOMWEB_BASE_URL", "localhost:8008"), "DICOMWEB_BASE_URL"},
		{"trần instance lớn hơn trần yêu cầu", with(base(), "DICOMWEB_MAX_INSTANCE_BYTES", "100", "DICOMWEB_MAX_REQUEST_BYTES", "50"), "DICOMWEB_MAX_INSTANCE_BYTES"},
		{"trần không dương", with(base(), "DICOMWEB_MAX_HEADER_BYTES", "0"), "DICOMWEB_MAX_HEADER_BYTES"},
		{"proxy sai", with(base(), "DICOMWEB_TRUSTED_PROXIES", "not-a-cidr"), "DICOMWEB_TRUSTED_PROXIES"},
		{"origin sai", with(base(), "DICOMWEB_ALLOWED_ORIGINS", "ftp://x"), "DICOMWEB_ALLOWED_ORIGINS"},
		{"workers pixelData bằng 0", with(base(), "DICOMWEB_PIXELDATA_WORKERS", "0"), "DICOMWEB_PIXELDATA_WORKERS"},
		{"workers pixelData quá nhiều", with(base(), "DICOMWEB_PIXELDATA_WORKERS", "100"), "DICOMWEB_PIXELDATA_WORKERS"},
		{"hạn mức âm", with(base(), "DICOMWEB_RATE_LIMIT", "-1"), "DICOMWEB_RATE_LIMIT"},
		{"schema riêng với sqlite", with(base(), "DATABASE_REQUIRE_SCHEMA", "true"), "DATABASE_REQUIRE_SCHEMA"},
		{"giá trị không phải số", with(base(), "DICOMWEB_MAX_HEADER_BYTES", "abc"), "DICOMWEB_MAX_HEADER_BYTES"},
	}
	for _, c := range cases {
		_, err := config.FromEnv(env(c.env))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: muốn lỗi chứa %q, được %v", c.name, c.want, err)
		}
	}
}

func TestProductionConstraints(t *testing.T) {
	s, err := config.FromEnv(env(prod()))
	if err != nil {
		t.Fatalf("cấu hình production hợp lệ bị từ chối: %v", err)
	}
	if s.RateLimit != config.DefaultProdRateLimit {
		t.Errorf("production không đặt hạn mức phải bật mặc định %d, được %v", config.DefaultProdRateLimit, s.RateLimit)
	}
	if s, _ := config.FromEnv(env(with(prod(), "DICOMWEB_RATE_LIMIT", "0"))); s.RateLimit != 0 {
		t.Errorf("đặt rõ 0 phải tắt: %v", s.RateLimit)
	}
	for name, e := range map[string]map[string]string{
		"HMAC":             with(prod(), "FHIR_JWKS_URL", "", "FHIR_JWT_HMAC_SECRET", strings.Repeat("k", 32)),
		"JWKS http":        with(prod(), "FHIR_JWKS_URL", "http://id.example.org/jwks.json"),
		"SQLite":           with(prod(), "DATABASE_URL", "sqlite://./x.db"),
		"base url http":    with(prod(), "DICOMWEB_BASE_URL", "http://dicom.example.org"),
		"origin *":         with(prod(), "DICOMWEB_ALLOWED_ORIGINS", "*"),
		"origin http":      with(prod(), "DICOMWEB_ALLOWED_ORIGINS", "http://app.example.org"),
		"S3 endpoint http": with(prod(), "FHIR_BINARY_STORAGE", "s3:b", "FHIR_S3_REGION", "r", "FHIR_S3_ACCESS_KEY_ID", "a", "FHIR_S3_SECRET_ACCESS_KEY", "b", "FHIR_S3_ENDPOINT", "http://minio:9000"),
	} {
		if _, err := config.FromEnv(env(e)); err == nil {
			t.Errorf("production với %s phải bị từ chối", name)
		}
	}
}

func TestStorageParsing(t *testing.T) {
	s, err := config.FromEnv(env(with(base(), "FHIR_BINARY_STORAGE", "s3:bucket/a/b/", "FHIR_S3_REGION", "r", "FHIR_S3_ACCESS_KEY_ID", "a", "FHIR_S3_SECRET_ACCESS_KEY", "b")))
	if err != nil {
		t.Fatal(err)
	}
	if s.StorageKind() != "s3" || s.S3Bucket() != "bucket" || s.S3Prefix() != "a/b/" {
		t.Errorf("s3: %q %q %q", s.StorageKind(), s.S3Bucket(), s.S3Prefix())
	}
	f, _ := config.FromEnv(env(base()))
	if f.StorageKind() != "file" || f.StorageDir() != "/tmp/dicom" {
		t.Errorf("file: %q %q", f.StorageKind(), f.StorageDir())
	}
}

func TestAllErrorsAreReportedTogether(t *testing.T) {
	_, err := config.FromEnv(env(map[string]string{}))
	if err == nil {
		t.Fatal("cấu hình rỗng phải bị từ chối")
	}
	for _, v := range []string{"FHIR_JWT_ISSUER", "FHIR_BINARY_STORAGE"} {
		if !strings.Contains(err.Error(), v) {
			t.Errorf("thiếu %s trong lỗi gộp: %v", v, err)
		}
	}
}
