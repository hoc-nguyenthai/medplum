package e2e

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/golang-jwt/jwt/v5"
)

// Token-exchange, Google và pre-authorize trên tiến trình thật: mỗi luồng cuối cùng cho token mà fhir-server chấp nhận.
func TestExchangeGoogleAndPreAuthorizeThroughRealProcesses(t *testing.T) {
	// IdP chỉ có /userinfo (token-exchange)
	userinfo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer ext-access" {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"email": "a@example.org", "sub": "ext-1", "email_verified": true})
	}))
	t.Cleanup(userinfo.Close)
	// JWKS của "Google"
	gk, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	jwks := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &gk.PublicKey, KeyID: "g1", Use: "sig"}}})
	}))
	t.Cleanup(jwks.Close)
	credential := func(email string, verified bool) string {
		tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"iss": "https://accounts.google.com", "aud": "gid-e2e", "sub": "g-1", "email": email,
			"email_verified": verified, "exp": time.Now().Add(time.Hour).Unix()})
		tok.Header["kid"] = "g1"
		s, _ := tok.SignedString(gk)
		return s
	}
	providers, _ := json.Marshal([]map[string]any{{"issuer": userinfo.URL, "clientId": "srv-cid", "identityProvider": map[string]any{"clientId": "srv-cid", "userInfoUrl": userinfo.URL}}})
	s := newStackCustom(t, false, map[string]string{
		"AUTH_ALLOW_PRIVATE_JWKS": "true", "AUTH_EXTERNAL_AUTH_PROVIDERS": string(providers), "AUTH_GOOGLE_CLIENT_ID": "gid-e2e", "AUTH_GOOGLE_JWKS_URL": jwks.URL,
	}, nil)
	callFhir := func(label, access string) {
		t.Helper()
		if r := s.fhirPost(access, "/Patient", patientJSON); r.status != 201 {
			t.Fatalf("%s: fhir-server từ chối token: %d %s", label, r.status, r.body)
		}
	}

	admin := s.signIn("a@example.org", s.clientA)
	adminAccess := admin["access_token"].(string)
	me := do(t, "GET", s.auth+"/auth/me", adminAccess, "", nil).json(t)
	membership := me["membership"].(map[string]any)["id"].(string)

	// 1. token-exchange (bắt buộc membership_id với IdP của máy chủ)
	form := url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:token-exchange"}, "client_id": {"srv-cid"}, "subject_token": {"ext-access"},
		"subject_token_type": {"urn:ietf:params:oauth:token-type:access_token"}}
	if r := s.token(form); r.status != 400 || r.json(t)["error_description"] != "Missing membership_id" {
		t.Fatalf("thiếu membership_id: %d %s", r.status, r.body)
	}
	form.Set("membership_id", membership)
	ex := s.token(form)
	if ex.status != 200 {
		t.Fatalf("token-exchange: %d %s", ex.status, ex.body)
	}
	callFhir("token-exchange", ex.json(t)["access_token"].(string))
	form.Set("subject_token", "sai")
	if r := s.token(form); r.status != 400 {
		t.Errorf("token IdP sai: %d %s", r.status, r.body)
	}

	// 2. Google: credential hợp lệ -> code -> token; email chưa xác minh bị từ chối
	g := s.postJSON("/auth/google", map[string]any{"googleClientId": "gid-e2e", "googleCredential": credential("a@example.org", true), "clientId": s.clientA,
		"scope": "openid", "codeChallenge": challengeS256(), "codeChallengeMethod": "S256"})
	code, _ := g.json(t)["code"].(string)
	if g.status != 200 || code == "" {
		t.Fatalf("google: %d %s", g.status, g.body)
	}
	tr := s.token(url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {s.clientA}, "code_verifier": {verifier}})
	if tr.status != 200 {
		t.Fatalf("đổi code google: %d %s", tr.status, tr.body)
	}
	callFhir("google", tr.json(t)["access_token"].(string))
	if r := s.postJSON("/auth/google", map[string]any{"googleClientId": "gid-e2e", "googleCredential": credential("a@example.org", false), "clientId": s.clientA}); r.status != 400 {
		t.Errorf("email chưa xác minh: %d %s", r.status, r.body)
	}

	// 3. preauthorize: admin mint, mã đổi được MỘT lần
	mint := func() resp {
		b, _ := json.Marshal(map[string]any{"clientId": s.clientA})
		req, _ := http.NewRequest("POST", s.auth+"/auth/preauthorize", bytes.NewReader(b))
		req.Header.Set("Authorization", "Bearer "+adminAccess)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Medplum-On-Behalf-Of", "ProjectMembership/"+membership)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var buf []byte
		buf, _ = io.ReadAll(res.Body)
		return resp{status: res.StatusCode, body: buf, hdr: res.Header}
	}
	m := mint()
	pre, _ := m.json(t)["preAuthorizedCode"].(string)
	if m.status != 200 || pre == "" {
		t.Fatalf("preauthorize: %d %s", m.status, m.body)
	}
	redeem := func() resp {
		return s.token(url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:pre-authorized_code"}, "client_id": {s.clientA}, "pre-authorized_code": {pre}})
	}
	r1 := redeem()
	if r1.status != 200 {
		t.Fatalf("redeem: %d %s", r1.status, r1.body)
	}
	callFhir("pre-authorized", r1.json(t)["access_token"].(string))
	if r2 := redeem(); r2.status != 400 || r2.json(t)["error_description"] != "Token already granted" {
		t.Errorf("dùng lại: %d %s", r2.status, r2.body)
	}
	// thiếu header on-behalf-of và người gọi không phải admin của project khác
	if r := s.bearerJSON(adminAccess, "/auth/preauthorize", map[string]any{"clientId": s.clientA}); r.status != 400 {
		t.Errorf("thiếu on-behalf-of: %d %s", r.status, r.body)
	}
	if r := s.bearerJSON("", "/auth/preauthorize", map[string]any{"clientId": s.clientA}); r.status != 401 {
		t.Errorf("không Bearer: %d", r.status)
	}
}
