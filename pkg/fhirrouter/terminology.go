package fhirrouter

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirsearch"
)

// Terminology trên CodeSystem và ValueSet lưu trong chính server (không có máy chủ thuật ngữ ngoài):
//
//	CodeSystem/$lookup, CodeSystem/{id}/$lookup           system+code (hoặc coding) -> display, thuộc tính, designation
//	CodeSystem/$validate-code                              url+code(+display)
//	ValueSet/$expand, ValueSet/{id}/$expand                url hoặc valueSet; filter, count, offset
//	ValueSet/$validate-code, ValueSet/{id}/$validate-code  url+(code, system | coding | codeableConcept)
//
// Hỗ trợ trong compose.include: system (cả CodeSystem), concept, valueSet (giao với system nếu có), filter
// is-a, descendent-of, "=", regex trên code và thuộc tính; compose.exclude. Không hỗ trợ: CodeSystem ngoài server
// (SNOMED, LOINC không nạp), $subsumes, $translate, $closure, filter other, phiên bản theo ngày.
const maxExpansion = 100000

func (r *Router) registerTerminology() {
	r.registerTerminology2()
	for _, m := range []string{"GET", "POST"} {
		r.Add(m, "CodeSystem/$lookup", r.lookup)
		r.Add(m, "CodeSystem/:id/$lookup", r.lookup)
		r.Add(m, "CodeSystem/$validate-code", r.validateCodeSystem)
		r.Add(m, "CodeSystem/:id/$validate-code", r.validateCodeSystem)
		r.Add(m, "ValueSet/$expand", r.expand)
		r.Add(m, "ValueSet/:id/$expand", r.expand)
		r.Add(m, "ValueSet/$validate-code", r.validateCodeValueSet)
		r.Add(m, "ValueSet/:id/$validate-code", r.validateCodeValueSet)
	}
}

// ---------------------------------------------------------------- tìm resource

func (r *Router) findByURL(ctx context.Context, repo Repository, rt, canonical string) (fhir.Resource, error) {
	u, version, _ := strings.Cut(canonical, "|")
	q := url.Values{"url": {u}, "_count": {"10"}}
	if version != "" {
		q.Set("version", version)
	}
	sr, err := fhirsearch.Parse(rt, q)
	if err != nil {
		return nil, err
	}
	sr.Total = "none"
	res, err := repo.Search(ctx, sr)
	if err != nil {
		return nil, err
	}
	if len(res.Matches) == 0 {
		return nil, NotFoundMsg(rt + " không tồn tại: " + canonical)
	}
	// nhiều phiên bản: lấy phiên bản lớn nhất theo chuỗi (đủ cho semver cùng độ dài và ngày)
	sort.SliceStable(res.Matches, func(i, j int) bool { return versionOf(res.Matches[i]) > versionOf(res.Matches[j]) })
	return res.Matches[0], nil
}

func versionOf(r fhir.Resource) string {
	switch x := r.(type) {
	case *fhir.CodeSystem:
		return x.Version
	case *fhir.ValueSet:
		return x.Version
	}
	return ""
}

func (r *Router) codeSystemFor(ctx context.Context, repo Repository, req *Request, in *Input, urlParams ...string) (*fhir.CodeSystem, error) {
	var res fhir.Resource
	var err error
	if id := req.Params["id"]; id != "" {
		res, err = repo.ReadResource(ctx, "CodeSystem", id)
	} else {
		u := ""
		for _, p := range urlParams {
			if u = in.String(p); u != "" {
				break
			}
		}
		if u == "" {
			return nil, BadRequest("Thiếu tham số " + strings.Join(urlParams, " hoặc "))
		}
		if v := in.String("version"); v != "" && !strings.Contains(u, "|") {
			u += "|" + v
		}
		res, err = r.findByURL(ctx, repo, "CodeSystem", u)
	}
	if err != nil {
		return nil, err
	}
	cs, ok := res.(*fhir.CodeSystem)
	if !ok {
		return nil, NotFound()
	}
	return cs, nil
}

// ---------------------------------------------------------------- chỉ mục khái niệm

type concept struct {
	c      *fhir.CodeSystemConcept
	parent string
}

type csIndex struct {
	cs    *fhir.CodeSystem
	byKey map[string]*concept
	order []string
	kids  map[string][]string
}

