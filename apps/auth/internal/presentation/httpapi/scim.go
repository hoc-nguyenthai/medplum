package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"mime"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"

	"trivita-fhir/apps/auth/internal/application"
	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/apps/auth/internal/domain/scim"
)

const scimContentType = "application/scim+json; charset=utf-8"

// registerScim đăng ký /scim/v2/Users (SCIM 2.0). Mọi lỗi (kể cả 401 và 403) có dạng SCIM Error. Trước mọi xử lý: danh sách IP được phép (nếu cấu hình), hạn mức
// của bước nhập thông tin đăng nhập (chống dò secret của client qua Basic), rồi xác thực Basic (client id:secret) hoặc Bearer.
func registerScim(e *echo.Echo, dep Deps) {
	h := &scimHandlers{svc: dep.Auth, authn: dep.Authenticator, allowed: dep.ScimAllowedNets, maxBody: dep.ScimMaxBodyBytes}
	var chain []echo.MiddlewareFunc
	chain = append(chain, h.ipGuard)
	if dep.LoginRateLimit > 0 {
		chain = append(chain, rateLimiter(dep.LoginRateLimit, dep.LoginRateBurst, nil))
	}
	chain = append(chain, h.authenticate)
	write := append(append([]echo.MiddlewareFunc{}, chain...), h.contentType)
	g := e.Group("/scim/v2")
	g.GET("/Users", h.list, chain...)
	g.GET("/Users/:id", h.get, chain...)
	g.POST("/Users", h.create, write...)
	g.PUT("/Users/:id", h.replace, write...)
	g.PATCH("/Users/:id", h.patch, write...)
	g.DELETE("/Users/:id", h.delete, chain...)
}

type scimHandlers struct {
	svc     *application.Service
	authn   application.Authenticator
	allowed []*net.IPNet
	maxBody int64
}

func scimStatus(k domain.Kind) (int, string) {
	switch k {
	case domain.KindBadRequest:
		return http.StatusBadRequest, "invalidValue"
	case domain.KindUnauthorized:
		return http.StatusUnauthorized, ""
	case domain.KindForbidden:
		return http.StatusForbidden, ""
	case domain.KindNotFound:
		return http.StatusNotFound, ""
	case domain.KindConflict:
		return http.StatusConflict, "uniqueness"
	case domain.KindTooManyRequests:
		return http.StatusTooManyRequests, ""
	}
	return http.StatusInternalServerError, ""
}

func scimWrite(c echo.Context, status int, v any) error {
	c.Response().Header().Set(echo.HeaderContentType, scimContentType)
	c.Response().WriteHeader(status)
	return json.NewEncoder(c.Response()).Encode(v)
}

func scimFail(c echo.Context, status int, scimType, detail string) error {
	return scimWrite(c, status, scim.Error{Schemas: []string{scim.SchemaError}, Status: strconv.Itoa(status), ScimType: scimType, Detail: detail})
}

// scimError đổi lỗi của application sang SCIM Error. Lỗi nội bộ trả chung chung (không lộ chi tiết).
func scimError(c echo.Context, err error) error {
	var pe *scim.ProblemError
	if errors.As(err, &pe) {
		return scimFail(c, pe.Status, pe.ScimType, pe.Detail)
	}
	var de *domain.Error
	if errors.As(err, &de) && de.Kind != domain.KindInternal {
		status, typ := scimStatus(de.Kind)
		return scimFail(c, status, typ, de.Message)
	}
	return scimFail(c, http.StatusInternalServerError, "", "Internal server error")
}

func (h *scimHandlers) ipGuard(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		if len(h.allowed) > 0 {
			ip := net.ParseIP(c.RealIP())
			ok := false
			for _, n := range h.allowed {
				if ip != nil && n.Contains(ip) {
					ok = true
					break
				}
			}
			if !ok {
				return scimFail(c, http.StatusForbidden, "", "Forbidden")
			}
		}
		return next(c)
	}
}

func (h *scimHandlers) unauthorized(c echo.Context) error {
	c.Response().Header().Add("WWW-Authenticate", `Basic realm="scim", charset="UTF-8"`)
	c.Response().Header().Add("WWW-Authenticate", `Bearer realm="scim"`)
	return scimFail(c, http.StatusUnauthorized, "", "Unauthorized")
}

