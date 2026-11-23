package e2e

import (
	"encoding/base64"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// /admin/projects/* trên tiến trình thật (AUTH_ADMIN_ENABLED): tạo client, mời user, đặt mật khẩu, xóa member; token của member bị xóa bị từ chối ngay ở auth.
func TestAdminAPIThroughRealProcesses(t *testing.T) {
	s := newStackCustom(t, false, map[string]string{"AUTH_ADMIN_ENABLED": "true"}, nil)
	admin := s.signIn("a@example.org", s.clientA)["access_token"].(string)
	base := "/admin/projects/" + s.projectA

	// tạo client qua API quản trị rồi dùng nó gọi fhir-server
	secret := strings.Repeat("k", 40)
	cr := s.bearerJSON(admin, base+"/client", map[string]any{"name": "May", "secret": secret, "grantType": []string{"client_credentials"}})
	if cr.status != 201 {
		t.Fatalf("tạo client: %d %s", cr.status, cr.body)
	}
	cid := cr.json(t)["id"].(string)
	tk := s.token(url.Values{"grant_type": {"client_credentials"}, "client_id": {cid}, "client_secret": {secret}})
	if tk.status != 200 {
		t.Fatalf("token client: %d %s", tk.status, tk.body)
	}
	cc := tk.json(t)["access_token"].(string)
	if r := s.fhirPost(cc, "/Patient", patientJSON); r.status != 201 {
		t.Fatalf("fhir-server với token của client tạo qua admin API: %d %s", r.status, r.body)
	}
	// client mới không phải admin
	if r := do(t, "GET", s.auth+base, cc, "", nil); r.status != 403 {
		t.Errorf("client không phải admin: %d", r.status)
	}
	// project khác: admin của A không đụng được B
	if r := do(t, "GET", s.auth+"/admin/projects/"+s.projectB, admin, "", nil); r.status != 403 {
		t.Errorf("project khác: %d", r.status)
	}

	// mời một Practitioner có mật khẩu, người đó đăng nhập được
	inv := s.bearerJSON(admin, base+"/invite", map[string]any{"resourceType": "Practitioner", "firstName": "Lan", "lastName": "Tran", "email": "lan@corp.example.org",
		"password": "mat-khau-moi-12345", "sendEmail": false})
	if inv.status != 200 {
		t.Fatalf("invite: %d %s", inv.status, inv.body)
	}
	midInv := inv.json(t)["id"].(string)
	lg := s.postJSON("/auth/login", map[string]any{"email": "lan@corp.example.org", "password": "mat-khau-moi-12345", "projectId": s.projectA, "clientId": s.clientA,
		"codeChallenge": challengeS256(), "codeChallengeMethod": "S256"})
	if lg.status != 200 || lg.json(t)["code"] == nil {
		t.Fatalf("đăng nhập người được mời: %d %s", lg.status, lg.body)
	}
	// đặt lại mật khẩu: user ở phạm vi server nên không thuộc project (400 User not found)
	if r := s.bearerJSON(admin, "/admin/projects/setpassword", map[string]any{"email": "lan@corp.example.org", "password": "mat-khau-khac-12345"}); r.status != 400 {
		t.Errorf("setpassword user server-scope: %d %s", r.status, r.body)
	}

	// xóa membership của client: token của nó bị auth từ chối ngay
	me := do(t, "GET", s.auth+"/auth/me", cc, "", nil)
	if me.status != 200 {
		t.Fatalf("me của client: %d %s", me.status, me.body)
	}
	mid := me.json(t)["membership"].(map[string]any)["id"].(string)
	req := do(t, "DELETE", s.auth+base+"/members/"+mid, admin, "", nil)
	if req.status != 200 {
		t.Fatalf("xóa member: %d %s", req.status, req.body)
	}
	if r := do(t, "GET", s.auth+"/auth/me", cc, "", nil); r.status != 401 {
		t.Errorf("token của member đã xóa: %d", r.status)
	}
	// xóa người được mời (user phạm vi server: giữ user, bỏ membership)
	if r := do(t, "DELETE", s.auth+base+"/members/"+midInv, admin, "", nil); r.status != 200 {
		t.Errorf("xóa người được mời: %d %s", r.status, r.body)
	}
}

// SCIM 2.0 và sites trên tiến trình thật: Okta (Basic của client có quyền admin) tạo, tắt, xóa user; tính năng `scim` bật cho project lúc seed
// (AUTH_DEFAULT_PROJECT_FEATURES).
func TestScimAndSitesThroughRealProcesses(t *testing.T) {
	s := newStackCustom(t, false, map[string]string{"AUTH_ADMIN_ENABLED": "true", "AUTH_SCIM_ENABLED": "true", "AUTH_DEFAULT_PROJECT_FEATURES": "scim"}, nil)
	admin := s.signIn("a@example.org", s.clientA)["access_token"].(string)
	base := "/admin/projects/" + s.projectA

	secret := strings.Repeat("o", 40)
	cr := s.bearerJSON(admin, base+"/client", map[string]any{"name": "Okta", "secret": secret, "grantType": []string{"client_credentials"}})
	if cr.status != 201 {
		t.Fatalf("tạo client: %d %s", cr.status, cr.body)
	}
	cid := cr.json(t)["id"].(string)
	basic := "Basic " + base64.StdEncoding.EncodeToString([]byte(cid+":"+secret))
	scimDo := func(method, path, body string) resp {
		t.Helper()
		req, err := http.NewRequest(method, s.auth+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", basic)
		req.Header.Set("Content-Type", "application/scim+json")
		res, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return resp{res.StatusCode, b, res.Header}
	}
	// client mới chưa phải admin: 403
	if r := scimDo("GET", "/scim/v2/Users", ""); r.status != 403 {
		t.Fatalf("client chưa admin: %d %s", r.status, r.body)
	}
	// nâng membership của client lên admin qua API quản trị
	tk := s.token(url.Values{"grant_type": {"client_credentials"}, "client_id": {cid}, "client_secret": {secret}})
	me := do(t, "GET", s.auth+"/auth/me", tk.json(t)["access_token"].(string), "", nil).json(t)
	mid := me["membership"].(map[string]any)["id"].(string)
	mem := do(t, "GET", s.auth+base+"/members/"+mid, admin, "", nil).json(t)
	mem["admin"] = true
	if r := s.bearerJSON(admin, base+"/members/"+mid, mem); r.status != 200 {
		t.Fatalf("nâng admin: %d %s", r.status, r.body)
	}
	if r := scimDo("GET", "/scim/v2/Users", ""); r.status != 200 || r.json(t)["schemas"].([]any)[0] != "urn:ietf:params:scim:api:messages:2.0:ListResponse" {
		t.Fatalf("list: %d %s", r.status, r.body)
	}
	// kịch bản Okta
	c := scimDo("POST", "/scim/v2/Users", `{"schemas":["urn:ietf:params:scim:schemas:core:2.0:User"],"userName":"okta.user@atko.com","name":{"givenName":"Okta","familyName":"User"},"emails":[{"value":"okta.user@atko.com","primary":true}],"active":true}`)
	if c.status != 201 {
		t.Fatalf("create: %d %s", c.status, c.body)
	}
	id := c.json(t)["id"].(string)
	if r := scimDo("POST", "/scim/v2/Users", `{"userName":"okta.user@atko.com","name":{"givenName":"Okta","familyName":"User"},"emails":[{"value":"okta.user@atko.com"}]}`); r.status != 409 {
		t.Errorf("tạo lại: %d %s", r.status, r.body)
	}
	if r := scimDo("GET", "/scim/v2/Users?filter="+url.QueryEscape(`userName eq "OKTA.USER@ATKO.COM"`), ""); r.status != 200 || r.json(t)["totalResults"] != float64(1) {
		t.Errorf("filter: %d %s", r.status, r.body)
	}
	if r := scimDo("PATCH", "/scim/v2/Users/"+id, `{"schemas":["urn:ietf:params:scim:api:messages:2.0:PatchOp"],"Operations":[{"op":"replace","value":{"active":false}}]}`); r.status != 200 || r.json(t)["active"] != false {
		t.Errorf("patch: %d %s", r.status, r.body)
	}
	if r := scimDo("PATCH", "/scim/v2/Users/"+id, `{"schemas":["urn:ietf:params:scim:api:messages:2.0:PatchOp"],"Operations":[{"op":"replace","path":"admin","value":true}]}`); r.status != 400 {
		t.Errorf("patch admin: %d %s", r.status, r.body)
	}
	// SCIM không đụng được membership của chính client (profile ClientApplication) nên 404, kể cả với id của nó
	if r := scimDo("GET", "/scim/v2/Users/"+mid, ""); r.status != 404 {
		t.Errorf("membership của client: %d %s", r.status, r.body)
	}
	if r := scimDo("DELETE", "/scim/v2/Users/"+id, ""); r.status != 204 {
		t.Errorf("delete: %d %s", r.status, r.body)
	}
	if r := scimDo("GET", "/scim/v2/Users/"+id, ""); r.status != 404 {
		t.Errorf("sau xóa: %d", r.status)
	}
	// Basic sai secret: 401 (SCIM Error)
	basic = "Basic " + base64.StdEncoding.EncodeToString([]byte(cid+":sai"))
	if r := scimDo("GET", "/scim/v2/Users", ""); r.status != 401 {
		t.Errorf("sai secret: %d", r.status)
	}

	// sites: đặt rồi đọc lại, secret bị che; trùng googleClientId với project khác là 409
	site := `[{"name":"web","domain":["app.example.org"],"googleClientId":"gid-e2e-a","googleClientSecret":"gs-a"}]`
	r := do(t, "POST", s.auth+base+"/sites", admin, "application/json", strings.NewReader(site))
	if r.status != 200 || strings.Contains(string(r.body), "gs-a") {
		t.Fatalf("sites: %d %s", r.status, r.body)
	}
	if r := do(t, "GET", s.auth+base, admin, "", nil); r.status != 200 || strings.Contains(string(r.body), "gs-a") {
		t.Errorf("GET lộ secret: %d %s", r.status, r.body)
	}
	adminB := s.signIn("b@example.org", s.clientB)["access_token"].(string)
	if r := do(t, "POST", s.auth+"/admin/projects/"+s.projectB+"/sites", adminB, "application/json", strings.NewReader(site)); r.status != 409 {
		t.Errorf("trùng googleClientId giữa project: %d %s", r.status, r.body)
	}
}
