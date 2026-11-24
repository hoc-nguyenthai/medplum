package fhirrouter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirsearch"
)

// GraphQL (tập con của FHIR GraphQL, giống Medplum): GET|POST [base]/$graphql, body JSON {query, variables,
// operationName} hoặc Content-Type application/graphql, hoặc GET ?query=...
//
//	{ Patient(id: "123") { id name { family given } } }
//	{ PatientList(family: "smith", _count: 10) { id birthDate } }
//	{ PatientConnection(_count: 5) { count edges { resource { ... on Patient { id } } } } }
//	{ ObservationList(subject: "Patient/123") { code { text } subject { reference resource { ... on Patient { name { family } } } } } }
//	{ Patient(id: "123") { ObservationList(_reference: subject) { id } } }
//
// Gốc: `Type(id:)` đọc một resource; `TypeList(...)` tìm kiếm (tham số search là tham số của field, gạch dưới đổi thành
// gạch ngang, ví dụ general_practitioner; `_count` mặc định 20, tối đa 100; `_offset`, `_sort`); `TypeConnection(...)`
// trả {count, offset, pageSize, edges{mode, resource}}. Trong resource: các trường JSON theo tên (kể cả value[x] như
// valueQuantity), `resource` trên Reference để nạp resource được tham chiếu, `OtherList(_reference: param)` để tìm các
// resource tham chiếu ngược về resource hiện tại, `... on Type` và fragment có tên, alias, biến ($var, giá trị mặc định).
//
// Mọi đọc đi qua repository của router (tách project, quyền theo loại, AccessPolicy áp như REST) và kết quả luôn là
// HTTP 200 với {data, errors} (lỗi cú pháp là 400). Giới hạn chống lạm dụng: tối đa maxGraphQLResources resource nạp
// cho một truy vấn, độ sâu selection 12, truy vấn dài tối đa 64KB.
//
// Chưa hỗ trợ: mutation, subscription, introspection (__schema), directive (@include/@skip bị bỏ qua), tham số của field
// thường (fhirpath, format...), kiểu và validate theo schema (trường không có trả null thay vì lỗi).
const (
	graphQLListDefault = 20
	graphQLListMax     = 100
)

// maxGraphQLResources là số resource tối đa nạp cho một truy vấn (biến để kiểm thử).
var maxGraphQLResources = 1000

func (r *Router) registerGraphQL() {
	for _, m := range []string{"GET", "POST"} {
		r.Add(m, "$graphql", r.graphql)
	}
}

type gqlError struct {
	Message string `json:"message"`
	Path    []any  `json:"path,omitempty"`
}

type gqlExec struct {
	r         *Router
	ctx       context.Context
	repo      Repository
	doc       *gqlDoc
	vars      map[string]any
	remaining int
	errs      []gqlError
}

func (r *Router) graphql(ctx context.Context, req *Request) (*Response, error) {
	query, opName, vars, err := graphqlInput(req)
	if err != nil {
		return nil, err
	}
	doc, err := parseGraphQL(query)
	if err != nil {
		return nil, BadRequest(err.Error())
	}
	var op *gqlOp
	switch {
	case opName != "":
		for _, o := range doc.ops {
			if o.name == opName {
				op = o
			}
		}
		if op == nil {
			return nil, BadRequest("không có operation " + opName)
		}
	case len(doc.ops) == 1:
		op = doc.ops[0]
	default:
		return nil, BadRequest("nhiều operation: cần operationName")
	}
	e := &gqlExec{r: r, ctx: ctx, repo: r.repoOf(ctx), doc: doc, vars: map[string]any{}, remaining: maxGraphQLResources}
	for name, def := range op.vars { // biến chưa truyền dùng giá trị mặc định
		if v, ok := vars[name]; ok {
			e.vars[name] = v
		} else if def != nil {
			e.vars[name] = e.literal(def)
		}
	}
	for name, v := range vars {
		if _, declared := op.vars[name]; declared {
			continue
		}
		_ = v // biến không khai báo bị bỏ qua như GraphQL thông thường
	}
	data := map[string]any{}
	for _, s := range e.flatten(op.sel, nil, "") {
		key := s.alias
		if key == "" {
			key = s.name
		}
		v, ok := e.root(s, []any{key})
		if ok {
			data[key] = v
		} else {
			data[key] = nil
		}
	}
	out := map[string]any{"data": data}
	if len(e.errs) > 0 {
		out["errors"] = e.errs
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return nil, Internal(err.Error())
	}
	return &Response{Status: 200, Raw: raw, ContentType: "application/json; charset=utf-8"}, nil
}

