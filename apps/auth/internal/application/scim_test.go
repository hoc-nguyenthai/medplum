package application_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"trivita-fhir/apps/auth/internal/application"
	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/apps/auth/internal/domain/scim"
	"trivita-fhir/pkg/fhir"
)

type scimFix struct {
	*admFix
	patientPol, practPol *fhir.AccessPolicy
}

func (e *env) scimFixture() *scimFix {
	f := &scimFix{admFix: e.adminFixture()}
	f.patientPol = put(e, &fhir.AccessPolicy{ResourceType: "AccessPolicy", Name: "pt", Meta: &fhir.Meta{Project: f.project.ID}})
	f.practPol = put(e, &fhir.AccessPolicy{ResourceType: "AccessPolicy", Name: "pr", Meta: &fhir.Meta{Project: f.project.ID}})
	cp := *f.project
	cp.Features = []string{"scim"}
	cp.DefaultPatientAccessPolicy = &fhir.Reference{Reference: "AccessPolicy/" + f.patientPol.ID}
	cp.DefaultAccessPolicies = []fhir.ProjectDefaultAccessPolicies{{ProfileType: "Practitioner", AccessPolicy: &fhir.Reference{Reference: "AccessPolicy/" + f.practPol.ID}}}
	up, err := e.repo.UpdateResource(context.Background(), &cp, fhirUpdate())
	if err != nil {
		e.t.Fatal(err)
	}
	f.project = up.(*fhir.Project)
	f.caller.Project = f.project
	d := f.svc.Deps
	d.BaseURL = "https://auth.example.org/"
	d.Scim = application.ScimConfig{Enabled: true, AllowBasic: true, MaxPage: 3, MaxPatchOps: 5}
	f.svc = application.NewService(d)
	return f
}

func (f *scimFix) create(userName, email, userType string, mut func(*scim.User)) (*scim.User, error) {
	u := scim.User{Schemas: []string{scim.SchemaUser}, UserName: userName, UserType: userType, Name: &scim.Name{GivenName: "Lan", FamilyName: "Tran"}, Emails: []scim.Email{{Value: email}}}
	if mut != nil {
		mut(&u)
	}
	return f.svc.ScimCreate(context.Background(), f.caller, u)
}

func problemOf(t *testing.T, err error, status int, scimType string) *scim.ProblemError {
	t.Helper()
	var pe *scim.ProblemError
	if !errors.As(err, &pe) || pe.Status != status || (scimType != "" && pe.ScimType != scimType) {
		t.Fatalf("mong ProblemError %d %s, được %#v (%v)", status, scimType, pe, err)
	}
	return pe
}

func patchOf(op, path, value string) scim.PatchOp {
	o := scim.PatchOperation{Op: op, Path: path}
	if value != "" {
		o.Value = []byte(value)
	}
	return scim.PatchOp{Schemas: []string{scim.SchemaPatch}, Operations: []scim.PatchOperation{o}}
}

