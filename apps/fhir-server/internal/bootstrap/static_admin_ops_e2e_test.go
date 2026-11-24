package bootstrap_test

import "testing"

// Chế độ token tĩnh không có AdminChecker (chỉ fhirauth.Repo của chế độ JWT cài nó), nên các thao tác quản trị phải bị chặn ở cổng HTTP theo
// RequiredRole: $import và $install cần admin; ghi trực tiếp AsyncJob (job do router tạo, client không có lý do tự ghi) cũng cần admin, vì ở chế độ
// này RecoverJobs sẽ nhặt bản ghi AsyncJob thô và chạy nó với quyền hệ thống (không có chữ ký quyền để kiểm).

func TestStaticTokenCannotRunAdminOnlyOperations(t *testing.T) {
	_, srv := newServer(t, settings(withAdmin))
	csImport := map[string]any{"resourceType": "Parameters", "parameter": []any{}}
	rawJob := map[string]any{"resourceType": "AsyncJob", "status": "accepted", "request": "reindex?resourceType=Patient"}

	for _, tok := range []string{readSecret, writeSecret} {
		for _, c := range []struct{ method, path string }{
			{"POST", "/fhir/R4/CodeSystem/$import"},
			{"POST", "/fhir/R4/CodeSystem/cs1/$import"},
			{"POST", "/fhir/R4/ConceptMap/$import"},
			{"POST", "/fhir/R4/ConceptMap/cm1/$import"},
			{"POST", "/fhir/R4/PackageRelease/pr1/$install"},
		} {
			want(t, call(t, srv, c.method, c.path, tok, csImport), 403)
		}
	}
	// AsyncJob: token write không ghi được, kể cả đường điều kiện và đường sửa/xóa
	want(t, call(t, srv, "POST", "/fhir/R4/AsyncJob", writeSecret, rawJob), 403)
	want(t, call(t, srv, "PUT", "/fhir/R4/AsyncJob/j1", writeSecret, rawJob), 403)
	want(t, call(t, srv, "PUT", "/fhir/R4/AsyncJob?status=accepted", writeSecret, rawJob), 403)
	want(t, call(t, srv, "DELETE", "/fhir/R4/AsyncJob/j1", writeSecret, nil), 403)
	want(t, call(t, srv, "DELETE", "/fhir/R4/AsyncJob?status=accepted", writeSecret, nil), 403)
	// và qua Bundle (entry được kiểm vai trò như yêu cầu ngoài)
	want(t, call(t, srv, "POST", "/fhir/R4", writeSecret, bundle("transaction", entry("POST", "AsyncJob", rawJob))), 403)
	b := want(t, call(t, srv, "POST", "/fhir/R4", writeSecret, bundle("batch", entry("POST", "CodeSystem/$import", csImport))), 200)
	if st := b.json(t)["entry"].([]any)[0].(map[string]any)["response"].(map[string]any)["status"].(string); st[:3] != "403" {
		t.Errorf("batch $import bằng token write: %q, muốn 403", st)
	}
	// không có tác dụng phụ: không AsyncJob nào được tạo
	if r := want(t, call(t, srv, "GET", "/fhir/R4/AsyncJob?_total=accurate&_count=0", adminSecret, nil), 200); r.json(t)["total"] != float64(0) {
		t.Errorf("AsyncJob bị tạo dù bị từ chối: %s", r.body)
	}

	// đọc AsyncJob trực tiếp chỉ cho admin (client theo dõi job qua route trạng thái, có kiểm chủ sở hữu); admin vẫn làm được
	want(t, call(t, srv, "GET", "/fhir/R4/AsyncJob", readSecret, nil), 403)
	want(t, call(t, srv, "GET", "/fhir/R4/AsyncJob", adminSecret, nil), 200)
	if r := call(t, srv, "POST", "/fhir/R4/CodeSystem/$import", adminSecret, csImport); r.status == 403 {
		t.Errorf("admin bị chặn $import: %s", r.body)
	}
}
