package application

import (
	"context"

	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
)

// GrantTypePreAuthorizedCode là grant_type của pre-authorized_code (giá trị client của Medplum gửi, core/client.ts).
const GrantTypePreAuthorizedCode = "urn:ietf:params:oauth:grant-type:pre-authorized_code"

// PreAuthorizedCodeRequest là yêu cầu grant pre-authorized_code.
type PreAuthorizedCodeRequest struct {
	ClientID, Code string
	RemoteAddress  string
	UserAgent      string
}

// PreAuthorizedCode đổi mã cấp sẵn (do /auth/preauthorize của P6 tạo) lấy token (token.ts:741-825). Mã là bearer, không có PKCE hay xác thực
// client: an toàn nhờ chỉ lưu BĂM (SHA-256), có hạn (`expiresAt`), dùng một lần và việc chiếm mã là một lần ghi có điều kiện phiên bản
// (hai yêu cầu song song chỉ một cái thành công). Dùng lại thì thu hồi Login. Khác Medplum: kiểm IP và chính sách TRƯỚC khi chiếm mã (lỗi
// không đốt mã), và chính sách không hỗ trợ bị từ chối như các grant khác.
func (s *Service) PreAuthorizedCode(ctx context.Context, req PreAuthorizedCodeRequest) (TokenResponse, *domain.OAuthError) {
	if req.ClientID == "" {
		return nil, domain.ErrInvalidRequest("Missing client_id")
	}
	if req.Code == "" {
		return nil, domain.ErrInvalidRequest("Missing pre-authorized_code")
	}
	client, err := s.GetClientApplication(ctx, req.ClientID)
	if err != nil {
		if domain.KindOf(err) == domain.KindNotFound {
			return nil, domain.ErrInvalidClient("Invalid client")
		}
		return nil, domain.ErrServer()
	}
	if client.Status != "" && client.Status != "active" {
		return nil, domain.ErrInvalidRequest("Invalid client")
	}
	login, err := s.Logins.FindByPreAuthorizedCodeHash(ctx, domain.HashSecret(req.Code))
	if err != nil {
		s.Logger.ErrorContext(ctx, "tra Login theo pre-authorized code lỗi", "error", err)
		return nil, domain.ErrServer()
	}
	if login == nil || login.AuthMethod != "pre-authorized" {
		return nil, domain.ErrInvalidRequest("Invalid pre-authorized_code")
	}
	if refOf(login.Client) != "ClientApplication/"+req.ClientID {
		return nil, domain.ErrInvalidRequest("Invalid client")
	}
	if login.Membership == nil {
		return nil, domain.ErrInvalidRequest("Invalid profile")
	}
	if domain.IsTrue(login.Revoked) {
		return nil, domain.ErrInvalidGrant("Token revoked")
	}
	if domain.IsTrue(login.Granted) {
		return nil, s.codeReused(ctx, login, req.RemoteAddress)
	}
	if expires, ok := parseAuthTime(login.ExpiresAt); !ok || !s.Clock.Now().Before(expires) {
		return nil, domain.ErrInvalidGrant("Pre-authorized code expired")
	}
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
	rules, err := s.ResolveIPAccessRules(ctx, m)
	if err != nil {
		return nil, domain.ErrAccessDenied(invalidAccessPolicy)
	}
	if err := domain.CheckIPAccessRules(req.RemoteAddress, rules); err != nil {
		return nil, domain.ErrInvalidRequest("IP address not allowed")
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
	login, oerr = s.claimCode(ctx, login, req.RemoteAddress)
	if oerr != nil {
		return nil, oerr
	}
	return s.IssueTokens(ctx, login, client, m, project, launch, scopeClaim, policy)
}

// RegisteredClient là kết quả của /oauth2/register (không có client_secret).
type RegisteredClient struct {
	ClientID       string   `json:"client_id" binding:"required" example:"0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a77"` // id của client dựng sẵn khớp redirect_uri
	ClientIDIssued int64    `json:"client_id_issued_at" binding:"required" example:"1795428900"`                 // thời điểm trả về, Unix epoch tính bằng giây
	RedirectURIs   []string `json:"redirect_uris" binding:"required" example:"https://app.example.com/callback"` // redirect URI khớp (đúng một phần tử)
}

// RegisterClient tra client dựng sẵn theo redirect_uri (register.ts:16): không phải đăng ký động theo RFC 7591, không tạo gì mới và không
// trả secret. redirect_uri phải bằng CHÍNH XÁC một redirect URI của client dựng sẵn.
func (s *Service) RegisterClient(uris []string) (*RegisteredClient, *domain.OAuthError) {
	if !s.OAuthRegister {
		return nil, domain.ErrInvalidRequest("Registration is not enabled")
	}
	if len(uris) == 0 || uris[0] == "" {
		return nil, domain.ErrInvalidRequest("Redirect URI is required")
	}
	for i := range s.StandardClients {
		c := &s.StandardClients[i]
		if domain.MatchRedirectURI(c, uris[0]) != "" {
			return &RegisteredClient{ClientID: c.ID, ClientIDIssued: s.Clock.Now().Unix(), RedirectURIs: uris[:1]}, nil
		}
	}
	return nil, domain.ErrInvalidRequest("Invalid redirect URI")
}
