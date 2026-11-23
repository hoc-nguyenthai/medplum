package oidc

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
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
	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
)

var (
	_ application.OIDCClient      = (*Client)(nil)
	_ application.IDTokenVerifier = (*IDVerifier)(nil)
)

var ctx = context.Background()

type signer struct {
	key *ecdsa.PrivateKey
	kid string
}

func newSigner(t *testing.T, kid string) signer {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return signer{k, kid}
}

func (s signer) jwk() jose.JSONWebKey {
	return jose.JSONWebKey{Key: &s.key.PublicKey, KeyID: s.kid, Algorithm: "ES256", Use: "sig"}
}

func (s signer) token(t *testing.T, claims jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodES256, claims)
	tok.Header["kid"] = s.kid
	out, err := tok.SignedString(s.key)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func jwksServer(t *testing.T, hits *atomic.Int32, keys func() []jose.JSONWebKey) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if hits != nil {
			hits.Add(1)
		}
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: keys()})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func good(extra jwt.MapClaims) jwt.MapClaims {
	c := jwt.MapClaims{"iss": "https://idp.example.org", "aud": "cid", "sub": "s1", "email": "an@example.org", "exp": time.Now().Add(time.Hour).Unix()}
	for k, v := range extra {
		c[k] = v
	}
	return c
}

func TestVerifyJWKSAcceptsAValidToken(t *testing.T) {
	s := newSigner(t, "k1")
	srv := jwksServer(t, nil, func() []jose.JSONWebKey { return []jose.JSONWebKey{s.jwk()} })
	v := NewIDVerifier(true)
	claims, err := v.VerifyJWKS(ctx, s.token(t, good(nil)), srv.URL, "https://idp.example.org", "cid")
	if err != nil || claims["email"] != "an@example.org" || claims["sub"] != "s1" {
		t.Fatalf("%v %v", claims, err)
	}
	// aud là mảng chứa client cũng hợp lệ
	if _, err := v.VerifyJWKS(ctx, s.token(t, good(jwt.MapClaims{"aud": []string{"khac", "cid"}})), srv.URL, "https://idp.example.org", "cid"); err != nil {
		t.Errorf("aud là mảng: %v", err)
	}
}

func TestVerifyJWKSRejectsEverythingElse(t *testing.T) {
	s, other := newSigner(t, "k1"), newSigner(t, "k1") // cùng kid, khác khóa
	srv := jwksServer(t, nil, func() []jose.JSONWebKey { return []jose.JSONWebKey{s.jwk()} })
	v := NewIDVerifier(true)
	hs := func() string {
		tok := jwt.NewWithClaims(jwt.SigningMethodHS256, good(nil))
		tok.Header["kid"] = "k1"
		out, _ := tok.SignedString([]byte("bi-mat-chung"))
		return out
	}
	none := func() string {
		tok := jwt.NewWithClaims(jwt.SigningMethodNone, good(nil))
		out, _ := tok.SignedString(jwt.UnsafeAllowNoneSignatureType)
		return out
	}
	for name, tok := range map[string]string{
		"sai iss":                    s.token(t, good(jwt.MapClaims{"iss": "https://evil.example.org"})),
		"thiếu iss":                  s.token(t, jwt.MapClaims{"aud": "cid", "exp": time.Now().Add(time.Hour).Unix()}),
		"sai aud":                    s.token(t, good(jwt.MapClaims{"aud": "ung-dung-khac"})),
		"thiếu aud":                  s.token(t, jwt.MapClaims{"iss": "https://idp.example.org", "exp": time.Now().Add(time.Hour).Unix()}),
		"hết hạn":                    s.token(t, good(jwt.MapClaims{"exp": time.Now().Add(-time.Minute).Unix()})),
		"thiếu exp":                  s.token(t, jwt.MapClaims{"iss": "https://idp.example.org", "aud": "cid"}),
		"chưa tới hạn (nbf)":         s.token(t, good(jwt.MapClaims{"nbf": time.Now().Add(time.Hour).Unix()})),
		"ký bằng khóa khác cùng kid": other.token(t, good(nil)),
		"kid lạ":                     newSigner(t, "khong-co").token(t, good(nil)),
		"HS256 (tấn công đổi thuật toán)": hs(),
		"alg none":       none(),
		"không phải JWT": "khong.phai.jwt",
		"rỗng":           "",
	} {
		if claims, err := v.VerifyJWKS(ctx, tok, srv.URL, "https://idp.example.org", "cid"); err == nil {
			t.Errorf("%s: phải bị từ chối, được %v", name, claims)
		}
	}
}

