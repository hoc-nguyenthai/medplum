package fhirauth

import (
	"regexp"
	"sort"
	"strings"

	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirrouter"
	"trivita-fhir/pkg/fhirsearch"
)

// Tìm kiếm không được dùng làm đường suy ra giá trị của trường ẩn. Gỡ trường khỏi kết quả trả về là chưa đủ: `Patient?birthdate=1990-01-01`,
// `_sort=birthdate`, `_filter`, `_content` hay `_include` theo tham chiếu bị ẩn vẫn cho biết giá trị (hoặc quan hệ) qua việc có hay không có kết quả và
// thứ tự của chúng. Nên yêu cầu nào đụng tới trường ẩn của loại liên quan bị từ chối 403, kiểm theo biểu thức FHIRPath của tham số tìm kiếm so với
// đường dẫn trường ẩn (quan hệ tiền tố theo từng đoạn: ẩn `name.family` thì cấm cả `family` lẫn `name`, vì `name` khớp cả họ).

const hiddenSearchMsg = "Chính sách truy cập không cho phép tìm kiếm, sắp xếp hoặc include theo trường bị ẩn: "

// checkSearch từ chối yêu cầu tìm kiếm chạm trường ẩn. req là yêu cầu của người gọi (chưa thêm điều kiện của chính sách).
func (p *Policy) checkSearch(req *fhirsearch.Request) error {
	if p == nil {
		return nil
	}
	for _, h := range p.hiddenSetsFor(req) {
		visit := func(code string, def fhir.SearchParamDef) error {
			if paramTouches(h, def) {
				return fhirrouter.Forbidden(hiddenSearchMsg + code)
			}
			return nil
		}
		for _, f := range req.Filters {
			if err := visit(f.Code, f.Def); err != nil {
				return err
			}
		}
		for _, s := range req.Sort {
			if err := visit(s.Code, s.Def); err != nil {
				return err
			}
		}
		for _, e := range req.Exprs {
			if err := visitExpr(e, visit); err != nil {
				return err
			}
		}
		for _, c := range req.Chained {
			if !c.Reverse { // tham số tham chiếu của chính loại đang tìm; _has thuộc loại khác và được kiểm ở sub-search
				if err := visit(c.Code, c.Def); err != nil {
					return err
				}
			}
		}
	}
	// _include / _revinclude: tham số tham chiếu thuộc loại nguồn (it.ResourceType), nên so với trường ẩn của chính loại đó
	for _, list := range [][]fhirsearch.IncludeTarget{req.Include, req.Revinclude} {
		for _, it := range list {
			h := p.hiddenOf(it.ResourceType)
			if len(h) == 0 {
				continue
			}
			if it.Param == "*" {
				return fhirrouter.Forbidden(hiddenSearchMsg + it.ResourceType + ":*")
			}
			for _, def := range fhir.SearchParams(it.ResourceType) {
				if def.Code == it.Param && paramTouches(h, def) {
					return fhirrouter.Forbidden(hiddenSearchMsg + it.ResourceType + ":" + it.Param)
				}
			}
		}
	}
	return nil
}

func visitExpr(e *fhirsearch.Expr, visit func(string, fhir.SearchParamDef) error) error {
	if e == nil {
		return nil
	}
	if e.Leaf != nil {
		if err := visit(e.Leaf.Code, e.Leaf.Def); err != nil {
			return err
		}
	}
	for _, k := range e.Kids {
		if err := visitExpr(k, visit); err != nil {
			return err
		}
	}
	return nil
}

// hiddenOf là danh sách trường ẩn của loại rt (rỗng nếu không có).
func (p *Policy) hiddenOf(rt string) []string {
	if r := p.fieldRule(rt); r != nil {
		return r.hidden
	}
	return nil
}

// hiddenSetsFor là các tập trường ẩn mà yêu cầu phải tránh: của loại đang tìm, của các loại trong _type, hoặc (tìm kiếm hệ thống không giới hạn loại) của
// mọi rule có trường ẩn kể cả rule "*".
func (p *Policy) hiddenSetsFor(req *fhirsearch.Request) [][]string {
	var out [][]string
	add := func(h []string) {
		if len(h) > 0 {
			out = append(out, h)
		}
	}
	switch {
	case req.ResourceType != "":
		add(p.hiddenOf(req.ResourceType))
	case len(req.Types) > 0:
		for _, t := range req.Types {
			add(p.hiddenOf(t))
		}
	default:
		types := make([]string, 0, len(p.rules))
		for t := range p.rules {
			types = append(types, t)
		}
		sort.Strings(types)
		for _, t := range types {
			add(p.hiddenOf(t))
		}
		if p.wild != nil {
			add(p.wild.hidden)
		}
	}
	return out
}

