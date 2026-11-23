package httpapi

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strings"

	"github.com/labstack/echo/v4"

	"trivita-fhir/pkg/fhirauth"
)

// Role là mức quyền của một token.
type Role = fhirauth.Role

const (
	RoleNone  = fhirauth.RoleNone
	RoleRead  = fhirauth.RoleRead
	RoleWrite = fhirauth.RoleWrite
	RoleAdmin = fhirauth.RoleAdmin
)

// TokenVerifier kiểm tra access token (JWT) và trả người gọi kèm project.
type TokenVerifier interface {
	Verify(ctx context.Context, raw string) (*fhirauth.Principal, error)
}

// Token là một bearer token có tên.
type Token struct{ Name, Secret string }

type entry struct {
	name string
	hash [32]byte
	role Role
}

// Authenticator so khớp bearer token trong thời gian không phụ thuộc nội dung (so băm SHA-256 của token, duyệt hết
// danh sách, không thoát sớm) để không lộ token qua timing.
type Authenticator struct {
	entries  []entry
	disabled bool
	verifier TokenVerifier // khác nil: chế độ JWT, token tĩnh không dùng
}

// NewJWTAuthenticator dựng Authenticator dùng JWT (OAuth2/OIDC). Principal (kèm project) được gắn vào context của
// yêu cầu để fhirauth.Scope tách dữ liệu theo project.
func NewJWTAuthenticator(v TokenVerifier) *Authenticator { return &Authenticator{verifier: v} }

// NewAuthenticator dựng từ danh sách token. disabled=true tắt xác thực (chỉ dùng cho dev; ghi actor "anonymous").
func NewAuthenticator(read, write, admin []Token, disabled bool) *Authenticator {
	a := &Authenticator{disabled: disabled}
	for _, t := range read {
		a.entries = append(a.entries, entry{t.Name, sha256.Sum256([]byte(t.Secret)), RoleRead})
	}
	for _, t := range write {
		a.entries = append(a.entries, entry{t.Name, sha256.Sum256([]byte(t.Secret)), RoleWrite})
	}
	for _, t := range admin {
		a.entries = append(a.entries, entry{t.Name, sha256.Sum256([]byte(t.Secret)), RoleAdmin})
	}
	return a
}

// Authenticate trả tên và vai trò của token ("", RoleNone nếu sai).
func (a *Authenticator) Authenticate(secret string) (string, Role) {
	h := sha256.Sum256([]byte(secret))
	name, role := "", RoleNone
	for _, e := range a.entries {
		if subtle.ConstantTimeCompare(h[:], e.hash[:]) == 1 {
			name, role = e.name, e.role
		}
	}
	return name, role
}

const actorKey = "fhir.actor"

func bearer(r *http.Request) string {
	h := r.Header.Get(echo.HeaderAuthorization)
	const p = "bearer "
	if len(h) > len(p) && strings.EqualFold(h[:len(p)], p) {
		return strings.TrimSpace(h[len(p):])
	}
	return ""
}

// RequiredRole cho biết vai trò tối thiểu của một yêu cầu FHIR:
//   - quản trị ($reindex, $expunge): RoleAdmin;
//   - đọc và tìm kiếm (GET, HEAD, POST _search) và các operation không ghi ($validate, $everything, $expand, $lookup, $validate-code, $subsumes, $translate, $export): RoleRead;
//   - mọi thao tác còn lại (kể cả batch/transaction, vì có thể chứa thao tác ghi): RoleWrite.
func RequiredRole(method, path string) Role {
	p := strings.TrimRight(path, "/")
	switch {
	case strings.Contains(p, "/bulkdata/") && strings.HasSuffix(p, "/status"):
		return RoleRead // theo dõi hoặc hủy export của chính mình, mọi method
	case strings.HasSuffix(p, "/$reindex") || strings.HasSuffix(p, "/$expunge"):
		return RoleAdmin
	case strings.HasSuffix(p, "/$validate") || strings.HasSuffix(p, "/$everything") || strings.HasSuffix(p, "/$expand") ||
		strings.HasSuffix(p, "/$lookup") || strings.HasSuffix(p, "/$validate-code") || strings.HasSuffix(p, "/$graphql") || strings.HasSuffix(p, "/$get-ws-binding-token") || strings.HasSuffix(p, "/$subsumes") || strings.HasSuffix(p, "/$translate"):
		return RoleRead // operation không ghi (terminology gồm cả POST)
	}
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return RoleRead
	case http.MethodPost:
		if strings.HasSuffix(p, "/_search") {
			return RoleRead // POST _search là tìm kiếm dạng form
		}
	}
	return RoleWrite
}

