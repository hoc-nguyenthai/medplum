package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"time"

	"trivita-fhir/pkg/fhir"
)

// Loại UserSecurityRequest.
const (
	SecurityRequestReset       = "reset"
	SecurityRequestInvite      = "invite"
	SecurityRequestVerifyEmail = "verify-email"
)

// securityRequestWindow là thời hạn hiệu lực theo loại (constants.ts:62-66). Link đặt lại mật khẩu ngắn vì trao quyền
// kiểm soát tài khoản; mời và xác minh email dài hơn vì người nhận có thể chưa thao tác ngay.
var securityRequestWindow = map[string]time.Duration{
	SecurityRequestReset:       time.Hour,
	SecurityRequestInvite:      7 * 24 * time.Hour,
	SecurityRequestVerifyEmail: 7 * 24 * time.Hour,
}

// MaxSecurityRequestWindow là cửa sổ dài nhất; dùng làm mốc tìm các request còn có thể hiệu lực.
const MaxSecurityRequestWindow = 7 * 24 * time.Hour

// SecurityRequestWindow trả thời hạn theo loại. Request cũ không có `type` được coi là reset (cửa sổ ngắn nhất).
func SecurityRequestWindow(typ string) time.Duration {
	if w, ok := securityRequestWindow[typ]; ok {
		return w
	}
	return securityRequestWindow[SecurityRequestReset]
}

// SecurityRequestExpiration trả mốc hết hạn (RFC 3339, mili giây) cho request loại typ tạo lúc now.
func SecurityRequestExpiration(typ string, now time.Time) string {
	return now.Add(SecurityRequestWindow(typ)).UTC().Format("2006-01-02T15:04:05.000Z")
}

func parseTime(s string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// IsSecurityRequestExpired: hết hạn khi `expiresAt` không đọc được hoặc now >= expiresAt. Thiếu `expiresAt` thì dùng
// meta.lastUpdated + cửa sổ theo loại; không có mốc nào thì coi như đã hết hạn (securityrequest.ts:45-51).
func IsSecurityRequestExpired(r *fhir.UserSecurityRequest, now time.Time) bool {
	var expires time.Time
	var ok bool
	if r.ExpiresAt != "" {
		expires, ok = parseTime(r.ExpiresAt)
	} else if r.Meta != nil && r.Meta.LastUpdated != "" {
		var updated time.Time
		if updated, ok = parseTime(r.Meta.LastUpdated); ok {
			expires = updated.Add(SecurityRequestWindow(r.Type))
		}
	}
	return !ok || !now.Before(expires)
}

// HashSecurityRequestSecret trả SHA-256 hex của secret gốc. Kho chỉ giữ băm (Q3, lệch L4 của P4): kho dữ liệu bị đọc không
// đủ để dùng link đặt lại mật khẩu. Secret gốc chỉ có trong thư.
func HashSecurityRequestSecret(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// SecurityRequestSecretMatches so secret người dùng đưa với băm đã lưu, trong thời gian hằng.
func SecurityRequestSecretMatches(stored, presented string) bool {
	h := HashSecurityRequestSecret(presented)
	return ConstantTimeEqual(&stored, &h)
}
