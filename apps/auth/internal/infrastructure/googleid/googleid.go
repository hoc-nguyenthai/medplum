// Package googleid xác minh credential (ID token) của Google Identity Services: JWKS của Google, iss https://accounts.google.com, alg RS256, aud là Google client id.
package googleid

import (
	"context"
	"errors"

	"github.com/golang-jwt/jwt/v5"

	"trivita-fhir/apps/auth/internal/infrastructure/oidc"
)

const (
	// DefaultJWKSURL là JWKS công khai của Google.
	DefaultJWKSURL = "https://www.googleapis.com/oauth2/v3/certs"
	// Issuer là iss của credential (auth/google.ts:31).
	Issuer = "https://accounts.google.com"
)

// Verifier cài application.GoogleVerifier.
type Verifier struct {
	inner   *oidc.IDVerifier
	jwksURL string
}

// New dựng Verifier với JWKS của Google. allowUnsafe và jwksURL chỉ cho test (jwksURL rỗng là DefaultJWKSURL).
func New(allowUnsafe bool, jwksURL string) *Verifier {
	if jwksURL == "" {
		jwksURL = DefaultJWKSURL
	}
	return &Verifier{inner: oidc.NewIDVerifier(allowUnsafe), jwksURL: jwksURL}
}

// Verify chỉ nhận RS256 (Google ký bằng RS256): token ký thuật toán khác bị từ chối trước khi tải JWKS.
func (v *Verifier) Verify(ctx context.Context, credential, audience string) (map[string]any, error) {
	h, _, err := jwt.NewParser().ParseUnverified(credential, jwt.MapClaims{})
	if err != nil {
		return nil, err
	}
	if alg, _ := h.Header["alg"].(string); alg != "RS256" {
		return nil, errors.New("googleid: chỉ nhận RS256")
	}
	return v.inner.VerifyJWKS(ctx, credential, v.jwksURL, Issuer, audience)
}