func TestScimOktaScenario(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.scimFixture()
		ctx := context.Background()
		// danh sách trống chỉ có admin (Practitioner của adminFixture là membership có profile Practitioner/p1)
		list, err := f.svc.ScimList(ctx, f.caller, application.ScimListParams{})
		if err != nil || list.StartIndex != 1 || list.Schemas[0] != scim.SchemaList || list.TotalResults != list.ItemsPerPage {
			t.Fatalf("list: %+v %v", list, err)
		}
		before := list.TotalResults
		// filter không có kết quả
		none, err := f.svc.ScimList(ctx, f.caller, application.ScimListParams{Filter: `userName eq "abcdefgh@atko.com"`})
		if err != nil || none.TotalResults != 0 || none.Resources == nil || len(none.Resources) != 0 {
			t.Fatalf("filter không khớp: %+v %v", none, err)
		}
		// tạo
		c, err := f.create("Alice.Okta@atko.com", "Alice.Okta@Atko.com", "", func(u *scim.User) { u.Active = ptr(true) })
		if err != nil {
			t.Fatal(err)
		}
		if c.UserName != "Alice.Okta@atko.com" || c.UserType != "Practitioner" || !*c.Active || c.Name.GivenName != "Lan" || c.Emails[0].Value != "alice.okta@atko.com" ||
			c.Schemas[0] != scim.SchemaUser || c.Meta.Location != "https://auth.example.org/scim/v2/Users/"+c.ID || c.Meta.ResourceType != "User" {
			t.Fatalf("created: %+v meta=%+v", c, c.Meta)
		}
		g, err := f.svc.ScimGet(ctx, f.caller, c.ID)
		if err != nil || g.UserName != c.UserName || g.Emails[0].Value != c.Emails[0].Value {
			t.Fatalf("get: %+v %v", g, err)
		}
		// tạo lại: 409
		_, err = f.create("Alice.Okta@atko.com", "Alice.Okta@Atko.com", "", nil)
		wantErr(t, err, domain.KindConflict, "User is already a member of this project")
		// filter không phân biệt hoa thường
		hit, err := f.svc.ScimList(ctx, f.caller, application.ScimListParams{Filter: `userName eq "ALICE.OKTA@ATKO.COM"`})
		if err != nil || hit.TotalResults != 1 || hit.Resources[0].ID != c.ID {
			t.Fatalf("filter hoa: %+v %v", hit, err)
		}
		all, _ := f.svc.ScimList(ctx, f.caller, application.ScimListParams{})
		if all.TotalResults != before+1 {
			t.Errorf("tổng: %d", all.TotalResults)
		}
		// id không tồn tại
		_, err = f.svc.ScimGet(ctx, f.caller, "khong-co")
		if pe := problemOf(t, err, 404, ""); pe.Detail != "Not found" {
			t.Errorf("detail: %q", pe.Detail)
		}
	})
}

func TestScimCreateRequirements(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.scimFixture()
		ctx := context.Background()
		_, err := f.create("", "a@x.org", "", nil)
		problemOf(t, err, 400, "invalidValue")
		_, err = f.create("u", "  ", "", nil)
		problemOf(t, err, 400, "invalidValue")
		_, err = f.create("u", "a@x.org", "", func(u *scim.User) { u.Emails = nil })
		problemOf(t, err, 400, "invalidValue")
		// userType lạ thành Practitioner; chặn "Bot"
		c, err := f.create("u1", "u1@x.org", "Bot", nil)
		if err != nil || c.UserType != "Practitioner" {
			t.Fatalf("userType lạ: %+v %v", c, err)
		}
		// Patient: policy mặc định của Patient; thiếu thì 400
		p, err := f.create("p1", "p1@x.org", "Patient", nil)
		if err != nil || p.UserType != "Patient" {
			t.Fatalf("patient: %+v %v", p, err)
		}
		m, _ := f.svc.Memberships.Get(ctx, p.ID)
		if refOfTest(m.AccessPolicy) != "AccessPolicy/"+f.patientPol.ID || domain.IsTrue(m.Admin) || m.UserName != "p1" || refOfTest(m.InvitedBy) != "User/"+f.au.ID {
			t.Errorf("membership của patient: %+v", m)
		}
		cp := *f.project
		cp.DefaultPatientAccessPolicy, cp.DefaultAccessPolicies = nil, nil
		up, err := e.repo.UpdateResource(ctx, &cp, fhirUpdate())
		if err != nil {
			t.Fatal(err)
		}
		f.caller.Project = up.(*fhir.Project)
		_, err = f.create("p2", "p2@x.org", "Patient", nil)
		if pe := problemOf(t, err, 400, "invalidValue"); pe.Detail != "Missing default patient access policy" {
			t.Errorf("patient: %q", pe.Detail)
		}
		_, err = f.create("u2", "u2@x.org", "", nil)
		if pe := problemOf(t, err, 400, "invalidValue"); pe.Detail != "Missing default practitioner access policy" {
			t.Errorf("practitioner: %q", pe.Detail)
		}
		// RelatedPerson không tạo mới được (cần patient)
		cp2 := *up.(*fhir.Project)
		cp2.DefaultAccessPolicies = []fhir.ProjectDefaultAccessPolicies{{ProfileType: "Practitioner", AccessPolicy: &fhir.Reference{Reference: "AccessPolicy/" + f.practPol.ID}}}
		if up, err = e.repo.UpdateResource(ctx, &cp2, fhirUpdate()); err != nil {
			t.Fatal(err)
		}
		f.caller.Project = up.(*fhir.Project)
		_, err = f.create("r1", "r1@x.org", "RelatedPerson", nil)
		wantErr(t, err, domain.KindBadRequest, "Patient is required to create a RelatedPerson")
		// externalId trùng trong project: 409
		if _, err = f.create("e1", "e1@x.org", "", func(u *scim.User) { u.ExternalID = sp("ext-1") }); err != nil {
			t.Fatal(err)
		}
		_, err = f.create("e2", "e2@x.org", "", func(u *scim.User) { u.ExternalID = sp("ext-1") })
		wantErr(t, err, domain.KindConflict, "externalId already exists in this project")
	})
}

