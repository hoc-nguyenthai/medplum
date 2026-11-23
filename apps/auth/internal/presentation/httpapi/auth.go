package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	"trivita-fhir/apps/auth/internal/application"
	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
)

// authKey là khóa lưu AuthState trong echo.Context.
const authKey = "auth.authState"

func registerAuth(e *echo.Echo, dep Deps) {
	h := &authHandlers{svc: dep.Auth, cookieSecure: dep.CookieSecure, externalEnabled: dep.ExternalEnabled}
	g := e.Group("/auth")
	// Các bước nhập thông tin đăng nhập có hạn mức riêng, chặt hơn (chậm việc dò mật khẩu). Gắn theo từng route (xem ghi chú dưới).
	var slow []echo.MiddlewareFunc
	if dep.LoginRateLimit > 0 {
		slow = append(slow, rateLimiter(dep.LoginRateLimit, dep.LoginRateBurst, nil))
	}
	g.POST("/login", h.login, slow...)
	g.POST("/profile", h.profile, slow...)
	g.POST("/scope", h.scope, slow...)
	g.POST("/method", h.method, slow...)
	g.POST("/newuser", h.newUser, slow...)
	g.POST("/newproject", h.newProject, slow...)
	g.POST("/newpatient", h.newPatient)
	g.GET("/login/:login", h.status)

	// Gắn middleware theo từng route: Group("", mw) của Echo làm mọi đường dẫn lạ dưới /auth thành 401 thay vì 404/405.
	bearer := requireBearer(dep.Authenticator)
	g.GET("/me", h.me, bearer)
	g.POST("/revoke", h.revoke, bearer)
	registerCredentials(g, h, bearer)
	registerMFA(g, h, bearer, slow)
	registerExternal(g, h, slow, h.externalEnabled)
	g.POST("/preauthorize", h.preauthorize, bearer)
}

type authHandlers struct {
	svc          *application.Service
	cookieSecure bool
	// externalEnabled: đăng ký callback đăng nhập ngoài (cần AUTH_APP_BASE_URL để chuyển người dùng về app và khóa ký state).
	externalEnabled bool
}

// ---- đọc body

// body là JSON object đã giải mã; trường thiếu hoặc sai kiểu đọc ra giá trị rỗng, việc kiểm tra thuộc validator của từng route.
type body map[string]any

func readBody(c echo.Context) (body, error) {
	var b body
	dec := json.NewDecoder(c.Request().Body)
	if err := dec.Decode(&b); err != nil || b == nil {
		return nil, domain.BadRequest("Content could not be parsed")
	}
	return b, nil
}

func (b body) str(key string) string {
	s, _ := b[key].(string)
	return s
}

func (b body) has(key string) bool { _, ok := b[key]; return ok }

func (b body) boolean(key string) bool { v, _ := b[key].(bool); return v }

// falsy: giống `optional({values: 'falsy'})` của express-validator: thiếu, null, "", 0, false.
func (b body) falsy(key string) bool {
	switch v := b[key].(type) {
	case nil:
		return true
	case string:
		return v == ""
	case bool:
		return !v
	case float64:
		return v == 0
	}
	return false
}

// ---- POST /auth/login

// LoginBody là body của POST /auth/login.
type LoginBody struct {
	Email               string `json:"email" binding:"required" example:"an@example.org"`                             // Email đăng nhập; bắt buộc, phải là địa chỉ email hợp lệ
	Password            string `json:"password" binding:"required" example:"mat-khau-dai-123"`                        // Mật khẩu; bắt buộc, 8 ký tự trở lên và không quá 72 byte
	ProjectID           string `json:"projectId,omitempty" example:"new"`                                             // Project đăng nhập: "new" (chưa thuộc project, cho phép không có membership) hoặc UUID project; bỏ trống thì suy ra từ clientId (nếu gửi cả hai phải khớp project của client)
	ClientID            string `json:"clientId,omitempty" example:"a3b1c2d4-0000-4000-8000-000000000001"`             // Id ClientApplication OAuth; có thì project được lấy từ client và cookie đăng nhập đặt theo client
	Scope               string `json:"scope,omitempty" example:"openid"`                                              // Scope OAuth yêu cầu, phân tách bằng khoảng trắng; mặc định "openid"
	Nonce               string `json:"nonce,omitempty" example:"c1a9f0e2"`                                            // Nonce OIDC; bỏ trống thì máy chủ tự sinh
	CodeChallenge       string `json:"codeChallenge,omitempty" example:"E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"` // Mã thách thức PKCE gắn vào login để kiểm ở bước đổi code
	CodeChallengeMethod string `json:"codeChallengeMethod,omitempty" enums:"plain,S256" example:"S256"`               // Phương thức PKCE của codeChallenge: plain hoặc S256
	Launch              string `json:"launch,omitempty" example:"a3b1c2d4-0000-4000-8000-000000000000"`               // Id ngữ cảnh SMART launch (nếu có)
	ResourceType        string `json:"resourceType,omitempty" example:"Practitioner"`                                 // Loại hồ sơ profile cần đăng nhập (ví dụ Practitioner, Patient); lưu vào login làm profileType
	RedirectURI         string `json:"redirectUri,omitempty" example:"https://app.example.org/callback"`              // URI chuyển hướng của client sau khi đăng nhập; nếu có phải là chuỗi
	Remember            bool   `json:"remember,omitempty" example:"false"`                                            // true để kèm refresh token trong kết quả đăng nhập; mặc định false
}

