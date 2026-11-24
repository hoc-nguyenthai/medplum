package fhirrouter_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirrouter"
	"trivita-fhir/pkg/fhirsearch"
)

// Job có chủ sở hữu: chỉ người tạo hoặc quản trị xem, hủy, xóa; người khác nhận 404; AsyncJob không lọt qua tìm kiếm hệ thống hay GraphQL.

type userKey struct{}

func asUser(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey{}, r.Header.Get("X-User"))))
	})
}

func ownerOpts() fhirrouter.Options {
	return fhirrouter.Options{
		JobOwner: func(ctx context.Context) string { s, _ := ctx.Value(userKey{}).(string); return s },
		JobAllowed: func(ctx context.Context, owner string) bool {
			u, _ := ctx.Value(userKey{}).(string)
			return u == "" || u == "root" || (owner != "" && owner == u)
		},
	}
}

func startExport(t *testing.T, h http.Handler, user string) string {
	t.Helper()
	w := serve(h, httptestRequest("GET", "/$export", "", "Prefer", "respond-async", "X-User", user))
	if w.StatusCode != 202 {
		t.Fatalf("export: %d", w.StatusCode)
	}
	loc := w.Header.Get("Content-Location")
	i := strings.Index(loc, "bulkdata/")
	return strings.TrimSuffix(loc[i+len("bulkdata/"):], "/status")
}

func TestJobIsStampedWithItsCreatorAndHiddenFromOthers(t *testing.T) {
	repo := fhirrouter.NewMemoryRepository()
	h := asUser(newRouter(t, repo, ownerOpts()).HTTPHandler())
	id := startExport(t, h, "alice")

	res, err := repo.ReadResource(context.Background(), "AsyncJob", id)
	if err != nil {
		t.Fatal(err)
	}
	rq := res.(*fhir.AsyncJob).Requester
	if rq == nil || rq.Identifier == nil || rq.Identifier.System != fhirrouter.JobOwnerSystem || rq.Identifier.Value != "alice" {
		t.Fatalf("requester = %+v, muốn chủ là alice", rq)
	}
	for _, path := range []string{"bulkdata/" + id + "/status", "job/" + id + "/status"} {
		if c, _ := putTo(h, "GET", path, "", "X-User", "bob"); c != 404 {
			t.Errorf("bob GET %s: %d, muốn 404", path, c)
		}
		if c, _ := putTo(h, "GET", path, "", "X-User", "alice"); c == 404 {
			t.Errorf("alice GET %s: 404, chủ phải thấy job", path)
		}
		if c, _ := putTo(h, "GET", path, "", "X-User", "root"); c == 404 {
			t.Errorf("root GET %s: 404, quản trị phải thấy job", path)
		}
	}
	if c, _ := putTo(h, "DELETE", "bulkdata/"+id+"/status", "", "X-User", "bob"); c != 404 {
		t.Errorf("bob DELETE: %d, muốn 404", c)
	}
	if _, err := repo.ReadResource(context.Background(), "AsyncJob", id); err != nil {
		t.Errorf("job bị đụng tới dù bob không được phép: %v", err)
	}
	if c, _ := putTo(h, "DELETE", "bulkdata/"+id+"/status", "", "X-User", "alice"); c != 202 {
		t.Errorf("alice DELETE: %d, muốn 202", c)
	}
}

func TestJobWithoutOwnerIsAdminOnlyAndBundlesAreScrubbed(t *testing.T) {
	repo := fhirrouter.NewMemoryRepository()
	h := asUser(newRouter(t, repo, ownerOpts()).HTTPHandler())
	// job cũ, không có chủ (tạo trước khi có kiểm chủ sở hữu)
	if _, err := repo.CreateResource(context.Background(), &fhir.AsyncJob{ID: "legacy", Status: "completed", Request: "export?_level=system"}); err != nil {
		t.Fatal(err)
	}
	if c, _ := putTo(h, "GET", "bulkdata/legacy/status", "", "X-User", "bob"); c != 404 {
		t.Errorf("job không chủ, bob: %d, muốn 404", c)
	}
	if c, _ := putTo(h, "GET", "bulkdata/legacy/status", "", "X-User", "root"); c != 200 {
		t.Errorf("job không chủ, root: %d, muốn 200", c)
	}
	id := startExport(t, h, "alice")

	// tìm kiếm cấp hệ thống: bob không thấy job nào, alice chỉ thấy job của mình, root thấy cả hai; total được trừ tương ứng
	count := func(user string) (int, string) {
		_, body := putTo(h, "GET", "?_type=AsyncJob&_total=accurate", "", "X-User", user)
		return strings.Count(body, `"resourceType":"AsyncJob"`), body
	}
	if n, b := count("bob"); n != 0 || !strings.Contains(b, `"total":0`) {
		t.Errorf("bob thấy %d job, total phải là 0: %s", n, b)
	}
	if n, b := count("alice"); n != 1 || !strings.Contains(b, id) || !strings.Contains(b, `"total":1`) {
		t.Errorf("alice phải thấy đúng 1 job của mình: n=%d %s", n, b)
	}
	if n, _ := count("root"); n != 2 {
		t.Errorf("root thấy %d job, muốn 2", n)
	}

	// GraphQL
	gq := `{"query":"{ AsyncJobList { id } }"}`
	if _, b := putTo(h, "POST", "$graphql", gq, "X-User", "bob"); strings.Contains(b, id) || !strings.Contains(b, "AsyncJob không truy vấn được bằng GraphQL") {
		t.Errorf("GraphQL bob: %s", b)
	}
	if _, b := putTo(h, "POST", "$graphql", gq, "X-User", "root"); !strings.Contains(b, id) {
		t.Errorf("GraphQL root phải thấy job: %s", b)
	}
}

