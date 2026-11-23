package httpapi

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"

	"trivita-fhir/apps/auth/internal/application"
	"trivita-fhir/apps/auth/internal/domain"
)

// externalStateCookie là cookie liên kết trình duyệt của luồng đăng nhập ngoài (đặt ở /auth/method, đọc ở callback).
const externalStateCookie = "auth-external-state"

func (h *authHandlers) setExternalStateCookie(c echo.Context, value string, maxAge int) {
	cookie := &http.Cookie{Name: externalStateCookie, Value: value, Path: "/auth/external", MaxAge: maxAge, HttpOnly: true, Secure: h.cookieSecure}
	// Lax: callback là điều hướng cấp cao nhất từ IdP về auth, Strict sẽ không gửi cookie
	cookie.SameSite = http.SameSiteLaxMode
	http.SetCookie(c.Response(), cookie)
}

// registerExternal đăng ký callback đăng nhập ngoài và clientinfo. Callback chịu hạn mức của bước nhập thông tin đăng nhập vì mỗi lần gọi làm auth gọi ra IdP.
func registerExternal(g *echo.Group, h *authHandlers, slow []echo.MiddlewareFunc, enabled bool) {
	g.GET("/clientinfo/:clientId", h.clientInfo)
	g.POST("/exchange", h.exchange, slow...)
	if h.svc.Google != nil {
		g.POST("/google", h.google, slow...)
	}
	if enabled {
		g.POST("/external/start", h.externalStart, slow...)
		g.GET("/external", h.externalCallback, slow...)
		g.GET("/external/:clientId", h.externalCallback, slow...)
	}
}

// externalCallback godoc
//
//	@Summary		Callback đăng nhập qua identity provider ngoài (OIDC)
//	@Description	IdP chuyển trình duyệt về đây với code và state. state phải do /auth/method phát (đã ký) và trình duyệt phải mang cookie auth-external-state tương ứng. Thành công: 302 tới redirectUri của client kèm login và code, hoặc về trang đăng nhập của app kèm login. Lỗi trả OperationOutcome JSON 400 (không chuyển hướng). /auth/external/{clientId} là callback riêng của client có identityProvider.
//	@Tags			auth
//	@Produce		application/fhir+json
//	@Param			clientId	path	string	true	"Client có identityProvider riêng (chỉ ở route /auth/external/{clientId}; route /auth/external không có tham số này)"
//	@Param			code		query	string	true	"Authorization code của IdP"
//	@Param			state		query	string	true	"state do /auth/method phát"
//	@Param			iss			query	string	false	"Issuer (RFC 9207)"
//	@Success		302
//	@Failure		400	{object}	fhir.OperationOutcome
//	@Failure		404	{object}	fhir.OperationOutcome
//	@Failure		429	{object}	fhir.OperationOutcome
//	@Router			/auth/external [get]
//	@Router			/auth/external/{clientId} [get]
func (h *authHandlers) externalCallback(c echo.Context) error {
	nonce := ""
	if ck, err := c.Cookie(externalStateCookie); err == nil {
		nonce = ck.Value
	}
	// cookie chỉ dùng một lần: xóa dù thành công hay lỗi để một callback bị phát lại không dùng được nữa
	h.setExternalStateCookie(c, "", -1)
	to, err := h.svc.ExternalCallback(c.Request().Context(), application.ExternalCallbackRequest{
		Code: c.QueryParam("code"), State: c.QueryParam("state"), Issuer: c.QueryParam("iss"), CallbackClientID: c.Param("clientId"), CookieNonce: nonce,
		RemoteAddress: c.RealIP(), UserAgent: c.Request().UserAgent(),
	})
	if err != nil {
		return writeError(c, err)
	}
	return c.Redirect(http.StatusFound, to)
}

