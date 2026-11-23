package application

import (
	"context"
	"errors"
	"strings"
	"time"

	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
)

// RefreshRequest là yêu cầu grant refresh_token đã được lớp presentation tách (Basic hoặc body).
type RefreshRequest struct {
	// Authenticated là client đã xác thực bằng private_key_jwt; nil nếu không dùng. Phải là client của Login.
	Authenticated *fhir.ClientApplication
	RefreshToken  string
	// ClientID và ClientSecret là thông tin client gửi lên (đều có thể rỗng).
	ClientID, ClientSecret string
	RemoteAddress          string
	UserAgent              string
}

var errInvalidRefresh = domain.ErrInvalidRequest("Invalid refresh token")

// refreshClaims xác minh refresh token do chính auth ký và trả login_id cùng secret trong token. Chấp nhận đúng loại `refresh` có aud
// riêng: access token, id token và JWT ký bằng khóa/thuật toán khác (none, HS256) đều bị từ chối.
func (s *Service) refreshClaims(raw string) (loginID, secret string, ok bool) {
	if s.Verifier == nil || raw == "" {
		return "", "", false
	}
	claims, err := s.Verifier.Verify(raw)
	if err != nil {
		return "", "", false
	}
	if iss, _ := claims["iss"].(string); iss != s.Tokens.Issuer {
		return "", "", false
	}
	if use, _ := claims["token_use"].(string); use != tokenUseRefresh {
		return "", "", false
	}
	if aud, _ := claims["aud"].(string); aud != s.Tokens.refreshAudience() {
		return "", "", false
	}
	loginID, _ = claims["login_id"].(string)
	secret, _ = claims["refresh_secret"].(string)
	return loginID, secret, loginID != "" && secret != ""
}

// Refresh xử lý grant refresh_token (token.ts:329-437). Mỗi lần dùng, refresh secret của Login được XOAY bằng một lần ghi có điều kiện
// theo phiên bản, nên hai yêu cầu song song không thể cùng xoay. Khác Medplum:
//   - client có secret mà Login không có PKCE (client bí mật) bắt buộc xác thực; client đã gửi secret thì secret phải đúng;
//   - luật ipAccessRule của membership được kiểm, và IP chỉ được ghi vào Login sau khi qua kiểm tra;
//   - ân hạn cấu hình được (AUTH_REFRESH_GRACE, 0 là tắt), trần tuyệt đối của phiên (AUTH_SESSION_MAX_AGE);
//   - Login không tồn tại trả invalid_grant thay vì làm handler ném lỗi.
//
// Dùng lại một secret đã xoay ngoài cửa sổ ân hạn (hoặc từ IP khác) là dấu hiệu lộ token: Login bị thu hồi.
func (s *Service) Refresh(ctx context.Context, req RefreshRequest) (TokenResponse, *domain.OAuthError) {
	loginID, presented, ok := s.refreshClaims(req.RefreshToken)
	if !ok {
		return nil, errInvalidRefresh
	}
	login, err := s.Logins.Get(ctx, loginID)
	if err != nil {
		if domain.KindOf(err) == domain.KindNotFound {
			return nil, domain.ErrInvalidGrant("Invalid refresh token")
		}
		s.Logger.ErrorContext(ctx, "đọc Login khi refresh lỗi", "error", err)
		return nil, domain.ErrServer()
	}
	if login.RefreshSecret == "" {
		return nil, errInvalidRefresh
	}
	if domain.IsTrue(login.Revoked) {
		return nil, domain.ErrInvalidGrant("Token revoked")
	}
	if login.Membership == nil {
		return nil, errInvalidRefresh
	}
	m, err := s.Memberships.Get(ctx, idOf(login.Membership.Reference))
	if err != nil {
		return nil, domain.ErrInvalidGrant("Invalid refresh token")
	}
	project, err := s.Projects.Get(ctx, idOf(refOf(m.Project)))
	if err != nil {
		return nil, domain.ErrInvalidGrant("Invalid refresh token")
	}
	if reason := domain.InactiveReason(project, m); reason != "" {
		return nil, domain.ErrAccessDenied(reason)
	}

	client, oerr := s.refreshClient(ctx, login, req)
	if oerr != nil {
		return nil, oerr
	}
	if max := s.Tokens.SessionMaxAge; max > 0 {
		if authTime, ok := parseAuthTime(login.AuthTime); !ok || !s.Clock.Now().Before(authTime.Add(max)) {
			return nil, domain.ErrInvalidGrant("Session expired")
		}
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
			return nil, domain.ErrInvalidGrant("Invalid refresh token")
		}
	}
	scopeClaim, policy, oerr := s.authorization(ctx, login.Scope, m)
	if oerr != nil {
		return nil, oerr
	}

	login, oerr = s.rotateRefresh(ctx, login, presented, req)
	if oerr != nil {
		return nil, oerr
	}
	return s.IssueTokens(ctx, login, client, m, project, launch, scopeClaim, policy)
}

