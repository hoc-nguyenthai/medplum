package config_test

import (
	"strings"
	"testing"
	"time"

	"trivita-fhir/apps/fhir-server/internal/config"
)

const secret = "0123456789abcdef01234567" // đúng MinSecretLen ký tự

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestDefaults(t *testing.T) {
	s, err := config.FromEnv(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if s.Addr != ":8005" || s.DatabaseURL != "sqlite://./fhir.db" || !s.DatabaseAutoMigrate || s.BaseURL != "http://localhost:8005/fhir/R4" {
		t.Fatalf("mặc định: %+v", s)
	}
	if s.ShutdownDrainDelay != 5*time.Second || s.ShutdownTimeout != 25*time.Second {
		t.Fatalf("shutdown: %+v", s)
	}
	// không token thì chỉ ValidateForServe báo lỗi (migrate không cần token)
	if err := s.ValidateForServe(); err == nil || !strings.Contains(err.Error(), "FHIR_WRITE_TOKENS") {
		t.Fatalf("ValidateForServe: %v", err)
	}
}

func TestFromEnv(t *testing.T) {
	s, err := config.FromEnv(env(map[string]string{
		"HTTP_ADDR": ":9000", "DATABASE_URL": "postgres://u:p@db/fhir", "DATABASE_AUTO_MIGRATE": "false",
		"FHIR_BASE_URL": "https://fhir.example.org/fhir/R4", "FHIR_SKIP_CONSTRAINTS": "true",
		"FHIR_READ_TOKENS": "viewer:" + secret, "FHIR_WRITE_TOKENS": "ops:" + secret + "x,etl:" + secret + "y",
		"SHUTDOWN_DRAIN_DELAY": "2s", "ENVIRONMENT": "production",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if s.Addr != ":9000" || s.DatabaseAutoMigrate || !s.SkipConstraints || s.ShutdownDrainDelay != 2*time.Second || s.BaseURL != "https://fhir.example.org/fhir/R4" {
		t.Fatalf("%+v", s)
	}
	if err := s.ValidateForServe(); err != nil {
		t.Fatal(err)
	}
}

func TestParseTokens(t *testing.T) {
	ok, err := config.ParseTokens(" a:" + secret + " , b-1:" + secret + ":có:dấu:hai:chấm")
	if err != nil || len(ok) != 2 || ok[1].Name != "b-1" || !strings.HasSuffix(ok[1].Secret, "hai:chấm") {
		t.Fatalf("%v %v", ok, err)
	}
	for name, raw := range map[string]string{
		"thiếu secret": "a",
		"tên sai":      "A_B:" + secret,
		"tên quá dài":  strings.Repeat("a", 33) + ":" + secret,
		"secret ngắn":  "a:short",
		"trùng tên":    "a:" + secret + ",a:" + secret + "z",
	} {
		_, err := config.ParseTokens(raw)
		if err == nil {
			t.Errorf("%s: muốn lỗi", name)
			continue
		}
		if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "short") {
			t.Errorf("%s: lỗi không được lộ secret: %v", name, err)
		}
	}
	if got, err := config.ParseTokens(""); err != nil || len(got) != 0 {
		t.Errorf("rỗng: %v %v", got, err)
	}
}

func TestValidationErrorsAreAggregated(t *testing.T) {
	_, err := config.FromEnv(env(map[string]string{
		"ENVIRONMENT": "production", "FHIR_AUTH_DISABLED": "true", "FHIR_SKIP_VALIDATION": "true",
		"DATABASE_URL": "sqlite://./x.db", "FHIR_BASE_URL": "not-a-url", "READINESS_TIMEOUT": "0s",
		"FHIR_READ_TOKENS": "same:" + secret, "FHIR_WRITE_TOKENS": "same:" + secret + "z",
		"DATABASE_MAX_OPEN_CONNS": "x",
	}))
	if err == nil {
		t.Fatal("muốn lỗi")
	}
	for _, want := range []string{"FHIR_AUTH_DISABLED", "FHIR_SKIP_VALIDATION", "SQLite", "FHIR_BASE_URL", "READINESS_TIMEOUT", "trùng tên", "DATABASE_MAX_OPEN_CONNS"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("thiếu lỗi %q trong:\n%v", want, err)
		}
	}
}

func TestAdminTokensAndNewSettings(t *testing.T) {
	s, err := config.FromEnv(env(map[string]string{"FHIR_ADMIN_TOKENS": "root:" + secret, "FHIR_SUBSCRIPTIONS": "true", "BROKER_DRIVER": "redis",
		"REDIS_URL": "redis://r:6379/0", "FHIR_JOB_CONCURRENCY": "4", "FHIR_WEBHOOK_TIMEOUT": "3s"}))
	if err != nil {
		t.Fatal(err)
	}
	if !s.Subscriptions || s.BrokerDriver != "redis" || s.JobConcurrency != 4 || s.WebhookTimeout != 3*time.Second {
		t.Fatalf("%+v", s)
	}
	// chỉ có token admin vẫn đủ để chạy server (admin gồm quyền ghi)
	if err := s.ValidateForServe(); err != nil {
		t.Fatal(err)
	}
	d := config.Default()
	if d.JobConcurrency != 2 || d.BrokerDriver != "memory" || d.Subscriptions || d.WebhookAllowPrivate || d.WebhookMaxAttempts != 5 {
		t.Fatalf("mặc định phải an toàn: %+v", d)
	}
}

func TestNewSettingsValidation(t *testing.T) {
	_, err := config.FromEnv(env(map[string]string{
		"ENVIRONMENT": "production", "DATABASE_URL": "postgres://u:p@db/x", "FHIR_WEBHOOK_ALLOW_PRIVATE": "true",
		"BROKER_DRIVER": "kafka", "FHIR_JOB_CONCURRENCY": "0", "FHIR_ADMIN_TOKENS": "same:" + secret + "a",
		"FHIR_WRITE_TOKENS": "same:" + secret + "b",
	}))
	if err == nil {
		t.Fatal("muốn lỗi")
	}
	for _, want := range []string{"FHIR_WEBHOOK_ALLOW_PRIVATE", "BROKER_DRIVER", "FHIR_JOB_CONCURRENCY", "trùng tên"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("thiếu %q trong:\n%v", want, err)
		}
	}
}

func TestAuthDisabledAllowedOutsideProduction(t *testing.T) {
	s, err := config.FromEnv(env(map[string]string{"FHIR_AUTH_DISABLED": "true"}))
	if err != nil || s.ValidateForServe() != nil {
		t.Fatalf("%v %v", err, s.ValidateForServe())
	}
}

func TestJWTSettingsValidation(t *testing.T) {
	ok := map[string]string{"FHIR_JWT_ISSUER": "https://id", "FHIR_JWT_AUDIENCE": "fhir", "FHIR_JWKS_URL": "https://id/jwks", "FHIR_JOB_SIGNING_KEY": strings.Repeat("s", 40), "FHIR_BASE_URL": "https://h/fhir/R4"}
	with := func(over map[string]string) map[string]string {
		m := map[string]string{}
		for k, v := range ok {
			m[k] = v
		}
		for k, v := range over {
			m[k] = v
		}
		return m
	}
	if s, err := config.FromEnv(env(with(nil))); err != nil || !s.JWTEnabled() || s.JWTProjectClaim != "fhir_project" || s.JWTScopeClaim != "scope" {
		t.Fatalf("cấu hình hợp lệ: %+v %v", s, err)
	}
	long := strings.Repeat("k", 40)
	for name, c := range map[string]struct {
		env  map[string]string
		want string
	}{
		"thiếu audience":        {with(map[string]string{"FHIR_JWT_AUDIENCE": ""}), "FHIR_JWT_AUDIENCE"},
		"thiếu khóa":            {with(map[string]string{"FHIR_JWKS_URL": ""}), "FHIR_JWKS_URL"},
		"cả JWKS lẫn HMAC":      {with(map[string]string{"FHIR_JWT_HMAC_SECRET": long}), "đúng một"},
		"HMAC quá ngắn":         {with(map[string]string{"FHIR_JWKS_URL": "", "FHIR_JWT_HMAC_SECRET": "short"}), "32"},
		"cùng token tĩnh":       {with(map[string]string{"FHIR_WRITE_TOKENS": "ops:" + secret}), "token tĩnh"},
		"cùng auth disabled":    {with(map[string]string{"FHIR_AUTH_DISABLED": "true"}), "token tĩnh"},
		"HMAC ở production":     {with(map[string]string{"ENVIRONMENT": "production", "FHIR_JWKS_URL": "", "FHIR_JWT_HMAC_SECRET": long}), "staging/production"},
		"JWKS http ở prod":      {with(map[string]string{"ENVIRONMENT": "production", "FHIR_JWKS_URL": "http://id/jwks", "DATABASE_URL": "postgres://x"}), "https"},
		"thiếu khóa ký job":     {with(map[string]string{"FHIR_JOB_SIGNING_KEY": ""}), "FHIR_JOB_SIGNING_KEY"},
		"khóa ký job ngắn":      {with(map[string]string{"FHIR_JOB_SIGNING_KEY": "short"}), "FHIR_JOB_SIGNING_KEY"},
		"super-admin không JWT": {map[string]string{"FHIR_JWT_SUPER_ADMIN": "true", "FHIR_BASE_URL": "https://h/fhir/R4"}, "FHIR_JWT_SUPER_ADMIN"},
		"audience mồ côi":       {map[string]string{"FHIR_JWT_AUDIENCE": "fhir", "FHIR_BASE_URL": "https://h/fhir/R4"}, "FHIR_JWT_ISSUER"},
	} {
		if _, err := config.FromEnv(env(c.env)); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, muốn chứa %q", name, err, c.want)
		}
	}
	// JWT bật thì không cần token tĩnh để serve
	s, _ := config.FromEnv(env(with(nil)))
	if err := s.ValidateForServe(); err != nil {
		t.Errorf("ValidateForServe với JWT: %v", err)
	}
}

