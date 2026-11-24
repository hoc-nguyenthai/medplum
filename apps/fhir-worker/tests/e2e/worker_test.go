// Package e2e_test chạy fhir-worker như một service ĐỘC LẬP: dựng image từ Dockerfile của repo rồi chạy cùng PostgreSQL thật bằng testcontainers, nói chuyện với nó chỉ qua cơ sở dữ liệu
// (AsyncJob), không import mã bên trong service. Vì dựng bằng Dockerfile nên các test này kiểm luôn cả image (người dùng không phải root, root filesystem chỉ đọc, HEALTHCHECK, từ chối
// cấu hình sai, tắt êm).
//
// Phía fhir-server được MÔ PHỎNG trong test (một fhirrouter.Router với DisableJobExecution=true trên cùng schema, ký quyền job bằng pkg/fhirauth); hai bên chỉ chung schema và chữ ký
// job-seal (hợp đồng dây, C-6). Container tới PostgreSQL bằng host.docker.internal (Docker Desktop, OrbStack).
package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"trivita-fhir/pkg/database"
	"trivita-fhir/pkg/fhirauth"
	"trivita-fhir/pkg/fhirrouter"
	"trivita-fhir/pkg/fhirstore"
	"trivita-fhir/pkg/testkit"
)

const dockerHost = "host.docker.internal"

var jobKey = strings.Repeat("j", 40)

// image dựng image fhir-worker từ Dockerfile RIÊNG: context là gốc repo, cùng cách `make docker APP=fhir-worker`.
func image(t *testing.T) string {
	t.Helper()
	testkit.RequireDocker(t)
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}").Output()
	if err != nil {
		t.Fatalf("không tìm được thư mục module: %v", err)
	}
	root := strings.TrimSpace(string(out))
	return testkit.BuildImage(t, testkit.BuildSpec{
		Name: "fhir-worker", Context: root, Dockerfile: filepath.Join(root, "apps/fhir-worker/Dockerfile"),
	})
}

func fromContainer(u string) string {
	return strings.NewReplacer("localhost", dockerHost, "127.0.0.1", dockerHost).Replace(u)
}

type stack struct {
	img   string
	dbURL string // địa chỉ từ máy chạy test
	srv   *httptest.Server
	t     *testing.T
}

// newStack dựng schema riêng ĐÃ migrate (như `fhir-server migrate up`) và API mô phỏng có job-seal; chưa chạy worker nào.
func newStack(t *testing.T) *stack {
	t.Helper()
	img := image(t)
	pg := testkit.Postgres(t)
	dsn, _ := pg.NewSchema(t)
	ctx := context.Background()
	db, err := database.Open(ctx, database.Config{URL: dsn})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	st, err := fhirstore.New(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	sealer, err := fhirauth.NewSealer([]byte(jobKey))
	if err != nil {
		t.Fatal(err)
	}
	r := fhirrouter.New(fhirauth.Scope(st), fhirrouter.Options{
		JobRepository: st, ScopedJobRepository: fhirauth.Scope(st), DisableJobExecution: true, BaseURL: "http://fhir.test/fhir/R4", JobLease: time.Minute,
		JobSeal: func(ctx context.Context, id, req string) (string, error) {
			p, _ := fhirauth.PrincipalFrom(ctx)
			return sealer.Seal(id, req, p), nil
		},
	})
	t.Cleanup(func() { _ = r.Shutdown(context.Background()) })
	h := http.StripPrefix("/fhir/R4", r.HTTPHandler())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if p := req.Header.Get("X-Project"); p != "" {
			req = req.WithContext(fhirauth.WithPrincipal(req.Context(), &fhirauth.Principal{Subject: "u-" + p, Project: p, Role: fhirauth.RoleAdmin}))
		}
		h.ServeHTTP(w, req)
	}))
	t.Cleanup(srv.Close)
	return &stack{img: img, dbURL: dsn, srv: srv, t: t}
}

// run chạy fhir-worker (root filesystem chỉ đọc: không có kho Binary nên không ghi đĩa); mut chỉnh biến môi trường.
func (s *stack) run(t *testing.T, mut func(env map[string]string)) *testkit.Service {
	t.Helper()
	env := map[string]string{
		"ENVIRONMENT": "local", "LOG_JSON": "true", "SHUTDOWN_DRAIN_DELAY": "0s", "FHIR_BASE_URL": "http://fhir.test/fhir/R4",
		"DATABASE_URL": fromContainer(s.dbURL), "FHIR_JWT_ISSUER": "https://id.test", "FHIR_JOB_SIGNING_KEY": jobKey,
		"FHIR_JOB_MAINTENANCE_INTERVAL": "500ms", "FHIR_JOB_RECOVERY_GRACE": "1ms",
	}
	if mut != nil {
		mut(env)
	}
	svc := testkit.RunService(t, testkit.ServiceSpec{Image: s.img, Port: "8011/tcp", Health: "/health/ready", Env: env, ReadOnly: true})
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("log fhir-worker:\n%s", svc.Logs(t))
		}
	})
	return svc
}

func (s *stack) do(method, path, project string, body any, hdr ...string) (int, http.Header, []byte) {
	s.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, s.srv.URL+"/fhir/R4/"+path, rd)
	req.Header.Set("Content-Type", "application/fhir+json")
	req.Header.Set("X-Project", project)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, res.Header, b
}

func (s *stack) patients(project string, n int) {
	s.t.Helper()
	for i := 0; i < n; i++ {
		if code, _, b := s.do("POST", "Patient", project, map[string]any{"resourceType": "Patient", "name": []any{map[string]any{"family": "P" + project}}}); code != 201 {
			s.t.Fatalf("tạo Patient: %d %s", code, b)
		}
	}
}

