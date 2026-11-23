package application

import (
	"context"
	"strings"
	"time"

	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
)

// Giới hạn hạn của pre-authorized code (preauthorize.ts).
const (
	DefaultPreAuthTTL = 3600
	MaxPreAuthTTL     = 604800
)

// PreAuthorizeRequest là yêu cầu mint pre-authorized code. Caller là AuthState của Bearer.
type PreAuthorizeRequest struct {
	Caller        *AuthState
	OnBehalfOf    string // giá trị header X-Medplum-On-Behalf-Of
	ClientID      string
	Scope, Nonce  string
	ExpiresIn     int
	RemoteAddress string
	UserAgent     string
}

// PreAuthorizeResult chỉ được trả MỘT lần: kho chỉ giữ SHA-256 của mã.
type PreAuthorizeResult struct {
	PreAuthorizedCode string `json:"preAuthorizedCode" binding:"required" example:"4f9c2e7a1b3d5f60a8c1e2d3b4a59687"`    // mã pre-authorized dùng một lần để đổi token; chỉ trả ở lần này (server chỉ lưu băm)
	ExpiresAt         string `json:"expiresAt" binding:"required" format:"date-time" example:"2026-11-23T10:15:00.000Z"` // thời điểm hết hạn của mã, RFC3339 UTC (now + ttl giây)
}

// resolveOnBehalfOf tìm membership đích của header X-Medplum-On-Behalf-Of (utils.ts:1369): `ProjectMembership/<id>` hoặc tham chiếu profile; cùng project của người gọi,
// còn hoạt động. Mọi thất bại đều là 403 chung (không cho biết membership có tồn tại ở project khác hay không).
func (s *Service) resolveOnBehalfOf(ctx context.Context, caller *AuthState, header string) (*fhir.ProjectMembership, error) {
	projectRef := "Project/" + caller.Project.ID
	var target *fhir.ProjectMembership
	if strings.HasPrefix(header, "ProjectMembership/") {
		m, err := s.Memberships.Get(ctx, strings.TrimPrefix(header, "ProjectMembership/"))
		if err != nil {
			if domain.KindOf(err) != domain.KindNotFound {
				return nil, err
			}
			return nil, domain.Forbidden("Forbidden")
		}
		target = m
	} else {
		m, err := s.Memberships.FindByProfile(ctx, header, projectRef)
		if err != nil {
			return nil, err
		}
		target = m
	}
	if target == nil || target.Project == nil || target.Project.Reference != projectRef || domain.InactiveReason(caller.Project, target) != "" {
		return nil, domain.Forbidden("Forbidden")
	}
	return target, nil
}

// PreAuthorize mint pre-authorized code cho một thành viên khác của project (auth/preauthorize.ts). Chỉ admin của project (không phải super admin).
// Người gọi bị giới hạn SMART chỉ mint được trong phạm vi scope của chính mình. Client phải thuộc project của người gọi (404 nếu không), đọc bằng kho hệ thống
// nên không phụ thuộc quyền đọc của user đích.
func (s *Service) PreAuthorize(ctx context.Context, req PreAuthorizeRequest) (*PreAuthorizeResult, error) {
	c := req.Caller
	if c == nil || c.Project == nil || c.Membership == nil || c.Login == nil {
		return nil, domain.Unauthorized("Unauthorized")
	}
	if domain.IsTrue(c.Project.SuperAdmin) || !domain.IsTrue(c.Membership.Admin) {
		return nil, domain.Forbidden("Forbidden")
	}
	if req.OnBehalfOf == "" {
		return nil, domain.BadRequest("Pre-authorization requires onBehalfOfMembership")
	}
	target, err := s.resolveOnBehalfOf(ctx, c, req.OnBehalfOf)
	if err != nil {
		return nil, err
	}
	callerScope := c.Login.Scope
	var smartTerms []string
	for _, t := range strings.Fields(callerScope) {
		if _, ok := domain.ParseSmartScope(t); ok {
			smartTerms = append(smartTerms, t)
		}
	}
	scope := req.Scope
	if scope == "" {
		scope = strings.Join(append([]string{"openid"}, smartTerms...), " ")
	}
	hasUnheld := len(smartTerms) > 0 && !domain.IsNonSmartScopeSubset(callerScope, scope)
	hasPatientContext := target.Profile != nil && strings.HasPrefix(target.Profile.Reference, "Patient/")
	if hasUnheld || !domain.IsSmartScopeSubset(callerScope, scope, hasPatientContext) {
		return nil, domain.BadRequestAt("Invalid scope", "scope")
	}
	client, err := s.GetClientApplication(ctx, req.ClientID)
	if err != nil {
		return nil, err // NotFound -> 404
	}
	if client.Meta == nil || client.Meta.Project != c.Project.ID {
		return nil, domain.NotFound()
	}
	code := domain.GenerateSecret(128)
	ttl := req.ExpiresIn
	if ttl == 0 {
		ttl = DefaultPreAuthTTL
	}
	now := s.Clock.Now().UTC()
	nonce := req.Nonce
	if nonce == "" {
		nonce = domain.NewID()
	}
	expires := now.Add(time.Duration(ttl) * time.Second).Format("2006-01-02T15:04:05.000Z")
	if _, err := s.Logins.Create(ctx, &fhir.Login{
		ResourceType: "Login", AuthMethod: "pre-authorized", Project: newRef("Project", c.Project.ID, c.Project.Name),
		Client: &fhir.Reference{Reference: "ClientApplication/" + client.ID}, Membership: newRef("ProjectMembership", target.ID, ""), User: target.User,
		AuthTime: now.Format("2006-01-02T15:04:05.000Z"), Scope: scope, Nonce: nonce, RemoteAddress: req.RemoteAddress, UserAgent: req.UserAgent,
		PreAuthorizedCodeHash: domain.HashSecret(code), ExpiresAt: expires,
	}); err != nil {
		s.Logger.ErrorContext(ctx, "tạo Login pre-authorized lỗi", "error", err)
		return nil, domain.Internal("Internal server error")
	}
	return &PreAuthorizeResult{PreAuthorizedCode: code, ExpiresAt: expires}, nil
}
