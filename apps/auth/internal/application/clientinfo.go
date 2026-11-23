package application

import (
	"context"

	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
)

// ClientInfo xử lý GET /auth/clientinfo/{clientId} (auth/clientinfo.ts:24): thông tin công khai của form đăng nhập của client. redirectURI (nil: không gửi; chuỗi
// rỗng vẫn là có gửi) phải khớp CHÍNH XÁC một URI đã đăng ký. Mọi trường signInForm là công khai; secret nằm ngoài signInForm nên không lộ. Khác Medplum (Q6): không
// ký lại URL Binary (auth không lưu Binary), giữ nguyên tham chiếu.
func (s *Service) ClientInfo(ctx context.Context, clientID string, redirectURI *string) (*fhir.ClientApplicationSignInForm, error) {
	client, err := s.GetClientApplication(ctx, clientID)
	if err != nil {
		return nil, err
	}
	if redirectURI != nil && domain.MatchRedirectURI(client, *redirectURI) == "" {
		return nil, domain.BadRequest("Invalid redirect URI")
	}
	if client.SignInForm == nil {
		return &fhir.ClientApplicationSignInForm{}, nil
	}
	return client.SignInForm, nil
}
