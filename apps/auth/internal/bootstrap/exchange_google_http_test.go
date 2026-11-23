package bootstrap

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/golang-jwt/jwt/v5"

	"trivita-fhir/apps/auth/internal/application"
	"trivita-fhir/apps/auth/internal/config"
	"trivita-fhir/pkg/fhir"
)

// userInfoIdP là một IdP chỉ có /userinfo (đủ cho token-exchange).
func userInfoIdP(t *testing.T, email string, verified any, hits *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("Authorization") != "Bearer ext-access" {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"email": email, "sub": "ext-1", "email_verified": verified})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newExchangeAPI(t *testing.T, email string, verified any) (*codeAPI, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	idp := userInfoIdP(t, email, verified, &hits)
	providers, _ := json.Marshal([]application.ExternalProvider{{Issuer: idp.URL, ClientID: "srv-cid",
		IdentityProvider: &fhir.IdentityProvider{ClientId: "srv-cid", UserInfoUrl: idp.URL + "/userinfo"}}})
	a := newCodeAPI(t, func(s *config.Settings) { s.AllowPrivateJWKS, s.ExternalAuthProviders = true, string(providers) })
	return a, &hits
}

func exchangeForm(a *codeAPI, mut func(url.Values)) url.Values {
	v := url.Values{"grant_type": {application.GrantTypeTokenExchange}, "client_id": {"srv-cid"}, "subject_token": {"ext-access"},
		"subject_token_type": {application.TokenTypeAccessToken}, "membership_id": {a.seed.Membership.ID}}
	if mut != nil {
		mut(v)
	}
	return v
}

func TestHTTPTokenExchangeGrant(t *testing.T) {
	a, hits := newExchangeAPI(t, "admin@example.org", true)
	r := a.form("/oauth2/token", exchangeForm(a, nil), nil)
	if r.status != 200 || r.json()["access_token"] == nil || r.hdr.Get("Cache-Control") != "no-store" {
		t.Fatalf("hợp lệ: %d %s %v", r.status, r.body, r.hdr)
	}
	bad := map[string]func(url.Values){
		"thiếu membership": func(v url.Values) { v.Del("membership_id") },
		"sai loại token":   func(v url.Values) { v.Set("subject_token_type", "x") },
		"client lạ":        func(v url.Values) { v.Set("client_id", "la") },
		"token IdP sai":    func(v url.Values) { v.Set("subject_token", "sai") },
	}
	for name, mut := range bad {
		r := a.form("/oauth2/token", exchangeForm(a, mut), nil)
		if r.status != 400 || r.json()["error"] == nil {
			t.Errorf("%s: %d %s", name, r.status, r.body)
		}
	}
	if hits.Load() != 2 { // lần hợp lệ + lần token IdP sai
		t.Errorf("số lần gọi IdP: %d", hits.Load())
	}
}

func TestHTTPTokenExchangeRejectsUnverifiedEmailAndUnknownUser(t *testing.T) {
	a, _ := newExchangeAPI(t, "admin@example.org", false)
	if r := a.form("/oauth2/token", exchangeForm(a, nil), nil); r.status != 400 {
		t.Errorf("email chưa xác minh: %d %s", r.status, r.body)
	}
	a2, _ := newExchangeAPI(t, "la@example.org", true)
	if r := a2.form("/oauth2/token", exchangeForm(a2, nil), nil); r.status != 400 {
		t.Errorf("user lạ: %d %s", r.status, r.body)
	}
}

func TestHTTPAuthExchangeRouteShapes(t *testing.T) {
	a, _ := newExchangeAPI(t, "admin@example.org", true)
	ok := a.do("POST", "/auth/exchange", `{"externalAccessToken":"ext-access","clientId":"srv-cid","membershipId":"`+a.seed.Membership.ID+`"}`, nil)
	if ok.status != 200 || ok.json()["access_token"] == nil {
		t.Fatalf("hợp lệ: %d %s", ok.status, ok.body)
	}
	// lỗi kiểm đầu vào: OperationOutcome
	r := a.do("POST", "/auth/exchange", `{}`, nil)
	if text, _ := firstIssue(t, r); r.status != 400 || text != "Missing externalAccessToken" {
		t.Errorf("thiếu token: %d %s", r.status, r.body)
	}
	// lỗi OAuth (client lạ): dạng OAuth
	r = a.do("POST", "/auth/exchange", `{"externalAccessToken":"x","clientId":"la"}`, nil)
	if r.status != 400 || r.json()["error"] != "invalid_request" {
		t.Errorf("client lạ: %d %s", r.status, r.body)
	}
	// lỗi đăng nhập (membership của project khác/không có): OperationOutcome
	r = a.do("POST", "/auth/exchange", `{"externalAccessToken":"ext-access","clientId":"srv-cid","membershipId":"`+a.seed.Membership.ID+`x"}`, nil)
	if r.status != 400 {
		t.Errorf("membership sai: %d %s", r.status, r.body)
	}
}

// ---- /auth/google

