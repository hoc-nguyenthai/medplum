// Package clientassertion xác minh client_assertion (private_key_jwt, RFC 7523) bằng JWKS tại jwks_uri của client. JWKS tải qua pkg/safehttp
// (chặn SSRF: https, không dải nội bộ, không theo redirect), có giới hạn kích thước và bộ nhớ đệm.
package clientassertion

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/golang-jwt/jwt/v5"

	"trivita-fhir/apps/auth/internal/application"
	"trivita-fhir/apps/auth/internal/infrastructure/jwks"
)

// Verifier cài đặt application.ClientAssertionVerifier. JWKS do gói jwks tải và nhớ.
type Verifier struct {
	f   *jwks.Fetcher
	now func() time.Time
}

// New dựng Verifier. allowPrivate=true cho phép jwks_uri là http hoặc trỏ vào dải nội bộ (chỉ dev/test).
func New(allowPrivate bool) *Verifier {
	v := &Verifier{f: jwks.New(allowPrivate), now: time.Now}
	v.f.Now = func() time.Time { return v.now() }
	return v
}

func (v *Verifier) keys(ctx context.Context, uri string, force bool) ([]jose.JSONWebKey, error) {
	return v.f.Keys(ctx, uri, force)
}

// Verify cài đặt application.ClientAssertionVerifier. Chữ ký sai ở một khóa thì thử khóa tiếp theo; lỗi khác chữ ký (hết hạn, aud, iss) dừng ngay.
func (v *Verifier) Verify(ctx context.Context, assertion, jwksURI, clientID, tokenURL string) (map[string]any, error) {
	header, _, err := jwt.NewParser().ParseUnverified(assertion, jwt.MapClaims{})
	if err != nil {
		return nil, err
	}
	alg, _ := header.Header["alg"].(string)
	kid, _ := header.Header["kid"].(string)
	if !jwks.Allowed(alg) {
		return nil, fmt.Errorf("thuật toán %q không được hỗ trợ", alg)
	}
	keys, err := v.keys(ctx, jwksURI, false)
	if err != nil {
		return nil, err
	}
	cands := jwks.Candidates(keys, kid, alg)
	if len(cands) == 0 { // khóa mới của client chưa có trong cache: tải lại một lần (có giới hạn tần suất)
		if keys, err = v.keys(ctx, jwksURI, true); err != nil {
			return nil, err
		}
		cands = jwks.Candidates(keys, kid, alg)
	}
	if len(cands) == 0 {
		return nil, application.ErrAssertionSignature
	}
	for _, k := range cands {
		claims := jwt.MapClaims{}
		_, err := jwt.NewParser(
			jwt.WithValidMethods([]string{alg}), jwt.WithIssuer(clientID), jwt.WithSubject(clientID), jwt.WithAudience(tokenURL),
			jwt.WithExpirationRequired(), jwt.WithTimeFunc(v.now),
		).ParseWithClaims(assertion, claims, func(*jwt.Token) (any, error) { return k.Key, nil })
		switch {
		case err == nil:
			return claims, nil
		case errors.Is(err, jwt.ErrTokenSignatureInvalid):
			continue
		default:
			return nil, err
		}
	}
	return nil, application.ErrAssertionSignature
}
