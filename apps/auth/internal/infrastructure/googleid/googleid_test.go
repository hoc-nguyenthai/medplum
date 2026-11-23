package googleid

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/golang-jwt/jwt/v5"

	"trivita-fhir/apps/auth/internal/application"
)

var _ application.GoogleVerifier = (*Verifier)(nil)

func claims(extra jwt.MapClaims) jwt.MapClaims {
	c := jwt.MapClaims{"iss": Issuer, "aud": "gid", "sub": "1", "email": "a@gmail.com", "email_verified": true, "exp": time.Now().Add(time.Hour).Unix()}
	for k, v := range extra {
		c[k] = v
	}
	return c
}

func TestVerify(t *testing.T) {
	rk, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &rk.PublicKey, KeyID: "g1", Use: "sig"}}})
	}))
	defer srv.Close()
	v := New(true, srv.URL)
	sign := func(m jwt.SigningMethod, key any, c jwt.MapClaims) string {
		tok := jwt.NewWithClaims(m, c)
		tok.Header["kid"] = "g1"
		s, err := tok.SignedString(key)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	ctx := context.Background()
	got, err := v.Verify(ctx, sign(jwt.SigningMethodRS256, rk, claims(nil)), "gid")
	if err != nil || got["email"] != "a@gmail.com" {
		t.Fatalf("hợp lệ: %v %v", got, err)
	}
	ec, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	for name, tok := range map[string]string{
		"sai aud":        sign(jwt.SigningMethodRS256, rk, claims(jwt.MapClaims{"aud": "khac"})),
		"sai iss":        sign(jwt.SigningMethodRS256, rk, claims(jwt.MapClaims{"iss": "https://evil.example"})),
		"hết hạn":        sign(jwt.SigningMethodRS256, rk, claims(jwt.MapClaims{"exp": time.Now().Add(-time.Hour).Unix()})),
		"thiếu exp":      sign(jwt.SigningMethodRS256, rk, func() jwt.MapClaims { c := claims(nil); delete(c, "exp"); return c }()),
		"khóa lạ":        sign(jwt.SigningMethodRS256, other, claims(nil)),
		"RS384":          sign(jwt.SigningMethodRS384, rk, claims(nil)),
		"ES256":          sign(jwt.SigningMethodES256, ec, claims(nil)),
		"HS256":          sign(jwt.SigningMethodHS256, []byte("k"), claims(nil)),
		"không phải jwt": "abc",
	} {
		if _, err := v.Verify(ctx, tok, "gid"); err == nil {
			t.Errorf("%s: phải bị từ chối", name)
		}
	}
	none, _ := jwt.NewWithClaims(jwt.SigningMethodNone, claims(nil)).SignedString(jwt.UnsafeAllowNoneSignatureType)
	if _, err := v.Verify(ctx, none, "gid"); err == nil {
		t.Error("alg none phải bị từ chối")
	}
}

func TestDefaultsAndSSRF(t *testing.T) {
	if New(false, "").jwksURL != DefaultJWKSURL {
		t.Error("mặc định phải là JWKS của Google")
	}
	// JWKS nội bộ bị chặn khi không bật unsafe
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { t.Error("không được gọi nội bộ") }))
	defer srv.Close()
	rk, _ := rsa.GenerateKey(rand.Reader, 2048)
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims(nil))
	tok.Header["kid"] = "g1"
	s, _ := tok.SignedString(rk)
	if _, err := New(false, srv.URL).Verify(context.Background(), s, "gid"); err == nil {
		t.Error("phải lỗi")
	}
}
