package docs

import (
	"os"
	"testing"

	"trivita-fhir/pkg/apicontract"
)

// Bảng route trong api-contract.md được SINH từ swagger.json (phần giữa hai dấu BEGIN/END); test này so sánh với tệp trong repo.
// Cập nhật: UPDATE_DOCS=1 go test ./apps/APP/docs -run TestAPIContractIsUpToDate
func TestAPIContractIsUpToDate(t *testing.T) {
	spec, err := os.ReadFile("swagger.json")
	if err != nil {
		t.Fatal(err)
	}
	table, err := apicontract.Render(spec)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := os.ReadFile("api-contract.md")
	if err != nil {
		t.Fatalf("thiếu api-contract.md: %v", err)
	}
	want, err := apicontract.Splice(string(doc), table)
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("UPDATE_DOCS") == "1" {
		if err := os.WriteFile("api-contract.md", []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	if string(doc) != want {
		t.Error("api-contract.md đã lỗi thời so với swagger.json: chạy UPDATE_DOCS=1 go test ./apps/APP/docs -run TestAPIContractIsUpToDate (APP=auth|fhir-server)")
	}
}
