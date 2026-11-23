package httpapi

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"trivita-fhir/apps/auth/internal/domain"
)

// registerCredentials đăng ký các route mật khẩu và xác minh email (P4.3). setpassword, verifyemail và resetpassword chỉ bị hạn mức
// chung của /auth/* như Medplum (mục 0.3 của kế hoạch P4); resetpassword không bao giờ báo email có tồn tại hay không.
func registerCredentials(g *echo.Group, h *authHandlers, bearer echo.MiddlewareFunc) {
	g.POST("/resetpassword", h.resetPassword)
	g.POST("/setpassword", h.setPassword)
	g.POST("/verifyemail", h.verifyEmail)
	g.POST("/changepassword", h.changePassword, bearer)
}

// ResetPasswordBody là body của POST /auth/resetpassword.
type ResetPasswordBody struct {
	Email            string `json:"email" binding:"required" example:"an@example.org"`                   // Email cần đặt lại mật khẩu; bắt buộc, phải hợp lệ
	ProjectID        string `json:"projectId,omitempty" example:"a3b1c2d4-0000-4000-8000-000000000000"`  // Id project của user (UUID); bỏ trống nếu user không thuộc project nào
	RedirectURI      string `json:"redirectUri,omitempty" example:"https://app.example.org/setpassword"` // URI app đính vào link trong thư đặt lại mật khẩu
	SendEmail        *bool  `json:"sendEmail,omitempty" example:"true"`                                  // false để không gửi thư (chỉ tạo yêu cầu); mặc định true
	RecaptchaToken   string `json:"recaptchaToken,omitempty" example:"03AGdBq25..."`                     // Token reCAPTCHA; chỉ cần khi máy chủ hoặc project bật reCAPTCHA
	RecaptchaSiteKey string `json:"recaptchaSiteKey,omitempty" example:"6LdExampleSiteKey"`              // Site key reCAPTCHA tương ứng; chỉ cần khi bật reCAPTCHA
}

func validateEmailField(b body, v *validationError) {
	if e, ok := b["email"].(string); !ok || !domain.IsEmail(e) {
		v.add("Valid email address is required", "email")
	}
}

// resetPassword godoc
//
//	@Summary		Yêu cầu thư đặt lại mật khẩu
//	@Description	Luôn trả 200 khi email hợp lệ, dù có tài khoản hay không và dù gửi thư có được hay không (không lộ tài khoản). Email thuộc tên miền đăng nhập ngoài trả 400.
//	@Tags			auth
//	@Accept			json
//	@Produce		application/fhir+json
//	@Param			body	body		ResetPasswordBody	true	"Email cần đặt lại"
//	@Success		200		{object}	fhir.OperationOutcome
//	@Failure		400		{object}	fhir.OperationOutcome
//	@Router			/auth/resetpassword [post]
func (h *authHandlers) resetPassword(c echo.Context) error {
	b, err := readBody(c)
	if err != nil {
		return writeError(c, err)
	}
	v := &validationError{}
	validateEmailField(b, v)
	if !b.falsy("projectId") {
		if p, ok := b["projectId"].(string); !ok || !domain.IsUUID(p) {
			v.add("Invalid projectId", "projectId")
		}
	}
	if err := v.orNil(); err != nil {
		return writeError(c, err)
	}
	if err := h.svc.ValidateRecaptcha(c.Request().Context(), b.str("recaptchaSiteKey"), b.str("projectId"), b.str("recaptchaToken"), false); err != nil {
		return writeError(c, err)
	}
	send := true
	if b.has("sendEmail") {
		send = b["sendEmail"] != false
	}
	if err := h.svc.ResetPassword(c.Request().Context(), b.str("email"), b.str("projectId"), b.str("redirectUri"), send); err != nil {
		return writeError(c, err)
	}
	return writeOutcome(c, http.StatusOK, allOK())
}

// SetPasswordBody là body của POST /auth/setpassword.
type SetPasswordBody struct {
	ID       string `json:"id" binding:"required" example:"a3b1c2d4-0000-4000-8000-000000000000"` // Id yêu cầu (PasswordChangeRequest) lấy từ link trong thư; bắt buộc, phải là UUID
	Secret   string `json:"secret" binding:"required" example:"s3cr3t-from-email-link"`           // Secret trong link thư; bắt buộc, không rỗng, dùng một lần
	Password string `json:"password" binding:"required" example:"mat-khau-moi-123"`               // Mật khẩu mới; bắt buộc, 8 ký tự trở lên và không quá 72 byte
}

func validateRequestRef(b body, v *validationError) {
	if id, ok := b["id"].(string); !ok || !domain.IsUUID(id) {
		v.add("Invalid request ID", "id")
	}
	if s, ok := b["secret"].(string); !ok || s == "" {
		v.add("Missing secret", "secret")
	}
}

