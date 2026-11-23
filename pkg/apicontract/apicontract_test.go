package apicontract

import (
	"strings"
	"testing"
)

const sample = `{"paths":{
 "/a/{id}":{"get":{"summary":"Đọc | một","tags":["t"],"security":[{"BearerAuth":[]}],"parameters":[{"name":"id","in":"path"}],"responses":{"200":{"schema":{"$ref":"#/definitions/x.Thing"}},"404":{}}},
            "delete":{"tags":["t"],"responses":{"204":{}}}},
 "/b":{"post":{"summary":"Tạo","tags":["u"],"parameters":[{"name":"body","in":"body","schema":{"$ref":"#/definitions/x.In"}},{"name":"q","in":"query"}],"responses":{"201":{"schema":{"type":"array","items":{"$ref":"#/definitions/x.Thing"}}},"400":{},"500":{}}}}}}`

func TestRender(t *testing.T) {
	got, err := Render([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"### Nhóm `t` (2 route)", "### Nhóm `u` (1 route)",
		"| `GET /a/{id}` | Đọc \\| một | Bearer | - | 200 `x.Thing` | 404 |",
		"| `DELETE /a/{id}` |  | công khai | - | 204 | - |",
		"| `POST /b` | Tạo | công khai | body `x.In`; query: q | 201 `[]x.Thing` | 400, 500 |",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("thiếu %q trong:\n%s", want, got)
		}
	}
	if strings.Index(got, "GET /a") > strings.Index(got, "DELETE /a") {
		t.Error("GET phải đứng trước DELETE")
	}
}

func TestRenderRejectsBadJSON(t *testing.T) {
	if _, err := Render([]byte("{")); err == nil {
		t.Fatal("phải báo lỗi")
	}
}

func TestSplice(t *testing.T) {
	doc := "đầu\n" + Begin + "\ncũ\n" + End + "\ncuối\n"
	got, err := Splice(doc, "MỚI\n")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "MỚI") || strings.Contains(got, "cũ") || !strings.HasPrefix(got, "đầu\n") || !strings.HasSuffix(got, "cuối\n") {
		t.Errorf("kết quả sai: %q", got)
	}
	again, _ := Splice(got, "MỚI\n")
	if again != got {
		t.Error("Splice phải lũy đẳng")
	}
	if _, err := Splice("không có dấu", "x"); err == nil {
		t.Error("thiếu dấu phải lỗi")
	}
}
