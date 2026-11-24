package docs

import (
	"os"
	"strings"
	"testing"

	"trivita-fhir/pkg/apicontract"
)

// Hợp đồng API của dicomweb: bảng route trong api-contract.md SINH từ swagger.json, và swagger.json phải đủ DTO, đúng cấu trúc. Quy tắc dùng chung nằm ở
// pkg/apicontract; ở đây chỉ khai báo phần riêng của service. Cập nhật bảng: UPDATE_DOCS=1 go test ./apps/dicomweb/docs -run TestAPIContractIsUpToDate
// Thiếu mô tả, required, ví dụ thì sửa struct hoặc annotation rồi `make swagger APP=dicomweb` (không sửa swagger.json bằng tay).

func TestAPIContractIsUpToDate(t *testing.T) {
	stale, err := apicontract.SyncDoc("swagger.json", "api-contract.md", os.Getenv("UPDATE_DOCS") == "1")
	if err != nil {
		t.Fatal(err)
	}
	if stale {
		t.Error("api-contract.md đã lỗi thời so với swagger.json: chạy UPDATE_DOCS=1 go test ./apps/dicomweb/docs -run TestAPIContractIsUpToDate")
	}
}

func specOptions() apicontract.Options {
	return apicontract.Options{
		RequestSuffixes: []string{"Body", "Request"},
		ContentType: func(def string) string {
			if strings.HasSuffix(def, "StowResponse") {
				return "application/dicom+json" // phản hồi STOW-RS là DICOM JSON; lỗi và probe là application/json
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
// thật, và `produces` khớp Content-Type thật của server (mọi phản hồi hiện là application/json).
func TestSwaggerIsStructurallyValid(t *testing.T) {
	problems, err := apicontract.Structure(readSpec(t), specOptions())
	report(t, problems, err)
}
