package bootstrap_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"trivita-fhir/apps/fhir-server/internal/config"
)

// Job (export, batch async, reindex) có chủ sở hữu là người tạo: chỉ người đó hoặc admin xem trạng thái, hủy, xóa. Người khác cùng project
// nhận 404 như job không tồn tại. Đọc AsyncJob trực tiếp qua REST chỉ cho admin, nếu không thì đi vòng được cả kiểm chủ sở hữu.

func callWith(t *testing.T, srv *httptest.Server, method, path, token string, headers ...string) resp {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := res.Body.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return resp{res.StatusCode, res.Header, []byte(sb.String())}
}

func TestJobsBelongToTheirCreator(t *testing.T) {
	_, srv := newServer(t, jwtSettings())
	as := func(sub, scope string) string {
		return tok(t, "A", scope, func(c jwt.MapClaims) { c["sub"] = sub })
	}
	alice, bob, admin := as("alice", "fhir.read"), as("bob", "fhir.read"), as("root", "fhir.admin")
	other := tok(t, "B", "fhir.read", func(c jwt.MapClaims) { c["sub"] = "alice" }) // cùng sub nhưng project khác

	acc := callWith(t, srv, "GET", "/fhir/R4/$export", alice, "Prefer", "respond-async")
	if acc.status != 202 {
		t.Fatalf("export: %d %s", acc.status, acc.body)
	}
	bulk := strings.TrimPrefix(acc.header.Get("Content-Location"), "http://fhir.test")
	id := bulk[strings.Index(bulk, "bulkdata/")+len("bulkdata/") : strings.LastIndex(bulk, "/status")]
	job := "/fhir/R4/job/" + id + "/status"

	// chờ job xong để cả nhánh "completed" (có danh sách file) cũng bị kiểm
	for i := 0; i < 300; i++ {
		if callWith(t, srv, "GET", bulk, alice).status != 202 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	for _, p := range []string{bulk, job} {
		want(t, callWith(t, srv, "GET", p, alice), 200)
		if r := callWith(t, srv, "GET", p, bob); r.status != 404 {
			t.Errorf("người khác GET %s: %d, muốn 404 (%s)", p, r.status, r.body)
		}
		if r := callWith(t, srv, "GET", p, other); r.status != 404 {
			t.Errorf("project khác GET %s: %d, muốn 404", p, r.status)
		}
		if r := callWith(t, srv, "GET", p, admin); r.status != 200 {
			t.Errorf("admin GET %s: %d, muốn 200", p, r.status)
		}
	}
	// hủy/xóa: người khác không được và job không bị đụng tới
	for _, p := range []string{bulk, job} {
		if r := callWith(t, srv, "DELETE", p, bob); r.status != 404 && r.status != 403 {
			t.Errorf("người khác DELETE %s: %d, muốn 404 (hoặc 403 do route job cần quyền write)", p, r.status)
		}
	}
	want(t, callWith(t, srv, "GET", bulk, alice), 200) // vẫn còn nguyên

	// đi vòng qua REST: đọc, tìm kiếm AsyncJob chỉ cho admin
	want(t, callWith(t, srv, "GET", "/fhir/R4/AsyncJob/"+id, bob), 403)
	want(t, callWith(t, srv, "GET", "/fhir/R4/AsyncJob", bob), 403)
	want(t, callWith(t, srv, "GET", "/fhir/R4/AsyncJob/"+id, alice), 403)
	want(t, callWith(t, srv, "GET", "/fhir/R4/AsyncJob/"+id, admin), 200)

	// tìm kiếm cấp hệ thống theo _type cũng không liệt kê được job của người khác
	if r := callWith(t, srv, "GET", "/fhir/R4?_type=AsyncJob", bob); r.status == 200 && strings.Contains(string(r.body), id) {
		t.Errorf("tìm kiếm hệ thống lộ job của người khác: %s", r.body)
	}

	// GraphQL và lịch sử cấp hệ thống không phải đường vòng
	for _, p := range []string{"/fhir/R4/_history", "/fhir/R4/AsyncJob/" + id + "/_history"} {
		if r := callWith(t, srv, "GET", p, bob); strings.Contains(string(r.body), id) {
			t.Errorf("GET %s lộ job của người khác: %d %s", p, r.status, r.body)
		}
	}
	if r := call(t, srv, "POST", "/fhir/R4/$graphql", bob, `{"query":"{ AsyncJobList { id status } }"}`); strings.Contains(string(r.body), id) {
		t.Errorf("GraphQL lộ job của người khác: %d %s", r.status, r.body)
	}

	// chủ sở hữu hủy được; admin xóa được job của người khác
	want(t, callWith(t, srv, "DELETE", bulk, alice), 202)
	acc2 := callWith(t, srv, "GET", "/fhir/R4/$export", bob, "Prefer", "respond-async")
	bulk2 := strings.TrimPrefix(acc2.header.Get("Content-Location"), "http://fhir.test")
	want(t, callWith(t, srv, "DELETE", bulk2, admin), 202)
}

// File kết quả của job là Binary đọc được bằng id: chỉ người tạo job hoặc admin được đọc, tìm, sửa, xóa (404 với người khác).
func TestJobResultFilesBelongToTheJobCreator(t *testing.T) {
	t.Run("kho trong CSDL", func(t *testing.T) { jobResultFilesBelongToCreator(t, jwtSettings()) })
	t.Run("kho Binary ngoài", func(t *testing.T) {
		mut, _ := storageSettings(t)
		jobResultFilesBelongToCreator(t, jwtSettings(mut))
	})
}

func jobResultFilesBelongToCreator(t *testing.T, s config.Settings) {
	_, srv := newServer(t, s)
	as := func(sub, scope string) string {
		return tok(t, "A", scope, func(c jwt.MapClaims) { c["sub"] = sub })
	}
	alice, bob, admin, writer := as("alice", "fhir.read"), as("bob", "fhir.read"), as("root", "fhir.admin"), as("writer", "fhir.write")
	want(t, call(t, srv, "POST", "/fhir/R4/Patient", writer, patient("Export")), 201)

	acc := callWith(t, srv, "GET", "/fhir/R4/$export?_type=Patient", alice, "Prefer", "respond-async")
	if acc.status != 202 {
		t.Fatalf("export: %d %s", acc.status, acc.body)
	}
	bulk := strings.TrimPrefix(acc.header.Get("Content-Location"), "http://fhir.test")
	var st resp
	for i := 0; i < 300; i++ {
		if st = callWith(t, srv, "GET", bulk, alice); st.status != 202 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	out, _ := st.json(t)["output"].([]any)
	if st.status != 200 || len(out) == 0 {
		t.Fatalf("export chưa xong: %d %s", st.status, st.body)
	}
	file := strings.TrimPrefix(out[0].(map[string]any)["url"].(string), "http://fhir.test")
	id := file[strings.LastIndex(file, "/")+1:]

	if r := callWith(t, srv, "GET", file, alice); r.status != 200 || !looksLikeExportFile(r.body) {
		t.Fatalf("chủ sở hữu phải đọc được file: %d %s", r.status, r.body)
	}
	if r := callWith(t, srv, "GET", file, admin); r.status != 200 {
		t.Errorf("admin đọc file: %d", r.status)
	}
	for _, c := range []struct{ name, method, path string }{
		{"đọc", "GET", file},
		{"đọc thô", "GET", file + "?_format=raw"},
		{"đọc thô theo Accept", "GET", file},
		{"lịch sử", "GET", file + "/_history"},
		{"vread", "GET", file + "/_history/x"},
		{"tìm Binary", "GET", "/fhir/R4/Binary"},
		{"tìm hệ thống", "GET", "/fhir/R4?_type=Binary"},
	} {
		for _, accept := range []string{"application/fhir+json", "application/fhir+ndjson", "*/*"} {
			r := callWith(t, srv, c.method, c.path, bob, "Accept", accept)
			if strings.Contains(string(r.body), id) || strings.Contains(string(r.body), "ndjson") || strings.Contains(string(r.body), `"resourceType":"Patient"`) {
				t.Errorf("%s (Accept %s): người khác thấy file kết quả: %d %.200s", c.name, accept, r.status, r.body)
			}
		}
	}
	if r := callWith(t, srv, "GET", file, bob); r.status != 404 {
		t.Errorf("bob đọc file: %d, muốn 404", r.status)
	}
	// ghi vào file của người khác (kể cả khi có quyền write): không đọc được nên không sửa, không xóa được
	w2 := as("mallory", "fhir.write")
	if r := call(t, srv, "PUT", file, w2, map[string]any{"resourceType": "Binary", "id": id, "contentType": "text/plain", "data": "eA=="}); r.status != 404 {
		t.Errorf("mallory PUT file: %d, muốn 404 (%s)", r.status, r.body)
	}
	if r := call(t, srv, "DELETE", file, w2, nil); r.status != 404 {
		t.Errorf("mallory DELETE file: %d, muốn 404", r.status)
	}
	if r := callWith(t, srv, "GET", file, alice); r.status != 200 || !looksLikeExportFile(r.body) {
		t.Errorf("file bị đổi: %d %s", r.status, r.body)
	}
	// Binary thường (không phải kết quả job) không bị ảnh hưởng
	made := want(t, call(t, srv, "POST", "/fhir/R4/Binary", writer, map[string]any{"resourceType": "Binary", "contentType": "text/plain", "data": "aGk="}), 201)
	want(t, callWith(t, srv, "GET", "/fhir/R4/Binary/"+made.str(t, "id"), bob), 200)
}

// looksLikeExportFile: thân là Binary JSON (contentType ndjson) khi kho trong CSDL, hoặc chính nội dung ndjson khi bật kho ngoài.
func looksLikeExportFile(b []byte) bool {
	return strings.Contains(string(b), "ndjson") || strings.Contains(string(b), `"resourceType":"Patient"`)
}

// Chủ sở hữu job (chỉ quyền read) xóa job đã xong thì file kết quả cũng bị xóa hẳn: không để lại Binary mà không ai dọn được.
func TestOwnerDeletingFinishedExportRemovesItsFiles(t *testing.T) {
	for name, s := range map[string]func(*testing.T) config.Settings{
		"kho trong CSDL": func(t *testing.T) config.Settings { return jwtSettings() },
		"kho Binary ngoài": func(t *testing.T) config.Settings {
			mut, _ := storageSettings(t)
			return jwtSettings(mut)
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, srv := newServer(t, s(t))
			as := func(sub, scope string) string { return tok(t, "A", scope, func(c jwt.MapClaims) { c["sub"] = sub }) }
			alice, admin, writer := as("alice", "fhir.read"), as("root", "fhir.admin"), as("writer", "fhir.write")
			want(t, call(t, srv, "POST", "/fhir/R4/Patient", writer, patient("Cleanup")), 201)
			acc := callWith(t, srv, "GET", "/fhir/R4/$export?_type=Patient", alice, "Prefer", "respond-async")
			bulk := strings.TrimPrefix(acc.header.Get("Content-Location"), "http://fhir.test")
			var st resp
			for i := 0; i < 300; i++ {
				if st = callWith(t, srv, "GET", bulk, alice); st.status != 202 {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			out, _ := st.json(t)["output"].([]any)
			if st.status != 200 || len(out) == 0 {
				t.Fatalf("export chưa xong: %d %s", st.status, st.body)
			}
			file := strings.TrimPrefix(out[0].(map[string]any)["url"].(string), "http://fhir.test")
			want(t, callWith(t, srv, "GET", file, admin), 200)
			want(t, callWith(t, srv, "DELETE", bulk, alice), 202) // job đã xong: xóa job và các file
			if r := callWith(t, srv, "GET", file, admin); r.status != 404 && r.status != 410 {
				t.Errorf("file kết quả còn sau khi chủ xóa job: %d %.120s", r.status, r.body)
			}
			if r := callWith(t, srv, "GET", "/fhir/R4/AsyncJob?_total=accurate&_count=0", admin); r.status == 200 && !strings.Contains(string(r.body), `"total":0`) {
				t.Errorf("job còn sau khi xóa: %s", r.body)
			}
		})
	}
}

// Job hết hạn bị Expunge khỏi kho SQL: không còn dòng nào trong fhir_resource lẫn fhir_history (xóa mềm giữ lại mọi phiên bản của job).
func TestExpiredJobIsExpungedFromTheStoreWithItsHistory(t *testing.T) {
	c, srv := newServer(t, jwtSettings(func(s *config.Settings) { s.ExportRetention = 50 * time.Millisecond }))
	alice := tok(t, "A", "fhir.read", func(cl jwt.MapClaims) { cl["sub"] = "alice" })
	writer := tok(t, "A", "fhir.write")
	want(t, call(t, srv, "POST", "/fhir/R4/Patient", writer, patient("Expire")), 201)
	acc := callWith(t, srv, "GET", "/fhir/R4/$export?_type=Patient", alice, "Prefer", "respond-async")
	bulk := strings.TrimPrefix(acc.header.Get("Content-Location"), "http://fhir.test")
	id := bulk[strings.Index(bulk, "bulkdata/")+len("bulkdata/") : strings.LastIndex(bulk, "/status")]
	for i := 0; i < 300; i++ {
		if callWith(t, srv, "GET", bulk, alice).status != 202 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	ctx := context.Background()
	hist, err := c.Store.ReadHistory(ctx, "AsyncJob", id)
	if err != nil || len(hist) < 2 {
		t.Fatalf("trước khi hết hạn job phải có nhiều phiên bản trong lịch sử: %d, %v", len(hist), err)
	}
	time.Sleep(120 * time.Millisecond)
	if n, err := c.Router.ExpireJobs(ctx); err != nil || n != 1 {
		t.Fatalf("ExpireJobs = %d, %v", n, err)
	}
	if h, err := c.Store.ReadHistory(ctx, "AsyncJob", id); err == nil && len(h) > 0 {
		t.Errorf("lịch sử AsyncJob còn %d phiên bản sau khi hết hạn (xóa mềm thay vì Expunge)", len(h))
	}
	if _, err := c.Store.ReadResource(ctx, "AsyncJob", id); err == nil {
		t.Error("job còn đọc được")
	}
	if r := callWith(t, srv, "GET", bulk, alice); r.status != 404 {
		t.Errorf("trạng thái của export hết hạn: %d, muốn 404", r.status)
	}
}
