package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"trivita-fhir/apps/fhir-cli/internal/cli"
	"trivita-fhir/pkg/fhirrouter"
)

const token = "secret-token-0123456789"

type server struct {
	srv       *httptest.Server
	unauth    atomic.Int64
	otherHost atomic.Int64
}

// newServer dựng fhirrouter (kho bộ nhớ) sau một lớp kiểm bearer token và (tùy chọn) header X-Fhir-Project.
func newServer(t *testing.T) *server { return newServerWith(t, fhirrouter.Options{}) }

func newServerWith(t *testing.T, opts fhirrouter.Options) *server {
	t.Helper()
	s := &server{}
	mux := http.NewServeMux()
	var router *fhirrouter.Router
	mux.HandleFunc("/fhir/R4/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			s.unauth.Add(1)
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		http.StripPrefix("/fhir/R4", router.HTTPHandler()).ServeHTTP(w, r)
	})
	s.srv = httptest.NewServer(mux)
	opts.BaseURL = "http://fhir.example.invalid/fhir/R4"
	router = fhirrouter.New(fhirrouter.NewMemoryRepository(), opts)
	t.Cleanup(func() { s.srv.Close(); _ = router.Shutdown(context.Background()) })
	return s
}

type result struct {
	code        int
	out, errOut string
}

func (s *server) run(t *testing.T, stdin string, args ...string) result {
	t.Helper()
	var out, errOut bytes.Buffer
	full := append([]string{"-base", s.srv.URL + "/fhir/R4", "-token", token}, args...)
	code := cli.Run(context.Background(), full, cli.Env{
		Stdin: strings.NewReader(stdin), Stdout: &out, Stderr: &errOut,
		Getenv: func(string) string { return "" }, PollInterval: 5 * time.Millisecond,
	})
	return result{code, out.String(), errOut.String()}
}

func (r result) ok(t *testing.T) result {
	t.Helper()
	if r.code != 0 {
		t.Fatalf("mã thoát %d\nstdout: %s\nstderr: %s", r.code, r.out, r.errOut)
	}
	return r
}

func (r result) fails(t *testing.T, code int, contains string) {
	t.Helper()
	if r.code != code || !strings.Contains(r.errOut, contains) {
		t.Fatalf("muốn mã %d và stderr chứa %q, được mã %d\nstdout: %s\nstderr: %s", code, contains, r.code, r.out, r.errOut)
	}
}

// patient tạo một Patient bằng lệnh create và trả id do máy chủ cấp (máy chủ chưa hỗ trợ update-as-create nên id do server quyết định).
func (s *server) patient(t *testing.T, family string) string {
	t.Helper()
	out := s.run(t, `{"resourceType":"Patient","name":[{"family":"`+family+`"}]}`, "create", "Patient", "-f", "-").ok(t).out
	return decode(t, out)["id"].(string)
}

// count đếm entry của Bundle kết quả search (Bundle rỗng không có trường entry).
func count(t *testing.T, out string) int {
	t.Helper()
	e, _ := decode(t, out)["entry"].([]any)
	return len(e)
}

func decode(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("không phải JSON: %s", s)
	}
	return m
}

func TestCRUDCommands(t *testing.T) {
	s := newServer(t)
	created := decode(t, s.run(t, `{"resourceType":"Patient","name":[{"family":"Smith"}]}`, "create", "Patient", "-f", "-").ok(t).out)
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("create: %v", created)
	}
	got := decode(t, s.run(t, "", "read", "Patient/"+id).ok(t).out)
	if got["id"] != id {
		t.Fatalf("read: %v", got)
	}
	// cờ đứng sau tham số vị trí
	upd := `{"resourceType":"Patient","id":"` + id + `","name":[{"family":"Jones"}]}`
	s.run(t, upd, "update", "Patient/"+id, "-f", "-").ok(t)
	v := decode(t, s.run(t, "", "read", "Patient/"+id).ok(t).out)
	vid := v["meta"].(map[string]any)["versionId"].(string)
	if decode(t, s.run(t, "", "read", "-version", vid, "Patient/"+id).ok(t).out)["name"].([]any)[0].(map[string]any)["family"] != "Jones" {
		t.Error("read -version")
	}
	// từ tệp
	f := filepath.Join(t.TempDir(), "p.json")
	_ = os.WriteFile(f, []byte(`{"resourceType":"Patient","name":[{"family":"File"}]}`), 0o644)
	s.run(t, "", "create", "Patient", "-f", f).ok(t)

	bundle := decode(t, s.run(t, "", "search", "Patient", "family=jones").ok(t).out)
	if len(bundle["entry"].([]any)) != 1 {
		t.Fatalf("search: %v", bundle)
	}
	s.run(t, "", "delete", "Patient/"+id).ok(t)
	s.run(t, "", "read", "Patient/"+id).fails(t, 1, "410") // đã xóa
	s.run(t, "", "read", "Patient/missing").fails(t, 1, "404")
}

