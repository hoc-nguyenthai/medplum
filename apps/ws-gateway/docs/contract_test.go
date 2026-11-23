package docs

import (
	"encoding/json"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"trivita-fhir/pkg/apicontract"
)

// Bảng route trong api-contract.md được SINH từ swagger.json (phần giữa hai dấu BEGIN/END); test này so sánh với tệp trong repo.
// Cập nhật: UPDATE_DOCS=1 go test ./apps/ws-gateway/docs -run TestAPIContractIsUpToDate
func TestAPIContractIsUpToDate(t *testing.T) {
	spec, err := os.ReadFile("swagger.json")
	if err != nil {
		t.Fatal(err)
	}
	table, err := apicontract.Render(spec)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := os.ReadFile("api-contract.md")
	if err != nil {
		t.Fatalf("thiếu api-contract.md: %v", err)
	}
	want, err := apicontract.Splice(string(doc), table)
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("UPDATE_DOCS") == "1" {
		if err := os.WriteFile("api-contract.md", []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	if string(doc) != want {
		t.Error("api-contract.md đã lỗi thời so với swagger.json: chạy UPDATE_DOCS=1 go test ./apps/ws-gateway/docs -run TestAPIContractIsUpToDate (APP=auth|ws-gateway)")
	}
}

// TestSwaggerDTOsAreComplete bắt buộc swagger.json của ws-gateway mô tả đủ mọi DTO do ws-gateway sở hữu (httpapi.*)
// ngoài phạm vi): mỗi trường có mô tả, mỗi DTO có danh sách `required` (từ tag `binding:"required"`), mỗi trường nguyên thủy của body yêu cầu có ví dụ hoặc
// enum; mỗi route có tóm tắt, mô tả, schema cho mọi phản hồi 2xx (trừ 204/302) và lỗi, mô tả cho từng tham số; mỗi tag có mô tả. Thiếu thì sửa struct hoặc
// annotation rồi `make swagger APP=ws-gateway` (không sửa swagger.json bằng tay).
func TestSwaggerDTOsAreComplete(t *testing.T) {
	// DTO mà mọi trường đều tùy chọn một cách chính đáng (lý do ở bên phải). Thêm vào đây phải có lý do; không dùng để né việc đánh dấu `required`.
	allOptional := map[string]string{
		"httpapi.FhircastHubRequest":  "một route, hai dạng body (đăng ký/hủy dùng hub.*, đổi ngữ cảnh dùng id/timestamp/event): không trường nào luôn bắt buộc",
		"httpapi.FhircastHubResponse": "một route, hai dạng phản hồi (hub.channel.endpoint, hoặc success và event)",
	}
	// phản hồi 2xx/4xx/5xx được phép không có schema vì server không trả body (101 không thuộc 2xx nên không cần liệt kê)
	noBody := map[string]string{}

	raw, err := os.ReadFile("swagger.json")
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Tags []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"tags"`
		Paths map[string]map[string]struct {
			Summary     string   `json:"summary"`
			Description string   `json:"description"`
			Tags        []string `json:"tags"`
			Parameters  []struct {
				Name        string `json:"name"`
				In          string `json:"in"`
				Description string `json:"description"`
				Schema      struct {
					Ref   string `json:"$ref"`
					Items struct {
						Ref string `json:"$ref"`
					} `json:"items"`
				} `json:"schema"`
			} `json:"parameters"`
			Responses map[string]struct {
				Schema map[string]any `json:"schema"`
			} `json:"responses"`
		} `json:"paths"`
		Definitions map[string]struct {
			Properties map[string]map[string]any `json:"properties"`
			Required   []string                  `json:"required"`
		} `json:"definitions"`
	}
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}

	tagDesc := map[string]string{}
	for _, tg := range spec.Tags {
		tagDesc[tg.Name] = tg.Description
	}
	used := map[string]bool{}
	for path, methods := range spec.Paths {
		for method, op := range methods {
			id := strings.ToUpper(method) + " " + path
			if op.Summary == "" {
				t.Errorf("%s: thiếu @Summary", id)
			}
			if op.Description == "" {
				t.Errorf("%s: thiếu @Description", id)
			}
			for _, tg := range op.Tags {
				used[tg] = true
			}
			for _, p := range op.Parameters {
				if p.In == "body" {
					if p.Schema.Ref == "" && p.Schema.Items.Ref == "" {
						t.Errorf("%s: body không trỏ tới DTO có tên (dùng struct, hoặc mảng của DTO)", id)
					}
					continue
				}
				if p.Description == "" {
					t.Errorf("%s: tham số %s (%s) thiếu mô tả", id, p.Name, p.In)
				}
			}
			for code, r := range op.Responses {
				if (strings.HasPrefix(code, "2") || strings.HasPrefix(code, "4") || strings.HasPrefix(code, "5")) && code != "204" && len(r.Schema) == 0 && noBody[id+" "+code] == "" {
					t.Errorf("%s: phản hồi %s thiếu schema", id, code)
				}
			}
		}
	}
	for tg := range used {
		if tagDesc[tg] == "" {
			t.Errorf("tag %q thiếu mô tả (@tag.description ở httpapi/swagger.go)", tg)
		}
	}

	names := make([]string, 0, len(spec.Definitions))
	for n := range spec.Definitions {
		if !strings.HasPrefix(n, "fhir.") {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	for _, n := range names {
		def := spec.Definitions[n]
		if len(def.Properties) == 0 {
			continue
		}
		if len(def.Required) == 0 && allOptional[n] == "" {
			t.Errorf("%s: không có trường bắt buộc nào (thêm `binding:\"required\"` cho trường luôn có/luôn phải gửi)", n)
		}
		isRequestBody := strings.HasSuffix(n, "Body") || strings.HasSuffix(n, "Request")
		for f, p := range def.Properties {
			_, isRef := p["$ref"]
			_, isAllOf := p["allOf"]
			if desc, _ := p["description"].(string); desc == "" && !isRef && !isAllOf {
				t.Errorf("%s.%s: thiếu mô tả (bình luận cuối dòng trên trường)", n, f)
			}
			if isRequestBody {
				switch p["type"] {
				case "string", "integer", "number", "boolean":
					_, hasExample := p["example"]
					_, hasEnum := p["enum"]
					if !hasExample && !hasEnum {
						t.Errorf("%s.%s: body yêu cầu thiếu example (tag `example:\"...\"`)", n, f)
					}
				}
			}
		}
	}
}