func TestJobDurabilitySettings(t *testing.T) {
	s, err := config.FromEnv(env(map[string]string{"FHIR_BASE_URL": "https://h/fhir/R4", "FHIR_JOB_LEASE": "2m", "FHIR_JOB_MAINTENANCE_INTERVAL": "0s", "FHIR_EXPORT_RETENTION": "-1h"}))
	if err != nil || s.JobLease != 2*time.Minute || s.JobMaintenanceInterval != 0 || s.ExportRetention != -time.Hour {
		t.Fatalf("%+v %v", s, err)
	}
	for k, v := range map[string]string{"FHIR_JOB_LEASE": "0s", "FHIR_JOB_MAINTENANCE_INTERVAL": "-1s"} {
		if _, err := config.FromEnv(env(map[string]string{"FHIR_BASE_URL": "https://h/fhir/R4", k: v})); err == nil || !strings.Contains(err.Error(), "FHIR_JOB_LEASE") {
			t.Errorf("%s=%s phải bị từ chối: %v", k, v, err)
		}
	}
}

func TestWebSocketSettings(t *testing.T) {
	base := map[string]string{"FHIR_BASE_URL": "https://h/fhir/R4", "FHIR_SUBSCRIPTIONS": "true", "FHIR_WEBSOCKET": "true"}
	with := func(over map[string]string) map[string]string {
		m := map[string]string{}
		for k, v := range base {
			m[k] = v
		}
		for k, v := range over {
			m[k] = v
		}
		return m
	}
	if s, err := config.FromEnv(env(with(nil))); err != nil || !s.WebSocket || s.WSMaxConnections != 1000 || s.WSMaxBindingsPerUser != 20 || s.WSHeartbeat != 30*time.Second {
		t.Fatalf("hợp lệ ở dev không cần khóa: %+v %v", s, err)
	}
	for name, c := range map[string]struct {
		env  map[string]string
		want string
	}{
		"thiếu subscriptions":          {with(map[string]string{"FHIR_SUBSCRIPTIONS": "false"}), "FHIR_SUBSCRIPTIONS"},
		"production thiếu khóa":        {with(map[string]string{"ENVIRONMENT": "production", "DATABASE_URL": "postgres://x"}), "FHIR_WS_SIGNING_KEY"},
		"redis thiếu khóa":             {with(map[string]string{"BROKER_DRIVER": "redis", "REDIS_URL": "redis://r:6379/0"}), "FHIR_WS_SIGNING_KEY"},
		"khóa ngắn":                    {with(map[string]string{"FHIR_WS_SIGNING_KEY": "short"}), "tối thiểu 32"},
		"giới hạn sai":                 {with(map[string]string{"FHIR_WS_MAX_CONNECTIONS": "0"}), "FHIR_WS_MAX_CONNECTIONS"},
		"hạn mức bind sai":             {with(map[string]string{"FHIR_WS_MAX_BINDINGS_PER_USER": "0"}), "FHIR_WS_MAX_BINDINGS_PER_USER"},
		"khóa cũ thiếu khóa hiện hành": {with(map[string]string{"FHIR_WS_SIGNING_KEY_PREVIOUS": strings.Repeat("o", 40)}), "FHIR_WS_SIGNING_KEY_PREVIOUS"},
		"khóa cũ ngắn":                 {with(map[string]string{"FHIR_WS_SIGNING_KEY": strings.Repeat("k", 40), "FHIR_WS_SIGNING_KEY_PREVIOUS": "short"}), "mỗi khóa tối thiểu 32"},
		"quá nhiều khóa cũ":            {with(map[string]string{"FHIR_WS_SIGNING_KEY": strings.Repeat("k", 40), "FHIR_WS_SIGNING_KEY_PREVIOUS": strings.Join([]string{strings.Repeat("a", 32), strings.Repeat("b", 32), strings.Repeat("c", 32), strings.Repeat("d", 32)}, ",")}), "tối đa 3"},
		"khóa cũ mồ côi (ws tắt)":      {map[string]string{"FHIR_BASE_URL": "https://h/fhir/R4", "FHIR_WS_SIGNING_KEY_PREVIOUS": strings.Repeat("o", 40)}, "FHIR_WEBSOCKET=true"},
		"cấu hình mồ côi":              {map[string]string{"FHIR_BASE_URL": "https://h/fhir/R4", "FHIR_WS_SIGNING_KEY": strings.Repeat("k", 40)}, "FHIR_WEBSOCKET=true"},
	} {
		if _, err := config.FromEnv(env(c.env)); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, muốn chứa %q", name, err, c.want)
		}
	}
	if _, err := config.FromEnv(env(with(map[string]string{"ENVIRONMENT": "production", "DATABASE_URL": "postgres://x", "FHIR_WS_SIGNING_KEY": strings.Repeat("k", 40)}))); err != nil {
		t.Errorf("production có khóa: %v", err)
	}
	s, err := config.FromEnv(env(with(map[string]string{"FHIR_WS_SIGNING_KEY": strings.Repeat("k", 40), "FHIR_WS_SIGNING_KEY_PREVIOUS": " " + strings.Repeat("a", 32) + " , " + strings.Repeat("b", 32) + ",,"})))
	if err != nil || len(s.PreviousWSKeys()) != 2 || s.PreviousWSKeys()[0] != strings.Repeat("a", 32) {
		t.Errorf("khóa cũ hợp lệ: %v %v", s.PreviousWSKeys(), err)
	}
}

