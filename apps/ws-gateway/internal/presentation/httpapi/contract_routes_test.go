package httpapi_test

import (
	"encoding/json"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var echoParam = regexp.MustCompile(`:([A-Za-z0-9_]+)`)

// Hợp đồng API (docs/swagger.json, từ đó sinh docs/api-contract.md) phải khớp với route thật khi FHIRcast bật: route có trong code mà thiếu ở spec
// (hoặc ngược lại) làm test đỏ. Quy ước ánh xạ: `/ws/fhircast/*` của Echo là `/ws/fhircast/{endpoint}` trong spec; bí danh không đưa vào spec là
// `POST {v}/` (dấu / cuối) và `GET /ws/fhircast` (không có endpoint, vào cùng handler để nhận "invalid endpoint").
func TestRoutesMatchSwaggerContract(t *testing.T) {
	f := newFC(t, nil)
	served := map[string]bool{}
	for _, r := range f.e.Routes() {
		path := strings.Replace(echoParam.ReplaceAllString(r.Path, "{$1}"), "/ws/fhircast/*", "/ws/fhircast/{endpoint}", 1)
		switch {
		case r.Method == "echo_route_not_found" || r.Method == "HEAD" || r.Method == "OPTIONS" || r.Method == "CONNECT" || r.Method == "TRACE" || r.Method == "PROPFIND" || r.Method == "REPORT":
			continue
		case r.Method == "POST" && strings.HasSuffix(path, "/") && path != "/":
			continue // alias dấu / cuối
		case r.Method == "GET" && path == "/ws/fhircast":
			continue // alias không có endpoint
		}
		served[strings.ToLower(r.Method)+" "+path] = true
	}
	raw, err := os.ReadFile("../../../docs/swagger.json")
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
		for m := range ms {
			documented[m+" "+p] = true
		}
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
		t.Errorf("route có trong code nhưng thiếu ở swagger.json (thêm annotation ở httpapi/swagger.go rồi `make swagger APP=ws-gateway`):\n  %s", strings.Join(inCodeOnly, "\n  "))
	}
	if len(inSpecOnly) > 0 {
		t.Errorf("route có trong swagger.json nhưng không được phục vụ:\n  %s", strings.Join(inSpecOnly, "\n  "))
	}
}
