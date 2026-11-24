package bootstrap

import (
	"context"
	"encoding/json"
	"github.com/alicebob/miniredis/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"trivita-fhir/apps/auth/internal/application"
	"trivita-fhir/apps/auth/internal/config"
	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirrouter"
)

const adminPassword = "mat-khau-admin-123"

type api struct {
	t    *testing.T
	c    *Container
	srv  *httptest.Server
	seed *application.ProjectInitResult
}

func newAPI(t *testing.T, mut func(*config.Settings), auth application.Authenticator) *api {
	t.Helper()
	c := newContainer(t, func(s *config.Settings) {
		s.PwnedEnabled = false
		s.AllowedOrigins = config.DevAllowedOrigin // như FromEnv làm khi ENVIRONMENT=local và chưa đặt biến
		if mut != nil {
			mut(s)
		}
	})
	c.Authenticator = auth
	e, err := c.Handler(nil)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(e)
	t.Cleanup(srv.Close)
	seed, err := c.Auth.SeedProject(context.Background(), application.SeedProjectRequest{
		Name: "Bệnh viện A", AdminEmail: "admin@example.org", AdminFirst: "An", AdminLast: "Nguyen", AdminPassword: adminPassword,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &api{t: t, c: c, srv: srv, seed: seed}
}

type resp struct {
	status int
	hdr    http.Header
	body   string
}

func (r resp) json() map[string]any {
	var m map[string]any
	if err := json.Unmarshal([]byte(r.body), &m); err != nil {
		panic(err)
	}
	return m
}

func (a *api) do(method, path, body string, hdr map[string]string) resp {
	a.t.Helper()
	req, err := http.NewRequest(method, a.srv.URL+path, strings.NewReader(body))
	if err != nil {
		a.t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return resp{res.StatusCode, res.Header, string(b)}
}

func (a *api) login(body string, hdr map[string]string) resp {
	return a.do("POST", "/auth/login", body, hdr)
}

func loginBody(extra string) string {
	return `{"email":"admin@example.org","password":"` + adminPassword + `"` + extra + `}`
}

func issues(r resp) []map[string]any {
	var out []map[string]any
	for _, i := range r.json()["issue"].([]any) {
		out = append(out, i.(map[string]any))
	}
	return out
}

func TestHTTPLoginSuccess(t *testing.T) {
	a := newAPI(t, nil, nil)
	r := a.login(loginBody(""), nil)
	if r.status != 200 || !strings.HasPrefix(r.hdr.Get("Content-Type"), "application/json") {
		t.Fatalf("%d %s %s", r.status, r.hdr.Get("Content-Type"), r.body)
	}
	m := r.json()
	if m["login"] == "" || len(m["code"].(string)) != 32 {
		t.Errorf("%s", r.body)
	}
	for _, leak := range []string{"passwordHash", "refreshSecret", "cookie", "memberships"} {
		if _, ok := m[leak]; ok {
			t.Errorf("không được có %s trong phản hồi: %s", leak, r.body)
		}
	}
	if len(r.hdr.Values("Set-Cookie")) != 0 {
		t.Error("không có client thì không đặt cookie")
	}
}

func TestHTTPLoginValidation(t *testing.T) {
	a := newAPI(t, nil, nil)
	r := a.login(`{"email":"xyz","password":"ngan","projectId":"abc","redirectUri":{"x":1}}`, nil)
	if r.status != 400 || !strings.HasPrefix(r.hdr.Get("Content-Type"), "application/fhir+json") {
		t.Fatalf("%d %s", r.status, r.hdr.Get("Content-Type"))
	}
	var got []string
	for _, i := range issues(r) {
		got = append(got, i["expression"].([]any)[0].(string)+": "+i["details"].(map[string]any)["text"].(string))
		if i["severity"] != "error" || i["code"] != "invalid" {
			t.Errorf("%v", i)
		}
	}
	want := []string{"email: Valid email address is required", "projectId: Invalid projectId", "password: Password must be at least 8 characters", "redirectUri: Invalid redirectUri"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("gom mọi lỗi theo thứ tự validator:\n got %v\nwant %v", got, want)
	}
	for body, msg := range map[string]string{
		`{}`: "Valid email address is required", `{"email":""}`: "Valid email address is required",
		`{"email":"a@b.co"}`: "Password must be at least 8 characters",
		`{"email":"a@b.co","password":"` + strings.Repeat("a", 73) + `"}`: "Password must be no more than 72 characters",
		`{"email":5,"password":"12345678"}`:                               "Valid email address is required",
	} {
		r := a.login(body, nil)
		if r.status != 400 || issues(r)[0]["details"].(map[string]any)["text"] != msg {
			t.Errorf("%s: %d %s", body, r.status, r.body)
		}
	}
	for _, bad := range []string{"", "không phải json", "[]", "null"} {
		if r := a.login(bad, nil); r.status != 400 || issues(r)[0]["details"].(map[string]any)["text"] != "Content could not be parsed" {
			t.Errorf("%q: %d %s", bad, r.status, r.body)
		}
	}
	// projectId rỗng được bỏ qua như falsy
	if r := a.login(loginBody(`,"projectId":""`), nil); r.status != 200 {
		t.Errorf("%d %s", r.status, r.body)
	}
}

func TestHTTPLoginWrongCredentialsLeakNothing(t *testing.T) {
	a := newAPI(t, nil, nil)
	wrong := a.login(`{"email":"admin@example.org","password":"mat-khau-sai-nhe"}`, nil)
	ghost := a.login(`{"email":"khong-co@example.org","password":"mat-khau-sai-nhe"}`, nil)
	if wrong.status != 400 || ghost.status != 400 || wrong.body != ghost.body {
		t.Errorf("sai mật khẩu và email không tồn tại phải giống hệt nhau:\n%s\n%s", wrong.body, ghost.body)
	}
	if issues(wrong)[0]["details"].(map[string]any)["text"] != "Email or password is invalid" {
		t.Errorf("%s", wrong.body)
	}
}

func TestHTTPLoginCookie(t *testing.T) {
	a := newAPI(t, nil, nil)
	client, err := a.c.Auth.CreateClient(context.Background(), a.seed.Project.ID, application.NewClient{Name: "SPA", RedirectURIs: []string{"https://app/cb"}})
	if err != nil {
		t.Fatal(err)
	}
	r := a.login(loginBody(`,"clientId":"`+client.ID+`","scope":"openid offline_access"`), nil)
	if r.status != 200 || r.json()["code"] == nil {
		t.Fatalf("%d %s", r.status, r.body)
	}
	ck := r.hdr.Get("Set-Cookie")
	for _, want := range []string{"medplum-" + client.ID + "=", "Max-Age=3600", "Path=/", "HttpOnly", "Secure", "SameSite=None"} {
		if !strings.Contains(ck, want) {
			t.Errorf("cookie thiếu %q: %s", want, ck)
		}
	}
	// sai redirect URI
	r = a.login(loginBody(`,"clientId":"`+client.ID+`","redirectUri":"https://evil/cb"`), nil)
	if r.status != 400 || issues(r)[0]["details"].(map[string]any)["text"] != "Invalid redirect URI" {
		t.Errorf("%d %s", r.status, r.body)
	}
	// projectId lệch project của client
	r = a.login(loginBody(`,"clientId":"`+client.ID+`","projectId":"`+domain.NewID()+`"`), nil)
	if r.status != 400 || issues(r)[0]["details"].(map[string]any)["text"] != "Invalid projectId" {
		t.Errorf("%d %s", r.status, r.body)
	}
	if r = a.login(loginBody(`,"clientId":"khong-co"`), nil); r.status != 404 {
		t.Errorf("client không có: %d %s", r.status, r.body)
	}
}

func TestHTTPCookieNotSecureInDev(t *testing.T) {
	a := newAPI(t, func(s *config.Settings) { s.CookieSecure = false }, nil)
	client, _ := a.c.Auth.CreateClient(context.Background(), a.seed.Project.ID, application.NewClient{Name: "SPA"})
	ck := a.login(loginBody(`,"clientId":"`+client.ID+`"`), nil).hdr.Get("Set-Cookie")
	if strings.Contains(ck, "Secure") || !strings.Contains(ck, "SameSite=Lax") || !strings.Contains(ck, "HttpOnly") {
		t.Errorf("dev HTTP: không Secure, SameSite=Lax: %s", ck)
	}
}

func TestHTTPProfileScopeStatusFlow(t *testing.T) {
	a := newAPI(t, nil, nil)
	ctx := context.Background()
	// admin có thêm một project nữa => hai membership
	second, err := a.c.Auth.SeedProject(ctx, application.SeedProjectRequest{Name: "Bệnh viện B", AdminEmail: "admin@example.org"})
	if err != nil {
		t.Fatal(err)
	}
	r := a.login(loginBody(`,"scope":"openid profile"`), nil)
	m := r.json()
	if r.status != 200 || m["code"] != nil || len(m["memberships"].([]any)) != 2 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	loginID := m["login"].(string)
	choice := m["memberships"].([]any)[0].(map[string]any)
	if choice["id"] == "" || choice["project"].(map[string]any)["display"] == "" || choice["profile"].(map[string]any)["reference"] == "" {
		t.Errorf("%v", choice)
	}

	// status khi chưa chọn
	if s := a.do("GET", "/auth/login/"+loginID, "", nil); s.status != 200 || len(s.json()["memberships"].([]any)) != 2 {
		t.Errorf("%d %s", s.status, s.body)
	}
	if s := a.do("GET", "/auth/login/khong-phai-uuid", "", nil); s.status != 400 || issues(s)[0]["details"].(map[string]any)["text"] != "Login ID is required" {
		t.Errorf("%d %s", s.status, s.body)
	}
	if s := a.do("GET", "/auth/login/"+domain.NewID(), "", nil); s.status != 404 {
		t.Errorf("%d", s.status)
	}
	// profile: thiếu trường, sai id, đúng
	if p := a.do("POST", "/auth/profile", `{}`, nil); p.status != 400 || len(issues(p)) != 2 {
		t.Errorf("%d %s", p.status, p.body)
	}
	if p := a.do("POST", "/auth/profile", `{"login":"`+loginID+`","profile":"khong-co"}`, nil); p.status != 400 || issues(p)[0]["details"].(map[string]any)["text"] != "Profile not found" {
		t.Errorf("%d %s", p.status, p.body)
	}
	if p := a.do("POST", "/auth/profile", `{"login":"`+domain.NewID()+`","profile":"`+choice["id"].(string)+`"}`, nil); p.status != 404 {
		t.Errorf("login không có: %d", p.status)
	}
	pr := a.do("POST", "/auth/profile", `{"login":"`+loginID+`","profile":"`+choice["id"].(string)+`"}`, nil)
	if pr.status != 200 || pr.json()["code"] == nil {
		t.Fatalf("%d %s", pr.status, pr.body)
	}
	if again := a.do("POST", "/auth/profile", `{"login":"`+loginID+`","profile":"`+choice["id"].(string)+`"}`, nil); again.status != 400 ||
		issues(again)[0]["details"].(map[string]any)["text"] != "Login profile already set" {
		t.Errorf("%d %s", again.status, again.body)
	}
	// scope
	if s := a.do("POST", "/auth/scope", `{"login":"`+loginID+`"}`, nil); s.status != 400 || issues(s)[0]["details"].(map[string]any)["text"] != "Missing scope" {
		t.Errorf("%d %s", s.status, s.body)
	}
	if s := a.do("POST", "/auth/scope", `{"login":"`+loginID+`","scope":"openid email"}`, nil); s.status != 400 || issues(s)[0]["details"].(map[string]any)["text"] != "Invalid scope" {
		t.Errorf("%d %s", s.status, s.body)
	}
	if s := a.do("POST", "/auth/scope", `{"login":"`+loginID+`","scope":"openid"}`, nil); s.status != 200 || s.json()["code"] == nil {
		t.Errorf("%d %s", s.status, s.body)
	}
	_ = second
}

func TestHTTPMethod(t *testing.T) {
	a := newAPI(t, nil, nil)
	if r := a.do("POST", "/auth/method", `{"email":"an@example.org"}`, nil); r.status != 200 || strings.TrimSpace(r.body) != "{}" {
		t.Errorf("%d %s", r.status, r.body)
	}
	if _, err := a.c.Store.CreateResource(context.Background(), &fhir.DomainConfiguration{ResourceType: "DomainConfiguration", Domain: "corp.example.org",
		IdentityProvider: &fhir.IdentityProvider{AuthorizeUrl: "https://idp.example.org/authorize", ClientId: "cid"}}); err != nil {
		t.Fatal(err)
	}
	r := a.do("POST", "/auth/method", `{"email":"an@corp.example.org"}`, nil)
	m := r.json()
	if r.status != 200 || m["domain"] != "corp.example.org" || !strings.Contains(m["authorizeUrl"].(string), "client_id=cid") ||
		!strings.Contains(m["authorizeUrl"].(string), "auth%2Fexternal") {
		t.Errorf("%d %s", r.status, r.body)
	}
	if r := a.do("POST", "/auth/method", `{"email":"xyz"}`, nil); r.status != 400 || issues(r)[0]["expression"].([]any)[0] != "email" {
		t.Errorf("%d %s", r.status, r.body)
	}
}

// ---- Bearer: /auth/me, /auth/revoke

type fakeAuthenticator struct{ state *application.AuthState }

func (f fakeAuthenticator) Authenticate(_ context.Context, token string) (*application.AuthState, error) {
	if token != "token-hop-le" {
		return nil, domain.Unauthorized("Unauthorized")
	}
	return f.state, nil
}

func TestHTTPBearerRoutesRequireAuthentication(t *testing.T) {
	a := newAPI(t, nil, nil) // chưa có P3: không có Authenticator
	for _, tc := range []struct{ method, path, body string }{{"GET", "/auth/me", ""}, {"POST", "/auth/revoke", `{"loginId":"` + domain.NewID() + `"}`}} {
		for _, hdr := range []map[string]string{nil, {"Authorization": "Bearer token-hop-le"}, {"Authorization": "Basic abc"}, {"Authorization": "Bearer "}} {
			r := a.do(tc.method, tc.path, tc.body, hdr)
			if r.status != 401 || !strings.Contains(r.hdr.Get("WWW-Authenticate"), "Bearer") ||
				!strings.HasPrefix(r.hdr.Get("Content-Type"), "application/fhir+json") || strings.Contains(r.body, "token-hop-le") {
				t.Errorf("%s %s %v: %d %s", tc.method, tc.path, hdr, r.status, r.body)
			}
		}
	}
}

func TestHTTPMeAndRevoke(t *testing.T) {
	ctx := context.Background()
	a := newAPI(t, nil, nil)
	admin, _ := a.c.Auth.FindUserByEmail(ctx, "admin@example.org", "")
	l := a.login(loginBody(""), nil).json()
	login, _ := a.c.Auth.Logins.Get(ctx, l["login"].(string))
	login, _ = a.c.Auth.GrantLogin(ctx, login)
	state := &application.AuthState{Login: login, Membership: a.seed.Membership, Project: a.seed.Project}
	a.c.Authenticator = fakeAuthenticator{state}
	// dựng lại handler với authenticator mới
	e, err := a.c.Handler(nil)
	if err != nil {
		t.Fatal(err)
	}
	srv2 := httptest.NewServer(e)
	defer srv2.Close()
	a.srv = srv2
	auth := map[string]string{"Authorization": "Bearer token-hop-le"}

	if r := a.do("GET", "/auth/me", "", map[string]string{"Authorization": "Bearer sai-token"}); r.status != 401 || !strings.Contains(r.hdr.Get("WWW-Authenticate"), "invalid_token") {
		t.Errorf("%d %v", r.status, r.hdr)
	}
	r := a.do("GET", "/auth/me", "", auth)
	if r.status != 200 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	m := r.json()
	if m["user"].(map[string]any)["email"] != "admin@example.org" || m["project"].(map[string]any)["name"] != "Bệnh viện A" ||
		m["membership"].(map[string]any)["admin"] != true || m["profile"].(map[string]any)["resourceType"] != "Practitioner" {
		t.Errorf("%s", r.body)
	}
	for _, leak := range []string{"passwordHash", "secret", admin.PasswordHash} {
		if strings.Contains(r.body, leak) {
			t.Errorf("/auth/me lộ %q", leak)
		}
	}
	if m["security"].(map[string]any)["sessions"] == nil || len(m["config"].(map[string]any)["menu"].([]any)) != 2 {
		t.Errorf("%s", r.body)
	}

	// revoke
	other := a.login(loginBody(""), nil).json()["login"].(string)
	if rv := a.do("POST", "/auth/revoke", `{"loginId":"abc"}`, auth); rv.status != 400 || issues(rv)[0]["details"].(map[string]any)["text"] != "Login ID is required." {
		t.Errorf("%d %s", rv.status, rv.body)
	}
	rv := a.do("POST", "/auth/revoke", `{"loginId":"`+other+`"}`, auth)
	if rv.status != 200 || !strings.HasPrefix(rv.hdr.Get("Content-Type"), "application/fhir+json") || rv.json()["id"] != "ok" {
		t.Fatalf("%d %s", rv.status, rv.body)
	}
	if got, _ := a.c.Auth.Logins.Get(ctx, other); !domain.IsTrue(got.Revoked) {
		t.Error("phải bị thu hồi")
	}
	if rv := a.do("POST", "/auth/revoke", `{"loginId":"`+domain.NewID()+`"}`, auth); rv.status != 404 {
		t.Errorf("%d", rv.status)
	}
}

// ---- lỗi nội bộ và bảo mật

func TestHTTPInternalErrorsLeakNothingAndNeverReturnACode(t *testing.T) {
	a := newAPI(t, nil, nil)
	// MFA hỏng (cổng lỗi, ví dụ kho trạng thái không dùng được) => 500 chung, KHÔNG có code: nhánh cần MFA luôn fail đóng
	a.c.Auth.MFA = application.FailClosedMFA{}
	proj, err := a.c.Store.ReadResource(context.Background(), "Project", a.seed.Project.ID)
	if err != nil {
		t.Fatal(err)
	}
	p := proj.(*fhir.Project)
	p.Setting = []fhir.ProjectSetting{{Name: "mfaRequired", ValueBoolean: func() *bool { t := true; return &t }()}}
	if _, err := a.c.Store.UpdateResource(context.Background(), p, fhirrouter.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	r := a.login(loginBody(""), nil)
	if r.status != 500 || strings.Contains(r.body, "code") && strings.Contains(r.body, `"code":"`) && !strings.Contains(r.body, `"code":"exception"`) {
		t.Fatalf("%d %s", r.status, r.body)
	}
	if issues(r)[0]["details"].(map[string]any)["text"] != "Internal server error" || issues(r)[0]["code"] != "exception" {
		t.Errorf("thông điệp chung: %s", r.body)
	}
	for _, leak := range []string{"MFA", "P5", "enrollment", "application:", "store"} {
		if strings.Contains(r.body, leak) {
			t.Errorf("lỗi 500 không được lộ %q: %s", leak, r.body)
		}
	}
}

func TestHTTPNotFoundUnderAuthIsOperationOutcome(t *testing.T) {
	a := newAPI(t, nil, nil)
	r := a.do("GET", "/auth/khong-co", "", nil)
	if r.status != 404 || !strings.HasPrefix(r.hdr.Get("Content-Type"), "application/fhir+json") || issues(r)[0]["code"] != "not-found" {
		t.Errorf("%d %s", r.status, r.body)
	}
	if r := a.do("GET", "/khong-co", "", nil); r.status != 404 || strings.TrimSpace(r.body) != `{"error":"Not Found"}` {
		t.Errorf("%d %s", r.status, r.body)
	}
	if r := a.do("GET", "/auth/login", "", nil); r.status != 405 {
		t.Errorf("sai method: %d", r.status)
	}
}

// ipAccessRule quyết định bằng IP người gọi, nên X-Forwarded-For do client tự đặt KHÔNG được tin trừ khi chỉ rõ proxy tin cậy.
func TestHTTPClientIPIsNotSpoofableViaXForwardedFor(t *testing.T) {
	setup := func(t *testing.T, trusted string) *api {
		a := newAPI(t, func(s *config.Settings) { s.TrustedProxies = trusted }, nil)
		ctx := context.Background()
		pol, err := a.c.Store.CreateResource(ctx, &fhir.AccessPolicy{ResourceType: "AccessPolicy", Name: "chặn", Meta: &fhir.Meta{Project: a.seed.Project.ID},
			IpAccessRule: []fhir.AccessPolicyIpAccessRule{{Value: "1.2.3.4", Action: "block"}}})
		if err != nil {
			t.Fatal(err)
		}
		m, err := a.c.Store.ReadResource(ctx, "ProjectMembership", a.seed.Membership.ID)
		if err != nil {
			t.Fatal(err)
		}
		mm := m.(*fhir.ProjectMembership)
		mm.AccessPolicy = &fhir.Reference{Reference: "AccessPolicy/" + pol.GetID()}
		if _, err := a.c.Store.UpdateResource(ctx, mm, fhirrouter.UpdateOptions{}); err != nil {
			t.Fatal(err)
		}
		return a
	}
	spoof := map[string]string{"X-Forwarded-For": "1.2.3.4"}

	a := setup(t, "")
	if r := a.login(loginBody(""), spoof); r.status != 200 {
		t.Errorf("không tin XFF: IP thật (127.0.0.1) không bị chặn: %d %s", r.status, r.body)
	}
	b := setup(t, "127.0.0.0/8") // proxy tin cậy là loopback (httptest kết nối từ 127.0.0.1)
	r := b.login(loginBody(""), spoof)
	if r.status != 400 || issues(r)[0]["details"].(map[string]any)["text"] != domain.IPNotAllowed {
		t.Errorf("tin XFF từ proxy tin cậy: IP 1.2.3.4 phải bị chặn: %d %s", r.status, r.body)
	}
	if r := b.login(loginBody(""), nil); r.status != 200 {
		t.Errorf("không có XFF thì dùng IP kết nối: %d %s", r.status, r.body)
	}
}

// ---- header bảo mật và CORS qua container thật

func TestHTTPResponsesAreNotCacheableAndAreSecured(t *testing.T) {
	a := newAPI(t, nil, nil)
	for name, r := range map[string]resp{
		"login thành công (có code)": a.login(loginBody(""), nil),
		"login sai mật khẩu":         a.login(`{"email":"admin@example.org","password":"mat-khau-sai-nhe"}`, nil),
		"body lỗi":                   a.login(`{}`, nil),
		"health":                     a.do("GET", "/health/live", "", nil),
		"không có route":             a.do("GET", "/khong-co", "", nil),
	} {
		if got := r.hdr.Get("Cache-Control"); got != "no-store, no-cache, must-revalidate" {
			t.Errorf("%s: Cache-Control = %q (phản hồi có thể chứa mã đăng nhập)", name, got)
		}
		if r.hdr.Get("X-Content-Type-Options") != "nosniff" || r.hdr.Get("X-Frame-Options") != "DENY" || r.hdr.Get("Content-Security-Policy") == "" {
			t.Errorf("%s: thiếu header bảo mật: %v", name, r.hdr)
		}
	}
	// http://localhost: không HSTS (trình duyệt sẽ ghi nhớ và chặn HTTP)
	if a.login(loginBody(""), nil).hdr.Get("Strict-Transport-Security") != "" {
		t.Error("không HSTS khi base URL là http")
	}
}

func TestHTTPCORSForLocalhost3000(t *testing.T) {
	a := newAPI(t, nil, nil) // ENVIRONMENT=local: mặc định cho phép http://localhost:3000
	origin := map[string]string{"Origin": "http://localhost:3000"}

	pre := a.do("OPTIONS", "/auth/login", "", map[string]string{"Origin": "http://localhost:3000", "Access-Control-Request-Method": "POST", "Access-Control-Request-Headers": "content-type"})
	if pre.status != 204 || pre.hdr.Get("Access-Control-Allow-Origin") != "http://localhost:3000" || pre.hdr.Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatalf("preflight: %d %v", pre.status, pre.hdr)
	}
	r := a.login(loginBody(`,"clientId":"`+a.mustClient()+`"`), origin)
	if r.status != 200 || r.hdr.Get("Access-Control-Allow-Origin") != "http://localhost:3000" || !strings.Contains(r.hdr.Get("Access-Control-Expose-Headers"), "X-Request-Id") {
		t.Fatalf("%d %v %s", r.status, r.hdr, r.body)
	}
	// origin khác không được phép (kể cả cổng khác)
	for _, o := range []string{"http://localhost:3001", "http://127.0.0.1:3000", "https://localhost:3000", "http://evil.example.org"} {
		r := a.login(loginBody(""), map[string]string{"Origin": o})
		if r.hdr.Get("Access-Control-Allow-Origin") != "" || r.hdr.Get("Access-Control-Allow-Credentials") != "" {
			t.Errorf("%s không được cho phép: %v", o, r.hdr)
		}
	}
	// ngoài tiền tố: health không cần CORS
	if h := a.do("GET", "/health/live", "", origin); h.hdr.Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("health không thuộc tiền tố CORS: %v", h.hdr)
	}
}

func TestHTTPCORSConfigurable(t *testing.T) {
	a := newAPI(t, func(s *config.Settings) { s.AllowedOrigins = "https://app.example.org" }, nil)
	if got := a.login(loginBody(""), map[string]string{"Origin": "http://localhost:3000"}).hdr.Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("đã cấu hình riêng thì localhost:3000 không còn: %q", got)
	}
	if got := a.login(loginBody(""), map[string]string{"Origin": "https://app.example.org"}).hdr.Get("Access-Control-Allow-Origin"); got != "https://app.example.org" {
		t.Errorf("%q", got)
	}
	none := newAPI(t, func(s *config.Settings) { s.AllowedOrigins = "" }, nil)
	if got := none.login(loginBody(""), map[string]string{"Origin": "http://localhost:3000"}).hdr.Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("rỗng thì không cho ai: %q", got)
	}
}

