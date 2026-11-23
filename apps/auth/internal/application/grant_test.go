package application_test

import (
	"context"
	"testing"

	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
)

func TestGrantLogin(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		u := e.user("a@example.org", nil)
		_, err := e.svc.GrantLogin(ctx, e.login(u, nil))
		wantErr(t, err, domain.KindBadRequest, "Login missing profile")

		l := e.login(u, func(l *fhir.Login) { l.Membership = &fhir.Reference{Reference: "ProjectMembership/m"} })
		out, err := e.svc.GrantLogin(ctx, l)
		if err != nil || !domain.IsTrue(out.Granted) {
			t.Fatalf("%+v %v", out, err)
		}
		if stored, _ := e.r.Logins.Get(ctx, l.ID); !domain.IsTrue(stored.Granted) {
			t.Error("phải ghi vào kho")
		}
		again, err := e.svc.GrantLogin(ctx, out) // đã granted: không ghi lại
		if err != nil || again.Meta.VersionId != out.Meta.VersionId {
			t.Errorf("không được ghi lại: %v", err)
		}
		// không ghi đè thu hồi xen giữa
		l2 := e.login(u, func(l *fhir.Login) { l.Membership = &fhir.Reference{Reference: "ProjectMembership/m"} })
		if err := e.svc.RevokeLogin(ctx, l2); err != nil {
			t.Fatal(err)
		}
		out2, err := e.svc.GrantLogin(ctx, l2)
		if err != nil || !domain.IsTrue(out2.Revoked) || !domain.IsTrue(out2.Granted) {
			t.Errorf("Grant không được xóa revoked: %+v %v", out2, err)
		}
	})
}

func TestRevokeLogin(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		u := e.user("a@example.org", nil)
		l := e.login(u, func(l *fhir.Login) { l.Scope = "openid profile" })
		if err := e.svc.RevokeLogin(ctx, l); err != nil {
			t.Fatal(err)
		}
		stored, _ := e.r.Logins.Get(ctx, l.ID)
		if !domain.IsTrue(stored.Revoked) || stored.Scope != "openid profile" {
			t.Errorf("%+v", stored)
		}
		before := stored.Meta.VersionId
		if err := e.svc.RevokeLogin(ctx, l); err != nil { // đã thu hồi: idempotent, không ghi lại
			t.Fatal(err)
		}
		after, _ := e.r.Logins.Get(ctx, l.ID)
		if after.Meta.VersionId != before {
			t.Error("thu hồi lần hai không được tạo phiên bản mới")
		}
		wantErr(t, e.svc.RevokeLogin(ctx, &fhir.Login{ID: "khong-co"}), domain.KindNotFound, "")
	})
}
