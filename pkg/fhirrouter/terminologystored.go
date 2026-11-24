package fhirrouter

import (
	"context"
	"strconv"

	"trivita-fhir/pkg/fhir"
)

// Terminology trên concept lưu ở ConceptStore (D3): dùng khi CodeSystem.content = not-present và Repository cài ConceptStore.
// Hỗ trợ lookup, $validate-code, và include trong $expand với concept, filter is-a, descendent-of, "=" (code hoặc thuộc tính).
// Không hỗ trợ filter regex trên kho này (trả 422). Mở rộng trọn một bảng mã quá trần maxExpansion thì trả 422 như bình thường;
// dùng filter hoặc danh sách concept để thu hẹp.

// storedConcepts trả kho concept nếu cs nên đọc từ đó.
func storedConcepts(repo Repository, cs *fhir.CodeSystem) (ConceptStore, bool) {
	if cs.Content != "not-present" {
		return nil, false
	}
	st, ok := repo.(ConceptStore)
	return st, ok
}

// hierarchyProperty là thuộc tính chứa code cha (Medplum: hierarchyMeaning, hoặc "parent" khi không khai báo).
func hierarchyProperty(cs *fhir.CodeSystem) string {
	if cs.HierarchyMeaning != "" {
		return cs.HierarchyMeaning
	}
	return "parent"
}

func (r *Router) lookupStored(ctx context.Context, st ConceptStore, cs *fhir.CodeSystem, code string, props map[string]bool) (*Response, error) {
	c, err := st.GetConcept(ctx, cs.ID, code)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, NotFoundMsg("Không có mã " + code + " trong " + cs.URL)
	}
	name := cs.Title
	if name == "" {
		name = cs.Name
	}
	out := &fhir.Parameters{Parameter: []fhir.ParametersParameter{
		{Name: "name", ValueString: name},
		{Name: "display", ValueString: c.Display},
	}}
	if cs.Version != "" {
		out.Parameter = append(out.Parameter, fhir.ParametersParameter{Name: "version", ValueString: cs.Version})
	}
	for _, d := range c.Designations {
		part := []fhir.ParametersParameter{{Name: "value", ValueString: d.Value}}
		if d.Language != "" {
			part = append(part, fhir.ParametersParameter{Name: "language", ValueCode: d.Language})
		}
		out.Parameter = append(out.Parameter, fhir.ParametersParameter{Name: "designation", Part: part})
	}
	h := hierarchyProperty(cs)
	for _, p := range c.Properties {
		pc := p.Property
		if pc == h {
			pc = "parent"
		}
		if len(props) == 0 || props[pc] {
			out.Parameter = append(out.Parameter, propParam(pc, p.Value))
		}
	}
	if len(props) == 0 || props["child"] {
		kids, _, err := st.QueryConcepts(ctx, cs.ID, ConceptQuery{
			Filters: []ConceptFilter{{Op: "=", Property: h, Value: code}}, Limit: maxLookupChildren,
		})
		if err != nil {
			return nil, err
		}
		for _, k := range kids {
			out.Parameter = append(out.Parameter, propParam("child", k.Code))
		}
	}
	return &Response{Status: 200, Resource: out}, nil
}

// maxLookupChildren chặn số "child" trả về cho một concept (nút gốc của bảng mã lớn có thể có rất nhiều con).
const maxLookupChildren = 1000

func (r *Router) validateStored(ctx context.Context, st ConceptStore, cs *fhir.CodeSystem, code, display string) (*Response, error) {
	c, err := st.GetConcept(ctx, cs.ID, code)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return validateResult(false, "", display, "Mã "+code+" không có trong "+cs.URL), nil
	}
	return validateResult(true, c.Display, display, ""), nil
}

