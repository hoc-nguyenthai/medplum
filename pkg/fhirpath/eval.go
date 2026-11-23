package fhirpath

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"time"
	"unicode"

	"trivita-fhir/pkg/fhir"
)

// Options điều chỉnh việc đánh giá biểu thức.
type Options struct {
	// Strict báo lỗi semantic khi truy cập phần tử không tồn tại trong schema FHIR.
	Strict bool
	// Vars là biến ngoài (%name), giá trị là JSON đã giải mã.
	Vars map[string][]any
	// Resolve tìm resource theo chuỗi tham chiếu; nil thì resolve() chỉ trả về bản rút gọn {resourceType, id}.
	Resolve func(reference string) map[string]any
	// Now cho now(), today(), timeOfDay(); nil dùng giờ hệ thống.
	Now func() time.Time
}

// Expression là một biểu thức đã phân tích, dùng lại được và an toàn đồng thời.
type Expression struct {
	src string
	ast node
}

// Compile phân tích biểu thức FHIRPath.
func Compile(src string) (*Expression, error) {
	n, err := parse(src)
	if err != nil {
		return nil, err
	}
	return &Expression{src: src, ast: n}, nil
}

// String trả về mã nguồn biểu thức.
func (e *Expression) String() string { return e.src }

// Input chuyển resource Go thành dữ liệu JSON để đánh giá.
func Input(r fhir.Resource) (any, error) {
	raw, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	return Decode(raw)
}

// Decode giải mã JSON giữ nguyên số (json.Number).
func Decode(raw []byte) (any, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}

// Eval đánh giá biểu thức trên input (object JSON của một resource, hoặc giá trị bất kỳ).
func (e *Expression) Eval(input any, opts *Options) ([]Value, error) {
	if opts == nil {
		opts = &Options{}
	}
	root := fromJSON(input, "")
	c := &context{root: root, focus: []Value{root}, opts: opts}
	if input == nil {
		c.focus = nil
	}
	return c.eval(e.ast)
}

// EvalAt đánh giá biểu thức với focus là một phần tử (typ là tên kiểu FHIR của nó, "" nếu chưa biết),
// %resource là resource chứa phần tử và %rootResource là resource ngoài cùng (nil nếu trùng resource).
// Dùng để kiểm tra invariant của phần tử.
func (e *Expression) EvalAt(focus any, typ string, resource, rootResource any, opts *Options) ([]Value, error) {
	if opts == nil {
		opts = &Options{}
	}
	this := fromJSON(focus, typ)
	c := &context{root: fromJSON(resource, ""), focus: []Value{this}, opts: opts}
	if rootResource != nil {
		r := fromJSON(rootResource, "")
		c.rootRes = &r
	}
	return c.eval(e.ast)
}

// Evaluate biên dịch rồi đánh giá một lần.
func Evaluate(src string, input any, opts *Options) ([]Value, error) {
	e, err := Compile(src)
	if err != nil {
		return nil, err
	}
	return e.Eval(input, opts)
}

// ---------------------------------------------------------------- context

type context struct {
	root     Value
	rootRes  *Value // %rootResource khi khác root (resource lồng trong Bundle/contained)
	focus    []Value
	this     *Value
	index    *int
	total    []Value
	hasTotal bool
	opts     *Options
}

func (c *context) with(focus []Value, this *Value, idx *int) *context {
	n := *c
	n.focus, n.this, n.index = focus, this, idx
	return &n
}

func (c *context) now() time.Time {
	if c.opts.Now != nil {
		return c.opts.Now()
	}
	return time.Now()
}

