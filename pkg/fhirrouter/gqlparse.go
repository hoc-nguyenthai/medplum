package fhirrouter

import (
	"fmt"
	"strconv"
	"strings"
)

// Bộ phân tích GraphQL tối giản cho truy vấn FHIR (xem graphql.go): query (có hoặc không từ khóa), biến, alias, tham số,
// selection lồng nhau, fragment có tên và inline fragment, chú thích #. Không hỗ trợ mutation, subscription, directive
// (bị bỏ qua có kiểm tra), kiểu và schema.

type gqlDoc struct {
	ops   []*gqlOp
	frags map[string]*gqlFrag
}

type gqlOp struct {
	name string
	vars map[string]gqlValue // giá trị mặc định (nil nếu không có); khóa là tên biến
	sel  []*gqlSel
}

type gqlFrag struct {
	typeCond string
	sel      []*gqlSel
}

type selKind int

const (
	selField selKind = iota
	selSpread
	selInline
)

type gqlSel struct {
	kind     selKind
	alias    string
	name     string // field: tên field; spread: tên fragment
	args     []gqlArg
	sel      []*gqlSel
	typeCond string // inline fragment
}

type gqlArg struct {
	name string
	val  gqlValue
}

// gqlValue: gqlVar, gqlEnum, string, int64, float64, bool, nil, []gqlValue, map[string]gqlValue.
type gqlValue any

type gqlVar string
type gqlEnum string

type gqlTok struct {
	kind string // name, int, float, string, punct, eof
	text string
	pos  int
}

type gqlLexer struct {
	src  string
	pos  int
	toks []gqlTok
}

func lexGraphQL(src string) ([]gqlTok, error) {
	l := &gqlLexer{src: src}
	for {
		l.skip()
		if l.pos >= len(l.src) {
			l.toks = append(l.toks, gqlTok{"eof", "", l.pos})
			return l.toks, nil
		}
		c := l.src[l.pos]
		start := l.pos
		switch {
		case c == '.':
			if !strings.HasPrefix(l.src[l.pos:], "...") {
				return nil, fmt.Errorf("ký tự '.' không hợp lệ tại %d", l.pos)
			}
			l.pos += 3
			l.toks = append(l.toks, gqlTok{"punct", "...", start})
		case strings.IndexByte("{}()[]:!$=@|&", c) >= 0:
			l.pos++
			l.toks = append(l.toks, gqlTok{"punct", string(c), start})
		case c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z'):
			for l.pos < len(l.src) && (l.src[l.pos] == '_' || (l.src[l.pos] >= '0' && l.src[l.pos] <= '9') || (l.src[l.pos] >= 'A' && l.src[l.pos] <= 'Z') || (l.src[l.pos] >= 'a' && l.src[l.pos] <= 'z')) {
				l.pos++
			}
			l.toks = append(l.toks, gqlTok{"name", l.src[start:l.pos], start})
		case c == '-' || (c >= '0' && c <= '9'):
			isFloat := false
			l.pos++
			for l.pos < len(l.src) {
				d := l.src[l.pos]
				if d >= '0' && d <= '9' {
					l.pos++
				} else if d == '.' || d == 'e' || d == 'E' || ((d == '+' || d == '-') && (l.src[l.pos-1] == 'e' || l.src[l.pos-1] == 'E')) {
					isFloat = true
					l.pos++
				} else {
					break
				}
			}
			k := "int"
			if isFloat {
				k = "float"
			}
			l.toks = append(l.toks, gqlTok{k, l.src[start:l.pos], start})
		case c == '"':
			s, err := l.str()
			if err != nil {
				return nil, err
			}
			l.toks = append(l.toks, gqlTok{"string", s, start})
		default:
			return nil, fmt.Errorf("ký tự %q không hợp lệ tại %d", c, l.pos)
		}
	}
}

func (l *gqlLexer) skip() {
	for l.pos < len(l.src) {
		switch c := l.src[l.pos]; {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == ',' || c == 0xEF || c == 0xBB || c == 0xBF:
			l.pos++
		case c == '#':
			for l.pos < len(l.src) && l.src[l.pos] != '\n' {
				l.pos++
			}
		default:
			return
		}
	}
}

func (l *gqlLexer) str() (string, error) {
	if strings.HasPrefix(l.src[l.pos:], `"""`) {
		end := strings.Index(l.src[l.pos+3:], `"""`)
		if end < 0 {
			return "", fmt.Errorf("chuỗi nhiều dòng không đóng")
		}
		s := l.src[l.pos+3 : l.pos+3+end]
		l.pos += end + 6
		return s, nil
	}
	l.pos++
	var b strings.Builder
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		switch {
		case c == '"':
			l.pos++
			return b.String(), nil
		case c == '\n':
			return "", fmt.Errorf("chuỗi không đóng")
		case c == '\\':
			l.pos++
			if l.pos >= len(l.src) {
				return "", fmt.Errorf("escape dở dang")
			}
			switch e := l.src[l.pos]; e {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			case 'b':
				b.WriteByte('\b')
			case 'f':
				b.WriteByte('\f')
			case '"', '\\', '/':
				b.WriteByte(e)
			case 'u':
				if l.pos+4 >= len(l.src) {
					return "", fmt.Errorf("\\u dở dang")
				}
				n, err := strconv.ParseUint(l.src[l.pos+1:l.pos+5], 16, 32)
				if err != nil {
					return "", fmt.Errorf("\\u không hợp lệ")
				}
				b.WriteRune(rune(n))
				l.pos += 4
			default:
				return "", fmt.Errorf("escape \\%c không hợp lệ", e)
			}
			l.pos++
		default:
			b.WriteByte(c)
			l.pos++
		}
	}
	return "", fmt.Errorf("chuỗi không đóng")
}