// graphqlInput lấy query, operationName và variables từ GET (?query=&variables=&operationName=), body JSON hoặc application/graphql.
func graphqlInput(req *Request) (query, opName string, vars map[string]any, err error) {
	vars = map[string]any{}
	switch b := req.Body.(type) {
	case string:
		query = b
	case map[string]any:
		query, _ = b["query"].(string)
		opName, _ = b["operationName"].(string)
		switch v := b["variables"].(type) {
		case map[string]any:
			vars = v
		case string:
			if v != "" {
				if err := json.Unmarshal([]byte(v), &vars); err != nil {
					return "", "", nil, BadRequest("variables không phải JSON object")
				}
			}
		case nil:
		default:
			return "", "", nil, BadRequest("variables phải là JSON object")
		}
	case nil:
	default:
		return "", "", nil, BadRequest("body phải là JSON {query, variables} hoặc application/graphql")
	}
	if q := req.Query.Get("query"); q != "" && query == "" {
		query = q
	}
	if o := req.Query.Get("operationName"); o != "" && opName == "" {
		opName = o
	}
	if v := req.Query.Get("variables"); v != "" && len(vars) == 0 {
		if err := json.Unmarshal([]byte(v), &vars); err != nil {
			return "", "", nil, BadRequest("variables không phải JSON object")
		}
	}
	if strings.TrimSpace(query) == "" {
		return "", "", nil, BadRequest("thiếu query")
	}
	return query, opName, vars, nil
}

func (e *gqlExec) fail(path []any, format string, a ...any) {
	e.errs = append(e.errs, gqlError{Message: fmt.Sprintf(format, a...), Path: append([]any(nil), path...)})
}

// literal đổi giá trị trong truy vấn (đã thay biến) thành giá trị Go.
func (e *gqlExec) literal(v gqlValue) any {
	switch x := v.(type) {
	case gqlVar:
		return e.vars[string(x)]
	case gqlEnum:
		return string(x)
	case []gqlValue:
		out := make([]any, len(x))
		for i, it := range x {
			out[i] = e.literal(it)
		}
		return out
	case map[string]gqlValue:
		out := map[string]any{}
		for k, it := range x {
			out[k] = e.literal(it)
		}
		return out
	}
	return v
}

// argString đổi giá trị tham số thành chuỗi query (danh sách nối bằng dấu phẩy).
func argString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		return fmt.Sprint(x)
	case int64, float64, json.Number:
		return fmt.Sprint(x)
	case []any:
		parts := make([]string, len(x))
		for i, it := range x {
			parts[i] = argString(it)
		}
		return strings.Join(parts, ",")
	}
	return fmt.Sprint(v)
}

func (e *gqlExec) args(s *gqlSel) map[string]any {
	out := map[string]any{}
	for _, a := range s.args {
		out[a.name] = e.literal(a.val)
	}
	return out
}

// flatten trải inline fragment và fragment có tên (lọc theo điều kiện kiểu) thành danh sách field.
func (e *gqlExec) flatten(sel []*gqlSel, obj map[string]any, typ string) []*gqlSel {
	return e.flattenSeen(sel, typ, map[string]bool{})
}

func (e *gqlExec) flattenSeen(sel []*gqlSel, typ string, visiting map[string]bool) []*gqlSel {
	var out []*gqlSel
	for _, s := range sel {
		switch s.kind {
		case selField:
			out = append(out, s)
		case selInline:
			if typeMatches(s.typeCond, typ) {
				out = append(out, e.flattenSeen(s.sel, typ, visiting)...)
			}
		case selSpread:
			f := e.doc.frags[s.name]
			if f == nil {
				e.fail(nil, "fragment %s không tồn tại", s.name)
				continue
			}
			if visiting[s.name] {
				e.fail(nil, "fragment %s tham chiếu vòng", s.name)
				continue
			}
			if typeMatches(f.typeCond, typ) {
				visiting[s.name] = true
				out = append(out, e.flattenSeen(f.sel, typ, visiting)...)
				delete(visiting, s.name)
			}
		}
	}
	return out
}

// typeMatches: điều kiện kiểu khớp khi trống, bằng kiểu resource của đối tượng, hoặc không phải tên resource (kiểu phần tử).
func typeMatches(cond, typ string) bool {
	if cond == "" || cond == typ || typ == "" {
		return true
	}
	return !fhir.IsResourceType(cond) || cond == "Resource" || cond == "DomainResource"
}

var gqlListName = regexp.MustCompile(`^([A-Z][A-Za-z]*)(List|Connection)$`)

