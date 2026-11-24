package fhirvalidate

import (
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"trivita-fhir/pkg/fhir"
)

// ProfileResolver trả StructureDefinition (JSON đã giải mã, nên dùng fhirpath.Decode) theo canonical URL, có thể kèm "|phiên bản".
// Trả (nil, nil) khi profile không tồn tại; lỗi tra cứu được báo là cảnh báo chứ không chặn việc ghi.
type ProfileResolver func(canonical string) (map[string]any, error)

// Kiểm tra theo profile (meta.profile -> StructureDefinition), V-03 và V-04 của docs/plans/modules/fhir-parity-audit.md.
//
// Phạm vi: cardinality (min, max, kể cả max 0), fixed[x], pattern[x], maxLength, giới hạn kiểu của choice [x],
// invariant do chính profile khai báo, slicing với discriminator value, pattern, exists, type (và rules closed, ordered).
// Dùng snapshot của StructureDefinition; không có snapshot thì dùng differential như thể đó là toàn bộ định nghĩa.
//
// Chưa làm: discriminator profile và đường dẫn có hàm FHIRPath (resolve(), extension(), where()) — slicing đó chỉ cho một cảnh báo,
// không áp cardinality của slice; binding terminology (V-05); kiểm resource lồng (contained, Bundle.entry) theo profile của nó;
// kiểm giá trị Extension theo profile kiểu của phần tử (type.profile) ngoài việc lấy url cố định để khớp slice.

// pnode là một ElementDefinition trong cây profile.
type pnode struct {
	def    map[string]any
	groups map[string]*pgroup
	order  []string // thứ tự xuất hiện của nhóm (để báo lỗi ổn định)
}

// pgroup gom phần tử không slice (base) và các slice của cùng một tên.
type pgroup struct {
	base   *pnode
	slices []*pnode
	names  []string
}

func newNode(def map[string]any) *pnode { return &pnode{def: def, groups: map[string]*pgroup{}} }

// buildProfile dựng cây từ danh sách ElementDefinition theo id (Patient.identifier:ssn.system).
func buildProfile(sd map[string]any) (*pnode, bool) {
	var els []any
	for _, part := range []string{"snapshot", "differential"} {
		if m, ok := sd[part].(map[string]any); ok {
			if e, ok := m["element"].([]any); ok && len(e) > 0 {
				els = e
				break
			}
		}
	}
	if len(els) == 0 {
		return nil, false
	}
	var root *pnode
	for _, raw := range els {
		def, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		id, _ := def["id"].(string)
		if id == "" {
			id, _ = def["path"].(string)
		}
		segs := strings.Split(id, ".")
		if root == nil {
			// phần tử đầu tiên là gốc; differential có thể bỏ qua nó
			if len(segs) == 1 {
				root = newNode(def)
				continue
			}
			root = newNode(map[string]any{})
		}
		if len(segs) < 2 {
			continue
		}
		cur := root
		okPath := true
		for _, seg := range segs[1 : len(segs)-1] {
			name, slice, _ := strings.Cut(seg, ":")
			g := cur.groups[name]
			if g == nil {
				okPath = false
				break
			}
			next := g.base
			if slice != "" {
				for i, n := range g.names {
					if n == slice {
						next = g.slices[i]
					}
				}
			}
			if next == nil {
				okPath = false
				break
			}
			cur = next
		}
		if !okPath {
			continue
		}
		name, slice, _ := strings.Cut(segs[len(segs)-1], ":")
		g := cur.groups[name]
		if g == nil {
			g = &pgroup{}
			cur.groups[name] = g
			cur.order = append(cur.order, name)
		}
		n := newNode(def)
		if slice == "" {
			g.base = n
		} else {
			g.slices = append(g.slices, n)
			g.names = append(g.names, slice)
		}
	}
	return root, root != nil
}

type pvalue struct {
	val  any
	path string
	typ  string // kiểu của giá trị khi phần tử là choice [x], rỗng nếu không biết
}

type profileValidator struct {
	v   *validator
	sd  map[string]any
	url string
	res ProfileResolver
}

