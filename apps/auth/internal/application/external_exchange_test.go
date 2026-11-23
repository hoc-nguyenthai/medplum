package application_test

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"trivita-fhir/apps/auth/internal/application"
	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
)

func fakeJWT(payload string) string {
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"RS256"}`)) + "." + enc([]byte(payload)) + ".sig"
}

// exchangeFix: một client có identityProvider riêng (userinfo) và một IdP cấu hình ở máy chủ, cùng trỏ vào user/membership của extFixture.
type fakeSigner struct{}

func (fakeSigner) Sign(map[string]any) (string, error) { return "signed.jwt.token", nil }

type exchangeFix struct {
	*extFix
	idpClient *fhir.ClientApplication
	serverID  string
}

func (e *env) exchangeFixture(mut func(*extFix)) *exchangeFix {
	f := e.extFixture(mut)
	idp := &fhir.IdentityProvider{ClientId: "ext-cid", UserInfoUrl: "https://idp.example.org/userinfo"}
	cl := put(e, &fhir.ClientApplication{ResourceType: "ClientApplication", Name: "Own IdP", Meta: &fhir.Meta{Project: f.project.ID}, IdentityProvider: idp})
	f.oidc.info = map[string]any{"email": extEmail, "sub": "sub-1", "email_verified": true}
	d := f.svc.Deps
	d.MFA, d.Signer = nil, fakeSigner{}
	d.External.Providers = []application.ExternalProvider{{Issuer: "https://srv.example.org", ClientID: "srv-cid",
		IdentityProvider: &fhir.IdentityProvider{ClientId: "srv-cid", UserInfoUrl: "https://srv.example.org/userinfo"}}}
	f.svc = application.NewService(d)
	return &exchangeFix{extFix: f, idpClient: cl, serverID: "srv-cid"}
}

func (f *exchangeFix) req(mut func(*application.ExchangeTokenRequest)) application.ExchangeTokenRequest {
	r := application.ExchangeTokenRequest{ClientID: f.idpClient.ID, SubjectToken: "opaque-token", SubjectTokenType: application.TokenTypeAccessToken, RemoteAddress: "1.2.3.4", UserAgent: "ua"}
	if mut != nil {
		mut(&r)
	}
	return r
}

func wantOAuth(t *testing.T, err error, code, desc string) *domain.OAuthError {
	t.Helper()
	var oe *domain.OAuthError
	if !errors.As(err, &oe) || oe.Code != code || (desc != "" && oe.Description != desc) {
		t.Fatalf("mong OAuthError %s %q, được %#v (%v)", code, desc, oe, err)
	}
	return oe
}

func TestExchangeTokenClientIdPHappyPath(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.exchangeFixture(nil)
		resp, err := f.svc.ExchangeToken(context.Background(), f.req(nil))
		if err != nil {
			t.Fatal(err)
		}
		if resp["access_token"] == "" || resp["access_token"] == nil || resp["token_type"] != "Bearer" {
			t.Fatalf("thiếu token: %v", resp)
		}
		if len(f.oidc.infoCalls) != 1 || f.oidc.infoCalls[0] != "opaque-token" {
			t.Fatalf("userinfo calls: %v", f.oidc.infoCalls)
		}
		if u, _ := f.svc.Users.Get(context.Background(), f.user.ID); !domain.IsTrue(u.EmailVerified) {
			t.Error("emailVerified phải được nâng")
		}
	})
}

func TestExchangeTokenValidationOrder(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.exchangeFixture(nil)
		ctx := context.Background()
		cases := []struct {
			name string
			mut  func(*application.ExchangeTokenRequest)
			desc string
		}{
			{"thiếu client", func(r *application.ExchangeTokenRequest) { r.ClientID = "" }, "Invalid client"},
			{"thiếu subject_token", func(r *application.ExchangeTokenRequest) { r.SubjectToken = "" }, "Invalid subject_token"},
			{"sai loại token", func(r *application.ExchangeTokenRequest) { r.SubjectTokenType = "urn:x" }, "Invalid subject_token_type"},
			{"client lạ", func(r *application.ExchangeTokenRequest) { r.ClientID = "khong-co" }, "Invalid client"},
			{"client không có idp", func(r *application.ExchangeTokenRequest) { r.ClientID = f.client.ID }, "Invalid client"},
		}
		for _, c := range cases {
			_, err := f.svc.ExchangeToken(ctx, f.req(c.mut))
			wantOAuth(t, err, "invalid_request", c.desc)
		}
		if len(f.oidc.infoCalls) != 0 {
			t.Errorf("không được gọi IdP khi yêu cầu sai: %v", f.oidc.infoCalls)
		}
	})
}

func TestExchangeTokenServerIdPRequiresMembership(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.exchangeFixture(nil)
		ctx := context.Background()
		_, err := f.svc.ExchangeToken(ctx, f.req(func(r *application.ExchangeTokenRequest) { r.ClientID = f.serverID }))
		wantOAuth(t, err, "invalid_request", "Missing membership_id")
		_, err = f.svc.ExchangeToken(ctx, f.req(func(r *application.ExchangeTokenRequest) { r.ClientID, r.MembershipID = f.serverID, "khong-co" }))
		wantOAuth(t, err, "invalid_request", "Invalid membership")
		if len(f.oidc.infoCalls) != 0 {
			t.Fatalf("không gọi IdP trước khi có membership: %v", f.oidc.infoCalls)
		}
		resp, err := f.svc.ExchangeToken(ctx, f.req(func(r *application.ExchangeTokenRequest) { r.ClientID, r.MembershipID = f.serverID, f.member.ID }))
		if err != nil || resp["access_token"] == nil {
			t.Fatalf("%v %v", resp, err)
		}
		// membership của NGƯỜI KHÁC: lỗi đăng nhập, không cấp token
		other := e.user("khac@corp.example.org", nil)
		om := e.membership(other, f.project, nil)
		_, err = f.svc.ExchangeToken(ctx, f.req(func(r *application.ExchangeTokenRequest) { r.ClientID, r.MembershipID = f.serverID, om.ID }))
		var le *application.ExchangeLoginError
		if !errors.As(err, &le) {
			t.Fatalf("mong ExchangeLoginError, được %v", err)
		}
	})
}

func TestExchangeTokenSubjectAudience(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.exchangeFixture(nil)
		ctx := context.Background()
		bad := fakeJWT(`{"aud":"app-khac"}`)
		_, err := f.svc.ExchangeToken(ctx, f.req(func(r *application.ExchangeTokenRequest) { r.SubjectToken = bad }))
		wantOAuth(t, err, "invalid_request", "Invalid subject_token")
		if len(f.oidc.infoCalls) != 0 {
			t.Fatal("token sai aud không được gửi tới IdP")
		}
		good := fakeJWT(`{"aud":["x","ext-cid"]}`)
		if _, err := f.svc.ExchangeToken(ctx, f.req(func(r *application.ExchangeTokenRequest) { r.SubjectToken = good })); err != nil {
			t.Fatalf("aud đúng: %v", err)
		}
	})
}

func TestExchangeTokenIdPFailures(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.exchangeFixture(nil)
		ctx := context.Background()
		f.oidc.infoErr = domain.TooManyRequests(0)
		_, err := f.svc.ExchangeToken(ctx, f.req(nil))
		if oe := wantOAuth(t, err, "invalid_request", ""); oe.Status != 429 {
			t.Errorf("429 phải giữ nguyên: %d", oe.Status)
		}
		f.oidc.infoErr = domain.BadRequest("Failed to verify code - check your identity provider configuration")
		_, err = f.svc.ExchangeToken(ctx, f.req(nil))
		wantOAuth(t, err, "invalid_request", "Failed to verify code - check your identity provider configuration")
		f.oidc.infoErr = errors.New("chi tiết nội bộ nhạy cảm")
		_, err = f.svc.ExchangeToken(ctx, f.req(nil))
		if oe := wantOAuth(t, err, "server_error", ""); strings.Contains(oe.Description, "nhạy cảm") {
			t.Errorf("lộ lỗi nội bộ: %v", oe)
		}
		f.oidc.infoErr = nil
		f.oidc.info = map[string]any{"email": extEmail, "email_verified": false}
		_, err = f.svc.ExchangeToken(ctx, f.req(nil))
		wantOAuth(t, err, "invalid_request", "Email address is not verified by the identity provider")
		f.oidc.info = map[string]any{"sub": "x"}
		if _, err = f.svc.ExchangeToken(ctx, f.req(nil)); err == nil {
			t.Error("thiếu email phải lỗi")
		}
		if n := len(e.logins()); n != 0 {
			t.Errorf("các lần lỗi không được để lại Login: %d", n)
		}
	})
}

func TestExchangeTokenInactiveProjectDenied(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.exchangeFixture(nil)
		closed := *f.project
		closed.Active = ptr(false)
		if _, err := e.repo.UpdateResource(context.Background(), &closed, fhirUpdate()); err != nil {
			t.Fatal(err)
		}
		_, err := f.svc.ExchangeToken(context.Background(), f.req(nil))
		if err == nil {
			t.Fatal("project đóng phải bị từ chối")
		}
	})
}

// ---- Google

type fakeGoogle struct {
	claims map[string]any
	err    error
	calls  []string
}

func (f *fakeGoogle) Verify(_ context.Context, cred, aud string) (map[string]any, error) {
	f.calls = append(f.calls, cred+"|"+aud)
	return f.claims, f.err
}

const googleEmail = "gg@example.com"

func (e *env) googleFixture(site string) (*application.Service, *fakeGoogle, *fhir.Project) {
	g := &fakeGoogle{claims: map[string]any{"email": googleEmail, "email_verified": true, "given_name": "Gia", "family_name": "Go"}}
	p := e.project(func(p *fhir.Project) {
		if site != "" {
			p.Site = []fhir.ProjectSite{{Name: "s", GoogleClientId: site}}
		}
	})
	d := e.svc.Deps
	d.MFA = nil
	d.Google, d.GoogleClientID = g, "primary-gid"
	d.External = application.ExternalConfig{StateKey: []byte(strings.Repeat("k", 32))}
	return application.NewService(d), g, p
}

func TestGoogleLoginExistingUser(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		svc, g, p := e.googleFixture("site-gid")
		u := e.user(googleEmail, nil)
		e.membership(u, p, nil)
		res, err := svc.GoogleLogin(context.Background(), application.GoogleLoginRequest{GoogleClientID: "site-gid", Credential: "cred", ProjectID: p.ID})
		if err != nil || res.Login == nil {
			t.Fatalf("%+v %v", res, err)
		}
		if g.calls[0] != "cred|site-gid" {
			t.Errorf("aud gửi cho verifier: %v", g.calls)
		}
		if got, _ := svc.Users.Get(context.Background(), u.ID); !domain.IsTrue(got.EmailVerified) {
			t.Error("emailVerified phải được nâng")
		}
		// client chính không cần Project.site
		res, err = svc.GoogleLogin(context.Background(), application.GoogleLoginRequest{GoogleClientID: "primary-gid", Credential: "cred", ProjectID: p.ID})
		if err != nil || res.Login == nil {
			t.Fatalf("client chính: %+v %v", res, err)
		}
		// projectId rác bị bỏ qua (như Medplum), không lỗi "Invalid projectId"
		if _, err = svc.GoogleLogin(context.Background(), application.GoogleLoginRequest{GoogleClientID: "primary-gid", Credential: "cred", ProjectID: "rac"}); err != nil {
			t.Fatalf("projectId rác: %v", err)
		}
	})
}

func TestGoogleLoginRejections(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		svc, g, p := e.googleFixture("site-gid")
		u := e.user(googleEmail, nil)
		e.membership(u, p, nil)
		ctx := context.Background()
		req := application.GoogleLoginRequest{GoogleClientID: "site-gid", Credential: "cred"}

		_, err := svc.GoogleLogin(ctx, application.GoogleLoginRequest{GoogleClientID: "la", Credential: "cred"})
		wantErr(t, err, domain.KindBadRequest, "Invalid googleClientId")
		if len(g.calls) != 0 {
			t.Fatal("googleClientId lạ không được tới verifier")
		}
		g.err = errors.New("jwt: chi tiết nhạy cảm")
		_, err = svc.GoogleLogin(ctx, req)
		wantErr(t, err, domain.KindBadRequest, "Failed to verify Google credential")
		if strings.Contains(err.Error(), "nhạy cảm") {
			t.Error("lộ lỗi thư viện")
		}
		g.err = nil
		g.claims = map[string]any{"email_verified": true}
		_, err = svc.GoogleLogin(ctx, req)
		wantErr(t, err, domain.KindBadRequest, "Google credential does not contain email address")
		for _, v := range []any{false, nil, "true"} {
			g.claims = map[string]any{"email": googleEmail, "email_verified": v}
			_, err = svc.GoogleLogin(ctx, req)
			wantErr(t, err, domain.KindBadRequest, "Email address is not verified by Google")
		}
		if n := len(e.logins()); n != 0 {
			t.Errorf("lỗi không để lại Login: %d", n)
		}
		svc.Google = nil
		if _, err = svc.GoogleLogin(ctx, req); err == nil {
			t.Error("chưa lắp verifier phải lỗi")
		}
	})
}

func TestGoogleLoginUserCreation(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		svc, _, p := e.googleFixture("site-gid")
		ctx := context.Background()
		_, err := svc.GoogleLogin(ctx, application.GoogleLoginRequest{GoogleClientID: "primary-gid", Credential: "c", ProjectID: p.ID})
		wantErr(t, err, domain.KindBadRequest, "User not found")
		res, err := svc.GoogleLogin(ctx, application.GoogleLoginRequest{GoogleClientID: "primary-gid", Credential: "c", ProjectID: p.ID, CreateUser: true})
		if err != nil || res.Login == nil {
			t.Fatalf("%+v %v", res, err)
		}
		u, _ := svc.FindUserByEmail(ctx, googleEmail, p.ID)
		if u == nil || !domain.IsTrue(u.EmailVerified) || u.FirstName != "Gia" || u.PasswordHash != "" {
			t.Fatalf("user tạo ra: %+v", u)
		}
		// lần hai không tạo trùng
		if _, err = svc.GoogleLogin(ctx, application.GoogleLoginRequest{GoogleClientID: "primary-gid", Credential: "c", ProjectID: p.ID, CreateUser: true}); err != nil && domain.KindOf(err) == domain.KindInternal {
			t.Fatal(err)
		}
		// đăng ký tắt chặn tạo user không thuộc project cụ thể
		d := svc.Deps
		d.Registration.Disabled = true
		off := application.NewService(d)
		g2 := &fakeGoogle{claims: map[string]any{"email": "moi@example.com", "email_verified": true}}
		d.Google = g2
		off = application.NewService(d)
		_, err = off.GoogleLogin(ctx, application.GoogleLoginRequest{GoogleClientID: "primary-gid", Credential: "c", CreateUser: true})
		wantErr(t, err, domain.KindBadRequest, "Registration is disabled")
	})
}

func TestGoogleLoginRedirectsToDomainIdP(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.extFixture(nil)
		g := &fakeGoogle{claims: map[string]any{"email": extEmail, "email_verified": true}}
		d := f.svc.Deps
		d.Google, d.GoogleClientID = g, "primary-gid"
		svc := application.NewService(d)
		res, err := svc.GoogleLogin(context.Background(), application.GoogleLoginRequest{GoogleClientID: "primary-gid", Credential: "c", ClientID: f.client.ID})
		if err != nil || res.External == nil || res.CookieNonce == "" || res.Login != nil || !strings.HasPrefix(res.External.AuthorizeURL, "https://idp.example.org/authorize") {
			t.Fatalf("%+v %v", res, err)
		}
		if n := len(e.logins()); n != 0 {
			t.Errorf("chưa được tạo Login: %d", n)
		}
	})
}

func TestExchangeTokenMembershipSelection(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.exchangeFixture(nil)
		ctx := context.Background()
		// user có HAI membership trong cùng project, không chỉ định membership_id (IdP riêng của client): lấy cái đầu tiên, không lỗi
		e.membership(f.user, f.project, func(m *fhir.ProjectMembership) { m.Profile = &fhir.Reference{Reference: "Practitioner/p2"} })
		if _, err := f.svc.ExchangeToken(ctx, f.req(nil)); err != nil {
			t.Fatalf("nhiều membership: %v", err)
		}
		// user không có membership nào: lỗi đăng nhập, không cấp token
		e2 := e.exchangeFixture(nil)
		lone := e.user("lone@corp.example.org", nil)
		e2.oidc.info = map[string]any{"email": lone.Email, "sub": "s2", "email_verified": true}
		_, err := e2.svc.ExchangeToken(ctx, e2.req(nil))
		var le *application.ExchangeLoginError
		if !errors.As(err, &le) {
			t.Fatalf("mong ExchangeLoginError: %v", err)
		}
	})
}

func TestGoogleClientIDIsScopedToTheRequestedProject(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		svc, _, p := e.googleFixture("site-gid")
		other := e.project(nil)
		u := e.user(googleEmail, nil)
		e.membership(u, p, nil)
		_, err := svc.GoogleLogin(context.Background(), application.GoogleLoginRequest{GoogleClientID: "site-gid", Credential: "c", ProjectID: other.ID})
		wantErr(t, err, domain.KindBadRequest, "Invalid googleClientId")
	})
}

func TestGoogleLoginProjectNewAllowsNoMembership(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		svc, _, _ := e.googleFixture("")
		e.user(googleEmail, nil) // user không thuộc project nào, không có membership
		ctx := context.Background()
		res, err := svc.GoogleLogin(ctx, application.GoogleLoginRequest{GoogleClientID: "primary-gid", Credential: "c", ProjectID: "new"})
		if err != nil || res.Login == nil || res.Login.Membership != nil {
			t.Fatalf("projectId=new: %+v %v", res, err)
		}
		if _, err = svc.GoogleLogin(ctx, application.GoogleLoginRequest{GoogleClientID: "primary-gid", Credential: "c"}); err == nil {
			t.Fatal("không projectId và không membership phải lỗi")
		}
	})
}
