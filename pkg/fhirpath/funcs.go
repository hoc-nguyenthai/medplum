package fhirpath

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"trivita-fhir/pkg/fhir"
	"unicode/utf8"
)

// perItem đánh giá biểu thức với từng phần tử là $this.
func (c *context) perItem(input []Value, e node, f func(i int, item Value, res []Value) (stop bool, err error)) error {
	for i, it := range input {
		item, idx := it, i
		res, err := c.with([]Value{item}, &item, &idx).eval(e)
		if err != nil {
			return err
		}
		if stop, err := f(i, item, res); err != nil || stop {
			return err
		}
	}
	return nil
}

func (c *context) criteria(res []Value) (bool, error) {
	b, err := toBool(res)
	if err != nil {
		return false, err
	}
	return b != nil && *b, nil
}

func (c *context) argValues(n funcNode, i int) ([]Value, error) {
	if i >= len(n.args) {
		return nil, errf(Semantic, "%s thiếu tham số thứ %d", n.name, i+1)
	}
	return c.eval(n.args[i])
}

func (c *context) argString(n funcNode, i int) (string, bool, error) {
	v, err := c.argValues(n, i)
	if err != nil {
		return "", false, err
	}
	if len(v) == 0 {
		return "", false, nil
	}
	if len(v) > 1 {
		return "", false, errf(Execution, "%s: tham số %d phải là một giá trị", n.name, i+1)
	}
	s, ok := v[0].V.(string)
	if !ok {
		return "", false, errf(Execution, "%s: tham số %d phải là chuỗi", n.name, i+1)
	}
	return s, true, nil
}

func (c *context) argInt(n funcNode, i int) (int64, bool, error) {
	v, err := c.argValues(n, i)
	if err != nil {
		return 0, false, err
	}
	if len(v) == 0 {
		return 0, false, nil
	}
	if len(v) > 1 {
		return 0, false, errf(Execution, "%s: tham số %d phải là một giá trị", n.name, i+1)
	}
	x, ok := v[0].V.(int64)
	if !ok {
		return 0, false, errf(Execution, "%s: tham số %d phải là số nguyên", n.name, i+1)
	}
	return x, true, nil
}

func typeArg(n funcNode) (string, error) {
	if len(n.args) != 1 {
		return "", errf(Semantic, "%s cần một tên kiểu", n.name)
	}
	var parts []string
	var walk func(x node) bool
	walk = func(x node) bool {
		switch t := x.(type) {
		case identNode:
			parts = append(parts, t.name)
			return true
		case memberNode:
			if !walk(t.left) {
				return false
			}
			parts = append(parts, t.name)
			return true
		}
		return false
	}
	if !walk(n.args[0]) {
		return "", errf(Semantic, "%s cần tên kiểu", n.name)
	}
	return strings.Join(parts, "."), nil
}

func singleString(input []Value, fn string) (string, bool, error) {
	if len(input) == 0 {
		return "", false, nil
	}
	if len(input) > 1 {
		return "", false, errf(Execution, "%s cần đầu vào một phần tử", fn)
	}
	s, ok := input[0].V.(string)
	if !ok {
		return "", false, errf(Execution, "%s chỉ áp dụng cho chuỗi, nhận %s", fn, input[0].T)
	}
	return s, true, nil
}

func singleNum(input []Value, fn string) (Value, bool, error) {
	if len(input) == 0 {
		return Value{}, false, nil
	}
	if len(input) > 1 {
		return Value{}, false, errf(Execution, "%s cần đầu vào một phần tử", fn)
	}
	if !isNum(input[0]) {
		if q, ok := input[0].V.(Quantity); ok && (fn == "abs") {
			return Value{q, "Quantity"}, true, nil
		}
		return Value{}, false, errf(Execution, "%s chỉ áp dụng cho số, nhận %s", fn, input[0].T)
	}
	return input[0], true, nil
}

