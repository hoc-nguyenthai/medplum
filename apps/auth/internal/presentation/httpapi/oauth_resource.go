package httpapi

import (
	"encoding/json"
	"mime"
	"net/http"
	"net/url"
	"strings"

	"github.com/labstack/echo/v4"

	"trivita-fhir/apps/auth/internal/application"
)

// userinfo godoc
//
//	@Summary		OIDC userinfo
//	@Description	Cần Bearer access token. Trả `sub`; thêm theo scope của login (khớp từng từ): `profile` (profile, locale, updated_at, name, given_name, family_name), `email` (email, email_verified).
//	@Description	Không có phone, address, birthdate, gender, picture: hồ sơ Patient/Practitioner nằm ở fhir-server, auth không đọc được (quyết định D-10).
//	@Tags			oauth
//	@Produce		json
//	@Security		BearerAuth
//	@Success		200	{object}	map[string]interface{}
//	@Failure		401	{object}	map[string]interface{}
//	@Router			/oauth2/userinfo [get]
//	@Router			/oauth2/userinfo [post]
func (h *oauthHandlers) userinfo(c echo.Context) error {
	info, err := h.svc.UserInfo(c.Request().Context(), authState(c))
	if err != nil {
		return writeError(c, err)
	}
	return c.JSON(http.StatusOK, info)
}

// IntrospectResponse là kết quả RFC 7662: chỉ {"active": false} khi token không dùng được, vì bất cứ lý do nào.
type IntrospectResponse struct {
	Active   bool   `json:"active"`
	Iat      int64  `json:"iat,omitempty"`
	Exp      int64  `json:"exp,omitempty"`
	Iss      string `json:"iss,omitempty"`
	Sub      string `json:"sub,omitempty"`
	ClientID string `json:"client_id,omitempty"`
	Scope    string `json:"scope,omitempty"`
	Patient  string `json:"patient,omitempty"`
}

// introspect godoc
//
//	@Summary		Kiểm tra một token (RFC 7662)
//	@Description	Cần Bearer. Chỉ hỏi được token của cùng project và cùng client với người gọi; refresh token đã xoay là không hoạt động. Mọi lý do không dùng được đều ra `{"active": false}`.
//	@Tags			oauth
//	@Accept			x-www-form-urlencoded,json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			token	formData	string	true	"token cần kiểm tra"
//	@Success		200		{object}	IntrospectResponse
//	@Failure		400		{string}	string	"Token to introspect must be provided"
//	@Failure		401		{object}	map[string]interface{}
//	@Router			/oauth2/introspect [post]
func (h *oauthHandlers) introspect(c echo.Context) error {
	token := ""
	if mt, _, _ := mime.ParseMediaType(c.Request().Header.Get("Content-Type")); mt == "application/json" {
		var body struct {
			Token string `json:"token"`
		}
		_ = json.NewDecoder(c.Request().Body).Decode(&body)
		token = body.Token
	} else if c.Request().ParseForm() == nil {
		token = c.Request().PostForm.Get("token")
	}
	if strings.TrimSpace(token) == "" {
		return c.String(http.StatusBadRequest, "Token to introspect must be provided")
	}
	c.Response().Header().Set("Cache-Control", "no-store")
	return c.JSON(http.StatusOK, h.svc.Introspect(c.Request().Context(), authState(c), token))
}

// logout godoc
//
//	@Summary		Đăng xuất (thu hồi login của access token)
//	@Description	Cần Bearer. Thu hồi login và xóa cookie đăng nhập của client. Sau đó chính access token này bị từ chối ở các route của auth (fhir-server chỉ biết khi token hết hạn).
//	@Tags			oauth
//	@Produce		json
//	@Security		BearerAuth
//	@Success		200	{object}	fhir.OperationOutcome
//	@Failure		401	{object}	fhir.OperationOutcome
//	@Router			/oauth2/logout [post]
//	@Router			/oauth2/logout [get]
func (h *oauthHandlers) logout(c echo.Context) error {
	a := authState(c)
	if err := h.svc.Logout(c.Request().Context(), a); err != nil {
		return writeError(c, err)
	}
	if a.Login.Client != nil {
		clearLoginCookie(c, a.Login.Client.Reference, h.cookieSecure)
	}
	return writeOutcome(c, http.StatusOK, allOK())
}

// clearLoginCookie xóa cookie đăng nhập `medplum-<clientId>` (cùng thuộc tính lúc đặt để trình duyệt chấp nhận việc xóa).
func clearLoginCookie(c echo.Context, clientRef string, secure bool) {
	id := clientRef
	if i := strings.LastIndex(id, "/"); i >= 0 {
		id = id[i+1:]
	}
	cookie := &http.Cookie{Name: "medplum-" + id, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: secure}
	if secure {
		cookie.SameSite = http.SameSiteNoneMode
	} else {
		cookie.SameSite = http.SameSiteLaxMode
	}
	c.SetCookie(cookie)
}

