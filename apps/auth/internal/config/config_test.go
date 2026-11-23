package config

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// testMFAKeys là khóa hợp lệ để cấu hình production qua được kiểm tra bắt buộc (staging/production cần khóa mã hóa mfaSecret và Redis).
// testStateKey là khóa ký state hợp lệ (32 byte base64).
const testStateKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

const testMFAKeys = "k1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestDefaults(t *testing.T) {
	s, err := FromEnv(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if s.Addr != ":8006" || s.AppName != "trivita-auth" || s.Environment != "local" {
		t.Errorf("mặc định sai: %+v", s)
	}
	if !s.RegisterEnabled || !s.NewProjectRegistrationEnabled {
		t.Error("đăng ký phải bật mặc định")
	}
	if s.ReadinessTimeout != 2*time.Second || !s.DatabaseAutoMigrate {
		t.Errorf("mặc định thời gian/migrate sai: %+v", s)
	}
	if !s.SwaggerEnabled() {
		t.Error("swagger phải bật ở local")
	}
}

func TestP1Settings(t *testing.T) {
	s, err := FromEnv(env(nil))
	if err != nil || s.BcryptCost != 10 || !s.PwnedEnabled || s.PwnedTimeout != 3*time.Second || !s.CLIClientEnabled || s.DefaultOAuthClients != "" {
		t.Fatalf("mặc định: %+v %v", s, err)
	}
	s, err = FromEnv(env(map[string]string{
		"AUTH_BCRYPT_COST": "12", "AUTH_PWNED_ENABLED": "false", "AUTH_PWNED_URL": "http://localhost:1/range/",
		"AUTH_PWNED_TIMEOUT": "500ms", "AUTH_CLI_CLIENT_ENABLED": "false", "AUTH_DEFAULT_OAUTH_CLIENTS": `[{"id":"a"}]`,
	}))
	if err != nil || s.BcryptCost != 12 || s.PwnedEnabled || s.PwnedURL != "http://localhost:1/range/" || s.PwnedTimeout != 500*time.Millisecond || s.CLIClientEnabled || s.DefaultOAuthClients == "" {
		t.Fatalf("override: %+v %v", s, err)
	}
}

func TestAllowedOrigins(t *testing.T) {
	list := func(m map[string]string) ([]string, error) {
		s, err := FromEnv(env(m))
		if err != nil {
			return nil, err
		}
		return s.AllowedOriginList()
	}
	// ENVIRONMENT=local mặc định cho phép ứng dụng web ở localhost:3000
	if got, err := list(nil); err != nil || len(got) != 1 || got[0] != "http://localhost:3000" {
		t.Errorf("mặc định ở local: %v %v", got, err)
	}
	if got, _ := list(map[string]string{"ENVIRONMENT": "development"}); len(got) != 0 {
		t.Errorf("chỉ local mới có mặc định: %v", got)
	}
	if got, _ := list(map[string]string{"ENVIRONMENT": "production", "DATABASE_URL": "postgres://u:p@h/db", "AUTH_BASE_URL": "https://a.example.org", "AUTH_SIGNING_KEY_PATH": "/keys", "AUTH_MFA_SECRET_KEYS": testMFAKeys, "AUTH_EXTERNAL_STATE_KEY": testStateKey, "AUTH_REDIS_URL": "redis://localhost:6379"}); len(got) != 0 {
		t.Errorf("production không có mặc định: %v", got)
	}
	got, err := list(map[string]string{"AUTH_ALLOWED_ORIGINS": " http://localhost:3000/ , HTTPS://App.Example.org "})
	if err != nil || strings.Join(got, "|") != "http://localhost:3000|https://app.example.org" {
		t.Errorf("chuẩn hóa: %v %v", got, err)
	}
	if got, err := list(map[string]string{"AUTH_ALLOWED_ORIGINS": "*"}); err != nil || len(got) != 1 || got[0] != "*" {
		t.Errorf("* được ở dev: %v %v", got, err)
	}
	prod := func(origins string) error {
		_, err := FromEnv(env(map[string]string{
			"ENVIRONMENT": "production", "DATABASE_URL": "postgres://u:p@h/db", "AUTH_BASE_URL": "https://a.example.org", "AUTH_SIGNING_KEY_PATH": "/keys", "AUTH_MFA_SECRET_KEYS": testMFAKeys, "AUTH_EXTERNAL_STATE_KEY": testStateKey, "AUTH_REDIS_URL": "redis://localhost:6379", "AUTH_ALLOWED_ORIGINS": origins,
		}))
		return err
	}
	if err := prod("https://app.example.org"); err != nil {
		t.Errorf("https hợp lệ ở production: %v", err)
	}
	for _, bad := range []string{"*", "http://app.example.org", "http://localhost:3000"} {
		if err := prod(bad); err == nil || !strings.Contains(err.Error(), "AUTH_ALLOWED_ORIGINS") {
			t.Errorf("production phải từ chối %q: %v", bad, err)
		}
	}
	for _, bad := range []string{"localhost:3000", "http://a.org/path", "ftp://a.org", "http://u:p@a.org"} {
		if _, err := FromEnv(env(map[string]string{"AUTH_ALLOWED_ORIGINS": bad})); err == nil || !strings.Contains(err.Error(), "AUTH_ALLOWED_ORIGINS") {
			t.Errorf("phải từ chối %q: %v", bad, err)
		}
	}
}

