package fhirpath

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Value là một phần tử của collection FHIRPath. V là một trong:
// bool, string, int64, float64, Quantity, map[string]any (object JSON).
// T là tên kiểu FHIR hoặc System ("boolean", "string", "date", "Patient", "Quantity", ...).
type Value struct {
	V any
	T string
}

// Quantity là giá trị có đơn vị. Calendar=true cho đơn vị lịch (days, weeks...).
type Quantity struct {
	Value    float64
	Unit     string
	Calendar bool
}

func boolV(b bool) Value   { return Value{b, "Boolean"} }
func intV(n int64) Value   { return Value{n, "Integer"} }
func decV(f float64) Value { return Value{f, "Decimal"} }
func strV(s string) Value  { return Value{s, "String"} }
func isNum(v Value) bool   { _, a := v.V.(int64); _, b := v.V.(float64); return a || b }
func isTemporal(v Value) bool {
	switch norm(v.T) {
	case "date", "dateTime", "time", "instant":
		return true
	}
	return false
}

// systemTypeSet là các kiểu System của FHIRPath (viết hoa); kiểu FHIR nguyên thủy viết thường.
var systemTypeSet = map[string]bool{"Boolean": true, "String": true, "Integer": true, "Decimal": true,
	"Date": true, "DateTime": true, "Time": true, "Quantity": true}

// norm đưa tên kiểu System (Date) về dạng FHIR (date) để xử lý chung.
func norm(t string) string {
	if systemTypeSet[t] && t != "Quantity" {
		return strings.ToLower(t[:1]) + t[1:]
	}
	return t
}

func toFloat(v Value) float64 {
	switch x := v.V.(type) {
	case int64:
		return float64(x)
	case float64:
		return x
	}
	return math.NaN()
}

// FromJSON chuyển dữ liệu JSON đã giải mã (kể cả json.Number) thành giá trị FHIRPath, gán kiểu theo typ nếu biết.
func fromJSON(v any, typ string) Value {
	switch x := v.(type) {
	case json.Number:
		if typ == "integer" || typ == "positiveInt" || typ == "unsignedInt" {
			if n, err := x.Int64(); err == nil {
				return Value{n, typ}
			}
		}
		if typ == "decimal" {
			f, _ := x.Float64()
			return Value{f, typ}
		}
		if n, err := x.Int64(); err == nil && !strings.ContainsAny(string(x), ".eE") {
			return Value{n, "integer"}
		}
		f, _ := x.Float64()
		return Value{f, "decimal"}
	case float64:
		if typ == "integer" || typ == "positiveInt" || typ == "unsignedInt" || (typ == "" && x == math.Trunc(x)) {
			if typ == "" {
				typ = "integer"
			}
			return Value{int64(x), typ}
		}
		if typ == "" {
			typ = "decimal"
		}
		return Value{x, typ}
	case string:
		if typ == "" {
			typ = "string"
		}
		return Value{x, typ}
	case bool:
		return Value{x, "boolean"}
	case map[string]any:
		if typ == "" || typ == "Resource" {
			if rt, ok := x["resourceType"].(string); ok {
				typ = rt
			}
		}
		return Value{x, typ}
	}
	return Value{v, typ}
}

// ---------------------------------------------------------------- ngày giờ

type temporal struct {
	y, mo, d, h, mi, s, ms int
	prec                   int // 1 năm .. 7 mili giây; với time: 4..7
	tz                     *int
	hasDate, hasTime       bool
}

func parseTemporal(s string) (temporal, bool) {
	var t temporal
	rest := s
	if strings.HasPrefix(rest, "T") { // time
		rest = rest[1:]
		t.hasTime = true
		return parseTimePart(t, rest)
	}
	t.hasDate = true
	if len(rest) < 4 {
		return t, false
	}
	n, err := strconv.Atoi(rest[:4])
	if err != nil {
		return t, false
	}
	t.y, t.prec = n, 1
	rest = rest[4:]
	if strings.HasPrefix(rest, "-") && len(rest) >= 3 {
		if t.mo, err = strconv.Atoi(rest[1:3]); err != nil {
			return t, false
		}
		t.prec, rest = 2, rest[3:]
		if strings.HasPrefix(rest, "-") && len(rest) >= 3 {
			if t.d, err = strconv.Atoi(rest[1:3]); err != nil {
				return t, false
			}
			t.prec, rest = 3, rest[3:]
		}
	}
	if rest == "" {
		return t, true
	}
	if strings.HasPrefix(rest, "T") {
		t.hasTime = true
		rest = rest[1:]
		if rest == "" {
			return t, true
		}
		return parseTimePart(t, rest)
	}
	return t, false
}

