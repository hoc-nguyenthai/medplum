package application

import (
	"context"
	"strings"

	"trivita-fhir/apps/auth/internal/domain"
)

// hubScopePrefix là tiền tố scope xin quyền dùng Hub FHIRcast (ví dụ `fhircast/*`).
const hubScopePrefix = "fhircast/"

// hasHubScope cho biết scope (đã cấp, chứ không phải scope client xin) có term FHIRcast.
func hasHubScope(scope string) bool {
	for _, t := range domain.ScopeTerms(scope) {
		if strings.HasPrefix(t, hubScopePrefix) {
			return true
		}
	}
	return false
}

// addHub thêm hub.url và hub.topic vào phản hồi token khi scope ĐÃ CẤP có `fhircast/` và Hub được cấu hình (như Medplum token.ts:1141,
// nhưng dùng scope đã cấp để hai trường luôn khớp với quyền thật trong access token). Không lấy được topic thì không cấp token
// (fail đóng, như Medplum): trả token thiếu hub.topic sẽ làm client im lặng không biết vì sao không đồng bộ được.
func (s *Service) addHub(ctx context.Context, resp TokenResponse, subject, scope string) *domain.OAuthError {
	if s.FHIRcast == nil || s.FHIRcast.Topics == nil || s.FHIRcast.URL == "" || !hasHubScope(scope) {
		return nil
	}
	topic, err := s.FHIRcast.Topics.TopicFor(ctx, subject)
	if err != nil || topic == "" {
		s.Logger.ErrorContext(ctx, "không lấy được topic FHIRcast", "error", err)
		return domain.ErrServer()
	}
	resp["hub.url"], resp["hub.topic"] = s.FHIRcast.URL, topic
	return nil
}
