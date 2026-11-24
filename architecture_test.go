package codebase_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const mod = "trivita-fhir"

// rule mô tả một tầng: thư mục chứa nó và các import (theo tiền tố) được phép ngoài stdlib.
// Stdlib luôn được phép; import nội bộ phải khớp allow; import bên thứ ba phải khớp third (trừ khi anyThird).
type rule struct {
	dir   string
	allow []string // tiền tố đường dẫn import nội bộ (trong module) được phép
	third []string // tiền tố import bên thứ ba được phép
	// anyThird cho phép mọi thư viện bên thứ ba (chỉ adapter ở infrastructure).
	anyThird bool
}

var observabilityThird = []string{"trivita-fhir/pkg/observability", "go.opentelemetry.io/otel"}

var rules = []rule{
	// ---- fhir-server ----
	// Không có domain/application/infrastructure riêng: nghiệp vụ nằm ở pkg/fhir*; presentation chỉ gắn router FHIR vào Echo.
	{dir: "apps/fhir-server/internal/presentation",
		allow: []string{mod + "/apps/fhir-server/internal/presentation", mod + "/apps/fhir-server/docs", mod + "/pkg/fhir", mod + "/pkg/fhirrouter", mod + "/pkg/fhirauth", mod + "/pkg/httpsec", mod + "/pkg/ratelimit"},
		third: []string{"github.com/labstack/echo/v4", "github.com/swaggo/echo-swagger", "golang.org/x/time/rate"}},
	{dir: "apps/fhir-server/internal/config", allow: []string{mod + "/apps/fhir-server/internal/config", mod + "/pkg/envconfig", mod + "/pkg/httpsec"}},

	// ---- auth ----
	// Có domain/application riêng. domain và application thuần (stdlib + pkg/fhir); infrastructure là nơi duy nhất dùng
	// thư viện bên thứ ba tùy ý; presentation chỉ gọi application/domain; bootstrap là composition root.
	{dir: "apps/auth/internal/domain",
		allow: []string{mod + "/apps/auth/internal/domain", mod + "/pkg/fhir"}},
	{dir: "apps/auth/internal/application",
		allow: []string{mod + "/apps/auth/internal/application", mod + "/apps/auth/internal/domain", mod + "/pkg/fhir", mod + "/pkg/fhirauth", mod + "/pkg/mailer"}},
	{dir: "apps/auth/internal/infrastructure", anyThird: true,
		allow: []string{mod + "/apps/auth/internal/infrastructure", mod + "/apps/auth/internal/application", mod + "/apps/auth/internal/domain",
			mod + "/pkg/fhir", mod + "/pkg/fhirstore", mod + "/pkg/fhirrouter", mod + "/pkg/fhirsearch", mod + "/pkg/database", mod + "/pkg/safehttp", mod + "/pkg/fhircast", mod + "/pkg/mailer"}},
	{dir: "apps/auth/internal/presentation",
		allow: []string{mod + "/apps/auth/internal/presentation", mod + "/apps/auth/internal/application", mod + "/apps/auth/internal/domain",
			mod + "/apps/auth/docs", mod + "/pkg/fhir", mod + "/pkg/httpsec", mod + "/pkg/ratelimit", mod + "/pkg/mailer"},
		third: []string{"github.com/labstack/echo/v4", "github.com/swaggo/echo-swagger", "golang.org/x/time/rate"}},
	{dir: "apps/auth/internal/config", allow: []string{mod + "/apps/auth/internal/config", mod + "/pkg/envconfig", mod + "/pkg/httpsec"}},
	{dir: "apps/auth/internal/bootstrap", anyThird: true, allow: []string{mod + "/apps/auth/", mod + "/pkg/"}},

	// ---- ws-gateway ----
	// Không có domain/application/infrastructure riêng (như fhir-server): nghiệp vụ nằm ở pkg/fhirsub và pkg/wsgroup; presentation
	// chỉ gắn handler websocket và health vào Echo; bootstrap là composition root (Redis, Hub, Feed).
	{dir: "apps/ws-gateway/internal/presentation",
		allow: []string{mod + "/apps/ws-gateway/internal/presentation", mod + "/pkg/httpsec", mod + "/pkg/fhircast"},
		third: []string{"github.com/labstack/echo/v4", "golang.org/x/time/rate"}},
	{dir: "apps/ws-gateway/internal/config", allow: []string{mod + "/apps/ws-gateway/internal/config", mod + "/pkg/envconfig"}},
	{dir: "apps/ws-gateway/internal/bootstrap", anyThird: true, allow: []string{mod + "/apps/ws-gateway/", mod + "/pkg/"}},

	// ---- dicomweb ----
	// Như ws-gateway: không có domain/application riêng. Nghiệp vụ DICOM sẽ nằm ở pkg/dicomweb (D2 trở đi); presentation chỉ gắn route, xác thực và probe vào Echo;
	// bootstrap là composition root (DB, kho Binary, Verifier).
	{dir: "apps/dicomweb/internal/presentation",
		allow: []string{mod + "/apps/dicomweb/internal/presentation", mod + "/apps/dicomweb/docs", mod + "/pkg/fhirauth", mod + "/pkg/httpsec"},
		third: []string{"github.com/labstack/echo/v4", "github.com/swaggo/echo-swagger", "golang.org/x/time/rate"}},
	{dir: "apps/dicomweb/internal/config", allow: []string{mod + "/apps/dicomweb/internal/config", mod + "/pkg/envconfig", mod + "/pkg/httpsec"}},
	{dir: "apps/dicomweb/internal/bootstrap", anyThird: true, allow: []string{mod + "/apps/dicomweb/", mod + "/pkg/"}},

	// ---- fhir-downloader ----
	// Như dicomweb: không có domain/application riêng. Nghiệp vụ tải nằm ở pkg/fhirdownload; presentation chỉ có probe (net/http, không route nghiệp vụ);
	// bootstrap là composition root (DB, kho Binary, broker Redis, worker).
	{dir: "apps/fhir-downloader/internal/presentation", allow: []string{mod + "/apps/fhir-downloader/internal/presentation", mod + "/pkg/httpsec"}},
	{dir: "apps/fhir-downloader/internal/config", allow: []string{mod + "/apps/fhir-downloader/internal/config", mod + "/pkg/envconfig"}},
	{dir: "apps/fhir-downloader/internal/bootstrap", anyThird: true, allow: []string{mod + "/apps/fhir-downloader/", mod + "/pkg/"}},

	// ---- fhir-notifier ----
	// Như fhir-downloader: không có domain/application riêng. Nghiệp vụ khớp và giao nằm ở pkg/fhirsub; presentation chỉ có probe (net/http);
	// bootstrap là composition root (DB, broker Redis, Manager).
	{dir: "apps/fhir-notifier/internal/presentation", allow: []string{mod + "/apps/fhir-notifier/internal/presentation", mod + "/pkg/httpsec"}},
	{dir: "apps/fhir-notifier/internal/config", allow: []string{mod + "/apps/fhir-notifier/internal/config", mod + "/pkg/envconfig"}},
	{dir: "apps/fhir-notifier/internal/bootstrap", anyThird: true, allow: []string{mod + "/apps/fhir-notifier/", mod + "/pkg/"}},

	// ---- fhir-cli ----
	// Client độc lập: chỉ stdlib + chính nó. Không kéo thư viện server (pkg/*) vào binary CLI.
	{dir: "apps/fhir-cli", allow: []string{mod + "/apps/fhir-cli"}},
}