func sp(s string) *string { return &s }

func TestScimCreateIgnoresPrivilegeFields(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.scimFixture()
		// struct SCIM không có chỗ cho admin/accessPolicy: dù body JSON có, decode bỏ qua; kiểm kết quả qua membership
		c, err := f.create("x", "x@x.org", "", nil)
		if err != nil {
			t.Fatal(err)
		}
		m, _ := f.svc.Memberships.Get(context.Background(), c.ID)
		if domain.IsTrue(m.Admin) || refOfTest(m.AccessPolicy) != "AccessPolicy/"+f.practPol.ID || len(m.Access) != 0 {
			t.Errorf("membership: %+v", m)
		}
	})
}

func TestScimAuthorization(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.scimFixture()
		ctx := context.Background()
		_, err := f.svc.ScimList(ctx, nil, application.ScimListParams{})
		wantErr(t, err, domain.KindUnauthorized, "")
		plain := *f.caller
		pm := *f.am
		pm.Admin = nil
		plain.Membership = &pm
		_, err = f.svc.ScimList(ctx, &plain, application.ScimListParams{})
		wantErr(t, err, domain.KindForbidden, "")
		_, err = f.svc.ScimCreate(ctx, &plain, scim.User{UserName: "x", Emails: []scim.Email{{Value: "x@x.org"}}})
		wantErr(t, err, domain.KindForbidden, "")
		wantErr(t, f.svc.ScimDelete(ctx, &plain, "x"), domain.KindForbidden, "")
		// project chưa bật tính năng scim
		cp := *f.project
		cp.Features = nil
		up, err := e.repo.UpdateResource(ctx, &cp, fhirUpdate())
		if err != nil {
			t.Fatal(err)
		}
		f.caller.Project = up.(*fhir.Project)
		_, err = f.svc.ScimList(ctx, f.caller, application.ScimListParams{})
		wantErr(t, err, domain.KindForbidden, "SCIM is not enabled for this project")
	})
}

