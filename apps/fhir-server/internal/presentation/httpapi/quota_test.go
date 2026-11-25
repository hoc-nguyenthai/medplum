package httpapi_test

import (
	"testing"

	"trivita-fhir/apps/fhir-server/internal/presentation/httpapi"
)

func TestQuotaCost(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		want         int
	}{
		{"GET", "/fhir/R4/Patient/1", httpapi.CostRead},
		{"HEAD", "/fhir/R4/Patient/1", httpapi.CostRead},
		{"GET", "/fhir/R4/Patient/1/_history/2", httpapi.CostSearch}, // vread tính như tìm kiếm
		{"GET", "/fhir/R4/Patient/1/_history", httpapi.CostSearch},
		{"GET", "/fhir/R4/Patient/_history", httpapi.CostSearch},
		{"GET", "/fhir/R4/_history", httpapi.CostSearch},
		{"GET", "/fhir/R4/Patient", httpapi.CostSearch},
		{"GET", "/fhir/R4/Patient/", httpapi.CostSearch},
		{"POST", "/fhir/R4/Patient/_search", httpapi.CostSearch},
		{"POST", "/fhir/R4/Patient", httpapi.CostWrite},
		{"PUT", "/fhir/R4/Patient/1", httpapi.CostWrite},
		{"PATCH", "/fhir/R4/Patient/1", httpapi.CostWrite},
		{"DELETE", "/fhir/R4/Patient/1", httpapi.CostWrite},
		{"POST", "/fhir/R4", httpapi.CostBundle},
		{"POST", "/fhir/R4/", httpapi.CostBundle},
		{"GET", "/fhir/R4", httpapi.CostSearch},
		{"POST", "/fhir/R4/Patient/$validate", httpapi.CostOperation},
		{"GET", "/fhir/R4/Patient/1/$everything", httpapi.CostOperation},
		{"GET", "/fhir/R4/$export", httpapi.CostOperation},
		{"POST", "/fhir/R4/$export", httpapi.CostOperation},
		{"GET", "/fhir/R4/metadata", 0},
		{"OPTIONS", "/fhir/R4/Patient", 0},
		{"GET", "/fhir/R4/Project/p1/$rate-limits", 0},
	} {
		if got := httpapi.QuotaCost(tc.method, tc.path); got != tc.want {
			t.Errorf("%s %s = %d, muốn %d", tc.method, tc.path, got, tc.want)
		}
	}
}
