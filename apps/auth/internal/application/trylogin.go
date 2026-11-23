package application

import (
	"context"
	"strings"

	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
)

const invalidCredentials = "Email or password is invalid"

func (s *Service) newLoginResource(req domain.LoginRequest, client *fhir.ClientApplication, user *fhir.User) *fhir.Login {
	l := &fhir.Login{
		ResourceType: "Login", User: &fhir.Reference{Reference: "User/" + user.ID}, AuthMethod: req.AuthMethod,
		AuthTime: s.Clock.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		Scope:    req.Scope, Nonce: req.Nonce, ProfileType: req.ResourceType,
		CodeChallenge: req.CodeChallenge, CodeChallengeMethod: req.CodeChallengeMethod,
		RemoteAddress: req.RemoteAddress, UserAgent: req.UserAgent, PictureUrl: req.PictureURL,
	}
	if client != nil {
		l.Client = &fhir.Reference{Reference: "ClientApplication/" + client.ID}
	}
	if req.RedirectURI != "" {
		domain.SetLoginRedirectURI(l, req.RedirectURI)
	}
	if req.LaunchID != "" {
		l.Launch = &fhir.Reference{Reference: "SmartAppLaunch/" + req.LaunchID}
	}
	if req.ProjectID != "" {
		l.Project = &fhir.Reference{Reference: "Project/" + req.ProjectID}
	}
	if domain.IncludeRefreshToken(req, client) {
		l.RefreshSecret = domain.GenerateSecret(32)
	}
	return l
}

// TryLogin xác thực và tạo Login (oauth/utils.ts:155). Trả login đã có membership nếu chỉ có đúng một lựa chọn; ngược lại
// trả login chưa có membership để người dùng chọn tiếp.
func (s *Service) TryLogin(ctx context.Context, req domain.LoginRequest) (*fhir.Login, error) {
	client, err := s.checkLoginRequest(ctx, req)
	if err != nil {
		return nil, err
	}
	user, err := s.findLoginUser(ctx, req)
	if err != nil {
		return nil, err
	}
	if user == nil {
		// Không có user: vẫn tốn một lần bcrypt với hash giả để thời gian không lộ email có tồn tại hay không.
		s.Logger.WarnContext(ctx, "tryLogin: không tìm thấy user", "project", req.ProjectID, "client", req.ClientID)
		if req.Password != "" {
			s.Hasher.CompareDummy(req.Password)
			return nil, domain.BadRequest(invalidCredentials)
		}
		return nil, domain.BadRequest("User not found")
	}

	authenticatedHash := user.PasswordHash
	switch {
	case req.Password != "":
		if !s.VerifyPassword(user, req.Password) {
			return nil, domain.BadRequest(invalidCredentials)
		}
	case req.HasGoogleCredentials || req.AuthMethod == domain.AuthExternal || req.AuthMethod == domain.AuthExchange:
		// đã được xác minh ở nơi khác (P6)
	default:
		return nil, domain.BadRequest("Invalid authentication method")
	}

	draft := s.newLoginResource(req, client, user)
	// Đăng nhập qua yếu tố ngoài mật khẩu (external, exchange, google): kiểm membership TRƯỚC khi tạo Login (Q10). Medplum tạo Login trước nên mỗi lần
	// đăng nhập ngoài thất bại để lại một Login rác; mật khẩu giữ thứ tự cũ vì cần tạo Login trước để đóng cửa sổ đua với SetPassword.
	var memberships []fhir.ProjectMembership
	if req.Password == "" {
		draft.User = &fhir.Reference{Reference: "User/" + user.ID}
		if memberships, err = s.selectableMemberships(ctx, draft, req); err != nil {
			return nil, err
		}
	}
	login, err := s.Logins.Create(ctx, draft)
	if err != nil {
		return nil, err
	}

	// Đọc lại user SAU khi tạo Login: nếu mật khẩu đổi trong lúc đó thì hoặc việc đổi mật khẩu thấy login này để thu hồi,
	// hoặc ta thấy mật khẩu mới ở đây (đóng cửa sổ đua với SetPassword).
	if req.Password != "" {
		fresh, err := s.Users.Get(ctx, user.ID)
		if err != nil {
			return nil, err
		}
		if fresh.PasswordHash != authenticatedHash {
			if rerr := s.RevokeLogin(ctx, login); rerr != nil {
				return nil, rerr
			}
			return nil, domain.BadRequest(invalidCredentials)
		}
	}

	if req.Password != "" {
		if memberships, err = s.selectableMemberships(ctx, login, req); err != nil {
			return nil, err
		}
	}
	if len(memberships) == 1 || (req.ForceUseFirstMembership && len(memberships) > 0) {
		return s.SetLoginMembership(ctx, login, &memberships[0])
	}
	return login, nil
}

