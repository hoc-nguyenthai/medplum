package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strings"

	"github.com/labstack/echo/v4"

	"trivita-fhir/pkg/fhirauth"
)

// TokenVerifier kiểm tra access token (JWT) và trả người gọi kèm project.
type TokenVerifier interface {
	Verify(ctx context.Context, raw string) (*fhirauth.Principal, error)
}

// ProjectHeader là header super-admin dùng để chọn project cần thao tác (cùng tên với fhir-server).
const ProjectHeader = "X-Fhir-Project"

var validProjectID = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

// requiredRole: đọc (QIDO, WADO) cần RoleRead, ghi (STOW) cần RoleWrite (Q-D1). Quyền theo loại resource (Dicom*, Binary) do fhirauth.Scope kiểm
// ở từng thao tác trên kho.
func requiredRole(method string) fhirauth.Role {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return fhirauth.RoleRead
	}
	return fhirauth.RoleWrite
}

func bearer(r *http.Request) string {
	h := r.Header.Get(echo.HeaderAuthorization)
	const p = "bearer "
	if len(h) > len(p) && strings.EqualFold(h[:len(p)], p) {
		return strings.TrimSpace(h[len(p):])
	}
	return ""
}

// guard kiểm token cho mọi yêu cầu DICOMweb. Thiếu hoặc sai token: 401. Đúng token nhưng thiếu quyền hoặc chọn project khác: 403.
// Tiền tố /projects/{projectId}/dicomweb phải khớp project của token (token thường); super-admin hành động trong project của URL hoặc
// header X-Fhir-Project và mọi yêu cầu của super-admin được ghi nhật ký kiểm toán.
func guard(v TokenVerifier) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			req := c.Request()
			raw := bearer(req)
			if raw == "" {
				c.Response().Header().Set("WWW-Authenticate", `Bearer realm="dicomweb"`)
				return echo.NewHTTPError(http.StatusUnauthorized, "Thiếu bearer token")
			}
			p, err := v.Verify(req.Context(), raw)
			switch {
			case errors.Is(err, fhirauth.ErrForbidden):
				slog.WarnContext(req.Context(), "token không dùng được cho FHIR", "error", err.Error())
				return echo.NewHTTPError(http.StatusForbidden, "Token không có quyền truy cập FHIR")
			case err != nil:
				slog.WarnContext(req.Context(), "token bị từ chối", "error", err.Error())
				c.Response().Header().Set("WWW-Authenticate", `Bearer realm="dicomweb", error="invalid_token"`)
				return echo.NewHTTPError(http.StatusUnauthorized, "Token không hợp lệ")
			}
			if p.Role < requiredRole(req.Method) {
				return echo.NewHTTPError(http.StatusForbidden, "Token không đủ quyền cho thao tác này")
			}
			urlProject := c.Param("projectId")
			header := strings.TrimSpace(req.Header.Get(ProjectHeader))
			if !p.Super {
				if (header != "" && header != p.Project) || (urlProject != "" && urlProject != p.Project) {
					return echo.NewHTTPError(http.StatusForbidden, "Không được dùng project khác")
				}
				c.SetRequest(req.WithContext(fhirauth.WithPrincipal(req.Context(), p)))
				return next(c)
			}
			target := urlProject
			if target == "" {
				target = header
			}
			if urlProject != "" && header != "" && header != urlProject {
				return echo.NewHTTPError(http.StatusBadRequest, "Project ở URL và header "+ProjectHeader+" khác nhau")
			}
			if !validProjectID.MatchString(target) {
				return echo.NewHTTPError(http.StatusBadRequest, "Super-admin phải chọn project bằng URL hoặc header "+ProjectHeader)
			}
			sp := *p
			home := sp.Project
			sp.Project = target
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
	}
}
