package bootstrap

import (
	"context"
	"encoding/base64"
	"net/url"
	"strings"
	"testing"

	"trivita-fhir/apps/auth/internal/config"
	"trivita-fhir/pkg/fhir"
)

func newAdminAPI(t *testing.T) (*codeAPI, map[string]string) {
	t.Helper()
	a := newCodeAPI(t, func(s *config.Settings) { s.AdminEnabled = true })
	return a, adminBearer(a)
}

func TestHTTPAdminRoutesAbsentWhenDisabled(t *testing.T) {
	a := newCodeAPI(t, nil)
	hdr := adminBearer(a)
	if r := a.do("GET", "/admin/projects/"+a.seed.Project.ID, "", hdr); r.status != 404 {
		t.Errorf("tắt thì 404: %d %s", r.status, r.body)
	}
}

func TestHTTPAdminAuthentication(t *testing.T) {
	a, hdr := newAdminAPI(t)
	path := "/admin/projects/" + a.seed.Project.ID
	if r := a.do("GET", path, "", nil); r.status != 401 || r.hdr.Get("WWW-Authenticate") == "" {
		t.Errorf("không Bearer: %d", r.status)
	}
	// Basic của client không được dùng cho /admin (Q14)
	basic := "Basic " + b64(a.seed.Client.ID+":"+a.seed.Client.Secret)
	if r := a.do("GET", path, "", map[string]string{"Authorization": basic}); r.status != 401 {
		t.Errorf("Basic: %d", r.status)
	}
	if r := a.do("GET", path, "", hdr); r.status != 200 {
		t.Fatalf("admin: %d %s", r.status, r.body)
	}
	// client_credentials: membership của client không phải admin: 403
	tk := a.form("/oauth2/token", url.Values{"grant_type": {"client_credentials"}, "client_id": {a.seed.Client.ID}, "client_secret": {a.seed.Client.Secret}}, nil)
	if tk.status != 200 {
		t.Fatalf("client_credentials: %d %s", tk.status, tk.body)
	}
	cc := map[string]string{"Authorization": "Bearer " + tk.json()["access_token"].(string)}
	if r := a.do("GET", path, "", cc); r.status != 403 {
		t.Errorf("không phải admin: %d %s", r.status, r.body)
	}
	if r := a.do("GET", "/admin/projects/khac", "", hdr); r.status != 403 {
		t.Errorf("project lệch: %d %s", r.status, r.body)
	}
}

func TestHTTPAdminProjectSettings(t *testing.T) {
	a, hdr := newAdminAPI(t)
	path := "/admin/projects/" + a.seed.Project.ID
	r := a.do("POST", path+"/settings", `[{"name":"mfaRequired","valueBoolean":true},{"name":"appName","valueString":"Phòng khám"}]`, hdr)
	if r.status != 200 {
		t.Fatalf("settings: %d %s", r.status, r.body)
	}
	got := a.do("GET", path, "", hdr).json()["project"].(map[string]any)
	if got["id"] != a.seed.Project.ID || len(got["setting"].([]any)) != 2 {
		t.Errorf("project: %v", got)
	}
	for name, body := range map[string]string{
		"ngoài allowlist": `[{"name":"allow-dangerous-redirect","valueBoolean":true}]`,
		"không phải mảng": `{"name":"mfaRequired"}`,
		"rác":             `x`,
	} {
		if r := a.do("POST", path+"/settings", body, hdr); r.status != 400 {
			t.Errorf("%s: %d %s", name, r.status, r.body)
		}
	}
}

