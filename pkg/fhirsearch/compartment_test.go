package fhirsearch_test

import (
	"encoding/json"
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

func doc(t *testing.T, js string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(js), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestCompartmentParseAndMatch(t *testing.T) {
	req, err := fhirsearch.ParseQuery("Observation", "_compartment=Patient/p1&status=final")
	if err != nil {
		t.Fatal(err)
	}
	if req.Compartment != "Patient/p1" || len(req.Exprs) != 1 || len(req.ExprRaw) != 0 {
		t.Fatalf("Compartment=%q Exprs=%d ExprRaw=%d", req.Compartment, len(req.Exprs), len(req.ExprRaw))
	}
	cases := map[string]bool{
		`{"resourceType":"Observation","status":"final","subject":{"reference":"Patient/p1"}}`:                                          true,
		`{"resourceType":"Observation","status":"final","subject":{"reference":"Patient/p2"},"performer":[{"reference":"Patient/p1"}]}`: true, // qua performer
		`{"resourceType":"Observation","status":"final","subject":{"reference":"Patient/p2"}}`:                                          false,
		`{"resourceType":"Observation","status":"final"}`:                                                                               false,
		`{"resourceType":"Observation","status":"amended","subject":{"reference":"Patient/p1"}}`:                                        false, // AND với status
		`{"resourceType":"Observation","status":"final","subject":{"reference":"Patient/p10"}}`:                                         false, // không khớp tiền tố
	}
	for js, want := range cases {
		ok, err := fhirsearch.Match(doc(t, js), req)
		if err != nil || ok != want {
			t.Errorf("%s: got %v (%v), want %v", js, ok, err, want)
		}
	}
	// Patient thuộc compartment của chính nó
	preq, err := fhirsearch.ParseQuery("Patient", "_compartment=Patient/p1")
	if err != nil {
		t.Fatal(err)
	}
	for js, want := range map[string]bool{`{"resourceType":"Patient","id":"p1"}`: true, `{"resourceType":"Patient","id":"p2"}`: false, `{"resourceType":"Patient","id":"p2","link":[{"other":{"reference":"Patient/p1"},"type":"seealso"}]}`: true} {
		ok, err := fhirsearch.Match(doc(t, js), preq)
		if err != nil || ok != want {
			t.Errorf("Patient %s: got %v (%v), want %v", js, ok, err, want)
		}
	}
}

func TestCompartmentRejectsBadInput(t *testing.T) {
	for name, q := range map[string]struct{ rt, query string }{
		"nhiều compartment":      {"Observation", "_compartment=Patient/1,Patient/2"},
		"loại khác Patient":      {"Observation", "_compartment=Group/1"},
		"thiếu id":               {"Observation", "_compartment=Patient/"},
		"có khoảng trắng":        {"Observation", "_compartment=Patient/a%20b"},
		"đường dẫn tuyệt đối":    {"Observation", "_compartment=http://x/Patient/1"},
		"đặt hai lần":            {"Observation", "_compartment=Patient/1&_compartment=Patient/2"},
		"search hệ thống":        {"", "_compartment=Patient/1"},
		"loại ngoài compartment": {"Practitioner", "_compartment=Patient/1"},
	} {
		if _, err := fhirsearch.ParseQuery(q.rt, q.query); err == nil {
			t.Errorf("%s: phải bị từ chối (%s?%s)", name, q.rt, q.query)
		}
	}
}

func TestCompartmentSurvivesQueryRebuild(t *testing.T) {
	req, err := fhirsearch.ParseQuery("Observation", "_compartment=Patient/p1&_count=5")
	if err != nil {
		t.Fatal(err)
	}
	q := req.Query(0)
	if q.Get("_compartment") != "Patient/p1" {
		t.Errorf("link phân trang phải giữ _compartment: %v", q)
	}
	again, err := fhirsearch.Parse("Observation", q)
	if err != nil || again.Compartment != "Patient/p1" {
		t.Errorf("dựng lại từ query: %v %+v", err, again)
	}
}