func TestOverrides(t *testing.T) {
	s, err := FromEnv(env(map[string]string{
		"HTTP_ADDR": ":9", "AUTH_REGISTER_ENABLED": "false", "AUTH_ISSUER": "http://localhost:9",
		"AUTH_BASE_URL": "http://localhost:9", "DATABASE_URL": "sqlite://./x.db",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if s.Addr != ":9" || s.RegisterEnabled || s.Issuer != "http://localhost:9" {
		t.Errorf("override sai: %+v", s)
	}
}

func TestValidation(t *testing.T) {
	prod := map[string]string{
		"ENVIRONMENT": "production", "DATABASE_URL": "postgres://u:p@h/db", "AUTH_BASE_URL": "https://a.example.org", "AUTH_SIGNING_KEY_PATH": "/keys", "AUTH_MFA_SECRET_KEYS": testMFAKeys, "AUTH_EXTERNAL_STATE_KEY": testStateKey, "AUTH_REDIS_URL": "redis://localhost:6379",
	}
	with := func(kv ...string) map[string]string {
		m := map[string]string{}
		for k, v := range prod {
			m[k] = v
		}
		for i := 0; i < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return m
	}
	cases := []struct {
		name string
		env  map[string]string
		want string // rỗng = hợp lệ
	}{
		{"production hợp lệ", with(), ""},
		{"environment lạ", map[string]string{"ENVIRONMENT": "prod"}, "ENVIRONMENT"},
		{"base url sai", map[string]string{"AUTH_BASE_URL": "auth"}, "AUTH_BASE_URL"},
		{"issuer sai", map[string]string{"AUTH_ISSUER": "x"}, "AUTH_ISSUER"},
		{"production cấm sqlite", with("DATABASE_URL", "sqlite://./a.db"), "SQLite"},
		{"production cấm debug", with("DEBUG", "true"), "DEBUG"},
		{"production cần https", with("AUTH_BASE_URL", "http://a.example.org"), "https"},
		{"production issuer https", with("AUTH_ISSUER", "http://a.example.org"), "AUTH_ISSUER"},
		{"staging cũng chặt", map[string]string{"ENVIRONMENT": "staging"}, "SQLite"},
		{"conn", map[string]string{"DATABASE_MAX_OPEN_CONNS": "0"}, "DATABASE_MAX_OPEN_CONNS"},
		{"readiness", map[string]string{"READINESS_TIMEOUT": "0s"}, "READINESS_TIMEOUT"},
		{"shutdown", map[string]string{"SHUTDOWN_TIMEOUT": "0s"}, "SHUTDOWN_TIMEOUT"},
		{"bool sai", map[string]string{"DEBUG": "maybe"}, "DEBUG"},
		{"bcrypt cost thấp", map[string]string{"AUTH_BCRYPT_COST": "3"}, "AUTH_BCRYPT_COST"},
		{"bcrypt cost cao", map[string]string{"AUTH_BCRYPT_COST": "32"}, "AUTH_BCRYPT_COST"},
		{"production cost >= 10", with("AUTH_BCRYPT_COST", "8"), "tối thiểu 10"},
		{"production không tắt pwned", with("AUTH_PWNED_ENABLED", "false"), "AUTH_PWNED_ENABLED"},
		{"pwned timeout", map[string]string{"AUTH_PWNED_TIMEOUT": "0s"}, "AUTH_PWNED_TIMEOUT"},
		{"pwned url", map[string]string{"AUTH_PWNED_URL": "abc"}, "AUTH_PWNED_URL"},
		{"clients không phải mảng", map[string]string{"AUTH_DEFAULT_OAUTH_CLIENTS": `{"id":"x"}`}, "AUTH_DEFAULT_OAUTH_CLIENTS"},
		{"clients JSON hỏng", map[string]string{"AUTH_DEFAULT_OAUTH_CLIENTS": `[`}, "AUTH_DEFAULT_OAUTH_CLIENTS"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, err := FromEnv(env(c.env))
			if c.want == "" {
				if err != nil {
					t.Fatalf("không mong lỗi: %v", err)
				}
				if s.SwaggerEnabled() {
					t.Error("swagger phải tắt ở production")
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("mong lỗi chứa %q, được %v", c.want, err)
			}
		})
	}
}

func TestRateLimitDefaults(t *testing.T) {
	prod := func(kv ...string) map[string]string {
		m := map[string]string{"ENVIRONMENT": "production", "DATABASE_URL": "postgres://u:p@h/db", "AUTH_BASE_URL": "https://a.example.org", "AUTH_SIGNING_KEY_PATH": "/keys", "AUTH_MFA_SECRET_KEYS": testMFAKeys, "AUTH_EXTERNAL_STATE_KEY": testStateKey, "AUTH_REDIS_URL": "redis://localhost:6379"}
		for i := 0; i < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return m
	}
	s, err := FromEnv(env(prod()))
	if err != nil || s.RateLimit != DefaultRateLimit || s.LoginRateLimit != DefaultLoginRateLimit || s.LoginRateBurst != DefaultLoginBurst {
		t.Fatalf("production không đặt: %+v %v", s, err)
	}
	if s, _ = FromEnv(env(prod("ENVIRONMENT", "staging"))); s.RateLimit != DefaultRateLimit {
		t.Errorf("staging: %v", s.RateLimit)
	}
	if s, _ = FromEnv(env(prod("AUTH_RATE_LIMIT", "0", "AUTH_LOGIN_RATE_LIMIT", "0"))); s.RateLimit != 0 || s.LoginRateLimit != 0 {
		t.Errorf("đặt rõ 0 phải tắt: %+v", s)
	}
	if s, _ = FromEnv(env(prod("AUTH_LOGIN_RATE_LIMIT", "3", "AUTH_LOGIN_RATE_BURST", "4"))); s.LoginRateLimit != 3 || s.LoginRateBurst != 4 {
		t.Errorf("giá trị đặt phải thắng: %+v", s)
	}
	if s, _ = FromEnv(env(nil)); s.RateLimit != 0 || s.LoginRateLimit != 0 {
		t.Errorf("local tắt: %+v", s)
	}
	for _, kv := range [][]string{{"AUTH_RATE_LIMIT", "-1"}, {"AUTH_LOGIN_RATE_BURST", "-2"}, {"AUTH_RATE_LIMIT", "nhanh"}} {
		if _, err := FromEnv(env(prod(kv...))); err == nil {
			t.Errorf("%v phải bị từ chối", kv)
		}
	}
}

func TestTokenSettings(t *testing.T) {
	s, err := FromEnv(env(nil))
	if err != nil || s.AccessTTL != 15*time.Minute || s.RefreshTTL != 14*24*time.Hour || s.IDTTL != time.Hour || s.SigningAlg != "RS256" {
		t.Fatalf("mặc định: %+v %v", s, err)
	}
	if s.IssuerURL() != s.BaseURL || s.AccessAudience() != s.BaseURL {
		t.Errorf("issuer và aud mặc định là BaseURL: %q %q", s.IssuerURL(), s.AccessAudience())
	}
	s, err = FromEnv(env(map[string]string{"AUTH_ISSUER": "http://iss.test", "AUTH_AUDIENCE": "trivita-fhir", "AUTH_ACCESS_TTL": "5m"}))
	if err != nil || s.IssuerURL() != "http://iss.test" || s.AccessAudience() != "trivita-fhir" || s.AccessTTL != 5*time.Minute {
		t.Fatalf("%+v %v", s, err)
	}
	prod := map[string]string{"ENVIRONMENT": "production", "DATABASE_URL": "postgres://u:p@h/db", "AUTH_BASE_URL": "https://a.example.org"}
	if _, err := FromEnv(env(prod)); err == nil || !strings.Contains(err.Error(), "AUTH_SIGNING_KEY_PATH") {
		t.Errorf("production phải bắt buộc khóa: %v", err)
	}
	if s.AuthCodeTTL != 10*time.Minute || s.AllowPlainPKCE {
		t.Errorf("code TTL mặc định 10 phút, plain tắt: %v %v", s.AuthCodeTTL, s.AllowPlainPKCE)
	}
	if s2, err := FromEnv(env(map[string]string{"AUTH_AUTH_CODE_TTL": "2m", "AUTH_ALLOW_PLAIN_PKCE": "true"})); err != nil || s2.AuthCodeTTL != 2*time.Minute || !s2.AllowPlainPKCE {
		t.Errorf("%+v %v", s2, err)
	}
	if s.RefreshGrace != 30*time.Second || s.SessionMaxAge != 0 {
		t.Errorf("ân hạn mặc định 30s, không trần phiên: %v %v", s.RefreshGrace, s.SessionMaxAge)
	}
	if s2, err := FromEnv(env(map[string]string{"AUTH_REFRESH_GRACE": "0", "AUTH_SESSION_MAX_AGE": "720h"})); err != nil || s2.RefreshGrace != 0 || s2.SessionMaxAge != 720*time.Hour {
		t.Errorf("ân hạn 0 là tắt: %+v %v", s2, err)
	}
	for _, bad := range []map[string]string{{"AUTH_REFRESH_GRACE": "-1s"}, {"AUTH_SESSION_MAX_AGE": "-1h"}, {"AUTH_AUTH_CODE_TTL": "0s"}, {"AUTH_SIGNING_ALG": "HS256"}, {"AUTH_ACCESS_TTL": "0s"}, {"AUTH_MAX_POLICY_CLAIM_BYTES": "0"}} {
		if _, err := FromEnv(env(bad)); err == nil {
			t.Errorf("%v phải bị từ chối", bad)
		}
	}
}

func TestAuthorizeSettings(t *testing.T) {
	local, err := FromEnv(env(nil))
	if err != nil || local.AppBaseURL != DevAllowedOrigin {
		t.Fatalf("local mặc định trỏ tới web dev: %q %v", local.AppBaseURL, err)
	}
	prod := func(kv ...string) map[string]string {
		m := map[string]string{"ENVIRONMENT": "production", "DATABASE_URL": "postgres://u:p@h/db", "AUTH_BASE_URL": "https://a.example.org", "AUTH_SIGNING_KEY_PATH": "/keys", "AUTH_MFA_SECRET_KEYS": testMFAKeys, "AUTH_EXTERNAL_STATE_KEY": testStateKey, "AUTH_REDIS_URL": "redis://localhost:6379"}
		for i := 0; i < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return m
	}
	if s, err := FromEnv(env(prod())); err != nil || s.AppBaseURL != "" {
		t.Errorf("production không đặt thì không có /oauth2/authorize: %q %v", s.AppBaseURL, err)
	}
	if s, err := FromEnv(env(prod("AUTH_APP_BASE_URL", "https://app.example.org", "AUTH_FHIR_BASE_URL", "https://fhir.example.org/fhir/R4"))); err != nil || s.FHIRBaseURL == "" {
		t.Errorf("%+v %v", s, err)
	}
	for _, bad := range []map[string]string{prod("AUTH_APP_BASE_URL", "http://app.example.org"), prod("AUTH_APP_BASE_URL", "app"), prod("AUTH_FHIR_BASE_URL", "fhir")} {
		if _, err := FromEnv(env(bad)); err == nil {
			t.Errorf("%v phải bị từ chối", bad)
		}
	}
}

func TestAssertionRegisterAndRedisSettings(t *testing.T) {
	d, err := FromEnv(env(nil))
	if err != nil || d.OAuthRegisterEnabled || d.AllowPrivateJWKS || d.RedisURL != "" {
		t.Fatalf("mặc định: %+v %v", d, err)
	}
	s, err := FromEnv(env(map[string]string{"AUTH_OAUTH_REGISTER_ENABLED": "true", "AUTH_ALLOW_PRIVATE_JWKS": "true", "AUTH_REDIS_URL": "redis://localhost:6379/0"}))
	if err != nil || !s.OAuthRegisterEnabled || !s.AllowPrivateJWKS || s.RedisURL == "" {
		t.Fatalf("%+v %v", s, err)
	}
	prod := map[string]string{"ENVIRONMENT": "production", "DATABASE_URL": "postgres://u:p@h/db", "AUTH_BASE_URL": "https://a.example.org", "AUTH_SIGNING_KEY_PATH": "/keys", "AUTH_MFA_SECRET_KEYS": testMFAKeys, "AUTH_EXTERNAL_STATE_KEY": testStateKey, "AUTH_REDIS_URL": "redis://localhost:6379"}
	with := func(k, v string) map[string]string {
		m := map[string]string{}
		for a, b := range prod {
			m[a] = b
		}
		m[k] = v
		return m
	}
	if _, err := FromEnv(env(with("AUTH_ALLOW_PRIVATE_JWKS", "true"))); err == nil {
		t.Error("production không được cho jwks_uri nội bộ (tắt chống SSRF)")
	}
	if _, err := FromEnv(env(with("AUTH_REDIS_URL", "rediss://redis.example.org:6380/0"))); err != nil {
		t.Errorf("rediss hợp lệ: %v", err)
	}
	for _, bad := range []string{"localhost:6379", "http://x", "redis://"} {
		if _, err := FromEnv(env(map[string]string{"AUTH_REDIS_URL": bad})); err == nil {
			t.Errorf("%q phải bị từ chối", bad)
		}
	}
}

func TestFHIRcastHubURL(t *testing.T) {
	prod := func(kv ...string) map[string]string {
		m := map[string]string{
			"ENVIRONMENT": "production", "DATABASE_URL": "postgres://u:p@h/db", "AUTH_BASE_URL": "https://a.example.org", "AUTH_SIGNING_KEY_PATH": "/keys", "AUTH_MFA_SECRET_KEYS": testMFAKeys, "AUTH_EXTERNAL_STATE_KEY": testStateKey, "AUTH_REDIS_URL": "redis://localhost:6379",
		}
		for i := 0; i < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return m
	}
	if s, err := FromEnv(env(nil)); err != nil || s.FHIRcastHubURL != "" {
		t.Fatalf("mặc định phải tắt: %q %v", s.FHIRcastHubURL, err)
	}
	if s, err := FromEnv(env(map[string]string{"AUTH_FHIRCAST_HUB_URL": "http://localhost:8007/fhircast/STU3"})); err != nil || s.FHIRcastHubURL != "http://localhost:8007/fhircast/STU3" {
		t.Errorf("local cho phép http: %q %v", s.FHIRcastHubURL, err)
	}
	if _, err := FromEnv(env(prod("AUTH_FHIRCAST_HUB_URL", "https://ws.example.org/fhircast/STU3", "AUTH_REDIS_URL", "redis://r:6379/0"))); err != nil {
		t.Errorf("production hợp lệ bị từ chối: %v", err)
	}
	for name, c := range map[string]struct {
		env  map[string]string
		want string
	}{
		"không phải URL":         {map[string]string{"AUTH_FHIRCAST_HUB_URL": "not a url"}, "AUTH_FHIRCAST_HUB_URL"},
		"scheme lạ":              {map[string]string{"AUTH_FHIRCAST_HUB_URL": "ftp://x/y"}, "AUTH_FHIRCAST_HUB_URL"},
		"có query":               {map[string]string{"AUTH_FHIRCAST_HUB_URL": "http://x/y?a=1"}, "query"},
		"có fragment":            {map[string]string{"AUTH_FHIRCAST_HUB_URL": "http://x/y#f"}, "fragment"},
		"production dùng http":   {prod("AUTH_FHIRCAST_HUB_URL", "http://ws.example.org/fhircast/STU3", "AUTH_REDIS_URL", "redis://r:6379/0"), "https"},
		"production thiếu Redis": {prod("AUTH_FHIRCAST_HUB_URL", "https://ws.example.org/fhircast/STU3", "AUTH_REDIS_URL", ""), "AUTH_REDIS_URL"},
	} {
		if _, err := FromEnv(env(c.env)); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: muốn lỗi chứa %q, được %v", name, c.want, err)
		}
	}
}

func TestDatabaseRequireSchema(t *testing.T) {
	if s, err := FromEnv(env(nil)); err != nil || s.DatabaseRequireSchema {
		t.Fatalf("mặc định phải tắt (không phá cách chạy database riêng): %v %v", s.DatabaseRequireSchema, err)
	}
	for _, url := range []string{"postgres://u:p@h:5432/trivita?sslmode=disable", "postgresql://u:p@h/trivita"} {
		if s, err := FromEnv(env(map[string]string{"DATABASE_URL": url, "DATABASE_REQUIRE_SCHEMA": "true"})); err != nil || !s.DatabaseRequireSchema {
			t.Errorf("%s với cờ bật phải hợp lệ: %v", url, err)
		}
	}
	for _, url := range []string{"sqlite://./auth.db", "sqlite://:memory:", "mysql://u:p@h/db"} {
		_, err := FromEnv(env(map[string]string{"DATABASE_URL": url, "DATABASE_REQUIRE_SCHEMA": "true"}))
		if err == nil || !strings.Contains(err.Error(), "DATABASE_REQUIRE_SCHEMA") {
			t.Errorf("%s với cờ bật phải lỗi (không có schema để kiểm): %v", url, err)
		}
	}
}

func TestDatabaseMigrateURL(t *testing.T) {
	pgApp, pgMigrate := "postgres://identity_app:p@h:5432/trivita?sslmode=disable", "postgres://identity_migrate:p@h:5432/trivita?sslmode=disable"
	if s, err := FromEnv(env(nil)); err != nil || s.DatabaseMigrateURL != "" {
		t.Fatalf("mặc định phải rỗng: %q %v", s.DatabaseMigrateURL, err)
	}
	ok := map[string]string{"DATABASE_URL": pgApp, "DATABASE_MIGRATE_URL": pgMigrate, "DATABASE_AUTO_MIGRATE": "false"}
	if s, err := FromEnv(env(ok)); err != nil || s.DatabaseMigrateURL != pgMigrate || s.DatabaseAutoMigrate {
		t.Fatalf("role migrate riêng với AUTO_MIGRATE=false phải hợp lệ: %v %v", s.DatabaseMigrateURL, err)
	}
	for name, c := range map[string]struct {
		env  map[string]string
		want string
	}{
		"app còn tự migrate":    {map[string]string{"DATABASE_URL": pgApp, "DATABASE_MIGRATE_URL": pgMigrate}, "DATABASE_AUTO_MIGRATE=false"},
		"không phải PostgreSQL": {map[string]string{"DATABASE_URL": pgApp, "DATABASE_MIGRATE_URL": "sqlite://./x.db", "DATABASE_AUTO_MIGRATE": "false"}, "chỉ áp dụng cho PostgreSQL"},
		"mysql":                 {map[string]string{"DATABASE_URL": pgApp, "DATABASE_MIGRATE_URL": "mysql://u:p@h/db", "DATABASE_AUTO_MIGRATE": "false"}, "chỉ áp dụng cho PostgreSQL"},
	} {
		if _, err := FromEnv(env(c.env)); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: muốn lỗi chứa %q, được %v", name, c.want, err)
		}
	}
}

// Đã đổi tên authen thành auth: biến AUTHEN_* cũ KHÔNG còn được đọc, nên phải từ chối khởi động thay vì bỏ qua âm thầm.
func TestLegacyAuthenEnvIsRefused(t *testing.T) {
	if err := legacyEnvError([]string{"PATH=/bin", "AUTH_BASE_URL=http://x", "FHIR_AUTH_DISABLED=true", "AUTHENTICATION=1"}); err != nil {
		t.Errorf("biến mới và biến không liên quan không được bị coi là cũ: %v", err)
	}
	err := legacyEnvError([]string{"PATH=/bin", "AUTHEN_BASE_URL=http://x", "AUTHEN_REDIS_URL=redis://r", "AUTH_OK=1"})
	if err == nil {
		t.Fatal("còn biến AUTHEN_* mà không báo lỗi")
	}
	for _, want := range []string{"2 biến", "AUTHEN_BASE_URL -> AUTH_BASE_URL", "AUTHEN_REDIS_URL -> AUTH_REDIS_URL"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("thông báo thiếu %q: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "http://x") || strings.Contains(err.Error(), "redis://r") {
		t.Errorf("thông báo không được lộ giá trị biến: %v", err)
	}
	many := []string{}
	for i := 0; i < 15; i++ {
		many = append(many, fmt.Sprintf("AUTHEN_V%02d=x", i))
	}
	if err := legacyEnvError(many); err == nil || !strings.Contains(err.Error(), "và 5 biến nữa") || strings.Contains(err.Error(), "AUTHEN_V14") {
		t.Errorf("phải rút gọn danh sách dài: %v", err)
	}
}

func TestLoadRefusesLegacyEnv(t *testing.T) {
	t.Setenv("AUTHEN_BASE_URL", "http://legacy")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "AUTHEN_BASE_URL -> AUTH_BASE_URL") {
		t.Errorf("Load phải từ chối biến cũ: %v", err)
	}
}

func TestMailProvider(t *testing.T) {
	prod := map[string]string{"ENVIRONMENT": "production", "DATABASE_URL": "postgres://u:p@h/db", "AUTH_BASE_URL": "https://a.example.org", "AUTH_SIGNING_KEY_PATH": "/keys", "AUTH_MFA_SECRET_KEYS": testMFAKeys, "AUTH_EXTERNAL_STATE_KEY": testStateKey, "AUTH_REDIS_URL": "redis://localhost:6379"}
	with := func(base map[string]string, kv ...string) func(string) string {
		m := map[string]string{}
		for k, v := range base {
			m[k] = v
		}
		for i := 0; i < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return env(m)
	}
	for _, tc := range []struct {
		name     string
		get      func(string) string
		provider string
		wantErr  string
	}{
		{"local mặc định log", with(nil), "log", ""},
		{"test mặc định log", with(nil, "ENVIRONMENT", "test"), "log", ""},
		{"production mặc định none", with(prod), "none", ""},
		{"production none rõ ràng", with(prod, "AUTH_MAIL_PROVIDER", "none"), "none", ""},
		{"production cấm log (ghi secret ra log)", with(prod, "AUTH_MAIL_PROVIDER", "log"), "", "AUTH_MAIL_PROVIDER=log"},
		{"staging cấm log", with(prod, "ENVIRONMENT", "staging", "AUTH_MAIL_PROVIDER", "log"), "", "AUTH_MAIL_PROVIDER=log"},
		{"giá trị lạ", with(nil, "AUTH_MAIL_PROVIDER", "smtp"), "", "AUTH_MAIL_PROVIDER"},
	} {
		s, err := FromEnv(tc.get)
		if tc.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("%s: mong lỗi %q, được %v", tc.name, tc.wantErr, err)
			}
			continue
		}
		if err != nil || s.MailProvider != tc.provider {
			t.Errorf("%s: provider=%q err=%v", tc.name, s.MailProvider, err)
		}
	}
	if s, _ := FromEnv(env(nil)); s.MailAppName != "Trivita" {
		t.Errorf("tên mặc định: %q", s.MailAppName)
	}
	if s, _ := FromEnv(env(map[string]string{"AUTH_MAIL_APP_NAME": "MedVita"})); s.MailAppName != "MedVita" {
		t.Errorf("tên đặt tay: %q", s.MailAppName)
	}
}

