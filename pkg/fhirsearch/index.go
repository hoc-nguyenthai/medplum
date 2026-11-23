package fhirsearch

import (
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirpath"
)

// File này cung cấp hai nửa của việc search bằng chỉ mục (SQL):
//   - Extract: trích các giá trị của một search parameter khi GHI resource;
//   - Conds: biên dịch một Filter thành các điều kiện khi TÌM.
// Cả hai dùng chung bộ trích giá trị và quy tắc so sánh với Match nên ngữ nghĩa giống nhau.

// StringEntry là một giá trị của tham số kiểu string.
type StringEntry struct{ Raw, Norm string }

// TokenEntry là một giá trị của tham số kiểu token. HasSystem=false nghĩa là không có system.
type TokenEntry struct {
	System    string
	HasSystem bool
	Code      string
}

// RefEntry là một tham chiếu. Type/ID tách từ dạng chuẩn hóa tại dấu "/" cuối cùng (rỗng nếu không có).
type RefEntry struct {
	Raw, Target, Type, ID string
}

// DateEntry là khoảng [Lo, Hi) tính bằng micro giây Unix.
type DateEntry struct{ Lo, Hi int64 }

// QuantityEntry là một giá trị kiểu quantity.
type QuantityEntry struct {
	Value              float64
	System, Code, Unit string
}

// Index là các giá trị đã trích của một search parameter trên một resource, theo thứ tự đánh giá.
type Index struct {
	Strings    []StringEntry
	Tokens     []TokenEntry
	Texts      []string // văn bản đã chuẩn hóa dùng cho :text
	References []RefEntry
	Dates      []DateEntry
	Numbers    []float64
	Quantities []QuantityEntry
	URIs       []StringEntry
}

// Empty cho biết không trích được giá trị nào.
func (ix *Index) Empty() bool {
	return len(ix.Strings)+len(ix.Tokens)+len(ix.Texts)+len(ix.References)+len(ix.Dates)+len(ix.Numbers)+len(ix.Quantities)+len(ix.URIs) == 0
}

func micros(t time.Time) int64 { return t.UnixMicro() }

// Extract trích các giá trị của search parameter def từ resource. Tham số composite và special bị bỏ qua.
func Extract(doc map[string]any, def fhir.SearchParamDef) (*Index, error) {
	ix := &Index{}
	if def.Type == "composite" || def.Type == "special" {
		return ix, nil
	}
	vals, err := Values(doc, def)
	if err != nil {
		return nil, err
	}
	return extractValues(vals, def)
}

// extractValues chuyển các giá trị đã đánh giá thành mục chỉ mục theo kiểu của def.
func extractValues(vals []fhirpath.Value, def fhir.SearchParamDef) (*Index, error) {
	ix := &Index{}
	for _, v := range vals {
		switch def.Type {
		case "string":
			for _, s := range stringsOf(v) {
				ix.Strings = append(ix.Strings, StringEntry{s, fold(s)})
			}
		case "token":
			for _, t := range tokensOf(v) {
				ix.Tokens = append(ix.Tokens, TokenEntry{t.system, t.system != "", t.code})
			}
			for _, s := range texts(v) {
				ix.Texts = append(ix.Texts, fold(s))
			}
		case "reference":
			s := refString(v)
			if s == "" {
				continue
			}
			n := NormalizeReference(s)
			e := RefEntry{Raw: s, Target: n}
			if i := strings.LastIndex(n, "/"); i >= 0 {
				e.Type, e.ID = n[:i], n[i+1:]
			}
			ix.References = append(ix.References, e)
		case "date":
			for _, iv := range intervalsOf(v) {
				ix.Dates = append(ix.Dates, DateEntry{micros(iv.lo), micros(iv.hi)})
			}
		case "number":
			if n, ok := numValue(v); ok {
				ix.Numbers = append(ix.Numbers, n)
			}
		case "quantity":
			if m, ok := v.V.(map[string]any); ok {
				if n, ok := toFloat(m["value"]); ok {
					s, _ := m["system"].(string)
					c, _ := m["code"].(string)
					u, _ := m["unit"].(string)
					ix.Quantities = append(ix.Quantities, QuantityEntry{n, s, c, u})
				}
			}
		case "uri":
			if s, ok := v.V.(string); ok {
				ix.URIs = append(ix.URIs, StringEntry{s, fold(s)})
			}
		}
	}
	return ix, nil
}

