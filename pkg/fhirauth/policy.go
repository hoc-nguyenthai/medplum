package fhirauth

import (
	"encoding/json"
	"fmt"
	"strings"

	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirsearch"
)

// Policy là AccessPolicy lấy từ claim của token (giống AccessPolicy.resource của Medplum): liệt kê các loại resource
// được truy cập, mỗi loại có thể kèm tiêu chí search (resource phải thỏa mới thấy/ghi được) và cờ chỉ đọc.
// Policy nil nghĩa là không có chính sách; Policy rỗng (không rule) nghĩa là từ chối mọi loại.
//
// Mỗi loại tối đa một rule. Tiêu chí dùng cú pháp query FHIR ("subject=Patient/123&status=final"), không hỗ trợ
// chained/_has/_include. Policy giao với Grants: một thao tác phải được cả hai cho phép.
type Policy struct {
	rules map[string]rule
	wild  *rule // rule "*": áp cho mọi loại không có rule riêng (trừ wildcardExcluded)
	raw   any   // giá trị claim gốc, để ký vào job nền
	// jobBinary: Policy của Principal job nền, cho phép tạo/xóa Binary (file kết quả) dù chính sách không liệt kê.
	jobBinary bool
}

type rule struct {
	deny     bool // loại bị cấm rõ ràng (kể cả khi có rule "*")
	readonly bool
	req      *fhirsearch.Request // nil: cả loại
}

// wildcardExcluded là các loại mà rule "*" không bao giờ khớp: loại quản trị project và loại bảo vệ của Medplum (access.ts), nên một
// policy "đọc/ghi mọi thứ" không cho chạm vào User, ProjectMembership, AccessPolicy, Login... Muốn truy cập phải có rule riêng.
var wildcardExcluded = map[string]bool{
	"AccessPolicy": true, "Cron": true, "Package": true, "PackageRelease": true, "PackageInstallation": true, "Project": true,
	"ProjectMembership": true, "User": true, "UserSecurityRequest": true,
	"DomainConfiguration": true, "Enterprise": true, "JsonWebKey": true, "Login": true,
}

// ruleFor trả rule áp cho loại rt: rule riêng (nếu deny thì không có), nếu không thì rule "*" (trừ loại bị loại khỏi wildcard).
func (p *Policy) ruleFor(rt string) (rule, bool) {
	if r, ok := p.rules[rt]; ok {
		return r, !r.deny
	}
	if p.wild != nil && !wildcardExcluded[rt] && knownTypes[rt] {
		return *p.wild, true
	}
	return rule{}, false
}

// policyRuleKeys là các khóa hợp lệ của một rule dạng object trong claim chính sách.
var policyRuleKeys = map[string]bool{"resourceType": true, "criteria": true, "readonly": true, "deny": true}

