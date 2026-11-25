package application_test

import (
	"bytes"
	"context"
	"log/slog"
	"net/url"
	"strings"
	"testing"

	"trivita-fhir/apps/auth/internal/application"
	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
)

func (f *admFix) launchClient(t *testing.T, uri string) *fhir.ClientApplication {
	t.Helper()
	c := f.client(t)
	if _, err := f.svc.Clients.Mutate(context.Background(), c.ID, func(x *fhir.ClientApplication) error { x.LaunchUri = uri; return nil }); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestAdminSmartLaunchCreatesLaunchAndURL(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		var buf bytes.Buffer
		e.svc.Logger = slog.New(slog.NewTextHandler(&buf, nil))
		e.svc.Tokens.FHIRBaseURL = "https://fhir.example.org/fhir/R4/"
		f := e.adminFixture()
		ctx := context.Background()
		c := f.launchClient(t, "https://app.example.org/launch?theme=dark")
		res, err := f.svc.AdminSmartLaunch(ctx, f.caller, f.project.ID, c.ID, application.SmartLaunchRequest{
			Patient: "Patient/p1", Encounter: "Encounter/e-1", FhirContext: []string{"DiagnosticReport/r.2"}})
		if err != nil {
			t.Fatal(err)
		}
		l, err := f.svc.Launches.Get(ctx, res.LaunchID)
		if err != nil {
			t.Fatal(err)
		}
		if l.Patient.Reference != "Patient/p1" || l.Encounter.Reference != "Encounter/e-1" || len(l.FhirContext) != 1 || l.FhirContext[0].Reference != "DiagnosticReport/r.2" || l.Meta == nil || l.Meta.Project != f.project.ID {
			t.Fatalf("launch: %+v", l)
		}
		u, err := url.Parse(res.LaunchURL)
		if err != nil || u.Host != "app.example.org" || u.Path != "/launch" || u.Query().Get("theme") != "dark" ||
			u.Query().Get("iss") != "https://fhir.example.org/fhir/R4" || u.Query().Get("launch") != res.LaunchID {
			t.Fatalf("launchUrl: %q %v", res.LaunchURL, err)
		}
		if log := buf.String(); !strings.Contains(log, "audit=true") || !strings.Contains(log, res.LaunchID) {
			t.Fatalf("log: %q", log)
		}
		// mỗi lần gọi là một launch mới
		res2, err := f.svc.AdminSmartLaunch(ctx, f.caller, f.project.ID, c.ID, application.SmartLaunchRequest{Patient: "Patient/p1"})
		if err != nil || res2.LaunchID == res.LaunchID {
			t.Fatalf("launch thứ hai: %+v %v", res2, err)
		}
		if l2, _ := f.svc.Launches.Get(ctx, res2.LaunchID); l2.Encounter != nil || len(l2.FhirContext) != 0 {
			t.Fatalf("chỉ patient: %+v", l2)
		}
	})
}

func TestAdminSmartLaunchRejects(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		e.svc.Tokens.FHIRBaseURL = "https://fhir.example.org/fhir/R4"
		f := e.adminFixture()
		ctx := context.Background()
		c := f.launchClient(t, "https://app.example.org/launch")
		call := func(a *application.AuthState, pid, cid string, r application.SmartLaunchRequest) error {
			_, err := f.svc.AdminSmartLaunch(ctx, a, pid, cid, r)
			return err
		}
		ok := application.SmartLaunchRequest{Patient: "Patient/p1"}
		many := make([]string, 21)
		for i := range many {
			many[i] = "Observation/o1"
		}
		for name, r := range map[string]application.SmartLaunchRequest{
			"không có ngữ cảnh": {}, "patient sai loại": {Patient: "Encounter/p1"}, "patient sai dạng": {Patient: "p1"}, "patient id lạ": {Patient: "Patient/a b"},
			"encounter sai loại": {Encounter: "Patient/e1"}, "context sai dạng": {Patient: "Patient/p1", FhirContext: []string{"x"}},
			"context có đường dẫn": {Patient: "Patient/p1", FhirContext: []string{"https://x.example/Observation/1"}}, "quá nhiều context": {FhirContext: many},
		} {
			if err := call(f.caller, f.project.ID, c.ID, r); !domain.Is(err, domain.KindBadRequest, "") {
				t.Errorf("%s: %v", name, err)
			}
		}
		// client không có launchUri
		noURI := f.client(t)
		wantErr(t, call(f.caller, f.project.ID, noURI.ID, ok), domain.KindBadRequest, "Client application does not have a launch URI")
		// launchUri hỏng
		bad := f.launchClient(t, "ftp://x.example/launch")
		wantErr(t, call(f.caller, f.project.ID, bad.ID, ok), domain.KindBadRequest, "Client launch URI is invalid")
		// chưa cấu hình AUTH_FHIR_BASE_URL
		e.svc.Tokens.FHIRBaseURL = ""
		wantErr(t, call(f.caller, f.project.ID, c.ID, ok), domain.KindBadRequest, "AUTH_FHIR_BASE_URL is not configured")
		e.svc.Tokens.FHIRBaseURL = "https://fhir.example.org/fhir/R4"
		// client ngoài project, không tồn tại, project lệch, không phải admin: 403 chung
		other := e.project(nil)
		oc, err := f.svc.CreateClient(ctx, other.ID, application.NewClient{Name: "Khác", GrantTypes: []string{"client_credentials"}})
		if err != nil {
			t.Fatal(err)
		}
		wantErr(t, call(f.caller, f.project.ID, oc.ID, ok), domain.KindForbidden, "")
		wantErr(t, call(f.caller, f.project.ID, "khong-co", ok), domain.KindForbidden, "")
		wantErr(t, call(f.caller, "project-khac", c.ID, ok), domain.KindForbidden, "")
		plain := *f.caller
		pm := *f.am
		pm.Admin = nil
		plain.Membership = &pm
		wantErr(t, call(&plain, f.project.ID, c.ID, ok), domain.KindForbidden, "")
		wantErr(t, call(&application.AuthState{}, f.project.ID, c.ID, ok), domain.KindUnauthorized, "")
	})
}