func TestVerifyJWKSRefetchesOnUnknownKidButNotTooOften(t *testing.T) {
	old, rotated := newSigner(t, "old"), newSigner(t, "new")
	var hits atomic.Int32
	var current atomic.Pointer[[]jose.JSONWebKey]
	set := []jose.JSONWebKey{old.jwk()}
	current.Store(&set)
	srv := jwksServer(t, &hits, func() []jose.JSONWebKey { return *current.Load() })
	now := time.Now()
	v := NewIDVerifier(true)
	v.now = func() time.Time { return now }
	if _, err := v.VerifyJWKS(ctx, old.token(t, good(nil)), srv.URL, "https://idp.example.org", "cid"); err != nil {
		t.Fatal(err)
	}
	// dùng lại token cũ: từ cache, không tải lại
	if _, err := v.VerifyJWKS(ctx, old.token(t, good(nil)), srv.URL, "https://idp.example.org", "cid"); err != nil || hits.Load() != 1 {
		t.Fatalf("phải dùng cache: hits=%d %v", hits.Load(), err)
	}
	// IdP xoay khóa: kid mới chưa có trong cache nhưng đã quá 30 giây kể từ lần tải: tải lại một lần
	set2 := []jose.JSONWebKey{old.jwk(), rotated.jwk()}
	current.Store(&set2)
	now = now.Add(time.Minute)
	if _, err := v.VerifyJWKS(ctx, rotated.token(t, good(nil)), srv.URL, "https://idp.example.org", "cid"); err != nil || hits.Load() != 2 {
		t.Fatalf("phải tải lại khi gặp kid mới: hits=%d %v", hits.Load(), err)
	}
	// kid lạ liên tục: không tải lại thường xuyên hơn 30 giây (không cho kẻ gọi ép auth tải JWKS vô hạn)
	stranger := newSigner(t, "la")
	for i := 0; i < 5; i++ {
		_, _ = v.VerifyJWKS(ctx, stranger.token(t, good(nil)), srv.URL, "https://idp.example.org", "cid")
	}
	if hits.Load() != 2 {
		t.Errorf("không được tải lại liên tục: hits=%d", hits.Load())
	}
}

func TestVerifyJWKSRefusesUnsafeJWKSLocationsWhenNotAllowed(t *testing.T) {
	s := newSigner(t, "k1")
	srv := jwksServer(t, nil, func() []jose.JSONWebKey { return []jose.JSONWebKey{s.jwk()} }) // http + 127.0.0.1
	strict := NewIDVerifier(false)
	if _, err := strict.VerifyJWKS(ctx, s.token(t, good(nil)), srv.URL, "https://idp.example.org", "cid"); err == nil {
		t.Error("jwksUrl http/nội bộ phải bị từ chối khi không bật chế độ không an toàn")
	}
}

func TestParseUnsigned(t *testing.T) {
	s := newSigner(t, "k1")
	v := NewIDVerifier(true)
	claims, err := v.ParseUnsigned(s.token(t, good(jwt.MapClaims{"exp": time.Now().Add(-time.Hour).Unix()})))
	if err != nil || claims["email"] != "an@example.org" {
		t.Errorf("chỉ giải mã, không kiểm hạn: %v %v", claims, err)
	}
	if _, err := v.ParseUnsigned("khong-phai-jwt"); err == nil {
		t.Error("không phải JWT phải lỗi")
	}
}

