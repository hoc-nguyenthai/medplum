package httpapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"

	"trivita-fhir/apps/fhir-server/internal/presentation/httpapi"
	"trivita-fhir/pkg/fhirauth"
)

func TestRequiredRole(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		want         httpapi.Role
	}{
		{"GET", "/Patient/1", httpapi.RoleRead},
		{"POST", "/CodeSystem/$import", httpapi.RoleAdmin},
		{"POST", "/CodeSystem/cs/$import", httpapi.RoleAdmin},
		{"POST", "/ConceptMap/cm/$import/", httpapi.RoleAdmin},
		{"POST", "/PackageRelease/pr/$install", httpapi.RoleAdmin},
		{"POST", "/AsyncJob", httpapi.RoleAdmin},
		{"POST", "/fhir/R4/AsyncJob", httpapi.RoleAdmin},
		{"PUT", "/AsyncJob/j", httpapi.RoleAdmin},
		{"PUT", "/fhir/R4/AsyncJob", httpapi.RoleAdmin},
		{"PATCH", "/AsyncJob/j", httpapi.RoleAdmin},
		{"DELETE", "/fhir/R4/AsyncJob/j", httpapi.RoleAdmin},
		{"GET", "/AsyncJob/j", httpapi.RoleAdmin},
		{"GET", "/fhir/R4/AsyncJob", httpapi.RoleAdmin},
		{"POST", "/fhir/R4/AsyncJob/_search", httpapi.RoleAdmin},
		{"GET", "/job/j/status", httpapi.RoleRead},
		{"POST", "/AsyncJobs", httpapi.RoleWrite},
		{"POST", "/Patient/AsyncJob", httpapi.RoleWrite},
		{"HEAD", "/Patient", httpapi.RoleRead},
		{"OPTIONS", "/", httpapi.RoleRead},
		{"POST", "/Patient/_search", httpapi.RoleRead},
		{"POST", "/Patient/_search/", httpapi.RoleRead},
		{"POST", "/Patient", httpapi.RoleWrite},
		{"POST", "/", httpapi.RoleWrite}, // batch/transaction có thể chứa thao tác ghi
		{"PUT", "/Patient/1", httpapi.RoleWrite},
		{"PATCH", "/Patient/1", httpapi.RoleWrite},
		{"DELETE", "/Patient/1", httpapi.RoleWrite},
		// operation: $validate và $everything không ghi nên chỉ cần quyền đọc; $reindex và $expunge là quản trị
		{"POST", "/Patient/$validate", httpapi.RoleRead},
		{"POST", "/Patient/1/$everything", httpapi.RoleRead},
		{"GET", "/Patient/1/$everything", httpapi.RoleRead},
		{"GET", "/job/abc/status", httpapi.RoleRead},
		{"DELETE", "/job/abc/status", httpapi.RoleWrite}, // hủy batch async: cùng quyền với lúc gửi batch
		// bulk export và terminology chỉ đọc (kể cả POST)
		{"GET", "/$export", httpapi.RoleRead},
		{"GET", "/Patient/$export", httpapi.RoleRead},
		{"GET", "/bulkdata/abc/status", httpapi.RoleRead},
		{"DELETE", "/bulkdata/abc/status", httpapi.RoleRead},
		{"POST", "/ValueSet/$expand", httpapi.RoleRead},
		{"POST", "/CodeSystem/$lookup", httpapi.RoleRead},
		{"POST", "/ValueSet/1/$validate-code", httpapi.RoleRead},
		{"POST", "/CodeSystem/$subsumes", httpapi.RoleRead},
		{"POST", "/$graphql", httpapi.RoleRead},
		{"POST", "/Subscription/1/$get-ws-binding-token", httpapi.RoleRead},
		{"POST", "/ConceptMap/1/$translate", httpapi.RoleRead},
		{"GET", "/Group/1/$export", httpapi.RoleRead},
		{"POST", "/Patient/$reindex", httpapi.RoleAdmin},
		{"POST", "/Patient/1/$reindex", httpapi.RoleAdmin},
		{"POST", "/Patient/1/$expunge", httpapi.RoleAdmin},
		{"GET", "/Patient/1/$expunge", httpapi.RoleAdmin}, // dù dùng method nào
	} {
		if got := httpapi.RequiredRole(tc.method, tc.path); got != tc.want {
			t.Errorf("%s %s: muốn %v, được %v", tc.method, tc.path, tc.want, got)
		}
	}
}

