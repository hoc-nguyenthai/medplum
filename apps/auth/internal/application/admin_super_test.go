package application_test

import (
	"context"
	"strings"
	"testing"

	"trivita-fhir/apps/auth/internal/application"
	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
)

type superFix struct {
	e      *env
	caller *application.AuthState
	user   *fhir.User
	pro    *fhir.Project
}

func (e *env) superFixture() *superFix {
	u := e.user("root@super.example.org", nil)
	p := e.project(func(p *fhir.Project) { p.Name = "Gốc"; p.SuperAdmin = ptr(true) })
	m := e.membership(u, p, func(m *fhir.ProjectMembership) { m.Admin = ptr(true) })
	login := e.login(u, nil)
	return &superFix{e: e, user: u, pro: p, caller: &application.AuthState{Login: login, Membership: m, Project: p}}
}

func TestSuperInitProjectCreatesProjectWithCallerAsOwnerByDefault(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.superFixture()
		ctx := context.Background()
		res, err := e.svc.SuperInitProject(ctx, f.caller, application.SuperInitProjectRequest{Name: "Bệnh viện B", Features: []string{"scim", "scim", "bots"}})
		if err != nil {
			t.Fatal(err)
		}
		p, err := e.svc.Projects.Get(ctx, res.Project.ID)
		if err != nil {
			t.Fatal(err)
		}
		if p.Name != "Bệnh viện B" || p.Owner == nil || p.Owner.Reference != "User/"+f.user.ID || strings.Join(p.Features, ",") != "scim,bots" {
			t.Fatalf("project: %+v", p)
		}
		if domain.IsTrue(p.SuperAdmin) {
			t.Fatal("project mới không được thừa kế superAdmin")
		}
		if len(p.DefaultAccessPolicies) != len(domain.DefaultPolicyProfileTypes) {
			t.Fatalf("thiếu access policy mặc định: %d", len(p.DefaultAccessPolicies))
		}
		if res.Client == nil || len(res.Client.Secret) < 32 || res.Membership == nil || !domain.IsTrue(res.Membership.Admin) || res.Membership.User.Reference != "User/"+f.user.ID {
			t.Fatalf("client/membership: %+v %+v", res.Client, res.Membership)
		}
		if res.Membership.Project.Reference != "Project/"+res.Project.ID {
			t.Fatalf("membership phải thuộc project mới: %+v", res.Membership.Project)
		}
	})
}

func TestSuperInitProjectWithAnotherGlobalOwner(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.superFixture()
		owner := e.user("chu@example.org", nil)
		res, err := e.svc.SuperInitProject(context.Background(), f.caller, application.SuperInitProjectRequest{Name: "B", OwnerEmail: "  CHU@Example.org "})
		if err != nil {
			t.Fatal(err)
		}
		if res.Project.Owner.Reference != "User/"+owner.ID || res.Membership.User.Reference != "User/"+owner.ID {
			t.Fatalf("chủ sở hữu phải là user chỉ định: %+v %+v", res.Project.Owner, res.Membership.User)
		}
	})
}

func TestSuperInitProjectRequiresASuperAdminAdmin(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.superFixture()
		req := application.SuperInitProjectRequest{Name: "B"}
		ctx := context.Background()
		if _, err := e.svc.SuperInitProject(ctx, nil, req); domain.KindOf(err) != domain.KindUnauthorized {
			t.Fatalf("không có người gọi: %v", err)
		}
		forbid := func(name string, a *application.AuthState) {
			t.Helper()
			if _, err := e.svc.SuperInitProject(ctx, a, req); domain.KindOf(err) != domain.KindForbidden {
				t.Errorf("%s: phải 403, nhận %v", name, err)
			}
		}
		// admin của project thường (không superAdmin)
		u := e.user("adm@thuong.example.org", nil)
		p := e.project(nil)
		m := e.membership(u, p, func(m *fhir.ProjectMembership) { m.Admin = ptr(true) })
		forbid("project thường", &application.AuthState{Login: e.login(u, nil), Membership: m, Project: p})
		// thành viên không phải admin của project super
		u2 := e.user("tv@super.example.org", nil)
		m2 := e.membership(u2, f.pro, nil)
		forbid("không phải admin", &application.AuthState{Login: e.login(u2, nil), Membership: m2, Project: f.pro})
		// login bị giới hạn scope SMART theo loại resource
		l := e.login(f.user, func(l *fhir.Login) { l.Scope = "openid user/Patient.read" })
		smart := &application.AuthState{Login: l, Membership: f.caller.Membership, Project: f.pro}
		e.user("chu@example.org", nil)
		// có ownerEmail để việc từ chối đến từ chính bước kiểm super-admin (không phải từ việc tra người gọi làm chủ sở hữu mặc định)
		if _, err := e.svc.SuperInitProject(ctx, smart, application.SuperInitProjectRequest{Name: "B", OwnerEmail: "chu@example.org"}); domain.KindOf(err) != domain.KindForbidden {
			t.Errorf("scope SMART: phải 403, nhận %v", err)
		}
		forbid("scope SMART", smart)
		if _, err := e.svc.SuperInitProject(ctx, &application.AuthState{Membership: f.caller.Membership, Project: f.pro}, req); domain.KindOf(err) != domain.KindUnauthorized {
			t.Errorf("thiếu login: %v", err)
		}
		// không tạo gì khi bị từ chối
		if _, err := e.svc.SuperInitProject(ctx, &application.AuthState{Login: f.caller.Login, Membership: f.caller.Membership, Project: &fhir.Project{ID: f.pro.ID}}, req); domain.KindOf(err) != domain.KindForbidden {
			t.Errorf("project trong token không có superAdmin: %v", err)
		}
	})
}

