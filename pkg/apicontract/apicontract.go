// Package apicontract giữ phần dùng chung của hợp đồng API theo từng service (swagger.json do swaggo sinh từ annotation):
//   - Render và Splice sinh bảng route của `apps/<service>/docs/api-contract.md` từ swagger.json: phần giữa hai dấu BEGIN/END là mã sinh, phần còn lại viết tay;
//   - SyncDoc đọc, so và (khi UPDATE_DOCS=1) ghi lại tệp đó;
//   - Completeness và Structure là bộ quy tắc kiểm swagger.json đủ DTO và đúng cấu trúc, chỉ khác nhau ở Options của từng service;
//   - DiffRoutes so route phục vụ thật với route trong swagger.json.
//
// Chỉ dùng stdlib, không import `testing`: hàm trả danh sách vi phạm để test của từng service tự báo lỗi (xem apps/<service>/docs/contract_test.go).
package apicontract

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Dấu đóng khung phần sinh trong tệp Markdown.
const (
	Begin = "<!-- BEGIN GENERATED ROUTES (từ swagger.json; chạy UPDATE_DOCS=1 go test để sinh lại, đừng sửa tay) -->"
	End   = "<!-- END GENERATED ROUTES -->"
)

type spec struct {
	Paths map[string]map[string]operation `json:"paths"`
}

type operation struct {
	Summary    string                `json:"summary"`
	Tags       []string              `json:"tags"`
	Security   []map[string][]string `json:"security"`
	Consumes   []string              `json:"consumes"`
	Produces   []string              `json:"produces"`
	Parameters []parameter           `json:"parameters"`
	Responses  map[string]response   `json:"responses"`
}

type parameter struct {
	Name     string  `json:"name"`
	In       string  `json:"in"`
	Required bool    `json:"required"`
	Schema   *schema `json:"schema"`
}

type response struct {
	Schema *schema `json:"schema"`
}

type schema struct {
	Ref   string  `json:"$ref"`
	Type  string  `json:"type"`
	Items *schema `json:"items"`
}

func (s *schema) name() string {
	switch {
	case s == nil:
		return ""
	case s.Ref != "":
		return s.Ref[strings.LastIndex(s.Ref, "/")+1:]
	case s.Type == "array" && s.Items != nil:
		return "[]" + s.Items.name()
	default:
		return s.Type
	}
}

var methodOrder = map[string]int{"get": 0, "post": 1, "put": 2, "patch": 3, "delete": 4}

// Render trả các bảng route (mỗi tag một mục `###`), sắp theo tag rồi đường dẫn, để kết quả ổn định.
func Render(swagger []byte) (string, error) {
	var sp spec
	if err := json.Unmarshal(swagger, &sp); err != nil {
		return "", fmt.Errorf("apicontract: swagger không đọc được: %w", err)
	}
	type row struct{ path, method string }
	byTag := map[string][]row{}
	for p, ms := range sp.Paths {
		for m, op := range ms {
			tag := "khác"
			if len(op.Tags) > 0 {
				tag = op.Tags[0]
			}
			byTag[tag] = append(byTag[tag], row{p, m})
		}
	}
	tags := make([]string, 0, len(byTag))
	for t := range byTag {
		tags = append(tags, t)
	}
	sort.Strings(tags)

	var b strings.Builder
	for _, tag := range tags {
		rows := byTag[tag]
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].path != rows[j].path {
				return rows[i].path < rows[j].path
			}
			return methodOrder[rows[i].method] < methodOrder[rows[j].method]
		})
		fmt.Fprintf(&b, "### Nhóm `%s` (%d route)\n\n| Route | Mô tả | Xác thực | Yêu cầu | Phản hồi thành công | Mã lỗi |\n|---|---|---|---|---|---|\n", tag, len(rows))
		for _, r := range rows {
			op := sp.Paths[r.path][r.method]
			fmt.Fprintf(&b, "| `%s %s` | %s | %s | %s | %s | %s |\n", strings.ToUpper(r.method), esc(r.path), esc(op.Summary), auth(op), request(op), success(op), errors(op))
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n") + "\n", nil
}

func esc(s string) string { return strings.ReplaceAll(strings.ReplaceAll(s, "|", "\\|"), "\n", " ") }

func auth(op operation) string {
	if len(op.Security) == 0 {
		return "công khai"
	}
	return "Bearer"
}

func request(op operation) string {
	var parts []string
	var form, query []string
	for _, p := range op.Parameters {
		switch p.In {
		case "body":
			parts = append(parts, "body `"+p.Schema.name()+"`")
		case "formData":
			form = append(form, p.Name)
		case "query":
			query = append(query, p.Name)
		}
	}
	if len(form) > 0 {
		parts = append(parts, "form: "+strings.Join(form, ", "))
	}
	if len(query) > 0 {
		parts = append(parts, "query: "+strings.Join(query, ", "))
	}
	if len(parts) == 0 {
		return "-"
	}
	return esc(strings.Join(parts, "; "))
}

func success(op operation) string {
	var out []string
	for _, code := range sortedCodes(op.Responses) {
		if !strings.HasPrefix(code, "2") && !strings.HasPrefix(code, "3") {
			continue
		}
		if n := op.Responses[code].Schema.name(); n != "" {
			out = append(out, code+" `"+n+"`")
		} else {
			out = append(out, code)
		}
	}
	return strings.Join(out, ", ")
}

func errors(op operation) string {
	var out []string
	for _, code := range sortedCodes(op.Responses) {
		if strings.HasPrefix(code, "4") || strings.HasPrefix(code, "5") {
			out = append(out, code)
		}
	}
	if len(out) == 0 {
		return "-"
	}
	return strings.Join(out, ", ")
}

func sortedCodes(m map[string]response) []string {
	out := make([]string, 0, len(m))
	for c := range m {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// Splice thay phần giữa Begin và End của doc bằng generated. Thiếu một trong hai dấu là lỗi (tránh ghi chèn sai chỗ).
func Splice(doc, generated string) (string, error) {
	i, j := strings.Index(doc, Begin), strings.Index(doc, End)
	if i < 0 || j < 0 || j < i {
		return "", fmt.Errorf("apicontract: tài liệu thiếu dấu %q hoặc %q", Begin, End)
	}
	return doc[:i+len(Begin)] + "\n\n" + generated + "\n" + doc[j:], nil
}
