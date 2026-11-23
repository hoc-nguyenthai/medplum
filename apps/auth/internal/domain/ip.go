package domain

import (
	"strings"

	"trivita-fhir/pkg/fhir"
)

// MatchesIPRule: `*` khớp mọi địa chỉ; bằng đúng; hoặc địa chỉ bắt đầu bằng rule (thêm dấu `.` nếu rule chưa kết thúc bằng
// `.`, nên "1.2.3" khớp "1.2.3.44" nhưng không khớp "1.2.30.44"). Đúng như matchesIpAccessRule của Medplum (util/url.ts:208).
func MatchesIPRule(remoteAddress, rule string) bool {
	prefix := rule
	if !strings.HasSuffix(rule, ".") {
		prefix += "."
	}
	return rule == "*" || rule == remoteAddress || strings.HasPrefix(remoteAddress, prefix)
}

// IPNotAllowed là thông điệp khi địa chỉ bị chặn.
const IPNotAllowed = "IP address not allowed"

// CheckIPAccessRules duyệt rule theo thứ tự; rule khớp đầu tiên quyết định: allow thì cho qua, block thì lỗi 400. Không có
// địa chỉ hoặc không có rule thì cho qua. Action khác allow/block bị bỏ qua (như Medplum).
func CheckIPAccessRules(remoteAddress string, rules []fhir.AccessPolicyIpAccessRule) error {
	if remoteAddress == "" || len(rules) == 0 {
		return nil
	}
	for _, r := range rules {
		if !MatchesIPRule(remoteAddress, r.Value) {
			continue
		}
		switch r.Action {
		case "allow":
			return nil
		case "block":
			return BadRequest(IPNotAllowed)
		}
	}
	return nil
}
