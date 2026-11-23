package application_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"trivita-fhir/apps/auth/internal/application"
	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirsearch"
)

type admFix struct {
	e       *env
	svc     *application.Service
	project *fhir.Project
	caller  *application.AuthState
	am      *fhir.ProjectMembership
	au      *fhir.User
}

func (e *env) adminFixture() *admFix {
	au := e.user("admin@adm.example.org", nil)
	p := e.project(func(p *fhir.Project) {
		p.Owner = &fhir.Reference{Reference: "User/" + au.ID}
		p.Secret = []fhir.ProjectSetting{{Name: "k", ValueString: "bi-mat"}}
		p.Site = []fhir.ProjectSite{{Name: "s", GoogleClientId: "g", GoogleClientSecret: "gs", RecaptchaSecretKey: "rs"}}
		p.Setting = []fhir.ProjectSetting{{Name: "allow-dangerous-redirect", ValueBoolean: ptr(true)}, {Name: "appName", ValueString: "Cu"}}
	})
	am := e.membership(au, p, func(m *fhir.ProjectMembership) { m.Admin = ptr(true) })
	login := put(e, &fhir.Login{ResourceType: "Login", AuthMethod: "password", User: &fhir.Reference{Reference: "User/" + au.ID}})
	return &admFix{e: e, svc: e.svc, project: p, caller: &application.AuthState{Login: login, Membership: am, Project: p}, am: am, au: au}
}

func (f *admFix) member(email string, admin bool, mut func(*fhir.User, *fhir.ProjectMembership)) (*fhir.User, *fhir.ProjectMembership) {
	var mm *fhir.ProjectMembership
	u := f.e.user(email, func(u *fhir.User) {
		u.Project = &fhir.Reference{Reference: "Project/" + f.project.ID}
	})
	mm = f.e.membership(u, f.project, func(m *fhir.ProjectMembership) {
		m.Profile = &fhir.Reference{Reference: "Practitioner/" + strings.Split(email, "@")[0]}
		if admin {
			m.Admin = ptr(true)
		}
	})
	return u, mm
}

func TestAdminGuard(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.adminFixture()
		ctx := context.Background()
		_, err := f.svc.AdminGetProject(ctx, nil, f.project.ID)
		wantErr(t, err, domain.KindUnauthorized, "")
		plain := *f.caller
		m := *f.am
		m.Admin = nil
		plain.Membership = &m
		_, err = f.svc.AdminGetProject(ctx, &plain, f.project.ID)
		wantErr(t, err, domain.KindForbidden, "")
		_, err = f.svc.AdminGetProject(ctx, f.caller, "project-khac")
		wantErr(t, err, domain.KindForbidden, "")
		super := *f.caller
		sp := *f.project
		sp.SuperAdmin = ptr(true)
		super.Project = &sp
		other := e.project(nil)
		if _, err = f.svc.AdminGetProject(ctx, &super, other.ID); !domain.Is(err, domain.KindForbidden, "") {
			t.Errorf("super admin không có quyền xuyên project: %v", err)
		}
	})
}

func TestAdminGetProjectMasksSecrets(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.adminFixture()
		v, err := f.svc.AdminGetProject(context.Background(), f.caller, f.project.ID)
		if err != nil || v.ID != f.project.ID || len(v.Secret) != 1 || v.Secret[0].Name != "k" || v.Secret[0].ValueString == "bi-mat" || v.Secret[0].ValueString == "" {
			t.Fatalf("secret: %+v %v", v, err)
		}
		if v.Site[0].GoogleClientSecret == "gs" || v.Site[0].RecaptchaSecretKey == "rs" || v.Site[0].GoogleClientSecret == "" || v.Site[0].GoogleClientId != "g" {
			t.Errorf("site: %+v", v.Site)
		}
	})
}

func TestAdminReplaceSettings(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.adminFixture()
		ctx := context.Background()
		bad := map[string][]fhir.ProjectSetting{
			"ngoài allowlist":      {{Name: "allow-dangerous-redirect", ValueBoolean: ptr(true)}},
			"legacyExternalAuth":   {{Name: "legacyExternalAuth", ValueBoolean: ptr(true)}},
			"mfaRequired sai kiểu": {{Name: "mfaRequired", ValueString: "true"}},
			"phương thức lạ":       {{Name: "allowedMfaMethods", ValueString: "totp,sms"}},
			"phương thức rỗng":     {{Name: "allowedMfaMethods"}},
			"appName rỗng":         {{Name: "appName", ValueString: "  "}},
			"appName quá dài":      {{Name: "appName", ValueString: strings.Repeat("a", 101)}},
			"trùng tên":            {{Name: "mfaRequired", ValueBoolean: ptr(true)}, {Name: "mfaRequired", ValueBoolean: ptr(false)}},
		}
		for name, in := range bad {
			_, err := f.svc.AdminReplaceSettings(ctx, f.caller, f.project.ID, in)
			if !domain.Is(err, domain.KindBadRequest, "") {
				t.Errorf("%s: %v", name, err)
			}
		}
		cur, _ := f.svc.Projects.Get(ctx, f.project.ID)
		if len(cur.Setting) != 2 {
			t.Fatalf("lỗi không được đổi gì: %+v", cur.Setting)
		}
		_, err := f.svc.AdminReplaceSettings(ctx, f.caller, f.project.ID, []fhir.ProjectSetting{{Name: "mfaRequired", ValueBoolean: ptr(true)}, {Name: "allowedMfaMethods", ValueString: "totp, email"}})
		if err != nil {
			t.Fatal(err)
		}
		cur, _ = f.svc.Projects.Get(ctx, f.project.ID)
		names := map[string]bool{}
		for _, st := range cur.Setting {
			names[st.Name] = true
		}
		// appName (thuộc allowlist) bị thay vì không có trong danh sách mới; setting ngoài allowlist được giữ
		if names["appName"] || !names["allow-dangerous-redirect"] || !names["mfaRequired"] || !names["allowedMfaMethods"] || len(cur.Setting) != 3 {
			t.Errorf("setting sau khi thay: %+v", cur.Setting)
		}
		// danh sách rỗng xóa hết setting thuộc allowlist
		if _, err = f.svc.AdminReplaceSettings(ctx, f.caller, f.project.ID, []fhir.ProjectSetting{}); err != nil {
			t.Fatal(err)
		}
		if cur, _ = f.svc.Projects.Get(ctx, f.project.ID); len(cur.Setting) != 1 || cur.Setting[0].Name != "allow-dangerous-redirect" {
			t.Errorf("rỗng: %+v", cur.Setting)
		}
		_, err = f.svc.AdminReplaceSettings(ctx, f.caller, "project-khac", nil)
		wantErr(t, err, domain.KindForbidden, "")
	})
}