func TestSearchAllFollowsPagesAndStaysOnServer(t *testing.T) {
	s := newServer(t)
	var lines []string
	for _, fam := range []string{"A", "B", "C", "D", "E", "F", "G"} {
		lines = append(lines, `{"resourceType":"Patient","name":[{"family":"`+fam+`"}]}`)
	}
	s.run(t, strings.Join(lines, "\n"), "import", "-f", "-").ok(t)
	out := s.run(t, "", "search", "Patient", "_count=3&_sort=family", "-all", "-ndjson").ok(t).out
	var fams []string
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		fams = append(fams, decode(t, l)["name"].([]any)[0].(map[string]any)["family"].(string))
	}
	if strings.Join(fams, ",") != "A,B,C,D,E,F,G" {
		t.Fatalf("families = %v", fams)
	}
	if n := s.unauth.Load(); n != 0 {
		t.Errorf("%d yêu cầu thiếu token (link next phải giữ token và đi qua cùng máy chủ)", n)
	}
	// -ndjson không -all: một trang; thiếu -ndjson với -all là lỗi cách dùng
	one := s.run(t, "", "search", "Patient", "_count=3&_sort=family", "-ndjson").ok(t).out
	if len(strings.Split(strings.TrimSpace(one), "\n")) != 3 {
		t.Errorf("một trang: %s", one)
	}
	s.run(t, "", "search", "Patient", "-all").fails(t, 2, "-all cần -ndjson")
}

func TestImportNDJSONBundleAndErrors(t *testing.T) {
	s := newServer(t)
	existing := s.patient(t, "Existing")
	nd := strings.Join([]string{
		`{"resourceType":"Patient","id":"` + existing + `","name":[{"family":"Updated"}]}`, // có id: PUT cập nhật resource đã có
		``,
		`{"resourceType":"Patient","name":[{"family":"NoId"}]}`, // không id: POST tạo mới
		`{"resourceType":"Observation","status":"final","code":{"text":"x"}}`,
	}, "\n")
	r := s.run(t, nd, "import", "-f", "-", "-size", "2").ok(t)
	if !strings.Contains(r.out, "đã nạp 3, lỗi 0, tổng 3") {
		t.Fatalf("out: %s", r.out)
	}
	if got := decode(t, s.run(t, "", "read", "Patient/"+existing).ok(t).out); got["name"].([]any)[0].(map[string]any)["family"] != "Updated" {
		t.Fatalf("PUT không cập nhật: %v", got)
	}

	// resource sai bị báo theo mục, mục tốt vẫn được nạp (batch) và mã thoát khác 0
	bad := `{"resourceType":"Patient","name":[{"family":"Good"}]}` + "\n" + `{"resourceType":"Patient","active":"yes"}`
	rr := s.run(t, bad, "import", "-f", "-")
	if rr.code != 1 || !strings.Contains(rr.errOut, "mục 2 (Patient)") || !strings.Contains(rr.out, "đã nạp 1, lỗi 1") {
		t.Fatalf("batch lỗi: %+v", rr)
	}
	if n := count(t, s.run(t, "", "search", "Patient", "family=good").ok(t).out); n != 1 {
		t.Fatalf("mục tốt phải được nạp: %d", n)
	}
	// PUT tới id chưa có: máy chủ chưa hỗ trợ update-as-create nên là lỗi theo mục
	rr = s.run(t, `{"resourceType":"Patient","id":"khong-co"}`, "import", "-f", "-")
	if rr.code != 1 || !strings.Contains(rr.errOut, "404") {
		t.Fatalf("PUT id chưa có: %+v", rr)
	}

	// transaction: một mục sai làm hỏng cả Bundle
	tx := `{"resourceType":"Patient","name":[{"family":"TxOnly"}]}` + "\n" + `{"resourceType":"Patient","active":"yes"}`
	s.run(t, tx, "import", "-f", "-", "-mode", "transaction").fails(t, 1, "gửi Bundle")
	if n := count(t, s.run(t, "", "search", "Patient", "family=txonly").ok(t).out); n != 0 {
		t.Fatalf("transaction lỗi phải hoàn tác: %d", n)
	}

	// đầu vào là Bundle
	b := `{"resourceType":"Bundle","type":"collection","entry":[{"resource":{"resourceType":"Patient","name":[{"family":"FromBundle"}]}}]}`
	s.run(t, b, "import", "-f", "-").ok(t)
	if n := count(t, s.run(t, "", "search", "Patient", "family=frombundle").ok(t).out); n != 1 {
		t.Fatalf("Bundle: %d", n)
	}

	s.run(t, "không phải json\n", "import", "-f", "-").fails(t, 1, "dòng 1")
	s.run(t, "", "import", "-f", "-").fails(t, 1, "rỗng")
	s.run(t, "{}", "import", "-f", "-").fails(t, 1, "resourceType")
	s.run(t, "", "import").fails(t, 2, "-f")
	s.run(t, "", "import", "-f", "-", "-mode", "x").fails(t, 2, "cách dùng")
}

