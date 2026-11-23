package httpsec

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var ok200 = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200); _, _ = w.Write([]byte("ok")) })

func serve(h http.Handler, method, path string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestSecurityHeaders(t *testing.T) {
	rec := serve(SecurityHeaders(HeadersOptions{BaseURL: "https://auth.example.org"})(ok200), "GET", "/x", nil)
	want := map[string]string{
		"Cache-Control":           "no-store, no-cache, must-revalidate",
		"Pragma":                  "no-cache",
		"Content-Security-Policy": "default-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none';",
		"Referrer-Policy":         "no-referrer",
		"X-Content-Type-Options":  "nosniff",
		"X-Frame-Options":         "DENY",
		"X-XSS-Protection":        "1; mode=block",
	}
	for k, v := range want {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("%s = %q, mong %q", k, got, v)
		}
	}
	if got := rec.Header().Get("Strict-Transport-Security"); got != "max-age=63072000; includeSubDomains; preload" {
		t.Errorf("HSTS với https: %q", got)
	}
	if got := rec.Header().Get("Permissions-Policy"); !strings.Contains(got, "camera=()") || !strings.Contains(got, "interest-cohort=()") {
		t.Errorf("Permissions-Policy: %q", got)
	}
	if rec.Code != 200 || rec.Body.String() != "ok" {
		t.Error("phải gọi handler kế tiếp")
	}
	// http thuần: không HSTS (trình duyệt sẽ ghi nhớ và chặn HTTP của localhost)
	if got := serve(SecurityHeaders(HeadersOptions{BaseURL: "http://localhost:8006"})(ok200), "GET", "/", nil).Header().Get("Strict-Transport-Security"); got != "" {
		t.Errorf("không HSTS với http: %q", got)
	}
	if got := serve(SecurityHeaders(HeadersOptions{})(ok200), "GET", "/", nil).Header().Get("Strict-Transport-Security"); got != "" {
		t.Errorf("không HSTS khi không có base URL: %q", got)
	}
}

func TestSecurityHeadersSkip(t *testing.T) {
	h := SecurityHeaders(HeadersOptions{Skip: func(r *http.Request) bool { return strings.HasPrefix(r.URL.Path, "/swagger/") }})(ok200)
	if got := serve(h, "GET", "/swagger/index.html", nil).Header().Get("Content-Security-Policy"); got != "" {
		t.Errorf("Skip phải bỏ qua header: %q", got)
	}
	rec := serve(h, "GET", "/auth/login", nil)
	if rec.Header().Get("Content-Security-Policy") == "" || rec.Header().Get("Cache-Control") == "" {
		t.Error("đường dẫn khác vẫn có header")
	}
}

func cors(o CORSOptions) http.Handler { return CORS(o)(ok200) }

var devOpts = CORSOptions{AllowedOrigins: []string{"http://localhost:3000"}, PathPrefixes: []string{"/auth/", "/oauth2/"}}

func TestCORSAllowedOrigin(t *testing.T) {
	rec := serve(cors(devOpts), "POST", "/auth/login", map[string]string{"Origin": "http://localhost:3000"})
	h := rec.Header()
	if h.Get("Access-Control-Allow-Origin") != "http://localhost:3000" || h.Get("Access-Control-Allow-Credentials") != "true" {
		t.Errorf("%v", h)
	}
	if h.Get("Access-Control-Allow-Origin") == "*" {
		t.Error("không bao giờ là *")
	}
	if !strings.Contains(strings.Join(h.Values("Vary"), ","), "Origin") {
		t.Errorf("phải có Vary: Origin: %v", h.Values("Vary"))
	}
	for _, name := range DefaultExposedHeaders {
		if !strings.Contains(h.Get("Access-Control-Expose-Headers"), name) {
			t.Errorf("thiếu expose %s: %s", name, h.Get("Access-Control-Expose-Headers"))
		}
	}
	if rec.Code != 200 || rec.Body.String() != "ok" {
		t.Error("request thường phải qua handler")
	}
}

func TestCORSRejectedCases(t *testing.T) {
	for name, c := range map[string]struct {
		origin, path string
	}{
		"origin lạ":                 {"http://evil.example.org", "/auth/login"},
		"cùng host khác cổng":       {"http://localhost:3001", "/auth/login"},
		"khác scheme":               {"https://localhost:3000", "/auth/login"},
		"đường dẫn ngoài danh sách": {"http://localhost:3000", "/health/live"},
		"hậu tố giả":                {"http://localhost:3000.evil.org", "/auth/login"},
	} {
		rec := serve(cors(devOpts), "POST", c.path, map[string]string{"Origin": c.origin})
		if rec.Header().Get("Access-Control-Allow-Origin") != "" || rec.Header().Get("Access-Control-Allow-Credentials") != "" {
			t.Errorf("%s: không được có header CORS: %v", name, rec.Header())
		}
		if rec.Code != 200 {
			t.Errorf("%s: request vẫn được xử lý (CORS do trình duyệt thi hành), mã %d", name, rec.Code)
		}
	}
	// không có Origin: đi thẳng, không Vary
	rec := serve(cors(devOpts), "POST", "/auth/login", nil)
	if rec.Header().Get("Vary") != "" || rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("%v", rec.Header())
	}
}