func (c *context) call(n funcNode) ([]Value, error) {
	var input []Value
	var err error
	if n.left != nil {
		if input, err = c.eval(n.left); err != nil {
			return nil, err
		}
	} else {
		input = c.focus
	}
	switch n.name {
	// ------------------------------------------------ tồn tại
	case "exists":
		if len(n.args) == 0 {
			return []Value{boolV(len(input) > 0)}, nil
		}
		found := false
		err := c.perItem(input, n.args[0], func(_ int, _ Value, res []Value) (bool, error) {
			ok, err := c.criteria(res)
			found = ok
			return ok, err
		})
		return []Value{boolV(found)}, err
	case "empty":
		return []Value{boolV(len(input) == 0)}, nil
	case "hasValue":
		if len(input) != 1 {
			return []Value{boolV(false)}, nil
		}
		_, isObj := input[0].V.(map[string]any)
		return []Value{boolV(!isObj)}, nil
	case "all":
		if len(n.args) != 1 {
			return nil, errf(Semantic, "all cần một tiêu chí")
		}
		all := true
		err := c.perItem(input, n.args[0], func(_ int, _ Value, res []Value) (bool, error) {
			ok, err := c.criteria(res)
			all = ok
			return !ok, err
		})
		return []Value{boolV(all)}, err
	case "allTrue", "anyTrue", "allFalse", "anyFalse":
		return boolAgg(n.name, input)
	case "count":
		return []Value{intV(int64(len(input)))}, nil
	case "distinct":
		return distinct(input), nil
	case "isDistinct":
		return []Value{boolV(len(distinct(input)) == len(input))}, nil
	// ------------------------------------------------ lọc
	case "where":
		if len(n.args) != 1 {
			return nil, errf(Semantic, "where cần một tiêu chí")
		}
		var out []Value
		err := c.perItem(input, n.args[0], func(_ int, it Value, res []Value) (bool, error) {
			ok, err := c.criteria(res)
			if ok {
				out = append(out, it)
			}
			return false, err
		})
		return out, err
	case "select":
		if len(n.args) != 1 {
			return nil, errf(Semantic, "select cần một biểu thức")
		}
		var out []Value
		err := c.perItem(input, n.args[0], func(_ int, _ Value, res []Value) (bool, error) {
			out = append(out, res...)
			return false, nil
		})
		return out, err
	case "repeat":
		if len(n.args) != 1 {
			return nil, errf(Semantic, "repeat cần một biểu thức")
		}
		var out []Value
		queue := input
		for len(queue) > 0 {
			var next []Value
			err := c.perItem(queue, n.args[0], func(_ int, _ Value, res []Value) (bool, error) {
				for _, r := range res {
					if !contains(out, r) {
						out = append(out, r)
						next = append(next, r)
					}
				}
				return false, nil
			})
			if err != nil {
				return nil, err
			}
			queue = next
		}
		return out, nil
	case "ofType":
		t, err := typeArg(n)
		if err != nil {
			return nil, err
		}
		var out []Value
		for _, v := range input {
			if isType(v, t) {
				out = append(out, v)
			}
		}
		return out, nil
	case "is", "as":
		t, err := typeArg(n)
		if err != nil {
			return nil, err
		}
		return c.eval(typeNode{n.name, valuesNode{input}, t})
	case "aggregate":
		if len(n.args) < 1 {
			return nil, errf(Semantic, "aggregate cần biểu thức")
		}
		var total []Value
		if len(n.args) > 1 {
			if total, err = c.eval(n.args[1]); err != nil {
				return nil, err
			}
		}
		for i, it := range input {
			item, idx := it, i
			cc := c.with([]Value{item}, &item, &idx)
			cc.total, cc.hasTotal = total, true
			if total, err = cc.eval(n.args[0]); err != nil {
				return nil, err
			}
		}
		return total, nil
	// ------------------------------------------------ cắt
	case "first":
		if len(input) == 0 {
			return nil, nil
		}
		return input[:1], nil
	case "last":
		if len(input) == 0 {
			return nil, nil
		}
		return input[len(input)-1:], nil
	case "tail":
		if len(input) <= 1 {
			return nil, nil
		}
		return input[1:], nil
	case "skip", "take":
		k, ok, err := c.argInt(n, 0)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, nil
		}
		k = max(k, 0)
		if n.name == "skip" {
			if int(k) >= len(input) {
				return nil, nil
			}
			return input[k:], nil
		}
		return input[:min(int(k), len(input))], nil
	case "single":
		if len(input) > 1 {
			return nil, errf(Execution, "single: có %d phần tử", len(input))
		}
		return input, nil
	case "not":
		if len(input) == 1 && isNum(input[0]) {
			switch toFloat(input[0]) {
			case 0:
				return []Value{boolV(true)}, nil
			case 1:
				return []Value{boolV(false)}, nil
			}
		}
		b, err := toBool(input)
		if err != nil {
			return nil, err
		}
		if b == nil {
			return nil, nil
		}
		return []Value{boolV(!*b)}, nil
	case "iif":
		if len(n.args) < 2 || len(n.args) > 3 {
			return nil, errf(Semantic, "iif cần 2 hoặc 3 tham số")
		}
		cond, err := c.with(input, nil, nil).eval(n.args[0])
		if err != nil {
			return nil, err
		}
		b, err := toBool(cond)
		if err != nil {
			return nil, err
		}
		if b != nil && *b {
			return c.with(input, nil, nil).eval(n.args[1])
		}
		if len(n.args) == 3 {
			return c.with(input, nil, nil).eval(n.args[2])
		}
		return nil, nil
	case "trace":
		return input, nil
	// ------------------------------------------------ tập hợp
	case "subsetOf", "supersetOf":
		other, err := c.argValues(n, 0)
		if err != nil {
			return nil, err
		}
		a, b := input, other
		if n.name == "supersetOf" {
			a, b = other, input
		}
		for _, v := range a {
			if !contains(b, v) {
				return []Value{boolV(false)}, nil
			}
		}
		return []Value{boolV(true)}, nil
	case "union":
		other, err := c.argValues(n, 0)
		if err != nil {
			return nil, err
		}
		return distinct(append(append([]Value{}, input...), other...)), nil
	case "combine":
		other, err := c.argValues(n, 0)
		if err != nil {
			return nil, err
		}
		return append(append([]Value{}, input...), other...), nil
	case "intersect", "exclude":
		other, err := c.argValues(n, 0)
		if err != nil {
			return nil, err
		}
		var out []Value
		if n.name == "intersect" {
			for _, v := range distinct(input) {
				if contains(other, v) {
					out = append(out, v)
				}
			}
			return out, nil
		}
		for _, v := range input {
			if !contains(other, v) {
				out = append(out, v)
			}
		}
		return out, nil
	case "children", "descendants":
		var out []Value
		var walk func(v Value)
		walk = func(v Value) {
			for _, ch := range childrenOf(c, v) {
				out = append(out, ch)
				if n.name == "descendants" {
					walk(ch)
				}
			}
		}
		for _, v := range input {
			walk(v)
		}
		return out, nil
	case "extension":
		url, ok, err := c.argString(n, 0)
		if err != nil || !ok {
			return nil, err
		}
		var out []Value
		if mn, ok := n.left.(memberNode); ok {
			parents, err := c.eval(mn.left)
			if err != nil {
				return nil, err
			}
			for _, p := range parents {
				if m, ok := p.V.(map[string]any); ok {
					if pm, ok := m["_"+mn.name].(map[string]any); ok {
						for _, e := range wrap(pm["extension"], "Extension") {
							if em, ok := e.V.(map[string]any); ok && em["url"] == url {
								out = append(out, e)
							}
						}
					}
				}
			}
		}
		for _, v := range input {
			exts, _ := c.child(v, "extension")
			for _, e := range exts {
				if m, ok := e.V.(map[string]any); ok && m["url"] == url {
					out = append(out, e)
				}
			}
		}
		return out, nil
	case "resolve":
		return c.resolve(input), nil
	case "htmlChecks":
		str, ok, err := singleString(input, n.name)
		if err != nil || !ok {
			return nil, err
		}
		return []Value{boolV(htmlOK(str))}, nil
	case "type":
		var out []Value
		for _, v := range input {
			ns, name := "FHIR", v.T
			if systemTypeSet[v.T] {
				ns = "System"
			}
			if name == "" {
				continue
			}
			out = append(out, Value{map[string]any{"namespace": ns, "name": name}, "TypeInfo"})
		}
		return out, nil
	// ------------------------------------------------ ngày giờ
	case "now":
		return []Value{{c.now().Format("2006-01-02T15:04:05.000-07:00"), "DateTime"}}, nil
	case "today":
		return []Value{{c.now().Format("2006-01-02"), "Date"}}, nil
	case "timeOfDay":
		return []Value{{c.now().Format("15:04:05.000"), "Time"}}, nil
	}
	return c.callTyped(n, input)
}

