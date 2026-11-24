package apicontract

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Tài liệu tham chiếu chi tiết từng route, SINH hoàn toàn từ swagger.json: header, tham số, bảng trường (kiểu, bắt buộc, ràng buộc, mô tả, ví dụ) của body yêu cầu
// và của từng phản hồi, kèm JSON mẫu dựng từ `example`. Mỗi tag một tệp. Các trường hợp (điều kiện nào ra phản hồi nào) viết tay ở thư mục cases cùng tên tag; mỗi route
// phải có mục tương ứng ở đó (CheckCases).

// ReferenceOptions chỉnh cách sinh theo từng service.
type ReferenceOptions struct {
	// CasesDir là đường dẫn tương đối từ thư mục tham chiếu tới thư mục cases (mặc định "../cases").
	CasesDir string
	// ExpandPrefixes liệt kê tiền tố tên định nghĩa được khai triển nhiều tầng; định nghĩa khác (kiểu thư viện như fhir.*) chỉ khai triển một tầng. Rỗng: khai triển hết.
	ExpandPrefixes []string
	// CommonHeaders là các header phản hồi chung ghi ở đầu mỗi tệp.
	CommonHeaders string
	// CommonSchemas ánh xạ tên định nghĩa dùng chung (envelope lỗi, OperationOutcome) sang liên kết tới nơi mô tả chúng một lần; các phản hồi dùng schema này chỉ ghi liên kết.
	CommonSchemas map[string]string
}

type rawSpec struct {
	Info struct {
		Title string `json:"title"`
	} `json:"info"`
	Tags []struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	} `json:"tags"`
	Paths       map[string]map[string]rawOp `json:"paths"`
	Definitions map[string]map[string]any   `json:"definitions"`
}

type rawOp struct {
	Summary     string                `json:"summary"`
	Description string                `json:"description"`
	Tags        []string              `json:"tags"`
	Security    []map[string][]string `json:"security"`
	Consumes    []string              `json:"consumes"`
	Produces    []string              `json:"produces"`
	Parameters  []map[string]any      `json:"parameters"`
	Responses   map[string]struct {
		Description string         `json:"description"`
		Schema      map[string]any `json:"schema"`
	} `json:"responses"`
}

// Slug là mã neo ổn định của một route, dùng chung giữa tệp sinh và tệp cases: "post-auth-login", "get-admin-projects-projectid".
func Slug(method, path string) string {
	re := regexp.MustCompile(`[^a-z0-9]+`)
	return strings.Trim(re.ReplaceAllString(strings.ToLower(method+"-"+path), "-"), "-")
}

type refWriter struct {
	defs   map[string]map[string]any
	opt    ReferenceOptions
	b      strings.Builder
	secDef map[string]bool
}

// RenderReference trả map tên tệp (vd "auth.md") -> nội dung Markdown.
func RenderReference(swagger []byte, opt ReferenceOptions) (map[string]string, error) {
	var sp rawSpec
	if err := json.Unmarshal(swagger, &sp); err != nil {
		return nil, fmt.Errorf("apicontract: swagger không đọc được: %w", err)
	}
	if opt.CasesDir == "" {
		opt.CasesDir = "../cases"
	}
	desc := map[string]string{}
	for _, t := range sp.Tags {
		desc[t.Name] = t.Description
	}
	type row struct{ path, method string }
	byTag := map[string][]row{}
	for p, ms := range sp.Paths {
		for m, op := range ms {
			tag := "khac"
			if len(op.Tags) > 0 {
				tag = op.Tags[0]
			}
			byTag[tag] = append(byTag[tag], row{p, m})
		}
	}
	out := map[string]string{}
	var tags []string
	for t := range byTag {
		tags = append(tags, t)
	}
	sort.Strings(tags)
	for _, tag := range tags {
		rows := byTag[tag]
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].path != rows[j].path {
				return rows[i].path < rows[j].path
			}
			return methodOrder[rows[i].method] < methodOrder[rows[j].method]
		})
		w := &refWriter{defs: sp.Definitions, opt: opt}
		fmt.Fprintf(&w.b, "# Tham chiếu API: nhóm `%s`\n\n> **Tệp này được SINH từ `swagger.json`** (%s); đừng sửa tay. Cập nhật: `UPDATE_DOCS=1 go test` ở thư mục `docs`. Điều kiện nào ra phản hồi nào (từng trường hợp, ví dụ JSON, header) ở [%s/%s.md](%s/%s.md).\n\n", tag, sp.Info.Title, opt.CasesDir, tag, opt.CasesDir, tag)
		if d := desc[tag]; d != "" {
			fmt.Fprintf(&w.b, "%s\n\n", d)
		}
		if opt.CommonHeaders != "" {
			w.b.WriteString(opt.CommonHeaders)
			w.b.WriteString("\n\n")
		}
		w.b.WriteString("| Route | Tóm tắt |\n|---|---|\n")
		for _, r := range rows {
			op := sp.Paths[r.path][r.method]
			fmt.Fprintf(&w.b, "| [`%s %s`](#%s) | %s |\n", strings.ToUpper(r.method), esc(r.path), Slug(r.method, r.path), esc(op.Summary))
		}
		w.b.WriteString("\n")
		for _, r := range rows {
			w.op(r.method, r.path, sp.Paths[r.path][r.method])
		}
		out[tag+".md"] = strings.TrimRight(w.b.String(), "\n") + "\n"
	}
	return out, nil
}