// validateProfiles kiểm resource ngoài cùng theo mọi meta.profile.
func (v *validator) validateProfiles(obj map[string]any) {
	meta, _ := obj["meta"].(map[string]any)
	list, _ := meta["profile"].([]any)
	rt, _ := obj["resourceType"].(string)
	for i, p := range list {
		u, _ := p.(string)
		if u == "" {
			continue
		}
		path := fmt.Sprintf("%s.meta.profile[%d]", rt, i)
		sd, err := v.opts.Profiles(u)
		if err != nil {
			v.add(Warning, CodeProcessing, path, "Không tải được profile %s: %v", u, err)
			continue
		}
		if sd == nil {
			sev := v.opts.UnknownProfile
			if sev == "" {
				sev = Warning
			}
			v.add(sev, CodeProcessing, path, "Không tìm thấy profile %s", u)
			continue
		}
		if t, _ := sd["type"].(string); t != "" && t != rt {
			v.add(Error, CodeStructure, path, "Profile %s dành cho %s, không áp dụng cho %s", u, t, rt)
			continue
		}
		root, ok := buildProfile(sd)
		if !ok {
			v.add(Warning, CodeProcessing, path, "Profile %s không có snapshot hoặc differential", u)
			continue
		}
		pv := &profileValidator{v: v, sd: sd, url: u, res: v.opts.Profiles}
		pv.visit(root, obj, rt, rt)
	}
}

func (pv *profileValidator) errf(code, path, format string, a ...any) {
	pv.v.add(Error, code, path, format+" (profile %s)", append(a, pv.url)...)
}

// visit kiểm một giá trị với nút profile của nó rồi đệ quy xuống các phần tử con.
func (pv *profileValidator) visit(n *pnode, val any, path, typ string) {
	pv.checkValue(n, val, path, typ)
	obj, ok := val.(map[string]any)
	if !ok {
		return
	}
	for _, name := range n.order {
		pv.group(n.groups[name], name, obj, path)
	}
}

func (pv *profileValidator) checkValue(n *pnode, val any, path, typ string) {
	if val == nil {
		return
	}
	for k, fv := range n.def {
		switch {
		case isChoiceKey(k, "fixed"):
			if !sameJSON(fv, val) {
				pv.errf(CodeValue, path, "Giá trị phải bằng giá trị cố định %s", compact(fv))
			}
		case isChoiceKey(k, "pattern"):
			if !patternMatch(fv, val) {
				pv.errf(CodeValue, path, "Giá trị không khớp mẫu %s", compact(fv))
			}
		}
	}
	if ml, ok := toInt(n.def["maxLength"]); ok {
		if s, isStr := val.(string); isStr && utf8.RuneCountInString(s) > ml {
			pv.errf(CodeValue, path, "Độ dài tối đa là %d ký tự", ml)
		}
	}
	// invariant do chính profile khai báo (source không phải định nghĩa lõi), đã có sẵn trong snapshot
	if cs, ok := n.def["constraint"].([]any); ok && typ != "" {
		var own []fhir.Constraint
		for _, raw := range cs {
			c, _ := raw.(map[string]any)
			src, _ := c["source"].(string)
			if src == "" || strings.HasPrefix(src, "http://hl7.org/fhir/StructureDefinition/") {
				continue
			}
			key, _ := c["key"].(string)
			expr, _ := c["expression"].(string)
			if expr == "" {
				continue
			}
			sev, _ := c["severity"].(string)
			human, _ := c["human"].(string)
			own = append(own, fhir.Constraint{Key: key, Severity: sev, Human: human, Expression: expr})
		}
		pv.v.constraints(own, val, typ, path)
	}
}

func (pv *profileValidator) group(g *pgroup, name string, obj map[string]any, path string) {
	if g == nil {
		return
	}
	base := g.base
	vals := collect(obj, name, path)
	if base != nil {
		min, hasMin := toInt(base.def["min"])
		if hasMin && len(vals) < min {
			pv.errf(CodeRequired, path+"."+strings.TrimSuffix(name, "[x]"), "Cần ít nhất %d giá trị, có %d", min, len(vals))
		}
		if mx, _ := base.def["max"].(string); mx != "" && mx != "*" {
			if m, err := strconv.Atoi(mx); err == nil && len(vals) > m {
				pv.errf(CodeStructure, path+"."+strings.TrimSuffix(name, "[x]"), "Cho phép tối đa %d giá trị, có %d", m, len(vals))
			}
		}
		if strings.HasSuffix(name, "[x]") {
			if allowed := typeCodes(base.def); len(allowed) > 0 {
				for _, pvv := range vals {
					if pvv.typ != "" && !containsFold(allowed, pvv.typ) {
						pv.errf(CodeStructure, pvv.path, "Kiểu %s không được profile cho phép (chỉ %s)", pvv.typ, strings.Join(allowed, ", "))
					}
				}
			}
		}
	}
	slicing, _ := nodeSlicing(base)
	if len(g.slices) == 0 || slicing == nil {
		if base != nil {
			for _, x := range vals {
				pv.visit(base, x.val, x.path, valueType(base, x))
			}
		}
		return
	}
	pv.slice(g, base, slicing, vals, path, name)
}