func validateLogin(b body) error {
	v := &validationError{}
	if e, ok := b["email"].(string); !ok || !domain.IsEmail(e) {
		v.add("Valid email address is required", "email")
	}
	if !b.falsy("projectId") {
		if p, ok := b["projectId"].(string); !ok || (p != "new" && !domain.IsUUID(p)) {
			v.add("Invalid projectId", "projectId")
		}
	}
	if err := domain.ValidatePassword(b.str("password")); err != nil {
		v.add(err.Error(), "password")
	}
	if !b.falsy("redirectUri") {
		if _, ok := b["redirectUri"].(string); !ok {
			v.add("Invalid redirectUri", "redirectUri")
		}
	}
	return v.orNil()
}

// login godoc
//
//	@Summary		Đăng nhập bằng email và mật khẩu
//	@Description	Trả đúng một trong các dạng LoginResult: code (đã chọn được profile), memberships (chọn profile), mfaRequired hoặc mfaEnrollRequired. Khi có code và login thuộc một client, đặt cookie medplum-<clientId>.
//	@Tags			auth
//	@Accept			json
//	@Produce		application/json,application/fhir+json
//	@Param			body	body		LoginBody	true	"Thông tin đăng nhập"
//	@Success		200		{object}	application.LoginResult
//	@Failure		400		{object}	fhir.OperationOutcome
//	@Router			/auth/login [post]
func (h *authHandlers) login(c echo.Context) error {
	b, err := readBody(c)
	if err != nil {
		return writeError(c, err)
	}
	if err := validateLogin(b); err != nil {
		return writeError(c, err)
	}
	ctx := c.Request().Context()
	projectID, err := h.svc.ResolveProjectID(ctx, b.str("clientId"), b.str("projectId"))
	if err != nil {
		return writeError(c, err)
	}
	scope := b.str("scope")
	if scope == "" {
		scope = "openid"
	}
	nonce := b.str("nonce")
	if nonce == "" {
		nonce = domain.NewID()
	}
	res, login, err := h.svc.Login(ctx, domain.LoginRequest{
		AuthMethod: domain.AuthPassword, Email: b.str("email"), Password: b.str("password"), ProjectID: projectID,
		ClientID: b.str("clientId"), Scope: scope, Nonce: nonce, ResourceType: b.str("resourceType"), LaunchID: b.str("launch"),
		CodeChallenge: b.str("codeChallenge"), CodeChallengeMethod: b.str("codeChallengeMethod"), RedirectURI: b.str("redirectUri"),
		Remember: b.boolean("remember"), RemoteAddress: c.RealIP(), UserAgent: c.Request().UserAgent(), Origin: c.Request().Header.Get("Origin"),
		AllowNoMembership: b.str("projectId") == "new",
	})
	if err != nil {
		return writeError(c, err)
	}
	h.sendResult(c, res, login)
	return c.JSON(http.StatusOK, res)
}

// sendResult đặt cookie đăng nhập khi phản hồi có code và login thuộc một client OAuth (auth/utils.ts:548).
func (h *authHandlers) sendResult(c echo.Context, res *application.LoginResult, login *fhir.Login) {
	if res.Code == "" || login == nil || login.Client == nil || res.Cookie == "" {
		return
	}
	id := login.Client.Reference
	if i := strings.LastIndex(id, "/"); i >= 0 {
		id = id[i+1:]
	}
	cookie := &http.Cookie{Name: "medplum-" + id, Value: res.Cookie, Path: "/", MaxAge: 3600, HttpOnly: true, Secure: h.cookieSecure}
	if h.cookieSecure {
		cookie.SameSite = http.SameSiteNoneMode
	} else {
		cookie.SameSite = http.SameSiteLaxMode // trình duyệt bỏ cookie SameSite=None không Secure
	}
	http.SetCookie(c.Response(), cookie)
}

