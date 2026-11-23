package fhirsearch

import (
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirpath"
)

var exprCache sync.Map // biểu thức -> *fhirpath.Expression

func compile(src string) (*fhirpath.Expression, error) {
	if v, ok := exprCache.Load(src); ok {
		return v.(*fhirpath.Expression), nil
	}
	e, err := fhirpath.Compile(src)
	if err != nil {
		return nil, err
	}
	exprCache.Store(src, e)
	return e, nil
}

// Values trích các giá trị của một search parameter từ resource (object JSON đã giải mã).
func Values(res map[string]any, def fhir.SearchParamDef) ([]fhirpath.Value, error) {
	if def.Expression == "" {
		return nil, nil
	}
	e, err := compile(def.Expression)
	if err != nil {
		return nil, err
	}
	return e.Eval(res, nil)
}

// Match cho biết resource có thỏa mọi bộ lọc của yêu cầu không (không xét sort/paging/include).
func Match(res map[string]any, req *Request) (bool, error) {
	if len(req.Chained) > 0 {
		return false, errf("chained search và _has chưa được hỗ trợ khi đối sánh từng resource")
	}
	if rt, _ := res["resourceType"].(string); req.ResourceType != "" && rt != req.ResourceType {
		return false, nil
	}
	if len(req.Types) > 0 {
		if rt, _ := res["resourceType"].(string); !slices.Contains(req.Types, rt) {
			return false, nil
		}
	}
	for _, f := range req.Filters {
		// với search hệ thống, bỏ qua tham số không áp dụng cho kiểu của resource này
		if req.ResourceType == "" {
			rt, _ := res["resourceType"].(string)
			def, ok := Lookup(rt, f.Code)
			if !ok {
				return false, nil
			}
			f.Def = def // mỗi kiểu có định nghĩa riêng cho cùng một code (Patient.name khác Organization.name)
		}
		ok, err := matchFilter(res, f)
		if err != nil || !ok {
			return false, err
		}
	}
	for _, e := range req.Exprs {
		ok, err := e.Match(res)
		if err != nil || !ok {
			return false, err
		}
	}
	return true, nil
}

func matchFilter(res map[string]any, f Filter) (bool, error) {
	if f.IsTerminology() {
		return false, errf("modifier :%s của token cần terminology và chưa được hỗ trợ khi đối sánh từng resource", f.Modifier)
	}
	if f.Def.Type == "composite" {
		return matchComposite(res, f)
	}
	vals, err := Values(res, f.Def)
	if err != nil {
		return false, err
	}
	if f.Modifier == "missing" {
		return (len(vals) == 0) == (f.Values[0] == "true"), nil
	}
	if len(vals) == 0 {
		return f.Modifier == "not", nil // :not khớp cả resource không có giá trị
	}
	matched := false
	for _, q := range f.Values {
		ok, err := matchOne(f, vals, q)
		if err != nil {
			return false, err
		}
		if ok {
			matched = true
			break
		}
	}
	if f.Modifier == "not" {
		return !matched, nil
	}
	return matched, nil
}

func matchOne(f Filter, vals []fhirpath.Value, q string) (bool, error) {
	switch f.Def.Type {
	case "string":
		return matchString(f, vals, q), nil
	case "token":
		return matchToken(f, vals, q), nil
	case "reference":
		return matchReference(f, vals, q), nil
	case "date":
		return matchDate(vals, q)
	case "number":
		return matchNumber(vals, q)
	case "quantity":
		return matchQuantity(vals, q)
	case "uri":
		return matchURI(f, vals, q), nil
	}
	return false, errf("kiểu tham số %s chưa được hỗ trợ", f.Def.Type)
}

// ---------------------------------------------------------------- string

func stringsOf(v fhirpath.Value) []string {
	switch x := v.V.(type) {
	case string:
		return []string{x}
	case map[string]any:
		var out []string
		add := func(keys ...string) {
			for _, k := range keys {
				switch s := x[k].(type) {
				case string:
					out = append(out, s)
				case []any:
					for _, e := range s {
						if es, ok := e.(string); ok {
							out = append(out, es)
						}
					}
				}
			}
		}
		switch v.T {
		case "HumanName":
			add("text", "family", "given", "prefix", "suffix")
		case "Address":
			add("text", "line", "city", "district", "state", "postalCode", "country")
		default:
			add("text", "value", "display", "code", "name")
		}
		return out
	}
	return nil
}

