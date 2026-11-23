package httpapi_test

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"trivita-fhir/pkg/apicontract"
)

var echoParam = regexp.MustCompile(`:([A-Za-z0-9_]+)`)

// Hợp đồng API (docs/swagger.json, từ đó sinh docs/api-contract.md) phải khớp với route thật khi FHIRcast bật: route có trong code mà thiếu ở spec
// (hoặc ngược lại) làm test đỏ. Quy ước ánh xạ: `/ws/fhircast/*` của Echo là `/ws/fhircast/{endpoint}` trong spec; bí danh không đưa vào spec là
// `POST {v}/` (dấu / cuối) và `GET /ws/fhircast` (không có endpoint, vào cùng handler để nhận "invalid endpoint").
func TestRoutesMatchSwaggerContract(t *testing.T) {
	f := newFC(t, nil)
	var served []string
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
		served = append(served, strings.ToLower(r.Method)+" "+path)
	}
	raw, err := os.ReadFile("../../../docs/swagger.json")
	if err != nil {
		t.Fatal(err)
	}
	inCodeOnly, inSpecOnly, err := apicontract.DiffRoutes(raw, served, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(inCodeOnly) > 0 {
		t.Errorf("route có trong code nhưng thiếu ở swagger.json (thêm annotation ở httpapi/swagger.go rồi `make swagger APP=ws-gateway`):\n  %s", strings.Join(inCodeOnly, "\n  "))
	}
	if len(inSpecOnly) > 0 {
		t.Errorf("route có trong swagger.json nhưng không được phục vụ:\n  %s", strings.Join(inSpecOnly, "\n  "))
	}
}
