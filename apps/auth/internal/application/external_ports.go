package application

import (
	"context"
	"time"

	"trivita-fhir/pkg/fhir"
)

// ExchangeCodeInput là đầu vào đổi authorization code ở tokenUrl của IdP ngoài.
type ExchangeCodeInput struct {
	RedirectURI string
	Code        string
	// CodeVerifier chỉ có khi IdP bật usePkce (xem mục 0.7 của kế hoạch: Medplum gửi chính code_challenge của app làm code_verifier).
	CodeVerifier string
}

// OIDCClient gọi IdP ngoài. Mọi lỗi trả ra là *domain.Error (thông điệp cố định, không lộ nội dung phản hồi của IdP).
type OIDCClient interface {
	// ExchangeCode đổi code lấy id_token.
	ExchangeCode(ctx context.Context, idp *fhir.IdentityProvider, in ExchangeCodeInput) (idToken string, err error)
	// UserInfo gọi userInfoUrl bằng token của IdP và trả claims. Phản hồi application/jwt chỉ được chấp nhận khi chữ ký xác minh được bằng JWKS của IdP (Q7);
	// chế độ userInfoMode=gcip không hỗ trợ (Q9). Lỗi là *domain.Error (429 khi IdP trả 429).
	UserInfo(ctx context.Context, idp *fhir.IdentityProvider, token string) (map[string]any, error)
}

// IDTokenVerifier xác minh id_token của IdP ngoài.
type IDTokenVerifier interface {
	// VerifyJWKS kiểm chữ ký bằng JWKS tại jwksURL (chỉ RS*/ES*), iss, aud và hạn; trả claims.
	VerifyJWKS(ctx context.Context, idToken, jwksURL, issuer, audience string) (map[string]any, error)
	// ParseUnsigned chỉ giải mã payload, KHÔNG kiểm gì: chỉ dùng khi AllowUnsafeOutbound (dev/test).
	ParseUnsigned(idToken string) (map[string]any, error)
}

// ExternalProvider là một IdP ngoài cấu hình ở máy chủ (tra theo issuer của state).
type ExternalProvider struct {
	Issuer           string                 `json:"issuer"`
	ClientID         string                 `json:"clientId,omitempty"`
	UserInfoURL      string                 `json:"userInfoUrl,omitempty"`
	IdentityProvider *fhir.IdentityProvider `json:"identityProvider,omitempty"`
}

// ExternalConfig là cấu hình đăng nhập ngoài. StateKey rỗng thì đăng nhập ngoài TẮT (không phát được state ký, nên callback luôn từ chối).
type ExternalConfig struct {
	Providers []ExternalProvider
	// StateKey khóa HMAC ký `state` (tối thiểu 32 byte). Nhiều instance phải dùng chung khóa.
	StateKey []byte
	// TTL là thời gian sống của state và cookie liên kết (mặc định 10 phút).
	TTL time.Duration
	// AllowUnsafeOutbound cho phép IdP không có jwksUrl (id_token không kiểm chữ ký) và URL http/nội bộ: chỉ dev/test (Q3).
	AllowUnsafeOutbound bool
}

// Enabled cho biết đã có khóa ký state hợp lệ (tối thiểu 32 byte).
func (c ExternalConfig) Enabled() bool { return len(c.StateKey) >= 32 }

// StateTTL là thời gian sống của state (đã áp mặc định).
func (c ExternalConfig) StateTTL() time.Duration { return c.ttl() }

func (c ExternalConfig) ttl() time.Duration {
	if c.TTL <= 0 {
		return 10 * time.Minute
	}
	return c.TTL
}

// OutboundURLCheck kiểm một URL mà auth sẽ GỌI ra ngoài (hoặc chuyển hướng tới): httpsOnly yêu cầu https; blockInternal chặn host là địa chỉ nội bộ theo literal hoặc tên
// chắc chắn là nội bộ (tên miền thường chỉ kiểm được sau khi resolve, lúc kết nối). Cài đặt ở infrastructure/oidc.
type OutboundURLCheck func(raw string, httpsOnly, blockInternal bool) error

// GoogleVerifier xác minh credential (JWT) của Google Identity Services cho một client id.
type GoogleVerifier interface {
	// Verify kiểm chữ ký (RS256, JWKS của Google), iss, aud và hạn; trả claims.
	Verify(ctx context.Context, credential, audience string) (map[string]any, error)
}