func (c *context) eval(n node) ([]Value, error) {
	switch x := n.(type) {
	case litNode:
		return []Value{x.v}, nil
	case emptyNode:
		return nil, nil
	case identNode:
		return c.ident(x.name)
	case varNode:
		return c.variable(x.name)
	case memberNode:
		left, err := c.eval(x.left)
		if err != nil {
			return nil, err
		}
		var out []Value
		for _, v := range left {
			ch, err := c.child(v, x.name)
			if err != nil {
				return nil, err
			}
			out = append(out, ch...)
		}
		return out, nil
	case indexNode:
		left, err := c.eval(x.left)
		if err != nil {
			return nil, err
		}
		idx, err := c.eval(x.idx)
		if err != nil {
			return nil, err
		}
		if len(idx) != 1 {
			return nil, nil
		}
		i, ok := idx[0].V.(int64)
		if !ok {
			return nil, errf(Execution, "chỉ số phải là số nguyên")
		}
		if i < 0 || int(i) >= len(left) {
			return nil, nil
		}
		return []Value{left[i]}, nil
	case unaryNode:
		v, err := c.eval(x.x)
		if err != nil {
			return nil, err
		}
		return unary(x.op, v)
	case binNode:
		return c.binary(x)
	case typeNode:
		v, err := c.eval(x.x)
		if err != nil {
			return nil, err
		}
		if x.op == "is" {
			if len(v) == 0 {
				return nil, nil
			}
			if len(v) > 1 {
				return nil, errf(Execution, "'is' cần một phần tử")
			}
			return []Value{boolV(isType(v[0], x.typ))}, nil
		}
		var out []Value
		for _, it := range v {
			if isType(it, x.typ) {
				out = append(out, it)
			}
		}
		return out, nil // áp dụng như ofType cho cả collection (dom-3 của FHIR dùng .as() trên nhiều phần tử)
	case valuesNode:
		return x.v, nil
	case funcNode:
		return c.call(x)
	}
	return nil, errf(Execution, "nút không hỗ trợ %T", n)
}

func (c *context) ident(name string) ([]Value, error) {
	var out []Value
	for _, v := range c.focus {
		if name != "" && unicode.IsUpper(rune(name[0])) {
			if v.T != "" && (fhir.IsSubtype(v.T, name) || v.T == name) {
				out = append(out, v)
				continue
			}
			if _, isMap := v.V.(map[string]any); isMap && c.focusIsRoot(v) {
				if fhir.HasType(name) || fhir.IsResourceType(name) {
					if c.opts.Strict {
						return nil, errf(Semantic, "%s không phải kiểu của %s", name, v.T)
					}
					continue // sai tên kiểu gốc: rỗng
				}
			}
		}
		ch, err := c.child(v, name)
		if err != nil {
			return nil, err
		}
		out = append(out, ch...)
	}
	return out, nil
}

func (c *context) focusIsRoot(v Value) bool {
	a, ok1 := v.V.(map[string]any)
	b, ok2 := c.root.V.(map[string]any)
	return ok1 && ok2 && len(a) == len(b) && len(a) > 0 && reflectSame(a, b)
}

func reflectSame(a, b map[string]any) bool { return jsonEqual(a, b) }

func (c *context) variable(name string) ([]Value, error) {
	switch name {
	case "$this":
		if c.this != nil {
			return []Value{*c.this}, nil
		}
		return c.focus, nil
	case "$index":
		if c.index != nil {
			return []Value{intV(int64(*c.index))}, nil
		}
		return nil, nil
	case "$total":
		if c.hasTotal {
			return c.total, nil
		}
		return nil, nil
	case "%rootResource":
		if c.rootRes != nil {
			return []Value{*c.rootRes}, nil
		}
		return []Value{c.root}, nil
	case "%context", "%resource":
		return []Value{c.root}, nil
	case "%ucum":
		return []Value{strV("http://unitsofmeasure.org")}, nil
	case "%sct":
		return []Value{strV("http://snomed.info/sct")}, nil
	case "%loinc":
		return []Value{strV("http://loinc.org")}, nil
	}
	if strings.HasPrefix(name, "%vs-") {
		return []Value{strV("http://hl7.org/fhir/ValueSet/" + name[4:])}, nil
	}
	if strings.HasPrefix(name, "%ext-") {
		return []Value{strV("http://hl7.org/fhir/StructureDefinition/" + name[5:])}, nil
	}
	if strings.HasPrefix(name, "%") {
		key := name[1:]
		if vs, ok := c.opts.Vars[key]; ok {
			out := make([]Value, len(vs))
			for i, v := range vs {
				out[i] = fromJSON(v, "")
			}
			return out, nil
		}
		return nil, errf(Execution, "biến %s chưa định nghĩa", name)
	}
	return nil, errf(Syntax, "biến không hợp lệ %s", name)
}

