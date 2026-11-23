package fhirsearch

import (
	"strings"
)

// Tham số _filter (FHIR search filter): biểu thức logic trên các điều kiện search, ví dụ
//
//	Patient?_filter=name co "smith" and (birthdate lt 1980-01-01 or gender eq male)
//	Observation?_filter=not (status eq final) and code eq http://loinc.org|8480-6
//	Patient?_filter=email pr true
//
// Cú pháp: điều kiện `tham-số toán-tử giá-trị`, `not ( ... )`, `( ... )`, `and`, `or` (không phân biệt hoa thường;
// not > and > or). Giá trị là chuỗi trong nháy kép (\" và \\ là escape) hoặc một từ không chứa khoảng trắng và dấu ngoặc.
// Nhiều tham số _filter là AND với nhau và với các tham số search khác.
//
// Toán tử theo kiểu tham số (đối sánh dùng cùng bộ so khớp với search thường):
//
//	string:                  eq (khớp chính xác, phân biệt hoa thường), ne, co (chứa), sw (bắt đầu bằng), pr
//	token, reference:        eq, ne, pr
//	uri:                     eq, ne, sw, pr
//	date, number, quantity:  eq, ne, gt, lt, ge, le, sa, eb, ap, pr
//
// pr (present) nhận true hoặc false. Không hỗ trợ: ew, ss, sb, in, re, po, tham số composite/special, modifier và
// đường dẫn chained (a.b) trong tham số, _filter ở search hệ thống (cần chỉ định loại resource).

// Expr là một nút của biểu thức _filter.
type Expr struct {
	Op   string // "and", "or", "not" hoặc "leaf"
	Kids []*Expr
	Leaf *Filter
}

// Match đánh giá biểu thức trên một resource.
func (e *Expr) Match(res map[string]any) (bool, error) {
	switch e.Op {
	case "leaf":
		return matchFilter(res, *e.Leaf)
	case "not":
		ok, err := e.Kids[0].Match(res)
		return !ok, err
	case "and", "or":
		for _, k := range e.Kids {
			ok, err := k.Match(res)
			if err != nil {
				return false, err
			}
			if ok && e.Op == "or" {
				return true, nil
			}
			if !ok && e.Op == "and" {
				return false, nil
			}
		}
		return e.Op == "and", nil
	}
	return false, errf("nút _filter không hợp lệ")
}

type ftok struct {
	kind string // lp, rp, word, str
	text string
}

func lexFilter(s string) ([]ftok, error) {
	var out []ftok
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '(':
			out = append(out, ftok{"lp", "("})
			i++
		case c == ')':
			out = append(out, ftok{"rp", ")"})
			i++
		case c == '"':
			var b strings.Builder
			i++
			closed := false
			for i < len(s) {
				if s[i] == '\\' && i+1 < len(s) && (s[i+1] == '"' || s[i+1] == '\\') {
					b.WriteByte(s[i+1])
					i += 2
					continue
				}
				if s[i] == '"' {
					closed = true
					i++
					break
				}
				b.WriteByte(s[i])
				i++
			}
			if !closed {
				return nil, errf("_filter: chuỗi không đóng")
			}
			out = append(out, ftok{"str", b.String()})
		default:
			j := i
			for j < len(s) && s[j] != ' ' && s[j] != '\t' && s[j] != '\n' && s[j] != '\r' && s[j] != '(' && s[j] != ')' && s[j] != '"' {
				j++
			}
			out = append(out, ftok{"word", s[i:j]})
			i = j
		}
	}
	return out, nil
}

type filterParser struct {
	toks []ftok
	i    int
	rt   string
	n    int // số điều kiện, để giới hạn kích thước
}

// Giới hạn chống lạm dụng.
const (
	maxFilterLeaves = 50
	maxFilterDepth  = 12
)

// ParseFilterExpr phân tích giá trị của _filter cho search trên resourceType rt.
func ParseFilterExpr(rt, src string) (*Expr, error) {
	if rt == "" {
		return nil, errf("_filter cần chỉ định loại resource")
	}
	toks, err := lexFilter(src)
	if err != nil {
		return nil, err
	}
	if len(toks) == 0 {
		return nil, errf("_filter rỗng")
	}
	p := &filterParser{toks: toks, rt: rt}
	e, err := p.or(0)
	if err != nil {
		return nil, err
	}
	if p.i != len(p.toks) {
		return nil, errf("_filter: thừa %q sau biểu thức", p.toks[p.i].text)
	}
	return e, nil
}

func (p *filterParser) peekWord(w string) bool {
	return p.i < len(p.toks) && p.toks[p.i].kind == "word" && strings.EqualFold(p.toks[p.i].text, w)
}