// storedIncludeQuery dịch một include (đã biết system lưu ở kho) thành truy vấn. Danh sách concept được ưu tiên hơn filter,
// giống đường đọc trong resource.
func storedIncludeQuery(cs *fhir.CodeSystem, inc fhir.ValueSetComposeInclude) (ConceptQuery, error) {
	q := ConceptQuery{HierarchyProperty: hierarchyProperty(cs)}
	if len(inc.Concept) > 0 {
		for _, c := range inc.Concept {
			q.Codes = append(q.Codes, c.Code)
		}
		return q, nil
	}
	for _, f := range inc.Filter {
		switch {
		case (f.Op == "is-a" || f.Op == "descendent-of") && (f.Property == "concept" || f.Property == "code"):
			q.Filters = append(q.Filters, ConceptFilter{Op: f.Op, Value: f.Value})
		case f.Op == "=" && f.Property != "":
			q.Filters = append(q.Filters, ConceptFilter{Op: "=", Property: f.Property, Value: f.Value})
		default:
			return q, Unprocessable("filter không hỗ trợ: " + f.Property + " " + f.Op)
		}
	}
	return q, nil
}

func (r *Router) storedMembers(ctx context.Context, st ConceptStore, cs *fhir.CodeSystem, inc fhir.ValueSetComposeInclude) ([]member, error) {
	q, err := storedIncludeQuery(cs, inc)
	if err != nil {
		return nil, err
	}
	q.Limit = maxExpansion + 1
	found, _, err := st.QueryConcepts(ctx, cs.ID, q)
	if err != nil {
		return nil, err
	}
	if len(found) == 0 {
		// CodeSystem chưa nạp concept nào thì báo như CodeSystem không có nội dung, thay vì mở rộng ra tập rỗng
		if _, total, err := st.QueryConcepts(ctx, cs.ID, ConceptQuery{Limit: 1}); err != nil {
			return nil, err
		} else if total == 0 {
			return nil, Unprocessable("CodeSystem " + cs.URL + " không có nội dung trên server")
		}
	}
	if len(found) > maxExpansion {
		return nil, Unprocessable("ValueSet quá lớn để mở rộng (tối đa " + strconv.Itoa(maxExpansion) + " mã)")
	}
	given := map[string]string{}
	for _, c := range inc.Concept {
		given[c.Code] = c.Display
	}
	out := make([]member, 0, len(found))
	for _, c := range found {
		d := c.Display
		if g := given[c.Code]; g != "" {
			d = g
		}
		out = append(out, member{cs.URL, cs.Version, c.Code, d})
	}
	return out, nil
}

// storedMembership kiểm một code có thuộc ValueSet mà không mở rộng cả tập. handled=false nghĩa là ValueSet không đủ điều kiện
// (có exclude, tham chiếu ValueSet khác, hoặc có include trên CodeSystem nằm trong resource) và phải mở rộng như thường.
func (r *Router) storedMembership(ctx context.Context, repo Repository, vs *fhir.ValueSet, system, code string) (display string, found, handled bool, err error) {
	if vs.Compose == nil || len(vs.Compose.Exclude) > 0 || len(vs.Compose.Include) == 0 {
		return "", false, false, nil
	}
	type plan struct {
		st ConceptStore
		cs *fhir.CodeSystem
		q  ConceptQuery
	}
	var plans []plan
	for _, inc := range vs.Compose.Include {
		if inc.System == "" || len(inc.ValueSet) > 0 {
			return "", false, false, nil
		}
		u := inc.System
		if inc.Version != "" {
			u += "|" + inc.Version
		}
		res, ferr := r.findByURL(ctx, repo, "CodeSystem", u)
		if ferr != nil {
			return "", false, false, nil
		}
		cs := res.(*fhir.CodeSystem)
		st, ok := storedConcepts(repo, cs)
		if !ok {
			return "", false, false, nil
		}
		q, qerr := storedIncludeQuery(cs, inc)
		if qerr != nil {
			return "", false, false, nil
		}
		plans = append(plans, plan{st, cs, q})
	}
	for _, p := range plans {
		if system != "" && p.cs.URL != system {
			continue
		}
		if len(p.q.Codes) > 0 {
			if !containsString(p.q.Codes, code) {
				continue
			}
		}
		q := p.q
		q.Filters = append(append([]ConceptFilter{}, q.Filters...), ConceptFilter{Op: "=", Property: "code", Value: code})
		q.Codes = nil
		if len(p.q.Codes) > 0 {
			q.Codes = []string{code}
		}
		got, _, qerr := p.st.QueryConcepts(ctx, p.cs.ID, q)
		if qerr != nil {
			return "", false, true, qerr
		}
		if len(got) > 0 {
			return got[0].Display, true, true, nil
		}
	}
	return "", false, true, nil
}

func containsString(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}