// fold chuẩn hóa để so sánh chuỗi: chữ thường, bỏ dấu (NFD rồi bỏ dấu kết hợp; đ -> d).
func fold(s string) string {
	var sb strings.Builder
	for _, r := range norm.NFD.String(strings.ToLower(s)) {
		switch {
		case unicode.Is(unicode.Mn, r):
		case r == 'đ':
			sb.WriteRune('d')
		default:
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

func matchString(f Filter, vals []fhirpath.Value, q string) bool {
	q = Unescape(q)
	for _, v := range vals {
		for _, s := range stringsOf(v) {
			switch f.Modifier {
			case "exact":
				if s == q {
					return true
				}
			case "contains":
				if strings.Contains(fold(s), fold(q)) {
					return true
				}
			default:
				if strings.HasPrefix(fold(s), fold(q)) {
					return true
				}
			}
		}
	}
	return false
}

// ---------------------------------------------------------------- token

type token struct{ system, code string }

func tokensOf(v fhirpath.Value) []token {
	switch x := v.V.(type) {
	case string:
		return []token{{"", x}}
	case bool:
		return []token{{"", strconv.FormatBool(x)}}
	case map[string]any:
		str := func(k string) string { s, _ := x[k].(string); return s }
		switch v.T {
		case "CodeableConcept":
			var out []token
			if cs, ok := x["coding"].([]any); ok {
				for _, c := range cs {
					if cm, ok := c.(map[string]any); ok {
						s, _ := cm["system"].(string)
						c2, _ := cm["code"].(string)
						out = append(out, token{s, c2})
					}
				}
			}
			return out
		case "Coding", "Quantity", "Age", "Duration", "Count", "Distance", "Money":
			return []token{{str("system"), str("code")}}
		case "Identifier", "ContactPoint":
			return []token{{str("system"), str("value")}}
		}
		if _, ok := x["code"]; ok {
			return []token{{str("system"), str("code")}}
		}
		if _, ok := x["value"]; ok {
			return []token{{str("system"), str("value")}}
		}
	}
	return nil
}

func texts(v fhirpath.Value) []string {
	x, ok := v.V.(map[string]any)
	if !ok {
		return nil
	}
	var out []string
	if s, ok := x["text"].(string); ok {
		out = append(out, s)
	}
	if s, ok := x["display"].(string); ok {
		out = append(out, s)
	}
	if cs, ok := x["coding"].([]any); ok {
		for _, c := range cs {
			if cm, ok := c.(map[string]any); ok {
				if s, ok := cm["display"].(string); ok {
					out = append(out, s)
				}
			}
		}
	}
	return out
}

// splitPipe tách theo | chưa escape; ok=false nếu không có dấu |.
func splitPipe(q string) (before, after string, ok bool) {
	for i := 0; i < len(q); i++ {
		if q[i] == '\\' {
			i++
			continue
		}
		if q[i] == '|' {
			return Unescape(q[:i]), Unescape(q[i+1:]), true
		}
	}
	return "", Unescape(q), false
}

func matchToken(f Filter, vals []fhirpath.Value, q string) bool {
	if f.Modifier == "text" {
		qq := fold(Unescape(q))
		for _, v := range vals {
			for _, t := range texts(v) {
				if strings.HasPrefix(fold(t), qq) {
					return true
				}
			}
		}
		return false
	}
	sys, code, hasSys := splitPipe(q)
	for _, v := range vals {
		for _, t := range tokensOf(v) {
			switch {
			case !hasSys && t.code == code:
				return true
			case hasSys && sys == "" && t.system == "" && t.code == code:
				return true
			case hasSys && sys != "" && t.system == sys && (code == "" || t.code == code):
				return true
			}
		}
	}
	return false
}

// ---------------------------------------------------------------- reference

// refString lấy chuỗi tham chiếu từ Reference hoặc chuỗi.
func refString(v fhirpath.Value) string {
	switch x := v.V.(type) {
	case string:
		return x
	case map[string]any:
		s, _ := x["reference"].(string)
		return s
	}
	return ""
}

// NormalizeReference đưa tham chiếu về dạng Type/id (bỏ phần _history và tiền tố URL tuyệt đối).
func NormalizeReference(ref string) string {
	if strings.HasPrefix(ref, "urn:") || strings.HasPrefix(ref, "#") {
		return ref
	}
	if i := strings.Index(ref, "/_history/"); i >= 0 {
		ref = ref[:i]
	}
	parts := strings.Split(strings.TrimSuffix(ref, "/"), "/")
	if len(parts) >= 2 && fhir.IsResourceType(parts[len(parts)-2]) {
		return parts[len(parts)-2] + "/" + parts[len(parts)-1]
	}
	return ref
}

// References trả về các tham chiếu (dạng Type/id) mà resource chứa theo một search parameter.
func References(res map[string]any, def fhir.SearchParamDef) ([]string, error) {
	vals, err := Values(res, def)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, v := range vals {
		if s := refString(v); s != "" {
			out = append(out, NormalizeReference(s))
		}
	}
	return out, nil
}

func matchReference(f Filter, vals []fhirpath.Value, q string) bool {
	q = Unescape(q)
	if f.TypeModifier != "" && !strings.Contains(q, "/") {
		q = f.TypeModifier + "/" + q
	}
	nq := NormalizeReference(q)
	for _, v := range vals {
		s := refString(v)
		if s == "" {
			continue
		}
		n := NormalizeReference(s)
		if n == nq || s == q {
			return true
		}
		if !strings.Contains(q, "/") && !strings.Contains(q, ":") {
			// chỉ có id: khớp phần id, và kiểu phải nằm trong target của tham số (nếu khai báo)
			if i := strings.LastIndex(n, "/"); i >= 0 && n[i+1:] == q {
				t := n[:i]
				if len(f.Def.Target) == 0 || slices.Contains(f.Def.Target, t) || slices.Contains(f.Def.Target, "Resource") {
					return true
				}
			}
		}
	}
	return false
}

// ---------------------------------------------------------------- uri

func matchURI(f Filter, vals []fhirpath.Value, q string) bool {
	q = Unescape(q)
	for _, v := range vals {
		s, ok := v.V.(string)
		if !ok {
			continue
		}
		switch f.Modifier {
		case "below":
			if strings.HasPrefix(s, q) {
				return true
			}
		case "above":
			if strings.HasPrefix(q, s) {
				return true
			}
		default:
			if s == q {
				return true
			}
		}
	}
	return false
}

// ---------------------------------------------------------------- prefix, số

var prefixes = []string{"eq", "ne", "gt", "lt", "ge", "le", "sa", "eb", "ap"}

// splitPrefix tách tiền tố so sánh (mặc định eq).
func splitPrefix(q string) (prefix, rest string) {
	if len(q) > 2 {
		for _, p := range prefixes {
			if strings.HasPrefix(q, p) {
				c := q[2]
				if c == '-' || c == '+' || c == '.' || (c >= '0' && c <= '9') {
					return p, q[2:]
				}
			}
		}
	}
	return "eq", q
}

// numberRange trả về khoảng ẩn của số theo độ chính xác chữ số: 5.4 -> [5.35, 5.45).
func numberRange(s string) (v, lo, hi float64, err error) {
	v, err = strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, 0, 0, errf("số không hợp lệ: %q", s)
	}
	mant := strings.ToLower(s)
	exp := 0
	if i := strings.IndexByte(mant, 'e'); i >= 0 {
		exp, _ = strconv.Atoi(mant[i+1:])
		mant = mant[:i]
	}
	places := 0
	if i := strings.IndexByte(mant, '.'); i >= 0 {
		places = len(mant) - i - 1
	}
	half := 0.5 * math.Pow(10, float64(exp-places))
	return v, v - half, v + half, nil
}

func numValue(v fhirpath.Value) (float64, bool) {
	switch x := v.V.(type) {
	case int64:
		return float64(x), true
	case float64:
		return x, true
	}
	return 0, false
}

func cmpNumber(prefix string, target, qv, lo, hi float64) bool {
	switch prefix {
	case "eq":
		return target >= lo && target < hi
	case "ne":
		return !(target >= lo && target < hi)
	case "gt", "sa":
		return target > qv
	case "lt", "eb":
		return target < qv
	case "ge":
		return target >= qv
	case "le":
		return target <= qv
	case "ap":
		return math.Abs(target-qv) <= 0.1*math.Abs(qv)
	}
	return false
}

func matchNumber(vals []fhirpath.Value, q string) (bool, error) {
	prefix, rest := splitPrefix(Unescape(q))
	qv, lo, hi, err := numberRange(rest)
	if err != nil {
		return false, err
	}
	for _, v := range vals {
		if n, ok := numValue(v); ok && cmpNumber(prefix, n, qv, lo, hi) {
			return true, nil
		}
	}
	return false, nil
}

func matchQuantity(vals []fhirpath.Value, q string) (bool, error) {
	parts := strings.Split(Unescape(q), "|")
	prefix, num := splitPrefix(parts[0])
	qv, lo, hi, err := numberRange(num)
	if err != nil {
		return false, err
	}
	var system, code string
	if len(parts) > 1 {
		system = parts[1]
	}
	if len(parts) > 2 {
		code = parts[2]
	}
	for _, v := range vals {
		m, ok := v.V.(map[string]any)
		if !ok {
			continue
		}
		n, ok := toFloat(m["value"])
		if !ok || !cmpNumber(prefix, n, qv, lo, hi) {
			continue
		}
		if code != "" {
			c, _ := m["code"].(string)
			u, _ := m["unit"].(string)
			if c != code && u != code {
				continue
			}
			if s, _ := m["system"].(string); system != "" && s != system {
				continue
			}
		}
		return true, nil
	}
	return false, nil
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int64:
		return float64(x), true
	case interface{ Float64() (float64, error) }:
		f, err := x.Float64()
		return f, err == nil
	}
	return 0, false
}

// ---------------------------------------------------------------- date

type interval struct{ lo, hi time.Time } // [lo, hi)

var (
	minTime = time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC)
	maxTime = time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)
)

