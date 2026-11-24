// Package integration_test chạy fhir-worker TRONG tiến trình test nhưng trên PostgreSQL THẬT (testcontainers): kiểm điều mà SQLite không kiểm được, như role DB hẹp quyền với
// DATABASE_REQUIRE_SCHEMA (mô hình D-1 của docs/service-principles.md) và lease giữa nhiều worker tranh job trên PostgreSQL thật (If-Match, transaction đồng thời).
// Phía fhir-server được mô phỏng bằng một fhirrouter.Router với DisableJobExecution=true trên cùng schema (chung với fhir-worker chỉ schema và chữ ký quyền của job).
package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"trivita-fhir/apps/fhir-worker/internal/bootstrap"
	"trivita-fhir/apps/fhir-worker/internal/config"
	"trivita-fhir/pkg/database"
	"trivita-fhir/pkg/fhirauth"
	"trivita-fhir/pkg/fhirrouter"
	"trivita-fhir/pkg/fhirsearch"
	"trivita-fhir/pkg/fhirstore"
	"trivita-fhir/pkg/testkit"
)

var jobKey = []byte(strings.Repeat("j", 40))

type rig struct {
	t     *testing.T
	store *fhirstore.Store
	srv   *httptest.Server
	dsn   string
}

// newRig dựng schema riêng với role CHỈ có quyền trên schema đó (như fhir_app), migrate bằng chính role đó (như `fhir-server migrate up`) và API mô phỏng có job-seal.
func newRig(t *testing.T) *rig {
	t.Helper()
	testkit.RequireDocker(t)
	pg := testkit.Postgres(t)
	dsn, _, _ := pg.NewServiceRole(t)
	ctx := context.Background()
	db, err := database.Open(ctx, database.Config{URL: dsn, RequireSchema: true})
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
	sealer, err := fhirauth.NewSealer(jobKey)
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
	return &rig{t: t, store: st, srv: srv, dsn: dsn}
}

func (r *rig) settings(t *testing.T, kv ...string) config.Settings {
	t.Helper()
	vars := map[string]string{
		"DATABASE_URL": r.dsn, "DATABASE_REQUIRE_SCHEMA": "true", "FHIR_BASE_URL": "http://fhir.test/fhir/R4", "SHUTDOWN_DRAIN_DELAY": "0s",
		"FHIR_JWT_ISSUER": "https://id.test", "FHIR_JOB_SIGNING_KEY": string(jobKey), "FHIR_JOB_MAINTENANCE_INTERVAL": "100ms", "FHIR_JOB_RECOVERY_GRACE": "1ms",
	}
	for i := 0; i+1 < len(kv); i += 2 {
		vars[kv[i]] = kv[i+1]
	}
	s, err := config.FromEnv(func(k string) string { return vars[k] })
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func (r *rig) do(method, path, project string, body any, hdr ...string) (int, http.Header, []byte) {
	r.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, r.srv.URL+"/fhir/R4/"+path, rd)
	req.Header.Set("Content-Type", "application/fhir+json")
	req.Header.Set("X-Project", project)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		r.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, res.Header, b
}

func (r *rig) patients(project string, n int) {
	r.t.Helper()
	for i := 0; i < n; i++ {
		if code, _, b := r.do("POST", "Patient", project, map[string]any{"resourceType": "Patient", "name": []any{map[string]any{"family": "P" + project}}}); code != 201 {
			r.t.Fatalf("tạo Patient: %d %s", code, b)
		}
	}
}

func (r *rig) startExport(project string) string {
	r.t.Helper()
	code, h, b := r.do("GET", "Patient/$export", project, nil, "Prefer", "respond-async")
	if code != 202 {
		r.t.Fatalf("$export: %d %s", code, b)
	}
	return strings.TrimPrefix(h.Get("Content-Location"), "http://fhir.test/fhir/R4/")
}

func (r *rig) waitExport(path, project string) (total int) {
	r.t.Helper()
	deadline := time.Now().Add(40 * time.Second)
	for time.Now().Before(deadline) {
		code, _, b := r.do("GET", path, project, nil)
		switch code {
		case 202:
			time.Sleep(50 * time.Millisecond)
		case 200:
			var out struct {
				Output []struct {
					Count int `json:"count"`
				} `json:"output"`
			}
			_ = json.Unmarshal(b, &out)
			for _, o := range out.Output {
				total += o.Count
			}
			return total
		default:
			r.t.Fatalf("trạng thái export: %d %s", code, b)
		}
	}
	r.t.Fatal("export chưa xong")
	return 0
}

func binaryCount(t *testing.T, st *fhirstore.Store) int {
	t.Helper()
	sr, _ := fhirsearch.Parse("Binary", nil)
	sr.Count = 200
	res, err := st.Search(context.Background(), sr)
	if err != nil {
		t.Fatal(err)
	}
	return len(res.Matches)
}

func start(t *testing.T, s config.Settings) *bootstrap.Container {
	t.Helper()
	c, err := bootstrap.New(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Stop(context.Background()) })
	return c
}

// Chạy bằng role hẹp quyền trên schema riêng với DATABASE_REQUIRE_SCHEMA=true và chữ ký quyền thật: export của từng project chỉ chứa dữ liệu của project đó.
func TestRunsSealedJobsUnderNarrowRoleAndRequiredSchema(t *testing.T) {
	r := newRig(t)
	r.patients("A", 3)
	r.patients("B", 5)
	pa, pb := r.startExport("A"), r.startExport("B")
	start(t, r.settings(t))
	if a, b := r.waitExport(pa, "A"), r.waitExport(pb, "B"); a != 3 || b != 5 {
		t.Errorf("project A có 3 Patient và B có 5; export được %d và %d", a, b)
	}
}

// Ba worker tranh sáu job trên PostgreSQL thật: lease (If-Match) bảo đảm mỗi job chạy đúng một lần, nên có đúng sáu Binary kết quả (mỗi export một file).
func TestWorkersCompeteForJobsOnRealPostgresAndEachJobRunsOnce(t *testing.T) {
	r := newRig(t)
	r.patients("A", 20)
	var paths []string
	for i := 0; i < 6; i++ {
		paths = append(paths, r.startExport("A"))
	}
	for i := 0; i < 3; i++ {
		start(t, r.settings(t, "FHIR_JOB_CONCURRENCY", "2"))
	}
	for _, p := range paths {
		if n := r.waitExport(p, "A"); n != 20 {
			t.Errorf("mỗi export phải có 20 Patient, được %d", n)
		}
	}
	time.Sleep(time.Second)
	if n := binaryCount(t, r.store); n != 6 {
		t.Errorf("muốn đúng 6 Binary (mỗi job một file, không job nào chạy hai lần), được %d", n)
	}
}