// ---------------------------------------------------------------- truy cập phần tử

func (c *context) child(v Value, name string) ([]Value, error) {
	m, ok := v.V.(map[string]any)
	if !ok {
		return nil, nil
	}
	t := v.T
	if rt, ok := m["resourceType"].(string); ok && (t == "" || t == "Resource" || t == "DomainResource") {
		t = rt
	}
	if info, ok := fhir.LookupElement(t, name); ok {
		if info.IsChoice() {
			for _, ty := range info.Types {
				prop := fhir.ChoiceProperty(name, ty)
				if raw, ok := m[prop]; ok {
					return wrapPrim(raw, m["_"+prop], ty), nil
				}
				if ext, ok := m["_"+prop]; ok {
					return wrapPrim(nil, ext, ty), nil
				}
			}
			return nil, nil
		}
		typ := ""
		if len(info.Types) > 0 {
			typ = info.Types[0]
		}
		raw, ok := m[name]
		ext, hasExt := m["_"+name]
		if !ok && !hasExt {
			return nil, nil
		}
		return wrapPrim(raw, ext, typ), nil
	}
	if c.opts.Strict && fhir.HasType(t) {
		return nil, errf(Semantic, "%s không có phần tử %q", t, name)
	}
	if raw, ok := m[name]; ok {
		return wrap(raw, ""), nil
	}
	// choice type khi không có schema: value + Quantity
	for _, k := range sortedKeys(m) {
		if len(k) > len(name) && strings.HasPrefix(k, name) && unicode.IsUpper(rune(k[len(name)])) {
			typ := strings.ToLower(k[len(name):len(name)+1]) + k[len(name)+1:]
			if !fhir.HasType(typ) && fhir.HasType(k[len(name):]) {
				typ = k[len(name):]
			}
			return wrap(m[k], typ), nil
		}
	}
	return nil, nil
}

// wrapPrim như wrap nhưng ghép thêm phần mở rộng _name của kiểu nguyên thủy: một giá trị chỉ có
// extension (không có giá trị) vẫn là một phần tử tồn tại; nó được biểu diễn bằng object của _name.
func wrapPrim(raw, ext any, typ string) []Value {
	if ext == nil {
		return wrap(raw, typ)
	}
	rawArr, isArr := raw.([]any)
	extArr, extIsArr := ext.([]any)
	if !isArr && !extIsArr {
		if raw != nil {
			return wrap(raw, typ)
		}
		if em, ok := ext.(map[string]any); ok {
			return []Value{{em, typ}}
		}
		return nil
	}
	n := max(len(rawArr), len(extArr))
	var out []Value
	for i := 0; i < n; i++ {
		var r, e any
		if i < len(rawArr) {
			r = rawArr[i]
		}
		if i < len(extArr) {
			e = extArr[i]
		}
		switch {
		case r != nil:
			out = append(out, fromJSON(r, typ))
		case e != nil:
			if em, ok := e.(map[string]any); ok {
				out = append(out, Value{em, typ})
			}
		}
	}
	return out
}

func wrap(raw any, typ string) []Value {
	switch x := raw.(type) {
	case nil:
		return nil
	case []any:
		out := make([]Value, 0, len(x))
		for _, e := range x {
			if e != nil {
				out = append(out, fromJSON(e, typ))
			}
		}
		return out
	}
	return []Value{fromJSON(raw, typ)}
}

// ---------------------------------------------------------------- kiểu

func isType(v Value, name string) bool {
	n := name
	switch {
	case strings.HasPrefix(n, "System."):
		n = n[7:]
		return systemTypeSet[n] && v.T == n
	case strings.HasPrefix(n, "FHIR."):
		n = n[5:]
	}
	if v.T == "" {
		return false
	}
	if systemTypeSet[n] && v.T == n {
		return true
	}
	return fhir.IsSubtype(v.T, n)
}

