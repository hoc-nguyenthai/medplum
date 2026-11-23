package application

import (
	"context"
	"net/url"
	"strings"
	"time"

	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
)

// AuthorizeRequest là tham số của /oauth2/authorize (GET query hoặc POST form) cùng cookie đăng nhập của client.
type AuthorizeRequest struct {
	ClientID, RedirectURI, Scope, ResponseType, State, Nonce string
	CodeChallenge, CodeChallengeMethod, Audience, Launch     string
	Prompt, MaxAge, IDTokenHint                              string
	HasRequestObject                                         bool   // có tham số `request` (không hỗ trợ)
	Cookie                                                   string // giá trị cookie `medplum-<client_id>` (rỗng nếu không có)
	RemoteAddress, UserAgent                                 string
}

// AuthorizeKind phân loại kết quả.
type AuthorizeKind int

// Các loại kết quả của Authorize.
const (
	// AuthorizeDirectError: lỗi hiển thị thẳng (400 văn bản), vì chưa chắc redirect_uri tin được.
	AuthorizeDirectError AuthorizeKind = iota + 1
	// AuthorizeToClient: chuyển về redirect_uri của client kèm Params (code hoặc lỗi), luôn có `iss` (RFC 9207).
	AuthorizeToClient
	// AuthorizeToApp: chuyển sang trang đăng nhập của ứng dụng kèm Params.
	AuthorizeToApp
)

// AuthorizeResult là kết quả của Authorize.
type AuthorizeResult struct {
	Kind    AuthorizeKind
	Message string     // AuthorizeDirectError
	URI     string     // AuthorizeToClient: redirect_uri đã đăng ký
	Params  url.Values // tham số thêm vào URL đích
}

func (s *Service) clientRedirect(uri, state, code, desc string) *AuthorizeResult {
	p := url.Values{"iss": {s.Tokens.Issuer}}
	if state != "" {
		p.Set("state", state)
	}
	if code != "" {
		p.Set("error", code)
		p.Set("error_description", desc)
	}
	return &AuthorizeResult{Kind: AuthorizeToClient, URI: uri, Params: p}
}

