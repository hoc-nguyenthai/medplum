package domain

import (
	"fmt"
	"strings"

	"trivita-fhir/pkg/fhir"
)

// reservedScopePrefix là không gian tên scope dành riêng cho auth: `fhir.read|write|admin|super` là quyền mà fhir-server
// (pkg/fhirauth) cấp theo vai trò. Client không bao giờ được tự khai chúng; Medplum lưu nguyên scope client gửi nên nếu port
// y nguyên thì client_credentials gửi `scope=fhir.admin` sẽ nhận quyền admin.
const reservedScopePrefix = "fhir."

// ScopeTerms tách scope cách nhau bằng dấu cách, bỏ term rỗng.
func ScopeTerms(scope string) []string { return strings.Fields(scope) }

// HasScopeTerm cho biết scope có đúng term (so theo từ, không theo chuỗi con).
func HasScopeTerm(scope, term string) bool {
	for _, t := range ScopeTerms(scope) {
		if t == term {
			return true
		}
	}
	return false
}

// ValidateRequestedScope từ chối scope do client khai mà chứa term trong không gian tên `fhir.` (kể cả viết hoa/thường khác).
func ValidateRequestedScope(scope string) *OAuthError {
	for _, t := range ScopeTerms(scope) {
		if strings.HasPrefix(strings.ToLower(t), reservedScopePrefix) {
			return ErrInvalidScope("Scope " + t + " is reserved")
		}
	}
	return nil
}

// RoleScopes là scope vai trò auth tự thêm vào access token cho fhir-server. Có scope SMART trong login thì KHÔNG thêm gì (quyền
// SMART cộng dồn với `fhir.write`, sẽ phá việc thu hẹp). Ngược lại: admin của project -> fhir.admin; chính sách mà mọi rule chỉ
// đọc -> fhir.read; còn lại fhir.write.
func RoleScopes(loginScope string, membership *fhir.ProjectMembership, policies []fhir.AccessPolicy) []string {
	if HasSmartResourceScopes(loginScope) {
		return nil
	}
	if IsTrue(membership.Admin) {
		return []string{"fhir.admin"}
	}
	if len(policies) > 0 {
		allReadonly, hasRule := true, false
		for _, p := range policies {
			for _, r := range p.Resource {
				hasRule = true
				allReadonly = allReadonly && IsTrue(r.Readonly)
			}
		}
		if hasRule && allReadonly {
			return []string{"fhir.read"}
		}
	}
	return []string{"fhir.write"}
}

// ErrUnsupportedPolicy: AccessPolicy dùng tính năng ở mức chính sách mà fhir-server không thể thu hẹp an toàn. Từ chối cấp token
// (fail đóng) thay vì cấp quyền rộng hơn người quản trị định.
const ErrUnsupportedPolicy = "Access policy not supported"

// policyGroup gom các entry cùng resourceType của (các) AccessPolicy của membership.
type policyGroup struct {
	rt      string
	entries []fhir.AccessPolicyResource
}

