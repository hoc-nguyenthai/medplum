package httpapi

import (
	"net/http"
	"unicode/utf8"

	"github.com/labstack/echo/v4"

	"trivita-fhir/apps/auth/internal/application"
	"trivita-fhir/apps/auth/internal/domain"
)

// NewUserBody là body của POST /auth/newuser.
type NewUserBody struct {
	FirstName           string `json:"firstName" binding:"required" example:"An"`                                     // Tên; bắt buộc, 1 đến 128 ký tự
	LastName            string `json:"lastName" binding:"required" example:"Nguyen"`                                  // Họ; bắt buộc, 1 đến 128 ký tự
	Email               string `json:"email" binding:"required" example:"an@example.org"`                             // Email; bắt buộc, hợp lệ, dài 3 đến 72 ký tự
	Password            string `json:"password" binding:"required" example:"mat-khau-dai-123"`                        // Mật khẩu; bắt buộc, 8 ký tự trở lên và không quá 72 byte
	ProjectID           string `json:"projectId,omitempty" example:"new"`                                             // "new" (sẽ tạo project mới), id project cho đăng ký mở, hoặc bỏ trống (user không thuộc project nào)
	ClientID            string `json:"clientId,omitempty" example:"a3b1c2d4-0000-4000-8000-000000000001"`             // Id ClientApplication; chỉ gửi clientId thì user vẫn không thuộc project nào
	Scope               string `json:"scope,omitempty" example:"openid"`                                              // Scope OAuth ghi vào login vừa tạo
	Nonce               string `json:"nonce,omitempty" example:"c1a9f0e2"`                                            // Nonce OIDC ghi vào login vừa tạo
	CodeChallenge       string `json:"codeChallenge,omitempty" example:"E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"` // Mã thách thức PKCE ghi vào login vừa tạo
	CodeChallengeMethod string `json:"codeChallengeMethod,omitempty" enums:"plain,S256" example:"S256"`               // Phương thức PKCE: plain hoặc S256
	RedirectURI         string `json:"redirectUri,omitempty" example:"https://app.example.org/callback"`              // URI chuyển hướng của client ghi vào login vừa tạo
	Remember            bool   `json:"remember,omitempty" example:"false"`                                            // true để kèm refresh token cho login vừa tạo; mặc định false
	RecaptchaToken      string `json:"recaptchaToken,omitempty" example:"03AGdBq25..."`                               // Token reCAPTCHA; chỉ cần khi máy chủ hoặc project bật reCAPTCHA
	RecaptchaSiteKey    string `json:"recaptchaSiteKey,omitempty" example:"6LdExampleSiteKey"`                        // Site key reCAPTCHA tương ứng với token; chỉ cần khi bật reCAPTCHA
}

// NewUserResponse là phản hồi của POST /auth/newuser: login chưa có membership, KHÔNG có code.
type NewUserResponse struct {
	Login                     string `json:"login" binding:"required" example:"a3b1c2d4-0000-4000-8000-000000000000"` // Id login vừa tạo (chưa có membership), dùng cho /auth/newproject hoặc /auth/newpatient
	EmailVerificationRequired bool   `json:"emailVerificationRequired" binding:"required" example:"false"`            // true khi máy chủ yêu cầu email đã xác minh để tạo project (cấu hình RequireVerifiedEmailForProject) và projectId của yêu cầu là "new"; ngược lại false
}

func validateNameField(b body, key, msg string, v *validationError) {
	s, ok := b[key].(string)
	if n := utf8.RuneCountInString(s); !ok || n < 1 || n > 128 {
		v.add(msg, key)
	}
}

func validateNewUser(b body) error {
	v := &validationError{}
	validateNameField(b, "firstName", "First name must be between 1 and 128 characters", v)
	validateNameField(b, "lastName", "Last name must be between 1 and 128 characters", v)
	if e, ok := b["email"].(string); !ok || len(e) < 3 || len(e) > 72 || !domain.IsEmail(e) {
		v.add("Valid email address between 3 and 72 characters is required", "email")
	}
	if err := domain.ValidatePassword(b.str("password")); err != nil {
		v.add(err.Error(), "password")
	}
	return v.orNil()
}

// newUser godoc
//
//	@Summary		Đăng ký tài khoản
//	@Description	Tạo user rồi tạo Login CHƯA có membership; phản hồi không có code (người dùng phải tạo hoặc chọn project/profile tiếp). reCAPTCHA kiểm trước cờ đăng ký, như Medplum. projectId là "new" (tạo project mới), id của project cho phép đăng ký mở, hoặc bỏ trống (user không thuộc project nào); chỉ gửi clientId thì user vẫn không thuộc project nào.
//	@Tags			auth
//	@Accept			json
//	@Produce		application/json,application/fhir+json
//	@Param			body	body		NewUserBody	true	"Thông tin đăng ký"
//	@Success		200		{object}	NewUserResponse
//	@Failure		400		{object}	fhir.OperationOutcome
//	@Failure		404		{object}	fhir.OperationOutcome
//	@Failure		429		{object}	fhir.OperationOutcome
//	@Router			/auth/newuser [post]
func (h *authHandlers) newUser(c echo.Context) error {
	b, err := readBody(c)
	if err != nil {
		return writeError(c, err)
	}
	if err := validateNewUser(b); err != nil {
		return writeError(c, err)
	}
	ctx := c.Request().Context()
	if err := h.svc.ValidateRecaptcha(ctx, b.str("recaptchaSiteKey"), b.str("projectId"), b.str("recaptchaToken"), true); err != nil {
		return writeError(c, err)
	}
	res, err := h.svc.NewUser(ctx, application.NewUserRequest{
		FirstName: b.str("firstName"), LastName: b.str("lastName"), Email: b.str("email"), Password: b.str("password"),
		ProjectID: b.str("projectId"), ClientID: b.str("clientId"), Scope: b.str("scope"), Nonce: b.str("nonce"),
		CodeChallenge: b.str("codeChallenge"), CodeChallengeMethod: b.str("codeChallengeMethod"), RedirectURI: b.str("redirectUri"),
		Remember: b.boolean("remember"), RemoteAddress: c.RealIP(), UserAgent: c.Request().UserAgent(), Origin: c.Request().Header.Get("Origin"),
	})
	if err != nil {
		return writeError(c, err)
	}
	return c.JSON(http.StatusOK, NewUserResponse{Login: res.LoginID, EmailVerificationRequired: res.EmailVerificationRequired})
}

