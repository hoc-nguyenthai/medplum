package application_test

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"trivita-fhir/apps/auth/internal/application"
	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirsearch"
)

type fakeOIDC struct {
	mu    sync.Mutex
	token string
	err   error
	calls []application.ExchangeCodeInput
	idps  []*fhir.IdentityProvider
	// UserInfo
	info      map[string]any
	infoErr   error
	infoCalls []string
}

func (f *fakeOIDC) UserInfo(_ context.Context, _ *fhir.IdentityProvider, token string) (map[string]any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.infoCalls = append(f.infoCalls, token)
	return f.info, f.infoErr
}

func (f *fakeOIDC) ExchangeCode(_ context.Context, idp *fhir.IdentityProvider, in application.ExchangeCodeInput) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls, f.idps = append(f.calls, in), append(f.idps, idp)
	return f.token, f.err
}

type verifyCall struct{ jwt, jwks, issuer, audience string }

type fakeVerifier struct {
	mu       sync.Mutex
	claims   map[string]any
	err      error
	calls    []verifyCall
	unsigned int
}

func (f *fakeVerifier) VerifyJWKS(_ context.Context, jwt, jwks, issuer, audience string) (map[string]any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, verifyCall{jwt, jwks, issuer, audience})
	return f.claims, f.err
}

func (f *fakeVerifier) ParseUnsigned(string) (map[string]any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.unsigned++
	return f.claims, f.err
}

type extFix struct {
	e       *env
	svc     *application.Service
	oidc    *fakeOIDC
	ver     *fakeVerifier
	project *fhir.Project
	client  *fhir.ClientApplication
	user    *fhir.User
	member  *fhir.ProjectMembership
	idp     *fhir.IdentityProvider
}

const (
	extRedirect = "https://app.example.org/cb"
	extEmail    = "an@corp.example.org"
)

func (e *env) extFixture(mut func(*extFix)) *extFix {
	e.t.Helper()
	f := &extFix{e: e, oidc: &fakeOIDC{token: "id.token.sig"}}
	f.idp = &fhir.IdentityProvider{AuthorizeUrl: "https://idp.example.org/authorize", TokenUrl: "https://idp.example.org/token", ClientId: "cid", ClientSecret: "sec",
		JwksUrl: "https://idp.example.org/jwks", Issuer: "https://idp.example.org"}
	f.ver = &fakeVerifier{claims: map[string]any{"email": extEmail, "sub": "sub-1", "email_verified": true}}
	if mut != nil {
		mut(f) // chỉ chỉnh idp và claims: phải xảy ra TRƯỚC khi idp được lưu vào DomainConfiguration
	}
	f.project = e.project(nil)
	f.user = e.user(extEmail, nil)
	f.member = e.membership(f.user, f.project, nil)
	f.client = put(e, &fhir.ClientApplication{ResourceType: "ClientApplication", Name: "Web", Meta: &fhir.Meta{Project: f.project.ID}, RedirectUris: []string{extRedirect}, PkceOptional: ptr(true)})
	put(e, &fhir.DomainConfiguration{ResourceType: "DomainConfiguration", Domain: "corp.example.org", IdentityProvider: f.idp,
		AllowedPostLoginRedirectUrls: []string{"https://app.example.org/signin"}})
	d := e.svc.Deps
	d.MFA = nil
	d.BaseURL = "https://auth.example.org/"
	d.Tokens = application.TokenConfig{AppBaseURL: "https://app.example.org", AuthCodeTTL: 10 * time.Minute, Issuer: "https://auth.example.org", Audiences: []string{"a"}, AccessTTL: time.Minute, RefreshTTL: time.Hour, IDTTL: time.Minute}
	d.OIDC, d.IDVerifier = f.oidc, f.ver
	d.External = application.ExternalConfig{StateKey: []byte(strings.Repeat("k", 32)), TTL: 10 * time.Minute}
	f.svc = application.NewService(d)
	return f
}

// start đi qua /auth/method như app thật: lấy state đã ký và cookie liên kết.
func (f *extFix) start(st domain.ExternalState) (state, cookie string) {
	f.e.t.Helper()
	res, nonce, err := f.svc.StartExternalLogin(context.Background(), extEmail, st)
	if err != nil || res == nil {
		f.e.t.Fatalf("start: %+v %v", res, err)
	}
	u, err := url.Parse(res.AuthorizeURL)
	if err != nil || u.Query().Get("state") == "" {
		f.e.t.Fatalf("authorizeUrl không có state: %s", res.AuthorizeURL)
	}
	return u.Query().Get("state"), nonce
}

func (f *extFix) run(st domain.ExternalState) (string, error) {
	state, cookie := f.start(st)
	return f.callback(state, cookie, nil)
}

func (f *extFix) callback(state, cookie string, mut func(*application.ExternalCallbackRequest)) (string, error) {
	req := application.ExternalCallbackRequest{Code: "ma-code", State: state, CookieNonce: cookie, RemoteAddress: "1.2.3.4", UserAgent: "ua"}
	if mut != nil {
		mut(&req)
	}
	return f.svc.ExternalCallback(context.Background(), req)
}

func (e *env) logins() []fhir.Login {
	e.t.Helper()
	res, err := e.repo.Search(context.Background(), &fhirsearch.Request{ResourceType: "Login", Count: 1000, Total: "none"})
	if err != nil {
		e.t.Fatal(err)
	}
	var out []fhir.Login
	for _, m := range res.Matches {
		out = append(out, *m.(*fhir.Login))
	}
	return out
}

func parseRedirect(t *testing.T, raw string) (*url.URL, url.Values) {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u, u.Query()
}

// ---- state

func TestExternalStateIsSignedBoundToTheBrowserAndExpires(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.extFixture(nil)
		state, cookie := f.start(domain.ExternalState{ClientID: f.client.ID, RedirectURI: extRedirect})
		if _, err := f.callback(state, cookie, nil); err != nil {
			t.Fatalf("đúng thì phải qua: %v", err)
		}
		bad := func(name, st, ck string) {
			t.Helper()
			_, err := f.callback(st, ck, nil)
			wantErr(t, err, domain.KindBadRequest, "Invalid state")
			_ = name
		}
		bad("sai cookie", state, "00000000000000000000000000000000")
		bad("thiếu cookie", state, "")
		payload, sig, _ := strings.Cut(state, ".")
		bad("sai chữ ký", payload+"."+strings.Repeat("A", len(sig)), cookie)
		bad("bị sửa payload", "A"+payload[1:]+"."+sig, cookie)
		bad("không có chữ ký", payload, cookie)
		bad("rác", "khong.phai.state", cookie)
		bad("quá lớn", strings.Repeat("a", 9<<10), cookie)
		// state do instance KHÁC (khóa khác) ký không dùng được
		other := e.extFixture(func(x *extFix) {})
		d := other.svc.Deps
		d.MFA = nil
		d.External.StateKey = []byte(strings.Repeat("z", 32))
		otherSvc := application.NewService(d)
		_, err := otherSvc.ExternalCallback(context.Background(), application.ExternalCallbackRequest{Code: "c", State: state, CookieNonce: cookie})
		wantErr(t, err, domain.KindBadRequest, "Invalid state")
		// hết hạn
		e.clock.t = e.clock.t.Add(10*time.Minute + time.Second)
		bad("hết hạn", state, cookie)
		// khác chữ ký của cùng nội dung: state phát lúc này có hạn mới
		e.clock.t = e.clock.t.Add(-10*time.Minute - time.Second)
		s2, c2 := f.start(domain.ExternalState{})
		e.clock.t = e.clock.t.Add(9*time.Minute + 59*time.Second)
		if _, err := f.callback(s2, c2, func(r *application.ExternalCallbackRequest) { r.CallbackClientID = "" }); err != nil && !domain.Is(err, domain.KindBadRequest, "") {
			t.Errorf("còn hạn 9 phút 59 giây: %v", err)
		}
	})
}