func nodeSlicing(base *pnode) (map[string]any, bool) {
	if base == nil {
		return nil, false
	}
	s, ok := base.def["slicing"].(map[string]any)
	return s, ok
}

type discriminator struct{ typ, path string }

func (pv *profileValidator) slice(g *pgroup, base *pnode, slicing map[string]any, vals []pvalue, path, name string) {
	var ds []discriminator
	if arr, ok := slicing["discriminator"].([]any); ok {
		for _, raw := range arr {
			m, _ := raw.(map[string]any)
			t, _ := m["type"].(string)
			p, _ := m["path"].(string)
			ds = append(ds, discriminator{t, p})
		}
	}
	rules, _ := slicing["rules"].(string)
	ordered, _ := slicing["ordered"].(bool)
	where := path + "." + strings.TrimSuffix(name, "[x]")

	assigned := make([]int, len(vals)) // chỉ số slice của từng giá trị, -1 là không khớp slice nào
	counts := make([]int, len(g.slices))
	for i, x := range vals {
		assigned[i] = -1
		for si, s := range g.slices {
			m, ok, why := pv.matchSlice(s, ds, x, base)
			if !ok {
				pv.v.add(Warning, CodeProcessing, where, "Bỏ qua kiểm slicing: %s (profile %s)", why, pv.url)
				// vẫn kiểm các ràng buộc chung của phần tử
				for _, y := range vals {
					pv.visit(base, y.val, y.path, valueType(base, y))
				}
				return
			}
			if m {
				assigned[i] = si
				counts[si]++
				break
			}
		}
	}
	for si, s := range g.slices {
		if min, ok := toInt(s.def["min"]); ok && counts[si] < min {
			pv.errf(CodeRequired, where, "Slice %q cần ít nhất %d phần tử, có %d", g.names[si], min, counts[si])
		}
		if mx, _ := s.def["max"].(string); mx != "" && mx != "*" {
			if m, err := strconv.Atoi(mx); err == nil && counts[si] > m {
				pv.errf(CodeStructure, where, "Slice %q cho phép tối đa %d phần tử, có %d", g.names[si], m, counts[si])
			}
		}
	}
	last := -1
	seenOpen := false
	for i, x := range vals {
		si := assigned[i]
		if si < 0 {
			if rules == "closed" {
				pv.errf(CodeStructure, x.path, "Phần tử không thuộc slice nào của slicing đóng")
			}
			seenOpen = true
			pv.visit(base, x.val, x.path, valueType(base, x))
			continue
		}
		if ordered && si < last {
			pv.errf(CodeStructure, x.path, "Slice %q sai thứ tự (slicing có ordered)", g.names[si])
		}
		if rules == "openAtEnd" && seenOpen {
			pv.errf(CodeStructure, x.path, "Slice %q phải đứng trước các phần tử ngoài slice (openAtEnd)", g.names[si])
		}
		if si > last {
			last = si
		}
		// phần tử khớp slice chịu cả ràng buộc chung của base lẫn của slice
		pv.visit(base, x.val, x.path, valueType(base, x))
		pv.visit(g.slices[si], x.val, x.path, valueType(g.slices[si], x))
	}
}