// trimBase bỏ dấu / cuối để nối đường dẫn.
func trimBase(u string) string { return strings.TrimRight(u, "/") }

// checkLoginRequest là phần kiểm tra yêu cầu đăng nhập không phụ thuộc user: dạng yêu cầu, client, origin, redirect URI, PKCE, launch.
// Tách riêng để đăng ký (newuser) kiểm TRƯỚC khi tạo user: ở Medplum các kiểm tra này chạy sau khi đã tạo user nên PKCE sai để lại
// user mồ côi (lệch L1).
func (s *Service) checkLoginRequest(ctx context.Context, req domain.LoginRequest) (*fhir.ClientApplication, error) {
	if err := domain.ValidateLoginRequest(req); err != nil {
		return nil, err
	}
	var client *fhir.ClientApplication
	if req.ClientID != "" {
		var err error
		if client, err = s.GetClientApplication(ctx, req.ClientID); err != nil {
			return nil, err
		}
		if !domain.OriginAllowed(client, req.Origin) {
			return nil, domain.BadRequest("Invalid origin")
		}
		if req.RedirectURI != "" && domain.MatchRedirectURI(client, req.RedirectURI) == "" {
			return nil, domain.BadRequest("Invalid redirect URI")
		}
	}
	if err := domain.ValidatePKCE(req, client); err != nil {
		return nil, err
	}
	if req.LaunchID != "" {
		if _, err := s.Launches.Get(ctx, req.LaunchID); err != nil {
			return nil, err
		}
	}
	return client, nil
}

// selectableMemberships liệt kê membership login có thể chọn, đã lọc theo membershipId; không có và không allowNoMembership thì "User not found".
func (s *Service) selectableMemberships(ctx context.Context, login *fhir.Login, req domain.LoginRequest) ([]fhir.ProjectMembership, error) {
	memberships, err := s.MembershipsForLogin(ctx, login)
	if err != nil {
		return nil, err
	}
	if req.MembershipID != "" {
		var only []fhir.ProjectMembership
		for _, m := range memberships {
			if m.ID == req.MembershipID {
				only = append(only, m)
			}
		}
		memberships = only
	}
	if len(memberships) == 0 && !req.AllowNoMembership {
		// mật khẩu: login đã tạo không bị thu hồi nhưng không bao giờ trả code ra ngoài (dọn bằng `purge-logins`); các phương thức khác: chưa tạo gì
		return nil, domain.BadRequest("User not found")
	}
	return memberships, nil
}

// findLoginUser tìm user cho yêu cầu đăng nhập: có externalId thì theo ProjectMembership.externalId trong project (KHỚP CHÍNH XÁC, phải có projectId); ngược lại
// theo email (oauth/utils.ts:860-945). Không dùng User.externalId (deprecated, chỉ cần khi có dữ liệu di trú).
func (s *Service) findLoginUser(ctx context.Context, req domain.LoginRequest) (*fhir.User, error) {
	if req.ExternalID == "" {
		return s.FindUserByEmail(ctx, req.Email, req.ProjectID)
	}
	if req.ProjectID == "" || req.ProjectID == "new" {
		return nil, nil
	}
	m, err := s.Memberships.FindByExternalID(ctx, req.ExternalID, "Project/"+req.ProjectID)
	if err != nil || m == nil || m.User == nil {
		return nil, err
	}
	u, err := s.Users.Get(ctx, idOf(refOf(m.User)))
	if domain.KindOf(err) == domain.KindNotFound {
		return nil, nil
	}
	return u, err
}
