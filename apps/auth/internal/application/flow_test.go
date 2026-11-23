package application_test

import (
	"context"
	"strings"
	"testing"

	"trivita-fhir/apps/auth/internal/application"
	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
)

func TestSelectProfile(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		f := e.fixture()
		m2 := e.membership(f.u, e.project(nil), nil)
		l := e.login(f.u, nil) // chưa chọn profile

		_, _, err := e.svc.SelectProfile(ctx, "khong-co", f.m.ID)
		wantErr(t, err, domain.KindNotFound, "")
		_, _, err = e.svc.SelectProfile(ctx, l.ID, "khong-co")
		wantErr(t, err, domain.KindBadRequest, "Profile not found")
		other := e.membership(e.pwUser("khac@example.org", nil), f.p, nil)
		_, _, err = e.svc.SelectProfile(ctx, l.ID, other.ID)
		wantErr(t, err, domain.KindBadRequest, "Invalid profile")

		res, updated, err := e.svc.SelectProfile(ctx, l.ID, m2.ID)
		if err != nil || !e.codeStoredAs(t, l.ID, res) || updated.Membership.Reference != "ProjectMembership/"+m2.ID {
			t.Fatalf("%+v %v", res, err)
		}
		_, _, err = e.svc.SelectProfile(ctx, l.ID, f.m.ID)
		wantErr(t, err, domain.KindBadRequest, "Login profile already set")
	})
}

func TestSelectScope(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		f := e.fixture()
		l := e.login(f.u, func(l *fhir.Login) {
			l.Scope = "openid profile"
			l.Membership = &fhir.Reference{Reference: "ProjectMembership/" + f.m.ID}
		})
		res, updated, err := e.svc.SelectScope(ctx, l.ID, "openid")
		if err != nil || updated.Scope != "openid" || !e.codeStoredAs(t, l.ID, res) {
			t.Fatalf("%+v %v", res, err)
		}
		_, _, err = e.svc.SelectScope(ctx, l.ID, "openid email")
		wantErr(t, err, domain.KindBadRequest, "Invalid scope")
		_, _, err = e.svc.SelectScope(ctx, "khong-co", "openid")
		wantErr(t, err, domain.KindNotFound, "")
	})
}

func TestLoginStatus(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		f := e.fixture()
		res, _, err := e.svc.LoginStatus(ctx, e.login(f.u, nil).ID)
		if err != nil || res.Login == "" {
			t.Fatalf("%+v %v", res, err)
		}
		_, _, err = e.svc.LoginStatus(ctx, e.login(f.u, func(l *fhir.Login) { l.Granted = ptr(true); l.Revoked = ptr(true) }).ID)
		wantErr(t, err, domain.KindBadRequest, "Login already granted") // granted kiểm trước revoked
		_, _, err = e.svc.LoginStatus(ctx, e.login(f.u, func(l *fhir.Login) { l.Revoked = ptr(true) }).ID)
		wantErr(t, err, domain.KindBadRequest, "Login revoked")
		_, _, err = e.svc.LoginStatus(ctx, domain.NewID())
		wantErr(t, err, domain.KindNotFound, "")
	})
}

func TestLoginMethod(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		put(e, &fhir.DomainConfiguration{ResourceType: "DomainConfiguration", Domain: "corp.example.org",
			IdentityProvider: &fhir.IdentityProvider{AuthorizeUrl: "https://idp.example.org/authorize?x=1", ClientId: "cid"}})
		put(e, &fhir.DomainConfiguration{ResourceType: "DomainConfiguration", Domain: "noclient.example.org",
			IdentityProvider: &fhir.IdentityProvider{AuthorizeUrl: "https://idp.example.org/authorize"}})
		put(e, &fhir.DomainConfiguration{ResourceType: "DomainConfiguration", Domain: "noidp.example.org"})
		put(e, &fhir.DomainConfiguration{ResourceType: "DomainConfiguration", Domain: "bad.example.org",
			IdentityProvider: &fhir.IdentityProvider{AuthorizeUrl: "idp.example.org/authorize", ClientId: "cid"}})

		for _, email := range []string{"an@example.org", "an@noclient.example.org", "an@noidp.example.org", "khong-co-at"} {
			res, err := e.svc.LoginMethod(ctx, email)
			if err != nil || res.Domain != "" || res.AuthorizeURL != "" {
				t.Errorf("%s phải trả {}: %+v %v", email, res, err)
			}
		}
		svc := *e.svc
		svc.BaseURL = "https://auth.example.org/"
		res, err := svc.LoginMethod(ctx, "An@CORP.example.org") // tên miền không phân biệt hoa thường; trả về chữ thường (lệch có chủ đích: client gửi lại domain thì so khớp được)
		if err != nil || res.Domain != "corp.example.org" {
			t.Fatalf("%+v %v", res, err)
		}
		for _, want := range []string{"https://idp.example.org/authorize?", "client_id=cid", "response_type=code", "scope=openid+profile+email", "x=1",
			"redirect_uri=https%3A%2F%2Fauth.example.org%2Fauth%2Fexternal"} {
			if !strings.Contains(res.AuthorizeURL, want) {
				t.Errorf("thiếu %q trong %s", want, res.AuthorizeURL)
			}
		}
		_, err = e.svc.LoginMethod(ctx, "an@bad.example.org")
		wantErr(t, err, domain.KindBadRequest, "Failed to construct URL for the domain")
	})
}