func TestScimIsolationAndMachineAccounts(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.scimFixture()
		ctx := context.Background()
		// membership của project khác, của Bot, của ClientApplication, user không phải 3 loại
		op := e.project(nil)
		ou := e.user("o@scim.example.org", nil)
		om := e.membership(ou, op, nil)
		cl := put(e, &fhir.ClientApplication{ResourceType: "ClientApplication", Name: "c", Meta: &fhir.Meta{Project: f.project.ID}})
		cm := e.membership(ou, f.project, func(m *fhir.ProjectMembership) { m.Profile = &fhir.Reference{Reference: "ClientApplication/" + cl.ID} })
		bm := e.membership(ou, f.project, func(m *fhir.ProjectMembership) { m.Profile = &fhir.Reference{Reference: "Bot/b1"} })
		for name, id := range map[string]string{"project khác": om.ID, "client": cm.ID, "bot": bm.ID, "không tồn tại": "khong-co"} {
			if _, err := f.svc.ScimGet(ctx, f.caller, id); !errors.As(err, new(*scim.ProblemError)) {
				t.Errorf("get %s: %v", name, err)
			} else {
				problemOf(t, err, 404, "")
			}
			_, err := f.svc.ScimReplace(ctx, f.caller, id, scim.User{Emails: []scim.Email{{Value: "z@x.org"}}})
			problemOf(t, err, 404, "")
			_, err = f.svc.ScimPatch(ctx, f.caller, id, patchOf("replace", "active", "false"))
			problemOf(t, err, 404, "")
			problemOf(t, f.svc.ScimDelete(ctx, f.caller, id), 404, "")
		}
		for _, id := range []string{om.ID, cm.ID, bm.ID} {
			if _, err := f.svc.Memberships.Get(ctx, id); err != nil {
				t.Errorf("membership %s bị xóa nhầm: %v", id, err)
			}
		}
		if got, _ := f.svc.Memberships.Get(ctx, cm.ID); got.Active != nil && !*got.Active {
			t.Error("membership của client bị tắt")
		}
		// danh sách không có membership project khác, client, bot; filter không rò userName của project khác
		e.membership(e.user("same@scim.example.org", nil), op, func(m *fhir.ProjectMembership) { m.UserName = "dung-chung" })
		l, _ := f.svc.ScimList(ctx, f.caller, application.ScimListParams{})
		for _, r := range l.Resources {
			if r.ID == om.ID || r.ID == cm.ID || r.ID == bm.ID {
				t.Errorf("rò membership: %+v", r)
			}
		}
		l, _ = f.svc.ScimList(ctx, f.caller, application.ScimListParams{Filter: `userName eq "dung-chung"`})
		if l.TotalResults != 0 {
			t.Errorf("rò userName project khác: %+v", l)
		}
	})
}

func TestScimSelfAndOwnerProtection(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.scimFixture()
		ctx := context.Background()
		// người gọi (admin, đồng thời là chủ project) không tắt/xóa chính mình
		_, err := f.svc.ScimPatch(ctx, f.caller, f.am.ID, patchOf("replace", "active", "false"))
		problemOf(t, err, 400, "invalidValue")
		problemOf(t, f.svc.ScimDelete(ctx, f.caller, f.am.ID), 400, "invalidValue")
		// admin khác gọi: không tắt/xóa chủ project
		_, m2 := f.member("b@scim.example.org", true, nil)
		caller2 := &application.AuthState{Login: f.caller.Login, Membership: m2, Project: f.project}
		_, err = f.svc.ScimPatch(ctx, caller2, f.am.ID, patchOf("replace", "active", "false"))
		problemOf(t, err, 400, "invalidValue")
		wantErr(t, f.svc.ScimDelete(ctx, caller2, f.am.ID), domain.KindBadRequest, "Cannot delete the owner of the project")
		cur, _ := f.svc.Memberships.Get(ctx, f.am.ID)
		if cur.Active != nil && !*cur.Active {
			t.Error("chủ project bị tắt")
		}
	})
}