// Cond là một giá trị trong danh sách OR của một Filter, đã phân tích sẵn để dịch sang SQL.
type Cond struct {
	Kind string // string, token, reference, date, number, quantity, uri

	Mode string // string: prefix|exact|contains; uri: eq|below|above
	Raw  string // giá trị gốc (đã bỏ escape)
	Norm string // string: giá trị đã chuẩn hóa; token text: văn bản chuẩn hóa
	Len  int    // số rune của Norm (cho so sánh tiền tố)

	// token
	Text      bool
	System    string
	HasSystem bool // có dấu |
	Code      string

	// reference
	Ref       string // dạng chuẩn hóa của giá trị tìm
	IDOnly    string // khác rỗng nếu chỉ có id (khớp theo phần id và kiểu đích)
	Targets   []string
	AnyTarget bool

	// date / number / quantity
	Prefix string
	Lo, Hi int64   // date: khoảng tìm kiếm (µs); Margin cho ap
	Margin int64   // date ap
	V      float64 // number/quantity: giá trị
	NLo    float64 // number/quantity: cận dưới ẩn
	NHi    float64 // number/quantity: cận trên ẩn (loại trừ)
	QSys   string  // quantity
	QCode  string  // quantity
}

// Conds biên dịch các giá trị OR của một Filter.
func Conds(f Filter) ([]Cond, error) {
	out := make([]Cond, 0, len(f.Values))
	for _, q := range f.Values {
		c, err := cond(f, q)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

func cond(f Filter, q string) (Cond, error) {
	switch f.Def.Type {
	case "string":
		raw := Unescape(q)
		n := fold(raw)
		mode := "prefix"
		if f.Modifier == "exact" || f.Modifier == "contains" {
			mode = f.Modifier
		}
		return Cond{Kind: "string", Mode: mode, Raw: raw, Norm: n, Len: utf8.RuneCountInString(n)}, nil
	case "token":
		if f.Modifier == "text" {
			n := fold(Unescape(q))
			return Cond{Kind: "token", Text: true, Norm: n, Len: utf8.RuneCountInString(n)}, nil
		}
		sys, code, has := splitPipe(q)
		return Cond{Kind: "token", System: sys, HasSystem: has, Code: code}, nil
	case "reference":
		raw := Unescape(q)
		if f.TypeModifier != "" && !strings.Contains(raw, "/") {
			raw = f.TypeModifier + "/" + raw
		}
		c := Cond{Kind: "reference", Raw: raw, Ref: NormalizeReference(raw)}
		if !strings.Contains(raw, "/") && !strings.Contains(raw, ":") {
			c.IDOnly = raw
			c.Targets = f.Def.Target
			c.AnyTarget = len(f.Def.Target) == 0
			for _, t := range f.Def.Target {
				if t == "Resource" {
					c.AnyTarget = true
				}
			}
		}
		return c, nil
	case "date":
		prefix, rest := splitPrefix(Unescape(q))
		s, ok := parseDate(rest)
		if !ok {
			return Cond{}, errf("ngày không hợp lệ: %q", rest)
		}
		c := Cond{Kind: "date", Prefix: prefix, Lo: micros(s.lo), Hi: micros(s.hi)}
		if prefix == "ap" {
			d := time.Since(s.lo)
			if d < 0 {
				d = -d
			}
			c.Margin = (d / 10).Microseconds()
		}
		return c, nil
	case "number":
		prefix, rest := splitPrefix(Unescape(q))
		v, lo, hi, err := numberRange(rest)
		if err != nil {
			return Cond{}, err
		}
		return Cond{Kind: "number", Prefix: prefix, V: v, NLo: lo, NHi: hi}, nil
	case "quantity":
		parts := strings.Split(Unescape(q), "|")
		prefix, num := splitPrefix(parts[0])
		v, lo, hi, err := numberRange(num)
		if err != nil {
			return Cond{}, err
		}
		c := Cond{Kind: "quantity", Prefix: prefix, V: v, NLo: lo, NHi: hi}
		if len(parts) > 1 {
			c.QSys = parts[1]
		}
		if len(parts) > 2 {
			c.QCode = parts[2]
		}
		return c, nil
	case "uri":
		mode := "eq"
		if f.Modifier == "below" || f.Modifier == "above" {
			mode = f.Modifier
		}
		return Cond{Kind: "uri", Mode: mode, Raw: Unescape(q)}, nil
	}
	return Cond{}, errf("kiểu tham số %s chưa được hỗ trợ", f.Def.Type)
}

// NumberMargin là ngưỡng của prefix ap cho số: |v - giá trị| <= NumberMargin * |giá trị|.
const NumberMargin = 0.1

// AbsNumberMargin trả về ngưỡng tuyệt đối của ap cho giá trị v.
func AbsNumberMargin(v float64) float64 { return NumberMargin * math.Abs(v) }

// SortKind cho biết khóa sắp xếp của một tham số lưu ở cột nào: "string", "token", "reference", "date", "number", "quantity".
func SortKind(def fhir.SearchParamDef) string { return def.Type }