func (w *refWriter) op(method, path string, op rawOp) {
	slug := Slug(method, path)
	fmt.Fprintf(&w.b, "---\n\n<a id=\"%s\"></a>\n## `%s %s`\n\n**%s**\n\n", slug, strings.ToUpper(method), path, op.Summary)
	if op.Description != "" {
		w.b.WriteString(op.Description)
		w.b.WriteString("\n\n")
	}
	tag := "khac"
	if len(op.Tags) > 0 {
		tag = op.Tags[0]
	}
	fmt.Fprintf(&w.b, "Các trường hợp phản hồi: [%s/%s.md#%s](%s/%s.md#%s)\n\n", w.opt.CasesDir, tag, slug, w.opt.CasesDir, tag, slug)

	// xác thực
	authText := "công khai (không cần header xác thực)"
	if len(op.Security) > 0 {
		var names []string
		for _, s := range op.Security {
			for n := range s {
				names = append(names, n)
			}
		}
		sort.Strings(names)
		authText = "bắt buộc: header `Authorization: Bearer <access_token>` (" + strings.Join(names, ", ") + ")"
	}
	fmt.Fprintf(&w.b, "| Thuộc tính | Giá trị |\n|---|---|\n| Xác thực | %s |\n| Content-Type yêu cầu | %s |\n| Content-Type phản hồi | %s |\n\n", authText, joinCode(op.Consumes, "không có body"), joinCode(op.Produces, "không có body"))

	// header yêu cầu
	var headers, others []map[string]any
	var body map[string]any
	for _, p := range op.Parameters {
		switch p["in"] {
		case "header":
			headers = append(headers, p)
		case "body":
			body = p
		default:
			others = append(others, p)
		}
	}
	w.b.WriteString("**Header yêu cầu**\n\n| Header | Bắt buộc | Mô tả |\n|---|---|---|\n")
	if len(op.Security) > 0 {
		w.b.WriteString("| `Authorization` | có | `Bearer <access_token>` |\n")
	}
	if len(op.Consumes) > 0 {
		fmt.Fprintf(&w.b, "| `Content-Type` | có | %s |\n", joinCode(op.Consumes, ""))
	}
	for _, h := range headers {
		fmt.Fprintf(&w.b, "| `%s` | %s | %s |\n", h["name"], yesNo(h["required"]), esc(str(h["description"])))
	}
	w.b.WriteString("| `Accept` | không | nên khớp Content-Type phản hồi |\n\n")

	// path, query, form
	if len(others) > 0 {
		w.b.WriteString("**Tham số**\n\n| Tên | Vị trí | Kiểu | Bắt buộc | Mô tả |\n|---|---|---|---|---|\n")
		for _, p := range others {
			fmt.Fprintf(&w.b, "| `%s` | %s | %s | %s | %s |\n", p["name"], posVN(str(p["in"])), typeOf(p), yesNo(p["required"]), esc(str(p["description"])))
		}
		w.b.WriteString("\n")
	}

	// body
	if body != nil {
		sch, _ := body["schema"].(map[string]any)
		fmt.Fprintf(&w.b, "**Body yêu cầu** (%s)\n\n", esc(str(body["description"])))
		w.schemaDoc(sch)
	} else if len(op.Consumes) == 0 && len(others) == 0 {
		w.b.WriteString("**Body yêu cầu:** không có.\n\n")
	}

	// phản hồi
	var codes []string
	for c := range op.Responses {
		codes = append(codes, c)
	}
	sort.Strings(codes)
	w.b.WriteString("**Phản hồi**\n\n")
	for _, c := range codes {
		r := op.Responses[c]
		fmt.Fprintf(&w.b, "**`%s`** %s\n\n", c, esc(r.Description))
		if r.Schema != nil {
			w.schemaDoc(r.Schema)
		} else {
			w.b.WriteString("Không có body.\n\n")
		}
	}
}