// authorize godoc
//
//	@Summary		Bắt đầu luồng authorization code (OAuth2/OIDC)
//	@Description	Kiểm tra tham số rồi chuyển hướng (302). Không có phiên: sang trang đăng nhập của ứng dụng (<AUTH_APP_BASE_URL>/oauth) kèm tham số; trang đó gọi /auth/login, /auth/profile, /auth/scope rồi chuyển về redirect_uri kèm code.
//	@Description	Có phiên (cookie `medplum-<client_id>` hoặc id_token_hint): tạo Login mới; prompt=none thì trả code ngay, không thì sang trang ứng dụng để xác nhận scope.
//	@Description	Lỗi về client_id hoặc redirect_uri hiển thị trực tiếp (400 văn bản); lỗi khác chuyển về redirect_uri kèm error, error_description, state và iss (RFC 9207).
//	@Description	Client không có secret bắt buộc có code_challenge (S256). PKCE không được kế thừa từ phiên cũ.
//	@Tags			oauth
//	@Param			response_type			query	string	true	"code"
//	@Param			client_id				query	string	true	"client_id"
//	@Param			redirect_uri			query	string	true	"khớp chính xác một redirect URI đã đăng ký"
//	@Param			scope					query	string	false	"mặc định defaultScope của client"
//	@Param			state					query	string	false	"trả nguyên"
//	@Param			nonce					query	string	false	"vào id token"
//	@Param			code_challenge			query	string	false	"PKCE"
//	@Param			code_challenge_method	query	string	false	"S256"
//	@Param			aud						query	string	false	"cùng scheme và host với auth hoặc fhir-server"
//	@Param			launch					query	string	false	"id SmartAppLaunch"
//	@Param			prompt					query	string	false	"none | login"
//	@Param			max_age					query	int		false	"tuổi tối đa của phiên (giây), mặc định 3600"
//	@Param			id_token_hint			query	string	false	"id token đã cấp cho client này"
//	@Success		302
//	@Failure		400	{string}	string
//	@Description	POST nhận cùng tham số ở form `application/x-www-form-urlencoded`.
//	@Router			/oauth2/authorize [get]
//	@Router			/oauth2/authorize [post]
func (h *oauthHandlers) authorize(c echo.Context) error {
	r := c.Request()
	var q interface{ Get(string) string }
	var has func(string) bool
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			return c.String(http.StatusBadRequest, "Invalid request body")
		}
		q, has = r.PostForm, func(k string) bool { return r.PostForm.Has(k) }
	} else {
		q, has = r.URL.Query(), func(k string) bool { return r.URL.Query().Has(k) }
	}
	req := application.AuthorizeRequest{
		ClientID: q.Get("client_id"), RedirectURI: q.Get("redirect_uri"), Scope: q.Get("scope"), ResponseType: q.Get("response_type"),
		State: q.Get("state"), Nonce: q.Get("nonce"), CodeChallenge: q.Get("code_challenge"), CodeChallengeMethod: q.Get("code_challenge_method"),
		Audience: q.Get("aud"), Launch: q.Get("launch"), Prompt: q.Get("prompt"), MaxAge: q.Get("max_age"), IDTokenHint: q.Get("id_token_hint"),
		HasRequestObject: has("request"), RemoteAddress: c.RealIP(), UserAgent: r.UserAgent(),
	}
	if ck, err := c.Cookie("medplum-" + req.ClientID); err == nil {
		req.Cookie = ck.Value
	}
	res, err := h.svc.Authorize(r.Context(), req)
	if err != nil {
		return writeError(c, err)
	}
	c.Response().Header().Set("Referrer-Policy", "no-referrer")
	switch res.Kind {
	case application.AuthorizeDirectError:
		return c.String(http.StatusBadRequest, res.Message)
	case application.AuthorizeToClient:
		return c.Redirect(http.StatusFound, withQuery(res.URI, res.Params))
	default:
		return c.Redirect(http.StatusFound, withQuery(h.appURL(), res.Params))
	}
}

// appURL là trang đăng nhập của ứng dụng: <AUTH_APP_BASE_URL>/oauth.
func (h *oauthHandlers) appURL() string { return strings.TrimRight(h.appBaseURL, "/") + "/oauth" }

// withQuery nối tham số vào URL, giữ nguyên tham số sẵn có trong URL (redirect_uri của client có thể đã có query).
func withQuery(raw string, params url.Values) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	q := u.Query()
	for k, vs := range params {
		q[k] = vs
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// RegisterBody là body của POST /oauth2/register.
type RegisterBody struct {
	RedirectURIs []string `json:"redirect_uris"`
}

// register godoc
//
//	@Summary		Tra client dựng sẵn theo redirect URI
//	@Description	Không phải đăng ký động theo RFC 7591: không tạo client mới và không trả client_secret. Chỉ có khi bật AUTH_OAUTH_REGISTER_ENABLED. redirect_uris[0] phải bằng chính xác redirect URI của một client dựng sẵn.
//	@Tags			oauth
//	@Accept			json
//	@Produce		json
//	@Param			body	body		RegisterBody	true	"redirect_uris"
//	@Success		201		{object}	application.RegisteredClient
//	@Failure		400		{object}	TokenErrorBody
//	@Router			/oauth2/register [post]
func (h *oauthHandlers) register(c echo.Context) error {
	var body struct {
		RedirectURIs []any `json:"redirect_uris"`
	}
	_ = json.NewDecoder(c.Request().Body).Decode(&body)
	var uris []string
	for _, u := range body.RedirectURIs {
		s, ok := u.(string)
		if !ok {
			uris = nil
			break
		}
		uris = append(uris, s)
	}
	res, oerr := h.svc.RegisterClient(uris)
	if oerr != nil {
		return writeOAuthError(c, oerr)
	}
	return c.JSON(http.StatusCreated, res)
}