// clientInfo godoc
//
//	@Summary		Thông tin công khai của form đăng nhập của một client
//	@Description	Không cần xác thực. redirect_uri (nếu gửi) phải khớp chính xác một URI đã đăng ký của client. Toàn bộ signInForm là công khai.
//	@Tags			auth
//	@Produce		application/json,application/fhir+json
//	@Param			clientId		path		string	true	"id client"
//	@Param			redirect_uri	query		string	false	"URI chuyển hướng"
//	@Success		200				{object}	fhir.ClientApplicationSignInForm
//	@Failure		400				{object}	fhir.OperationOutcome
//	@Failure		404				{object}	fhir.OperationOutcome
//	@Router			/auth/clientinfo/{clientId} [get]
func (h *authHandlers) clientInfo(c echo.Context) error {
	var redirect *string
	if q := c.QueryParams(); q.Has("redirect_uri") {
		v := q.Get("redirect_uri")
		redirect = &v
	}
	res, err := h.svc.ClientInfo(c.Request().Context(), c.Param("clientId"), redirect)
	if err != nil {
		return writeError(c, err)
	}
	return c.JSON(http.StatusOK, res)
}

// ExternalStartBody là body của POST /auth/external/start.
type ExternalStartBody struct {
	ClientID            string `json:"clientId" binding:"required" example:"a3b1c2d4-0000-4000-8000-000000000000"`    // Id ClientApplication có identityProvider riêng; bắt buộc, không rỗng
	ProjectID           string `json:"projectId,omitempty" example:"a3b1c2d4-0000-4000-8000-000000000001"`            // Id project; ghi vào state do auth ký
	Scope               string `json:"scope,omitempty" example:"openid"`                                              // Scope OAuth; ghi vào state do auth ký
	Nonce               string `json:"nonce,omitempty" example:"c1a9f0e2"`                                            // Nonce OIDC; ghi vào state do auth ký
	Launch              string `json:"launch,omitempty" example:"a3b1c2d4-0000-4000-8000-000000000000"`               // Id SMART launch; ghi vào state do auth ký
	CodeChallenge       string `json:"codeChallenge,omitempty" example:"E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"` // Mã thách thức PKCE; ghi vào state do auth ký
	CodeChallengeMethod string `json:"codeChallengeMethod,omitempty" enums:"plain,S256" example:"S256"`               // Phương thức PKCE: plain hoặc S256
	RedirectURI         string `json:"redirectUri,omitempty" example:"https://app.example.org/callback"`              // URI chuyển hướng của client; ghi vào state do auth ký
	ReturnTo            string `json:"returnTo,omitempty" example:"https://app.example.org/signin"`                   // Trang của app để quay về sau đăng nhập ngoài; ghi vào state do auth ký
}

// externalStart godoc
//
//	@Summary		Bắt đầu đăng nhập qua identity provider RIÊNG của một client
//	@Description	Với client có identityProvider (callback /auth/external/{clientId}). Trả {authorizeUrl} đã kèm state do auth ký và đặt cookie liên kết trình duyệt auth-external-state; app chuyển người dùng tới authorizeUrl. Đăng nhập theo tên miền email dùng /auth/method.
//	@Tags			auth
//	@Accept			json
//	@Produce		application/json,application/fhir+json
//	@Param			body	body		ExternalStartBody	true	"Client và tham số đăng nhập"
//	@Success		200		{object}	application.MethodResult
//	@Failure		400		{object}	fhir.OperationOutcome
//	@Failure		404		{object}	fhir.OperationOutcome
//	@Failure		429		{object}	fhir.OperationOutcome
//	@Router			/auth/external/start [post]
func (h *authHandlers) externalStart(c echo.Context) error {
	b, err := readBody(c)
	if err != nil {
		return writeError(c, err)
	}
	if b.str("clientId") == "" {
		v := &validationError{}
		v.add("Missing clientId", "clientId")
		return writeError(c, v)
	}
	res, nonce, err := h.svc.StartClientExternalLogin(c.Request().Context(), b.str("clientId"), domain.ExternalState{
		ProjectID: b.str("projectId"), Scope: b.str("scope"), Nonce: b.str("nonce"), Launch: b.str("launch"), CodeChallenge: b.str("codeChallenge"),
		CodeChallengeMethod: b.str("codeChallengeMethod"), RedirectURI: b.str("redirectUri"), ReturnTo: b.str("returnTo"),
	})
	if err != nil {
		return writeError(c, err)
	}
	h.setExternalStateCookie(c, nonce, int(h.svc.External.StateTTL().Seconds()))
	return c.JSON(http.StatusOK, res)
}