// pkgRules: thư viện dùng chung không được biết app, và chỉ được phụ thuộc nhau theo hướng cho phép.
var pkgRules = map[string][]string{
	"pkg/apicontract":   {}, // sinh bảng route của api-contract.md từ swagger.json; chỉ stdlib
	"pkg/core":          {},
	"pkg/messaging":     {mod + "/pkg/core"},
	"pkg/envconfig":     {},
	"pkg/httpserve":     {},
	"pkg/safehttp":      {},
	"pkg/mailer":        {mod + "/pkg/safehttp"},                                                                                                                     // gói gốc chỉ stdlib; gói con smtp dùng safehttp.DialContext cho SMTP riêng của project; smtptest chỉ cho test
	"pkg/binarystore":   {},                                                                                                                                          // kho nhị phân + bộ ký URL: chỉ stdlib; gói con storetest chỉ cho test
	"pkg/fhirdownload":  {mod + "/pkg/binarystore", mod + "/pkg/fhir", mod + "/pkg/fhirrouter", mod + "/pkg/fhirsub", mod + "/pkg/messaging", mod + "/pkg/safehttp"}, // tự tải attachment ngoài về Binary khi nhận sự kiện ghi resource
	"pkg/fhirbinary":    {mod + "/pkg/fhir", mod + "/pkg/fhirrouter", mod + "/pkg/binarystore"},                                                                      // decorator Binary, /storage và Binary thô; không import fhirauth hay fhirstore
	"pkg/ratelimit":     {},                                                                                                                                          // token bucket theo khóa: bộ nhớ hoặc Redis dùng chung nhiều replica
	"pkg/httpsec":       {},                                                                                                                                          // middleware net/http thuần (header bảo mật, CORS); không biết framework hay app
	"pkg/database":      {mod + "/pkg/core"},
	"pkg/fhir":          {},                                                                                                                                                                                   // kiểu FHIR R4 (phần lớn là mã sinh bởi tools/fhirgen); chỉ stdlib
	"pkg/fhirpath":      {mod + "/pkg/fhir"},                                                                                                                                                                  // engine FHIRPath dùng schema FHIR để phân giải choice type
	"pkg/fhirvalidate":  {mod + "/pkg/fhir", mod + "/pkg/fhirpath"},                                                                                                                                           // validator: schema + invariant FHIRPath
	"pkg/fhirsearch":    {mod + "/pkg/fhir", mod + "/pkg/fhirpath"},                                                                                                                                           // parse + đối sánh search
	"pkg/fhirrouter":    {mod + "/pkg/fhir", mod + "/pkg/fhirpath", mod + "/pkg/fhirsearch", mod + "/pkg/fhirvalidate", mod + "/pkg/workflow"},                                                                // REST FHIR trên interface Repository
	"pkg/fhirauth":      {mod + "/pkg/fhir", mod + "/pkg/fhirrouter", mod + "/pkg/fhirsearch"},                                                                                                                // JWT và tách dữ liệu theo project
	"pkg/fhirstore":     {mod + "/pkg/fhir", mod + "/pkg/fhirsearch", mod + "/pkg/fhirrouter", mod + "/pkg/database"},                                                                                         // Repository SQL (PostgreSQL, SQLite)
	"pkg/dicomrender":   {},                                                                                                                                                                                   // dựng ảnh PNG/JPEG từ frame DICOM bằng thư viện chuẩn, không phụ thuộc gói nào trong repo
	"pkg/dicomread":     {mod + "/pkg/dicomweb"},                                                                                                                                                              // bộ đọc metadata bằng thư viện suyashkumar/dicom, cô lập thư viện ở một gói
	"pkg/dicomretrieve": {mod + "/pkg/binarystore", mod + "/pkg/dicomrender", mod + "/pkg/dicomweb", mod + "/pkg/fhir", mod + "/pkg/fhirrouter", mod + "/pkg/fhirsearch"},                                     // QIDO-RS và WADO-RS: tìm study/series/instance, metadata, tải tệp, frame, rendered
	"pkg/dicomstow":     {mod + "/pkg/binarystore", mod + "/pkg/dicomread", mod + "/pkg/dicomweb", mod + "/pkg/fhir", mod + "/pkg/fhirrouter", mod + "/pkg/fhirsearch"},                                       // STOW-RS: nhận multipart, lưu nội dung và resource
	"pkg/dicomtest":     {},                                                                                                                                                                                   // dựng tệp DICOM tổng hợp cho test, chỉ stdlib
	"pkg/dicomweb":      {mod + "/pkg/fhir"},                                                                                                                                                                  // phần thuần của DICOMweb: DICOM JSON, chuyển đổi, ánh xạ study/series, làm sạch metadata; không HTTP, không DB, không thư viện DICOM
	"pkg/fhirseed":      {mod + "/pkg/fhir", mod + "/pkg/fhirauth", mod + "/pkg/fhirrouter"},                                                                                                                  // nạp định nghĩa dựng sẵn thành resource (fhir-server seed definitions)
	"pkg/fhirsub":       {mod + "/pkg/core", mod + "/pkg/fhir", mod + "/pkg/fhirauth", mod + "/pkg/fhirrouter", mod + "/pkg/fhirsearch", mod + "/pkg/messaging", mod + "/pkg/safehttp", mod + "/pkg/wsgroup"}, // Subscription rest-hook + websocket
	"pkg/fhircast":      {mod + "/pkg/wsgroup"},                                                                                                                                                               // Hub FHIRcast: gói gốc chỉ stdlib + go-redis, gói con ws dùng wsgroup; không gọi mạng ra ngoài (xem TestFhircastNeverDialsOut)
	"pkg/testkit":       {},                                                                                                                                                                                   // testcontainers cho test tích hợp/e2e; chỉ được import từ test hoặc tests/ (xem TestTestkitOnlyUsedByTests)
	"pkg/wsgroup":       {},                                                                                                                                                                                   // nhóm kết nối WebSocket dùng chung (Origin, giới hạn, đóng êm); chỉ stdlib và coder/websocket
	"pkg/observability": {},                                                                                                                                                                                   // thư viện quan sát (OTel + echo4obs): dùng echo ở gói con adapter, không biết app
	"pkg/workflow":      {mod + "/pkg/observability"},                                                                                                                                                         // gồm cả pkg/workflow/temporal (chỉ được phụ thuộc pkg/workflow) (và pkg/observability cho asynq)
}

