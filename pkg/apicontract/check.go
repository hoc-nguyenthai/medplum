package apicontract

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

// Options là phần khác nhau giữa các service khi kiểm một swagger.json; mọi quy tắc còn lại dùng chung.
type Options struct {
	// Own cho biết một định nghĩa DTO có do service sở hữu không (chỉ định nghĩa của service bị kiểm mô tả, required, ví dụ). Mặc định: mọi định nghĩa
	// không mang tiền tố "fhir." (kiểu FHIR do tools/fhirgen sinh nằm ngoài phạm vi).
	Own func(def string) bool
	// AllOptional liệt kê DTO mà mọi trường đều tùy chọn một cách chính đáng, kèm lý do. Không dùng để né việc đánh dấu `required`.
	AllOptional map[string]string
	// NoBody liệt kê phản hồi 2xx/4xx/5xx được phép không có schema vì server không trả body: khóa là "METHOD /đường-dẫn mã", giá trị là lý do.
	NoBody map[string]string
	// ContentType trả Content-Type thật mà server dùng cho phản hồi có schema là định nghĩa def (đã bỏ "#/definitions/"); rỗng: không kiểm. Mặc định: mọi
	// định nghĩa là application/json. Phải khớp hành vi thật (đã kiểm bằng cách gọi server), không phải hình dung.
	ContentType func(def string) string
	// RequestSuffixes là hậu tố tên DTO của body yêu cầu: trường nguyên thủy của chúng phải có example hoặc enum. Mặc định {"Body"}.
	RequestSuffixes []string
}

func (o Options) own(def string) bool {
	if o.Own != nil {
		return o.Own(def)
	}
	return !strings.HasPrefix(def, "fhir.")
}

func (o Options) contentType(def string) string {
	if o.ContentType != nil {
		return o.ContentType(def)
	}
	return "application/json"
}

func (o Options) isRequestBody(def string) bool {
	suffixes := o.RequestSuffixes
	if len(suffixes) == 0 {
		suffixes = []string{"Body"}
	}
	for _, s := range suffixes {
		if strings.HasSuffix(def, s) {
			return true
		}
	}
	return false
}