func TestExternalCallbackMissingParameters(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.extFixture(nil)
		state, cookie := f.start(domain.ExternalState{})
		_, err := f.callback(state, cookie, func(r *application.ExternalCallbackRequest) { r.Code = "" })
		wantErr(t, err, domain.KindBadRequest, "Missing code")
		_, err = f.callback("", cookie, nil)
		wantErr(t, err, domain.KindBadRequest, "Missing state")
		if len(f.oidc.calls) != 0 {
			t.Error("thiếu tham số thì chưa được gọi IdP")
		}
	})
}

func TestExternalLoginDisabledWithoutStateKey(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.extFixture(nil)
		d := f.svc.Deps
		d.MFA = nil
		d.External.StateKey = nil
		svc := application.NewService(d)
		// /auth/method vẫn báo IdP của domain nhưng KHÔNG phát state (không cookie)
		res, nonce, err := svc.StartExternalLogin(context.Background(), extEmail, domain.ExternalState{})
		if err != nil || res == nil || nonce != "" || strings.Contains(res.AuthorizeURL, "state=") {
			t.Errorf("%+v %q %v", res, nonce, err)
		}
		// khóa quá ngắn cũng coi như chưa bật
		d.External.StateKey = []byte("ngan")
		if res, nonce, _ := application.NewService(d).StartExternalLogin(context.Background(), extEmail, domain.ExternalState{}); nonce != "" || strings.Contains(res.AuthorizeURL, "state=") {
			t.Error("khóa ngắn không được dùng")
		}
		_, err = svc.ExternalCallback(context.Background(), application.ExternalCallbackRequest{Code: "c", State: "s", CookieNonce: "n"})
		wantErr(t, err, domain.KindBadRequest, "Invalid state")
		_, _, err = svc.IssueExternalState(domain.ExternalState{})
		wantErr(t, err, domain.KindInternal, "")
	})
}

func TestStartExternalLoginPutsOnlyServerFieldsInTheSignedState(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.extFixture(nil)
		// tên miền hạ chữ thường và do SERVER quyết định (client không ép domain khác vào state)
		res, nonce, err := f.svc.StartExternalLogin(context.Background(), "An@CORP.Example.org", domain.ExternalState{Domain: "evil.example.org", ClientID: f.client.ID})
		if err != nil || res.Domain != "corp.example.org" || nonce == "" || len(nonce) != 32 {
			t.Fatalf("%+v %q %v", res, nonce, err)
		}
		u, _ := url.Parse(res.AuthorizeURL)
		if u.Query().Get("redirect_uri") != "https://auth.example.org/auth/external" || u.Query().Get("client_id") != "cid" {
			t.Errorf("%s", res.AuthorizeURL)
		}
		// mỗi lần gọi một nonce mới
		_, n2, _ := f.svc.StartExternalLogin(context.Background(), extEmail, domain.ExternalState{})
		if n2 == nonce {
			t.Error("nonce phải ngẫu nhiên")
		}
		// domain không có IdP: không state, không cookie
		if r, n, err := f.svc.StartExternalLogin(context.Background(), "an@khac.example.org", domain.ExternalState{}); err != nil || r != nil || n != "" {
			t.Errorf("%+v %q %v", r, n, err)
		}
		// authorizeUrl hỏng: lỗi, không phát state
		put(e, &fhir.DomainConfiguration{ResourceType: "DomainConfiguration", Domain: "hong.example.org", IdentityProvider: &fhir.IdentityProvider{AuthorizeUrl: "khong-phai-url", ClientId: "c"}})
		_, n3, err := f.svc.StartExternalLogin(context.Background(), "an@hong.example.org", domain.ExternalState{})
		wantErr(t, err, domain.KindBadRequest, "Failed to construct URL for the domain")
		if n3 != "" {
			t.Error("lỗi thì không phát cookie")
		}
	})
}

// ---- luồng thành công

func TestExternalCallbackSuccessWithClientRedirect(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.extFixture(nil)
		state, cookie := f.start(domain.ExternalState{ClientID: f.client.ID, Scope: "openid profile", Nonce: "n-1", RedirectURI: extRedirect + "?x=1"})
		// redirectUri của state không khớp chính xác (có query) => từ chối; dùng đúng URI đã đăng ký
		_, err := f.callback(state, cookie, nil)
		wantErr(t, err, domain.KindBadRequest, "Invalid redirect URI")
		f.oidc.calls, f.ver.calls = nil, nil
		state, cookie = f.start(domain.ExternalState{ClientID: f.client.ID, Scope: "openid profile", Nonce: "n-1", RedirectURI: extRedirect})
		to, err := f.callback(state, cookie, nil)
		if err != nil {
			t.Fatal(err)
		}
		u, q := parseRedirect(t, to)
		if u.Scheme+"://"+u.Host+u.Path != extRedirect || q.Get("code") == "" || q.Get("login") == "" {
			t.Fatalf("redirect: %s", to)
		}
		lg := e.getLogin(q.Get("login"))
		if lg.AuthMethod != domain.AuthExternal || lg.Membership == nil || refOf(lg.User) != "User/"+f.user.ID || lg.Scope != "openid profile" || lg.Nonce != "n-1" ||
			refOf(lg.Client) != "ClientApplication/"+f.client.ID || lg.RemoteAddress != "1.2.3.4" || lg.UserAgent != "ua" {
			t.Errorf("login: %+v", lg)
		}
		if lg.Code != domain.HashSecret(q.Get("code")) {
			t.Error("kho chỉ giữ băm của code")
		}
		if !domain.IsTrue(e.reload(f.user).EmailVerified) {
			t.Error("IdP đã xác thực nên email phải được đánh dấu đã xác minh")
		}
		// gọi IdP đúng tham số
		if len(f.oidc.calls) != 1 || f.oidc.calls[0].Code != "ma-code" || f.oidc.calls[0].RedirectURI != "https://auth.example.org/auth/external" || f.oidc.calls[0].CodeVerifier != "" {
			t.Errorf("ExchangeCode: %+v", f.oidc.calls)
		}
		if len(f.ver.calls) != 1 || f.ver.calls[0] != (verifyCall{"id.token.sig", "https://idp.example.org/jwks", "https://idp.example.org", "cid"}) {
			t.Errorf("VerifyJWKS: %+v", f.ver.calls)
		}
	})
}