func TestAdminCreateClient(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.adminFixture()
		ctx := context.Background()
		c, err := f.svc.AdminCreateClient(ctx, f.caller, f.project.ID, application.AdminClientRequest{Name: "App", RedirectURIs: []string{"https://app.example.org/cb"}, GrantTypes: []string{"authorization_code"}})
		if err != nil || len(c.Secret) != 64 || c.Meta == nil || c.Meta.Project != f.project.ID {
			t.Fatalf("%+v %v", c, err)
		}
		ms, _ := f.svc.Memberships.ListForUser(ctx, "ClientApplication/"+c.ID, "Project/"+f.project.ID, 5)
		if len(ms) != 1 || refOfTest(ms[0].Profile) != "ClientApplication/"+c.ID {
			t.Errorf("membership của client: %+v", ms)
		}
		secret := strings.Repeat("s", 32)
		if c, err = f.svc.AdminCreateClient(ctx, f.caller, f.project.ID, application.AdminClientRequest{Name: "B", Secret: secret}); err != nil || c.Secret != secret {
			t.Errorf("secret tự đặt: %v %v", c, err)
		}
		other := e.project(nil)
		foreignPol := put(e, &fhir.AccessPolicy{ResourceType: "AccessPolicy", Name: "x", Meta: &fhir.Meta{Project: other.ID}})
		cases := map[string]application.AdminClientRequest{
			"thiếu name":          {Name: "  "},
			"secret ngắn":         {Name: "x", Secret: strings.Repeat("s", 31)},
			"secret quá dài":      {Name: "x", Secret: strings.Repeat("s", 257)},
			"grant lạ":            {Name: "x", GrantTypes: []string{"password"}},
			"redirect không abs":  {Name: "x", RedirectURIs: []string{"/cb"}},
			"redirect scheme lạ":  {Name: "x", RedirectURIs: []string{"javascript://x"}},
			"redirect có hash":    {Name: "x", RedirectURIs: []string{"https://a.example.org/cb#f"}},
			"policy project khác": {Name: "x", AccessPolicy: &fhir.Reference{Reference: "AccessPolicy/" + foreignPol.ID}},
		}
		for name, r := range cases {
			if _, err := f.svc.AdminCreateClient(ctx, f.caller, f.project.ID, r); !domain.Is(err, domain.KindBadRequest, "") {
				t.Errorf("%s: %v", name, err)
			}
		}
		_, err = f.svc.AdminCreateClient(ctx, f.caller, other.ID, application.AdminClientRequest{Name: "x"})
		wantErr(t, err, domain.KindForbidden, "")
		if n := countOf(t, e, "ClientApplication"); n != 2 {
			t.Errorf("lỗi không để lại client: %d", n)
		}
	})
}

func TestAdminMembersReadAndIsolation(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.adminFixture()
		ctx := context.Background()
		_, mm := f.member("a@adm.example.org", false, nil)
		got, err := f.svc.AdminGetMember(ctx, f.caller, f.project.ID, mm.ID)
		if err != nil || got.ID != mm.ID {
			t.Fatalf("%v %v", got, err)
		}
		op := e.project(nil)
		ou := e.user("o@adm.example.org", nil)
		om := e.membership(ou, op, nil)
		for name, id := range map[string]string{"project khác": om.ID, "không tồn tại": "khong-co"} {
			_, err := f.svc.AdminGetMember(ctx, f.caller, f.project.ID, id)
			if !domain.Is(err, domain.KindForbidden, "Forbidden") {
				t.Errorf("%s phải là 403 chung: %v", name, err)
			}
		}
		_, err = f.svc.AdminGetMember(ctx, f.caller, op.ID, mm.ID)
		wantErr(t, err, domain.KindForbidden, "")
	})
}

func TestAdminUpdateMember(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.adminFixture()
		ctx := context.Background()
		_, mm := f.member("a@adm.example.org", false, nil)
		body := func(mut func(*fhir.ProjectMembership)) *fhir.ProjectMembership {
			b := *mm
			if mut != nil {
				mut(&b)
			}
			return &b
		}
		own := put(e, &fhir.AccessPolicy{ResourceType: "AccessPolicy", Name: "p", Meta: &fhir.Meta{Project: f.project.ID}})
		other := e.project(nil)
		foreign := put(e, &fhir.AccessPolicy{ResourceType: "AccessPolicy", Name: "q", Meta: &fhir.Meta{Project: other.ID}})
		out, err := f.svc.AdminUpdateMember(ctx, f.caller, f.project.ID, mm.ID, body(func(b *fhir.ProjectMembership) {
			b.Admin, b.ExternalId, b.AccessPolicy = ptr(true), "ext-1", &fhir.Reference{Reference: "AccessPolicy/" + own.ID}
		}))
		if err != nil || !domain.IsTrue(out.Admin) || out.ExternalId != "ext-1" || out.AccessPolicy.Reference != "AccessPolicy/"+own.ID {
			t.Fatalf("hợp lệ: %+v %v", out, err)
		}
		for name, tc := range map[string]struct {
			b    *fhir.ProjectMembership
			kind domain.Kind
		}{
			"id lệch":           {body(func(b *fhir.ProjectMembership) { b.ID = "x" }), domain.KindForbidden},
			"resourceType lệch": {body(func(b *fhir.ProjectMembership) { b.ResourceType = "Patient" }), domain.KindForbidden},
			"đổi user":          {body(func(b *fhir.ProjectMembership) { b.User = &fhir.Reference{Reference: "User/khac"} }), domain.KindBadRequest},
			"đổi profile":       {body(func(b *fhir.ProjectMembership) { b.Profile = &fhir.Reference{Reference: "Practitioner/khac"} }), domain.KindBadRequest},
			"đổi project":       {body(func(b *fhir.ProjectMembership) { b.Project = &fhir.Reference{Reference: "Project/" + other.ID} }), domain.KindBadRequest},
			"policy ngoài": {body(func(b *fhir.ProjectMembership) {
				b.AccessPolicy = &fhir.Reference{Reference: "AccessPolicy/" + foreign.ID}
			}), domain.KindBadRequest},
			"access ngoài": {body(func(b *fhir.ProjectMembership) {
				b.Access = []fhir.ProjectMembershipAccess{{Policy: &fhir.Reference{Reference: "AccessPolicy/" + foreign.ID}}}
			}), domain.KindBadRequest},
		} {
			if _, err := f.svc.AdminUpdateMember(ctx, f.caller, f.project.ID, mm.ID, tc.b); !domain.Is(err, tc.kind, "") {
				t.Errorf("%s: %v", name, err)
			}
		}
		// externalId trùng member khác
		_, m2 := f.member("b@adm.example.org", false, nil)
		_, err = f.svc.AdminUpdateMember(ctx, f.caller, f.project.ID, m2.ID, func() *fhir.ProjectMembership { b := *m2; b.ExternalId = "ext-1"; return &b }())
		wantErr(t, err, domain.KindConflict, "")
		// các trường khác của membership bị bỏ qua (không ghi đè tùy ý)
		cur, _ := f.svc.Memberships.Get(ctx, mm.ID)
		if cur.Profile.Reference != mm.Profile.Reference || cur.User.Reference != mm.User.Reference {
			t.Error("user/profile phải giữ nguyên")
		}
	})
}