func TestUpdateAsCreateSetting(t *testing.T) {
	if config.Default().UpdateAsCreate {
		t.Fatal("mặc định phải tắt")
	}
	s, err := config.FromEnv(env(map[string]string{"FHIR_UPDATE_AS_CREATE": "true"}))
	if err != nil || !s.UpdateAsCreate {
		t.Fatalf("%+v %v", s, err)
	}
}

func TestWSModeSettings(t *testing.T) {
	key := strings.Repeat("k", 40)
	issuer := map[string]string{
		"FHIR_BASE_URL": "https://h/fhir/R4", "FHIR_SUBSCRIPTIONS": "true", "FHIR_WS_MODE": "issuer",
		"BROKER_DRIVER": "redis", "REDIS_URL": "redis://r:6379/0", "FHIR_WS_SIGNING_KEY": key, "FHIR_WS_URL": "wss://ws.example.org/ws/subscriptions-r4",
	}
	with := func(base map[string]string, over map[string]string) map[string]string {
		m := map[string]string{}
		for k, v := range base {
			m[k] = v
		}
		for k, v := range over {
			m[k] = v
		}
		return m
	}
	mode := func(m map[string]string) string {
		s, err := config.FromEnv(env(m))
		if err != nil {
			t.Fatalf("%v: %v", m, err)
		}
		return s.WSModeResolved()
	}
	plain := map[string]string{"FHIR_BASE_URL": "https://h/fhir/R4", "FHIR_SUBSCRIPTIONS": "true"}
	if got := mode(plain); got != config.WSModeOff {
		t.Errorf("mặc định = %s", got)
	}
	if got := mode(with(plain, map[string]string{"FHIR_WEBSOCKET": "true"})); got != config.WSModeEmbedded {
		t.Errorf("FHIR_WEBSOCKET=true = %s, muốn embedded (bí danh cũ)", got)
	}
	if got := mode(with(plain, map[string]string{"FHIR_WS_MODE": "embedded"})); got != config.WSModeEmbedded {
		t.Errorf("FHIR_WS_MODE=embedded = %s", got)
	}
	if got := mode(with(plain, map[string]string{"FHIR_WS_MODE": "off"})); got != config.WSModeOff {
		t.Errorf("FHIR_WS_MODE=off = %s", got)
	}
	if got := mode(with(plain, map[string]string{"FHIR_WS_MODE": "embedded", "FHIR_WEBSOCKET": "true"})); got != config.WSModeEmbedded {
		t.Errorf("hai cách cùng nói embedded phải hợp lệ: %s", got)
	}
	if got := mode(issuer); got != config.WSModeIssuer {
		t.Errorf("issuer = %s", got)
	}
	// khóa cũ hợp lệ ở issuer
	if _, err := config.FromEnv(env(with(issuer, map[string]string{"FHIR_WS_SIGNING_KEY_PREVIOUS": strings.Repeat("o", 40)}))); err != nil {
		t.Errorf("issuer có khóa cũ: %v", err)
	}

	for name, c := range map[string]struct {
		env  map[string]string
		want string
	}{
		"chế độ lạ":                  {with(plain, map[string]string{"FHIR_WS_MODE": "gateway"}), "FHIR_WS_MODE"},
		"alias xung đột với issuer":  {with(issuer, map[string]string{"FHIR_WEBSOCKET": "true"}), "mâu thuẫn"},
		"alias xung đột với off":     {with(plain, map[string]string{"FHIR_WS_MODE": "off", "FHIR_WEBSOCKET": "true"}), "mâu thuẫn"},
		"issuer thiếu subscriptions": {with(issuer, map[string]string{"FHIR_SUBSCRIPTIONS": "false"}), "FHIR_SUBSCRIPTIONS"},
		"issuer với broker bộ nhớ":   {with(issuer, map[string]string{"BROKER_DRIVER": "memory"}), "BROKER_DRIVER=redis"},
		"issuer thiếu khóa":          {with(issuer, map[string]string{"FHIR_WS_SIGNING_KEY": ""}), "FHIR_WS_SIGNING_KEY: bắt buộc ở FHIR_WS_MODE=issuer"},
		"issuer khóa ngắn":           {with(issuer, map[string]string{"FHIR_WS_SIGNING_KEY": "short"}), "tối thiểu 32"},
		"issuer thiếu URL gateway":   {with(issuer, map[string]string{"FHIR_WS_URL": ""}), "FHIR_WS_URL: bắt buộc"},
		"issuer URL http":            {with(issuer, map[string]string{"FHIR_WS_URL": "https://ws.example.org/x"}), "ws:// hoặc wss://"},
		"issuer URL không host":      {with(issuer, map[string]string{"FHIR_WS_URL": "wss://"}), "ws:// hoặc wss://"},
		"issuer production ws://":    {with(issuer, map[string]string{"ENVIRONMENT": "production", "DATABASE_URL": "postgres://x", "FHIR_WS_URL": "ws://ws.example.org/x"}), "chỉ nhận wss://"},
		"issuer khóa cũ ngắn":        {with(issuer, map[string]string{"FHIR_WS_SIGNING_KEY_PREVIOUS": "short"}), "mỗi khóa tối thiểu 32"},
		"off nhưng có URL gateway":   {with(plain, map[string]string{"FHIR_WS_URL": "wss://ws.example.org/x"}), "FHIR_WS_*"},
	} {
		if _, err := config.FromEnv(env(c.env)); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, muốn chứa %q", name, err, c.want)
		}
	}
	// cấu hình của phía giữ kết nối đặt ở fhir-server issuer là nhầm chỗ: từ chối, nêu tên từng biến
	for _, k := range []string{"FHIR_WS_MAX_CONNECTIONS", "FHIR_WS_MAX_BINDINGS_PER_USER", "FHIR_WS_ALLOWED_ORIGINS", "FHIR_WS_HEARTBEAT"} {
		v := "5"
		if k == "FHIR_WS_ALLOWED_ORIGINS" {
			v = "app.example.org"
		}
		if k == "FHIR_WS_HEARTBEAT" {
			v = "10s"
		}
		if _, err := config.FromEnv(env(with(issuer, map[string]string{k: v}))); err == nil || !strings.Contains(err.Error(), k) || !strings.Contains(err.Error(), "ws-gateway") {
			t.Errorf("%s ở issuer phải bị từ chối và nhắc ws-gateway: %v", k, err)
		}
	}
	// ...nhưng vẫn hợp lệ ở embedded (nơi chúng có tác dụng)
	emb := with(plain, map[string]string{"FHIR_WS_MODE": "embedded", "FHIR_WS_MAX_CONNECTIONS": "5", "FHIR_WS_ALLOWED_ORIGINS": "app.example.org"})
	if _, err := config.FromEnv(env(emb)); err != nil {
		t.Errorf("embedded có giới hạn: %v", err)
	}
}