// ---------------------------------------------------------------- toán tử

func unary(op string, v []Value) ([]Value, error) {
	if len(v) == 0 {
		return nil, nil
	}
	if len(v) > 1 {
		return nil, errf(Execution, "toán tử một ngôi cần một phần tử")
	}
	if op == "+" {
		if isNum(v[0]) {
			return v, nil
		}
		return nil, errf(Execution, "+ không áp dụng cho %s", v[0].T)
	}
	switch x := v[0].V.(type) {
	case int64:
		return []Value{{-x, v[0].T}}, nil
	case float64:
		return []Value{{-x, v[0].T}}, nil
	case Quantity:
		x.Value = -x.Value
		return []Value{{x, v[0].T}}, nil
	}
	return nil, errf(Execution, "- không áp dụng cho %s", v[0].T)
}

// tri-state boolean: nil = rỗng
func toBool(v []Value) (*bool, error) {
	if len(v) == 0 {
		return nil, nil
	}
	if len(v) > 1 {
		return nil, errf(Execution, "cần một giá trị boolean, nhận %d phần tử", len(v))
	}
	if b, ok := v[0].V.(bool); ok {
		return &b, nil
	}
	t := true
	return &t, nil
}

func fromBool(b *bool) []Value {
	if b == nil {
		return nil
	}
	return []Value{boolV(*b)}
}

func (c *context) binary(n binNode) ([]Value, error) {
	l, err := c.eval(n.l)
	if err != nil {
		return nil, err
	}
	r, err := c.eval(n.r)
	if err != nil {
		return nil, err
	}
	switch n.op {
	case "and", "or", "xor", "implies":
		a, err := toBool(l)
		if err != nil {
			return nil, err
		}
		b, err := toBool(r)
		if err != nil {
			return nil, err
		}
		return fromBool(logic(n.op, a, b)), nil
	case "|":
		return distinct(append(append([]Value{}, l...), r...)), nil
	case "&":
		ls, rs := "", ""
		if len(l) > 1 || len(r) > 1 {
			return nil, errf(Execution, "& cần một phần tử")
		}
		if len(l) == 1 {
			s, ok := l[0].V.(string)
			if !ok {
				return nil, errf(Execution, "& cần chuỗi")
			}
			ls = s
		}
		if len(r) == 1 {
			s, ok := r[0].V.(string)
			if !ok {
				return nil, errf(Execution, "& cần chuỗi")
			}
			rs = s
		}
		return []Value{strV(ls + rs)}, nil
	case "=", "!=":
		if len(l) == 0 || len(r) == 0 {
			return nil, nil
		}
		if len(l) != len(r) {
			return []Value{boolV(n.op == "!=")}, nil
		}
		all, known := true, true
		for i := range l {
			e, k := equals(l[i], r[i])
			if !k {
				known = false
			} else if !e {
				all = false
				known = true
				break
			}
		}
		if all && !known {
			return nil, nil
		}
		return []Value{boolV(all == (n.op == "="))}, nil
	case "~", "!~":
		res := equivCollections(l, r)
		return []Value{boolV(res == (n.op == "~"))}, nil
	case "<", ">", "<=", ">=":
		if len(l) == 0 || len(r) == 0 {
			return nil, nil
		}
		if len(l) > 1 || len(r) > 1 {
			return nil, errf(Execution, "so sánh cần một phần tử")
		}
		cmp, known, err := compare(l[0], r[0])
		if err != nil {
			return nil, err
		}
		if !known {
			return nil, nil
		}
		return []Value{boolV(map[string]bool{"<": cmp < 0, ">": cmp > 0, "<=": cmp <= 0, ">=": cmp >= 0}[n.op])}, nil
	case "in":
		if len(l) == 0 {
			return nil, nil
		}
		if len(l) > 1 {
			return nil, errf(Execution, "'in' cần một phần tử bên trái")
		}
		return []Value{boolV(contains(r, l[0]))}, nil
	case "contains":
		if len(r) == 0 {
			return nil, nil
		}
		if len(r) > 1 {
			return nil, errf(Execution, "'contains' cần một phần tử bên phải")
		}
		return []Value{boolV(contains(l, r[0]))}, nil
	case "+", "-", "*", "/", "div", "mod":
		return arith(n.op, l, r)
	}
	return nil, errf(Execution, "toán tử không hỗ trợ %s", n.op)
}