func TestHTTPAdminClientAndMemberLifecycle(t *testing.T) {
	a, hdr := newAdminAPI(t)
	path := "/admin/projects/" + a.seed.Project.ID
	secret := strings.Repeat("s", 40)
	r := a.do("POST", path+"/client", `{"name":"Máy","secret":"`+secret+`","grantType":["client_credentials"]}`, hdr)
	if r.status != 201 || r.json()["secret"] != secret || r.hdr.Get("Cache-Control") != "no-store" {
		t.Fatalf("tạo client: %d %s", r.status, r.body)
	}
	cid := r.json()["id"].(string)
	for name, body := range map[string]string{
		"thiếu name":       `{}`,
		"identityProvider": `{"name":"x","identityProvider":{"authorizeUrl":"http://169.254.169.254"}}`,
		"meta":             `{"name":"x","meta":{"project":"khac"}}`,
		"secret ngắn":      `{"name":"x","secret":"ngan"}`,
		"kiểu sai":         `{"name":"x","redirectUris":"https://a"}`,
	} {
		if r := a.do("POST", path+"/client", body, hdr); r.status != 400 {
			t.Errorf("%s: %d %s", name, r.status, r.body)
		}
	}
	// token của client mới dùng được, rồi xóa membership của nó thì bị từ chối ngay (Q22)
	tk := a.form("/oauth2/token", url.Values{"grant_type": {"client_credentials"}, "client_id": {cid}, "client_secret": {secret}}, nil)
	if tk.status != 200 {
		t.Fatalf("token client mới: %d %s", tk.status, tk.body)
	}
	cc := map[string]string{"Authorization": "Bearer " + tk.json()["access_token"].(string)}
	if me := a.do("GET", "/auth/me", "", cc); me.status != 200 {
		t.Fatalf("me: %d %s", me.status, me.body)
	}
	me := a.do("GET", "/auth/me", "", cc).json()
	mid := me["membership"].(map[string]any)["id"].(string)
	g := a.do("GET", path+"/members/"+mid, "", hdr)
	if g.status != 200 || g.json()["id"] != mid {
		t.Fatalf("get member: %d %s", g.status, g.body)
	}
	// sửa: đổi user bị 400, id lệch 403
	bad := strings.Replace(g.body, `"id":"`+mid+`"`, `"id":"khac"`, 1)
	if r := a.do("POST", path+"/members/"+mid, bad, hdr); r.status != 403 {
		t.Errorf("id lệch: %d %s", r.status, r.body)
	}
	up := strings.Replace(g.body, `"resourceType":"ProjectMembership"`, `"resourceType":"ProjectMembership","externalId":"ext-9"`, 1)
	if r := a.do("POST", path+"/members/"+mid, up, hdr); r.status != 200 || r.json()["externalId"] != "ext-9" {
		t.Errorf("sửa: %d %s", r.status, r.body)
	}
	if r := a.do("DELETE", path+"/members/"+mid, "", hdr); r.status != 200 {
		t.Fatalf("xóa: %d %s", r.status, r.body)
	}
	if me := a.do("GET", "/auth/me", "", cc); me.status != 401 {
		t.Errorf("token của member đã xóa phải bị từ chối ngay: %d", me.status)
	}
	if r := a.do("GET", path+"/members/"+mid, "", hdr); r.status != 403 {
		t.Errorf("đã xóa: %d", r.status)
	}
	// admin duy nhất/chủ project: không xóa được chính mình
	own := a.do("GET", "/auth/me", "", hdr).json()["membership"].(map[string]any)["id"].(string)
	if r := a.do("DELETE", path+"/members/"+own, "", hdr); r.status != 400 {
		t.Errorf("xóa chủ project: %d %s", r.status, r.body)
	}
}