func TestDatabaseRequireSchema(t *testing.T) {
	if s, err := config.FromEnv(env(nil)); err != nil || s.DatabaseRequireSchema {
		t.Fatalf("mặc định phải tắt (không phá cách chạy database riêng): %v %v", s.DatabaseRequireSchema, err)
	}
	pg := map[string]string{"DATABASE_URL": "postgres://u:p@h:5432/trivita?sslmode=disable", "DATABASE_REQUIRE_SCHEMA": "true"}
	if s, err := config.FromEnv(env(pg)); err != nil || !s.DatabaseRequireSchema {
		t.Errorf("PostgreSQL với cờ bật phải hợp lệ: %v %v", s.DatabaseRequireSchema, err)
	}
	pg2 := map[string]string{"DATABASE_URL": "postgresql://u:p@h/trivita", "DATABASE_REQUIRE_SCHEMA": "true"}
	if _, err := config.FromEnv(env(pg2)); err != nil {
		t.Errorf("scheme postgresql:// cũng hợp lệ: %v", err)
	}
	for _, url := range []string{"sqlite://./fhir.db", "sqlite://:memory:", "mysql://u:p@h/db"} {
		_, err := config.FromEnv(env(map[string]string{"DATABASE_URL": url, "DATABASE_REQUIRE_SCHEMA": "true"}))
		if err == nil || !strings.Contains(err.Error(), "DATABASE_REQUIRE_SCHEMA") {
			t.Errorf("%s với cờ bật phải lỗi (không có schema để kiểm): %v", url, err)
		}
	}
}