func TestAdminLastAdminProtection(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.adminFixture() // người gọi là admin duy nhất
		ctx := context.Background()
		demote := *f.am
		demote.Admin = nil
		_, err := f.svc.AdminUpdateMember(ctx, f.caller, f.project.ID, f.am.ID, &demote)
		wantErr(t, err, domain.KindBadRequest, "Cannot remove the last administrator of the project")
		off := *f.am
		off.Active = ptr(false)
		_, err = f.svc.AdminUpdateMember(ctx, f.caller, f.project.ID, f.am.ID, &off)
		wantErr(t, err, domain.KindBadRequest, "Cannot remove the last administrator of the project")
		// có admin thứ hai: hạ quyền được; admin cuối lại bị chặn
		_, m2 := f.member("b@adm.example.org", true, nil)
		if _, err = f.svc.AdminUpdateMember(ctx, f.caller, f.project.ID, f.am.ID, &demote); err != nil {
			t.Fatalf("còn admin khác: %v", err)
		}
		caller2 := &application.AuthState{Login: f.caller.Login, Membership: m2, Project: f.project}
		d2 := *m2
		d2.Admin = nil
		_, err = f.svc.AdminUpdateMember(ctx, caller2, f.project.ID, m2.ID, &d2)
		wantErr(t, err, domain.KindBadRequest, "Cannot remove the last administrator of the project")
		wantErr(t, f.svc.AdminDeleteMember(ctx, caller2, f.project.ID, m2.ID), domain.KindBadRequest, "")
	})
}

func TestAdminDeleteMember(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.adminFixture()
		ctx := context.Background()
		// chủ project không xóa được (kể cả khi có admin khác)
		f.member("b@adm.example.org", true, nil)
		wantErr(t, f.svc.AdminDeleteMember(ctx, f.caller, f.project.ID, f.am.ID), domain.KindBadRequest, "Cannot delete the owner of the project")
		// user thuộc project và chỉ có membership này: xóa cả user
		u, mm := f.member("c@adm.example.org", false, nil)
		if err := f.svc.AdminDeleteMember(ctx, f.caller, f.project.ID, mm.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := f.svc.Memberships.Get(ctx, mm.ID); !domain.Is(err, domain.KindNotFound, "") {
			t.Errorf("membership còn: %v", err)
		}
		if _, err := f.svc.Users.Get(ctx, u.ID); !domain.Is(err, domain.KindNotFound, "") {
			t.Errorf("user phải bị xóa: %v", err)
		}
		// user thuộc project nhưng còn membership khác: giữ user
		u2, m3 := f.member("d@adm.example.org", false, nil)
		e.membership(u2, e.project(nil), nil)
		if err := f.svc.AdminDeleteMember(ctx, f.caller, f.project.ID, m3.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := f.svc.Users.Get(ctx, u2.ID); err != nil {
			t.Errorf("user còn membership khác phải được giữ: %v", err)
		}
		// user phạm vi server: chỉ xóa membership
		su := e.user("e@adm.example.org", nil)
		sm := e.membership(su, f.project, nil)
		if err := f.svc.AdminDeleteMember(ctx, f.caller, f.project.ID, sm.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := f.svc.Users.Get(ctx, su.ID); err != nil {
			t.Errorf("user server-scope phải được giữ: %v", err)
		}
		// membership project khác hoặc không có: 403 và không xóa gì
		om := e.membership(e.user("o@adm.example.org", nil), e.project(nil), nil)
		wantErr(t, f.svc.AdminDeleteMember(ctx, f.caller, f.project.ID, om.ID), domain.KindForbidden, "")
		wantErr(t, f.svc.AdminDeleteMember(ctx, f.caller, f.project.ID, "khong-co"), domain.KindForbidden, "")
		if _, err := f.svc.Memberships.Get(ctx, om.ID); err != nil {
			t.Errorf("membership project khác bị xóa nhầm: %v", err)
		}
	})
}

func TestAdminSetPassword(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.adminFixture()
		ctx := context.Background()
		u, _ := f.member("a@adm.example.org", false, nil)
		lg := put(e, &fhir.Login{ResourceType: "Login", AuthMethod: "password", User: &fhir.Reference{Reference: "User/" + u.ID}})
		if err := f.svc.AdminSetPassword(ctx, f.caller, "A@Adm.Example.org", "mat-khau-moi-12345"); err != nil {
			t.Fatal(err)
		}
		got, _ := f.svc.Users.Get(ctx, u.ID)
		if got.PasswordHash == "" || !e.hasher.Compare("mat-khau-moi-12345", got.PasswordHash) {
			t.Error("mật khẩu chưa đổi")
		}
		if l, _ := f.svc.Logins.Get(ctx, lg.ID); !domain.IsTrue(l.Revoked) {
			t.Error("phải thu hồi mọi login")
		}
		e.user("srv@adm.example.org", nil) // user phạm vi server
		for name, email := range map[string]string{"server-scope": "srv@adm.example.org", "không tồn tại": "khong@adm.example.org"} {
			wantErr(t, f.svc.AdminSetPassword(ctx, f.caller, email, "mat-khau-moi-12345"), domain.KindBadRequest, "User not found")
			_ = name
		}
		plain := *f.caller
		m := *f.am
		m.Admin = nil
		plain.Membership = &m
		wantErr(t, f.svc.AdminSetPassword(ctx, &plain, "a@adm.example.org", "mat-khau-moi-12345"), domain.KindForbidden, "")
		wantErr(t, f.svc.AdminSetPassword(ctx, nil, "a@adm.example.org", "mat-khau-moi-12345"), domain.KindUnauthorized, "")
	})
}

func refOfTest(r *fhir.Reference) string {
	if r == nil {
		return ""
	}
	return r.Reference
}

func countOf(t *testing.T, e *env, rt string) int {
	t.Helper()
	res, err := e.repo.Search(context.Background(), &fhirsearch.Request{ResourceType: rt, Count: 1000, Total: "none"})
	if err != nil {
		t.Fatal(err)
	}
	return len(res.Matches)
}

