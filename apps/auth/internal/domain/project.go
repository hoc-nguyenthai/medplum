package domain

import "trivita-fhir/pkg/fhir"

// Lý do membership hoặc project không hoạt động.
const (
	ProfileNotActive = "Profile not active"
	ProjectNotActive = "Project not active"
)

func isFalse(b *bool) bool { return b != nil && !*b }

// IsTrue: con trỏ bool khác nil và true.
func IsTrue(b *bool) bool { return b != nil && *b }

// InactiveReason trả lý do không hoạt động, hoặc "" nếu cả hai hoạt động. Thiếu `active` nghĩa là hoạt động. Không có
// membership thì chỉ kiểm project (fhir/projectmembership.ts:33).
func InactiveReason(project *fhir.Project, membership *fhir.ProjectMembership) string {
	if membership != nil && isFalse(membership.Active) {
		return ProfileNotActive
	}
	if project != nil && isFalse(project.Active) {
		return ProjectNotActive
	}
	return ""
}

// ProjectHasFeature cho biết project bật tính năng (ví dụ "google-auth-required").
func ProjectHasFeature(p *fhir.Project, feature string) bool {
	if p == nil {
		return false
	}
	for _, f := range p.Features {
		if f == feature {
			return true
		}
	}
	return false
}

// ProjectSetting trả setting theo tên.
func ProjectSetting(p *fhir.Project, name string) *fhir.ProjectSetting {
	if p == nil {
		return nil
	}
	for i := range p.Setting {
		if p.Setting[i].Name == name {
			return &p.Setting[i]
		}
	}
	return nil
}