// root xử lý một field gốc: Type(id), TypeList, TypeConnection.
func (e *gqlExec) root(s *gqlSel, path []any) (any, bool) {
	if m := gqlListName.FindStringSubmatch(s.name); m != nil && fhir.IsResourceType(m[1]) {
		return e.search(m[1], m[2] == "Connection", s, nil, path)
	}
	if fhir.IsResourceType(s.name) {
		args := e.args(s)
		id := argString(args["id"])
		if id == "" || len(args) != 1 {
			e.fail(path, "%s cần đúng một tham số id", s.name)
			return nil, false
		}
		res, ok := e.load(s.name, id, path)
		if !ok {
			return nil, true
		}
		return e.object(res, s.sel, path), true
	}
	e.fail(path, "field gốc không hợp lệ: %s (dùng Type, TypeList hoặc TypeConnection)", s.name)
	return nil, false
}

// load nạp một resource qua repository (tính vào ngân sách). Không tìm thấy trả (nil, false) mà không báo lỗi.
func (e *gqlExec) load(rt, id string, path []any) (map[string]any, bool) {
	if e.remaining <= 0 {
		e.fail(path, "truy vấn vượt giới hạn %d resource", maxGraphQLResources)
		return nil, false
	}
	if e.jobsDenied(rt) {
		e.fail(path, "AsyncJob không truy vấn được bằng GraphQL")
		return nil, false
	}
	e.remaining--
	res, err := e.repo.ReadResource(e.ctx, rt, id)
	if err != nil {
		var he *Error
		if !errors.As(err, &he) || (he.Status != 404 && he.Status != 410) {
			e.fail(path, "%s", errMessage(err))
		}
		return nil, false
	}
	m, err := toMap(res)
	if err != nil {
		e.fail(path, "%s", err.Error())
		return nil, false
	}
	return m, true
}

func errMessage(err error) string {
	var he *Error
	if errors.As(err, &he) && he.Outcome != nil && len(he.Outcome.Issue) > 0 {
		i := he.Outcome.Issue[0]
		if i.Diagnostics != "" {
			return i.Diagnostics
		}
		if i.Details != nil && i.Details.Text != "" {
			return i.Details.Text
		}
	}
	return err.Error()
}

// search chạy TypeList/TypeConnection. reverse (khác nil) là resource hiện tại cho OtherList(_reference: param).
// jobsDenied: truy vấn loại AsyncJob chỉ dành cho người xem được mọi job (quản trị); người khác dùng route trạng thái có kiểm chủ sở hữu.
func (e *gqlExec) jobsDenied(rt string) bool {
	return rt == "AsyncJob" && !e.r.jobsAll(e.ctx)
}

func (e *gqlExec) search(rt string, conn bool, s *gqlSel, reverse map[string]any, path []any) (any, bool) {
	if e.jobsDenied(rt) {
		e.fail(path, "AsyncJob không truy vấn được bằng GraphQL")
		return nil, false
	}
	q := url.Values{}
	count, offset := graphQLListDefault, 0
	reverseSet := false
	for name, v := range e.args(s) {
		switch name {
		case "_count":
			n, ok := toInt(v)
			if !ok || n < 0 {
				e.fail(path, "_count phải là số nguyên không âm")
				return nil, false
			}
			count = min(n, graphQLListMax)
		case "_offset":
			n, ok := toInt(v)
			if !ok || n < 0 {
				e.fail(path, "_offset phải là số nguyên không âm")
				return nil, false
			}
			offset = n
		case "_reference":
			if reverse == nil {
				e.fail(path, "_reference chỉ dùng trong một resource")
				return nil, false
			}
			param := argString(v)
			if param == "" {
				e.fail(path, "_reference cần tên tham số search")
				return nil, false
			}
			q.Set(searchParamName(param), fmt.Sprint(reverse["resourceType"])+"/"+fmt.Sprint(reverse["id"]))
			reverseSet = true
		case "_sort":
			q.Set("_sort", argString(v))
		default:
			q.Set(searchParamName(name), argString(v))
		}
	}
	if reverse != nil && !reverseSet {
		e.fail(path, "%s trong resource cần _reference", s.name)
		return nil, false
	}
	sr, err := fhirsearch.Parse(rt, q)
	if err != nil {
		e.fail(path, "%s", err.Error())
		return nil, false
	}
	sr.Count, sr.Offset = count, offset
	sr.Total = "none"
	if conn {
		sr.Total = "accurate"
	}
	if e.remaining <= 0 {
		e.fail(path, "truy vấn vượt giới hạn %d resource", maxGraphQLResources)
		return nil, false
	}
	res, err := e.r.searchRepo(e.ctx, e.repo, sr)
	if err != nil {
		e.fail(path, "%s", errMessage(err))
		return nil, false
	}
	if len(res.Matches) > e.remaining {
		res.Matches = res.Matches[:e.remaining]
		e.fail(path, "truy vấn vượt giới hạn %d resource, kết quả bị cắt", maxGraphQLResources)
	}
	e.remaining -= len(res.Matches)
	var docs []map[string]any
	for _, m := range res.Matches {
		d, err := toMap(m)
		if err != nil {
			e.fail(path, "%s", err.Error())
			return nil, false
		}
		docs = append(docs, d)
	}
	if !conn {
		list := make([]any, len(docs))
		for i, d := range docs {
			list[i] = e.object(d, s.sel, append(path, i))
		}
		return list, true
	}
	total := len(docs)
	if res.Total != nil {
		total = *res.Total
	}
	edges := make([]any, len(docs))
	for i, d := range docs {
		edges[i] = map[string]any{"mode": "match", "resource": d}
	}
	conn2 := map[string]any{"count": total, "offset": offset, "pageSize": count, "edges": edges}
	return e.object(conn2, s.sel, path), true
}