func TestDatabaseMigrateURL(t *testing.T) {
	pgApp, pgMigrate := "postgres://fhir_app:p@h:5432/trivita?sslmode=disable", "postgres://fhir_migrate:p@h:5432/trivita?sslmode=disable"
	if s, err := config.FromEnv(env(nil)); err != nil || s.DatabaseMigrateURL != "" {
		t.Fatalf("mặc định phải rỗng: %q %v", s.DatabaseMigrateURL, err)
	}
	ok := map[string]string{"DATABASE_URL": pgApp, "DATABASE_MIGRATE_URL": pgMigrate, "DATABASE_AUTO_MIGRATE": "false"}
	if s, err := config.FromEnv(env(ok)); err != nil || s.DatabaseMigrateURL != pgMigrate || s.DatabaseAutoMigrate {
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
		if _, err := config.FromEnv(env(c.env)); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: muốn lỗi chứa %q, được %v", name, c.want, err)
		}
	}
}

func TestStorageSettings(t *testing.T) {
	base := map[string]string{"FHIR_WRITE_TOKENS": "ops:" + secret}
	with := func(extra map[string]string) (config.Settings, error) {
		m := map[string]string{}
		for k, v := range base {
			m[k] = v
		}
		for k, v := range extra {
			m[k] = v
		}
		return config.FromEnv(env(m))
	}
	// mặc định: tắt
	s, err := with(nil)
	if err != nil || s.StorageEnabled() {
		t.Fatalf("mặc định phải tắt: %v", err)
	}
	if s.StorageURLTTL != time.Hour || s.StorageMaxUploadBytes != 256<<20 || s.Base64BinaryMaxBytes != 1<<20 || s.StorageLegacyGET {
		t.Fatalf("mặc định: %+v", s)
	}
	// bật dạng file, URL công khai suy từ FHIR_BASE_URL
	s, err = with(map[string]string{"FHIR_BINARY_STORAGE": "file:/var/lib/blobs", "FHIR_BASE_URL": "https://fhir.example.org/fhir/R4"})
	if err != nil || !s.StorageEnabled() || s.StorageDir() != "/var/lib/blobs" || s.StoragePublicURL() != "https://fhir.example.org/storage" {
		t.Fatalf("%+v %v", s, err)
	}
	s, _ = with(map[string]string{"FHIR_BINARY_STORAGE": "file:/x", "FHIR_STORAGE_BASE_URL": "https://cdn.example.org/files/"})
	if s.StoragePublicURL() != "https://cdn.example.org/files" {
		t.Fatalf("%q", s.StoragePublicURL())
	}
	for name, extra := range map[string]map[string]string{
		"s3 thiếu region/khóa":   {"FHIR_BINARY_STORAGE": "s3:bucket"},
		"azure chưa hỗ trợ":      {"FHIR_BINARY_STORAGE": "azure:c"},
		"không tiền tố":          {"FHIR_BINARY_STORAGE": "/var/lib/blobs"},
		"file: rỗng":             {"FHIR_BINARY_STORAGE": "file:"},
		"base url tương đối":     {"FHIR_BINARY_STORAGE": "file:/x", "FHIR_STORAGE_BASE_URL": "/storage"},
		"base url có query":      {"FHIR_BINARY_STORAGE": "file:/x", "FHIR_STORAGE_BASE_URL": "https://h/storage?a=1"},
		"ttl 0":                  {"FHIR_BINARY_STORAGE": "file:/x", "FHIR_STORAGE_URL_TTL": "0s"},
		"ttl quá 24h":            {"FHIR_BINARY_STORAGE": "file:/x", "FHIR_STORAGE_URL_TTL": "25h"},
		"upload 0":               {"FHIR_BINARY_STORAGE": "file:/x", "FHIR_STORAGE_MAX_UPLOAD_BYTES": "0"},
		"base64 max 0":           {"FHIR_BINARY_STORAGE": "file:/x", "FHIR_BASE64_BINARY_MAX_BYTES": "0"},
		"khóa cũ không khóa mới": {"FHIR_BINARY_STORAGE": "file:/x", "FHIR_STORAGE_SIGNING_PREVIOUS_KEYS": "/k1.pub"},
		"quá 3 khóa cũ":          {"FHIR_BINARY_STORAGE": "file:/x", "FHIR_STORAGE_SIGNING_KEY_PATH": "/k", "FHIR_STORAGE_SIGNING_PREVIOUS_KEYS": "/a,/b,/c,/d"},
	} {
		if _, err := with(extra); err == nil {
			t.Errorf("%s: không lỗi", name)
		}
	}
	// cấu hình lỗi nhưng kho tắt thì bỏ qua
	if _, err := with(map[string]string{"FHIR_STORAGE_URL_TTL": "99h"}); err != nil {
		t.Errorf("kho tắt không được kiểm: %v", err)
	}
	// production: bắt buộc khóa cố định và https
	prod := map[string]string{"ENVIRONMENT": "production", "FHIR_BINARY_STORAGE": "file:/x", "FHIR_BASE_URL": "https://fhir.example.org/fhir/R4", "FHIR_READ_TOKENS": "v:" + secret, "DATABASE_URL": "postgres://u:p@db/fhir"}
	if _, err := with(prod); err == nil || !strings.Contains(err.Error(), "FHIR_STORAGE_SIGNING_KEY_PATH") {
		t.Errorf("production thiếu khóa: %v", err)
	}
	prod["FHIR_STORAGE_SIGNING_KEY_PATH"] = "/etc/keys/storage.pem"
	if _, err := with(prod); err != nil {
		t.Errorf("production đủ: %v", err)
	}
	prod["FHIR_BASE_URL"] = "http://fhir.example.org/fhir/R4"
	if _, err := with(prod); err == nil || !strings.Contains(err.Error(), "https") {
		t.Errorf("production http: %v", err)
	}
}

