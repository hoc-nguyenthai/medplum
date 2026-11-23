package httpapi

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/labstack/echo/v4"

	"trivita-fhir/apps/auth/internal/application"
	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
)

// OAuthInfo là thông tin công khai để dựng JWKS và các tài liệu discovery.
type OAuthInfo struct {
	// Issuer là claim iss của token.
	Issuer string
	// JWKS trả JSON {"keys":[...]} chỉ gồm tham số công khai.
	JWKS func() []byte
	// SigningAlg là thuật toán ký id token/access token.
	SigningAlg string
	// AllowPlainPKCE khai thêm `plain` ở code_challenge_methods_supported.
	AllowPlainPKCE bool
	// AuthorizeEnabled đăng ký /oauth2/authorize (cần AUTH_APP_BASE_URL) và khai authorization_endpoint ở discovery.
	AuthorizeEnabled bool
	// AppBaseURL là địa chỉ ứng dụng đăng nhập (chuyển hướng tới <AppBaseURL>/oauth).
	AppBaseURL string
	// RegisterEnabled đăng ký /oauth2/register và khai registration_endpoint ở discovery.
	RegisterEnabled bool
}

func registerOAuth(e *echo.Echo, dep Deps) {
	h := &oauthHandlers{svc: dep.Auth, info: *dep.OAuth, baseURL: strings.TrimRight(dep.BaseURL, "/"), cookieSecure: dep.CookieSecure, appBaseURL: dep.OAuth.AppBaseURL, pkceMethods: []string{"S256"}}
	if dep.OAuth.AllowPlainPKCE {
		h.pkceMethods = append(h.pkceMethods, "plain")
	}
	e.POST("/oauth2/token", h.token)
	if dep.OAuth.RegisterEnabled {
		e.POST("/oauth2/register", h.register)
	}
	if dep.OAuth.AuthorizeEnabled {
		e.GET("/oauth2/authorize", h.authorize)
		e.POST("/oauth2/authorize", h.authorize)
	}
	// Bearer gắn theo từng route (không dùng Group("", mw): đường dẫn lạ dưới /oauth2 sẽ thành 401 thay vì 404/405).
	bearer := requireBearer(dep.Authenticator)
	e.GET("/oauth2/userinfo", h.userinfo, bearer)
	e.POST("/oauth2/userinfo", h.userinfo, bearer)
	e.POST("/oauth2/introspect", h.introspect, bearer)
	e.GET("/oauth2/logout", h.logout, bearer)
	e.POST("/oauth2/logout", h.logout, bearer)
	e.GET("/.well-known/jwks.json", h.jwks)
	e.GET("/.well-known/openid-configuration", h.openidConfiguration)
	e.GET("/.well-known/oauth-authorization-server", h.openidConfiguration)
	e.GET("/.well-known/oauth-protected-resource", h.protectedResource)
	e.GET("/.well-known/smart-configuration", h.smartConfiguration)
	e.GET("/.well-known/smart-styles.json", h.smartStyles)
}

type oauthHandlers struct {
	svc          *application.Service
	info         OAuthInfo
	baseURL      string
	cookieSecure bool
	appBaseURL   string
	// pkceMethods là code_challenge_methods_supported.
	pkceMethods []string
}

// ---- /oauth2/token

// TokenErrorBody là lỗi OAuth (RFC 6749 §5.2).
type TokenErrorBody struct {
	Error            string `json:"error" binding:"required" example:"invalid_request"`       // mã lỗi OAuth (invalid_request, invalid_client, invalid_grant, unsupported_grant_type...)
	ErrorDescription string `json:"error_description,omitempty" example:"Missing grant_type"` // mô tả lỗi cho người lập trình; có thể vắng
}

func writeOAuthError(c echo.Context, e *domain.OAuthError) error {
	c.Response().Header().Set("Cache-Control", "no-store")
	c.Response().Header().Set("Pragma", "no-cache")
	return c.JSON(e.Status, TokenErrorBody{Error: e.Code, ErrorDescription: e.Description})
}

