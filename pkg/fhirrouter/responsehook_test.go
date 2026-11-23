package fhirrouter_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirrouter"
)

func TestResponseHook(t *testing.T) {
	var calls atomic.Int32
	hook := func(_ context.Context, res fhir.Resource) fhir.Resource {
		calls.Add(1)
		if p, ok := res.(*fhir.Patient); ok { // đổi bản sao, không sửa đối tượng đầu vào
			cp := *p
			cp.Language = "hooked"
			return &cp
		}
		return res
	}
	repo := fhirrouter.NewMemoryRepository()
	h := fhirrouter.New(repo, fhirrouter.Options{ResponseHook: hook}).HTTPHandler()

	code, body := putTo(h, "POST", "Patient", `{"resourceType":"Patient"}`)
	if code != 201 || !strings.Contains(body, `"language":"hooked"`) {
		t.Fatalf("create: %d %s", code, body)
	}
	id := strings.Split(strings.Split(body, `"id":"`)[1], `"`)[0]
	if code, body := putTo(h, "GET", "Patient/"+id, ""); code != 200 || !strings.Contains(body, "hooked") {
		t.Fatalf("read: %d %s", code, body)
	}
	if code, body := putTo(h, "GET", "Patient", ""); code != 200 || !strings.Contains(body, `"resourceType":"Bundle"`) {
		t.Fatalf("search: %d %s", code, body)
	}
	if code, body := putTo(h, "GET", "metadata", ""); code != 200 || !strings.Contains(body, "CapabilityStatement") {
		t.Fatalf("metadata: %d %s", code, body)
	}
	// dữ liệu lưu không bị hook đổi
	stored, _ := repo.ReadResource(context.Background(), "Patient", id)
	if stored.(*fhir.Patient).Language == "hooked" {
		t.Fatal("hook đã sửa đối tượng trong kho")
	}
	// lỗi không qua hook
	before := calls.Load()
	if code, _ := putTo(h, "GET", "Patient/ghost", ""); code != 404 {
		t.Fatalf("%d", code)
	}
	if calls.Load() != before {
		t.Fatal("phản hồi lỗi không được qua hook")
	}
	// không có hook: hành vi cũ
	h2 := fhirrouter.New(fhirrouter.NewMemoryRepository(), fhirrouter.Options{}).HTTPHandler()
	if code, body := putTo(h2, "POST", "Patient", `{"resourceType":"Patient"}`); code != 201 || strings.Contains(body, "hooked") {
		t.Fatalf("%d %s", code, body)
	}
}
