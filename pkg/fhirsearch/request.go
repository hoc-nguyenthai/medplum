// Package fhirsearch phân tích yêu cầu search FHIR (tham số, modifier, prefix, _count, _sort, _include...)
// và đối sánh một resource với yêu cầu đó. Không phụ thuộc kho lưu trữ nên dùng lại được cho
// repository bộ nhớ lẫn SQL.
//
// Hỗ trợ: string, token, reference, date, number, quantity, uri, chained search (subject:Patient.name=x) và _has
// (phân tích ở đây, thực thi bằng sub-search ở fhirrouter) và composite (composite.go). Chưa hỗ trợ: special,
// các modifier :in/:above/:below của token.
package fhirsearch

import (
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"trivita-fhir/pkg/fhir"
)

// Error là lỗi của yêu cầu search (tương ứng HTTP 400).
type Error struct{ Msg string }

func (e *Error) Error() string { return e.Msg }

func errf(f string, a ...any) *Error { return &Error{fmt.Sprintf(f, a...)} }

// Filter là một điều kiện search. Nhiều Filter cùng Request là AND; các Values trong một Filter là OR.
type Filter struct {
	Code         string
	Def          fhir.SearchParamDef
	Modifier     string   // exact, contains, text, not, missing, below, above, in, not-in, identifier, of-type
	TypeModifier string   // modifier là tên resource (subject:Patient=1)
	Values       []string // đã tách theo dấu phẩy chưa escape
}

// SortRule là một khóa sắp xếp.
type SortRule struct {
	Code       string
	Def        fhir.SearchParamDef
	Descending bool
}

// IncludeTarget mô tả _include hoặc _revinclude: Type:param[:targetType].
type IncludeTarget struct {
	ResourceType string
	Param        string
	Target       string
	Iterate      bool
}

// Request là yêu cầu search đã phân tích.
type Request struct {
	ResourceType string   // rỗng với search hệ thống
	Types        []string // _type của search hệ thống
	Filters      []Filter
	Sort         []SortRule
	Count        int
	Offset       int
	Total        string // none, estimate, accurate
	Summary      string
	Elements     []string
	Include      []IncludeTarget
	Revinclude   []IncludeTarget
	// Chained là các điều kiện chained search (subject:Patient.name=x) và _has. Chúng không tự đối sánh được: người
	// gọi phải biến chúng thành Filter trước khi dùng Match hoặc Repository (xem fhirrouter, nơi chạy sub-search).
	Chained []Chained
	// Exprs là các biểu thức _filter (AND với nhau và với Filters); ExprRaw là chuỗi gốc để dựng lại query.
	Exprs   []*Expr
	ExprRaw []string
	// Compartment là giá trị _compartment (Patient/<id>) nếu có. Biểu thức tương ứng nằm trong Exprs (OR của các tham số compartment) nhưng KHÔNG nằm
	// trong ExprRaw: nó được dựng lại từ trường này khi tạo query.
	Compartment string
}

// HasCriteria cho biết yêu cầu có điều kiện lọc nào (tham số thường, chained/_has hoặc _filter).
func (r *Request) HasCriteria() bool {
	return len(r.Filters) > 0 || len(r.Chained) > 0 || len(r.Exprs) > 0
}

// Chained là một điều kiện chained search hoặc _has.
type Chained struct {
	Key, Raw string // tham số gốc, để dựng lại query (link phân trang)
	// Reverse=false (chained thuận): Code là tham số reference của kiểu đang tìm, Types là các kiểu đích cần sub-search.
	// Reverse=true (_has): Types[0] là kiểu có tham chiếu ngược về, Def là tham số reference của kiểu đó.
	Reverse bool
	Code    string
	Def     fhir.SearchParamDef
	Types   []string
	Query   url.Values // truy vấn của sub-search, ví dụ {"name": ["smith"]}; có thể chứa chained lồng nhau
}

const (
	// DefaultCount là số kết quả mỗi trang mặc định; MaxCount là mức tối đa.
	DefaultCount = 20
	MaxCount     = 1000
)

var controlParams = map[string]bool{"_format": true, "_pretty": true, "_cursor": true}

// ParseQuery phân tích chuỗi query dạng "name=a&gender=male".
func ParseQuery(resourceType, query string) (*Request, error) {
	q, err := url.ParseQuery(query)
	if err != nil {
		return nil, errf("query không hợp lệ: %v", err)
	}
	return Parse(resourceType, q)
}

