package application

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
)

// ClientAssertionTypeJWTBearer là giá trị client_assertion_type của private_key_jwt (RFC 7523).
const ClientAssertionTypeJWTBearer = "urn:ietf:params:oauth:client-assertion-type:jwt-bearer"

// MaxClientAssertionLifetime là thời hạn tối đa còn lại của assertion tính từ lúc nhận: assertion sống lâu thì jti phải được nhớ lâu,
// và assertion lộ ra dùng được lâu. Medplum không giới hạn.
const MaxClientAssertionLifetime = 5 * time.Minute

// ErrAssertionSignature: chữ ký của assertion không khớp khóa nào trong JWKS của client.
var ErrAssertionSignature = errors.New("application: chữ ký client assertion không hợp lệ")

// ClientAssertionVerifier kiểm chữ ký và các claim iss, sub, aud, exp của assertion bằng JWKS tại jwksURI. Trả lỗi bọc ErrAssertionSignature
// khi mọi khóa đều không khớp chữ ký; mọi lỗi khác (hết hạn, aud sai, không tải được JWKS, URL bị chặn) là lỗi thường.
type ClientAssertionVerifier interface {
	Verify(ctx context.Context, assertion, jwksURI, clientID, tokenURL string) (map[string]any, error)
}

// JTIStore nhớ các jti đã dùng để assertion không phát lại được. UseOnce trả false nếu key đã dùng. Lỗi hạ tầng là lỗi (fail đóng).
type JTIStore interface {
	UseOnce(ctx context.Context, key string, ttl time.Duration) (bool, error)
}

func assertionErr(desc string) *domain.OAuthError { return domain.ErrInvalidRequest(desc) }

// unverifiedPayload giải payload của JWT CHƯA xác minh, chỉ để biết client nào (iss) mà tải JWKS đúng.
func unverifiedPayload(jwt string) (map[string]any, bool) {
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" {
		return nil, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, false
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil || m == nil {
		return nil, false
	}
	return m, true
}

// AuthenticateClientAssertion xác thực client bằng private_key_jwt (token.ts:945-1012). Khác Medplum: bắt buộc exp và exp không quá 5 phút
// kể từ bây giờ, bắt buộc jti và jti chỉ dùng một lần (ghi SAU khi chữ ký đã đúng, để kẻ không có khóa không làm "cháy" jti), JWKS tải qua
// client chặn SSRF (https, không dải nội bộ), và client không cần có `secret`.
func (s *Service) AuthenticateClientAssertion(ctx context.Context, assertionType, assertion, tokenURL string) (*fhir.ClientApplication, *domain.OAuthError) {
	if assertionType != ClientAssertionTypeJWTBearer {
		return nil, assertionErr("Unsupported client assertion type")
	}
	claims, ok := unverifiedPayload(assertion)
	if !ok {
		return nil, assertionErr("Invalid client assertion")
	}
	if aud, _ := claims["aud"].(string); aud != tokenURL { // chuỗi, không phải mảng (như Medplum)
		return nil, assertionErr("Invalid client assertion audience")
	}
	iss, _ := claims["iss"].(string)
	if sub, _ := claims["sub"].(string); iss == "" || iss != sub {
		return nil, assertionErr("Invalid client assertion issuer")
	}
	client, err := s.Clients.Get(ctx, iss)
	if err != nil {
		if domain.KindOf(err) == domain.KindNotFound {
			return nil, assertionErr("Client not found")
		}
		s.Logger.ErrorContext(ctx, "đọc client khi xác thực assertion lỗi", "error", err)
		return nil, domain.ErrServer()
	}
	if client.Status != "" && client.Status != "active" {
		return nil, domain.ErrInvalidRequest("Invalid client")
	}
	if client.JwksUri == "" {
		return nil, assertionErr("Client must have a JWK Set URL")
	}
	if s.Assertions == nil {
		return nil, assertionErr("Unsupported client assertion type")
	}
	verified, err := s.Assertions.Verify(ctx, assertion, client.JwksUri, client.ID, tokenURL)
	if err != nil {
		if errors.Is(err, ErrAssertionSignature) {
			return nil, assertionErr("Invalid client assertion signature")
		}
		s.Logger.WarnContext(ctx, "client assertion bị từ chối", "client", client.ID, "error", err.Error())
		return nil, assertionErr("Invalid client assertion")
	}
	exp, _ := verified["exp"].(float64)
	expires := time.Unix(int64(exp), 0)
	now := s.Clock.Now()
	if exp == 0 || expires.After(now.Add(MaxClientAssertionLifetime)) {
		return nil, assertionErr("Client assertion expiration is too far in the future")
	}
	jti, _ := verified["jti"].(string)
	if jti == "" || len(jti) > 256 {
		return nil, assertionErr("Client assertion must have a jti")
	}
	if s.JTIs == nil {
		return nil, domain.ErrServer()
	}
	fresh, err := s.JTIs.UseOnce(ctx, client.ID+"|"+jti, expires.Sub(now)+time.Minute)
	if err != nil {
		s.Logger.ErrorContext(ctx, "JTIStore lỗi", "error", err)
		return nil, domain.ErrServer()
	}
	if !fresh {
		s.Logger.WarnContext(ctx, "client assertion bị dùng lại", "client", client.ID)
		return nil, assertionErr("Client assertion already used")
	}
	return client, nil
}