// token godoc
//
//	@Summary		Đổi thông tin lấy token (OAuth2)
//	@Description	Chỉ nhận application/x-www-form-urlencoded. Grant hiện có: client_credentials, authorization_code và refresh_token (xoay secret; dùng lại thì thu hồi Login) (PKCE bắt buộc khi Login có challenge; client xác thực bằng HTTP Basic hoặc client_id + client_secret trong body).
//	@Description	Scope do client khai không được chứa `fhir.*`; access token mang `fhir_project`, `scope` (kèm scope vai trò do auth thêm) và `fhir_access_policy` để fhir-server nhận.
//	@Tags			oauth
//	@Accept			x-www-form-urlencoded
//	@Produce		application/json
//	@Param			grant_type		formData	string	true	"client_credentials | authorization_code | refresh_token"
//	@Param			code			formData	string	false	"authorization_code"
//	@Param			code_verifier	formData	string	false	"authorization_code (PKCE)"
//	@Param			redirect_uri	formData	string	false	"authorization_code: bắt buộc nếu lúc đăng nhập đã gửi"
//	@Param			refresh_token	formData	string	false	"refresh_token"
//	@Param			client_id		formData	string	false	"nếu không dùng Basic"
//	@Param			client_secret	formData	string	false	"nếu không dùng Basic"
//	@Param			scope			formData	string	false	"mặc định openid"
//	@Success		200				{object}	map[string]interface{}
//	@Failure		400				{object}	TokenErrorBody
//	@Router			/oauth2/token [post]
func (h *oauthHandlers) token(c echo.Context) error {
	if mt, _, err := mime.ParseMediaType(c.Request().Header.Get("Content-Type")); err != nil || mt != "application/x-www-form-urlencoded" {
		return c.String(http.StatusBadRequest, "Unsupported content type") // như Medplum: văn bản thuần, không phải JSON
	}
	if err := c.Request().ParseForm(); err != nil {
		return writeOAuthError(c, domain.ErrInvalidRequest("Invalid request body"))
	}
	form := c.Request().PostForm
	ctx := c.Request().Context()
	ip, ua := c.RealIP(), c.Request().UserAgent()
	grant := form.Get("grant_type")
	if grant == "" {
		return writeOAuthError(c, domain.ErrInvalidRequest("Missing grant_type"))
	}

	var resp application.TokenResponse
	var oerr *domain.OAuthError
	switch grant {
	case "client_credentials", "authorization_code", "refresh_token":
		auth, aerr := h.clientAuth(c, form)
		if aerr != nil {
			return writeOAuthError(c, aerr)
		}
		switch grant {
		case "client_credentials":
			resp, oerr = h.svc.ClientCredentials(ctx, application.ClientCredentialsRequest{
				Authenticated: auth.client, ClientID: auth.id, ClientSecret: auth.secret, Scope: form.Get("scope"), RemoteAddress: ip, UserAgent: ua,
			})
		case "authorization_code":
			resp, oerr = h.svc.AuthorizationCode(ctx, application.AuthorizationCodeRequest{
				Authenticated: auth.client, ClientID: auth.id, ClientSecret: auth.secret, Code: form.Get("code"), CodeVerifier: form.Get("code_verifier"),
				RedirectURI: form.Get("redirect_uri"), RemoteAddress: ip, UserAgent: ua,
			})
		default:
			resp, oerr = h.svc.Refresh(ctx, application.RefreshRequest{
				Authenticated: auth.client, RefreshToken: form.Get("refresh_token"), ClientID: auth.id, ClientSecret: auth.secret, RemoteAddress: ip, UserAgent: ua,
			})
		}
	case application.GrantTypePreAuthorizedCode:
		resp, oerr = h.svc.PreAuthorizedCode(ctx, application.PreAuthorizedCodeRequest{
			ClientID: form.Get("client_id"), Code: form.Get("pre-authorized_code"), RemoteAddress: ip, UserAgent: ua,
		})
	case application.GrantTypeTokenExchange:
		var err error
		resp, err = h.svc.ExchangeToken(ctx, application.ExchangeTokenRequest{
			ClientID: form.Get("client_id"), SubjectToken: form.Get("subject_token"), SubjectTokenType: form.Get("subject_token_type"),
			MembershipID: form.Get("membership_id"), Scope: form.Get("scope"), Nonce: form.Get("nonce"), RemoteAddress: ip, UserAgent: ua,
		})
		oerr = toOAuthError(err)
	default:
		return writeOAuthError(c, domain.ErrInvalidRequest("Unsupported grant_type"))
	}
	if oerr != nil {
		if oerr.Status >= 500 {
			return writeOAuthError(c, domain.ErrServer())
		}
		return writeOAuthError(c, oerr)
	}
	c.Response().Header().Set("Cache-Control", "no-store")
	c.Response().Header().Set("Pragma", "no-cache")
	return c.JSON(http.StatusOK, resp)
}