func TestS3StorageSettings(t *testing.T) {
	good := map[string]string{
		"FHIR_WRITE_TOKENS": "ops:" + secret, "FHIR_BINARY_STORAGE": "s3:my-bkt/env/prod/", "FHIR_S3_REGION": "eu-west-1",
		"FHIR_S3_ACCESS_KEY_ID": "AK", "FHIR_S3_SECRET_ACCESS_KEY": "SK",
	}
	with := func(over map[string]string) (config.Settings, error) {
		m := map[string]string{}
		for k, v := range good {
			m[k] = v
		}
		for k, v := range over {
			if v == "-" {
				delete(m, k)
			} else {
				m[k] = v
			}
		}
		return config.FromEnv(env(m))
	}
	s, err := with(nil)
	if err != nil || s.StorageKind() != "s3" || s.S3Bucket() != "my-bkt" || s.S3Prefix() != "env/prod/" || s.S3Region != "eu-west-1" {
		t.Fatalf("%+v %v", s, err)
	}
	if s, _ = with(map[string]string{"FHIR_BINARY_STORAGE": "s3:b1"}); s.S3Prefix() != "" || s.S3Bucket() != "b1" {
		t.Errorf("không prefix: %q %q", s.S3Bucket(), s.S3Prefix())
	}
	for name, over := range map[string]map[string]string{
		"thiếu region":       {"FHIR_S3_REGION": "-"},
		"thiếu key id":       {"FHIR_S3_ACCESS_KEY_ID": "-"},
		"thiếu secret":       {"FHIR_S3_SECRET_ACCESS_KEY": "-"},
		"thiếu bucket":       {"FHIR_BINARY_STORAGE": "s3:/prefix"},
		"sse sai":            {"FHIR_S3_SSE": "aes"},
		"endpoint tương đối": {"FHIR_S3_ENDPOINT": "minio:9000"},
		"endpoint có path":   {"FHIR_S3_ENDPOINT": "http://minio:9000/x"},
		"endpoint userinfo":  {"FHIR_S3_ENDPOINT": "http://u:p@minio:9000"},
	} {
		if _, err := with(over); err == nil {
			t.Errorf("%s: không lỗi", name)
		}
	}
	if _, err := with(map[string]string{"FHIR_S3_ENDPOINT": "http://minio:9000", "FHIR_S3_SSE": "aws:kms", "FHIR_S3_SESSION_TOKEN": "t"}); err != nil {
		t.Errorf("hợp lệ: %v", err)
	}
	// production: endpoint http bị chặn; https thì được
	prod := map[string]string{"ENVIRONMENT": "production", "FHIR_BASE_URL": "https://fhir.example.org/fhir/R4", "FHIR_READ_TOKENS": "v:" + secret, "DATABASE_URL": "postgres://u:p@db/fhir",
		"FHIR_STORAGE_SIGNING_KEY_PATH": "/etc/keys/storage.pem", "FHIR_S3_ENDPOINT": "http://minio:9000"}
	if _, err := with(prod); err == nil || !strings.Contains(err.Error(), "FHIR_S3_ENDPOINT") {
		t.Errorf("production http: %v", err)
	}
	prod["FHIR_S3_ENDPOINT"] = "https://minio.internal:9000"
	if _, err := with(prod); err != nil {
		t.Errorf("production https: %v", err)
	}
}

