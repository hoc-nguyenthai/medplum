package application_test

import (
	"context"
	"testing"

	"trivita-fhir/apps/auth/internal/application"
	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
)

func TestSuperRescopeUserMovesGlobalUserIntoProject(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.superFixture()
		ctx := context.Background()
		target := e.project(nil)
		u := e.user("nv@bv.example.org", nil) // user toàn cục
		e.membership(u, target, nil)
		login := e.login(u, nil)
		got, err := e.svc.SuperRescopeUser(ctx, f.caller, u.ID, target.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Project == nil || got.Project.Reference != "Project/"+target.ID || got.Meta == nil || got.Meta.Project != target.ID {
			t.Fatalf("user: %+v", got)
		}
		in, _ := e.svc.Users.FindInProject(ctx, "nv@bv.example.org", target.ID)
		glob, _ := e.svc.Users.FindGlobal(ctx, "nv@bv.example.org")
		if in == nil || in.ID != u.ID || glob != nil {
			t.Fatalf("tra cứu: trong project=%v toàn cục=%v", in, glob)
		}
		if l, _ := e.svc.Logins.Get(ctx, login.ID); !domain.IsTrue(l.Revoked) {
			t.Error("login cũ phải bị thu hồi")
		}
		// làm lại: đã thuộc project
		wantErr(t, errOnly(e.svc.SuperRescopeUser(ctx, f.caller, u.ID, target.ID)), domain.KindBadRequest, "User is already scoped to a project")
	})
}

func errOnly(_ *fhir.User, err error) error { return err }

func TestSuperRescopeUserRejects(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.superFixture()
		ctx := context.Background()
		target, other := e.project(nil), e.project(nil)
		rescope := func(a *application.AuthState, uid, pid string) error {
			return errOnly(e.svc.SuperRescopeUser(ctx, a, uid, pid))
		}
		multi := e.user("multi@bv.example.org", nil)
		e.membership(multi, target, nil)
		e.membership(multi, other, nil)
		wantErr(t, rescope(f.caller, multi.ID, target.ID), domain.KindBadRequest, "User has memberships in other projects")
		none := e.user("none@bv.example.org", nil)
		wantErr(t, rescope(f.caller, none.ID, target.ID), domain.KindBadRequest, "User has no membership to scope the user to")
		// email đã có trong project đích
		dup := e.user("dup@bv.example.org", nil)
		e.membership(dup, target, nil)
		e.user("dup@bv.example.org", func(x *fhir.User) { x.Project = &fhir.Reference{Reference: "Project/" + target.ID} })
		wantErr(t, rescope(f.caller, dup.ID, target.ID), domain.KindConflict, "")
		if got, _ := e.svc.Users.Get(ctx, dup.ID); got.Project != nil {
			t.Fatal("lỗi không được đổi gì")
		}
		scoped := e.user("sc@bv.example.org", func(x *fhir.User) { x.Project = &fhir.Reference{Reference: "Project/" + other.ID} })
		e.membership(scoped, other, nil)
		wantErr(t, rescope(f.caller, scoped.ID, target.ID), domain.KindBadRequest, "User is already scoped to a project")
		wantErr(t, rescope(f.caller, f.user.ID, target.ID), domain.KindBadRequest, "Cannot rescope the caller")
		wantErr(t, rescope(f.caller, multi.ID, "khong-co"), domain.KindBadRequest, "Invalid projectId")
		wantErr(t, rescope(f.caller, multi.ID, " "), domain.KindBadRequest, "projectId is required")
		wantErr(t, rescope(f.caller, "khong-co", target.ID), domain.KindNotFound, "")
		// không phải super admin
		adm := e.adminFixture()
		wantErr(t, rescope(adm.caller, multi.ID, target.ID), domain.KindForbidden, "")
		wantErr(t, rescope(&application.AuthState{}, multi.ID, target.ID), domain.KindUnauthorized, "")
	})
}