// toOAuthError đổi lỗi của ExchangeToken sang lỗi OAuth: lỗi TryLogin (domain) thành invalid_request kèm thông báo nghiệp vụ, lỗi lạ thành server_error.
func toOAuthError(err error) *domain.OAuthError {
	var oe *domain.OAuthError
	var le *application.ExchangeLoginError
	var de *domain.Error
	switch {
	case err == nil:
		return nil
	case errors.As(err, &oe):
		return oe
	case errors.As(err, &le) && errors.As(le.Err, &de) && de.Kind != domain.KindInternal:
		return domain.ErrInvalidRequest(de.Message)
	}
	return domain.ErrServer()
}

// clientAuth là cách client xác thực ở /oauth2/token: private_key_jwt (client_assertion), hoặc HTTP Basic/body (secret).
type clientAuth struct {
	id, secret string
	client     *fhir.ClientApplication // có khi đã xác thực bằng private_key_jwt
}

// clientAuth chọn đúng MỘT cách xác thực (RFC 6749 §2.3: không được dùng nhiều cách cùng lúc).
func (h *oauthHandlers) clientAuth(c echo.Context, form url.Values) (clientAuth, *domain.OAuthError) {
	assertionType, assertion := form.Get("client_assertion_type"), form.Get("client_assertion")
	if assertionType == "" && assertion == "" {
		id, secret, oerr := clientCredentials(c.Request(), form)
		return clientAuth{id: id, secret: secret}, oerr
	}
	if c.Request().Header.Get("Authorization") != "" || form.Get("client_secret") != "" {
		return clientAuth{}, domain.ErrInvalidRequest("Multiple client authentication methods")
	}
	client, oerr := h.svc.AuthenticateClientAssertion(c.Request().Context(), assertionType, assertion, h.endpoint("/oauth2/token"))
	if oerr != nil {
		return clientAuth{}, oerr
	}
	if id := form.Get("client_id"); id != "" && id != client.ID {
		return clientAuth{}, domain.ErrInvalidRequest("Invalid client")
	}
	return clientAuth{id: client.ID, client: client}, nil
}

// clientCredentials lấy client_id và secret: HTTP Basic nếu có header Authorization, không thì từ body (token.ts:839).
// Basic được đọc đúng RFC 6749 §2.3.1: tách ở dấu `:` ĐẦU TIÊN (secret có thể chứa `:`), giải UTF-8 và URL-decode từng phần.
// (Medplum dùng split(':') lấy hai phần tử đầu nên cắt cụt secret có `:`, và decode ASCII.)
func clientCredentials(r *http.Request, form url.Values) (id, secret string, oerr *domain.OAuthError) {
	header := r.Header.Get("Authorization")
	if header == "" {
		return form.Get("client_id"), form.Get("client_secret"), nil
	}
	const prefix = "Basic "
	if len(header) < len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", "", domain.ErrInvalidRequest("Invalid authorization header")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(header[len(prefix):]))
	if err != nil || !utf8.Valid(raw) {
		return "", "", domain.ErrInvalidRequest("Invalid authorization header")
	}
	user, pass, ok := strings.Cut(string(raw), ":")
	if !ok {
		return "", "", domain.ErrInvalidRequest("Invalid authorization header")
	}
	return formDecode(user), formDecode(pass), nil
}

