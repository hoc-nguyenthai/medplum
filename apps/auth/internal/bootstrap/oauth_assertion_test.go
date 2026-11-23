package bootstrap

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/golang-jwt/jwt/v5"

	"trivita-fhir/apps/auth/internal/application"
	"trivita-fhir/apps/auth/internal/config"
	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirrouter"
)

// jwksServer là jwks_uri của client: danh sách khóa đổi được, đếm số lần bị tải.
type jwksServer struct {
	srv  *httptest.Server
	mu   sync.Mutex
	keys []jose.JSONWebKey
	hits atomic.Int32
}

func newJWKS(t *testing.T) *jwksServer {
	t.Helper()
	j := &jwksServer{}
	j.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		j.hits.Add(1)
		j.mu.Lock()
		defer j.mu.Unlock()
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: j.keys})
	}))
	t.Cleanup(j.srv.Close)
	return j
}

func (j *jwksServer) set(keys ...jose.JSONWebKey) {
	j.mu.Lock()
	j.keys = keys
	j.mu.Unlock()
}

func (j *jwksServer) url() string { return j.srv.URL + "/jwks.json" }

type testKey struct {
	priv   any
	method jwt.SigningMethod
	pub    jose.JSONWebKey
}

func ecKey(t *testing.T, kid string) testKey {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return testKey{k, jwt.SigningMethodES256, jose.JSONWebKey{Key: &k.PublicKey, KeyID: kid, Algorithm: "ES256", Use: "sig"}}
}

func rsaKey(t *testing.T, kid string) testKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return testKey{k, jwt.SigningMethodRS256, jose.JSONWebKey{Key: &k.PublicKey, KeyID: kid, Algorithm: "RS256", Use: "sig"}}
}

type assertionAPI struct {
	*codeAPI
	jwks *jwksServer
}