// PolicyClaim dịch các AccessPolicy của membership thành claim `fhir_access_policy` ([{resourceType, criteria, readonly, deny}]) cho
// pkg/fhirauth. fhir-server hiểu ít hơn Medplum, nên phép dịch luôn THU HẸP (không bao giờ nới) quyền so với chính sách gốc:
//
//   - compartment hoặc basedOn ở mức chính sách: không dịch được -> lỗi, từ chối cấp token;
//   - entry có hiddenFields, interaction, compartment hoặc tham số hoá `%...`: không dịch được -> cả LOẠI đó bị cấm (`deny`) và không
//     được nhận quyền từ entry `*` (Medplum lấy hiddenFields từ entry khớp đầu tiên; thiếu nó thì `*` sẽ lộ trường ẩn như secret của client);
//   - readonlyFields, writeConstraint: không thực thi được -> loại đó chỉ đọc;
//   - nhiều entry cùng loại: gộp khi cùng criteria (chỉ đọc nếu mọi entry chỉ đọc); khác criteria thì lấy entry toàn loại nếu có, không
//     thì cấm loại đó (fhir-server chỉ nhận một rule mỗi loại);
//   - entry `*` không dịch được thì bỏ `*` (loại không liệt kê thì không có quyền).
//
// narrowed liệt kê các chỗ đã thu hẹp (để ghi log). claim nil khi membership không có chính sách.
func PolicyClaim(policies []fhir.AccessPolicy) (claim []map[string]any, narrowed []string, err error) {
	if len(policies) == 0 {
		return nil, nil, nil
	}
	var groups []*policyGroup
	byType := map[string]*policyGroup{}
	for _, p := range policies {
		if p.Compartment != nil || len(p.BasedOn) > 0 {
			return nil, nil, fmt.Errorf("%s: compartment hoặc basedOn của chính sách", ErrUnsupportedPolicy)
		}
		for _, r := range p.Resource {
			if r.ResourceType == "" {
				return nil, nil, fmt.Errorf("%s: entry thiếu resourceType", ErrUnsupportedPolicy)
			}
			g := byType[r.ResourceType]
			if g == nil {
				g = &policyGroup{rt: r.ResourceType}
				byType[r.ResourceType] = g
				groups = append(groups, g)
			}
			g.entries = append(g.entries, r)
		}
	}
	claim = []map[string]any{}
	for _, g := range groups {
		unsupported, forcedReadonly := false, false
		for _, r := range g.entries {
			if r.Compartment != nil || len(r.HiddenFields) > 0 || len(r.Interaction) > 0 || strings.Contains(r.Criteria, "%") || (g.rt == "*" && r.Criteria != "") {
				unsupported = true
			}
			if len(r.ReadonlyFields) > 0 || len(r.WriteConstraint) > 0 {
				forcedReadonly = true
			}
		}
		switch {
		case unsupported && g.rt == "*":
			narrowed = append(narrowed, `bỏ "*" vì có tính năng không dịch được`)
			continue
		case unsupported:
			narrowed = append(narrowed, "cấm "+g.rt+" vì dùng hiddenFields, interaction, compartment hoặc tham số hoá")
			claim = append(claim, map[string]any{"resourceType": g.rt, "deny": true})
			continue
		}
		if forcedReadonly {
			narrowed = append(narrowed, g.rt+" chỉ đọc vì dùng readonlyFields hoặc writeConstraint")
		}
		entry, note := mergeEntries(g, forcedReadonly)
		if note != "" {
			narrowed = append(narrowed, note)
		}
		claim = append(claim, entry)
	}
	return claim, narrowed, nil
}

// mergeEntries gộp các entry (đã hỗ trợ) cùng loại thành một rule.
func mergeEntries(g *policyGroup, forceReadonly bool) (map[string]any, string) {
	criteria := map[string]bool{}
	for _, r := range g.entries {
		criteria[r.Criteria] = true
	}
	entries := g.entries
	note := ""
	if len(criteria) > 1 {
		if criteria[""] {
			var whole []fhir.AccessPolicyResource
			for _, r := range entries {
				if r.Criteria == "" {
					whole = append(whole, r)
				}
			}
			entries, note = whole, g.rt+": nhiều criteria, chỉ giữ phần toàn loại"
		} else {
			return map[string]any{"resourceType": g.rt, "deny": true}, "cấm " + g.rt + " vì nhiều criteria khác nhau không gộp được"
		}
	}
	readonly := forceReadonly
	if !readonly {
		readonly = true
		for _, r := range entries {
			readonly = readonly && IsTrue(r.Readonly)
		}
	}
	e := map[string]any{"resourceType": g.rt}
	if c := entries[0].Criteria; c != "" && g.rt != "*" {
		e["criteria"] = c
	}
	if readonly {
		e["readonly"] = true
	}
	return e, note
}