// valuesNode cho phép chèn một collection đã tính vào AST.
type valuesNode struct{ v []Value }

func boolAgg(name string, input []Value) ([]Value, error) {
	bools := make([]bool, 0, len(input))
	for _, v := range input {
		b, ok := v.V.(bool)
		if !ok {
			return nil, errf(Execution, "%s cần boolean", name)
		}
		bools = append(bools, b)
	}
	res := false
	switch name {
	case "allTrue":
		res = true
		for _, b := range bools {
			res = res && b
		}
	case "anyTrue":
		for _, b := range bools {
			res = res || b
		}
	case "allFalse":
		res = true
		for _, b := range bools {
			res = res && !b
		}
	case "anyFalse":
		for _, b := range bools {
			res = res || !b
		}
	}
	return []Value{boolV(res)}, nil
}

func childrenOf(c *context, v Value) []Value {
	m, ok := v.V.(map[string]any)
	if !ok {
		return nil
	}
	var out []Value
	t := v.T
	if rt, ok := m["resourceType"].(string); ok {
		t = rt
	}
	for _, k := range sortedKeys(m) {
		if k == "resourceType" {
			continue
		}
		name := strings.TrimPrefix(k, "_")
		if typ, ok := propertyType(t, name); ok {
			if strings.HasPrefix(k, "_") {
				if _, hasValue := m[name]; hasValue {
					continue // đã gộp cùng giá trị
				}
				out = append(out, wrapPrim(nil, m[k], typ)...)
			} else {
				out = append(out, wrapPrim(m[k], m["_"+k], typ)...)
			}
			continue
		}
		out = append(out, wrap(m[k], "")...)
	}
	_ = t
	return out
}

