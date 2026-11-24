package httpapi

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"github.com/labstack/echo/v4"

	"trivita-fhir/apps/auth/internal/application"
	"trivita-fhir/apps/auth/internal/domain"
)

// registerSuperAdmin đăng ký /admin/super/* (tắt mặc định bằng AUTH_SUPER_ADMIN_ENABLED). Chỉ Bearer của người dùng; việc kiểm super-admin nằm ở application.
func registerSuperAdmin(e *echo.Echo, dep Deps) {
	h := &authHandlers{svc: dep.Auth, cookieSecure: dep.CookieSecure}
	bearer := requireBearer(dep.Authenticator)
	var slow []echo.MiddlewareFunc
	if dep.LoginRateLimit > 0 {
		slow = append(slow, rateLimiter(dep.rateStore("login:super", dep.LoginRateLimit, dep.LoginRateBurst), nil))
	}
	g := e.Group("/admin/super")
	g.POST("/projects", h.superInitProject, append([]echo.MiddlewareFunc{bearer}, slow...)...)
}

// SuperProjectBody là body của POST /admin/super/projects (Project/$init).
type SuperProjectBody struct {
	Name       string   `json:"name" binding:"required" example:"Bệnh viện B"`    // tên project, 1 đến 72 ký tự; bắt buộc
	OwnerEmail string   `json:"ownerEmail,omitempty" example:"owner@example.org"` // email của user TOÀN CỤC đã tồn tại sẽ là admin đầu tiên; bỏ trống là chính người gọi
	Features   []string `json:"features,omitempty" example:"scim"`                // tính năng bật sẵn cho project (tên chữ thường, số, gạch ngang; tối đa 20)
}

var superProjectFields = map[string]bool{"name": true, "ownerEmail": true, "features": true}

// SuperProjectResponse là body của POST /admin/super/projects.
type SuperProjectResponse struct {
	ProjectID    string `json:"projectId" binding:"required" example:"0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a44"`  // id project mới
	Name         string `json:"name" binding:"required" example:"Bệnh viện B"`                                // tên project
	Owner        string `json:"owner" binding:"required" example:"User/0192f3a4-7b1c-7d2e-9a10-3c5e8f1b2a45"` // tham chiếu user chủ sở hữu
	MembershipID string `json:"membershipId" binding:"required"`                                              // id ProjectMembership admin của chủ sở hữu trong project mới
	ClientID     string `json:"clientId" binding:"required"`                                                  // id client mặc định của project
	ClientSecret string `json:"clientSecret" binding:"required"`                                              // secret của client mặc định; CHỈ hiện trong phản hồi này
}

// superInitProject godoc
//
//	@Summary		Tạo project mới (Project/$init)
//	@Description	Chỉ admin của project được đánh dấu superAdmin (`auth project super`). Tạo project cùng AccessPolicy mặc định, client mặc định và membership admin cho chủ sở hữu trong một transaction. `ownerEmail` phải là user toàn cục đã tồn tại (không gắn project); bỏ trống là chính người gọi. Trường ngoài danh sách là 400. `clientSecret` chỉ hiện một lần. Ghi log kiểm toán. Tắt mặc định (AUTH_SUPER_ADMIN_ENABLED).
//	@Tags			admin
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		application/json,application/fhir+json
//	@Param			body	body		SuperProjectBody	true	"Project"
//	@Success		201		{object}	SuperProjectResponse
//	@Failure		400		{object}	fhir.OperationOutcome
//	@Failure		401		{object}	fhir.OperationOutcome
//	@Failure		403		{object}	fhir.OperationOutcome
//	@Router			/admin/super/projects [post]
func (h *authHandlers) superInitProject(c echo.Context) error {
	b, err := readBody(c)
	if err != nil {
		return writeError(c, err)
	}
	var unknown []string
	for k := range b {
		if !superProjectFields[k] {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return writeError(c, domain.BadRequestAt("Unsupported field: "+strings.Join(unknown, ", "), unknown[0]))
	}
	raw, _ := json.Marshal(b)
	var in SuperProjectBody
	if err := json.Unmarshal(raw, &in); err != nil {
		return writeError(c, domain.BadRequest("Invalid project body"))
	}
	res, err := h.svc.SuperInitProject(c.Request().Context(), authState(c), application.SuperInitProjectRequest{Name: in.Name, OwnerEmail: in.OwnerEmail, Features: in.Features})
	if err != nil {
		return writeError(c, err)
	}
	c.Response().Header().Set("Cache-Control", "no-store")
	return c.JSON(http.StatusCreated, SuperProjectResponse{
		ProjectID: res.Project.ID, Name: res.Project.Name, Owner: res.Project.Owner.Reference, MembershipID: res.Membership.ID,
		ClientID: res.Client.ID, ClientSecret: res.Client.Secret,
	})
}
