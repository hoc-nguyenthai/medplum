package docs

import (
	"os"
	"strings"
	"testing"

	"trivita-fhir/pkg/apicontract"
)

// Tài liệu tham chiếu từng route (api-contract/reference/<tag>.md) SINH hoàn toàn từ swagger.json. Cập nhật:
// UPDATE_DOCS=1 go test ./apps/fhir-server/docs -run TestAPIReferenceIsUpToDate
// Các trường hợp phản hồi viết tay ở api-contract/cases/<tag>.md; TestCasesCoverEveryRoute bắt route thiếu mục hoặc mục mồ côi.

func referenceOptions() apicontract.ReferenceOptions {
	return apicontract.ReferenceOptions{
		CasesDir:       "../cases",
		ExpandPrefixes: []string{"httpapi."},
		CommonSchemas: map[string]string{
			"fhir.OperationOutcome":      "[common.md mục 3](../common.md#3-định-dạng-lỗi)",
			"fhir.OperationOutcomeIssue": "[common.md mục 3](../common.md#3-định-dạng-lỗi)",
		},
		CommonHeaders: "**Header phản hồi chung (mọi route):** `Cache-Control: no-store, no-cache, must-revalidate`, `Pragma: no-cache`, " +
			"`Content-Security-Policy: default-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none';`, " +
			"`X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`, `X-Request-Id`. " +
			"Định dạng lỗi, xác thực, hạn mức: [../common.md](../common.md).",
	}
}

func TestAPIReferenceIsUpToDate(t *testing.T) {
	stale, err := apicontract.SyncReference("swagger.json", "api-contract/reference", referenceOptions(), os.Getenv("UPDATE_DOCS") == "1")
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) > 0 {
		t.Errorf("tài liệu tham chiếu lỗi thời so với swagger.json: %s; chạy UPDATE_DOCS=1 go test ./apps/fhir-server/docs -run TestAPIReferenceIsUpToDate", strings.Join(stale, ", "))
	}
}

func TestCasesCoverEveryRoute(t *testing.T) {
	problems, err := apicontract.CheckCases(readSpec(t), "api-contract/cases")
	report(t, problems, err)
}