type googleIdP struct {
	srv *httptest.Server
	key *rsa.PrivateKey
}

func newGoogleIdP(t *testing.T) *googleIdP {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	g := &googleIdP{key: k}
	g.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &k.PublicKey, KeyID: "g1", Algorithm: "RS256", Use: "sig"}}})
	}))
	t.Cleanup(g.srv.Close)
	return g
}

func (g *googleIdP) credential(t *testing.T, extra jwt.MapClaims) string {
	t.Helper()
	c := jwt.MapClaims{"iss": "https://accounts.google.com", "aud": "gid-1", "sub": "g-1", "email": "admin@example.org", "email_verified": true, "exp": time.Now().Add(time.Hour).Unix()}
	for k, v := range extra {
		c[k] = v
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, c)
	tok.Header["kid"] = "g1"
	s, err := tok.SignedString(g.key)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func newGoogleAPI(t *testing.T) (*codeAPI, *googleIdP) {
	t.Helper()
	g := newGoogleIdP(t)
	a := newCodeAPI(t, func(s *config.Settings) {
		s.AllowPrivateJWKS, s.GoogleEnabled, s.GoogleClientID, s.GoogleJWKSURL = true, true, "gid-1", g.srv.URL
	})
	return a, g
}

func googleBody(cred string, extra string) string {
	b, _ := json.Marshal(cred)
	return `{"googleClientId":"gid-1","googleCredential":` + string(b) + extra + `}`
}

func TestHTTPGoogleLogin(t *testing.T) {
	a, g := newGoogleAPI(t)
	r := a.do("POST", "/auth/google", googleBody(g.credential(t, nil), `,"clientId":"`+a.seed.Client.ID+`","scope":"openid","redirectUri":"`+redirectURI+`"`), nil)
	if r.status != 200 || r.json()["code"] == nil {
		t.Fatalf("hợp lệ: %d %s", r.status, r.body)
	}
	if !strings.Contains(r.hdr.Get("Set-Cookie"), "medplum-"+a.seed.Client.ID) {
		t.Errorf("cookie đăng nhập: %q", r.hdr.Get("Set-Cookie"))
	}
	lg := a.storedLogin(r.json()["login"].(string))
	if lg.AuthMethod != "google" {
		t.Errorf("authMethod: %s", lg.AuthMethod)
	}
	// code đổi được token
	tk := a.exchange(url.Values{"code": {r.json()["code"].(string)}, "client_id": {a.seed.Client.ID}, "redirect_uri": {redirectURI}, "code_verifier": {verifier}}, nil)
	_ = tk // PKCE tùy client; chỉ kiểm không panic, luồng đổi code đã có test riêng
}

func TestHTTPGoogleLoginRejections(t *testing.T) {
	a, g := newGoogleAPI(t)
	text := func(r resp) string { s, _ := firstIssue(t, r); return s }
	if r := a.do("POST", "/auth/google", `{}`, nil); r.status != 400 || len(issues(r)) != 2 {
		t.Errorf("thiếu trường: %d %s", r.status, r.body)
	}
	if r := a.do("POST", "/auth/google", googleBody("x", `,"redirectUri":5`), nil); r.status != 400 || text(r) != "Invalid redirectUri" {
		t.Errorf("redirectUri: %d %s", r.status, r.body)
	}
	if r := a.do("POST", "/auth/google", googleBody("rac", ""), nil); r.status != 400 || text(r) != "Failed to verify Google credential" {
		t.Errorf("credential rác: %d %s", r.status, r.body)
	}
	if r := a.do("POST", "/auth/google", googleBody(g.credential(t, jwt.MapClaims{"email_verified": false}), ""), nil); r.status != 400 || text(r) != "Email address is not verified by Google" {
		t.Errorf("chưa xác minh: %d %s", r.status, r.body)
	}
	if r := a.do("POST", "/auth/google", googleBody(g.credential(t, jwt.MapClaims{"aud": "khac"}), ""), nil); r.status != 400 || text(r) != "Failed to verify Google credential" {
		t.Errorf("sai aud: %d %s", r.status, r.body)
	}
	b := `{"googleClientId":"la","googleCredential":"` + g.credential(t, nil) + `"}`
	if r := a.do("POST", "/auth/google", b, nil); r.status != 400 || text(r) != "Invalid googleClientId" {
		t.Errorf("client lạ: %d %s", r.status, r.body)
	}
	if r := a.do("POST", "/auth/google", googleBody(g.credential(t, jwt.MapClaims{"email": "moi@example.org"}), ""), nil); r.status != 400 || text(r) != "User not found" {
		t.Errorf("user lạ không createUser: %d %s", r.status, r.body)
	}
}

func TestHTTPGoogleRouteAbsentWhenNotConfigured(t *testing.T) {
	a := newCodeAPI(t, nil)
	if r := a.do("POST", "/auth/google", googleBody("x", ""), nil); r.status != 404 && r.status != 405 {
		t.Errorf("route phải vắng: %d %s", r.status, r.body)
	}
	_ = base64.StdEncoding
}
