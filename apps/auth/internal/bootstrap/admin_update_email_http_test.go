package bootstrap

import (
	"context"
	"testing"

	"trivita-fhir/pkg/fhir"
)

// Đổi email qua HTTP: đăng nhập bằng email mới được, email cũ không; lỗi theo hợp đồng.
func TestHTTPAdminUpdateMemberEmail(t *testing.T) {
	a, hdr := newAdminAPI(t)
	ctx := context.Background()
	pid := a.seed.Project.ID
	hash, err := a.c.Auth.HashPassword("mat-khau-cu-12345")
	if err != nil {
		t.Fatal(err)
	}
	ur, err := a.c.Auth.Resources.Create(ctx, &fhir.User{ResourceType: "User", Email: "pu@example.org", FirstName: "P", LastName: "U", PasswordHash: hash,
		Project: &fhir.Reference{Reference: "Project/" + pid}})
	if err != nil {
		t.Fatal(err)
	}
	mr, err := a.c.Auth.Resources.Create(ctx, &fhir.ProjectMembership{ResourceType: "ProjectMembership", User: &fhir.Reference{Reference: "User/" + ur.(*fhir.User).ID},
		Project: &fhir.Reference{Reference: "Project/" + pid}, Profile: &fhir.Reference{Reference: "Practitioner/pu"}})
	if err != nil {
		t.Fatal(err)
	}
	path := "/admin/projects/" + pid + "/members/" + mr.(*fhir.ProjectMembership).ID + "/update-email"
	login := func(email string) int {
		return a.login(`{"email":"`+email+`","password":"mat-khau-cu-12345","projectId":"`+pid+`"}`, nil).status
	}
	if login("pu@example.org") != 200 {
		t.Fatal("đăng nhập ban đầu")
	}
	if r := a.do("POST", path, `{"email":"Moi@Example.org"}`, hdr); r.status != 200 {
		t.Fatalf("đổi: %d %s", r.status, r.body)
	}
	if login("moi@example.org") != 200 || login("pu@example.org") == 200 {
		t.Fatal("sau khi đổi chỉ email mới đăng nhập được")
	}
	for name, body := range map[string]string{
		"không phải object": `[]`, "rác": `x`, "thiếu email": `{}`, "email sai kiểu": `{"email":5}`, "sai định dạng": `{"email":"abc"}`,
		"trường lạ": `{"email":"a@example.org","firstName":"X"}`, "trùng email hiện tại": `{"email":"moi@example.org"}`,
	} {
		if r := a.do("POST", path, body, hdr); r.status != 400 {
			t.Errorf("%s: %d %s", name, r.status, r.body)
		}
	}
	if r := a.do("POST", path, `{"email":"a@example.org"}`, nil); r.status != 401 {
		t.Errorf("không Bearer: %d", r.status)
	}
	// admin seed ở phạm vi server: không đổi được
	seed := "/admin/projects/" + pid + "/members/" + a.seed.Membership.ID + "/update-email"
	if r := a.do("POST", seed, `{"email":"a@example.org"}`, hdr); r.status != 403 {
		t.Errorf("server-scope: %d %s", r.status, r.body)
	}
	if r := a.do("POST", "/admin/projects/khac/members/x/update-email", `{"email":"a@example.org"}`, hdr); r.status != 403 {
		t.Errorf("project lệch: %d", r.status)
	}
}
