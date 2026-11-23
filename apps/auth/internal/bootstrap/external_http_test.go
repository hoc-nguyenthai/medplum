package bootstrap

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/golang-jwt/jwt/v5"

	"trivita-fhir/apps/auth/internal/config"
	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
)

// fakeIdP là một OIDC IdP thật (httptest): /token đổi code lấy id_token ký ES256, /jwks công bố khóa.
type fakeIdP struct {
	srv      *httptest.Server
	key      *ecdsa.PrivateKey
	mu       sync.Mutex
	email    string
	emailVer any
	tokens   []url.Values
	auths    []string
}

func newFakeIdP(t *testing.T, email string) *fakeIdP {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeIdP{key: k, email: email, emailVer: true}
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		f.tokens, f.auths = append(f.tokens, r.PostForm), append(f.auths, r.Header.Get("Authorization"))
		email, ver := f.email, f.emailVer
		f.mu.Unlock()
		claims := jwt.MapClaims{"iss": f.srv.URL, "aud": "idp-client", "sub": "idp-sub-1", "email": email, "email_verified": ver, "exp": time.Now().Add(time.Hour).Unix()}
		tok := jwt.NewWithClaims(jwt.SigningMethodES256, claims)
		tok.Header["kid"] = "idp-k1"
		signed, _ := tok.SignedString(f.key)
		_ = json.NewEncoder(w).Encode(map[string]any{"id_token": signed, "access_token": "x"})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &f.key.PublicKey, KeyID: "idp-k1", Algorithm: "ES256", Use: "sig"}}})
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeIdP) provider() *fhir.IdentityProvider {
	return &fhir.IdentityProvider{AuthorizeUrl: f.srv.URL + "/authorize", TokenUrl: f.srv.URL + "/token", JwksUrl: f.srv.URL + "/jwks", Issuer: f.srv.URL, ClientId: "idp-client", ClientSecret: "idp-secret"}
}

// newExternalAPI dựng auth với đăng nhập ngoài bật (AUTH_APP_BASE_URL có sẵn trong newCodeAPI) và cho phép IdP http cục bộ (dev/test).
func newExternalAPI(t *testing.T, idp *fakeIdP, domainName string) *codeAPI {
	t.Helper()
	a := newCodeAPI(t, func(s *config.Settings) { s.AllowPrivateJWKS = true })
	if _, err := a.c.Auth.Resources.Create(context.Background(), &fhir.DomainConfiguration{ResourceType: "DomainConfiguration", Domain: domainName, IdentityProvider: idp.provider(),
		AllowedPostLoginRedirectUrls: []string{appURL + "/signin"}}); err != nil {
		t.Fatal(err)
	}
	return a
}

func cookieOf(r resp, name string) string {
	for _, line := range r.hdr.Values("Set-Cookie") {
		if strings.HasPrefix(line, name+"=") {
			return strings.SplitN(strings.TrimPrefix(line, name+"="), ";", 2)[0]
		}
	}
	return ""
}

func (a *codeAPI) startExternal(body string) (state, cookie string, r resp) {
	a.t.Helper()
	r = a.do("POST", "/auth/method", body, nil)
	if r.status != 200 {
		a.t.Fatalf("method: %d %s", r.status, r.body)
	}
	u, err := url.Parse(r.json()["authorizeUrl"].(string))
	if err != nil {
		a.t.Fatal(err)
	}
	return u.Query().Get("state"), cookieOf(r, "auth-external-state"), r
}

func (a *codeAPI) callbackExt(path, state, cookie string) resp {
	a.t.Helper()
	hdr := map[string]string{}
	if cookie != "" {
		hdr["Cookie"] = "auth-external-state=" + cookie
	}
	return a.noRedirect("GET", path+"?code=ma-code&state="+url.QueryEscape(state), hdr)
}