// newAssertionAPI: client mặc định không còn secret, jwks_uri trỏ vào máy chủ JWKS giả (cho phép dải nội bộ ở môi trường test).
func newAssertionAPI(t *testing.T, mut func(*config.Settings)) *assertionAPI {
	t.Helper()
	a := newCodeAPI(t, func(s *config.Settings) {
		s.AllowPrivateJWKS = true
		if mut != nil {
			mut(s)
		}
	})
	j := newJWKS(t)
	c := *a.seed.Client
	secret := c.Secret
	c.Secret, c.JwksUri = "", j.url()
	updated, err := a.c.Store.UpdateResource(context.Background(), &c, fhirrouter.UpdateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	a.seed.Client = updated.(*fhir.ClientApplication)
	a.seed.Client.Secret = secret
	return &assertionAPI{codeAPI: a, jwks: j}
}

func (a *assertionAPI) tokenURL() string { return a.c.Settings.BaseURL + "/oauth2/token" }

// assertion dựng client_assertion hợp lệ rồi áp mut để làm hỏng một thứ.
func (a *assertionAPI) assertion(k testKey, mut func(jwt.MapClaims, *jwt.Token)) string {
	a.t.Helper()
	claims := jwt.MapClaims{"iss": a.seed.Client.ID, "sub": a.seed.Client.ID, "aud": a.tokenURL(), "exp": time.Now().Add(2 * time.Minute).Unix(), "jti": domain.NewID()}
	tok := jwt.NewWithClaims(k.method, claims)
	tok.Header["kid"] = k.pub.KeyID
	if mut != nil {
		mut(claims, tok)
	}
	raw, err := tok.SignedString(k.priv)
	if err != nil {
		a.t.Fatal(err)
	}
	return raw
}

func (a *assertionAPI) post(grant string, assertion string, extra url.Values) resp {
	a.t.Helper()
	v := url.Values{"grant_type": {grant}, "client_assertion_type": {application.ClientAssertionTypeJWTBearer}, "client_assertion": {assertion}}
	for k, vals := range extra {
		v[k] = vals
	}
	return a.form("/oauth2/token", v, nil)
}

func TestPrivateKeyJWTAuthenticatesEveryGrantWithoutASecret(t *testing.T) {
	for name, key := range map[string]func(*testing.T, string) testKey{"ES256": ecKey, "RS256": rsaKey} {
		a := newAssertionAPI(t, nil)
		k := key(t, "k1")
		a.jwks.set(k.pub)

		// client_credentials: client không có secret vẫn xác thực được (không bắt chước ràng buộc token.ts:1071)
		r := a.post("client_credentials", a.assertion(k, nil), nil)
		if r.status != 200 {
			t.Fatalf("%s client_credentials: %d %s", name, r.status, r.body)
		}
		if p, err := a.verifier().Verify(context.Background(), r.json()["access_token"].(string)); err != nil || p.Subject != a.seed.Client.ID {
			t.Errorf("%s: fhirauth: %+v %v", name, p, err)
		}
		// authorization_code: Login không PKCE, chỉ client đã xác thực bằng assertion mới đổi được
		code, _, _ := a.loginCode(loginOpts{scope: "openid offline_access"})
		wantOAuthError(t, a.exchange(url.Values{"code": {code}}, nil), "invalid_request", "Missing verification context")
		tk := a.post("authorization_code", a.assertion(k, nil), url.Values{"code": {code}})
		if tk.status != 200 || tk.json()["refresh_token"] == nil {
			t.Fatalf("%s authorization_code: %d %s", name, tk.status, tk.body)
		}
		// refresh_token: assertion được chấp nhận (client không có secret nên không có gì để "bắt buộc", nhưng assertion sai vẫn bị từ chối)
		refresh := tk.json()["refresh_token"].(string)
		if bad := a.post("refresh_token", a.assertion(ecKey(t, "k1"), nil), url.Values{"refresh_token": {refresh}}); bad.status != 400 {
			t.Errorf("%s: assertion sai chữ ký phải bị từ chối: %d", name, bad.status)
		}
		if rr := a.post("refresh_token", a.assertion(k, nil), url.Values{"refresh_token": {refresh}}); rr.status != 200 {
			t.Errorf("%s refresh_token: %d %s", name, rr.status, rr.body)
		}
	}
}

func TestPrivateKeyJWTRejectsBadAssertions(t *testing.T) {
	a := newAssertionAPI(t, nil)
	k := ecKey(t, "k1")
	a.jwks.set(k.pub)
	other := ecKey(t, "k1") // cùng kid, khóa khác
	rs := rsaKey(t, "k1")
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"aud sai", a.assertion(k, func(c jwt.MapClaims, _ *jwt.Token) { c["aud"] = "http://khac/oauth2/token" }), "Invalid client assertion audience"},
		{"aud là mảng", a.assertion(k, func(c jwt.MapClaims, _ *jwt.Token) { c["aud"] = []string{a.tokenURL()} }), "Invalid client assertion audience"},
		{"iss khác sub", a.assertion(k, func(c jwt.MapClaims, _ *jwt.Token) { c["sub"] = "ai-do" }), "Invalid client assertion issuer"},
		{"không có iss", a.assertion(k, func(c jwt.MapClaims, _ *jwt.Token) { delete(c, "iss"); delete(c, "sub") }), "Invalid client assertion issuer"},
		{"client không tồn tại", a.assertion(k, func(c jwt.MapClaims, _ *jwt.Token) { c["iss"], c["sub"] = "khong-co", "khong-co" }), "Client not found"},
		{"chữ ký sai", a.assertion(other, nil), "Invalid client assertion signature"},
		{"kid lạ", a.assertion(k, func(_ jwt.MapClaims, tk *jwt.Token) { tk.Header["kid"] = "khac" }), "Invalid client assertion signature"},
		{"hết hạn", a.assertion(k, func(c jwt.MapClaims, _ *jwt.Token) { c["exp"] = time.Now().Add(-time.Minute).Unix() }), "Invalid client assertion"},
		{"không có exp", a.assertion(k, func(c jwt.MapClaims, _ *jwt.Token) { delete(c, "exp") }), "Invalid client assertion"},
		{"exp quá xa", a.assertion(k, func(c jwt.MapClaims, _ *jwt.Token) { c["exp"] = time.Now().Add(time.Hour).Unix() }), "Client assertion expiration is too far in the future"},
		{"không có jti", a.assertion(k, func(c jwt.MapClaims, _ *jwt.Token) { delete(c, "jti") }), "Client assertion must have a jti"},
		{"thuật toán RS256 với khóa EC", a.assertion(rs, nil), "Invalid client assertion signature"},
		{"rác", "abc.def", "Invalid client assertion"},
		{"rỗng", "", "Invalid client assertion"},
	}
	for _, c := range cases {
		if r := a.post("client_credentials", c.raw, nil); r.status != 400 || r.json()["error"] != "invalid_request" || r.json()["error_description"] != c.want {
			t.Errorf("%s: %d %s, muốn %q", c.name, r.status, r.body, c.want)
		}
	}
	// HS256 ký bằng một chuỗi bất kỳ (tấn công nhầm thuật toán)
	hs := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"iss": a.seed.Client.ID, "sub": a.seed.Client.ID, "aud": a.tokenURL(), "exp": time.Now().Add(time.Minute).Unix(), "jti": "x"})
	hs.Header["kid"] = "k1"
	hsRaw, _ := hs.SignedString([]byte("bi-mat"))
	wantOAuthError(t, a.post("client_credentials", hsRaw, nil), "invalid_request", "Invalid client assertion")
	// loại assertion sai
	wantOAuthError(t, a.form("/oauth2/token", url.Values{"grant_type": {"client_credentials"}, "client_assertion_type": {"x"}, "client_assertion": {"y"}}, nil), "invalid_request", "Unsupported client assertion type")
	// không có jwks_uri
	c := *a.seed.Client
	c.JwksUri = ""
	if _, err := a.c.Store.UpdateResource(context.Background(), &c, fhirrouter.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	wantOAuthError(t, a.post("client_credentials", a.assertion(k, nil), nil), "invalid_request", "Client must have a JWK Set URL")
	if a.loginCount() != 0 {
		t.Errorf("assertion bị từ chối không được tạo Login, có %d", a.loginCount())
	}
}