// ---- ExchangeCode

type tokenCall struct {
	auth, ctype, accept, enc string
	form                     url.Values
}

func tokenServer(t *testing.T, calls *[]tokenCall, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		*calls = append(*calls, tokenCall{r.Header.Get("Authorization"), r.Header.Get("Content-Type"), r.Header.Get("Accept"), r.Header.Get("Accept-Encoding"), r.PostForm})
		if r.Method != http.MethodPost || r.URL.RawQuery != "" {
			t.Errorf("phải POST và không có query: %s %q", r.Method, r.URL.RawQuery)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestExchangeCodeBasicAuthAndForm(t *testing.T) {
	var calls []tokenCall
	srv := tokenServer(t, &calls, 200, `{"id_token":"tok.en.sig","access_token":"khong-dung"}`)
	c := NewClient(true)
	idp := &fhir.IdentityProvider{TokenUrl: srv.URL, ClientId: "cid", ClientSecret: "bi:mat"}
	tok, err := c.ExchangeCode(ctx, idp, application.ExchangeCodeInput{RedirectURI: "https://auth/auth/external", Code: "c0de", CodeVerifier: "ver"})
	if err != nil || tok != "tok.en.sig" || len(calls) != 1 {
		t.Fatalf("%q %v %d", tok, err, len(calls))
	}
	call := calls[0]
	if want := "Basic " + base64.StdEncoding.EncodeToString([]byte("cid:bi:mat")); call.auth != want {
		t.Errorf("Basic: %q", call.auth)
	}
	if call.ctype != "application/x-www-form-urlencoded" || call.accept != "application/json" || call.enc != "identity" {
		t.Errorf("header: %+v", call)
	}
	f := call.form
	if f.Get("grant_type") != "authorization_code" || f.Get("code") != "c0de" || f.Get("redirect_uri") != "https://auth/auth/external" || f.Get("code_verifier") != "ver" ||
		f.Has("client_secret") || f.Has("client_id") {
		t.Errorf("form: %v", f)
	}
	// không có verifier thì không gửi code_verifier
	calls = nil
	if _, err := c.ExchangeCode(ctx, idp, application.ExchangeCodeInput{RedirectURI: "r", Code: "c"}); err != nil || calls[0].form.Has("code_verifier") {
		t.Errorf("không verifier: %v %v", err, calls[0].form)
	}
}

func TestExchangeCodeClientSecretPost(t *testing.T) {
	var calls []tokenCall
	srv := tokenServer(t, &calls, 200, `{"id_token":"a.b.c"}`)
	idp := &fhir.IdentityProvider{TokenUrl: srv.URL, ClientId: "cid", ClientSecret: "sec", TokenAuthMethod: "client_secret_post"}
	if _, err := NewClient(true).ExchangeCode(ctx, idp, application.ExchangeCodeInput{RedirectURI: "r", Code: "c"}); err != nil {
		t.Fatal(err)
	}
	if calls[0].auth != "" || calls[0].form.Get("client_id") != "cid" || calls[0].form.Get("client_secret") != "sec" {
		t.Errorf("%+v", calls[0])
	}
}

func TestExchangeCodeErrors(t *testing.T) {
	const generic = "Failed to verify code - check your identity provider configuration"
	for name, tc := range map[string]struct {
		status int
		body   string
		idp    func(url string) *fhir.IdentityProvider
		want   string
	}{
		"IdP trả 400":               {400, `{"error":"invalid_grant","error_description":"BI-MAT-CUA-IDP"}`, nil, generic},
		"IdP trả 500":               {500, `oops`, nil, generic},
		"JSON hỏng":                 {200, `<html>`, nil, generic},
		"thiếu id_token":            {200, `{"access_token":"x"}`, nil, "Missing id_token in external identity provider response"},
		"id_token không phải chuỗi": {200, `{"id_token":123}`, nil, "Missing id_token in external identity provider response"},
		"id_token rỗng":             {200, `{"id_token":""}`, nil, "Missing id_token in external identity provider response"},
		"phản hồi quá lớn":          {200, `{"id_token":"` + strings.Repeat("a", maxResponse) + `"}`, nil, generic},
		"thiếu tokenUrl":            {200, `{}`, func(string) *fhir.IdentityProvider { return &fhir.IdentityProvider{ClientId: "c", ClientSecret: "s"} }, "Missing token URL for external identity provider"},
		"thiếu client id":           {200, `{}`, func(u string) *fhir.IdentityProvider { return &fhir.IdentityProvider{TokenUrl: u, ClientSecret: "s"} }, "Missing client ID or client secret for external identity provider"},
		"thiếu client secret":       {200, `{}`, func(u string) *fhir.IdentityProvider { return &fhir.IdentityProvider{TokenUrl: u, ClientId: "c"} }, "Missing client ID or client secret for external identity provider"},
		"thiếu secret (post)": {200, `{}`, func(u string) *fhir.IdentityProvider {
			return &fhir.IdentityProvider{TokenUrl: u, ClientId: "c", TokenAuthMethod: "client_secret_post"}
		}, "Missing client ID or client secret for external identity provider"},
		"tokenUrl hỏng": {200, `{}`, func(string) *fhir.IdentityProvider {
			return &fhir.IdentityProvider{TokenUrl: "không phải url", ClientId: "c", ClientSecret: "s"}
		}, generic},
	} {
		var calls []tokenCall
		srv := tokenServer(t, &calls, tc.status, tc.body)
		idp := &fhir.IdentityProvider{TokenUrl: srv.URL, ClientId: "cid", ClientSecret: "sec"}
		if tc.idp != nil {
			idp = tc.idp(srv.URL)
		}
		_, err := NewClient(true).ExchangeCode(ctx, idp, application.ExchangeCodeInput{RedirectURI: "r", Code: "c"})
		if !domain.Is(err, domain.KindBadRequest, tc.want) {
			t.Errorf("%s: %v, mong %q", name, err, tc.want)
		} else if strings.Contains(err.Error(), "BI-MAT") {
			t.Errorf("%s: lỗi lộ nội dung phản hồi của IdP", name)
		}
	}
}

func TestExchangeCodeNeverReachesInternalOrPlainHTTPUnlessAllowed(t *testing.T) {
	var calls []tokenCall
	srv := tokenServer(t, &calls, 200, `{"id_token":"a.b.c"}`) // http://127.0.0.1
	idp := &fhir.IdentityProvider{TokenUrl: srv.URL, ClientId: "c", ClientSecret: "s"}
	if _, err := NewClient(false).ExchangeCode(ctx, idp, application.ExchangeCodeInput{RedirectURI: "r", Code: "c"}); !domain.Is(err, domain.KindBadRequest, "Failed to verify code - check your identity provider configuration") {
		t.Errorf("http/nội bộ phải bị từ chối: %v", err)
	}
	if len(calls) != 0 {
		t.Error("không được gọi tới địa chỉ nội bộ")
	}
}

func TestExchangeCodeDoesNotFollowRedirects(t *testing.T) {
	var hits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`{"id_token":"a.b.c"}`))
	}))
	defer target.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	idp := &fhir.IdentityProvider{TokenUrl: srv.URL, ClientId: "c", ClientSecret: "s"}
	if _, err := NewClient(true).ExchangeCode(ctx, idp, application.ExchangeCodeInput{RedirectURI: "r", Code: "c"}); err == nil || hits.Load() != 0 {
		t.Errorf("không được theo chuyển hướng (secret không được gửi sang nơi khác): %v hits=%d", err, hits.Load())
	}
}
