package bootstrap_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
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