type gqlParser struct {
	toks []gqlTok
	i    int
}

func (p *gqlParser) peek() gqlTok { return p.toks[p.i] }
func (p *gqlParser) next() gqlTok {
	t := p.toks[p.i]
	if t.kind != "eof" {
		p.i++
	}
	return t
}

func (p *gqlParser) is(punct string) bool {
	t := p.peek()
	return t.kind == "punct" && t.text == punct
}

func (p *gqlParser) expect(punct string) error {
	if !p.is(punct) {
		return p.errf("cần %q", punct)
	}
	p.i++
	return nil
}

func (p *gqlParser) errf(format string, a ...any) error {
	t := p.peek()
	near := t.text
	if t.kind == "eof" {
		near = "hết đầu vào"
	}
	return fmt.Errorf("lỗi cú pháp GraphQL tại %d (%s): %s", t.pos, near, fmt.Sprintf(format, a...))
}

// maxGraphQLQueryBytes giới hạn kích thước truy vấn.
const maxGraphQLQueryBytes = 64 << 10

func parseGraphQL(src string) (*gqlDoc, error) {
	if len(src) > maxGraphQLQueryBytes {
		return nil, fmt.Errorf("truy vấn quá dài (> %d byte)", maxGraphQLQueryBytes)
	}
	toks, err := lexGraphQL(src)
	if err != nil {
		return nil, err
	}
	p := &gqlParser{toks: toks}
	doc := &gqlDoc{frags: map[string]*gqlFrag{}}
	for p.peek().kind != "eof" {
		switch {
		case p.is("{"):
			sel, err := p.selectionSet(0)
			if err != nil {
				return nil, err
			}
			doc.ops = append(doc.ops, &gqlOp{sel: sel})
		case p.peek().kind == "name" && p.peek().text == "query":
			p.next()
			op, err := p.operation()
			if err != nil {
				return nil, err
			}
			doc.ops = append(doc.ops, op)
		case p.peek().kind == "name" && (p.peek().text == "mutation" || p.peek().text == "subscription"):
			return nil, p.errf("%s chưa được hỗ trợ (chỉ có query)", p.peek().text)
		case p.peek().kind == "name" && p.peek().text == "fragment":
			p.next()
			name := p.next()
			if name.kind != "name" || name.text == "on" {
				return nil, p.errf("tên fragment không hợp lệ")
			}
			if on := p.next(); on.kind != "name" || on.text != "on" {
				return nil, p.errf("cần 'on'")
			}
			tc := p.next()
			if tc.kind != "name" {
				return nil, p.errf("cần tên kiểu")
			}
			sel, err := p.selectionSet(0)
			if err != nil {
				return nil, err
			}
			if _, dup := doc.frags[name.text]; dup {
				return nil, fmt.Errorf("fragment %s khai báo hai lần", name.text)
			}
			doc.frags[name.text] = &gqlFrag{typeCond: tc.text, sel: sel}
		default:
			return nil, p.errf("cần query hoặc fragment")
		}
	}
	if len(doc.ops) == 0 {
		return nil, fmt.Errorf("không có thao tác query nào")
	}
	return doc, nil
}

func (p *gqlParser) operation() (*gqlOp, error) {
	op := &gqlOp{vars: map[string]gqlValue{}}
	if p.peek().kind == "name" {
		op.name = p.next().text
	}
	if p.is("(") {
		p.next()
		for !p.is(")") {
			if err := p.expect("$"); err != nil {
				return nil, err
			}
			name := p.next()
			if name.kind != "name" {
				return nil, p.errf("cần tên biến")
			}
			if err := p.expect(":"); err != nil {
				return nil, err
			}
			if err := p.skipType(); err != nil {
				return nil, err
			}
			var def gqlValue
			if p.is("=") {
				p.next()
				v, err := p.value(false, 0)
				if err != nil {
					return nil, err
				}
				def = v
			}
			op.vars[name.text] = def
		}
		p.next()
	}
	if err := p.skipDirectives(); err != nil {
		return nil, err
	}
	sel, err := p.selectionSet(0)
	if err != nil {
		return nil, err
	}
	op.sel = sel
	return op, nil
}

func (p *gqlParser) skipType() error {
	if p.is("[") {
		p.next()
		if err := p.skipType(); err != nil {
			return err
		}
		if err := p.expect("]"); err != nil {
			return err
		}
	} else if t := p.next(); t.kind != "name" {
		return p.errf("cần tên kiểu")
	}
	if p.is("!") {
		p.next()
	}
	return nil
}

