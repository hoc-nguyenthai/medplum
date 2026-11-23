package application

import (
	"context"
	"errors"

	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
)

// Hằng số của token-exchange (RFC 8693).
const (
	GrantTypeTokenExchange = "urn:ietf:params:oauth:grant-type:token-exchange"
	TokenTypeAccessToken   = "urn:ietf:params:oauth:token-type:access_token"
)

// ExchangeTokenRequest là yêu cầu đổi token của IdP ngoài lấy token của auth (auth/exchange.ts, oauth/token.ts:532).
type ExchangeTokenRequest struct {
	// ClientID là client id của IdP ở cấu hình máy chủ, hoặc id ClientApplication có identityProvider riêng.
	ClientID         string
	SubjectToken     string
	SubjectTokenType string
	MembershipID     string
	Scope, Nonce     string
	RemoteAddress    string
	UserAgent        string
}

// ExchangeLoginError bọc lỗi của TryLogin: /auth/exchange trả chúng dạng OperationOutcome (như Medplum), grant token-exchange trả dạng OAuth.
type ExchangeLoginError struct{ Err error }

func (e *ExchangeLoginError) Error() string { return e.Err.Error() }
func (e *ExchangeLoginError) Unwrap() error { return e.Err }

// ExchangeToken đổi token của IdP ngoài lấy token của auth. Lỗi trả ra là *domain.OAuthError (yêu cầu, IdP, danh tính) hoặc *ExchangeLoginError (TryLogin).
// Khác Medplum (đã duyệt): IdP cấu hình ở máy chủ BẮT BUỘC có membership_id (Q5; Medplum chọn membership đầu tiên của user trong MỌI project); userinfo dạng JWT
// phải có chữ ký xác minh được (Q7); token là JWT thì aud/azp phải thuộc ứng dụng này (Q11); email IdP báo chưa xác minh bị từ chối (Q2).
func (s *Service) ExchangeToken(ctx context.Context, req ExchangeTokenRequest) (TokenResponse, error) {
	if req.ClientID == "" {
		return nil, domain.ErrInvalidRequest("Invalid client")
	}
	if req.SubjectToken == "" {
		return nil, domain.ErrInvalidRequest("Invalid subject_token")
	}
	if req.SubjectTokenType != TokenTypeAccessToken {
		return nil, domain.ErrInvalidRequest("Invalid subject_token_type")
	}
	if s.OIDC == nil {
		return nil, domain.ErrServer()
	}
	var (
		idp    *fhir.IdentityProvider
		client *fhir.ClientApplication
		server bool
	)
	for _, p := range s.External.Providers {
		cid := p.ClientID
		if cid == "" && p.IdentityProvider != nil {
			cid = p.IdentityProvider.ClientId
		}
		if cid != "" && cid == req.ClientID {
			idp, server = p.IdentityProvider, true
			if idp == nil && p.UserInfoURL != "" {
				idp = &fhir.IdentityProvider{UserInfoUrl: p.UserInfoURL}
			}
			break
		}
	}
	if idp == nil {
		c, err := s.GetClientApplication(ctx, req.ClientID)
		if err != nil || c.IdentityProvider == nil || (c.Status != "" && c.Status != "active") {
			if err != nil && domain.KindOf(err) != domain.KindNotFound {
				return nil, domain.ErrServer()
			}
			return nil, domain.ErrInvalidRequest("Invalid client")
		}
		client, idp = c, c.IdentityProvider
	}
	projectID := ""
	if server {
		if req.MembershipID == "" {
			return nil, domain.ErrInvalidRequest("Missing membership_id") // Q5
		}
		m, err := s.Memberships.Get(ctx, req.MembershipID)
		if err != nil || m.Project == nil {
			return nil, domain.ErrInvalidRequest("Invalid membership")
		}
		projectID = idOf(refOf(m.Project))
	} else if client.Meta != nil {
		projectID = client.Meta.Project
	}
	if idp.UserInfoUrl == "" {
		return nil, domain.ErrInvalidRequest("Missing user info URL")
	}
	if !domain.SubjectTokenAudienceOK(req.SubjectToken, idp) {
		return nil, domain.ErrInvalidRequest("Invalid subject_token")
	}
	claims, err := s.OIDC.UserInfo(ctx, idp, req.SubjectToken)
	if err != nil {
		return nil, exchangeIdPError(err)
	}
	email, externalID, err := domain.ResolveExternalIdentity(idp, claims)
	if err != nil {
		return nil, domain.ErrInvalidRequest(err.Error())
	}
	if domain.IsExternalEmailUnverified(claims) {
		return nil, domain.ErrInvalidRequest("Email address is not verified by the identity provider")
	}
	scope, nonce := req.Scope, req.Nonce
	if scope == "" {
		scope = "openid offline_access"
	}
	if nonce == "" {
		nonce = domain.NewID()
	}
	clientID := ""
	if client != nil {
		clientID = client.ID
	}
	login, err := s.TryLogin(ctx, domain.LoginRequest{
		AuthMethod: domain.AuthExchange, Email: email, ExternalID: externalID, ProjectID: projectID, ClientID: clientID, Scope: scope, Nonce: nonce,
		MembershipID: req.MembershipID, ForceUseFirstMembership: true, RemoteAddress: req.RemoteAddress, UserAgent: req.UserAgent,
	})
	if err != nil {
		return nil, &ExchangeLoginError{Err: err}
	}
	if login.Membership == nil {
		return nil, &ExchangeLoginError{Err: domain.BadRequest("User not found")}
	}
	if _, err := s.Users.Mutate(ctx, idOf(refOf(login.User)), func(u *fhir.User) error {
		if domain.IsTrue(u.EmailVerified) {
			return ErrNoChange
		}
		u.EmailVerified = ptrTrue()
		return nil
	}); err != nil {
		return nil, domain.ErrServer()
	}
	return s.issueForGrantedLogin(ctx, login, client, req.RemoteAddress)
}