// refreshClient nạp client của Login và xác thực nếu cần. Phải xác thực khi client có secret và Login không đi qua PKCE (client bí mật);
// client công khai (PKCE) không có secret để gửi, refresh token chính là bằng chứng. Credential đã gửi thì luôn phải đúng.
func (s *Service) refreshClient(ctx context.Context, login *fhir.Login, req RefreshRequest) (*fhir.ClientApplication, *domain.OAuthError) {
	loginClient := refOf(login.Client)
	if req.ClientID != "" && loginClient != "ClientApplication/"+req.ClientID {
		return nil, domain.ErrInvalidGrant("Incorrect client")
	}
	if loginClient == "" {
		if req.ClientSecret != "" || req.Authenticated != nil {
			return nil, domain.ErrInvalidGrant("Incorrect client")
		}
		return nil, nil
	}
	client, err := s.GetClientApplication(ctx, idOf(loginClient))
	if err != nil {
		return nil, domain.ErrInvalidRequest("Invalid client")
	}
	if client.Status != "" && client.Status != "active" {
		return nil, domain.ErrInvalidRequest("Invalid client")
	}
	if req.Authenticated != nil {
		if req.Authenticated.ID != client.ID {
			return nil, domain.ErrInvalidGrant("Incorrect client")
		}
		return client, nil
	}
	confidential := client.Secret != "" && login.CodeChallenge == ""
	if req.ClientSecret == "" {
		if confidential {
			return nil, domain.ErrInvalidGrant("Incorrect client secret")
		}
		return client, nil
	}
	if client.Secret == "" || !domain.MatchClientSecret(client, req.ClientSecret) {
		return nil, domain.ErrInvalidRequest("Invalid secret")
	}
	return client, nil
}

// rotateRefresh xoay refresh secret bằng cập nhật có điều kiện. Trả Login với secret hiện hành để ký token. Xung đột phiên bản thì đọc lại
// và đánh giá lại: secret trình lên giờ là secret TRƯỚC thì rơi vào ân hạn (cùng IP), nếu không thì là dùng lại.
func (s *Service) rotateRefresh(ctx context.Context, login *fhir.Login, presented string, req RefreshRequest) (*fhir.Login, *domain.OAuthError) {
	now := s.Clock.Now()
	for attempt := 0; attempt < claimAttempts; attempt++ {
		if domain.IsTrue(login.Revoked) {
			return nil, domain.ErrInvalidGrant("Token revoked")
		}
		switch {
		case domain.EqualSecret(login.RefreshSecret, presented):
			next := *login
			next.PreviousRefreshSecret = presented
			next.RefreshSecret = domain.GenerateSecret(32)
			next.RefreshSecretRotatedAt = now.UTC().Format("2006-01-02T15:04:05.000Z")
			next.RemoteAddress, next.UserAgent = req.RemoteAddress, req.UserAgent
			version := ""
			if login.Meta != nil {
				version = login.Meta.VersionId
			}
			out, err := s.Logins.UpdateIfMatch(ctx, &next, version)
			if err == nil {
				return out, nil
			}
			if !errors.Is(err, domain.ErrVersionConflict) {
				s.Logger.ErrorContext(ctx, "xoay refresh secret lỗi", "login", login.ID, "error", err)
				return nil, domain.ErrServer()
			}
		case s.withinGrace(login, presented, req.RemoteAddress, now):
			s.Logger.InfoContext(ctx, "refresh token trước đó được chấp nhận trong ân hạn", "login", login.ID, "ip", req.RemoteAddress)
			return login, nil
		default:
			s.Logger.WarnContext(ctx, "refresh token đã xoay bị dùng lại, thu hồi Login", "login", login.ID, "client", refOf(login.Client), "ip", req.RemoteAddress)
			if err := s.RevokeLogin(ctx, login); err != nil {
				s.Logger.ErrorContext(ctx, "không thu hồi được Login sau khi refresh token bị dùng lại", "login", login.ID, "error", err)
			}
			return nil, domain.ErrInvalidGrant("Token revoked")
		}
		var err error
		if login, err = s.Logins.Get(ctx, login.ID); err != nil {
			return nil, domain.ErrServer()
		}
	}
	return nil, domain.ErrInvalidGrant("Token revoked")
}

// withinGrace: secret trình lên là secret TRƯỚC, vừa xoay trong cửa sổ ân hạn, và yêu cầu đến từ đúng IP đã xoay (isWithinRefreshGracePeriod,
// token.ts:447). Đây là cách nhiều yêu cầu song song hoặc một yêu cầu thử lại cùng nhận token mà không bị coi là dùng lại.
func (s *Service) withinGrace(login *fhir.Login, presented, ip string, now time.Time) bool {
	grace := s.Tokens.RefreshGrace
	if grace <= 0 || domain.IsTrue(login.Revoked) || login.RefreshSecret == "" || login.PreviousRefreshSecret == "" || login.RefreshSecretRotatedAt == "" ||
		strings.TrimSpace(ip) == "" || ip != login.RemoteAddress || !domain.EqualSecret(login.PreviousRefreshSecret, presented) {
		return false
	}
	rotated, ok := parseAuthTime(login.RefreshSecretRotatedAt)
	if !ok {
		return false
	}
	age := now.Sub(rotated)
	return age >= 0 && age < grace
}
