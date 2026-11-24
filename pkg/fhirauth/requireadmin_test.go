package fhirauth_test

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"

	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirauth"
	"trivita-fhir/pkg/fhirrouter"
)

func TestRequireAdmin(t *testing.T) {
	repo := fhirauth.Scope(fhirrouter.NewMemoryRepository())
	pol := &fhirauth.Policy{}
	cases := []struct {
		name string
		p    *fhirauth.Principal
		ok   bool
	}{
		{"admin", &fhirauth.Principal{Subject: "u", Project: "A", Role: fhirauth.RoleAdmin}, true},
		{"super", &fhirauth.Principal{Subject: "u", Role: fhirauth.RoleRead, Super: true}, true},
		{"write", &fhirauth.Principal{Subject: "u", Project: "A", Role: fhirauth.RoleWrite}, false},
		{"read", &fhirauth.Principal{Subject: "u", Project: "A", Role: fhirauth.RoleRead}, false},
		{"admin bị chính sách thu hẹp", &fhirauth.Principal{Subject: "u", Project: "A", Role: fhirauth.RoleAdmin, Policy: pol}, false},
	}
	for _, c := range cases {
		err := repo.RequireAdmin(fhirauth.WithPrincipal(context.Background(), c.p))
		if c.ok && err != nil {
			t.Errorf("%s: %v", c.name, err)
		}
		var fe *fhirrouter.Error
		if !c.ok && (!errors.As(err, &fe) || fe.Status != 403) {
			t.Errorf("%s: phải 403, được %v", c.name, err)
		}
	}
	var fe *fhirrouter.Error
	if err := repo.RequireAdmin(context.Background()); !errors.As(err, &fe) || fe.Status != 403 {
		t.Errorf("không có Principal phải 403: %v", err)
	}
}