type fullSpec struct {
	Tags []struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	} `json:"tags"`
	Paths map[string]map[string]struct {
		Summary     string   `json:"summary"`
		Description string   `json:"description"`
		Tags        []string `json:"tags"`
		Produces    []string `json:"produces"`
		Parameters  []struct {
			Name        string `json:"name"`
			In          string `json:"in"`
			Description string `json:"description"`
			Required    bool   `json:"required"`
			Example     any    `json:"example"`
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

func parseFull(swagger []byte) (*fullSpec, any, error) {
	var sp fullSpec
	if err := json.Unmarshal(swagger, &sp); err != nil {
		return nil, nil, fmt.Errorf("apicontract: swagger không đọc được: %w", err)
	}
	var generic any
	_ = json.Unmarshal(swagger, &generic)
	return &sp, generic, nil
}

// Completeness kiểm swagger.json mô tả đủ: mỗi route có tóm tắt, mô tả, schema cho mọi phản hồi 2xx/4xx/5xx (trừ 204 và Options.NoBody), mô tả cho từng tham số
// không phải body, body trỏ tới DTO có tên (hoặc mảng của DTO); mỗi tag được dùng có mô tả; mỗi DTO của service (Options.Own) có mô tả cho từng trường, danh sách
// `required` (trừ Options.AllOptional) và, với body yêu cầu, ví dụ hoặc enum cho từng trường nguyên thủy. Trả danh sách vi phạm đã sắp xếp (rỗng là đạt).
func Completeness(swagger []byte, o Options) ([]string, error) {
	sp, _, err := parseFull(swagger)
	if err != nil {
		return nil, err
	}
	var out []string
	add := func(format string, args ...any) { out = append(out, fmt.Sprintf(format, args...)) }

	tagDesc := map[string]string{}
	for _, tg := range sp.Tags {
		tagDesc[tg.Name] = tg.Description
	}
	used := map[string]bool{}
	for path, methods := range sp.Paths {
		for method, op := range methods {
			id := strings.ToUpper(method) + " " + path
			if op.Summary == "" {
				add("%s: thiếu @Summary", id)
			}
			if op.Description == "" {
				add("%s: thiếu @Description", id)
			}
			for _, tg := range op.Tags {
				used[tg] = true
			}
			for _, p := range op.Parameters {
				if p.In == "body" {
					if p.Schema.Ref == "" && p.Schema.Items.Ref == "" {
						add("%s: body không trỏ tới DTO có tên (dùng struct, hoặc mảng của DTO)", id)
					}
					continue
				}
				if p.Description == "" {
					add("%s: tham số %s (%s) thiếu mô tả", id, p.Name, p.In)
				}
			}
			for code, r := range op.Responses {
				checked := strings.HasPrefix(code, "2") || strings.HasPrefix(code, "4") || strings.HasPrefix(code, "5")
				if checked && code != "204" && len(r.Schema) == 0 && o.NoBody[id+" "+code] == "" {
					add("%s: phản hồi %s thiếu schema", id, code)
				}
			}
		}
	}
	for tg := range used {
		if tagDesc[tg] == "" {
			add("tag %q thiếu mô tả (@tag.description)", tg)
		}
	}

	names := make([]string, 0, len(sp.Definitions))
	for n := range sp.Definitions {
		if o.own(n) {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	for _, n := range names {
		def := sp.Definitions[n]
		if len(def.Properties) == 0 {
			continue
		}
		if len(def.Required) == 0 && o.AllOptional[n] == "" {
			add("%s: không có trường bắt buộc nào (thêm `binding:\"required\"` cho trường luôn có/luôn phải gửi)", n)
		}
		isRequestBody := o.isRequestBody(n)
		for f, p := range def.Properties {
			_, isRef := p["$ref"]
			_, isAllOf := p["allOf"]
			if desc, _ := p["description"].(string); desc == "" && !isRef && !isAllOf {
				add("%s.%s: thiếu mô tả (bình luận cuối dòng trên trường)", n, f)
			}
			if isRequestBody {
				switch p["type"] {
				case "string", "integer", "number", "boolean":
					_, hasExample := p["example"]
					_, hasEnum := p["enum"]
					if !hasExample && !hasEnum {
						add("%s.%s: body yêu cầu thiếu example (tag `example:\"...\"`)", n, f)
					}
				}
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

var pathVar = regexp.MustCompile(`\{(\w+)\}`)

// Structure kiểm các lỗi mà trình xem OpenAPI hoặc IDE báo, hoặc làm "Try it out" sai: mọi $ref phải trỏ tới định nghĩa có thật; tham số đường dẫn phải khớp mẫu
// đường dẫn và có required; tham số query/path/header/formData không được có `example` (Swagger 2.0 chỉ cho `example` ở schema); `required` của DTO phải là trường
// có thật; `produces` phải chứa Content-Type thật (Options.ContentType) của mọi phản hồi có schema. Trả danh sách vi phạm đã sắp xếp.
func Structure(swagger []byte, o Options) ([]string, error) {
	sp, generic, err := parseFull(swagger)
	if err != nil {
		return nil, err
	}
	var out []string
	add := func(format string, args ...any) { out = append(out, fmt.Sprintf(format, args...)) }

	var refs func(v any)
	refs = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, val := range x {
				if k == "$ref" {
					s, _ := val.(string)
					if _, ok := sp.Definitions[strings.TrimPrefix(s, "#/definitions/")]; !strings.HasPrefix(s, "#/definitions/") || !ok {
						add("$ref %q không trỏ tới định nghĩa có thật", s)
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
	refs(generic)

	for path, methods := range sp.Paths {
		want := map[string]bool{}
		for _, m := range pathVar.FindAllStringSubmatch(path, -1) {
			want[m[1]] = true
		}
		for method, op := range methods {
			id := strings.ToUpper(method) + " " + path
			got := map[string]bool{}
			for _, p := range op.Parameters {
				if p.In != "body" && p.Example != nil {
					add("%s: tham số %s (%s) có `example`: Swagger 2.0 chỉ cho `example` ở schema, không cho tham số query/path/header/formData (đưa ví dụ vào mô tả)", id, p.Name, p.In)
				}
				if p.In != "path" {
					continue
				}
				got[p.Name] = true
				if !p.Required {
					add("%s: tham số đường dẫn %q phải có required: true", id, p.Name)
				}
			}
			for n := range want {
				if !got[n] {
					add("%s: thiếu tham số đường dẫn %q", id, n)
				}
			}
			for n := range got {
				if !want[n] {
					add("%s: tham số đường dẫn %q không có trong mẫu đường dẫn", id, n)
				}
			}
			need := map[string]bool{}
			for _, r := range op.Responses {
				ref, _ := r.Schema["$ref"].(string)
				if ref = strings.TrimPrefix(ref, "#/definitions/"); ref != "" {
					if ct := o.contentType(ref); ct != "" {
						need[ct] = true
					}
				}
			}
			have := map[string]bool{}
			for _, p := range op.Produces {
				have[p] = true
			}
			for mt := range need {
				if !have[mt] {
					add("%s: produces thiếu %s (có %v): server trả Content-Type này cho một trong các phản hồi khai báo", id, mt, op.Produces)
				}
			}
		}
	}
	for n, def := range sp.Definitions {
		for _, r := range def.Required {
			if _, ok := def.Properties[r]; !ok {
				add("%s: required %q không phải trường của DTO", n, r)
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

// DiffRoutes so các route phục vụ thật (khóa "method đường-dẫn", method viết thường, tham số dạng {tên}) với các route trong swagger.json. ignore (có thể nil) cho
// biết khóa của swagger nào được phép không có route thật (ví dụ route do router con xử lý). Trả hai danh sách đã sắp xếp: có trong code mà thiếu ở spec, và ngược lại.
func DiffRoutes(swagger []byte, served []string, ignore func(key string) bool) (inCodeOnly, inSpecOnly []string, err error) {
	var sp struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(swagger, &sp); err != nil {
		return nil, nil, fmt.Errorf("apicontract: swagger không đọc được: %w", err)
	}
	documented := map[string]bool{}
	for p, ms := range sp.Paths {
		for m := range ms {
			documented[m+" "+p] = true
		}
	}
	servedSet := map[string]bool{}
	for _, k := range served {
		servedSet[k] = true
		if !documented[k] {
			inCodeOnly = append(inCodeOnly, k)
		}
	}
	for k := range documented {
		if !servedSet[k] && (ignore == nil || !ignore(k)) {
			inSpecOnly = append(inSpecOnly, k)
		}
	}
	sort.Strings(inCodeOnly)
	sort.Strings(inSpecOnly)
	return inCodeOnly, inSpecOnly, nil
}

// SyncDoc đọc swaggerPath và docPath (Markdown có hai dấu Begin/End), sinh lại bảng route và so với tệp. update=true thì ghi đè tệp (UPDATE_DOCS=1). Trả stale=true khi
// tệp lỗi thời và update=false.
func SyncDoc(swaggerPath, docPath string, update bool) (stale bool, err error) {
	spec, err := os.ReadFile(swaggerPath)
	if err != nil {
		return false, err
	}
	table, err := Render(spec)
	if err != nil {
		return false, err
	}
	doc, err := os.ReadFile(docPath)
	if err != nil {
		return false, fmt.Errorf("thiếu %s: %w", docPath, err)
	}
	want, err := Splice(string(doc), table)
	if err != nil {
		return false, err
	}
	if update {
		return false, os.WriteFile(docPath, []byte(want), 0o644)
	}
	return string(doc) != want, nil
}
