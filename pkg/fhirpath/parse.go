package fhirpath

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ---------------------------------------------------------------- lỗi

// Kind phân loại lỗi theo đặc tả FHIRPath.
type Kind string

const (
	Syntax    Kind = "syntax"
	Semantic  Kind = "semantic"
	Execution Kind = "execution"
)

// Error là lỗi phân tích hoặc đánh giá biểu thức.
type Error struct {
	Kind Kind
	Msg  string
}

func (e *Error) Error() string { return fmt.Sprintf("fhirpath %s: %s", e.Kind, e.Msg) }

func errf(k Kind, f string, a ...any) *Error { return &Error{Kind: k, Msg: fmt.Sprintf(f, a...)} }

// ---------------------------------------------------------------- lexer

type tokKind int

const (
	tEOF tokKind = iota
	tIdent
	tQuoted // `ident`
	tString
	tNumber
	tDate // @...
	tVar  // %name, $this
	tOp
)

type token struct {
	kind tokKind
	text string
	pos  int
}

func lex(src string) ([]token, error) {
	var out []token
	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			i++
		case strings.HasPrefix(src[i:], "//"):
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case strings.HasPrefix(src[i:], "/*"):
			end := strings.Index(src[i+2:], "*/")
			if end < 0 {
				return nil, errf(Syntax, "chú thích /* không đóng")
			}
			i += end + 4
		case c == '\'':
			s, n, err := lexQuoted(src, i, '\'')
			if err != nil {
				return nil, err
			}
			out = append(out, token{tString, s, i})
			i = n
		case c == '`':
			s, n, err := lexQuoted(src, i, '`')
			if err != nil {
				return nil, err
			}
			out = append(out, token{tQuoted, s, i})
			i = n
		case c >= '0' && c <= '9':
			j := i
			for j < len(src) && src[j] >= '0' && src[j] <= '9' {
				j++
			}
			if j+1 < len(src) && src[j] == '.' && src[j+1] >= '0' && src[j+1] <= '9' {
				j++
				for j < len(src) && src[j] >= '0' && src[j] <= '9' {
					j++
				}
			}
			out = append(out, token{tNumber, src[i:j], i})
			i = j
		case c == '@':
			j := lexDate(src, i+1)
			out = append(out, token{tDate, src[i+1 : j], i})
			i = j
		case c == '%' || c == '$':
			j := i + 1
			if j < len(src) && (src[j] == '`' || src[j] == '\'') {
				s, n, err := lexQuoted(src, j, src[j])
				if err != nil {
					return nil, err
				}
				out = append(out, token{tVar, string(c) + s, i})
				i = n
				continue
			}
			for j < len(src) && isIdentPart(rune(src[j])) {
				j++
			}
			out = append(out, token{tVar, src[i:j], i})
			i = j
		case isIdentStart(rune(c)):
			j := i
			for j < len(src) && isIdentPart(rune(src[j])) {
				j++
			}
			out = append(out, token{tIdent, src[i:j], i})
			i = j
		default:
			if _, size := utf8.DecodeRuneInString(src[i:]); size > 1 {
				return nil, errf(Syntax, "ký tự không hợp lệ tại %d", i)
			}
			for _, op := range []string{"!=", "!~", "<=", ">=", "=", "~", "<", ">", "+", "-", "*", "/", "&", "|", ".", ",", "(", ")", "[", "]", "{", "}"} {
				if strings.HasPrefix(src[i:], op) {
					out = append(out, token{tOp, op, i})
					i += len(op)
					goto next
				}
			}
			return nil, errf(Syntax, "ký tự không hợp lệ %q tại %d", string(c), i)
		next:
		}
	}
	return append(out, token{tEOF, "", len(src)}), nil
}

func isIdentStart(r rune) bool { return r == '_' || unicode.IsLetter(r) }
func isIdentPart(r rune) bool  { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }

func lexQuoted(src string, start int, q byte) (string, int, error) {
	var sb strings.Builder
	i := start + 1
	for i < len(src) {
		c := src[i]
		switch {
		case c == q:
			return sb.String(), i + 1, nil
		case c == '\\' && i+1 < len(src):
			i++
			switch src[i] {
			case 'n':
				sb.WriteByte('\n')
			case 'r':
				sb.WriteByte('\r')
			case 't':
				sb.WriteByte('\t')
			case 'f':
				sb.WriteByte('\f')
			case 'u':
				if i+4 >= len(src) {
					return "", 0, errf(Syntax, "escape \\u không đủ chữ số")
				}
				n, err := strconv.ParseUint(src[i+1:i+5], 16, 32)
				if err != nil {
					return "", 0, errf(Syntax, "escape \\u không hợp lệ")
				}
				sb.WriteRune(rune(n))
				i += 4
			case '\'', '"', '`', '\\', '/':
				sb.WriteByte(src[i])
			default:
				return "", 0, errf(Syntax, "escape không hợp lệ \\%c", src[i])
			}
			i++
		default:
			sb.WriteByte(c)
			i++
		}
	}
	return "", 0, errf(Syntax, "chuỗi không đóng")
}