func TestExternalCallbackRedirectsToTheAppSignInPage(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.extFixture(nil)
		// không redirectUri: về trang đăng nhập của app kèm login và các tham số PKCE để app hoàn tất
		state, cookie := f.start(domain.ExternalState{ClientID: f.client.ID, Scope: "openid", Nonce: "nn", CodeChallenge: "chal", CodeChallengeMethod: "S256"})
		to, err := f.callback(state, cookie, nil)
		if err != nil {
			t.Fatal(err)
		}
		u, q := parseRedirect(t, to)
		if u.Scheme+"://"+u.Host+u.Path != "https://app.example.org/signin" || q.Get("login") == "" || q.Get("scope") != "openid" || q.Get("nonce") != "nn" ||
			q.Get("code_challenge") != "chal" || q.Get("code_challenge_method") != "S256" || q.Has("code") {
			t.Fatalf("%s", to)
		}
		// có launch: về /oauth
		put(e, &fhir.SmartAppLaunch{ResourceType: "SmartAppLaunch"})
		launch := put(e, &fhir.SmartAppLaunch{ResourceType: "SmartAppLaunch"})
		state, cookie = f.start(domain.ExternalState{ClientID: f.client.ID, Launch: launch.ID})
		to, err = f.callback(state, cookie, nil)
		if err != nil {
			t.Fatal(err)
		}
		if u, _ := parseRedirect(t, to); u.Path != "/oauth" {
			t.Errorf("launch: %s", to)
		}
	})
}

func TestExternalCallbackReturnToMustBeAllowedByTheDomain(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.extFixture(nil)
		for name, tc := range map[string]struct{ returnTo, wantPrefix string }{
			"được phép":      {"https://app.example.org/signin/next", "https://app.example.org/signin/next"},
			"khác host":      {"https://evil.example.org/signin", "https://app.example.org/signin"},
			"khác đường dẫn": {"https://app.example.org/admin", "https://app.example.org/signin"},
			"javascript":     {"javascript:alert(1)", "https://app.example.org/signin"},
		} {
			// luồng domain (không client): returnTo được kiểm với allowedPostLoginRedirectUrls
			state, cookie := f.start(domain.ExternalState{ReturnTo: tc.returnTo})
			to, err := f.callback(state, cookie, nil)
			if err != nil || !strings.HasPrefix(to, tc.wantPrefix) {
				t.Errorf("%s: %s %v", name, to, err)
			}
		}
	})
}

// ---- chọn IdP

func TestExternalCallbackSelectsTheIdentityProvider(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.extFixture(nil)
		// callback RIÊNG của client: dùng client.identityProvider, không thử nguồn khác (kể cả domain)
		clientIdP := &fhir.IdentityProvider{TokenUrl: "https://client-idp.example.org/token", ClientId: "client-cid", ClientSecret: "s", JwksUrl: "https://client-idp.example.org/jwks", Issuer: "https://client-idp.example.org"}
		withIdP := put(e, &fhir.ClientApplication{ResourceType: "ClientApplication", Name: "Riêng", Meta: &fhir.Meta{Project: f.project.ID}, IdentityProvider: clientIdP, PkceOptional: ptr(true)})
		state, cookie := f.start(domain.ExternalState{ClientID: withIdP.ID})
		if _, err := f.callback(state, cookie, func(r *application.ExternalCallbackRequest) { r.CallbackClientID = withIdP.ID }); err != nil {
			t.Fatal(err)
		}
		if f.oidc.idps[0] != nil && f.oidc.idps[0].ClientId != "client-cid" {
			t.Errorf("phải dùng IdP của client: %+v", f.oidc.idps[0])
		}
		if f.oidc.calls[0].RedirectURI != "https://auth.example.org/auth/external/"+withIdP.ID {
			t.Errorf("redirect_uri của callback riêng: %q", f.oidc.calls[0].RedirectURI)
		}
		// client trên callback riêng mà state mang client khác
		_, err := f.callback(state, cookie, func(r *application.ExternalCallbackRequest) { r.CallbackClientID = f.client.ID })
		wantErr(t, err, domain.KindBadRequest, "Invalid client")
		// callback riêng của client KHÔNG có IdP: không rơi về domain
		s2, c2 := f.start(domain.ExternalState{ClientID: f.client.ID})
		_, err = f.callback(s2, c2, func(r *application.ExternalCallbackRequest) { r.CallbackClientID = f.client.ID })
		wantErr(t, err, domain.KindBadRequest, "Identity provider not found")
		// client không tồn tại ở callback riêng: 404
		s3, c3 := f.start(domain.ExternalState{ClientID: domain.NewID()})
		_, err = f.callback(s3, c3, func(r *application.ExternalCallbackRequest) { r.CallbackClientID = domain.NewID() })
		wantErr(t, err, domain.KindBadRequest, "Invalid client")
		_, err = f.callback(s3, c3, nil)
		wantErr(t, err, domain.KindNotFound, "")
	})
}

func TestExternalCallbackLegacyClientIdentityProviderRules(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.extFixture(nil)
		clientIdP := &fhir.IdentityProvider{TokenUrl: "https://c/token", ClientId: "c", ClientSecret: "s", JwksUrl: "https://c/jwks", Issuer: "https://c"}
		mk := func(project string) *fhir.ClientApplication {
			c := &fhir.ClientApplication{ResourceType: "ClientApplication", Name: "X", IdentityProvider: clientIdP, PkceOptional: ptr(true)}
			if project != "" {
				c.Meta = &fhir.Meta{Project: project}
			}
			return put(e, c)
		}
		try := func(c *fhir.ClientApplication) error {
			state, cookie := f.start(domain.ExternalState{ClientID: c.ID})
			_, err := f.callback(state, cookie, nil)
			return err
		}
		// client trong project mà project chưa bật systemSetting legacyExternalAuth: phải dùng callback riêng, KHÔNG gọi mạng
		c1 := mk(f.project.ID)
		err := try(c1)
		wantErr(t, err, domain.KindBadRequest, "Client identity provider must use the per-client callback https://auth.example.org/auth/external/"+c1.ID)
		if len(f.oidc.calls) != 0 {
			t.Error("không được gọi IdP")
		}
		// Project.setting (admin project sửa được) KHÔNG có tác dụng: chỉ systemSetting (super admin)
		setting := e.project(func(p *fhir.Project) {
			p.Setting = []fhir.ProjectSetting{{Name: "legacyExternalAuth", ValueBoolean: ptr(true)}}
		})
		wantErr(t, try(mk(setting.ID)), domain.KindBadRequest, "")
		// systemSetting bật: được
		system := e.project(func(p *fhir.Project) {
			p.SystemSetting = []fhir.ProjectSetting{{Name: "legacyExternalAuth", ValueBoolean: ptr(true)}}
		})
		if err := try(mk(system.ID)); err != nil && !domain.Is(err, domain.KindBadRequest, "User not found") {
			t.Errorf("systemSetting bật phải qua bước chọn IdP (chỉ dừng ở tìm user): %v", err)
		}
		if len(f.oidc.calls) == 0 {
			t.Error("systemSetting bật: phải gọi IdP")
		}
		// client không thuộc project nào: luôn cho
		if err := try(mk("")); err != nil && !domain.Is(err, domain.KindBadRequest, "User not found") {
			t.Errorf("client không project: %v", err)
		}
	})
}