func toInt(v any) (int, bool) {
	switch x := v.(type) {
	case int64:
		return int(x), true
	case float64:
		return int(x), float64(int(x)) == x
	case json.Number:
		n, err := x.Int64()
		return int(n), err == nil
	}
	return 0, false
}

// searchParamName đổi tên tham số GraphQL (gạch dưới) thành tên tham số search (gạch ngang), giữ gạch dưới đầu.
func searchParamName(n string) string {
	if strings.HasPrefix(n, "_") {
		return "_" + strings.ReplaceAll(n[1:], "_", "-")
	}
	return strings.ReplaceAll(n, "_", "-")
}

// object thực thi selection trên một đối tượng JSON.
func (e *gqlExec) object(obj map[string]any, sel []*gqlSel, path []any) map[string]any {
	typ, _ := obj["resourceType"].(string)
	out := map[string]any{}
	for _, s := range e.flatten(sel, obj, typ) {
		key := s.alias
		if key == "" {
			key = s.name
		}
		p := append(append([]any(nil), path...), key)
		out[key] = e.field(obj, typ, s, p)
	}
	return out
}

func (e *gqlExec) field(obj map[string]any, typ string, s *gqlSel, path []any) any {
	switch {
	case s.name == "__typename":
		if typ != "" {
			return typ
		}
		return nil
	case s.name == "resource" && typ == "" && obj["reference"] != nil:
		return e.reference(obj, s, path)
	}
	if m := gqlListName.FindStringSubmatch(s.name); m != nil && typ != "" && obj["id"] != nil && fhir.IsResourceType(m[1]) {
		if _, has := obj[s.name]; !has {
			v, ok := e.search(m[1], m[2] == "Connection", s, obj, path)
			if !ok {
				return nil
			}
			return v
		}
	}
	if len(s.args) > 0 {
		e.fail(path, "tham số của field %s chưa được hỗ trợ", s.name)
		return nil
	}
	return e.value(obj[s.name], s.sel, path)
}

// reference nạp resource được trỏ bởi một Reference ("Type/id", cho phép URL tuyệt đối kết thúc bằng Type/id).
func (e *gqlExec) reference(ref map[string]any, s *gqlSel, path []any) any {
	norm := fhirsearch.NormalizeReference(fmt.Sprint(ref["reference"]))
	rt, id, ok := strings.Cut(norm, "/")
	if !ok || !fhir.IsResourceType(rt) || id == "" || strings.Contains(id, "/") {
		return nil // tham chiếu nội bộ (#id), urn: hoặc không phân giải được
	}
	res, ok := e.load(rt, id, path)
	if !ok {
		return nil
	}
	return e.object(res, s.sel, path)
}

// value áp selection lên một giá trị JSON (đối tượng, mảng hoặc giá trị đơn).
func (e *gqlExec) value(v any, sel []*gqlSel, path []any) any {
	switch x := v.(type) {
	case nil:
		return nil
	case []any:
		out := make([]any, len(x))
		for i, it := range x {
			out[i] = e.value(it, sel, append(append([]any(nil), path...), i))
		}
		return out
	case map[string]any:
		if len(sel) == 0 {
			e.fail(path, "field kiểu đối tượng cần selection")
			return nil
		}
		return e.object(x, sel, path)
	}
	return v // giá trị đơn: bỏ qua selection nếu có
}