func (c *context) resolve(input []Value) []Value {
	var out []Value
	for _, v := range input {
		ref := ""
		switch x := v.V.(type) {
		case string:
			ref = x
		case map[string]any:
			ref, _ = x["reference"].(string)
		}
		if ref == "" {
			continue
		}
		if c.opts.Resolve != nil {
			if r := c.opts.Resolve(ref); r != nil {
				out = append(out, fromJSON(r, ""))
			}
			continue
		}
		// bản rút gọn từ chuỗi tham chiếu Type/id
		parts := strings.Split(strings.TrimSuffix(ref, "/"), "/")
		if len(parts) >= 2 {
			t, id := parts[len(parts)-2], parts[len(parts)-1]
			if i := strings.IndexByte(id, '/'); i >= 0 {
				id = id[:i]
			}
			if t != "" {
				out = append(out, Value{map[string]any{"resourceType": t, "id": id}, t})
			}
		}
	}
	return out
}

// callTyped: hàm chuỗi, số, chuyển đổi.
func (c *context) callTyped(n funcNode, input []Value) ([]Value, error) {
	switch n.name {
	// ------------------------------------------------ chuỗi
	case "length", "upper", "lower", "trim", "toChars":
		s, ok, err := singleString(input, n.name)
		if err != nil || !ok {
			return nil, err
		}
		switch n.name {
		case "length":
			return []Value{intV(int64(utf8.RuneCountInString(s)))}, nil
		case "upper":
			return []Value{strV(strings.ToUpper(s))}, nil
		case "lower":
			return []Value{strV(strings.ToLower(s))}, nil
		case "trim":
			return []Value{strV(strings.TrimSpace(s))}, nil
		}
		var out []Value
		for _, r := range s {
			out = append(out, strV(string(r)))
		}
		return out, nil
	case "startsWith", "endsWith", "contains", "indexOf":
		s, ok, err := singleString(input, n.name)
		if err != nil {
			return nil, err
		}
		arg, aok, err := c.argString(n, 0)
		if err != nil {
			return nil, err
		}
		if !ok || !aok {
			return nil, nil
		}
		switch n.name {
		case "startsWith":
			return []Value{boolV(strings.HasPrefix(s, arg))}, nil
		case "endsWith":
			return []Value{boolV(strings.HasSuffix(s, arg))}, nil
		case "contains":
			return []Value{boolV(strings.Contains(s, arg))}, nil
		}
		i := strings.Index(s, arg)
		if i >= 0 {
			i = utf8.RuneCountInString(s[:i])
		}
		return []Value{intV(int64(i))}, nil
	case "substring":
		s, ok, err := singleString(input, n.name)
		if err != nil || !ok {
			return nil, err
		}
		start, sok, err := c.argInt(n, 0)
		if err != nil || !sok {
			return nil, err
		}
		runes := []rune(s)
		if start < 0 || int(start) >= len(runes) {
			return nil, nil
		}
		end := int64(len(runes))
		if len(n.args) > 1 {
			l, lok, err := c.argInt(n, 1)
			if err != nil {
				return nil, err
			}
			if !lok {
				return nil, nil
			}
			end = min(start+max(l, 0), end)
		}
		return []Value{strV(string(runes[start:end]))}, nil
	case "replace":
		s, ok, err := singleString(input, n.name)
		if err != nil {
			return nil, err
		}
		a, aok, err := c.argString(n, 0)
		if err != nil {
			return nil, err
		}
		b, bok, err := c.argString(n, 1)
		if err != nil {
			return nil, err
		}
		if !ok || !aok || !bok {
			return nil, nil
		}
		return []Value{strV(strings.ReplaceAll(s, a, b))}, nil
	case "matches", "replaceMatches":
		s, ok, err := singleString(input, n.name)
		if err != nil {
			return nil, err
		}
		pat, pok, err := c.argString(n, 0)
		if err != nil {
			return nil, err
		}
		rep := ""
		rok := true
		if n.name == "replaceMatches" {
			if rep, rok, err = c.argString(n, 1); err != nil {
				return nil, err
			}
		}
		if !ok || !pok || !rok {
			return nil, nil
		}
		re, err := regexp.Compile("(?s)" + pat)
		if err != nil {
			return nil, errf(Execution, "regex không hợp lệ: %v", err)
		}
		if n.name == "matches" {
			return []Value{boolV(re.MatchString(s))}, nil
		}
		return []Value{strV(re.ReplaceAllString(s, rep))}, nil
	case "split":
		s, ok, err := singleString(input, n.name)
		if err != nil {
			return nil, err
		}
		sep, sok, err := c.argString(n, 0)
		if err != nil || !ok || !sok {
			return nil, err
		}
		var out []Value
		for _, p := range strings.Split(s, sep) {
			out = append(out, strV(p))
		}
		return out, nil
	case "join":
		sep := ""
		if len(n.args) > 0 {
			var ok bool
			var err error
			if sep, ok, err = c.argString(n, 0); err != nil || !ok {
				return nil, err
			}
		}
		parts := make([]string, 0, len(input))
		for _, v := range input {
			s, ok := v.V.(string)
			if !ok {
				return nil, errf(Execution, "join cần chuỗi")
			}
			parts = append(parts, s)
		}
		return []Value{strV(strings.Join(parts, sep))}, nil
	// ------------------------------------------------ toán học
	case "abs", "ceiling", "floor", "truncate", "round", "sqrt", "ln", "exp":
		v, ok, err := singleNum(input, n.name)
		if err != nil || !ok {
			return nil, err
		}
		if q, isQ := v.V.(Quantity); isQ {
			q.Value = math.Abs(q.Value)
			return []Value{{q, "Quantity"}}, nil
		}
		f := toFloat(v)
		_, isInt := v.V.(int64)
		switch n.name {
		case "abs":
			if isInt {
				return []Value{intV(int64(math.Abs(f)))}, nil
			}
			return []Value{decV(math.Abs(f))}, nil
		case "ceiling":
			return []Value{intV(int64(math.Ceil(f)))}, nil
		case "floor":
			return []Value{intV(int64(math.Floor(f)))}, nil
		case "truncate":
			return []Value{intV(int64(math.Trunc(f)))}, nil
		case "round":
			prec := int64(0)
			if len(n.args) > 0 {
				p, pok, err := c.argInt(n, 0)
				if err != nil || !pok {
					return nil, err
				}
				prec = p
			}
			p := math.Pow(10, float64(prec))
			return []Value{decV(math.Round(f*p) / p)}, nil
		case "sqrt":
			if f < 0 {
				return nil, nil
			}
			return []Value{decV(math.Sqrt(f))}, nil
		case "ln":
			if f <= 0 {
				return nil, nil
			}
			return []Value{decV(math.Log(f))}, nil
		default:
			return []Value{decV(math.Exp(f))}, nil
		}
	case "log", "power":
		v, ok, err := singleNum(input, n.name)
		if err != nil || !ok {
			return nil, err
		}
		o, err := c.argValues(n, 0)
		if err != nil {
			return nil, err
		}
		if len(o) != 1 || !isNum(o[0]) {
			return nil, nil
		}
		if n.name == "log" {
			if toFloat(v) <= 0 || toFloat(o[0]) <= 0 {
				return nil, nil
			}
			return []Value{decV(math.Log(toFloat(v)) / math.Log(toFloat(o[0])))}, nil
		}
		r := math.Pow(toFloat(v), toFloat(o[0]))
		if math.IsNaN(r) || math.IsInf(r, 0) {
			return nil, nil
		}
		_, ai := v.V.(int64)
		_, bi := o[0].V.(int64)
		if ai && bi && toFloat(o[0]) >= 0 {
			return []Value{intV(int64(r))}, nil
		}
		return []Value{decV(r)}, nil
	}
	return c.callConvert(n, input)
}