// exchangeIdPError đổi lỗi gọi IdP sang lỗi OAuth: 429 của IdP giữ nguyên, còn lại là invalid_request kèm thông báo cố định.
func exchangeIdPError(err error) *domain.OAuthError {
	var de *domain.Error
	if errors.As(err, &de) {
		if de.Kind == domain.KindTooManyRequests {
			return &domain.OAuthError{Code: "invalid_request", Description: de.Message, Status: 429}
		}
		return domain.ErrInvalidRequest(de.Message)
	}
	return domain.ErrServer()
}

// issueForGrantedLogin cấp token cho Login đã có membership mà không qua code: chiếm Login bằng ghi có điều kiện rồi phát token (như PreAuthorizedCode).
func (s *Service) issueForGrantedLogin(ctx context.Context, login *fhir.Login, client *fhir.ClientApplication, ip string) (TokenResponse, error) {
	m, err := s.Memberships.Get(ctx, idOf(login.Membership.Reference))
	if err != nil {
		return nil, domain.ErrInvalidRequest("Invalid profile")
	}
	project, err := s.Projects.Get(ctx, idOf(refOf(m.Project)))
	if err != nil {
		return nil, domain.ErrInvalidRequest("Invalid profile")
	}
	if reason := domain.InactiveReason(project, m); reason != "" {
		return nil, domain.ErrAccessDenied(reason)
	}
	var launch *fhir.SmartAppLaunch
	if login.Launch != nil {
		if launch, err = s.Launches.Get(ctx, idOf(login.Launch.Reference)); err != nil {
			return nil, domain.ErrInvalidRequest("Invalid launch")
		}
	}
	scopeClaim, policy, oerr := s.authorization(ctx, login.Scope, m)
	if oerr != nil {
		return nil, oerr
	}
	if login, oerr = s.claimCode(ctx, login, ip); oerr != nil {
		return nil, oerr
	}
	resp, oerr := s.IssueTokens(ctx, login, client, m, project, launch, scopeClaim, policy)
	if oerr != nil {
		return nil, oerr
	}
	return resp, nil
}