// ---- POST /auth/profile, /auth/scope

// ProfileBody là body của POST /auth/profile.
type ProfileBody struct {
	Login   string `json:"login" binding:"required" example:"a3b1c2d4-0000-4000-8000-000000000000"`   // Id login đang dở (từ LoginResult.login); bắt buộc có mặt trong body
	Profile string `json:"profile" binding:"required" example:"a3b1c2d4-0000-4000-8000-000000000001"` // Id ProjectMembership chọn làm profile; bắt buộc có mặt trong body (phải thuộc các membership của login)
}

// profile godoc
//
//	@Summary		Chọn profile (ProjectMembership) cho login
//	@Description	Gắn một ProjectMembership đã chọn vào login đang dở (login và profile đều bắt buộc có mặt trong body), rồi trả LoginResult như /auth/login (thường có code). Membership không tồn tại trả 400 "Profile not found". Có code và login thuộc một client thì đặt cookie medplum-<clientId>.
//	@Tags		auth
//	@Accept		json
//	@Produce		application/json,application/fhir+json
//	@Param		body	body		ProfileBody	true	"login (id login đang dở) và profile (id ProjectMembership)"
//	@Success	200		{object}	application.LoginResult
//	@Failure	400		{object}	fhir.OperationOutcome
//	@Router		/auth/profile [post]
func (h *authHandlers) profile(c echo.Context) error {
	b, err := readBody(c)
	if err != nil {
		return writeError(c, err)
	}
	v := &validationError{}
	if !b.has("login") {
		v.add("Missing login", "login")
	}
	if !b.has("profile") {
		v.add("Missing profile", "profile")
	}
	if err := v.orNil(); err != nil {
		return writeError(c, err)
	}
	res, login, err := h.svc.SelectProfile(c.Request().Context(), b.str("login"), b.str("profile"))
	if err != nil {
		return writeError(c, err)
	}
	h.sendResult(c, res, login)
	return c.JSON(http.StatusOK, res)
}

// ScopeBody là body của POST /auth/scope.
type ScopeBody struct {
	Login string `json:"login" binding:"required" example:"a3b1c2d4-0000-4000-8000-000000000000"` // Id login đang dở; bắt buộc có mặt trong body
	Scope string `json:"scope" binding:"required" example:"openid profile"`                       // Scope mới, phân tách bằng khoảng trắng; bắt buộc có mặt trong body và chỉ được thu hẹp
}

// scope godoc
//
//	@Summary		Thu hẹp scope của login
//	@Description	Đổi scope của login đang dở sang scope mới (login và scope đều bắt buộc có mặt trong body), rồi trả LoginResult như /auth/login. Scope mới chỉ được thu hẹp so với scope hiện có. Có code và login thuộc một client thì đặt cookie medplum-<clientId>.
//	@Tags		auth
//	@Accept		json
//	@Produce		application/json,application/fhir+json
//	@Param		body	body		ScopeBody	true	"login và scope mới"
//	@Success	200		{object}	application.LoginResult
//	@Failure	400		{object}	fhir.OperationOutcome
//	@Router		/auth/scope [post]
func (h *authHandlers) scope(c echo.Context) error {
	b, err := readBody(c)
	if err != nil {
		return writeError(c, err)
	}
	v := &validationError{}
	if !b.has("login") {
		v.add("Missing login", "login")
	}
	if !b.has("scope") {
		v.add("Missing scope", "scope")
	}
	if err := v.orNil(); err != nil {
		return writeError(c, err)
	}
	res, login, err := h.svc.SelectScope(c.Request().Context(), b.str("login"), b.str("scope"))
	if err != nil {
		return writeError(c, err)
	}
	h.sendResult(c, res, login)
	return c.JSON(http.StatusOK, res)
}

// ---- GET /auth/login/:login, POST /auth/method