func TestHTTPSwaggerIsUsableInDev(t *testing.T) {
	a := newAPI(t, nil, nil)
	if r := a.do("GET", "/swagger/doc.json", "", nil); r.status != 200 || r.hdr.Get("Content-Security-Policy") != "" {
		t.Errorf("%d CSP=%q", r.status, r.hdr.Get("Content-Security-Policy"))
	}
}

func (a *api) mustClient() string {
	c, err := a.c.Auth.CreateClient(context.Background(), a.seed.Project.ID, application.NewClient{Name: "SPA"})
	if err != nil {
		a.t.Fatal(err)
	}
	return c.ID
}

// ---- hạn mức

func TestHTTPLoginRateLimitIsStricterAndPerIP(t *testing.T) {
	a := newAPI(t, func(s *config.Settings) { s.LoginRateLimit, s.LoginRateBurst = 0.001, 2 }, nil)
	for i := 0; i < 2; i++ {
		if r := a.login(loginBody(""), nil); r.status != 200 {
			t.Fatalf("lần %d: %d %s", i, r.status, r.body)
		}
	}
	r := a.login(loginBody(""), nil)
	if r.status != 429 || r.hdr.Get("Retry-After") == "" || issues(r)[0]["code"] != "throttled" || r.hdr.Get("X-Frame-Options") != "DENY" {
		t.Fatalf("muốn 429 throttled: %d %v %s", r.status, r.hdr, r.body)
	}
	// các bước nhập thông tin khác dùng chung bộ đếm đăng nhập; route đọc và health thì không
	if r := a.do("POST", "/auth/method", `{"email":"x"}`, nil); r.status != 429 {
		t.Errorf("/auth/method: %d", r.status)
	}
	if r := a.do("GET", "/auth/login/abc", "", nil); r.status == 429 {
		t.Errorf("GET status không thuộc hạn mức đăng nhập")
	}
	if r := a.do("GET", "/health/live", "", nil); r.status != 200 {
		t.Errorf("health: %d", r.status)
	}
	// XFF giả không né được khi không khai proxy tin cậy
	if r := a.login(loginBody(""), map[string]string{"X-Forwarded-For": "9.9.9.9"}); r.status != 429 {
		t.Errorf("spoof XFF né được hạn mức: %d", r.status)
	}
}

