package httpapi

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"github.com/labstack/echo/v4"

	"trivita-fhir/apps/auth/internal/application"
	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
)

// registerAdmin đăng ký /admin/projects/* (P7). Chỉ Bearer của người dùng (không Basic client); mọi route cần admin của project (kiểm ở application).
func registerAdmin(e *echo.Echo, dep Deps) {
	h := &authHandlers{svc: dep.Auth, cookieSecure: dep.CookieSecure}
	bearer := requireBearer(dep.Authenticator)
	var slow []echo.MiddlewareFunc
	if dep.LoginRateLimit > 0 {
		slow = append(slow, rateLimiter(dep.LoginRateLimit, dep.LoginRateBurst, nil))
	}
	g := e.Group("/admin/projects")
	g.POST("/setpassword", h.adminSetPassword, append([]echo.MiddlewareFunc{bearer}, slow...)...)
	g.GET("/:projectId", h.adminGetProject, bearer)
	g.POST("/:projectId/settings", h.adminSettings, bearer)
	g.POST("/:projectId/client", h.adminClient, bearer)
	g.POST("/:projectId/sites", h.adminSites, bearer)
	g.POST("/:projectId/invite", h.adminInvite, append([]echo.MiddlewareFunc{bearer}, slow...)...)
	g.GET("/:projectId/members/:membershipId", h.adminGetMember, bearer)
	g.POST("/:projectId/members/:membershipId", h.adminUpdateMember, bearer)
	g.DELETE("/:projectId/members/:membershipId", h.adminDeleteMember, bearer)
	g.POST("/:projectId/members/:membershipId/resetpassword", h.adminResetPassword, append([]echo.MiddlewareFunc{bearer}, slow...)...)
	g.POST("/:projectId/members/:membershipId/mfa/reset", h.adminResetMFA, append([]echo.MiddlewareFunc{bearer}, slow...)...)
}

// AdminProjectResponse là body của GET /admin/projects/{projectId}.
type AdminProjectResponse struct {
	Project *application.AdminProjectView `json:"project" binding:"required"` // project của người gọi (secret và khóa bí mật của site đã bị che)
}

// adminGetProject godoc
//
//	@Summary		Đọc project của chính mình (admin)
//	@Description	Chỉ admin của project; projectId khác project của người gọi là 403. Giá trị `secret` và khóa bí mật trong `site` bị che.
//	@Tags			admin
//	@Security		BearerAuth
//	@Produce		application/json,application/fhir+json
//	@Param			projectId	path		string	true	"id project"
//	@Success		200			{object}	AdminProjectResponse
//	@Failure		401			{object}	fhir.OperationOutcome
//	@Failure		403			{object}	fhir.OperationOutcome
//	@Router			/admin/projects/{projectId} [get]
func (h *authHandlers) adminGetProject(c echo.Context) error {
	v, err := h.svc.AdminGetProject(c.Request().Context(), authState(c), c.Param("projectId"))
	if err != nil {
		return writeError(c, err)
	}
	return c.JSON(http.StatusOK, AdminProjectResponse{Project: v})
}

// adminSettings godoc
//
//	@Summary		Đặt các setting của project (allowlist)
//	@Description	Body là mảng ProjectSetting. Chỉ nhận mfaRequired (boolean), allowedMfaMethods (totp,email) và appName; tên khác là 400. Thay các setting thuộc allowlist, giữ nguyên setting ngoài allowlist đang có (ví dụ do vận hành đặt).
//	@Tags			admin
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		application/json,application/fhir+json
//	@Param			projectId	path		string					true	"id project"
//	@Param			body		body		[]fhir.ProjectSetting	true	"Danh sách setting"
//	@Success		200			{object}	AdminProjectResponse
//	@Failure		400			{object}	fhir.OperationOutcome
//	@Failure		401			{object}	fhir.OperationOutcome
//	@Failure		403			{object}	fhir.OperationOutcome
//	@Router			/admin/projects/{projectId}/settings [post]
func (h *authHandlers) adminSettings(c echo.Context) error {
	var in []fhir.ProjectSetting
	if err := json.NewDecoder(c.Request().Body).Decode(&in); err != nil || in == nil {
		return writeError(c, domain.BadRequest("Content could not be parsed"))
	}
	v, err := h.svc.AdminReplaceSettings(c.Request().Context(), authState(c), c.Param("projectId"), in)
	if err != nil {
		return writeError(c, err)
	}
	return c.JSON(http.StatusOK, AdminProjectResponse{Project: v})
}