func TestExportDownloadsFilesAndCleansUp(t *testing.T) {
	s := newServer(t)
	e1, _ := s.patient(t, "E1"), s.patient(t, "E2")
	s.run(t, `{"resourceType":"Observation","status":"final","code":{"text":"x"},"subject":{"reference":"Patient/`+e1+`"}}`, "import", "-f", "-").ok(t)
	dir := filepath.Join(t.TempDir(), "out")

	r := s.run(t, "", "export", "-o", dir, "-type", "Patient,Observation").ok(t)
	if !strings.Contains(r.out, "Patient-1.ndjson") || !strings.Contains(r.out, "Observation-1.ndjson") {
		t.Fatalf("out: %s", r.out)
	}
	data, err := os.ReadFile(filepath.Join(dir, "Patient-1.ndjson"))
	if err != nil || len(strings.Split(strings.TrimSpace(string(data)), "\n")) != 2 {
		t.Fatalf("Patient-1.ndjson: %q %v", data, err)
	}
	// job và file Binary đã được dọn trên máy chủ
	if n := count(t, s.run(t, "", "search", "Binary").ok(t).out); n != 0 {
		t.Errorf("còn %d Binary sau export", n)
	}
	// -keep giữ lại file trên máy chủ
	s.run(t, "", "export", "-o", filepath.Join(t.TempDir(), "o2"), "-type", "Patient", "-keep").ok(t)
	if decode(t, s.run(t, "", "search", "Binary").ok(t).out)["entry"] == nil {
		t.Error("-keep phải giữ Binary")
	}
	// mức group chỉ xuất thành viên
	g := `{"resourceType":"Group","type":"person","actual":true,"member":[{"entity":{"reference":"Patient/` + e1 + `"}}]}`
	gid := decode(t, s.run(t, g, "create", "Group", "-f", "-").ok(t).out)["id"].(string)
	gdir := filepath.Join(t.TempDir(), "g")
	s.run(t, "", "export", "-o", gdir, "-level", "group/"+gid, "-type", "Patient").ok(t)
	gd, _ := os.ReadFile(filepath.Join(gdir, "Patient-1.ndjson"))
	if strings.Count(string(gd), "\n") != 1 || !strings.Contains(string(gd), e1) {
		t.Errorf("group export: %q", gd)
	}
	s.run(t, "", "export", "-o", t.TempDir(), "-level", "patient", "-type", "Patient").ok(t)
	s.run(t, "", "export").fails(t, 2, "cách dùng")
	s.run(t, "", "export", "-o", dir, "-level", "nope").fails(t, 2, "-level")
	s.run(t, "", "export", "-o", dir, "-level", "group/missing").fails(t, 1, "404")
}

func TestReindexWaitsForJob(t *testing.T) {
	s := newServer(t)
	id := s.patient(t, "R1")
	r := s.run(t, "", "reindex", "Patient").ok(t)
	if !strings.Contains(r.errOut, "reindex xong") {
		t.Fatalf("stderr: %s", r.errOut)
	}
	s.run(t, "", "reindex", "Patient", id).ok(t)
	s.run(t, "", "reindex", "Patient/"+id).fails(t, 2, "cách dùng")
}