// NewProjectBody là body của POST /auth/newproject.
type NewProjectBody struct {
	Login       string `json:"login" binding:"required" example:"a3b1c2d4-0000-4000-8000-000000000000"` // Id login từ /auth/newuser; bắt buộc, phải là UUID
	ProjectName string `json:"projectName" binding:"required" example:"Bệnh viện A"`                    // Tên project; bắt buộc, 4 đến 72 ký tự
}

func validateNewProject(b body) error {
	v := &validationError{}
	if id, ok := b["login"].(string); !ok || !domain.IsUUID(id) {
		v.add("Login ID is required.", "login")
	}
	if n, ok := b["projectName"].(string); !ok || utf8.RuneCountInString(n) < 4 || utf8.RuneCountInString(n) > 72 {
		v.add("Project name must be between 4 and 72 characters", "projectName")
	}
	return v.orNil()
}

// newProject godoc
//
//	@Summary		Tạo project mới cho người vừa đăng ký
//	@Description	Dùng login từ /auth/newuser. Tạo project (6 chính sách mặc định, client mặc định, hồ sơ Practitioner và membership admin) trong một transaction rồi trả code như /auth/login. Mọi kiểm tra trạng thái login (đã thu hồi, đã cấp code, đã có membership) chạy trước khi tạo project. Login còn thiếu bước MFA nhận phản hồi mfaRequired hoặc mfaEnrollRequired và chưa tạo gì.
//	@Tags			auth
//	@Accept			json
//	@Produce		application/json,application/fhir+json
//	@Param			body	body		NewProjectBody	true	"login và tên project"
//	@Success		200		{object}	application.LoginResult
//	@Failure		400		{object}	fhir.OperationOutcome
//	@Failure		404		{object}	fhir.OperationOutcome
//	@Failure		429		{object}	fhir.OperationOutcome
//	@Router			/auth/newproject [post]
func (h *authHandlers) newProject(c echo.Context) error {
	b, err := readBody(c)
	if err != nil {
		return writeError(c, err)
	}
	if err := validateNewProject(b); err != nil {
		return writeError(c, err)
	}
	res, login, err := h.svc.NewProject(c.Request().Context(), application.NewProjectRequest{LoginID: b.str("login"), ProjectName: b.str("projectName")})
	if err != nil {
		return writeError(c, err)
	}
	h.sendResult(c, res, login)
	return c.JSON(http.StatusOK, res)
}

// NewPatientBody là body của POST /auth/newpatient.
type NewPatientBody struct {
	Login     string `json:"login" binding:"required" example:"a3b1c2d4-0000-4000-8000-000000000000"`     // Id login từ /auth/newuser; bắt buộc, không rỗng
	ProjectID string `json:"projectId" binding:"required" example:"a3b1c2d4-0000-4000-8000-000000000001"` // Id project cho phép bệnh nhân tự đăng ký; bắt buộc, không rỗng
}

// newPatient godoc
//
//	@Summary		Bệnh nhân tự đăng ký vào project
//	@Description	Dùng login từ /auth/newuser của một user thuộc project (projectId trong body của newuser). Project phải cho đăng ký mở (setting openPatientRegistration và có chính sách Patient). Tạo hồ sơ Patient và membership kèm chính sách đó, rồi trả code như /auth/login. Không có hạn mức riêng ngoài hạn mức chung của /auth/*.
//	@Tags			auth
//	@Accept			json
//	@Produce		application/json,application/fhir+json
//	@Param			body	body		NewPatientBody	true	"login và projectId"
//	@Success		200		{object}	application.LoginResult
//	@Failure		400		{object}	fhir.OperationOutcome
//	@Failure		404		{object}	fhir.OperationOutcome
//	@Router			/auth/newpatient [post]
func (h *authHandlers) newPatient(c echo.Context) error {
	b, err := readBody(c)
	if err != nil {
		return writeError(c, err)
	}
	v := &validationError{}
	if s, ok := b["login"].(string); !ok || s == "" {
		v.add("Missing login", "login")
	}
	if s, ok := b["projectId"].(string); !ok || s == "" {
		v.add("Project ID is required", "projectId")
	}
	if err := v.orNil(); err != nil {
		return writeError(c, err)
	}
	res, login, err := h.svc.NewPatient(c.Request().Context(), application.NewPatientRequest{LoginID: b.str("login"), ProjectID: b.str("projectId")})
	if err != nil {
		return writeError(c, err)
	}
	h.sendResult(c, res, login)
	return c.JSON(http.StatusOK, res)
}
