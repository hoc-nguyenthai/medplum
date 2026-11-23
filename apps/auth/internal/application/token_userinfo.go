package application

import (
	"context"
	"strings"

	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
)

// UserInfo xây phản hồi OIDC userinfo cho login đã xác thực (userinfo.ts:24). Scope được khớp theo TỪ trên scope của Login. Hồ sơ
// Patient/Practitioner nằm ở kho lâm sàng của fhir-server (D-10), auth không đọc được, nên các trường lấy từ hồ sơ đó (phone, address,
// birthdate, gender, picture...) không có; tên và email lấy từ User. Client lấy hồ sơ đầy đủ từ fhir-server bằng `profile`.
func (s *Service) UserInfo(ctx context.Context, a *AuthState) (map[string]any, error) {
	userRef := refOf(a.Login.User)
	info := map[string]any{"sub": idOf(userRef)}
	scope := a.Login.Scope
	var user *fhir.User
	if strings.HasPrefix(userRef, "User/") {
		u, err := s.Users.Get(ctx, idOf(userRef))
		if err != nil {
			return nil, err
		}
		user = u
	}
	if domain.HasScopeTerm(scope, "profile") {
		if profile := refOf(a.Membership.Profile); profile != "" {
			info["profile"] = profile
		}
		info["locale"] = "en-US"
		if user != nil {
			if t, ok := parseAuthTime(lastUpdated(user.Meta)); ok {
				info["updated_at"] = t.Unix()
			}
			if name := strings.TrimSpace(user.FirstName + " " + user.LastName); name != "" {
				info["name"] = name
			}
			if user.FirstName != "" {
				info["given_name"] = user.FirstName
			}
			if user.LastName != "" {
				info["family_name"] = user.LastName
			}
		}
	}
	if domain.HasScopeTerm(scope, "email") && user != nil && user.Email != "" {
		info["email"], info["email_verified"] = user.Email, domain.IsTrue(user.EmailVerified)
	}
	return info, nil
}

func lastUpdated(m *fhir.Meta) string {
	if m == nil {
		return ""
	}
	return m.LastUpdated
}

// maxSessionChain là số Login tổ tiên tối đa được thu hồi khi đăng xuất (chặn vòng lặp do dữ liệu hỏng).
const maxSessionChain = 16

// Logout thu hồi login của người gọi (logout.ts:10) VÀ các Login tổ tiên theo `basedOn`: Login do /oauth2/authorize tạo từ một phiên đang có
// (cookie) dựa trên Login gốc giữ cookie, nên thu hồi cả chuỗi mới thật sự chấm dứt phiên trình duyệt (Q11). Chỉ ghi `revoked` lên bản mới
// nhất. Các Login con của tổ tiên (ứng dụng khác dùng chung phiên) chưa bị thu hồi: Login chưa có search param `based-on` để liệt kê.
func (s *Service) Logout(ctx context.Context, a *AuthState) error {
	login := a.Login
	for i := 0; i < maxSessionChain && login != nil; i++ {
		if err := s.RevokeLogin(ctx, login); err != nil {
			return err
		}
		if login.BasedOn == nil || !strings.HasPrefix(login.BasedOn.Reference, "Login/") {
			return nil
		}
		parent, err := s.Logins.Get(ctx, idOf(login.BasedOn.Reference))
		if err != nil {
			if domain.KindOf(err) == domain.KindNotFound {
				return nil
			}
			return err
		}
		login = parent
	}
	return nil
}

// Introspect trả kết quả RFC 7662 cho một token, do người gọi đã xác thực (Bearer) hỏi. Mọi lý do token không dùng được (chữ ký, hết hạn,
// sai loại, Login chưa cấp hoặc đã thu hồi, membership hay project ngừng hoạt động, refresh token đã xoay, token của project hoặc client khác)
// đều ra `{"active": false}`, không phân biệt, để người hỏi không dò được gì về token không phải của mình.
func (s *Service) Introspect(ctx context.Context, caller *AuthState, raw string) map[string]any {
	inactive := map[string]any{"active": false}
	if s.Verifier == nil {
		return inactive
	}
	claims, err := s.Verifier.Verify(raw)
	if err != nil {
		return inactive
	}
	if iss, _ := claims["iss"].(string); iss != s.Tokens.Issuer {
		return inactive
	}
	use, _ := claims["token_use"].(string)
	switch use {
	case tokenUseAccess:
		if _, has := claims["refresh_secret"]; has || !audienceMatches(claims["aud"], s.Tokens.Audiences) {
			return inactive
		}
	case tokenUseRefresh:
		if aud, _ := claims["aud"].(string); aud != s.Tokens.refreshAudience() {
			return inactive
		}
	default: // id token và mọi loại khác
		return inactive
	}
	loginID, _ := claims["login_id"].(string)
	if loginID == "" {
		return inactive
	}
	login, err := s.Logins.Get(ctx, loginID)
	if err != nil || !domain.IsTrue(login.Granted) || domain.IsTrue(login.Revoked) || login.Membership == nil {
		return inactive
	}
	m, err := s.Memberships.Get(ctx, idOf(login.Membership.Reference))
	if err != nil {
		return inactive
	}
	project, err := s.Projects.Get(ctx, idOf(refOf(m.Project)))
	if err != nil || domain.InactiveReason(project, m) != "" {
		return inactive
	}
	// chỉ được hỏi token của CÙNG project và CÙNG client với mình (canIntrospectToken, introspect.ts:78)
	clientID, _ := claims["client_id"].(string)
	callerClient := ""
	if caller.Login.Client != nil {
		callerClient = idOf(caller.Login.Client.Reference)
	}
	if project.ID != caller.Project.ID || clientID != callerClient {
		return inactive
	}
	scope, _ := claims["scope"].(string)
	if use == tokenUseRefresh {
		if !domain.EqualSecret(login.RefreshSecret, stringClaim(claims, "refresh_secret")) || login.RefreshSecret == "" {
			return inactive // đã xoay: chỉ secret hiện hành là hoạt động
		}
		scope = login.Scope
	}
	out := map[string]any{
		"active": true, "iat": numClaim(claims, "iat"), "exp": numClaim(claims, "exp"), "iss": s.Tokens.Issuer,
		"sub": stringClaim(claims, "sub"), "scope": scope,
	}
	if use == tokenUseRefresh {
		out["sub"] = idOf(refOf(login.User))
	}
	if clientID != "" {
		out["client_id"] = clientID
	}
	if patient := s.patientOf(ctx, login, m); patient != "" {
		out["patient"] = patient
	}
	return out
}

func stringClaim(c map[string]any, k string) string { v, _ := c[k].(string); return v }

func numClaim(c map[string]any, k string) int64 {
	switch v := c[k].(type) {
	case float64:
		return int64(v)
	case int64:
		return v
	}
	return 0
}

// patientOf là ngữ cảnh patient của login: từ SmartAppLaunch, hoặc chính profile nếu là Patient (ghi đè như sendTokenResponse).
func (s *Service) patientOf(ctx context.Context, login *fhir.Login, m *fhir.ProjectMembership) string {
	if profile := refOf(m.Profile); strings.HasPrefix(profile, "Patient/") {
		return idOf(profile)
	}
	if login.Launch == nil {
		return ""
	}
	launch, err := s.Launches.Get(ctx, idOf(login.Launch.Reference))
	if err != nil {
		return ""
	}
	return launchValue(launch.Patient)
}