func TestUsageAndConnectionErrors(t *testing.T) {
	s := newServer(t)
	s.run(t, "", "nope").fails(t, 2, "lệnh không biết")
	s.run(t, "", "read", "Patient").fails(t, 2, "Type/id")
	s.run(t, "", "create", "Patient/1", "-f", "-").fails(t, 2, "tên loại")
	s.run(t, "", "create", "Patient").fails(t, 2, "-f")
	s.run(t, "not json", "create", "Patient", "-f", "-").fails(t, 1, "JSON")
	s.run(t, "", "update", "Patient", "-f", "-").fails(t, 2, "Type/id")

	var out, errOut bytes.Buffer
	if code := cli.Run(context.Background(), []string{"-base", "not-a-url", "read", "Patient/1"}, cli.Env{Stdout: &out, Stderr: &errOut, Getenv: func(string) string { return "" }}); code != 2 {
		t.Errorf("URL sai: %d %s", code, errOut.String())
	}
	if code := cli.Run(context.Background(), nil, cli.Env{Stdout: &out, Stderr: &errOut, Getenv: func(string) string { return "" }}); code != 2 {
		t.Errorf("không lệnh: %d", code)
	}
	if code := cli.Run(context.Background(), []string{"help"}, cli.Env{Stdout: &out, Stderr: &errOut, Getenv: func(string) string { return "" }}); code != 0 || !strings.Contains(errOut.String(), "Lệnh:") {
		t.Errorf("help: %d", code)
	}
	// sai token: 401 và thông điệp lỗi của máy chủ
	var o2, e2 bytes.Buffer
	code := cli.Run(context.Background(), []string{"-base", s.srv.URL + "/fhir/R4", "-token", "sai", "read", "Patient/1"}, cli.Env{Stdout: &o2, Stderr: &e2, Getenv: func(string) string { return "" }})
	if code != 1 || !strings.Contains(e2.String(), "401") {
		t.Errorf("sai token: %d %s", code, e2.String())
	}
	// cấu hình lấy từ biến môi trường
	var o3, e3 bytes.Buffer
	env := map[string]string{"FHIR_BASE_URL": s.srv.URL + "/fhir/R4", "FHIR_TOKEN": token}
	code = cli.Run(context.Background(), []string{"search", "Patient", "-ndjson"}, cli.Env{Stdout: &o3, Stderr: &e3, Getenv: func(k string) string { return env[k] }})
	if code != 0 {
		t.Errorf("biến môi trường: %d %s", code, e3.String())
	}
}

// Chuyển dữ liệu giữa hai máy chủ giữ nguyên id (và nhờ vậy giữ nguyên tham chiếu) khi máy chủ đích bật update-as-create.
func TestExportThenImportKeepsIDsWhenUpdateAsCreateIsOn(t *testing.T) {
	src := newServer(t)
	pid := src.patient(t, "Roundtrip")
	src.run(t, `{"resourceType":"Observation","status":"final","code":{"text":"x"},"subject":{"reference":"Patient/`+pid+`"}}`, "import", "-f", "-").ok(t)
	dir := filepath.Join(t.TempDir(), "dump")
	src.run(t, "", "export", "-o", dir, "-type", "Patient,Observation").ok(t)

	// đích mặc định (update-as-create tắt): resource có id chưa tồn tại bị từ chối theo mục
	off := newServer(t)
	rr := off.run(t, "", "import", "-f", filepath.Join(dir, "Patient-1.ndjson"))
	if rr.code != 1 || !strings.Contains(rr.errOut, "404") {
		t.Fatalf("đích tắt update-as-create: %+v", rr)
	}

	dst := newServerWith(t, fhirrouter.Options{UpdateAsCreate: true})
	dst.run(t, "", "import", "-f", filepath.Join(dir, "Patient-1.ndjson")).ok(t)
	dst.run(t, "", "import", "-f", filepath.Join(dir, "Observation-1.ndjson")).ok(t)
	got := decode(t, dst.run(t, "", "read", "Patient/"+pid).ok(t).out)
	if got["id"] != pid {
		t.Fatalf("id đổi: %v", got)
	}
	obs := decode(t, dst.run(t, "", "search", "Observation", "subject=Patient/"+pid).ok(t).out)
	if n, _ := obs["entry"].([]any); len(n) != 1 {
		t.Fatalf("tham chiếu không còn khớp: %v", obs)
	}
	// nạp lại lần nữa là cập nhật, không lỗi
	dst.run(t, "", "import", "-f", filepath.Join(dir, "Patient-1.ndjson")).ok(t)
}

func TestExportWithTypeFilter(t *testing.T) {
	s := newServer(t)
	s.run(t, `{"resourceType":"Patient","active":true,"name":[{"family":"On"}]}
{"resourceType":"Patient","active":false,"name":[{"family":"Off"}]}`, "import", "-f", "-").ok(t)
	dir := filepath.Join(t.TempDir(), "f")
	r := s.run(t, "", "export", "-o", dir, "-filter", "Patient?active=true").ok(t)
	if !strings.Contains(r.out, "1 resource") {
		t.Fatalf("out: %s", r.out)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "Patient-1.ndjson"))
	if !strings.Contains(string(data), "On") || strings.Contains(string(data), "Off") {
		t.Errorf("lọc sai: %s", data)
	}
	s.run(t, "", "export", "-o", dir, "-filter", "Patient").fails(t, 1, "_typeFilter")
}
