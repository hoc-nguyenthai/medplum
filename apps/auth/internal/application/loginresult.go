package application

import (
	"context"
	"time"

	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
)

// MembershipChoice là một lựa chọn profile trong phản hồi đăng nhập khi người dùng có nhiều profile.
type MembershipChoice struct {
	ID         string            `json:"id"`
	Project    *fhir.Reference   `json:"project,omitempty"`
	Profile    *fhir.Reference   `json:"profile,omitempty"`
	Identifier []fhir.Identifier `json:"identifier,omitempty"`
}

// LoginResult là phản hồi sau mỗi bước đăng nhập (xem docs/api-contract/auth-dto.md): đúng một dạng trong năm dạng.
type LoginResult struct {
	Login             string             `json:"login"`
	Code              string             `json:"code,omitempty"`
	MfaRequired       bool               `json:"mfaRequired,omitempty"`
	MfaMethods        []string           `json:"mfaMethods,omitempty"`
	Email             string             `json:"email,omitempty"`
	MfaEnrollRequired bool               `json:"mfaEnrollRequired,omitempty"`
	EnrollURI         string             `json:"enrollUri,omitempty"`
	EnrollQRCode      string             `json:"enrollQrCode,omitempty"`
	AllowedMfaMethods []string           `json:"allowedMfaMethods,omitempty"`
	Memberships       []MembershipChoice `json:"memberships,omitempty"`
	// Cookie là giá trị cookie đăng nhập (chỉ có cùng lúc với Code); không bao giờ vào JSON, lớp presentation đặt vào Set-Cookie.
	Cookie string `json:"-"`
}

// BuildLoginResult dựng phản hồi cho login (auth/utils.ts:474). Khi một nhánh cần MFA mà MFAGate lỗi (mặc định ở P2), trả lỗi
// và KHÔNG trả code: không có đường nào để bỏ qua MFA.
func (s *Service) BuildLoginResult(ctx context.Context, login *fhir.Login) (*LoginResult, error) {
	if login.User == nil {
		return nil, domain.BadRequest("User reference is missing")
	}
	user, err := s.Users.Get(ctx, idOf(login.User.Reference))
	if err != nil {
		return nil, err
	}
	project := s.LoginProject(ctx, login)

	switch domain.DecideLoginResult(user, project, login) {
	case domain.ResultMFAEnroll:
		uri, qr, err := s.MFA.BuildEnrollment(ctx, login, user, project)
		if err != nil {
			return nil, domain.InternalErr("MFA enrollment", err)
		}
		return &LoginResult{Login: login.ID, MfaEnrollRequired: true, EnrollURI: uri, EnrollQRCode: qr, AllowedMfaMethods: domain.AllowedMFAMethods(project)}, nil
	case domain.ResultMFARequired:
		methods := domain.EnrolledMFAMethods(user)
		// Email là phương thức duy nhất: gửi mã ngay (chỉ một lần mỗi login: emailMfa đã có nghĩa là đã gửi).
		if len(methods) == 1 && methods[0] == domain.MFAEmail && login.EmailMfa == nil {
			if err := s.MFA.SendEmailCode(ctx, login, user, project); err != nil {
				return nil, domain.InternalErr("MFA email", err)
			}
		}
		return &LoginResult{Login: login.ID, MfaRequired: true, MfaMethods: methods, Email: user.Email}, nil
	case domain.ResultNewProject:
		return &LoginResult{Login: login.ID}, nil
	case domain.ResultCode:
		code, cookie, err := s.issueAuthCode(ctx, login)
		if err != nil {
			return nil, err
		}
		return &LoginResult{Login: login.ID, Code: code, Cookie: cookie}, nil
	}

	memberships, err := s.MembershipsForLogin(ctx, login)
	if err != nil {
		return nil, err
	}
	// Project lấy MỚI từ kho (display = tên hiện tại); project không đọc được thì bỏ trường project (như Medplum).
	fresh := map[string]*fhir.Reference{}
	for _, m := range memberships {
		pref := refOf(m.Project)
		if _, seen := fresh[pref]; seen {
			continue
		}
		fresh[pref] = nil
		if p, err := s.Projects.Get(ctx, idOf(pref)); err == nil {
			fresh[pref] = newRef("Project", p.ID, p.Name)
		}
	}
	choices := make([]MembershipChoice, 0, len(memberships))
	for _, m := range memberships {
		choices = append(choices, MembershipChoice{ID: m.ID, Project: fresh[refOf(m.Project)], Profile: m.Profile, Identifier: m.Identifier})
	}
	return &LoginResult{Login: login.ID, Memberships: choices}, nil
}

// authCodeTTL là hạn của code (AuthCodeTTL, mặc định 10 phút như AUTH_CODE_EXPIRATION_MS của Medplum).
func (s *Service) authCodeTTL() time.Duration {
	if s.Tokens.AuthCodeTTL > 0 {
		return s.Tokens.AuthCodeTTL
	}
	return 10 * time.Minute
}

// issueAuthCode phát code và cookie đăng nhập MỚI cho login và chỉ lưu BĂM của chúng (D-15), kèm mốc hết hạn tường minh
// (Login.expiresAt, không phụ thuộc meta.lastUpdated nên các lần ghi Login sau đó không kéo dài code). Gọi lại thì code cũ mất hiệu lực.
// Login đã cấp token hoặc đã thu hồi thì không phát (lỗi).
func (s *Service) issueAuthCode(ctx context.Context, login *fhir.Login) (code, cookie string, err error) {
	code, cookie = domain.GenerateSecret(16), domain.GenerateSecret(16)
	expires := s.Clock.Now().Add(s.authCodeTTL()).UTC().Format("2006-01-02T15:04:05.000Z")
	_, err = s.Logins.Mutate(ctx, login.ID, func(l *fhir.Login) error {
		if domain.IsTrue(l.Granted) || domain.IsTrue(l.Revoked) || l.Membership == nil {
			return domain.BadRequest("Login cannot issue a code")
		}
		l.Code, l.Cookie, l.ExpiresAt = domain.HashSecret(code), domain.HashSecret(cookie), expires
		return nil
	})
	return code, cookie, err
}
