package application

import (
	"context"
	"regexp"
	"strings"

	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
)

const (
	maxProjectSecrets     = 50
	maxProjectSecretValue = 4096
)

var projectSecretName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,63}$`)

// secretValueKinds đếm số value[x] được đặt của một mục secret.
func secretValueKinds(st fhir.ProjectSetting) int {
	n := 0
	if st.ValueString != "" {
		n++
	}
	if st.ValueBoolean != nil {
		n++
	}
	if st.ValueInteger != nil {
		n++
	}
	if st.ValueDecimal != "" {
		n++
	}
	return n
}

// AdminReplaceSecrets thay TOÀN BỘ Project.secret (POST /admin/projects/:id/secrets). Mỗi mục có tên hợp lệ, không trùng, đúng một value[x] (string tối đa 4096 ký tự).
// Giá trị bị che khi đọc (`********`): gửi lại đúng giá trị che nghĩa là GIỮ giá trị hiện có của secret cùng tên (không có giá trị cũ thì 400), nên admin đổi một secret
// mà không cần biết các secret còn lại. Secret thiếu trong danh sách bị xóa; `[]` xóa hết. Secret `smtp*` chỉ nhận khi máy chủ cho phép SMTP riêng của project
// (AllowProjectSMTP): smtpHost quyết định máy chủ nào ta kết nối tới. Nhật ký kiểm toán chỉ có tên secret, không có giá trị.
func (s *Service) AdminReplaceSecrets(ctx context.Context, a *AuthState, projectID string, in []fhir.ProjectSetting) (*AdminProjectView, error) {
	p, err := adminProject(a, projectID)
	if err != nil {
		return nil, err
	}
	if len(in) > maxProjectSecrets {
		return nil, domain.BadRequestAt("Too many secrets", "secret")
	}
	seen := map[string]bool{}
	for _, st := range in {
		if !projectSecretName.MatchString(st.Name) {
			return nil, domain.BadRequestAt("Secret name must start with a letter and use only letters, digits, '_', '.' or '-' (up to 64 characters)", "secret")
		}
		if seen[st.Name] {
			return nil, domain.BadRequestAt("Duplicate secret: "+st.Name, "secret")
		}
		seen[st.Name] = true
		if secretValueKinds(st) != 1 {
			return nil, domain.BadRequestAt("Secret "+st.Name+" must have exactly one value", "secret")
		}
		if len(st.ValueString) > maxProjectSecretValue {
			return nil, domain.BadRequestAt("Secret value too long: "+st.Name, "secret")
		}
		if strings.HasPrefix(st.Name, "smtp") && !s.Email.AllowProjectSMTP {
			return nil, domain.BadRequestAt("SMTP secrets are not enabled on this server: "+st.Name, "secret")
		}
	}
	out, err := s.Projects.Mutate(ctx, p.ID, func(cur *fhir.Project) error {
		next := make([]fhir.ProjectSetting, len(in))
		copy(next, in)
		for i := range next {
			if !isMask(next[i].ValueString) {
				continue
			}
			var old *fhir.ProjectSetting
			for j := range cur.Secret {
				if cur.Secret[j].Name == next[i].Name {
					old = &cur.Secret[j]
				}
			}
			if old == nil {
				return domain.BadRequestAt("Secret "+next[i].Name+" has no existing value to keep", "secret")
			}
			next[i] = *old
		}
		cur.Secret = next
		return nil
	})
	if err != nil {
		return nil, err
	}
	names := make([]string, len(in))
	for i, st := range in {
		names[i] = st.Name
	}
	s.Logger.WarnContext(ctx, "thay secret của project", "audit", true, "actor", refOf(a.Membership.User), "project_id", p.ID, "secret_names", names)
	return projectView(out), nil
}