func TestExternalCallbackIdentityProviderNotFoundAndIssuerLookup(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.extFixture(nil)
		// không domain, không client, không issuer
		state, cookie := f.start(domain.ExternalState{})
		_ = state
		bare, nonce, err := f.svc.IssueExternalState(domain.ExternalState{})
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.callback(bare, nonce, nil)
		wantErr(t, err, domain.KindBadRequest, "Identity provider not found")
		_ = cookie
		// issuer của state tra trong cấu hình máy chủ
		d := f.svc.Deps
		d.MFA = nil
		d.External.Providers = []application.ExternalProvider{{Issuer: "https://srv-idp.example.org", IdentityProvider: &fhir.IdentityProvider{TokenUrl: "https://s/token", ClientId: "scid", ClientSecret: "s", JwksUrl: "https://s/jwks", Issuer: "https://srv-idp.example.org"}}}
		svc := application.NewService(d)
		st, n2, _ := svc.IssueExternalState(domain.ExternalState{Issuer: "https://srv-idp.example.org"})
		if _, err := svc.ExternalCallback(context.Background(), application.ExternalCallbackRequest{Code: "c", State: st, CookieNonce: n2}); err != nil && !domain.Is(err, domain.KindBadRequest, "User not found") {
			t.Errorf("issuer cấu hình ở máy chủ: %v", err)
		}
		if got := f.oidc.idps; len(got) == 0 || got[len(got)-1].ClientId != "scid" {
			t.Errorf("phải dùng IdP của máy chủ: %+v", got)
		}
		// issuer lạ
		st2, n3, _ := svc.IssueExternalState(domain.ExternalState{Issuer: "https://la.example.org"})
		_, err = svc.ExternalCallback(context.Background(), application.ExternalCallbackRequest{Code: "c", State: st2, CookieNonce: n3})
		wantErr(t, err, domain.KindBadRequest, "Identity provider not found")
	})
}

func TestExternalCallbackIssuerParameter(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.extFixture(nil)
		state, cookie := f.start(domain.ExternalState{ClientID: f.client.ID, RedirectURI: extRedirect})
		_, err := f.callback(state, cookie, func(r *application.ExternalCallbackRequest) { r.Issuer = "https://evil.example.org" })
		wantErr(t, err, domain.KindBadRequest, "Invalid issuer")
		if len(f.oidc.calls) != 0 {
			t.Error("sai issuer thì chưa được gọi IdP (RFC 9207: chống mix-up)")
		}
		state, cookie = f.start(domain.ExternalState{ClientID: f.client.ID, RedirectURI: extRedirect})
		if _, err := f.callback(state, cookie, func(r *application.ExternalCallbackRequest) { r.Issuer = "https://idp.example.org" }); err != nil {
			t.Errorf("iss khớp: %v", err)
		}
		// iss vắng mặt hoặc idp.issuer không cấu hình: bỏ qua
		state, cookie = f.start(domain.ExternalState{ClientID: f.client.ID, RedirectURI: extRedirect})
		if _, err := f.callback(state, cookie, nil); err != nil {
			t.Errorf("không có iss: %v", err)
		}
	})
}

// ---- xác minh id_token

func TestExternalCallbackRequiresJWKSOutsideDevelopment(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.extFixture(func(x *extFix) { x.idp.JwksUrl = "" })
		state, cookie := f.start(domain.ExternalState{})
		_, err := f.callback(state, cookie, nil)
		wantErr(t, err, domain.KindBadRequest, "Missing JWKS URL for external identity provider")
		if len(f.oidc.calls) != 0 || len(e.logins()) != 0 {
			t.Error("không có jwksUrl thì không được gọi IdP hay tạo Login (id_token không có chữ ký để tin)")
		}
		// dev/test: cho phép id_token không kiểm chữ ký
		d := f.svc.Deps
		d.MFA = nil
		d.External.AllowUnsafeOutbound = true
		svc := application.NewService(d)
		st, n, _ := svc.IssueExternalState(domain.ExternalState{Domain: "corp.example.org"})
		if _, err := svc.ExternalCallback(context.Background(), application.ExternalCallbackRequest{Code: "c", State: st, CookieNonce: n}); err != nil {
			t.Fatal(err)
		}
		if f.ver.unsigned != 1 || len(f.ver.calls) != 0 {
			t.Errorf("phải dùng ParseUnsigned: %d %d", f.ver.unsigned, len(f.ver.calls))
		}
	})
}

func TestExternalCallbackIDTokenVerificationInputs(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.extFixture(func(x *extFix) { x.idp.Audience = "aud-rieng"; x.idp.UsePkce = ptr(true) })
		state, cookie := f.start(domain.ExternalState{CodeChallenge: "ma-challenge", CodeChallengeMethod: "S256"})
		if _, err := f.callback(state, cookie, nil); err != nil {
			t.Fatal(err)
		}
		if f.ver.calls[0].audience != "aud-rieng" {
			t.Errorf("audience cấu hình: %+v", f.ver.calls[0])
		}
		// usePkce: code_verifier là chính code_challenge của app (hành vi của Medplum, mục 0.7)
		if f.oidc.calls[0].CodeVerifier != "ma-challenge" {
			t.Errorf("CodeVerifier: %q", f.oidc.calls[0].CodeVerifier)
		}
		// thiếu thiết bị PKCE: không gửi verifier
		f.oidc.calls = nil
		if _, err := f.run(domain.ExternalState{}); err != nil || f.oidc.calls[0].CodeVerifier != "" {
			t.Errorf("không có challenge thì không có verifier: %v %+v", err, f.oidc.calls)
		}
	})
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.extFixture(func(x *extFix) { x.idp.UsePkce = nil })
		if _, err := f.run(domain.ExternalState{CodeChallenge: "c", CodeChallengeMethod: "S256"}); err != nil || f.oidc.calls[0].CodeVerifier != "" {
			t.Errorf("usePkce tắt thì không gửi verifier: %v %+v", err, f.oidc.calls)
		}
	})
}

func TestExternalCallbackIDTokenFailures(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.extFixture(func(x *extFix) { x.idp.Issuer = "" })
		_, err := f.run(domain.ExternalState{})
		wantErr(t, err, domain.KindBadRequest, "Missing issuer for external identity provider")
	})
	// chữ ký/iss/aud sai: cùng một thông báo, không lộ lý do
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.extFixture(nil)
		f.ver.err = errors.New("chi tiết nội bộ: chữ ký sai")
		_, err := f.run(domain.ExternalState{})
		wantErr(t, err, domain.KindBadRequest, "Failed to verify code - check your identity provider configuration")
		if strings.Contains(err.Error(), "nội bộ") {
			t.Error("không lộ lý do xác minh thất bại")
		}
		if len(e.logins()) != 0 {
			t.Error("id_token sai thì không tạo Login")
		}
	})
	// lỗi của IdP khi đổi code: truyền nguyên (đã là thông báo cố định) và không xác minh
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.extFixture(nil)
		f.oidc.err = domain.BadRequest("Failed to verify code - check your identity provider configuration")
		_, err := f.run(domain.ExternalState{})
		wantErr(t, err, domain.KindBadRequest, "Failed to verify code - check your identity provider configuration")
		if len(f.ver.calls) != 0 {
			t.Error("đổi code lỗi thì không xác minh")
		}
	})
	// thiếu phụ thuộc: lỗi nội bộ, không bao giờ cho qua
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.extFixture(nil)
		d := f.svc.Deps
		d.MFA, d.OIDC = nil, nil
		svc := application.NewService(d)
		st, n, _ := svc.IssueExternalState(domain.ExternalState{Domain: "corp.example.org"})
		_, err := svc.ExternalCallback(context.Background(), application.ExternalCallbackRequest{Code: "c", State: st, CookieNonce: n})
		wantErr(t, err, domain.KindInternal, "")
	})
}

// ---- danh tính