// Authorize kiểm tra yêu cầu ủy quyền và quyết định bước tiếp theo (authorize.ts:60-169). Giống Medplum, auth KHÔNG tự hiện màn đăng nhập:
// chưa có phiên thì chuyển sang ứng dụng đăng nhập (Tokens.AppBaseURL) để nó gọi /auth/login, /auth/profile, /auth/scope. Chỉ khi đã có
// phiên (cookie hoặc id_token_hint) thì tạo Login mới và, với prompt=none, trả code ngay. Khác Medplum: lỗi redirect có `iss`, `max_age`
// sai là lỗi, không kế thừa PKCE từ Login cũ, phương thức challenge được kiểm giá trị, client không secret bắt buộc có PKCE, redirect_uri
// được lưu vào Login mới, và luật ipAccessRule được kiểm khi tạo Login mới.
func (s *Service) Authorize(ctx context.Context, req AuthorizeRequest) (*AuthorizeResult, error) {
	direct := func(m string) (*AuthorizeResult, error) {
		return &AuthorizeResult{Kind: AuthorizeDirectError, Message: m}, nil
	}
	client, err := s.GetClientApplication(ctx, req.ClientID)
	if err != nil {
		if domain.KindOf(err) == domain.KindNotFound || req.ClientID == "" {
			return direct("Client not found")
		}
		return nil, err
	}
	if client.Status != "" && client.Status != "active" {
		return direct("Client not found")
	}
	if req.RedirectURI == "" {
		return direct("Missing redirect URI")
	}
	if !domain.ValidRedirectURI(req.RedirectURI) || domain.MatchRedirectURI(client, req.RedirectURI) == "" {
		return direct("Invalid redirect URI")
	}
	redirectErr := func(code, desc string) (*AuthorizeResult, error) {
		return s.clientRedirect(req.RedirectURI, req.State, code, desc), nil
	}

	scope := req.Scope
	if scope == "" {
		scope = strings.Join(client.DefaultScope, " ")
	}
	switch {
	case scope == "":
		return redirectErr("invalid_request", "Missing scope")
	case req.ResponseType != "code":
		return redirectErr("unsupported_response_type", "Invalid response type")
	case req.HasRequestObject:
		return redirectErr("request_not_supported", "Unsupported request parameter")
	case !domain.ValidAudience(req.Audience, s.Tokens.BaseURL, s.Tokens.Issuer, s.Tokens.FHIRBaseURL):
		return redirectErr("invalid_request", "Invalid audience")
	case req.CodeChallenge != "" && req.CodeChallengeMethod == "":
		return redirectErr("invalid_request", "Missing code challenge method")
	case req.CodeChallenge != "" && req.CodeChallengeMethod != "S256" && !(req.CodeChallengeMethod == "plain" && s.Tokens.AllowPlainPKCE):
		return redirectErr("invalid_request", "Invalid code challenge method")
	case req.CodeChallenge == "" && client.Secret == "":
		// client không có secret chỉ có PKCE để chứng minh người đổi code là người đã đăng nhập
		return redirectErr("invalid_request", "Missing code challenge")
	}
	maxAge, ok := domain.ParseMaxAge(req.MaxAge)
	if !ok {
		return redirectErr("invalid_request", "Invalid max_age")
	}
	if req.Launch != "" {
		if _, err := s.Launches.Get(ctx, req.Launch); err != nil {
			return redirectErr("invalid_request", "Invalid launch")
		}
	}

	existing := s.existingSession(ctx, client, req, maxAge)
	switch {
	case existing == nil && req.Prompt == "none":
		return redirectErr("login_required", "Login required")
	case existing != nil && req.Prompt != "login":
		return s.authorizeFromSession(ctx, client, existing, req, scope)
	}
	return s.appRedirect(req, scope, ""), nil
}

// appRedirect chuyển sang trang đăng nhập của ứng dụng kèm các tham số OAuth (chỉ danh sách trắng, không có id_token_hint để khỏi đưa token
// vào URL). loginID (nếu có) báo trang đó tiếp tục với Login vừa tạo.
func (s *Service) appRedirect(req AuthorizeRequest, scope, loginID string) *AuthorizeResult {
	p := url.Values{}
	for k, v := range map[string]string{
		"response_type": req.ResponseType, "client_id": req.ClientID, "redirect_uri": req.RedirectURI, "scope": scope, "state": req.State,
		"nonce": req.Nonce, "code_challenge": req.CodeChallenge, "code_challenge_method": req.CodeChallengeMethod, "aud": req.Audience,
		"launch": req.Launch, "prompt": req.Prompt, "max_age": req.MaxAge,
	} {
		if v != "" {
			p.Set(k, v)
		}
	}
	if loginID != "" {
		p.Set("login", loginID)
	}
	return &AuthorizeResult{Kind: AuthorizeToApp, Params: p}
}

// existingSession tìm phiên đăng nhập sẵn có: id_token_hint trước, rồi cookie. Phiên phải đã cấp token (granted), chưa thu hồi, thuộc đúng
// client, còn trong max_age tính từ authTime, và membership/project còn hoạt động.
func (s *Service) existingSession(ctx context.Context, client *fhir.ClientApplication, req AuthorizeRequest, maxAge int64) *fhir.Login {
	var login *fhir.Login
	if req.IDTokenHint != "" {
		login = s.loginFromIDTokenHint(ctx, client, req.IDTokenHint)
	}
	if login == nil && req.Cookie != "" {
		if l, err := s.Logins.FindByCookie(ctx, domain.HashSecret(req.Cookie)); err == nil {
			login = l
		}
	}
	if login == nil || !domain.IsTrue(login.Granted) || domain.IsTrue(login.Revoked) || login.Membership == nil ||
		refOf(login.Client) != "ClientApplication/"+client.ID {
		return nil
	}
	authTime, ok := parseAuthTime(login.AuthTime)
	if !ok || s.Clock.Now().After(authTime.Add(time.Duration(maxAge)*time.Second)) {
		return nil
	}
	m, err := s.Memberships.Get(ctx, idOf(login.Membership.Reference))
	if err != nil {
		return nil
	}
	project, err := s.Projects.Get(ctx, idOf(refOf(m.Project)))
	if err != nil || domain.InactiveReason(project, m) != "" {
		return nil
	}
	return login
}

