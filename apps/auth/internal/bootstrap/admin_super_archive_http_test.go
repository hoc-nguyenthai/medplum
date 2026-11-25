package bootstrap

import (
	"context"
	"net/url"
	"testing"

	"trivita-fhir/apps/auth/internal/config"
)

// Project lưu trữ mềm bị chặn ngay ở cấp token, restore phục hồi; chỉ super admin; không archive project của mình.
func TestHTTPSuperAdminArchiveAndRestoreProject(t *testing.T) {
	a := newCodeAPI(t, func(s *config.Settings) { s.AdminEnabled, s.SuperAdminEnabled = true, true })
	ctx := context.Background()
	if err := a.c.Auth.SetProjectSuperAdmin(ctx, a.seed.Project.ID, true); err != nil {
		t.Fatal(err)
	}
	hdr := adminBearer(a)
	created := a.do("POST", "/admin/super/projects", `{"name":"Cần lưu trữ"}`, hdr)
	if created.status != 201 {
		t.Fatalf("tạo: %d %s", created.status, created.body)
	}
	pid, cid, secret := created.json()["projectId"].(string), created.json()["clientId"].(string), created.json()["clientSecret"].(string)
	token := func() int {
		return a.form("/oauth2/token", url.Values{"grant_type": {"client_credentials"}, "client_id": {cid}, "client_secret": {secret}}, nil).status
	}
	if token() != 200 {
		t.Fatal("trước archive phải cấp được token")
	}
	for _, p := range []string{"/archive", "/restore"} {
		if r := a.do("POST", "/admin/super/projects/"+pid+p, "", nil); r.status != 401 {
			t.Errorf("%s không Bearer: %d", p, r.status)
		}
	}
	r := a.do("POST", "/admin/super/projects/"+pid+"/archive", "", hdr)
	if r.status != 200 || r.json()["projectId"] != pid || r.json()["active"] != false {
		t.Fatalf("archive: %d %s", r.status, r.body)
	}
	if token() == 200 {
		t.Fatal("project đã lưu trữ không được cấp token")
	}
	if r := a.do("POST", "/admin/super/projects/"+pid+"/archive", "", hdr); r.status != 200 {
		t.Errorf("lặp archive: %d", r.status)
	}
	r = a.do("POST", "/admin/super/projects/"+pid+"/restore", "", hdr)
	if r.status != 200 || r.json()["active"] != true {
		t.Fatalf("restore: %d %s", r.status, r.body)
	}
	if token() != 200 {
		t.Fatal("sau restore phải cấp lại được token")
	}
	if r := a.do("POST", "/admin/super/projects/"+a.seed.Project.ID+"/archive", "", hdr); r.status != 400 {
		t.Errorf("archive project của mình: %d %s", r.status, r.body)
	}
	if r := a.do("POST", "/admin/super/projects/khong-co/archive", "", hdr); r.status != 404 {
		t.Errorf("project lạ: %d %s", r.status, r.body)
	}
	// project thường (client mới vừa tạo không phải super admin): 403
	tk := a.form("/oauth2/token", url.Values{"grant_type": {"client_credentials"}, "client_id": {cid}, "client_secret": {secret}}, nil)
	cc := map[string]string{"Authorization": "Bearer " + tk.json()["access_token"].(string)}
	if r := a.do("POST", "/admin/super/projects/"+pid+"/archive", "", cc); r.status != 403 {
		t.Errorf("không phải super: %d %s", r.status, r.body)
	}
}