// setPassword godoc
//
//	@Summary		Đặt mật khẩu bằng link trong thư
//	@Description	Thu hồi mọi phiên đăng nhập của user. Link dùng được một lần.
//	@Tags			auth
//	@Accept			json
//	@Produce		application/fhir+json
//	@Param			body	body		SetPasswordBody	true	"id và secret của link, mật khẩu mới"
//	@Success		200		{object}	fhir.OperationOutcome
//	@Failure		400		{object}	fhir.OperationOutcome
//	@Failure		404		{object}	fhir.OperationOutcome
//	@Router			/auth/setpassword [post]
func (h *authHandlers) setPassword(c echo.Context) error {
	b, err := readBody(c)
	if err != nil {
		return writeError(c, err)
	}
	v := &validationError{}
	validateRequestRef(b, v)
	if err := domain.ValidatePassword(b.str("password")); err != nil {
		v.add(err.Error(), "password")
	}
	if err := v.orNil(); err != nil {
		return writeError(c, err)
	}
	if err := h.svc.SetPasswordWithRequest(c.Request().Context(), b.str("id"), b.str("secret"), b.str("password")); err != nil {
		return writeError(c, err)
	}
	return writeOutcome(c, http.StatusOK, allOK())
}

// VerifyEmailBody là body của POST /auth/verifyemail.
type VerifyEmailBody struct {
	ID     string `json:"id" binding:"required" example:"a3b1c2d4-0000-4000-8000-000000000000"` // Id yêu cầu xác minh lấy từ link trong thư; bắt buộc, phải là UUID
	Secret string `json:"secret" binding:"required" example:"s3cr3t-from-email-link"`           // Secret trong link thư; bắt buộc, không rỗng
}

// verifyEmail godoc
//
//	@Summary		Xác minh email bằng link trong thư
//	@Description	Có redirectUri trong request thì trả 200 kèm header Location (không phải 302), như Medplum.
//	@Tags			auth
//	@Accept			json
//	@Produce		application/fhir+json
//	@Param			body	body		VerifyEmailBody	true	"id và secret của link"
//	@Success		200		{object}	fhir.OperationOutcome
//	@Failure		400		{object}	fhir.OperationOutcome
//	@Failure		404		{object}	fhir.OperationOutcome
//	@Router			/auth/verifyemail [post]
func (h *authHandlers) verifyEmail(c echo.Context) error {
	b, err := readBody(c)
	if err != nil {
		return writeError(c, err)
	}
	v := &validationError{}
	validateRequestRef(b, v)
	if err := v.orNil(); err != nil {
		return writeError(c, err)
	}
	redirect, err := h.svc.VerifyEmail(c.Request().Context(), b.str("id"), b.str("secret"))
	if err != nil {
		return writeError(c, err)
	}
	if redirect != "" {
		c.Response().Header().Set(echo.HeaderLocation, redirect)
	}
	return writeOutcome(c, http.StatusOK, allOK())
}

// ChangePasswordBody là body của POST /auth/changepassword.
type ChangePasswordBody struct {
	OldPassword string `json:"oldPassword" binding:"required" example:"mat-khau-cu-123"`  // Mật khẩu hiện tại; bắt buộc, không rỗng
	NewPassword string `json:"newPassword" binding:"required" example:"mat-khau-moi-123"` // Mật khẩu mới; bắt buộc, 8 ký tự trở lên và không quá 72 byte
}

// changePassword godoc
//
//	@Summary		Đổi mật khẩu của chính mình
//	@Description	Thu hồi MỌI phiên đăng nhập của user, kể cả phiên đang dùng.
//	@Tags			auth
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		application/fhir+json
//	@Param			body	body		ChangePasswordBody	true	"Mật khẩu cũ và mới"
//	@Success		200		{object}	fhir.OperationOutcome
//	@Failure		400		{object}	fhir.OperationOutcome
//	@Failure		401		{object}	fhir.OperationOutcome
//	@Failure		403		{object}	fhir.OperationOutcome
//	@Router			/auth/changepassword [post]
func (h *authHandlers) changePassword(c echo.Context) error {
	b, err := readBody(c)
	if err != nil {
		return writeError(c, err)
	}
	v := &validationError{}
	if b.str("oldPassword") == "" {
		v.add("Missing oldPassword", "oldPassword")
	}
	if err := domain.ValidatePassword(b.str("newPassword")); err != nil {
		v.add(err.Error(), "newPassword")
	}
	if err := v.orNil(); err != nil {
		return writeError(c, err)
	}
	if err := h.svc.ChangePassword(c.Request().Context(), authState(c), b.str("oldPassword"), b.str("newPassword")); err != nil {
		return writeError(c, err)
	}
	return writeOutcome(c, http.StatusOK, allOK())
}
