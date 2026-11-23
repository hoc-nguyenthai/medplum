package domain

import (
	"crypto/sha256"
	"crypto/subtle"

	"trivita-fhir/pkg/fhir"
)

// EqualSecret so hai chuỗi bí mật trong thời gian hằng: băm SHA-256 cả hai rồi so, nên không lộ độ dài (timingSafeEqualStr
// của Medplum thoát sớm khi khác độ dài).
func EqualSecret(a, b string) bool {
	ha, hb := sha256.Sum256([]byte(a)), sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(ha[:], hb[:]) == 1
}

// MatchClientSecret so secret client gửi lên với `secret` hoặc `retiringSecret` (token.ts:1066). Luôn so cả hai vế (so với
// `secret` nếu không có retiringSecret) để thời gian không lộ client có secret cũ hay không. Client không có secret: false.
func MatchClientSecret(c *fhir.ClientApplication, presented string) bool {
	if c.Secret == "" {
		return false
	}
	retiring := c.RetiringSecret
	if retiring == "" {
		retiring = c.Secret
	}
	a := EqualSecret(c.Secret, presented)
	b := EqualSecret(retiring, presented)
	return a || b
}