func importsOf(t *testing.T, dir string) map[string][]string { // file -> imports
	t.Helper()
	out := map[string][]string{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		out[path] = append(out[path], nil...) // file không import vẫn được tính là có file Go (ví dụ doc.go)
		for _, imp := range f.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			out[path] = append(out[path], p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) == 0 {
		t.Fatalf("không có file Go nào trong %s (sai đường dẫn quy tắc?)", dir)
	}
	return out
}

func isStdlib(p string) bool {
	return !strings.Contains(strings.SplitN(p, "/", 2)[0], ".") && !strings.HasPrefix(p, mod)
}

func hasPrefix(p string, prefixes []string) bool {
	for _, pre := range prefixes {
		if p == pre || strings.HasPrefix(p, strings.TrimSuffix(pre, "/")+"/") || strings.HasSuffix(pre, "/") && strings.HasPrefix(p, pre) {
			return true
		}
	}
	return false
}

func TestAppDependencyRules(t *testing.T) {
	for _, r := range rules {
		for file, imps := range importsOf(t, r.dir) {
			for _, p := range imps {
				switch {
				case isStdlib(p):
				case strings.HasPrefix(p, mod):
					if !hasPrefix(p, r.allow) {
						t.Errorf("%s import %s: không được phép (tầng %s)", file, p, r.dir)
					}
				default:
					if !r.anyThird && !hasPrefix(p, r.third) {
						t.Errorf("%s import bên thứ ba %s: không được phép (tầng %s)", file, p, r.dir)
					}
				}
			}
		}
	}
}

func TestSharedLibrariesDoNotKnowTheApp(t *testing.T) {
	for dir, allowed := range pkgRules {
		for file, imps := range importsOf(t, dir) {
			for _, p := range imps {
				if !strings.HasPrefix(p, mod) {
					continue
				}
				if strings.HasPrefix(p, mod+"/apps") {
					t.Errorf("%s import %s: thư viện dùng chung không được phụ thuộc app", file, p)
				} else if !strings.HasPrefix(p, mod+"/"+dir) && !hasPrefix(p, allowed) {
					t.Errorf("%s import %s: hướng phụ thuộc không được phép giữa các thư viện", file, p)
				}
			}
		}
	}
}

// Adapter cụ thể (gorm, redis, aws...) chỉ được dùng trong pkg/ và infrastructure, composition root.
func TestEchoAndGormStayInTheirLayers(t *testing.T) {
	forbidden := map[string][]string{
		"labstack/echo": {"pkg"},
		"gorm.io":       {"apps/fhir-server"},
	}
	for needle, dirs := range forbidden {
		for _, dir := range dirs {
			for file, imps := range importsOf(t, dir) {
				for _, p := range imps {
					if strings.Contains(p, needle) && !(needle == "labstack/echo" && strings.Contains(filepath.ToSlash(file), "pkg/observability/")) && !(needle == "gorm.io" && dir == "pkg" && strings.HasSuffix(filepath.ToSlash(file), "pkg/database/database.go")) {
						t.Errorf("%s import %s: %s không được dùng ở %s", file, p, needle, dir)
					}
				}
			}
		}
	}
}

// Mỗi apps/<tên> là MỘT ứng dụng rõ ràng: có điểm vào cmd/<tên>/main.go, internal/ riêng và README; không import app khác
// (kể cả cmd/ và bootstrap, nơi các quy tắc tầng không phủ tới); và mọi app đều có quy tắc phụ thuộc ở đầu file này.
func TestEveryAppIsAClearApplication(t *testing.T) {
	entries, err := os.ReadDir("apps")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if !e.IsDir() {
			t.Errorf("apps/%s: apps/ chỉ chứa thư mục ứng dụng", e.Name())
			continue
		}
		app := e.Name()
		for _, need := range []string{"cmd/" + app + "/main.go", "internal", "README.md"} {
			if _, err := os.Stat(filepath.Join("apps", app, need)); err != nil {
				t.Errorf("apps/%s thiếu %s", app, need)
			}
		}
		covered := false
		for _, r := range rules {
			if strings.HasPrefix(r.dir, "apps/"+app+"/") || r.dir == "apps/"+app {
				covered = true
			}
		}
		if !covered {
			t.Errorf("apps/%s chưa có quy tắc phụ thuộc trong rules", app)
		}
		for file, imps := range importsOf(t, filepath.Join("apps", app)) {
			for _, p := range imps {
				if strings.HasPrefix(p, mod+"/apps/") && !strings.HasPrefix(p, mod+"/apps/"+app+"/") {
					t.Errorf("%s import %s: app không được phụ thuộc app khác (đưa phần dùng chung vào pkg/)", file, p)
				}
			}
		}
	}
}

