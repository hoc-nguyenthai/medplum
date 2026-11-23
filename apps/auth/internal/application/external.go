package application

import (
	"context"
	"net/url"
	"strings"

	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
)

// ExternalCallbackRequest là yêu cầu callback của IdP ngoài (GET /auth/external[/{clientId}]).
type ExternalCallbackRequest struct {
	Code, State string
	// Issuer là tham số `iss` của query (RFC 9207), rỗng nếu IdP không gửi.
	Issuer string
	// CallbackClientID là {clientId} trên đường dẫn (callback riêng của client), rỗng ở callback chung.
	CallbackClientID string
	// CookieNonce là cookie liên kết trình duyệt do StartExternalLogin đặt.
	CookieNonce              string
	RemoteAddress, UserAgent string
}

// ExternalCallback xử lý callback đăng nhập ngoài (auth/external.ts:56) và trả URL chuyển hướng người dùng. Khác Medplum (đã duyệt): state phải do auth ký và gắn
// với cookie của chính trình duyệt (Q1); email IdP báo chưa xác minh bị từ chối (Q2); IdP phải có jwksUrl ngoài dev (Q3); redirect URI được kiểm TRƯỚC khi tạo
// Login hay ghi emailVerified; Login chỉ được tạo sau khi biết có membership (Q10).
func (s *Service) ExternalCallback(ctx context.Context, in ExternalCallbackRequest) (string, error) {
	if in.Code == "" {
		return "", domain.BadRequest("Missing code")
	}
	if in.State == "" {
		return "", domain.BadRequest("Missing state")
	}
	st, err := s.openExternalState(in.State, in.CookieNonce)
	if err != nil {
		return "", err
	}
	if in.CallbackClientID != "" && st.ClientID != in.CallbackClientID {
		return "", domain.BadRequest("Invalid client")
	}
	var cfg *fhir.DomainConfiguration
	if st.Domain != "" && in.CallbackClientID == "" {
		if cfg, err = s.DomainConfigs.FindByDomain(ctx, lower(st.Domain)); err != nil {
			return "", err
		}
	}
	var client *fhir.ClientApplication
	if st.ClientID != "" {
		if client, err = s.GetClientApplication(ctx, st.ClientID); err != nil {
			return "", err
		}
	}
	idp, err := s.selectIdentityProvider(ctx, in, st, client, cfg)
	if err != nil {
		return "", err
	}
	if idp == nil {
		return "", domain.BadRequest("Identity provider not found")
	}
	if in.Issuer != "" && idp.Issuer != "" && in.Issuer != idp.Issuer {
		return "", domain.BadRequest("Invalid issuer")
	}
	claims, err := s.verifyExternalCode(ctx, in, st, idp)
	if err != nil {
		return "", err
	}
	email, externalID, err := domain.ResolveExternalIdentity(idp, claims)
	if err != nil {
		return "", err
	}
	if domain.IsExternalEmailUnverified(claims) {
		return "", domain.BadRequest("Email address is not verified by the identity provider") // Q2: Medplum chỉ ghi log rồi cho đăng nhập
	}
	if st.Domain != "" && !strings.HasSuffix(email, "@"+lower(st.Domain)) {
		return "", domain.BadRequest("Email address does not match domain")
	}
	projectID := st.ProjectID
	if client != nil {
		clientProject := ""
		if client.Meta != nil {
			clientProject = client.Meta.Project
		}
		if st.ProjectID != "" && st.ProjectID != clientProject {
			return "", domain.BadRequest("Invalid project")
		}
		projectID = clientProject
	}
	redirect := ""
	if client != nil && st.RedirectURI != "" {
		if redirect, err = s.matchExternalRedirect(ctx, client, st.RedirectURI); err != nil {
			return "", err
		}
	}
	scope, nonce := st.Scope, st.Nonce
	if scope == "" {
		scope = "openid offline_access"
	}
	if nonce == "" {
		nonce = domain.NewID()
	}
	login, err := s.TryLogin(ctx, domain.LoginRequest{
		AuthMethod: domain.AuthExternal, Email: email, ExternalID: externalID, ProjectID: projectID, ClientID: st.ClientID, Scope: scope, Nonce: nonce,
		LaunchID: st.Launch, CodeChallenge: st.CodeChallenge, CodeChallengeMethod: st.CodeChallengeMethod, RemoteAddress: in.RemoteAddress, UserAgent: in.UserAgent,
	})
	if err != nil {
		return "", err
	}
	if in.CallbackClientID == "" && client != nil && client.IdentityProvider != nil {
		s.Logger.WarnContext(ctx, "client có identityProvider đăng nhập qua callback chung (legacyExternalAuth)", "client", client.ID)
	}
	// IdP đã xác thực danh tính nên email được coi là đã xác minh (idempotent: không tạo phiên bản mới nếu đã đúng)
	if _, err := s.Users.Mutate(ctx, idOf(refOf(login.User)), func(u *fhir.User) error {
		if domain.IsTrue(u.EmailVerified) {
			return ErrNoChange
		}
		u.EmailVerified = ptrTrue()
		return nil
	}); err != nil {
		return "", err
	}
	if login.Membership != nil && redirect != "" {
		res, err := s.BuildLoginResult(ctx, login)
		if err != nil {
			return "", err
		}
		return redirectWith(st.RedirectURI, url.Values{"login": {login.ID}, "code": {res.Code}})
	}
	return s.signInRedirect(login, st, cfg)
}