func TestHTTPGlobalRateLimitCoversAuthPathsOnly(t *testing.T) {
	a := newAPI(t, func(s *config.Settings) { s.RateLimit, s.RateBurst = 0.001, 2 }, nil)
	for i := 0; i < 2; i++ {
		a.do("GET", "/auth/login/abc", "", nil)
	}
	if r := a.do("GET", "/auth/login/abc", "", nil); r.status != 429 {
		t.Fatalf("vượt hạn mức chung: %d", r.status)
	}
	if r := a.do("GET", "/.well-known/jwks.json", "", nil); r.status != 429 {
		t.Errorf("cùng bộ đếm cho /.well-known: %d", r.status)
	}
	for _, p := range []string{"/health/live", "/health/ready"} {
		if r := a.do("GET", p, "", nil); r.status == 429 {
			t.Errorf("%s không được giới hạn", p)
		}
	}
}

func TestHTTPRateLimitOffByDefaultInDev(t *testing.T) {
	a := newAPI(t, nil, nil)
	for i := 0; i < 30; i++ {
		if r := a.login(loginBody(""), nil); r.status == 429 {
			t.Fatalf("dev không giới hạn: lần %d", i)
		}
	}
}

// Hai replica cùng AUTH_REDIS_URL và AUTH_RATE_LIMIT_SHARED chia một hạn mức đăng nhập (không bật thì mỗi replica có đợt riêng).
func TestHTTPLoginRateLimitSharedAcrossReplicas(t *testing.T) {
	mr := miniredis.RunT(t)
	shared := func(s *config.Settings) {
		s.LoginRateLimit, s.LoginRateBurst, s.RateLimitShared, s.RedisURL = 0.001, 2, true, "redis://"+mr.Addr()+"/0"
	}
	a, b := newAPI(t, shared, nil), newAPI(t, shared, nil)
	ok, denied := 0, 0
	for i := 0; i < 2; i++ {
		for _, x := range []*api{a, b} {
			switch r := x.login(loginBody(""), nil); r.status {
			case 200:
				ok++
			case 429:
				denied++
			}
		}
	}
	if ok != 2 || denied != 2 {
		t.Fatalf("chia sẻ: qua %d, bị chặn %d; muốn 2 và 2", ok, denied)
	}
}
