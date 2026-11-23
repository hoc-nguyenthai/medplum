package apicontract

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// good là một swagger đạt mọi quy tắc với Options mặc định.
const good = `{
 "tags":[{"name":"t","description":"nhóm t"}],
 "paths":{
  "/a/{id}":{"post":{"summary":"s","description":"d","tags":["t"],"produces":["application/json","application/fhir+json"],
    "parameters":[{"name":"id","in":"path","required":true,"description":"id"},{"name":"body","in":"body","schema":{"$ref":"#/definitions/x.InBody"}}],
    "responses":{"200":{"schema":{"$ref":"#/definitions/x.Out"}},"400":{"schema":{"$ref":"#/definitions/fhir.OperationOutcome"}},"204":{}}}}
 },
 "definitions":{
  "x.InBody":{"required":["name"],"properties":{"name":{"type":"string","description":"tên","example":"An"},"flag":{"type":"boolean","description":"cờ","example":true}}},
  "x.Out":{"required":["id"],"properties":{"id":{"type":"string","description":"id"}}},
  "fhir.OperationOutcome":{"properties":{"resourceType":{"type":"string"}}}
 }}`

func opts() Options {
	return Options{ContentType: func(def string) string {
		if def == "fhir.OperationOutcome" {
			return "application/fhir+json"
		}
		return "application/json"
	}}
}

func TestGoodSpecPasses(t *testing.T) {
	for name, fn := range map[string]func([]byte, Options) ([]string, error){"Completeness": Completeness, "Structure": Structure} {
		got, err := fn([]byte(good), opts())
		if err != nil || len(got) != 0 {
			t.Errorf("%s: phải đạt, được %v (%v)", name, got, err)
		}
	}
}

// mutate thay một đoạn của spec tốt rồi kiểm có đúng một vi phạm chứa want.
func mutate(t *testing.T, name, from, to, want string, fn func([]byte, Options) ([]string, error), o Options) {
	t.Helper()
	if !strings.Contains(good, from) {
		t.Fatalf("%s: mẫu %q không có trong spec tốt", name, from)
	}
	got, err := fn([]byte(strings.Replace(good, from, to, 1)), o)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	for _, g := range got {
		if strings.Contains(g, want) {
			return
		}
	}
	t.Errorf("%s: mong vi phạm chứa %q, được %v", name, want, got)
}

func TestCompletenessCatchesGaps(t *testing.T) {
	c := Completeness
	mutate(t, "thiếu summary", `"summary":"s"`, `"summary":""`, "thiếu @Summary", c, opts())
	mutate(t, "thiếu description", `"description":"d"`, `"description":""`, "thiếu @Description", c, opts())
	mutate(t, "tham số thiếu mô tả", `"required":true,"description":"id"`, `"required":true`, "thiếu mô tả", c, opts())
	mutate(t, "body không phải DTO", `"schema":{"$ref":"#/definitions/x.InBody"}`, `"schema":{"type":"object"}`, "body không trỏ tới DTO", c, opts())
	mutate(t, "2xx thiếu schema", `"200":{"schema":{"$ref":"#/definitions/x.Out"}}`, `"200":{}`, "phản hồi 200 thiếu schema", c, opts())
	mutate(t, "lỗi thiếu schema", `"400":{"schema":{"$ref":"#/definitions/fhir.OperationOutcome"}}`, `"400":{}`, "phản hồi 400 thiếu schema", c, opts())
	mutate(t, "tag thiếu mô tả", `"description":"nhóm t"`, `"description":""`, `tag "t" thiếu mô tả`, c, opts())
	mutate(t, "trường thiếu mô tả", `"type":"string","description":"tên"`, `"type":"string"`, "x.InBody.name: thiếu mô tả", c, opts())
	mutate(t, "thiếu required", `"required":["id"],`, ``, "x.Out: không có trường bắt buộc", c, opts())
	mutate(t, "body thiếu example", `"description":"tên","example":"An"`, `"description":"tên"`, "thiếu example", c, opts())
}