func TestAdminResetMemberPassword(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.adminFixture()
		ctx := context.Background()
		u, mm := f.member("a@adm.example.org", false, nil)
		if err := f.svc.AdminResetMemberPassword(ctx, f.caller, f.project.ID, mm.ID); err != nil {
			t.Fatal(err)
		}
		sent := e.mail.Sent()
		if len(sent) != 1 || sent[0].To != u.Email || !strings.Contains(sent[0].Text, "https://app.example.org/setpassword/") {
			t.Fatalf("thư: %+v", sent)
		}
		// lỗi gửi thư là lỗi rõ ràng (khác route công khai)
		e.mail.Err = errors.New("smtp down")
		wantErr(t, f.svc.AdminResetMemberPassword(ctx, f.caller, f.project.ID, mm.ID), domain.KindInternal, "")
		e.mail.Err = nil
		// user phạm vi server hoặc project khác: 403, không gửi thư
		su := e.user("srv@adm.example.org", nil)
		sm := e.membership(su, f.project, nil)
		before := len(e.mail.Sent())
		wantErr(t, f.svc.AdminResetMemberPassword(ctx, f.caller, f.project.ID, sm.ID), domain.KindForbidden, "")
		wantErr(t, f.svc.AdminResetMemberPassword(ctx, f.caller, f.project.ID, "khong-co"), domain.KindForbidden, "")
		om := e.membership(e.user("o@adm.example.org", nil), e.project(nil), nil)
		wantErr(t, f.svc.AdminResetMemberPassword(ctx, f.caller, f.project.ID, om.ID), domain.KindForbidden, "")
		if len(e.mail.Sent()) != before {
			t.Error("không được gửi thư cho user ngoài phạm vi")
		}
		// user không có email
		nu := e.user("", func(x *fhir.User) { x.Project = &fhir.Reference{Reference: "Project/" + f.project.ID}; x.Email = "" })
		nm := e.membership(nu, f.project, func(m *fhir.ProjectMembership) { m.Profile = &fhir.Reference{Reference: "Practitioner/noemail"} })
		wantErr(t, f.svc.AdminResetMemberPassword(ctx, f.caller, f.project.ID, nm.ID), domain.KindBadRequest, "User does not have an email address")
		plain := *f.caller
		pm := *f.am
		pm.Admin = nil
		plain.Membership = &pm
		wantErr(t, f.svc.AdminResetMemberPassword(ctx, &plain, f.project.ID, mm.ID), domain.KindForbidden, "")
	})
}

func TestAdminResetMemberMFA(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.adminFixture()
		ctx := context.Background()
		mk := func(email string, mut func(*fhir.User)) (*fhir.User, *fhir.ProjectMembership) {
			u, _ := e.totpUser(email)
			if _, err := f.svc.Users.Mutate(ctx, u.ID, func(x *fhir.User) error {
				x.Project = &fhir.Reference{Reference: "Project/" + f.project.ID}
				x.MfaMethod = []string{"totp", "email"}
				if mut != nil {
					mut(x)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			m := e.membership(u, f.project, func(m *fhir.ProjectMembership) {
				m.Profile = &fhir.Reference{Reference: "Practitioner/" + strings.Split(email, "@")[0]}
			})
			return u, m
		}
		u, mm := mk("a@adm.example.org", nil)
		old := e.getUser(u.ID).MfaSecret
		// mặc định gỡ totp: secret đổi, email còn
		if err := f.svc.AdminResetMemberMFA(ctx, f.caller, f.project.ID, mm.ID, nil); err != nil {
			t.Fatal(err)
		}
		got := e.getUser(u.ID)
		if got.MfaSecret == old || got.MfaSecret == "" || !domain.IsTrue(got.MfaEnrolled) || len(got.MfaMethod) != 1 || got.MfaMethod[0] != "email" {
			t.Fatalf("sau reset totp: %+v", got)
		}
		wantErr(t, f.svc.AdminResetMemberMFA(ctx, f.caller, f.project.ID, mm.ID, "totp"), domain.KindBadRequest, "User is not enrolled in MFA method: totp")
		if err := f.svc.AdminResetMemberMFA(ctx, f.caller, f.project.ID, mm.ID, "email"); err != nil {
			t.Fatal(err)
		}
		if got = e.getUser(u.ID); domain.IsTrue(got.MfaEnrolled) || len(got.MfaMethod) != 0 {
			t.Errorf("hết yếu tố: %+v", got)
		}
		for name, m := range map[string]any{"sms": "sms", "số": 5} {
			if err := f.svc.AdminResetMemberMFA(ctx, f.caller, f.project.ID, mm.ID, m); !domain.Is(err, domain.KindBadRequest, `Method must be "totp" or "email"`) {
				t.Errorf("%s: %v", name, err)
			}
		}
		// user phạm vi server: 403, không đổi gì
		su, sm := e.totpUser("srv@adm.example.org")
		smm := e.membership(su, f.project, nil)
		_ = sm
		before := e.getUser(su.ID).MfaSecret
		wantErr(t, f.svc.AdminResetMemberMFA(ctx, f.caller, f.project.ID, smm.ID, nil), domain.KindForbidden, "")
		if e.getUser(su.ID).MfaSecret != before {
			t.Error("user server-scope bị đổi")
		}
		plain := *f.caller
		pm := *f.am
		pm.Admin = nil
		plain.Membership = &pm
		wantErr(t, f.svc.AdminResetMemberMFA(ctx, &plain, f.project.ID, mm.ID, nil), domain.KindForbidden, "")
	})
}

// ---- invite

func (f *admFix) invite(mut func(*application.InviteRequest)) (*fhir.ProjectMembership, error) {
	r := application.InviteRequest{ResourceType: "Practitioner", FirstName: "Lan", LastName: "Tran", Email: "Lan@Inv.Example.org"}
	if mut != nil {
		mut(&r)
	}
	return f.svc.AdminInvite(context.Background(), f.caller, f.project.ID, r)
}

func TestAdminInviteNewPractitioner(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.adminFixture()
		ctx := context.Background()
		m, err := f.invite(nil)
		if err != nil {
			t.Fatal(err)
		}
		if refOfTest(m.Project) != "Project/"+f.project.ID || !strings.HasPrefix(refOfTest(m.Profile), "Practitioner/") || refOfTest(m.InvitedBy) != "User/"+f.au.ID {
			t.Fatalf("membership: %+v", m)
		}
		u, err := f.svc.Users.Get(ctx, idOfTest(m.User))
		if err != nil || u.Email != "lan@inv.example.org" || u.Project != nil || u.PasswordHash == "" {
			t.Fatalf("user (Practitioner mặc định phạm vi server): %+v %v", u, err)
		}
		sent := e.mail.Sent()
		if len(sent) != 1 || sent[0].To != "lan@inv.example.org" || !strings.Contains(sent[0].Text, "/setpassword/") {
			t.Fatalf("thư mời: %+v", sent)
		}
		prof, err := f.svc.Resources.Get(ctx, "Practitioner", idOfTest(m.Profile))
		if err != nil || prof.GetMeta().Project != f.project.ID {
			t.Fatalf("hồ sơ: %v %v", prof, err)
		}
		// mời lại: 409
		_, err = f.invite(nil)
		wantErr(t, err, domain.KindConflict, "User is already a member of this project")
		if countOf(t, e, "Practitioner") != 1 {
			t.Error("lần mời trùng không được để lại hồ sơ mồ côi")
		}
	})
}

