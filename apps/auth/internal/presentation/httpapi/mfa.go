package httpapi

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"trivita-fhir/apps/auth/internal/application"
)

// registerMFA đăng ký các route MFA (P5). verify, login-enroll và send-email nhập bằng id login (công khai) và dùng chung hạn mức IP của bước nhập thông tin
// đăng nhập; ngoài ra mỗi lượt thử còn bị giới hạn theo login và theo user (xem application.MFAConfig).
func registerMFA(g *echo.Group, h *authHandlers, bearer echo.MiddlewareFunc, slow []echo.MiddlewareFunc) {
	g.POST("/mfa/verify", h.mfaVerify, slow...)
	g.POST("/mfa/login-enroll", h.mfaLoginEnroll, slow...)
	g.POST("/mfa/send-email", h.mfaSendEmail, slow...)
	g.GET("/mfa/status", h.mfaStatus, bearer)
	g.POST("/mfa/enroll", h.mfaEnroll, bearer)
	g.POST("/mfa/send-email-challenge", h.mfaSendEmailChallenge, bearer)
	g.POST("/mfa/disable", h.mfaDisable, bearer)
}

// MFAVerifyBody là body của POST /auth/mfa/verify.
type MFAVerifyBody struct {
	Login string `json:"login" binding:"required" example:"a3b1c2d4-0000-4000-8000-000000000000"` // id login đang chờ MFA (lấy từ phản hồi /auth/login); bắt buộc, rỗng là 400
	Token string `json:"token" binding:"required" example:"123456"`                               // mã TOTP (6 số) hoặc mã email MFA; bắt buộc, rỗng là 400
}

// mfaVerify godoc
//
//	@Summary		Xác minh mã MFA khi đăng nhập
//	@Description	Mã TOTP hoặc mã email. Đúng thì login được đánh dấu đã xác minh và phản hồi như /auth/login (code, memberships...). Người dùng bắt buộc MFA chưa đăng ký mà nhập mã cho secret được mời của chính login này thì được đăng ký TOTP luôn. Mỗi lượt thử bị giới hạn: 5 mỗi login và 20 mỗi user trong 10 phút; lần sai thứ 5 thu hồi login; vượt hạn mức trả 429 kèm rate-limit-reset. Mã TOTP chỉ dùng một lần.
//	@Tags			auth
//	@Accept			json
//	@Produce		application/json,application/fhir+json
//	@Param			body	body		MFAVerifyBody	true	"login và mã"
//	@Success		200		{object}	application.LoginResult
//	@Failure		400		{object}	fhir.OperationOutcome
//	@Failure		404		{object}	fhir.OperationOutcome
//	@Failure		429		{object}	fhir.OperationOutcome
//	@Router			/auth/mfa/verify [post]
func (h *authHandlers) mfaVerify(c echo.Context) error {
	b, err := readBody(c)
	if err != nil {
		return writeError(c, err)
	}
	v := &validationError{}
	if b.str("login") == "" {
		v.add("Missing login", "login")
	}
	if b.str("token") == "" {
		v.add("Missing token", "token")
	}
	if err := v.orNil(); err != nil {
		return writeError(c, err)
	}
	res, login, err := h.svc.MFAVerify(c.Request().Context(), b.str("login"), b.str("token"))
	if err != nil {
		return writeError(c, err)
	}
	h.sendResult(c, res, login)
	return c.JSON(http.StatusOK, res)
}

// MFALoginEnrollBody là body của POST /auth/mfa/login-enroll.
type MFALoginEnrollBody struct {
	Login  string `json:"login" binding:"required" example:"a3b1c2d4-0000-4000-8000-000000000000"` // id login đang chờ đăng ký MFA; bắt buộc, rỗng là 400
	Method string `json:"method,omitempty" enums:"totp,email" example:"totp"`                      // phương thức đăng ký; giá trị khác "email" (hoặc bỏ trống) được hiểu là totp
	Token  string `json:"token,omitempty" example:"123456"`                                        // mã TOTP của secret trong enrollUri; chỉ dùng với method=totp (bắt buộc khi đó), bỏ qua với email
}

