package domain

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"regexp"

	"trivita-fhir/pkg/fhir"
)

// verifierFormat là code_verifier theo RFC 7636 §4.1: 43 đến 128 ký tự unreserved.
var verifierFormat = regexp.MustCompile(`^[A-Za-z0-9\-._~]{43,128}$`)

// ChallengeS256 là base64url (không đệm) của SHA-256 của verifier (RFC 7636 §4.2).
func ChallengeS256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// VerifyPKCE kiểm code_verifier với challenge đã lưu trong Login, so hằng thời gian (Medplum so bằng `===`). `S256` luôn được nhận;
// `plain` chỉ khi allowPlain (mặc định tắt, vì plain không chống được việc lộ challenge). Verifier sai định dạng RFC 7636 bị từ chối.
func VerifyPKCE(challenge, method, verifier string, allowPlain bool) bool {
	if challenge == "" || !verifierFormat.MatchString(verifier) {
		return false
	}
	switch method {
	case "S256":
		return EqualSecret(challenge, ChallengeS256(verifier))
	case "plain", "":
		return allowPlain && EqualSecret(challenge, verifier)
	}
	return false
}

// HashSecret là SHA-256 dạng hex của một bí mật ngắn hạn (code, cookie đăng nhập). Kho chỉ giữ băm, nên đọc được Login cũng không dùng
// lại được code (decision D-15). Bí mật có 128 bit ngẫu nhiên nên không cần muối hay hàm băm chậm.
func HashSecret(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// LoginRedirectURIExtension là extension trên Login lưu redirect_uri đã dùng lúc đăng nhập: Medplum không lưu nên không kiểm lại ở
// /oauth2/token (RFC 6749 §4.1.3 yêu cầu khớp), auth lưu và so chính xác.
const LoginRedirectURIExtension = "https://trivita.ai/fhir/StructureDefinition/login-redirect-uri"

// LoginRedirectURI trả redirect_uri đã lưu trong login, hoặc "".
func LoginRedirectURI(l *fhir.Login) string {
	for _, e := range l.Extension {
		if e.URL == LoginRedirectURIExtension {
			return e.ValueUri
		}
	}
	return ""
}

// SetLoginRedirectURI lưu redirect_uri vào login (thay giá trị cũ nếu có).
func SetLoginRedirectURI(l *fhir.Login, uri string) {
	for i, e := range l.Extension {
		if e.URL == LoginRedirectURIExtension {
			l.Extension[i].ValueUri = uri
			return
		}
	}
	l.Extension = append(l.Extension, fhir.Extension{URL: LoginRedirectURIExtension, ValueUri: uri})
}