func TestRecaptchaAndRegistrationSettings(t *testing.T) {
	s, err := FromEnv(env(nil))
	if err != nil || s.RecaptchaSiteKey != "" || s.RecaptchaSecretKey != "" || s.RecaptchaTimeout != 3*time.Second || s.RequireVerifiedEmailForProjectCreation {
		t.Fatalf("mặc định: %+v %v", s, err)
	}
	s, err = FromEnv(env(map[string]string{
		"AUTH_RECAPTCHA_SITE_KEY": "site", "AUTH_RECAPTCHA_SECRET_KEY": "secret", "AUTH_RECAPTCHA_TIMEOUT": "500ms",
		"AUTH_REQUIRE_VERIFIED_EMAIL_FOR_PROJECT_CREATION": "true", "AUTH_REGISTER_ENABLED": "false",
	}))
	if err != nil || s.RecaptchaSiteKey != "site" || s.RecaptchaSecretKey != "secret" || s.RecaptchaTimeout != 500*time.Millisecond ||
		!s.RequireVerifiedEmailForProjectCreation || s.RegisterEnabled {
		t.Fatalf("đặt tay: %+v %v", s, err)
	}
	if s, _ := FromEnv(env(nil)); s.SupportEmail != "" {
		t.Errorf("mặc định không có email hỗ trợ: %q", s.SupportEmail)
	}
	if s, _ := FromEnv(env(map[string]string{"AUTH_SUPPORT_EMAIL": "ho-tro@example.org"})); s.SupportEmail != "ho-tro@example.org" {
		t.Errorf("email hỗ trợ: %q", s.SupportEmail)
	}
	for _, bad := range []string{"0s", "-1s"} {
		if _, err := FromEnv(env(map[string]string{"AUTH_RECAPTCHA_TIMEOUT": bad})); err == nil || !strings.Contains(err.Error(), "AUTH_RECAPTCHA_TIMEOUT") {
			t.Errorf("timeout %q phải bị từ chối: %v", bad, err)
		}
	}
}