func indexCodeSystem(cs *fhir.CodeSystem) *csIndex {
	ix := &csIndex{cs: cs, byKey: map[string]*concept{}, kids: map[string][]string{}}
	var walk func(cs []fhir.CodeSystemConcept, parent string)
	walk = func(list []fhir.CodeSystemConcept, parent string) {
		for i := range list {
			c := &list[i]
			if _, dup := ix.byKey[c.Code]; !dup {
				ix.byKey[c.Code] = &concept{c, parent}
				ix.order = append(ix.order, c.Code)
				if parent != "" {
					ix.kids[parent] = append(ix.kids[parent], c.Code)
				}
			}
			walk(c.Concept, c.Code)
		}
	}
	walk(cs.Concept, "")
	return ix
}

func (ix *csIndex) descendants(code string, self bool) []string {
	var out []string
	if self {
		out = append(out, code)
	}
	var rec func(string)
	rec = func(c string) {
		for _, k := range ix.kids[c] {
			out = append(out, k)
			rec(k)
		}
	}
	rec(code)
	return out
}

func propString(p fhir.CodeSystemConceptProperty) string {
	switch {
	case p.ValueCode != "":
		return p.ValueCode
	case p.ValueString != "":
		return p.ValueString
	case p.ValueCoding != nil:
		return p.ValueCoding.Code
	case p.ValueBoolean != nil:
		if *p.ValueBoolean {
			return "true"
		}
		return "false"
	case p.ValueInteger != nil:
		return itoa(*p.ValueInteger)
	}
	return ""
}

func itoa(n int) string { return strconv.Itoa(n) }

// ---------------------------------------------------------------- $lookup

func (r *Router) lookup(ctx context.Context, req *Request) (*Response, error) {
	in, err := ParseInput(req)
	if err != nil {
		return nil, err
	}
	repo := r.repoOf(ctx)
	code, system := in.String("code"), in.String("system")
	if code == "" {
		if cd := codingParam(in, "coding"); cd != nil {
			code, system = cd.code, cd.system
		}
	}
	if code == "" {
		return nil, BadRequest("Thiếu tham số code (hoặc coding)")
	}
	cs, err := r.codeSystemFor(ctx, repo, req, &Input{values: map[string][]string{"system": {system}, "version": in.values["version"]}}, "system")
	if err != nil {
		return nil, err
	}
	ix := indexCodeSystem(cs)
	c, ok := ix.byKey[code]
	if !ok {
		return nil, NotFoundMsg("Không có mã " + code + " trong " + cs.URL)
	}
	name := cs.Title
	if name == "" {
		name = cs.Name
	}
	out := &fhir.Parameters{Parameter: []fhir.ParametersParameter{
		{Name: "name", ValueString: name},
		{Name: "display", ValueString: c.c.Display},
	}}
	if cs.Version != "" {
		out.Parameter = append(out.Parameter, fhir.ParametersParameter{Name: "version", ValueString: cs.Version})
	}
	if c.c.Definition != "" {
		out.Parameter = append(out.Parameter, fhir.ParametersParameter{Name: "definition", ValueString: c.c.Definition})
	}
	for _, d := range c.c.Designation {
		part := []fhir.ParametersParameter{{Name: "value", ValueString: d.Value}}
		if d.Language != "" {
			part = append(part, fhir.ParametersParameter{Name: "language", ValueCode: d.Language})
		}
		out.Parameter = append(out.Parameter, fhir.ParametersParameter{Name: "designation", Part: part})
	}
	props := map[string]bool{}
	for _, p := range in.List("property") {
		props[p] = true
	}
	if c.parent != "" && (len(props) == 0 || props["parent"]) {
		out.Parameter = append(out.Parameter, propParam("parent", c.parent))
	}
	for _, k := range ix.kids[code] {
		if len(props) == 0 || props["child"] {
			out.Parameter = append(out.Parameter, propParam("child", k))
		}
	}
	for _, p := range c.c.Property {
		if len(props) == 0 || props[p.Code] {
			out.Parameter = append(out.Parameter, propParam(p.Code, propString(p)))
		}
	}
	return &Response{Status: 200, Resource: out}, nil
}

func propParam(code, value string) fhir.ParametersParameter {
	return fhir.ParametersParameter{Name: "property", Part: []fhir.ParametersParameter{
		{Name: "code", ValueCode: code}, {Name: "value", ValueString: value},
	}}
}

type codingValue struct{ system, code, display string }