func idOfTest(r *fhir.Reference) string {
	_, id, _ := strings.Cut(refOfTest(r), "/")
	return id
}

func TestAdminInviteScopesAndDefaults(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.adminFixture()
		ctx := context.Background()
		pol := put(e, &fhir.AccessPolicy{ResourceType: "AccessPolicy", Name: "pt", Meta: &fhir.Meta{Project: f.project.ID}})
		adminPol := put(e, &fhir.AccessPolicy{ResourceType: "AccessPolicy", Name: "ad", Meta: &fhir.Meta{Project: f.project.ID}})
		prac := put(e, &fhir.AccessPolicy{ResourceType: "AccessPolicy", Name: "pr", Meta: &fhir.Meta{Project: f.project.ID}})
		cp := *f.project
		cp.DefaultPatientAccessPolicy = &fhir.Reference{Reference: "AccessPolicy/" + pol.ID}
		cp.DefaultAccessPolicies = []fhir.ProjectDefaultAccessPolicies{{ProfileType: "Admin", AccessPolicy: &fhir.Reference{Reference: "AccessPolicy/" + adminPol.ID}},
			{ProfileType: "Practitioner", AccessPolicy: &fhir.Reference{Reference: "AccessPolicy/" + prac.ID}},
			{ProfileType: "RelatedPerson", AccessPolicy: &fhir.Reference{Reference: "AccessPolicy/" + pol.ID}}}
		if _, err := e.repo.UpdateResource(ctx, &cp, fhirUpdate()); err != nil {
			t.Fatal(err)
		}
		no := false
		// Patient: phạm vi project, policy mặc định của Patient
		m, err := f.invite(func(r *application.InviteRequest) {
			r.ResourceType, r.Email, r.SendEmail = "Patient", "bn@inv.example.org", &no
		})
		if err != nil {
			t.Fatal(err)
		}
		u, _ := f.svc.Users.Get(ctx, idOfTest(m.User))
		if refOfTest(u.Project) != "Project/"+f.project.ID || refOfTest(m.AccessPolicy) != "AccessPolicy/"+pol.ID || len(e.mail.Sent()) != 0 {
			t.Errorf("patient: user=%+v m=%+v thư=%d", u, m, len(e.mail.Sent()))
		}
		// Practitioner thường / admin chọn policy khác nhau
		m, err = f.invite(func(r *application.InviteRequest) { r.Email, r.SendEmail = "p1@inv.example.org", &no })
		if err != nil || refOfTest(m.AccessPolicy) != "AccessPolicy/"+prac.ID {
			t.Errorf("practitioner: %+v %v", m, err)
		}
		m, err = f.invite(func(r *application.InviteRequest) {
			r.Email, r.SendEmail, r.Membership.Admin = "p2@inv.example.org", &no, ptr(true)
		})
		if err != nil || refOfTest(m.AccessPolicy) != "AccessPolicy/"+adminPol.ID || !domain.IsTrue(m.Admin) {
			t.Errorf("admin: %+v %v", m, err)
		}
		// policy do người gọi chỉ định thắng mặc định; policy ngoài project bị từ chối
		m, err = f.invite(func(r *application.InviteRequest) {
			r.Email, r.SendEmail, r.Membership.AccessPolicy = "p3@inv.example.org", &no, &fhir.Reference{Reference: "AccessPolicy/" + pol.ID}
		})
		if err != nil || refOfTest(m.AccessPolicy) != "AccessPolicy/"+pol.ID {
			t.Errorf("chỉ định: %+v %v", m, err)
		}
		other := e.project(nil)
		foreign := put(e, &fhir.AccessPolicy{ResourceType: "AccessPolicy", Name: "x", Meta: &fhir.Meta{Project: other.ID}})
		_, err = f.invite(func(r *application.InviteRequest) {
			r.Email, r.SendEmail, r.Membership.AccessPolicy = "p4@inv.example.org", &no, &fhir.Reference{Reference: "AccessPolicy/" + foreign.ID}
		})
		wantErr(t, err, domain.KindBadRequest, "Invalid access policy")
		if _, err := f.svc.FindUserByEmail(ctx, "p4@inv.example.org", ""); err != nil {
			t.Fatal(err)
		}
		// RelatedPerson: cần Patient cùng project; access có tham số patient
		bn := put(e, &fhir.Patient{ResourceType: "Patient", Meta: &fhir.Meta{Project: f.project.ID}})
		fp := put(e, &fhir.Patient{ResourceType: "Patient", Meta: &fhir.Meta{Project: other.ID}})
		_, err = f.invite(func(r *application.InviteRequest) {
			r.ResourceType, r.Email, r.SendEmail = "RelatedPerson", "rp@inv.example.org", &no
		})
		wantErr(t, err, domain.KindBadRequest, "Patient is required to create a RelatedPerson")
		_, err = f.invite(func(r *application.InviteRequest) {
			r.ResourceType, r.Email, r.SendEmail, r.Patient = "RelatedPerson", "rp@inv.example.org", &no, &fhir.Reference{Reference: "Organization/1"}
		})
		wantErr(t, err, domain.KindBadRequest, "Patient must be a reference to a Patient resource")
		_, err = f.invite(func(r *application.InviteRequest) {
			r.ResourceType, r.Email, r.SendEmail, r.Patient = "RelatedPerson", "rp@inv.example.org", &no, &fhir.Reference{Reference: "Patient/" + fp.ID}
		})
		wantErrContains(t, err, domain.KindBadRequest, "does not belong to project")
		_, err = f.invite(func(r *application.InviteRequest) {
			r.ResourceType, r.Email, r.SendEmail, r.Patient = "RelatedPerson", "rp@inv.example.org", &no, &fhir.Reference{Reference: "Patient/khong-co"}
		})
		wantErrContains(t, err, domain.KindBadRequest, "does not exist")
		m, err = f.invite(func(r *application.InviteRequest) {
			r.ResourceType, r.Email, r.SendEmail, r.Patient = "RelatedPerson", "rp@inv.example.org", &no, &fhir.Reference{Reference: "Patient/" + bn.ID}
		})
		if err != nil || len(m.Access) != 1 || len(m.Access[0].Parameter) != 1 || refOfTest(m.Access[0].Parameter[0].ValueReference) != "Patient/"+bn.ID {
			t.Fatalf("relatedperson: %+v %v", m, err)
		}
		// scope server cho Patient
		m, err = f.invite(func(r *application.InviteRequest) {
			r.ResourceType, r.Email, r.SendEmail, r.Scope = "Patient", "sv@inv.example.org", &no, "server"
		})
		if err != nil {
			t.Fatal(err)
		}
		if u, _ = f.svc.Users.Get(ctx, idOfTest(m.User)); u.Project != nil {
			t.Error("scope=server phải tạo user phạm vi server")
		}
	})
}