func TestCORSPreflight(t *testing.T) {
	pre := map[string]string{
		"Origin": "http://localhost:3000", "Access-Control-Request-Method": "POST",
		"Access-Control-Request-Headers": "content-type,authorization",
	}
	rec := serve(cors(devOpts), "OPTIONS", "/auth/login", pre)
	h := rec.Header()
	if rec.Code != 204 || rec.Body.Len() != 0 {
		t.Fatalf("%d %q", rec.Code, rec.Body)
	}
	if h.Get("Access-Control-Allow-Origin") != "http://localhost:3000" || h.Get("Access-Control-Allow-Credentials") != "true" ||
		h.Get("Access-Control-Allow-Methods") != "GET,HEAD,PUT,PATCH,POST,DELETE" ||
		h.Get("Access-Control-Allow-Headers") != "content-type,authorization" || h.Get("Access-Control-Max-Age") != "600" {
		t.Errorf("%v", h)
	}
	vary := strings.Join(h.Values("Vary"), ",")
	for _, v := range []string{"Origin", "Access-Control-Request-Method", "Access-Control-Request-Headers"} {
		if !strings.Contains(vary, v) {
			t.Errorf("Vary thiếu %s: %s", v, vary)
		}
	}
	// preflight không được phép: 204 nhưng không có header CORS
	for name, hdr := range map[string]map[string]string{
		"origin lạ":       {"Origin": "http://evil.example.org", "Access-Control-Request-Method": "POST"},
		"đường dẫn ngoài": nil,
	} {
		path := "/auth/login"
		if hdr == nil {
			hdr, path = pre, "/health/live"
		}
		rec := serve(cors(devOpts), "OPTIONS", path, hdr)
		if rec.Code != 204 || rec.Header().Get("Access-Control-Allow-Origin") != "" || rec.Header().Get("Access-Control-Allow-Methods") != "" {
			t.Errorf("%s: %d %v", name, rec.Code, rec.Header())
		}
	}
	// OPTIONS có Origin nhưng không phải preflight (thiếu Access-Control-Request-Method): đi qua handler
	rec = serve(cors(devOpts), "OPTIONS", "/auth/login", map[string]string{"Origin": "http://localhost:3000"})
	if rec.Code != 200 {
		t.Errorf("không phải preflight thì qua handler: %d", rec.Code)
	}
}

func TestPreflightDoesNotCallHandler(t *testing.T) {
	called := false
	h := CORS(devOpts)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	serve(h, "OPTIONS", "/auth/login", map[string]string{"Origin": "http://localhost:3000", "Access-Control-Request-Method": "POST"})
	if called {
		t.Error("preflight phải được trả lời tại middleware")
	}
}

func TestCORSNormalizationWildcardAndDefaults(t *testing.T) {
	o := CORSOptions{AllowedOrigins: []string{"HTTP://LocalHost:3000/"}, MaxAge: 90 * time.Second, ExposedHeaders: []string{"X-One"}}
	rec := serve(cors(o), "POST", "/anything", map[string]string{"Origin": "http://localhost:3000"})
	if rec.Header().Get("Access-Control-Allow-Origin") != "http://localhost:3000" || rec.Header().Get("Access-Control-Expose-Headers") != "X-One" {
		t.Errorf("chuẩn hóa hoa thường và dấu /: %v", rec.Header())
	}
	rec = serve(cors(o), "OPTIONS", "/x", map[string]string{"Origin": "http://localhost:3000", "Access-Control-Request-Method": "GET"})
	if rec.Header().Get("Access-Control-Max-Age") != "90" || rec.Header().Get("Access-Control-Allow-Headers") != "" {
		t.Errorf("%v", rec.Header())
	}
	// không giới hạn đường dẫn khi PathPrefixes rỗng
	if got := serve(cors(o), "GET", "/bat-ky", map[string]string{"Origin": "http://localhost:3000"}).Header().Get("Access-Control-Allow-Origin"); got == "" {
		t.Error("PathPrefixes rỗng thì mọi đường dẫn")
	}
	// "*": phản chiếu origin (kèm credentials), không bao giờ trả "*"
	rec = serve(cors(CORSOptions{AllowedOrigins: []string{"*"}}), "GET", "/", map[string]string{"Origin": "https://bat-ky.example.org"})
	if rec.Header().Get("Access-Control-Allow-Origin") != "https://bat-ky.example.org" || rec.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Errorf("%v", rec.Header())
	}
	// không có origin nào được phép
	if got := serve(cors(CORSOptions{}), "GET", "/", map[string]string{"Origin": "http://localhost:3000"}).Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("danh sách rỗng thì không origin nào: %q", got)
	}
	// giữ Vary có sẵn
	pre := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Header().Add("Vary", "Accept-Encoding") })
	rec = httptest.NewRecorder()
	CORS(o)(pre).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Header().Get("Vary") != "Accept-Encoding" {
		t.Errorf("không Origin thì middleware không thêm Vary: %q", rec.Header().Values("Vary"))
	}
}

func TestParseOrigin(t *testing.T) {
	good := map[string]string{
		"http://localhost:3000": "http://localhost:3000", "HTTPS://App.Example.org/": "https://app.example.org", " https://a.b:8443 ": "https://a.b:8443",
	}
	for in, want := range good {
		if got, err := ParseOrigin(in); err != nil || got != want {
			t.Errorf("%q: %q %v", in, got, err)
		}
	}
	for _, in := range []string{"", "localhost:3000", "ftp://x.org", "http://", "https://a.org/path", "https://a.org?x=1", "https://a.org#f", "https://u:p@a.org", "*", "//a.org"} {
		_, err := ParseOrigin(in)
		var oe *OriginError
		if !errors.As(err, &oe) || oe.Origin != in || err.Error() == "" {
			t.Errorf("%q phải lỗi: %v", in, err)
		}
	}
}
