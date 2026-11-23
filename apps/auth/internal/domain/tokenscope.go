package domain

import (
	"fmt"
	"regexp"
	"slices"
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
	bad     bool // có entry mà criteria không dịch được sang dạng query thuần của fhir-server
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
	return PolicyClaimWith(policies, PolicyOptions{})
}

// PolicyOptions bật phần dịch mở rộng của P8 (compartment Patient). Mặc định (zero value) là hành vi cũ: tham số hóa `%...` và nhiều criteria khác nhau
// trong một loại đều bị cấm cả loại.
type PolicyOptions struct {
	// Compartment bật: (1) giải `%patient`, `%profile` (và `.id`) trong criteria bằng Vars, thành giá trị cụ thể kiểm định dạng chặt; thiếu hoặc sai định dạng
	// thì cấm cả loại (fail đóng); (2) nhiều criteria khác nhau cùng loại thành nhiều rule (OR). CHỈ bật khi mọi fhir-server đã nâng cấp lên bản hiểu
	// `_compartment` và nhiều rule cùng loại (P8 M1, M2): bản cũ từ chối claim như vậy nên token bị 403.
	Compartment bool
	// Vars là biến của từng AccessPolicy, cùng thứ tự với policies (độ dài khác hoặc nil: không có biến). Khóa viết thường: patient, profile.
	Vars []map[string]string
}

// maxPolicyRules là số rule tối đa phát ra cho một loại (khớp giới hạn của fhirauth.ParsePolicy).
const maxPolicyRules = 16

func PolicyClaimWith(policies []fhir.AccessPolicy, opts PolicyOptions) (claim []map[string]any, narrowed []string, err error) {
	if len(policies) == 0 {
		return nil, nil, nil
	}
	var groups []*policyGroup
	byType := map[string]*policyGroup{}
	for pi, p := range policies {
		if p.Compartment != nil || len(p.BasedOn) > 0 {
			return nil, nil, fmt.Errorf("%s: compartment hoặc basedOn của chính sách", ErrUnsupportedPolicy)
		}
		var vars map[string]string
		if pi < len(opts.Vars) {
			vars = opts.Vars[pi]
		}
		for _, r := range p.Resource {
			if r.ResourceType == "" {
				return nil, nil, fmt.Errorf("%s: entry thiếu resourceType", ErrUnsupportedPolicy)
			}
			badCriteria := false
			if opts.Compartment && strings.Contains(r.Criteria, "%") {
				if resolved, ok := ResolvePolicyCriteria(r.Criteria, vars); ok {
					r.Criteria = resolved
				} else {
					narrowed = append(narrowed, r.ResourceType+": không giải được tham số trong criteria (thiếu, sai định dạng hoặc ngoài danh sách cho phép)")
				}
			}
			g := byType[r.ResourceType]
			if g == nil {
				g = &policyGroup{rt: r.ResourceType}
				byType[r.ResourceType] = g
				groups = append(groups, g)
			}
			// Criteria của AccessPolicy có dạng "Loại?query" (Medplum), còn pkg/fhirauth.ParsePolicy nhận criteria CHỈ gồm query: phải bỏ tiền tố, nếu không
			// token bị từ chối (fail đóng) với mọi chính sách có criteria. Sau khi giải biến (nếu có) để tiền tố cũng được giải.
			if !strings.Contains(r.Criteria, "%") {
				if q, ok := policyQuery(r.ResourceType, r.Criteria); ok {
					r.Criteria = q
				} else {
					badCriteria = true
				}
			}
			g.bad = g.bad || badCriteria
			g.entries = append(g.entries, r)
		}
	}
	claim = []map[string]any{}
	for _, g := range groups {
		unsupported, forcedReadonly := g.bad, false
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
		entries, note := mergeEntries(g, forcedReadonly, opts.Compartment)
		if note != "" {
			narrowed = append(narrowed, note)
		}
		claim = append(claim, entries...)
	}
	return claim, narrowed, nil
}

