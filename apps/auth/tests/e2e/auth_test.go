// Package e2e_test chạy auth như một service ĐỘC LẬP: dựng image từ apps/auth/Dockerfile rồi chạy trong container, chỉ nói chuyện với nó qua
// mạng và lệnh con của chính image (không import mã bên trong service). Vì dựng bằng Dockerfile nên kiểm luôn cả image: người dùng không root,
// thư mục dữ liệu ghi được bởi UID 65532, HEALTHCHECK, tắt êm, từ chối cấu hình sai (kể cả biến AUTHEN_* của tên cũ).
package e2e_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"trivita-fhir/pkg/fhirauth"
	"trivita-fhir/pkg/testkit"
)

const adminPassword = "mat-khau-admin-e2e-123"

// image dựng image auth từ Dockerfile RIÊNG: context là gốc repo, thư viện quan sát đi qua build context có tên (cùng cách `make docker APP=auth`).
func image(t *testing.T) string {
	t.Helper()
	testkit.RequireDocker(t)
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}").Output()
	if err != nil {
		t.Fatalf("không tìm được thư mục module: %v", err)
	}
	root := strings.TrimSpace(string(out))
	obs := filepath.Join(filepath.Dir(root), "trivita-observability-go")
	if st, err := os.Stat(obs); err != nil || !st.IsDir() {
		t.Skipf("không có thư viện quan sát ở %s (go.mod có replace tới đó): đặt nó cạnh repo để dựng image", obs)
	}
	return testkit.BuildImage(t, testkit.BuildSpec{
		Name: "auth", Context: root, Dockerfile: filepath.Join(root, "apps/auth/Dockerfile"),
		NamedContexts: map[string]string{"observability": obs},
	})
}

func baseEnv() map[string]string {
	return map[string]string{
		"ENVIRONMENT": "local", "AUTH_BASE_URL": "http://auth.e2e.test", "AUTH_PWNED_ENABLED": "false", "AUTH_COOKIE_SECURE": "false",
		"AUTH_APP_BASE_URL": "http://localhost:3000", "SHUTDOWN_DRAIN_DELAY": "0s", "LOG_JSON": "true",
	}
}

func run(t *testing.T, env map[string]string) *testkit.Service {
	t.Helper()
	svc := testkit.RunService(t, testkit.ServiceSpec{Image: image(t), Port: "8000/tcp", Health: "/health/ready", Env: env})
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("---- log auth ----\n%s", svc.Logs(t))
		}
	})
	return svc
}

func getJSON(t *testing.T, u string) map[string]any {
	t.Helper()
	resp, err := http.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("%s: không phải JSON (%d): %s", u, resp.StatusCode, b)
	}
	return m
}

func TestImageIsHardenedAndHealthcheckWorks(t *testing.T) {
	svc := run(t, baseEnv())
	out, err := exec.Command("docker", "inspect", "--format", "{{.Config.User}}|{{json .Config.Healthcheck.Test}}|{{json .Config.Entrypoint}}", image(t)).Output()
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(strings.TrimSpace(string(out)), "|", 3)
	if parts[0] != "65532:65532" {
		t.Errorf("USER = %q, muốn 65532:65532 (UID số để runAsNonRoot kiểm được)", parts[0])
	}
	if !strings.Contains(parts[1], "healthcheck") || parts[2] != `["/app"]` {
		t.Errorf("HEALTHCHECK/ENTRYPOINT: %s | %s", parts[1], parts[2])
	}
	if code, out := svc.Exec(t, "/app", "healthcheck"); code != 0 {
		t.Errorf("`/app healthcheck` thoát %d: %s", code, out)
	}
	for _, p := range []string{"/healthz", "/health/live", "/health/startup", "/health/ready"} {
		resp, err := http.Get(svc.URL + p)
		if err != nil || resp.StatusCode != 200 {
			t.Errorf("%s: %v %v", p, err, resp)
		}
		if resp != nil {
			resp.Body.Close()
		}
	}
}