// ExchangeBody là body của POST /auth/exchange (deprecated: dùng grant token-exchange ở /oauth2/token).
type ExchangeBody struct {
	ExternalAccessToken string `json:"externalAccessToken" binding:"required" example:"ya29.a0AfH6SMB..."`         // Access token do IdP ngoài cấp (subject token); bắt buộc, không rỗng
	ClientID            string `json:"clientId" binding:"required" example:"a3b1c2d4-0000-4000-8000-000000000000"` // Id ClientApplication cấu hình IdP; bắt buộc, không rỗng
	MembershipID        string `json:"membershipId,omitempty" example:"a3b1c2d4-0000-4000-8000-000000000001"`      // Id ProjectMembership đích; bắt buộc với IdP cấu hình ở máy chủ, tùy chọn với trường hợp khác
}

// exchange godoc
//
//	@Summary		Đổi access token của IdP ngoài lấy token của auth (deprecated)
//	@Description	Vỏ mỏng của grant urn:ietf:params:oauth:grant-type:token-exchange. Trả BODY TOKEN (access_token, id_token, refresh_token...), KHÔNG phải LoginResult như tài liệu cũ ghi. IdP cấu hình ở máy chủ bắt buộc có membershipId. Lỗi validator trả OperationOutcome 400; lỗi IdP và danh tính trả {error, error_description}; lỗi đăng nhập (user không có...) trả OperationOutcome.
//	@Tags			auth
//	@Accept			json
//	@Produce		application/json,application/fhir+json
//	@Param			body	body		ExchangeBody	true	"Token của IdP và client"
//	@Success		200		{object}	map[string]interface{}
//	@Failure		400		{object}	fhir.OperationOutcome
//	@Failure		429		{object}	TokenErrorBody
//	@Router			/auth/exchange [post]
func (h *authHandlers) exchange(c echo.Context) error {
	b, err := readBody(c)
	if err != nil {
		return writeError(c, err)
	}
	v := &validationError{}
	if b.str("externalAccessToken") == "" {
		v.add("Missing externalAccessToken", "externalAccessToken")
	}
	if b.str("clientId") == "" {
		v.add("Missing clientId", "clientId")
	}
	if err := v.orNil(); err != nil {
		return writeError(c, err)
	}
	resp, err := h.svc.ExchangeToken(c.Request().Context(), application.ExchangeTokenRequest{
		ClientID: b.str("clientId"), SubjectToken: b.str("externalAccessToken"), SubjectTokenType: application.TokenTypeAccessToken, MembershipID: b.str("membershipId"),
		RemoteAddress: c.RealIP(), UserAgent: c.Request().UserAgent(),
	})
	if err != nil {
		var le *application.ExchangeLoginError
		if errors.As(err, &le) {
			return writeError(c, le.Err) // như Medplum: lỗi tryLogin ở /auth/exchange là OperationOutcome
		}
		oe := toOAuthError(err)
		if oe.Status >= 500 {
			oe = domain.ErrServer()
		}
		return writeOAuthError(c, oe)
	}
	c.Response().Header().Set("Cache-Control", "no-store")
	c.Response().Header().Set("Pragma", "no-cache")
	return c.JSON(http.StatusOK, resp)
}