func TestExternalCallbackIdentityRules(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		// Q2: IdP báo email CHƯA xác minh thì từ chối, và chưa tạo Login hay đánh dấu emailVerified
		f := e.extFixture(func(x *extFix) { x.ver.claims = map[string]any{"email": extEmail, "email_verified": false} })
		state, cookie := f.start(domain.ExternalState{})
		_, err := f.callback(state, cookie, nil)
		wantErr(t, err, domain.KindBadRequest, "Email address is not verified by the identity provider")
		if len(e.logins()) != 0 || domain.IsTrue(e.reload(f.user).EmailVerified) {
			t.Error("email chưa xác minh: không tạo Login, không đánh dấu emailVerified")
		}
		f.ver.claims = map[string]any{"email": extEmail, "email_verified": "false"}
		wantErr(t, mustErr(f.run(domain.ExternalState{})), domain.KindBadRequest, "Email address is not verified by the identity provider")
		// email_verified vắng mặt: không phải "chưa xác minh"
		f.ver.claims = map[string]any{"email": extEmail}
		if _, err := f.run(domain.ExternalState{}); err != nil {
			t.Errorf("không có email_verified: %v", err)
		}
		// email của IdP không thuộc tên miền của state
		f.ver.claims = map[string]any{"email": "an@khac.example.org", "email_verified": true}
		wantErr(t, mustErr(f.run(domain.ExternalState{})), domain.KindBadRequest, "Email address does not match domain")
		// hậu tố giống nhưng không phải tên miền con đúng nghĩa: "@" là một phần của hậu tố
		f.ver.claims = map[string]any{"email": "an@evilcorp.example.org", "email_verified": true}
		wantErr(t, mustErr(f.run(domain.ExternalState{})), domain.KindBadRequest, "Email address does not match domain")
		// claim thiếu email
		f.ver.claims = map[string]any{"sub": "x"}
		wantErr(t, mustErr(f.run(domain.ExternalState{})), domain.KindBadRequest, "External token does not contain email address")
		// danh sách email KHÔNG được tách: ra "User not found", không đăng nhập vào user nào
		f.ver.claims = map[string]any{"email": extEmail + ",admin@corp.example.org"}
		wantErr(t, mustErr(f.run(domain.ExternalState{})), domain.KindBadRequest, "User not found")
		// email viết hoa được hạ chữ thường trước khi tìm
		f.ver.claims = map[string]any{"email": "AN@Corp.Example.ORG", "email_verified": true}
		if _, err := f.run(domain.ExternalState{}); err != nil {
			t.Errorf("email hoa: %v", err)
		}
	})
}

func mustErr(_ string, err error) error { return err }

func TestExternalCallbackUnknownUserCreatesNoLogin(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.extFixture(func(x *extFix) { x.ver.claims = map[string]any{"email": "la@corp.example.org", "email_verified": true} })
		state, cookie := f.start(domain.ExternalState{})
		_, err := f.callback(state, cookie, nil)
		wantErr(t, err, domain.KindBadRequest, "User not found")
		// user có nhưng không có membership nào: cũng không tạo Login (Q10; Medplum tạo rồi mới báo lỗi)
		e.user("khong-membership@corp.example.org", nil)
		f.ver.claims = map[string]any{"email": "khong-membership@corp.example.org", "email_verified": true}
		wantErr(t, mustErr(f.run(domain.ExternalState{ClientID: f.client.ID})), domain.KindBadRequest, "User not found")
		if n := len(e.logins()); n != 0 {
			t.Errorf("thất bại không được để lại Login rác: %d", n)
		}
	})
}

func TestExternalCallbackBySubjectUsesProjectMembershipExternalID(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.extFixture(func(x *extFix) { x.ver.claims = map[string]any{"sub": "ext-123", "email": "bat-ky@corp.example.org"} })
		own := *f.idp
		own.UseSubject = ptr(true)
		client := put(e, &fhir.ClientApplication{ResourceType: "ClientApplication", Name: "Sub", Meta: &fhir.Meta{Project: f.project.ID}, IdentityProvider: &own, PkceOptional: ptr(true)})
		login := func(clientID string) (string, error) {
			res, nonce, err := f.svc.StartClientExternalLogin(context.Background(), clientID, domain.ExternalState{})
			if err != nil {
				t.Fatal(err)
			}
			u, _ := url.Parse(res.AuthorizeURL)
			return f.callback(u.Query().Get("state"), nonce, func(r *application.ExternalCallbackRequest) { r.CallbackClientID = clientID })
		}
		// membership chưa có externalId: không tìm thấy
		_, err := login(client.ID)
		wantErr(t, err, domain.KindBadRequest, "User not found")
		// gắn externalId vào membership: đăng nhập được, KHÔNG phụ thuộc email
		if _, err := e.repo.UpdateResource(context.Background(), withExternalID(f.member, "ext-123"), fhirUpdate()); err != nil {
			t.Fatal(err)
		}
		to, err := login(client.ID)
		if err != nil {
			t.Fatal(err)
		}
		_, q := parseRedirect(t, to)
		if lg := e.getLogin(q.Get("login")); refOf(lg.User) != "User/"+f.user.ID || lg.Membership == nil {
			t.Errorf("phải đăng nhập đúng user của membership: %+v", lg)
		}
		// externalId khớp CHÍNH XÁC: "ext-12", "ext-1234", biến thể hoa và có khoảng trắng đều không khớp
		for _, sub := range []string{"ext-12", "ext-1234", "EXT-123", "ext-123 "} {
			f.ver.claims = map[string]any{"sub": sub}
			_, err := login(client.ID)
			wantErr(t, err, domain.KindBadRequest, "User not found")
		}
		// externalId của project KHÁC không dùng được
		other := e.project(nil)
		oc := put(e, &fhir.ClientApplication{ResourceType: "ClientApplication", Name: "O", Meta: &fhir.Meta{Project: other.ID}, IdentityProvider: &own, PkceOptional: ptr(true)})
		f.ver.claims = map[string]any{"sub": "ext-123"}
		_, err = login(oc.ID)
		wantErr(t, err, domain.KindBadRequest, "User not found")
		// thiếu sub
		f.ver.claims = map[string]any{"email": "a@corp.example.org"}
		_, err = login(client.ID)
		wantErr(t, err, domain.KindBadRequest, "External token does not contain subject")
	})
}

