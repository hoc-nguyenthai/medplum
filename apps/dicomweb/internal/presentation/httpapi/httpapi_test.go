package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
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

// ---------------------------------------------------------------- STOW-RS (D3)

type fakeStower struct {
	got httpapi.StowRequest
	res httpapi.StowResult
	err error
	n   int
}

func (f *fakeStower) Stow(_ context.Context, req httpapi.StowRequest) (httpapi.StowResult, error) {
	f.n++
	b, _ := io.ReadAll(req.Body)
	req.Body = strings.NewReader(string(b))
	f.got = req
	return f.res, f.err
}

func post(h http.Handler, path, token, ct, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	req.Header.Set("Content-Type", ct)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestStowRouteDispatch(t *testing.T) {
	f := &fakeStower{res: httpapi.StowResult{Status: 202, Body: map[string]any{"00081190": map[string]any{"vr": "UR", "Value": []string{"x"}}}}}
	h := newHandler(t, func(d *httpapi.Deps) { d.Stow = f; d.BaseURL = "https://dicom.example.org/" })
	const ct = `multipart/related; type="application/dicom"; boundary=b`

	rec := post(h, "/dicomweb/studies", "write-a", ct, "BODY")
	if rec.Code != 202 || rec.Header().Get("Content-Type") != "application/dicom+json" || !strings.Contains(rec.Body.String(), `"00081190"`) {
		t.Fatalf("%d %q %s", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
	}
	if f.got.ContentType != ct || f.got.StudyUID != "" || f.got.RetrieveBase != "https://dicom.example.org/dicomweb" {
		t.Errorf("yêu cầu: %+v", f.got)
	}
	if b, _ := io.ReadAll(f.got.Body); string(b) != "BODY" {
		t.Errorf("thân: %q", b)
	}
	// vào study cụ thể, và RetrieveBase theo đúng tiền tố đang dùng (kể cả tiền tố có project)
	for path, base := range map[string]string{
		"/dicomweb/studies/1.2.3":                "https://dicom.example.org/dicomweb",
		"/api/dicomweb/studies/1.2.3":            "https://dicom.example.org/api/dicomweb",
		"/projects/A/dicomweb/studies/1.2.3":     "https://dicom.example.org/projects/A/dicomweb",
		"/api/projects/A/dicomweb/studies/1.2.3": "https://dicom.example.org/api/projects/A/dicomweb",
	} {
		if rec := post(h, path, "write-a", ct, ""); rec.Code != 202 {
			t.Fatalf("%s: %d", path, rec.Code)
		}
		if f.got.StudyUID != "1.2.3" || f.got.RetrieveBase != base {
			t.Errorf("%s: %+v", path, f.got)
		}
	}
}

// Xác thực và quyền đứng TRƯỚC STOW: yêu cầu bị từ chối không bao giờ chạm tới dịch vụ.
func TestStowIsGuarded(t *testing.T) {
	f := &fakeStower{res: httpapi.StowResult{Status: 200, Body: map[string]any{}}}
	h := newHandler(t, func(d *httpapi.Deps) { d.Stow = f })
	const ct = "multipart/related; boundary=b"
	for name, c := range map[string]struct {
		path, token string
		want        int
	}{
		"không token": {"/dicomweb/studies", "", 401}, "token sai": {"/dicomweb/studies", "x", 401}, "token chỉ đọc": {"/dicomweb/studies", "read-a", 403},
		"project khác trong URL": {"/projects/B/dicomweb/studies", "write-a", 403},
	} {
		if rec := post(h, c.path, c.token, ct, "x"); rec.Code != c.want {
			t.Errorf("%s: %d, muốn %d", name, rec.Code, c.want)
		}
	}
	if f.n != 0 {
		t.Errorf("dịch vụ STOW bị gọi %d lần dù yêu cầu bị từ chối", f.n)
	}
}

func TestStowErrorsAreJSON(t *testing.T) {
	for name, c := range map[string]struct {
		err  error
		want int
	}{
		"415": {&httpapi.StatusError{Status: 415, Msg: "Cần Content-Type multipart/related"}, 415},
		"403": {&httpapi.StatusError{Status: 403, Msg: "Không đủ quyền"}, 403},
		"413": {&http.MaxBytesError{Limit: 1}, 413},
		"500": {errors.New("chi tiết nội bộ bí mật"), 500},
	} {
		h := newHandler(t, func(d *httpapi.Deps) { d.Stow = &fakeStower{err: c.err} })
		rec := post(h, "/dicomweb/studies", "write-a", "multipart/related; boundary=b", "x")
		if rec.Code != c.want {
			t.Errorf("%s: %d", name, rec.Code)
			continue
		}
		msg := errorOf(t, rec)
		if strings.Contains(msg, "bí mật") {
			t.Errorf("%s: lộ chi tiết nội bộ: %q", name, msg)
		}
	}
}

// Không có dịch vụ STOW (cấu hình chưa lắp) thì route vẫn 501 sau xác thực, không panic.
func TestStowWithoutServiceIs501(t *testing.T) {
	if rec := post(newHandler(t), "/dicomweb/studies", "write-a", "multipart/related; boundary=b", "x"); rec.Code != 501 {
		t.Errorf("%d", rec.Code)
	}
}

// ---------------------------------------------------------------- QIDO-RS và WADO-RS (D4)

type fakeRetriever struct {
	page        httpapi.QueryPage
	err         error
	meta        func(io.Writer) error
	frames      httpapi.FrameStream
	dl          httpapi.Download
	rendered    httpapi.Rendered
	lastLevel   string
	lastScope   string
	lastParams  url.Values
	calls       []string
	lastLimit   string
	lastOffset  string
	lastStudy   string
	lastFrames  string
	lastInstUID string
}

func (f *fakeRetriever) Search(_ context.Context, level, study, series string, params url.Values) (httpapi.QueryPage, error) {
	f.calls = append(f.calls, level)
	f.lastLevel, f.lastScope, f.lastParams = level, study+"|"+series, params
	f.lastLimit, f.lastOffset, f.lastStudy = params.Get("limit"), params.Get("offset"), study
	return f.page, f.err
}

func (f *fakeRetriever) Metadata(_ context.Context, study, series, inst string) (func(io.Writer) error, error) {
	f.calls = append(f.calls, "metadata")
	f.lastStudy = study + "|" + series
	f.lastInstUID = inst
	return f.meta, f.err
}

func (f *fakeRetriever) Download(_ context.Context, study, series, inst string) (httpapi.Download, error) {
	f.calls = append(f.calls, "download")
	f.lastScope = study + "|" + series + "|" + inst
	return f.dl, f.err
}

func (f *fakeRetriever) Rendered(_ context.Context, study, series, inst, frame, accept string, params url.Values) (httpapi.Rendered, error) {
	f.calls = append(f.calls, "rendered")
	f.lastScope = study + "|" + series + "|" + inst + "|" + frame + "|" + accept + "|" + params.Get("window")
	return f.rendered, f.err
}

func (f *fakeRetriever) InstanceFile(_ context.Context, study, series, inst string) (io.ReadCloser, error) {
	f.calls = append(f.calls, "file")
	f.lastScope = study + "|" + series + "|" + inst
	return io.NopCloser(strings.NewReader("DICMDATA")), f.err
}

func (f *fakeRetriever) Bulkdata(_ context.Context, study, series, inst, tag string) (httpapi.FrameStream, error) {
	f.calls = append(f.calls, "bulkdata")
	f.lastScope = study + "|" + series + "|" + inst + "|" + tag
	return f.frames, f.err
}

func (f *fakeRetriever) Frames(_ context.Context, study, series, inst, frames string) (httpapi.FrameStream, error) {
	f.calls = append(f.calls, "frames")
	f.lastStudy, f.lastInstUID, f.lastFrames = study+"|"+series, inst, frames
	return f.frames, f.err
}

func TestQidoRoutes(t *testing.T) {
	f := &fakeRetriever{page: httpapi.QueryPage{Items: []map[string]any{{"0020000D": map[string]any{"vr": "UI", "Value": []string{"1.2"}}}}, More: true}}
	h := newHandler(t, func(d *httpapi.Deps) { d.Retrieve = f })
	rec := do(h, "GET", "/dicomweb/studies?limit=5&offset=10&PatientName=ignored", "read-a")
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/dicom+json" || !strings.Contains(rec.Body.String(), `"0020000D"`) {
		t.Fatalf("%d %q %s", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
	}
	if f.lastLimit != "5" || f.lastOffset != "10" {
		t.Errorf("phân trang: %q %q", f.lastLimit, f.lastOffset)
	}
	if w := rec.Header().Get("Warning"); !strings.HasPrefix(w, "299 ") {
		t.Errorf("Warning khi còn kết quả: %q", w)
	}
	f.page.More = false
	if rec := do(h, "GET", "/api/projects/A/dicomweb/studies/1.2.3/series", "read-a"); rec.Code != 200 || rec.Header().Get("Warning") != "" || f.lastStudy != "1.2.3" {
		t.Errorf("series: %d %q study=%q", rec.Code, rec.Header().Get("Warning"), f.lastStudy)
	}
	// kết quả rỗng là mảng rỗng
	f.page = httpapi.QueryPage{Items: []map[string]any{}}
	if rec := do(h, "GET", "/dicomweb/studies", "read-a"); rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Errorf("rỗng: %d %q", rec.Code, rec.Body.String())
	}
}

func TestQidoInstanceRoutesAndWarnings(t *testing.T) {
	f := &fakeRetriever{page: httpapi.QueryPage{Items: []map[string]any{}, Ignored: []string{"Foo"}}}
	h := newHandler(t, func(d *httpapi.Deps) { d.Retrieve = f })
	for path, want := range map[string]string{
		"/dicomweb/series":                          "series||",
		"/dicomweb/instances":                       "instance||",
		"/dicomweb/studies/S/instances":             "instance|S|",
		"/dicomweb/studies/S/series/R/instances":    "instance|S|R",
		"/api/projects/A/dicomweb/studies/S/series": "series|S|",
	} {
		rec := do(h, "GET", path+"?Foo=1&Modality=CT", "read-a")
		if rec.Code != 200 {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Body.String())
		}
		if got := f.lastLevel + "|" + f.lastScope; got != want {
			t.Errorf("%s: %q, muốn %q", path, got, want)
		}
		if f.lastParams.Get("Modality") != "CT" {
			t.Errorf("%s: tham số không chuyển xuống: %v", path, f.lastParams)
		}
		if w := rec.Header().Get("Warning"); !strings.Contains(w, "Foo") {
			t.Errorf("%s: thiếu Warning cho tham số bị bỏ qua: %q", path, w)
		}
	}
}