// parseDate chuyển ngày giờ FHIR từng phần thành khoảng [lo, hi).
func parseDate(s string) (interval, bool) {
	base, loc := s, time.UTC
	if strings.HasSuffix(s, "Z") {
		base = s[:len(s)-1]
	} else if i := len(s) - 6; i > 10 && (s[i] == '+' || s[i] == '-') && s[i+3] == ':' {
		h, e1 := strconv.Atoi(s[i+1 : i+3])
		m, e2 := strconv.Atoi(s[i+4:])
		if e1 != nil || e2 != nil {
			return interval{}, false
		}
		off := h*3600 + m*60
		if s[i] == '-' {
			off = -off
		}
		loc, base = time.FixedZone("", off), s[:i]
	}
	var layout string
	var step func(time.Time) time.Time
	switch {
	case len(base) == 4:
		layout, step = "2006", func(t time.Time) time.Time { return t.AddDate(1, 0, 0) }
	case len(base) == 7:
		layout, step = "2006-01", func(t time.Time) time.Time { return t.AddDate(0, 1, 0) }
	case len(base) == 10:
		layout, step = "2006-01-02", func(t time.Time) time.Time { return t.AddDate(0, 0, 1) }
	case len(base) == 13:
		layout, step = "2006-01-02T15", func(t time.Time) time.Time { return t.Add(time.Hour) }
	case len(base) == 16:
		layout, step = "2006-01-02T15:04", func(t time.Time) time.Time { return t.Add(time.Minute) }
	case len(base) == 19:
		layout, step = "2006-01-02T15:04:05", func(t time.Time) time.Time { return t.Add(time.Second) }
	case len(base) > 20 && base[19] == '.':
		layout, step = "2006-01-02T15:04:05.999999999", func(t time.Time) time.Time { return t.Add(time.Millisecond) }
	default:
		return interval{}, false
	}
	t, err := time.ParseInLocation(layout, base, loc)
	if err != nil {
		return interval{}, false
	}
	t = t.UTC()
	return interval{t, step(t)}, true
}

