package bootstrap

import (
	"encoding/json"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"trivita-fhir/apps/auth/internal/config"
)

var echoParam = regexp.MustCompile(`:([A-Za-z0-9_]+)`)

// Hợp đồng API (docs/swagger.json, từ đó sinh docs/api-contract.md) phải khớp tuyệt đối với route thật khi mọi tính năng tùy chọn đều bật:
// route có trong code mà thiếu ở spec (hoặc ngược lại) làm test đỏ. Route ngoài hợp đồng (swagger UI, health phụ) liệt kê ở outsideContract.
func TestRoutesMatchSwaggerContract(t *testing.T) {
	c := newContainer(t, func(s *config.Settings) {
		s.PwnedEnabled = false
		s.AppBaseURL = config.DevAllowedOrigin // bật /oauth2/authorize và đăng nhập ngoài
		s.OAuthRegisterEnabled = true
		s.AdminEnabled = true
		s.ScimEnabled = true
		s.EmailEnabled = true
		s.GoogleEnabled = true
	})
	e, err := c.Handler(nil)
	if err != nil {
		t.Fatal(err)
	}
	outsideContract := func(path string) bool {
		return strings.HasPrefix(path, "/swagger") || path == "/healthz" || path == "/health/startup" || strings.Contains(path, "*")
	}
	served := map[string]bool{}
	for _, r := range e.Routes() {
		path := echoParam.ReplaceAllString(r.Path, "{$1}")
		if outsideContract(path) || r.Method == "echo_route_not_found" || r.Method == "HEAD" || r.Method == "OPTIONS" || r.Method == "CONNECT" || r.Method == "TRACE" || r.Method == "PROPFIND" || r.Method == "REPORT" {
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
		for m := range ms {
			documented[m+" "+p] = true
		}
	}

	var missingInSpec, missingInCode []string
	for k := range served {
		if !documented[k] {
			missingInSpec = append(missingInSpec, k)
		}
	}
	for k := range documented {
		if !served[k] {
			missingInCode = append(missingInCode, k)
		}
	}
	sort.Strings(missingInSpec)
	sort.Strings(missingInCode)
	if len(missingInSpec) > 0 {
		t.Errorf("route có trong code nhưng thiếu ở swagger.json (thêm annotation rồi `make swagger APP=auth`):\n  %s", strings.Join(missingInSpec, "\n  "))
	}
	if len(missingInCode) > 0 {
		t.Errorf("route có trong swagger.json nhưng không được phục vụ (đã bật mọi tính năng tùy chọn):\n  %s", strings.Join(missingInCode, "\n  "))
	}
}