func TestAdminInviteValidation(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.adminFixture()
		no := false
		cases := map[string]func(*application.InviteRequest){
			"resourceType":    func(r *application.InviteRequest) { r.ResourceType = "Organization" },
			"firstName":       func(r *application.InviteRequest) { r.FirstName = " " },
			"lastName":        func(r *application.InviteRequest) { r.LastName = "" },
			"thiếu cả hai":    func(r *application.InviteRequest) { r.Email, r.ExternalID = "", "" },
			"email sai":       func(r *application.InviteRequest) { r.Email = "khong-phai-email" },
			"externalId rỗng": func(r *application.InviteRequest) { r.Email, r.ExternalID = "", "  " },
			"scope lạ":        func(r *application.InviteRequest) { r.Scope = "global" },
			"mật khẩu ngắn":   func(r *application.InviteRequest) { r.Password = "ngan" },
		}
		for name, mut := range cases {
			if _, err := f.invite(func(r *application.InviteRequest) { r.SendEmail = &no; mut(r) }); !domain.Is(err, domain.KindBadRequest, "") {
				t.Errorf("%s: %v", name, err)
			}
		}
		e.pwned.bad["mat-khau-ro-ri-123"] = 5
		if _, err := f.invite(func(r *application.InviteRequest) { r.SendEmail, r.Password = &no, "mat-khau-ro-ri-123" }); !domain.Is(err, domain.KindBadRequest, "") {
			t.Errorf("pwned: %v", err)
		}
		if countOf(t, e, "Practitioner") != 0 || countOf(t, e, "ProjectMembership") != 1 {
			t.Error("lỗi kiểm đầu vào không được ghi gì")
		}
		// quyền
		plain := *f.caller
		pm := *f.am
		pm.Admin = nil
		plain.Membership = &pm
		_, err := f.svc.AdminInvite(context.Background(), &plain, f.project.ID, application.InviteRequest{ResourceType: "Patient", FirstName: "a", LastName: "b", Email: "x@y.org"})
		wantErr(t, err, domain.KindForbidden, "")
		_, err = f.svc.AdminInvite(context.Background(), f.caller, "khac", application.InviteRequest{ResourceType: "Patient", FirstName: "a", LastName: "b", Email: "x@y.org"})
		wantErr(t, err, domain.KindForbidden, "")
	})
}

func TestAdminInviteEmailDomainRules(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.adminFixture()
		ctx := context.Background()
		no := false
		d := f.svc.Deps
		d.BlockedEmailDomains = []string{"Bad.example.org"}
		f.svc = application.NewService(d)
		_, err := f.invite(func(r *application.InviteRequest) { r.Email, r.SendEmail = "x@bad.example.org", &no })
		wantErr(t, err, domain.KindBadRequest, "Email domain is not allowed for this project")
		// Patient không bị hạn chế theo miền
		if _, err = f.invite(func(r *application.InviteRequest) {
			r.ResourceType, r.Email, r.SendEmail = "Patient", "x@bad.example.org", &no
		}); err != nil {
			t.Errorf("patient: %v", err)
		}
		// allowedPractitionerEmailDomain: nhiều mục, rỗng là không hạn chế
		cp := *f.project
		cp.Setting = append(cp.Setting, fhir.ProjectSetting{Name: "allowedPractitionerEmailDomain", ValueString: "ok.example.org"}, fhir.ProjectSetting{Name: "allowedPractitionerEmailDomain", ValueString: "Two.example.org"})
		if _, err = e.repo.UpdateResource(ctx, &cp, fhirUpdate()); err != nil {
			t.Fatal(err)
		}
		_, err = f.invite(func(r *application.InviteRequest) { r.Email, r.SendEmail = "x@khac.example.org", &no })
		wantErr(t, err, domain.KindBadRequest, "Email domain is not allowed for this project")
		for _, em := range []string{"a@ok.example.org", "b@TWO.example.org"} {
			if _, err = f.invite(func(r *application.InviteRequest) { r.Email, r.SendEmail = em, &no }); err != nil {
				t.Errorf("%s: %v", em, err)
			}
		}
		// danh sách chặn thắng danh sách cho phép
		cp2 := *f.project
		cp2.Setting = append([]fhir.ProjectSetting{}, cp.Setting...)
		cp2.Setting = append(cp2.Setting, fhir.ProjectSetting{Name: "allowedPractitionerEmailDomain", ValueString: "bad.example.org"})
		cur, _ := f.svc.Projects.Get(ctx, f.project.ID)
		cp2.ID, cp2.Meta = cur.ID, cur.Meta
		if _, err = e.repo.UpdateResource(ctx, &cp2, fhirUpdate()); err != nil {
			t.Fatal(err)
		}
		_, err = f.invite(func(r *application.InviteRequest) { r.Email, r.SendEmail = "z@bad.example.org", &no })
		wantErr(t, err, domain.KindBadRequest, "Email domain is not allowed for this project")
		// externalId không bị hạn chế theo miền
		if _, err = f.invite(func(r *application.InviteRequest) { r.Email, r.ExternalID = "", "ext-d" }); err != nil {
			t.Errorf("externalId: %v", err)
		}
	})
}

func TestAdminInviteExternalIDAndDuplicates(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.adminFixture()
		ctx := context.Background()
		m, err := f.invite(func(r *application.InviteRequest) { r.Email, r.ExternalID = "", "ext-1" })
		if err != nil {
			t.Fatal(err)
		}
		u, _ := f.svc.Users.Get(ctx, idOfTest(m.User))
		if m.ExternalId != "ext-1" || u.Email != "" || refOfTest(u.Project) != "Project/"+f.project.ID || len(e.mail.Sent()) != 0 {
			t.Fatalf("externalId: m=%+v u=%+v thư=%d", m, u, len(e.mail.Sent()))
		}
		_, err = f.invite(func(r *application.InviteRequest) { r.Email, r.ExternalID = "", "ext-1" })
		wantErr(t, err, domain.KindConflict, "externalId already exists in this project")
		// xóa rồi dùng lại được
		if err := f.svc.AdminDeleteMember(ctx, f.caller, f.project.ID, m.ID); err != nil {
			t.Fatal(err)
		}
		if _, err = f.invite(func(r *application.InviteRequest) { r.Email, r.ExternalID = "", "ext-1" }); err != nil {
			t.Errorf("dùng lại sau khi xóa: %v", err)
		}
	})
}