// AdminClientBody là body của POST /admin/projects/{projectId}/client.
type AdminClientBody struct {
	Name         string          `json:"name" binding:"required" example:"Ứng dụng bệnh nhân"`                                                                       // tên client; bắt buộc, rỗng hoặc chỉ khoảng trắng là 400
	Description  string          `json:"description,omitempty" example:"Ứng dụng di động cho bệnh nhân"`                                                             // mô tả client (tùy chọn)
	RedirectURIs []string        `json:"redirectUris,omitempty" example:"https://app.example.org/callback"`                                                          // các redirect URI hợp lệ cho luồng authorization_code; mỗi URI phải hợp lệ, không thì 400
	PkceOptional bool            `json:"pkceOptional,omitempty" example:"false"`                                                                                     // true: không bắt buộc PKCE cho client này (mặc định false)
	GrantType    []string        `json:"grantType,omitempty" enums:"authorization_code,client_credentials,refresh_token" example:"client_credentials"`               // các grant client được dùng (ngoài ba giá trị này server còn nhận token-exchange và pre-authorized_code); giá trị khác là 400
	Secret       string          `json:"secret,omitempty" minLength:"32" maxLength:"256" example:"0f3c9a1e7b5d4c2a8e6f1b3d5a7c9e0f2b4d6f8a1c3e5b7d9f0a2c4e6b8d0f1a"` // secret do người gọi đặt (32-256 ký tự); bỏ trống thì server sinh 64 ký tự hex; chỉ trả trong phản hồi tạo
	AccessPolicy *fhir.Reference `json:"accessPolicy,omitempty"`                                                                                                     // AccessPolicy áp cho client; phải thuộc project
}

var adminClientFields = map[string]bool{"name": true, "description": true, "redirectUris": true, "pkceOptional": true, "grantType": true, "secret": true, "accessPolicy": true}

