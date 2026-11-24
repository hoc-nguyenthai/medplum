package fhirrouter_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirrouter"
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
