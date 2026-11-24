package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"trivita-fhir/apps/dicomweb/internal/presentation/httpapi"
	"trivita-fhir/pkg/fhirauth"
)

// fakeVerifier ánh xạ chuỗi token sang Principal; "forbidden" giả lập token hợp lệ nhưng không dùng được cho FHIR, token khác là không hợp lệ.
type fakeVerifier map[string]*fhirauth.Principal

func (f fakeVerifier) Verify(_ context.Context, raw string) (*fhirauth.Principal, error) {
	if raw == "forbidden" {
		return nil, fhirauth.ErrForbidden
	}
	if p, ok := f[raw]; ok {
		return p, nil
	}
	return nil, context.Canceled
}

var tokens = fakeVerifier{
	"read-a":  {Subject: "r", Project: "A", Role: fhirauth.RoleRead},
	"write-a": {Subject: "w", Project: "A", Role: fhirauth.RoleWrite},
	"super":   {Subject: "s", Project: "home", Role: fhirauth.RoleAdmin, Super: true},
}

func newHandler(t *testing.T, mut ...func(*httpapi.Deps)) http.Handler {
	t.Helper()
	dep := httpapi.Deps{
		Verifier: tokens, MaxRequestBytes: 1 << 20,
		Ready: func(context.Context) (bool, map[string]bool) { return true, map[string]bool{"database": true} },
	}
	for _, m := range mut {
		m(&dep)
	}
	e, err := httpapi.New(dep)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func do(h http.Handler, method, path, token string, hdr ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(""))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func errorOf(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type của lỗi: %q", ct)
	}
	var b struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil || b.Error == "" {
		t.Fatalf("lỗi phải có dạng {\"error\": ...}: %q (%v)", rec.Body.String(), err)
	}
	return b.Error
}

func TestAuthenticationAndRoles(t *testing.T) {
	h := newHandler(t)
	cases := []struct {
		name, method, path, token string
		want                      int
	}{
		{"không token", "GET", "/dicomweb/studies", "", 401},
		{"token sai", "GET", "/dicomweb/studies", "garbage", 401},
		{"token đúng nhưng không dùng cho FHIR", "GET", "/dicomweb/studies", "forbidden", 403},
		{"đọc bằng token đọc: qua xác thực, chưa cài", "GET", "/dicomweb/studies", "read-a", 501},
		{"STOW bằng token đọc", "POST", "/dicomweb/studies", "read-a", 403},
		{"STOW bằng token ghi: qua xác thực, chưa cài", "POST", "/dicomweb/studies", "write-a", 501},
		{"STOW vào study cụ thể", "POST", "/dicomweb/studies/1.2.3", "write-a", 501},
		{"series", "GET", "/dicomweb/studies/1.2.3/series", "read-a", 501},
		{"metadata", "GET", "/dicomweb/studies/1.2.3/series/4.5/metadata", "read-a", 501},
		{"frame", "GET", "/dicomweb/studies/1.2.3/series/4.5/instances/6.7/frames/1", "read-a", 501},
		{"route chưa cài nào cũng 501, không phải 200 rỗng (H5)", "GET", "/dicomweb/studies/1.2.3/rendered", "read-a", 501},
		{"xóa", "DELETE", "/dicomweb/studies/1.2.3", "read-a", 403},
		{"route chưa cài vẫn cần xác thực", "GET", "/dicomweb/studies/1.2.3/rendered", "", 401},
		{"tiền tố /api/dicomweb", "GET", "/api/dicomweb/studies", "read-a", 501},
		{"ngoài DICOMweb", "GET", "/fhir/R4/Patient", "read-a", 404},
	}
	for _, c := range cases {
		rec := do(h, c.method, c.path, c.token)
		if rec.Code != c.want {
			t.Errorf("%s: %d, muốn %d (%s)", c.name, rec.Code, c.want, rec.Body.String())
			continue
		}
		if c.want >= 400 {
			errorOf(t, rec)
		}
	}
	if rec := do(h, "GET", "/dicomweb/studies", "garbage"); !strings.Contains(rec.Header().Get("WWW-Authenticate"), "invalid_token") {
		t.Errorf("WWW-Authenticate: %q", rec.Header().Get("WWW-Authenticate"))
	}
	if rec := do(h, "GET", "/dicomweb/studies", ""); !strings.Contains(rec.Header().Get("WWW-Authenticate"), "Bearer") {
		t.Errorf("thiếu WWW-Authenticate khi không có token: %q", rec.Header().Get("WWW-Authenticate"))
	}
}