// codingParam đọc tham số kiểu Coding (hoặc coding đầu tiên của CodeableConcept) từ body Parameters.
func codingParam(in *Input, name string) *codingValue {
	params, _ := in.Body["parameter"].([]any)
	for _, p := range params {
		pm, _ := p.(map[string]any)
		if pm["name"] != name {
			continue
		}
		if c, ok := pm["valueCoding"].(map[string]any); ok {
			return &codingValue{str(c["system"]), str(c["code"]), str(c["display"])}
		}
		if cc, ok := pm["valueCodeableConcept"].(map[string]any); ok {
			cods, _ := cc["coding"].([]any)
			for _, c := range cods {
				if cm, ok := c.(map[string]any); ok {
					return &codingValue{str(cm["system"]), str(cm["code"]), str(cm["display"])}
				}
			}
		}
	}
	return nil
}

func fhirUnmarshal(doc map[string]any) (fhir.Resource, error) {
	raw, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	return fhir.UnmarshalResource(raw)
}

func str(v any) string { s, _ := v.(string); return s }

// ---------------------------------------------------------------- mở rộng ValueSet

type member struct{ system, version, code, display string }

func (m member) key() string { return m.system + "|" + m.code }

func (r *Router) loadValueSet(ctx context.Context, repo Repository, req *Request, in *Input) (*fhir.ValueSet, error) {
	if id := req.Params["id"]; id != "" {
		res, err := repo.ReadResource(ctx, "ValueSet", id)
		if err != nil {
			return nil, err
		}
		vs, ok := res.(*fhir.ValueSet)
		if !ok {
			return nil, NotFound()
		}
		return vs, nil
	}
	if doc, ok := in.Resource("valueSet"); ok {
		res, err := fhirUnmarshal(doc)
		if err != nil {
			return nil, BadRequest("valueSet không hợp lệ: " + err.Error())
		}
		vs, ok := res.(*fhir.ValueSet)
		if !ok {
			return nil, BadRequest("valueSet phải là ValueSet")
		}
		return vs, nil
	}
	u := in.String("url")
	if u == "" {
		return nil, BadRequest("Thiếu tham số url hoặc valueSet")
	}
	if v := in.String("valueSetVersion"); v != "" && !strings.Contains(u, "|") {
		u += "|" + v
	}
	res, err := r.findByURL(ctx, repo, "ValueSet", u)
	if err != nil {
		return nil, err
	}
	return res.(*fhir.ValueSet), nil
}

func (r *Router) expandMembers(ctx context.Context, repo Repository, vs *fhir.ValueSet, visiting map[string]bool) ([]member, error) {
	if vs.Compose == nil {
		if vs.Expansion != nil { // đã có expansion sẵn
			var out []member
			var walk func([]fhir.ValueSetExpansionContains)
			walk = func(l []fhir.ValueSetExpansionContains) {
				for _, c := range l {
					if c.Code != "" {
						out = append(out, member{c.System, c.Version, c.Code, c.Display})
					}
					walk(c.Contains)
				}
			}
			walk(vs.Expansion.Contains)
			return out, nil
		}
		return nil, Unprocessable("ValueSet không có compose")
	}
	if vs.URL != "" {
		if visiting[vs.URL] {
			return nil, Unprocessable("ValueSet tham chiếu vòng: " + vs.URL)
		}
		visiting[vs.URL] = true
		defer delete(visiting, vs.URL)
	}
	var out []member
	seen := map[string]bool{}
	for _, inc := range vs.Compose.Include {
		ms, err := r.includeMembers(ctx, repo, inc, visiting)
		if err != nil {
			return nil, err
		}
		for _, m := range ms {
			if !seen[m.key()] {
				seen[m.key()] = true
				out = append(out, m)
			}
		}
	}
	if len(vs.Compose.Exclude) > 0 {
		drop := map[string]bool{}
		for _, exc := range vs.Compose.Exclude {
			ms, err := r.includeMembers(ctx, repo, exc, visiting)
			if err != nil {
				return nil, err
			}
			for _, m := range ms {
				drop[m.key()] = true
			}
		}
		kept := out[:0]
		for _, m := range out {
			if !drop[m.key()] {
				kept = append(kept, m)
			}
		}
		out = kept
	}
	if len(out) > maxExpansion {
		return nil, Unprocessable("ValueSet quá lớn để mở rộng (tối đa " + strconv.Itoa(maxExpansion) + " mã)")
	}
	return out, nil
}

