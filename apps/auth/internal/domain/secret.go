package domain

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
)

// GenerateSecret trả n byte ngẫu nhiên an toàn mật mã dạng hex (n=16 cho 32 ký tự), như generateSecret của Medplum.
// Lỗi đọc nguồn ngẫu nhiên là không thể phục hồi nên panic (không bao giờ trả secret yếu).
func GenerateSecret(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("domain: không đọc được nguồn ngẫu nhiên: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// ConstantTimeEqual so sánh hai chuỗi bí mật trong thời gian hằng. Cả hai nil thì bằng nhau; một bên nil thì khác;
// khác độ dài thì khác (như timingSafeEqualStr của Medplum).
func ConstantTimeEqual(a, b *string) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	}
	return subtle.ConstantTimeCompare([]byte(*a), []byte(*b)) == 1
}