func TestStartClientExternalLogin(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.extFixture(nil)
		ctx := context.Background()
		own := *f.idp
		own.AuthorizeUrl = "https://client-idp.example.org/authorize"
		c := put(e, &fhir.ClientApplication{ResourceType: "ClientApplication", Name: "Riêng", Meta: &fhir.Meta{Project: f.project.ID}, IdentityProvider: &own, RedirectUris: []string{extRedirect}, PkceOptional: ptr(true)})
		res, nonce, err := f.svc.StartClientExternalLogin(ctx, c.ID, domain.ExternalState{ClientID: "client-khac", Domain: "evil.example.org", RedirectURI: extRedirect})
		if err != nil || nonce == "" || res.Domain != "" {
			t.Fatalf("%+v %q %v", res, nonce, err)
		}
		u, _ := url.Parse(res.AuthorizeURL)
		if u.Host != "client-idp.example.org" || u.Query().Get("redirect_uri") != "https://auth.example.org/auth/external/"+c.ID || u.Query().Get("client_id") != "cid" {
			t.Errorf("%s", res.AuthorizeURL)
		}
		// state do SERVER quyết định client và domain: giá trị app gửi bị ghi đè
		to, err := f.callback(u.Query().Get("state"), nonce, func(r *application.ExternalCallbackRequest) { r.CallbackClientID = c.ID })
		if err != nil && !domain.Is(err, domain.KindBadRequest, "User not found") {
			t.Errorf("state phải mang đúng client: %s %v", to, err)
		}
		// lỗi
		_, _, err = f.svc.StartClientExternalLogin(ctx, f.client.ID, domain.ExternalState{})
		wantErr(t, err, domain.KindBadRequest, "Client does not have an identity provider")
		_, _, err = f.svc.StartClientExternalLogin(ctx, c.ID, domain.ExternalState{RedirectURI: "https://evil.example.org/cb"})
		wantErr(t, err, domain.KindBadRequest, "Invalid redirect URI")
		_, _, err = f.svc.StartClientExternalLogin(ctx, domain.NewID(), domain.ExternalState{})
		wantErr(t, err, domain.KindNotFound, "")
		bad := put(e, &fhir.ClientApplication{ResourceType: "ClientApplication", Name: "Hỏng", IdentityProvider: &fhir.IdentityProvider{AuthorizeUrl: "khong-phai-url", ClientId: "c"}})
		_, _, err = f.svc.StartClientExternalLogin(ctx, bad.ID, domain.ExternalState{})
		wantErr(t, err, domain.KindBadRequest, "Failed to construct URL for the domain")
		// chưa bật (không khóa ký state)
		d := f.svc.Deps
		d.MFA = nil
		d.External.StateKey = nil
		_, _, err = application.NewService(d).StartClientExternalLogin(ctx, c.ID, domain.ExternalState{})
		wantErr(t, err, domain.KindInternal, "")
	})
}

func withExternalID(m *fhir.ProjectMembership, id string) *fhir.ProjectMembership {
	c := *m
	c.ExternalId = id
	return &c
}

// ---- client và project

func TestExternalCallbackClientProjectRules(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.extFixture(nil)
		other := e.project(nil)
		// state mang projectId khác project của client
		state, cookie := f.start(domain.ExternalState{ClientID: f.client.ID, ProjectID: other.ID})
		_, err := f.callback(state, cookie, nil)
		wantErr(t, err, domain.KindBadRequest, "Invalid project")
		// projectId trùng thì được
		state, cookie = f.start(domain.ExternalState{ClientID: f.client.ID, ProjectID: f.project.ID})
		if _, err := f.callback(state, cookie, nil); err != nil {
			t.Errorf("projectId trùng: %v", err)
		}
		// project của client quyết định phạm vi tìm user: user của project KHÁC không đăng nhập được qua client này
		stranger := e.user("la@corp.example.org", nil)
		e.membership(stranger, other, nil)
		f.ver.claims = map[string]any{"email": "la@corp.example.org", "email_verified": true}
		state, cookie = f.start(domain.ExternalState{ClientID: f.client.ID})
		_, err = f.callback(state, cookie, nil)
		wantErr(t, err, domain.KindBadRequest, "User not found")
	})
}

func TestExternalCallbackRedirectURIIsCheckedBeforeAnySideEffect(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.extFixture(nil)
		state, cookie := f.start(domain.ExternalState{ClientID: f.client.ID, RedirectURI: "https://evil.example.org/cb"})
		_, err := f.callback(state, cookie, nil)
		wantErr(t, err, domain.KindBadRequest, "Invalid redirect URI")
		if len(e.logins()) != 0 || domain.IsTrue(e.reload(f.user).EmailVerified) {
			t.Error("redirect sai: không tạo Login, không ghi emailVerified (Medplum làm cả hai rồi mới báo lỗi)")
		}
		if len(f.oidc.calls) != 1 {
			t.Errorf("id_token đã được xác minh trước khi kiểm redirect: %d", len(f.oidc.calls))
		}
		// khớp một phần chỉ khi project bật allow-dangerous-redirect
		partial := extRedirect + "/sau/hon"
		state, cookie = f.start(domain.ExternalState{ClientID: f.client.ID, RedirectURI: partial})
		_, err = f.callback(state, cookie, nil)
		wantErr(t, err, domain.KindBadRequest, "Invalid redirect URI")
		dangerous := e.project(func(p *fhir.Project) {
			p.Setting = []fhir.ProjectSetting{{Name: "allow-dangerous-redirect", ValueBoolean: ptr(true)}}
		})
		du := e.user("nguy-hiem@corp.example.org", nil)
		e.membership(du, dangerous, nil)
		dc := put(e, &fhir.ClientApplication{ResourceType: "ClientApplication", Name: "D", Meta: &fhir.Meta{Project: dangerous.ID}, RedirectUris: []string{extRedirect}, PkceOptional: ptr(true)})
		f.ver.claims = map[string]any{"email": "nguy-hiem@corp.example.org", "email_verified": true}
		state, cookie = f.start(domain.ExternalState{ClientID: dc.ID, RedirectURI: partial})
		to, err := f.callback(state, cookie, nil)
		if err != nil || !strings.HasPrefix(to, partial+"?") {
			t.Fatalf("khớp một phần khi project cho phép: %s %v", to, err)
		}
		// khác origin vẫn bị từ chối kể cả khi cho phép khớp một phần
		state, cookie = f.start(domain.ExternalState{ClientID: dc.ID, RedirectURI: "https://evil.example.org/cb/sau"})
		_, err = f.callback(state, cookie, nil)
		wantErr(t, err, domain.KindBadRequest, "Invalid redirect URI")
	})
}

func TestExternalCallbackEmailVerifiedIsIdempotent(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.extFixture(nil)
		for i := 0; i < 2; i++ {
			if _, err := f.run(domain.ExternalState{ClientID: f.client.ID}); err != nil {
				t.Fatal(err)
			}
		}
		u := e.reload(f.user)
		again, _ := e.r.Users.Get(context.Background(), f.user.ID)
		if !domain.IsTrue(u.EmailVerified) || u.Meta.VersionId != again.Meta.VersionId {
			t.Error("emailVerified")
		}
		v1 := u.Meta.VersionId
		if _, err := f.run(domain.ExternalState{ClientID: f.client.ID}); err != nil {
			t.Fatal(err)
		}
		if e.reload(f.user).Meta.VersionId != v1 {
			t.Error("đã xác minh rồi thì không được tạo phiên bản mới của User")
		}
	})
}

// ---- clientinfo

func TestClientInfo(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		form := &fhir.ClientApplicationSignInForm{}
		c := put(e, &fhir.ClientApplication{ResourceType: "ClientApplication", Name: "App", Secret: "BI-MAT-KHONG-LO", RedirectUris: []string{"https://app.example.org/cb"}, SignInForm: form})
		got, err := e.svc.ClientInfo(ctx, c.ID, nil)
		if err != nil || got == nil {
			t.Fatalf("%+v %v", got, err)
		}
		empty := ""
		_, err = e.svc.ClientInfo(ctx, c.ID, &empty)
		wantErr(t, err, domain.KindBadRequest, "Invalid redirect URI") // gửi redirect_uri rỗng vẫn là có gửi
		ok := "https://app.example.org/cb"
		if _, err := e.svc.ClientInfo(ctx, c.ID, &ok); err != nil {
			t.Error(err)
		}
		for _, bad := range []string{"https://app.example.org/cb/", "https://app.example.org/cb?x=1", "https://evil.example.org/cb"} {
			bad := bad
			_, err := e.svc.ClientInfo(ctx, c.ID, &bad)
			wantErr(t, err, domain.KindBadRequest, "Invalid redirect URI")
		}
		_, err = e.svc.ClientInfo(ctx, domain.NewID(), nil)
		wantErr(t, err, domain.KindNotFound, "")
		// client không có signInForm: trả {} (không nil)
		bare := put(e, &fhir.ClientApplication{ResourceType: "ClientApplication", Name: "Tr"})
		if g, err := e.svc.ClientInfo(ctx, bare.ID, nil); err != nil || g == nil {
			t.Errorf("%+v %v", g, err)
		}
		// client dựng sẵn (medplum-cli) tra được mà không cần có trong kho
		if _, err := e.svc.ClientInfo(ctx, domain.CLIClientID, nil); err != nil {
			t.Errorf("client dựng sẵn: %v", err)
		}
	})
}