// Thư viện mới trong pkg/ phải khai báo quy tắc phụ thuộc, và quy tắc không được trỏ vào thư mục không còn tồn tại.
func TestEveryPkgHasARule(t *testing.T) {
	entries, err := os.ReadDir("pkg")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() {
			if _, ok := pkgRules["pkg/"+e.Name()]; !ok {
				t.Errorf("pkg/%s chưa có quy tắc trong pkgRules", e.Name())
			}
		}
	}
	for dir := range pkgRules {
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("pkgRules có %s nhưng thư mục không tồn tại", dir)
		}
	}
}

// FHIRcast chỉ có kênh websocket, KHÔNG có callback đi ra: Hub không được có đường nào để gọi ra ngoài (không SSRF).
//   - gói gốc pkg/fhircast (kiểu, kho, Hub) không được import net hay net/http;
//   - gói con (kênh websocket cần http.Handler) không được import net, và dùng net/http thì chỉ phía nhận: cấm http.Client,
//     http.Get/Post/PostForm/Head, http.DefaultClient, http.NewRequest*, http.Transport, http.DefaultTransport.
func TestFhircastNeverDialsOut(t *testing.T) {
	const root = "pkg/fhircast"
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	forbiddenHTTP := map[string]bool{"Client": true, "Get": true, "Post": true, "PostForm": true, "Head": true, "DefaultClient": true,
		"NewRequest": true, "NewRequestWithContext": true, "Transport": true, "DefaultTransport": true, "RoundTripper": true}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		inRoot := filepath.Dir(path) == root
		httpName := ""
		for _, imp := range f.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			switch {
			case p == "net":
				t.Errorf("%s import net: pkg/fhircast không được có đường gọi mạng ra ngoài", path)
			case p == "net/http" && inRoot:
				t.Errorf("%s import net/http: gói gốc pkg/fhircast không được dùng HTTP", path)
			case p == "net/http":
				httpName = "http"
				if imp.Name != nil {
					httpName = imp.Name.Name
				}
			}
		}
		if httpName != "" {
			ast.Inspect(f, func(n ast.Node) bool {
				if sel, ok := n.(*ast.SelectorExpr); ok {
					if id, ok := sel.X.(*ast.Ident); ok && id.Name == httpName && forbiddenHTTP[sel.Sel.Name] {
						t.Errorf("%s dùng http.%s: pkg/fhircast chỉ được nhận kết nối, không được gọi ra", path, sel.Sel.Name)
					}
				}
				return true
			})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = entries
}