// ---- revoke, me

func (e *env) authState(f fixture, mut func(*fhir.Login)) *application.AuthState {
	l := e.login(f.u, func(l *fhir.Login) {
		l.Membership = &fhir.Reference{Reference: "ProjectMembership/" + f.m.ID}
		l.Granted = ptr(true)
		if mut != nil {
			mut(l)
		}
	})
	return &application.AuthState{Login: l, Membership: f.m, Project: f.p}
}

func TestRevokeSession(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		f := e.fixture()
		a := e.authState(f, nil)
		target := e.login(f.u, nil)
		if err := e.svc.RevokeSession(ctx, a, target.ID); err != nil {
			t.Fatal(err)
		}
		if got, _ := e.r.Logins.Get(ctx, target.ID); !domain.IsTrue(got.Revoked) {
			t.Error("phải bị thu hồi")
		}
		// login của user khác: 404 (không lộ sự tồn tại)
		others := e.login(e.pwUser("khac@example.org", nil), nil)
		wantErr(t, e.svc.RevokeSession(ctx, a, others.ID), domain.KindNotFound, "")
		if got, _ := e.r.Logins.Get(ctx, others.ID); domain.IsTrue(got.Revoked) {
			t.Error("login của người khác không được bị thu hồi")
		}
		wantErr(t, e.svc.RevokeSession(ctx, a, domain.NewID()), domain.KindNotFound, "")

		// người gọi có SMART scope: 403
		smart := e.authState(f, func(l *fhir.Login) { l.Scope = "openid user/Patient.rs" })
		wantErr(t, e.svc.RevokeSession(ctx, smart, e.login(f.u, nil).ID), domain.KindForbidden, "Forbidden")
		// membership của người gọi không phải User (ClientApplication): 403
		clientM := &fhir.ProjectMembership{ID: "m", User: &fhir.Reference{Reference: "ClientApplication/c"}}
		wantErr(t, e.svc.RevokeSession(ctx, &application.AuthState{Login: a.Login, Membership: clientM, Project: f.p}, e.login(f.u, nil).ID), domain.KindForbidden, "")
	})
}

func TestRevokeSessionForNonPasswordCaller(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		f := e.fixture()
		a := e.authState(f, func(l *fhir.Login) { l.AuthMethod = "external" }) // không quản lý được tài khoản
		inProject := e.login(f.u, func(l *fhir.Login) { l.Membership = &fhir.Reference{Reference: "ProjectMembership/" + f.m.ID} })
		if err := e.svc.RevokeSession(ctx, a, inProject.ID); err != nil {
			t.Fatalf("login thuộc membership trong project hiện tại: %v", err)
		}
		otherP := e.project(nil)
		otherM := e.membership(f.u, otherP, nil)
		inOther := e.login(f.u, func(l *fhir.Login) { l.Membership = &fhir.Reference{Reference: "ProjectMembership/" + otherM.ID} })
		wantErr(t, e.svc.RevokeSession(ctx, a, inOther.ID), domain.KindNotFound, "")
		noMember := e.login(f.u, nil)
		wantErr(t, e.svc.RevokeSession(ctx, a, noMember.ID), domain.KindNotFound, "")
		for _, id := range []string{inOther.ID, noMember.ID} {
			if got, _ := e.r.Logins.Get(ctx, id); domain.IsTrue(got.Revoked) {
				t.Error("không được thu hồi")
			}
		}
	})
}