// matchSlice cho biết giá trị có khớp mọi discriminator của slice. ok=false nghĩa là không đánh giá được (kèm lý do).
func (pv *profileValidator) matchSlice(s *pnode, ds []discriminator, x pvalue, base *pnode) (match, ok bool, why string) {
	if len(ds) == 0 {
		return false, false, "slicing không có discriminator"
	}
	for _, d := range ds {
		if strings.ContainsAny(d.path, "()") {
			return false, false, fmt.Sprintf("discriminator %s %q dùng hàm FHIRPath", d.typ, d.path)
		}
		switch d.typ {
		case "value", "pattern":
			exp, found := pv.expectedAt(s, d.path)
			if !found {
				return false, false, fmt.Sprintf("slice không cố định giá trị tại %q", d.path)
			}
			if !anyPattern(exp, navigate(x.val, d.path)) {
				return false, true, ""
			}
		case "exists":
			n, found := pv.nodeAt(s, d.path)
			if !found {
				return false, false, fmt.Sprintf("slice không khai báo %q", d.path)
			}
			has := len(navigate(x.val, d.path)) > 0
			if mn, _ := toInt(n.def["min"]); mn >= 1 && !has {
				return false, true, ""
			}
			if mx, _ := n.def["max"].(string); mx == "0" && has {
				return false, true, ""
			}
		case "type":
			if d.path != "$this" {
				return false, false, fmt.Sprintf("discriminator type %q", d.path)
			}
			t := x.typ
			if t == "" {
				if tc := typeCodes(base.def); len(tc) == 1 {
					t = tc[0]
				}
			}
			codes := typeCodes(s.def)
			if t == "" || len(codes) == 0 {
				return false, false, "không biết kiểu của giá trị"
			}
			if !containsFold(codes, t) {
				return false, true, ""
			}
		default:
			return false, false, fmt.Sprintf("discriminator %q", d.typ)
		}
	}
	return true, true, ""
}

// nodeAt đi xuống theo đường dẫn "a.b.c" từ nút slice; không thấy thì thử profile kiểu của phần tử (Extension.url cố định).
func (pv *profileValidator) nodeAt(s *pnode, path string) (*pnode, bool) {
	if path == "$this" || path == "" {
		return s, true
	}
	cur := s
	for i, seg := range strings.Split(path, ".") {
		g := cur.groups[seg]
		if g == nil || g.base == nil {
			if i == 0 {
				if root := pv.typeProfile(cur); root != nil {
					if g2 := root.groups[seg]; g2 != nil && g2.base != nil {
						cur = g2.base
						continue
					}
				}
			}
			return nil, false
		}
		cur = g.base
	}
	return cur, true
}

func (pv *profileValidator) expectedAt(s *pnode, path string) (any, bool) {
	n, ok := pv.nodeAt(s, path)
	if !ok {
		return nil, false
	}
	for k, v := range n.def {
		if isChoiceKey(k, "fixed") || isChoiceKey(k, "pattern") {
			return v, true
		}
	}
	return nil, false
}

// typeProfile nạp profile kiểu (type[0].profile[0]) của một phần tử, nếu resolver có.
func (pv *profileValidator) typeProfile(n *pnode) *pnode {
	ts, _ := n.def["type"].([]any)
	if len(ts) == 0 || pv.res == nil {
		return nil
	}
	t, _ := ts[0].(map[string]any)
	ps, _ := t["profile"].([]any)
	if len(ps) == 0 {
		return nil
	}
	u, _ := ps[0].(string)
	if u == "" {
		return nil
	}
	sd, err := pv.res(u)
	if err != nil || sd == nil {
		return nil
	}
	root, _ := buildProfile(sd)
	return root
}

// ---------------------------------------------------------------- tiện ích

// collect lấy các giá trị của một phần tử (tên hoặc name[x]) trong object, kèm đường dẫn và kiểu (với [x]).
func collect(obj map[string]any, name, path string) []pvalue {
	var out []pvalue
	add := func(key, typ string) {
		v, hasVal := obj[key]
		ext, hasExt := obj["_"+key]
		if !hasVal && !hasExt {
			return
		}
		if arr, ok := v.([]any); ok {
			for i, it := range arr {
				out = append(out, pvalue{it, fmt.Sprintf("%s.%s[%d]", path, key, i), typ})
			}
			return
		}
		if !hasVal {
			if arr, ok := ext.([]any); ok {
				for i := range arr {
					out = append(out, pvalue{nil, fmt.Sprintf("%s.%s[%d]", path, key, i), typ})
				}
				return
			}
		}
		out = append(out, pvalue{v, path + "." + key, typ})
	}
	if prefix, ok := strings.CutSuffix(name, "[x]"); ok {
		for _, k := range sortedKeys(obj) {
			if strings.HasPrefix(k, "_") || !strings.HasPrefix(k, prefix) || len(k) == len(prefix) {
				continue
			}
			r, _ := utf8.DecodeRuneInString(k[len(prefix):])
			if !unicode.IsUpper(r) {
				continue
			}
			add(k, k[len(prefix):])
		}
		return out
	}
	add(name, "")
	return out
}