func TestAuthenticateAndGuard(t *testing.T) {
	a := httpapi.NewAuthenticator(
		[]httpapi.Token{{Name: "viewer", Secret: "read-secret-0123456789abcdef"}},
		[]httpapi.Token{{Name: "ops", Secret: "write-secret-0123456789abcdef"}},
		[]httpapi.Token{{Name: "root", Secret: "admin-secret-0123456789abcdef"}}, false)
	if n, r := a.Authenticate("read-secret-0123456789abcdef"); n != "viewer" || r != httpapi.RoleRead {
		t.Fatalf("read: %s %v", n, r)
	}
	if n, r := a.Authenticate("write-secret-0123456789abcdef"); n != "ops" || r != httpapi.RoleWrite {
		t.Fatalf("write: %s %v", n, r)
	}
	if n, r := a.Authenticate("admin-secret-0123456789abcdef"); n != "root" || r != httpapi.RoleAdmin {
		t.Fatalf("admin: %s %v", n, r)
	}
	for _, bad := range []string{"", "nope", "read-secret-0123456789abcdef ", "READ-SECRET-0123456789ABCDEF"} {
		if _, r := a.Authenticate(bad); r != httpapi.RoleNone {
			t.Errorf("%q phải bị từ chối", bad)
		}
	}

	e := echo.New()
	e.Use(a.Guard())
	ok := func(c echo.Context) error { return c.String(200, "ok") }
	e.Any("/*", ok)
	do := func(method, path, auth string) int {
		req := httptest.NewRequest(method, path, nil)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		return rec.Code
	}
	cases := []struct {
		method, path, auth string
		want               int
	}{
		{"GET", "/fhir/R4/metadata", "", 200},  // công khai
		{"GET", "/fhir/R4/metadata/", "", 200}, // dấu / cuối
		{"POST", "/fhir/R4/metadata", "", 401}, // chỉ GET công khai
		{"GET", "/fhir/R4/Patient", "", 401},   // thiếu token
		{"GET", "/fhir/R4/Patient", "Bearer nope", 401},
		{"GET", "/fhir/R4/Patient", "Basic abc", 401},
		{"GET", "/fhir/R4/Patient", "bearer read-secret-0123456789abcdef", 200}, // scheme không phân biệt hoa thường
		{"POST", "/fhir/R4/Patient", "Bearer read-secret-0123456789abcdef", 403},
		{"POST", "/fhir/R4/Patient/_search", "Bearer read-secret-0123456789abcdef", 200},
		{"POST", "/fhir/R4", "Bearer read-secret-0123456789abcdef", 403},
		{"POST", "/fhir/R4/Patient/$validate", "Bearer read-secret-0123456789abcdef", 200},
		{"POST", "/fhir/R4/Patient/$reindex", "Bearer write-secret-0123456789abcdef", 403}, // ghi chưa đủ quyền quản trị
		{"POST", "/fhir/R4/Patient/1/$expunge", "Bearer write-secret-0123456789abcdef", 403},
		{"POST", "/fhir/R4/Patient/$reindex", "Bearer admin-secret-0123456789abcdef", 200},
		{"POST", "/fhir/R4/Patient", "Bearer admin-secret-0123456789abcdef", 200}, // admin gồm cả quyền ghi
		{"PUT", "/fhir/R4/Patient/1", "Bearer write-secret-0123456789abcdef", 200},
		{"DELETE", "/fhir/R4/Patient/1", "Bearer write-secret-0123456789abcdef", 200},
	}
	for _, tc := range cases {
		// Guard trả lỗi HTTPError; Echo mặc định chuyển thành mã tương ứng
		if got := do(tc.method, tc.path, tc.auth); got != tc.want {
			t.Errorf("%s %s [%s]: muốn %d, được %d", tc.method, tc.path, tc.auth, tc.want, got)
		}
	}

	off := echo.New()
	off.Use(httpapi.NewAuthenticator(nil, nil, nil, true).Guard())
	off.Any("/*", ok)
	rec := httptest.NewRecorder()
	off.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/fhir/R4/Patient/1", nil))
	if rec.Code != 200 {
		t.Errorf("disabled: %d", rec.Code)
	}
}

// Token tĩnh: Guard gắn Principal mang vai trò (không project) để thành phần sau cổng biết người gọi được ghi hay không.
func TestStaticTokenGuardAttachesRolePrincipal(t *testing.T) {
	a := httpapi.NewAuthenticator([]httpapi.Token{{Name: "viewer", Secret: "read-secret-0123456789abcdef"}}, []httpapi.Token{{Name: "ops", Secret: "write-secret-0123456789abcdef"}}, nil, false)
	e := echo.New()
	var got *fhirauth.Principal
	h := a.Guard()(func(c echo.Context) error {
		got, _ = fhirauth.PrincipalFrom(c.Request().Context())
		return c.NoContent(200)
	})
	for secret, want := range map[string]struct {
		name string
		role httpapi.Role
	}{"read-secret-0123456789abcdef": {"viewer", httpapi.RoleRead}, "write-secret-0123456789abcdef": {"ops", httpapi.RoleWrite}} {
		got = nil
		req := httptest.NewRequest("GET", httpapi.Prefix+"/Patient", nil)
		req.Header.Set("Authorization", "Bearer "+secret)
		if err := h(e.NewContext(req, httptest.NewRecorder())); err != nil {
			t.Fatal(err)
		}
		if got == nil || got.Subject != want.name || got.Role != want.role || got.Project != "" {
			t.Fatalf("%s: principal = %+v", want.name, got)
		}
	}
	// kiểm trên Principal của token ghi (thứ tự duyệt map ngẫu nhiên nên `got` ở trên có thể là của token đọc)
	req := httptest.NewRequest("GET", httpapi.Prefix+"/Patient", nil)
	req.Header.Set("Authorization", "Bearer write-secret-0123456789abcdef")
	if err := h(e.NewContext(req, httptest.NewRecorder())); err != nil {
		t.Fatal(err)
	}
	if fhirauth.CanUpdate(fhirauth.WithPrincipal(context.Background(), got), "Binary") != true {
		t.Fatal("token ghi phải được cập nhật")
	}
	got = nil
	req = httptest.NewRequest("GET", httpapi.Prefix+"/Patient", nil)
	req.Header.Set("Authorization", "Bearer read-secret-0123456789abcdef")
	if err := h(e.NewContext(req, httptest.NewRecorder())); err != nil {
		t.Fatal(err)
	}
	if fhirauth.CanUpdate(fhirauth.WithPrincipal(context.Background(), got), "Binary") {
		t.Fatal("token đọc không được cập nhật")
	}
}

