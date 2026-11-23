package application

import (
	"context"
	"errors"
	"time"

	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
)

// AuthorizationCodeRequest là yêu cầu grant authorization_code đã được lớp presentation tách (Basic hoặc body).
type AuthorizationCodeRequest struct {
	// Authenticated là client đã xác thực bằng private_key_jwt; nil nếu không dùng. Phải là client của Login.
	Authenticated *fhir.ClientApplication
	// ClientID và ClientSecret là thông tin client gửi lên (đều có thể rỗng: client công khai chứng minh bằng PKCE).
	ClientID, ClientSecret string
	Code, CodeVerifier     string
	RedirectURI            string
	RemoteAddress          string
	UserAgent              string
}

// claimAttempts là số lần thử chiếm code khi gặp xung đột phiên bản (mỗi lần xung đột nghĩa là Login vừa bị ghi bởi người khác).
const claimAttempts = 5

// AuthorizationCode xử lý grant authorization_code (token.ts:223-321), chặt hơn Medplum ở các điểm sau (kế hoạch P3 mục 8):
//   - code chỉ lưu dạng băm, hạn tính từ lúc phát (Login.expiresAt), không bị kéo dài bởi các lần ghi Login;
//   - PKCE LUÔN được kiểm khi Login có challenge (kể cả client pkceOptional); Login không có challenge chỉ qua được khi client đã
//     xác thực bằng secret; chỉ nhận S256 (plain khi AllowPlainPKCE);
//   - redirect_uri lưu lúc đăng nhập phải khớp chính xác;
//   - code được CHIẾM nguyên tử (cập nhật có điều kiện theo phiên bản) trước khi ký token: hai yêu cầu song song, đúng một cái thành công;
//   - dùng lại code đã cấp thì thu hồi Login (phát hiện lộ code) và ghi log;
//   - sai verifier hay sai secret KHÔNG tiêu thụ code (kẻ lộ code không đốt được code của người dùng thật).
//
// Nếu client gửi secret thì secret phải đúng (không bỏ qua như Medplum ở nhánh PKCE). Client không gửi secret thì dựa vào PKCE.
func (s *Service) AuthorizationCode(ctx context.Context, req AuthorizationCodeRequest) (TokenResponse, *domain.OAuthError) {
	if req.Code == "" {
		return nil, domain.ErrInvalidRequest("Missing code")
	}
	login, err := s.Logins.FindByCode(ctx, domain.HashSecret(req.Code))
	if err != nil {
		s.Logger.ErrorContext(ctx, "tra Login theo code lỗi", "error", err)
		return nil, domain.ErrServer()
	}
	if login == nil {
		return nil, domain.ErrInvalidRequest("Invalid code")
	}
	loginClient := refOf(login.Client)
	if req.ClientID != "" && loginClient != "ClientApplication/"+req.ClientID {
		return nil, domain.ErrInvalidRequest("Invalid client")
	}
	if login.Membership == nil {
		return nil, domain.ErrInvalidRequest("Invalid profile")
	}
	// Q8 (phòng thủ chiều sâu): Medplum chỉ ép MFA ở chỗ duy nhất là sendLoginResult không trả code; token endpoint không kiểm mfaVerified. Kiểm thêm ở đây: login mật
	// khẩu của user đã đăng ký MFA (hoặc bị bắt buộc) mà chưa xác minh thì không đổi được code, dù code có lọt ra bằng đường nào.
	if login.AuthMethod == domain.AuthPassword && !domain.IsTrue(login.MfaVerified) && login.User != nil {
		user, err := s.Users.Get(ctx, idOf(refOf(login.User)))
		if err != nil {
			s.Logger.ErrorContext(ctx, "đọc user khi đổi code lỗi", "error", err)
			return nil, domain.ErrServer()
		}
		switch domain.DecideLoginResult(user, s.LoginProject(ctx, login), login) {
		case domain.ResultMFAEnroll, domain.ResultMFARequired:
			return nil, domain.ErrInvalidGrant("MFA verification required")
		}
	}
	if domain.IsTrue(login.Revoked) {
		return nil, domain.ErrInvalidGrant("Token revoked")
	}
	if domain.IsTrue(login.Granted) {
		return nil, s.codeReused(ctx, login, req.RemoteAddress)
	}
	if expires, ok := parseAuthTime(login.ExpiresAt); !ok || !s.Clock.Now().Before(expires) {
		return nil, domain.ErrInvalidGrant("Authorization code expired")
	}

	// client: client_id gửi lên (đã khớp Login) hoặc client của Login; Login không có client thì không có client
	var client *fhir.ClientApplication
	if loginClient != "" {
		if client, err = s.GetClientApplication(ctx, idOf(loginClient)); err != nil {
			return nil, domain.ErrInvalidRequest("Invalid client")
		}
		if client.Status != "" && client.Status != "active" {
			return nil, domain.ErrInvalidRequest("Invalid client")
		}
	}
	authenticated := false
	if req.Authenticated != nil {
		if loginClient != "ClientApplication/"+req.Authenticated.ID {
			return nil, domain.ErrInvalidRequest("Invalid client")
		}
		authenticated = true
	}
	if req.ClientSecret != "" {
		if client == nil {
			return nil, domain.ErrInvalidRequest("Invalid client")
		}
		if client.Secret == "" {
			return nil, domain.ErrInvalidRequest("Invalid client")
		}
		if !domain.MatchClientSecret(client, req.ClientSecret) {
			return nil, domain.ErrInvalidRequest("Invalid secret")
		}
		authenticated = true
	}

	switch {
	case login.CodeChallenge != "":
		if req.CodeVerifier == "" {
			return nil, domain.ErrInvalidRequest("Missing code verifier")
		}
		if !domain.VerifyPKCE(login.CodeChallenge, login.CodeChallengeMethod, req.CodeVerifier, s.Tokens.AllowPlainPKCE) {
			s.Logger.WarnContext(ctx, "PKCE verifier sai khi đổi code", "login", login.ID, "client", loginClient, "ip", req.RemoteAddress)
			return nil, domain.ErrInvalidRequest("Invalid code verifier")
		}
	case !authenticated:
		// Không có challenge và client không xác thực: không có bằng chứng nào người đổi code là người đã đăng nhập.
		s.Logger.WarnContext(ctx, "từ chối đổi code không có PKCE và không xác thực client", "login", login.ID, "client", loginClient, "ip", req.RemoteAddress)
		return nil, domain.ErrInvalidRequest("Missing verification context")
	}
	if stored := domain.LoginRedirectURI(login); stored != "" {
		if req.RedirectURI == "" {
			return nil, domain.ErrInvalidRequest("Missing redirect_uri")
		}
		if req.RedirectURI != stored {
			return nil, domain.ErrInvalidGrant("Invalid redirect_uri")
		}
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

// claimCode đặt granted=true kèm điều kiện phiên bản: chỉ MỘT yêu cầu thắng. Xung đột thì đọc lại; thấy đã granted nghĩa là code bị
// dùng lại (hoặc yêu cầu song song thua cuộc) và Login bị thu hồi.
func (s *Service) claimCode(ctx context.Context, login *fhir.Login, ip string) (*fhir.Login, *domain.OAuthError) {
	for attempt := 0; attempt < claimAttempts; attempt++ {
		if domain.IsTrue(login.Revoked) {
			return nil, domain.ErrInvalidGrant("Token revoked")
		}
		if domain.IsTrue(login.Granted) {
			return nil, s.codeReused(ctx, login, ip)
		}
		next := *login
		next.Granted = ptrTrue()
		version := ""
		if login.Meta != nil {
			version = login.Meta.VersionId
		}
		out, err := s.Logins.UpdateIfMatch(ctx, &next, version)
		if err == nil {
			return out, nil
		}
		if !errors.Is(err, domain.ErrVersionConflict) {
			s.Logger.ErrorContext(ctx, "chiếm code lỗi", "login", login.ID, "error", err)
			return nil, domain.ErrServer()
		}
		if login, err = s.Logins.Get(ctx, login.ID); err != nil {
			return nil, domain.ErrServer()
		}
	}
	return nil, domain.ErrInvalidGrant("Authorization code already used")
}

// codeReused thu hồi Login khi một code đã cấp token bị đưa ra đổi lần nữa (RFC 6749 §4.1.2, RFC 9700 §4.5): ghi log cảnh báo.
func (s *Service) codeReused(ctx context.Context, login *fhir.Login, ip string) *domain.OAuthError {
	s.Logger.WarnContext(ctx, "authorization code bị dùng lại, thu hồi Login", "login", login.ID, "client", refOf(login.Client), "ip", ip)
	if err := s.RevokeLogin(ctx, login); err != nil {
		s.Logger.ErrorContext(ctx, "không thu hồi được Login sau khi code bị dùng lại", "login", login.ID, "error", err)
	}
	return domain.ErrInvalidGrant("Token already granted")
}

// parseAuthTime đọc mốc thời gian RFC 3339 (có hoặc không có phần giây lẻ).
func parseAuthTime(v string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339Nano, v)
	return t, err == nil
}