func (h *scimHandlers) authenticate(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		hdr := c.Request().Header.Get("Authorization")
		var state *application.AuthState
		var err error
		switch {
		case len(hdr) > 6 && strings.EqualFold(hdr[:6], "Basic "):
			raw, derr := base64.StdEncoding.DecodeString(strings.TrimSpace(hdr[6:]))
			id, secret, ok := strings.Cut(string(raw), ":")
			if derr != nil || !ok || id == "" || secret == "" {
				return h.unauthorized(c)
			}
			state, err = h.svc.ScimBasicAuth(c.Request().Context(), id, secret)
		default:
			token, ok := bearerToken(c.Request())
			if !ok || h.authn == nil {
				return h.unauthorized(c)
			}
			state, err = h.authn.Authenticate(c.Request().Context(), token)
		}
		if err != nil {
			if domain.KindOf(err) == domain.KindUnauthorized {
				return h.unauthorized(c)
			}
			return scimError(c, err)
		}
		c.Set(authKey, state)
		return next(c)
	}
}

func (h *scimHandlers) contentType(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		mt, _, err := mime.ParseMediaType(c.Request().Header.Get(echo.HeaderContentType))
		if err != nil || (mt != "application/json" && mt != "application/scim+json") {
			return scimFail(c, http.StatusUnsupportedMediaType, "", "Content-Type must be application/scim+json or application/json")
		}
		return next(c)
	}
}

// decode đọc body JSON có giới hạn kích thước; echo.Bind không dùng vì chỉ nhận application/json.
func (h *scimHandlers) decode(c echo.Context, v any) error {
	r := http.MaxBytesReader(c.Response(), c.Request().Body, h.maxBody)
	if err := json.NewDecoder(r).Decode(v); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return &scim.ProblemError{Status: http.StatusRequestEntityTooLarge, Detail: "Request body too large"}
		}
		return &scim.ProblemError{Status: http.StatusBadRequest, ScimType: "invalidSyntax", Detail: "Content could not be parsed"}
	}
	return nil
}

// list godoc
//
//	@Summary		SCIM: liệt kê user của project
//	@Description	Chỉ admin của project (Bearer, hoặc Basic client:secret khi AUTH_SCIM_ALLOW_BASIC) và project có tính năng `scim`. filter chỉ hỗ trợ `userName eq "x"` (không phân biệt hoa thường), dạng khác là 400 invalidFilter. Phân trang thật: startIndex từ 1, count mặc định và trần là AUTH_SCIM_MAX_PAGE. Lỗi dạng SCIM Error.
//	@Tags			scim
//	@Security		BearerAuth
//	@Produce		json
//	@Param			filter		query		string	false	"userName eq \"x\""
//	@Param			startIndex	query		int		false	"bắt đầu từ 1"
//	@Param			count		query		int		false	"số kết quả"
//	@Success		200			{object}	scim.ListResponse
//	@Failure		400			{object}	scim.Error
//	@Failure		401			{object}	scim.Error
//	@Failure		403			{object}	scim.Error
//	@Router			/scim/v2/Users [get]
func (h *scimHandlers) list(c echo.Context) error {
	p := application.ScimListParams{Filter: c.QueryParam("filter")}
	for _, q := range []struct {
		name string
		dst  **int
	}{{"startIndex", &p.StartIndex}, {"count", &p.Count}} {
		if raw := c.QueryParam(q.name); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil {
				return scimFail(c, http.StatusBadRequest, "invalidValue", q.name+" must be an integer")
			}
			*q.dst = &n
		}
	}
	res, err := h.svc.ScimList(c.Request().Context(), authState(c), p)
	if err != nil {
		return scimError(c, err)
	}
	return scimWrite(c, http.StatusOK, res)
}

// get godoc
//
//	@Summary		SCIM: đọc một user
//	@Description	id là id của ProjectMembership. Membership ngoài project, thuộc Bot/ClientApplication hoặc không tồn tại đều là 404.
//	@Tags			scim
//	@Security		BearerAuth
//	@Produce		json
//	@Param			id	path		string	true	"id membership"
//	@Success		200	{object}	scim.User
//	@Failure		404	{object}	scim.Error
//	@Router			/scim/v2/Users/{id} [get]
func (h *scimHandlers) get(c echo.Context) error {
	u, err := h.svc.ScimGet(c.Request().Context(), authState(c), c.Param("id"))
	if err != nil {
		return scimError(c, err)
	}
	return scimWrite(c, http.StatusOK, u)
}