// importsOfAll như importsOf nhưng tính cả file _test.go (thư mục tests/ gồm toàn test).
func importsOfAll(t *testing.T, dir string) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return out
	}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		out[path] = nil
		for _, imp := range f.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			out[path] = append(out[path], p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// pkg/testkit kéo theo Docker và testcontainers: chỉ test (file _test.go) và thư mục tests/ được dùng; mã chạy thật (không phải
// test) của app và của pkg khác không được import nó, để binary production không mang theo thư viện test.
func TestTestkitOnlyUsedByTests(t *testing.T) {
	for _, dir := range []string{"pkg/testkit", "pkg/mailer/smtptest", "pkg/binarystore/storetest"} {
		testHelperOnlyUsedByTests(t, dir)
	}
}

// testHelperOnlyUsedByTests: gói trợ giúp test (testkit, smtptest) chỉ được import từ file _test.go hoặc thư mục tests/.
func testHelperOnlyUsedByTests(t *testing.T, dir string) {
	kit := mod + "/" + dir
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		slash := filepath.ToSlash(path)
		if d.IsDir() {
			if strings.HasPrefix(slash, ".") && slash != "." || slash == dir || slash == "node_modules" || slash == "medplum" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(slash, ".go") || strings.HasSuffix(slash, "_test.go") {
			return nil
		}
		inTests := strings.HasPrefix(slash, "tests/") || strings.Contains(slash, "/tests/")
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range f.Imports {
			if p, _ := strconv.Unquote(imp.Path.Value); strings.HasPrefix(p, kit) && !inTests {
				t.Errorf("%s import %s: chỉ file _test.go hoặc thư mục tests/ được dùng %s", slash, p, dir)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// apps/<svc>/tests chỉ được dùng API công khai và internal/ của CHÍNH app đó (Go cho phép vì tests/ nằm dưới thư mục cha của
// internal/); không import app khác. tests/ ở gốc (e2e nhiều service) không import app nào: nó chạy image/binary, không gọi mã.
func TestTestsDirsStayInTheirApp(t *testing.T) {
	entries, err := os.ReadDir("apps")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		own := mod + "/apps/" + e.Name()
		for file, imps := range importsOfAll(t, filepath.Join("apps", e.Name(), "tests")) {
			for _, p := range imps {
				if strings.HasPrefix(p, mod+"/apps/") && !strings.HasPrefix(p, own+"/") && p != own {
					t.Errorf("%s import %s: tests/ của một app không được import app khác", file, p)
				}
			}
		}
	}
	for file, imps := range importsOfAll(t, "tests") {
		for _, p := range imps {
			if strings.HasPrefix(p, mod+"/apps/") {
				t.Errorf("%s import %s: tests/ ở gốc (e2e nhiều service) không được import mã của app nào", file, p)
			}
		}
	}
}

// Mô hình một database nhiều schema (docs/plans/microservices-shared-db.md): mã dùng chung và mã của app phải viết SQL KHÔNG gắn tên schema
// (tên bảng trần, schema do search_path của role quyết định), vì cùng một bộ migration chạy cho nhiều service. Một câu SQL gắn tên schema
// (FROM identity.users, JOIN fhir.fhir_resource...) hoặc một lời gọi `SET search_path` trong mã là cách đọc lẫn schema của service khác hoặc
// phá cách ly. Chỉ duyệt mã chạy thật (không phải _test.go), tệp migration và mọi chuỗi trong mã; script triển khai (deploy/) được phép đặt tên schema.
var qualifiedSQL = regexp.MustCompile(`(?is)\b(from|join|into|update|table|references|truncate|on)\s+(?:only\s+)?("?[a-z_][a-z0-9_]*"?)\.("?[a-z_][a-z0-9_]*"?)`)

var allowedSchemaPrefixes = []string{"information_schema", "pg_catalog", "pg_temp"}

var setSearchPath = regexp.MustCompile(`(?i)\bset\s+(local\s+)?search_path\b`)

// schemaLintFindings trả các vi phạm trong MỘT đoạn SQL/chuỗi. `ALTER ROLE ... SET search_path` và `ALTER DATABASE ...` là cách CẤU HÌNH đúng (và là
// lời khuyên trong thông báo lỗi) nên được phép; `SET search_path` trần là đổi đường tìm kiếm của phiên, không được phép.
func schemaLintFindings(src string) []string {
	var out []string
	for _, m := range qualifiedSQL.FindAllStringSubmatch(src, -1) {
		schema := strings.Trim(strings.ToLower(m[2]), `"`)
		ok := false
		for _, a := range allowedSchemaPrefixes {
			if schema == a {
				ok = true
			}
		}
		if !ok {
			out = append(out, strings.TrimSpace(m[0]))
		}
	}
	low := strings.ToLower(src)
	if setSearchPath.MatchString(src) && !strings.Contains(low, "alter role") && !strings.Contains(low, "alter database") {
		out = append(out, "SET search_path")
	}
	return out
}

// stringLiterals trả giá trị mọi chuỗi trong một tệp Go (bỏ qua chú thích và tên định danh như `fhir.Patient`, nên không báo nhầm).
func stringLiterals(t *testing.T, path string) []string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			if v, err := strconv.Unquote(lit.Value); err == nil {
				out = append(out, v)
			}
		}
		return true
	})
	return out
}

