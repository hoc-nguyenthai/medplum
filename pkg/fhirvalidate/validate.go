// Package fhirvalidate kiểm tra một resource FHIR R4 (JSON) theo định nghĩa cơ sở:
// cấu trúc (thuộc tính lạ, kiểu, mảng hay đơn, rỗng), cardinality tối thiểu, choice type,
// định dạng kiểu nguyên thủy (regex của đặc tả) và các invariant FHIRPath (dom-2, pat-1, obs-6, ...).
//
// Chưa làm: profile và slicing, fixed/pattern, binding với terminology, kiểm tra đích của Reference.
package fhirvalidate

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"sync"

	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirpath"
)

// Severity là mức độ của một vấn đề.
type Severity string

const (
	Error   Severity = "error"
	Warning Severity = "warning"
)

// Mã vấn đề (khớp IssueType của FHIR).
const (
	CodeStructure  = "structure"
	CodeRequired   = "required"
	CodeValue      = "value"
	CodeInvariant  = "invariant"
	CodeProcessing = "processing"
)

// Issue là một vấn đề phát hiện được.
type Issue struct {
	Severity Severity
	Code     string
	Path     string // ví dụ Patient.name[0].given[1]
	Message  string
}

func (i Issue) String() string {
	return fmt.Sprintf("%s %s: %s (%s)", i.Severity, i.Path, i.Message, i.Code)
}

// Result là kết quả kiểm tra.
type Result struct{ Issues []Issue }

// OK cho biết không có lỗi (cảnh báo vẫn được tính là hợp lệ).
func (r Result) OK() bool { return len(r.Errors()) == 0 }

// Errors trả về các vấn đề mức error.
func (r Result) Errors() []Issue {
	var out []Issue
	for _, i := range r.Issues {
		if i.Severity == Error {
			out = append(out, i)
		}
	}
	return out
}

// Outcome chuyển kết quả thành OperationOutcome (một issue "informational" nếu không có vấn đề).
func (r Result) Outcome() *fhir.OperationOutcome {
	out := &fhir.OperationOutcome{}
	for _, i := range r.Issues {
		out.Issue = append(out.Issue, fhir.OperationOutcomeIssue{
			Severity:    string(i.Severity),
			Code:        i.Code,
			Details:     &fhir.CodeableConcept{Text: i.Message},
			Diagnostics: i.Message,
			Expression:  []string{i.Path},
		})
	}
	if len(out.Issue) == 0 {
		out.Issue = []fhir.OperationOutcomeIssue{{Severity: "information", Code: "informational", Details: &fhir.CodeableConcept{Text: "All OK"}}}
	}
	return out
}

// Options điều chỉnh việc kiểm tra.
type Options struct {
	// SkipConstraints bỏ qua invariant FHIRPath (nhanh hơn, chỉ kiểm tra cấu trúc).
	SkipConstraints bool
}

// Validate kiểm tra resource là object JSON đã giải mã (nên dùng fhirpath.Decode để giữ nguyên số).
func Validate(resource any, opts *Options) Result {
	if opts == nil {
		opts = &Options{}
	}
	obj, ok := resource.(map[string]any)
	if !ok {
		return Result{[]Issue{{Error, CodeStructure, "", "Resource phải là một object JSON"}}}
	}
	v := &validator{opts: *opts, top: obj}
	v.resource(obj, "", true)
	return Result{v.issues}
}

// ValidateJSON giải mã rồi kiểm tra.
func ValidateJSON(raw []byte, opts *Options) Result {
	v, err := fhirpath.Decode(raw)
	if err != nil {
		return Result{[]Issue{{Error, CodeStructure, "", "JSON không hợp lệ: " + err.Error()}}}
	}
	return Validate(v, opts)
}

// ValidateResource kiểm tra một resource Go.
func ValidateResource(r fhir.Resource, opts *Options) Result {
	v, err := fhirpath.Input(r)
	if err != nil {
		return Result{[]Issue{{Error, CodeStructure, "", err.Error()}}}
	}
	return Validate(v, opts)
}

// ---------------------------------------------------------------- nội bộ

var primitives = map[string]bool{
	"boolean": true, "integer": true, "unsignedInt": true, "positiveInt": true, "decimal": true,
	"string": true, "code": true, "id": true, "markdown": true, "uri": true, "url": true, "canonical": true,
	"oid": true, "uuid": true, "base64Binary": true, "date": true, "dateTime": true, "instant": true,
	"time": true, "xhtml": true,
}

type prop struct {
	el    fhir.ElementInfo
	typ   string
	isExt bool // thuộc tính _name chứa phần mở rộng của kiểu nguyên thủy
}

var (
	propCache  sync.Map // tên kiểu -> map[string]prop
	regexCache sync.Map // tên kiểu nguyên thủy -> *regexp.Regexp
	exprCache  sync.Map // biểu thức -> *exprEntry
)