func TestSuperInitProjectValidation(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.superFixture()
		ctx := context.Background()
		scoped := e.user("trong@project.example.org", func(u *fhir.User) { u.Project = &fhir.Reference{Reference: "Project/" + f.pro.ID} })
		_ = scoped
		many := make([]string, 21)
		for i := range many {
			many[i] = "f" + strings.Repeat("a", i)
		}
		for name, tc := range map[string]struct {
			req  application.SuperInitProjectRequest
			want string
		}{
			"tên rỗng":            {application.SuperInitProjectRequest{Name: "  "}, "Project name"},
			"tên quá dài":         {application.SuperInitProjectRequest{Name: strings.Repeat("ế", 73)}, "Project name"},
			"chủ không tồn tại":   {application.SuperInitProjectRequest{Name: "B", OwnerEmail: "khong-co@example.org"}, "Owner not found"},
			"chủ thuộc project":   {application.SuperInitProjectRequest{Name: "B", OwnerEmail: "trong@project.example.org"}, "Owner not found"},
			"tính năng sai tên":   {application.SuperInitProjectRequest{Name: "B", Features: []string{"Bots!"}}, "Invalid feature name"},
			"quá nhiều tính năng": {application.SuperInitProjectRequest{Name: "B", Features: many}, "Too many features"},
		} {
			_, err := e.svc.SuperInitProject(ctx, f.caller, tc.req)
			if domain.KindOf(err) != domain.KindBadRequest || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("%s: muốn 400 %q, nhận %v", name, tc.want, err)
			}
		}
	})
}

// Chủ sở hữu mặc định là người gọi: user thuộc một project (không toàn cục) không được làm chủ project mới.
func TestSuperInitProjectDefaultOwnerMustBeGlobal(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		p := e.project(func(p *fhir.Project) { p.SuperAdmin = ptr(true) })
		u := e.user("trong@super.example.org", func(u *fhir.User) { u.Project = &fhir.Reference{Reference: "Project/" + p.ID} })
		m := e.membership(u, p, func(m *fhir.ProjectMembership) { m.Admin = ptr(true) })
		a := &application.AuthState{Login: e.login(u, nil), Membership: m, Project: p}
		_, err := e.svc.SuperInitProject(context.Background(), a, application.SuperInitProjectRequest{Name: "B"})
		if domain.KindOf(err) != domain.KindBadRequest || !strings.Contains(err.Error(), "global user") {
			t.Fatalf("muốn 400 global user, nhận %v", err)
		}
	})
}

func TestSetProjectSuperAdmin(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		p := e.project(nil)
		ctx := context.Background()
		get := func() bool {
			got, err := e.svc.Projects.Get(ctx, p.ID)
			if err != nil {
				t.Fatal(err)
			}
			return domain.IsTrue(got.SuperAdmin)
		}
		if err := e.svc.SetProjectSuperAdmin(ctx, p.ID, true); err != nil || !get() {
			t.Fatalf("bật: %v", err)
		}
		if err := e.svc.SetProjectSuperAdmin(ctx, p.ID, true); err != nil || !get() {
			t.Fatalf("bật lần hai không làm gì: %v", err)
		}
		if err := e.svc.SetProjectSuperAdmin(ctx, p.ID, false); err != nil || get() {
			t.Fatalf("tắt: %v", err)
		}
		if err := e.svc.SetProjectSuperAdmin(ctx, "khong-co", true); domain.KindOf(err) != domain.KindNotFound {
			t.Fatalf("project không có: %v", err)
		}
	})
}