func TestAdminInviteExistingUserUpsertAndScopeConflict(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.adminFixture()
		ctx := context.Background()
		no := false
		m1, err := f.invite(func(r *application.InviteRequest) { r.SendEmail = &no })
		if err != nil {
			t.Fatal(err)
		}
		// upsert: cập nhật membership có sẵn (giữ id/profile), không tạo hồ sơ mới
		m2, err := f.invite(func(r *application.InviteRequest) { r.SendEmail, r.Upsert, r.Membership.Admin = &no, true, ptr(true) })
		if err != nil || m2.ID != m1.ID || refOfTest(m2.Profile) != refOfTest(m1.Profile) || !domain.IsTrue(m2.Admin) || countOf(t, e, "Practitioner") != 1 {
			t.Fatalf("upsert: %+v %v", m2, err)
		}
		_, err = f.invite(func(r *application.InviteRequest) {
			r.SendEmail, r.Upsert, r.Membership.Profile = &no, true, &fhir.Reference{Reference: "Practitioner/khac"}
		})
		wantErr(t, err, domain.KindConflict, "User is already a member of this project with a different profile")
		// scope khác với user đã là member: 409; forceNewMembership bỏ kiểm này
		_, err = f.invite(func(r *application.InviteRequest) { r.SendEmail, r.Scope = &no, "project" })
		wantErr(t, err, domain.KindConflict, "User is already a member of this project")
		if _, err = f.invite(func(r *application.InviteRequest) { r.SendEmail, r.Scope, r.ForceNewMembership = &no, "project", true }); err != nil {
			t.Errorf("forceNewMembership: %v", err)
		}
		// user đã tồn tại ở project khác được dùng lại, thư là thông báo (không phải link đặt mật khẩu)
		other := e.project(nil)
		ou := e.user("old@inv.example.org", nil)
		e.membership(ou, other, nil)
		before := len(e.mail.Sent())
		m3, err := f.invite(func(r *application.InviteRequest) { r.Email = "old@inv.example.org" })
		if err != nil || idOfTest(m3.User) != ou.ID {
			t.Fatalf("user có sẵn: %+v %v", m3, err)
		}
		sent := e.mail.Sent()
		if len(sent) != before+1 || strings.Contains(sent[len(sent)-1].Text, "/setpassword/") {
			t.Errorf("thư cho user cũ: %+v", sent[before:])
		}
		got, _ := f.svc.Users.Get(ctx, ou.ID)
		if got.PasswordHash != ou.PasswordHash {
			t.Error("không được đổi mật khẩu của user có sẵn")
		}
	})
}

func TestAdminInviteMailFailureAndConfig(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.adminFixture()
		e.mail.Err = errors.New("smtp down")
		_, err := f.invite(nil)
		if !domain.Is(err, domain.KindBadRequest, "") || !strings.Contains(err.Error(), "Invitation created") {
			t.Fatalf("lỗi thư: %v", err)
		}
		// dữ liệu đã tạo, nên mời lại là 409 (dùng resetpassword để gửi lại)
		e.mail.Err = nil
		_, err = f.invite(nil)
		wantErr(t, err, domain.KindConflict, "")
		// chưa cấu hình thư: lỗi TRƯỚC khi ghi, trừ khi sendEmail=false
		d := f.svc.Deps
		d.Mailer = nil
		nomail := application.NewService(d)
		_, err = nomail.AdminInvite(context.Background(), f.caller, f.project.ID, application.InviteRequest{ResourceType: "Practitioner", FirstName: "a", LastName: "b", Email: "n@inv.example.org"})
		wantErr(t, err, domain.KindBadRequest, "Email sending is not configured; set sendEmail to false")
		if u, _ := f.svc.FindUserByEmail(context.Background(), "n@inv.example.org", ""); u != nil {
			t.Error("không được tạo user khi thiếu cấu hình thư")
		}
		no := false
		if _, err = nomail.AdminInvite(context.Background(), f.caller, f.project.ID, application.InviteRequest{ResourceType: "Practitioner", FirstName: "a", LastName: "b", Email: "n@inv.example.org", SendEmail: &no}); err != nil {
			t.Errorf("sendEmail=false: %v", err)
		}
	})
}

func wantErrContains(t *testing.T, err error, kind domain.Kind, sub string) {
	t.Helper()
	if err == nil || !domain.Is(err, kind, "") || !strings.Contains(err.Error(), sub) {
		t.Fatalf("mong lỗi kind=%d chứa %q, được %v", kind, sub, err)
	}
}

func TestAdminInviteProfileAndExternalIDEdgeCases(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.adminFixture()
		no := false
		other := e.project(nil)
		foreign := put(e, &fhir.Practitioner{ResourceType: "Practitioner", Meta: &fhir.Meta{Project: other.ID}})
		own := put(e, &fhir.Practitioner{ResourceType: "Practitioner", Meta: &fhir.Meta{Project: f.project.ID}})
		pt := put(e, &fhir.Patient{ResourceType: "Patient", Meta: &fhir.Meta{Project: f.project.ID}})
		_, err := f.invite(func(r *application.InviteRequest) {
			r.SendEmail, r.Membership.Profile = &no, &fhir.Reference{Reference: "Practitioner/" + foreign.ID}
		})
		wantErr(t, err, domain.KindBadRequest, "Profile does not belong to project")
		_, err = f.invite(func(r *application.InviteRequest) {
			r.SendEmail, r.Membership.Profile = &no, &fhir.Reference{Reference: "Practitioner/khong-co"}
		})
		wantErr(t, err, domain.KindBadRequest, "Profile does not belong to project")
		_, err = f.invite(func(r *application.InviteRequest) {
			r.SendEmail, r.Membership.Profile = &no, &fhir.Reference{Reference: "Patient/" + pt.ID}
		})
		wantErr(t, err, domain.KindBadRequest, "Profile resourceType does not match request")
		m, err := f.invite(func(r *application.InviteRequest) {
			r.SendEmail, r.Membership.Profile = &no, &fhir.Reference{Reference: "Practitioner/" + own.ID}
		})
		if err != nil || refOfTest(m.Profile) != "Practitioner/"+own.ID {
			t.Fatalf("profile có sẵn trong project: %+v %v", m, err)
		}
		// upsert giữ nguyên externalId của chính nó thì không tự xung đột
		_, err = f.invite(func(r *application.InviteRequest) { r.SendEmail, r.Membership.ExternalID = &no, "ext-up" })
		wantErr(t, err, domain.KindConflict, "")
		m, err = f.invite(func(r *application.InviteRequest) {
			r.SendEmail, r.Email, r.Membership.ExternalID = &no, "k2@inv.example.org", "ext-up"
		})
		if err != nil {
			t.Fatal(err)
		}
		if m2, err := f.invite(func(r *application.InviteRequest) {
			r.SendEmail, r.Email, r.Upsert, r.Membership.ExternalID = &no, "k2@inv.example.org", true, "ext-up"
		}); err != nil || m2.ID != m.ID {
			t.Errorf("upsert cùng externalId: %+v %v", m2, err)
		}
	})
}