// ---------------------------------------------------------------- AST

type node interface{}

type (
	litNode    struct{ v Value }
	emptyNode  struct{}
	identNode  struct{ name string }
	varNode    struct{ name string }
	memberNode struct {
		left node
		name string
	}
	funcNode struct {
		left node // nil nếu gọi trực tiếp
		name string
		args []node
	}
	indexNode struct{ left, idx node }
	unaryNode struct {
		op string
		x  node
	}
	binNode struct {
		op   string
		l, r node
	}
	typeNode struct {
		op  string // is | as
		x   node
		typ string
	}
)

// ---------------------------------------------------------------- parser

type parser struct {
	toks []token
	pos  int
}

// Parse phân tích biểu thức FHIRPath.
func parse(src string) (node, error) {
	toks, err := lex(src)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	n, err := p.expr(0)
	if err != nil {
		return nil, err
	}
	if p.cur().kind != tEOF {
		return nil, errf(Syntax, "thừa %q tại %d", p.cur().text, p.cur().pos)
	}
	return n, nil
}

func (p *parser) cur() token { return p.toks[p.pos] }
func (p *parser) next() token {
	t := p.toks[p.pos]
	if t.kind != tEOF {
		p.pos++
	}
	return t
}
func (p *parser) isOp(s string) bool { t := p.cur(); return t.kind == tOp && t.text == s }
func (p *parser) isWord(s string) bool {
	t := p.cur()
	return t.kind == tIdent && t.text == s
}

// binary precedence: số lớn = ràng buộc chặt hơn.
func binPrec(t token) (int, string) {
	switch t.kind {
	case tOp:
		switch t.text {
		case "*", "/":
			return 10, t.text
		case "+", "-", "&":
			return 9, t.text
		case "|":
			return 7, t.text
		case "<", ">", "<=", ">=":
			return 6, t.text
		case "=", "~", "!=", "!~":
			return 5, t.text
		}
	case tIdent:
		switch t.text {
		case "div", "mod":
			return 10, t.text
		case "is", "as":
			return 6, t.text
		case "in", "contains":
			return 4, t.text
		case "and":
			return 3, t.text
		case "xor", "or":
			return 2, t.text
		case "implies":
			return 1, t.text
		}
	}
	return -1, ""
}

func (p *parser) expr(min int) (node, error) {
	left, err := p.unary()
	if err != nil {
		return nil, err
	}
	for {
		prec, op := binPrec(p.cur())
		if prec < 0 || prec < min {
			return left, nil
		}
		p.next()
		if op == "is" || op == "as" {
			typ, err := p.typeSpec()
			if err != nil {
				return nil, err
			}
			left = typeNode{op, left, typ}
			continue
		}
		right, err := p.expr(prec + 1)
		if err != nil {
			return nil, err
		}
		left = binNode{op, left, right}
	}
}

func (p *parser) typeSpec() (string, error) {
	t := p.next()
	if t.kind != tIdent && t.kind != tQuoted {
		return "", errf(Syntax, "cần tên kiểu tại %d", t.pos)
	}
	name := t.text
	for p.isOp(".") && (p.toks[p.pos+1].kind == tIdent || p.toks[p.pos+1].kind == tQuoted) {
		p.next()
		name += "." + p.next().text
	}
	return name, nil
}

func (p *parser) unary() (node, error) {
	if p.isOp("-") || p.isOp("+") {
		op := p.next().text
		x, err := p.unary()
		if err != nil {
			return nil, err
		}
		if l, ok := x.(litNode); ok && op == "-" {
			switch v := l.v.V.(type) {
			case int64:
				return litNode{Value{-v, l.v.T}}, nil
			case float64:
				return litNode{Value{-v, l.v.T}}, nil
			}
		}
		return unaryNode{op, x}, nil
	}
	return p.postfix()
}

func (p *parser) postfix() (node, error) {
	n, err := p.primary()
	if err != nil {
		return nil, err
	}
	for {
		switch {
		case p.isOp("."):
			p.next()
			t := p.next()
			if t.kind != tIdent && t.kind != tQuoted {
				return nil, errf(Syntax, "cần tên sau '.' tại %d", t.pos)
			}
			if p.isOp("(") && t.kind == tIdent {
				args, err := p.args()
				if err != nil {
					return nil, err
				}
				n = funcNode{n, t.text, args}
			} else {
				n = memberNode{n, t.text}
			}
		case p.isOp("["):
			p.next()
			idx, err := p.expr(0)
			if err != nil {
				return nil, err
			}
			if !p.isOp("]") {
				return nil, errf(Syntax, "thiếu ']' tại %d", p.cur().pos)
			}
			p.next()
			n = indexNode{n, idx}
		default:
			return n, nil
		}
	}
}

