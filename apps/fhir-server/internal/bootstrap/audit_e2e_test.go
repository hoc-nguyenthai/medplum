package bootstrap_test

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"trivita-fhir/apps/fhir-server/internal/config"
)

func auditEvents(t *testing.T, srv *httptest.Server, token string) []any {
	t.Helper()
	b := want(t, call(t, srv, "GET", "/fhir/R4/AuditEvent?_count=100&_sort=_lastUpdated", token, nil), 200).json(t)
	var out []any
	entries, _ := b["entry"].([]any)
	for _, e := range entries {
		out = append(out, e.(map[string]any)["resource"])
	}
	return out
}

func subtypes(evs []any) map[string]int {
	m := map[string]int{}
	for _, e := range evs {
		m[e.(map[string]any)["subtype"].([]any)[0].(map[string]any)["code"].(string)]++
	}
	return m
}

// FHIR_AUDIT=writes: create/update/delete của người gọi có AuditEvent trong project của họ (kèm người gọi, IP, thực thể); project khác không thấy;
// không sửa, không xóa được qua API; thao tác bị từ chối hoặc chỉ đọc không sinh AuditEvent.
func TestAuditEventsWritesEndToEnd(t *testing.T) {
	_, srv := newServer(t, jwtSettings(func(s *config.Settings) { s.Audit = "writes" }))
	a, b, ro := tok(t, "A", "fhir.write"), tok(t, "B", "fhir.write"), tok(t, "A", "fhir.read")

	id := want(t, call(t, srv, "POST", "/fhir/R4/Patient", a, patient("Kiểm")), 201).str(t, "id")
	want(t, call(t, srv, "PUT", "/fhir/R4/Patient/"+id, a, map[string]any{"resourceType": "Patient", "id": id, "gender": "female"}), 200)
	want(t, call(t, srv, "DELETE", "/fhir/R4/Patient/"+id, a, nil), 204)
	want(t, call(t, srv, "GET", "/fhir/R4/Patient?family=x", a, nil), 200) // đọc: không có AuditEvent ở chế độ writes
	if r := call(t, srv, "POST", "/fhir/R4/Patient", ro, patient("Cấm")); r.status != 403 {
		t.Fatalf("token chỉ đọc: %d", r.status)
	}

	evs := auditEvents(t, srv, a)
	if got := subtypes(evs); len(evs) != 3 || got["create"] != 1 || got["update"] != 1 || got["delete"] != 1 {
		t.Fatalf("cần đúng create, update, delete; có %v", got)
	}
	first := evs[0].(map[string]any)
	agent := first["agent"].([]any)[0].(map[string]any)
	if agent["who"].(map[string]any)["identifier"].(map[string]any)["value"] != "user@A" || agent["requestor"] != true {
		t.Errorf("agent: %v", agent)
	}
	if net, _ := agent["network"].(map[string]any); net == nil || !strings.HasPrefix(net["address"].(string), "127.0.0.1") {
		t.Errorf("agent.network: %v", agent["network"])
	}
	if first["entity"].([]any)[0].(map[string]any)["what"].(map[string]any)["reference"] != "Patient/"+id {
		t.Errorf("entity: %v", first["entity"])
	}
	if first["meta"].(map[string]any)["project"] != nil && first["meta"].(map[string]any)["project"] != "A" {
		t.Errorf("meta.project: %v", first["meta"])
	}

	// project B không thấy AuditEvent của A và có AuditEvent riêng
	if n := len(auditEvents(t, srv, b)); n != 0 {
		t.Fatalf("project B thấy %d AuditEvent của A", n)
	}
	want(t, call(t, srv, "POST", "/fhir/R4/Patient", b, patient("B")), 201)
	if n := len(auditEvents(t, srv, b)); n != 1 {
		t.Fatalf("project B cần 1 AuditEvent của chính nó, có %d", n)
	}
	if n := len(auditEvents(t, srv, a)); n != 3 {
		t.Fatalf("A vẫn 3 AuditEvent, có %d", n)
	}

	// không sửa, không xóa AuditEvent qua API
	aid := first["id"].(string)
	want(t, call(t, srv, "DELETE", "/fhir/R4/AuditEvent/"+aid, a, nil), 403)
	first["recorded"] = "2000-01-01T00:00:00.000Z"
	want(t, call(t, srv, "PUT", "/fhir/R4/AuditEvent/"+aid, a, first), 403)
	if n := len(auditEvents(t, srv, a)); n != 3 {
		t.Fatalf("AuditEvent bị đổi: %d", n)
	}
}

