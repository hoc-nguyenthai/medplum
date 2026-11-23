package domain

import (
	"net/url"
	"regexp"
	"strings"
)

// smartScopeFormat: ^(patient|user|system)/(\w+|*).(read|write|c?r?u?d?s?|*)$ (fhir/smart.ts).
var smartScopeFormat = regexp.MustCompile(`^(patient|user|system)/(\w+|\*)\.(read|write|c?r?u?d?s?|\*)$`)

// SmartScope là một scope SMART đã parse (V1 `read`/`write` được chuẩn hóa sang V2 `rs`/`cud`).
type SmartScope struct {
	PermissionType string // patient | user | system
	ResourceType   string // tên loại hoặc "*"
	Scope          string // chuỗi quyền V2, ví dụ "rs", "cruds"
	Params         [][2]string
}

// ParseSmartScope parse một term; ok=false nếu không phải scope SMART (openid, profile, offline_access, launch...).
func ParseSmartScope(term string) (SmartScope, bool) {
	base, query, _ := strings.Cut(term, "?")
	m := smartScopeFormat.FindStringSubmatch(base)
	if m == nil {
		return SmartScope{}, false
	}
	s := SmartScope{PermissionType: m[1], ResourceType: m[2], Scope: normalizeV2(m[3])}
	if query != "" {
		for _, pair := range strings.Split(query, "&") {
			if pair == "" {
				continue
			}
			k, v, _ := strings.Cut(pair, "=")
			ku, errK := url.QueryUnescape(k)
			vu, errV := url.QueryUnescape(v)
			if errK != nil || errV != nil {
				ku, vu = k, v
			}
			s.Params = append(s.Params, [2]string{ku, vu})
		}
	}
	return s, true
}

func normalizeV2(s string) string {
	switch s {
	case "*":
		return "cruds"
	case "read":
		return "rs"
	case "write":
		return "cud"
	}
	return s
}

// ParseSmartScopes parse mọi scope SMART trong chuỗi scope cách nhau bằng dấu cách; bỏ qua term khác.
func ParseSmartScopes(scope string) []SmartScope {
	var out []SmartScope
	if scope == "" {
		return out
	}
	for _, term := range strings.Split(scope, " ") {
		if s, ok := ParseSmartScope(term); ok {
			out = append(out, s)
		}
	}
	return out
}

// HasSmartResourceScopes: login bị giới hạn bởi scope SMART (ví dụ patient/Observation.rs).
func HasSmartResourceScopes(scope string) bool { return len(ParseSmartScopes(scope)) > 0 }

func matchesPermissionType(existing, candidate string, hasPatientContext bool) bool {
	switch {
	case candidate == existing:
		return true
	case candidate == "user":
		return existing == "system"
	}
	return candidate == "patient" && hasPatientContext
}

// MatchesScope cho biết candidate hẹp hơn hoặc bằng existing (oauth/utils.ts:644).
func MatchesScope(existing, candidate SmartScope, hasPatientContext bool) bool {
	if !matchesPermissionType(existing.PermissionType, candidate.PermissionType, hasPatientContext) {
		return false
	}
	if existing.ResourceType != "*" && candidate.ResourceType != existing.ResourceType {
		return false
	}
	// Quyền phải là tập con KHÁC RỖNG: quyền rỗng không hẹp hơn quyền nào.
	if candidate.Scope == "" || len(candidate.Scope) > len(existing.Scope) {
		return false
	}
	for _, c := range candidate.Scope {
		if !strings.ContainsRune(existing.Scope, c) {
			return false
		}
	}
	// Nếu existing có tham số thì candidate phải có ĐÚNG các tham số đó, cùng thứ tự.
	if len(existing.Params) > 0 {
		if len(existing.Params) != len(candidate.Params) {
			return false
		}
		for i, p := range existing.Params {
			if candidate.Params[i] != p {
				return false
			}
		}
	}
	return true
}

// IsSmartScopeSubset: scope yêu cầu không cấp nhiều quyền SMART hơn scope hiện có. Scope hiện có không có SMART là không
// giới hạn nên luôn đúng; ngược lại, scope yêu cầu phải có ít nhất một scope SMART và mỗi cái phải được một scope hiện có
// bao trùm.
func IsSmartScopeSubset(existing, requested string, hasPatientContext bool) bool {
	ex := ParseSmartScopes(existing)
	if len(ex) == 0 {
		return true
	}
	req := ParseSmartScopes(requested)
	if len(req) == 0 {
		return false
	}
	for _, c := range req {
		covered := false
		for _, e := range ex {
			if MatchesScope(e, c, hasPatientContext) {
				covered = true
				break
			}
		}
		if !covered {
			return false
		}
	}
	return true
}

// IsNonSmartScopeSubset: mọi term không phải SMART trong scope yêu cầu, trừ `openid` và term rỗng, phải có trong scope hiện có.
func IsNonSmartScopeSubset(existing, requested string) bool {
	have := map[string]bool{}
	for _, t := range strings.Split(existing, " ") {
		have[t] = true
	}
	for _, t := range strings.Split(requested, " ") {
		if t == "" || t == "openid" {
			continue
		}
		if _, smart := ParseSmartScope(t); smart {
			continue
		}
		if !have[t] {
			return false
		}
	}
	return true
}