func TestMetadataAndDownloadRoutes(t *testing.T) {
	f := &fakeRetriever{
		meta: func(w io.Writer) error { _, err := io.WriteString(w, `[{}]`); return err },
		dl:   httpapi.Download{ContentType: `multipart/related; type="application/dicom"; boundary=b`, Write: func(w io.Writer) error { _, err := io.WriteString(w, "--b--\r\n"); return err }},
	}
	h := newHandler(t, func(d *httpapi.Deps) { d.Retrieve = f })
	for path, want := range map[string]string{
		"/dicomweb/studies/S/metadata":                      "S|",
		"/dicomweb/studies/S/series/R/instances/I/metadata": "S|R",
	} {
		if rec := do(h, "GET", path, "read-a"); rec.Code != 200 || rec.Body.String() != "[{}]" || f.lastStudy != want {
			t.Errorf("%s: %d %q %q", path, rec.Code, rec.Body.String(), f.lastStudy)
		}
	}
	for path, want := range map[string]string{
		"/dicomweb/studies/S":                      "S||",
		"/dicomweb/studies/S/series/R":             "S|R|",
		"/dicomweb/studies/S/series/R/instances/I": "S|R|I",
	} {
		rec := do(h, "GET", path, "read-a")
		if rec.Code != 200 || rec.Header().Get("Content-Type") != f.dl.ContentType || rec.Header().Get("Content-Length") != "" || f.lastScope != want {
			t.Errorf("%s: %d %q %q scope=%q", path, rec.Code, rec.Header().Get("Content-Type"), rec.Header().Get("Content-Length"), f.lastScope)
		}
	}
	// lỗi trước khi ghi: 404 JSON
	f.err = &httpapi.StatusError{Status: 404, Msg: "Instance not found"}
	if rec := do(h, "GET", "/dicomweb/studies/S/series/R/instances/I", "read-a"); rec.Code != 404 {
		t.Errorf("404: %d", rec.Code)
	}
}

