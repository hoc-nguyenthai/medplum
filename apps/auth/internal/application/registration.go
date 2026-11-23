package application

import (
	"context"
	"strings"

	"trivita-fhir/apps/auth/internal/domain"
)

// RegistrationConfig là các cờ đăng ký. Dạng "Disabled" để zero value giữ hành vi mặc định của Medplum (đăng ký bật).
type RegistrationConfig struct {
	// Disabled=true chặn newuser và newproject ("Registration is disabled").
	Disabled bool
	// NewProjectDisabled=true cho đăng ký user nhưng chặn tạo project mới.
	NewProjectDisabled bool
	// RequireVerifiedEmailForProject bắt xác minh email trước khi tạo project: newuser với projectId "new" gửi thư xác minh.
	RequireVerifiedEmailForProject bool
	// DefaultProjectFeatures gán cho Project.features của project mới.
	DefaultProjectFeatures []string
}

// NewUserRequest là yêu cầu POST /auth/newuser (đã qua validator và reCAPTCHA).
type NewUserRequest struct {
	FirstName, LastName, Email, Password string
	// ProjectID là giá trị thô trong body (rỗng, "new" hoặc id project): quyết định phạm vi kiểm tra trùng email và project của User.
	ProjectID string
	ClientID  string

	Scope, Nonce, CodeChallenge, CodeChallengeMethod, RedirectURI string
	Remember                                                      bool
	RemoteAddress, UserAgent, Origin                              string
}

// NewUserResult là kết quả đăng ký: login chưa có membership, KHÔNG có code (người dùng phải chọn hoặc tạo project/profile tiếp).
type NewUserResult struct {
	LoginID                   string
	EmailVerificationRequired bool
}

// NewUser xử lý POST /auth/newuser (auth/newuser.ts:41): đăng ký user rồi tạo Login chưa có membership.
func (s *Service) NewUser(ctx context.Context, req NewUserRequest) (*NewUserResult, error) {
	if s.Registration.Disabled {
		return nil, domain.BadRequest("Registration is disabled")
	}
	projectID := req.ProjectID
	if req.ClientID != "" {
		client, err := s.GetClientApplication(ctx, req.ClientID)
		if err != nil {
			return nil, err
		}
		clientProject := ""
		if client.Meta != nil {
			clientProject = client.Meta.Project
		}
		if projectID != "" && clientProject != projectID {
			return nil, domain.BadRequest("Client and project do not match")
		}
		if projectID == "" {
			projectID = clientProject
		}
	}
	email := strings.ToLower(req.Email)
	scope, nonce := req.Scope, req.Nonce
	if scope == "" {
		scope = "openid"
	}
	if nonce == "" {
		nonce = domain.NewID()
	}
	login := domain.LoginRequest{
		AuthMethod: domain.AuthPassword, Email: email, Password: req.Password, ProjectID: projectID, ClientID: req.ClientID,
		Scope: scope, Nonce: nonce, CodeChallenge: req.CodeChallenge, CodeChallengeMethod: req.CodeChallengeMethod, RedirectURI: req.RedirectURI,
		Remember: req.Remember, RemoteAddress: req.RemoteAddress, UserAgent: req.UserAgent, Origin: req.Origin, AllowNoMembership: true,
	}
	// kiểm yêu cầu đăng nhập TRƯỚC khi tạo user (lệch L1): PKCE hay redirect sai không được để lại user mồ côi
	if _, err := s.checkLoginRequest(ctx, login); err != nil {
		return nil, err
	}
	if projectID != "" && projectID != "new" {
		project, err := s.Projects.Get(ctx, projectID)
		if err != nil {
			return nil, err
		}
		if err := domain.ProjectRegistrationAllowed(project); err != nil {
			return nil, err
		}
	}
	// User chỉ gắn project khi body nêu projectId (không phải khi suy ra từ client): như Medplum (newuser.ts:127,140)
	user, err := s.CreateUser(ctx, NewUser{FirstName: req.FirstName, LastName: req.LastName, Email: email, Password: req.Password, ProjectID: req.ProjectID})
	if err != nil {
		return nil, err
	}
	l, err := s.TryLogin(ctx, login)
	if err != nil {
		return nil, err
	}
	res := &NewUserResult{LoginID: l.ID}
	if s.Registration.RequireVerifiedEmailForProject && projectID == "new" {
		res.EmailVerificationRequired = true
		if err := s.SendVerificationEmail(ctx, user, l.ID); err != nil {
			// lệch L5: user và login đã tạo; lỗi gửi thư không làm hỏng đăng ký (người dùng xin gửi lại ở newproject)
			s.Logger.ErrorContext(ctx, "không gửi được thư xác minh email", "user_id", user.ID, "error", err)
		}
	}
	return res, nil
}