func TestScimReplaceAndPatch(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.scimFixture()
		ctx := context.Background()
		c, err := f.create("pt1", "Pt1@X.org", "Patient", nil) // Patient: user thuộc project
		if err != nil {
			t.Fatal(err)
		}
		m, _ := f.svc.Memberships.Get(ctx, c.ID)
		user, _ := f.svc.Users.Get(ctx, idOfTest(m.User))
		if refOfTest(user.Project) != "Project/"+f.project.ID {
			t.Fatalf("patient phải thuộc project: %+v", user)
		}
		// một security request chưa dùng và một login: đổi email chữ hoa/thường khác thì KHÔNG retire/revoke
		sr := put(e, &fhir.UserSecurityRequest{ResourceType: "UserSecurityRequest", Type: "reset", User: &fhir.Reference{Reference: "User/" + user.ID}, Meta: &fhir.Meta{Project: f.project.ID},
			ExpiresAt: e.clock.Now().Add(3600e9).UTC().Format("2006-01-02T15:04:05.000Z")})
		lg := put(e, &fhir.Login{ResourceType: "Login", AuthMethod: "password", User: &fhir.Reference{Reference: "User/" + user.ID}})
		in := scim.User{ID: c.ID, Name: &scim.Name{GivenName: "Moi", FamilyName: "Ten"}, ExternalID: sp("ext-9"), Emails: []scim.Email{{Value: "PT1@x.org"}}, Active: ptr(true)}
		out, err := f.svc.ScimReplace(ctx, f.caller, c.ID, in)
		if err != nil || out.Name.GivenName != "Moi" || *out.ExternalID != "ext-9" || out.Emails[0].Value != "pt1@x.org" {
			t.Fatalf("PUT: %+v %v", out, err)
		}
		if got, _ := f.svc.SecurityRequests.Get(ctx, sr.ID); domain.IsTrue(got.Used) {
			t.Error("cùng email (khác hoa thường) không được retire security request")
		}
		if got, _ := f.svc.Logins.Get(ctx, lg.ID); domain.IsTrue(got.Revoked) {
			t.Error("cùng email không được revoke login")
		}
		// id trong body khác đường dẫn: 400
		_, err = f.svc.ScimReplace(ctx, f.caller, c.ID, scim.User{ID: "khac"})
		problemOf(t, err, 400, "invalidValue")
		// PATCH đổi email thật + tắt: emailVerified=false, retire, revoke
		if _, err := f.svc.Users.Mutate(ctx, user.ID, func(x *fhir.User) error { x.EmailVerified = ptr(true); return nil }); err != nil {
			t.Fatal(err)
		}
		p := scim.PatchOp{Schemas: []string{scim.SchemaPatch}, Operations: []scim.PatchOperation{{Op: "replace", Value: []byte(`{"active":false,"emails":[{"value":"New@X.org"}]}`)}}}
		out, err = f.svc.ScimPatch(ctx, f.caller, c.ID, p)
		if err != nil || *out.Active || len(out.Emails) != 1 || out.Emails[0].Value != "new@x.org" {
			t.Fatalf("PATCH: %+v %v", out, err)
		}
		got, _ := f.svc.Users.Get(ctx, user.ID)
		if got.Email != "new@x.org" || domain.IsTrue(got.EmailVerified) {
			t.Errorf("user: %+v", got)
		}
		if g, _ := f.svc.SecurityRequests.Get(ctx, sr.ID); !domain.IsTrue(g.Used) {
			t.Error("security request phải bị retire")
		}
		if g, _ := f.svc.Logins.Get(ctx, lg.ID); !domain.IsTrue(g.Revoked) {
			t.Error("login phải bị revoke")
		}
		if mm, _ := f.svc.Memberships.Get(ctx, c.ID); mm.Active == nil || *mm.Active {
			t.Error("membership phải bị tắt")
		}
		// email không hợp lệ, email trùng user khác cùng project
		_, err = f.svc.ScimReplace(ctx, f.caller, c.ID, scim.User{Emails: []scim.Email{{Value: "khong-phai-email"}}})
		problemOf(t, err, 400, "invalidValue")
		other, err := f.create("pt2", "pt2@x.org", "Patient", nil)
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.svc.ScimReplace(ctx, f.caller, other.ID, scim.User{Emails: []scim.Email{{Value: "new@x.org"}}})
		wantErr(t, err, domain.KindConflict, "Email already registered")
		// PATCH lỗi cú pháp và đường dẫn nguy hiểm: không đổi gì
		mBefore, _ := f.svc.Memberships.Get(ctx, c.ID)
		for _, bad := range []scim.PatchOp{patchOf("replace", "admin", "true"), patchOf("replace", "accessPolicy", `{"reference":"AccessPolicy/1"}`), patchOf("replace", "profile", `"x"`), patchOf("replace", "/active", "true")} {
			_, err := f.svc.ScimPatch(ctx, f.caller, c.ID, bad)
			problemOf(t, err, 400, "")
		}
		mAfter, _ := f.svc.Memberships.Get(ctx, c.ID)
		if mAfter.Meta.VersionId != mBefore.Meta.VersionId {
			t.Error("PATCH bị từ chối không được ghi")
		}
		// quá số thao tác
		many := scim.PatchOp{Schemas: []string{scim.SchemaPatch}}
		for i := 0; i < 6; i++ {
			many.Operations = append(many.Operations, scim.PatchOperation{Op: "replace", Path: "active", Value: []byte("true")})
		}
		_, err = f.svc.ScimPatch(ctx, f.caller, c.ID, many)
		problemOf(t, err, 400, "tooMany")
	})
}