// Parse phân tích query của search cho một resource type (rỗng nếu search hệ thống dùng _type).
func Parse(resourceType string, query url.Values) (*Request, error) {
	req := &Request{ResourceType: resourceType, Count: DefaultCount, Total: "none"}
	if resourceType != "" && !fhir.IsResourceType(resourceType) {
		return nil, errf("resource type không hợp lệ: %s", resourceType)
	}
	keys := make([]string, 0, len(query))
	for k := range query {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	// _type phải được xử lý trước vì các tham số khác tra cứu theo kiểu đó
	slices.SortStableFunc(keys, func(a, b string) int {
		return map[bool]int{true: 0, false: 1}[a == "_type"] - map[bool]int{true: 0, false: 1}[b == "_type"]
	})
	for _, key := range keys {
		for _, raw := range query[key] {
			if err := req.apply(key, raw); err != nil {
				return nil, err
			}
		}
	}
	if resourceType == "" && len(req.Types) == 0 && len(req.Filters) > 0 {
		return nil, errf("search hệ thống có tham số cần _type")
	}
	return req, nil
}

func (r *Request) apply(key, raw string) error {
	switch key {
	case "_count":
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			return errf("_count không hợp lệ: %q", raw)
		}
		r.Count = min(n, MaxCount)
		return nil
	case "_offset":
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			return errf("_offset không hợp lệ: %q", raw)
		}
		r.Offset = n
		return nil
	case "_total":
		if raw != "none" && raw != "estimate" && raw != "accurate" {
			return errf("_total không hợp lệ: %q", raw)
		}
		r.Total = raw
		return nil
	case "_summary":
		r.Summary = raw
		return nil
	case "_elements":
		r.Elements = append(r.Elements, strings.Split(raw, ",")...)
		return nil
	case "_type":
		for _, t := range strings.Split(raw, ",") {
			if !fhir.IsResourceType(t) {
				return errf("_type không hợp lệ: %s", t)
			}
			r.Types = append(r.Types, t)
		}
		return nil
	case "_sort":
		return r.parseSort(raw)
	case "_include", "_include:iterate", "_revinclude", "_revinclude:iterate":
		t, err := parseInclude(raw, strings.HasSuffix(key, ":iterate"))
		if err != nil {
			return err
		}
		if strings.HasPrefix(key, "_include") {
			r.Include = append(r.Include, t)
		} else {
			r.Revinclude = append(r.Revinclude, t)
		}
		return nil
	case "_filter":
		e, err := ParseFilterExpr(r.ResourceType, raw)
		if err != nil {
			return err
		}
		r.Exprs = append(r.Exprs, e)
		r.ExprRaw = append(r.ExprRaw, raw)
		return nil
	case "_compartment":
		if r.ResourceType == "" {
			return errf("_compartment cần chỉ định loại resource")
		}
		if r.Compartment != "" {
			return errf("_compartment chỉ được đặt một lần")
		}
		e, err := compartmentExpr(r.ResourceType, raw)
		if err != nil {
			return err
		}
		r.Exprs = append(r.Exprs, e)
		r.Compartment = raw
		return nil
	case "_query", "_contained", "_containedType":
		return errf("tham số %s chưa được hỗ trợ", key)
	}
	if strings.HasPrefix(key, "_has:") {
		return r.parseHas(key, raw)
	}
	if controlParams[key] {
		return nil
	}
	return r.parseFilter(key, raw)
}

func parseInclude(raw string, iterate bool) (IncludeTarget, error) {
	parts := strings.Split(raw, ":")
	if len(parts) < 2 || len(parts) > 3 || (parts[0] != "*" && !fhir.IsResourceType(parts[0])) {
		return IncludeTarget{}, errf("giá trị _include không hợp lệ: %q (cần Type:param[:target])", raw)
	}
	t := IncludeTarget{ResourceType: parts[0], Param: parts[1], Iterate: iterate}
	if len(parts) == 3 {
		t.Target = parts[2]
	}
	return t, nil
}

func (r *Request) parseSort(raw string) error {
	for _, item := range strings.Split(raw, ",") {
		desc := strings.HasPrefix(item, "-")
		code := strings.TrimPrefix(item, "-")
		def, ok := Lookup(r.ResourceType, code)
		if !ok {
			return errf("tham số sắp xếp không hợp lệ: %s", code)
		}
		if def.Type == "composite" || def.Type == "special" {
			return errf("không sắp xếp được theo tham số %s kiểu %s", code, def.Type)
		}
		r.Sort = append(r.Sort, SortRule{Code: code, Def: def, Descending: desc})
	}
	return nil
}

