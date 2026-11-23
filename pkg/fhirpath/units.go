package fhirpath

import (
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Bộ UCUM rút gọn: đủ để so sánh và tính toán các đơn vị phổ biến (khối lượng, chiều dài, thể tích, thời gian)
// và đơn vị lịch của FHIRPath. Đơn vị không nhận ra được coi là một chiều riêng (chỉ bằng chính nó).

type unitAtom struct {
	dim    map[string]int
	factor float64
}

var atoms = map[string]unitAtom{
	"m": {map[string]int{"L": 1}, 1}, "g": {map[string]int{"M": 1}, 1}, "s": {map[string]int{"T": 1}, 1},
	"L": {map[string]int{"L": 3}, 1e-3}, "l": {map[string]int{"L": 3}, 1e-3},
	"min": {map[string]int{"T": 1}, 60}, "h": {map[string]int{"T": 1}, 3600}, "d": {map[string]int{"T": 1}, 86400},
	"wk": {map[string]int{"T": 1}, 604800}, "mo": {map[string]int{"T": 1}, 2629800}, "a": {map[string]int{"T": 1}, 31557600},
	"mol": {map[string]int{"N": 1}, 1},
	"%":   {map[string]int{}, 0.01}, "1": {map[string]int{}, 1},
	"[lb_av]": {map[string]int{"M": 1}, 453.59237}, "[oz_av]": {map[string]int{"M": 1}, 28.349523125},
	"[in_i]": {map[string]int{"L": 1}, 0.0254}, "[ft_i]": {map[string]int{"L": 1}, 0.3048},
}

var prefixes = map[string]float64{
	"n": 1e-9, "u": 1e-6, "m": 1e-3, "c": 1e-2, "d": 1e-1, "da": 10, "h": 100, "k": 1e3, "M": 1e6, "G": 1e9,
}

var calendarToUCUM = map[string]string{
	"year": "a", "month": "mo", "week": "wk", "day": "d", "hour": "h", "minute": "min", "second": "s", "millisecond": "ms",
}

var termRe = regexp.MustCompile(`^(.*?)(-?\d+)?$`)

type unitInfo struct {
	dims   map[string]int
	factor float64
}

func (u unitInfo) key() string {
	keys := make([]string, 0, len(u.dims))
	for k, e := range u.dims {
		if e != 0 {
			keys = append(keys, k+strconv.Itoa(e))
		}
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

func resolveAtom(a string) unitInfo {
	if at, ok := atoms[a]; ok {
		return unitInfo{copyDims(at.dim), at.factor}
	}
	for _, p := range []string{"da", "n", "u", "m", "c", "d", "h", "k", "M", "G"} {
		if strings.HasPrefix(a, p) {
			if at, ok := atoms[a[len(p):]]; ok && a[len(p):] != "1" && a[len(p):] != "%" {
				return unitInfo{copyDims(at.dim), at.factor * prefixes[p]}
			}
		}
	}
	return unitInfo{map[string]int{a: 1}, 1} // đơn vị lạ: chiều riêng
}

func copyDims(m map[string]int) map[string]int {
	c := make(map[string]int, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}

// parseUnit phân tích chuỗi đơn vị (m, cm, g/m, m2, kg.m/s2, {week}, [lb_av], 1).
func parseUnit(u string) unitInfo {
	u = strings.TrimSpace(u)
	if strings.HasPrefix(u, "{") && strings.HasSuffix(u, "}") {
		name := u[1 : len(u)-1]
		if c, ok := calendarUnits[name]; ok {
			return resolveAtom(calendarToUCUM[c])
		}
		return unitInfo{map[string]int{}, 1}
	}
	if c, ok := calendarUnits[u]; ok {
		return resolveAtom(calendarToUCUM[c])
	}
	res := unitInfo{map[string]int{}, 1}
	sign := 1
	start := 0
	flush := func(end int) {
		term := u[start:end]
		if term == "" {
			return
		}
		atom, exp := term, 1
		if m := termRe.FindStringSubmatch(term); m != nil && m[2] != "" && m[1] != "" && !strings.HasPrefix(term, "[") {
			atom = m[1]
			exp, _ = strconv.Atoi(m[2])
		}
		ai := resolveAtom(atom)
		for k, e := range ai.dims {
			res.dims[k] += e * exp * sign
		}
		res.factor *= math.Pow(ai.factor, float64(exp*sign))
	}
	inBracket := false
	for i := 0; i < len(u); i++ {
		switch c := u[i]; {
		case c == '[':
			inBracket = true
		case c == ']':
			inBracket = false
		case !inBracket && (c == '.' || c == '/'):
			flush(i)
			if c == '/' {
				sign = -1
			}
			start = i + 1
		}
	}
	flush(len(u))
	return res
}

// base trả về (khóa chiều, giá trị quy về đơn vị cơ sở).
func baseValue(q Quantity) (string, float64) {
	ui := parseUnit(q.Unit)
	return ui.key(), q.Value * ui.factor
}

// cmpQuantity so sánh hai Quantity; ok=false nếu khác chiều (không so sánh được).
func cmpQuantity(a, b Quantity) (c int, ok bool) {
	ka, va := baseValue(a)
	kb, vb := baseValue(b)
	if ka != kb {
		return 0, false
	}
	if math.Abs(va-vb) <= 1e-9*math.Max(math.Abs(va), math.Abs(vb)) {
		return 0, true
	}
	return cmpF(va, vb), true
}

func equivQuantity(a, b Quantity) bool {
	ka, va := baseValue(a)
	kb, vb := baseValue(b)
	if ka != kb {
		return false
	}
	if va == 0 {
		return vb == 0
	}
	bInA := a.Value * vb / va // giá trị của b quy theo đơn vị của a
	d := min(decimalsF(a.Value), decimalsF(b.Value))
	p := math.Pow(10, float64(d))
	return math.Round(bInA*p) == math.Round(a.Value*p)
}

func mulQuantity(a, b Quantity, divide bool) Quantity {
	v := a.Value * b.Value
	op := "."
	if divide {
		v, op = a.Value/b.Value, "/"
	}
	ua, ub := a.Unit, b.Unit
	var unit string
	switch {
	case divide && ua == ub:
		unit = "1"
	case !divide && ua == ub && !strings.ContainsAny(ua, "./0123456789[{"):
		unit = ua + "2"
	case ua == "1" || ua == "":
		unit = ub
		if divide {
			unit = "/" + ub
		}
	case ub == "1" || ub == "":
		unit = ua
	default:
		unit = ua + op + ub
	}
	return Quantity{Value: roundDec(v, 10), Unit: unit}
}

func roundDec(f float64, places int) float64 {
	p := math.Pow(10, float64(places))
	return math.Round(f*p) / p
}

func decimalsF(f float64) int {
	s := strconv.FormatFloat(f, 'f', -1, 64)
	if i := strings.IndexByte(s, '.'); i >= 0 {
		return len(s) - i - 1
	}
	return 0
}