func (w *refWriter) schemaDoc(sch map[string]any) {
	name, sch := w.resolve(sch)
	if link, ok := w.opt.CommonSchemas[name]; ok {
		fmt.Fprintf(&w.b, "Schema: `%s` (cấu trúc dùng chung, xem %s).\n\n", name, link)
		return
	}
	if name != "" {
		fmt.Fprintf(&w.b, "Schema: `%s`\n\n", name)
	}
	if t := str(sch["type"]); t == "array" {
		if items, ok := sch["items"].(map[string]any); ok {
			in, isch := w.resolve(items)
			fmt.Fprintf(&w.b, "Mảng các phần tử `%s`.\n\n", firstNonEmpty(in, str(isch["type"])))
			sch = isch
		}
	}
	rows := w.rows(sch, "", 0, map[string]bool{name: true})
	if len(rows) > 0 {
		w.b.WriteString("| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |\n|---|---|---|---|---|---|\n")
		for _, r := range rows {
			w.b.WriteString(r)
			w.b.WriteString("\n")
		}
		w.b.WriteString("\n")
	} else if ap, ok := sch["additionalProperties"]; ok {
		_ = ap
		w.b.WriteString("Object tự do (khóa và giá trị tùy ý).\n\n")
	}
	if ex := w.example(sch, 0, map[string]bool{name: true}); ex != nil {
		b, _ := json.MarshalIndent(ex, "", "  ")
		fmt.Fprintf(&w.b, "```json\n%s\n```\n\n", b)
	}
}