func (r *Request) parseFilter(key, raw string) error {
	code, mod, _ := strings.Cut(key, ":")
	if strings.Contains(key, ".") && !strings.Contains(strings.SplitN(key, ".", 2)[0], "\\") {
		return r.parseChain(key, raw)
	}
	f := Filter{Code: code}
	if mod != "" {
		if fhir.IsResourceType(mod) {
			f.TypeModifier = mod
		} else {
			switch mod {
			case "exact", "contains", "text", "not", "missing", "below", "above", "in", "not-in", "identifier", "of-type":
				f.Modifier = mod
			default:
				return errf("modifier không hợp lệ: %s", mod)
			}
		}
	}
	types := r.Types
	if r.ResourceType != "" {
		types = []string{r.ResourceType}
	}
	if len(types) == 0 {
		return errf("search hệ thống có tham số %s cần _type", code)
	}
	var def fhir.SearchParamDef
	found := false
	for _, t := range types {
		if d, ok := Lookup(t, code); ok {
			def, found = d, true
			break
		}
	}
	if !found {
		return errf("tham số search không hợp lệ: %s", code)
	}
	f.Def = def
	if def.Type == "special" {
		return errf("tham số %s kiểu %s chưa được hỗ trợ", code, def.Type)
	}
	if f.Modifier == "identifier" || ((f.Modifier == "in" || f.Modifier == "not-in") && def.Type != "token") {
		return errf("modifier :%s chưa được hỗ trợ", f.Modifier)
	}
	f.Values = splitValues(raw)
	if f.Modifier == "missing" && (len(f.Values) != 1 || (f.Values[0] != "true" && f.Values[0] != "false")) {
		return errf(":missing cần true hoặc false")
	}
	if def.Type == "composite" {
		if _, err := CompositeConds(f); err != nil { // 400 ngay khi parse, không đợi tới lúc thực thi
			return err
		}
	}
	r.Filters = append(r.Filters, f)
	return nil
}

func (r *Request) baseTypes() []string {
	if r.ResourceType != "" {
		return []string{r.ResourceType}
	}
	return r.Types
}

// parseChain xử lý "param[:Type].rest=value": tìm trên các kiểu đích những resource khớp rest=value.
func (r *Request) parseChain(key, raw string) error {
	left, rest, _ := strings.Cut(key, ".")
	code, mod, _ := strings.Cut(left, ":")
	var def fhir.SearchParamDef
	found := false
	for _, t := range r.baseTypes() {
		if d, ok := Lookup(t, code); ok {
			def, found = d, true
			break
		}
	}
	if !found {
		if len(r.baseTypes()) == 0 {
			return errf("search hệ thống có tham số %s cần _type", code)
		}
		return errf("tham số search không hợp lệ: %s", code)
	}
	if def.Type != "reference" {
		return errf("chained search cần tham số reference, %s là %s", code, def.Type)
	}
	targets := def.Target
	if mod != "" {
		if !fhir.IsResourceType(mod) || (len(def.Target) > 0 && !slices.Contains(def.Target, mod) && !slices.Contains(def.Target, "Resource")) {
			return errf("kiểu đích %q không hợp lệ cho %s", mod, code)
		}
		targets = []string{mod}
	}
	if len(targets) == 0 {
		return errf("chained search qua %s cần chỉ rõ kiểu đích (%s:Type.param)", code, code)
	}
	query := url.Values{rest: {raw}}
	var ok []string
	var lastErr error
	for _, t := range targets {
		if !fhir.IsResourceType(t) {
			continue
		}
		if _, err := Parse(t, query); err != nil {
			lastErr = err
			continue
		}
		ok = append(ok, t)
	}
	if len(ok) == 0 {
		if lastErr == nil {
			lastErr = errf("không có kiểu đích hợp lệ cho %s", key)
		}
		return lastErr
	}
	r.Chained = append(r.Chained, Chained{Key: key, Raw: raw, Code: code, Def: def, Types: ok, Query: query})
	return nil
}

// parseHas xử lý "_has:Type:refParam:rest=value": resource có ít nhất một Type tham chiếu tới nó qua refParam và khớp rest=value.
func (r *Request) parseHas(key, raw string) error {
	parts := strings.SplitN(key, ":", 4)
	if len(parts) != 4 || parts[1] == "" || parts[2] == "" || parts[3] == "" || !fhir.IsResourceType(parts[1]) {
		return errf("_has không hợp lệ: %q (cần _has:Type:param:filter)", key)
	}
	base := r.baseTypes()
	if len(base) != 1 {
		return errf("_has chỉ dùng với đúng một kiểu resource")
	}
	def, ok := Lookup(parts[1], parts[2])
	if !ok || def.Type != "reference" {
		return errf("_has: %s không phải tham số reference của %s", parts[2], parts[1])
	}
	if len(def.Target) > 0 && !slices.Contains(def.Target, base[0]) && !slices.Contains(def.Target, "Resource") {
		return errf("_has: %s.%s không trỏ tới %s", parts[1], parts[2], base[0])
	}
	query := url.Values{parts[3]: {raw}}
	if _, err := Parse(parts[1], query); err != nil {
		return err
	}
	r.Chained = append(r.Chained, Chained{Key: key, Raw: raw, Reverse: true, Code: parts[2], Def: def, Types: []string{parts[1]}, Query: query})
	return nil
}

