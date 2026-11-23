package oidc

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/go-jose/go-jose/v4"
	"github.com/golang-jwt/jwt/v5"

	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
)

const failedMsg = "Failed to verify code - check your identity provider configuration"

func userInfoServer(t *testing.T, status int, ctype, body string, seen *http.Header) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if seen != nil {
			*seen = r.Header.Clone()
		}
		if r.Method != http.MethodGet {
			t.Errorf("userinfo phải GET: %s", r.Method)
		}
		w.Header().Set("Content-Type", ctype)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestUserInfoJSON(t *testing.T) {
	var h http.Header
	srv := userInfoServer(t, 200, "application/json; charset=utf-8", `{"email":"a@b.c","sub":"1"}`, &h)
	idp := &fhir.IdentityProvider{UserInfoUrl: srv.URL}
	claims, err := NewClient(true).UserInfo(ctx, idp, "tok-123")
	if err != nil || claims["email"] != "a@b.c" {
		t.Fatalf("%v %v", claims, err)
	}
	if h.Get("Authorization") != "Bearer tok-123" || h.Get("Accept-Encoding") != "identity" {
		t.Errorf("header: %v", h)
	}
}

func TestUserInfoErrorsUseFixedMessages(t *testing.T) {
	c := NewClient(true)
	for name, tc := range map[string]struct {
		status      int
		ctype, body string
		want        string
	}{
		"500":             {500, "application/json", `{}`, failedMsg},
		"401":             {401, "application/json", `{"error":"bí mật"}`, failedMsg},
		"json hỏng":       {200, "application/json", `{`, failedMsg},
		"json null":       {200, "application/json", `null`, failedMsg},
		"content-type lạ": {200, "text/html", `<html>`, "Failed to verify code - unsupported content type"},
		"jwt không jwks":  {200, "application/jwt", "a.b.c", "Failed to verify code - unsigned user info response"},
		"quá lớn":         {200, "application/json", `{"x":"` + strings.Repeat("a", maxResponse) + `"}`, failedMsg},
		"jwt sai chữ ký":  {200, "application/jwt", "a.b.c", ""},
	} {
		srv := userInfoServer(t, tc.status, tc.ctype, tc.body, nil)
		idp := &fhir.IdentityProvider{UserInfoUrl: srv.URL}
		_, err := c.UserInfo(ctx, idp, "t")
		if err == nil {
			t.Errorf("%s: phải lỗi", name)
			continue
		}
		if tc.want != "" && !domain.Is(err, domain.KindBadRequest, tc.want) {
			t.Errorf("%s: %v", name, err)
		}
		if strings.Contains(err.Error(), "bí mật") {
			t.Errorf("%s: lộ nội dung IdP", name)
		}
	}
}

func TestUserInfo429AndModes(t *testing.T) {
	srv := userInfoServer(t, 429, "text/plain", "slow", nil)
	_, err := NewClient(true).UserInfo(ctx, &fhir.IdentityProvider{UserInfoUrl: srv.URL}, "t")
	if !domain.Is(err, domain.KindTooManyRequests, "") {
		t.Errorf("429: %v", err)
	}
	if _, err = NewClient(true).UserInfo(ctx, &fhir.IdentityProvider{}, "t"); !domain.Is(err, domain.KindBadRequest, "Missing user info URL") {
		t.Errorf("thiếu URL: %v", err)
	}
	if _, err = NewClient(true).UserInfo(ctx, &fhir.IdentityProvider{UserInfoUrl: srv.URL, UserInfoMode: "gcip"}, "t"); !domain.Is(err, domain.KindBadRequest, "Unsupported user info mode") {
		t.Errorf("gcip: %v", err)
	}
}

func TestUserInfoJWTMustBeSigned(t *testing.T) {
	s := newSigner(t, "k1")
	jwks := jwksServer(t, nil, func() []jose.JSONWebKey { return []jose.JSONWebKey{s.jwk()} })
	idp := func(srv *httptest.Server) *fhir.IdentityProvider {
		return &fhir.IdentityProvider{UserInfoUrl: srv.URL, JwksUrl: jwks.URL, Issuer: "https://idp.example.org", ClientId: "cid"}
	}
	c := NewClient(true)
	ok := userInfoServer(t, 200, "application/jwt", s.token(t, good(nil)), nil)
	claims, err := c.UserInfo(ctx, idp(ok), "t")
	if err != nil || claims["email"] != "an@example.org" {
		t.Fatalf("hợp lệ: %v %v", claims, err)
	}
	other := newSigner(t, "k1") // cùng kid, khóa khác
	forged := userInfoServer(t, 200, "application/jwt", other.token(t, good(nil)), nil)
	if _, err = c.UserInfo(ctx, idp(forged), "t"); !domain.Is(err, domain.KindBadRequest, failedMsg) {
		t.Errorf("chữ ký giả: %v", err)
	}
	wrongAud := userInfoServer(t, 200, "application/jwt", s.token(t, good(jwt.MapClaims{"aud": "khac"})), nil)
	if _, err = c.UserInfo(ctx, idp(wrongAud), "t"); err == nil {
		t.Error("aud sai phải lỗi")
	}
	none := jwt.NewWithClaims(jwt.SigningMethodNone, good(nil))
	raw, _ := none.SignedString(jwt.UnsafeAllowNoneSignatureType)
	unsigned := userInfoServer(t, 200, "application/jwt", raw, nil)
	if _, err = c.UserInfo(ctx, idp(unsigned), "t"); err == nil {
		t.Error("alg none phải bị từ chối")
	}
}

func TestUserInfoNeverReachesInternalUnlessAllowed(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits.Add(1); _, _ = w.Write([]byte(`{}`)) }))
	defer srv.Close()
	_, err := NewClient(false).UserInfo(ctx, &fhir.IdentityProvider{UserInfoUrl: srv.URL}, "t")
	if !domain.Is(err, domain.KindBadRequest, failedMsg) || hits.Load() != 0 {
		t.Errorf("phải chặn SSRF: %v hits=%d", err, hits.Load())
	}
}

func TestUserInfoDoesNotFollowRedirects(t *testing.T) {
	var hits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits.Add(1) }))
	defer target.Close()
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer redir.Close()
	if _, err := NewClient(true).UserInfo(ctx, &fhir.IdentityProvider{UserInfoUrl: redir.URL}, "t"); err == nil || hits.Load() != 0 {
		t.Errorf("không được theo redirect (kèm Bearer): %v hits=%d", err, hits.Load())
	}
}
