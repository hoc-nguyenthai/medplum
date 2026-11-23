package fhirsearch

import (
	"strconv"
	"strings"
	"sync"

	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirpath"
)

// Tham số composite (Observation?code-value-quantity=http://loinc.org|8480-6$gt100).
//
// Một tham số composite có biểu thức cha (Expression, ví dụ "Observation" hoặc "Observation.component") cho ra các
// phần tử, và mỗi thành phần có biểu thức tương đối so với phần tử đó. Resource khớp khi có MỘT phần tử mà mọi thành
// phần đều khớp giá trị tương ứng (các giá trị cách nhau bằng "$"); nhiều giá trị cách nhau bằng dấu phẩy là OR.
//
// Chỉ mục SQL: với mỗi phần tử e và thành phần i, các giá trị của thành phần được ghi vào bảng chỉ mục của kiểu thành
// phần với code "<code>#<i>" và ord = e. Điều kiện composite nối các thành phần theo cùng (resource, ord).
// Không hỗ trợ modifier, :missing, và sắp xếp theo tham số composite.

type compositePart struct {
	def  fhir.SearchParamDef // định nghĩa của thành phần (kiểu, tên)
	expr string              // biểu thức tương đối so với phần tử
}

var compositeCache sync.Map // URL của tham số composite -> []compositePart

func compositeParts(def fhir.SearchParamDef) ([]compositePart, error) {
	if v, ok := compositeCache.Load(def.URL); ok {
		return v.([]compositePart), nil
	}
	if len(def.Component) == 0 || len(def.Base) == 0 {
		return nil, errf("tham số composite %s không có thành phần", def.Code)
	}
	var parts []compositePart
	for _, c := range def.Component {
		var found *fhir.SearchParamDef
		for _, base := range def.Base {
			defs := fhir.SearchParams(base)
			for i := range defs {
				if defs[i].URL == c.Definition {
					found = &defs[i]
					break
				}
			}
			if found != nil {
				break
			}
		}
		if found == nil {
			return nil, errf("tham số composite %s: không tìm thấy thành phần %s", def.Code, c.Definition)
		}
		switch found.Type {
		case "string", "token", "reference", "date", "number", "quantity", "uri":
		default:
			return nil, errf("tham số composite %s: thành phần kiểu %s chưa được hỗ trợ", def.Code, found.Type)
		}
		parts = append(parts, compositePart{def: *found, expr: c.Expression})
	}
	compositeCache.Store(def.URL, parts)
	return parts, nil
}

// splitComposite tách một giá trị composite theo "$" chưa escape; phải đúng n thành phần.
func splitComposite(q string, n int) ([]string, error) {
	var parts []string
	var cur strings.Builder
	for i := 0; i < len(q); i++ {
		switch {
		case q[i] == '\\' && i+1 < len(q):
			cur.WriteByte(q[i])
			i++
			cur.WriteByte(q[i])
		case q[i] == '$':
			parts = append(parts, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(q[i])
		}
	}
	parts = append(parts, cur.String())
	if len(parts) != n {
		return nil, errf("giá trị composite %q cần đúng %d thành phần cách nhau bằng $", q, n)
	}
	return parts, nil
}

// CompositePart là một thành phần của một giá trị composite, đã biên dịch.
type CompositePart struct {
	Def  fhir.SearchParamDef
	Cond Cond
	// Code là code của chỉ mục SQL của thành phần (xem CompositeCode).
	Code string
}

// CompositeCode là code dùng trong bảng chỉ mục cho thành phần thứ i của tham số composite.
func CompositeCode(code string, i int) string { return code + "#" + strconv.Itoa(i) }

// CompositeConds biên dịch từng giá trị OR của một Filter composite thành danh sách thành phần (theo thứ tự).
// Dùng để kiểm tra khi parse và để dịch sang SQL.
func CompositeConds(f Filter) ([][]CompositePart, error) {
	if f.Modifier != "" {
		return nil, errf("tham số composite %s không hỗ trợ modifier :%s", f.Code, f.Modifier)
	}
	parts, err := compositeParts(f.Def)
	if err != nil {
		return nil, err
	}
	out := make([][]CompositePart, 0, len(f.Values))
	for _, q := range f.Values {
		qs, err := splitComposite(q, len(parts))
		if err != nil {
			return nil, err
		}
		row := make([]CompositePart, len(parts))
		for i, p := range parts {
			c, err := cond(Filter{Code: p.def.Code, Def: p.def}, qs[i])
			if err != nil {
				return nil, err
			}
			row[i] = CompositePart{Def: p.def, Cond: c, Code: CompositeCode(f.Code, i)}
		}
		out = append(out, row)
	}
	return out, nil
}

// compositeElements trả, với mỗi phần tử của biểu thức cha, các giá trị của từng thành phần.
func compositeElements(doc map[string]any, def fhir.SearchParamDef, parts []compositePart) ([][][]fhirpath.Value, error) {
	els, err := Values(doc, def)
	if err != nil {
		return nil, err
	}
	out := make([][][]fhirpath.Value, 0, len(els))
	for _, el := range els {
		row := make([][]fhirpath.Value, len(parts))
		for i, p := range parts {
			e, err := compile(p.expr)
			if err != nil {
				return nil, err
			}
			if row[i], err = e.EvalAt(el.V, el.T, doc, nil, nil); err != nil {
				return nil, err
			}
		}
		out = append(out, row)
	}
	return out, nil
}

func matchComposite(doc map[string]any, f Filter) (bool, error) {
	if _, err := CompositeConds(f); err != nil { // kiểm cú pháp
		return false, err
	}
	parts, _ := compositeParts(f.Def)
	els, err := compositeElements(doc, f.Def, parts)
	if err != nil {
		return false, err
	}
	for _, q := range f.Values {
		qs, _ := splitComposite(q, len(parts))
		for _, el := range els {
			all := true
			for i, p := range parts {
				if len(el[i]) == 0 {
					all = false
					break
				}
				ok, err := matchOne(Filter{Code: p.def.Code, Def: p.def}, el[i], qs[i])
				if err != nil {
					return false, err
				}
				if !ok {
					all = false
					break
				}
			}
			if all {
				return true, nil
			}
		}
	}
	return false, nil
}

// CompositeEntry là chỉ mục của một thành phần trên một phần tử (ghi vào bảng chỉ mục với Code và Ord này).
type CompositeEntry struct {
	Code string
	Ord  int
	Def  fhir.SearchParamDef // định nghĩa thành phần (quyết định bảng chỉ mục)
	Ix   *Index
}

// ExtractComposite trích chỉ mục của tham số composite khi ghi resource.
func ExtractComposite(doc map[string]any, def fhir.SearchParamDef) ([]CompositeEntry, error) {
	parts, err := compositeParts(def)
	if err != nil {
		return nil, err
	}
	els, err := compositeElements(doc, def, parts)
	if err != nil {
		return nil, err
	}
	var out []CompositeEntry
	for e, row := range els {
		for i, p := range parts {
			ix, err := extractValues(row[i], p.def)
			if err != nil {
				return nil, err
			}
			if !ix.Empty() {
				out = append(out, CompositeEntry{Code: CompositeCode(def.Code, i), Ord: e, Def: p.def, Ix: ix})
			}
		}
	}
	return out, nil
}
