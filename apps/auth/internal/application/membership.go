package application

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
)

const invalidAccessPolicy = "Cannot authenticate: Invalid access policy configuration. Please contact your administrator to update your project membership."

// literalReference: tham chiếu `Type/id` đơn lẻ, an toàn làm giá trị search (loại trừ `,` và `\`) (fhir/smart.ts).
var literalReference = regexp.MustCompile(`^[A-Z][A-Za-z]*/[A-Za-z0-9\-.]{1,64}$`)

// patientContext lấy ngữ cảnh Patient từ SMART launch hoặc từ chính profile của membership.
func patientContext(launch *fhir.SmartAppLaunch, membership *fhir.ProjectMembership) string {
	var candidates []*fhir.Reference
	if launch != nil {
		candidates = append(candidates, launch.Patient)
	}
	candidates = append(candidates, membership.Profile)
	for _, c := range candidates {
		if r := refOf(c); literalReference.MatchString(r) && strings.HasPrefix(r, "Patient/") {
			return r
		}
	}
	return ""
}

// accessPolicyRefs gom các AccessPolicy mà membership tham chiếu (accessPolicy và access[].policy).
func accessPolicyRefs(m *fhir.ProjectMembership) []*fhir.Reference {
	var refs []*fhir.Reference
	if m.AccessPolicy != nil {
		refs = append(refs, m.AccessPolicy)
	}
	for _, a := range m.Access {
		if a.Policy != nil {
			refs = append(refs, a.Policy)
		}
	}
	return refs
}

// ResolveIPAccessRules đọc các AccessPolicy của membership và gom ipAccessRule theo thứ tự. Đọc lỗi, hoặc policy thuộc
// project khác, thì từ chối với một thông báo chung (không lộ chi tiết cấu hình khi đăng nhập). Medplum còn thay biến
// (%profile, %patient, tham số) trong cả policy; biến không có tác dụng với ipAccessRule nên bỏ qua. Policy của project
// liên kết (Project.link) chưa được hỗ trợ.
func (s *Service) ResolveIPAccessRules(ctx context.Context, m *fhir.ProjectMembership) ([]fhir.AccessPolicyIpAccessRule, error) {
	projectID := idOf(refOf(m.Project))
	var rules []fhir.AccessPolicyIpAccessRule
	for _, ref := range accessPolicyRefs(m) {
		policy, err := s.AccessPolicies.Get(ctx, idOf(ref.Reference))
		if err != nil || policy == nil || (policy.Meta != nil && policy.Meta.Project != "" && policy.Meta.Project != projectID) {
			return nil, domain.BadRequest(invalidAccessPolicy)
		}
		rules = append(rules, policy.IpAccessRule...)
	}
	return rules, nil
}

// SetLoginMembership chọn membership (profile) cho login, sau khi qua mọi kiểm tra (oauth/utils.ts:510). Bước ghi là
// so-sánh-và-đổi theo phiên bản của login: nhiều request đồng thời cho cùng một login thì đúng một cái thắng.
func (s *Service) SetLoginMembership(ctx context.Context, login *fhir.Login, membership *fhir.ProjectMembership) (*fhir.Login, error) {
	switch {
	case domain.IsTrue(login.Revoked):
		return nil, domain.BadRequest("Login revoked")
	case domain.IsTrue(login.Granted):
		return nil, domain.BadRequest("Login granted")
	case login.Membership != nil:
		return nil, domain.BadRequest("Login profile already set")
	case refOf(membership.User) != refOf(login.User):
		return nil, domain.BadRequest("Invalid profile")
	}
	// Login bắt đầu cho một project cụ thể (qua client hoặc IdP của project đó) chỉ hoàn tất được bằng membership của project đó.
	if lp := refOf(login.Project); lp != "" && lp != "Project/new" && refOf(membership.Project) != lp {
		return nil, domain.BadRequest("Invalid profile")
	}

	if membership.Project == nil {
		return nil, domain.NotFound()
	}
	project, err := s.Projects.Get(ctx, idOf(membership.Project.Reference))
	if err != nil {
		return nil, err
	}
	if reason := domain.InactiveReason(project, membership); reason != "" {
		return nil, domain.BadRequest(reason)
	}
	if domain.ProjectHasFeature(project, "google-auth-required") && login.AuthMethod != "google" {
		return nil, domain.BadRequest("Google authentication is required")
	}

	// Kiểm tra IP và scope SMART qua access policy. (Cập nhật ảnh Google và AuditEvent của Medplum chưa port: ghi log thay thế.)
	var launch *fhir.SmartAppLaunch
	if login.Launch != nil {
		if launch, err = s.Launches.Get(ctx, idOf(login.Launch.Reference)); err != nil {
			return nil, err
		}
	}
	smart := domain.ParseSmartScopes(login.Scope)
	if domain.IsTrue(project.SuperAdmin) && (login.Launch != nil || len(smart) > 0) {
		return nil, domain.BadRequest("SMART scopes are not supported for super admin logins")
	}
	rules, err := s.ResolveIPAccessRules(ctx, membership)
	if err != nil {
		return nil, err
	}
	if patientContext(launch, membership) == "" {
		for _, sc := range smart {
			if sc.PermissionType == "patient" {
				return nil, domain.BadRequest("Missing patient context for patient/ scope")
			}
		}
	}
	if err := domain.CheckIPAccessRules(login.RemoteAddress, rules); err != nil {
		return nil, err
	}

	s.Logger.InfoContext(ctx, "login membership set",
		"login", login.ID, "project", project.ID, "membership", membership.ID, "auth_method", login.AuthMethod)

	updated := *login
	updated.Project = newRef("Project", project.ID, project.Name)
	updated.Membership = newRef("ProjectMembership", membership.ID, "")
	if domain.IsTrue(project.SuperAdmin) {
		updated.RefreshSecret = "" // super admin không có refresh token
	}
	versionID := ""
	if login.Meta != nil {
		versionID = login.Meta.VersionId
	}
	out, err := s.Logins.UpdateIfMatch(ctx, &updated, versionID)
	if errors.Is(err, domain.ErrVersionConflict) {
		return nil, domain.BadRequest("Login profile already set")
	}
	return out, err
}
