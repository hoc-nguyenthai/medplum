package application_test

import (
	"context"
	"strings"
	"testing"

	"trivita-fhir/apps/auth/internal/application"
	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirrouter"
)

func fhirUpdate() fhirrouter.UpdateOptions { return fhirrouter.UpdateOptions{} }

// openProjectWithPolicy là project nhận đăng ký mở với chính sách Patient có thật trong kho.
func (e *env) openProjectWithPolicy(legacy bool) (*fhir.Project, *fhir.AccessPolicy) {
	pol := put(e, &fhir.AccessPolicy{ResourceType: "AccessPolicy", Name: "Patient mở"})
	p := e.project(func(p *fhir.Project) {
		p.Setting = []fhir.ProjectSetting{{Name: domain.OpenPatientRegistrationSetting, ValueBoolean: ptr(true)}}
		ref := &fhir.Reference{Reference: "AccessPolicy/" + pol.ID}
		if legacy {
			p.DefaultAccessPolicies = []fhir.ProjectDefaultAccessPolicies{{ProfileType: "Practitioner", AccessPolicy: &fhir.Reference{Reference: "AccessPolicy/khac"}}}
			p.DefaultPatientAccessPolicy = ref
		} else {
			p.DefaultAccessPolicies = []fhir.ProjectDefaultAccessPolicies{{ProfileType: "Patient", AccessPolicy: ref}}
		}
	})
	return p, pol
}

// patientLogin đăng ký một user vào project p và trả user cùng login chưa có membership.
func (e *env) patientLogin(p *fhir.Project, email string) (*fhir.User, string) {
	e.t.Helper()
	res, err := e.svc.NewUser(context.Background(), e.newUserReq(email, func(r *application.NewUserRequest) { r.ProjectID = p.ID }))
	if err != nil {
		e.t.Fatal(err)
	}
	u, _ := e.svc.FindUserInScope(context.Background(), email, p.ID)
	return u, res.LoginID
}

func TestNewPatientCreatesProfileAndMembership(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		legacy := legacy
		eachEnv(t, func(t *testing.T, e *env) {
			ctx := context.Background()
			p, pol := e.openProjectWithPolicy(legacy) // legacy: chỉ có defaultPatientAccessPolicy
			u, l := e.patientLogin(p, "benh-nhan@example.org")
			res, login, err := e.svc.NewPatient(ctx, application.NewPatientRequest{LoginID: l, ProjectID: p.ID})
			if err != nil {
				t.Fatal(err)
			}
			if res.Code == "" || res.Cookie == "" || !e.codeStoredAs(t, l, res) || login.Membership == nil || refOf(login.Project) != "Project/"+p.ID {
				t.Fatalf("kết quả: %+v", res)
			}
			m, err := e.r.Memberships.Get(ctx, strings.TrimPrefix(refOf(login.Membership), "ProjectMembership/"))
			if err != nil || refOf(m.AccessPolicy) != "AccessPolicy/"+pol.ID || domain.IsTrue(m.Admin) || refOf(m.User) != "User/"+u.ID ||
				refOf(m.Project) != "Project/"+p.ID || !strings.HasPrefix(refOf(m.Profile), "Patient/") {
				t.Fatalf("membership: %+v %v", m, err)
			}
			pat, err := e.repo.ReadResource(ctx, "Patient", strings.TrimPrefix(refOf(m.Profile), "Patient/"))
			if err != nil {
				t.Fatal(err)
			}
			pt := pat.(*fhir.Patient)
			if pt.Meta == nil || pt.Meta.Project != p.ID || len(pt.Name) != 1 || pt.Name[0].Given[0] != "An" || pt.Name[0].Family != "Nguyen" ||
				len(pt.Telecom) != 1 || pt.Telecom[0].Value != "benh-nhan@example.org" || pt.Telecom[0].System != "email" {
				t.Errorf("patient: %+v", pt)
			}
			_, _, err = e.svc.NewPatient(ctx, application.NewPatientRequest{LoginID: l, ProjectID: p.ID})
			wantErr(t, err, domain.KindBadRequest, "Login already has a membership")
			if e.count("Patient") != 1 || e.count("ProjectMembership") != 1 {
				t.Errorf("dùng lại login tạo thêm: patient=%d membership=%d", e.count("Patient"), e.count("ProjectMembership"))
			}
		})
	}
}