type exprEntry struct {
	e   *fhirpath.Expression
	err error
}

func propsOf(typ string) map[string]prop {
	if v, ok := propCache.Load(typ); ok {
		return v.(map[string]prop)
	}
	m := map[string]prop{}
	for _, el := range fhir.Elements(typ) {
		for _, t := range el.Types {
			key := el.Name
			if el.IsChoice() {
				key = fhir.ChoiceProperty(el.Name, t)
			}
			m[key] = prop{el: el, typ: t}
			if primitives[t] {
				m["_"+key] = prop{el: el, typ: t, isExt: true}
			}
		}
	}
	propCache.Store(typ, m)
	return m
}

func regexFor(typ string) *regexp.Regexp {
	if v, ok := regexCache.Load(typ); ok {
		return v.(*regexp.Regexp)
	}
	var re *regexp.Regexp
	if src, ok := fhir.PrimitiveRegex(typ); ok {
		re = regexp.MustCompile(`^(?s:` + src + `)$`)
	}
	regexCache.Store(typ, re)
	return re
}

type validator struct {
	opts   Options
	top    map[string]any // resource ngoài cùng (%rootResource)
	cur    map[string]any // resource đang kiểm tra (%resource)
	issues []Issue
}

func (v *validator) add(sev Severity, code, path, format string, a ...any) {
	v.issues = append(v.issues, Issue{sev, code, path, fmt.Sprintf(format, a...)})
}

// errorCount đếm lỗi cấu trúc (không tính vi phạm invariant: lỗi invariant của phần tử con
// không được chặn việc kiểm tra invariant của phần tử cha).
func (v *validator) errorCount() int {
	n := 0
	for _, i := range v.issues {
		if i.Severity == Error && i.Code != CodeInvariant {
			n++
		}
	}
	return n
}

func (v *validator) resource(obj map[string]any, path string, top bool) {
	rt, ok := obj["resourceType"].(string)
	if !ok || rt == "" {
		v.add(Error, CodeStructure, path, "Thiếu resourceType")
		return
	}
	if !fhir.IsResourceType(rt) {
		v.add(Error, CodeStructure, path, "resourceType không hợp lệ: %q", rt)
		return
	}
	if path == "" {
		path = rt
	}
	prev := v.cur
	v.cur = obj
	v.object(rt, obj, path, true)
	v.cur = prev
}

func (v *validator) object(typ string, obj map[string]any, path string, isResource bool) {
	start := v.errorCount()
	ps := propsOf(typ)
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	present := map[string]bool{} // phần tử có mặt (giá trị hoặc phần mở rộng _name)
	variants := map[string]int{} // số biến thể choice có giá trị
	for _, k := range keys {
		if isResource && k == "resourceType" {
			continue
		}
		p, ok := ps[k]
		if !ok {
			v.add(Error, CodeStructure, path+"."+k, "Thuộc tính không hợp lệ %q", k)
			continue
		}
		present[p.el.Name] = true
		if p.isExt {
			v.extension(p, obj[k], path+"."+k)
		} else {
			variants[p.el.Name]++
			_, hasExt := obj["_"+k]
			v.property(p, obj[k], path+"."+k, hasExt)
		}
	}
	for _, el := range fhir.Elements(typ) {
		if el.Min > 0 && !present[el.Name] {
			v.add(Error, CodeRequired, path+"."+el.Name, "Thiếu thuộc tính bắt buộc %q", el.Name)
		}
		if el.IsChoice() && variants[el.Name] > 1 {
			v.add(Error, CodeStructure, path+"."+el.Name, "Chỉ được có một giá trị cho %s[x]", el.Name)
		}
	}
	if v.errorCount() == start {
		v.constraints(fhir.TypeConstraints(typ), obj, typ, path)
	}
}

func (v *validator) property(p prop, val any, path string, hasExt bool) {
	if p.el.Array {
		arr, ok := val.([]any)
		if !ok {
			v.add(Error, CodeStructure, path, "Cần một mảng")
			return
		}
		if len(arr) == 0 {
			v.add(Error, CodeStructure, path, "Mảng rỗng không hợp lệ")
			return
		}
		for i, item := range arr {
			if item == nil && hasExt {
				continue // vị trí chỉ có phần mở rộng (_name)
			}
			v.value(p, item, path+"["+strconv.Itoa(i)+"]")
		}
		return
	}
	if _, isArr := val.([]any); isArr {
		v.add(Error, CodeStructure, path, "Cần một giá trị đơn, không phải mảng")
		return
	}
	if val == nil && hasExt {
		return
	}
	v.value(p, val, path)
}

