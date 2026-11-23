package application

import (
	"context"
	"unicode/utf8"

	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
)

// EmailVerificationRequiredMessage là thông báo khi bắt xác minh email trước khi tạo project.
const EmailVerificationRequiredMessage = "Email verification is required to create a project. Check your email for a verification link."

// NewProjectRequest là yêu cầu POST /auth/newproject (đã qua validator).
type NewProjectRequest struct {
	LoginID     string
	ProjectName string
}

// NewProject xử lý POST /auth/newproject (auth/newproject.ts:34): tạo project cho người dùng vừa đăng ký rồi chọn membership admin của
// project đó cho login. Mọi kiểm tra về trạng thái login chạy TRƯỚC khi tạo project (lệch L3): ở Medplum project được tạo xong mới phát hiện
// login đã bị thu hồi hay đã cấp code, để lại project mồ côi, và người chưa qua MFA vẫn tạo được project bằng id login.
// Login còn thiếu bước MFA thì trả phản hồi MFA (mfaRequired hoặc mfaEnrollRequired) mà không tạo gì.
func (s *Service) NewProject(ctx context.Context, req NewProjectRequest) (*LoginResult, *fhir.Login, error) {
	if s.Registration.Disabled {
		return nil, nil, domain.BadRequest("Registration is disabled")
	}
	if s.Registration.NewProjectDisabled {
		return nil, nil, domain.BadRequest("New project registration is disabled")
	}
	if n := utf8.RuneCountInString(req.ProjectName); n < 4 || n > maxProjectNameLength {
		return nil, nil, domain.BadRequestAt("Project name must be between 4 and 72 characters", "projectName")
	}
	login, err := s.Logins.Get(ctx, req.LoginID)
	if err != nil {
		return nil, nil, err
	}
	switch {
	case login.Membership != nil:
		return nil, nil, domain.BadRequest("Login already has a membership")
	case refOf(login.Project) != "" && refOf(login.Project) != "Project/new":
		return nil, nil, domain.BadRequest("Invalid project")
	case domain.IsTrue(login.Revoked):
		return nil, nil, domain.BadRequest("Login revoked")
	case domain.IsTrue(login.Granted):
		return nil, nil, domain.BadRequest("Login granted")
	case login.User == nil:
		return nil, nil, domain.BadRequest("User reference is missing")
	}
	user, err := s.Users.Get(ctx, idOf(refOf(login.User)))
	if err != nil {
		return nil, nil, err
	}
	switch domain.DecideLoginResult(user, nil, login) {
	case domain.ResultMFAEnroll, domain.ResultMFARequired:
		res, err := s.BuildLoginResult(ctx, login)
		return res, login, err
	}
	if s.Registration.RequireVerifiedEmailForProject && !domain.IsTrue(user.EmailVerified) {
		if err := s.SendVerificationEmail(ctx, user, login.ID); err != nil {
			s.Logger.ErrorContext(ctx, "không gửi được thư xác minh email", "user_id", user.ID, "error", err)
		}
		return nil, nil, domain.BadRequest(EmailVerificationRequiredMessage)
	}
	owned, err := s.Projects.HasOwnedBy(ctx, "User/"+user.ID)
	if err != nil {
		return nil, nil, err
	}

	var created *ProjectInitResult
	if err := s.Tx.InTx(ctx, func(tx TxRepos) error {
		var err error
		created, err = s.CreateProject(ctx, tx.Resources, ProjectInit{Name: req.ProjectName, Admin: user, Features: s.Registration.DefaultProjectFeatures})
		return err
	}); err != nil {
		return nil, nil, err
	}
	updated, err := s.SetLoginMembership(ctx, login, created.Membership)
	if err != nil {
		return nil, nil, err
	}
	if !owned {
		s.sendWelcomeEmail(ctx, user, created.Project)
	}
	res, err := s.BuildLoginResult(ctx, updated)
	return res, updated, err
}

// sendWelcomeEmail gửi thư chào chủ project lần đầu; lỗi chỉ ghi log (không làm hỏng việc tạo project).
func (s *Service) sendWelcomeEmail(ctx context.Context, user *fhir.User, project *fhir.Project) {
	if user.Email == "" || !s.mailReady() {
		return
	}
	if err := s.Mailer.Send(ctx, welcomeMessage(s.Mail, user, project.Name)); err != nil {
		s.Logger.WarnContext(ctx, "Failed to send welcome email", "project_id", project.ID, "error", err)
	}
}