func TestCompletenessOptionsExemptions(t *testing.T) {
	o := opts()
	o.AllOptional = map[string]string{"x.Out": "toàn trường tùy chọn"}
	got, _ := Completeness([]byte(strings.Replace(good, `"required":["id"],`, ``, 1)), o)
	if len(got) != 0 {
		t.Errorf("AllOptional phải miễn: %v", got)
	}
	o = opts()
	o.NoBody = map[string]string{"POST /a/{id} 400": "không có body"}
	got, _ = Completeness([]byte(strings.Replace(good, `"400":{"schema":{"$ref":"#/definitions/fhir.OperationOutcome"}}`, `"400":{}`, 1)), o)
	if len(got) != 0 {
		t.Errorf("NoBody phải miễn: %v", got)
	}
	o = opts()
	o.Own = func(d string) bool { return d == "x.Out" }
	got, _ = Completeness([]byte(strings.Replace(good, `"type":"string","description":"tên"`, `"type":"string"`, 1)), o)
	if len(got) != 0 {
		t.Errorf("Own phải loại DTO không thuộc service: %v", got)
	}
	o = opts()
	o.RequestSuffixes = []string{"Request"}
	got, _ = Completeness([]byte(strings.Replace(good, `"description":"tên","example":"An"`, `"description":"tên"`, 1)), o)
	if len(got) != 0 {
		t.Errorf("hậu tố khác thì x.InBody không còn là body yêu cầu: %v", got)
	}
}

func TestStructureCatchesDefects(t *testing.T) {
	s := Structure
	mutate(t, "$ref hỏng", `"#/definitions/x.Out"`, `"#/definitions/x.Nope"`, "không trỏ tới định nghĩa có thật", s, opts())
	mutate(t, "path param không required", `"required":true,"description":"id"`, `"required":false,"description":"id"`, "phải có required: true", s, opts())
	mutate(t, "path param lệch mẫu", `"name":"id","in":"path"`, `"name":"uid","in":"path"`, `thiếu tham số đường dẫn "id"`, s, opts())
	mutate(t, "required không phải trường", `"required":["name"]`, `"required":["ghost"]`, `required "ghost" không phải trường`, s, opts())
	mutate(t, "example trên tham số path", `"name":"id","in":"path","required":true`, `"name":"id","in":"path","required":true,"example":"1"`, "có `example`", s, opts())
	mutate(t, "produces thiếu", `"produces":["application/json","application/fhir+json"]`, `"produces":["application/json"]`, "produces thiếu application/fhir+json", s, opts())
}

func TestDiffRoutes(t *testing.T) {
	in, sp, err := DiffRoutes([]byte(good), []string{"post /a/{id}", "get /extra"}, nil)
	if err != nil || len(in) != 1 || in[0] != "get /extra" || len(sp) != 0 {
		t.Fatalf("code-only: %v spec-only: %v err: %v", in, sp, err)
	}
	in, sp, _ = DiffRoutes([]byte(good), nil, nil)
	if len(in) != 0 || len(sp) != 1 || sp[0] != "post /a/{id}" {
		t.Fatalf("spec-only: %v %v", in, sp)
	}
	_, sp, _ = DiffRoutes([]byte(good), nil, func(k string) bool { return strings.HasPrefix(k, "post /a") })
	if len(sp) != 0 {
		t.Errorf("ignore phải bỏ qua: %v", sp)
	}
	if _, _, err := DiffRoutes([]byte("{"), nil, nil); err == nil {
		t.Error("JSON hỏng phải lỗi")
	}
}

func TestSyncDoc(t *testing.T) {
	dir := t.TempDir()
	spec := filepath.Join(dir, "swagger.json")
	doc := filepath.Join(dir, "api-contract.md")
	_ = os.WriteFile(spec, []byte(good), 0o644)
	_ = os.WriteFile(doc, []byte("đầu\n"+Begin+"\ncũ\n"+End+"\ncuối\n"), 0o644)
	if stale, err := SyncDoc(spec, doc, false); err != nil || !stale {
		t.Fatalf("tệp cũ phải báo stale: %v %v", stale, err)
	}
	if stale, err := SyncDoc(spec, doc, true); err != nil || stale {
		t.Fatalf("update: %v %v", stale, err)
	}
	if stale, err := SyncDoc(spec, doc, false); err != nil || stale {
		t.Fatalf("sau update phải khớp: %v %v", stale, err)
	}
	b, _ := os.ReadFile(doc)
	if !strings.Contains(string(b), "`POST /a/{id}`") || !strings.HasSuffix(string(b), "cuối\n") {
		t.Errorf("nội dung sau sinh sai: %q", b)
	}
	if _, err := SyncDoc(spec, filepath.Join(dir, "none.md"), false); err == nil {
		t.Error("thiếu tài liệu phải lỗi")
	}
}
