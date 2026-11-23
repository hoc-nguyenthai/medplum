package bootstrap_test

import (
	"context"
	"encoding/json"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"trivita-fhir/apps/fhir-server/internal/bootstrap"
)

var echoParam = regexp.MustCompile(`:([A-Za-z0-9_]+)`)

// Hợp đồng API (docs/swagger.json, từ đó sinh docs/api-contract.md) phải khớp với route thật: route có trong code mà thiếu ở spec
// (hoặc ngược lại) làm test đỏ. GIỚI HẠN: router FHIR (/fhir/R4/...) là một http.Handler gắn bằng wildcard (pkg/fhirrouter) nên Echo không liệt kê được
// từng route của nó; phần đó chỉ được kiểm bằng test của pkg/fhirrouter và e2e. Test này kiểm mọi route Echo còn lại (health...) và việc wildcard FHIR có mặt.
func TestRoutesMatchSwaggerContract(t *testing.T) {
	c, err := bootstrap.New(context.Background(), settings())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Stop(context.Background()) })
	e, err := c.Handler(nil)
	if err != nil {
		t.Fatal(err)
	}
	fhirMounted := false
	skipMethod := map[string]bool{"echo_route_not_found": true, "HEAD": true, "OPTIONS": true, "CONNECT": true, "TRACE": true, "PROPFIND": true, "REPORT": true}
	served := map[string]bool{}
	for _, r := range e.Routes() {
		path := echoParam.ReplaceAllString(r.Path, "{$1}")
		if strings.HasPrefix(path, "/fhir/R4") {
			fhirMounted = true
			continue
		}
		if skipMethod[r.Method] || strings.HasPrefix(path, "/swagger") || strings.Contains(path, "*") {
			continue
		}
		served[strings.ToLower(r.Method)+" "+path] = true
	}
	raw, err := os.ReadFile("../../docs/swagger.json")
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	documented := map[string]bool{}
	for p, ms := range spec.Paths {
		if strings.HasPrefix(p, "/fhir/R4") {
			continue
		}
		for m := range ms {
			documented[m+" "+p] = true
		}
	}
	if !fhirMounted {
		t.Error("router FHIR (/fhir/R4) không được gắn vào Echo")
	}
	var inCodeOnly, inSpecOnly []string
	for k := range served {
		if !documented[k] {
			inCodeOnly = append(inCodeOnly, k)
		}
	}
	for k := range documented {
		if !served[k] {
			inSpecOnly = append(inSpecOnly, k)
		}
	}
	sort.Strings(inCodeOnly)
	sort.Strings(inSpecOnly)
	if len(inCodeOnly) > 0 {
		t.Errorf("route có trong code nhưng thiếu ở swagger.json (thêm annotation rồi `make swagger APP=fhir-server`):\n  %s", strings.Join(inCodeOnly, "\n  "))
	}
	if len(inSpecOnly) > 0 {
		t.Errorf("route có trong swagger.json nhưng không được phục vụ:\n  %s", strings.Join(inSpecOnly, "\n  "))
	}
}