func TestMFASettings(t *testing.T) {
	s, err := FromEnv(env(nil))
	if err != nil || s.MFAWindow != 1 || s.MFALoginAttemptLimit != 5 || s.MFAUserAttemptLimit != 20 || s.MFALoginExpiration != 10*time.Minute ||
		s.MFAEmailCodeExpiration != 20*time.Minute || s.MFAEnrollmentExpiration != 20*time.Minute || s.MFAEmailSendLimit != 5 || s.MFASecretKeys != "" {
		t.Fatalf("mặc định: %+v %v", s, err)
	}
	s, err = FromEnv(env(map[string]string{
		"AUTH_MFA_WINDOW": "2", "AUTH_MFA_LOGIN_ATTEMPT_LIMIT": "3", "AUTH_MFA_USER_ATTEMPT_LIMIT": "9", "AUTH_MFA_LOGIN_EXPIRATION": "5m",
		"AUTH_MFA_EMAIL_CODE_EXPIRATION": "10m", "AUTH_MFA_ENROLLMENT_EXPIRATION": "15m", "AUTH_MFA_EMAIL_SEND_LIMIT": "2", "AUTH_MFA_SECRET_KEYS": testMFAKeys,
	}))
	if err != nil || s.MFAWindow != 2 || s.MFALoginAttemptLimit != 3 || s.MFAUserAttemptLimit != 9 || s.MFALoginExpiration != 5*time.Minute ||
		s.MFAEmailCodeExpiration != 10*time.Minute || s.MFAEnrollmentExpiration != 15*time.Minute || s.MFAEmailSendLimit != 2 || s.MFASecretKeys != testMFAKeys {
		t.Fatalf("đặt tay: %+v %v", s, err)
	}
	for _, kv := range [][2]string{{"AUTH_MFA_WINDOW", "-1"}, {"AUTH_MFA_WINDOW", "6"}, {"AUTH_MFA_LOGIN_ATTEMPT_LIMIT", "0"}, {"AUTH_MFA_USER_ATTEMPT_LIMIT", "0"},
		{"AUTH_MFA_EMAIL_SEND_LIMIT", "0"}, {"AUTH_MFA_LOGIN_EXPIRATION", "0s"}, {"AUTH_MFA_EMAIL_CODE_EXPIRATION", "-1m"}, {"AUTH_MFA_ENROLLMENT_EXPIRATION", "0s"}} {
		if _, err := FromEnv(env(map[string]string{kv[0]: kv[1]})); err == nil || !strings.Contains(err.Error(), "AUTH_MFA_") {
			t.Errorf("%s=%s phải bị từ chối: %v", kv[0], kv[1], err)
		}
	}
	// staging/production: bắt buộc khóa mã hóa secret và Redis dùng chung
	prod := map[string]string{"ENVIRONMENT": "production", "DATABASE_URL": "postgres://u:p@h/db", "AUTH_BASE_URL": "https://a.example.org", "AUTH_SIGNING_KEY_PATH": "/keys"}
	with := func(kv ...string) map[string]string {
		m := map[string]string{}
		for k, v := range prod {
			m[k] = v
		}
		for i := 0; i < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return m
	}
	for name, c := range map[string]struct {
		env  map[string]string
		want string
	}{
		"thiếu khóa":  {with("AUTH_REDIS_URL", "redis://r:6379"), "AUTH_MFA_SECRET_KEYS"},
		"thiếu Redis": {with("AUTH_MFA_SECRET_KEYS", testMFAKeys), "AUTH_REDIS_URL"},
	} {
		for _, e := range []string{"production", "staging"} {
			c.env["ENVIRONMENT"] = e
			if _, err := FromEnv(env(c.env)); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("%s (%s): muốn lỗi chứa %q, được %v", name, e, c.want, err)
			}
		}
	}
	if _, err := FromEnv(env(with("AUTH_MFA_SECRET_KEYS", testMFAKeys, "AUTH_REDIS_URL", "redis://r:6379"))); err != nil {
		t.Errorf("production đủ khóa và Redis phải hợp lệ: %v", err)
	}
}

