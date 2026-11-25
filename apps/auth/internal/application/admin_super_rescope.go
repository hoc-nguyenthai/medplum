package application

import (
	"context"
	"errors"
	"strings"

	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
)

// SuperRescopeUser chuyển một user TOÀN CỤC (không gắn project) thành user của đúng một project (POST /admin/super/users/:id/rescope, tương ứng User/$rescope).
// Dùng khi một tài khoản lẽ ra chỉ thuộc một project (nhờ vậy admin project mới quản trị được: đặt mật khẩu, đổi email, gỡ MFA). Điều kiện: người gọi là super admin; user
// không gắn project nào; user có ít nhất một membership và MỌI membership đều thuộc project đích (còn membership ở project khác thì 400: chuyển sẽ cắt tài khoản
// khỏi project kia); không phải chính người gọi; project đích chưa có user khác cùng email (409). Trong một transaction giữ khóa theo (project, email): ghi User.project
// và meta.project, thu hồi mọi login (phiên cũ mở trong phạm vi cũ). Chỉ chiều server → project; chiều ngược lại không hỗ trợ (email toàn cục có thể trùng giữa các project).
func (s *Service) SuperRescopeUser(ctx context.Context, a *AuthState, userID, projectID string) (*fhir.User, error) {
	if err := requireSuperAdmin(a); err != nil {
		return nil, err
	}
	if strings.TrimSpace(projectID) == "" {
		return nil, domain.BadRequestAt("projectId is required", "projectId")
	}
	callerRef, err := callerUserRef(a)
	if err != nil {
		return nil, err
	}
	if callerRef == "User/"+userID {
		return nil, domain.BadRequest("Cannot rescope the caller")
	}
	if _, err := s.Projects.Get(ctx, projectID); err != nil {
		if domain.KindOf(err) == domain.KindNotFound {
			return nil, domain.BadRequestAt("Invalid projectId", "projectId")
		}
		return nil, err
	}
	user, err := s.Users.Get(ctx, userID)
	if err != nil {
		return nil, err // 404 nếu không có: route chỉ dành cho super admin nên không cần che
	}
	if user.Project != nil {
		return nil, domain.BadRequest("User is already scoped to a project")
	}
	email := strings.ToLower(user.Email)
	var out *fhir.User
	err = s.Tx.InTx(ctx, func(tx TxRepos) error {
		if email != "" {
			if err := tx.Locker.Lock(ctx, "user-email:"+projectID+":"+email); err != nil {
				return err
			}
			clash, err := tx.Users.FindInProject(ctx, email, projectID)
			if err != nil {
				return err
			}
			if clash != nil {
				return domain.Conflict("A user with this email already exists in the project")
			}
		}
		ms, err := tx.Memberships.ListAllForUser(ctx, "User/"+user.ID, 1000)
		if err != nil {
			return err
		}
		if len(ms) == 0 {
			return domain.BadRequest("User has no membership to scope the user to")
		}
		for _, m := range ms {
			if refOf(m.Project) != "Project/"+projectID {
				return domain.BadRequest("User has memberships in other projects")
			}
		}
		out, err = tx.Users.Mutate(ctx, user.ID, func(x *fhir.User) error {
			x.Project = &fhir.Reference{Reference: "Project/" + projectID}
			if x.Meta == nil {
				x.Meta = &fhir.Meta{}
			}
			x.Meta.Project = projectID
			return nil
		})
		if err != nil {
			return err
		}
		return revokeUserLoginsWith(ctx, tx.Logins, user)
	})
	if err != nil {
		if errors.Is(err, domain.ErrVersionConflict) {
			return nil, domain.Conflict("Resource was modified concurrently, retry")
		}
		return nil, err
	}
	s.Logger.WarnContext(ctx, "super-admin chuyển user sang phạm vi project", "audit", true, "actor", callerRef, "actor_project", a.Project.ID, "user_id", user.ID, "project_id", projectID)
	return out, nil
}