// selectIdentityProvider chọn IdP theo thứ tự của Medplum (auth/external.ts:224): callback riêng của client, client (chỉ khi cho phép legacy), domain, issuer.
func (s *Service) selectIdentityProvider(ctx context.Context, in ExternalCallbackRequest, st domain.ExternalState, client *fhir.ClientApplication, cfg *fhir.DomainConfiguration) (*fhir.IdentityProvider, error) {
	if in.CallbackClientID != "" {
		if client == nil {
			return nil, nil
		}
		return client.IdentityProvider, nil // callback riêng của client: không thử nguồn khác
	}
	if client != nil && client.IdentityProvider != nil {
		allowed := true
		if client.Meta != nil && client.Meta.Project != "" {
			project, err := s.Projects.Get(ctx, client.Meta.Project)
			if err != nil {
				return nil, err
			}
			// chỉ systemSetting (super admin sửa được) mới bật callback chung cho client có IdP riêng; Project.setting do admin project đặt KHÔNG có tác dụng
			allowed = systemSettingTrue(project, "legacyExternalAuth")
		}
		if !allowed {
			return nil, domain.BadRequest("Client identity provider must use the per-client callback " + s.externalCallbackURL(client.ID))
		}
		return client.IdentityProvider, nil
	}
	if cfg != nil && cfg.IdentityProvider != nil {
		return cfg.IdentityProvider, nil
	}
	if st.Issuer != "" {
		for i := range s.External.Providers {
			if p := s.External.Providers[i]; p.Issuer == st.Issuer && p.IdentityProvider != nil {
				return p.IdentityProvider, nil
			}
		}
	}
	return nil, nil
}

func systemSettingTrue(p *fhir.Project, name string) bool {
	for _, x := range p.SystemSetting {
		if x.Name == name {
			return domain.IsTrue(x.ValueBoolean)
		}
	}
	return false
}

