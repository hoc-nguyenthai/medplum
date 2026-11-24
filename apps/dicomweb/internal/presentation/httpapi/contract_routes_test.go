package httpapi_test

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"trivita-fhir/apps/dicomweb/internal/presentation/httpapi"
	"trivita-fhir/pkg/apicontract"
)

var echoParam = regexp.MustCompile(`:([A-Za-z0-9_]+)`)

// Hợp đồng API (docs/swagger.json, từ đó sinh docs/api-contract.md) phải khớp với route thật: route có trong code mà thiếu ở spec (hoặc ngược lại) làm test đỏ.
// Quy ước: spec chỉ liệt kê tiền tố /dicomweb (ba tiền tố còn lại trỏ vào cùng handler và được mô tả ở tag dicomweb), và route bắt mọi đường dẫn
// DICOMweb chưa cài (`/*`, trả 501) không đưa vào spec.
func TestRoutesMatchSwaggerContract(t *testing.T) {
	e := newHandler(t).(*echo.Echo)
	var served []string
	for _, r := range e.Routes() {
		path := echoParam.ReplaceAllString(r.Path, "{$1}")
		switch {
		case r.Method == "echo_route_not_found" || r.Method == "HEAD" || r.Method == "OPTIONS" || r.Method == "CONNECT" || r.Method == "TRACE" || r.Method == "PROPFIND" || r.Method == "REPORT":
			continue
		case strings.HasSuffix(path, "/*") && !strings.HasPrefix(path, "/swagger"), path == "/dicomweb":
			continue // route bắt mọi đường dẫn chưa cài
		case strings.HasPrefix(path, "/swagger/"):
			continue // giao diện Swagger UI, không thuộc hợp đồng
		case strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/projects/"):
			continue // tiền tố tương đương của /dicomweb
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
		t.Errorf("route có trong code nhưng thiếu ở swagger.json (thêm annotation ở httpapi/swagger.go rồi `make swagger APP=dicomweb`):\n  %s", strings.Join(inCodeOnly, "\n  "))
	}
	if len(inSpecOnly) > 0 {
		t.Errorf("route có trong swagger.json nhưng không được phục vụ:\n  %s", strings.Join(inSpecOnly, "\n  "))
	}
}

// Bốn tiền tố phải phục vụ cùng một tập route (ba tiền tố sau không có ở spec nên dễ lệch).
func TestAllPrefixesServeTheSameRoutes(t *testing.T) {
	e := newHandler(t).(*echo.Echo)
	byPrefix := map[string]map[string]bool{}
	for _, r := range e.Routes() {
		for _, p := range httpapi.Prefixes {
			if strings.HasPrefix(r.Path, p) {
				if byPrefix[p] == nil {
					byPrefix[p] = map[string]bool{}
				}
				byPrefix[p][r.Method+" "+strings.TrimPrefix(r.Path, p)] = true
			}
		}
	}
	ref := byPrefix["/dicomweb"]
	if len(ref) == 0 {
		t.Fatal("không có route dưới /dicomweb")
	}
	for _, p := range httpapi.Prefixes[1:] {
		if len(byPrefix[p]) != len(ref) {
			t.Errorf("%s có %d route, /dicomweb có %d", p, len(byPrefix[p]), len(ref))
		}
		for k := range ref {
			if !byPrefix[p][k] {
				t.Errorf("%s thiếu route %s", p, k)
			}
		}
	}
}