// loginFromIDTokenHint: id token do auth ký cho đúng client này (aud = client_id, token_use=id); refresh token và access token bị từ chối.
func (s *Service) loginFromIDTokenHint(ctx context.Context, client *fhir.ClientApplication, hint string) *fhir.Login {
	if s.Verifier == nil {
		return nil
	}
	claims, err := s.Verifier.Verify(hint)
	if err != nil {
		return nil
	}
	if stringClaim(claims, "iss") != s.Tokens.Issuer || stringClaim(claims, "token_use") != tokenUseID || stringClaim(claims, "aud") != client.ID {
		return nil
	}
	id := stringClaim(claims, "login_id")
	if id == "" {
		return nil
	}
	login, err := s.Logins.Get(ctx, id)
	if err != nil {
		return nil
	}
	return login
}

// authorizeFromSession tạo Login MỚI dựa trên phiên sẵn có (Login cũ không bị đụng) và: prompt=none thì trả code ngay; không thì chuyển sang
// trang của ứng dụng để người dùng xác nhận scope.
func (s *Service) authorizeFromSession(ctx context.Context, client *fhir.ClientApplication, old *fhir.Login, req AuthorizeRequest, scope string) (*AuthorizeResult, error) {
	m, err := s.Memberships.Get(ctx, idOf(old.Membership.Reference))
	if err != nil {
		return nil, err
	}
	rules, err := s.ResolveIPAccessRules(ctx, m)
	if err == nil {
		err = domain.CheckIPAccessRules(req.RemoteAddress, rules)
	}
	if err != nil {
		return s.clientRedirect(req.RedirectURI, req.State, "access_denied", "IP address not allowed"), nil
	}
	nl := &fhir.Login{
		ResourceType: "Login", BasedOn: newRef("Login", old.ID, ""), Client: old.Client, Project: old.Project, ProfileType: old.ProfileType,
		User: old.User, Membership: old.Membership, Scope: scope, AuthMethod: old.AuthMethod, AuthTime: old.AuthTime, MfaVerified: old.MfaVerified,
		PictureUrl: old.PictureUrl, Nonce: req.Nonce, CodeChallenge: req.CodeChallenge, CodeChallengeMethod: req.CodeChallengeMethod,
		RemoteAddress: req.RemoteAddress, UserAgent: req.UserAgent,
	}
	// PKCE không được kế thừa từ Login cũ (Medplum: `codeChallenge ?? old.codeChallenge`): đó là challenge của một lần ủy quyền khác.
	if old.RefreshSecret != "" {
		nl.RefreshSecret = domain.GenerateSecret(32)
	}
	switch {
	case req.Launch != "":
		nl.Launch = newRef("SmartAppLaunch", req.Launch, "")
	case old.Launch != nil:
		nl.Launch = old.Launch
	}
	domain.SetLoginRedirectURI(nl, req.RedirectURI)
	created, err := s.Logins.Create(ctx, nl)
	if err != nil {
		return nil, err
	}
	if req.Prompt != "none" {
		return s.appRedirect(req, scope, created.ID), nil
	}
	code, _, err := s.issueAuthCode(ctx, created)
	if err != nil {
		return nil, err
	}
	res := s.clientRedirect(req.RedirectURI, req.State, "", "")
	res.Params.Set("code", code)
	return res, nil
}