func (p *gqlParser) skipDirectives() error {
	for p.is("@") {
		p.next()
		if p.next().kind != "name" {
			return p.errf("cần tên directive")
		}
		if p.is("(") {
			if _, err := p.arguments(); err != nil {
				return err
			}
		}
	}
	return nil
}

// maxGraphQLDepth là độ sâu tối đa của selection.
const maxGraphQLDepth = 12

func (p *gqlParser) selectionSet(depth int) ([]*gqlSel, error) {
	if depth >= maxGraphQLDepth {
		return nil, p.errf("selection lồng quá sâu (> %d)", maxGraphQLDepth)
	}
	if err := p.expect("{"); err != nil {
		return nil, err
	}
	var out []*gqlSel
	for !p.is("}") {
		if p.peek().kind == "eof" {
			return nil, p.errf("thiếu '}'")
		}
		s, err := p.selection(depth)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	p.next()
	if len(out) == 0 {
		return nil, p.errf("selection rỗng")
	}
	return out, nil
}

func (p *gqlParser) selection(depth int) (*gqlSel, error) {
	if p.is("...") {
		p.next()
		if t := p.peek(); t.kind == "name" && t.text != "on" {
			p.next()
			if err := p.skipDirectives(); err != nil {
				return nil, err
			}
			return &gqlSel{kind: selSpread, name: t.text}, nil
		}
		s := &gqlSel{kind: selInline}
		if t := p.peek(); t.kind == "name" && t.text == "on" {
			p.next()
			tc := p.next()
			if tc.kind != "name" {
				return nil, p.errf("cần tên kiểu")
			}
			s.typeCond = tc.text
		}
		if err := p.skipDirectives(); err != nil {
			return nil, err
		}
		sub, err := p.selectionSet(depth + 1)
		if err != nil {
			return nil, err
		}
		s.sel = sub
		return s, nil
	}
	name := p.next()
	if name.kind != "name" {
		return nil, p.errf("cần tên field")
	}
	s := &gqlSel{kind: selField, name: name.text}
	if p.is(":") {
		p.next()
		real := p.next()
		if real.kind != "name" {
			return nil, p.errf("cần tên field sau alias")
		}
		s.alias, s.name = name.text, real.text
	}
	if p.is("(") {
		args, err := p.arguments()
		if err != nil {
			return nil, err
		}
		s.args = args
	}
	if err := p.skipDirectives(); err != nil {
		return nil, err
	}
	if p.is("{") {
		sub, err := p.selectionSet(depth + 1)
		if err != nil {
			return nil, err
		}
		s.sel = sub
	}
	return s, nil
}

func (p *gqlParser) arguments() ([]gqlArg, error) {
	if err := p.expect("("); err != nil {
		return nil, err
	}
	var out []gqlArg
	for !p.is(")") {
		name := p.next()
		if name.kind != "name" {
			return nil, p.errf("cần tên tham số")
		}
		if err := p.expect(":"); err != nil {
			return nil, err
		}
		v, err := p.value(true, 0)
		if err != nil {
			return nil, err
		}
		out = append(out, gqlArg{name.text, v})
	}
	p.next()
	return out, nil
}

func (p *gqlParser) value(allowVar bool, depth int) (gqlValue, error) {
	if depth > 8 {
		return nil, p.errf("giá trị lồng quá sâu")
	}
	t := p.next()
	switch t.kind {
	case "int":
		n, err := strconv.ParseInt(t.text, 10, 64)
		if err != nil {
			return nil, p.errf("số nguyên không hợp lệ")
		}
		return n, nil
	case "float":
		f, err := strconv.ParseFloat(t.text, 64)
		if err != nil {
			return nil, p.errf("số thực không hợp lệ")
		}
		return f, nil
	case "string":
		return t.text, nil
	case "name":
		switch t.text {
		case "true":
			return true, nil
		case "false":
			return false, nil
		case "null":
			return nil, nil
		}
		return gqlEnum(t.text), nil
	case "punct":
		switch t.text {
		case "$":
			if !allowVar {
				return nil, p.errf("không dùng biến ở đây")
			}
			n := p.next()
			if n.kind != "name" {
				return nil, p.errf("cần tên biến")
			}
			return gqlVar(n.text), nil
		case "[":
			list := []gqlValue{}
			for !p.is("]") {
				v, err := p.value(allowVar, depth+1)
				if err != nil {
					return nil, err
				}
				list = append(list, v)
			}
			p.next()
			return list, nil
		case "{":
			obj := map[string]gqlValue{}
			for !p.is("}") {
				k := p.next()
				if k.kind != "name" {
					return nil, p.errf("cần tên trường")
				}
				if err := p.expect(":"); err != nil {
					return nil, err
				}
				v, err := p.value(allowVar, depth+1)
				if err != nil {
					return nil, err
				}
				obj[k.text] = v
			}
			p.next()
			return obj, nil
		}
	}
	p.i--
	return nil, p.errf("giá trị không hợp lệ")
}