func sortedKeys(m map[string]any) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// navigate đi theo đường dẫn "a.b" (mỗi bước có thể là mảng) và trả mọi giá trị tìm được; "$this" là chính giá trị.
func navigate(val any, path string) []any {
	cur := []any{val}
	if path == "$this" || path == "" {
		return cur
	}
	for _, seg := range strings.Split(path, ".") {
		var next []any
		for _, c := range cur {
			m, ok := c.(map[string]any)
			if !ok {
				continue
			}
			if prefix, isChoice := strings.CutSuffix(seg, "[x]"); isChoice {
				for _, k := range sortedKeys(m) {
					if strings.HasPrefix(k, prefix) && len(k) > len(prefix) && !strings.HasPrefix(k, "_") {
						next = appendVal(next, m[k])
					}
				}
				continue
			}
			if v, ok := m[seg]; ok {
				next = appendVal(next, v)
			}
		}
		cur = next
	}
	return cur
}

func appendVal(dst []any, v any) []any {
	if arr, ok := v.([]any); ok {
		return append(dst, arr...)
	}
	return append(dst, v)
}

func anyPattern(pattern any, actual []any) bool {
	for _, a := range actual {
		if patternMatch(pattern, a) {
			return true
		}
	}
	return false
}

// patternMatch: mọi khóa của mẫu phải có trong giá trị với nội dung khớp; mảng của mẫu: mỗi phần tử phải khớp một phần tử của giá trị.
func patternMatch(pattern, actual any) bool {
	switch p := pattern.(type) {
	case map[string]any:
		a, ok := actual.(map[string]any)
		if !ok {
			return false
		}
		for k, pv := range p {
			av, present := a[k]
			if !present || !patternMatch(pv, av) {
				return false
			}
		}
		return true
	case []any:
		var items []any
		switch a := actual.(type) {
		case []any:
			items = a
		default:
			items = []any{a}
		}
		for _, pi := range p {
			found := false
			for _, ai := range items {
				if patternMatch(pi, ai) {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
		return true
	default:
		return scalar(pattern) == scalar(actual)
	}
}

// sameJSON so sánh bằng (fixed[x]): cùng cấu trúc và giá trị, không bỏ qua khóa thừa.
func sameJSON(a, b any) bool {
	return reflect.DeepEqual(normalize(a), normalize(b))
}

func normalize(v any) any {
	switch x := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(x))
		for k, e := range x {
			m[k] = normalize(e)
		}
		return m
	case []any:
		s := make([]any, len(x))
		for i, e := range x {
			s[i] = normalize(e)
		}
		return s
	default:
		return scalar(v)
	}
}

func scalar(v any) string {
	if n, ok := v.(interface{ String() string }); ok {
		return n.String()
	}
	if f, ok := v.(float64); ok {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	return fmt.Sprint(v)
}

func compact(v any) string {
	s := fmt.Sprintf("%v", normalize(v))
	return truncate(s)
}

// isChoiceKey: "fixedString", "patternCodeableConcept" (tiền tố + chữ hoa).
func isChoiceKey(k, prefix string) bool {
	if !strings.HasPrefix(k, prefix) || len(k) == len(prefix) {
		return false
	}
	r, _ := utf8.DecodeRuneInString(k[len(prefix):])
	return unicode.IsUpper(r)
}

func toInt(v any) (int, bool) {
	switch x := v.(type) {
	case float64:
		return int(x), true
	case int:
		return x, true
	case interface{ Int64() (int64, error) }:
		n, err := x.Int64()
		return int(n), err == nil
	}
	return 0, false
}

func typeCodes(def map[string]any) []string {
	var out []string
	ts, _ := def["type"].([]any)
	for _, t := range ts {
		m, _ := t.(map[string]any)
		if c, _ := m["code"].(string); c != "" {
			out = append(out, c)
		}
	}
	return out
}

func containsFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}

// valueType là kiểu dùng để đánh giá invariant của một giá trị: kiểu của biến thể choice, hoặc kiểu duy nhất của phần tử.
func valueType(n *pnode, x pvalue) string {
	if x.typ != "" {
		for _, c := range typeCodes(n.def) {
			if strings.EqualFold(c, x.typ) {
				return c
			}
		}
		return x.typ
	}
	if tc := typeCodes(n.def); len(tc) == 1 {
		return tc[0]
	}
	return ""
}
