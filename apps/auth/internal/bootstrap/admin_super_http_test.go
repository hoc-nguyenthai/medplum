package bootstrap

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"trivita-fhir/apps/auth/internal/config"
)

func TestHTTPSuperAdminRoutesAbsentWhenDisabled(t *testing.T) {
	a := newCodeAPI(t, nil)
	if r := a.do("POST", "/admin/super/projects", `{"name":"X"}`, adminBearer(a)); r.status != 404 {
		t.Errorf("tắt thì 404: %d %s", r.status, r.body)
	}
}

// Project/$init qua HTTP: chỉ admin của project superAdmin; project mới dùng được ngay (client mặc định lấy được token), không thừa kế superAdmin.
func TestHTTPSuperAdminInitProject(t *testing.T) {
	a := newCodeAPI(t, func(s *config.Settings) { s.AdminEnabled, s.SuperAdminEnabled = true, true })
	hdr := adminBearer(a)
	if r := a.do("POST", "/admin/super/projects", "", nil); r.status != 401 {
		t.Errorf("không Bearer: %d", r.status)
	}
	if r := a.do("POST", "/admin/super/projects", `{"name":"B"}`, hdr); r.status != 403 {
		t.Fatalf("project thường phải 403: %d %s", r.status, r.body)
	}
	if err := a.c.Auth.SetProjectSuperAdmin(context.Background(), a.seed.Project.ID, true); err != nil {
		t.Fatal(err)
	}
	hdr = adminBearer(a) // token mới sau khi project được đánh dấu

	for name, body := range map[string]string{
		"không phải JSON": `x`,
		"thiếu tên":       `{}`,
		"trường lạ":       `{"name":"B","superAdmin":true}`,
		"chủ không có":    `{"name":"B","ownerEmail":"khong-co@example.org"}`,
		"tính năng sai":   `{"name":"B","features":["Sai!"]}`,
	} {
		if r := a.do("POST", "/admin/super/projects", body, hdr); r.status != 400 {
			t.Errorf("%s: %d %s", name, r.status, r.body)
		}
	}

	r := a.do("POST", "/admin/super/projects", `{"name":"Bệnh viện B","features":["scim"]}`, hdr)
	if r.status != 201 || r.hdr.Get("Cache-Control") != "no-store" {
		t.Fatalf("tạo: %d %s %q", r.status, r.body, r.hdr.Get("Cache-Control"))
	}
	m := r.json()
	pid, _ := m["projectId"].(string)
	secret, _ := m["clientSecret"].(string)
	if pid == "" || pid == a.seed.Project.ID || m["name"] != "Bệnh viện B" || !strings.HasPrefix(m["owner"].(string), "User/") || m["membershipId"] == "" || len(secret) < 32 {
		t.Fatalf("phản hồi: %s", r.body)
	}
	// project mới đăng nhập được bằng client mặc định, và không phải super-admin
	tk := a.form("/oauth2/token", url.Values{"grant_type": {"client_credentials"}, "client_id": {m["clientId"].(string)}, "client_secret": {secret}}, nil)
	if tk.status != 200 {
		t.Fatalf("client mặc định của project mới: %d %s", tk.status, tk.body)
	}
	cc := map[string]string{"Authorization": "Bearer " + tk.json()["access_token"].(string)}
	if r := a.do("POST", "/admin/super/projects", `{"name":"C"}`, cc); r.status != 403 {
		t.Errorf("project mới không phải super-admin: %d", r.status)
	}
	got, err := a.c.Auth.Projects.Get(context.Background(), pid)
	if err != nil || len(got.Features) != 1 || got.Features[0] != "scim" || got.SuperAdmin != nil {
		t.Fatalf("project lưu: %+v %v", got, err)
	}
	// bỏ cờ superAdmin có hiệu lực ngay với token đang dùng (project đọc lại từ kho mỗi yêu cầu)
	if err := a.c.Auth.SetProjectSuperAdmin(context.Background(), a.seed.Project.ID, false); err != nil {
		t.Fatal(err)
	}
	if r := a.do("POST", "/admin/super/projects", `{"name":"D"}`, hdr); r.status != 403 {
		t.Errorf("bỏ cờ phải 403 ngay: %d %s", r.status, r.body)
	}
}

func TestProjectSuperCommand(t *testing.T) {
	s := devSettings(t)
	pid := seed(t, s)
	out, err := run(t, s, env(nil), "", "project", "super", "-project", pid)
	if err != nil || field(out, "superAdmin") != "enabled" {
		t.Fatalf("%v\n%s", err, out)
	}
	c, err := New(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := c.Auth.Projects.Get(context.Background(), pid)
	_ = c.Stop(context.Background())
	if p.SuperAdmin == nil || !*p.SuperAdmin {
		t.Fatalf("phải đánh dấu: %+v", p)
	}
	if out, err = run(t, s, env(nil), "", "project", "super", "-project", pid, "-disable"); err != nil || field(out, "superAdmin") != "disabled" {
		t.Fatalf("%v\n%s", err, out)
	}
	for name, args := range map[string][]string{
		"thiếu -project":   {"project", "super"},
		"project không có": {"project", "super", "-project", "khong-co"},
	} {
		if _, err := run(t, s, env(nil), "", args...); err == nil {
			t.Errorf("%s: phải lỗi", name)
		}
	}
}