func TestAdminReplaceSites(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.adminFixture()
		ctx := context.Background()
		d := f.svc.Deps
		d.GoogleClientID = "primary-gid"
		f.svc = application.NewService(d)
		good := fhir.ProjectSite{Name: "web", Domain: []string{"App.Example.org"}, GoogleClientId: "g1", GoogleClientSecret: "gs1", RecaptchaSiteKey: "rk1", RecaptchaSecretKey: "rs1"}
		v, err := f.svc.AdminReplaceSites(ctx, f.caller, f.project.ID, []fhir.ProjectSite{good})
		if err != nil {
			t.Fatal(err)
		}
		if v.Site[0].GoogleClientSecret == "gs1" || v.Site[0].RecaptchaSecretKey == "rs1" || v.Site[0].GoogleClientId != "g1" {
			t.Errorf("phản hồi phải che secret: %+v", v.Site)
		}
		cur, _ := f.svc.Projects.Get(ctx, f.project.ID)
		if cur.Site[0].GoogleClientSecret != "gs1" || cur.Site[0].RecaptchaSecretKey != "rs1" {
			t.Fatalf("kho phải giữ secret thật: %+v", cur.Site)
		}
		// gửi lại giá trị che: giữ secret cũ; đổi googleClientId nhưng giữ secret
		keep := good
		keep.GoogleClientSecret, keep.RecaptchaSecretKey, keep.GoogleClientId = "********", "********", "g1b"
		if _, err = f.svc.AdminReplaceSites(ctx, f.caller, f.project.ID, []fhir.ProjectSite{keep}); err != nil {
			t.Fatal(err)
		}
		if cur, _ = f.svc.Projects.Get(ctx, f.project.ID); cur.Site[0].GoogleClientSecret != "gs1" || cur.Site[0].GoogleClientId != "g1b" {
			t.Errorf("giữ secret: %+v", cur.Site)
		}
		// site mới không có secret cũ để giữ
		nw := keep
		nw.Name = "moi"
		_, err = f.svc.AdminReplaceSites(ctx, f.caller, f.project.ID, []fhir.ProjectSite{nw})
		wantErr(t, err, domain.KindBadRequest, "googleClientSecret has no existing value to keep")

		bad := map[string][]fhir.ProjectSite{
			"thiếu tên":                       {{Name: " "}},
			"trùng tên":                       {{Name: "a"}, {Name: "a"}},
			"domain sai":                      {{Name: "a", Domain: []string{"a b.org"}}},
			"domain có @":                     {{Name: "a", Domain: []string{"x@a.org"}}},
			"google thiếu secret":             {{Name: "a", GoogleClientId: "g"}},
			"recaptcha thiếu id":              {{Name: "a", RecaptchaSecretKey: "s"}},
			"quá dài":                         {{Name: "a", GoogleClientId: strings.Repeat("x", 513), GoogleClientSecret: "s"}},
			"trùng googleId trong danh sách":  {{Name: "a", GoogleClientId: "g", GoogleClientSecret: "s"}, {Name: "b", GoogleClientId: "g", GoogleClientSecret: "s"}},
			"trùng recaptcha trong danh sách": {{Name: "a", RecaptchaSiteKey: "k", RecaptchaSecretKey: "s"}, {Name: "b", RecaptchaSiteKey: "k", RecaptchaSecretKey: "s"}},
		}
		for name, in := range bad {
			if _, err := f.svc.AdminReplaceSites(ctx, f.caller, f.project.ID, in); !domain.Is(err, domain.KindBadRequest, "") {
				t.Errorf("%s: %v", name, err)
			}
		}
		many := make([]fhir.ProjectSite, 21)
		for i := range many {
			many[i] = fhir.ProjectSite{Name: strings.Repeat("n", i+1)}
		}
		if _, err := f.svc.AdminReplaceSites(ctx, f.caller, f.project.ID, many); !domain.Is(err, domain.KindBadRequest, "") {
			t.Errorf("quá nhiều site: %v", err)
		}
		_, err = f.svc.AdminReplaceSites(ctx, f.caller, f.project.ID, []fhir.ProjectSite{{Name: "p", GoogleClientId: "primary-gid", GoogleClientSecret: "s"}})
		wantErr(t, err, domain.KindConflict, "googleClientId is reserved by the server")
		// lỗi không đổi gì
		if cur, _ = f.svc.Projects.Get(ctx, f.project.ID); len(cur.Site) != 1 || cur.Site[0].GoogleClientId != "g1b" {
			t.Errorf("lỗi không được đổi site: %+v", cur.Site)
		}
		plain := *f.caller
		pm := *f.am
		pm.Admin = nil
		plain.Membership = &pm
		_, err = f.svc.AdminReplaceSites(ctx, &plain, f.project.ID, nil)
		wantErr(t, err, domain.KindForbidden, "")
	})
}

func TestAdminReplaceSitesUniqueAcrossProjects(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.adminFixture()
		ctx := context.Background()
		e.project(func(p *fhir.Project) {
			p.Site = []fhir.ProjectSite{{Name: "x", GoogleClientId: "dung", GoogleClientSecret: "s", RecaptchaSiteKey: "kdung", RecaptchaSecretKey: "s"}}
		})
		_, err := f.svc.AdminReplaceSites(ctx, f.caller, f.project.ID, []fhir.ProjectSite{{Name: "a", GoogleClientId: "dung", GoogleClientSecret: "s"}})
		wantErr(t, err, domain.KindConflict, "googleClientId is already used by another project")
		_, err = f.svc.AdminReplaceSites(ctx, f.caller, f.project.ID, []fhir.ProjectSite{{Name: "a", RecaptchaSiteKey: "kdung", RecaptchaSecretKey: "s"}})
		wantErr(t, err, domain.KindConflict, "recaptchaSiteKey is already used by another project")
		// chính project mình giữ lại giá trị của mình thì không xung đột
		mine := []fhir.ProjectSite{{Name: "a", GoogleClientId: "cua-toi", GoogleClientSecret: "s", RecaptchaSiteKey: "k-toi", RecaptchaSecretKey: "s"}}
		for i := 0; i < 2; i++ {
			if _, err = f.svc.AdminReplaceSites(ctx, f.caller, f.project.ID, mine); err != nil {
				t.Fatalf("lần %d: %v", i+1, err)
			}
		}
		if err := func() error {
			_, err := f.svc.AdminReplaceSites(ctx, f.caller, f.project.ID, []fhir.ProjectSite{})
			return err
		}(); err != nil {
			t.Errorf("danh sách rỗng xóa hết site: %v", err)
		}
	})
}