// status godoc
//
//	@Summary		Trạng thái một login đang dở
//	@Description	Trả LoginResult hiện tại của login (code, memberships, mfaRequired hoặc mfaEnrollRequired tùy bước đang dở) để app tiếp tục luồng đăng nhập. Id login phải là UUID, sai trả 400, không có login trả 404. Có code và login thuộc một client thì đặt cookie medplum-<clientId>.
//	@Tags		auth
//	@Produce		application/json,application/fhir+json
//	@Param		login	path		string	true	"Login id (UUID)"
//	@Success	200		{object}	application.LoginResult
//	@Failure	400		{object}	fhir.OperationOutcome
//	@Failure	404		{object}	fhir.OperationOutcome
//	@Router		/auth/login/{login} [get]
func (h *authHandlers) status(c echo.Context) error {
	id := c.Param("login")
	if !domain.IsUUID(id) {
		v := &validationError{}
		v.add("Login ID is required", "login")
		return writeError(c, v)
	}
	res, login, err := h.svc.LoginStatus(c.Request().Context(), id)
	if err != nil {
		return writeError(c, err)
	}
	h.sendResult(c, res, login)
	return c.JSON(http.StatusOK, res)
}

// MethodBody là body của POST /auth/method. Các trường sau email chỉ dùng khi đăng nhập ngoài đã bật: chúng được ghi vào `state` do auth KÝ (Q1).
type MethodBody struct {
	Email               string `json:"email" binding:"required" example:"an@example.org"`                             // Email cần xác định cách đăng nhập; bắt buộc, phải hợp lệ
	ClientID            string `json:"clientId,omitempty" example:"a3b1c2d4-0000-4000-8000-000000000001"`             // Id ClientApplication; chỉ ghi vào state ký khi đăng nhập ngoài đã bật
	ProjectID           string `json:"projectId,omitempty" example:"a3b1c2d4-0000-4000-8000-000000000000"`            // Id project; chỉ ghi vào state ký khi đăng nhập ngoài đã bật
	Scope               string `json:"scope,omitempty" example:"openid"`                                              // Scope OAuth; chỉ ghi vào state ký khi đăng nhập ngoài đã bật
	Nonce               string `json:"nonce,omitempty" example:"c1a9f0e2"`                                            // Nonce OIDC; chỉ ghi vào state ký khi đăng nhập ngoài đã bật
	Launch              string `json:"launch,omitempty" example:"a3b1c2d4-0000-4000-8000-000000000000"`               // Id SMART launch; chỉ ghi vào state ký khi đăng nhập ngoài đã bật
	CodeChallenge       string `json:"codeChallenge,omitempty" example:"E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"` // Mã thách thức PKCE; chỉ ghi vào state ký khi đăng nhập ngoài đã bật
	CodeChallengeMethod string `json:"codeChallengeMethod,omitempty" enums:"plain,S256" example:"S256"`               // Phương thức PKCE: plain hoặc S256; chỉ ghi vào state ký khi đăng nhập ngoài đã bật
	RedirectURI         string `json:"redirectUri,omitempty" example:"https://app.example.org/callback"`              // URI chuyển hướng của client; chỉ ghi vào state ký khi đăng nhập ngoài đã bật
	ReturnTo            string `json:"returnTo,omitempty" example:"https://app.example.org/signin"`                   // Trang của app để quay về sau đăng nhập ngoài; chỉ ghi vào state ký khi đăng nhập ngoài đã bật
}

// method godoc
//
//	@Summary		Cách đăng nhập của một email
//	@Description	Trả {} nếu đăng nhập bằng mật khẩu; trả {domain, authorizeUrl} nếu tên miền email cấu hình identity provider ngoài. Khi đăng nhập ngoài đã bật, authorizeUrl đã kèm `state` do auth ký (chứa các tham số đăng nhập trong body) và phản hồi đặt cookie liên kết trình duyệt `auth-external-state`: app KHÔNG tự thêm state, chỉ chuyển người dùng tới authorizeUrl. Tên miền trả về là chữ thường.
//	@Tags			auth
//	@Accept			json
//	@Produce		application/json,application/fhir+json
//	@Param			body	body		MethodBody	true	"Email"
//	@Success		200		{object}	application.MethodResult
//	@Failure		400		{object}	fhir.OperationOutcome
//	@Router			/auth/method [post]
func (h *authHandlers) method(c echo.Context) error {
	b, err := readBody(c)
	if err != nil {
		return writeError(c, err)
	}
	if e, ok := b["email"].(string); !ok || !domain.IsEmail(e) {
		v := &validationError{}
		v.add("Valid email address is required", "email")
		return writeError(c, v)
	}
	res, nonce, err := h.svc.StartExternalLogin(c.Request().Context(), b.str("email"), domain.ExternalState{
		ClientID: b.str("clientId"), ProjectID: b.str("projectId"), Scope: b.str("scope"), Nonce: b.str("nonce"), Launch: b.str("launch"),
		CodeChallenge: b.str("codeChallenge"), CodeChallengeMethod: b.str("codeChallengeMethod"), RedirectURI: b.str("redirectUri"), ReturnTo: b.str("returnTo"),
	})
	if err != nil {
		return writeError(c, err)
	}
	if res == nil {
		return c.JSON(http.StatusOK, &application.MethodResult{})
	}
	if nonce != "" {
		h.setExternalStateCookie(c, nonce, int(h.svc.External.StateTTL().Seconds()))
	}
	return c.JSON(http.StatusOK, res)
}

