package fhirauth_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirauth"
	"trivita-fhir/pkg/fhirrouter"
	"trivita-fhir/pkg/fhirsearch"
)

func TestSMARTScopesToGrants(t *testing.T) {
	v, err := fhirauth.NewVerifier(fhirauth.Config{Issuer: iss, Audience: aud, HMACSecret: secret})
	if err != nil {
		t.Fatal(err)
	}
	verify := func(scope string) (*fhirauth.Principal, error) {
		return v.Verify(context.Background(), hs(t, claims(map[string]any{"scope": scope})))
	}
	p, err := verify("system/Observation.read user/Patient.write")
	if err != nil {
		t.Fatal(err)
	}
	g := p.Grants
	if !g.CanRead("Observation") || g.Can("Observation", fhirauth.PermWrite) || !g.Can("Patient", fhirauth.PermWrite) || g.CanRead("Condition") || g.FullSearch() {
		t.Fatalf("grants: %v", g)
	}
	if p.Role != fhirauth.RoleWrite {
		t.Fatalf("role %v", p.Role)
	}
	if p, _ = verify("system/Observation.rs"); p == nil || p.Role != fhirauth.RoleRead || p.Grants.Can("Observation", fhirauth.PermWrite) {
		t.Fatalf("v2 chữ cái: %+v", p)
	}
	if p, _ = verify("system/Observation.cud"); p == nil || !p.Grants.Can("Observation", fhirauth.PermWrite) || p.Grants.CanRead("Observation") {
		t.Fatalf("v2 ghi: %+v", p)
	}
	if p, _ = verify("fhir.read system/Patient.write"); p == nil || !p.Grants.FullSearch() || !p.Grants.Can("Patient", fhirauth.PermWrite) || p.Grants.Can("Observation", fhirauth.PermWrite) {
		t.Fatalf("kết hợp: %+v", p)
	}
	// không cấp quyền: patient/, loại lạ, quyền lạ
	for _, s := range []string{"patient/Observation.read", "system/Nope.read", "system/Patient.zzz", "user/Patient", "openid"} {
		if _, err := verify(s); !errors.Is(err, fhirauth.ErrForbidden) {
			t.Errorf("%q phải bị từ chối: %v", s, err)
		}
	}
}

func TestGrantsEnforcedByScope(t *testing.T) {
	base := newSQLite(t)
	repo := fhirauth.Scope(base)
	ctxWith := func(g fhirauth.Grants) context.Context {
		return fhirauth.WithPrincipal(context.Background(), &fhirauth.Principal{Subject: "u", Project: "p1", Role: fhirauth.RoleWrite, Grants: g})
	}
	admin := ctxWith(fhirauth.Grants{"*": fhirauth.PermAll})
	patient := mustCreate(t, repo, admin, `{"resourceType":"Patient","name":[{"family":"A"}]}`)
	obs := mustCreate(t, repo, admin, `{"resourceType":"Observation","status":"final","code":{"text":"x"},"subject":{"reference":"Patient/`+patient+`"}}`)
	_ = mustCreate(t, repo, admin, `{"resourceType":"Condition","subject":{"reference":"Patient/`+patient+`"},"clinicalStatus":{"text":"a"}}`)

	limited := ctxWith(fhirauth.Grants{"Observation": fhirauth.PermAllRead, "Patient": fhirauth.PermAll})
	forbidden := func(what string, err error) {
		t.Helper()
		var oe *fhirrouter.Error
		if !errors.As(err, &oe) || oe.Status != http.StatusForbidden {
			t.Errorf("%s: muốn 403, được %v", what, err)
		}
	}
	if _, err := repo.ReadResource(limited, "Observation", obs); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ReadResource(limited, "Condition", "x"); err == nil {
		t.Error("đọc Condition phải bị chặn")
	} else {
		forbidden("read Condition", err)
	}
	o, _ := repo.ReadResource(limited, "Observation", obs)
	_, err := repo.UpdateResource(limited, o, fhirrouter.UpdateOptions{})
	forbidden("update Observation", err)
	forbidden("delete Observation", repo.DeleteResource(limited, "Observation", obs))
	_, err = repo.ReadHistory(limited, "Condition", "x")
	forbidden("history Condition", err)

	search := func(rt, q string) (*fhirrouter.SearchResult, error) {
		r, err := fhirsearch.ParseQuery(rt, q)
		if err != nil {
			t.Fatal(err)
		}
		return repo.Search(limited, r)
	}
	if _, err := search("Condition", ""); err == nil {
		t.Error("search Condition phải bị chặn")
	} else {
		forbidden("search Condition", err)
	}
	if res, err := search("Observation", ""); err != nil || len(res.Matches) != 1 {
		t.Fatalf("search Observation: %v %v", res, err)
	}
	// search hệ thống không _type: chỉ thấy loại đọc được
	res, err := search("", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range res.Matches {
		if m.GetResourceType() == "Condition" {
			t.Error("search hệ thống lộ Condition")
		}
	}
	if len(res.Matches) != 2 {
		t.Errorf("muốn Patient+Observation, được %d", len(res.Matches))
	}
	_, err = search("", "_type=Condition")
	forbidden("_type=Condition", err)
	// _revinclude không đưa Condition vào
	if res, err := search("Patient", "_revinclude=Observation:subject&_revinclude=Condition:subject"); err != nil {
		t.Fatal(err)
	} else {
		for _, m := range res.Included {
			if m.GetResourceType() == "Condition" {
				t.Error("_revinclude lộ Condition")
			}
		}
	}
	// ghi Patient được phép
	if _, err := repo.CreateResource(limited, mustParse(t, `{"resourceType":"Patient"}`)); err != nil {
		t.Fatal(err)
	}
	_, err = repo.CreateResource(limited, mustParse(t, `{"resourceType":"Observation","status":"final","code":{"text":"x"}}`))
	forbidden("create Observation", err)
}

func mustParse(t *testing.T, js string) fhir.Resource {
	t.Helper()
	r, err := fhir.UnmarshalResource([]byte(js))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func mustCreate(t *testing.T, repo fhirrouter.Repository, ctx context.Context, js string) string {
	t.Helper()
	r, err := repo.CreateResource(ctx, mustParse(t, js))
	if err != nil {
		t.Fatal(err)
	}
	return r.GetID()
}

func TestSuperScope(t *testing.T) {
	mk := func(allow bool) *fhirauth.Verifier {
		v, err := fhirauth.NewVerifier(fhirauth.Config{Issuer: iss, Audience: aud, HMACSecret: secret, AllowSuperAdmin: allow})
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	tokNoProject := hs(t, claims(map[string]any{"scope": "fhir.super", "fhir_project": nil}))
	p, err := mk(true).Verify(context.Background(), tokNoProject)
	if err != nil || !p.Super || p.Role != fhirauth.RoleAdmin || !p.Grants.Can("Patient", fhirauth.PermWrite) || p.Project != "" {
		t.Fatalf("%+v %v", p, err)
	}
	if _, err := mk(false).Verify(context.Background(), tokNoProject); !errors.Is(err, fhirauth.ErrForbidden) {
		t.Fatalf("tắt: phải 403, được %v", err)
	}
	// tắt: scope bị bỏ qua nhưng token vẫn dùng được nếu có scope khác
	p, err = mk(false).Verify(context.Background(), hs(t, claims(map[string]any{"scope": "fhir.super fhir.read"})))
	if err != nil || p.Super || p.Role != fhirauth.RoleRead {
		t.Fatalf("%+v %v", p, err)
	}
}