// Tiền tố có project phải khớp project của token; header X-Fhir-Project không được chọn project khác (token thường).
func TestProjectPrefixMustMatchToken(t *testing.T) {
	h := newHandler(t)
	for _, prefix := range []string{"/projects/A/dicomweb", "/api/projects/A/dicomweb"} {
		if rec := do(h, "GET", prefix+"/studies", "read-a"); rec.Code != 501 {
			t.Errorf("%s đúng project: %d", prefix, rec.Code)
		}
	}
	for _, prefix := range []string{"/projects/B/dicomweb", "/api/projects/B/dicomweb"} {
		rec := do(h, "GET", prefix+"/studies", "read-a")
		if rec.Code != 403 || !strings.Contains(errorOf(t, rec), "project khác") {
			t.Errorf("%s project khác phải 403: %d %s", prefix, rec.Code, rec.Body.String())
		}
	}
	if rec := do(h, "GET", "/dicomweb/studies", "read-a", "X-Fhir-Project", "B"); rec.Code != 403 {
		t.Errorf("header chọn project khác: %d", rec.Code)
	}
	if rec := do(h, "GET", "/dicomweb/studies", "read-a", "X-Fhir-Project", "A"); rec.Code != 501 {
		t.Errorf("header trùng project: %d", rec.Code)
	}
}

func TestSuperAdminActsInChosenProject(t *testing.T) {
	h := newHandler(t)
	if rec := do(h, "GET", "/dicomweb/studies", "super"); rec.Code != 400 {
		t.Errorf("super-admin không chọn project phải 400, được %d", rec.Code)
	}
	if rec := do(h, "GET", "/dicomweb/studies", "super", "X-Fhir-Project", "bad project!"); rec.Code != 400 {
		t.Errorf("project không hợp lệ phải 400, được %d", rec.Code)
	}
	if rec := do(h, "GET", "/dicomweb/studies", "super", "X-Fhir-Project", "B"); rec.Code != 501 {
		t.Errorf("super-admin chọn bằng header: %d", rec.Code)
	}
	if rec := do(h, "GET", "/projects/B/dicomweb/studies", "super"); rec.Code != 501 {
		t.Errorf("super-admin chọn bằng URL: %d", rec.Code)
	}
	if rec := do(h, "GET", "/projects/B/dicomweb/studies", "super", "X-Fhir-Project", "C"); rec.Code != 400 {
		t.Errorf("URL và header khác nhau phải 400: %d", rec.Code)
	}
}

func TestHealthProbes(t *testing.T) {
	ready := true
	h := newHandler(t, func(d *httpapi.Deps) {
		d.Ready = func(context.Context) (bool, map[string]bool) { return ready, map[string]bool{"database": ready} }
	})
	for _, p := range []string{"/healthz", "/health/live", "/health/startup", "/health/ready"} {
		if rec := do(h, "GET", p, ""); rec.Code != 200 {
			t.Errorf("%s: %d", p, rec.Code)
		}
	}
	ready = false
	if rec := do(h, "GET", "/health/ready", ""); rec.Code != 503 || !strings.Contains(rec.Body.String(), `"ready":false`) {
		t.Errorf("ready khi không sẵn sàng: %d %s", rec.Code, rec.Body.String())
	}
	for _, p := range []string{"/healthz", "/health/live", "/health/startup"} { // liveness và startup không phụ thuộc phụ thuộc ngoài
		if rec := do(h, "GET", p, ""); rec.Code != 200 {
			t.Errorf("%s khi phụ thuộc lỗi: %d", p, rec.Code)
		}
	}
	if rec := do(h, "GET", "/swagger/doc.json", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), "Trivita DICOMweb API") {
		t.Errorf("swagger: %d", rec.Code)
	}
}

func TestBodyLimitAndRateLimit(t *testing.T) {
	h := newHandler(t, func(d *httpapi.Deps) { d.MaxRequestBytes = 10; d.RateLimit = 1000 })
	req := httptest.NewRequest("POST", "/dicomweb/studies", strings.NewReader(strings.Repeat("x", 11)))
	req.Header.Set("Authorization", "Bearer write-a")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 413 {
		t.Errorf("body vượt trần: %d %s", rec.Code, rec.Body.String())
	}
	errorOf(t, rec)

	limited := newHandler(t, func(d *httpapi.Deps) { d.RateLimit, d.RateBurst = 1, 2 })
	var last int
	for i := 0; i < 10; i++ {
		last = do(limited, "GET", "/dicomweb/studies", "read-a").Code
	}
	if last != 429 {
		t.Errorf("hạn mức theo IP: lần cuối %d, muốn 429", last)
	}
	if rec := do(limited, "GET", "/health/live", ""); rec.Code != 200 {
		t.Errorf("health không bị hạn mức: %d", rec.Code)
	}
}

func TestServerErrorsDoNotLeakDetails(t *testing.T) {
	h := newHandler(t, func(d *httpapi.Deps) { d.Verifier = panicVerifier{} })
	rec := do(h, "GET", "/dicomweb/studies", "x")
	if rec.Code != 500 || strings.Contains(rec.Body.String(), "bí mật nội bộ") {
		t.Errorf("lỗi 5xx: %d %s", rec.Code, rec.Body.String())
	}
	if e := errorOf(t, rec); e != "Internal Server Error" {
		t.Errorf("thông điệp: %q", e)
	}
}

type panicVerifier struct{}

func (panicVerifier) Verify(context.Context, string) (*fhirauth.Principal, error) {
	panic("bí mật nội bộ")
}
