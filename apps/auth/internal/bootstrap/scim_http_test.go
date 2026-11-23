package bootstrap

import (
	"context"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"trivita-fhir/apps/auth/internal/config"
	"trivita-fhir/pkg/fhir"
)

func newScimAPI(t *testing.T, mut func(*config.Settings)) (*codeAPI, map[string]string) {
	t.Helper()
	a := newCodeAPI(t, func(s *config.Settings) {
		s.ScimEnabled = true
		if mut != nil {
			mut(s)
		}
	})
	if _, err := a.c.Auth.Projects.Mutate(context.Background(), a.seed.Project.ID, func(p *fhir.Project) error { p.Features = append(p.Features, "scim"); return nil }); err != nil {
		t.Fatal(err)
	}
	return a, adminBearer(a)
}

func scimHdr(h map[string]string, extra ...string) map[string]string {
	out := map[string]string{"Content-Type": "application/scim+json"}
	for k, v := range h {
		out[k] = v
	}
	for i := 0; i+1 < len(extra); i += 2 {
		out[extra[i]] = extra[i+1]
	}
	return out
}

func scimErrOf(t *testing.T, r resp, status int, scimType string) map[string]any {
	t.Helper()
	m := r.json()
	if r.status != status || m["status"] != itoa(status) || (scimType != "" && m["scimType"] != scimType) ||
		m["schemas"].([]any)[0] != "urn:ietf:params:scim:api:messages:2.0:Error" || !strings.HasPrefix(r.hdr.Get("Content-Type"), "application/scim+json") {
		t.Fatalf("mong SCIM Error %d %s: %d %s (%s)", status, scimType, r.status, r.body, r.hdr.Get("Content-Type"))
	}
	return m
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestHTTPScimDisabledByDefault(t *testing.T) {
	a := newCodeAPI(t, nil)
	hdr := adminBearer(a)
	if r := a.do("GET", "/scim/v2/Users", "", hdr); r.status != 404 {
		t.Errorf("tắt thì 404: %d", r.status)
	}
}

func TestHTTPScimAuthentication(t *testing.T) {
	a, hdr := newScimAPI(t, nil)
	r := a.do("GET", "/scim/v2/Users", "", nil)
	scimErrOf(t, r, 401, "")
	if v := r.hdr.Values("Www-Authenticate"); len(v) != 2 || !strings.HasPrefix(v[0], "Basic") || !strings.HasPrefix(v[1], "Bearer") {
		t.Errorf("WWW-Authenticate: %v", v)
	}
	scimErrOf(t, a.do("GET", "/scim/v2/Users", "", map[string]string{"Authorization": "Bearer sai"}), 401, "")
	scimErrOf(t, a.do("GET", "/scim/v2/Users", "", map[string]string{"Authorization": "Basic !!!"}), 401, "")
	scimErrOf(t, a.do("GET", "/scim/v2/Users", "", map[string]string{"Authorization": "Basic " + b64("khong-co:x")}), 401, "")
	scimErrOf(t, a.do("GET", "/scim/v2/Users", "", map[string]string{"Authorization": "Basic " + b64(a.seed.Client.ID+":sai")}), 401, "")
	scimErrOf(t, a.do("GET", "/scim/v2/Users", "", map[string]string{"Authorization": "Basic " + b64(":")}), 401, "")
	// client mặc định không phải admin: 403 (SCIM Error)
	basic := map[string]string{"Authorization": "Basic " + b64(a.seed.Client.ID+":"+a.seed.Client.Secret)}
	scimErrOf(t, a.do("GET", "/scim/v2/Users", "", basic), 403, "")
	// nâng membership của client lên admin: Basic dùng được
	cl := "ClientApplication/" + a.seed.Client.ID
	ms, err := a.c.Auth.Memberships.ListForUser(context.Background(), cl, "", 5)
	if err != nil || len(ms) != 1 {
		t.Fatalf("membership của client: %v %v", ms, err)
	}
	if _, err := a.c.Auth.Memberships.Mutate(context.Background(), ms[0].ID, func(m *fhir.ProjectMembership) error { m.Admin = ptr2(true); return nil }); err != nil {
		t.Fatal(err)
	}
	ok := a.do("GET", "/scim/v2/Users", "", basic)
	if ok.status != 200 || !strings.HasPrefix(ok.hdr.Get("Content-Type"), "application/scim+json") || ok.json()["schemas"].([]any)[0] != "urn:ietf:params:scim:api:messages:2.0:ListResponse" {
		t.Fatalf("Basic admin: %d %s", ok.status, ok.body)
	}
	if r := a.do("GET", "/scim/v2/Users", "", hdr); r.status != 200 {
		t.Errorf("Bearer admin: %d %s", r.status, r.body)
	}
	// AllowBasic=false
	b, _ := newScimAPI(t, func(s *config.Settings) { s.ScimAllowBasic = false })
	scimErrOf(t, b.do("GET", "/scim/v2/Users", "", map[string]string{"Authorization": "Basic " + b64(b.seed.Client.ID+":"+b.seed.Client.Secret)}), 401, "")
}

func ptr2(b bool) *bool { return &b }

func TestHTTPScimIPAllowlist(t *testing.T) {
	a, hdr := newScimAPI(t, func(s *config.Settings) { s.ScimAllowedIPs = "10.0.0.0/8" })
	// httptest gọi từ 127.0.0.1: ngoài danh sách
	scimErrOf(t, a.do("GET", "/scim/v2/Users", "", hdr), 403, "")
	b, hdr2 := newScimAPI(t, func(s *config.Settings) { s.ScimAllowedIPs = "127.0.0.0/8, ::1/128" })
	if r := b.do("GET", "/scim/v2/Users", "", hdr2); r.status != 200 {
		t.Errorf("trong danh sách: %d %s", r.status, r.body)
	}
}

func TestHTTPScimOktaFlow(t *testing.T) {
	a, hdr := newScimAPI(t, nil)
	h := scimHdr(hdr)
	body := `{"schemas":["urn:ietf:params:scim:schemas:core:2.0:User"],"userName":"Okta.User@atko.com","name":{"givenName":"Okta","familyName":"User"},"emails":[{"primary":true,"value":"Okta.User@atko.com","type":"work"}],"displayName":"Okta User","active":true,"groups":[{"value":"g"}],"admin":true}`
	r := a.do("POST", "/scim/v2/Users", body, h)
	if r.status != 201 || r.hdr.Get("Location") == "" || !strings.HasPrefix(r.hdr.Get("Content-Type"), "application/scim+json") {
		t.Fatalf("create: %d %s", r.status, r.body)
	}
	c := r.json()
	id := c["id"].(string)
	if c["userName"] != "Okta.User@atko.com" || c["active"] != true || strings.Contains(r.body, "passwordHash") || !strings.HasSuffix(c["meta"].(map[string]any)["location"].(string), "/scim/v2/Users/"+id) {
		t.Fatalf("body: %s", r.body)
	}
	// Content-Type application/json cũng được
	if r := a.do("GET", "/scim/v2/Users/"+id, "", hdr); r.status != 200 || r.json()["id"] != id {
		t.Errorf("get: %d %s", r.status, r.body)
	}
	scimErrOf(t, a.do("POST", "/scim/v2/Users", body, h), 409, "uniqueness")
	// danh sách + filter hoa + filter lạ
	flt := url.QueryEscape(`userName eq "OKTA.USER@ATKO.COM"`)
	if r := a.do("GET", "/scim/v2/Users?filter="+flt, "", hdr); r.status != 200 || r.json()["totalResults"] != float64(1) {
		t.Errorf("filter: %d %s", r.status, r.body)
	}
	scimErrOf(t, a.do("GET", "/scim/v2/Users?filter="+url.QueryEscape(`active eq true`), "", hdr), 400, "invalidFilter")
	scimErrOf(t, a.do("GET", "/scim/v2/Users?count=abc", "", hdr), 400, "invalidValue")
	scimErrOf(t, a.do("GET", "/scim/v2/Users?startIndex=x", "", hdr), 400, "invalidValue")
	if r := a.do("GET", "/scim/v2/Users?count=1&startIndex=1", "", hdr); r.status != 200 || len(r.json()["Resources"].([]any)) != 1 {
		t.Errorf("phân trang: %d %s", r.status, r.body)
	}
	scimErrOf(t, a.do("GET", "/scim/v2/Users/khong-co", "", hdr), 404, "")
	// quyền không bị nâng bằng body
	ms, _ := a.c.Auth.Memberships.ListByProject(context.Background(), "Project/"+a.seed.Project.ID)
	for _, m := range ms {
		if m.ID == id && (m.Admin != nil && *m.Admin) {
			t.Errorf("body có admin:true không được nâng quyền: %+v", m)
		}
	}
	// PATCH kiểu Okta: tắt
	p := `{"schemas":["urn:ietf:params:scim:api:messages:2.0:PatchOp"],"Operations":[{"op":"replace","value":{"active":false}}]}`
	if r := a.do("PATCH", "/scim/v2/Users/"+id, p, h); r.status != 200 || r.json()["active"] != false {
		t.Errorf("patch: %d %s", r.status, r.body)
	}
	scimErrOf(t, a.do("PATCH", "/scim/v2/Users/"+id, `{"schemas":["urn:ietf:params:scim:api:messages:2.0:PatchOp"],"Operations":[{"op":"replace","path":"admin","value":true}]}`, h), 400, "invalidPath")
	scimErrOf(t, a.do("PATCH", "/scim/v2/Users/"+id, `{"Operations":[]}`, h), 400, "invalidSyntax")
	// PUT: id body khác
	scimErrOf(t, a.do("PUT", "/scim/v2/Users/"+id, `{"id":"khac","emails":[{"value":"x@y.org"}]}`, h), 400, "invalidValue")
	if r := a.do("PUT", "/scim/v2/Users/"+id, `{"externalId":"ext-1","active":true}`, h); r.status != 200 || r.json()["externalId"] != "ext-1" {
		t.Errorf("put: %d %s", r.status, r.body)
	}
	// xóa
	if r := a.do("DELETE", "/scim/v2/Users/"+id, "", hdr); r.status != 204 || r.body != "" {
		t.Errorf("delete: %d %q", r.status, r.body)
	}
	scimErrOf(t, a.do("GET", "/scim/v2/Users/"+id, "", hdr), 404, "")
}

func TestHTTPScimRequestHandling(t *testing.T) {
	a, hdr := newScimAPI(t, nil)
	post := func(ct, body string) resp {
		return a.do("POST", "/scim/v2/Users", body, map[string]string{"Authorization": hdr["Authorization"], "Content-Type": ct})
	}
	scimErrOf(t, post("text/plain", `{}`), 415, "")
	scimErrOf(t, post("", `{}`), 415, "")
	scimErrOf(t, post("application/scim+json; charset=utf-8", `không phải json`), 400, "invalidSyntax")
	scimErrOf(t, post("application/json", `{"userName":"x"}`), 400, "invalidValue") // thiếu email
	// body quá lớn: 413
	huge := `{"userName":"` + strings.Repeat("a", 2<<20) + `"}`
	small, _ := newScimAPI(t, func(s *config.Settings) { s.ScimMaxBodyBytes = 1024 })
	r := small.do("POST", "/scim/v2/Users", `{"userName":"`+strings.Repeat("a", 5000)+`"}`, map[string]string{"Authorization": adminBearer(small)["Authorization"], "Content-Type": "application/json"})
	if r.status != 413 {
		t.Errorf("body lớn phải 413: %d %s", r.status, r.body)
	}
	_ = huge
	// project chưa bật tính năng scim
	b := newCodeAPI(t, func(s *config.Settings) { s.ScimEnabled = true })
	scimErrOf(t, b.do("GET", "/scim/v2/Users", "", adminBearer(b)), 403, "")
}

func TestHTTPScimSharesTheLoginRateLimit(t *testing.T) {
	a, _ := newScimAPI(t, func(s *config.Settings) { s.LoginRateLimit, s.LoginRateBurst = 0.001, 3 })
	bad := map[string]string{"Authorization": "Basic " + b64(a.seed.Client.ID+":sai")}
	for i := 0; i < 3; i++ {
		scimErrOf(t, a.do("GET", "/scim/v2/Users", "", bad), 401, "")
	}
	// hết hạn mức: dò secret của client qua Basic bị chặn
	if r := a.do("GET", "/scim/v2/Users", "", bad); r.status != 429 {
		t.Errorf("phải 429: %d %s", r.status, r.body)
	}
}
