package application_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"trivita-fhir/apps/auth/internal/application"
	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
)

type preFix struct {
	e       *env
	svc     *application.Service
	project *fhir.Project
	admin   *application.AuthState
	target  *fhir.ProjectMembership
	client  *fhir.ClientApplication
}

func (e *env) preFixture(callerScope string) *preFix {
	p := e.project(nil)
	au := e.user("admin@pre.example.org", nil)
	am := e.membership(au, p, func(m *fhir.ProjectMembership) { m.Admin = ptr(true) })
	tu := e.user("target@pre.example.org", nil)
	tm := e.membership(tu, p, func(m *fhir.ProjectMembership) { m.Profile = &fhir.Reference{Reference: "Practitioner/target"} })
	cl := put(e, &fhir.ClientApplication{ResourceType: "ClientApplication", Name: "App", Meta: &fhir.Meta{Project: p.ID}})
	login := put(e, &fhir.Login{ResourceType: "Login", AuthMethod: "password", Scope: callerScope, User: &fhir.Reference{Reference: "User/" + au.ID}})
	d := e.svc.Deps
	d.MFA, d.Signer = nil, fakeSigner{}
	return &preFix{e: e, svc: application.NewService(d), project: p, admin: &application.AuthState{Login: login, Membership: am, Project: p}, target: tm, client: cl}
}

func (f *preFix) req(mut func(*application.PreAuthorizeRequest)) application.PreAuthorizeRequest {
	r := application.PreAuthorizeRequest{Caller: f.admin, OnBehalfOf: "ProjectMembership/" + f.target.ID, ClientID: f.client.ID, RemoteAddress: "1.2.3.4", UserAgent: "ua"}
	if mut != nil {
		mut(&r)
	}
	return r
}

func TestPreAuthorizeMintsOnceAndStoresOnlyTheHash(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.preFixture("openid")
		res, err := f.svc.PreAuthorize(context.Background(), f.req(nil))
		if err != nil || len(res.PreAuthorizedCode) < 100 || res.ExpiresAt == "" {
			t.Fatalf("%+v %v", res, err)
		}
		exp, _ := time.Parse(time.RFC3339, res.ExpiresAt)
		if d := exp.Sub(e.clock.t); d < 3599*time.Second || d > 3601*time.Second {
			t.Errorf("hạn mặc định 3600s: %v", d)
		}
		l, err := f.svc.Logins.FindByPreAuthorizedCodeHash(context.Background(), domain.HashSecret(res.PreAuthorizedCode))
		if err != nil || l == nil || l.AuthMethod != "pre-authorized" || l.Scope != "openid" || l.Nonce == "" || l.Code != "" ||
			l.PreAuthorizedCodeHash == res.PreAuthorizedCode || l.Membership.Reference != "ProjectMembership/"+f.target.ID || l.User.Reference != f.target.User.Reference {
			t.Fatalf("login: %+v %v", l, err)
		}
		// mã đổi được token (redeem đã có từ P3)
		d := f.svc.Deps
		d.Signer = fakeSigner{}
		tok, oerr := f.svc.PreAuthorizedCode(context.Background(), application.PreAuthorizedCodeRequest{ClientID: f.client.ID, Code: res.PreAuthorizedCode, RemoteAddress: "1.2.3.4"})
		if oerr != nil || tok["access_token"] == nil {
			t.Fatalf("redeem: %v %v", tok, oerr)
		}
		_, oerr = f.svc.PreAuthorizedCode(context.Background(), application.PreAuthorizedCodeRequest{ClientID: f.client.ID, Code: res.PreAuthorizedCode, RemoteAddress: "1.2.3.4"})
		if oerr == nil || oerr.Description != "Token already granted" {
			t.Errorf("dùng lại: %v", oerr)
		}
		// hạn tùy chỉnh và nonce tùy chỉnh
		r2, err := f.svc.PreAuthorize(context.Background(), f.req(func(r *application.PreAuthorizeRequest) { r.ExpiresIn, r.Nonce = 60, "n-1" }))
		if err != nil {
			t.Fatal(err)
		}
		l2, _ := f.svc.Logins.FindByPreAuthorizedCodeHash(context.Background(), domain.HashSecret(r2.PreAuthorizedCode))
		if exp2, _ := time.Parse(time.RFC3339, r2.ExpiresAt); exp2.Sub(e.clock.t) > 61*time.Second || l2.Nonce != "n-1" {
			t.Errorf("hạn/nonce tùy chỉnh: %v %s", r2.ExpiresAt, l2.Nonce)
		}
		if r2.PreAuthorizedCode == res.PreAuthorizedCode {
			t.Error("mỗi lần một mã khác")
		}
	})
}

func TestPreAuthorizeAuthorization(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.preFixture("openid")
		ctx := context.Background()
		_, err := f.svc.PreAuthorize(ctx, f.req(func(r *application.PreAuthorizeRequest) { r.Caller = nil }))
		wantErr(t, err, domain.KindUnauthorized, "")
		nonAdmin := *f.admin
		m := *f.admin.Membership
		m.Admin = nil
		nonAdmin.Membership = &m
		_, err = f.svc.PreAuthorize(ctx, f.req(func(r *application.PreAuthorizeRequest) { r.Caller = &nonAdmin }))
		wantErr(t, err, domain.KindForbidden, "")
		super := *f.admin
		sp := *f.project
		sp.SuperAdmin = ptr(true)
		super.Project = &sp
		_, err = f.svc.PreAuthorize(ctx, f.req(func(r *application.PreAuthorizeRequest) { r.Caller = &super }))
		wantErr(t, err, domain.KindForbidden, "")
		_, err = f.svc.PreAuthorize(ctx, f.req(func(r *application.PreAuthorizeRequest) { r.OnBehalfOf = "" }))
		wantErr(t, err, domain.KindBadRequest, "Pre-authorization requires onBehalfOfMembership")
	})
}