// mfaLoginEnroll godoc
//
//	@Summary		Đăng ký MFA ngay trong lúc đăng nhập
//	@Description	Dùng khi MFA bắt buộc mà user chưa đăng ký (phản hồi login có mfaEnrollRequired). method=email: đăng ký và gửi mã tới email (nhập mã qua /auth/mfa/verify). method=totp (mặc định): gửi token của secret trong enrollUri. Cùng hạn mức thử với /auth/mfa/verify.
//	@Tags			auth
//	@Accept			json
//	@Produce		application/json,application/fhir+json
//	@Param			body	body		MFALoginEnrollBody	true	"login, phương thức và token"
//	@Success		200		{object}	application.LoginResult
//	@Failure		400		{object}	fhir.OperationOutcome
//	@Failure		429		{object}	fhir.OperationOutcome
//	@Router			/auth/mfa/login-enroll [post]
func (h *authHandlers) mfaLoginEnroll(c echo.Context) error {
	b, err := readBody(c)
	if err != nil {
		return writeError(c, err)
	}
	if b.str("login") == "" {
		v := &validationError{}
		v.add("Missing login", "login")
		return writeError(c, v)
	}
	res, login, err := h.svc.MFALoginEnroll(c.Request().Context(), b.str("login"), b["method"], b.str("token"))
	if err != nil {
		return writeError(c, err)
	}
	h.sendResult(c, res, login)
	return c.JSON(http.StatusOK, res)
}

// MFASendEmailBody là body của POST /auth/mfa/send-email.
type MFASendEmailBody struct {
	Login string `json:"login" binding:"required" example:"a3b1c2d4-0000-4000-8000-000000000000"` // id login đang chờ MFA cần gửi lại mã email; bắt buộc, rỗng là 400
}

// mfaSendEmail godoc
//
//	@Summary		Gửi (lại) mã MFA qua email cho login đang chờ
//	@Description	Chỉ cho user đã đăng ký email MFA. Gửi lại ghi đè mã cũ; tối đa 5 lần mỗi login trong 20 phút.
//	@Tags			auth
//	@Accept			json
//	@Produce		application/fhir+json
//	@Param			body	body		MFASendEmailBody	true	"login"
//	@Success		200		{object}	fhir.OperationOutcome
//	@Failure		400		{object}	fhir.OperationOutcome
//	@Failure		429		{object}	fhir.OperationOutcome
//	@Router			/auth/mfa/send-email [post]
func (h *authHandlers) mfaSendEmail(c echo.Context) error {
	b, err := readBody(c)
	if err != nil {
		return writeError(c, err)
	}
	if b.str("login") == "" {
		v := &validationError{}
		v.add("Missing login", "login")
		return writeError(c, v)
	}
	if err := h.svc.MFASendEmail(c.Request().Context(), b.str("login")); err != nil {
		return writeError(c, err)
	}
	return writeOutcome(c, http.StatusOK, allOK())
}

// mfaStatus godoc
//
//	@Summary		Trạng thái MFA của chính mình
//	@Description	Danh sách phương thức đã đăng ký và được phép, project có bắt buộc MFA không, và (nếu chưa đăng ký TOTP) enrollUri cùng enrollQrCode. Secret trong enrollUri chỉ thành yếu tố khi chính phiên này xác nhận bằng mã.
//	@Tags			auth
//	@Security		BearerAuth
//	@Produce		application/json,application/fhir+json
//	@Success		200	{object}	application.MFAStatusResult
//	@Failure		401	{object}	fhir.OperationOutcome
//	@Failure		403	{object}	fhir.OperationOutcome
//	@Router			/auth/mfa/status [get]
func (h *authHandlers) mfaStatus(c echo.Context) error {
	res, err := h.svc.MFAStatus(c.Request().Context(), authState(c))
	if err != nil {
		return writeError(c, err)
	}
	return c.JSON(http.StatusOK, res)
}

// MFAEnrollBody là body của POST /auth/mfa/enroll.
type MFAEnrollBody struct {
	Method string `json:"method,omitempty" enums:"totp,email" example:"totp"` // phương thức cần đăng ký; giá trị khác "email" (hoặc bỏ trống) được hiểu là totp
	Token  string `json:"token" binding:"required" example:"123456"`          // mã xác nhận: mã email (method=email) hoặc mã TOTP (method=totp); rỗng là 400 "Missing token"
}