// create godoc
//
//	@Summary		SCIM: tạo user
//	@Description	Chỉ userName, name, emails[0], externalId, userType (Patient|Practitioner|RelatedPerson, mặc định Practitioner) và active được dùng; mọi trường khác (admin, accessPolicy, groups, roles, extension) bị bỏ qua. Patient và Practitioner phải có chính sách truy cập mặc định của project. 409 nếu đã là member hoặc externalId trùng. Không gửi thư.
//	@Tags			scim
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			body	body		scim.User	true	"User"
//	@Success		201		{object}	scim.User
//	@Failure		400		{object}	scim.Error
//	@Failure		409		{object}	scim.Error
//	@Router			/scim/v2/Users [post]
func (h *scimHandlers) create(c echo.Context) error {
	var in scim.User
	if err := h.decode(c, &in); err != nil {
		return scimError(c, err)
	}
	u, err := h.svc.ScimCreate(c.Request().Context(), authState(c), in)
	if err != nil {
		return scimError(c, err)
	}
	if u.Meta != nil {
		c.Response().Header().Set(echo.HeaderLocation, u.Meta.Location)
	}
	return scimWrite(c, http.StatusCreated, u)
}

// replace godoc
//
//	@Summary		SCIM: thay user (PUT)
//	@Description	Dùng id của đường dẫn (id trong body có và khác là 400). Chỉ ghi name, externalId, emails[0], active. Đổi email của User thuộc project: emailVerified=false, vô hiệu mọi security request và thu hồi mọi login (một transaction); User ở phạm vi server không ghi được (đổi email là 403). userName không đổi được.
//	@Tags			scim
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			id		path		string		true	"id membership"
//	@Param			body	body		scim.User	true	"User"
//	@Success		200		{object}	scim.User
//	@Failure		400		{object}	scim.Error
//	@Failure		403		{object}	scim.Error
//	@Failure		404		{object}	scim.Error
//	@Router			/scim/v2/Users/{id} [put]
func (h *scimHandlers) replace(c echo.Context) error {
	var in scim.User
	if err := h.decode(c, &in); err != nil {
		return scimError(c, err)
	}
	u, err := h.svc.ScimReplace(c.Request().Context(), authState(c), c.Param("id"), in)
	if err != nil {
		return scimError(c, err)
	}
	return scimWrite(c, http.StatusOK, u)
}

// patch godoc
//
//	@Summary		SCIM: sửa user (PATCH)
//	@Description	PatchOp với add, replace, remove. Chỉ active, externalId, name, name.givenName, name.familyName, emails ghi được; userName, userType và thuộc tính chuẩn không lưu (displayName...) bị bỏ qua; đường dẫn khác (admin, accessPolicy, profile...) là 400 invalidPath. add trên thuộc tính đa trị thay nguyên.
//	@Tags			scim
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			id		path		string			true	"id membership"
//	@Param			body	body		scim.PatchOp	true	"PatchOp"
//	@Success		200		{object}	scim.User
//	@Failure		400		{object}	scim.Error
//	@Router			/scim/v2/Users/{id} [patch]
func (h *scimHandlers) patch(c echo.Context) error {
	var in scim.PatchOp
	if err := h.decode(c, &in); err != nil {
		return scimError(c, err)
	}
	u, err := h.svc.ScimPatch(c.Request().Context(), authState(c), c.Param("id"), in)
	if err != nil {
		return scimError(c, err)
	}
	return scimWrite(c, http.StatusOK, u)
}

// delete godoc
//
//	@Summary		SCIM: xóa user
//	@Description	Xóa membership (User thuộc project mà hết membership thì bị xóa theo). Không xóa chủ project, admin cuối cùng hay membership của chính người gọi (400).
//	@Tags			scim
//	@Security		BearerAuth
//	@Param			id	path	string	true	"id membership"
//	@Success		204
//	@Failure		400	{object}	scim.Error
//	@Failure		404	{object}	scim.Error
//	@Router			/scim/v2/Users/{id} [delete]
func (h *scimHandlers) delete(c echo.Context) error {
	if err := h.svc.ScimDelete(c.Request().Context(), authState(c), c.Param("id")); err != nil {
		return scimError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}