// paramTouches cho biết tham số tìm kiếm def đụng tới một trong các trường ẩn hidden.
func paramTouches(hidden []string, def fhir.SearchParamDef) bool {
	switch def.Code {
	case "_content", "_text":
		return true // quét toàn bộ nội dung hoặc phần tường thuật của resource
	}
	var bases [][]pathSeg
	if len(def.Component) > 0 { // composite: biểu thức thành phần tương đối so với biểu thức gốc
		bases = exprPaths(def.Expression)
		if len(bases) == 0 {
			bases = [][]pathSeg{nil}
		}
		for _, c := range def.Component {
			for _, rel := range exprPaths(c.Expression) {
				for _, b := range bases {
					if touchesAny(hidden, append(append([]pathSeg(nil), b...), rel...)) {
						return true
					}
				}
			}
		}
		return false
	}
	for _, path := range exprPaths(def.Expression) {
		if touchesAny(hidden, path) {
			return true
		}
	}
	return false
}

func touchesAny(hidden []string, path []pathSeg) bool {
	for _, h := range hidden {
		if related(strings.Split(h, "."), path) {
			return true
		}
	}
	return false
}

// pathSeg là một đoạn của đường dẫn trong biểu thức: tên phần tử và kiểu chọn (từ ofType hoặc as), nếu có.
type pathSeg struct{ name, typ string }

// related: một đường dẫn là tiền tố của đường dẫn kia, so từng đoạn. Đường dẫn rỗng (cả resource) liên quan tới mọi thứ.
func related(hidden []string, path []pathSeg) bool {
	for i := 0; i < len(hidden) && i < len(path); i++ {
		if !segMatch(hidden[i], path[i]) {
			return false
		}
	}
	return true
}

func segMatch(h string, e pathSeg) bool {
	hb := strings.TrimSuffix(h, "[x]")
	if hb == e.name {
		return true
	}
	if !strings.HasPrefix(hb, e.name) || len(hb) <= len(e.name) {
		return false
	}
	suffix := hb[len(e.name):]
	if e.typ != "" {
		return strings.EqualFold(suffix, e.typ)
	}
	return choiceTypes[suffix] // ẩn `valueQuantity` trong khi biểu thức đọc `value` (mọi kiểu)
}

var (
	asRE    = regexp.MustCompile(`\s+as\s+(\w+)`)
	chainRE = regexp.MustCompile(`[A-Za-z_?][\w?]*(?:[.~][A-Za-z_?][\w?]*)*`)
)

// exprPaths trích các đường dẫn phần tử từ biểu thức FHIRPath của một tham số tìm kiếm ("Patient.name.family | Patient.name.given",
// "Observation.value.ofType(Quantity)", "Patient.telecom.where(system='phone')"). Hàm gọi (where, exists, resolve...) cắt đường dẫn tại đó: đường dẫn ngắn hơn
// là phép xấp xỉ thận trọng (liên quan tới nhiều trường ẩn hơn). Tiền tố loại resource bị bỏ.
func exprPaths(expr string) [][]pathSeg {
	s := asRE.ReplaceAllString(expr, "~$1")
	s = stripCalls(s)
	s = strings.NewReplacer("(", " ", ")", " ").Replace(s)
	var out [][]pathSeg
	for _, chain := range chainRE.FindAllString(s, -1) {
		var path []pathSeg
		for i, part := range strings.Split(chain, ".") {
			if part == "?" {
				break // hàm gọi: dừng ở đây
			}
			name, typ, _ := strings.Cut(part, "~")
			if name == "" || strings.HasPrefix(name, "?") {
				break
			}
			if i == 0 && (name == "Resource" || name == "DomainResource" || fhir.IsResourceType(name)) {
				continue
			}
			path = append(path, pathSeg{name: name, typ: typ})
		}
		out = append(out, path)
	}
	return out
}

// stripCalls thay mọi lời gọi hàm `tên(đối số)` bằng "?" (ofType(X) và as(X) thành "~X" gắn vào đoạn trước); ngoặc nhóm (không đứng ngay sau tên) giữ nguyên.
func stripCalls(s string) string {
	for {
		open := -1
		for i := 1; i < len(s); i++ {
			if s[i] == '(' && isWord(s[i-1]) {
				open = i
				break
			}
		}
		if open < 0 {
			return s
		}
		depth, closeIdx := 0, -1
		for j := open; j < len(s); j++ {
			if s[j] == '(' {
				depth++
			} else if s[j] == ')' {
				if depth--; depth == 0 {
					closeIdx = j
					break
				}
			}
		}
		if closeIdx < 0 {
			return s[:open] // ngoặc không cân: bỏ phần còn lại (thận trọng: đường dẫn ngắn hơn)
		}
		start := open
		for start > 0 && isWord(s[start-1]) {
			start--
		}
		name := s[start:open]
		repl := "?"
		if name == "ofType" || name == "as" {
			repl = "~" + strings.TrimSpace(s[open+1:closeIdx])
			if start > 0 && s[start-1] == '.' {
				start--
			}
		}
		s = s[:start] + repl + s[closeIdx+1:]
	}
}

func isWord(b byte) bool {
	return b == '_' || (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}