func (s *stack) startExport(project string) string {
	s.t.Helper()
	code, h, b := s.do("GET", "Patient/$export", project, nil, "Prefer", "respond-async")
	if code != 202 {
		s.t.Fatalf("$export: %d %s", code, b)
	}
	return strings.TrimPrefix(h.Get("Content-Location"), "http://fhir.test/fhir/R4/")
}

// status trả mã của trạng thái job và tổng số resource trong output (khi xong).
func (s *stack) status(path, project string) (int, int) {
	s.t.Helper()
	code, _, b := s.do("GET", path, project, nil)
	total := 0
	if code == 200 {
		var out struct {
			Output []struct {
				Count int `json:"count"`
			} `json:"output"`
		}
		_ = json.Unmarshal(b, &out)
		for _, o := range out.Output {
			total += o.Count
		}
	}
	return code, total
}

func (s *stack) waitExport(path, project string) int {
	s.t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		code, n := s.status(path, project)
		switch code {
		case 200:
			return n
		case 202:
			time.Sleep(100 * time.Millisecond)
		default:
			s.t.Fatalf("trạng thái export: %d", code)
		}
	}
	s.t.Fatal("export chưa xong sau 60 giây")
	return 0
}

func TestImageIsHealthyNonRootAndRunsSealedJobsPerProject(t *testing.T) {
	s := newStack(t)
	s.patients("A", 2)
	s.patients("B", 4)
	pa, pb := s.startExport("A"), s.startExport("B")
	time.Sleep(time.Second)
	if code, _ := s.status(pa, "A"); code != 202 {
		t.Fatalf("chưa có worker thì job còn accepted: %d", code)
	}
	svc := s.run(t, nil)
	if code, out := svc.Exec(t, "/app", "healthcheck"); code != 0 {
		t.Fatalf("/app healthcheck: %d %s", code, out)
	}
	if code, out := svc.Exec(t, "/app", "khong-co-lenh"); code == 0 || !strings.Contains(out, "cách dùng") {
		t.Errorf("lệnh lạ phải bị từ chối: %d %s", code, out)
	}
	if a, b := s.waitExport(pa, "A"), s.waitExport(pb, "B"); a != 2 || b != 4 {
		t.Errorf("project A có 2 Patient và B có 4; export được %d và %d", a, b)
	}
}

// Worker dừng (SIGTERM, thoát mã 0); job tạo lúc không có worker nằm ở accepted; chạy lại thì chạy nốt.
func TestStopsGracefullyAndRunsJobsCreatedWhileDown(t *testing.T) {
	s := newStack(t)
	first := s.run(t, nil)
	s.patients("A", 3)
	p1 := s.startExport("A")
	if n := s.waitExport(p1, "A"); n != 3 {
		t.Fatalf("export đầu: %d", n)
	}
	start := time.Now()
	if code := first.Terminate(t, 20*time.Second); code != 0 {
		t.Fatalf("tắt êm phải thoát mã 0, được %d", code)
	}
	if d := time.Since(start); d > 15*time.Second {
		t.Errorf("tắt êm mất %s", d)
	}
	p2 := s.startExport("A") // tạo lúc không có worker nào chạy
	time.Sleep(time.Second)
	if code, _ := s.status(p2, "A"); code != 202 {
		t.Fatalf("chưa có worker mà job đã xong: %d", code)
	}
	s.run(t, nil)
	if n := s.waitExport(p2, "A"); n != 3 {
		t.Errorf("export thứ hai sau khi chạy lại: %d", n)
	}
}

func TestRefusesBadConfigurationAndUnmigratedSchema(t *testing.T) {
	img := image(t)
	t.Run("JWT thiếu khóa ký job", func(t *testing.T) {
		code, logs := testkit.RunToExit(t, testkit.ServiceSpec{Image: img, Env: map[string]string{"ENVIRONMENT": "local", "DATABASE_URL": "sqlite://:memory:", "FHIR_JWT_ISSUER": "https://id.test"}}, 30*time.Second)
		if code == 0 || !strings.Contains(logs, "FHIR_JOB_SIGNING_KEY") {
			t.Fatalf("muốn thoát khác 0 và nêu FHIR_JOB_SIGNING_KEY: %d %s", code, logs)
		}
	})
	t.Run("chu kỳ nhận job 0", func(t *testing.T) {
		code, logs := testkit.RunToExit(t, testkit.ServiceSpec{Image: img, Env: map[string]string{"ENVIRONMENT": "local", "DATABASE_URL": "sqlite://:memory:", "FHIR_JOB_MAINTENANCE_INTERVAL": "0s"}}, 30*time.Second)
		if code == 0 || !strings.Contains(logs, "FHIR_JOB_MAINTENANCE_INTERVAL") {
			t.Fatalf("muốn thoát khác 0 và nêu FHIR_JOB_MAINTENANCE_INTERVAL: %d %s", code, logs)
		}
	})
	t.Run("schema chưa migrate", func(t *testing.T) {
		testkit.RequireDocker(t)
		pg := testkit.Postgres(t)
		dsn, _ := pg.NewSchema(t) // schema trống: chưa chạy `fhir-server migrate up`
		code, logs := testkit.RunToExit(t, testkit.ServiceSpec{Image: img, Env: map[string]string{"ENVIRONMENT": "local", "DATABASE_URL": fromContainer(dsn)}}, 60*time.Second)
		if code == 0 || !strings.Contains(logs, "schema chưa được migrate") {
			t.Fatalf("muốn từ chối vì schema chưa migrate: %d %s", code, logs)
		}
	})
}