// ---- Bearer: GET /auth/me, POST /auth/revoke

// requireBearer xác thực Bearer token bằng Authenticator; thiếu hoặc sai thì 401 kèm WWW-Authenticate.
func requireBearer(a application.Authenticator) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			token, ok := bearerToken(c.Request())
			if !ok || a == nil {
				c.Response().Header().Set("WWW-Authenticate", `Bearer realm="auth"`)
				return writeOutcome(c, http.StatusUnauthorized, outcome("error", "login", "Unauthorized"))
			}
			state, err := a.Authenticate(c.Request().Context(), token)
			if err != nil {
				if domain.KindOf(err) != domain.KindUnauthorized {
					return writeError(c, err)
				}
				c.Response().Header().Set("WWW-Authenticate", `Bearer realm="auth", error="invalid_token"`)
				return writeOutcome(c, http.StatusUnauthorized, outcome("error", "login", "Unauthorized"))
			}
			c.Set(authKey, state)
			return next(c)
		}
	}
}

func bearerToken(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	if len(h) < 8 || !strings.EqualFold(h[:7], "Bearer ") {
		return "", false
	}
	t := strings.TrimSpace(h[7:])
	return t, t != ""
}

func authState(c echo.Context) *application.AuthState {
	s, _ := c.Get(authKey).(*application.AuthState)
	return s
}

// me godoc
//
//	@Summary		Thông tin người đang đăng nhập
//	@Description	profile chỉ có resourceType và id (hồ sơ đầy đủ nằm ở fhir-server); accessPolicy chưa có ở bản này.
//	@Tags			auth
//	@Security		BearerAuth
//	@Produce		application/json,application/fhir+json
//	@Success		200	{object}	application.Me
//	@Failure		401	{object}	fhir.OperationOutcome
//	@Router			/auth/me [get]
func (h *authHandlers) me(c echo.Context) error {
	me, err := h.svc.GetMe(c.Request().Context(), authState(c))
	if err != nil {
		return writeError(c, err)
	}
	return c.JSON(http.StatusOK, me)
}

// RevokeBody là body của POST /auth/revoke.
type RevokeBody struct {
	LoginID string `json:"loginId" binding:"required" example:"a3b1c2d4-0000-4000-8000-000000000000"` // Id login (phiên) của chính người gọi cần thu hồi; bắt buộc, phải là UUID
}

// revoke godoc
//
//	@Summary		Thu hồi một phiên đăng nhập của chính mình
//	@Description	Cần Bearer token. Thu hồi một login (phiên) thuộc về chính người gọi theo loginId (UUID, bắt buộc); login của người khác hoặc không có trả lỗi 403/404. Thành công trả OperationOutcome "All OK".
//	@Tags		auth
//	@Security	BearerAuth
//	@Accept		json
//	@Produce		application/fhir+json
//	@Param		body	body		RevokeBody	true	"loginId cần thu hồi"
//	@Success	200		{object}	fhir.OperationOutcome
//	@Failure	400		{object}	fhir.OperationOutcome
//	@Failure	401		{object}	fhir.OperationOutcome
//	@Failure	403		{object}	fhir.OperationOutcome
//	@Failure	404		{object}	fhir.OperationOutcome
//	@Router		/auth/revoke [post]
func (h *authHandlers) revoke(c echo.Context) error {
	b, err := readBody(c)
	if err != nil {
		return writeError(c, err)
	}
	if id, ok := b["loginId"].(string); !ok || !domain.IsUUID(id) {
		v := &validationError{}
		v.add("Login ID is required.", "loginId")
		return writeError(c, v)
	}
	if err := h.svc.RevokeSession(c.Request().Context(), authState(c), b.str("loginId")); err != nil {
		return writeError(c, err)
	}
	return writeOutcome(c, http.StatusOK, allOK())
}