func TestRenderedRoutes(t *testing.T) {
	f := &fakeRetriever{rendered: httpapi.Rendered{ContentType: "image/png", Body: []byte("PNG")}}
	h := newHandler(t, func(d *httpapi.Deps) { d.Retrieve = f })
	for path, want := range map[string]string{
		"/dicomweb/studies/S/series/R/instances/I/rendered?window=1,2":          "S|R|I||image/png|1,2",
		"/dicomweb/studies/S/series/R/instances/I/frames/3/rendered?window=1,2": "S|R|I|3|image/png|1,2",
	} {
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("Authorization", "Bearer read-a")
		req.Header.Set("Accept", "image/png")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/png" || rec.Body.String() != "PNG" || f.lastScope != want {
			t.Errorf("%s: %d %q %q scope=%q", path, rec.Code, rec.Header().Get("Content-Type"), rec.Body.String(), f.lastScope)
		}
	}
	f.err = &httpapi.StatusError{Status: 406, Msg: "x"}
	if rec := do(h, "GET", "/dicomweb/studies/S/series/R/instances/I/rendered", "read-a"); rec.Code != 406 {
		t.Errorf("406: %d", rec.Code)
	}
}

func TestWadoURIAndBulkdataRoutes(t *testing.T) {
	f := &fakeRetriever{rendered: httpapi.Rendered{ContentType: "image/jpeg", Body: []byte("JPG")},
		frames: httpapi.FrameStream{ContentType: "multipart/related; type=\"application/octet-stream\"; boundary=b", Length: 5, Write: func(w io.Writer) error { _, err := io.WriteString(w, "12345"); return err }}}
	h := newHandler(t, func(d *httpapi.Deps) { d.Retrieve = f })
	base := "/dicomweb/wado?requestType=WADO&studyUID=S&seriesUID=R&objectUID=I"
	for _, c := range []struct {
		name, query, accept string
		want                int
		wantType, wantBody  string
		wantScope           string
	}{
		{"mặc định là JPEG", "", "", 200, "image/jpeg", "JPG", "S|R|I||image/jpeg|"},
		{"PNG kèm frame và window", "&contentType=image/png&frameNumber=2&windowCenter=40&windowWidth=80", "", 200, "image/jpeg", "JPG", "S|R|I|2|image/png|40,80"},
		{"tệp DICOM", "&contentType=application/dicom", "", 200, "application/dicom", "DICMDATA", "S|R|I"},
		{"danh sách có q: dicom thắng", "&contentType=image/jpeg%3Bq=0.3,application/dicom", "", 200, "application/dicom", "DICMDATA", "S|R|I"},
		{"Accept khi không có contentType", "", "application/dicom", 200, "application/dicom", "DICMDATA", "S|R|I"},
		{"ký tự đại diện", "&contentType=*/*", "", 200, "image/jpeg", "JPG", "S|R|I||image/jpeg|"},
		{"contentType không hỗ trợ", "&contentType=application/pdf", "", 406, "", "", ""},
		{"anonymize", "&anonymize=yes", "", 400, "", "", ""},
		{"anonymize=no được", "&anonymize=no", "", 200, "image/jpeg", "JPG", ""},
	} {
		req := httptest.NewRequest("GET", base+c.query, nil)
		req.Header.Set("Authorization", "Bearer read-a")
		if c.accept != "" {
			req.Header.Set("Accept", c.accept)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != c.want || (c.want == 200 && (rec.Header().Get("Content-Type") != c.wantType || rec.Body.String() != c.wantBody)) {
			t.Errorf("%s: %d %q %q", c.name, rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
			continue
		}
		if c.wantScope != "" && f.lastScope != c.wantScope {
			t.Errorf("%s: scope %q, muốn %q", c.name, f.lastScope, c.wantScope)
		}
	}
	for name, path := range map[string]string{
		"thiếu requestType": "/dicomweb/wado?studyUID=S&seriesUID=R&objectUID=I",
		"requestType sai":   "/dicomweb/wado?requestType=X&studyUID=S&seriesUID=R&objectUID=I",
		"thiếu objectUID":   "/dicomweb/wado?requestType=WADO&studyUID=S&seriesUID=R",
	} {
		if rec := do(h, "GET", path, "read-a"); rec.Code != 400 {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
	if rec := do(h, "GET", "/dicomweb/wado?requestType=wado&studyUID=S&seriesUID=R&objectUID=I", "read-a"); rec.Code != 200 {
		t.Errorf("requestType không phân biệt hoa thường: %d", rec.Code)
	}
	// bulkdata
	rec := do(h, "GET", "/dicomweb/studies/S/series/R/instances/I/bulkdata/7FE00010", "read-a")
	if rec.Code != 200 || rec.Body.String() != "12345" || rec.Header().Get("Content-Length") != "5" || f.lastScope != "S|R|I|7FE00010" {
		t.Errorf("bulkdata: %d %q %q scope=%q", rec.Code, rec.Body.String(), rec.Header().Get("Content-Length"), f.lastScope)
	}
	// lỗi, 401 và project
	f.err = &httpapi.StatusError{Status: 404, Msg: "Bulk data not found"}
	for _, p := range []string{"/dicomweb/studies/S/series/R/instances/I/bulkdata/00091001", base} {
		if rec := do(h, "GET", p, "read-a"); rec.Code != 404 {
			t.Errorf("%s: %d", p, rec.Code)
		}
	}
	f.calls = nil
	if rec := do(h, "GET", base, ""); rec.Code != 401 || len(f.calls) != 0 {
		t.Errorf("không token: %d %v", rec.Code, f.calls)
	}
	if rec := do(h, "GET", "/projects/B/dicomweb/wado?requestType=WADO&studyUID=S&seriesUID=R&objectUID=I", "read-a"); rec.Code != 403 {
		t.Errorf("project khác: %d", rec.Code)
	}
}

func TestRetrieveErrorsAndAuth(t *testing.T) {
	for name, c := range map[string]struct {
		err  error
		want int
	}{
		"404": {&httpapi.StatusError{Status: 404, Msg: "Study not found"}, 404}, "416": {&httpapi.StatusError{Status: 416, Msg: "Requested frame number exceeds total frames available"}, 416},
		"400": {&httpapi.StatusError{Status: 400, Msg: "Invalid frame number"}, 400}, "500": {errors.New("chi tiết nội bộ bí mật"), 500},
	} {
		f := &fakeRetriever{err: c.err}
		h := newHandler(t, func(d *httpapi.Deps) { d.Retrieve = f })
		for _, path := range []string{"/dicomweb/studies", "/dicomweb/studies/1/series", "/dicomweb/studies/1/series/2/metadata", "/dicomweb/studies/1/series/2/instances/3/frames/1"} {
			rec := do(h, "GET", path, "read-a")
			if rec.Code != c.want {
				t.Errorf("%s %s: %d", name, path, rec.Code)
				continue
			}
			if msg := errorOf(t, rec); strings.Contains(msg, "bí mật") {
				t.Errorf("%s %s: lộ chi tiết nội bộ: %q", name, path, msg)
			}
		}
	}
	// xác thực đứng trước: không token thì dịch vụ không bị gọi
	f := &fakeRetriever{}
	h := newHandler(t, func(d *httpapi.Deps) { d.Retrieve = f })
	for _, tok := range []string{"", "bad", "forbidden"} {
		do(h, "GET", "/dicomweb/studies", tok)
		do(h, "GET", "/projects/B/dicomweb/studies", "read-a")
	}
	if len(f.calls) != 0 {
		t.Errorf("dịch vụ bị gọi khi bị từ chối: %v", f.calls)
	}
}

func TestSeriesMetadataStreaming(t *testing.T) {
	f := &fakeRetriever{meta: func(w io.Writer) error { _, err := io.WriteString(w, `[{"a":1},{"b":2}]`); return err }}
	h := newHandler(t, func(d *httpapi.Deps) { d.Retrieve = f })
	rec := do(h, "GET", "/dicomweb/studies/S/series/R/metadata", "read-a")
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/dicom+json" || rec.Body.String() != `[{"a":1},{"b":2}]` || f.lastStudy != "S|R" {
		t.Errorf("%d %q %q %q", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String(), f.lastStudy)
	}
}

func TestFrameResponseHeaders(t *testing.T) {
	body := "--b\r\nContent-Type: application/octet-stream\r\n\r\nXYZ\r\n--b--\r\n"
	f := &fakeRetriever{frames: httpapi.FrameStream{ContentType: `multipart/related; type="application/octet-stream"; boundary=b`, Length: int64(len(body)),
		Write: func(w io.Writer) error { _, err := io.WriteString(w, body); return err }}}
	h := newHandler(t, func(d *httpapi.Deps) { d.Retrieve = f })
	rec := do(h, "GET", "/dicomweb/studies/S/series/R/instances/I/frames/1,2", "read-a")
	if rec.Code != 200 || rec.Header().Get("Content-Type") != f.frames.ContentType || rec.Header().Get("Content-Length") != fmt.Sprint(len(body)) || rec.Body.String() != body {
		t.Errorf("%d %q %q %q", rec.Code, rec.Header().Get("Content-Type"), rec.Header().Get("Content-Length"), rec.Body.String())
	}
	if f.lastStudy != "S|R" || f.lastInstUID != "I" || f.lastFrames != "1,2" {
		t.Errorf("tham số: %q %q %q", f.lastStudy, f.lastInstUID, f.lastFrames)
	}
}

// Lỗi sau khi đã ghi một phần: ngắt kết nối để client thấy phản hồi bị cắt (không để JSON dở dang trông như hợp lệ).
func TestMidStreamFailureAbortsTheConnection(t *testing.T) {
	f := &fakeRetriever{
		meta:   func(w io.Writer) error { io.WriteString(w, `[{"a":1},`); return errors.New("kho lỗi") },
		frames: httpapi.FrameStream{ContentType: "multipart/related; boundary=b", Length: 1000, Write: func(w io.Writer) error { io.WriteString(w, "--b\r\n"); return errors.New("kho lỗi") }},
	}
	srv := httptest.NewServer(newHandler(t, func(d *httpapi.Deps) { d.Retrieve = f }))
	defer srv.Close()
	for _, path := range []string{"/dicomweb/studies/S/series/R/metadata", "/dicomweb/studies/S/series/R/instances/I/frames/1"} {
		req, _ := http.NewRequest("GET", srv.URL+path, nil)
		req.Header.Set("Authorization", "Bearer read-a")
		res, err := srv.Client().Do(req)
		if err != nil {
			continue // ngắt trước cả tiêu đề cũng là cắt đúng nghĩa
		}
		if _, err := io.ReadAll(res.Body); err == nil {
			t.Errorf("%s: client đọc hết mà không biết bị cắt", path)
		}
		res.Body.Close()
	}
}

func TestRetrieveWithoutServiceIs501(t *testing.T) {
	h := newHandler(t)
	for _, path := range []string{"/dicomweb/studies", "/dicomweb/studies/1/series", "/dicomweb/studies/1/series/2/metadata", "/dicomweb/studies/1/series/2/instances/3/frames/1"} {
		if rec := do(h, "GET", path, "read-a"); rec.Code != 501 {
			t.Errorf("%s: %d", path, rec.Code)
		}
	}
}