func (r *Router) includeMembers(ctx context.Context, repo Repository, inc fhir.ValueSetComposeInclude, visiting map[string]bool) ([]member, error) {
	var base []member
	if inc.System != "" {
		u := inc.System
		if inc.Version != "" {
			u += "|" + inc.Version
		}
		res, err := r.findByURL(ctx, repo, "CodeSystem", u)
		if err != nil {
			return nil, Unprocessable("CodeSystem không có trên server: " + u)
		}
		cs := res.(*fhir.CodeSystem)
		if cs.Content == "not-present" {
			return nil, Unprocessable("CodeSystem " + cs.URL + " không có nội dung trên server")
		}
		ix := indexCodeSystem(cs)
		mk := func(code string) member {
			c := ix.byKey[code]
			return member{cs.URL, cs.Version, code, c.c.Display}
		}
		switch {
		case len(inc.Concept) > 0:
			for _, c := range inc.Concept {
				m := member{cs.URL, cs.Version, c.Code, c.Display}
				if cc, ok := ix.byKey[c.Code]; ok && m.display == "" {
					m.display = cc.c.Display
				}
				base = append(base, m)
			}
		case len(inc.Filter) > 0:
			codes := ix.order
			for _, f := range inc.Filter {
				var err error
				if codes, err = applyFilter(ix, codes, f); err != nil {
					return nil, err
				}
			}
			for _, c := range codes {
				base = append(base, mk(c))
			}
		default:
			for _, c := range ix.order {
				base = append(base, mk(c))
			}
		}
	}
	if len(inc.ValueSet) > 0 {
		var inter map[string]member
		for _, ref := range inc.ValueSet {
			res, err := r.findByURL(ctx, repo, "ValueSet", ref)
			if err != nil {
				return nil, Unprocessable("ValueSet không có trên server: " + ref)
			}
			ms, err := r.expandMembers(ctx, repo, res.(*fhir.ValueSet), visiting)
			if err != nil {
				return nil, err
			}
			cur := map[string]member{}
			for _, m := range ms {
				if inter == nil || hasKey(inter, m.key()) {
					cur[m.key()] = m
				}
			}
			inter = cur
		}
		if inc.System == "" {
			for _, m := range inter {
				base = append(base, m)
			}
			sort.SliceStable(base, func(i, j int) bool { return base[i].key() < base[j].key() })
		} else {
			kept := base[:0]
			for _, m := range base {
				if hasKey(inter, m.key()) {
					kept = append(kept, m)
				}
			}
			base = kept
		}
	}
	return base, nil
}

func hasKey(m map[string]member, k string) bool { _, ok := m[k]; return ok }

func applyFilter(ix *csIndex, codes []string, f fhir.ValueSetComposeIncludeFilter) ([]string, error) {
	in := map[string]bool{}
	for _, c := range codes {
		in[c] = true
	}
	var out []string
	keep := func(c string) {
		if in[c] {
			out = append(out, c)
		}
	}
	switch {
	case f.Op == "is-a" && (f.Property == "concept" || f.Property == "code"):
		for _, c := range ix.descendants(f.Value, true) {
			keep(c)
		}
	case f.Op == "descendent-of" && (f.Property == "concept" || f.Property == "code"):
		for _, c := range ix.descendants(f.Value, false) {
			keep(c)
		}
	case f.Op == "regex":
		re, err := regexp.Compile("^(?:" + f.Value + ")$")
		if err != nil {
			return nil, BadRequest("filter regex không hợp lệ: " + err.Error())
		}
		for _, c := range codes {
			v := c
			if f.Property != "code" && f.Property != "concept" {
				v = ""
				for _, p := range ix.byKey[c].c.Property {
					if p.Code == f.Property && re.MatchString(propString(p)) {
						v = c
					}
				}
				if v == "" {
					continue
				}
				out = append(out, c)
				continue
			}
			if re.MatchString(v) {
				out = append(out, c)
			}
		}
	case f.Op == "=" && f.Property != "":
		for _, c := range codes {
			if f.Property == "code" || f.Property == "concept" {
				if c == f.Value {
					out = append(out, c)
				}
				continue
			}
			for _, p := range ix.byKey[c].c.Property {
				if p.Code == f.Property && propString(p) == f.Value {
					out = append(out, c)
					break
				}
			}
		}
	default:
		return nil, Unprocessable("filter không hỗ trợ: " + f.Property + " " + f.Op)
	}
	return out, nil
}

