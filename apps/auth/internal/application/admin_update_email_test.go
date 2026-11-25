package application_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
)

func TestAdminUpdateMemberEmailWritesAndRevokes(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.adminFixture()
		ctx := context.Background()
		u, mm := f.member("cu@adm.example.org", false, nil)
		login := put(e, &fhir.Login{ResourceType: "Login", AuthMethod: "password", User: &fhir.Reference{Reference: "User/" + u.ID}})
		req, _, err := f.svc.IssueSecurityRequest(ctx, u, domain.SecurityRequestReset, "")
		if err != nil {
			t.Fatal(err)
		}
		before := len(e.mail.Sent())
		if err := f.svc.AdminUpdateMemberEmail(ctx, f.caller, f.project.ID, mm.ID, "  Moi@Adm.Example.org "); err != nil {
			t.Fatal(err)
		}
		got, _ := f.svc.Users.Get(ctx, u.ID)
		if got.Email != "moi@adm.example.org" || got.EmailVerified == nil || *got.EmailVerified {
			t.Fatalf("user: email=%q verified=%v", got.Email, got.EmailVerified)
		}
		if l, _ := f.svc.Logins.Get(ctx, login.ID); !domain.IsTrue(l.Revoked) {
			t.Error("login phải bị thu hồi")
		}
		if r, _ := f.svc.SecurityRequests.Get(ctx, req.ID); !domain.IsTrue(r.Used) {
			t.Error("security request phải bị vô hiệu")
		}
		sent := e.mail.Sent()
		if len(sent) != before+1 || sent[before].To != "cu@adm.example.org" || strings.Contains(sent[before].Text, "moi@") {
			t.Fatalf("thư báo phải tới địa chỉ cũ: %+v", sent)
		}
		// lỗi gửi thư báo không làm hỏng việc đổi
		e.mail.Err = errors.New("smtp down")
		if err := f.svc.AdminUpdateMemberEmail(ctx, f.caller, f.project.ID, mm.ID, "lai@adm.example.org"); err != nil {
			t.Fatal(err)
		}
		e.mail.Err = nil
		if got, _ = f.svc.Users.Get(ctx, u.ID); got.Email != "lai@adm.example.org" {
			t.Fatalf("email: %q", got.Email)
		}
	})
}

func TestAdminUpdateMemberEmailRejects(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.adminFixture()
		ctx := context.Background()
		u, mm := f.member("cu@adm.example.org", false, nil)
		f.member("khac@adm.example.org", false, nil)
		for name, email := range map[string]string{
			"rỗng": "", "sai định dạng": "khong-phai-email", "trùng email hiện tại": " CU@adm.example.org ", "trùng user khác": "Khac@adm.example.org",
		} {
			err := f.svc.AdminUpdateMemberEmail(ctx, f.caller, f.project.ID, mm.ID, email)
			if !domain.Is(err, domain.KindBadRequest, "") {
				t.Errorf("%s: %v", name, err)
			}
		}
		// Practitioner chịu danh sách chặn của máy chủ và allowedPractitionerEmailDomain của project
		e.svc.BlockedEmailDomains = []string{"chan.example.org"}
		wantErr(t, f.svc.AdminUpdateMemberEmail(ctx, f.caller, f.project.ID, mm.ID, "x@chan.example.org"), domain.KindBadRequest, "Email domain is not allowed for this project")
		if got, _ := f.svc.Users.Get(ctx, u.ID); got.Email != "cu@adm.example.org" {
			t.Fatalf("lỗi không được đổi gì: %q", got.Email)
		}
		// không phải admin, project lệch, member ngoài phạm vi
		wantErr(t, f.svc.AdminUpdateMemberEmail(ctx, f.caller, "project-khac", mm.ID, "a@adm.example.org"), domain.KindForbidden, "")
		wantErr(t, f.svc.AdminUpdateMemberEmail(ctx, f.caller, f.project.ID, "khong-co", "a@adm.example.org"), domain.KindForbidden, "")
		su := e.user("srv@adm.example.org", nil) // user phạm vi server
		sm := e.membership(su, f.project, nil)
		wantErr(t, f.svc.AdminUpdateMemberEmail(ctx, f.caller, f.project.ID, sm.ID, "a@adm.example.org"), domain.KindForbidden, "")
		if got, _ := f.svc.Users.Get(ctx, su.ID); got.Email != "srv@adm.example.org" {
			t.Fatal("user phạm vi server không được đổi")
		}
		plain := *f.caller
		pm := *f.am
		pm.Admin = nil
		plain.Membership = &pm
		wantErr(t, f.svc.AdminUpdateMemberEmail(ctx, &plain, f.project.ID, mm.ID, "a@adm.example.org"), domain.KindForbidden, "")
	})
}
