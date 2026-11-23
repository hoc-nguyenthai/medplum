package application

import (
	"context"

	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
)

// SetLoginScope thu hẹp scope của login (oauth/utils.ts:739): scope mới phải bằng hoặc hẹp hơn scope ban đầu. Chỉ ghi trường
// `scope` lên bản MỚI NHẤT trong kho, nên một lần thu hồi xảy ra sau khi đọc `login` vẫn được giữ.
func (s *Service) SetLoginScope(ctx context.Context, login *fhir.Login, scope string) (*fhir.Login, error) {
	switch {
	case domain.IsTrue(login.Revoked):
		return nil, domain.BadRequest("Login revoked")
	case domain.IsTrue(login.Granted):
		return nil, domain.BadRequest("Login granted")
	case login.Membership == nil:
		return nil, domain.BadRequest("Login profile not set")
	}
	existing := domain.ParseSmartScopes(login.Scope)
	submitted := domain.ParseSmartScopes(scope)
	// Login có scope SMART là bị giới hạn; không được bỏ hết SMART scope để thành không giới hạn.
	if len(existing) > 0 && len(submitted) == 0 {
		return nil, domain.BadRequest("Invalid scope")
	}
	if !domain.IsNonSmartScopeSubset(login.Scope, scope) {
		return nil, domain.BadRequest("Invalid scope")
	}
	for _, candidate := range submitted {
		covered := false
		for _, e := range existing {
			if domain.MatchesScope(e, candidate, false) {
				covered = true
				break
			}
		}
		if !covered {
			return nil, domain.BadRequest("Invalid scope")
		}
	}
	return s.Logins.Mutate(ctx, login.ID, func(l *fhir.Login) error {
		l.Scope = scope
		return nil
	})
}
