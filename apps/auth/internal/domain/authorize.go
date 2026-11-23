package domain

import (
	"net/url"
	"strings"
)

// ValidAudience cho biết tham số `aud` của /oauth2/authorize hợp lệ: vắng thì hợp lệ; có thì phải parse được và cùng scheme và host với một
// trong các địa chỉ cho phép (auth hoặc fhir-server), như isValidAudience của Medplum (authorize.ts:176). SMART đặt `aud` là URL FHIR.
func ValidAudience(aud string, allowed ...string) bool {
	if aud == "" {
		return true
	}
	u, err := url.Parse(aud)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return false
	}
	for _, a := range allowed {
		if a == "" {
			continue
		}
		if b, err := url.Parse(a); err == nil && strings.EqualFold(b.Scheme, u.Scheme) && strings.EqualFold(b.Host, u.Host) {
			return true
		}
	}
	return false
}

// ValidRedirectURI cho biết redirect_uri parse được thành URL tuyệt đối (có scheme). Việc so khớp với client dùng MatchRedirectURI.
func ValidRedirectURI(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme != "" && (u.Host != "" || u.Opaque != "" || u.Path != "")
}

// ParseMaxAge đọc `max_age` (giây): vắng là mặc định 3600; phải là số nguyên không âm. Medplum dùng parseInt nên `abc` ra NaN và làm
// phép so tuổi phiên luôn sai, tức BỎ QUA kiểm tuổi; ở đây giá trị sai là lỗi.
func ParseMaxAge(v string) (seconds int64, ok bool) {
	if v == "" {
		return 3600, true
	}
	var n int64
	for _, r := range v {
		if r < '0' || r > '9' || n > 1<<40 {
			return 0, false
		}
		n = n*10 + int64(r-'0')
	}
	return n, true
}