func formDecode(s string) string {
	if v, err := url.QueryUnescape(s); err == nil {
		return v
	}
	return s
}

// ---- /.well-known

// jwks godoc
//
//	@Summary		JWKS (khóa công khai để xác minh token)
//	@Description	Chỉ khóa công khai; có `ETag` và `Cache-Control: public, max-age=300`.
//	@Tags			well-known
//	@Produce		application/json
//	@Success		200	{object}	map[string]interface{}
//	@Router			/.well-known/jwks.json [get]
func (h *oauthHandlers) jwks(c echo.Context) error {
	return serveJSONCached(c, h.info.JWKS())
}

// serveJSONCached trả JSON công khai có ETag và cache 5 phút (JWKS, discovery); hỗ trợ If-None-Match.
func serveJSONCached(c echo.Context, body []byte) error {
	sum := sha256.Sum256(body)
	etag := `"` + hex.EncodeToString(sum[:8]) + `"`
	h := c.Response().Header()
	h.Set("Cache-Control", "public, max-age=300")
	h.Set("ETag", etag)
	if c.Request().Header.Get("If-None-Match") == etag {
		return c.NoContent(http.StatusNotModified)
	}
	return c.Blob(http.StatusOK, echo.MIMEApplicationJSONCharsetUTF8, body)
}

func (h *oauthHandlers) endpoint(path string) string { return h.baseURL + path }

// openidConfiguration trả tài liệu discovery. Chỉ khai những gì ĐÃ cài (Medplum khai cả id_token, HS256, pairwise mà không làm).
// Điểm cuối và grant mới được thêm vào khi các bước P3 sau đó hoàn tất.
//
//	@Summary		Discovery OIDC / OAuth 2.0 authorization server
//	@Description	Cùng một tài liệu ở hai đường dẫn. Chỉ khai những gì đã cài.
//	@Tags			well-known
//	@Produce		json
//	@Success		200	{object}	map[string]interface{}
//	@Router			/.well-known/openid-configuration [get]
//	@Router			/.well-known/oauth-authorization-server [get]
func (h *oauthHandlers) openidConfiguration(c echo.Context) error {
	doc := map[string]any{
		"issuer":                                           h.info.Issuer,
		"token_endpoint":                                   h.endpoint("/oauth2/token"),
		"userinfo_endpoint":                                h.endpoint("/oauth2/userinfo"),
		"introspection_endpoint":                           h.endpoint("/oauth2/introspect"),
		"jwks_uri":                                         h.endpoint("/.well-known/jwks.json"),
		"id_token_signing_alg_values_supported":            []string{h.info.SigningAlg},
		"grant_types_supported":                            grantTypes,
		"response_types_supported":                         []string{},
		"subject_types_supported":                          []string{"public"},
		"scopes_supported":                                 []string{"openid", "profile", "email", "phone", "address"},
		"token_endpoint_auth_methods_supported":            authMethods,
		"token_endpoint_auth_signing_alg_values_supported": assertionAlgs,
		"code_challenge_methods_supported":                 h.pkceMethods,
	}
	h.advertiseAuthorize(doc)
	return serveJSON(c, doc)
}

// authMethods là token_endpoint_auth_methods_supported và grantTypes là grant_types_supported: chỉ khai thứ đã cài.
var (
	authMethods   = []string{"client_secret_basic", "client_secret_post", "private_key_jwt", "none"}
	assertionAlgs = []string{"RS256", "RS384", "RS512", "ES256", "ES384", "ES512"}
	grantTypes    = []string{"authorization_code", "refresh_token", "client_credentials", application.GrantTypePreAuthorizedCode, application.GrantTypeTokenExchange}
)

// advertiseAuthorize khai authorization_endpoint và response_types_supported chỉ khi /oauth2/authorize có thật.
func (h *oauthHandlers) advertiseAuthorize(doc map[string]any) {
	if h.info.RegisterEnabled {
		doc["registration_endpoint"] = h.endpoint("/oauth2/register")
	}
	if h.info.AuthorizeEnabled {
		doc["authorization_endpoint"] = h.endpoint("/oauth2/authorize")
		doc["response_types_supported"] = []string{"code"}
	}
}