// Cài gói qua HTTP với repo tách project: chỉ admin project, Bundle chạy trong project của người gọi, không thấy gói của project khác.
func TestPackageInstallRespectsRoleAndProject(t *testing.T) {
	onEachBackend(t, func(t *testing.T, e *compEnv) {
		bundle := `{"resourceType":"Bundle","type":"transaction","entry":[{"resource":{"resourceType":"ValueSet","status":"active","url":"http://example.org/vs-pkg"},"request":{"method":"POST","url":"ValueSet"}}]}`
		bin := mustCreate(t, e.repo, e.admin, `{"resourceType":"Binary","contentType":"application/fhir+json","data":"`+b64(bundle)+`"}`)
		rel := mustCreate(t, e.repo, e.admin, `{"resourceType":"PackageRelease","version":"1","content":{"url":"Binary/`+bin+`"}}`)
		path := "PackageRelease/" + rel + "/$install"

		for name, p := range map[string]*fhirauth.Principal{
			"write":        {Subject: "w", Project: "p1", Role: fhirauth.RoleWrite},
			"admin+policy": {Subject: "a", Project: "p1", Role: fhirauth.RoleAdmin, Policy: &fhirauth.Policy{}},
		} {
			if code, _ := e.do(p, "POST", path, ""); code != 403 {
				t.Errorf("%s phải 403, được %d", name, code)
			}
		}
		other := &fhirauth.Principal{Subject: "o", Project: "p2", Role: fhirauth.RoleAdmin}
		if code, _ := e.do(other, "POST", path, ""); code != 404 {
			t.Errorf("gói của project khác phải 404, được %d", code)
		}
		if code, b := e.do(e.admin1(), "GET", "ValueSet?url=http://example.org/vs-pkg", ""); code != 200 || len(ids(b, "ValueSet")) != 0 {
			t.Fatalf("chưa cài thì chưa có ValueSet: %d %v", code, b)
		}
		if code, b := e.do(e.admin1(), "POST", path, ""); code != 200 {
			t.Fatalf("admin cài: %d %v", code, b)
		}
		if _, b := e.do(e.admin1(), "GET", "ValueSet?url=http://example.org/vs-pkg", ""); len(ids(b, "ValueSet")) != 1 {
			t.Errorf("ValueSet phải nằm trong project p1: %v", b)
		}
		if _, b := e.do(other, "GET", "ValueSet?url=http://example.org/vs-pkg", ""); len(ids(b, "ValueSet")) != 0 {
			t.Errorf("project p2 không được thấy ValueSet của p1: %v", b)
		}
	})
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// CodeSystem/$import qua HTTP với repo tách project: chỉ admin, và chỉ vào CodeSystem của project mình.
func TestCodeSystemImportRespectsRoleAndProject(t *testing.T) {
	onEachBackend(t, func(t *testing.T, e *compEnv) {
		cs := mustCreate(t, e.repo, e.admin, `{"resourceType":"CodeSystem","url":"http://example.org/cs-imp","status":"active","content":"not-present"}`)
		path := "CodeSystem/" + cs + "/$import"
		body := `{"resourceType":"Parameters","parameter":[{"name":"concept","valueCoding":{"code":"A","display":"Alpha"}}]}`

		write := &fhirauth.Principal{Subject: "w", Project: "p1", Role: fhirauth.RoleWrite}
		if code, _ := e.do(write, "POST", path, body); code != 403 {
			t.Errorf("write phải 403, được %d", code)
		}
		other := &fhirauth.Principal{Subject: "o", Project: "p2", Role: fhirauth.RoleAdmin}
		if code, _ := e.do(other, "POST", path, body); code != 404 {
			t.Errorf("CodeSystem của project khác phải 404, được %d", code)
		}
		if code, b := e.do(e.admin1(), "POST", path, body); code != 200 {
			t.Fatalf("admin nạp: %d %v", code, b)
		}
		cstore := e.repo.(fhirrouter.ConceptStore)
		if c, err := cstore.GetConcept(fhirauth.WithPrincipal(context.Background(), e.admin1()), cs, "A"); err != nil || c == nil || c.Display != "Alpha" {
			t.Errorf("concept sau khi nạp: %+v %v", c, err)
		}
		if _, err := cstore.GetConcept(fhirauth.WithPrincipal(context.Background(), other), cs, "A"); err == nil {
			t.Error("project khác không được đọc concept")
		}
	})
}

// ConceptMap/$import qua HTTP với repo tách project: chỉ admin, và chỉ vào ConceptMap của project mình.
func TestConceptMapImportRespectsRoleAndProject(t *testing.T) {
	onEachBackend(t, func(t *testing.T, e *compEnv) {
		cm := mustCreate(t, e.repo, e.admin, `{"resourceType":"ConceptMap","url":"http://example.org/cm-imp","status":"active"}`)
		path := "ConceptMap/" + cm + "/$import"
		body := `{"resourceType":"Parameters","parameter":[{"name":"mapping","part":[` +
			`{"name":"source","valueCoding":{"system":"http://a","code":"1"}},{"name":"target","valueCoding":{"system":"http://b","code":"X"}}]}]}`

		write := &fhirauth.Principal{Subject: "w", Project: "p1", Role: fhirauth.RoleWrite}
		if code, _ := e.do(write, "POST", path, body); code != 403 {
			t.Errorf("write phải 403, được %d", code)
		}
		other := &fhirauth.Principal{Subject: "o", Project: "p2", Role: fhirauth.RoleAdmin}
		if code, _ := e.do(other, "POST", path, body); code != 404 {
			t.Errorf("ConceptMap của project khác phải 404, được %d", code)
		}
		if code, b := e.do(e.admin1(), "POST", path, body); code != 200 {
			t.Fatalf("admin nạp: %d %v", code, b)
		}
		ms := e.repo.(fhirrouter.MappingStore)
		if got, err := ms.FindMappings(fhirauth.WithPrincipal(context.Background(), e.admin1()), cm, fhirrouter.MappingQuery{}); err != nil || len(got) != 1 {
			t.Errorf("mapping sau khi nạp: %+v %v", got, err)
		}
		if _, err := ms.FindMappings(fhirauth.WithPrincipal(context.Background(), other), cm, fhirrouter.MappingQuery{}); err == nil {
			t.Error("project khác không được đọc ánh xạ")
		}
	})
}

type recordingResender struct {
	fhirrouter.Repository
	calls []string
}

func (r *recordingResender) ResendSubscriptions(_ context.Context, rt, id, vid, interaction string) error {
	r.calls = append(r.calls, rt+"/"+id+"|"+vid+"|"+interaction)
	return nil
}

// $resend chỉ cho quản trị project (không bị chính sách thu hẹp), chỉ với resource của project mình, rồi mới chuyển cho repository bên trong.
func TestResendSubscriptionsRespectsRoleAndProject(t *testing.T) {
	inner := &recordingResender{Repository: fhirrouter.NewMemoryRepository()}
	repo := fhirauth.Scope(inner)
	admin := fhirauth.WithPrincipal(context.Background(), &fhirauth.Principal{Subject: "a", Project: "A", Role: fhirauth.RoleAdmin})
	if _, err := repo.CreateResource(admin, &fhir.Patient{ID: "p1"}); err != nil {
		t.Fatal(err)
	}
	status := func(err error) int {
		var fe *fhirrouter.Error
		if errors.As(err, &fe) {
			return fe.Status
		}
		return 0
	}
	rs := repo
	for name, p := range map[string]*fhirauth.Principal{
		"write":        {Subject: "w", Project: "A", Role: fhirauth.RoleWrite},
		"admin+policy": {Subject: "a", Project: "A", Role: fhirauth.RoleAdmin, Policy: &fhirauth.Policy{}},
	} {
		if err := rs.ResendSubscriptions(fhirauth.WithPrincipal(context.Background(), p), "Patient", "p1", "", "update"); status(err) != 403 {
			t.Errorf("%s phải 403: %v", name, err)
		}
	}
	if err := rs.ResendSubscriptions(context.Background(), "Patient", "p1", "", "update"); status(err) != 403 {
		t.Errorf("không Principal phải 403")
	}
	other := fhirauth.WithPrincipal(context.Background(), &fhirauth.Principal{Subject: "o", Project: "B", Role: fhirauth.RoleAdmin})
	if err := rs.ResendSubscriptions(other, "Patient", "p1", "", "update"); status(err) != 404 {
		t.Errorf("admin project khác phải 404: %v", err)
	}
	if len(inner.calls) != 0 {
		t.Fatalf("repository bên trong bị gọi khi bị từ chối: %v", inner.calls)
	}
	if err := rs.ResendSubscriptions(admin, "Patient", "p1", "v1", "create"); err != nil || len(inner.calls) != 1 || inner.calls[0] != "Patient/p1|v1|create" {
		t.Errorf("admin: %v %v", err, inner.calls)
	}
	// repository bên trong không hỗ trợ: 501
	plain := fhirauth.Scope(fhirrouter.NewMemoryRepository())
	if _, err := plain.CreateResource(admin, &fhir.Patient{ID: "p2"}); err != nil {
		t.Fatal(err)
	}
	if err := plain.ResendSubscriptions(admin, "Patient", "p2", "", "update"); status(err) != 501 {
		t.Errorf("không hỗ trợ: %v", err)
	}
}