// splitValues tách theo dấu phẩy chưa escape (OR) và bỏ escape của \,; các escape \| \$ \\ giữ lại để
// bộ đối sánh xử lý theo kiểu tham số (xem Unescape).
func splitValues(raw string) []string {
	var out []string
	var sb strings.Builder
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		switch {
		case c == '\\' && i+1 < len(raw) && raw[i+1] == ',':
			sb.WriteByte(',')
			i++
		case c == '\\' && i+1 < len(raw) && strings.IndexByte("|$\\", raw[i+1]) >= 0:
			sb.WriteByte(c) // giữ nguyên escape \| \$ \\ cho bộ đối sánh theo kiểu tham số
			sb.WriteByte(raw[i+1])
			i++
		case c == ',':
			out = append(out, sb.String())
			sb.Reset()
		default:
			sb.WriteByte(c)
		}
	}
	return append(out, sb.String())
}

// Lookup tìm search parameter theo code cho một resource type.
// Ưu tiên khai báo riêng của resource, sau đó mới tới tham số chung (Resource, DomainResource).
func Lookup(resourceType, code string) (fhir.SearchParamDef, bool) {
	var general *fhir.SearchParamDef
	defs := fhir.SearchParams(resourceType)
	for i := range defs {
		d := defs[i]
		if d.Code != code {
			continue
		}
		if slices.Contains(d.Base, resourceType) {
			return d, true
		}
		if general == nil {
			general = &defs[i]
		}
	}
	if general != nil {
		return *general, true
	}
	return fhir.SearchParamDef{}, false
}

// Query dựng lại chuỗi query chuẩn của yêu cầu (dùng cho link phân trang); offset do người gọi đặt.
func (r *Request) Query(offset int) url.Values {
	q := url.Values{}
	for _, f := range r.Filters {
		key := f.Code
		switch {
		case f.Modifier != "":
			key += ":" + f.Modifier
		case f.TypeModifier != "":
			key += ":" + f.TypeModifier
		}
		q.Add(key, strings.Join(escapeAll(f.Values), ","))
	}
	for _, c := range r.Chained {
		q.Add(c.Key, c.Raw)
	}
	for _, raw := range r.ExprRaw {
		q.Add("_filter", raw)
	}
	if r.Compartment != "" {
		q.Set("_compartment", r.Compartment)
	}
	if len(r.Types) > 0 {
		q.Set("_type", strings.Join(r.Types, ","))
	}
	if len(r.Sort) > 0 {
		parts := make([]string, len(r.Sort))
		for i, s := range r.Sort {
			parts[i] = map[bool]string{true: "-", false: ""}[s.Descending] + s.Code
		}
		q.Set("_sort", strings.Join(parts, ","))
	}
	q.Set("_count", strconv.Itoa(r.Count))
	if offset > 0 {
		q.Set("_offset", strconv.Itoa(offset))
	}
	if r.Total != "none" {
		q.Set("_total", r.Total)
	}
	for _, i := range r.Include {
		q.Add("_include", includeString(i))
	}
	for _, i := range r.Revinclude {
		q.Add("_revinclude", includeString(i))
	}
	return q
}

func includeString(i IncludeTarget) string {
	s := i.ResourceType + ":" + i.Param
	if i.Target != "" {
		s += ":" + i.Target
	}
	return s
}

func escapeAll(vs []string) []string {
	out := make([]string, len(vs))
	r := strings.NewReplacer(",", `\,`)
	for i, v := range vs {
		out[i] = r.Replace(v)
	}
	return out
}

// Unescape bỏ escape \| \$ \\ khỏi một giá trị search (dùng sau khi đã tách theo | nếu cần).
func Unescape(v string) string {
	return strings.NewReplacer(`\|`, "|", `\$`, "$", `\\`, `\`).Replace(v)
}

// IsTerminology cho biết đây là điều kiện token dùng terminology (:in, :not-in, :above, :below). Chúng không tự đối
// sánh được: người gọi phải giải thành danh sách mã trước khi dùng Match hoặc Repository (xem fhirrouter).
func (f Filter) IsTerminology() bool {
	if f.Def.Type != "token" {
		return false
	}
	switch f.Modifier {
	case "in", "not-in", "above", "below":
		return true
	}
	return false
}

// NeedsResolution cho biết yêu cầu còn điều kiện phải giải trước khi chạy: chained/_has hoặc token dùng terminology.
func NeedsResolution(r *Request) bool {
	if len(r.Chained) > 0 {
		return true
	}
	for _, f := range r.Filters {
		if f.IsTerminology() {
			return true
		}
	}
	return false
}