func TestPrivateKeyJWTJTIIsSingleUseAndOnlyBurntAfterValidSignature(t *testing.T) {
	a := newAssertionAPI(t, nil)
	k := ecKey(t, "k1")
	a.jwks.set(k.pub)
	// chữ ký sai không làm cháy jti: kẻ không có khóa không chặn được client thật
	bad := ecKey(t, "k1")
	wantOAuthError(t, a.post("client_credentials", a.assertion(bad, func(c jwt.MapClaims, _ *jwt.Token) { c["jti"] = "j-1" }), nil), "invalid_request", "Invalid client assertion signature")
	good := a.assertion(k, func(c jwt.MapClaims, _ *jwt.Token) { c["jti"] = "j-1" })
	if r := a.post("client_credentials", good, nil); r.status != 200 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	wantOAuthError(t, a.post("client_credentials", good, nil), "invalid_request", "Client assertion already used")
	// jti khác dùng được
	if r := a.post("client_credentials", a.assertion(k, nil), nil); r.status != 200 {
		t.Errorf("%d %s", r.status, r.body)
	}
}

func TestPrivateKeyJWTConcurrentReplayYieldsOneToken(t *testing.T) {
	a := newAssertionAPI(t, func(s *config.Settings) { s.DatabaseMaxOpenConns = 1 })
	k := ecKey(t, "k1")
	a.jwks.set(k.pub)
	raw := a.assertion(k, nil)
	var wg sync.WaitGroup
	var ok atomic.Int32
	start := make(chan struct{})
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if a.post("client_credentials", raw, nil).status == 200 {
				ok.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	if ok.Load() != 1 {
		t.Errorf("phát lại song song: %d thành công, muốn 1", ok.Load())
	}
}

func TestPrivateKeyJWTMultipleKeysWithSameKid(t *testing.T) {
	a := newAssertionAPI(t, nil)
	k := ecKey(t, "dup")
	a.jwks.set(ecKey(t, "dup").pub, ecKey(t, "dup").pub, k.pub, ecKey(t, "khac").pub) // khóa đúng là khóa thứ 3 trùng kid
	if r := a.post("client_credentials", a.assertion(k, nil), nil); r.status != 200 {
		t.Fatalf("thử tới khóa thứ 3: %d %s", r.status, r.body)
	}
	// lỗi khác chữ ký (hết hạn) dừng ngay, không sang khóa kế
	expired := a.assertion(k, func(c jwt.MapClaims, _ *jwt.Token) { c["exp"] = time.Now().Add(-time.Minute).Unix() })
	wantOAuthError(t, a.post("client_credentials", expired, nil), "invalid_request", "Invalid client assertion")
}

func TestPrivateKeyJWTRefetchesJWKSOnUnknownKidButNotTooOften(t *testing.T) {
	a := newAssertionAPI(t, nil)
	k1 := ecKey(t, "k1")
	a.jwks.set(k1.pub)
	if r := a.post("client_credentials", a.assertion(k1, nil), nil); r.status != 200 {
		t.Fatal(r.body)
	}
	hits := a.jwks.hits.Load()
	// cache được dùng lại cho kid đã biết
	a.post("client_credentials", a.assertion(k1, nil), nil)
	if a.jwks.hits.Load() != hits {
		t.Errorf("không được tải lại JWKS khi cache còn hạn: %d -> %d", hits, a.jwks.hits.Load())
	}
	// client xoay khóa: kid lạ làm auth tải lại, nhưng không quá một lần mỗi 30 giây (không ép tải liên tục)
	k2 := ecKey(t, "k2")
	a.jwks.set(k1.pub, k2.pub)
	wantOAuthError(t, a.post("client_credentials", a.assertion(k2, nil), nil), "invalid_request", "Invalid client assertion signature")
	if a.jwks.hits.Load() != hits {
		t.Errorf("lần tải lại vừa xảy ra trong 30 giây trước nên chưa được tải: %d", a.jwks.hits.Load())
	}
}

func TestPrivateKeyJWTJWKSURLIsProtectedAgainstSSRF(t *testing.T) {
	// môi trường KHÔNG cho phép dải nội bộ: jwks_uri trỏ loopback, http, hoặc có userinfo đều bị chặn
	a := newCodeAPI(t, nil)
	k := ecKey(t, "k1")
	j := newJWKS(t)
	j.set(k.pub)
	for _, uri := range []string{j.url(), "http://example.org/jwks.json", "https://127.0.0.1/jwks.json", "https://localhost/jwks.json", "https://user:pw@example.org/jwks.json", "ftp://example.org/x", "https://169.254.169.254/latest/meta-data/"} {
		c := *a.seed.Client
		c.JwksUri = uri
		updated, err := a.c.Store.UpdateResource(context.Background(), &c, fhirrouter.UpdateOptions{})
		if err != nil {
			t.Fatal(err)
		}
		a.seed.Client = updated.(*fhir.ClientApplication)
		claims := jwt.MapClaims{"iss": a.seed.Client.ID, "sub": a.seed.Client.ID, "aud": a.c.Settings.BaseURL + "/oauth2/token", "exp": time.Now().Add(time.Minute).Unix(), "jti": domain.NewID()}
		tok := jwt.NewWithClaims(k.method, claims)
		tok.Header["kid"] = "k1"
		raw, _ := tok.SignedString(k.priv)
		r := a.form("/oauth2/token", url.Values{"grant_type": {"client_credentials"}, "client_assertion_type": {application.ClientAssertionTypeJWTBearer}, "client_assertion": {raw}}, nil)
		if r.status != 400 || r.json()["error_description"] != "Invalid client assertion" {
			t.Errorf("%s: %d %s", uri, r.status, r.body)
		}
	}
	if hits := j.hits.Load(); hits != 0 {
		t.Errorf("jwks_uri nội bộ không được bị kết nối tới (%d lần)", hits)
	}
}

func TestPrivateKeyJWTCannotBeCombinedWithOtherClientAuthentication(t *testing.T) {
	a := newAssertionAPI(t, nil)
	k := ecKey(t, "k1")
	a.jwks.set(k.pub)
	raw := a.assertion(k, nil)
	v := url.Values{"grant_type": {"client_credentials"}, "client_assertion_type": {application.ClientAssertionTypeJWTBearer}, "client_assertion": {raw}}
	wantOAuthError(t, a.form("/oauth2/token", v, basic(a.seed.Client.ID, "x")), "invalid_request", "Multiple client authentication methods")
	v.Set("client_secret", "x")
	wantOAuthError(t, a.form("/oauth2/token", v, nil), "invalid_request", "Multiple client authentication methods")
	v.Del("client_secret")
	v.Set("client_id", "client-khac")
	wantOAuthError(t, a.form("/oauth2/token", v, nil), "invalid_request", "Invalid client")
}

// Login của client A không đổi được bằng assertion của client B.
func TestPrivateKeyJWTCannotRedeemAnotherClientsLogin(t *testing.T) {
	a := newAssertionAPI(t, nil)
	k := ecKey(t, "k1")
	a.jwks.set(k.pub)
	code, _, _ := a.loginCode(loginOpts{})
	other, err := a.c.Auth.CreateClient(context.Background(), a.seed.Project.ID, application.NewClient{Name: "Hai"})
	if err != nil {
		t.Fatal(err)
	}
	j2 := newJWKS(t)
	k2 := ecKey(t, "k2")
	j2.set(k2.pub)
	oc := *other
	oc.JwksUri = j2.url()
	if _, err := a.c.Store.UpdateResource(context.Background(), &oc, fhirrouter.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	claims := jwt.MapClaims{"iss": other.ID, "sub": other.ID, "aud": a.tokenURL(), "exp": time.Now().Add(time.Minute).Unix(), "jti": domain.NewID()}
	tok := jwt.NewWithClaims(k2.method, claims)
	tok.Header["kid"] = "k2"
	raw, _ := tok.SignedString(k2.priv)
	wantOAuthError(t, a.post("authorization_code", raw, url.Values{"code": {code}}), "invalid_request", "Invalid client")
	// code vẫn dùng được bởi đúng client
	if r := a.post("authorization_code", a.assertion(k, nil), url.Values{"code": {code}}); r.status != 200 {
		t.Errorf("%d %s", r.status, r.body)
	}
}