// Dữ liệu SQLite nằm ở /data: thư mục phải ghi được bởi UID 65532 (nếu quyền sai thì không migrate được và service không lên).
// Luồng thật: seed project bằng lệnh con của image, xin token, kiểm token bằng JWKS do chính container công bố (đúng cách fhir-server làm).
func TestSeedTokenAndJWKSInsideTheImage(t *testing.T) {
	svc := run(t, baseEnv())
	cfg := getJSON(t, svc.URL+"/.well-known/openid-configuration")
	if cfg["issuer"] != "http://auth.e2e.test" || !strings.HasPrefix(cfg["token_endpoint"].(string), "http://auth.e2e.test/") {
		t.Fatalf("discovery: %v", cfg)
	}
	if keys, _ := getJSON(t, svc.URL+"/.well-known/jwks.json")["keys"].([]any); len(keys) == 0 {
		t.Fatal("JWKS rỗng")
	}

	code, out := svc.ExecEnv(t, map[string]string{"AUTH_SEED_ADMIN_PASSWORD": adminPassword},
		"/app", "seed", "project", "-name", "E2E", "-admin-email", "e2e@example.org", "-admin-first", "A", "-admin-last", "B")
	if code != 0 {
		t.Fatalf("seed project thoát %d: %s", code, out)
	}
	field := func(name string) string {
		m := regexp.MustCompile(`(?m)^` + name + `: (.+)$`).FindStringSubmatch(out)
		if m == nil {
			t.Fatalf("seed không in %s:\n%s", name, out)
		}
		return strings.TrimSpace(m[1])
	}
	projectID, clientID, secret := field("projectId"), field("clientId"), field("clientSecret")

	resp, err := http.PostForm(svc.URL+"/oauth2/token", url.Values{"grant_type": {"client_credentials"}, "client_id": {clientID}, "client_secret": {secret}, "scope": {"fhircast/*"}})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var tok map[string]any
	_ = json.Unmarshal(b, &tok)
	access, _ := tok["access_token"].(string)
	if resp.StatusCode != 200 || access == "" {
		t.Fatalf("xin token: %d %s", resp.StatusCode, b)
	}

	v, err := fhirauth.NewVerifier(fhirauth.Config{Issuer: "http://auth.e2e.test", Audience: "http://auth.e2e.test", JWKSURL: svc.URL + "/.well-known/jwks.json"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	claims, err := v.VerifyClaims(ctx, access)
	if err != nil {
		t.Fatalf("token của container không xác minh được bằng JWKS của chính nó: %v", err)
	}
	if claims.Project != projectID || claims.Subject != clientID || !contains(claims.Scopes, "fhircast/*") {
		t.Errorf("claims: %+v (project mong đợi %s)", claims, projectID)
	}
	if _, has := tok["hub.topic"]; has {
		t.Errorf("chưa cấu hình AUTH_FHIRCAST_HUB_URL mà vẫn có hub.topic: %v", tok)
	}
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// SIGTERM: thoát mã 0 trong hạn (tắt êm), không bị ép kill.
func TestGracefulShutdownExitsZero(t *testing.T) {
	svc := run(t, baseEnv())
	start := time.Now()
	if code := svc.Terminate(t, 20*time.Second); code != 0 {
		t.Errorf("mã thoát = %d, muốn 0", code)
	}
	if d := time.Since(start); d > 15*time.Second {
		t.Errorf("tắt mất %v: SIGTERM không được xử lý êm", d)
	}
}

// Cấu hình sai thì từ chối khởi động ngay, nói rõ biến nào sai, mã thoát khác 0. Gồm biến AUTHEN_* của tên cũ: không được bỏ qua âm thầm.
func TestRefusesToStartOnBadConfiguration(t *testing.T) {
	img := image(t)
	cases := map[string]struct {
		env  map[string]string
		want string
	}{
		"biến AUTHEN_* của tên cũ":           {map[string]string{"AUTHEN_BASE_URL": "http://old"}, "AUTHEN_BASE_URL -> AUTH_BASE_URL"},
		"production dùng SQLite":             {map[string]string{"ENVIRONMENT": "production", "AUTH_BASE_URL": "https://a.example.org", "AUTH_SIGNING_KEY_PATH": "/keys"}, "SQLite"},
		"DATABASE_REQUIRE_SCHEMA với SQLite": {map[string]string{"DATABASE_REQUIRE_SCHEMA": "true"}, "DATABASE_REQUIRE_SCHEMA"},
		"base URL sai":                       {map[string]string{"AUTH_BASE_URL": "auth"}, "AUTH_BASE_URL"},
	}
	for name, c := range cases {
		code, logs := testkit.RunToExit(t, testkit.ServiceSpec{Image: img, Env: c.env}, 30*time.Second)
		if code == 0 || !strings.Contains(logs, c.want) {
			t.Errorf("%s: mã thoát %d, log phải nhắc %q:\n%s", name, code, c.want, logs)
		}
	}
}