func TestExternalLoginSettings(t *testing.T) {
	s, err := FromEnv(env(nil))
	if err != nil || s.ExternalStateKey != "" || s.ExternalStateTTL != 10*time.Minute || s.ExternalAuthProviders != "" {
		t.Fatalf("mặc định: %+v %v", s, err)
	}
	s, err = FromEnv(env(map[string]string{"AUTH_EXTERNAL_STATE_KEY": testStateKey, "AUTH_EXTERNAL_STATE_TTL": "5m", "AUTH_EXTERNAL_AUTH_PROVIDERS": `[{"issuer":"https://x"}]`}))
	if err != nil || s.ExternalStateKey != testStateKey || s.ExternalStateTTL != 5*time.Minute || s.ExternalAuthProviders == "" {
		t.Fatalf("đặt tay: %+v %v", s, err)
	}
	for name, kv := range map[string][2]string{
		"khóa không phải base64": {"AUTH_EXTERNAL_STATE_KEY", "!!!"}, "khóa ngắn": {"AUTH_EXTERNAL_STATE_KEY", "dGhpcyBpcyB0b28gc2hvcnQ="},
		"ttl 0": {"AUTH_EXTERNAL_STATE_TTL", "0s"}, "ttl âm": {"AUTH_EXTERNAL_STATE_TTL", "-1m"},
	} {
		if _, err := FromEnv(env(map[string]string{kv[0]: kv[1]})); err == nil || !strings.Contains(err.Error(), kv[0]) {
			t.Errorf("%s phải bị từ chối: %v", name, err)
		}
	}
	// staging/production có AUTH_APP_BASE_URL thì bắt buộc khóa ký state; không có AUTH_APP_BASE_URL thì đăng nhập ngoài tắt nên không cần
	prod := map[string]string{"ENVIRONMENT": "production", "DATABASE_URL": "postgres://u:p@h/db", "AUTH_BASE_URL": "https://a.example.org", "AUTH_SIGNING_KEY_PATH": "/keys",
		"AUTH_MFA_SECRET_KEYS": testMFAKeys, "AUTH_REDIS_URL": "redis://r:6379"}
	if _, err := FromEnv(env(prod)); err != nil {
		t.Errorf("không có AUTH_APP_BASE_URL: không cần khóa state: %v", err)
	}
	prod["AUTH_APP_BASE_URL"] = "https://app.example.org"
	for _, e := range []string{"production", "staging"} {
		prod["ENVIRONMENT"] = e
		if _, err := FromEnv(env(prod)); err == nil || !strings.Contains(err.Error(), "AUTH_EXTERNAL_STATE_KEY") {
			t.Errorf("%s có AUTH_APP_BASE_URL thiếu khóa state phải lỗi: %v", e, err)
		}
	}
	prod["AUTH_EXTERNAL_STATE_KEY"] = testStateKey
	if _, err := FromEnv(env(prod)); err != nil {
		t.Errorf("đủ khóa: %v", err)
	}
}

