package e2e

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/golang-jwt/jwt/v5"
)

// idpServer là một OIDC IdP thật (httptest) dùng cho e2e: /token đổi code lấy id_token ký ES256, /jwks công bố khóa.
type idpServer struct {
	srv   *httptest.Server
	key   *ecdsa.PrivateKey
	email string
	calls int
}

func newIdP(t *testing.T, email string) *idpServer {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	i := &idpServer{key: k, email: email}
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		i.calls++
		tok := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{"iss": i.srv.URL, "aud": "idp-client", "sub": "idp-sub", "email": i.email, "email_verified": true, "exp": time.Now().Add(time.Hour).Unix()})
		tok.Header["kid"] = "k1"
		signed, _ := tok.SignedString(i.key)
		_ = json.NewEncoder(w).Encode(map[string]any{"id_token": signed})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &i.key.PublicKey, KeyID: "k1", Algorithm: "ES256", Use: "sig"}}})
	})
	i.srv = httptest.NewServer(mux)
	t.Cleanup(i.srv.Close)
	return i
}

// Đăng nhập qua IdP ngoài trên tiến trình thật: `auth domain add` (CLI) cấu hình IdP, /auth/method phát state ký, callback đổi code ở IdP và xác minh
// id_token bằng JWKS, rồi đổi code của auth lấy token và gọi fhir-server.
func TestExternalLoginThroughRealProcesses(t *testing.T) {
	idp := newIdP(t, "a@example.org") // admin của project Alpha (seed)
	s := newStackCustom(t, false, map[string]string{"AUTH_ALLOW_PRIVATE_JWKS": "true"}, func(authBin string, authEnv map[string]string) {
		cmd := exec.Command(authBin, "domain", "add", "-domain", "example.org", "-authorize-url", idp.srv.URL+"/authorize", "-token-url", idp.srv.URL+"/token",
			"-jwks-url", idp.srv.URL+"/jwks", "-issuer", idp.srv.URL, "-client-id", "idp-client")
		env := map[string]string{"AUTH_DOMAIN_IDP_CLIENT_SECRET": "idp-secret"}
		for k, v := range authEnv {
			env[k] = v
		}
		cmd.Env = envList(env)
		if out, err := cmd.CombinedOutput(); err != nil || !strings.Contains(string(out), "domain: example.org") || strings.Contains(string(out), "idp-secret") {
			t.Fatalf("domain add: %v\n%s", err, out)
		}
	})

	// /auth/method: nhận ra tên miền và phát state ký kèm cookie liên kết trình duyệt
	m := s.postJSON("/auth/method", map[string]any{"email": "A@Example.org", "clientId": s.clientA, "scope": "openid offline_access", "codeChallenge": challengeS256(), "codeChallengeMethod": "S256"})
	if m.status != 200 || m.json(t)["domain"] != "example.org" {
		t.Fatalf("method: %d %s", m.status, m.body)
	}
	cookie := ""
	for _, c := range m.hdr.Values("Set-Cookie") {
		if strings.HasPrefix(c, "auth-external-state=") {
			cookie = strings.SplitN(strings.TrimPrefix(c, "auth-external-state="), ";", 2)[0]
		}
	}
	authorize, _ := url.Parse(m.json(t)["authorizeUrl"].(string))
	state := authorize.Query().Get("state")
	if cookie == "" || state == "" || authorize.Query().Get("client_id") != "idp-client" || authorize.Query().Get("redirect_uri") != s.auth+"/auth/external" {
		t.Fatalf("authorizeUrl=%s cookie=%q", authorize, cookie)
	}
	callback := func(withCookie string) resp {
		req, _ := http.NewRequest("GET", s.auth+"/auth/external?code=ma-code&state="+url.QueryEscape(state), nil)
		if withCookie != "" {
			req.Header.Set("Cookie", "auth-external-state="+withCookie)
		}
		res, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Timeout: 15 * time.Second}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		return resp{status: res.StatusCode, hdr: res.Header}
	}
	// trình duyệt khác (không có cookie) không dùng được state bị lộ
	if r := callback(""); r.status != 400 || idp.calls != 0 {
		t.Fatalf("không cookie: %d, IdP đã bị gọi %d lần", r.status, idp.calls)
	}
	r := callback(cookie)
	if r.status != 302 {
		t.Fatalf("callback: %d", r.status)
	}
	loc, _ := url.Parse(r.hdr.Get("Location"))
	login := loc.Query().Get("login")
	if loc.Host != "localhost:3000" || loc.Path != "/signin" || login == "" || loc.Query().Get("code_challenge") != challengeS256() {
		t.Fatalf("Location: %s", r.hdr.Get("Location"))
	}
	// app hoàn tất: hỏi trạng thái login để lấy code (đã có membership vì chỉ một profile), rồi đổi lấy token
	st := do(t, "GET", s.auth+"/auth/login/"+login, "", "", nil)
	code, _ := st.json(t)["code"].(string)
	if st.status != 200 || code == "" {
		t.Fatalf("status: %d %s", st.status, st.body)
	}
	tr := s.token(url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {s.clientA}, "code_verifier": {verifier}})
	if tr.status != 200 {
		t.Fatalf("đổi code: %d %s", tr.status, tr.body)
	}
	if created := s.fhirPost(tr.json(t)["access_token"].(string), "/Patient", patientJSON); created.status != 201 {
		t.Fatalf("fhir-server với token từ đăng nhập ngoài: %d %s", created.status, created.body)
	}

	// IdP trả email của người KHÁC tên miền: từ chối, không có Login mới
	idp.email = "ke-tan-cong@khac.example.org"
	m2 := s.postJSON("/auth/method", map[string]any{"email": "a@example.org", "clientId": s.clientA})
	cookie2 := ""
	for _, c := range m2.hdr.Values("Set-Cookie") {
		if strings.HasPrefix(c, "auth-external-state=") {
			cookie2 = strings.SplitN(strings.TrimPrefix(c, "auth-external-state="), ";", 2)[0]
		}
	}
	u2, _ := url.Parse(m2.json(t)["authorizeUrl"].(string))
	state = u2.Query().Get("state")
	if r := callback(cookie2); r.status != 400 {
		t.Errorf("email khác tên miền: %d", r.status)
	}
}