func (v *validator) value(p prop, val any, path string) {
	typ := p.typ
	if val == nil {
		v.add(Error, CodeStructure, path, "Giá trị null không hợp lệ")
		return
	}
	start := v.errorCount()
	switch {
	case primitives[typ]:
		v.primitive(typ, val, path)
	case typ == "Resource" || fhir.IsResourceType(typ):
		obj, ok := val.(map[string]any)
		if !ok {
			v.add(Error, CodeStructure, path, "Cần một resource (object)")
			return
		}
		if typ != "Resource" {
			if rt, _ := obj["resourceType"].(string); rt != typ {
				v.add(Error, CodeStructure, path, "Cần resource kiểu %s, nhận %q", typ, rt)
				return
			}
		}
		v.resource(obj, path, false)
	default:
		obj, ok := val.(map[string]any)
		if !ok {
			v.add(Error, CodeStructure, path, "Cần một object kiểu %s", typ)
			return
		}
		if len(obj) == 0 {
			v.add(Error, CodeStructure, path, "Object rỗng không hợp lệ")
			return
		}
		if !fhir.HasType(typ) {
			v.add(Warning, CodeProcessing, path, "Kiểu %q chưa có trong schema", typ)
			return
		}
		v.object(typ, obj, path, false)
	}
	if v.errorCount() == start {
		v.constraints(p.el.Constraints, val, typ, path)
	}
}

func (v *validator) primitive(typ string, val any, path string) {
	var s string
	switch typ {
	case "boolean":
		if _, ok := val.(bool); !ok {
			v.add(Error, CodeValue, path, "Cần boolean")
		}
		return
	case "integer", "unsignedInt", "positiveInt", "decimal":
		switch n := val.(type) {
		case interface{ String() string }: // json.Number
			s = n.String()
		case float64:
			s = strconv.FormatFloat(n, 'f', -1, 64)
		default:
			v.add(Error, CodeValue, path, "Cần số (%s)", typ)
			return
		}
	default:
		str, ok := val.(string)
		if !ok {
			v.add(Error, CodeValue, path, "Cần chuỗi (%s)", typ)
			return
		}
		if str == "" {
			v.add(Error, CodeValue, path, "Chuỗi rỗng không hợp lệ")
			return
		}
		s = str
	}
	if re := regexFor(typ); re != nil && !re.MatchString(s) {
		v.add(Error, CodeValue, path, "Giá trị không đúng định dạng %s: %q", typ, truncate(s))
	}
}

func truncate(s string) string {
	if len(s) > 60 {
		return s[:60] + "…"
	}
	return s
}

// extension kiểm tra thuộc tính _name (id và extension của một giá trị nguyên thủy).
func (v *validator) extension(p prop, val any, path string) {
	check := func(item any, ipath string) {
		if item == nil {
			return
		}
		obj, ok := item.(map[string]any)
		if !ok || len(obj) == 0 {
			v.add(Error, CodeStructure, ipath, "Cần object không rỗng")
			return
		}
		v.object("Element", obj, ipath, false)
	}
	if p.el.Array {
		arr, ok := val.([]any)
		if !ok {
			v.add(Error, CodeStructure, path, "Cần một mảng")
			return
		}
		for i, it := range arr {
			check(it, path+"["+strconv.Itoa(i)+"]")
		}
		return
	}
	check(val, path)
}

// ---------------------------------------------------------------- invariant

func (v *validator) constraints(cs []fhir.Constraint, focus any, typ, path string) {
	if v.opts.SkipConstraints || len(cs) == 0 {
		return
	}
	var root any
	if v.cur != nil && !sameMap(v.cur, v.top) {
		root = v.top
	}
	resource := any(v.cur)
	if v.cur == nil {
		resource = v.top
	}
	for _, c := range cs {
		ent := compile(c.Expression)
		if ent.err != nil {
			v.add(Warning, CodeProcessing, path, "Không phân tích được invariant %s: %v", c.Key, ent.err)
			continue
		}
		res, err := ent.e.EvalAt(focus, typ, resource, root, nil)
		if err != nil {
			v.add(Warning, CodeProcessing, path, "Không đánh giá được invariant %s: %v", c.Key, err)
			continue
		}
		// theo đặc tả chỉ kết quả false mới là vi phạm; rỗng (không xác định) coi như đạt
		if len(res) != 1 {
			continue
		}
		if b, ok := res[0].V.(bool); !ok || b {
			continue
		}
		sev := Error
		if c.Severity == "warning" {
			sev = Warning
		}
		v.add(sev, CodeInvariant, path, "Vi phạm invariant %s: %s", c.Key, c.Human)
	}
}

func sameMap(a, b map[string]any) bool {
	// so sánh danh tính con trỏ của map bằng địa chỉ qua fmt (map không so sánh trực tiếp được)
	return fmt.Sprintf("%p", a) == fmt.Sprintf("%p", b)
}

func compile(src string) *exprEntry {
	if v, ok := exprCache.Load(src); ok {
		return v.(*exprEntry)
	}
	e, err := fhirpath.Compile(src)
	ent := &exprEntry{e, err}
	exprCache.Store(src, ent)
	return ent
}
