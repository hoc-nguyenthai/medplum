package fhirauth_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"sort"
	"strings"
	"testing"

	"trivita-fhir/pkg/fhirauth"
	"trivita-fhir/pkg/fhirrouter"
	"trivita-fhir/pkg/fhirsearch"
)

// Chính sách `_compartment=Patient/<id>` (P8 M1): token của bệnh nhân chỉ thấy resource trong compartment của mình qua mọi đường đọc, và total/phân trang chỉ
// tính phần được thấy.
type compEnv struct {
	t        *testing.T
	h        http.Handler
	repo     fhirrouter.Repository
	admin    context.Context
	p1, p2   string
	o1, o2   string // o1: subject P1; o2: subject P2
	o3       string // subject P2 nhưng performer P1: thuộc compartment của cả hai
	patient1 *fhirauth.Principal
}

func newCompEnv(t *testing.T, base fhirrouter.Repository) *compEnv {
	t.Helper()
	repo := fhirauth.Scope(base)
	r := fhirrouter.New(repo, fhirrouter.Options{JobRepository: base})
	t.Cleanup(func() { _ = r.Shutdown(context.Background()) })
	e := &compEnv{t: t, repo: repo, h: http.StripPrefix("/fhir/R4", r.HTTPHandler())}
	e.admin = fhirauth.WithPrincipal(context.Background(), &fhirauth.Principal{Subject: "admin", Project: "p1", Role: fhirauth.RoleAdmin})
	e.p1 = mustCreate(t, repo, e.admin, `{"resourceType":"Patient"}`)
	e.p2 = mustCreate(t, repo, e.admin, `{"resourceType":"Patient"}`)
	obs := func(subject, performer string) string {
		s := `{"resourceType":"Observation","status":"final","code":{"text":"x"},"subject":{"reference":"Patient/` + subject + `"}`
		if performer != "" {
			s += `,"performer":[{"reference":"Patient/` + performer + `"}]`
		}
		return s + `}`
	}
	e.o1 = mustCreate(t, repo, e.admin, obs(e.p1, ""))
	e.o2 = mustCreate(t, repo, e.admin, obs(e.p2, ""))
	e.o3 = mustCreate(t, repo, e.admin, obs(e.p2, e.p1))
	for i := 0; i < 4; i++ { // nhiều resource của P2 mới hơn, để trang đầu không vô tình đúng
		mustCreate(t, repo, e.admin, obs(e.p2, ""))
	}
	pol, err := fhirauth.ParsePolicy([]any{
		map[string]any{"resourceType": "Patient", "criteria": "_compartment=Patient/" + e.p1, "readonly": true},
		map[string]any{"resourceType": "Observation", "criteria": "_compartment=Patient/" + e.p1, "readonly": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	e.patient1 = &fhirauth.Principal{Subject: "patient1", Project: "p1", Role: fhirauth.RoleWrite, Grants: fhirauth.Grants{"*": fhirauth.PermAll}, Policy: pol}
	return e
}

// onEachBackend chạy cùng một kịch bản trên memory, SQLite và (khi đặt FHIRSTORE_TEST_POSTGRES_URL) PostgreSQL: quy tắc compartment phải cho kết quả
// giống nhau ở mọi kho, vì SQL của biểu thức OR được dựng riêng cho từng dialect.
func onEachBackend(t *testing.T, fn func(*testing.T, *compEnv)) {
	t.Helper()
	backends := map[string]func(*testing.T) fhirrouter.Repository{
		"memory": func(*testing.T) fhirrouter.Repository { return fhirrouter.NewMemoryRepository() },
		"sqlite": func(t *testing.T) fhirrouter.Repository { return newSQLite(t) },
	}
	if os.Getenv(postgresEnv) != "" {
		backends["postgres"] = func(t *testing.T) fhirrouter.Repository { return newPostgres(t) }
	}
	for name, mk := range backends {
		t.Run(name, func(t *testing.T) { fn(t, newCompEnv(t, mk(t))) })
	}
}

func (e *compEnv) do(p *fhirauth.Principal, method, path, body string) (int, map[string]any) {
	e.t.Helper()
	req := httptest.NewRequest(method, "/fhir/R4/"+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/fhir+json")
	req = req.WithContext(fhirauth.WithPrincipal(req.Context(), p))
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func (e *compEnv) admin1() *fhirauth.Principal {
	return &fhirauth.Principal{Subject: "admin", Project: "p1", Role: fhirauth.RoleAdmin}
}

// ids trả "Loại/id" của các entry (có thể lọc theo loại), đã sắp xếp.
func ids(b map[string]any, onlyType string) []string {
	var out []string
	for _, x := range asList(b["entry"]) {
		res, _ := x.(map[string]any)["resource"].(map[string]any)
		if res == nil {
			continue
		}
		t, _ := res["resourceType"].(string)
		id, _ := res["id"].(string)
		if onlyType == "" || t == onlyType {
			out = append(out, t+"/"+id)
		}
	}
	sort.Strings(out)
	return out
}

func TestCompartmentPolicyRead(t *testing.T) { onEachBackend(t, testCompartmentPolicyRead) }

func testCompartmentPolicyRead(t *testing.T, e *compEnv) {
	pt := e.patient1
	if c, _ := e.do(pt, "GET", "Observation/"+e.o1, ""); c != 200 {
		t.Errorf("obs của P1: %d", c)
	}
	if c, _ := e.do(pt, "GET", "Observation/"+e.o3, ""); c != 200 {
		t.Errorf("obs thuộc compartment của P1 qua performer: %d", c)
	}
	if c, _ := e.do(pt, "GET", "Observation/"+e.o2, ""); c != 404 {
		t.Errorf("obs của P2 phải 404, được %d", c)
	}
	if c, _ := e.do(pt, "GET", "Patient/"+e.p1, ""); c != 200 {
		t.Errorf("chính P1: %d", c)
	}
	if c, _ := e.do(pt, "GET", "Patient/"+e.p2, ""); c != 404 {
		t.Errorf("P2 phải 404, được %d", c)
	}
	if c, _ := e.do(pt, "GET", "Observation/"+e.o2+"/_history", ""); c != 404 {
		t.Errorf("history của obs P2 phải 404, được %d", c)
	}
	if c, b := e.do(pt, "GET", "Observation/"+e.o1+"/_history", ""); c != 200 || len(ids(b, "")) != 1 {
		t.Errorf("history của obs P1: %d %v", c, ids(b, ""))
	}
}

func TestCompartmentPolicySearchTotalAndPaging(t *testing.T) {
	onEachBackend(t, testCompartmentPolicySearchTotalAndPaging)
}

func testCompartmentPolicySearchTotalAndPaging(t *testing.T, e *compEnv) {
	c, b := e.do(e.patient1, "GET", "Observation?_total=accurate&_count=1", "")
	if c != 200 || b["total"] != float64(2) || len(ids(b, "")) != 1 {
		t.Fatalf("total phải là 2 (không lộ 7 resource của project) và trang đầy: %d total=%v ids=%v", c, b["total"], ids(b, ""))
	}
	got := map[string]bool{}
	for page := 0; page < 5 && b != nil; page++ {
		for _, id := range ids(b, "Observation") {
			got[id] = true
		}
		next := ""
		for _, l := range asList(b["link"]) {
			if m, ok := l.(map[string]any); ok && m["relation"] == "next" {
				next, _ = m["url"].(string)
			}
		}
		if next == "" {
			break
		}
		next = next[strings.Index(next, "Observation?"):]
		_, b = e.do(e.patient1, "GET", next, "")
	}
	want := []string{"Observation/" + e.o1, "Observation/" + e.o3}
	var have []string
	for k := range got {
		have = append(have, k)
	}
	sort.Strings(have)
	sort.Strings(want)
	if !slices.Equal(have, want) {
		t.Errorf("duyệt hết các trang phải ra đúng %v, được %v", want, have)
	}
	// điều kiện của người dùng AND với chính sách: o3 vẫn thấy (subject P2 nhưng performer P1), o2 thì không
	_, b = e.do(e.patient1, "GET", "Observation?subject=Patient/"+e.p2, "")
	if got := ids(b, ""); !slices.Equal(got, []string{"Observation/" + e.o3}) {
		t.Errorf("subject=P2 chỉ còn o3: %v", got)
	}
	// _compartment của người dùng AND với chính sách (giao hai compartment): chỉ o3 thuộc cả P1 lẫn P2
	_, b = e.do(e.patient1, "GET", "Observation?_compartment=Patient/"+e.p2, "")
	if got := ids(b, ""); !slices.Equal(got, []string{"Observation/" + e.o3}) {
		t.Errorf("giao compartment P1 và P2: %v", got)
	}
}

func TestCompartmentPolicyIncludesAndEverything(t *testing.T) {
	onEachBackend(t, testCompartmentPolicyIncludesAndEverything)
}

func testCompartmentPolicyIncludesAndEverything(t *testing.T, e *compEnv) {
	pt := e.patient1
	// _include: Patient của o3 (P2) tham chiếu được, nhưng không được lộ vì ngoài compartment
	_, b := e.do(pt, "GET", "Observation?_include=Observation:subject", "")
	if got := ids(b, "Patient"); !slices.Equal(got, []string{"Patient/" + e.p1}) {
		t.Errorf("_include chỉ được kèm Patient của P1: %v", got)
	}
	// _revinclude: từ Patient, chỉ kèm Observation thuộc compartment
	_, b = e.do(pt, "GET", "Patient?_revinclude=Observation:subject", "")
	if m := ids(b, "Patient"); !slices.Equal(m, []string{"Patient/" + e.p1}) {
		t.Errorf("search Patient chỉ thấy P1: %v", m)
	}
	for _, id := range ids(b, "Observation") {
		if id == "Observation/"+e.o2 {
			t.Errorf("_revinclude lộ obs của P2")
		}
	}
	// $everything của người khác bị chặn
	if c, _ := e.do(pt, "GET", "Patient/"+e.p2+"/$everything", ""); c != 404 {
		t.Errorf("$everything của P2 phải 404, được %d", c)
	}
	// so với $everything của chính P1 (quyền admin): cùng tập Observation với search _compartment
	_, ev := e.do(e.admin1(), "GET", "Patient/"+e.p1+"/$everything", "")
	_, sr := e.do(e.admin1(), "GET", "Observation?_compartment=Patient/"+e.p1, "")
	if a, b := ids(ev, "Observation"), ids(sr, "Observation"); !slices.Equal(a, b) || len(a) != 2 {
		t.Errorf("$everything %v khác search _compartment %v", a, b)
	}
	// $everything của chính P1 qua token bệnh nhân khớp tập trên
	_, mine := e.do(pt, "GET", "Patient/"+e.p1+"/$everything", "")
	want := []string{"Observation/" + e.o1, "Observation/" + e.o3}
	sort.Strings(want) // id là UUID ngẫu nhiên: ids() trả theo thứ tự chữ
	if a := ids(mine, "Observation"); !slices.Equal(a, want) {
		t.Errorf("$everything của P1 qua token P1: %v, muốn %v", a, want)
	}
}

func TestCompartmentPolicyWriteAndBatch(t *testing.T) {
	onEachBackend(t, testCompartmentPolicyWriteAndBatch)
}

func testCompartmentPolicyWriteAndBatch(t *testing.T, e *compEnv) {
	pt := e.patient1
	if c, _ := e.do(pt, "PUT", "Observation/"+e.o1, `{"resourceType":"Observation","id":"`+e.o1+`","status":"final","code":{"text":"y"},"subject":{"reference":"Patient/`+e.p1+`"}}`); c != 403 {
		t.Errorf("rule readonly phải chặn ghi: %d", c)
	}
	bundle := `{"resourceType":"Bundle","type":"batch","entry":[
		{"request":{"method":"GET","url":"Observation/` + e.o1 + `"}},
		{"request":{"method":"GET","url":"Observation/` + e.o2 + `"}}]}`
	c, b := e.do(pt, "POST", "", bundle)
	if c != 200 {
		t.Fatalf("batch: %d", c)
	}
	var statuses []string
	for _, x := range asList(b["entry"]) {
		r, _ := x.(map[string]any)["response"].(map[string]any)
		s, _ := r["status"].(string)
		statuses = append(statuses, strings.Fields(s + " ")[0])
	}
	if !slices.Equal(statuses, []string{"200", "404"}) {
		t.Errorf("batch phải thấy o1 và không thấy o2: %v", statuses)
	}
}

func TestCompartmentPolicyParsing(t *testing.T) {
	for _, bad := range []string{
		"Observation?_compartment=Patient/1,Patient/2", // nhiều compartment
		"Observation?_compartment=Group/1",
		"Observation?_compartment=Patient/",
		"Observation?_compartment=Patient/a b",
		"Observation?_compartment=Patient/1&_compartment=Patient/2",
		"Practitioner?_compartment=Patient/1", // loại không thuộc compartment Patient
		"Observation?_compartment=Patient/1&_filter=status eq final",
	} {
		if _, err := fhirauth.ParsePolicy([]any{bad}); err == nil {
			t.Errorf("phải từ chối: %q", bad)
		}
	}
	if _, err := fhirauth.ParsePolicy([]any{"Observation?_compartment=Patient/123", map[string]any{"resourceType": "Patient", "criteria": "_compartment=Patient/123", "readonly": true}}); err != nil {
		t.Errorf("dạng hợp lệ bị từ chối: %v", err)
	}
	// search của người dùng: cần chỉ định loại
	if _, err := fhirsearch.ParseQuery("", "_compartment=Patient/1"); err == nil {
		t.Error("search hệ thống với _compartment phải lỗi")
	}
}

// $everything của token có chính sách: loại không được đọc bị bỏ qua khi không chỉ định _type; chỉ định _type bị cấm thì vẫn lỗi.
func TestEverythingSkipsForbiddenTypesOnlyWithoutExplicitType(t *testing.T) {
	onEachBackend(t, testEverythingSkipsForbiddenTypesOnlyWithoutExplicitType)
}

func testEverythingSkipsForbiddenTypesOnlyWithoutExplicitType(t *testing.T, e *compEnv) {
	mustCreate(t, e.repo, e.admin, `{"resourceType":"Condition","subject":{"reference":"Patient/`+e.p1+`"}}`) // ngoài chính sách
	c, b := e.do(e.patient1, "GET", "Patient/"+e.p1+"/$everything", "")
	if c != 200 {
		t.Fatalf("không chỉ định _type: %d", c)
	}
	for _, id := range ids(b, "") {
		if strings.HasPrefix(id, "Condition/") {
			t.Errorf("lộ loại ngoài chính sách: %s", id)
		}
	}
	if c, _ = e.do(e.patient1, "GET", "Patient/"+e.p1+"/$everything?_type=Condition", ""); c != 403 {
		t.Errorf("chỉ định _type bị cấm phải 403, được %d", c)
	}
}

// Điều kiện tĩnh của chính sách (không phải compartment) cũng phải được đẩy xuống kho: total và trang chỉ tính phần được thấy.
func TestStaticPolicyCriteriaArePushedDown(t *testing.T) {
	onEachBackend(t, testStaticPolicyCriteriaArePushedDown)
}

func testStaticPolicyCriteriaArePushedDown(t *testing.T, e *compEnv) {
	pol, err := fhirauth.ParsePolicy([]any{map[string]any{"resourceType": "Observation", "criteria": "subject=Patient/" + e.p1}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := fhirauth.WithPrincipal(context.Background(), &fhirauth.Principal{Subject: "u", Project: "p1", Role: fhirauth.RoleWrite, Grants: fhirauth.Grants{"*": fhirauth.PermAll}, Policy: pol})
	req, err := fhirsearch.ParseQuery("Observation", "_count=1&_total=accurate")
	if err != nil {
		t.Fatal(err)
	}
	res, err := e.repo.Search(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if res.Total == nil || *res.Total != 1 || len(res.Matches) != 1 {
		t.Errorf("chỉ o1 (subject P1) được thấy: total=%v matches=%d (project có 7 Observation)", res.Total, len(res.Matches))
	}
}
