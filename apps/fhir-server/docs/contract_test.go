package docs

import (
	"os"
	"strings"
	"testing"

	"trivita-fhir/pkg/apicontract"
)

// Hợp đồng API của fhir-server: bảng route trong api-contract.md SINH từ swagger.json, và swagger.json phải đủ DTO, đúng cấu trúc. Quy tắc dùng chung nằm ở
// pkg/apicontract; ở đây chỉ khai báo phần riêng của service. Cập nhật bảng: UPDATE_DOCS=1 go test ./apps/fhir-server/docs -run TestAPIContractIsUpToDate
// Thiếu mô tả, required, ví dụ thì sửa struct hoặc annotation rồi `make swagger APP=fhir-server` (không sửa swagger.json bằng tay).

func TestAPIContractIsUpToDate(t *testing.T) {
	stale, err := apicontract.SyncDoc("swagger.json", "api-contract.md", os.Getenv("UPDATE_DOCS") == "1")
	if err != nil {
		t.Fatal(err)
	}
	if stale {
		t.Error("api-contract.md đã lỗi thời so với swagger.json: chạy UPDATE_DOCS=1 go test ./apps/fhir-server/docs -run TestAPIContractIsUpToDate")
	}
}

func specOptions() apicontract.Options {
	return apicontract.Options{
		AllOptional: map[string]string{
			"httpapi.HumanNameBody":    "body mẫu: mọi thành phần của HumanName đều tùy chọn",
			"httpapi.IdentifierBody":   "body mẫu: mọi thành phần của Identifier đều tùy chọn",
			"httpapi.ContactPointBody": "body mẫu: mọi thành phần của ContactPoint đều tùy chọn",
			"httpapi.GraphQLResponse":  "data và errors loại trừ nhau theo tình huống (lỗi cú pháp không có data; thành công hoàn toàn không có errors)",
		},
		NoBody: map[string]string{
			"GET /fhir/R4/bulkdata/{id}/status 202": "export đang chạy: chỉ có header X-Progress và Retry-After",
		},
		ContentType: func(def string) string {
			if strings.HasPrefix(def, "fhir.") {
				return "application/fhir+json"
			}
			return "application/json"
		},
	}
}

func readSpec(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile("swagger.json")
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func report(t *testing.T, problems []string, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range problems {
		t.Error(p)
	}
}

// TestSwaggerDTOsAreComplete: mỗi trường DTO có mô tả, mỗi DTO có `required` (tag `binding:"required"`), body yêu cầu có ví dụ hoặc enum cho trường nguyên
// thủy; mỗi route có tóm tắt, mô tả, schema cho mọi phản hồi và mô tả cho từng tham số; mỗi tag có mô tả.
func TestSwaggerDTOsAreComplete(t *testing.T) {
	problems, err := apicontract.Completeness(readSpec(t), specOptions())
	report(t, problems, err)
}

// TestSwaggerIsStructurallyValid: $ref có thật, tham số path khớp mẫu và có required, không có `example` ở tham số query/path, `required` của DTO là trường
// thật, và `produces` khớp Content-Type thật của server (mọi kiểu fhir.* là application/fhir+json, DTO httpapi.* (health, GraphQL, manifest bulk export) là application/json; đã kiểm bằng cách gọi server thật).
func TestSwaggerIsStructurallyValid(t *testing.T) {
	problems, err := apicontract.Structure(readSpec(t), specOptions())
	report(t, problems, err)
}
