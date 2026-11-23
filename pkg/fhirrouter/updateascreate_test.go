package fhirrouter_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"trivita-fhir/pkg/fhirrouter"
)

func putTo(h http.Handler, method, path, body string, headers ...string) (int, string) {
	req := httptest.NewRequest(method, "/"+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/fhir+json")
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	raw, _ := io.ReadAll(w.Result().Body)
	return w.Code, string(raw)
}

func TestUpdateAsCreateIsOffByDefault(t *testing.T) {
	r := fhirrouter.New(fhirrouter.NewMemoryRepository(), fhirrouter.Options{})
	h := r.HTTPHandler()
	if code, body := putTo(h, "PUT", "Patient/abc", `{"resourceType":"Patient","id":"abc"}`); code != 404 {
		t.Fatalf("mặc định phải 404: %d %s", code, body)
	}
}

func TestUpdateAsCreate(t *testing.T) {
	r := fhirrouter.New(fhirrouter.NewMemoryRepository(), fhirrouter.Options{UpdateAsCreate: true})
	h := r.HTTPHandler()
	put := func(id, body string, hdr ...string) (int, string) {
		return putTo(h, "PUT", "Patient/"+id, body, hdr...)
	}

	code, body := put("my-id.1", `{"resourceType":"Patient","id":"my-id.1","name":[{"family":"Chosen"}]}`)
	if code != 201 || !strings.Contains(body, `"id":"my-id.1"`) {
		t.Fatalf("tạo mới: %d %s", code, body)
	}
	if code, _ := putTo(h, "GET", "Patient/my-id.1", ""); code != 200 {
		t.Fatalf("đọc lại: %d", code)
	}
	// lần thứ hai là cập nhật thường
	if code, body := put("my-id.1", `{"resourceType":"Patient","id":"my-id.1","name":[{"family":"Changed"}]}`); code != 200 || !strings.Contains(body, "Changed") {
		t.Fatalf("cập nhật: %d %s", code, body)
	}
	// body không có id vẫn tạo được
	if code, _ := put("noid", `{"resourceType":"Patient"}`); code != 201 {
		t.Fatalf("body không id: %d", code)
	}
	// id không hợp lệ
	for _, id := range []string{"has_underscore", "a%20b", strings.Repeat("a", 65)} {
		if code, _ := put(id, `{"resourceType":"Patient"}`); code != 400 {
			t.Errorf("id %q: %d, muốn 400", id, code)
		}
	}
	// If-Match yêu cầu phiên bản đang có nên không tạo
	if code, _ := put("ifmatch", `{"resourceType":"Patient"}`, "If-Match", `W/"1"`); code != 404 {
		t.Errorf("If-Match với id chưa có: %d, muốn 404", code)
	}
	// id trong body khác đường dẫn vẫn là 400
	if code, _ := put("x1", `{"resourceType":"Patient","id":"x2"}`); code != 400 {
		t.Errorf("id không khớp: %d", code)
	}
	// resource đã xóa không được tạo lại
	if code, _ := putTo(h, "DELETE", "Patient/my-id.1", ""); code != 204 {
		t.Fatalf("xóa: %d", code)
	}
	if code, _ := put("my-id.1", `{"resourceType":"Patient","id":"my-id.1"}`); code != 410 {
		t.Errorf("tạo lại resource đã xóa: %d, muốn 410", code)
	}
	// PATCH không bao giờ tạo
	if code, _ := putTo(h, "PATCH", "Patient/patch-missing", `[{"op":"add","path":"/active","value":true}]`, "Content-Type", "application/json-patch+json"); code != 404 {
		t.Errorf("PATCH id chưa có: %d, muốn 404", code)
	}
	// trong transaction: PUT tới id chưa có tạo mới
	tx := `{"resourceType":"Bundle","type":"transaction","entry":[{"resource":{"resourceType":"Patient","id":"in-tx"},"request":{"method":"PUT","url":"Patient/in-tx"}}]}`
	if code, body := putTo(h, "POST", "", tx); code != 200 || !strings.Contains(body, "201") {
		t.Errorf("transaction: %d %s", code, body)
	}
	// kiểu resource sai trong body vẫn bị từ chối
	if code, _ := putTo(h, "PUT", "Patient/mismatch", `{"resourceType":"Observation"}`); code != 400 {
		t.Errorf("sai kiểu: %d", code)
	}
}
