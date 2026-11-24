package application

import (
	"context"
	"strings"

	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
)

// Quản trị cấp super-admin qua HTTP (/admin/super/*, tắt mặc định bằng AUTH_SUPER_ADMIN_ENABLED). Khác /admin/projects/* (mỗi admin chỉ quản trị project của
// mình), các route này tác động tới project khác và chỉ dành cho admin của một project được đánh dấu superAdmin (`auth project super`).

// maxSuperProjectFeatures là số tính năng tối đa khi tạo project qua HTTP.
const maxSuperProjectFeatures = 20

// requireSuperAdmin chỉ cho qua admin hoạt động của project superAdmin, qua login không giới hạn bằng scope SMART theo loại resource.
func requireSuperAdmin(a *AuthState) error {
	if a == nil || a.Project == nil || a.Membership == nil || a.Login == nil {
		return domain.Unauthorized("Unauthorized")
	}
	if !domain.IsTrue(a.Project.SuperAdmin) || !domain.IsTrue(a.Membership.Admin) || domain.HasSmartResourceScopes(a.Login.Scope) {
		return errAdminForbidden
	}
	return nil
}

// SuperInitProjectRequest là yêu cầu tạo project (Project/$init).
type SuperInitProjectRequest struct {
	Name string
	// OwnerEmail là email của user toàn cục (không gắn project) sẽ là admin đầu tiên; rỗng là chính người gọi.
	OwnerEmail string
	Features   []string
}

// SuperInitProject tạo project mới cùng AccessPolicy mặc định, client mặc định và membership admin cho chủ sở hữu, theo đúng trình tự của CreateProject
// (fhir/operations/projectinit.ts) trong MỘT transaction. Chủ sở hữu phải là user toàn cục đã tồn tại (không tạo user ở đây: không có mật khẩu để đặt). Trả kết quả
// kèm secret của client mặc định, chỉ hiện một lần. Ghi log kiểm toán (`audit=true`).
func (s *Service) SuperInitProject(ctx context.Context, a *AuthState, req SuperInitProjectRequest) (*ProjectInitResult, error) {
	if err := requireSuperAdmin(a); err != nil {
		return nil, err
	}
	features, err := normalizeFeatures(req.Features)
	if err != nil {
		return nil, err
	}
	ownerUser, err := s.resolveOwner(ctx, a, strings.ToLower(strings.TrimSpace(req.OwnerEmail)))
	if err != nil {
		return nil, err
	}
	var result *ProjectInitResult
	err = s.Tx.InTx(ctx, func(tx TxRepos) error {
		var err error
		result, err = s.CreateProject(ctx, tx.Resources, ProjectInit{Name: req.Name, Admin: ownerUser, Features: features})
		return err
	})
	if err != nil {
		return nil, err
	}
	s.Logger.WarnContext(ctx, "super-admin tạo project", "audit", true, "actor", refOf(a.Membership.User), "actor_project", a.Project.ID,
		"project_id", result.Project.ID, "owner", "User/"+ownerUser.ID)
	return result, nil
}

func (s *Service) resolveOwner(ctx context.Context, a *AuthState, email string) (*fhir.User, error) {
	if email == "" {
		ref, err := callerUserRef(a)
		if err != nil {
			return nil, err
		}
		u, err := s.Users.Get(ctx, idOf(ref))
		if err != nil {
			return nil, err
		}
		return u, ensureGlobalOwner(u)
	}
	u, err := s.FindUserInScope(ctx, email, "")
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, domain.BadRequestAt("Owner not found", "ownerEmail")
	}
	return u, ensureGlobalOwner(u)
}

func ensureGlobalOwner(u *fhir.User) error {
	if u.Project != nil {
		return domain.BadRequestAt("Owner must be a global user (not scoped to a project)", "ownerEmail")
	}
	return nil
}

// normalizeFeatures kiểm tên tính năng (cùng quy tắc với `auth project feature`), bỏ trùng giữ thứ tự, tối đa maxSuperProjectFeatures.
func normalizeFeatures(in []string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, f := range in {
		if !featureName.MatchString(f) {
			return nil, domain.BadRequestAt("Invalid feature name", "features")
		}
		if !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	if len(out) > maxSuperProjectFeatures {
		return nil, domain.BadRequestAt("Too many features", "features")
	}
	return out, nil
}
