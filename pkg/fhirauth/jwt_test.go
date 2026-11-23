package fhirauth_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/golang-jwt/jwt/v5"

	"trivita-fhir/pkg/fhirauth"
)

const (
	iss = "https://id.example.test"
	aud = "fhir"
)

var secret = []byte("0123456789abcdef0123456789abcdef")

func claims(over map[string]any) jwt.MapClaims {
	c := jwt.MapClaims{
		"iss": iss, "aud": aud, "sub": "user-1", "exp": time.Now().Add(time.Hour).Unix(),
		"fhir_project": "proj-1", "scope": "openid fhir.write",
	}
	for k, v := range over {
		if v == nil {
			delete(c, k)
		} else {
			c[k] = v
		}
	}
	return c
}

func hs(t *testing.T, c jwt.MapClaims) string {
	t.Helper()
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(secret)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestVerifyHMAC(t *testing.T) {
	v, err := fhirauth.NewVerifier(fhirauth.Config{Issuer: iss, Audience: aud, HMACSecret: secret})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	p, err := v.Verify(ctx, hs(t, claims(nil)))
	if err != nil || p.Subject != "user-1" || p.Project != "proj-1" || p.Role != fhirauth.RoleWrite {
		t.Fatalf("token hợp lệ: %+v %v", p, err)
	}

	roles := map[string]fhirauth.Role{
		"fhir.read": fhirauth.RoleRead, "system/*.read": fhirauth.RoleRead,
		"fhir.write": fhirauth.RoleWrite, "system/*.*": fhirauth.RoleWrite,
		"fhir.read fhir.admin": fhirauth.RoleAdmin, "fhir.admin": fhirauth.RoleAdmin,
	}
	for scope, want := range roles {
		p, err := v.Verify(ctx, hs(t, claims(map[string]any{"scope": scope})))
		if err != nil || p.Role != want {
			t.Errorf("scope %q: %+v %v, muốn %v", scope, p, err, want)
		}
	}
	if p, err := v.Verify(ctx, hs(t, claims(map[string]any{"scope": []string{"fhir.read"}}))); err != nil || p.Role != fhirauth.RoleRead {
		t.Errorf("scope dạng mảng: %+v %v", p, err)
	}

	invalid := map[string]string{
		"hết hạn":      hs(t, claims(map[string]any{"exp": time.Now().Add(-time.Hour).Unix()})),
		"thiếu exp":    hs(t, claims(map[string]any{"exp": nil})),
		"sai issuer":   hs(t, claims(map[string]any{"iss": "https://evil"})),
		"sai audience": hs(t, claims(map[string]any{"aud": "other"})),
		"thiếu sub":    hs(t, claims(map[string]any{"sub": nil})),
		"rác":          "not-a-jwt",
		"rỗng":         "",
		"alg none":     mustNone(t, claims(nil)),
		"sai chữ ký": func() string {
			s, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, claims(nil)).SignedString([]byte("another-secret-another-secret-123"))
			return s
		}(),
		"HS384 không cho": func() string {
			s, _ := jwt.NewWithClaims(jwt.SigningMethodHS384, claims(nil)).SignedString(secret)
			return s
		}(),
	}
	for name, tok := range invalid {
		if _, err := v.Verify(ctx, tok); !errors.Is(err, fhirauth.ErrInvalidToken) {
			t.Errorf("%s: err = %v, muốn ErrInvalidToken", name, err)
		}
	}
	forbidden := map[string]string{
		"thiếu project":       hs(t, claims(map[string]any{"fhir_project": nil})),
		"không có scope fhir": hs(t, claims(map[string]any{"scope": "openid profile"})),
		"thiếu scope":         hs(t, claims(map[string]any{"scope": nil})),
	}
	for name, tok := range forbidden {
		if _, err := v.Verify(ctx, tok); !errors.Is(err, fhirauth.ErrForbidden) {
			t.Errorf("%s: err = %v, muốn ErrForbidden", name, err)
		}
	}
}

