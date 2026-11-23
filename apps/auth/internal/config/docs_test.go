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

	"trivita-fhir/apps/auth/internal/config"
)

// docs/config.md được SINH từ mã (tên biến môi trường, trường, giá trị mặc định, lời giải thích ở bình luận của trường) và test này
// so sánh với tệp trong repo: thêm hoặc đổi một biến mà không cập nhật tài liệu thì test đỏ. Cập nhật: UPDATE_DOCS=1 go test ./apps/auth/internal/config -run TestConfigDocIsUpToDate
const docPath = "../../docs/config.md"

// extraDesc bổ sung mô tả cho biến mà trường Settings không có bình luận riêng (chỉ nằm trong bình luận nhóm hoặc không có). Khóa là TÊN BIẾN.
// Biến nào có bình luận ở config.go thì bình luận đó thắng; mục ở đây mà không còn cần thiết thì test báo để xóa.
var extraDesc = map[string]string{
	"APP_NAME":                              "Tên service trong log và telemetry.",
	"HTTP_ADDR":                             "Địa chỉ lắng nghe HTTP.",
	"DEBUG":                                 "Chế độ debug; cấm ở staging/production.",
	"READINESS_TIMEOUT":                     "Thời gian chờ tối đa khi `/health/ready` kiểm tra DB.",
	"SHUTDOWN_DRAIN_DELAY":                  "Thời gian giữ service sống nhưng báo `/health/ready` 503 trước khi đóng, để load balancer rút instance (SIGTERM).",
	"SHUTDOWN_TIMEOUT":                      "Thời gian tối đa chờ các yêu cầu đang chạy xong khi tắt.",
	"READ_HEADER_TIMEOUT":                   "Thời gian chờ đọc header của yêu cầu (chống slowloris).",
	"DATABASE_ECHO":                         "In câu SQL ra log (chỉ để gỡ lỗi; có thể lộ dữ liệu).",
	"DATABASE_AUTO_MIGRATE":                 "Tự chạy migration khi khởi động. Phải là false khi đặt DATABASE_MIGRATE_URL.",
	"DATABASE_MAX_OPEN_CONNS":               "Số kết nối tối đa của pool (phải > 0).",
	"DATABASE_MAX_IDLE_CONNS":               "Số kết nối nhàn rỗi giữ lại (>= 0).",
	"DATABASE_CONN_MAX_LIFETIME":            "Tuổi tối đa của một kết nối trong pool.",
	"AUTH_AUDIENCE":                         "Claim `aud` của access token. Rỗng: dùng chính issuer.",
	"AUTH_ALLOW_PLAIN_PKCE":                 "Cho phép `code_challenge_method=plain` (mặc định chỉ S256).",
	"AUTH_REDIS_URL":                        "Redis (`redis://` hoặc `rediss://`) dùng chung nhiều instance cho jti của client assertion, hạn mức thử MFA và chống dùng lại mã MFA. Rỗng: nhớ trong bộ nhớ (chỉ đúng với MỘT instance). Bắt buộc ở staging/production.",
	"AUTH_EXTERNAL_STATE_TTL":               "Hạn của `state` đăng nhập ngoài và cookie liên kết.",
	"AUTH_EXTERNAL_AUTH_PROVIDERS":          "JSON mảng `{issuer, clientId?, userInfoUrl?, identityProvider?}` các IdP ngoài, tra theo issuer của state.",
	"AUTH_GOOGLE_ENABLED":                   "Bật route đăng nhập Google (`/auth/google`).",
	"AUTH_NEW_PROJECT_REGISTRATION_ENABLED": "false: chỉ tắt tạo project mới (đăng ký người dùng vẫn mở nếu AUTH_REGISTER_ENABLED=true).",
	"AUTH_REQUIRE_VERIFIED_EMAIL_FOR_PROJECT_CREATION": "`/auth/newuser` với `projectId=new` gửi thư xác minh email trước khi cho tạo project.",
	"AUTH_RECAPTCHA_TIMEOUT":                           "Thời gian chờ khi gọi dịch vụ reCAPTCHA (phải > 0).",
	"AUTH_SMTP_PORT":                                   "Cổng SMTP (mặc định 587; 465 là TLS ngay từ đầu).",
	"AUTH_SMTP_ALLOW_PLAINTEXT":                        "Chỉ dev: cho gửi không mã hóa khi máy chủ thiếu STARTTLS; cấm ở staging/production.",
	"AUTH_SMTP_TIMEOUT":                                "Thời gian chờ nối và nhận lời chào SMTP.",
	"AUTH_EMAIL_FHIR_TIMEOUT":                          "Thời gian chờ khi đọc tệp đính kèm `Binary/<id>` từ fhir-server (phải dương khi có AUTH_EMAIL_FHIR_URL).",
	"AUTH_EMAIL_MAX_RECIPIENTS":                        "Tổng người nhận (To, Cc, Bcc) tối đa mỗi thư (phải > 0).",
	"AUTH_EMAIL_MAX_BYTES":                             "Cỡ tối đa của một thư và của mỗi tệp tải theo URL (byte, phải > 0).",
	"AUTH_SCIM_ALLOWED_IPS":                            "Danh sách CIDR (cách nhau bằng dấu phẩy) được gọi `/scim/v2`; rỗng: không hạn chế. Bắt buộc ở staging/production khi bật SCIM với Basic.",
	"AUTH_SCIM_MAX_PAGE":                               "Cỡ trang mặc định và trần của `count` ở SCIM (phải > 0).",
	"AUTH_SCIM_MAX_PATCH_OPS":                          "Số thao tác tối đa của một PATCH SCIM (phải > 0).",
	"AUTH_SCIM_MAX_BODY_BYTES":                         "Cỡ body tối đa của yêu cầu SCIM (byte, phải > 0).",
	"AUTH_MFA_WINDOW":                                  "Số bước TOTP được chấp nhận mỗi bên (trong [0, 5]).",
	"AUTH_MFA_LOGIN_ATTEMPT_LIMIT":                     "Số lần thử mã MFA tối đa mỗi login trong AUTH_MFA_LOGIN_EXPIRATION.",
	"AUTH_MFA_USER_ATTEMPT_LIMIT":                      "Số lần thử mã MFA tối đa mỗi user trong AUTH_MFA_LOGIN_EXPIRATION.",
	"AUTH_MFA_LOGIN_EXPIRATION":                        "Cửa sổ đếm lượt thử MFA và tuổi tối đa của login đang chờ MFA.",
	"AUTH_MFA_EMAIL_CODE_EXPIRATION":                   "Hạn của mã MFA gửi qua email.",
	"AUTH_MFA_ENROLLMENT_EXPIRATION":                   "Hạn của secret MFA được mời đăng ký.",
	"AUTH_MFA_EMAIL_SEND_LIMIT":                        "Số lần gửi mã MFA qua email tối đa mỗi login trong hạn mã.",
	"AUTH_PWNED_TIMEOUT":                               "Thời gian chờ khi gọi API mật khẩu rò rỉ; hết hạn tính là lỗi (fail đóng).",
	"AUTH_RATE_BURST":                                  "Cỡ đợt của hạn mức chung (<= 0: gấp đôi AUTH_RATE_LIMIT).",
	"AUTH_LOGIN_RATE_BURST":                            "Cỡ đợt của hạn mức bước nhập thông tin đăng nhập; staging/production mặc định 10 khi không đặt cả AUTH_LOGIN_RATE_LIMIT.",
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
	b.WriteString("# auth: cấu hình\n\n")
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
		case (r.field == "RateLimit" || r.field == "LoginRateLimit") && d == "0":
			d = fmt.Sprintf("tắt ở local; staging/production: %v", map[string]float64{"RateLimit": config.DefaultRateLimit, "LoginRateLimit": config.DefaultLoginRateLimit}[r.field])
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
		t.Errorf("%s đã lỗi thời so với config.go: chạy UPDATE_DOCS=1 go test ./apps/auth/internal/config -run TestConfigDocIsUpToDate", docPath)
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

// prodRules là phần cố định của config.md: ràng buộc mà FromEnv/validate ép (xem config.go; config_test.go kiểm từng điều).
const prodRules = `
## Ràng buộc ở staging/production

` + "`ENVIRONMENT`" + ` là ` + "`staging`" + ` hoặc ` + "`production`" + ` thì service **từ chối khởi động** (liệt kê mọi lỗi một lượt) nếu vi phạm:

| Bắt buộc | Cấm |
|---|---|
| ` + "`AUTH_SIGNING_KEY_PATH`" + ` (tạo bằng ` + "`auth keys generate`" + `) | ` + "`DEBUG=true`" + ` |
| ` + "`AUTH_BASE_URL`" + `, ` + "`AUTH_ISSUER`" + `, ` + "`AUTH_APP_BASE_URL`" + `, ` + "`AUTH_FHIRCAST_HUB_URL`" + `, ` + "`AUTH_EMAIL_FHIR_URL`" + ` dùng https | SQLite làm ` + "`DATABASE_URL`" + ` |
| ` + "`AUTH_MFA_SECRET_KEYS`" + ` (mã hóa secret TOTP khi lưu) | ` + "`AUTH_MAIL_PROVIDER=log`" + ` (ghi link có secret ra log) |
| ` + "`AUTH_REDIS_URL`" + ` (hạn mức MFA, jti và topic FHIRcast dùng chung giữa instance) | ` + "`AUTH_ALLOW_PRIVATE_JWKS`" + `, ` + "`AUTH_EMAIL_ALLOW_UNSAFE_OUTBOUND`" + `, ` + "`AUTH_SMTP_ALLOW_PLAINTEXT`" + ` |
| ` + "`AUTH_EXTERNAL_STATE_KEY`" + ` khi có ` + "`AUTH_APP_BASE_URL`" + ` (base64, tối thiểu 32 byte) | ` + "`AUTH_COOKIE_SECURE=false`" + `, ` + "`AUTH_PWNED_ENABLED=false`" + ` |
| ` + "`AUTH_SCIM_ALLOWED_IPS`" + ` khi bật SCIM với Basic | ` + "`AUTH_BCRYPT_COST`" + ` dưới 10 |
| ` + "`AUTH_ALLOWED_ORIGINS`" + ` chỉ https, không có ` + "`*`" + ` | |

Hạn mức ` + "`AUTH_RATE_LIMIT`" + `/` + "`AUTH_LOGIN_RATE_LIMIT`" + ` tự bật (20 và 1 yêu cầu/giây/IP, đợt đăng nhập 10) khi không đặt; đặt ` + "`0`" + ` để tắt hẳn.
Tiền tố cũ ` + "`AUTHEN_*`" + ` bị từ chối (đã đổi thành ` + "`AUTH_*`" + `).
`
