package application

import (
	"context"

	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
)

// GrantLogin đánh dấu login đã được cấp token (granted=true), sau khi kiểm login đã có membership. Việc ký token thuộc P3.
func (s *Service) GrantLogin(ctx context.Context, login *fhir.Login) (*fhir.Login, error) {
	if login.Membership == nil {
		return nil, domain.BadRequest("Login missing profile")
	}
	if domain.IsTrue(login.Granted) {
		return login, nil
	}
	return s.Logins.Mutate(ctx, login.ID, func(l *fhir.Login) error {
		l.Granted = ptrTrue()
		return nil
	})
}

// RevokeLogin thu hồi một login. Chỉ ghi `revoked` lên bản mới nhất (Medplum ghi cả đối tượng; ở đây không ghi đè thay đổi khác).
func (s *Service) RevokeLogin(ctx context.Context, login *fhir.Login) error {
	_, err := s.Logins.Mutate(ctx, login.ID, func(l *fhir.Login) error {
		if domain.IsTrue(l.Revoked) {
			return ErrNoChange
		}
		l.Revoked = ptrTrue()
		return nil
	})
	return err
}

func ptrTrue() *bool { t := true; return &t }