// sqlWithoutComments bỏ các dòng chú thích `--` của tệp .sql.
func sqlWithoutComments(src string) string {
	var keep []string
	for _, l := range strings.Split(src, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(l), "--") {
			keep = append(keep, l)
		}
	}
	return strings.Join(keep, "\n")
}

func TestSQLIsSchemaAgnostic(t *testing.T) {
	scan := func(root string) {
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			slash := filepath.ToSlash(path)
			ext := filepath.Ext(slash)
			if (ext != ".go" && ext != ".sql") || strings.HasSuffix(slash, "_test.go") || strings.HasPrefix(slash, "pkg/testkit/") ||
				strings.Contains(slash, "/docs/") || strings.Contains(slash, "/tests/") || strings.HasPrefix(slash, "tests/") {
				return nil
			}
			var texts []string
			if ext == ".go" {
				texts = stringLiterals(t, path)
			} else {
				b, rerr := os.ReadFile(path)
				if rerr != nil {
					return rerr
				}
				texts = []string{sqlWithoutComments(string(b))}
			}
			for _, txt := range texts {
				for _, f := range schemaLintFindings(txt) {
					t.Errorf("%s: SQL gắn tên schema hoặc đổi search_path (%q): dùng tên bảng trần, schema do role quyết định", slash, f)
				}
			}
			return nil
		})
	}
	scan("pkg")
	scan("apps")
}