func TestPreAuthorizeOnBehalfOfResolution(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.preFixture("openid")
		ctx := context.Background()
		// tham chiếu profile
		if _, err := f.svc.PreAuthorize(ctx, f.req(func(r *application.PreAuthorizeRequest) { r.OnBehalfOf = "Practitioner/target" })); err != nil {
			t.Fatalf("theo profile: %v", err)
		}
		// membership của project KHÁC
		op := e.project(nil)
		ou := e.user("other@pre.example.org", nil)
		om := e.membership(ou, op, func(m *fhir.ProjectMembership) { m.Profile = &fhir.Reference{Reference: "Practitioner/other-proj"} })
		for _, h := range map[string]string{
			"project khác":         "ProjectMembership/" + om.ID,
			"không tồn tại":        "ProjectMembership/khong-co",
			"profile lạ":           "Practitioner/khong-co",
			"profile project khác": om.Profile.Reference,
		} {
			_, err := f.svc.PreAuthorize(ctx, f.req(func(r *application.PreAuthorizeRequest) { r.OnBehalfOf = h }))
			wantErr(t, err, domain.KindForbidden, "Forbidden")
		}
		// membership đích không hoạt động
		closed := *f.target
		closed.Active = ptr(false)
		if _, err := e.repo.UpdateResource(ctx, &closed, fhirUpdate()); err != nil {
			t.Fatal(err)
		}
		_, err := f.svc.PreAuthorize(ctx, f.req(nil))
		wantErr(t, err, domain.KindForbidden, "Forbidden")
	})
}

func TestPreAuthorizeScope(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		scopeOf := func(f *preFix, res *application.PreAuthorizeResult) string {
			l, _ := f.svc.Logins.FindByPreAuthorizedCodeHash(ctx, domain.HashSecret(res.PreAuthorizedCode))
			return l.Scope
		}
		// người gọi không bị giới hạn SMART: scope mặc định openid, xin gì cũng được
		f := e.preFixture("openid fhirUser")
		res, err := f.svc.PreAuthorize(ctx, f.req(nil))
		if err != nil || scopeOf(f, res) != "openid" {
			t.Fatalf("mặc định: %v %v", res, err)
		}
		if _, err = f.svc.PreAuthorize(ctx, f.req(func(r *application.PreAuthorizeRequest) { r.Scope = "openid patient/Observation.rs" })); err != nil {
			t.Fatalf("không giới hạn: %v", err)
		}
		if _, err = f.svc.PreAuthorize(ctx, f.req(func(r *application.PreAuthorizeRequest) { r.Scope = "openid offline_access launch" })); err != nil {
			t.Fatalf("người gọi không giới hạn SMART xin term phi-SMART tùy ý: %v", err)
		}
		// người gọi bị giới hạn SMART
		g := e.preFixture("openid user/Observation.rs user/Patient.rs")
		res, err = g.svc.PreAuthorize(ctx, g.req(nil))
		if err != nil || scopeOf(g, res) != "openid user/Observation.rs user/Patient.rs" {
			t.Fatalf("mặc định kế thừa SMART: %v %v", res, err)
		}
		for name, sc := range map[string]string{
			"hẹp hơn": "openid user/Observation.r",
			"bằng":    "openid user/Observation.rs",
		} {
			if _, err := g.svc.PreAuthorize(ctx, g.req(func(r *application.PreAuthorizeRequest) { r.Scope = sc })); err != nil {
				t.Errorf("%s: %v", name, err)
			}
		}
		for name, sc := range map[string]string{
			"rộng hơn":               "openid user/Observation.cruds",
			"resource khác":          "openid user/Condition.rs",
			"không SMART":            "openid",
			"term phi SMART lạ":      "openid user/Observation.rs offline_access",
			"patient không ngữ cảnh": "openid patient/Observation.rs",
		} {
			_, err := g.svc.PreAuthorize(ctx, g.req(func(r *application.PreAuthorizeRequest) { r.Scope = sc }))
			if !domain.Is(err, domain.KindBadRequest, "Invalid scope") {
				t.Errorf("%s: %v", name, err)
			}
		}
		// patient/ chỉ khi đích là Patient
		pt := e.user("pt@pre.example.org", nil)
		pm := e.membership(pt, g.project, func(m *fhir.ProjectMembership) { m.Profile = &fhir.Reference{Reference: "Patient/p1"} })
		if _, err := g.svc.PreAuthorize(ctx, g.req(func(r *application.PreAuthorizeRequest) {
			r.OnBehalfOf, r.Scope = "ProjectMembership/"+pm.ID, "openid patient/Observation.rs"
		})); err != nil {
			t.Errorf("patient context: %v", err)
		}
	})
}

func TestPreAuthorizeClientMustBelongToCallerProject(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.preFixture("openid")
		ctx := context.Background()
		other := e.project(nil)
		foreign := put(e, &fhir.ClientApplication{ResourceType: "ClientApplication", Name: "X", Meta: &fhir.Meta{Project: other.ID}})
		_, err := f.svc.PreAuthorize(ctx, f.req(func(r *application.PreAuthorizeRequest) { r.ClientID = foreign.ID }))
		wantErr(t, err, domain.KindNotFound, "")
		_, err = f.svc.PreAuthorize(ctx, f.req(func(r *application.PreAuthorizeRequest) { r.ClientID = "khong-co" }))
		wantErr(t, err, domain.KindNotFound, "")
		if n := len(e.logins()); n != 1 { // chỉ Login của người gọi
			t.Errorf("lỗi không để lại Login: %d", n)
		}
		_ = strings.TrimSpace
	})
}