func TestSubscriptionAutoDisable(t *testing.T) {
	s, err := config.FromEnv(env(map[string]string{"FHIR_SUBSCRIPTIONS": "true",
		"FHIR_SUBSCRIPTION_AUTO_DISABLE": `[{"maxConsecutiveFailures":10,"timeWindowSeconds":3600},{"maxConsecutiveFailures":3,"timeWindowSeconds":60}]`}))
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.AutoDisableTriggers()
	if err != nil || len(got) != 2 || got[0] != (config.AutoDisableTrigger{MaxConsecutiveFailures: 10, TimeWindowSeconds: 3600}) {
		t.Fatalf("%+v %v", got, err)
	}
	if got, err := config.Default().AutoDisableTriggers(); err != nil || got != nil {
		t.Fatalf("mặc định phải tắt: %+v %v", got, err)
	}
	for name, vars := range map[string]map[string]string{
		"không phải JSON":  {"FHIR_SUBSCRIPTIONS": "true", "FHIR_SUBSCRIPTION_AUTO_DISABLE": "10/3600"},
		"số không dương":   {"FHIR_SUBSCRIPTIONS": "true", "FHIR_SUBSCRIPTION_AUTO_DISABLE": `[{"maxConsecutiveFailures":0,"timeWindowSeconds":60}]`},
		"thiếu trường":     {"FHIR_SUBSCRIPTIONS": "true", "FHIR_SUBSCRIPTION_AUTO_DISABLE": `[{"maxConsecutiveFailures":3}]`},
		"sai tên trường":   {"FHIR_SUBSCRIPTIONS": "true", "FHIR_SUBSCRIPTION_AUTO_DISABLE": `[{"maxFailures":3,"timeWindowSeconds":60}]`},
		"tắt Subscription": {"FHIR_SUBSCRIPTION_AUTO_DISABLE": `[{"maxConsecutiveFailures":3,"timeWindowSeconds":60}]`},
	} {
		_, err := config.FromEnv(env(vars))
		if err == nil || !strings.Contains(err.Error(), "FHIR_SUBSCRIPTION_AUTO_DISABLE") {
			t.Errorf("%s: muốn lỗi FHIR_SUBSCRIPTION_AUTO_DISABLE, được %v", name, err)
		}
	}
}