func mustNone(t *testing.T, c jwt.MapClaims) string {
	s, err := jwt.NewWithClaims(jwt.SigningMethodNone, c).SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestVerifierConfig(t *testing.T) {
	bad := []fhirauth.Config{
		{},
		{Issuer: iss, Audience: aud},
		{Issuer: iss, Audience: aud, HMACSecret: []byte("short")},
		{Issuer: iss, Audience: aud, HMACSecret: secret, JWKSURL: "https://x/jwks"},
		{Issuer: iss, HMACSecret: secret},
	}
	for i, c := range bad {
		if _, err := fhirauth.NewVerifier(c); err == nil {
			t.Errorf("cấu hình %d phải bị từ chối", i)
		}
	}
	v, _ := fhirauth.NewVerifier(fhirauth.Config{Issuer: iss, Audience: aud, HMACSecret: secret, ProjectClaim: "tenant", ScopeClaim: "scp"})
	tok := hs(t, claims(map[string]any{"tenant": "t1", "scp": "fhir.read", "fhir_project": nil, "scope": nil}))
	if p, err := v.Verify(context.Background(), tok); err != nil || p.Project != "t1" || p.Role != fhirauth.RoleRead {
		t.Errorf("claim tùy biến: %+v %v", p, err)
	}
}

// ---------------------------------------------------------------- JWKS

type idp struct {
	srv   *httptest.Server
	keys  map[string]*rsa.PrivateKey
	hits  atomic.Int32
	down  atomic.Bool
	clock atomic.Int64 // giây tương đối
}

func newIdP(t *testing.T) *idp {
	p := &idp{keys: map[string]*rsa.PrivateKey{}}
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.hits.Add(1)
		if p.down.Load() {
			http.Error(w, "down", 500)
			return
		}
		var set jose.JSONWebKeySet
		for kid, k := range p.keys {
			set.Keys = append(set.Keys, jose.JSONWebKey{Key: &k.PublicKey, KeyID: kid, Algorithm: "RS256", Use: "sig"})
		}
		_ = json.NewEncoder(w).Encode(set)
	}))
	t.Cleanup(p.srv.Close)
	return p
}

func (p *idp) addKey(t *testing.T, kid string) {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p.keys[kid] = k
}

