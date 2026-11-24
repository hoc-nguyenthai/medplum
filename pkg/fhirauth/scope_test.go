package fhirauth_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"trivita-fhir/pkg/database"
	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirauth"
	"trivita-fhir/pkg/fhirrouter"
	"trivita-fhir/pkg/fhirrouter/repotest"
	"trivita-fhir/pkg/fhirsearch"
	"trivita-fhir/pkg/fhirstore"
)

func newSQLite(t *testing.T) *fhirstore.Store {
	t.Helper()
	ctx := context.Background()
	db, err := database.Open(ctx, database.Config{URL: "sqlite://:memory:"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s, err := fhirstore.New(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return s
}

// asProject cố định Principal cho mọi lời gọi (mô phỏng middleware xác thực), để chạy được bộ test dùng chung.
type asProject struct {
	fhirrouter.Repository
	project string
}

func (a asProject) Reindex(ctx context.Context, t, id string, p func(int)) (int, error) {
	return a.Repository.(fhirrouter.Reindexer).Reindex(a.ctx(ctx), t, id, p)
}
func (a asProject) Expunge(ctx context.Context, t, id string) error {
	return a.Repository.(fhirrouter.Expunger).Expunge(a.ctx(ctx), t, id)
}

func (a asProject) ctx(ctx context.Context) context.Context {
	return fhirauth.WithPrincipal(ctx, &fhirauth.Principal{Subject: "u", Project: a.project, Role: fhirauth.RoleAdmin})
}
func (a asProject) CreateResource(ctx context.Context, r fhir.Resource) (fhir.Resource, error) {
	return a.Repository.CreateResource(a.ctx(ctx), r)
}
func (a asProject) ReadResource(ctx context.Context, t, id string) (fhir.Resource, error) {
	return a.Repository.ReadResource(a.ctx(ctx), t, id)
}
func (a asProject) ReadVersion(ctx context.Context, t, id, v string) (fhir.Resource, error) {
	return a.Repository.ReadVersion(a.ctx(ctx), t, id, v)
}
func (a asProject) ReadHistory(ctx context.Context, t, id string) ([]fhirrouter.HistoryEntry, error) {
	return a.Repository.ReadHistory(a.ctx(ctx), t, id)
}
func (a asProject) UpdateResource(ctx context.Context, r fhir.Resource, o fhirrouter.UpdateOptions) (fhir.Resource, error) {
	return a.Repository.UpdateResource(a.ctx(ctx), r, o)
}
func (a asProject) DeleteResource(ctx context.Context, t, id string) error {
	return a.Repository.DeleteResource(a.ctx(ctx), t, id)
}
func (a asProject) Search(ctx context.Context, r *fhirsearch.Request) (*fhirrouter.SearchResult, error) {
	return a.Repository.Search(a.ctx(ctx), r)
}
func (a asProject) LockKey(ctx context.Context, key string) error {
	return a.Repository.(fhirrouter.KeyLocker).LockKey(a.ctx(ctx), key)
}

func (a asProject) RunInTransaction(ctx context.Context, fn func(fhirrouter.Repository) error) error {
	return a.Repository.(fhirrouter.Transactional).RunInTransaction(a.ctx(ctx), func(rp fhirrouter.Repository) error {
		return fn(asProject{rp, a.project})
	})
}

// Một project dùng Scope phải hành xử y hệt repository không tách (bộ test hành vi REST dùng chung).
func TestScopedParity(t *testing.T) {
	backends := map[string]func(*testing.T) fhirrouter.Repository{
		"memory": func(*testing.T) fhirrouter.Repository { return fhirrouter.NewMemoryRepository() },
		"sqlite": func(t *testing.T) fhirrouter.Repository { return newSQLite(t) },
	}
	if os.Getenv(postgresEnv) != "" {
		backends["postgres"] = func(t *testing.T) fhirrouter.Repository { return newPostgres(t) }
	}
	for name, mk := range backends {
		t.Run(name, func(t *testing.T) {
			repotest.Run(t, func(t *testing.T) fhirrouter.Repository {
				base := mk(t)
				return asProject{Repository: fhirauth.Scope(base), project: "p1"}
			})
		})
	}
}

// ---------------------------------------------------------------- cô lập giữa project

type tenantEnv struct {
	t   *testing.T
	srv http.Handler
}

func (e *tenantEnv) do(project, method, path, body string) (int, map[string]any) {
	e.t.Helper()
	req := httptest.NewRequest(method, "/fhir/R4/"+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/fhir+json")
	if project != "" {
		req = req.WithContext(fhirauth.WithPrincipal(req.Context(), &fhirauth.Principal{Subject: "u-" + project, Project: project, Role: fhirauth.RoleAdmin}))
	}
	rec := httptest.NewRecorder()
	e.srv.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func newTenantEnv(t *testing.T, base fhirrouter.Repository) *tenantEnv {
	r := fhirrouter.New(fhirauth.Scope(base), fhirrouter.Options{JobRepository: base})
	t.Cleanup(func() { _ = r.Shutdown(context.Background()) })
	return &tenantEnv{t, http.StripPrefix("/fhir/R4", r.HTTPHandler())}
}

func entries(b map[string]any) int {
	e, _ := b["entry"].([]any)
	return len(e)
}

func TestProjectIsolation(t *testing.T) {
	for name, mk := range withPostgres(t, map[string]func() fhirrouter.Repository{
		"memory": func() fhirrouter.Repository { return fhirrouter.NewMemoryRepository() },
		"sqlite": func() fhirrouter.Repository { return newSQLite(t) },
	}) {
		t.Run(name, func(t *testing.T) {
			e := newTenantEnv(t, mk())

			code, a := e.do("A", "POST", "Patient", `{"resourceType":"Patient","name":[{"family":"Alpha"}],"meta":{"project":"B"}}`)
			if code != 201 {
				t.Fatalf("create A: %d %v", code, a)
			}
			if p := a["meta"].(map[string]any)["project"]; p != "A" {
				t.Fatalf("meta.project do client gửi phải bị ghi đè, got %v", p)
			}
			idA := a["id"].(string)
			_, b := e.do("B", "POST", "Patient", `{"resourceType":"Patient","name":[{"family":"Beta"}]}`)
			idB := b["id"].(string)

			// đọc chéo
			for _, c := range []struct{ project, method, path, body string }{
				{"B", "GET", "Patient/" + idA, ""},
				{"B", "GET", "Patient/" + idA + "/_history", ""},
				{"B", "PUT", "Patient/" + idA, `{"resourceType":"Patient","id":"` + idA + `","name":[{"family":"Hijack"}]}`},
				{"B", "DELETE", "Patient/" + idA, ""},
				{"B", "POST", "Patient/" + idA + "/$expunge", ""},
				{"B", "GET", "Patient/" + idA + "/$everything", ""},
			} {
				if code, _ := e.do(c.project, c.method, c.path, c.body); code != 404 {
					t.Errorf("%s %s bởi project khác: %d, muốn 404", c.method, c.path, code)
				}
			}
			// A vẫn nguyên vẹn
			if code, got := e.do("A", "GET", "Patient/"+idA, ""); code != 200 || got["name"].([]any)[0].(map[string]any)["family"] != "Alpha" {
				t.Fatalf("dữ liệu của A bị đổi: %d %v", code, got)
			}

			// search chỉ thấy project của mình, cả search hệ thống
			for _, path := range []string{"Patient", "Patient?name=Alpha", "?_type=Patient", "Patient?_project=A"} {
				_, ra := e.do("B", "GET", path, "")
				for _, en := range asList(ra["entry"]) {
					if id := en.(map[string]any)["resource"].(map[string]any)["id"]; id == idA {
						t.Errorf("B thấy dữ liệu của A qua %q", path)
					}
				}
			}
			if _, r := e.do("A", "GET", "Patient", ""); entries(r) != 1 {
				t.Errorf("A phải thấy đúng 1 Patient, got %d", entries(r))
			}
			if _, r := e.do("B", "GET", "Patient?_total=accurate", ""); r["total"] != float64(1) {
				t.Errorf("total của B = %v, muốn 1", r["total"])
			}

			// _include/_revinclude không rò: Observation của B trỏ Patient của A
			_, _ = e.do("B", "POST", "Observation", `{"resourceType":"Observation","status":"final","code":{"text":"x"},"subject":{"reference":"Patient/`+idA+`"}}`)
			_, inc := e.do("B", "GET", "Observation?_include=Observation:subject", "")
			for _, en := range asList(inc["entry"]) {
				if en.(map[string]any)["resource"].(map[string]any)["resourceType"] == "Patient" {
					t.Error("_include rò Patient của project khác")
				}
			}
			_, rev := e.do("B", "GET", "Patient?_revinclude=Observation:subject", "")
			if entries(rev) != 1 {
				t.Errorf("_revinclude của B: %d entry, muốn 1 (Patient của B)", entries(rev))
			}
			_ = idB

			// chained search và _has không dùng dữ liệu của project khác làm điều kiện
			_, _ = e.do("B", "POST", "Observation", `{"resourceType":"Observation","status":"final","code":{"text":"y"},"subject":{"reference":"Patient/`+idB+`"}}`)
			if _, r := e.do("B", "GET", "Observation?subject:Patient.family=Alpha", ""); entries(r) != 0 {
				t.Error("chained của B khớp Patient của A")
			}
			if _, r := e.do("B", "GET", "Observation?subject:Patient.family=Beta", ""); entries(r) != 1 {
				t.Errorf("chained của B không thấy Patient của B: %d", entries(r))
			}
			if _, r := e.do("A", "GET", "Patient?_has:Observation:subject:status=final", ""); entries(r) != 0 {
				t.Error("_has của A thấy Observation của B")
			}
			if _, r := e.do("B", "GET", "Patient?_has:Observation:subject:status=final", ""); entries(r) != 1 {
				t.Errorf("_has của B: %d", entries(r))
			}

			// transaction cũng bị tách và gán project
			code, tx := e.do("B", "POST", "", `{"resourceType":"Bundle","type":"transaction","entry":[{"request":{"method":"POST","url":"Patient"},"resource":{"resourceType":"Patient","name":[{"family":"Gamma"}]}}]}`)
			if code != 200 {
				t.Fatalf("transaction: %d %v", code, tx)
			}
			if _, r := e.do("A", "GET", "Patient?name=Gamma", ""); entries(r) != 0 {
				t.Error("A thấy Patient tạo bằng transaction của B")
			}
			if _, r := e.do("B", "GET", "Patient?name=Gamma", ""); entries(r) != 1 {
				t.Error("B không thấy Patient tạo bằng transaction của chính mình")
			}

			// lịch sử và expunge của chủ sở hữu
			e.do("A", "DELETE", "Patient/"+idA, "")
			if code, _ := e.do("A", "GET", "Patient/"+idA+"/_history", ""); code != 200 {
				t.Errorf("history của chủ sau khi xóa: %d", code)
			}
			if code, _ := e.do("B", "POST", "Patient/"+idA+"/$expunge", ""); code != 404 {
				t.Errorf("expunge resource đã xóa của project khác: %d, muốn 404", code)
			}
			if code, _ := e.do("A", "POST", "Patient/"+idA+"/$expunge", ""); code != 200 {
				t.Errorf("expunge của chủ: %d", code)
			}

			// không có Principal thì đóng
			if code, _ := e.do("", "GET", "Patient", ""); code != 403 {
				t.Errorf("thiếu principal: %d, muốn 403", code)
			}
			if code, _ := e.do("", "POST", "Patient", `{"resourceType":"Patient"}`); code != 403 {
				t.Errorf("tạo khi thiếu principal: %d, muốn 403", code)
			}
		})
	}
}

func asList(v any) []any { l, _ := v.([]any); return l }

// Update-as-create dưới tách project: id đã bị project khác dùng phải trả 404 như chưa tồn tại (không lộ sự tồn tại),
// và không bao giờ ghi đè dữ liệu của project khác.
func TestUpdateAsCreateAcrossProjects(t *testing.T) {
	for name, mk := range withPostgres(t, map[string]func() fhirrouter.Repository{
		"memory": func() fhirrouter.Repository { return fhirrouter.NewMemoryRepository() },
		"sqlite": func() fhirrouter.Repository { return newSQLite(t) },
	}) {
		t.Run(name, func(t *testing.T) {
			base := mk()
			r := fhirrouter.New(fhirauth.Scope(base), fhirrouter.Options{JobRepository: base, UpdateAsCreate: true})
			t.Cleanup(func() { _ = r.Shutdown(context.Background()) })
			e := &tenantEnv{t, http.StripPrefix("/fhir/R4", r.HTTPHandler())}

			if code, out := e.do("A", "PUT", "Patient/shared-id", `{"resourceType":"Patient","id":"shared-id","name":[{"family":"OfA"}]}`); code != 201 {
				t.Fatalf("A tạo: %d %v", code, out)
			}
			// B cố tạo hoặc ghi đè cùng id: 404, dữ liệu của A không đổi
			if code, _ := e.do("B", "PUT", "Patient/shared-id", `{"resourceType":"Patient","id":"shared-id","name":[{"family":"Hijack"}]}`); code != 404 {
				t.Fatalf("B ghi đè id của A: %d, muốn 404", code)
			}
			code, out := e.do("A", "GET", "Patient/shared-id", "")
			if code != 200 || !strings.Contains(string(mustJSON(t, out)), "OfA") || !strings.Contains(string(mustJSON(t, out)), `"project":"A"`) {
				t.Fatalf("dữ liệu của A bị đổi: %d %v", code, out)
			}
			// B tạo id khác được, và A không thấy
			if code, _ := e.do("B", "PUT", "Patient/b-only", `{"resourceType":"Patient","id":"b-only"}`); code != 201 {
				t.Fatalf("B tạo: %d", code)
			}
			if code, _ := e.do("A", "GET", "Patient/b-only", ""); code != 404 {
				t.Errorf("A thấy dữ liệu của B: %d", code)
			}
			// không có người gọi: bị từ chối
			if code, _ := e.do("", "PUT", "Patient/anon", `{"resourceType":"Patient","id":"anon"}`); code != 403 {
				t.Errorf("không principal: %d", code)
			}
		})
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// recordingReindexer ghi lại mọi lần reindex để kiểm Reindex cả loại chỉ chạm resource của project người gọi.
type recordingReindexer struct {
	fhirrouter.Repository
	calls []string
}

func (r *recordingReindexer) Reindex(_ context.Context, rt, id string, _ func(int)) (int, error) {
	r.calls = append(r.calls, rt+"/"+id)
	return 1, nil
}

// $reindex cả loại (id rỗng) không được chạm resource của project khác: kho bên dưới chỉ biết reindex theo loại trên mọi project, nên lớp tách project
// phải liệt kê id của project rồi reindex từng resource.
func TestReindexTypeStaysInsideCallerProject(t *testing.T) {
	rec := &recordingReindexer{Repository: fhirrouter.NewMemoryRepository()}
	repo := fhirauth.Scope(rec)
	as := func(project string) context.Context {
		return fhirauth.WithPrincipal(context.Background(), &fhirauth.Principal{Subject: "u", Project: project, Role: fhirauth.RoleAdmin})
	}
	var idsA []string
	for i := 0; i < 3; i++ {
		r, err := repo.CreateResource(as("A"), &fhir.Patient{})
		if err != nil {
			t.Fatal(err)
		}
		idsA = append(idsA, r.GetID())
	}
	rb, err := repo.CreateResource(as("B"), &fhir.Patient{})
	if err != nil {
		t.Fatal(err)
	}

	n, err := repo.Reindex(as("A"), "Patient", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 || len(rec.calls) != 3 {
		t.Fatalf("project A có 3 Patient, reindex %d (gọi %v)", n, rec.calls)
	}
	for _, c := range rec.calls {
		if c == "Patient/"+rb.GetID() {
			t.Fatalf("reindex chạm resource của project B: %v", rec.calls)
		}
	}
	if _, err := repo.Reindex(as("B"), "Patient", "", nil); err != nil || len(rec.calls) != 4 || rec.calls[3] != "Patient/"+rb.GetID() {
		t.Fatalf("project B chỉ reindex Patient của B: %v %v", err, rec.calls)
	}
	// token có chính sách truy cập vẫn bị chặn
	policy, err := fhirauth.ParsePolicy([]any{})
	if err != nil {
		t.Fatal(err)
	}
	pol := fhirauth.WithPrincipal(context.Background(), &fhirauth.Principal{Subject: "u", Project: "A", Role: fhirauth.RoleAdmin, Policy: policy})
	if _, err := repo.Reindex(pol, "Patient", "", nil); err == nil {
		t.Fatal("token có chính sách phải bị chặn reindex")
	}
}