// Chính bộ lint phải bắt được vi phạm (tránh "xanh vì regex không khớp gì").
func TestSchemaLintDetectsViolations(t *testing.T) {
	for _, bad := range []string{
		`SELECT * FROM identity.fhir_resource`, `select 1 from fhir.fhir_resource r join identity.users u on r.id = u.id`,
		`INSERT INTO shared.audit VALUES (1)`, `UPDATE fhir.fhir_resource SET x = 1`, `CREATE TABLE identity.x (id int)`,
		`ALTER TABLE ONLY fhir.fhir_history ADD COLUMN y int`, `CREATE INDEX i ON fhir.fhir_resource (id)`, `TRUNCATE public.fhir_resource`,
		`REFERENCES identity.users(id)`, `FROM "identity"."fhir_resource"`, `SET search_path = fhir`, `set local search_path to fhir, public`,
		"SELECT *\nFROM\n  identity.users",
	} {
		if len(schemaLintFindings(bad)) == 0 {
			t.Errorf("lint không bắt được: %q", bad)
		}
	}
	for _, good := range []string{
		`SELECT * FROM fhir_resource`, `INSERT INTO fhir_history (id) VALUES (1)`, `CREATE INDEX i ON fhir_resource (id)`,
		`SELECT table_name FROM information_schema.tables`, `SELECT * FROM pg_catalog.pg_tables`, `SHOW search_path`,
		`ALTER ROLE app SET search_path = identity`, `đặt ALTER ROLE <role> SET search_path = <schema>`, `ALTER DATABASE d SET search_path TO x`,
		`// Dữ liệu từ http://hl7.org/fhir.html và "fhir.write" scope`, `x := fhir.Patient{}`, `the update.of rows`,
	} {
		if fs := schemaLintFindings(good); len(fs) != 0 {
			t.Errorf("lint báo nhầm %q: %v", good, fs)
		}
	}
}