// FHIR_AUDIT=all thêm read và search (nhưng không kiểm toán việc đọc chính AuditEvent); mặc định (off) không có gì.
func TestAuditEventsAllAndOffEndToEnd(t *testing.T) {
	_, srv := newServer(t, jwtSettings(func(s *config.Settings) { s.Audit = "all" }))
	a := tok(t, "A", "fhir.write")
	id := want(t, call(t, srv, "POST", "/fhir/R4/Patient", a, patient("Đọc")), 201).str(t, "id")
	want(t, call(t, srv, "GET", "/fhir/R4/Patient/"+id, a, nil), 200)
	want(t, call(t, srv, "GET", "/fhir/R4/Patient?family=%C4%90%E1%BB%8Dc", a, nil), 200)
	got := subtypes(auditEvents(t, srv, a))
	if got["create"] != 1 || got["read"] != 1 || got["search-type"] != 1 || len(got) != 3 {
		t.Fatalf("chế độ all: %v", got)
	}
	again := subtypes(auditEvents(t, srv, a)) // đọc AuditEvent không sinh AuditEvent mới
	if again["search-type"] != 1 {
		t.Fatalf("đọc AuditEvent bị kiểm toán: %v", again)
	}

	_, off := newServer(t, jwtSettings())
	want(t, call(t, off, "POST", "/fhir/R4/Patient", a, patient("Tắt")), 201)
	if n := len(auditEvents(t, off, a)); n != 0 {
		t.Fatalf("mặc định không được ghi AuditEvent, có %d", n)
	}
}

// Batch async chạy bằng quyền người gọi: entry của job cũng có AuditEvent, thuộc project của người gọi.
func TestAuditEventsForAsyncBatchJob(t *testing.T) {
	_, srv := newServer(t, jwtSettings(func(s *config.Settings) { s.AsyncBatch = true; s.Audit = "writes" }))
	a := tok(t, "A", "fhir.write")
	bundle := mustMarshal(t, map[string]any{"resourceType": "Bundle", "type": "batch", "entry": []any{
		map[string]any{"request": map[string]any{"method": "POST", "url": "Patient"}, "resource": patient("Job")}}})
	req, _ := http.NewRequest("POST", srv.URL+"/fhir/R4", bytes.NewReader(bundle))
	req.Header.Set("Authorization", "Bearer "+a)
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
	for i := 0; i < 300; i++ {
		if call(t, srv, "GET", path, a, nil).status != 202 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	var creates int
	for _, e := range auditEvents(t, srv, a) {
		ev := e.(map[string]any)
		if ev["subtype"].([]any)[0].(map[string]any)["code"] == "create" && strings.HasPrefix(ev["entity"].([]any)[0].(map[string]any)["what"].(map[string]any)["reference"].(string), "Patient/") {
			creates++
		}
	}
	if creates != 1 {
		t.Fatalf("job batch async: cần 1 AuditEvent create Patient, có %d", creates)
	}
}

// FHIR_AUDIT=all: bulk export đọc dữ liệu bằng quyền người gọi qua repository của job nên việc đọc đó cũng có AuditEvent (agent là job), thuộc project của người gọi.
func TestAuditEventsForBulkExportReads(t *testing.T) {
	_, srv := newServer(t, jwtSettings(func(s *config.Settings) { s.Audit = "all" }))
	a := tok(t, "A", "fhir.write")
	want(t, call(t, srv, "POST", "/fhir/R4/Patient", a, patient("Export")), 201)
	acc := callWith(t, srv, "GET", "/fhir/R4/$export?_type=Patient", a, "Prefer", "respond-async")
	if acc.status != 202 {
		t.Fatalf("export: %d %s", acc.status, acc.body)
	}
	bulk := strings.TrimPrefix(acc.header.Get("Content-Location"), "http://fhir.test")
	for i := 0; i < 300; i++ {
		if callWith(t, srv, "GET", bulk, a).status != 202 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	var byJob int
	for _, e := range auditEvents(t, srv, a) {
		ev := e.(map[string]any)
		agents := ev["agent"].([]any)
		who := agents[len(agents)-1].(map[string]any)["who"].(map[string]any)["identifier"].(map[string]any)["value"].(string)
		if len(agents) == 2 && agents[0].(map[string]any)["who"].(map[string]any)["identifier"].(map[string]any)["value"] == "user@A" && strings.HasPrefix(who, "job:") && ev["subtype"].([]any)[0].(map[string]any)["code"] == "search-type" {
			byJob++
		}
	}
	if byJob == 0 {
		t.Fatal("việc đọc của bulk export phải có AuditEvent do job thực hiện")
	}
}