func parseTimePart(t temporal, rest string) (temporal, bool) {
	// tách múi giờ
	if rest != "" {
		if rest[len(rest)-1] == 'Z' {
			z := 0
			t.tz = &z
			rest = rest[:len(rest)-1]
		} else if i := strings.LastIndexAny(rest, "+-"); i > 0 {
			z := rest[i:]
			if len(z) == 6 && z[3] == ':' {
				hh, e1 := strconv.Atoi(z[1:3])
				mm, e2 := strconv.Atoi(z[4:6])
				if e1 != nil || e2 != nil {
					return t, false
				}
				off := hh*60 + mm
				if z[0] == '-' {
					off = -off
				}
				t.tz = &off
				rest = rest[:i]
			}
		}
	}
	if len(rest) < 2 {
		return t, false
	}
	var err error
	if t.h, err = strconv.Atoi(rest[:2]); err != nil {
		return t, false
	}
	t.prec = 4
	rest = rest[2:]
	if strings.HasPrefix(rest, ":") && len(rest) >= 3 {
		if t.mi, err = strconv.Atoi(rest[1:3]); err != nil {
			return t, false
		}
		t.prec, rest = 5, rest[3:]
		if strings.HasPrefix(rest, ":") && len(rest) >= 3 {
			if t.s, err = strconv.Atoi(rest[1:3]); err != nil {
				return t, false
			}
			t.prec, rest = 6, rest[3:]
			if strings.HasPrefix(rest, ".") && len(rest) >= 2 {
				frac := rest[1:]
				if len(frac) > 3 {
					frac = frac[:3]
				}
				for len(frac) < 3 {
					frac += "0"
				}
				if t.ms, err = strconv.Atoi(frac); err != nil {
					return t, false
				}
				t.prec, rest = 7, ""
			}
		}
	}
	return t, rest == "" || t.prec == 7
}

func parseDateLiteral(s string) (Value, error) {
	if strings.HasPrefix(s, "T") {
		if _, ok := parseTemporal(s); !ok {
			return Value{}, errf(Syntax, "time không hợp lệ @%s", s)
		}
		return Value{s[1:], "Time"}, nil
	}
	t, ok := parseTemporal(s)
	if !ok {
		return Value{}, errf(Syntax, "ngày giờ không hợp lệ @%s", s)
	}
	if t.hasTime {
		return Value{strings.TrimSuffix(s, "T"), "DateTime"}, nil
	}
	return Value{s, "Date"}, nil
}

// canon chuyển về một dạng so sánh: các trường theo thứ tự, đổi về UTC nếu có múi giờ.
func (t temporal) fields() []int {
	if t.hasDate && t.tz != nil && t.prec >= 4 {
		tm := time.Date(t.y, time.Month(max(t.mo, 1)), max(t.d, 1), t.h, t.mi, t.s, t.ms*1e6, time.UTC).Add(-time.Duration(*t.tz) * time.Minute)
		return []int{tm.Year(), int(tm.Month()), tm.Day(), tm.Hour(), tm.Minute(), tm.Second(), tm.Nanosecond() / 1e6}
	}
	return []int{t.y, t.mo, t.d, t.h, t.mi, t.s, t.ms}
}

// cmpTemporal so sánh hai giá trị thời gian; known=false nếu độ chính xác khác nhau không đủ để kết luận.
func cmpTemporal(a, b Value) (c int, known bool, ok bool) {
	as, _ := a.V.(string)
	bs, _ := b.V.(string)
	ta, ok1 := parseTemporal(temporalSrc(as, a.T))
	tb, ok2 := parseTemporal(temporalSrc(bs, b.T))
	if !ok1 || !ok2 || (ta.hasDate != tb.hasDate) {
		return 0, false, false
	}
	// một bên có múi giờ, bên kia không (đến mức giờ): không kết luận được
	if ta.hasDate && ta.prec >= 4 && tb.prec >= 4 && (ta.tz == nil) != (tb.tz == nil) {
		return 0, false, true
	}
	fa, fb := ta.fields(), tb.fields()
	pa, pb := ta.prec, tb.prec
	if !ta.hasDate { // time: chỉ giờ phút giây
		fa, fb = fa[3:], fb[3:]
		pa, pb = pa-3, pb-3
		if pa >= 3 {
			pa = 4
		}
		if pb >= 3 {
			pb = 4
		}
	} else {
		if pa >= 6 {
			pa = 7
		}
		if pb >= 6 {
			pb = 7
		}
	}
	n := min(pa, pb)
	for i := 0; i < n; i++ {
		if fa[i] < fb[i] {
			return -1, true, true
		}
		if fa[i] > fb[i] {
			return 1, true, true
		}
	}
	if pa != pb {
		return 0, false, true
	}
	return 0, true, true
}

