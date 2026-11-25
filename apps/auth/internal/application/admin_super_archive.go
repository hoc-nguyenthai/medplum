package application

import (
	"context"
	"strings"

	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
)

// SuperSetProjectActive lưu trữ MỀM hoặc khôi phục một project (POST /admin/super/projects/:id/archive và /restore, tương ứng Project/$archive và $restore): đặt
// Project.active=false (archive) hoặc true (restore). Khác Medplum (xuất dữ liệu ra kho ngoài rồi xóa), dữ liệu FHIR giữ nguyên trong cơ sở dữ liệu; project không hoạt động bị chặn
// ở MỌI đường: đăng nhập, cấp và làm mới token, userinfo, bearer của /admin và SCIM đều đọc lại `active` mỗi lần (`Project not active`), nên token đang dùng cũng ngừng có
// hiệu lực ngay. Login không bị thu hồi: restore là phục hồi nguyên trạng. Chỉ super admin; không archive project của chính người gọi (tự khóa mình); lặp lại cùng trạng thái
// không làm gì (vẫn 200). Project không tồn tại là 404 (route chỉ dành cho super admin). Ghi log kiểm toán.
func (s *Service) SuperSetProjectActive(ctx context.Context, a *AuthState, projectID string, active bool) error {
	if err := requireSuperAdmin(a); err != nil {
		return err
	}
	if strings.TrimSpace(projectID) == "" {
		return domain.BadRequestAt("projectId is required", "projectId")
	}
	if !active && projectID == a.Project.ID {
		return domain.BadRequest("Cannot archive the project of the caller")
	}
	changed := false
	_, err := s.Projects.Mutate(ctx, projectID, func(p *fhir.Project) error {
		if isActive := p.Active == nil || *p.Active; isActive == active {
			return ErrNoChange
		}
		p.Active = ptrBool(active)
		changed = true
		return nil
	})
	if err != nil {
		return err
	}
	if changed {
		s.Logger.WarnContext(ctx, "super-admin đổi trạng thái lưu trữ của project", "audit", true, "actor", refOf(a.Membership.User), "actor_project", a.Project.ID, "project_id", projectID, "active", active)
	}
	return nil
}