func (p *idp) token(t *testing.T, kid string, c jwt.MapClaims) string {
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, c)
	tok.Header["kid"] = kid
	s, err := tok.SignedString(p.keys[kid])
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestVerifyJWKS(t *testing.T) {
	p := newIdP(t)
	p.addKey(t, "k1")
	now := time.Now()
	var offset atomic.Int64
	v, err := fhirauth.NewVerifier(fhirauth.Config{
		Issuer: iss, Audience: aud, JWKSURL: p.srv.URL, JWKSCacheTTL: time.Minute,
		Now: func() time.Time { return now.Add(time.Duration(offset.Load())) },
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	if pr, err := v.Verify(ctx, p.token(t, "k1", claims(nil))); err != nil || pr.Project != "proj-1" {
		t.Fatalf("token RS256: %+v %v", pr, err)
	}
	for i := 0; i < 5; i++ {
		if _, err := v.Verify(ctx, p.token(t, "k1", claims(nil))); err != nil {
			t.Fatal(err)
		}
	}
	if p.hits.Load() != 1 {
		t.Errorf("JWKS phải được cache: %d lần tải", p.hits.Load())
	}

	// kid lạ: bị từ chối, và việc làm mới bị giới hạn tốc độ
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims(nil))
	tok.Header["kid"] = "ghost"
	forged, _ := tok.SignedString(other)
	for i := 0; i < 10; i++ {
		if _, err := v.Verify(ctx, forged); !errors.Is(err, fhirauth.ErrInvalidToken) {
			t.Fatalf("kid lạ: %v", err)
		}
	}
	if p.hits.Load() > 2 {
		t.Errorf("kid lạ làm tải JWKS %d lần, muốn chặn bằng rate limit", p.hits.Load())
	}

	// token ký bằng khóa khác nhưng mạo kid hợp lệ
	tok = jwt.NewWithClaims(jwt.SigningMethodRS256, claims(nil))
	tok.Header["kid"] = "k1"
	forged, _ = tok.SignedString(other)
	if _, err := v.Verify(ctx, forged); !errors.Is(err, fhirauth.ErrInvalidToken) {
		t.Errorf("mạo chữ ký: %v", err)
	}

	// HS256 với khóa công khai làm secret (alg confusion) phải bị từ chối
	conf := jwt.NewWithClaims(jwt.SigningMethodHS256, claims(nil))
	conf.Header["kid"] = "k1"
	cs, _ := conf.SignedString([]byte("whatever-whatever-whatever-123456"))
	if _, err := v.Verify(ctx, cs); !errors.Is(err, fhirauth.ErrInvalidToken) {
		t.Errorf("alg confusion: %v", err)
	}

	// xoay khóa: thêm k2 sau khi hết thời gian chờ làm mới
	p.addKey(t, "k2")
	offset.Store(int64(45 * time.Second))
	if _, err := v.Verify(ctx, p.token(t, "k2", claims(nil))); err != nil {
		t.Errorf("khóa mới sau khi xoay: %v", err)
	}

	// IdP sập: vẫn dùng khóa đã cache dù hết hạn
	p.down.Store(true)
	offset.Store(int64(10 * time.Minute))
	if _, err := v.Verify(ctx, p.token(t, "k1", claims(map[string]any{"exp": time.Now().Add(time.Hour).Unix()}))); err != nil {
		t.Errorf("IdP sập, khóa cache: %v", err)
	}
}

func TestVerifyClaimsDoesNotRequireFHIRRoleOrProject(t *testing.T) {
	v, _ := fhirauth.NewVerifier(fhirauth.Config{Issuer: iss, Audience: aud, HMACSecret: secret})
	ctx := context.Background()

	// token chỉ có scope riêng của dịch vụ khác: Verify từ chối (không có quyền FHIR) nhưng VerifyClaims chấp nhận
	tok := hs(t, claims(map[string]any{"scope": "openid fhircast/*"}))
	if _, err := v.Verify(ctx, tok); !errors.Is(err, fhirauth.ErrForbidden) {
		t.Fatalf("Verify phải từ chối token không có scope FHIR: %v", err)
	}
	c, err := v.VerifyClaims(ctx, tok)
	if err != nil || c.Subject != "user-1" || c.Project != "proj-1" || len(c.Scopes) != 2 || c.Scopes[1] != "fhircast/*" || c.Expires.IsZero() {
		t.Fatalf("VerifyClaims: %+v %v", c, err)
	}
	// không có project vẫn đọc được (caller tự quyết)
	if c, err := v.VerifyClaims(ctx, hs(t, claims(map[string]any{"fhir_project": nil}))); err != nil || c.Project != "" {
		t.Errorf("không project: %+v %v", c, err)
	}
	// nhưng chữ ký, iss, aud, exp, sub vẫn bị kiểm
	for name, tok := range map[string]string{
		"sai iss":    hs(t, claims(map[string]any{"iss": "https://evil"})),
		"sai aud":    hs(t, claims(map[string]any{"aud": "other"})),
		"hết hạn":    hs(t, claims(map[string]any{"exp": time.Now().Add(-time.Hour).Unix()})),
		"thiếu exp":  hs(t, claims(map[string]any{"exp": nil})),
		"thiếu sub":  hs(t, claims(map[string]any{"sub": nil})),
		"không ký":   mustNone(t, claims(nil)),
		"rác":        "not.a.jwt",
		"chữ ký sai": hs(t, claims(nil))[:len(hs(t, claims(nil)))-3] + "AAA",
	} {
		if _, err := v.VerifyClaims(ctx, tok); !errors.Is(err, fhirauth.ErrInvalidToken) {
			t.Errorf("%s: phải ErrInvalidToken, được %v", name, err)
		}
	}
}