// callConvert: toXxx và convertsToXxx.
func (c *context) callConvert(n funcNode, input []Value) ([]Value, error) {
	name := n.name
	converts := strings.HasPrefix(name, "convertsTo")
	target := ""
	switch {
	case converts:
		target = strings.TrimPrefix(name, "convertsTo")
	case strings.HasPrefix(name, "to"):
		target = strings.TrimPrefix(name, "to")
	default:
		return nil, errf(Semantic, "hàm không tồn tại: %s", name)
	}
	switch target {
	case "Boolean", "Integer", "Decimal", "String", "Date", "DateTime", "Time", "Quantity":
	default:
		return nil, errf(Semantic, "hàm không tồn tại: %s", name)
	}
	if len(input) == 0 {
		if converts {
			return nil, nil
		}
		return nil, nil
	}
	if len(input) > 1 {
		return nil, errf(Execution, "%s cần đầu vào một phần tử", name)
	}
	res, ok := convert(input[0], target)
	if converts {
		return []Value{boolV(ok)}, nil
	}
	if !ok {
		return nil, nil
	}
	return []Value{res}, nil
}

func convert(v Value, target string) (Value, bool) {
	switch target {
	case "Boolean":
		switch x := v.V.(type) {
		case bool:
			return v, true
		case string:
			switch strings.ToLower(x) {
			case "true", "t", "yes", "y", "1", "1.0":
				return boolV(true), true
			case "false", "f", "no", "n", "0", "0.0":
				return boolV(false), true
			}
		case int64:
			if x == 0 || x == 1 {
				return boolV(x == 1), true
			}
		case float64:
			if x == 0 || x == 1 {
				return boolV(x == 1), true
			}
		}
	case "Integer":
		switch x := v.V.(type) {
		case int64:
			return intV(x), true
		case bool:
			if x {
				return intV(1), true
			}
			return intV(0), true
		case string:
			if n, err := strconv.ParseInt(x, 10, 64); err == nil {
				return intV(n), true
			}
		}
	case "Decimal":
		switch x := v.V.(type) {
		case int64:
			return decV(float64(x)), true
		case float64:
			return v, true
		case bool:
			if x {
				return decV(1), true
			}
			return decV(0), true
		case string:
			if f, err := strconv.ParseFloat(x, 64); err == nil && !strings.ContainsAny(x, "eE") {
				return decV(f), true
			}
		}
	case "String":
		if s, ok := toStringValue(v); ok {
			return strV(s), true
		}
	case "Date", "DateTime", "Time":
		s, ok := v.V.(string)
		if !ok || !isTemporal(v) && target != "Time" && target != "Date" && target != "DateTime" {
			break
		}
		if target == "Time" {
			src := temporalSrc(s, "time")
			if t, ok := parseTemporal(src); ok && !t.hasDate {
				return Value{strings.TrimPrefix(s, "T"), "Time"}, true
			}
			break
		}
		if t, ok := parseTemporal(temporalSrc(s, v.T)); ok && t.hasDate {
			switch {
			case target == "Date" && !t.hasTime:
				return Value{s, "Date"}, true
			case target == "Date" && t.hasTime:
				return Value{strings.SplitN(s, "T", 2)[0], "Date"}, true
			case target == "DateTime":
				return Value{s, "DateTime"}, true
			}
		}
	case "Quantity":
		switch x := v.V.(type) {
		case Quantity:
			return v, true
		case int64, float64:
			return Value{Quantity{toFloat(v), "1", false}, "Quantity"}, true
		case bool:
			f := 0.0
			if x {
				f = 1
			}
			return Value{Quantity{f, "1", false}, "Quantity"}, true
		case string:
			if q, ok := parseQuantity(x); ok {
				return Value{q, "Quantity"}, true
			}
		}
	}
	return Value{}, false
}

var quantityRe = regexp.MustCompile(`^(-?\d+(?:\.\d+)?)\s*(?:'([^']*)'|([a-z]+))?$`)

func parseQuantity(s string) (Quantity, bool) {
	m := quantityRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return Quantity{}, false
	}
	f, _ := strconv.ParseFloat(m[1], 64)
	switch {
	case m[2] != "":
		return Quantity{f, m[2], false}, true
	case m[3] != "":
		if u, ok := calendarUnits[m[3]]; ok {
			return Quantity{f, u, true}, true
		}
		return Quantity{}, false
	}
	return Quantity{f, "1", false}, true
}

// propertyType trả về kiểu của thuộc tính JSON prop (kể cả biến thể choice valueUri) của kiểu typ.
func propertyType(typ, prop string) (string, bool) {
	if info, ok := fhir.LookupElement(typ, prop); ok && !info.IsChoice() && len(info.Types) > 0 {
		return info.Types[0], true
	}
	for _, el := range fhir.Elements(typ) {
		if el.IsChoice() {
			for _, ty := range el.Types {
				if fhir.ChoiceProperty(el.Name, ty) == prop {
					return ty, true
				}
			}
		}
	}
	return "", false
}