// Binary do batch async tạo (đầu vào, đoạn kết quả tạm, kết quả cuối) mang chủ sở hữu của job; Guard ẩn chúng với người khác.
func TestAsyncBatchBinariesCarryTheJobOwner(t *testing.T) {
	repo := fhirrouter.NewMemoryRepository()
	o := ownerOpts()
	o.AsyncBatch, o.BatchCheckpointEntries = true, 2
	r := newRouter(t, repo, o)
	h := asUser(r.HTTPHandler())
	w := serve(h, httptestRequest("POST", "/", asyncBundle, "Prefer", "respond-async", "X-User", "alice"))
	if w.StatusCode != 202 {
		t.Fatalf("batch async: %d", w.StatusCode)
	}
	id := jobIDFrom(t, w)
	job, _ := waitJob(t, repo, id)
	resultRef := job.Output.Parameter[0].ValueReference.Reference

	g := fhirrouter.NewGuard(repo, o.JobAllowed)
	if _, err := g.ReadResource(as("alice"), "Binary", resultRef[len("Binary/"):]); err != nil {
		t.Errorf("chủ job đọc kết quả: %v", err)
	}
	if _, err := g.ReadResource(as("bob"), "Binary", resultRef[len("Binary/"):]); !isNotFound(err) {
		t.Errorf("người khác đọc kết quả batch async: %v, muốn 404", err)
	}
	sr, _ := fhirsearch.Parse("Binary", url.Values{})
	sr.Count = 100
	all, err := repo.Search(context.Background(), sr)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range all.Matches {
		if b, ok := m.(*fhir.Binary); ok && fhirrouter.BinaryOwner(b) != "alice" {
			t.Errorf("Binary %s của job không mang chủ: %+v", b.ID, b.SecurityContext)
		}
	}
	if len(all.Matches) == 0 {
		t.Error("không có Binary nào để kiểm")
	}
}

// Binary đầu vào của batch async (chứa cả Bundle gốc) có chủ ngay từ lúc job được nhận, khi job còn chờ.
func TestAsyncBatchInputBinaryHasOwnerWhileJobWaits(t *testing.T) {
	repo := fhirrouter.NewMemoryRepository()
	o := ownerOpts()
	o.AsyncBatch, o.DisableJobExecution = true, true
	h := asUser(newRouter(t, repo, o).HTTPHandler())
	if w := serve(h, httptestRequest("POST", "/", asyncBundle, "Prefer", "respond-async", "X-User", "alice")); w.StatusCode != 202 {
		t.Fatalf("batch async: %d", w.StatusCode)
	}
	sr, _ := fhirsearch.Parse("Binary", url.Values{})
	all, err := repo.Search(context.Background(), sr)
	if err != nil || len(all.Matches) != 1 {
		t.Fatalf("muốn đúng một Binary đầu vào: %v %v", all, err)
	}
	if got := fhirrouter.BinaryOwner(all.Matches[0].(*fhir.Binary)); got != "alice" {
		t.Errorf("Binary đầu vào có chủ %q, muốn alice", got)
	}
}

// $graph theo GraphDefinition do quản trị soạn không được dẫn tới AsyncJob của người khác: người không phải quản trị nhận 403, quản trị vẫn đi được.
func TestGraphCannotReachJobs(t *testing.T) {
	repo := fhirrouter.NewMemoryRepository()
	ctx := context.Background()
	for _, r := range []fhir.Resource{
		&fhir.Patient{ID: "p1"},
		&fhir.AsyncJob{ID: "j1", Status: "completed", Request: "export?_level=system"},
		&fhir.GraphDefinition{ID: "g-search", Name: "jobsearch", Start: "Patient", Status: "active", Link: []fhir.GraphDefinitionLink{{Target: []fhir.GraphDefinitionLinkTarget{{Type: "AsyncJob", Params: "_id={ref}"}}}}},
		&fhir.GraphDefinition{ID: "g-ok", Name: "patientonly", Start: "Patient", Status: "active", Link: []fhir.GraphDefinitionLink{{Target: []fhir.GraphDefinitionLinkTarget{{Type: "Observation", Params: "subject={ref}"}}}}},
		&fhir.GraphDefinition{ID: "g-mixed", Name: "mixed", Start: "Patient", Status: "active", Link: []fhir.GraphDefinitionLink{{Target: []fhir.GraphDefinitionLinkTarget{{Type: "Observation", Params: "subject={ref}"}, {Type: "AsyncJob", Params: "_id={ref}"}}}}},
		&fhir.GraphDefinition{ID: "g-path", Name: "jobpath", Start: "Patient", Status: "active", Link: []fhir.GraphDefinitionLink{{Path: "generalPractitioner", Target: []fhir.GraphDefinitionLinkTarget{{Type: "AsyncJob"}}}}},
	} {
		if _, err := repo.CreateResource(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	o := ownerOpts()
	o.SkipValidation = true
	h := asUser(newRouter(t, repo, o).HTTPHandler())
	for _, name := range []string{"jobsearch", "mixed", "jobpath"} {
		if c, b := putTo(h, "GET", "Patient/p1/$graph?graph="+name, "", "X-User", "bob"); c != 403 {
			t.Errorf("bob %s: %d %s, muốn 403", name, c, b)
		}
		if c, b := putTo(h, "GET", "Patient/p1/$graph?graph="+name, "", "X-User", "root"); c != 200 {
			t.Errorf("quản trị %s: %d %s, muốn 200", name, c, b)
		}
	}
	if c, b := putTo(h, "GET", "Patient/p1/$graph?graph=patientonly", "", "X-User", "bob"); c != 200 {
		t.Errorf("graph không dính AsyncJob: %d %s", c, b)
	}
}