// mergeEntries gộp các entry (đã hỗ trợ) cùng loại thành một rule, hoặc (multi=true) thành nhiều rule khi các criteria khác nhau và đều không phải
// toàn loại: fhir-server hiểu nhiều rule cùng loại là OR (P8 M2), mọi rule của loại phải cùng `readonly` nên lấy mức hẹp nhất (chỉ đọc nếu BẤT KỲ entry
// nào chỉ đọc).
func mergeEntries(g *policyGroup, forceReadonly, multi bool) ([]map[string]any, string) {
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
		} else if multi {
			if len(criteria) > maxPolicyRules {
				return []map[string]any{{"resourceType": g.rt, "deny": true}}, fmt.Sprintf("cấm %s vì có %d criteria, tối đa %d", g.rt, len(criteria), maxPolicyRules)
			}
			anyReadonly := forceReadonly
			for _, r := range g.entries {
				anyReadonly = anyReadonly || IsTrue(r.Readonly)
			}
			keys := make([]string, 0, len(criteria))
			for c := range criteria {
				keys = append(keys, c)
			}
			slices.Sort(keys)
			rules := make([]map[string]any, 0, len(keys))
			for _, c := range keys {
				e := map[string]any{"resourceType": g.rt, "criteria": c}
				if anyReadonly {
					e["readonly"] = true
				}
				rules = append(rules, e)
			}
			return rules, ""
		} else {
			return []map[string]any{{"resourceType": g.rt, "deny": true}}, "cấm " + g.rt + " vì nhiều criteria khác nhau không gộp được"
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
	return []map[string]any{e}, note
}

// policyQuery trả phần query của criteria dạng "Loại?query"; tiền tố loại phải trùng loại của rule (khác thì không dịch được). Criteria rỗng hoặc đã là query thuần
// thì giữ nguyên.
func policyQuery(rt, criteria string) (string, bool) {
	i := strings.Index(criteria, "?")
	if i < 0 {
		return criteria, true
	}
	if criteria[:i] != rt {
		return "", false
	}
	return criteria[i+1:], true
}

// criteriaVar khớp một biến `%tên` kèm đường dẫn chấm (`%patient`, `%patient.id`).
var criteriaVar = regexp.MustCompile(`%([A-Za-z_][A-Za-z0-9_]*)((?:\.[A-Za-z_][A-Za-z0-9_]*)*)`)

// policyVarFormat là định dạng giá trị cho phép của từng biến. Chặt có chủ ý: giá trị đi thẳng vào chuỗi criteria mà fhir-server phân tích, nên một ký tự
// như `&`, `,`, `|`, `$` hay khoảng trắng có thể nới criteria; chỉ nhận đúng `Loại/id` với id ký tự FHIR.
var policyVarFormat = map[string]*regexp.Regexp{
	"patient": regexp.MustCompile(`^Patient/[A-Za-z0-9.\-]{1,64}$`),
	"profile": regexp.MustCompile(`^(Patient|Practitioner|RelatedPerson)/[A-Za-z0-9.\-]{1,64}$`),
}

// ResolvePolicyCriteria thay `%patient`, `%patient.id`, `%profile`, `%profile.id` trong criteria bằng giá trị trong vars. ok=false (và criteria không dùng được)
// nếu có biến ngoài danh sách, đường dẫn khác `.id`, giá trị thiếu hoặc sai định dạng, hoặc còn sót ký tự `%` sau khi thay.
func ResolvePolicyCriteria(criteria string, vars map[string]string) (string, bool) {
	ok := true
	out := criteriaVar.ReplaceAllStringFunc(criteria, func(m string) string {
		sub := criteriaVar.FindStringSubmatch(m)
		name, path := sub[1], sub[2]
		format, known := policyVarFormat[name]
		if !known || (path != "" && path != ".id") {
			ok = false
			return m
		}
		value := vars[name]
		if !format.MatchString(value) {
			ok = false
			return m
		}
		if path == ".id" {
			return value[strings.Index(value, "/")+1:]
		}
		return value
	})
	return out, ok && !strings.Contains(out, "%")
}