func TestGetMe(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		f := e.fixture()
		f.m.Admin = ptr(true)
		f.p.Features = []string{"bots"}
		a := e.authState(f, nil)
		// các phiên: login đã cấp token và chưa thu hồi; loại login chưa cấp, đã thu hồi, không có membership
		mem := &fhir.Reference{Reference: "ProjectMembership/" + f.m.ID}
		sess := e.login(f.u, func(l *fhir.Login) {
			l.Membership = mem
			l.Granted = ptr(true)
			l.UserAgent = "Mozilla/5.0 (Windows NT 10.0) Chrome/120.0 Safari/537.36"
			l.RemoteAddress = "9.9.9.9"
		})
		e.login(f.u, func(l *fhir.Login) { l.Membership = mem })
		e.login(f.u, func(l *fhir.Login) { l.Membership = mem; l.Granted = ptr(true); l.Revoked = ptr(true) })
		e.login(f.u, func(l *fhir.Login) { l.Granted = ptr(true) })

		me, err := e.svc.GetMe(ctx, a)
		if err != nil {
			t.Fatal(err)
		}
		if me.User == nil || me.User.Email != "an@example.org" || me.User.PasswordHash != "" {
			t.Errorf("user không được lộ passwordHash: %+v", me.User)
		}
		if me.Project.ID != f.p.ID || me.Project.Name != "Dự án" || len(me.Project.Features) != 1 {
			t.Errorf("project: %+v", me.Project)
		}
		if me.Membership.ID != f.m.ID || !domain.IsTrue(me.Membership.Admin) || me.Membership.Profile.Reference != "Practitioner/p1" {
			t.Errorf("membership: %+v", me.Membership)
		}
		if me.Profile["resourceType"] != "Practitioner" || me.Profile["id"] != "p1" || len(me.Profile) != 2 {
			t.Errorf("profile chỉ có resourceType và id (D-10): %+v", me.Profile)
		}
		if me.Config == nil || len(me.Config.Menu) != 2 || me.Config.Menu[1].Title != "Admin" {
			t.Errorf("config mặc định có menu Admin: %+v", me.Config)
		}
		sec := me.Security
		if sec == nil || sec.MfaEnrolled || sec.MfaRequired || len(sec.Memberships) != 1 || sec.Memberships[0].ID != f.m.ID {
			t.Fatalf("security: %+v", sec)
		}
		var gotSess []string
		for _, s := range sec.Sessions {
			gotSess = append(gotSess, s.ID)
		}
		if !contains(gotSess, sess.ID) || !contains(gotSess, a.Login.ID) || len(gotSess) != 2 {
			t.Errorf("chỉ các login đã cấp, chưa thu hồi, có membership: %v", gotSess)
		}
		for _, s := range sec.Sessions {
			if s.ID == sess.ID && (s.Browser != "Chrome" || s.OS != "Windows" || s.RemoteAddress != "9.9.9.9" || s.LastUpdated == "") {
				t.Errorf("%+v", s)
			}
		}
	})
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func TestGetMeVariants(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		f := e.fixture()
		// login có SMART scope: không có security (không quản lý được tài khoản)
		me, err := e.svc.GetMe(ctx, e.authState(f, func(l *fhir.Login) { l.Scope = "openid user/Patient.rs" }))
		if err != nil || me.User == nil || me.Security != nil {
			t.Errorf("SMART: %+v %v", me, err)
		}
		// login không quản lý được tài khoản: chỉ thấy phiên của membership mình
		a := e.authState(f, func(l *fhir.Login) { l.AuthMethod = "external" })
		otherM := e.membership(f.u, e.project(nil), nil)
		otherLogin := e.login(f.u, func(l *fhir.Login) {
			l.Granted = ptr(true)
			l.Membership = &fhir.Reference{Reference: "ProjectMembership/" + otherM.ID}
		})
		me, err = e.svc.GetMe(ctx, a)
		if err != nil || me.Security == nil {
			t.Fatalf("%+v %v", me.Security, err)
		}
		for _, sess := range me.Security.Sessions {
			if sess.ID == otherLogin.ID {
				t.Errorf("phiên thuộc membership của project khác không được hiện: %+v", sess)
			}
		}
		if len(me.Security.Sessions) != 2 { // phiên của chính a và phiên SMART ở trên, cùng membership
			t.Errorf("sessions: %+v", me.Security.Sessions)
		}
		// membership của ClientApplication: không có user, không có security, profile rút gọn (không lộ secret)
		clientM := &fhir.ProjectMembership{ID: "cm", User: &fhir.Reference{Reference: "ClientApplication/c1"}, Profile: &fhir.Reference{Reference: "ClientApplication/c1"}}
		me, err = e.svc.GetMe(ctx, &application.AuthState{Login: a.Login, Membership: clientM, Project: f.p})
		if err != nil || me.User != nil || me.Security != nil || me.Profile["resourceType"] != "ClientApplication" || len(me.Profile) != 2 {
			t.Errorf("client: %+v %v", me, err)
		}
		// UserConfiguration riêng, có sẵn menu thì giữ; không có menu thì thêm menu mặc định
		custom := put(e, &fhir.UserConfiguration{ResourceType: "UserConfiguration", Name: "riêng", Menu: []fhir.UserConfigurationMenu{{Title: "Của tôi"}}})
		f.m.UserConfiguration = &fhir.Reference{Reference: "UserConfiguration/" + custom.ID}
		me, err = e.svc.GetMe(ctx, e.authState(f, nil))
		if err != nil || me.Config.Name != "riêng" || len(me.Config.Menu) != 1 || me.Config.Menu[0].Title != "Của tôi" {
			t.Errorf("%+v %v", me.Config, err)
		}
		noMenu := put(e, &fhir.UserConfiguration{ResourceType: "UserConfiguration", Name: "không menu"})
		f.m.UserConfiguration = &fhir.Reference{Reference: "UserConfiguration/" + noMenu.ID}
		me, _ = e.svc.GetMe(ctx, e.authState(f, nil))
		if me.Config.Name != "không menu" || len(me.Config.Menu) == 0 {
			t.Errorf("%+v", me.Config)
		}
		f.m.UserConfiguration = &fhir.Reference{Reference: "UserConfiguration/khong-co"}
		if _, err := e.svc.GetMe(ctx, e.authState(f, nil)); !domain.Is(err, domain.KindNotFound, "") {
			t.Errorf("config không đọc được: %v", err)
		}
		// MFA bắt buộc phản ánh trong security
		f.m.UserConfiguration = nil
	})
}