func TestExternalStateSizeCapAppliesToValidlySignedStatesToo(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.extFixture(nil)
		// state hợp lệ về chữ ký nhưng quá 8 KiB (ví dụ returnTo khổng lồ): vẫn bị từ chối
		big, cookie, err := f.svc.IssueExternalState(domain.ExternalState{Domain: "corp.example.org", ReturnTo: "https://app.example.org/signin/" + strings.Repeat("a", 9<<10)})
		if err != nil || len(big) <= 8<<10 {
			t.Fatalf("chuẩn bị: %d %v", len(big), err)
		}
		_, err = f.callback(big, cookie, nil)
		wantErr(t, err, domain.KindBadRequest, "Invalid state")
		if len(f.oidc.calls) != 0 {
			t.Error("chưa được gọi IdP")
		}
		// ngay dưới trần thì qua
		ok, c2, _ := f.svc.IssueExternalState(domain.ExternalState{Domain: "corp.example.org", ReturnTo: "https://app.example.org/signin/" + strings.Repeat("a", 4<<10)})
		if len(ok) > 8<<10 {
			t.Fatal("chuẩn bị")
		}
		if _, err := f.callback(ok, c2, nil); err != nil {
			t.Errorf("dưới trần: %v", err)
		}
	})
}

func TestExternalCallbackIssuerIsCheckedEvenWithoutConfiguredIssuer(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		// IdP không cấu hình issuer (chỉ có thể xảy ra khi cho phép id_token không ký, dev/test): tham số iss KHÔNG được bỏ qua mù quáng,
		// nhưng Medplum bỏ qua khi idp.issuer rỗng: giữ hành vi đó
		f := e.extFixture(func(x *extFix) { x.idp.JwksUrl, x.idp.Issuer = "", "" })
		d := f.svc.Deps
		d.MFA = nil
		d.External.AllowUnsafeOutbound = true
		svc := application.NewService(d)
		st, n, _ := svc.IssueExternalState(domain.ExternalState{Domain: "corp.example.org"})
		if _, err := svc.ExternalCallback(context.Background(), application.ExternalCallbackRequest{Code: "c", State: st, CookieNonce: n, Issuer: "https://bat-ky.example.org"}); err != nil {
			t.Errorf("idp.issuer rỗng thì bỏ qua iss: %v", err)
		}
	})
}

func TestExternalCallbackDefaultScopeAndMergedRedirectQuery(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.extFixture(nil)
		// không có scope trong state: mặc định "openid offline_access"
		to, err := f.run(domain.ExternalState{ClientID: f.client.ID})
		if err != nil {
			t.Fatal(err)
		}
		_, q := parseRedirect(t, to)
		if q.Get("scope") != "openid offline_access" {
			t.Errorf("scope mặc định: %q", q.Get("scope"))
		}
		// redirectUri đăng ký có sẵn query: giữ nguyên query đó và thêm login, code
		withQuery := put(e, &fhir.ClientApplication{ResourceType: "ClientApplication", Name: "Q", Meta: &fhir.Meta{Project: f.project.ID}, RedirectUris: []string{extRedirect + "?a=1&b=2"}, PkceOptional: ptr(true)})
		to, err = f.run(domain.ExternalState{ClientID: withQuery.ID, RedirectURI: extRedirect + "?a=1&b=2"})
		if err != nil {
			t.Fatal(err)
		}
		u, q := parseRedirect(t, to)
		if u.Path != "/cb" || q.Get("a") != "1" || q.Get("b") != "2" || q.Get("code") == "" || q.Get("login") == "" {
			t.Errorf("phải gộp query: %s", to)
		}
		// app không thể ghi đè login và code bằng query của redirectUri
		spoof := put(e, &fhir.ClientApplication{ResourceType: "ClientApplication", Name: "S", Meta: &fhir.Meta{Project: f.project.ID}, RedirectUris: []string{extRedirect + "?code=GIA&login=GIA"}, PkceOptional: ptr(true)})
		to, err = f.run(domain.ExternalState{ClientID: spoof.ID, RedirectURI: extRedirect + "?code=GIA&login=GIA"})
		if err != nil {
			t.Fatal(err)
		}
		if _, q := parseRedirect(t, to); q.Get("code") == "GIA" || q.Get("login") == "GIA" {
			t.Errorf("login/code của server phải thắng: %s", to)
		}
	})
}

func TestExternalCallbackPartialRedirectNeedsTheSettingToBeTrue(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.extFixture(nil)
		off := e.project(func(p *fhir.Project) {
			p.Setting = []fhir.ProjectSetting{{Name: "allow-dangerous-redirect", ValueBoolean: ptr(false)}}
		})
		u := e.user("off@corp.example.org", nil)
		e.membership(u, off, nil)
		c := put(e, &fhir.ClientApplication{ResourceType: "ClientApplication", Name: "Off", Meta: &fhir.Meta{Project: off.ID}, RedirectUris: []string{extRedirect}, PkceOptional: ptr(true)})
		f.ver.claims = map[string]any{"email": "off@corp.example.org", "email_verified": true}
		_, err := f.run(domain.ExternalState{ClientID: c.ID, RedirectURI: extRedirect + "/sau/hon"})
		wantErr(t, err, domain.KindBadRequest, "Invalid redirect URI")
	})
}

func TestExternalCallbackPerClientCallbackNeverFallsBackToTheServerProviders(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.extFixture(nil)
		d := f.svc.Deps
		d.MFA = nil
		d.External.Providers = []application.ExternalProvider{{Issuer: "https://srv.example.org", IdentityProvider: f.idp}}
		svc := application.NewService(d)
		// client KHÔNG có IdP riêng, callback riêng của client, state mang issuer của IdP cấu hình ở máy chủ: không được dùng IdP đó
		st, n, _ := svc.IssueExternalState(domain.ExternalState{ClientID: f.client.ID, Issuer: "https://srv.example.org"})
		_, err := svc.ExternalCallback(context.Background(), application.ExternalCallbackRequest{Code: "c", State: st, CookieNonce: n, CallbackClientID: f.client.ID})
		wantErr(t, err, domain.KindBadRequest, "Identity provider not found")
		if len(f.oidc.calls) != 0 {
			t.Error("không được gọi IdP")
		}
	})
}

func TestStartClientExternalLoginRequiresAuthorizeURLAndClientID(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.extFixture(nil)
		for name, idp := range map[string]*fhir.IdentityProvider{
			"thiếu authorizeUrl": {ClientId: "c", TokenUrl: "https://i/token"},
			"thiếu clientId":     {AuthorizeUrl: "https://i/authorize", TokenUrl: "https://i/token"},
		} {
			c := put(e, &fhir.ClientApplication{ResourceType: "ClientApplication", Name: name, IdentityProvider: idp})
			_, _, err := f.svc.StartClientExternalLogin(context.Background(), c.ID, domain.ExternalState{})
			wantErr(t, err, domain.KindBadRequest, "Client does not have an identity provider")
		}
	})
}