func temporalSrc(s, typ string) string {
	if norm(typ) == "time" && !strings.HasPrefix(s, "T") {
		return "T" + s
	}
	return s
}

// ---------------------------------------------------------------- so sánh

// asQuantity chuyển Quantity cấu trúc hoặc object JSON Quantity.
func asQuantity(v Value) (Quantity, bool) {
	switch x := v.V.(type) {
	case Quantity:
		return x, true
	case map[string]any:
		if v.T != "Quantity" && !(v.T == "" || v.T == "Age" || v.T == "Duration" || v.T == "Count" || v.T == "Distance" || v.T == "SimpleQuantity") {
			return Quantity{}, false
		}
		val, ok := x["value"]
		if !ok {
			return Quantity{}, false
		}
		f := toFloat(fromJSON(val, "decimal"))
		unit, _ := x["code"].(string)
		if unit == "" {
			unit, _ = x["unit"].(string)
		}
		return Quantity{Value: f, Unit: unit}, true
	}
	return Quantity{}, false
}

func calendarName(u string) string {
	u = strings.Trim(u, "{}")
	if c, ok := calendarUnits[u]; ok {
		return c
	}
	return u
}

var ucumToCalendar = map[string]string{"a": "year", "mo": "month", "wk": "week", "d": "day", "h": "hour", "min": "minute", "s": "second", "ms": "millisecond"}

func sameUnit(a, b Quantity) bool {
	if a.Unit == b.Unit {
		return true
	}
	ua, ub := calendarName(a.Unit), calendarName(b.Unit)
	if ua == ub {
		return true
	}
	if c, ok := ucumToCalendar[a.Unit]; ok && c == ub && b.Calendar {
		return true
	}
	if c, ok := ucumToCalendar[b.Unit]; ok && c == ua && a.Calendar {
		return true
	}
	return false
}

// equals triển khai toán tử = (known=false nghĩa là kết quả rỗng).
func equals(a, b Value) (eq, known bool) {
	if qa, ok := asQuantity(a); ok {
		if qb, ok := asQuantity(b); ok {
			c, ok := cmpQuantity(qa, qb)
			return ok && c == 0, true
		}
		return false, true
	}
	switch x := a.V.(type) {
	case bool:
		y, ok := b.V.(bool)
		return ok && x == y, true
	case int64, float64:
		if !isNum(b) {
			return false, true
		}
		return toFloat(a) == toFloat(b), true
	case string:
		y, ok := b.V.(string)
		if !ok {
			return false, true
		}
		if isTemporal(a) && isTemporal(b) {
			if norm(a.T) != norm(b.T) {
				// date và dateTime: có múi giờ thì chắc chắn khác nhau, không có thì chưa kết luận được
				if ta, ok := parseTemporal(temporalSrc(x, a.T)); ok && ta.hasDate && ta.hasTime && ta.tz == nil {
					return false, false
				}
				if tb, ok := parseTemporal(temporalSrc(y, b.T)); ok && tb.hasDate && tb.hasTime && tb.tz == nil {
					return false, false
				}
				return false, true
			}
			c, known, ok := cmpTemporal(a, b)
			if !ok {
				return x == y, true
			}
			return c == 0, known
		}
		return x == y, true
	case map[string]any:
		y, ok := b.V.(map[string]any)
		return ok && jsonEqual(x, y), true
	}
	return false, true
}

func jsonEqual(a, b any) bool {
	return reflect.DeepEqual(normalizeJSON(a), normalizeJSON(b))
}

func normalizeJSON(v any) any {
	switch x := v.(type) {
	case json.Number:
		f, _ := x.Float64()
		return f
	case int64:
		return float64(x)
	case map[string]any:
		m := make(map[string]any, len(x))
		for k, e := range x {
			m[k] = normalizeJSON(e)
		}
		return m
	case []any:
		s := make([]any, len(x))
		for i, e := range x {
			s[i] = normalizeJSON(e)
		}
		return s
	}
	return v
}