// smartConfiguration godoc
//
//	@Summary		SMART configuration
//	@Description	SMART on FHIR discovery, khai cùng endpoint với openid-configuration.
//	@Tags			well-known
//	@Produce		application/json
//	@Success		200	{object}	map[string]interface{}
//	@Router			/.well-known/smart-configuration [get]
func (h *oauthHandlers) smartConfiguration(c echo.Context) error {
	doc := map[string]any{
		"issuer":                                h.info.Issuer,
		"jwks_uri":                              h.endpoint("/.well-known/jwks.json"),
		"token_endpoint":                        h.endpoint("/oauth2/token"),
		"introspection_endpoint":                h.endpoint("/oauth2/introspect"),
		"grant_types_supported":                 grantTypes,
		"token_endpoint_auth_methods_supported": authMethods,
		"token_endpoint_auth_signing_alg_values_supported": assertionAlgs,
		"code_challenge_methods_supported":                 h.pkceMethods,
		"scopes_supported":                                 []string{"user/*.cruds", "system/*.cruds", "openid"},
		"response_types_supported":                         []string{},
		"capabilities":                                     []string{"client-confidential-symmetric", "permission-v1", "permission-v2"},
	}
	h.advertiseAuthorize(doc)
	return serveJSON(c, doc)
}

// smartStyles godoc
//
//	@Summary		SMART style (màu, font cho ứng dụng nhúng)
//	@Description	Giá trị cố định.
//	@Tags			well-known
//	@Produce		application/json
//	@Success		200	{object}	map[string]interface{}
//	@Router			/.well-known/smart-styles.json [get]
func (h *oauthHandlers) smartStyles(c echo.Context) error {
	return serveJSON(c, map[string]string{
		"color_background": "#edeae3", "color_error": "#9e2d2d", "color_highlight": "#69b5ce", "color_modal_backdrop": "",
		"color_success": "#498e49", "color_text": "#303030", "dim_border_radius": "6px", "dim_font_size": "13px",
		"dim_spacing_size": "20px", "font_family_body": "Georgia, Times, 'Times New Roman', serif",
		"font_family_heading": "'HelveticaNeue-Light', Helvetica, Arial, 'Lucida Grande', sans-serif",
	})
}

// protectedResource trả RFC 9728. `resource` tùy chọn phải bắt đầu bằng địa chỉ của auth (như Medplum); mặc định là chính auth.
//
//	@Summary		Protected resource metadata (RFC 9728)
//	@Description	`resource` tùy chọn phải bắt đầu bằng địa chỉ của auth; mặc định là chính auth.
//	@Tags			well-known
//	@Produce		json
//	@Param			resource	query		string	false	"phải bắt đầu bằng địa chỉ của auth"
//	@Success		200			{object}	map[string]interface{}
//	@Failure		400			{object}	map[string]interface{}
//	@Router			/.well-known/oauth-protected-resource [get]
func (h *oauthHandlers) protectedResource(c echo.Context) error {
	resource := h.baseURL + "/"
	if vals := c.QueryParams()["resource"]; len(vals) > 0 {
		if len(vals) != 1 || !strings.HasPrefix(vals[0], h.baseURL) {
			return c.String(http.StatusBadRequest, "Invalid resource URL")
		}
		resource = vals[0]
	}
	return serveJSON(c, map[string]any{
		"resource": resource, "issuer": h.info.Issuer, "authorization_servers": []string{h.info.Issuer},
		"scopes_supported": []string{"openid", "profile", "email", "phone", "address"}, "bearer_methods_supported": []string{"header"},
		"introspection_endpoint": h.endpoint("/oauth2/introspect"),
	})
}

func serveJSON(c echo.Context, v any) error {
	c.Response().Header().Set("Cache-Control", "public, max-age=300")
	return c.JSON(http.StatusOK, v)
}