// adminClient godoc
//
//	@Summary		Tạo ClientApplication trong project
//	@Description	Tạo client và membership của nó. `secret` (tối thiểu 32 ký tự) do người gọi đặt hoặc server sinh (64 ký tự hex); chỉ được trả trong phản hồi này. Trường ngoài danh sách (identityProvider, signInForm, status, meta...) là 400. accessPolicy phải thuộc project.
//	@Tags			admin
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		application/json,application/fhir+json
//	@Param			projectId	path		string			true	"id project"
//	@Param			body		body		AdminClientBody	true	"Client"
//	@Success		201			{object}	fhir.ClientApplication
//	@Failure		400			{object}	fhir.OperationOutcome
//	@Failure		401			{object}	fhir.OperationOutcome
//	@Failure		403			{object}	fhir.OperationOutcome
//	@Router			/admin/projects/{projectId}/client [post]
func (h *authHandlers) adminClient(c echo.Context) error {
	b, err := readBody(c)
	if err != nil {
		return writeError(c, err)
	}
	var unknown []string
	for k := range b {
		if !adminClientFields[k] {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return writeError(c, domain.BadRequestAt("Unsupported field: "+strings.Join(unknown, ", "), unknown[0]))
	}
	raw, _ := json.Marshal(b)
	var in AdminClientBody
	if err := json.Unmarshal(raw, &in); err != nil {
		return writeError(c, domain.BadRequest("Invalid client body"))
	}
	client, err := h.svc.AdminCreateClient(c.Request().Context(), authState(c), c.Param("projectId"), application.AdminClientRequest{
		Name: in.Name, Description: in.Description, RedirectURIs: in.RedirectURIs, PkceOptional: in.PkceOptional, GrantTypes: in.GrantType, AccessPolicy: in.AccessPolicy, Secret: in.Secret,
	})
	if err != nil {
		return writeError(c, err)
	}
	c.Response().Header().Set("Cache-Control", "no-store")
	return c.JSON(http.StatusCreated, client)
}

// adminGetMember godoc
//
//	@Summary		Đọc một membership của project
//	@Description	Membership không thuộc project hoặc không tồn tại đều là 403.
//	@Tags			admin
//	@Security		BearerAuth
//	@Produce		application/json,application/fhir+json
//	@Param			projectId		path		string	true	"id project"
//	@Param			membershipId	path		string	true	"id membership"
//	@Success		200				{object}	fhir.ProjectMembership
//	@Failure		401				{object}	fhir.OperationOutcome
//	@Failure		403				{object}	fhir.OperationOutcome
//	@Router			/admin/projects/{projectId}/members/{membershipId} [get]
func (h *authHandlers) adminGetMember(c echo.Context) error {
	m, err := h.svc.AdminGetMember(c.Request().Context(), authState(c), c.Param("projectId"), c.Param("membershipId"))
	if err != nil {
		return writeError(c, err)
	}
	return c.JSON(http.StatusOK, m)
}

// adminUpdateMember godoc
//
//	@Summary		Sửa membership
//	@Description	Body là toàn bộ ProjectMembership (resourceType và id khớp đường dẫn, nếu không 403). Chỉ admin, active, externalId, accessPolicy, access được đổi; đổi user, profile hoặc project là 400. accessPolicy phải thuộc project. Không hạ quyền hay vô hiệu hóa admin cuối cùng (400).
//	@Tags			admin
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		application/json,application/fhir+json
//	@Param			projectId		path		string					true	"id project"
//	@Param			membershipId	path		string					true	"id membership"
//	@Param			body			body		fhir.ProjectMembership	true	"Membership"
//	@Success		200				{object}	fhir.ProjectMembership
//	@Failure		400				{object}	fhir.OperationOutcome
//	@Failure		401				{object}	fhir.OperationOutcome
//	@Failure		403				{object}	fhir.OperationOutcome
//	@Failure		409				{object}	fhir.OperationOutcome
//	@Router			/admin/projects/{projectId}/members/{membershipId} [post]
func (h *authHandlers) adminUpdateMember(c echo.Context) error {
	var in fhir.ProjectMembership
	if err := json.NewDecoder(c.Request().Body).Decode(&in); err != nil {
		return writeError(c, domain.BadRequest("Content could not be parsed"))
	}
	m, err := h.svc.AdminUpdateMember(c.Request().Context(), authState(c), c.Param("projectId"), c.Param("membershipId"), &in)
	if err != nil {
		return writeError(c, err)
	}
	return c.JSON(http.StatusOK, m)
}

// adminDeleteMember godoc
//
//	@Summary		Xóa membership
//	@Description	Không xóa chủ project hay admin cuối cùng (400). User thuộc project mà hết membership thì bị xóa cùng; user dùng chung nhiều project thì giữ. Token của membership bị từ chối ngay.
//	@Tags			admin
//	@Security		BearerAuth
//	@Produce		application/fhir+json
//	@Param			projectId		path		string	true	"id project"
//	@Param			membershipId	path		string	true	"id membership"
//	@Success		200				{object}	fhir.OperationOutcome
//	@Failure		400				{object}	fhir.OperationOutcome
//	@Failure		401				{object}	fhir.OperationOutcome
//	@Failure		403				{object}	fhir.OperationOutcome
//	@Router			/admin/projects/{projectId}/members/{membershipId} [delete]
func (h *authHandlers) adminDeleteMember(c echo.Context) error {
	if err := h.svc.AdminDeleteMember(c.Request().Context(), authState(c), c.Param("projectId"), c.Param("membershipId")); err != nil {
		return writeError(c, err)
	}
	return writeOutcome(c, http.StatusOK, outcome("information", "informational", "All OK"))
}

// AdminSetPasswordBody là body của POST /admin/projects/setpassword.
type AdminSetPasswordBody struct {
	Email    string `json:"email" binding:"required" example:"an@example.org"`       // email của user cần đặt mật khẩu; bắt buộc, phải là email hợp lệ
	Password string `json:"password" binding:"required" example:"Matkhau-Moi-2026!"` // mật khẩu mới; bắt buộc, phải qua kiểm tra độ mạnh/rò rỉ (domain.ValidatePassword)
}

// adminSetPassword godoc
//
//	@Summary		Đặt mật khẩu cho user của project
//	@Description	Chỉ user thuộc project của người gọi (user ở phạm vi server là 400 `User not found`). Kiểm mật khẩu bị rò rỉ, thu hồi mọi login của user.
//	@Tags			admin
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		application/fhir+json
//	@Param			body	body		AdminSetPasswordBody	true	"email và mật khẩu mới"
//	@Success		200		{object}	fhir.OperationOutcome
//	@Failure		400		{object}	fhir.OperationOutcome
//	@Failure		401		{object}	fhir.OperationOutcome
//	@Failure		403		{object}	fhir.OperationOutcome
//	@Failure		429		{object}	fhir.OperationOutcome
//	@Router			/admin/projects/setpassword [post]
func (h *authHandlers) adminSetPassword(c echo.Context) error {
	b, err := readBody(c)
	if err != nil {
		return writeError(c, err)
	}
	v := &validationError{}
	if e, ok := b["email"].(string); !ok || !domain.IsEmail(e) {
		v.add("Valid email address is required", "email")
	}
	if err := domain.ValidatePassword(b.str("password")); err != nil {
		v.add(err.Error(), "password")
	}
	if err := v.orNil(); err != nil {
		return writeError(c, err)
	}
	if err := h.svc.AdminSetPassword(c.Request().Context(), authState(c), b.str("email"), b.str("password")); err != nil {
		return writeError(c, err)
	}
	return writeOutcome(c, http.StatusOK, outcome("information", "informational", "All OK"))
}

// adminResetPassword godoc
//
//	@Summary		Gửi thư đặt lại mật khẩu cho một member
//	@Description	Chỉ user thuộc project (user phạm vi server là 403). Khác /auth/resetpassword công khai, lỗi được báo rõ: thiếu email 400, chưa cấu hình thư 400, gửi thư lỗi 500.
//	@Tags			admin
//	@Security		BearerAuth
//	@Produce		application/fhir+json
//	@Param			projectId		path		string	true	"id project"
//	@Param			membershipId	path		string	true	"id membership"
//	@Success		200				{object}	fhir.OperationOutcome
//	@Failure		400				{object}	fhir.OperationOutcome
//	@Failure		401				{object}	fhir.OperationOutcome
//	@Failure		403				{object}	fhir.OperationOutcome
//	@Failure		429		{object}	fhir.OperationOutcome
//	@Router			/admin/projects/{projectId}/members/{membershipId}/resetpassword [post]
func (h *authHandlers) adminResetPassword(c echo.Context) error {
	if err := h.svc.AdminResetMemberPassword(c.Request().Context(), authState(c), c.Param("projectId"), c.Param("membershipId")); err != nil {
		return writeError(c, err)
	}
	return writeOutcome(c, http.StatusOK, outcome("information", "informational", "All OK"))
}

// MFAResetBody là body tùy chọn của POST /admin/projects/{projectId}/members/{membershipId}/mfa/reset (có thể không gửi body). Hiện chỉ để mô tả schema: handler tự đọc body.
type MFAResetBody struct {
	Method string `json:"method,omitempty" enums:"totp,email" example:"totp"` // yếu tố MFA cần gỡ; bỏ trống hoặc null là totp; khác totp/email là 400; user phải đang có yếu tố đó
}

// adminResetMFA godoc
//
//	@Summary		Gỡ một yếu tố MFA của member
//	@Description	Body tùy chọn {method: totp|email} (mặc định totp). User phải đang có yếu tố đó (400). Gỡ totp thì secret bị thay. Chỉ user thuộc project.
//	@Tags			admin
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		application/fhir+json
//	@Param			projectId		path		string	true	"id project"
//	@Param			membershipId	path		string	true	"id membership"
//	@Param			body			body		MFAResetBody	false	"Phương thức MFA cần gỡ (tùy chọn)"
//	@Success		200				{object}	fhir.OperationOutcome
//	@Failure		400				{object}	fhir.OperationOutcome
//	@Failure		401				{object}	fhir.OperationOutcome
//	@Failure		403				{object}	fhir.OperationOutcome
//	@Failure		429		{object}	fhir.OperationOutcome
//	@Router			/admin/projects/{projectId}/members/{membershipId}/mfa/reset [post]
func (h *authHandlers) adminResetMFA(c echo.Context) error {
	var method any
	if c.Request().ContentLength != 0 {
		b, err := readBody(c)
		if err != nil {
			return writeError(c, err)
		}
		if v, ok := b["method"]; ok && v != nil {
			method = v
		}
	}
	if err := h.svc.AdminResetMemberMFA(c.Request().Context(), authState(c), c.Param("projectId"), c.Param("membershipId"), method); err != nil {
		return writeError(c, err)
	}
	return writeOutcome(c, http.StatusOK, outcome("information", "informational", "All OK"))
}

// InviteMembershipBody là phần membership của lời mời.
type InviteMembershipBody struct {
	Profile      *fhir.Reference                `json:"profile,omitempty"`                       // hồ sơ có sẵn để gắn vào membership (dùng thay cho việc tạo hồ sơ mới)
	Admin        *bool                          `json:"admin,omitempty" example:"false"`         // true: cấp quyền admin project cho membership
	Active       *bool                          `json:"active,omitempty" example:"true"`         // trạng thái hoạt động của membership; bỏ trống là mặc định của server
	ExternalID   string                         `json:"externalId,omitempty" example:"hr-10234"` // id ngoài của membership (từ hệ thống nhân sự/IdP); nếu có thì không được rỗng/chỉ khoảng trắng; trùng là 409
	AccessPolicy *fhir.Reference                `json:"accessPolicy,omitempty"`                  // AccessPolicy áp cho membership; phải thuộc project
	Access       []fhir.ProjectMembershipAccess `json:"access,omitempty"`                        // các AccessPolicy tham số hóa gắn thêm cho membership
}

// InviteBody là body của POST /admin/projects/{projectId}/invite. Trường ngoài danh sách (kể cả accessPolicy, access, admin ở mức gốc đã deprecated ở Medplum) là 400.
type InviteBody struct {
	ResourceType       string               `json:"resourceType" binding:"required" enums:"Patient,Practitioner,RelatedPerson" example:"Practitioner"` // loại hồ sơ tạo cho user; bắt buộc
	FirstName          string               `json:"firstName" binding:"required" example:"An"`                                                         // tên; bắt buộc, không được chỉ khoảng trắng
	LastName           string               `json:"lastName" binding:"required" example:"Nguyễn"`                                                      // họ; bắt buộc, không được chỉ khoảng trắng
	Email              string               `json:"email,omitempty" example:"an@example.org"`                                                          // email của user (hạ chữ thường); phải có email hoặc externalId, nếu có thì phải hợp lệ
	ExternalID         string               `json:"externalId,omitempty" example:"hr-10234"`                                                           // id ngoài; phải có email hoặc externalId, nếu có thì không được rỗng/chỉ khoảng trắng
	Patient            *fhir.Reference      `json:"patient,omitempty"`                                                                                 // Patient/<id> mà RelatedPerson liên quan; bắt buộc khi resourceType=RelatedPerson và không có membership.profile
	Password           string               `json:"password,omitempty" example:"Matkhau-Moi-2026!"`                                                    // mật khẩu ban đầu; bỏ trống thì user tự đặt qua thư mời; nếu có phải qua ValidatePassword
	Scope              string               `json:"scope,omitempty" enums:"project,server" example:"project"`                                          // phạm vi của User; bỏ trống thì server chọn (thường server, hoặc project khi project yêu cầu)
	SendEmail          *bool                `json:"sendEmail,omitempty" example:"true"`                                                                // false: không gửi thư mời; bỏ trống là gửi khi có email (cần cấu hình thư, không thì 400)
	Membership         InviteMembershipBody `json:"membership,omitempty"`                                                                              // tùy chọn của ProjectMembership được tạo
	Upsert             bool                 `json:"upsert,omitempty" example:"false"`                                                                  // true: user đã là member thì cập nhật thay vì 409
	ForceNewMembership bool                 `json:"forceNewMembership,omitempty" example:"false"`                                                      // true: luôn tạo membership mới thay vì dùng lại membership hiện có
	MFARequired        *bool                `json:"mfaRequired,omitempty" example:"false"`                                                             // đặt User.mfaRequired cho user mới; bỏ trống là không đặt
}

// adminInvite godoc
//
//	@Summary		Mời user vào project
//	@Description	Tạo (hoặc dùng lại) User, hồ sơ Patient/Practitioner/RelatedPerson và ProjectMembership trong một transaction, rồi gửi thư (trừ sendEmail=false). Cần email hoặc externalId. Practitioner bị chặn theo AUTH_BLOCKED_EMAIL_DOMAINS và setting allowedPractitionerEmailDomain. 409 nếu user đã là member (trừ upsert) hoặc externalId trùng. Gửi thư lỗi là 400 nhưng dữ liệu đã tạo (thông báo nêu rõ membership). Trả ProjectMembership.
//	@Tags			admin
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		application/json,application/fhir+json
//	@Param			projectId	path		string		true	"id project"
//	@Param			body		body		InviteBody	true	"Lời mời"
//	@Success		200			{object}	fhir.ProjectMembership
//	@Failure		400			{object}	fhir.OperationOutcome
//	@Failure		401			{object}	fhir.OperationOutcome
//	@Failure		403			{object}	fhir.OperationOutcome
//	@Failure		409			{object}	fhir.OperationOutcome
//	@Failure		429		{object}	fhir.OperationOutcome
//	@Router			/admin/projects/{projectId}/invite [post]
func (h *authHandlers) adminInvite(c echo.Context) error {
	var in InviteBody
	dec := json.NewDecoder(c.Request().Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		msg := "Content could not be parsed"
		if strings.HasPrefix(err.Error(), "json: unknown field") {
			msg = "Unsupported field: " + strings.Trim(strings.TrimPrefix(err.Error(), "json: unknown field "), `"`)
		} else if te, ok := err.(*json.UnmarshalTypeError); ok {
			msg = "Invalid " + te.Field
		}
		return writeError(c, domain.BadRequest(msg))
	}
	m, err := h.svc.AdminInvite(c.Request().Context(), authState(c), c.Param("projectId"), application.InviteRequest{
		ResourceType: in.ResourceType, FirstName: in.FirstName, LastName: in.LastName, Email: in.Email, ExternalID: in.ExternalID, Patient: in.Patient,
		Password: in.Password, Scope: in.Scope, SendEmail: in.SendEmail, Upsert: in.Upsert, ForceNewMembership: in.ForceNewMembership, MFARequired: in.MFARequired,
		Membership: application.InviteMembership{Profile: in.Membership.Profile, Admin: in.Membership.Admin, Active: in.Membership.Active, ExternalID: in.Membership.ExternalID,
			AccessPolicy: in.Membership.AccessPolicy, Access: in.Membership.Access},
	})
	if err != nil {
		return writeError(c, err)
	}
	return c.JSON(http.StatusOK, m)
}

// adminSites godoc
//
//	@Summary		Đặt các site của project (Google, reCAPTCHA)
//	@Description	Body là mảng ProjectSite; thay toàn bộ Project.site. googleClientId và recaptchaSiteKey phải duy nhất giữa các project (409) và googleClientId không được trùng Google client chính của máy chủ; cặp id/secret phải đi cùng nhau. Phản hồi che khóa bí mật (`********`); gửi lại đúng giá trị che nghĩa là giữ secret hiện có của site cùng tên.
//	@Tags			admin
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		application/json,application/fhir+json
//	@Param			projectId	path		string				true	"id project"
//	@Param			body		body		[]fhir.ProjectSite	true	"Danh sách site"
//	@Success		200			{object}	AdminProjectResponse
//	@Failure		400			{object}	fhir.OperationOutcome
//	@Failure		401			{object}	fhir.OperationOutcome
//	@Failure		403			{object}	fhir.OperationOutcome
//	@Failure		409			{object}	fhir.OperationOutcome
//	@Router			/admin/projects/{projectId}/sites [post]
func (h *authHandlers) adminSites(c echo.Context) error {
	var in []fhir.ProjectSite
	if err := json.NewDecoder(c.Request().Body).Decode(&in); err != nil || in == nil {
		return writeError(c, domain.BadRequest("Content could not be parsed"))
	}
	v, err := h.svc.AdminReplaceSites(c.Request().Context(), authState(c), c.Param("projectId"), in)
	if err != nil {
		return writeError(c, err)
	}
	return c.JSON(http.StatusOK, AdminProjectResponse{Project: v})
}
