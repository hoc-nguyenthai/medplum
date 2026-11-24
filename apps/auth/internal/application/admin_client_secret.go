package application

import (
	"context"

	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
)

// RotateSecretRequest là yêu cầu xoay secret của client (ClientApplication/$rotate-secret).
type RotateSecretRequest struct {
	// Secret (tùy chọn) là secret mới do người gọi đặt (32 đến 256 ký tự); rỗng thì sinh ngẫu nhiên 32 byte (64 ký tự hex).
	Secret string
	// RetireOnly: chỉ bỏ secret cũ đang chờ hết hạn (retiringSecret), không đổi secret hiện tại. Loại trừ với Secret.
	RetireOnly bool
}

// RotateSecretResult là kết quả xoay secret.
type RotateSecretResult struct {
	ClientID string
	// Secret là secret MỚI (chỉ hiện ở lần này); rỗng khi RetireOnly.
	Secret string
	// Retiring cho biết sau thao tác client còn secret cũ dùng được song song (retiringSecret).
	Retiring bool
}

// AdminRotateClientSecret xoay secret của một client trong project của người gọi: secret hiện tại thành `retiringSecret` (vẫn dùng được, để các nơi
// triển khai kịp đổi), secret mới là `secret`; retiringSecret cũ hơn (nếu có) bị bỏ. RetireOnly bỏ retiringSecret mà không đổi secret hiện tại (idempotent).
// Client không có trong project, không có trong kho (client dựng sẵn không nằm trong kho nên cũng vậy) hoặc không phải của người gọi đều là 403 chung, không cho dò id. Client không có
// secret (client công khai) không xoay được (400). Không thu hồi token đã cấp. Ghi log kiểm toán, không bao giờ ghi giá trị secret.
func (s *Service) AdminRotateClientSecret(ctx context.Context, a *AuthState, projectID, clientID string, req RotateSecretRequest) (*RotateSecretResult, error) {
	p, err := adminProject(a, projectID)
	if err != nil {
		return nil, err
	}
	if req.RetireOnly && req.Secret != "" {
		return nil, domain.BadRequestAt("secret and retireOnly are mutually exclusive", "retireOnly")
	}
	if req.Secret != "" && (len(req.Secret) < MinClientSecret || len(req.Secret) > 256) {
		return nil, domain.BadRequestAt("secret must be between 32 and 256 characters", "secret")
	}
	newSecret := ""
	if !req.RetireOnly {
		newSecret = clientSecret(req.Secret)
	}
	var retiring bool
	_, err = s.Clients.Mutate(ctx, clientID, func(c *fhir.ClientApplication) error {
		if c.Meta == nil || c.Meta.Project != p.ID {
			return errAdminForbidden
		}
		if c.Secret == "" {
			return domain.BadRequestAt("Client has no secret to rotate", "clientId")
		}
		if req.RetireOnly {
			retiring = false
			if c.RetiringSecret == "" {
				return ErrNoChange
			}
			c.RetiringSecret = ""
			return nil
		}
		if domain.EqualSecret(newSecret, c.Secret) {
			return domain.BadRequestAt("New secret must differ from the current secret", "secret")
		}
		c.RetiringSecret, c.Secret = c.Secret, newSecret
		retiring = true
		return nil
	})
	if err != nil {
		if domain.KindOf(err) == domain.KindNotFound {
			return nil, errAdminForbidden
		}
		return nil, err
	}
	s.Logger.WarnContext(ctx, "xoay secret của client", "audit", true, "actor", refOf(a.Membership.User), "project_id", p.ID, "client_id", clientID, "retire_only", req.RetireOnly)
	return &RotateSecretResult{ClientID: clientID, Secret: newSecret, Retiring: retiring}, nil
}