// intervalsOf chuyển giá trị ngày (chuỗi, Period) thành khoảng thời gian.
func intervalsOf(v fhirpath.Value) []interval {
	switch x := v.V.(type) {
	case string:
		if iv, ok := parseDate(x); ok {
			return []interval{iv}
		}
	case map[string]any:
		lo, hi := minTime, maxTime
		hasAny := false
		if s, ok := x["start"].(string); ok {
			if iv, ok := parseDate(s); ok {
				lo, hasAny = iv.lo, true
			}
		}
		if s, ok := x["end"].(string); ok {
			if iv, ok := parseDate(s); ok {
				hi, hasAny = iv.hi, true
			}
		}
		if hasAny {
			return []interval{{lo, hi}}
		}
	}
	return nil
}

func cmpInterval(prefix string, t, s interval) bool {
	within := !t.lo.Before(s.lo) && !t.hi.After(s.hi)
	switch prefix {
	case "eq":
		return within
	case "ne":
		return !within
	case "gt":
		return t.hi.After(s.hi)
	case "lt":
		return t.lo.Before(s.lo)
	case "ge":
		return t.hi.After(s.hi) || within
	case "le":
		return t.lo.Before(s.lo) || within
	case "sa":
		return !t.lo.Before(s.hi)
	case "eb":
		return !t.hi.After(s.lo)
	case "ap":
		d := time.Since(s.lo)
		if d < 0 {
			d = -d
		}
		margin := d / 10
		return t.lo.Before(s.hi.Add(margin)) && t.hi.After(s.lo.Add(-margin))
	}
	return false
}

