package application

import (
	"context"

	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
)

// Recaptcha xác minh token reCAPTCHA với dịch vụ của Google. Lỗi mạng là lỗi (fail đóng), không phải "không phải người máy".
type Recaptcha interface {
	Verify(ctx context.Context, secret, token string) (bool, error)
}

// RecaptchaConfig là khóa reCAPTCHA mức máy chủ. SecretKey rỗng thì bỏ qua hoàn toàn bước kiểm tra ở mức máy chủ; project vẫn có thể
// đặt khóa riêng trong Project.site.
type RecaptchaConfig struct {
	SiteKey, SecretKey string
}

// ValidateRecaptcha là middleware reCAPTCHA của Medplum (auth/utils.ts:649). siteKey khác khóa máy chủ nghĩa là khóa của một project:
// tìm project, lấy secret của nó và (khi checkProject) kiểm project có nhận đăng ký mở. Có secret thì bắt buộc có token hợp lệ.
func (s *Service) ValidateRecaptcha(ctx context.Context, siteKey, projectID, token string, checkProject bool) error {
	secret := s.RecaptchaKeys.SecretKey
	if siteKey != "" && siteKey != s.RecaptchaKeys.SiteKey {
		project, err := s.Projects.FindByRecaptchaSiteKey(ctx, siteKey, projectID)
		if err != nil {
			return err
		}
		if project == nil {
			return domain.BadRequest("Invalid recaptchaSiteKey")
		}
		secret = siteSecret(project, siteKey)
		if secret == "" {
			return domain.BadRequest("Invalid recaptchaSecretKey")
		}
		if checkProject {
			if err := domain.ProjectRegistrationAllowed(project); err != nil {
				return err
			}
		}
	}
	if secret == "" {
		return nil
	}
	if token == "" {
		return domain.BadRequest("Recaptcha token is required")
	}
	if s.Recaptcha == nil {
		return domain.Internal("reCAPTCHA được bật nhưng chưa có bộ xác minh")
	}
	ok, err := s.Recaptcha.Verify(ctx, secret, token)
	if err != nil {
		return domain.InternalErr("xác minh reCAPTCHA", err)
	}
	if !ok {
		return domain.BadRequest("Recaptcha failed")
	}
	return nil
}

func siteSecret(p *fhir.Project, siteKey string) string {
	for _, site := range p.Site {
		if site.RecaptchaSiteKey == siteKey {
			return site.RecaptchaSecretKey
		}
	}
	return ""
}