func TestScimSettings(t *testing.T) {
	s, err := FromEnv(env(nil))
	if err != nil || s.ScimEnabled || !s.ScimAllowBasic || s.ScimMaxPage != 100 || s.ScimMaxPatchOps != 50 || s.ScimMaxBodyBytes != 1<<20 || s.AdminEnabled {
		t.Fatalf("mặc định: %+v %v", s, err)
	}
	s, err = FromEnv(env(map[string]string{"AUTH_SCIM_ENABLED": "true", "AUTH_SCIM_ALLOW_BASIC": "false", "AUTH_SCIM_MAX_PAGE": "10", "AUTH_SCIM_MAX_PATCH_OPS": "3",
		"AUTH_SCIM_MAX_BODY_BYTES": "2048", "AUTH_SCIM_ALLOWED_IPS": "10.0.0.0/8, 2001:db8::/32"}))
	if err != nil || !s.ScimEnabled || s.ScimAllowBasic || s.ScimMaxPage != 10 || s.ScimMaxPatchOps != 3 || s.ScimMaxBodyBytes != 2048 {
		t.Fatalf("override: %+v %v", s, err)
	}
	if nets, err := s.ScimAllowedNets(); err != nil || len(nets) != 2 {
		t.Errorf("CIDR: %v %v", nets, err)
	}
	for name, kv := range map[string][2]string{"CIDR sai": {"AUTH_SCIM_ALLOWED_IPS", "khong-phai-cidr"}, "page 0": {"AUTH_SCIM_MAX_PAGE", "0"}, "ops 0": {"AUTH_SCIM_MAX_PATCH_OPS", "0"}, "body 0": {"AUTH_SCIM_MAX_BODY_BYTES", "0"}} {
		if _, err := FromEnv(env(map[string]string{kv[0]: kv[1]})); err == nil {
			t.Errorf("%s phải lỗi", name)
		}
	}
	prod := map[string]string{"ENVIRONMENT": "production", "DATABASE_URL": "postgres://u:p@h/db", "AUTH_BASE_URL": "https://a.example.org", "AUTH_SIGNING_KEY_PATH": "/keys", "AUTH_MFA_SECRET_KEYS": testMFAKeys, "AUTH_EXTERNAL_STATE_KEY": testStateKey, "AUTH_REDIS_URL": "redis://localhost:6379"}
	with := func(kv ...string) map[string]string {
		m := map[string]string{}
		for k, v := range prod {
			m[k] = v
		}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return m
	}
	// production: SCIM + Basic bắt buộc danh sách IP
	if _, err := FromEnv(env(with("AUTH_SCIM_ENABLED", "true"))); err == nil || !strings.Contains(err.Error(), "AUTH_SCIM_ALLOWED_IPS") {
		t.Errorf("thiếu IP: %v", err)
	}
	for name, m := range map[string]map[string]string{
		"có IP":     with("AUTH_SCIM_ENABLED", "true", "AUTH_SCIM_ALLOWED_IPS", "203.0.113.0/24"),
		"tắt Basic": with("AUTH_SCIM_ENABLED", "true", "AUTH_SCIM_ALLOW_BASIC", "false"),
		"không bật": with(),
	} {
		if _, err := FromEnv(env(m)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	// ngoài production không bắt buộc IP
	if _, err := FromEnv(env(map[string]string{"AUTH_SCIM_ENABLED": "true"})); err != nil {
		t.Errorf("local: %v", err)
	}
}

func TestSMTPSettings(t *testing.T) {
	s, err := FromEnv(env(nil))
	if err != nil || s.SMTPPort != 587 || s.SMTPTimeout != 20*time.Second || s.SMTPAllowPlaintext || s.MailProvider != "log" {
		t.Fatalf("mặc định: %+v %v", s, err)
	}
	smtp := map[string]string{"AUTH_MAIL_PROVIDER": "smtp", "AUTH_SMTP_HOST": "smtp.example.org", "AUTH_SMTP_FROM": "Trivita <no-reply@example.org>", "AUTH_SMTP_USERNAME": "u", "AUTH_SMTP_PASSWORD": "p"}
	with := func(base map[string]string, kv ...string) map[string]string {
		m := map[string]string{}
		for k, v := range base {
			m[k] = v
		}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return m
	}
	s, err = FromEnv(env(with(smtp, "AUTH_SMTP_PORT", "465", "AUTH_SMTP_SECURE", "true", "AUTH_SMTP_APPROVED_SENDERS", "a@x.org, b@x.org", "AUTH_SMTP_TIMEOUT", "5s", "AUTH_SMTP_CA_FILE", "/ca.pem")))
	if err != nil || s.MailProvider != "smtp" || s.SMTPHost != "smtp.example.org" || s.SMTPPort != 465 || s.SMTPSecure != "true" || s.SMTPPassword != "p" || s.SMTPTimeout != 5*time.Second || s.SMTPCAFile != "/ca.pem" {
		t.Fatalf("smtp: %+v %v", s, err)
	}
	for name, m := range map[string]map[string]string{
		"thiếu host":              with(smtp, "AUTH_SMTP_HOST", ""),
		"host trắng":              with(smtp, "AUTH_SMTP_HOST", "  "),
		"port 0":                  with(smtp, "AUTH_SMTP_PORT", "0"),
		"port quá lớn":            with(smtp, "AUTH_SMTP_PORT", "70000"),
		"thiếu from":              with(smtp, "AUTH_SMTP_FROM", ""),
		"from sai":                with(smtp, "AUTH_SMTP_FROM", "khong-phai-email"),
		"from có CRLF":            with(smtp, "AUTH_SMTP_FROM", "a@x.org\r\nBcc: v@y.org"),
		"username thiếu mật khẩu": with(smtp, "AUTH_SMTP_PASSWORD", ""),
		"secure sai":              with(smtp, "AUTH_SMTP_SECURE", "co"),
		"timeout 0":               with(smtp, "AUTH_SMTP_TIMEOUT", "0s"),
	} {
		if _, err := FromEnv(env(m)); err == nil {
			t.Errorf("%s phải lỗi", name)
		}
	}
	// không cần đăng nhập: bỏ username thì không cần mật khẩu
	if _, err := FromEnv(env(with(smtp, "AUTH_SMTP_USERNAME", "", "AUTH_SMTP_PASSWORD", ""))); err != nil {
		t.Errorf("relay không đăng nhập: %v", err)
	}
	// khi provider không phải smtp, cấu hình SMTP thiếu không bị đòi
	if _, err := FromEnv(env(map[string]string{"AUTH_MAIL_PROVIDER": "log"})); err != nil {
		t.Error(err)
	}
	prod := map[string]string{"ENVIRONMENT": "production", "DATABASE_URL": "postgres://u:p@h/db", "AUTH_BASE_URL": "https://a.example.org", "AUTH_SIGNING_KEY_PATH": "/keys", "AUTH_MFA_SECRET_KEYS": testMFAKeys, "AUTH_EXTERNAL_STATE_KEY": testStateKey, "AUTH_REDIS_URL": "redis://localhost:6379"}
	if _, err := FromEnv(env(with(prod, "AUTH_MAIL_PROVIDER", "smtp", "AUTH_SMTP_HOST", "smtp.example.org", "AUTH_SMTP_FROM", "no-reply@example.org"))); err != nil {
		t.Errorf("production smtp hợp lệ: %v", err)
	}
	// production: cấm gửi không mã hóa dù provider nào, và smtp vẫn đòi đủ cấu hình
	if _, err := FromEnv(env(with(prod, "AUTH_SMTP_ALLOW_PLAINTEXT", "true"))); err == nil || !strings.Contains(err.Error(), "AUTH_SMTP_ALLOW_PLAINTEXT") {
		t.Errorf("plaintext ở production: %v", err)
	}
	if _, err := FromEnv(env(with(prod, "AUTH_MAIL_PROVIDER", "smtp"))); err == nil {
		t.Error("production smtp thiếu host/from phải lỗi")
	}
	// ngoài production cho phép (dev)
	if _, err := FromEnv(env(with(smtp, "AUTH_SMTP_ALLOW_PLAINTEXT", "true"))); err != nil {
		t.Errorf("plaintext ở dev: %v", err)
	}
}

func TestEmailSettings(t *testing.T) {
	s, err := FromEnv(env(nil))
	if err != nil || s.EmailEnabled || s.EmailAllowProjectSMTP || s.EmailAllowUnsafeOutbound || s.EmailMaxRecipients != 50 || s.EmailMaxBytes != 10<<20 {
		t.Fatalf("mặc định: %+v %v", s, err)
	}
	s, err = FromEnv(env(map[string]string{"AUTH_EMAIL_ENABLED": "true", "AUTH_EMAIL_ALLOW_PROJECT_SMTP": "true", "AUTH_EMAIL_ALLOW_UNSAFE_OUTBOUND": "true", "AUTH_EMAIL_MAX_RECIPIENTS": "5", "AUTH_EMAIL_MAX_BYTES": "2048"}))
	if err != nil || !s.EmailEnabled || !s.EmailAllowProjectSMTP || !s.EmailAllowUnsafeOutbound || s.EmailMaxRecipients != 5 || s.EmailMaxBytes != 2048 {
		t.Fatalf("override: %+v %v", s, err)
	}
	for name, kv := range map[string][2]string{"người nhận 0": {"AUTH_EMAIL_MAX_RECIPIENTS", "0"}, "bytes 0": {"AUTH_EMAIL_MAX_BYTES", "0"}} {
		if _, err := FromEnv(env(map[string]string{kv[0]: kv[1]})); err == nil {
			t.Errorf("%s phải lỗi", name)
		}
	}
	prod := map[string]string{"ENVIRONMENT": "production", "DATABASE_URL": "postgres://u:p@h/db", "AUTH_BASE_URL": "https://a.example.org", "AUTH_SIGNING_KEY_PATH": "/keys", "AUTH_MFA_SECRET_KEYS": testMFAKeys, "AUTH_EXTERNAL_STATE_KEY": testStateKey, "AUTH_REDIS_URL": "redis://localhost:6379"}
	with := func(kv ...string) map[string]string {
		m := map[string]string{}
		for k, v := range prod {
			m[k] = v
		}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return m
	}
	if _, err := FromEnv(env(with("AUTH_EMAIL_ALLOW_UNSAFE_OUTBOUND", "true"))); err == nil || !strings.Contains(err.Error(), "AUTH_EMAIL_ALLOW_UNSAFE_OUTBOUND") {
		t.Errorf("unsafe outbound ở production: %v", err)
	}
	if _, err := FromEnv(env(with("AUTH_EMAIL_ENABLED", "true", "AUTH_EMAIL_ALLOW_PROJECT_SMTP", "true"))); err != nil {
		t.Errorf("production hợp lệ: %v", err)
	}
}

func TestEmailFHIRURLSettings(t *testing.T) {
	s, err := FromEnv(env(nil))
	if err != nil || s.EmailFHIRURL != "" || s.EmailFHIRTimeout.Seconds() != 30 {
		t.Fatalf("mặc định: %q %v %v", s.EmailFHIRURL, s.EmailFHIRTimeout, err)
	}
	s, err = FromEnv(env(map[string]string{"AUTH_EMAIL_FHIR_URL": "http://fhir-server:8005/fhir/R4", "AUTH_EMAIL_FHIR_TIMEOUT": "5s"}))
	if err != nil || s.EmailFHIRURL != "http://fhir-server:8005/fhir/R4" || s.EmailFHIRTimeout.Seconds() != 5 {
		t.Fatalf("%+v %v", s, err)
	}
	for name, v := range map[string]string{"tương đối": "/fhir/R4", "ftp": "ftp://h/x", "userinfo": "https://u:p@h/x", "query": "https://h/x?a=1", "query rỗng": "https://h/x?", "fragment": "https://h/x#f", "không host": "http://"} {
		if _, err := FromEnv(env(map[string]string{"AUTH_EMAIL_FHIR_URL": v})); err == nil || !strings.Contains(err.Error(), "AUTH_EMAIL_FHIR_URL") {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := FromEnv(env(map[string]string{"AUTH_EMAIL_FHIR_URL": "https://h/x", "AUTH_EMAIL_FHIR_TIMEOUT": "0s"})); err == nil {
		t.Error("timeout 0 phải lỗi")
	}
	prod := map[string]string{"ENVIRONMENT": "production", "DATABASE_URL": "postgres://u:p@h/db", "AUTH_BASE_URL": "https://a.example.org", "AUTH_SIGNING_KEY_PATH": "/keys", "AUTH_MFA_SECRET_KEYS": testMFAKeys, "AUTH_EXTERNAL_STATE_KEY": testStateKey, "AUTH_REDIS_URL": "redis://localhost:6379"}
	with := func(u string) map[string]string {
		m := map[string]string{"AUTH_EMAIL_FHIR_URL": u}
		for k, v := range prod {
			m[k] = v
		}
		return m
	}
	if _, err := FromEnv(env(with("http://fhir-server:8005/fhir/R4"))); err == nil || !strings.Contains(err.Error(), "https") {
		t.Errorf("production http: %v", err)
	}
	if _, err := FromEnv(env(with("https://fhir.example.org/fhir/R4"))); err != nil {
		t.Errorf("production https: %v", err)
	}
}