func (r *Router) expand(ctx context.Context, req *Request) (*Response, error) {
	in, err := ParseInput(req)
	if err != nil {
		return nil, err
	}
	repo := r.repoOf(ctx)
	vs, err := r.loadValueSet(ctx, repo, req, in)
	if err != nil {
		return nil, err
	}
	count, err := in.Int("count", 0)
	if err != nil {
		return nil, err
	}
	offset, err := in.Int("offset", 0)
	if err != nil {
		return nil, err
	}
	if count < 0 || offset < 0 {
		return nil, BadRequest("count và offset phải >= 0")
	}
	ms, err := r.expandMembers(ctx, repo, vs, map[string]bool{})
	if err != nil {
		return nil, err
	}
	if f := strings.ToLower(strings.TrimSpace(in.String("filter"))); f != "" {
		kept := ms[:0:0]
		for _, m := range ms {
			if strings.Contains(strings.ToLower(m.display), f) || strings.Contains(strings.ToLower(m.code), f) {
				kept = append(kept, m)
			}
		}
		ms = kept
	}
	total := len(ms)
	if offset > len(ms) {
		offset = len(ms)
	}
	ms = ms[offset:]
	if count > 0 && count < len(ms) {
		ms = ms[:count]
	}
	exp := &fhir.ValueSetExpansion{Timestamp: time.Now().UTC().Format(time.RFC3339), Total: &total, Offset: &offset}
	for _, m := range ms {
		exp.Contains = append(exp.Contains, fhir.ValueSetExpansionContains{System: m.system, Version: m.version, Code: m.code, Display: m.display})
	}
	out := *vs
	out.Compose, out.Expansion = nil, exp
	return &Response{Status: 200, Resource: &out}, nil
}

// ---------------------------------------------------------------- $validate-code

func (r *Router) validateCodeValueSet(ctx context.Context, req *Request) (*Response, error) {
	in, err := ParseInput(req)
	if err != nil {
		return nil, err
	}
	repo := r.repoOf(ctx)
	vs, err := r.loadValueSet(ctx, repo, req, in)
	if err != nil {
		return nil, err
	}
	code, system, display := in.String("code"), in.String("system"), in.String("display")
	if code == "" {
		cd := codingParam(in, "coding")
		if cd == nil {
			cd = codingParam(in, "codeableConcept")
		}
		if cd == nil {
			return nil, BadRequest("Thiếu code, coding hoặc codeableConcept")
		}
		code, system, display = cd.code, cd.system, cd.display
	}
	ms, err := r.expandMembers(ctx, repo, vs, map[string]bool{})
	if err != nil {
		return nil, err
	}
	for _, m := range ms {
		if m.code == code && (system == "" || m.system == system) {
			return validateResult(true, m.display, display, ""), nil
		}
	}
	return validateResult(false, "", display, "Mã "+code+" không thuộc ValueSet"), nil
}

func (r *Router) validateCodeSystem(ctx context.Context, req *Request) (*Response, error) {
	in, err := ParseInput(req)
	if err != nil {
		return nil, err
	}
	repo := r.repoOf(ctx)
	code, display := in.String("code"), in.String("display")
	if code == "" {
		cd := codingParam(in, "coding")
		if cd == nil {
			return nil, BadRequest("Thiếu code hoặc coding")
		}
		code, display = cd.code, cd.display
	}
	cs, err := r.codeSystemFor(ctx, repo, req, in, "url", "system")
	if err != nil {
		return nil, err
	}
	c, ok := indexCodeSystem(cs).byKey[code]
	if !ok {
		return validateResult(false, "", display, "Mã "+code+" không có trong "+cs.URL), nil
	}
	return validateResult(true, c.c.Display, display, ""), nil
}

func validateResult(ok bool, actual, given, msg string) *Response {
	out := &fhir.Parameters{Parameter: []fhir.ParametersParameter{{Name: "result", ValueBoolean: &ok}}}
	if actual != "" {
		out.Parameter = append(out.Parameter, fhir.ParametersParameter{Name: "display", ValueString: actual})
	}
	if ok && given != "" && actual != "" && given != actual {
		msg = "display \"" + given + "\" khác display chuẩn \"" + actual + "\""
	}
	if msg != "" {
		out.Parameter = append(out.Parameter, fhir.ParametersParameter{Name: "message", ValueString: msg})
	}
	return &Response{Status: 200, Resource: out}
}
