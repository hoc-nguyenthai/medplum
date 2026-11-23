package bootstrap

import (
	"context"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"trivita-fhir/apps/auth/internal/application"
	"trivita-fhir/apps/auth/internal/config"
	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirauth"
	"trivita-fhir/pkg/fhirrouter"
)

const (
	appURL      = "https://app.example.org"
	redirectURI = "https://app.example.org/cb"
	verifier    = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk" // ví dụ của RFC 7636 Appendix B (43 ký tự)
)

type testClock struct{ t atomic.Int64 }

func (c *testClock) Now() time.Time { return time.Unix(c.t.Load(), 0) }

// codeAPI là api đã đăng ký redirect_uri cho client mặc định và dùng đồng hồ điều khiển được.
type codeAPI struct {
	*api
	clock *testClock
}

func newCodeAPI(t *testing.T, mut func(*config.Settings)) *codeAPI {
	t.Helper()
	a := newAPI(t, func(s *config.Settings) {
		s.AppBaseURL = appURL
		if mut != nil {
			mut(s)
		}
	}, nil)
	c := *a.seed.Client
	c.RedirectUris = []string{redirectURI}
	updated, err := a.c.Store.UpdateResource(context.Background(), &c, fhirrouter.UpdateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	a.seed.Client = updated.(*fhir.ClientApplication)
	a.seed.Client.Secret = c.Secret
	clk := &testClock{}
	clk.t.Store(time.Now().Unix())
	a.c.Auth.Clock = clk
	return &codeAPI{api: a, clock: clk}
}

type loginOpts struct {
	scope, challenge, method, redirect, launch string
}

// loginCode đăng nhập qua /auth/login như một SPA và trả code, login id và giá trị cookie.
func (a *codeAPI) loginCode(o loginOpts) (code, loginID, cookie string) {
	a.t.Helper()
	body := `,"clientId":"` + a.seed.Client.ID + `"`
	if o.scope != "" {
		body += `,"scope":"` + o.scope + `"`
	}
	if o.challenge != "" {
		body += `,"codeChallenge":"` + o.challenge + `","codeChallengeMethod":"` + o.method + `"`
	}
	if o.redirect != "" {
		body += `,"redirectUri":"` + o.redirect + `"`
	}
	if o.launch != "" {
		body += `,"launch":"` + o.launch + `"`
	}
	r := a.login(loginBody(body), nil)
	if r.status != 200 {
		a.t.Fatalf("login: %d %s", r.status, r.body)
	}
	m := r.json()
	code, _ = m["code"].(string)
	if code == "" {
		a.t.Fatalf("không có code: %s", r.body)
	}
	for _, line := range r.hdr.Values("Set-Cookie") {
		if strings.HasPrefix(line, "medplum-"+a.seed.Client.ID+"=") {
			cookie = strings.SplitN(strings.TrimPrefix(line, "medplum-"+a.seed.Client.ID+"="), ";", 2)[0]
		}
	}
	return code, m["login"].(string), cookie
}

func (a *codeAPI) exchange(v url.Values, hdr map[string]string) resp {
	a.t.Helper()
	form := url.Values{"grant_type": {"authorization_code"}}
	for k, vals := range v {
		form[k] = vals
	}
	return a.form("/oauth2/token", form, hdr)
}

func s256() string { return domain.ChallengeS256(verifier) }

func wantOAuthError(t *testing.T, r resp, code, desc string) {
	t.Helper()
	m := r.json()
	if r.status != 400 || m["error"] != code || m["error_description"] != desc || m["access_token"] != nil {
		t.Errorf("muốn 400 %s/%q, được %d %s", code, desc, r.status, r.body)
	}
}

func (a *codeAPI) storedLogin(id string) *fhir.Login {
	a.t.Helper()
	l, err := a.c.Auth.Logins.Get(context.Background(), id)
	if err != nil {
		a.t.Fatal(err)
	}
	return l
}

func TestAuthorizationCodeWithPKCEIssuesTokensForFhirServer(t *testing.T) {
	a := newCodeAPI(t, nil)
	code, loginID, cookie := a.loginCode(loginOpts{scope: "openid profile email offline_access", challenge: s256(), method: "S256", redirect: redirectURI})

	// kho chỉ có băm của code và cookie (D-15), kèm hạn tường minh
	l := a.storedLogin(loginID)
	if l.Code == code || l.Code != domain.HashSecret(code) || l.Cookie == cookie || l.Cookie != domain.HashSecret(cookie) || l.ExpiresAt == "" {
		t.Errorf("Login phải chỉ lưu băm: code=%q cookie=%q expires=%q", l.Code, l.Cookie, l.ExpiresAt)
	}
	if domain.LoginRedirectURI(l) != redirectURI {
		t.Errorf("redirect_uri phải được lưu: %v", l.Extension)
	}

	r := a.exchange(url.Values{"code": {code}, "code_verifier": {verifier}, "redirect_uri": {redirectURI}, "client_id": {a.seed.Client.ID}}, nil)
	if r.status != 200 || r.hdr.Get("Cache-Control") != "no-store" {
		t.Fatalf("%d %v %s", r.status, r.hdr, r.body)
	}
	body := r.json()
	if body["token_type"] != "Bearer" || body["scope"] != "openid profile email offline_access" || body["id_token"] == nil || body["refresh_token"] == nil ||
		body["profile"].(map[string]any)["reference"] != "Practitioner/"+a.seed.Profile.ID {
		t.Fatalf("body: %v", body)
	}
	p, err := a.verifier().Verify(context.Background(), body["access_token"].(string))
	if err != nil {
		t.Fatalf("fhirauth từ chối: %v", err)
	}
	if p.Project != a.seed.Project.ID || p.Role != fhirauth.RoleAdmin {
		t.Errorf("principal: %+v", p)
	}
	if got := a.storedLogin(loginID); !domain.IsTrue(got.Granted) || domain.IsTrue(got.Revoked) {
		t.Errorf("login phải granted: %+v", got)
	}
	// refresh token không dùng được làm Bearer (aud và token_use khác)
	if r := a.do("GET", "/auth/me", "", map[string]string{"Authorization": "Bearer " + body["refresh_token"].(string)}); r.status != 401 {
		t.Errorf("refresh token làm Bearer: %d", r.status)
	}
	// access token của người dùng dùng được ở route của auth
	if r := a.do("GET", "/auth/me", "", map[string]string{"Authorization": "Bearer " + body["access_token"].(string)}); r.status != 200 {
		t.Errorf("/auth/me: %d %s", r.status, r.body)
	}
	// id token có email theo scope và nonce
	idClaims, err := a.c.Keys.Verify(body["id_token"].(string))
	if err != nil || idClaims["email"] != "admin@example.org" || idClaims["aud"] != a.seed.Client.ID || idClaims["fhirUser"] == nil {
		t.Errorf("id token: %v %v", idClaims, err)
	}
}

func TestAuthorizationCodeBasicErrors(t *testing.T) {
	a := newCodeAPI(t, nil)
	code, _, _ := a.loginCode(loginOpts{challenge: s256(), method: "S256"})
	wantOAuthError(t, a.exchange(url.Values{}, nil), "invalid_request", "Missing code")
	wantOAuthError(t, a.exchange(url.Values{"code": {"khong-co"}}, nil), "invalid_request", "Invalid code")
	wantOAuthError(t, a.exchange(url.Values{"code": {code}, "client_id": {"client-khac"}, "code_verifier": {verifier}}, nil), "invalid_request", "Invalid client")
	wantOAuthError(t, a.exchange(url.Values{"code": {code}}, map[string]string{"Authorization": "Basic !!!"}), "invalid_request", "Invalid authorization header")
	wantOAuthError(t, a.exchange(url.Values{"code": {code}, "client_assertion_type": {"x"}}, nil), "invalid_request", "Unsupported client assertion type")
	// các lỗi trên không tiêu thụ code
	if r := a.exchange(url.Values{"code": {code}, "code_verifier": {verifier}}, nil); r.status != 200 {
		t.Errorf("code vẫn phải dùng được: %d %s", r.status, r.body)
	}
}

func TestAuthorizationCodeExpiresFromIssueTimeNotFromLastWrite(t *testing.T) {
	a := newCodeAPI(t, nil)
	code, loginID, _ := a.loginCode(loginOpts{challenge: s256(), method: "S256"})
	a.clock.t.Add(int64((9*time.Minute + 59*time.Second).Seconds()))
	// ghi vào Login (đổi scope) không được kéo dài hạn của code (Medplum tính từ meta.lastUpdated nên bị kéo dài)
	if _, err := a.c.Auth.Logins.Mutate(context.Background(), loginID, func(l *fhir.Login) error { l.UserAgent = "đã ghi"; return nil }); err != nil {
		t.Fatal(err)
	}
	a.clock.t.Add(2)
	wantOAuthError(t, a.exchange(url.Values{"code": {code}, "code_verifier": {verifier}}, nil), "invalid_grant", "Authorization code expired")
}

func TestAuthorizationCodePKCE(t *testing.T) {
	a := newCodeAPI(t, nil)
	issue := func() string {
		c, _, _ := a.loginCode(loginOpts{challenge: s256(), method: "S256"})
		return c
	}
	code := issue()
	wantOAuthError(t, a.exchange(url.Values{"code": {code}}, nil), "invalid_request", "Missing code verifier")
	for name, bad := range map[string]string{"sai": strings.Repeat("a", 43), "quá ngắn": "abc", "ký tự cấm": strings.Repeat("a", 42) + "!", "quá dài": strings.Repeat("a", 129)} {
		if r := a.exchange(url.Values{"code": {code}, "code_verifier": {bad}}, nil); r.status != 400 || r.json()["error_description"] != "Invalid code verifier" {
			t.Errorf("%s: %d %s", name, r.status, r.body)
		}
	}
	// verifier sai không tiêu thụ code: kẻ lộ code không đốt được code của người dùng thật
	if r := a.exchange(url.Values{"code": {code}, "code_verifier": {verifier}}, nil); r.status != 200 {
		t.Errorf("verifier đúng sau các lần sai: %d %s", r.status, r.body)
	}

	// plain bị từ chối mặc định, dù verifier khớp challenge
	plainVerifier := strings.Repeat("p", 50)
	c2, _, _ := a.loginCode(loginOpts{challenge: plainVerifier, method: "plain"})
	wantOAuthError(t, a.exchange(url.Values{"code": {c2}, "code_verifier": {plainVerifier}}, nil), "invalid_request", "Invalid code verifier")

	allow := newCodeAPI(t, func(s *config.Settings) { s.AllowPlainPKCE = true })
	c3, _, _ := allow.loginCode(loginOpts{challenge: plainVerifier, method: "plain"})
	if r := allow.exchange(url.Values{"code": {c3}, "code_verifier": {plainVerifier}}, nil); r.status != 200 {
		t.Errorf("plain khi bật: %d %s", r.status, r.body)
	}
	if got := allow.do("GET", "/.well-known/openid-configuration", "", nil).json()["code_challenge_methods_supported"].([]any); len(got) != 2 {
		t.Errorf("phải khai plain khi bật: %v", got)
	}
	if got := a.do("GET", "/.well-known/openid-configuration", "", nil).json()["code_challenge_methods_supported"].([]any); len(got) != 1 || got[0] != "S256" {
		t.Errorf("mặc định chỉ S256: %v", got)
	}
}

// Medplum bỏ qua PKCE với client pkceOptional dù Login có challenge (test T:770 của họ khẳng định điều đó); auth luôn kiểm.
func TestAuthorizationCodePkceOptionalClientStillVerifiesChallenge(t *testing.T) {
	a := newCodeAPI(t, nil)
	c := *a.seed.Client
	yes := true
	c.PkceOptional = &yes
	if _, err := a.c.Store.UpdateResource(context.Background(), &c, fhirrouter.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	code, _, _ := a.loginCode(loginOpts{challenge: s256(), method: "S256"})
	wantOAuthError(t, a.exchange(url.Values{"code": {code}}, nil), "invalid_request", "Missing code verifier")
	wantOAuthError(t, a.exchange(url.Values{"code": {code}, "code_verifier": {strings.Repeat("x", 43)}}, nil), "invalid_request", "Invalid code verifier")
	if r := a.exchange(url.Values{"code": {code}, "code_verifier": {verifier}}, nil); r.status != 200 {
		t.Errorf("%d %s", r.status, r.body)
	}
}

func TestAuthorizationCodeWithoutChallengeRequiresClientAuthentication(t *testing.T) {
	a := newCodeAPI(t, nil)
	id, secret := a.seed.Client.ID, a.seed.Client.Secret
	code, _, _ := a.loginCode(loginOpts{})
	wantOAuthError(t, a.exchange(url.Values{"code": {code}}, nil), "invalid_request", "Missing verification context")
	wantOAuthError(t, a.exchange(url.Values{"code": {code}, "client_id": {id}}, nil), "invalid_request", "Missing verification context")
	wantOAuthError(t, a.exchange(url.Values{"code": {code}}, basic(id, "sai")), "invalid_request", "Invalid secret")
	if r := a.exchange(url.Values{"code": {code}}, basic(id, secret)); r.status != 200 {
		t.Fatalf("client xác thực bằng secret: %d %s", r.status, r.body)
	}
	// secret gửi kèm phải đúng ngay cả khi PKCE hợp lệ (Medplum bỏ qua secret ở nhánh PKCE)
	c2, _, _ := a.loginCode(loginOpts{challenge: s256(), method: "S256"})
	wantOAuthError(t, a.exchange(url.Values{"code": {c2}, "code_verifier": {verifier}}, basic(id, "sai")), "invalid_request", "Invalid secret")
	if r := a.exchange(url.Values{"code": {c2}, "code_verifier": {verifier}}, basic(id, secret)); r.status != 200 {
		t.Errorf("PKCE kèm secret đúng: %d %s", r.status, r.body)
	}
	// secret phụ (retiringSecret) vẫn dùng được khi đang xoay secret
	c := *a.seed.Client
	c.RetiringSecret, c.Secret = secret, "secret-moi-0123456789"
	if _, err := a.c.Store.UpdateResource(context.Background(), &c, fhirrouter.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	c3, _, _ := a.loginCode(loginOpts{})
	if r := a.exchange(url.Values{"code": {c3}}, basic(id, secret)); r.status != 200 {
		t.Errorf("retiringSecret: %d %s", r.status, r.body)
	}
}

func TestAuthorizationCodeRedirectURIMustMatchLoginExactly(t *testing.T) {
	a := newCodeAPI(t, nil)
	code, _, _ := a.loginCode(loginOpts{challenge: s256(), method: "S256", redirect: redirectURI})
	base := url.Values{"code": {code}, "code_verifier": {verifier}}
	wantOAuthError(t, a.exchange(base, nil), "invalid_request", "Missing redirect_uri")
	for _, bad := range []string{redirectURI + "/", redirectURI + "?x=1", "https://evil.example.org/cb", strings.ToUpper(redirectURI)} {
		v := url.Values{"code": base["code"], "code_verifier": base["code_verifier"], "redirect_uri": {bad}}
		wantOAuthError(t, a.exchange(v, nil), "invalid_grant", "Invalid redirect_uri")
	}
	base.Set("redirect_uri", redirectURI)
	if r := a.exchange(base, nil); r.status != 200 {
		t.Errorf("redirect_uri đúng: %d %s", r.status, r.body)
	}
	// Login không có redirect_uri thì không đòi
	c2, _, _ := a.loginCode(loginOpts{challenge: s256(), method: "S256"})
	if r := a.exchange(url.Values{"code": {c2}, "code_verifier": {verifier}}, nil); r.status != 200 {
		t.Errorf("không có redirect_uri lúc đăng nhập: %d %s", r.status, r.body)
	}
}

func TestAuthorizationCodeReuseRevokesLoginAndTokens(t *testing.T) {
	a := newCodeAPI(t, nil)
	code, loginID, _ := a.loginCode(loginOpts{challenge: s256(), method: "S256"})
	v := url.Values{"code": {code}, "code_verifier": {verifier}}
	first := a.exchange(v, nil)
	if first.status != 200 {
		t.Fatalf("%d %s", first.status, first.body)
	}
	access := first.json()["access_token"].(string)
	me := func() int {
		return a.do("GET", "/auth/me", "", map[string]string{"Authorization": "Bearer " + access}).status
	}
	if me() != 200 {
		t.Fatal("token lần 1 phải dùng được")
	}
	wantOAuthError(t, a.exchange(v, nil), "invalid_grant", "Token already granted")
	if !domain.IsTrue(a.storedLogin(loginID).Revoked) {
		t.Error("dùng lại code phải thu hồi Login")
	}
	if me() != 401 {
		t.Error("token đã cấp ở lần 1 phải bị thu hồi (ở route của auth)")
	}
	wantOAuthError(t, a.exchange(v, nil), "invalid_grant", "Token revoked")
}

func TestAuthorizationCodeOnRevokedLoginIsRefused(t *testing.T) {
	a := newCodeAPI(t, nil)
	code, loginID, _ := a.loginCode(loginOpts{challenge: s256(), method: "S256"})
	if err := a.c.Auth.RevokeLogin(context.Background(), a.storedLogin(loginID)); err != nil {
		t.Fatal(err)
	}
	wantOAuthError(t, a.exchange(url.Values{"code": {code}, "code_verifier": {verifier}}, nil), "invalid_grant", "Token revoked")
}

// Chiếm code là một lần ghi có điều kiện theo phiên bản: 16 yêu cầu song song cùng code, đúng một cái thành công.
func TestAuthorizationCodeConcurrentRedemptionYieldsExactlyOneToken(t *testing.T) {
	// SQLite chỉ có một người ghi: dùng một kết nối như bộ test của kho (PostgreSQL mới có ghi đồng thời thật, xem make test-auth-pg)
	a := newCodeAPI(t, func(s *config.Settings) { s.DatabaseMaxOpenConns = 1 })
	code, loginID, _ := a.loginCode(loginOpts{challenge: s256(), method: "S256"})
	// Rào chắn buộc cả 16 yêu cầu cùng đọc thấy Login chưa granted rồi MỚI cùng đi chiếm code: không có nó, trên SQLite các yêu cầu
	// gần như chạy tuần tự và một cách chiếm code không nguyên tử vẫn qua được bài kiểm.
	barrier := &barrierLogins{LoginRepository: a.c.Auth.Logins, ready: make(chan struct{}), want: 16}
	a.c.Auth.Logins = barrier
	var wg sync.WaitGroup
	var ok, usedUp atomic.Int32
	start := make(chan struct{})
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			r := a.exchange(url.Values{"code": {code}, "code_verifier": {verifier}}, nil)
			switch d := r.json()["error_description"]; {
			case r.status == 200:
				ok.Add(1)
			case d == "Token already granted" || d == "Token revoked" || d == "Authorization code already used":
				usedUp.Add(1)
			default:
				t.Errorf("kết quả lạ: %d %s", r.status, r.body)
			}
		}()
	}
	close(start)
	wg.Wait()
	if ok.Load() != 1 || usedUp.Load() != 15 {
		t.Errorf("thành công=%d, bị từ chối=%d; muốn 1 và 15", ok.Load(), usedUp.Load())
	}
	// các yêu cầu thua cuộc phát hiện code bị dùng lại nên thu hồi Login
	if !domain.IsTrue(a.storedLogin(loginID).Revoked) {
		t.Error("Login phải bị thu hồi sau khi code bị dùng lại")
	}
}

func TestAuthorizationCodeStatusPollRotatesCode(t *testing.T) {
	a := newCodeAPI(t, nil)
	code, loginID, _ := a.loginCode(loginOpts{challenge: s256(), method: "S256"})
	r := a.do("GET", "/auth/login/"+loginID, "", nil)
	next, _ := r.json()["code"].(string)
	if r.status != 200 || next == "" || next == code {
		t.Fatalf("poll phải phát code mới: %d %s", r.status, r.body)
	}
	wantOAuthError(t, a.exchange(url.Values{"code": {code}, "code_verifier": {verifier}}, nil), "invalid_request", "Invalid code")
	if r := a.exchange(url.Values{"code": {next}, "code_verifier": {verifier}}, nil); r.status != 200 {
		t.Errorf("code mới: %d %s", r.status, r.body)
	}
	// Login đã cấp token thì poll không phát code nữa
	if r := a.do("GET", "/auth/login/"+loginID, "", nil); r.status != 400 {
		t.Errorf("poll sau khi cấp token: %d %s", r.status, r.body)
	}
}

func TestAuthorizationCodeRefusesInactiveClientAndProfile(t *testing.T) {
	a := newCodeAPI(t, nil)
	code, _, _ := a.loginCode(loginOpts{challenge: s256(), method: "S256"})
	c := *a.seed.Client
	c.Status = "off"
	if _, err := a.c.Store.UpdateResource(context.Background(), &c, fhirrouter.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	wantOAuthError(t, a.exchange(url.Values{"code": {code}, "code_verifier": {verifier}}, nil), "invalid_request", "Invalid client")
	c.Status = "active"
	if _, err := a.c.Store.UpdateResource(context.Background(), &c, fhirrouter.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	m := *a.seed.Membership
	f := false
	m.Active = &f
	if _, err := a.c.Store.UpdateResource(context.Background(), &m, fhirrouter.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if r := a.exchange(url.Values{"code": {code}, "code_verifier": {verifier}}, nil); r.status != 400 || r.json()["access_token"] != nil {
		t.Errorf("membership tắt: %d %s", r.status, r.body)
	}
}

func TestAuthorizationCodeSmartLaunchContext(t *testing.T) {
	a := newCodeAPI(t, nil)
	launch, err := a.c.Store.CreateResource(context.Background(), &fhir.SmartAppLaunch{ResourceType: "SmartAppLaunch",
		Patient:     &fhir.Reference{Reference: "Patient/p1"},
		Encounter:   &fhir.Reference{Reference: "Encounter/e1", Identifier: &fhir.Identifier{Value: "ENC-9"}},
		FhirContext: []fhir.Reference{{Reference: "Observation/o1"}}})
	if err != nil {
		t.Fatal(err)
	}
	code, _, _ := a.loginCode(loginOpts{challenge: s256(), method: "S256", launch: launch.GetID()})
	r := a.exchange(url.Values{"code": {code}, "code_verifier": {verifier}}, nil)
	body := r.json()
	if r.status != 200 || body["patient"] != "p1" || body["encounter"] != "ENC-9" || body["need_patient_banner"] != true || body["fhirContext"] == nil {
		t.Errorf("%d %s", r.status, r.body)
	}
	// không có launch: không có các trường đó
	c2, _, _ := a.loginCode(loginOpts{challenge: s256(), method: "S256"})
	plain := a.exchange(url.Values{"code": {c2}, "code_verifier": {verifier}}, nil).json()
	if plain["patient"] != nil || plain["encounter"] != nil || plain["need_patient_banner"] != nil {
		t.Errorf("không có launch: %v", plain)
	}
}

// barrierLogins làm FindByCode chờ tới khi đủ `want` lời gọi đã đọc xong, rồi thả tất cả cùng lúc.
type barrierLogins struct {
	application.LoginRepository
	ready chan struct{}
	want  int32
	seen  atomic.Int32
}

func (b *barrierLogins) FindByCode(ctx context.Context, hash string) (*fhir.Login, error) {
	l, err := b.LoginRepository.FindByCode(ctx, hash)
	if b.seen.Add(1) == b.want {
		close(b.ready)
	}
	select {
	case <-b.ready:
	case <-time.After(5 * time.Second):
	}
	return l, err
}
