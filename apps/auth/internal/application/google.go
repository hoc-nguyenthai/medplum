package application

import (
	"context"

	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
)

// GoogleLoginRequest là yêu cầu POST /auth/google (đã qua validator).
type GoogleLoginRequest struct {
	GoogleClientID, Credential string
	ClientID, ProjectID        string
	ResourceType               string
	Scope, Nonce, RedirectURI  string
	CodeChallenge              string
	CodeChallengeMethod        string
	// CreateUser cho phép tạo user mới từ credential (đăng ký bằng Google).
	CreateUser               bool
	RemoteAddress, UserAgent string
	Origin                   string
}

// GoogleResult là kết quả: hoặc tên miền của email thuộc IdP ngoài (External, kèm CookieNonce của state ký), hoặc một Login để dựng LoginResult.
type GoogleResult struct {
	External    *MethodResult
	CookieNonce string
	Login       *fhir.Login
}

// GoogleLogin xử lý POST /auth/google (auth/google.ts:50). Khác Medplum (đã duyệt): credential phải báo email ĐÃ xác minh (Q2; Medplum không kiểm trước khi cho đăng nhập),
// credential thiếu email là 400 (Medplum lỗi 500), thông báo lỗi xác minh cố định (không lộ thông báo của thư viện JWT), và tạo user qua đường có khóa chống trùng.
func (s *Service) GoogleLogin(ctx context.Context, req GoogleLoginRequest) (*GoogleResult, error) {
	if s.Google == nil {
		return nil, domain.Internal("đăng nhập Google chưa được lắp ghép")
	}
	projectID := req.ProjectID
	if projectID != "" && projectID != "new" && !domain.IsUUID(projectID) {
		projectID = "" // validateProjectId của Medplum: giá trị lạ bị bỏ qua âm thầm
	}
	var err error
	if projectID, err = s.ResolveProjectID(ctx, req.ClientID, projectID); err != nil {
		return nil, err
	}
	if req.GoogleClientID != s.GoogleClientID {
		projects, err := s.Projects.ByGoogleClientID(ctx, req.GoogleClientID, projectID)
		if err != nil {
			return nil, err
		}
		switch len(projects) {
		case 0:
			return nil, domain.BadRequest("Invalid googleClientId")
		case 1:
			projectID = projects[0].ID
		default:
			s.Logger.WarnContext(ctx, "nhiều project dùng chung googleClientId", "count", len(projects))
		}
	}
	claims, err := s.Google.Verify(ctx, req.Credential, req.GoogleClientID)
	if err != nil {
		s.Logger.WarnContext(ctx, "credential Google không hợp lệ", "error", err)
		return nil, domain.BadRequest("Failed to verify Google credential")
	}
	rawEmail, _ := claims["email"].(string)
	if rawEmail == "" {
		return nil, domain.BadRequest("Google credential does not contain email address")
	}
	if verified, _ := claims["email_verified"].(bool); !verified {
		return nil, domain.BadRequest("Email address is not verified by Google")
	}
	email := lower(rawEmail)

	// tên miền của email thuộc IdP ngoài: trả authorizeUrl (kèm state ký) và dừng
	if ext, nonce, err := s.StartExternalLogin(ctx, email, domain.ExternalState{ClientID: req.ClientID, ProjectID: projectID, Scope: req.Scope, Nonce: req.Nonce,
		CodeChallenge: req.CodeChallenge, CodeChallengeMethod: req.CodeChallengeMethod, RedirectURI: req.RedirectURI}); err != nil {
		return nil, err
	} else if ext != nil {
		return &GoogleResult{External: ext, CookieNonce: nonce}, nil
	}

	user, err := s.FindUserByEmail(ctx, email, projectID)
	if err != nil {
		return nil, err
	}
	created := false
	if user == nil {
		if !req.CreateUser {
			return nil, domain.BadRequest("User not found")
		}
		// đăng ký bị tắt chỉ chặn đăng ký vào project mới/không project; vào project cụ thể thì vẫn tạo (như Medplum)
		if s.Registration.Disabled && (projectID == "" || projectID == "new") {
			return nil, domain.BadRequest("Registration is disabled")
		}
		first, _ := claims["given_name"].(string)
		last, _ := claims["family_name"].(string)
		u := &fhir.User{ResourceType: "User", FirstName: first, LastName: last, Email: email, EmailVerified: ptrTrue()}
		if projectID != "" && projectID != "new" {
			u.Project, u.Meta = &fhir.Reference{Reference: "Project/" + projectID}, &fhir.Meta{Project: projectID}
		}
		if user, err = s.createUserLocked(ctx, u, projectID); err != nil {
			return nil, err
		}
		created = true
	} else if !domain.IsTrue(user.EmailVerified) {
		// credential báo email đã xác minh: chỉ nâng lên, không bao giờ hạ
		if _, err := s.Users.Mutate(ctx, user.ID, func(x *fhir.User) error {
			if domain.IsTrue(x.EmailVerified) {
				return ErrNoChange
			}
			x.EmailVerified = ptrTrue()
			return nil
		}); err != nil {
			return nil, err
		}
	}
	scope, nonce := req.Scope, req.Nonce
	if scope == "" {
		scope = "openid offline_access"
	}
	if nonce == "" {
		nonce = domain.NewID()
	}
	picture, _ := claims["picture"].(string)
	login, err := s.TryLogin(ctx, domain.LoginRequest{
		AuthMethod: domain.AuthGoogle, Email: email, HasGoogleCredentials: true, ProjectID: projectID, ClientID: req.ClientID, ResourceType: req.ResourceType,
		Scope: scope, Nonce: nonce, CodeChallenge: req.CodeChallenge, CodeChallengeMethod: req.CodeChallengeMethod, RedirectURI: req.RedirectURI, PictureURL: picture,
		AllowNoMembership: created || projectID == "new", RemoteAddress: req.RemoteAddress, UserAgent: req.UserAgent, Origin: req.Origin,
	})
	if err != nil {
		return nil, err
	}
	return &GoogleResult{Login: login}, nil
}
