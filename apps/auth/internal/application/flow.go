package application

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
)

// Login xử lý POST /auth/login: TryLogin rồi dựng phản hồi. Trả cả login để tầng HTTP đặt cookie khi có code.
func (s *Service) Login(ctx context.Context, req domain.LoginRequest) (*LoginResult, *fhir.Login, error) {
	login, err := s.TryLogin(ctx, req)
	if err != nil {
		return nil, nil, err
	}
	res, err := s.BuildLoginResult(ctx, login)
	if err != nil {
		return nil, nil, err
	}
	s.Logger.InfoContext(ctx, "login success", "project", req.ProjectID, "client", req.ClientID)
	return res, login, nil
}

// SelectProfile xử lý POST /auth/profile: chọn membership (mọi lỗi đọc membership thành "Profile not found").
func (s *Service) SelectProfile(ctx context.Context, loginID, membershipID string) (*LoginResult, *fhir.Login, error) {
	login, err := s.Logins.Get(ctx, loginID)
	if err != nil {
		return nil, nil, err
	}
	m, err := s.Memberships.Get(ctx, membershipID)
	if err != nil {
		return nil, nil, domain.BadRequest("Profile not found")
	}
	updated, err := s.SetLoginMembership(ctx, login, m)
	if err != nil {
		return nil, nil, err
	}
	res, err := s.BuildLoginResult(ctx, updated)
	return res, updated, err
}

// SelectScope xử lý POST /auth/scope.
func (s *Service) SelectScope(ctx context.Context, loginID, scope string) (*LoginResult, *fhir.Login, error) {
	login, err := s.Logins.Get(ctx, loginID)
	if err != nil {
		return nil, nil, err
	}
	updated, err := s.SetLoginScope(ctx, login, scope)
	if err != nil {
		return nil, nil, err
	}
	res, err := s.BuildLoginResult(ctx, updated)
	return res, updated, err
}

// LoginStatus xử lý GET /auth/login/{login}: login đã cấp token hoặc đã thu hồi thì không còn dùng được (granted kiểm trước).
func (s *Service) LoginStatus(ctx context.Context, loginID string) (*LoginResult, *fhir.Login, error) {
	login, err := s.Logins.Get(ctx, loginID)
	if err != nil {
		return nil, nil, err
	}
	if domain.IsTrue(login.Granted) {
		return nil, nil, domain.BadRequest("Login already granted")
	}
	if domain.IsTrue(login.Revoked) {
		return nil, nil, domain.BadRequest("Login revoked")
	}
	res, err := s.BuildLoginResult(ctx, login)
	return res, login, err
}

// MethodResult là phản hồi của POST /auth/method: rỗng nếu đăng nhập bằng mật khẩu, có đủ hai trường nếu qua IdP ngoài.
type MethodResult struct {
	Domain       string `json:"domain,omitempty"`
	AuthorizeURL string `json:"authorizeUrl,omitempty"`
}

// LoginMethod cho biết email đăng nhập bằng mật khẩu hay qua identity provider của tên miền (auth/method.ts).
func (s *Service) LoginMethod(ctx context.Context, email string) (*MethodResult, error) {
	res, err := s.ExternalAuthFor(ctx, email)
	if err != nil || res == nil {
		return &MethodResult{}, err
	}
	return res, nil
}

// externalIdP tìm IdP của tên miền email: nil nếu không có DomainConfiguration hoặc cấu hình chưa đủ (cần authorizeUrl và clientId). domainPart đã hạ chữ thường
// (lệch có chủ đích: Medplum trả nguyên văn nhưng tìm bằng chữ thường, nên client gửi lại domain có chữ hoa sẽ sai ở bước kiểm email).
func (s *Service) externalIdP(ctx context.Context, email string) (domainPart string, cfg *fhir.DomainConfiguration, err error) {
	at := strings.LastIndex(email, "@")
	if at < 0 {
		return "", nil, nil
	}
	domainPart = lower(email[at+1:])
	cfg, err = s.DomainConfigs.FindByDomain(ctx, domainPart)
	if err != nil || cfg == nil {
		return domainPart, nil, err
	}
	idp := cfg.IdentityProvider
	if idp == nil || idp.AuthorizeUrl == "" || idp.ClientId == "" {
		return domainPart, nil, nil
	}
	return domainPart, cfg, nil
}