func TestScimServerScopedUserIsReadOnlyExceptMembership(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.scimFixture()
		ctx := context.Background()
		c, err := f.create("pr1", "Pr1@X.org", "", nil) // Practitioner không externalId: User phạm vi server
		if err != nil {
			t.Fatal(err)
		}
		m, _ := f.svc.Memberships.Get(ctx, c.ID)
		user, _ := f.svc.Users.Get(ctx, idOfTest(m.User))
		if user.Project != nil {
			t.Fatalf("phải là user phạm vi server: %+v", user)
		}
		// đổi email thật: 403, không đổi gì
		_, err = f.svc.ScimReplace(ctx, f.caller, c.ID, scim.User{Emails: []scim.Email{{Value: "khac@x.org"}}})
		wantErr(t, err, domain.KindForbidden, "Cannot change the email of a user outside this project")
		_, err = f.svc.ScimPatch(ctx, f.caller, c.ID, patchOf("replace", "emails", `[{"value":"khac@x.org"}]`))
		wantErr(t, err, domain.KindForbidden, "")
		// cùng email khác hoa thường + đổi tên: 200 nhưng tên của User không đổi; active ghi được vào membership
		out, err := f.svc.ScimReplace(ctx, f.caller, c.ID, scim.User{Name: &scim.Name{GivenName: "Hack", FamilyName: "Er"}, Emails: []scim.Email{{Value: "PR1@x.org"}}, Active: ptr(false)})
		if err != nil || *out.Active {
			t.Fatalf("PUT: %+v %v", out, err)
		}
		got, _ := f.svc.Users.Get(ctx, user.ID)
		if got.FirstName != user.FirstName || got.Email != user.Email || got.Meta.VersionId != user.Meta.VersionId {
			t.Errorf("user server-scope không được đổi: %+v", got)
		}
		// PATCH active=false khi đã false: versionId của user không đổi
		if _, err := f.svc.ScimPatch(ctx, f.caller, c.ID, patchOf("replace", "active", "true")); err != nil {
			t.Fatal(err)
		}
		if g, _ := f.svc.Users.Get(ctx, user.ID); g.Meta.VersionId != user.Meta.VersionId {
			t.Error("PATCH active không được ghi User")
		}
	})
}

func TestScimEmailChangeIsAtomic(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		if strings.HasSuffix(t.Name(), "/memory") {
			t.Skip("kho bộ nhớ không có transaction")
		}
		f := e.scimFixture()
		ctx := context.Background()
		c, err := f.create("pt1", "pt1@x.org", "Patient", nil)
		if err != nil {
			t.Fatal(err)
		}
		m, _ := f.svc.Memberships.Get(ctx, c.ID)
		user, _ := f.svc.Users.Get(ctx, idOfTest(m.User))
		lg := put(e, &fhir.Login{ResourceType: "Login", AuthMethod: "password", User: &fhir.Reference{Reference: "User/" + user.ID}})
		// transaction báo lỗi SAU khi fn chạy xong: mọi ghi phải hoàn tác
		d := f.svc.Deps
		d.Tx = boomTx{d.Tx}
		boom := application.NewService(d)
		_, err = boom.ScimPatch(ctx, f.caller, c.ID, patchOf("replace", "emails", `[{"value":"moi@x.org"}]`))
		if err == nil {
			t.Fatal("phải lỗi")
		}
		got, _ := f.svc.Users.Get(ctx, user.ID)
		if got.Email != "pt1@x.org" {
			t.Errorf("email phải được hoàn tác: %s", got.Email)
		}
		if l, _ := f.svc.Logins.Get(ctx, lg.ID); domain.IsTrue(l.Revoked) {
			t.Error("revoke phải được hoàn tác")
		}
	})
}

type boomTx struct{ application.Transactor }