func TestAutoDownloadSettings(t *testing.T) {
	d := config.Default()
	if d.AutoDownload || d.AutoDownloadAllowPrivate || d.AutoDownloadTimeout != 30*time.Second {
		t.Fatalf("mặc định phải TẮT: %+v", d)
	}
	ok, err := config.FromEnv(env(map[string]string{"FHIR_BINARY_STORAGE": "file:/tmp/x", "FHIR_AUTO_DOWNLOAD": "true", "FHIR_AUTO_DOWNLOAD_MAX_BYTES": "1000",
		"FHIR_AUTO_DOWNLOAD_ALLOWED_PREFIXES": " https://a/ , ,https://b/x ", "FHIR_AUTO_DOWNLOAD_IGNORED_PREFIXES": "https://a/no/", "FHIR_AUTO_DOWNLOAD_TIMEOUT": "5s"}))
	if err != nil || !ok.AutoDownload || ok.AutoDownloadMaxBytes != 1000 || ok.AutoDownloadTimeout != 5*time.Second {
		t.Fatalf("hợp lệ: %+v %v", ok, err)
	}
	if got := config.AutoDownloadPrefixes(ok.AutoDownloadAllowedPrefixes); len(got) != 2 || got[0] != "https://a/" || got[1] != "https://b/x" {
		t.Errorf("tách tiền tố: %q", got)
	}
	for name, vars := range map[string]map[string]string{
		"thiếu kho":            {"FHIR_AUTO_DOWNLOAD": "true"},
		"tiền tố khi tắt":      {"FHIR_AUTO_DOWNLOAD_ALLOWED_PREFIXES": "https://a/"},
		"AllowPrivate khi tắt": {"FHIR_AUTO_DOWNLOAD_ALLOW_PRIVATE": "true"},
		"MaxBytes âm":          {"FHIR_BINARY_STORAGE": "file:/tmp/x", "FHIR_AUTO_DOWNLOAD": "true", "FHIR_AUTO_DOWNLOAD_MAX_BYTES": "-1"},
		"hạn 0":                {"FHIR_BINARY_STORAGE": "file:/tmp/x", "FHIR_AUTO_DOWNLOAD": "true", "FHIR_AUTO_DOWNLOAD_TIMEOUT": "0s"},
	} {
		if _, err := config.FromEnv(env(vars)); err == nil || !strings.Contains(err.Error(), "FHIR_AUTO_DOWNLOAD") && !strings.Contains(err.Error(), "REDIS_URL") {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestAsyncBatchSettings(t *testing.T) {
	d := config.Default()
	if d.AsyncBatch || d.BatchCheckpointEntries != 10 {
		t.Fatalf("mặc định: AsyncBatch=%v BatchCheckpointEntries=%d", d.AsyncBatch, d.BatchCheckpointEntries)
	}
	s, err := config.FromEnv(env(map[string]string{"FHIR_ASYNC_BATCH": "true", "FHIR_BATCH_CHECKPOINT_ENTRIES": "50"}))
	if err != nil || !s.AsyncBatch || s.BatchCheckpointEntries != 50 {
		t.Fatalf("%+v %v", s, err)
	}
	if _, err := config.FromEnv(env(map[string]string{"FHIR_BATCH_CHECKPOINT_ENTRIES": "0"})); err == nil ||
		!strings.Contains(err.Error(), "FHIR_BATCH_CHECKPOINT_ENTRIES") {
		t.Fatalf("muốn lỗi, được %v", err)
	}
}