// TestSwaggerIsStructurallyValid bắt các lỗi mà trình xem OpenAPI/IDE báo hoặc làm "Try it out" sai: tham số đường dẫn phải khớp mẫu đường dẫn và `required`,
// mọi $ref phải tồn tại, `required` của DTO phải là trường có thật, và `produces` phải khớp Content-Type thật của server: OperationOutcome của Hub là application/fhir+json, DTO khác
// là application/json (đã kiểm bằng cách gọi Hub qua test harness: lỗi do outcome() ghi fhir+json, thành công do c.JSON ghi json).
func TestSwaggerIsStructurallyValid(t *testing.T) {
	raw, err := os.ReadFile("swagger.json")
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Paths map[string]map[string]struct {
			Produces   []string `json:"produces"`
			Consumes   []string `json:"consumes"`
			Parameters []struct {
				Name     string `json:"name"`
				In       string `json:"in"`
				Required bool   `json:"required"`
				Example  any    `json:"example"`
			} `json:"parameters"`
			Responses map[string]struct {
				Schema struct {
					Ref string `json:"$ref"`
				} `json:"schema"`
			} `json:"responses"`
		} `json:"paths"`
		Definitions map[string]struct {
			Properties map[string]json.RawMessage `json:"properties"`
			Required   []string                   `json:"required"`
		} `json:"definitions"`
	}
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	// mọi $ref ở bất kỳ đâu phải trỏ tới định nghĩa có thật
	var refs func(v any)
	refs = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, val := range x {
				if k == "$ref" {
					if s, _ := val.(string); !strings.HasPrefix(s, "#/definitions/") || spec.Definitions[strings.TrimPrefix(s, "#/definitions/")].Properties == nil && !definitionExists(raw, strings.TrimPrefix(s, "#/definitions/")) {
						t.Errorf("$ref %q không trỏ tới định nghĩa có thật", s)
					}
					continue
				}
				refs(val)
			}
		case []any:
			for _, e := range x {
				refs(e)
			}
		}
	}
	var generic any
	_ = json.Unmarshal(raw, &generic)
	refs(generic)

	pathVar := regexp.MustCompile(`\{(\w+)\}`)
	for path, methods := range spec.Paths {
		want := map[string]bool{}
		for _, m := range pathVar.FindAllStringSubmatch(path, -1) {
			want[m[1]] = true
		}
		for method, op := range methods {
			id := strings.ToUpper(method) + " " + path
			got := map[string]bool{}
			for _, p := range op.Parameters {
				if p.In != "body" && p.Example != nil {
					t.Errorf("%s: tham số %s (%s) có `example`: Swagger 2.0 chỉ cho `example` ở schema, không cho tham số query/path/header/formData (đưa ví dụ vào mô tả)", id, p.Name, p.In)
				}
				if p.In != "path" {
					continue
				}
				got[p.Name] = true
				if !p.Required {
					t.Errorf("%s: tham số đường dẫn %q phải có required: true", id, p.Name)
				}
			}
			for n := range want {
				if !got[n] {
					t.Errorf("%s: thiếu tham số đường dẫn %q", id, n)
				}
			}
			for n := range got {
				if !want[n] {
					t.Errorf("%s: tham số đường dẫn %q không có trong mẫu đường dẫn", id, n)
				}
			}
			need := map[string]bool{}
			for _, r := range op.Responses {
				ref := strings.TrimPrefix(r.Schema.Ref, "#/definitions/")
				switch {
				case ref == "":
				case strings.HasSuffix(ref, "OperationOutcome"):
					need["application/fhir+json"] = true
				default:
					need["application/json"] = true
				}
			}
			have := map[string]bool{}
			for _, p := range op.Produces {
				have[p] = true
			}
			for mt := range need {
				if !have[mt] {
					t.Errorf("%s: produces thiếu %s (có %v): server trả Content-Type này cho một trong các phản hồi khai báo", id, mt, op.Produces)
				}
			}
		}
	}
	for n, def := range spec.Definitions {
		for _, r := range def.Required {
			if _, ok := def.Properties[r]; !ok {
				t.Errorf("%s: required %q không phải trường của DTO", n, r)
			}
		}
	}
}

func definitionExists(raw []byte, name string) bool {
	var s struct {
		Definitions map[string]json.RawMessage `json:"definitions"`
	}
	_ = json.Unmarshal(raw, &s)
	_, ok := s.Definitions[name]
	return ok
}
