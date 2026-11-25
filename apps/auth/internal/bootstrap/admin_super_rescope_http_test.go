package bootstrap

import (
	"context"
	"testing"

	"trivita-fhir/apps/auth/internal/config"
	"trivita-fhir/pkg/fhir"
)

func TestHTTPSuperAdminRescopeUser(t *testing.T) {
	a := newCodeAPI(t, func(s *config.Settings) { s.AdminEnabled, s.SuperAdminEnabled = true, true })
	ctx := context.Background()
	if err := a.c.Auth.SetProjectSuperAdmin(ctx, a.seed.Project.ID, true); err != nil {
		t.Fatal(err)
	}
	hdr := adminBearer(a)
	pr, err := a.c.Auth.Resources.Create(ctx, &fhir.Project{ResourceType: "Project", Name: "Đích"})
	if err != nil {
		t.Fatal(err)
	}
	pid := pr.(*fhir.Project).ID
	ur, err := a.c.Auth.Resources.Create(ctx, &fhir.User{ResourceType: "User", Email: "nv@example.org", FirstName: "N", LastName: "V"})
	if err != nil {
		t.Fatal(err)
	}
	uid := ur.(*fhir.User).ID
	path := "/admin/super/users/" + uid + "/rescope"
	for name, body := range map[string]string{
		"rác": `x`, "thiếu projectId": `{}`, "sai kiểu": `{"projectId":5}`, "trường lạ": `{"projectId":"` + pid + `","x":1}`,
		"project không có": `{"projectId":"khong-co"}`, "chưa có membership": `{"projectId":"` + pid + `"}`,
	} {
		if r := a.do("POST", path, body, hdr); r.status != 400 {
			t.Errorf("%s: %d %s", name, r.status, r.body)
		}
	}
	if _, err := a.c.Auth.Resources.Create(ctx, &fhir.ProjectMembership{ResourceType: "ProjectMembership", User: &fhir.Reference{Reference: "User/" + uid},
		Project: &fhir.Reference{Reference: "Project/" + pid}, Profile: &fhir.Reference{Reference: "Practitioner/nv"}}); err != nil {
		t.Fatal(err)
	}
	if r := a.do("POST", path, `{"projectId":"`+pid+`"}`, nil); r.status != 401 {
		t.Errorf("không Bearer: %d", r.status)
	}
	r := a.do("POST", path, `{"projectId":"`+pid+`"}`, hdr)
	if r.status != 200 || r.json()["userId"] != uid || r.json()["projectId"] != pid {
		t.Fatalf("rescope: %d %s", r.status, r.body)
	}
	if u, _ := a.c.Auth.Users.FindInProject(ctx, "nv@example.org", pid); u == nil || u.ID != uid {
		t.Fatal("user phải thuộc project")
	}
	if r := a.do("POST", "/admin/super/users/khong-co/rescope", `{"projectId":"`+pid+`"}`, hdr); r.status != 404 {
		t.Errorf("user lạ: %d %s", r.status, r.body)
	}
	if err := a.c.Auth.SetProjectSuperAdmin(ctx, a.seed.Project.ID, false); err != nil {
		t.Fatal(err)
	}
	if r := a.do("POST", path, `{"projectId":"`+pid+`"}`, hdr); r.status != 403 {
		t.Errorf("bỏ cờ: %d", r.status)
	}
}