func (s *Service) externalCallbackURL(clientID string) string {
	u := trimBase(s.BaseURL) + "/auth/external"
	if clientID != "" {
		u += "/" + clientID
	}
	return u
}

// ExternalAuthFor trả cấu hình đăng nhập ngoài của tên miền email, nil nếu không có hoặc cấu hình chưa đủ. URL KHÔNG có state (dùng để nhận biết tên miền
// thuộc IdP, ví dụ chặn đặt lại mật khẩu); đăng nhập thật phải đi qua StartExternalLogin để có state ký.
func (s *Service) ExternalAuthFor(ctx context.Context, email string) (*MethodResult, error) {
	domainPart, cfg, err := s.externalIdP(ctx, strings.ToLower(email))
	if err != nil || cfg == nil {
		return nil, err
	}
	u, err := domain.BuildAuthorizeURL(cfg.IdentityProvider, s.externalCallbackURL(""), "")
	if err != nil {
		s.Logger.ErrorContext(ctx, "authorizeUrl không hợp lệ", "domain", domainPart)
		return nil, err
	}
	return &MethodResult{Domain: domainPart, AuthorizeURL: u}, nil
}

// StartExternalLogin là /auth/method khi đăng nhập ngoài đã bật (Q1): phát `state` ĐÃ KÝ (chứa tham số đăng nhập do app gửi) và trả kèm giá trị cookie liên
// kết trình duyệt. Callback chỉ nhận state do chính auth phát, đúng trình duyệt đã gọi /auth/method (chống login fixation và CSRF ở callback). Chưa bật thì
// như ExternalAuthFor (không state, không cookie).
func (s *Service) StartExternalLogin(ctx context.Context, email string, st domain.ExternalState) (*MethodResult, string, error) {
	if !s.External.Enabled() {
		res, err := s.ExternalAuthFor(ctx, email)
		return res, "", err
	}
	domainPart, cfg, err := s.externalIdP(ctx, strings.ToLower(email))
	if err != nil || cfg == nil {
		return nil, "", err
	}
	st.Domain = domainPart
	state, nonce, err := s.IssueExternalState(st)
	if err != nil {
		return nil, "", err
	}
	u, err := domain.BuildAuthorizeURL(cfg.IdentityProvider, s.externalCallbackURL(""), state)
	if err != nil {
		return nil, "", err
	}
	return &MethodResult{Domain: domainPart, AuthorizeURL: u}, nonce, nil
}

// StartClientExternalLogin khởi đầu đăng nhập ngoài của một client có identityProvider RIÊNG (callback /auth/external/{clientId}): luồng này không đi theo tên miền email nên
// không qua /auth/method. Phát state ký và cookie liên kết như StartExternalLogin; state không có domain (IdP và danh tính do client quyết định, có thể ánh xạ theo sub).
func (s *Service) StartClientExternalLogin(ctx context.Context, clientID string, st domain.ExternalState) (*MethodResult, string, error) {
	if !s.External.Enabled() {
		return nil, "", domain.Internal("đăng nhập ngoài chưa được bật")
	}
	client, err := s.GetClientApplication(ctx, clientID)
	if err != nil {
		return nil, "", err
	}
	idp := client.IdentityProvider
	if idp == nil || idp.AuthorizeUrl == "" || idp.ClientId == "" {
		return nil, "", domain.BadRequest("Client does not have an identity provider")
	}
	if st.RedirectURI != "" && domain.MatchRedirectURI(client, st.RedirectURI) == "" {
		return nil, "", domain.BadRequest("Invalid redirect URI")
	}
	st.ClientID, st.Domain = client.ID, ""
	state, nonce, err := s.IssueExternalState(st)
	if err != nil {
		return nil, "", err
	}
	u, err := domain.BuildAuthorizeURL(idp, s.externalCallbackURL(client.ID), state)
	if err != nil {
		return nil, "", err
	}
	return &MethodResult{AuthorizeURL: u}, nonce, nil
}

func lower(s string) string { return strings.ToLower(s) }

// parseAbsURL yêu cầu có scheme và host: url.Parse chấp nhận "example.com/oauth2/authorize" nên phải kiểm riêng để khớp Medplum
// (URL thiếu giao thức là lỗi).
func parseAbsURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if u.Scheme == "" || u.Host == "" {
		return nil, errors.New("URL thiếu giao thức hoặc host")
	}
	return u, nil
}
