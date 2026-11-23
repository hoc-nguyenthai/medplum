package domain

import (
	"regexp"
	"strings"
)

var (
	emailLocal  = regexp.MustCompile("^[A-Za-z0-9!#$%&'*+/=?^_`{|}~-]+(\\.[A-Za-z0-9!#$%&'*+/=?^_`{|}~-]+)*$")
	emailLabel  = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?$`)
	emailTLD    = regexp.MustCompile(`^[A-Za-z]{2,}$`)
	uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
)

// IsEmail kiểm tra email theo cách chặt gần với isEmail mặc định của validator.js: không tên hiển thị, phần local theo
// atext, tên miền có ít nhất một dấu chấm, nhãn chữ-số-gạch ngang không đầu/cuối bằng gạch, TLD toàn chữ cái dài >= 2, tối
// đa 254 ký tự (local <= 64). Khác biệt với validator.js ở các ca hiếm (IDN, địa chỉ IP trong ngoặc) được chấp nhận: ở đây
// từ chối chúng.
func IsEmail(s string) bool {
	if s == "" || len(s) > 254 {
		return false
	}
	at := strings.LastIndex(s, "@")
	if at <= 0 || at == len(s)-1 {
		return false
	}
	local, host := s[:at], s[at+1:]
	if len(local) > 64 || !emailLocal.MatchString(local) {
		return false
	}
	labels := strings.Split(host, ".")
	if len(labels) < 2 || !emailTLD.MatchString(labels[len(labels)-1]) {
		return false
	}
	for _, l := range labels {
		if len(l) > 63 || !emailLabel.MatchString(l) {
			return false
		}
	}
	return true
}

// IsUUID cho biết s có dạng UUID (8-4-4-4-12 hex).
func IsUUID(s string) bool { return uuidPattern.MatchString(s) }
