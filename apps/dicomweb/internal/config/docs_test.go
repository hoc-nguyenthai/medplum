package config_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"trivita-fhir/apps/dicomweb/internal/config"
)

// docs/config.md được SINH từ mã (tên biến môi trường, trường, giá trị mặc định, lời giải thích ở bình luận của trường) và test này
// so sánh với tệp trong repo: thêm hoặc đổi một biến mà không cập nhật tài liệu thì test đỏ. Cập nhật: UPDATE_DOCS=1 go test ./apps/dicomweb/internal/config -run TestConfigDocIsUpToDate
const docPath = "../../docs/config.md"

func TestConfigDocIsUpToDate(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "config.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	// bình luận của từng trường Settings (bình luận ngay trên hoặc cùng dòng)
	comments := map[string]string{}
	ast.Inspect(f, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok || ts.Name.Name != "Settings" {
			return true
		}
		for _, fld := range ts.Type.(*ast.StructType).Fields.List {
			var parts []string
			if fld.Doc != nil {
				parts = append(parts, strings.TrimSpace(fld.Doc.Text()))
			}
			if fld.Comment != nil {
				parts = append(parts, strings.TrimSpace(fld.Comment.Text()))
			}
			for _, nm := range fld.Names {
				comments[nm.Name] = strings.Join(strings.Fields(strings.Join(parts, " ")), " ")
			}
		}
		return false
	})
	// cặp (trường, biến) từ các lời gọi r.X(&s.Field, "ENV")
	type row struct{ env, field string }
	var rows []row
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) != 2 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if id, ok := sel.X.(*ast.Ident); !ok || id.Name != "r" {
			return true
		}
		un, ok := call.Args[0].(*ast.UnaryExpr)
		if !ok {
			return true
		}
		fs, ok := un.X.(*ast.SelectorExpr)
		lit, ok2 := call.Args[1].(*ast.BasicLit)
		if !ok || !ok2 {
			return true
		}
		rows = append(rows, row{strings.Trim(lit.Value, `"`), fs.Sel.Name})
		return true
	})
	sort.Slice(rows, func(i, j int) bool { return rows[i].env < rows[j].env })

	// .env.example phải nhắc MỌI biến (dạng `VAR=` hoặc `# VAR=`): quy ước "mẫu cấu hình đầy đủ" của docs/service-principles.md.
	if envExample, err := os.ReadFile("../../.env.example"); err != nil {
		t.Errorf("thiếu .env.example: %v", err)
	} else {
		for _, r := range rows {
			if !regexp.MustCompile(`(?m)^\s*#?\s*` + regexp.QuoteMeta(r.env) + `=`).Match(envExample) {
				t.Errorf(".env.example thiếu biến %s (thêm dòng `# %s=<mặc định>` kèm một dòng mô tả)", r.env, r.env)
			}
		}
	}
	def := reflect.ValueOf(config.Default())
	var b strings.Builder
	b.WriteString("# dicomweb: cấu hình\n\n")
	b.WriteString("Tệp này **được sinh từ `internal/config/config.go`** (xem `docs_test.go`); đừng sửa tay. Biến của `trivita-observability-go` (`OTEL_*`, `LOG_LEVEL`, `LOG_JSON`) do thư viện đó đọc, không nằm ở đây. Khi cấu hình sai, service từ chối khởi động và nói rõ biến nào sai.\n\n")
	b.WriteString("| Biến | Mặc định | Ý nghĩa |\n|---|---|---|\n")
	for _, r := range rows {
		v := def.FieldByName(r.field)
		if !v.IsValid() {
			t.Fatalf("không có trường %s", r.field)
		}
		var d string
		switch x := v.Interface().(type) {
		case string:
			d = x
		case time.Duration:
			d = x.String()
		default:
			d = fmt.Sprint(x)
		}
		switch {
		case d == "":
			d = "(rỗng)"
		case r.field == "RateLimit" && d == "0":
			d = "0 (tắt ở local; 20 ở staging/production)"
		case strings.Contains(strings.ToLower(r.env), "secret") || strings.Contains(strings.ToLower(r.env), "signing_key"):
			d = "(không có; bắt buộc hoặc cấm tùy chế độ)"
		}
		c := cleanComment(r.field, comments[r.field])
		if c == "" {
			c = "(chưa có mô tả trong mã)"
		}
		fmt.Fprintf(&b, "| `%s` | `%s` | %s |\n", r.env, strings.ReplaceAll(d, "|", "\\|"), strings.ReplaceAll(c, "|", "\\|"))
	}
	got := b.String()
	if os.Getenv("UPDATE_DOCS") == "1" {
		if err := os.WriteFile(docPath, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("thiếu %s: chạy với UPDATE_DOCS=1 để sinh", docPath)
	}
	if string(want) != got {
		t.Errorf("%s đã lỗi thời so với config.go: chạy UPDATE_DOCS=1 go test ./apps/dicomweb/internal/config -run TestConfigDocIsUpToDate", docPath)
	}
}

// cleanComment bỏ tiêu đề nhóm ("---- ... ----") và tên trường đứng đầu câu để bảng đọc tự nhiên.
func cleanComment(field, c string) string {
	for {
		i := strings.Index(c, "----")
		if i < 0 {
			break
		}
		j := strings.Index(c[i+4:], "----")
		if j < 0 {
			break
		}
		c = strings.TrimSpace(c[:i] + " " + c[i+4+j+4:])
	}
	c = strings.TrimPrefix(c, field+" ")
	return strings.TrimSpace(c)
}
