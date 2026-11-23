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

	"trivita-fhir/apps/fhir-server/internal/config"
)

// docs/config.md được SINH từ mã (tên biến môi trường, trường, giá trị mặc định, lời giải thích ở bình luận của trường) và test này
// so sánh với tệp trong repo: thêm hoặc đổi một biến mà không cập nhật tài liệu thì test đỏ. Cập nhật: UPDATE_DOCS=1 go test ./apps/fhir-server/internal/config -run TestConfigDocIsUpToDate
const docPath = "../../docs/config.md"

// extraDesc bổ sung mô tả cho biến mà trường Settings không có bình luận riêng (chỉ nằm trong bình luận nhóm hoặc không có). Khóa là TÊN BIẾN.
// Biến nào có bình luận ở config.go thì bình luận đó thắng; mục ở đây mà không còn cần thiết thì test báo để xóa.
var extraDesc = map[string]string{
	"APP_NAME":                           "Tên service trong log và telemetry.",
	"HTTP_ADDR":                          "Địa chỉ lắng nghe HTTP.",
	"DEBUG":                              "Chế độ debug; cấm ở staging/production.",
	"READINESS_TIMEOUT":                  "Thời gian chờ tối đa khi `/readyz` kiểm DB (phải > 0).",
	"SHUTDOWN_DRAIN_DELAY":               "Thời gian giữ service sống nhưng báo `/readyz` 503 trước khi đóng, để load balancer rút instance (SIGTERM); phải >= 0.",
	"SHUTDOWN_TIMEOUT":                   "Thời gian tối đa chờ yêu cầu đang chạy xong khi tắt (phải > 0). Job nền chưa xong được nhả lease để instance khác chạy lại.",
	"READ_HEADER_TIMEOUT":                "Thời gian chờ đọc header của yêu cầu (chống slowloris).",
	"DATABASE_URL":                       "URL cơ sở dữ liệu: `postgres://` (production) hoặc `sqlite://<tệp>` (chỉ dev). Cấm SQLite ở staging/production. Chung một PostgreSQL với auth thì dùng role `fhir_app` có schema riêng `fhir` và bật DATABASE_REQUIRE_SCHEMA.",
	"DATABASE_ECHO":                      "In câu SQL ra log (chỉ để gỡ lỗi; có thể lộ dữ liệu lâm sàng).",
	"DATABASE_AUTO_MIGRATE":              "Tự chạy migration khi khởi động. Nhiều replica hoặc role migrate riêng thì đặt false và chạy `fhir-server migrate up` ở bước deploy; phải là false khi đặt DATABASE_MIGRATE_URL.",
	"DATABASE_MAX_OPEN_CONNS":            "Số kết nối tối đa của pool (phải > 0). Tổng mọi replica của mọi service phải nhỏ hơn `max_connections` của PostgreSQL.",
	"DATABASE_MAX_IDLE_CONNS":            "Số kết nối nhàn rỗi giữ lại (>= 0).",
	"DATABASE_CONN_MAX_LIFETIME":         "Tuổi tối đa của một kết nối trong pool.",
	"BROKER_DRIVER":                      "Broker sự kiện của Subscription: `memory` (một tiến trình) hoặc `redis` (nhiều replica, cần REDIS_URL).",
	"REDIS_URL":                          "Redis (`redis://...`) cho broker khi bật FHIR_SUBSCRIPTIONS với BROKER_DRIVER=redis (bắt buộc); cùng Redis với ws-gateway khi FHIR_WS_MODE=issuer.",
	"FHIR_BATCH_CHECKPOINT_ENTRIES":      "Số entry giữa hai lần lưu checkpoint của batch async (ngoài ra lưu ít nhất mỗi 5 giây); phải > 0.",
	"FHIR_EXPORT_RETENTION":              "Thời gian giữ kết quả bulk export trước khi bị dọn; âm là không bao giờ xóa.",
	"FHIR_JOB_MAINTENANCE_INTERVAL":      "Chu kỳ chạy lại job mồ côi và xóa kết quả export hết hạn; 0 là tắt (phải >= 0).",
	"FHIR_JWKS_URL":                      "URL JWKS của identity provider (thường `<auth>/.well-known/jwks.json`) để kiểm chữ ký access token; https ở staging/production. Khi bật JWT cần đúng một trong FHIR_JWKS_URL hoặc FHIR_JWT_HMAC_SECRET.",
	"FHIR_JWT_AUDIENCE":                  "Claim `aud` mong đợi của access token (bằng AUTH_AUDIENCE của auth, mặc định chính issuer); bắt buộc khi đặt FHIR_JWT_ISSUER.",
	"FHIR_JWT_PROJECT_CLAIM":             "Tên claim chứa project của token, dùng để tách dữ liệu theo project (mặc định `fhir_project`); không được rỗng.",
	"FHIR_JWT_SCOPE_CLAIM":               "Tên claim chứa scope của token (mặc định `scope`); không được rỗng.",
	"FHIR_JWT_POLICY_CLAIM":              "Tên claim chứa AccessPolicy đã dịch của token (mặc định `fhir_access_policy`); không được rỗng.",
	"FHIR_RATE_BURST":                    "Cỡ đợt của hạn mức mỗi IP (mặc định gấp đôi FHIR_RATE_LIMIT, tối thiểu 1; không được âm).",
	"FHIR_S3_ACCESS_KEY_ID":              "Khóa truy cập S3, bắt buộc khi FHIR_BINARY_STORAGE dạng `s3:`; chỉ qua biến môi trường, không bao giờ vào log (chưa hỗ trợ khóa tạm từ IMDS/STS).",
	"FHIR_S3_SECRET_ACCESS_KEY":          "Secret của khóa truy cập S3, bắt buộc khi dùng `s3:`; chỉ qua biến môi trường, không bao giờ vào log.",
	"FHIR_S3_SESSION_TOKEN":              "Token phiên tạm (STS) đi kèm khóa truy cập S3, nếu bạn cấp khóa tạm bằng tay; rỗng là khóa dài hạn.",
	"FHIR_SKIP_CONSTRAINTS":              "Chỉ kiểm cấu trúc khi ghi, bỏ invariant FHIRPath (nhanh hơn, kém an toàn hơn). Khác FHIR_SKIP_VALIDATION (bỏ hẳn kiểm tra, cấm ở staging/production).",
	"FHIR_STORAGE_SIGNING_PREVIOUS_KEYS": "Các tệp PEM khóa CÔNG KHAI cũ (cách nhau bằng dấu phẩy, tối đa 3), chỉ để xác minh URL ký cũ khi xoay khóa; cần đặt cả FHIR_STORAGE_SIGNING_KEY_PATH.",
	"FHIR_WEBHOOK_WORKERS":               "Số worker giao Subscription rest-hook đồng thời (phải > 0).",
	"FHIR_WEBHOOK_MAX_ATTEMPTS":          "Số lần thử giao tối đa cho mỗi sự kiện rest-hook (phải > 0).",
	"FHIR_WEBHOOK_TIMEOUT":               "Thời gian chờ mỗi lần giao rest-hook (phải > 0).",
}

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
	usedExtra := map[string]bool{}
	def := reflect.ValueOf(config.Default())
	var b strings.Builder
	b.WriteString("# fhir-server: cấu hình\n\n")
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
			d = fmt.Sprintf("tắt ở local; staging/production: %d", config.DefaultProdRateLimit)
		}
		c := cleanComment(r.field, comments[r.field])
		if c == "" {
			c = extraDesc[r.env]
			usedExtra[r.env] = true
		} else if _, dup := extraDesc[r.env]; dup {
			t.Errorf("extraDesc[%s] thừa: trường đã có bình luận ở config.go, xóa mục này", r.env)
		}
		if c == "" {
			c = "(chưa có mô tả trong mã)"
		}
		fmt.Fprintf(&b, "| `%s` | `%s` | %s |\n", r.env, strings.ReplaceAll(d, "|", "\\|"), strings.ReplaceAll(c, "|", "\\|"))
	}
	b.WriteString(prodRules)
	for env := range extraDesc {
		if !usedExtra[env] {
			t.Errorf("extraDesc[%s] không khớp biến nào trong config.go: xóa mục này", env)
		}
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
		t.Errorf("%s đã lỗi thời so với config.go: chạy UPDATE_DOCS=1 go test ./apps/fhir-server/internal/config -run TestConfigDocIsUpToDate", docPath)
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

// prodRules là phần cố định của config.md: ràng buộc mà FromEnv/validate ép (xem config.go).
const prodRules = `
## Ràng buộc ở staging/production

` + "`ENVIRONMENT`" + ` là ` + "`staging`" + ` hoặc ` + "`production`" + ` thì service **từ chối khởi động** (liệt kê mọi lỗi một lượt) nếu vi phạm:

| Bắt buộc | Cấm |
|---|---|
| Xác thực bật: token tĩnh (` + "`FHIR_READ_TOKENS`" + `, ` + "`FHIR_WRITE_TOKENS`" + `, ` + "`FHIR_ADMIN_TOKENS`" + `, secret >= 24 ký tự) hoặc JWT (` + "`FHIR_JWT_ISSUER`" + ` kèm ` + "`FHIR_JWT_AUDIENCE`" + ` và ` + "`FHIR_JWKS_URL`" + `) | ` + "`FHIR_AUTH_DISABLED=true`" + ` |
| ` + "`FHIR_JWKS_URL`" + ` dùng https khi bật JWT | ` + "`FHIR_JWT_HMAC_SECRET`" + ` (chỉ cho dev/test; dùng JWKS) |
| ` + "`FHIR_JOB_SIGNING_KEY`" + ` (>= 32 ký tự, mọi replica dùng chung) khi dùng JWKS | ` + "`FHIR_SKIP_VALIDATION=true`" + ` |
| ` + "`FHIR_WS_SIGNING_KEY`" + ` (>= 32 ký tự) khi bật websocket Subscription | SQLite làm ` + "`DATABASE_URL`" + ` |
| ` + "`FHIR_WS_URL`" + ` dùng ` + "`wss://`" + ` ở ` + "`FHIR_WS_MODE=issuer`" + ` | ` + "`FHIR_WEBHOOK_ALLOW_PRIVATE=true`" + ` (tắt chống SSRF) |
| ` + "`FHIR_STORAGE_SIGNING_KEY_PATH`" + ` (khóa ký URL cố định) và ` + "`FHIR_STORAGE_BASE_URL`" + ` https khi bật kho Binary | |
| ` + "`FHIR_S3_ENDPOINT`" + ` dùng https khi có | |
| ` + "`FHIR_ALLOWED_ORIGINS`" + ` chỉ https, không có ` + "`*`" + ` | |

Hạn mức ` + "`FHIR_RATE_LIMIT`" + ` tự bật (20 yêu cầu/giây/IP) khi không đặt; đặt ` + "`0`" + ` để tắt hẳn. ` + "`FHIR_JWT_ISSUER`" + ` loại trừ với token tĩnh và ` + "`FHIR_AUTH_DISABLED`" + `. ` + "`FHIR_WS_MODE=issuer`" + ` cần ` + "`BROKER_DRIVER=redis`" + `.
`