// GoogleBody là body của POST /auth/google.
type GoogleBody struct {
	GoogleClientID      string `json:"googleClientId" binding:"required" example:"1234567890-abc.apps.googleusercontent.com"` // Client id Google (aud của credential); bắt buộc có mặt trong body
	GoogleCredential    string `json:"googleCredential" binding:"required" example:"eyJhbGciOiJSUzI1NiIs..."`                 // ID token (JWT RS256) của Google Identity Services; bắt buộc có mặt trong body
	ClientID            string `json:"clientId,omitempty" example:"a3b1c2d4-0000-4000-8000-000000000001"`                     // Id ClientApplication OAuth; có thì project lấy từ client
	ProjectID           string `json:"projectId,omitempty" example:"a3b1c2d4-0000-4000-8000-000000000000"`                    // Id project đăng nhập
	CreateUser          bool   `json:"createUser,omitempty" example:"false"`                                                  // true cho phép tạo user mới từ credential nếu chưa có; mặc định false
	ResourceType        string `json:"resourceType,omitempty" example:"Practitioner"`                                         // Loại hồ sơ profile cần đăng nhập (ví dụ Practitioner, Patient); lưu vào login làm profileType
	Scope               string `json:"scope,omitempty" example:"openid"`                                                      // Scope OAuth yêu cầu, phân tách bằng khoảng trắng
	Nonce               string `json:"nonce,omitempty" example:"c1a9f0e2"`                                                    // Nonce OIDC
	CodeChallenge       string `json:"codeChallenge,omitempty" example:"E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"`         // Mã thách thức PKCE
	CodeChallengeMethod string `json:"codeChallengeMethod,omitempty" enums:"plain,S256" example:"S256"`                       // Phương thức PKCE: plain hoặc S256
	RedirectURI         string `json:"redirectUri,omitempty" example:"https://app.example.org/callback"`                      // URI chuyển hướng của client; nếu có phải là chuỗi
}

// google godoc
//
//	@Summary		Đăng nhập bằng Google Identity Services
//	@Description	Xác minh googleCredential (ID token RS256 của Google, aud là googleClientId) rồi đăng nhập. Credential phải báo email đã xác minh. Nếu tên miền của email có DomainConfiguration thì trả {domain, authorizeUrl} (kèm cookie state) thay vì LoginResult. createUser cho phép tạo user mới từ credential. Thông báo lỗi xác minh cố định (không lộ chi tiết).
//	@Tags			auth
//	@Accept			json
//	@Produce		application/json,application/fhir+json
//	@Param			body	body		GoogleBody	true	"Credential của Google"
//	@Success		200		{object}	application.LoginResult
//	@Failure		400		{object}	fhir.OperationOutcome
//	@Failure		429		{object}	fhir.OperationOutcome
//	@Router			/auth/google [post]
func (h *authHandlers) google(c echo.Context) error {
	b, err := readBody(c)
	if err != nil {
		return writeError(c, err)
	}
	v := &validationError{}
	if !b.has("googleClientId") {
		v.add("Missing googleClientId", "googleClientId")
	}
	if !b.has("googleCredential") {
		v.add("Missing googleCredential", "googleCredential")
	}
	if !b.falsy("redirectUri") {
		if _, ok := b["redirectUri"].(string); !ok {
			v.add("Invalid redirectUri", "redirectUri")
		}
	}
	if err := v.orNil(); err != nil {
		return writeError(c, err)
	}
	ctx := c.Request().Context()
	res, err := h.svc.GoogleLogin(ctx, application.GoogleLoginRequest{
		GoogleClientID: b.str("googleClientId"), Credential: b.str("googleCredential"), ClientID: b.str("clientId"), ProjectID: b.str("projectId"),
		ResourceType: b.str("resourceType"), Scope: b.str("scope"), Nonce: b.str("nonce"), RedirectURI: b.str("redirectUri"),
		CodeChallenge: b.str("codeChallenge"), CodeChallengeMethod: b.str("codeChallengeMethod"), CreateUser: b.boolean("createUser"),
		RemoteAddress: c.RealIP(), UserAgent: c.Request().UserAgent(), Origin: c.Request().Header.Get("Origin"),
	})
	if err != nil {
		return writeError(c, err)
	}
	if res.External != nil {
		if res.CookieNonce != "" {
			h.setExternalStateCookie(c, res.CookieNonce, int(h.svc.External.StateTTL().Seconds()))
		}
		return c.JSON(http.StatusOK, res.External)
	}
	out, err := h.svc.BuildLoginResult(ctx, res.Login)
	if err != nil {
		return writeError(c, err)
	}
	h.sendResult(c, out, res.Login)
	return c.JSON(http.StatusOK, out)
}