func (b boomTx) InTx(ctx context.Context, fn func(application.TxRepos) error) error {
	return b.Transactor.InTx(ctx, func(tx application.TxRepos) error {
		if err := fn(tx); err != nil {
			return err
		}
		return errors.New("lỗi giả sau khi ghi")
	})
}

func TestScimDelete(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.scimFixture()
		ctx := context.Background()
		pt, err := f.create("pt1", "pt1@x.org", "Patient", nil)
		if err != nil {
			t.Fatal(err)
		}
		pm, _ := f.svc.Memberships.Get(ctx, pt.ID)
		if err := f.svc.ScimDelete(ctx, f.caller, pt.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := f.svc.Users.Get(ctx, idOfTest(pm.User)); !domain.Is(err, domain.KindNotFound, "") {
			t.Errorf("User thuộc project hết membership phải bị xóa: %v", err)
		}
		problemOf(t, f.svc.ScimDelete(ctx, f.caller, pt.ID), 404, "")
		pr, err := f.create("pr1", "pr1@x.org", "", nil)
		if err != nil {
			t.Fatal(err)
		}
		prm, _ := f.svc.Memberships.Get(ctx, pr.ID)
		if err := f.svc.ScimDelete(ctx, f.caller, pr.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := f.svc.Users.Get(ctx, idOfTest(prm.User)); err != nil {
			t.Errorf("User phạm vi server phải được giữ: %v", err)
		}
		l, _ := f.svc.ScimList(ctx, f.caller, application.ScimListParams{Filter: `userName eq "pt1"`})
		if l.TotalResults != 0 {
			t.Error("đã xóa vẫn còn trong danh sách")
		}
	})
}

func TestScimPagination(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.scimFixture()
		ctx := context.Background()
		for i := 0; i < 5; i++ {
			if _, err := f.create("u"+string(rune('a'+i)), "u"+string(rune('a'+i))+"@x.org", "", nil); err != nil {
				t.Fatal(err)
			}
		}
		full, _ := f.svc.ScimList(ctx, f.caller, application.ScimListParams{Count: ptr(3)})
		total := full.TotalResults
		if total < 6 || full.ItemsPerPage != 3 || len(full.Resources) != 3 || full.StartIndex != 1 {
			t.Fatalf("trang 1: %+v", full)
		}
		// MaxPage=3 là trần: count lớn bị cắt
		big, _ := f.svc.ScimList(ctx, f.caller, application.ScimListParams{Count: ptr(1000)})
		if len(big.Resources) != 3 || big.TotalResults != total {
			t.Errorf("trần: %d/%d", len(big.Resources), big.TotalResults)
		}
		// mặc định cũng là trần
		def, _ := f.svc.ScimList(ctx, f.caller, application.ScimListParams{})
		if len(def.Resources) != 3 {
			t.Errorf("mặc định: %d", len(def.Resources))
		}
		// trang 2 liền kề, không trùng, ổn định theo id
		p2, _ := f.svc.ScimList(ctx, f.caller, application.ScimListParams{StartIndex: ptr(4), Count: ptr(3)})
		seen := map[string]bool{}
		for _, r := range full.Resources {
			seen[r.ID] = true
		}
		for _, r := range p2.Resources {
			if seen[r.ID] {
				t.Errorf("trùng giữa hai trang: %s", r.ID)
			}
		}
		if p2.StartIndex != 4 || p2.ItemsPerPage != len(p2.Resources) || p2.TotalResults != total {
			t.Errorf("trang 2: %+v", p2)
		}
		for i := 1; i < len(full.Resources); i++ {
			if full.Resources[i-1].ID >= full.Resources[i].ID {
				t.Error("không sắp theo id")
			}
		}
		// count=0 chỉ trả tổng; startIndex<1 coi là 1; startIndex vượt tổng: rỗng
		z, _ := f.svc.ScimList(ctx, f.caller, application.ScimListParams{Count: ptr(0)})
		if z.TotalResults != total || len(z.Resources) != 0 || z.Resources == nil {
			t.Errorf("count=0: %+v", z)
		}
		lo, _ := f.svc.ScimList(ctx, f.caller, application.ScimListParams{StartIndex: ptr(-5), Count: ptr(1)})
		if lo.StartIndex != 1 || len(lo.Resources) != 1 || lo.Resources[0].ID != full.Resources[0].ID {
			t.Errorf("startIndex<1: %+v", lo)
		}
		hi, _ := f.svc.ScimList(ctx, f.caller, application.ScimListParams{StartIndex: ptr(999)})
		if len(hi.Resources) != 0 || hi.TotalResults != total {
			t.Errorf("vượt tổng: %+v", hi)
		}
		neg, _ := f.svc.ScimList(ctx, f.caller, application.ScimListParams{Count: ptr(-1)})
		if len(neg.Resources) != 0 {
			t.Errorf("count âm: %+v", neg)
		}
		// filter lạ: 400 invalidFilter
		_, err := f.svc.ScimList(ctx, f.caller, application.ScimListParams{Filter: `active eq true`})
		problemOf(t, err, 400, "invalidFilter")
	})
}

