package docs

import (
	"os"
	"strings"
	"testing"

	"trivita-fhir/pkg/apicontract"
)

// Hợp đồng API của auth: bảng route trong api-contract.md SINH từ swagger.json, và swagger.json phải đủ DTO, đúng cấu trúc. Quy tắc dùng chung nằm ở
// pkg/apicontract; ở đây chỉ khai báo phần riêng của service. Cập nhật bảng: UPDATE_DOCS=1 go test ./apps/auth/docs -run TestAPIContractIsUpToDate
// Thiếu mô tả, required, ví dụ thì sửa struct hoặc annotation rồi `make swagger APP=auth` (không sửa swagger.json bằng tay).

func TestAPIContractIsUpToDate(t *testing.T) {
	stale, err := apicontract.SyncDoc("swagger.json", "api-contract.md", os.Getenv("UPDATE_DOCS") == "1")
	if err != nil {
		t.Fatal(err)
	}
	if stale {
		t.Error("api-contract.md đã lỗi thời so với swagger.json: chạy UPDATE_DOCS=1 go test ./apps/auth/docs -run TestAPIContractIsUpToDate")
	}
}

func specOptions() apicontract.Options {
	return apicontract.Options{
		AllOptional: map[string]string{
			"application.MethodResult":     "phản hồi rỗng khi email đăng nhập bằng mật khẩu (chỉ có trường khi tên miền có IdP ngoài)",
			"httpapi.EmailAttachmentBody":  "mỗi tệp cần content hoặc path (một trong hai), không trường nào luôn bắt buộc",
			"httpapi.InviteMembershipBody": "toàn bộ là ghi đè tùy chọn lên membership được tạo",
			"httpapi.MFAResetBody":         "body tùy chọn; bỏ trống là gỡ mọi yếu tố",
			"httpapi.RotateSecretBody":     "body tùy chọn; bỏ trống là xoay với secret server sinh",
			"httpapi.SmartLaunchBody":      "cần ít nhất một trong patient, encounter, fhirContext; không trường nào luôn bắt buộc",
			"scim.Name":                    "SCIM name: mọi thành phần đều tùy chọn",
		},
		ContentType: func(def string) string {
			switch {
			case def == "fhir.OperationOutcome":
				return "application/fhir+json"
			case strings.HasPrefix(def, "scim."):
				return "application/scim+json"
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
// thật, và `produces` khớp Content-Type thật của server (OperationOutcome là application/fhir+json, scim.* là application/scim+json, DTO khác là application/json; xem httpapi/errors.go, scim.go).
func TestSwaggerIsStructurallyValid(t *testing.T) {
	problems, err := apicontract.Structure(readSpec(t), specOptions())
	report(t, problems, err)
}