// PreAuthorizeBody là body của POST /auth/preauthorize.
type PreAuthorizeBody struct {
	ClientID  string `json:"clientId" binding:"required" example:"a3b1c2d4-0000-4000-8000-000000000000"` // Id ClientApplication sẽ đổi mã; bắt buộc, phải là UUID
	Scope     string `json:"scope,omitempty" example:"openid"`                                           // Scope của mã; mặc định openid cộng các scope SMART của người gọi
	Nonce     string `json:"nonce,omitempty" example:"c1a9f0e2"`                                         // Nonce OIDC gắn vào mã; nếu có phải là chuỗi
	ExpiresIn int    `json:"expiresIn,omitempty" minimum:"1" maximum:"604800" example:"3600"`            // Thời hạn mã, tính bằng giây (số nguyên 1 đến 604800); bỏ trống thì dùng mặc định của máy chủ
}

// preauthorize godoc
//
//	@Summary		Mint pre-authorized code cho một thành viên khác của project
//	@Description	Chỉ admin của project (không phải super admin). Header X-Medplum-On-Behalf-Of chỉ ProjectMembership/<id> hoặc tham chiếu profile của thành viên đích (cùng project). scope mặc định là openid cộng các scope SMART của người gọi; người gọi bị giới hạn SMART chỉ mint được trong phạm vi của mình. Mã chỉ trả MỘT lần (kho giữ SHA-256); đổi mã ở /oauth2/token (grant pre-authorized_code).
//	@Tags			auth
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		application/json,application/fhir+json
//	@Param			X-Medplum-On-Behalf-Of	header		string				true	"ProjectMembership/<id> hoặc profile của đích"
//	@Param			body					body		PreAuthorizeBody	true	"Client và tham số"
//	@Success		200						{object}	application.PreAuthorizeResult
//	@Failure		400						{object}	fhir.OperationOutcome
//	@Failure		401						{object}	fhir.OperationOutcome
//	@Failure		403						{object}	fhir.OperationOutcome
//	@Failure		404						{object}	fhir.OperationOutcome
//	@Router			/auth/preauthorize [post]
func (h *authHandlers) preauthorize(c echo.Context) error {
	b, err := readBody(c)
	if err != nil {
		return writeError(c, err)
	}
	// validator chạy TRƯỚC kiểm quyền (như Medplum)
	v := &validationError{}
	if id, ok := b["clientId"].(string); !ok || !domain.IsUUID(id) {
		v.add("Client ID is required", "clientId")
	}
	for _, k := range []struct{ key, msg string }{{"scope", "Scope must be a string"}, {"nonce", "Nonce must be a string"}} {
		if !b.falsy(k.key) {
			if _, ok := b[k.key].(string); !ok {
				v.add(k.msg, k.key)
			}
		}
	}
	expires := 0
	if raw, present := b["expiresIn"]; present && raw != nil {
		f, ok := raw.(float64)
		if !ok || f != float64(int(f)) || f < 1 || f > application.MaxPreAuthTTL {
			v.add("expiresIn must be a positive integer not exceeding 604800 seconds", "expiresIn")
		} else {
			expires = int(f)
		}
	}
	if err := v.orNil(); err != nil {
		return writeError(c, err)
	}
	res, err := h.svc.PreAuthorize(c.Request().Context(), application.PreAuthorizeRequest{
		Caller: authState(c), OnBehalfOf: c.Request().Header.Get("X-Medplum-On-Behalf-Of"), ClientID: b.str("clientId"), Scope: b.str("scope"), Nonce: b.str("nonce"),
		ExpiresIn: expires, RemoteAddress: c.RealIP(), UserAgent: c.Request().UserAgent(),
	})
	if err != nil {
		return writeError(c, err)
	}
	c.Response().Header().Set("Cache-Control", "no-store")
	c.Response().Header().Set("Pragma", "no-cache")
	return c.JSON(http.StatusOK, res)
}