func (a *codeAPI) noRedirect(method, path string, hdr map[string]string) resp {
	a.t.Helper()
	req, _ := http.NewRequest(method, a.srv.URL+path, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer res.Body.Close()
	var buf strings.Builder
	b := make([]byte, 4096)
	for {
		n, e := res.Body.Read(b)
		buf.Write(b[:n])
		if e != nil {
			break
		}
	}
	return resp{res.StatusCode, res.Header, buf.String()}
}

func TestHTTPExternalLoginEndToEndWithARealIdP(t *testing.T) {
	ctx := context.Background()
	idp := newFakeIdP(t, "admin@example.org")
	a := newExternalAPI(t, idp, "example.org")

	body := `{"email":"Admin@Example.ORG","clientId":"` + a.seed.Client.ID + `","scope":"openid profile","redirectUri":"` + redirectURI + `","codeChallenge":"` + s256() + `","codeChallengeMethod":"S256"}`
	state, cookie, m := a.startExternal(body)
	if cookie == "" || !strings.Contains(m.hdr.Get("Set-Cookie"), "HttpOnly") || !strings.Contains(m.hdr.Get("Set-Cookie"), "Path=/auth/external") || !strings.Contains(m.hdr.Get("Set-Cookie"), "SameSite=Lax") {
		t.Fatalf("cookie liên kết: %q", m.hdr.Get("Set-Cookie"))
	}
	if m.json()["domain"] != "example.org" {
		t.Errorf("domain chữ thường: %v", m.json())
	}
	// callback không có cookie, hoặc cookie của trình duyệt khác: từ chối, chưa gọi IdP
	for name, ck := range map[string]string{"không cookie": "", "cookie khác": "00000000000000000000000000000000"} {
		r := a.callbackExt("/auth/external", state, ck)
		if text, _ := firstIssue(t, r); r.status != 400 || text != "Invalid state" {
			t.Errorf("%s: %d %s", name, r.status, r.body)
		}
	}
	if len(idp.tokens) != 0 {
		t.Fatal("state không hợp lệ thì chưa được gọi IdP")
	}
	r := a.callbackExt("/auth/external", state, cookie)
	if r.status != 302 {
		t.Fatalf("callback: %d %s", r.status, r.body)
	}
	if !strings.Contains(r.hdr.Get("Set-Cookie"), "auth-external-state=;") && !strings.Contains(r.hdr.Get("Set-Cookie"), "Max-Age=0") {
		t.Errorf("cookie liên kết phải bị xóa sau callback: %q", r.hdr.Get("Set-Cookie"))
	}
	loc, _ := url.Parse(r.hdr.Get("Location"))
	if loc.Scheme+"://"+loc.Host+loc.Path != redirectURI || loc.Query().Get("code") == "" || loc.Query().Get("login") == "" {
		t.Fatalf("Location: %s", r.hdr.Get("Location"))
	}
	// IdP nhận đúng tham số: code, redirect_uri của auth, Basic auth; KHÔNG nhận code_verifier (usePkce tắt)
	if len(idp.tokens) != 1 || idp.tokens[0].Get("code") != "ma-code" || idp.tokens[0].Get("redirect_uri") != a.c.Settings.BaseURL+"/auth/external" || idp.tokens[0].Has("code_verifier") ||
		!strings.HasPrefix(idp.auths[0], "Basic ") {
		t.Errorf("gọi IdP: %v %v", idp.tokens, idp.auths)
	}
	// replay: cùng state, cookie đã bị xóa nên không còn; kể cả khi kẻ tấn công có cả cookie cũ thì state hết hạn/đã dùng ở trình duyệt (cookie xóa)
	if rr := a.callbackExt("/auth/external", state, ""); rr.status != 400 {
		t.Errorf("replay không cookie: %d", rr.status)
	}
	// đổi code ở /oauth2/token bằng PKCE: nhận token, rồi fhir-server (qua xác minh của chính auth) chấp nhận
	tk := a.exchange(url.Values{"code": {loc.Query().Get("code")}, "code_verifier": {verifier}, "client_id": {a.seed.Client.ID}, "redirect_uri": {redirectURI}}, nil)
	if tk.status != 200 || tk.json()["access_token"] == nil {
		t.Fatalf("đổi code: %d %s", tk.status, tk.body)
	}
	lg := a.storedLogin(loc.Query().Get("login"))
	if lg.AuthMethod != domain.AuthExternal || !domain.IsTrue(lg.Granted) {
		t.Errorf("login: %+v", lg)
	}
	admin, _ := a.c.Auth.FindUserByEmail(ctx, "admin@example.org", "")
	if !domain.IsTrue(admin.EmailVerified) {
		t.Error("IdP đã xác thực email")
	}
}

func TestHTTPExternalLoginRejections(t *testing.T) {
	idp := newFakeIdP(t, "admin@example.org")
	a := newExternalAPI(t, idp, "example.org")
	body := `{"email":"admin@example.org","clientId":"` + a.seed.Client.ID + `","redirectUri":"` + redirectURI + `","codeChallenge":"` + s256() + `","codeChallengeMethod":"S256"}`

	// IdP báo email chưa xác minh (Q2)
	idp.emailVer = false
	state, cookie, _ := a.startExternal(body)
	r := a.callbackExt("/auth/external", state, cookie)
	if text, _ := firstIssue(t, r); r.status != 400 || text != "Email address is not verified by the identity provider" || r.hdr.Get("Location") != "" {
		t.Errorf("%d %s", r.status, r.body)
	}
	idp.emailVer = true
	// IdP trả email thuộc người khác (không thuộc tên miền của state)
	idp.email = "ke-tan-cong@khac.example.org"
	state, cookie, _ = a.startExternal(body)
	r = a.callbackExt("/auth/external", state, cookie)
	if text, _ := firstIssue(t, r); r.status != 400 || text != "Email address does not match domain" {
		t.Errorf("%d %s", r.status, r.body)
	}
	// user không tồn tại: không để lại Login
	idp.email = "khong-co@example.org"
	state, cookie, _ = a.startExternal(body)
	before := countLogins(t, a)
	r = a.callbackExt("/auth/external", state, cookie)
	if text, _ := firstIssue(t, r); r.status != 400 || text != "User not found" || countLogins(t, a) != before {
		t.Errorf("%d %s logins %d→%d", r.status, r.body, before, countLogins(t, a))
	}
	// state tự dựng theo kiểu Medplum (JSON thô, không ký): từ chối
	raw := url.QueryEscape(`{"clientId":"` + a.seed.Client.ID + `","domain":"example.org"}`)
	rr := a.noRedirect("GET", "/auth/external?code=c&state="+raw, map[string]string{"Cookie": "auth-external-state=" + cookie})
	if text, _ := firstIssue(t, rr); rr.status != 400 || text != "Invalid state" {
		t.Errorf("state không ký: %d %s", rr.status, rr.body)
	}
	for path, want := range map[string]string{"/auth/external?state=x": "Missing code", "/auth/external?code=x": "Missing state"} {
		rr := a.noRedirect("GET", path, nil)
		if text, _ := firstIssue(t, rr); rr.status != 400 || text != want {
			t.Errorf("%s: %d %s", path, rr.status, rr.body)
		}
	}
}

func countLogins(t *testing.T, a *codeAPI) int {
	t.Helper()
	ids, err := a.c.Auth.Logins.ListIDsUpdatedBefore(context.Background(), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	return len(ids)
}

func TestHTTPExternalRoutesAbsentWithoutAppBaseURL(t *testing.T) {
	a := newAPI(t, func(s *config.Settings) { s.AppBaseURL = "" }, nil)
	for _, p := range []string{"/auth/external?code=c&state=s", "/auth/external/x?code=c&state=s"} {
		if r := a.do("GET", p, "", nil); r.status != 404 {
			t.Errorf("%s: %d", p, r.status)
		}
	}
	if r := a.do("POST", "/auth/external/start", `{"clientId":"x"}`, nil); r.status != 404 && r.status != 405 {
		t.Errorf("start: %d", r.status)
	}
	// /auth/method vẫn dùng được và không phát state hay cookie
	if r := a.do("POST", "/auth/method", `{"email":"admin@example.org"}`, nil); r.status != 200 || r.hdr.Get("Set-Cookie") != "" || r.body != "{}\n" && r.body != "{}" {
		t.Errorf("%d %q %v", r.status, r.body, r.hdr)
	}
}

func TestHTTPExternalStartForClientWithOwnIdentityProvider(t *testing.T) {
	ctx := context.Background()
	idp := newFakeIdP(t, "admin@example.org")
	a := newExternalAPI(t, idp, "example.org")
	own := idp.provider()
	own.UseSubject = ptrBool(true)
	cl, err := a.c.Auth.Resources.Create(ctx, &fhir.ClientApplication{ResourceType: "ClientApplication", Name: "Riêng", Meta: &fhir.Meta{Project: a.seed.Project.ID},
		IdentityProvider: own, RedirectUris: []string{redirectURI}, PkceOptional: ptrBool(true)})
	if err != nil {
		t.Fatal(err)
	}
	if r := a.do("POST", "/auth/external/start", `{}`, nil); r.status != 400 {
		t.Errorf("thiếu clientId: %d", r.status)
	} else if text, expr := firstIssue(t, r); text != "Missing clientId" || expr != "clientId" {
		t.Errorf("%q %q", text, expr)
	}
	if r := a.do("POST", "/auth/external/start", `{"clientId":"`+a.seed.Client.ID+`"}`, nil); r.status != 400 {
		t.Errorf("client không có IdP: %d %s", r.status, r.body)
	}
	r := a.do("POST", "/auth/external/start", `{"clientId":"`+cl.GetID()+`","redirectUri":"`+redirectURI+`"}`, nil)
	if r.status != 200 || cookieOf(r, "auth-external-state") == "" {
		t.Fatalf("%d %s", r.status, r.body)
	}
	u, _ := url.Parse(r.json()["authorizeUrl"].(string))
	if u.Query().Get("redirect_uri") != a.c.Settings.BaseURL+"/auth/external/"+cl.GetID() || u.Query().Get("state") == "" {
		t.Errorf("%s", u)
	}
	// subject chưa gắn externalId vào membership: callback riêng của client => User not found
	cb := a.callbackExt("/auth/external/"+cl.GetID(), u.Query().Get("state"), cookieOf(r, "auth-external-state"))
	if text, _ := firstIssue(t, cb); cb.status != 400 || text != "User not found" {
		t.Errorf("%d %s", cb.status, cb.body)
	}
	// callback chung với state của client có IdP riêng + project không bật legacyExternalAuth: phải dùng callback riêng
	r2 := a.do("POST", "/auth/external/start", `{"clientId":"`+cl.GetID()+`"}`, nil)
	u2, _ := url.Parse(r2.json()["authorizeUrl"].(string))
	cb2 := a.callbackExt("/auth/external", u2.Query().Get("state"), cookieOf(r2, "auth-external-state"))
	if text, _ := firstIssue(t, cb2); cb2.status != 400 || !strings.Contains(text, "must use the per-client callback") {
		t.Errorf("%d %s", cb2.status, cb2.body)
	}
}

func TestHTTPClientInfo(t *testing.T) {
	a := newAPI(t, nil, nil)
	cid := a.seed.Client.ID
	ok := a.do("GET", "/auth/clientinfo/"+cid, "", nil)
	if ok.status != 200 || strings.Contains(ok.body, a.seed.Client.Secret) {
		t.Fatalf("%d %s", ok.status, ok.body)
	}
	if r := a.do("GET", "/auth/clientinfo/"+domain.NewID(), "", nil); r.status != 404 {
		t.Errorf("client lạ: %d", r.status)
	}
	if r := a.do("GET", "/auth/clientinfo/"+cid+"?redirect_uri=https%3A%2F%2Fevil.example.org", "", nil); r.status != 400 {
		t.Errorf("redirect_uri lạ: %d", r.status)
	} else if text, _ := firstIssue(t, r); text != "Invalid redirect URI" {
		t.Errorf("%q", text)
	}
	if r := a.do("GET", "/auth/clientinfo/"+cid+"?redirect_uri=", "", nil); r.status != 400 {
		t.Errorf("redirect_uri rỗng vẫn là có gửi: %d", r.status)
	}
	if r := a.do("GET", "/auth/clientinfo/"+domain.CLIClientID, "", nil); r.status != 200 {
		t.Errorf("client dựng sẵn: %d", r.status)
	}
}

// Callback và start làm auth gọi ra IdP, nên dùng chung hạn mức của bước nhập thông tin đăng nhập (chặt hơn mức chung của /auth/*).
func TestHTTPExternalRoutesShareTheLoginRateLimit(t *testing.T) {
	idp := newFakeIdP(t, "admin@example.org")
	a := newCodeAPI(t, func(s *config.Settings) { s.AllowPrivateJWKS = true; s.LoginRateLimit, s.LoginRateBurst = 0.001, 3 })
	_ = idp
	for i := 0; i < 3; i++ {
		if r := a.login(loginBody(""), nil); r.status != 200 {
			t.Fatalf("lần %d: %d %s", i, r.status, r.body)
		}
	}
	for _, tc := range []struct{ method, path, body string }{
		{"GET", "/auth/external?code=c&state=s", ""},
		{"GET", "/auth/external/" + domain.NewID() + "?code=c&state=s", ""},
		{"POST", "/auth/external/start", `{"clientId":"x"}`},
	} {
		if r := a.noRedirectBody(tc.method, tc.path, tc.body); r.status != 429 {
			t.Errorf("%s %s phải chung hạn mức đăng nhập: %d %s", tc.method, tc.path, r.status, r.body)
		}
	}
}

func (a *codeAPI) noRedirectBody(method, path, body string) resp {
	a.t.Helper()
	req, _ := http.NewRequest(method, a.srv.URL+path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return resp{res.StatusCode, res.Header, string(b)}
}

func TestHTTPExternalCallbackPassesTheIssParameter(t *testing.T) {
	idp := newFakeIdP(t, "admin@example.org")
	a := newExternalAPI(t, idp, "example.org")
	body := `{"email":"admin@example.org","clientId":"` + a.seed.Client.ID + `","redirectUri":"` + redirectURI + `","codeChallenge":"` + s256() + `","codeChallengeMethod":"S256"}`
	state, cookie, _ := a.startExternal(body)
	r := a.noRedirect("GET", "/auth/external?code=c&iss="+url.QueryEscape("https://evil.example.org")+"&state="+url.QueryEscape(state), map[string]string{"Cookie": "auth-external-state=" + cookie})
	if text, _ := firstIssue(t, r); r.status != 400 || text != "Invalid issuer" {
		t.Errorf("%d %s", r.status, r.body)
	}
	if len(idp.tokens) != 0 {
		t.Error("sai iss thì chưa được gọi IdP")
	}
}