func TestExternalCallbackStateCarriesAndForcesTheServerClientAndDomain(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.extFixture(nil)
		// app gửi clientId của client khác vào /auth/method: state vẫn do app quyết định tham số, nhưng domain luôn do server suy ra từ email
		res, nonce, err := f.svc.StartExternalLogin(context.Background(), extEmail, domain.ExternalState{Domain: "evil.example.org"})
		if err != nil {
			t.Fatal(err)
		}
		u, _ := url.Parse(res.AuthorizeURL)
		f.ver.claims = map[string]any{"email": "an@evil.example.org", "email_verified": true}
		_, err = f.callback(u.Query().Get("state"), nonce, nil)
		wantErr(t, err, domain.KindBadRequest, "Email address does not match domain")
	})
}

// ---- AddDomainConfiguration

func goodDomain() application.AddDomainRequest {
	return application.AddDomainRequest{Domain: "Corp.Example.ORG", AuthorizeURL: "https://idp.example.org/authorize", TokenURL: "https://idp.example.org/token", JWKSURL: "https://idp.example.org/jwks",
		Issuer: "https://idp.example.org", ClientID: "cid", ClientSecret: "sec", UsePKCE: true, ClientSecretPost: true, AllowedPostLoginRedirectURLs: []string{"https://app.example.org/signin"}}
}

func TestAddDomainConfiguration(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		withAud := goodDomain()
		withAud.Audience = "aud-rieng"
		cfg, err := e.svc.AddDomainConfiguration(ctx, withAud)
		if err == nil && cfg.IdentityProvider.Audience != "aud-rieng" {
			t.Errorf("phải lưu audience: %+v", cfg.IdentityProvider)
		}
		if err != nil || cfg.Domain != "corp.example.org" || cfg.IdentityProvider == nil || cfg.IdentityProvider.TokenAuthMethod != "client_secret_post" ||
			!domain.IsTrue(cfg.IdentityProvider.UsePkce) || domain.IsTrue(cfg.IdentityProvider.UseSubject) || cfg.IdentityProvider.ClientSecret != "sec" {
			t.Fatalf("%+v %v", cfg, err)
		}
		// tra cứu được theo tên miền không phân biệt hoa thường
		if got, _ := e.r.DomainConfigs.FindByDomain(ctx, "corp.example.org"); got == nil || got.ID != cfg.ID {
			t.Error("phải tìm lại được")
		}
		// mỗi tên miền một cấu hình
		_, err = e.svc.AddDomainConfiguration(ctx, goodDomain())
		wantErr(t, err, domain.KindConflict, "Domain already configured")
		sub := goodDomain()
		sub.Domain, sub.UseSubject, sub.ClientSecretPost = "other.example.org", true, false
		cfg2, err := e.svc.AddDomainConfiguration(ctx, sub)
		if err != nil || !domain.IsTrue(cfg2.IdentityProvider.UseSubject) || cfg2.IdentityProvider.TokenAuthMethod != "" {
			t.Fatalf("%+v %v", cfg2, err)
		}
	})
}

func TestAddDomainConfigurationValidation(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		bad := map[string]func(*application.AddDomainRequest){
			"domain rỗng":             func(r *application.AddDomainRequest) { r.Domain = "" },
			"domain là email":         func(r *application.AddDomainRequest) { r.Domain = "an@corp.example.org" },
			"domain có đường dẫn":     func(r *application.AddDomainRequest) { r.Domain = "corp.example.org/x" },
			"thiếu authorizeUrl":      func(r *application.AddDomainRequest) { r.AuthorizeURL = "" },
			"thiếu tokenUrl":          func(r *application.AddDomainRequest) { r.TokenURL = "" },
			"thiếu client id":         func(r *application.AddDomainRequest) { r.ClientID = "" },
			"thiếu secret":            func(r *application.AddDomainRequest) { r.ClientSecret = "" },
			"thiếu jwksUrl":           func(r *application.AddDomainRequest) { r.JWKSURL = "" },
			"có jwksUrl thiếu issuer": func(r *application.AddDomainRequest) { r.Issuer = "" },
			"tokenUrl http":           func(r *application.AddDomainRequest) { r.TokenURL = "http://idp.example.org/token" },
			"jwksUrl http":            func(r *application.AddDomainRequest) { r.JWKSURL = "http://idp.example.org/jwks" },
			"authorizeUrl http":       func(r *application.AddDomainRequest) { r.AuthorizeURL = "http://idp.example.org/a" },
			"userInfoUrl http":        func(r *application.AddDomainRequest) { r.UserInfoURL = "http://idp.example.org/u" },
			"tokenUrl loopback":       func(r *application.AddDomainRequest) { r.TokenURL = "https://127.0.0.1/token" },
			"tokenUrl metadata cloud": func(r *application.AddDomainRequest) { r.TokenURL = "https://169.254.169.254/latest" },
			"tokenUrl riêng tư":       func(r *application.AddDomainRequest) { r.TokenURL = "https://10.0.0.5/token" },
			"tokenUrl localhost":      func(r *application.AddDomainRequest) { r.TokenURL = "https://localhost/token" },
			"tokenUrl IPv6 loopback":  func(r *application.AddDomainRequest) { r.TokenURL = "https://[::1]/token" },
			"jwksUrl nội bộ":          func(r *application.AddDomainRequest) { r.JWKSURL = "https://192.168.1.1/jwks" },
			"userInfoUrl nội bộ":      func(r *application.AddDomainRequest) { r.UserInfoURL = "https://172.16.0.1/u" },
			"tokenUrl có userinfo":    func(r *application.AddDomainRequest) { r.TokenURL = "https://u:p@idp.example.org/token" },
			"tokenUrl không phải URL": func(r *application.AddDomainRequest) { r.TokenURL = "khong-phai-url" },
		}
		for name, mut := range bad {
			r := goodDomain()
			mut(&r)
			if _, err := e.svc.AddDomainConfiguration(ctx, r); domain.KindOf(err) != domain.KindBadRequest {
				t.Errorf("%s: phải bị từ chối (400), được %v", name, err)
			}
		}
		if e.count("DomainConfiguration") != 0 {
			t.Errorf("cấu hình xấu không được lưu: %d", e.count("DomainConfiguration"))
		}
		// authorizeUrl chỉ là đích chuyển hướng của TRÌNH DUYỆT (auth không gọi tới): IdP nội bộ của doanh nghiệp (IP riêng) là hợp lệ; ngược lại tokenUrl thì không
		internalIdP := goodDomain()
		internalIdP.Domain, internalIdP.AuthorizeURL = "noibo.example.org", "https://10.0.0.5/authorize"
		if _, err := e.svc.AddDomainConfiguration(ctx, internalIdP); err != nil {
			t.Errorf("authorizeUrl nội bộ cho trình duyệt: %v", err)
		}
		// chế độ không an toàn (dev/test): cho http, nội bộ và không jwksUrl
		r := goodDomain()
		r.AllowUnsafeURLs, r.TokenURL, r.JWKSURL, r.Issuer = true, "http://127.0.0.1:9999/token", "", ""
		if _, err := e.svc.AddDomainConfiguration(ctx, r); err != nil {
			t.Errorf("dev/test: %v", err)
		}
	})
}