// ParsePolicy dựng Policy từ giá trị claim: mảng phần tử là object {"resourceType","criteria","readonly","deny"} hoặc chuỗi
// "Observation?subject=Patient/123". resourceType "*" (một lần, không có criteria) áp cho mọi loại không có rule riêng, trừ các loại
// quản trị (wildcardExcluded); `"deny": true` cấm loại đó kể cả khi có "*". Rule riêng thay thế (không cộng dồn với) rule "*".
func ParsePolicy(v any) (*Policy, error) {
	list, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("claim chính sách phải là mảng")
	}
	p := &Policy{rules: map[string]rule{}, raw: v}
	for _, e := range list {
		var rt, criteria string
		var ro, deny bool
		switch x := e.(type) {
		case string:
			rt, criteria, _ = strings.Cut(x, "?")
		case map[string]any:
			// Fail đóng: khóa lạ có thể là ràng buộc mới của bản `auth` mới hơn; bỏ qua nó sẽ cấp quyền rộng hơn ý định
			// (rule không criteria = cả loại). Từ chối để token bị 403 thay vì đọc nhầm.
			for k := range x {
				if !policyRuleKeys[k] {
					return nil, fmt.Errorf("khóa không hỗ trợ %q trong rule chính sách", k)
				}
			}
			rt, _ = x["resourceType"].(string)
			criteria, _ = x["criteria"].(string)
			ro, _ = x["readonly"].(bool)
			deny, _ = x["deny"].(bool)
			if d, ok := x["deny"]; ok {
				if _, isBool := d.(bool); !isBool {
					return nil, fmt.Errorf("deny của %s phải là boolean", rt)
				}
			}
			if c, ok := x["criteria"]; ok {
				if _, isStr := c.(string); !isStr {
					return nil, fmt.Errorf("criteria của %s phải là chuỗi", rt)
				}
			}
			if r, ok := x["readonly"]; ok {
				if _, isBool := r.(bool); !isBool {
					return nil, fmt.Errorf("readonly của %s phải là boolean", rt)
				}
			}
		default:
			return nil, fmt.Errorf("phần tử chính sách không hợp lệ")
		}
		if !knownTypes[rt] && rt != "*" {
			return nil, fmt.Errorf("loại resource không biết: %q", rt)
		}
		if _, dup := p.rules[rt]; dup || (rt == "*" && p.wild != nil) {
			return nil, fmt.Errorf("loại %s có nhiều hơn một rule", rt)
		}
		r := rule{readonly: ro, deny: deny}
		if rt == "*" && strings.TrimSpace(criteria) != "" {
			return nil, fmt.Errorf(`rule "*" không có criteria`)
		}
		if rt == "*" && deny {
			return nil, fmt.Errorf(`rule "*" không có deny`)
		}
		if strings.TrimSpace(criteria) != "" {
			req, err := fhirsearch.ParseQuery(rt, strings.TrimPrefix(criteria, "?"))
			if err != nil {
				return nil, fmt.Errorf("criteria của %s: %v", rt, err)
			}
			// Biểu thức duy nhất được phép là do `_compartment=Patient/<id>` sinh ra (Request.Compartment); `_filter` của người dùng thì không.
			if fhirsearch.NeedsResolution(req) || len(req.ExprRaw) > 0 || len(req.Include) > 0 || len(req.Revinclude) > 0 {
				return nil, fmt.Errorf("criteria của %s không được dùng chained/_has/_include, _filter hoặc modifier :in/:above/:below", rt)
			}
			r.req = req
		}
		if rt == "*" {
			p.wild = &r
			continue
		}
		p.rules[rt] = r
	}
	return p, nil
}

// Types liệt kê các loại có rule.
func (p *Policy) Types() []string {
	var out []string
	for _, rt := range fhir.ResourceTypes {
		if _, ok := p.ruleFor(rt); ok {
			out = append(out, rt)
		}
	}
	return out
}

func (p *Policy) allows(rt string, write bool) bool {
	if p == nil || rt == jobType || (p.jobBinary && rt == "Binary" && write) {
		return true
	}
	r, ok := p.ruleFor(rt)
	return ok && !(write && r.readonly)
}

// filters trả điều kiện search cần thêm cho loại rt (nil nếu cả loại).
func (p *Policy) filters(rt string) []fhirsearch.Filter {
	if p == nil {
		return nil
	}
	if r, ok := p.ruleFor(rt); ok && r.req != nil {
		return r.req.Filters
	}
	return nil
}

// exprs trả các biểu thức cần AND thêm cho loại rt (hiện chỉ có biểu thức của `_compartment`); nil nếu không có.
func (p *Policy) exprs(rt string) []*fhirsearch.Expr {
	if p == nil {
		return nil
	}
	if r, ok := p.ruleFor(rt); ok && r.req != nil {
		return r.req.Exprs
	}
	return nil
}

// matches cho biết resource thỏa tiêu chí của rule (loại không có rule: false).
func (p *Policy) matches(res fhir.Resource) bool {
	if p == nil {
		return true
	}
	if res == nil {
		return false
	}
	if res.GetResourceType() == jobType || (p.jobBinary && res.GetResourceType() == "Binary") {
		return true
	}
	r, ok := p.ruleFor(res.GetResourceType())
	if !ok {
		return false
	}
	if r.req == nil {
		return true
	}
	raw, err := json.Marshal(res)
	if err != nil {
		return false
	}
	var doc map[string]any
	if json.Unmarshal(raw, &doc) != nil {
		return false
	}
	return p.matchDoc(res.GetResourceType(), doc)
}

// matchDoc như matches nhưng trên JSON đã parse (doc nil: chỉ khớp khi rule không có tiêu chí).
func (p *Policy) matchDoc(rt string, doc map[string]any) bool {
	if p == nil {
		return true
	}
	r, ok := p.ruleFor(rt)
	if !ok {
		return false
	}
	if r.req == nil {
		return true
	}
	if doc == nil {
		return false
	}
	ok, err := fhirsearch.Match(doc, r.req)
	return err == nil && ok
}
