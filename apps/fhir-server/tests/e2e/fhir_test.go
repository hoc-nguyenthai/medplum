// Package e2e_test chạy fhir-server như một service ĐỘC LẬP: dựng image từ apps/fhir-server/Dockerfile rồi chạy trong container, chỉ nói chuyện với
// nó qua mạng (HTTP FHIR) và lệnh con của chính image, không import mã bên trong service. Vì dựng bằng Dockerfile nên kiểm luôn cả image: người dùng
// không root, thư mục dữ liệu ghi được bởi UID 65532, HEALTHCHECK, tắt êm, từ chối cấu hình sai.
package e2e_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"trivita-fhir/pkg/testkit"
)

const (
	writeSecret = "write-secret-0123456789abcdef"
	readSecret  = "read-secret-0123456789abcdef"
)

// image dựng image fhir-server từ Dockerfile RIÊNG: context là gốc repo, thư viện quan sát qua build context có tên (cùng cách `make docker APP=fhir-server`).
func image(t *testing.T) string {
	t.Helper()
	testkit.RequireDocker(t)
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}").Output()
	if err != nil {
		t.Fatalf("không tìm được thư mục module: %v", err)
	}
	root := strings.TrimSpace(string(out))
	return testkit.BuildImage(t, testkit.BuildSpec{
		Name: "fhir-server", Context: root, Dockerfile: filepath.Join(root, "apps/fhir-server/Dockerfile"),
	})
}

func baseEnv() map[string]string {
	return map[string]string{
		"ENVIRONMENT": "local", "FHIR_BASE_URL": "http://fhir.e2e.test/fhir/R4", "SHUTDOWN_DRAIN_DELAY": "0s", "LOG_JSON": "true",
		"FHIR_WRITE_TOKENS": "ops:" + writeSecret, "FHIR_READ_TOKENS": "viewer:" + readSecret,
	}
}

func run(t *testing.T, env map[string]string) *testkit.Service {
	t.Helper()
	svc := testkit.RunService(t, testkit.ServiceSpec{Image: image(t), Port: "8000/tcp", Health: "/healthz", Env: env})
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("---- log fhir-server ----\n%s", svc.Logs(t))
		}
	})
	return svc
}

type resp struct {
	status int
	body   []byte
}

func (r resp) json(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(r.body, &m); err != nil {
		t.Fatalf("không phải JSON (%d): %s", r.status, r.body)
	}
	return m
}

func do(t *testing.T, method, u, token, body string) resp {
	t.Helper()
	req, err := http.NewRequest(method, u, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/fhir+json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return resp{res.StatusCode, b}
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
	// probe của fhir-server là /healthz và /readyz (khác bộ /health/* của auth và ws-gateway: xem docs/service-principles.md mục 8)
	for _, p := range []string{"/healthz", "/readyz"} {
		if r := do(t, "GET", svc.URL+p, "", ""); r.status != 200 {
			t.Errorf("%s: %d %s", p, r.status, r.body)
		}
	}
	if code, out := svc.Exec(t, "/app", "migrate", "status"); code != 0 || !strings.Contains(out, "chưa áp dụng: 0") {
		t.Errorf("lệnh con `migrate status`: code=%d %q", code, out)
	}
}

// FHIR qua image với SQLite trong /data (thư mục phải ghi được bởi UID 65532, nếu quyền sai thì service không migrate được và không lên) và token tĩnh.
func TestServesFHIRFromTheImage(t *testing.T) {
	svc := run(t, baseEnv())
	api := svc.URL + "/fhir/R4"

	if r := do(t, "GET", api+"/metadata", "", ""); r.status != 200 || r.json(t)["resourceType"] != "CapabilityStatement" {
		t.Errorf("metadata: %d %s", r.status, r.body)
	}
	if r := do(t, "GET", api+"/Patient", "", ""); r.status != 401 {
		t.Errorf("không token phải 401, được %d", r.status)
	}
	if r := do(t, "POST", api+"/Patient", readSecret, `{"resourceType":"Patient"}`); r.status != 403 {
		t.Errorf("token chỉ đọc ghi dữ liệu phải 403, được %d", r.status)
	}

	r := do(t, "POST", api+"/Patient", writeSecret, `{"resourceType":"Patient","name":[{"family":"TrongImage"}],"gender":"female"}`)
	if r.status != 201 {
		t.Fatalf("tạo Patient: %d %s", r.status, r.body)
	}
	id, _ := r.json(t)["id"].(string)
	if got := do(t, "GET", api+"/Patient/"+id, readSecret, ""); got.status != 200 || got.json(t)["id"] != id {
		t.Errorf("đọc lại: %d %s", got.status, got.body)
	}
	q := url.Values{"family": {"TrongImage"}}
	if s := do(t, "GET", api+"/Patient?"+q.Encode(), readSecret, ""); s.status != 200 {
		t.Errorf("tìm kiếm: %d %s", s.status, s.body)
	} else if entries, _ := s.json(t)["entry"].([]any); len(entries) != 1 {
		t.Errorf("tìm family=TrongImage phải ra đúng 1 kết quả, được %d: %s", len(entries), s.body)
	}
	if bad := do(t, "POST", api+"/Patient", writeSecret, `{"resourceType":"Patient","active":"khong-phai-boolean"}`); bad.status != 400 {
		t.Errorf("dữ liệu sai kiểu phải 400 (kiểm tra dữ liệu bật mặc định): %d %s", bad.status, bad.body)
	}
}

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

// Cấu hình sai thì từ chối khởi động ngay, nói rõ biến nào sai, mã thoát khác 0.
func TestRefusesToStartOnBadConfiguration(t *testing.T) {
	img := image(t)
	cases := map[string]struct {
		env  map[string]string
		want string
	}{
		"không có token và không tắt xác thực": {map[string]string{"ENVIRONMENT": "local"}, "FHIR_WRITE_TOKENS"},
		"secret quá ngắn":                      {map[string]string{"FHIR_WRITE_TOKENS": "ops:ngan"}, "secret tối thiểu"},
		"production dùng SQLite":               {map[string]string{"ENVIRONMENT": "production", "FHIR_WRITE_TOKENS": "ops:" + writeSecret}, "SQLite"},
		"production tắt xác thực":              {map[string]string{"ENVIRONMENT": "production", "FHIR_AUTH_DISABLED": "true", "DATABASE_URL": "postgres://u:p@h/db"}, "FHIR_AUTH_DISABLED"},
		"DATABASE_REQUIRE_SCHEMA với SQLite":   {map[string]string{"FHIR_WRITE_TOKENS": "ops:" + writeSecret, "DATABASE_REQUIRE_SCHEMA": "true"}, "DATABASE_REQUIRE_SCHEMA"},
		"FHIR_BASE_URL sai":                    {map[string]string{"FHIR_WRITE_TOKENS": "ops:" + writeSecret, "FHIR_BASE_URL": "khong-phai-url"}, "FHIR_BASE_URL"},
	}
	for name, c := range cases {
		code, logs := testkit.RunToExit(t, testkit.ServiceSpec{Image: img, Env: c.env}, 30*time.Second)
		if code == 0 || !strings.Contains(logs, c.want) {
			t.Errorf("%s: mã thoát %d, log phải nhắc %q:\n%s", name, code, c.want, logs)
		}
	}
}