// AuthorizeEntry dùng đúng RequiredRole của cổng HTTP cho entry của Bundle: token write không qua được $reindex/$expunge, kể cả khi viết
// đường dẫn theo nhiều cách; không có người gọi (xác thực tắt) thì cho qua như Guard.
func TestAuthorizeEntryAppliesRequiredRole(t *testing.T) {
	ctxOf := func(r httpapi.Role) context.Context {
		return fhirauth.WithPrincipal(context.Background(), &fhirauth.Principal{Subject: "u", Role: r})
	}
	cases := []struct {
		role         httpapi.Role
		method, path string
		ok           bool
	}{
		{httpapi.RoleWrite, "POST", "Patient/$reindex", false},
		{httpapi.RoleWrite, "POST", "/Patient/$reindex", false},
		{httpapi.RoleWrite, "POST", "Patient/$reindex/", false},
		{httpapi.RoleWrite, "POST", "Patient/p1/$reindex", false},
		{httpapi.RoleWrite, "POST", "Patient/p1/$expunge", false},
		{httpapi.RoleRead, "POST", "Patient", false},
		{httpapi.RoleRead, "PUT", "Patient/p1", false},
		{httpapi.RoleRead, "DELETE", "Patient/p1", false},
		{httpapi.RoleRead, "GET", "Patient/p1", true},
		{httpapi.RoleRead, "POST", "Patient/_search", true},
		{httpapi.RoleRead, "POST", "Patient/$validate", true},
		{httpapi.RoleWrite, "POST", "Patient", true},
		{httpapi.RoleWrite, "PUT", "Patient/p1", true},
		{httpapi.RoleAdmin, "POST", "Patient/$reindex", true},
		{httpapi.RoleAdmin, "POST", "Patient/p1/$expunge", true},
	}
	for _, c := range cases {
		err := httpapi.AuthorizeEntry(ctxOf(c.role), c.method, c.path)
		if (err == nil) != c.ok {
			t.Errorf("role=%v %s %s: err=%v, muốn ok=%v", c.role, c.method, c.path, err, c.ok)
		}
	}
	if err := httpapi.AuthorizeEntry(context.Background(), "POST", "Patient/$reindex"); err != nil {
		t.Errorf("không có người gọi (xác thực tắt) phải cho qua: %v", err)
	}
}

func TestJobOwnerAndAccess(t *testing.T) {
	as := func(sub string, role httpapi.Role, super bool) context.Context {
		return fhirauth.WithPrincipal(context.Background(), &fhirauth.Principal{Subject: sub, Role: role, Super: super})
	}
	if got := httpapi.JobOwner(as("alice", httpapi.RoleRead, false)); got != "alice" {
		t.Errorf("JobOwner = %q", got)
	}
	if got := httpapi.JobOwner(context.Background()); got != "" {
		t.Errorf("không có người gọi thì không có chủ: %q", got)
	}
	cases := []struct {
		name  string
		ctx   context.Context
		owner string
		ok    bool
	}{
		{"chủ sở hữu", as("alice", httpapi.RoleRead, false), "alice", true},
		{"người khác", as("bob", httpapi.RoleRead, false), "alice", false},
		{"người khác có quyền write", as("bob", httpapi.RoleWrite, false), "alice", false},
		{"admin", as("root", httpapi.RoleAdmin, false), "alice", true},
		{"super-admin", as("su", httpapi.RoleRead, true), "alice", true},
		{"job không chủ, người thường", as("bob", httpapi.RoleWrite, false), "", false},
		{"job không chủ, subject rỗng", as("", httpapi.RoleRead, false), "", false},
		{"job không chủ, admin", as("root", httpapi.RoleAdmin, false), "", true},
		{"xác thực tắt", context.Background(), "alice", true},
	}
	for _, c := range cases {
		if got := httpapi.JobAllowed(c.ctx, c.owner); got != c.ok {
			t.Errorf("%s: JobAllowed = %v, muốn %v", c.name, got, c.ok)
		}
	}
}