// Guard là middleware kiểm tra token theo RequiredRole. /metadata công khai (để client tự khám phá).
// Thiếu hoặc sai token -> 401; đúng token nhưng thiếu quyền -> 403.
func (a *Authenticator) Guard() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			req := c.Request()
			if a.disabled || (req.Method == http.MethodGet && strings.TrimRight(req.URL.Path, "/") == Prefix+"/metadata") {
				c.Set(actorKey, "anonymous")
				return next(c)
			}
			if a.verifier != nil {
				return a.guardJWT(c, next)
			}
			name, role := a.Authenticate(bearer(req))
			if role == RoleNone {
				c.Response().Header().Set("WWW-Authenticate", `Bearer realm="fhir"`)
				return echo.NewHTTPError(http.StatusUnauthorized, "Thiếu hoặc sai bearer token")
			}
			if role < RequiredRole(req.Method, req.URL.Path) {
				return echo.NewHTTPError(http.StatusForbidden, "Token không đủ quyền cho thao tác này")
			}
			c.Set(actorKey, name)
			// Principal không project: chỉ để các thành phần cần biết vai trò (ví dụ cấp URL tải lên); không tách dữ liệu theo project.
			c.SetRequest(req.WithContext(fhirauth.WithPrincipal(req.Context(), &fhirauth.Principal{Subject: name, Role: role})))
			return next(c)
		}
	}
}

func (a *Authenticator) guardJWT(c echo.Context, next echo.HandlerFunc) error {
	req := c.Request()
	raw := bearer(req)
	if raw == "" {
		c.Response().Header().Set("WWW-Authenticate", `Bearer realm="fhir"`)
		return echo.NewHTTPError(http.StatusUnauthorized, "Thiếu bearer token")
	}
	p, err := a.verifier.Verify(req.Context(), raw)
	switch {
	case errors.Is(err, fhirauth.ErrForbidden):
		slog.WarnContext(req.Context(), "token không dùng được cho FHIR", "error", err.Error())
		return echo.NewHTTPError(http.StatusForbidden, "Token không có quyền truy cập FHIR")
	case err != nil:
		slog.WarnContext(req.Context(), "token bị từ chối", "error", err.Error())
		c.Response().Header().Set("WWW-Authenticate", `Bearer realm="fhir", error="invalid_token"`)
		return echo.NewHTTPError(http.StatusUnauthorized, "Token không hợp lệ")
	}
	if p.Role < RequiredRole(req.Method, req.URL.Path) {
		return echo.NewHTTPError(http.StatusForbidden, "Token không đủ quyền cho thao tác này")
	}
	target := strings.TrimSpace(req.Header.Get(ProjectHeader))
	if !p.Super {
		if target != "" && target != p.Project {
			return echo.NewHTTPError(http.StatusForbidden, "Token không được chọn project khác")
		}
		c.Set(actorKey, p.Subject)
		c.SetRequest(req.WithContext(fhirauth.WithPrincipal(req.Context(), p)))
		return next(c)
	}
	// super-admin: hành động trong project do header chọn; mọi yêu cầu (kể cả bị từ chối) được ghi nhật ký kiểm toán
	if !validProjectID.MatchString(target) {
		return echo.NewHTTPError(http.StatusBadRequest, "Super-admin phải chọn project bằng header "+ProjectHeader)
	}
	sp := *p
	home := sp.Project
	sp.Project = target
	c.Set(actorKey, p.Subject)
	c.SetRequest(req.WithContext(fhirauth.WithPrincipal(req.Context(), &sp)))
	err = next(c)
	status := c.Response().Status
	var he *echo.HTTPError
	if errors.As(err, &he) {
		status = he.Code
	}
	slog.WarnContext(req.Context(), "super-admin truy cập project", "audit", true, "subject", p.Subject, "home_project", home,
		"target_project", target, "method", req.Method, "path", req.URL.Path, "status", status)
	return err
}

// ProjectHeader là header super-admin dùng để chọn project cần thao tác.
const ProjectHeader = "X-Fhir-Project"

var validProjectID = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)
