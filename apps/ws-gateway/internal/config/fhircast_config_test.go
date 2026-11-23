package config_test

import (
	"strings"
	"testing"
	"time"

	"trivita-fhir/apps/ws-gateway/internal/config"
)

func fc(over map[string]string) map[string]string {
	m := with(map[string]string{
		"FHIRCAST_ENABLED": "true", "FHIRCAST_PUBLIC_URL": "ws://localhost:8007",
		"FHIRCAST_JWT_ISSUER": "https://id", "FHIRCAST_JWT_AUDIENCE": "fhir", "FHIRCAST_JWT_HMAC_SECRET": strings.Repeat("s", 40),
	})
	for k, v := range over {
		if v == "" {
			delete(m, k)
		} else {
			m[k] = v
		}
	}
	return m
}

func prodFc(over map[string]string) map[string]string {
	m := fc(map[string]string{
		"ENVIRONMENT": "production", "REDIS_URL": "redis://r:6379/0", "FHIRCAST_PUBLIC_URL": "wss://ws.example.org",
		"FHIRCAST_JWT_HMAC_SECRET": "", "FHIRCAST_JWKS_URL": "https://id.example.org/jwks",
	})
	for k, v := range over {
		if v == "" {
			delete(m, k)
		} else {
			m[k] = v
		}
	}
	return m
}

func TestFhircastDisabledByDefaultAndDefaults(t *testing.T) {
	s, err := config.FromEnv(env(with(nil)))
	if err != nil || s.FhircastEnabled {
		t.Fatalf("mặc định phải tắt: %v %v", s.FhircastEnabled, err)
	}
	s, err = config.FromEnv(env(fc(nil)))
	if err != nil {
		t.Fatal(err)
	}
	if !s.FhircastRequireScope || s.FhircastAllowTopicMismatch || s.FhircastLease != time.Hour || s.FhircastContextTTL != 24*time.Hour ||
		s.FhircastMaxConnections != 1000 || s.FhircastMaxSocketsPerEndpoint != 4 || s.FhircastHeartbeat != 10*time.Second || s.FhircastMaxContextBytes != 256<<10 || s.FhircastMaxUpdateEntries != 200 || s.FhircastMaxBodyBytes != 1<<20 {
		t.Errorf("mặc định: %+v", s)
	}
	if _, err := config.FromEnv(env(prodFc(nil))); err != nil {
		t.Errorf("cấu hình production hợp lệ bị từ chối: %v", err)
	}
}

func TestFhircastValidation(t *testing.T) {
	for name, c := range map[string]struct {
		env  map[string]string
		want string
	}{
		"đặt cấu hình mà chưa bật": {with(map[string]string{"FHIRCAST_JWKS_URL": "https://x"}), "FHIRCAST_ENABLED chưa bật"},
		"thiếu public url":         {fc(map[string]string{"FHIRCAST_PUBLIC_URL": ""}), "FHIRCAST_PUBLIC_URL: bắt buộc"},
		"public url là http":       {fc(map[string]string{"FHIRCAST_PUBLIC_URL": "http://h"}), "ws:// hoặc wss://"},
		"public url có đường dẫn":  {fc(map[string]string{"FHIRCAST_PUBLIC_URL": "ws://h/x"}), "không có đường dẫn"},
		"public url thiếu host":    {fc(map[string]string{"FHIRCAST_PUBLIC_URL": "ws://"}), "ws:// hoặc wss://"},
		"prod dùng ws":             {prodFc(map[string]string{"FHIRCAST_PUBLIC_URL": "ws://h"}), "phải là wss://"},
		"thiếu issuer":             {fc(map[string]string{"FHIRCAST_JWT_ISSUER": ""}), "FHIRCAST_JWT_ISSUER và FHIRCAST_JWT_AUDIENCE"},
		"thiếu audience":           {fc(map[string]string{"FHIRCAST_JWT_AUDIENCE": ""}), "FHIRCAST_JWT_ISSUER và FHIRCAST_JWT_AUDIENCE"},
		"không có khóa nào":        {fc(map[string]string{"FHIRCAST_JWT_HMAC_SECRET": ""}), "đúng một trong hai"},
		"cả hai khóa":              {fc(map[string]string{"FHIRCAST_JWKS_URL": "https://x"}), "đúng một trong hai"},
		"hmac ngắn":                {fc(map[string]string{"FHIRCAST_JWT_HMAC_SECRET": "short"}), "tối thiểu 32"},
		"prod dùng hmac":           {prodFc(map[string]string{"FHIRCAST_JWKS_URL": "", "FHIRCAST_JWT_HMAC_SECRET": strings.Repeat("s", 40)}), "không dùng ở staging/production"},
		"prod jwks http":           {prodFc(map[string]string{"FHIRCAST_JWKS_URL": "http://id/jwks"}), "phải là https"},
		"prod tắt scope":           {prodFc(map[string]string{"FHIRCAST_REQUIRE_SCOPE": "false"}), "REQUIRE_SCOPE=false"},
		"lease 0":                  {fc(map[string]string{"FHIRCAST_LEASE": "0s"}), "FHIRCAST_LEASE"},
		"lease quá dài":            {fc(map[string]string{"FHIRCAST_LEASE": "25h"}), "FHIRCAST_LEASE"},
		"context ttl 0":            {fc(map[string]string{"FHIRCAST_CONTEXT_TTL": "0s"}), "FHIRCAST_CONTEXT_TTL"},
		"context bytes 0":          {fc(map[string]string{"FHIRCAST_MAX_CONTEXT_BYTES": "0"}), "phải > 0"},
		"update entries 0":         {fc(map[string]string{"FHIRCAST_MAX_UPDATE_ENTRIES": "0"}), "phải > 0"},
		"body 0":                   {fc(map[string]string{"FHIRCAST_MAX_BODY_BYTES": "0"}), "phải > 0"},
		"max connections 0":        {fc(map[string]string{"FHIRCAST_MAX_CONNECTIONS": "0"}), "FHIRCAST_MAX_CONNECTIONS"},
		"sockets mỗi endpoint 0":   {fc(map[string]string{"FHIRCAST_MAX_SOCKETS_PER_ENDPOINT": "0"}), "FHIRCAST_MAX_SOCKETS_PER_ENDPOINT"},
		"heartbeat 0":              {fc(map[string]string{"FHIRCAST_HEARTBEAT": "0s"}), "FHIRCAST_HEARTBEAT"},
	} {
		_, err := config.FromEnv(env(c.env))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: muốn lỗi chứa %q, được %v", name, c.want, err)
		}
	}
	s, err := config.FromEnv(env(fc(map[string]string{"FHIRCAST_REQUIRE_SCOPE": "false", "FHIRCAST_ALLOW_TOPIC_MISMATCH": "true"})))
	if err != nil || s.FhircastRequireScope || !s.FhircastAllowTopicMismatch {
		t.Errorf("hai cờ nới lỏng ở local: %+v %v", s, err)
	}
}