func logic(op string, a, b *bool) *bool {
	t, f := true, false
	switch op {
	case "and":
		if (a != nil && !*a) || (b != nil && !*b) {
			return &f
		}
		if a == nil || b == nil {
			return nil
		}
		return &t
	case "or":
		if (a != nil && *a) || (b != nil && *b) {
			return &t
		}
		if a == nil || b == nil {
			return nil
		}
		return &f
	case "xor":
		if a == nil || b == nil {
			return nil
		}
		r := *a != *b
		return &r
	case "implies":
		if a != nil && !*a {
			return &t
		}
		if b != nil && *b {
			return &t
		}
		if a == nil || b == nil {
			return nil
		}
		return &f
	}
	return nil
}

func equivCollections(l, r []Value) bool {
	if len(l) != len(r) {
		return false
	}
	used := make([]bool, len(r))
	for _, a := range l {
		found := false
		for j, b := range r {
			if !used[j] && equivalent(a, b) {
				used[j], found = true, true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func arith(op string, l, r []Value) ([]Value, error) {
	if len(l) == 0 || len(r) == 0 {
		return nil, nil
	}
	if len(l) > 1 || len(r) > 1 {
		return nil, errf(Execution, "toán tử %s cần một phần tử mỗi bên", op)
	}
	a, b := l[0], r[0]
	// chuỗi
	if as, ok := a.V.(string); ok && op == "+" && !isTemporal(a) {
		bs, ok := b.V.(string)
		if !ok {
			return nil, errf(Execution, "không cộng chuỗi với %s", b.T)
		}
		return []Value{strV(as + bs)}, nil
	}
	// ngày giờ +/- Quantity
	if isTemporal(a) {
		if q, ok := asQuantity(b); ok && (op == "+" || op == "-") {
			return addDuration(a, q, op == "-")
		}
		return nil, errf(Execution, "phép toán %s không áp dụng cho %s", op, a.T)
	}
	// Quantity
	qa, aq := a.V.(Quantity)
	qb, bq := b.V.(Quantity)
	if aq || bq {
		switch {
		case aq && bq && (op == "+" || op == "-"):
			if !sameUnit(qa, qb) {
				return nil, errf(Execution, "đơn vị không khớp: %s và %s", qa.Unit, qb.Unit)
			}
			v := qa.Value + qb.Value
			if op == "-" {
				v = qa.Value - qb.Value
			}
			return []Value{{Quantity{v, qa.Unit, qa.Calendar}, "Quantity"}}, nil
		case aq && bq && (op == "*" || op == "/"):
			if op == "/" && qb.Value == 0 {
				return nil, nil
			}
			return []Value{{mulQuantity(qa, qb, op == "/"), "Quantity"}}, nil
		case aq && isNum(b) && (op == "*" || op == "/"):
			v := qa.Value * toFloat(b)
			if op == "/" {
				v = qa.Value / toFloat(b)
			}
			return []Value{{Quantity{v, qa.Unit, qa.Calendar}, "Quantity"}}, nil
		case bq && isNum(a) && op == "*":
			return []Value{{Quantity{qb.Value * toFloat(a), qb.Unit, qb.Calendar}, "Quantity"}}, nil
		}
		return nil, errf(Execution, "phép toán %s không áp dụng cho Quantity", op)
	}
	if !isNum(a) || !isNum(b) {
		return nil, errf(Execution, "phép toán %s cần số, nhận %s và %s", op, a.T, b.T)
	}
	ai, aInt := a.V.(int64)
	bi, bInt := b.V.(int64)
	both := aInt && bInt
	fa, fb := toFloat(a), toFloat(b)
	switch op {
	case "+":
		if both {
			return []Value{intV(ai + bi)}, nil
		}
		return []Value{decV(roundDec(fa+fb, 10))}, nil
	case "-":
		if both {
			return []Value{intV(ai - bi)}, nil
		}
		return []Value{decV(roundDec(fa-fb, 10))}, nil
	case "*":
		if both {
			return []Value{intV(ai * bi)}, nil
		}
		return []Value{decV(roundDec(fa*fb, 10))}, nil
	case "/":
		if fb == 0 {
			return nil, nil
		}
		return []Value{decV(roundDec(fa/fb, 8))}, nil
	case "div":
		if fb == 0 {
			return nil, nil
		}
		if both {
			return []Value{intV(ai / bi)}, nil
		}
		return []Value{intV(int64(math.Trunc(fa / fb)))}, nil
	case "mod":
		if fb == 0 {
			return nil, nil
		}
		if both {
			return []Value{intV(ai % bi)}, nil
		}
		return []Value{decV(roundDec(math.Mod(fa, fb), 10))}, nil
	}
	return nil, errf(Execution, "toán tử %s", op)
}

func addDuration(a Value, q Quantity, neg bool) ([]Value, error) {
	s := a.V.(string)
	t, ok := parseTemporal(temporalSrc(s, a.T))
	if !ok {
		return nil, errf(Execution, "ngày giờ không hợp lệ %q", s)
	}
	n := int(q.Value)
	if neg {
		n = -n
	}
	unit := calendarName(q.Unit)
	if c, ok := ucumToCalendar[q.Unit]; ok {
		unit = c
	}
	if norm(a.T) == "time" {
		d := time.Date(2000, 1, 1, t.h, t.mi, t.s, t.ms*1e6, time.UTC)
		switch unit {
		case "hour":
			d = d.Add(time.Duration(n) * time.Hour)
		case "minute":
			d = d.Add(time.Duration(n) * time.Minute)
		case "second":
			d = d.Add(time.Duration(n) * time.Second)
		case "millisecond":
			d = d.Add(time.Duration(n) * time.Millisecond)
		default:
			return nil, errf(Execution, "đơn vị %s không hợp lệ cho time", q.Unit)
		}
		return []Value{{d.Format("15:04:05.000"), "Time"}}, nil
	}
	d := time.Date(t.y, time.Month(max(t.mo, 1)), max(t.d, 1), t.h, t.mi, t.s, t.ms*1e6, time.UTC)
	switch unit {
	case "year":
		d = d.AddDate(n, 0, 0)
	case "month":
		d = d.AddDate(0, n, 0)
	case "week":
		d = d.AddDate(0, 0, 7*n)
	case "day":
		d = d.AddDate(0, 0, n)
	case "hour":
		d = d.Add(time.Duration(n) * time.Hour)
	case "minute":
		d = d.Add(time.Duration(n) * time.Minute)
	case "second":
		d = d.Add(time.Duration(n) * time.Second)
	case "millisecond":
		d = d.Add(time.Duration(n) * time.Millisecond)
	default:
		return nil, errf(Execution, "đơn vị %s không hợp lệ", q.Unit)
	}
	// giữ nguyên độ chính xác ban đầu
	out := d.Format("2006")
	if t.prec >= 2 {
		out = d.Format("2006-01")
	}
	if t.prec >= 3 {
		out = d.Format("2006-01-02")
	}
	if t.hasTime && t.prec >= 4 {
		out += "T" + d.Format("15")
		if t.prec >= 5 {
			out += d.Format(":04")
		}
		if t.prec >= 6 {
			out += d.Format(":05")
		}
		if t.prec >= 7 {
			out += d.Format(".000")
		}
		if t.tz != nil {
			if *t.tz == 0 {
				out += "Z"
			} else {
				out += tzString(*t.tz)
			}
		}
	}
	return []Value{{out, a.T}}, nil
}

func tzString(off int) string {
	sign := "+"
	if off < 0 {
		sign, off = "-", -off
	}
	return sign + twoDigits(off/60) + ":" + twoDigits(off%60)
}

func twoDigits(n int) string {
	if n < 10 {
		return "0" + string(rune('0'+n))
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}
