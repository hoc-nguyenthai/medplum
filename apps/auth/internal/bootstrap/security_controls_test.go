package bootstrap

import (
	"bytes"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"testing"

	"trivita-fhir/apps/auth/internal/config"

	"github.com/golang-jwt/jwt/v5"
)

// B9 mục 8 dòng 19: hạn mức chung phải phủ cả /oauth2/token và /oauth2/authorize (dò secret client tốn mỗi lần một lần băm),
// không chỉ /auth/*. Mỗi đường dẫn dùng một instance riêng để bộ đếm không lẫn.
func TestRateLimitCoversOAuthTokenAndAuthorize(t *testing.T) {
	for _, c := range []struct{ method, path string }{
		{"POST", "/oauth2/token"},
		{"GET", "/oauth2/authorize"},
		{"POST", "/oauth2/introspect"},
	} {
		a := newAPI(t, func(s *config.Settings) {
			s.RateLimit, s.RateBurst = 0.001, 2
			s.AppBaseURL = appURL
		}, nil)
		for i := 0; i < 2; i++ {
			if r := a.do(c.method, c.path, "", nil); r.status == 429 {
				t.Fatalf("%s %s: lần %d đã bị chặn", c.method, c.path, i+1)
			}
		}
		if r := a.do(c.method, c.path, "", nil); r.status != 429 {
			t.Errorf("%s %s: vượt hạn mức vẫn %d", c.method, c.path, r.status)
		}
	}
}

// syncBuffer cho phép handler của slog ghi từ nhiều goroutine của máy chủ thử.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// B9 mục 8 dòng 20 (audit): các sự kiện đáng ngờ phải để lại dấu vết mức cảnh báo, và nhật ký KHÔNG chứa secret/token/code/verifier.
// Đây là kiểm soát bằng slog; bảng audit riêng chưa làm (xem p3 mục 11).
func TestSecurityEventsAreLoggedWithoutSecrets(t *testing.T) {
	a := newAssertionAPI(t, func(s *config.Settings) { s.RefreshGrace = 0 })
	logs := &syncBuffer{}
	a.c.Auth.Logger = slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	var secrets []string

	// 1. PKCE verifier sai khi đổi code
	badVerifier := strings.Repeat("a", 43)
	code, _, _ := a.loginCode(loginOpts{challenge: s256(), method: "S256"})
	secrets = append(secrets, code, badVerifier)
	wantOAuthError(t, a.exchange(url.Values{"code": {code}, "code_verifier": {badVerifier}}, nil), "invalid_request", "Invalid code verifier")

	// 2. code dùng lại
	r := a.exchange(url.Values{"code": {code}, "code_verifier": {verifier}}, nil)
	if r.status != 200 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	secrets = append(secrets, verifier, r.json()["access_token"].(string))
	wantOAuthError(t, a.exchange(url.Values{"code": {code}, "code_verifier": {verifier}}, nil), "invalid_grant", "Token already granted")

	// 3. refresh token đã xoay bị dùng lại
	tk := a.tokens()
	old := tk["refresh_token"].(string)
	oldSecret := a.refreshSecretOf(old)
	secrets = append(secrets, old, oldSecret)
	next := a.refresh(old, nil)
	if next.status != 200 {
		t.Fatalf("%d %s", next.status, next.body)
	}
	secrets = append(secrets, next.json()["refresh_token"].(string), a.refreshSecretOf(next.json()["refresh_token"].(string)))
	wantOAuthError(t, a.refresh(old, nil), "invalid_grant", "Token revoked")

	// 4. client assertion bị phát lại
	k := ecKey(t, "k1")
	a.jwks.set(k.pub)
	raw := a.assertion(k, func(c jwt.MapClaims, _ *jwt.Token) { c["jti"] = "audit-1" })
	secrets = append(secrets, raw, a.seed.Client.Secret)
	if rr := a.post("client_credentials", raw, nil); rr.status != 200 {
		t.Fatalf("%d %s", rr.status, rr.body)
	}
	wantOAuthError(t, a.post("client_credentials", raw, nil), "invalid_request", "Client assertion already used")

	out := logs.String()
	for _, want := range []string{
		"PKCE verifier sai khi đổi code",
		"authorization code bị dùng lại, thu hồi Login",
		"refresh token đã xoay bị dùng lại, thu hồi Login",
		"client assertion bị dùng lại",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("thiếu dấu vết kiểm toán %q; nhật ký:\n%s", want, out)
		}
	}
	for _, s := range secrets {
		if s != "" && strings.Contains(out, s) {
			t.Errorf("nhật ký lộ giá trị bí mật %q...", s[:min(len(s), 8)])
		}
	}
}