func (p *parser) args() ([]node, error) {
	p.next() // (
	var out []node
	if p.isOp(")") {
		p.next()
		return out, nil
	}
	for {
		a, err := p.expr(0)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
		if p.isOp(",") {
			p.next()
			continue
		}
		if p.isOp(")") {
			p.next()
			return out, nil
		}
		return nil, errf(Syntax, "cần ',' hoặc ')' tại %d", p.cur().pos)
	}
}

var calendarUnits = map[string]string{
	"year": "year", "years": "year", "month": "month", "months": "month", "week": "week", "weeks": "week",
	"day": "day", "days": "day", "hour": "hour", "hours": "hour", "minute": "minute", "minutes": "minute",
	"second": "second", "seconds": "second", "millisecond": "millisecond", "milliseconds": "millisecond",
}

func (p *parser) primary() (node, error) {
	t := p.next()
	switch t.kind {
	case tString:
		return litNode{strV(t.text)}, nil
	case tNumber:
		var v Value
		if strings.Contains(t.text, ".") {
			f, _ := strconv.ParseFloat(t.text, 64)
			v = decV(f)
		} else {
			n, err := strconv.ParseInt(t.text, 10, 64)
			if err != nil {
				return nil, errf(Syntax, "số quá lớn %s", t.text)
			}
			v = intV(n)
		}
		// Quantity: số + 'đơn vị' hoặc đơn vị lịch
		f := toFloat(v)
		if c := p.cur(); c.kind == tString {
			p.next()
			return litNode{Value{Quantity{f, c.text, false}, "Quantity"}}, nil
		} else if c.kind == tIdent {
			if u, ok := calendarUnits[c.text]; ok {
				p.next()
				return litNode{Value{Quantity{f, u, true}, "Quantity"}}, nil
			}
		}
		return litNode{v}, nil
	case tDate:
		v, err := parseDateLiteral(t.text)
		if err != nil {
			return nil, err
		}
		return litNode{v}, nil
	case tVar:
		return varNode{t.text}, nil
	case tQuoted:
		if p.isOp("(") {
			return nil, errf(Syntax, "tên hàm không được đặt trong dấu `")
		}
		return identNode{t.text}, nil
	case tIdent:
		switch t.text {
		case "true":
			return litNode{boolV(true)}, nil
		case "false":
			return litNode{boolV(false)}, nil
		}
		if p.isOp("(") {
			args, err := p.args()
			if err != nil {
				return nil, err
			}
			return funcNode{nil, t.text, args}, nil
		}
		return identNode{t.text}, nil
	case tOp:
		switch t.text {
		case "(":
			n, err := p.expr(0)
			if err != nil {
				return nil, err
			}
			if !p.isOp(")") {
				return nil, errf(Syntax, "thiếu ')' tại %d", p.cur().pos)
			}
			p.next()
			return n, nil
		case "{":
			if !p.isOp("}") {
				return nil, errf(Syntax, "cần '}' tại %d", p.cur().pos)
			}
			p.next()
			return emptyNode{}, nil
		}
	}
	return nil, errf(Syntax, "không mong đợi %q tại %d", t.text, t.pos)
}

// lexDate trả về vị trí kết thúc của literal ngày giờ bắt đầu tại i (sau dấu '@').
func lexDate(src string, i int) int {
	digits := func(j int) int {
		for j < len(src) && src[j] >= '0' && src[j] <= '9' {
			j++
		}
		return j
	}
	timePart := func(j int) int {
		j = digits(j)
		for j < len(src) && src[j] == ':' {
			j = digits(j + 1)
		}
		if j+1 < len(src) && src[j] == '.' && src[j+1] >= '0' && src[j+1] <= '9' {
			j = digits(j + 1)
		}
		return j
	}
	j := i
	if j < len(src) && src[j] == 'T' {
		return timePart(j + 1)
	}
	j = digits(j)
	for k := 0; k < 2 && j+1 < len(src) && src[j] == '-' && src[j+1] >= '0' && src[j+1] <= '9'; k++ {
		j = digits(j + 1)
	}
	if j < len(src) && src[j] == 'T' {
		j = timePart(j + 1)
		switch {
		case j < len(src) && src[j] == 'Z':
			j++
		case j+1 < len(src) && (src[j] == '+' || src[j] == '-') && src[j+1] >= '0' && src[j+1] <= '9':
			j = digits(j + 1)
			if j < len(src) && src[j] == ':' {
				j = digits(j + 1)
			}
		}
	}
	return j
}