func TestScimBasicAuth(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.scimFixture()
		ctx := context.Background()
		secret := strings.Repeat("s", 40)
		cl := put(e, &fhir.ClientApplication{ResourceType: "ClientApplication", Name: "okta", Meta: &fhir.Meta{Project: f.project.ID}, Secret: secret})
		cuser := &fhir.Reference{Reference: "ClientApplication/" + cl.ID}
		cm := put(e, &fhir.ProjectMembership{ResourceType: "ProjectMembership", Project: &fhir.Reference{Reference: "Project/" + f.project.ID}, User: cuser, Profile: cuser, Meta: &fhir.Meta{Project: f.project.ID}})
		// client không phải admin: xác thực được nhưng bị 403 ở use case
		st, err := f.svc.ScimBasicAuth(ctx, cl.ID, secret)
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.svc.ScimList(ctx, st, application.ScimListParams{})
		wantErr(t, err, domain.KindForbidden, "")
		// client có admin: dùng được, và tạo user với invitedBy là client
		if _, err := f.svc.Memberships.Mutate(ctx, cm.ID, func(m *fhir.ProjectMembership) error { m.Admin = ptr(true); return nil }); err != nil {
			t.Fatal(err)
		}
		if st, err = f.svc.ScimBasicAuth(ctx, cl.ID, secret); err != nil {
			t.Fatal(err)
		}
		c, err := f.svc.ScimCreate(ctx, st, scim.User{UserName: "viaokta", Name: &scim.Name{GivenName: "A", FamilyName: "B"}, Emails: []scim.Email{{Value: "viaokta@x.org"}}})
		if err != nil {
			t.Fatal(err)
		}
		if m, _ := f.svc.Memberships.Get(ctx, c.ID); refOfTest(m.InvitedBy) != "ClientApplication/"+cl.ID {
			t.Errorf("invitedBy: %+v", m.InvitedBy)
		}
		for name, tc := range map[string][2]string{"sai secret": {cl.ID, "sai"}, "client lạ": {"khong-co", secret}, "thiếu": {"", ""}} {
			if _, err := f.svc.ScimBasicAuth(ctx, tc[0], tc[1]); !domain.Is(err, domain.KindUnauthorized, "Unauthorized") {
				t.Errorf("%s: %v", name, err)
			}
		}
		// client bị đóng, project bị đóng
		closed := *cl
		closed.Status = "off"
		if _, err := e.repo.UpdateResource(ctx, &closed, fhirUpdate()); err != nil {
			t.Fatal(err)
		}
		if _, err := f.svc.ScimBasicAuth(ctx, cl.ID, secret); !domain.Is(err, domain.KindUnauthorized, "Unauthorized") {
			t.Errorf("client đóng: %v", err)
		}
		// AllowBasic=false
		d := f.svc.Deps
		d.Scim.AllowBasic = false
		if _, err := application.NewService(d).ScimBasicAuth(ctx, cl.ID, secret); !domain.Is(err, domain.KindUnauthorized, "Unauthorized") {
			t.Errorf("tắt Basic: %v", err)
		}
	})
}
