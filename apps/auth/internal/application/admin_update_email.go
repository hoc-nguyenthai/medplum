package application

import (
	"context"
	"errors"
	"strings"

	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
)

func emailChangedMessage(c MailConfig, to string) Message {
	n := c.appName()
	return Message{To: to, Subject: n + " email address changed", Text: "The email address of your " + n + " account was changed by an administrator, " +
		"so this address no longer signs in to it.\n\nIf you did not expect this, contact your administrator.\n\nThank you,\n" + n + "\n"}
}

// AdminUpdateMemberEmail đổi email đăng nhập của user thuộc membership (POST .../members/:mid/update-email, tương ứng User/$update-email của Medplum). Chỉ user thuộc ĐÚNG
// project của người gọi (user ở phạm vi server hay project khác là 403 chung). Email mới được hạ chữ thường, đúng định dạng, khác email hiện tại, không trùng trong project
// (400), Practitioner chịu danh sách chặn và allowedPractitionerEmailDomain như khi mời. Một transaction giữ khóa theo (project, email) như lúc tạo user: ghi email,
// emailVerified=false, vô hiệu mọi security request (link đặt lại mật khẩu gửi tới địa chỉ cũ), thu hồi mọi login. Sau khi ghi, thư báo gửi tới địa chỉ CŨ (nếu đã cấu hình
// thư; lỗi gửi chỉ ghi log). Không đổi telecom của hồ sơ Practitioner/Patient (thuộc fhir-server). Log kiểm toán không chứa email.
func (s *Service) AdminUpdateMemberEmail(ctx context.Context, a *AuthState, projectID, id, email string) error {
	m, u, err := s.adminMemberUser(ctx, a, projectID, id)
	if err != nil {
		return err
	}
	email = strings.ToLower(strings.TrimSpace(email))
	if !domain.IsEmail(email) {
		return domain.BadRequestAt("Valid email address is required", "email")
	}
	old := strings.ToLower(u.Email)
	if email == old {
		return domain.BadRequestAt("New email is the same as the current email", "email")
	}
	if strings.HasPrefix(refOf(m.Profile), "Practitioner/") {
		project, err := s.Projects.Get(ctx, a.Project.ID)
		if err != nil {
			return err
		}
		if err := s.checkPractitionerDomain(project, email); err != nil {
			return err
		}
	}
	pid := a.Project.ID
	err = s.Tx.InTx(ctx, func(tx TxRepos) error {
		if err := tx.Locker.Lock(ctx, "user-email:"+pid+":"+email); err != nil {
			return err
		}
		clash, err := tx.Users.FindInProject(ctx, email, pid)
		if err != nil {
			return err
		}
		if clash != nil && clash.ID != u.ID {
			return domain.BadRequestAt("Email already registered", "email")
		}
		if _, err := tx.Users.Mutate(ctx, u.ID, func(x *fhir.User) error {
			x.Email, x.EmailVerified = email, ptrBool(false)
			return nil
		}); err != nil {
			return err
		}
		if err := s.retireSecurityRequests(ctx, tx.SecurityRequests, u, func(*fhir.UserSecurityRequest) bool { return true }); err != nil {
			return err
		}
		return revokeUserLoginsWith(ctx, tx.Logins, u)
	})
	if err != nil {
		if errors.Is(err, domain.ErrVersionConflict) {
			return domain.Conflict("Resource was modified concurrently, retry")
		}
		return err
	}
	s.Logger.WarnContext(ctx, "đổi email của user", "audit", true, "actor", refOf(a.Membership.User), "project_id", pid, "membership_id", m.ID, "user_id", u.ID)
	if old != "" && s.mailReady() {
		if err := s.Mailer.Send(ctx, emailChangedMessage(s.Mail, old)); err != nil {
			s.Logger.ErrorContext(ctx, "không gửi được thư báo đổi email", "user_id", u.ID, "error", err)
		}
	}
	return nil
}