// mfaEnroll godoc
//
//	@Summary		Đăng ký thêm một phương thức MFA cho chính mình
//	@Description	method=email: token là mã nhận từ /auth/mfa/send-email-challenge. method=totp (mặc định): token là mã của secret trong enrollUri của /auth/mfa/status. Cũng chịu hạn mức thử như /auth/mfa/verify (lệch có chủ đích so với Medplum).
//	@Tags			auth
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		application/fhir+json
//	@Param			body	body		MFAEnrollBody	true	"phương thức và token"
//	@Success		200		{object}	fhir.OperationOutcome
//	@Failure		400		{object}	fhir.OperationOutcome
//	@Failure		401		{object}	fhir.OperationOutcome
//	@Failure		403		{object}	fhir.OperationOutcome
//	@Failure		429		{object}	fhir.OperationOutcome
//	@Router			/auth/mfa/enroll [post]
func (h *authHandlers) mfaEnroll(c echo.Context) error {
	b, err := readBody(c)
	if err != nil {
		return writeError(c, err)
	}
	if err := h.svc.MFAEnroll(c.Request().Context(), authState(c), b["method"], b.str("token")); err != nil {
		return writeError(c, err)
	}
	return writeOutcome(c, http.StatusOK, allOK())
}

// mfaSendEmailChallenge godoc
//
//	@Summary		Gửi mã email cho phiên đang dùng
//	@Description	Để đăng ký email MFA hoặc thỏa yếu tố email khi tắt MFA. Tối đa 5 lần mỗi login trong 20 phút.
//	@Tags			auth
//	@Security		BearerAuth
//	@Produce		application/fhir+json
//	@Success		200	{object}	fhir.OperationOutcome
//	@Failure		400	{object}	fhir.OperationOutcome
//	@Failure		401	{object}	fhir.OperationOutcome
//	@Failure		403	{object}	fhir.OperationOutcome
//	@Failure		429	{object}	fhir.OperationOutcome
//	@Router			/auth/mfa/send-email-challenge [post]
func (h *authHandlers) mfaSendEmailChallenge(c echo.Context) error {
	if err := h.svc.MFASendEmailChallenge(c.Request().Context(), authState(c)); err != nil {
		return writeError(c, err)
	}
	return writeOutcome(c, http.StatusOK, allOK())
}

// MFADisableBody là body của POST /auth/mfa/disable.
type MFADisableBody struct {
	Method string `json:"method,omitempty" enums:"totp,email" example:"totp"` // phương thức cần gỡ; bỏ trống là gỡ tất cả (cần là một yếu tố đã đăng ký, không thì 400)
	Token  string `json:"token" binding:"required" example:"123456"`          // mã chứng minh một yếu tố MFA đang có (TOTP hoặc mã email); rỗng là 400 "Missing token"
}

// mfaDisable godoc
//
//	@Summary		Tắt MFA (một phương thức hoặc tất cả)
//	@Description	Phải chứng minh một yếu tố đang có bằng token (TOTP hoặc mã email). Không gỡ được yếu tố cuối khi MFA là bắt buộc. Gỡ TOTP thì secret cũ bị thay bằng secret ngẫu nhiên mới. Chịu hạn mức thử như /auth/mfa/verify.
//	@Tags			auth
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		application/fhir+json
//	@Param			body	body		MFADisableBody	true	"phương thức (tùy chọn) và token"
//	@Success		200		{object}	fhir.OperationOutcome
//	@Failure		400		{object}	fhir.OperationOutcome
//	@Failure		401		{object}	fhir.OperationOutcome
//	@Failure		403		{object}	fhir.OperationOutcome
//	@Failure		429		{object}	fhir.OperationOutcome
//	@Router			/auth/mfa/disable [post]
func (h *authHandlers) mfaDisable(c echo.Context) error {
	b, err := readBody(c)
	if err != nil {
		return writeError(c, err)
	}
	req := application.MFADisableRequest{}
	if b.has("method") {
		req.Method = b["method"]
	}
	if b.has("token") {
		req.Token = b["token"]
	}
	if err := h.svc.MFADisable(c.Request().Context(), authState(c), req); err != nil {
		return writeError(c, err)
	}
	return writeOutcome(c, http.StatusOK, allOK())
}