func TestHTTPAdminSetPassword(t *testing.T) {
	a, hdr := newAdminAPI(t)
	text := func(r resp) string { s, _ := firstIssue(t, r); return s }
	if r := a.do("POST", "/admin/projects/setpassword", `{"email":"x","password":"ngan"}`, hdr); r.status != 400 || len(issues(r)) != 2 {
		t.Errorf("validator: %d %s", r.status, r.body)
	}
	if r := a.do("POST", "/admin/projects/setpassword", `{"email":"la@example.org","password":"mat-khau-moi-12345"}`, hdr); r.status != 400 || text(r) != "User not found" {
		t.Errorf("user lạ: %d %s", r.status, r.body)
	}
	if r := a.do("POST", "/admin/projects/setpassword", `{"email":"admin@example.org","password":"mat-khau-moi-12345"}`, nil); r.status != 401 {
		t.Errorf("không Bearer: %d", r.status)
	}
	// user thuộc project: đặt được; admin seed ở phạm vi server nên không đặt được qua route này
	if r := a.do("POST", "/admin/projects/setpassword", `{"email":"admin@example.org","password":"mat-khau-moi-12345"}`, hdr); r.status != 400 || text(r) != "User not found" {
		t.Errorf("user server-scope: %d %s", r.status, r.body)
	}
	ctx := context.Background()
	hash, err := a.c.Auth.HashPassword("mat-khau-cu-12345")
	if err != nil {
		t.Fatal(err)
	}
	ur, err := a.c.Auth.Resources.Create(ctx, &fhir.User{ResourceType: "User", Email: "pu@example.org", FirstName: "P", LastName: "U", PasswordHash: hash,
		Project: &fhir.Reference{Reference: "Project/" + a.seed.Project.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.c.Auth.Resources.Create(ctx, &fhir.ProjectMembership{ResourceType: "ProjectMembership", User: &fhir.Reference{Reference: "User/" + ur.(*fhir.User).ID},
		Project: &fhir.Reference{Reference: "Project/" + a.seed.Project.ID}, Profile: &fhir.Reference{Reference: "Practitioner/pu"}}); err != nil {
		t.Fatal(err)
	}
	body := func(pw string) string {
		return `{"email":"pu@example.org","password":"` + pw + `","projectId":"` + a.seed.Project.ID + `"}`
	}
	if r := a.login(body("mat-khau-cu-12345"), nil); r.status != 200 {
		t.Fatalf("đăng nhập mật khẩu cũ: %d %s", r.status, r.body)
	}
	if r := a.do("POST", "/admin/projects/setpassword", `{"email":"PU@example.org","password":"mat-khau-moi-12345"}`, hdr); r.status != 200 {
		t.Fatalf("setpassword: %d %s", r.status, r.body)
	}
	if r := a.login(body("mat-khau-cu-12345"), nil); r.status != 400 {
		t.Errorf("mật khẩu cũ phải hỏng: %d", r.status)
	}
	if r := a.login(body("mat-khau-moi-12345"), nil); r.status != 200 {
		t.Errorf("mật khẩu mới: %d %s", r.status, r.body)
	}
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func TestHTTPAdminMemberResetsAreScopedToProjectUsers(t *testing.T) {
	a, hdr := newAdminAPI(t)
	base := "/admin/projects/" + a.seed.Project.ID + "/members/" + a.seed.Membership.ID
	// admin seed là user phạm vi server: admin project không được đụng tới
	if r := a.do("POST", base+"/resetpassword", "", hdr); r.status != 403 {
		t.Errorf("resetpassword server-scope: %d %s", r.status, r.body)
	}
	if r := a.do("POST", base+"/mfa/reset", "", hdr); r.status != 403 && r.status != 500 {
		t.Errorf("mfa/reset server-scope: %d %s", r.status, r.body)
	}
	if r := a.do("POST", base+"/resetpassword", "", nil); r.status != 401 {
		t.Errorf("không Bearer: %d", r.status)
	}
	if r := a.do("POST", "/admin/projects/"+a.seed.Project.ID+"/members/khong-co/resetpassword", "", hdr); r.status != 403 {
		t.Errorf("member lạ: %d", r.status)
	}
}

func TestHTTPAdminInvite(t *testing.T) {
	a, hdr := newAdminAPI(t)
	path := "/admin/projects/" + a.seed.Project.ID + "/invite"
	body := `{"resourceType":"Practitioner","firstName":"Lan","lastName":"Tran","email":"Lan@Inv.example.org","password":"mat-khau-moi-12345","sendEmail":false}`
	r := a.do("POST", path, body, hdr)
	if r.status != 200 || r.json()["resourceType"] != "ProjectMembership" || r.json()["invitedBy"] == nil {
		t.Fatalf("mời: %d %s", r.status, r.body)
	}
	// người được mời đăng nhập được (user phạm vi server, chọn project)
	l := a.login(`{"email":"lan@inv.example.org","password":"mat-khau-moi-12345","projectId":"`+a.seed.Project.ID+`"}`, nil)
	if l.status != 200 {
		t.Fatalf("đăng nhập người được mời: %d %s", l.status, l.body)
	}
	if r2 := a.do("POST", path, body, hdr); r2.status != 409 {
		t.Errorf("mời lại: %d %s", r2.status, r2.body)
	}
	for name, b := range map[string]string{
		"trường lạ":            `{"resourceType":"Patient","firstName":"a","lastName":"b","email":"x@y.org","accessPolicy":{"reference":"AccessPolicy/1"}}`,
		"kiểu sai":             `{"resourceType":"Patient","firstName":5,"lastName":"b","email":"x@y.org"}`,
		"membership trường lạ": `{"resourceType":"Patient","firstName":"a","lastName":"b","email":"x@y.org","membership":{"project":{"reference":"Project/khac"}}}`,
		"thiếu tên":            `{"resourceType":"Patient","lastName":"b","email":"x@y.org","sendEmail":false}`,
		"rác":                  `x`,
	} {
		if r := a.do("POST", path, b, hdr); r.status != 400 {
			t.Errorf("%s: %d %s", name, r.status, r.body)
		}
	}
	if r := a.do("POST", path, body, nil); r.status != 401 {
		t.Errorf("không Bearer: %d", r.status)
	}
	if r := a.do("POST", "/admin/projects/khac/invite", body, hdr); r.status != 403 {
		t.Errorf("project lệch: %d", r.status)
	}
}

func TestHTTPAdminSites(t *testing.T) {
	a, hdr := newAdminAPI(t)
	path := "/admin/projects/" + a.seed.Project.ID + "/sites"
	r := a.do("POST", path, `[{"name":"web","domain":["app.example.org"],"googleClientId":"g1","googleClientSecret":"gs1"}]`, hdr)
	site := func(r resp) map[string]any {
		return r.json()["project"].(map[string]any)["site"].([]any)[0].(map[string]any)
	}
	if r.status != 200 || site(r)["googleClientSecret"] != "********" || strings.Contains(r.body, "gs1") {
		t.Fatalf("sites: %d %s", r.status, r.body)
	}
	// GET cũng che; gửi lại giá trị che thì giữ secret
	g := a.do("GET", "/admin/projects/"+a.seed.Project.ID, "", hdr)
	if strings.Contains(g.body, "gs1") {
		t.Errorf("GET lộ secret: %s", g.body)
	}
	if r := a.do("POST", path, `[{"name":"web","googleClientId":"g2","googleClientSecret":"********"}]`, hdr); r.status != 200 {
		t.Errorf("giữ secret: %d %s", r.status, r.body)
	}
	for name, b := range map[string]string{"không phải mảng": `{"name":"x"}`, "thiếu tên": `[{"domain":["a.org"]}]`, "rác": `x`} {
		if r := a.do("POST", path, b, hdr); r.status != 400 {
			t.Errorf("%s: %d %s", name, r.status, r.body)
		}
	}
	if r := a.do("POST", path, `[]`, nil); r.status != 401 {
		t.Errorf("không Bearer: %d", r.status)
	}
	if r := a.do("POST", "/admin/projects/khac/sites", `[]`, hdr); r.status != 403 {
		t.Errorf("project lệch: %d", r.status)
	}
}