func (p *filterParser) or(depth int) (*Expr, error) {
	first, err := p.and(depth)
	if err != nil {
		return nil, err
	}
	kids := []*Expr{first}
	for p.peekWord("or") {
		p.i++
		k, err := p.and(depth)
		if err != nil {
			return nil, err
		}
		kids = append(kids, k)
	}
	if len(kids) == 1 {
		return first, nil
	}
	return &Expr{Op: "or", Kids: kids}, nil
}

func (p *filterParser) and(depth int) (*Expr, error) {
	first, err := p.factor(depth)
	if err != nil {
		return nil, err
	}
	kids := []*Expr{first}
	for p.peekWord("and") {
		p.i++
		k, err := p.factor(depth)
		if err != nil {
			return nil, err
		}
		kids = append(kids, k)
	}
	if len(kids) == 1 {
		return first, nil
	}
	return &Expr{Op: "and", Kids: kids}, nil
}

func (p *filterParser) factor(depth int) (*Expr, error) {
	if depth >= maxFilterDepth {
		return nil, errf("_filter lồng quá sâu (> %d)", maxFilterDepth)
	}
	if p.i >= len(p.toks) {
		return nil, errf("_filter: biểu thức dở dang")
	}
	t := p.toks[p.i]
	switch {
	case t.kind == "lp":
		p.i++
		e, err := p.or(depth + 1)
		if err != nil {
			return nil, err
		}
		if p.i >= len(p.toks) || p.toks[p.i].kind != "rp" {
			return nil, errf("_filter: thiếu dấu ')'")
		}
		p.i++
		return e, nil
	case p.peekWord("not"):
		p.i++
		if p.i >= len(p.toks) || p.toks[p.i].kind != "lp" {
			return nil, errf("_filter: sau 'not' cần dấu '('")
		}
		p.i++
		e, err := p.or(depth + 1)
		if err != nil {
			return nil, err
		}
		if p.i >= len(p.toks) || p.toks[p.i].kind != "rp" {
			return nil, errf("_filter: thiếu dấu ')'")
		}
		p.i++
		return &Expr{Op: "not", Kids: []*Expr{e}}, nil
	case t.kind == "word":
		return p.leaf()
	}
	return nil, errf("_filter: không hiểu %q", t.text)
}

func (p *filterParser) leaf() (*Expr, error) {
	if p.n++; p.n > maxFilterLeaves {
		return nil, errf("_filter có quá nhiều điều kiện (> %d)", maxFilterLeaves)
	}
	path := p.toks[p.i].text
	p.i++
	if p.i+1 >= len(p.toks) || p.toks[p.i].kind != "word" || (p.toks[p.i+1].kind != "word" && p.toks[p.i+1].kind != "str") {
		return nil, errf("_filter: điều kiện %q cần dạng 'tham-số toán-tử giá-trị'", path)
	}
	op := strings.ToLower(p.toks[p.i].text)
	val := p.toks[p.i+1].text
	p.i += 2
	if strings.ContainsAny(path, ".:") {
		return nil, errf("_filter: tham số %q không được có modifier hoặc đường dẫn chained", path)
	}
	def, ok := Lookup(p.rt, path)
	if !ok {
		return nil, errf("_filter: tham số search không hợp lệ: %s", path)
	}
	leaf := func(mod, v string) *Expr {
		return &Expr{Op: "leaf", Leaf: &Filter{Code: path, Def: def, Modifier: mod, Values: []string{strings.ReplaceAll(v, ",", `\,`)}}}
	}
	if op == "pr" {
		switch strings.ToLower(val) {
		case "true":
			return leaf("missing", "false"), nil
		case "false":
			return leaf("missing", "true"), nil
		}
		return nil, errf("_filter: pr cần true hoặc false")
	}
	not := func(e *Expr) *Expr { return &Expr{Op: "not", Kids: []*Expr{e}} }
	switch def.Type {
	case "string":
		switch op {
		case "eq":
			return leaf("exact", val), nil
		case "ne":
			return not(leaf("exact", val)), nil
		case "co":
			return leaf("contains", val), nil
		case "sw":
			return leaf("", val), nil
		}
	case "token", "reference":
		switch op {
		case "eq":
			return leaf("", val), nil
		case "ne":
			return not(leaf("", val)), nil
		}
	case "uri":
		switch op {
		case "eq":
			return leaf("", val), nil
		case "ne":
			return not(leaf("", val)), nil
		case "sw":
			return leaf("below", val), nil
		}
	case "date", "number", "quantity":
		switch op {
		case "eq":
			return leaf("", val), nil
		case "ne", "gt", "lt", "ge", "le", "sa", "eb", "ap":
			return leaf("", op+val), nil
		}
	default:
		return nil, errf("_filter: tham số %s kiểu %s chưa được hỗ trợ", path, def.Type)
	}
	return nil, errf("_filter: toán tử %q không dùng được cho tham số %s kiểu %s", op, path, def.Type)
}
