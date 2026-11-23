package bootstrap_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"trivita-fhir/apps/fhir-server/internal/config"
)

// Batch async chạy với đúng quyền và project của người gọi: entry ngoài scope bị 403 riêng (không làm hỏng cả job), resource
// tạo ra thuộc project của người gọi, project khác không thấy job lẫn kết quả.
//
// Chạy cả khi bật kho Binary: Binary của job nằm trong CSDL (như bulk export) mà vẫn đọc được qua router có kho.
func TestAsyncBatchRunsWithCallerGrants(t *testing.T) {
	t.Run("không có kho Binary", func(t *testing.T) {
		testAsyncBatchGrants(t, jwtSettings(func(s *config.Settings) { s.AsyncBatch = true }))
	})
	t.Run("có kho Binary", func(t *testing.T) {
		dir := t.TempDir()
		testAsyncBatchGrants(t, jwtSettings(func(s *config.Settings) { s.AsyncBatch = true; s.BinaryStorage = "file:" + dir }))
	})
}

func testAsyncBatchGrants(t *testing.T, s config.Settings) {
	_, srv := newServer(t, s)
	patWrite, fullA, fullB := tok(t, "A", "user/Patient.cruds"), tok(t, "A", "fhir.write"), tok(t, "B", "fhir.write")

	bundle := mustMarshal(t, map[string]any{"resourceType": "Bundle", "type": "batch", "entry": []any{
		map[string]any{"request": map[string]any{"method": "POST", "url": "Patient"}, "resource": patient("Async")},
		map[string]any{"request": map[string]any{"method": "POST", "url": "Observation"},
			"resource": map[string]any{"resourceType": "Observation", "status": "final", "code": map[string]any{"text": "x"}}},
	}})
	req, _ := http.NewRequest("POST", srv.URL+"/fhir/R4", bytes.NewReader(bundle))
	req.Header.Set("Authorization", "Bearer "+patWrite)
	req.Header.Set("Content-Type", "application/fhir+json")
	req.Header.Set("Prefer", "respond-async")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 202 {
		t.Fatalf("muốn 202, được %d: %s", res.StatusCode, raw)
	}
	loc := res.Header.Get("Content-Location")
	path := loc[strings.Index(loc, "/fhir/R4/"):]

	want(t, call(t, srv, "GET", path, fullB, nil), 404) // project khác không thấy job
	var st resp
	for i := 0; ; i++ {
		if st = call(t, srv, "GET", path, patWrite, nil); st.status != 202 || i > 300 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	want(t, st, 200)
	ref := st.str(t, "output", "parameter", "0", "valueReference", "reference")
	binID, ok := strings.CutPrefix(ref, "Binary/")
	if !ok {
		t.Fatalf("output: %s", st.body)
	}
	want(t, call(t, srv, "GET", "/fhir/R4/Binary/"+binID, fullB, nil), 404)
	bin := want(t, call(t, srv, "GET", "/fhir/R4/Binary/"+binID, fullA, nil), 200)
	data := bin.body // có kho Binary: GET Binary trả thẳng nội dung (Binary thô)
	if bin.str(t, "resourceType") == "Binary" {
		data, _ = base64.StdEncoding.DecodeString(bin.str(t, "data"))
	}
	var out struct {
		Entry []struct {
			Response struct{ Status string } `json:"response"`
		} `json:"entry"`
	}
	if err := json.Unmarshal(data, &out); err != nil || len(out.Entry) != 2 {
		t.Fatalf("Bundle kết quả: %s", data)
	}
	if s := out.Entry[0].Response.Status; !strings.HasPrefix(s, "201") {
		t.Errorf("entry Patient: %s", s)
	}
	if s := out.Entry[1].Response.Status; !strings.HasPrefix(s, "403") {
		t.Errorf("entry Observation ngoài scope: %s", s)
	}
	pats := want(t, call(t, srv, "GET", "/fhir/R4/Patient?family=Async", fullA, nil), 200).json(t)["entry"].([]any)
	if len(pats) != 1 || pats[0].(map[string]any)["resource"].(map[string]any)["meta"].(map[string]any)["project"] != "A" {
		t.Errorf("Patient phải thuộc project A: %v", pats)
	}
	if n := want(t, call(t, srv, "GET", "/fhir/R4/Patient?family=Async", fullB, nil), 200).json(t)["entry"]; n != nil {
		t.Errorf("project B thấy Patient của A: %v", n)
	}
}