// verifyExternalCode đổi code lấy id_token rồi xác minh (auth/external.ts:324). Có jwksUrl: kiểm chữ ký, iss, aud, hạn. Không có: Q3 chỉ cho dev/test, vì
// nếu không id_token chỉ được tin nhờ kênh TLS tới tokenUrl (không kiểm chữ ký, iss, aud, hạn).
func (s *Service) verifyExternalCode(ctx context.Context, in ExternalCallbackRequest, st domain.ExternalState, idp *fhir.IdentityProvider) (map[string]any, error) {
	if s.OIDC == nil || s.IDVerifier == nil {
		return nil, domain.Internal("đăng nhập ngoài chưa được lắp ghép")
	}
	if idp.JwksUrl == "" && !s.External.AllowUnsafeOutbound {
		return nil, domain.BadRequest("Missing JWKS URL for external identity provider")
	}
	ex := ExchangeCodeInput{RedirectURI: s.externalCallbackURL(in.CallbackClientID), Code: in.Code}
	if domain.IsTrue(idp.UsePkce) && st.CodeChallenge != "" {
		ex.CodeVerifier = st.CodeChallenge
	}
	idToken, err := s.OIDC.ExchangeCode(ctx, idp, ex)
	if err != nil {
		return nil, err
	}
	if idp.JwksUrl == "" {
		claims, err := s.IDVerifier.ParseUnsigned(idToken)
		if err != nil {
			return nil, domain.BadRequest("Failed to verify code - check your identity provider configuration")
		}
		return claims, nil
	}
	if idp.Issuer == "" {
		return nil, domain.BadRequest("Missing issuer for external identity provider")
	}
	aud := idp.Audience
	if aud == "" {
		aud = idp.ClientId
	}
	claims, err := s.IDVerifier.VerifyJWKS(ctx, idToken, idp.JwksUrl, idp.Issuer, aud)
	if err != nil {
		s.Logger.WarnContext(ctx, "id_token của IdP ngoài không hợp lệ", "issuer", idp.Issuer, "error", err)
		return nil, domain.BadRequest("Failed to verify code - check your identity provider configuration")
	}
	return claims, nil
}

// matchExternalRedirect kiểm redirect URI của state với client: khớp chính xác; chỉ khi project bật setting allow-dangerous-redirect mới cho khớp một phần.
func (s *Service) matchExternalRedirect(ctx context.Context, client *fhir.ClientApplication, requested string) (string, error) {
	if m := domain.MatchRedirectURI(client, requested); m != "" {
		return m, nil
	}
	if client.Meta != nil && client.Meta.Project != "" {
		if project, err := s.Projects.Get(ctx, client.Meta.Project); err == nil {
			if st := domain.ProjectSetting(project, "allow-dangerous-redirect"); st != nil && domain.IsTrue(st.ValueBoolean) {
				if m := domain.MatchRedirectURIPartial(client, requested); m != "" {
					s.Logger.WarnContext(ctx, "redirect URI khớp một phần (allow-dangerous-redirect)", "client", client.ID)
					return m, nil
				}
			}
		}
	}
	return "", domain.BadRequest("Invalid redirect URI")
}

func redirectWith(base string, q url.Values) (string, error) {
	u, err := url.Parse(base)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", domain.BadRequest("Invalid redirect URI")
	}
	existing := u.Query()
	for k, v := range q {
		existing[k] = v
	}
	u.RawQuery = existing.Encode()
	return u.String(), nil
}

// signInRedirect chuyển người dùng về trang đăng nhập của app (hoặc returnTo hợp lệ của domain) kèm login và tham số PKCE để app hoàn tất (chọn profile/lấy code).
func (s *Service) signInRedirect(login *fhir.Login, st domain.ExternalState, cfg *fhir.DomainConfiguration) (string, error) {
	page := ""
	if cfg != nil && st.ReturnTo != "" && domain.ValidReturnTo(st.ReturnTo, cfg.AllowedPostLoginRedirectUrls) {
		page = st.ReturnTo
	}
	if page == "" {
		base := trimBase(s.Tokens.AppBaseURL)
		if base == "" {
			return "", domain.Internal("AUTH_APP_BASE_URL chưa đặt")
		}
		page = base + "/signin"
		if login.Launch != nil {
			page = base + "/oauth"
		}
	}
	q := url.Values{"login": {login.ID}}
	for k, v := range map[string]string{"scope": login.Scope, "nonce": login.Nonce, "code_challenge": login.CodeChallenge, "code_challenge_method": login.CodeChallengeMethod} {
		if v != "" {
			q.Set(k, v)
		}
	}
	return redirectWith(page, q)
}