// Mọi kiểm tra chạy TRƯỚC khi tạo gì: bị từ chối thì không có Patient hay membership mồ côi.
func TestNewPatientRejectionsCreateNothing(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		p, _ := e.openProjectWithPolicy(false)
		other := e.project(nil)
		u, _ := e.patientLogin(p, "an@example.org")
		global := e.user("toan-cuc@example.org", nil)
		inOther := e.user("khac@example.org", func(x *fhir.User) { x.Project = &fhir.Reference{Reference: "Project/" + other.ID} })
		mk := func(user *fhir.User, mut func(*fhir.Login)) string {
			return e.login(user, func(l *fhir.Login) {
				l.Project = &fhir.Reference{Reference: "Project/" + p.ID}
				if mut != nil {
					mut(l)
				}
			}).ID
		}
		for _, tc := range []struct {
			name, login, project, want string
			kind                       domain.Kind
		}{
			{"đã thu hồi", mk(u, func(l *fhir.Login) { l.Revoked = ptr(true) }), p.ID, "Login revoked", domain.KindBadRequest},
			{"đã cấp code", mk(u, func(l *fhir.Login) { l.Granted = ptr(true) }), p.ID, "Login granted", domain.KindBadRequest},
			{"login thuộc project khác", mk(u, func(l *fhir.Login) { l.Project = &fhir.Reference{Reference: "Project/" + other.ID} }), p.ID, "Invalid project", domain.KindBadRequest},
			{"projectId lệch login", mk(u, nil), other.ID, "Invalid project", domain.KindBadRequest},
			{"user toàn cục", mk(global, nil), p.ID, "User must be scoped to the project", domain.KindBadRequest},
			{"user của project khác", mk(inOther, func(l *fhir.Login) { l.Project = nil }), p.ID, "User must be scoped to the project", domain.KindBadRequest},
			{"login không có", domain.NewID(), p.ID, "", domain.KindNotFound},
		} {
			_, _, err := e.svc.NewPatient(ctx, application.NewPatientRequest{LoginID: tc.login, ProjectID: tc.project})
			wantErr(t, err, tc.kind, tc.want)
		}
		if e.count("Patient") != 0 || e.count("ProjectMembership") != 0 {
			t.Errorf("để lại resource mồ côi: patient=%d membership=%d", e.count("Patient"), e.count("ProjectMembership"))
		}
	})
}

func TestNewPatientProjectConditions(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		req := func(l, p string) application.NewPatientRequest {
			return application.NewPatientRequest{LoginID: l, ProjectID: p}
		}
		scopedLogin := func(p *fhir.Project, email string) string {
			u := e.user(email, func(x *fhir.User) { x.Project = &fhir.Reference{Reference: "Project/" + p.ID} })
			return e.login(u, func(l *fhir.Login) { l.Project = &fhir.Reference{Reference: "Project/" + p.ID} }).ID
		}
		closed := e.project(nil)
		_, _, err := e.svc.NewPatient(ctx, req(scopedLogin(closed, "a@example.org"), closed.ID))
		wantErr(t, err, domain.KindBadRequest, "Project does not allow open registration")

		inactive, _ := e.openProjectWithPolicy(false)
		inactive.Active = ptr(false)
		if _, err := e.repo.UpdateResource(ctx, inactive, fhirUpdate()); err != nil {
			t.Fatal(err)
		}
		_, _, err = e.svc.NewPatient(ctx, req(scopedLogin(inactive, "b@example.org"), inactive.ID))
		wantErr(t, err, domain.KindBadRequest, "Project not active")

		// chính sách được tham chiếu không còn: lỗi, không tạo gì
		dangling := e.project(func(p *fhir.Project) {
			p.Setting = []fhir.ProjectSetting{{Name: domain.OpenPatientRegistrationSetting, ValueBoolean: ptr(true)}}
			p.DefaultAccessPolicies = []fhir.ProjectDefaultAccessPolicies{{ProfileType: "Patient", AccessPolicy: &fhir.Reference{Reference: "AccessPolicy/" + domain.NewID()}}}
		})
		_, _, err = e.svc.NewPatient(ctx, req(scopedLogin(dangling, "c@example.org"), dangling.ID))
		wantErr(t, err, domain.KindNotFound, "")
		_, _, err = e.svc.NewPatient(ctx, req(scopedLogin(dangling, "d@example.org"), domain.NewID()))
		if err == nil {
			t.Error("project không có phải lỗi")
		}
		if e.count("Patient") != 0 || e.count("ProjectMembership") != 0 {
			t.Errorf("để lại resource: patient=%d membership=%d", e.count("Patient"), e.count("ProjectMembership"))
		}
	})
}

func TestNewPatientWithPendingMFACreatesNothing(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		p, _ := e.openProjectWithPolicy(false)
		u, l := e.patientLogin(p, "an@example.org")
		if _, err := e.r.Users.Mutate(ctx, u.ID, func(x *fhir.User) error { x.MfaEnrolled = ptr(true); x.MfaSecret = "S"; return nil }); err != nil {
			t.Fatal(err)
		}
		res, _, err := e.svc.NewPatient(ctx, application.NewPatientRequest{LoginID: l, ProjectID: p.ID})
		if err != nil || !res.MfaRequired || res.Code != "" || e.count("Patient") != 0 {
			t.Fatalf("%+v %v patient=%d", res, err, e.count("Patient"))
		}
	})
}