func (w *refWriter) expands(name string) bool {
	if len(w.opt.ExpandPrefixes) == 0 {
		return true
	}
	for _, p := range w.opt.ExpandPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// resolve theo $ref hoặc allOf một phần tử; trả tên định nghĩa (rỗng nếu inline) và schema đã gỡ.
func (w *refWriter) resolve(s map[string]any) (string, map[string]any) {
	{
		if ref := str(s["$ref"]); ref != "" {
			n := ref[strings.LastIndex(ref, "/")+1:]
			d, ok := w.defs[n]
			if !ok {
				return n, map[string]any{}
			}
			return n, d
		}
		if all, ok := s["allOf"].([]any); ok && len(all) == 1 {
			if m, ok := all[0].(map[string]any); ok {
				n, d := w.resolve(m)
				// giữ mô tả riêng của trường nếu có
				if desc := str(s["description"]); desc != "" {
					cp := maps.Clone(d)
					cp["description"] = desc
					d = cp
				}
				return n, d
			}
		}
	}
	return "", s
}

func (w *refWriter) rows(sch map[string]any, prefix string, depth int, seen map[string]bool) []string {
	props, _ := sch["properties"].(map[string]any)
	if len(props) == 0 {
		return nil
	}
	req := map[string]bool{}
	if rs, ok := sch["required"].([]any); ok {
		for _, r := range rs {
			req[str(r)] = true
		}
	}
	names := make([]string, 0, len(props))
	for n := range props {
		if strings.HasPrefix(n, "_") || n == "extension" || n == "modifierExtension" || n == "contained" {
			continue // phần tử mở rộng của kiểu nguyên thủy FHIR: không phải hợp đồng của route
		}
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool {
		if req[names[i]] != req[names[j]] {
			return req[names[i]]
		}
		return names[i] < names[j]
	})
	var out []string
	for _, n := range names {
		p, _ := props[n].(map[string]any)
		cn, cs := w.resolve(p)
		typ := typeOf(cs)
		if cn != "" && str(cs["type"]) == "object" || cn != "" && cs["properties"] != nil {
			typ = "object `" + cn + "`"
		}
		itemName := ""
		if str(cs["type"]) == "array" {
			if items, ok := cs["items"].(map[string]any); ok {
				in, isch := w.resolve(items)
				itemName = in
				if in != "" {
					typ = "mảng `" + in + "`"
				} else {
					typ = "mảng " + typeOf(isch)
				}
			}
		}
		field := prefix + n
		desc := str(p["description"])
		if desc == "" {
			desc = str(cs["description"])
		}
		out = append(out, fmt.Sprintf("| `%s` | %s | %s | %s | %s | %s |", field, typ, yesNo(req[n]), constraints(cs), esc(desc), exampleCell(p, cs)))
		// khai triển lồng
		child := cn
		if itemName != "" {
			child = itemName
		}
		if depth < 4 && child != "" && !seen[child] && w.expands(child) {
			seen2 := copySeen(seen)
			seen2[child] = true
			var csch map[string]any
			if itemName != "" {
				items, _ := cs["items"].(map[string]any)
				_, csch = w.resolve(items)
				out = append(out, w.rows(csch, field+"[].", depth+1, seen2)...)
			} else {
				out = append(out, w.rows(cs, field+".", depth+1, seen2)...)
			}
		}
	}
	return out
}

func copySeen(m map[string]bool) map[string]bool {
	return maps.Clone(m)
}

func (w *refWriter) example(s map[string]any, depth int, seen map[string]bool) any {
	if depth > 5 {
		return nil
	}
	if ex, ok := s["example"]; ok {
		return ex
	}
	switch str(s["type"]) {
	case "array":
		items, _ := s["items"].(map[string]any)
		n, is := w.resolve(items)
		if n != "" && seen[n] {
			return []any{}
		}
		s2 := seen
		if n != "" {
			s2 = copySeen(seen)
			s2[n] = true
		}
		if !w.expandsOrInline(n) {
			return []any{}
		}
		if e := w.example(is, depth+1, s2); e != nil {
			return []any{e}
		}
		return []any{}
	case "object", "":
		props, _ := s["properties"].(map[string]any)
		if len(props) == 0 {
			return nil
		}
		names := make([]string, 0, len(props))
		for n := range props {
			if strings.HasPrefix(n, "_") || n == "extension" || n == "modifierExtension" || n == "contained" {
				continue
			}
			names = append(names, n)
		}
		sort.Strings(names)
		m := map[string]any{}
		for _, n := range names {
			p, _ := props[n].(map[string]any)
			cn, cs := w.resolve(p)
			if cn != "" && (seen[cn] || !w.expands(cn)) {
				continue
			}
			seen2 := seen
			if cn != "" {
				seen2 = copySeen(seen)
				seen2[cn] = true
			}
			if e := w.example(cs, depth+1, seen2); e != nil {
				if _, hasEx := p["example"]; hasEx || w.exampleWorthy(cs) {
					m[n] = e
				}
			}
		}
		if len(m) == 0 {
			return nil
		}
		return m
	case "string":
		if en, ok := s["enum"].([]any); ok && len(en) > 0 {
			return en[0]
		}
		return nil
	}
	return nil
}

func (w *refWriter) expandsOrInline(n string) bool { return n == "" || w.expands(n) }

// exampleWorthy: chỉ đưa vào JSON mẫu các trường có ví dụ, enum, hoặc là object/array dựng được từ ví dụ con.
func (w *refWriter) exampleWorthy(cs map[string]any) bool {
	if _, ok := cs["example"]; ok {
		return true
	}
	if _, ok := cs["enum"]; ok {
		return true
	}
	return str(cs["type"]) == "object" || str(cs["type"]) == "array" || cs["properties"] != nil
}

func constraints(s map[string]any) string {
	var c []string
	if en, ok := s["enum"].([]any); ok {
		var v []string
		for _, e := range en {
			v = append(v, fmt.Sprintf("`%v`", e))
		}
		c = append(c, "một trong: "+strings.Join(v, ", "))
	}
	if items, ok := s["items"].(map[string]any); ok {
		if en, ok := items["enum"].([]any); ok {
			var v []string
			for _, e := range en {
				v = append(v, fmt.Sprintf("`%v`", e))
			}
			c = append(c, "mỗi phần tử một trong: "+strings.Join(v, ", "))
		}
	}
	for _, k := range []struct{ key, label string }{{"minLength", "độ dài tối thiểu"}, {"maxLength", "độ dài tối đa"}, {"minimum", "tối thiểu"}, {"maximum", "tối đa"}} {
		if v, ok := s[k.key]; ok {
			c = append(c, fmt.Sprintf("%s %v", k.label, v))
		}
	}
	if f := str(s["format"]); f != "" {
		c = append(c, "định dạng "+f)
	}
	return strings.Join(c, "; ")
}

func exampleCell(p, cs map[string]any) string {
	ex, ok := p["example"]
	if !ok {
		ex, ok = cs["example"]
	}
	if !ok {
		return ""
	}
	b, _ := json.Marshal(ex)
	return "`" + esc(string(b)) + "`"
}

func typeOf(p map[string]any) string {
	t := str(p["type"])
	if t == "" {
		if p["properties"] != nil {
			return "object"
		}
		return "object"
	}
	if f := str(p["format"]); f != "" && t != "string" {
		return t + " (" + f + ")"
	}
	if t == "object" {
		if _, ok := p["additionalProperties"]; ok && p["properties"] == nil {
			return "object tự do"
		}
	}
	return t
}

func yesNo(v any) string {
	if b, ok := v.(bool); ok && b {
		return "có"
	}
	return "không"
}

func posVN(in string) string {
	switch in {
	case "path":
		return "đường dẫn"
	case "query":
		return "query"
	case "formData":
		return "form"
	}
	return in
}

func joinCode(v []string, empty string) string {
	if len(v) == 0 {
		return empty
	}
	c := make([]string, len(v))
	for i, s := range v {
		c[i] = "`" + s + "`"
	}
	return strings.Join(c, ", ")
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func firstNonEmpty(a ...string) string {
	for _, s := range a {
		if s != "" {
			return s
		}
	}
	return ""
}

// SyncReference sinh tài liệu tham chiếu vào dir (mỗi tag một tệp). update=true thì ghi; trả danh sách tệp lỗi thời (khi update=false) hoặc thừa.
func SyncReference(swaggerPath, dir string, opt ReferenceOptions, update bool) (stale []string, err error) {
	spec, err := os.ReadFile(swaggerPath)
	if err != nil {
		return nil, err
	}
	files, err := RenderReference(spec, opt)
	if err != nil {
		return nil, err
	}
	if update {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	for name, want := range files {
		p := filepath.Join(dir, name)
		got, rerr := os.ReadFile(p)
		if string(got) != want || rerr != nil {
			if update {
				if err := os.WriteFile(p, []byte(want), 0o644); err != nil {
					return nil, err
				}
			} else {
				stale = append(stale, name)
			}
		}
	}
	sort.Strings(stale)
	return stale, nil
}

// CheckCases kiểm mỗi route trong swagger có mục `<a id="slug">` trong casesDir/<tag>.md, và ngược lại không có mục mồ côi. Trả danh sách vi phạm.
func CheckCases(swagger []byte, casesDir string) ([]string, error) {
	var sp rawSpec
	if err := json.Unmarshal(swagger, &sp); err != nil {
		return nil, err
	}
	want := map[string]map[string]string{} // tag -> slug -> route
	for p, ms := range sp.Paths {
		for m, op := range ms {
			tag := "khac"
			if len(op.Tags) > 0 {
				tag = op.Tags[0]
			}
			if want[tag] == nil {
				want[tag] = map[string]string{}
			}
			want[tag][Slug(m, p)] = strings.ToUpper(m) + " " + p
		}
	}
	anchor := regexp.MustCompile(`<a id="([a-z0-9-]+)"></a>`)
	var problems []string
	for tag, slugs := range want {
		raw, err := os.ReadFile(filepath.Join(casesDir, tag+".md"))
		if err != nil {
			problems = append(problems, fmt.Sprintf("thiếu tệp cases/%s.md (nhóm %s có %d route)", tag, tag, len(slugs)))
			continue
		}
		have := map[string]bool{}
		for _, m := range anchor.FindAllStringSubmatch(string(raw), -1) {
			have[m[1]] = true
		}
		for s, route := range slugs {
			if !have[s] {
				problems = append(problems, fmt.Sprintf("cases/%s.md thiếu mục cho %s (neo %s)", tag, route, s))
			}
		}
		for s := range have {
			if _, ok := slugs[s]; !ok {
				problems = append(problems, fmt.Sprintf("cases/%s.md có mục mồ côi %q (route không còn trong swagger)", tag, s))
			}
		}
	}
	sort.Strings(problems)
	return problems, nil
}