// equivalent triển khai toán tử ~.
func equivalent(a, b Value) bool {
	if qa, ok := asQuantity(a); ok {
		if qb, ok := asQuantity(b); ok {
			return equivQuantity(qa, qb)
		}
		return false
	}
	switch x := a.V.(type) {
	case string:
		y, ok := b.V.(string)
		if !ok {
			return false
		}
		if isTemporal(a) && isTemporal(b) {
			c, known, ok := cmpTemporal(a, b)
			return ok && known && c == 0
		}
		return normSpace(x) == normSpace(y)
	case float64, int64:
		if !isNum(b) {
			return false
		}
		fa, fb := toFloat(a), toFloat(b)
		_, ia := a.V.(int64)
		_, ib := b.V.(int64)
		if ia && ib {
			return fa == fb
		}
		// so sánh theo độ chính xác thấp hơn
		da, db := decimals(a), decimals(b)
		d := min(da, db)
		p := math.Pow(10, float64(d))
		return math.Round(fa*p) == math.Round(fb*p)
	case map[string]any:
		y, ok := b.V.(map[string]any)
		return ok && jsonEquivalent(x, y)
	}
	e, k := equals(a, b)
	return e && k
}

func decimals(v Value) int {
	if _, ok := v.V.(int64); ok {
		return 0
	}
	return decimalsF(toFloat(v))
}

func normSpace(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }

func jsonEquivalent(a, b map[string]any) bool {
	if len(a) != len(b) {
		return false
	}
	for k, x := range a {
		y, ok := b[k]
		if !ok {
			return false
		}
		switch xv := x.(type) {
		case string:
			yv, ok := y.(string)
			if !ok || normSpace(xv) != normSpace(yv) {
				return false
			}
		default:
			if !jsonEqual(x, y) {
				return false
			}
		}
	}
	return true
}

// compare so sánh thứ tự; known=false nếu rỗng.
func compare(a, b Value) (c int, known bool, err error) {
	if qa, ok := asQuantity(a); ok {
		qb, ok := asQuantity(b)
		if !ok {
			return 0, false, errf(Execution, "không so sánh được Quantity với %s", b.T)
		}
		c, ok := cmpQuantity(qa, qb)
		if !ok {
			return 0, false, nil
		}
		return c, true, nil
	}
	switch x := a.V.(type) {
	case int64, float64:
		if !isNum(b) {
			return 0, false, errf(Execution, "không so sánh được số với %s", b.T)
		}
		return cmpF(toFloat(a), toFloat(b)), true, nil
	case string:
		y, ok := b.V.(string)
		if !ok {
			return 0, false, errf(Execution, "không so sánh được chuỗi với %s", b.T)
		}
		if isTemporal(a) || isTemporal(b) {
			if isTemporal(a) && isTemporal(b) {
				c, known, ok := cmpTemporal(a, b)
				if !ok {
					return 0, false, nil
				}
				return c, known, nil
			}
			return 0, false, errf(Execution, "không so sánh được %s với %s", a.T, b.T)
		}
		return strings.Compare(x, y), true, nil
	}
	return 0, false, errf(Execution, "kiểu %s không so sánh được", a.T)
}

func cmpF(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// ---------------------------------------------------------------- tiện ích collection

func contains(col []Value, v Value) bool {
	for _, c := range col {
		if e, k := equals(c, v); k && e {
			return true
		}
	}
	return false
}

func distinct(col []Value) []Value {
	var out []Value
	for _, v := range col {
		if !contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

// toStringValue dùng cho toString() và nối chuỗi.
func toStringValue(v Value) (string, bool) {
	switch x := v.V.(type) {
	case string:
		return x, true
	case bool:
		return strconv.FormatBool(x), true
	case int64:
		return strconv.FormatInt(x, 10), true
	case float64:
		return formatDecimalValue(x), true
	case Quantity:
		if x.Calendar {
			return fmt.Sprintf("%s '{%s}'", formatDecimal(x.Value), calendarName(x.Unit)), true
		}
		return fmt.Sprintf("%s '%s'", formatDecimal(x.Value), x.Unit), true
	}
	return "", false
}

func formatDecimal(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// formatDecimalValue in số thập phân; số nguyên kiểu decimal giữ ".0" (1.0 -> "1.0").
func formatDecimalValue(f float64) string {
	s := formatDecimal(f)
	if !strings.ContainsAny(s, ".eE") {
		return s + ".0"
	}
	return s
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