func matchDate(vals []fhirpath.Value, q string) (bool, error) {
	prefix, rest := splitPrefix(Unescape(q))
	s, ok := parseDate(rest)
	if !ok {
		return false, errf("ngày không hợp lệ: %q", rest)
	}
	for _, v := range vals {
		for _, t := range intervalsOf(v) {
			if cmpInterval(prefix, t, s) {
				return true, nil
			}
		}
	}
	return false, nil
}

// ---------------------------------------------------------------- sort, include

type sortKey struct {
	has  bool
	num  float64
	str  string
	isNo bool
}

func keyOf(res map[string]any, def fhir.SearchParamDef) sortKey {
	vals, err := Values(res, def)
	if err != nil || len(vals) == 0 {
		return sortKey{}
	}
	v := vals[0]
	switch def.Type {
	case "date":
		if ivs := intervalsOf(v); len(ivs) > 0 {
			return sortKey{has: true, num: float64(ivs[0].lo.UnixNano()), isNo: true}
		}
	case "number", "quantity":
		if n, ok := numValue(v); ok {
			return sortKey{has: true, num: n, isNo: true}
		}
		if m, ok := v.V.(map[string]any); ok {
			if f, ok := toFloat(m["value"]); ok {
				return sortKey{has: true, num: f, isNo: true}
			}
		}
	case "token":
		if ts := tokensOf(v); len(ts) > 0 {
			return sortKey{has: true, str: ts[0].code}
		}
	case "reference":
		return sortKey{has: true, str: refString(v)}
	default:
		if ss := stringsOf(v); len(ss) > 0 {
			return sortKey{has: true, str: fold(ss[0])}
		}
	}
	return sortKey{}
}

// Sort sắp xếp ổn định các resource theo các khóa của yêu cầu (thiếu giá trị xếp cuối).
func Sort(resources []map[string]any, rules []SortRule) {
	if len(rules) == 0 {
		return
	}
	idx := make([]int, len(resources))
	all := make([][]sortKey, len(resources))
	for i := range resources {
		idx[i] = i
		ks := make([]sortKey, len(rules))
		for j, r := range rules {
			ks[j] = keyOf(resources[i], r.Def)
		}
		all[i] = ks
	}
	sort.SliceStable(idx, func(a, b int) bool {
		for j, r := range rules {
			ka, kb := all[idx[a]][j], all[idx[b]][j]
			if ka.has != kb.has {
				return ka.has // thiếu giá trị xếp cuối
			}
			if !ka.has {
				continue
			}
			var c int
			if ka.isNo {
				c = cmpFloat(ka.num, kb.num)
			} else {
				c = strings.Compare(ka.str, kb.str)
			}
			if c == 0 {
				continue
			}
			if r.Descending {
				return c > 0
			}
			return c < 0
		}
		return false
	})
	sorted := make([]map[string]any, len(resources))
	for i, j := range idx {
		sorted[i] = resources[j]
	}
	copy(resources, sorted)
}

func cmpFloat(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
