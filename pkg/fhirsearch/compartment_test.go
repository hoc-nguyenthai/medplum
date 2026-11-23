package fhirsearch_test

import (
	"net/url"
	"testing"

	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirsearch"
)

// Mọi cặp (resource type, search parameter) của compartment Patient phải tìm kiếm được, vì $everything dựa vào đó.
func TestPatientCompartmentParamsAreUsable(t *testing.T) {
	comp := fhir.PatientCompartment()
	if len(comp) < 50 || len(comp["Observation"]) == 0 {
		t.Fatalf("compartment có %d kiểu, nghi ngờ dữ liệu thiếu", len(comp))
	}
	for rt, params := range comp {
		for _, p := range params {
			if _, err := fhirsearch.Parse(rt, url.Values{p: {"Patient/1"}}); err != nil {
				t.Errorf("%s.%s: %v", rt, p, err)
			}
		}
	}
}
