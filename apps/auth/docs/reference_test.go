package docs

import (
	"strings"
	"testing"

	"os"
	"trivita-fhir/pkg/apicontract"
)

// Tài liệu tham chiếu từng route (api-contract/reference/<tag>.md) SINH hoàn toàn từ swagger.json: header, tham số, bảng trường và JSON mẫu của yêu cầu và từng
// phản hồi. Cập nhật: UPDATE_DOCS=1 go test ./apps/auth/docs -run TestAPIReferenceIsUpToDate
// Các trường hợp phản hồi (điều kiện, ví dụ, header) viết tay ở api-contract/cases/<tag>.md; TestCasesCoverEveryRoute bắt route thiếu mục hoặc mục mồ côi.

func referenceOptions() apicontract.ReferenceOptions {
	return apicontract.ReferenceOptions{
		CasesDir:       "../cases",
		ExpandPrefixes: []string{"application.", "httpapi.", "scim."},
		CommonSchemas: map[string]string{
			"fhir.OperationOutcome":  "[common.md mục 3](../common.md#3-operationoutcome)",
			"httpapi.TokenErrorBody": "[common.md mục 4](../common.md#4-lỗi-oauth2)",
			"scim.Error":             "[common.md mục 5](../common.md#5-lỗi-scim)",
			"httpapi.ErrorBody":      "[common.md mục 6](../common.md#6-lỗi-json-thường)",
		},
		CommonHeaders: "**Header phản hồi chung (mọi route):** `Cache-Control: no-store, no-cache, must-revalidate`, `Pragma: no-cache`, `Content-Security-Policy: default-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none';`, " +
			"`X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`, `X-Request-Id`; `Strict-Transport-Security` khi `AUTH_BASE_URL` là https. " +
			"Chi tiết và các envelope lỗi dùng chung: [../common.md](../common.md).",
	}
}

func TestAPIReferenceIsUpToDate(t *testing.T) {
	stale, err := apicontract.SyncReference("swagger.json", "api-contract/reference", referenceOptions(), os.Getenv("UPDATE_DOCS") == "1")
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) > 0 {
		t.Errorf("tài liệu tham chiếu lỗi thời so với swagger.json: %s; chạy UPDATE_DOCS=1 go test ./apps/auth/docs -run TestAPIReferenceIsUpToDate", strings.Join(stale, ", "))
	}
}

func TestCasesCoverEveryRoute(t *testing.T) {
	problems, err := apicontract.CheckCases(readSpec(t), "api-contract/cases")
	report(t, problems, err)
}
