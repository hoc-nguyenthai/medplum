package bootstrap

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"trivita-fhir/apps/auth/internal/application"
	"trivita-fhir/pkg/fhir"
)

// adminBearer đăng nhập admin của project seed (PKCE) và trả access token thật.
func adminBearer(a *codeAPI) map[string]string {
	a.t.Helper()
	code, _, _ := a.loginCode(loginOpts{challenge: s256(), method: "S256", redirect: redirectURI})
	r := a.exchange(url.Values{"code": {code}, "code_verifier": {verifier}, "client_id": {a.seed.Client.ID}, "redirect_uri": {redirectURI}}, nil)
	if r.status != 200 || r.json()["access_token"] == nil {
		a.t.Fatalf("token admin: %d %s", r.status, r.body)
	}
	return map[string]string{"Authorization": "Bearer " + r.json()["access_token"].(string)}
}

func TestHTTPPreAuthorizeEndToEnd(t *testing.T) {
	a := newCodeAPI(t, nil)
	ctx := context.Background()
	// một thành viên đích khác admin
	tur, err := a.c.Auth.Resources.Create(ctx, &fhir.User{ResourceType: "User", Email: "dich@example.org", FirstName: "Dich", LastName: "Nguyen"})
	if err != nil {
		t.Fatal(err)
	}
	tu := tur.(*fhir.User)
	tm, err := a.c.Auth.Resources.Create(ctx, &fhir.ProjectMembership{ResourceType: "ProjectMembership", User: &fhir.Reference{Reference: "User/" + tu.ID},
		Project: &fhir.Reference{Reference: "Project/" + a.seed.Project.ID}, Profile: &fhir.Reference{Reference: "Practitioner/dich"}})
	if err != nil {
		t.Fatal(err)
	}
	tmID := tm.(*fhir.ProjectMembership).ID
	hdr := adminBearer(a)
	hdr["X-Medplum-On-Behalf-Of"] = "ProjectMembership/" + tmID
	body := `{"clientId":"` + a.seed.Client.ID + `"}`

	// không Bearer: 401
	if r := a.do("POST", "/auth/preauthorize", body, map[string]string{"X-Medplum-On-Behalf-Of": hdr["X-Medplum-On-Behalf-Of"]}); r.status != 401 {
		t.Errorf("không xác thực: %d", r.status)
	}
	r := a.do("POST", "/auth/preauthorize", body, hdr)
	if r.status != 200 || r.hdr.Get("Cache-Control") != "no-store" {
		t.Fatalf("mint: %d %s", r.status, r.body)
	}
	m := r.json()
	code, _ := m["preAuthorizedCode"].(string)
	if code == "" || m["expiresAt"] == nil || m["code"] != nil {
		t.Fatalf("body: %v", m)
	}
	// đổi mã: token cho THÀNH VIÊN ĐÍCH
	tok := a.form("/oauth2/token", url.Values{"grant_type": {application.GrantTypePreAuthorizedCode}, "client_id": {a.seed.Client.ID}, "pre-authorized_code": {code}}, nil)
	if tok.status != 200 || tok.json()["access_token"] == nil || tok.json()["refresh_token"] != nil {
		t.Fatalf("redeem: %d %s", tok.status, tok.body)
	}
	// token đó là của đích: /auth/me
	me := a.do("GET", "/auth/me", "", map[string]string{"Authorization": "Bearer " + tok.json()["access_token"].(string)})
	if me.status != 200 || !strings.Contains(me.body, "dich@example.org") {
		t.Errorf("me: %d %s", me.status, me.body)
	}
	// dùng lại mã: lỗi và THU HỒI luôn token đã cấp
	if again := a.form("/oauth2/token", url.Values{"grant_type": {application.GrantTypePreAuthorizedCode}, "client_id": {a.seed.Client.ID}, "pre-authorized-code": {code}, "pre-authorized_code": {code}}, nil); again.status != 400 || again.json()["error_description"] != "Token already granted" {
		t.Errorf("dùng lại: %d %s", again.status, again.body)
	}
	if me2 := a.do("GET", "/auth/me", "", map[string]string{"Authorization": "Bearer " + tok.json()["access_token"].(string)}); me2.status != 401 {
		t.Errorf("token phải bị thu hồi sau khi mã bị dùng lại: %d", me2.status)
	}
}

func TestHTTPPreAuthorizeValidationAndErrors(t *testing.T) {
	a := newCodeAPI(t, nil)
	hdr := adminBearer(a)
	text := func(r resp) string { s, _ := firstIssue(t, r); return s }
	cid := a.seed.Client.ID
	// validator chạy TRƯỚC kiểm quyền/onBehalfOf
	for name, tc := range map[string]struct{ body, want string }{
		"thiếu clientId":      {`{}`, "Client ID is required"},
		"clientId không uuid": {`{"clientId":"abc"}`, "Client ID is required"},
		"scope sai kiểu":      {`{"clientId":"` + cid + `","scope":5}`, "Scope must be a string"},
		"nonce sai kiểu":      {`{"clientId":"` + cid + `","nonce":5}`, "Nonce must be a string"},
		"expiresIn 0":         {`{"clientId":"` + cid + `","expiresIn":0}`, "expiresIn must be a positive integer not exceeding 604800 seconds"},
		"expiresIn quá lớn":   {`{"clientId":"` + cid + `","expiresIn":604801}`, "expiresIn must be a positive integer not exceeding 604800 seconds"},
		"expiresIn thập phân": {`{"clientId":"` + cid + `","expiresIn":1.5}`, "expiresIn must be a positive integer not exceeding 604800 seconds"},
		"expiresIn chuỗi":     {`{"clientId":"` + cid + `","expiresIn":"5"}`, "expiresIn must be a positive integer not exceeding 604800 seconds"},
	} {
		if r := a.do("POST", "/auth/preauthorize", tc.body, hdr); r.status != 400 || text(r) != tc.want {
			t.Errorf("%s: %d %s", name, r.status, r.body)
		}
	}
	if r := a.do("POST", "/auth/preauthorize", `{"clientId":"`+cid+`"}`, hdr); r.status != 400 || text(r) != "Pre-authorization requires onBehalfOfMembership" {
		t.Errorf("thiếu header: %d %s", r.status, r.body)
	}
	hdr["X-Medplum-On-Behalf-Of"] = "ProjectMembership/khong-co"
	if r := a.do("POST", "/auth/preauthorize", `{"clientId":"`+cid+`"}`, hdr); r.status != 403 {
		t.Errorf("membership lạ: %d %s", r.status, r.body)
	}
	hdr["X-Medplum-On-Behalf-Of"] = "ProjectMembership/" + a.seed.Membership.ID
	if r := a.do("POST", "/auth/preauthorize", `{"clientId":"3f2b8c1e-0000-4000-8000-000000000000"}`, hdr); r.status != 404 {
		t.Errorf("client lạ: %d %s", r.status, r.body)
	}
	if r := a.do("POST", "/auth/preauthorize", `{"clientId":"`+cid+`","scope":"openid user/Observation.rs"}`, hdr); r.status != 200 {
		t.Errorf("admin không giới hạn xin scope SMART: %d %s", r.status, r.body)
	}
}
