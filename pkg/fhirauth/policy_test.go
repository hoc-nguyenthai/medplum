package fhirauth_test

import (
	"context"
	"errors"
	"testing"

	"trivita-fhir/pkg/fhirauth"
	"trivita-fhir/pkg/fhirrouter"
	"trivita-fhir/pkg/fhirsearch"
)

func TestParsePolicy(t *testing.T) {
	ok := []any{
		[]any{},
		[]any{"Observation?subject=Patient/1", map[string]any{"resourceType": "Patient", "readonly": true}},
		[]any{map[string]any{"resourceType": "Observation", "criteria": "status=final"}},
	}
	for i, v := range ok {
		if _, err := fhirauth.ParsePolicy(v); err != nil {
			t.Errorf("ok[%d]: %v", i, err)
		}
	}
	for name, v := range map[string]any{
		"không phải mảng": "x",
		"loại lạ":         []any{"Nope"},
		"trùng loại":      []any{"Patient", "Patient?active=true"},
		"chained":         []any{"Observation?subject:Patient.name=x"},
		"_include":        []any{"Observation?_include=Observation:subject"},
		"tham số lạ":      []any{"Observation?nope=1"},
		"terminology":     []any{"Observation?code:below=http://x|y"},
		"_filter":         []any{"Observation?_filter=status%20eq%20final"},
		"readonly sai":    []any{map[string]any{"resourceType": "Patient", "readonly": "yes"}},
		"phần tử lạ":      []any{42},
		// S-C1: khóa lạ phải bị từ chối, nếu không rule không criteria sẽ cấp cả loại.
		"khóa lạ":            []any{map[string]any{"resourceType": "Communication", "criteriaAny": []any{"sender=Patient/1"}}},
		"khóa lạ kèm hợp lệ": []any{map[string]any{"resourceType": "Observation", "criteria": "status=final", "writeConstraint": []any{"x"}}},
	} {
		if _, err := fhirauth.ParsePolicy(v); err == nil {
			t.Errorf("%s: muốn lỗi", name)
		}
	}
}

func TestVerifyReadsPolicyClaim(t *testing.T) {
	v, err := fhirauth.NewVerifier(fhirauth.Config{Issuer: iss, Audience: aud, HMACSecret: secret})
	if err != nil {
		t.Fatal(err)
	}
	p, err := v.Verify(context.Background(), hs(t, claims(map[string]any{"fhir_access_policy": []any{"Patient"}})))
	if err != nil || p.Policy == nil {
		t.Fatalf("%+v %v", p, err)
	}
	if p, _ = v.Verify(context.Background(), hs(t, claims(nil))); p == nil || p.Policy != nil {
		t.Fatalf("không claim thì không có chính sách: %+v", p)
	}
	_, err = v.Verify(context.Background(), hs(t, claims(map[string]any{"fhir_access_policy": "Patient"})))
	if !errors.Is(err, fhirauth.ErrForbidden) {
		t.Fatalf("claim sai phải 403: %v", err)
	}
}

func TestAccessPolicyEnforced(t *testing.T) {
	repo := fhirauth.Scope(newSQLite(t))
	as := func(pol *fhirauth.Policy) context.Context {
		return fhirauth.WithPrincipal(context.Background(), &fhirauth.Principal{Subject: "u", Project: "p1", Role: fhirauth.RoleWrite,
			Grants: fhirauth.Grants{"*": fhirauth.PermAll}, Policy: pol})
	}
	admin := as(nil)
	p1 := mustCreate(t, repo, admin, `{"resourceType":"Patient"}`)
	p2 := mustCreate(t, repo, admin, `{"resourceType":"Patient"}`)
	obs := func(p string) string {
		return `{"resourceType":"Observation","status":"final","code":{"text":"x"},"subject":{"reference":"Patient/` + p + `"}}`
	}
	o1 := mustCreate(t, repo, admin, obs(p1))
	o2 := mustCreate(t, repo, admin, obs(p2))
	mustCreate(t, repo, admin, `{"resourceType":"Condition","subject":{"reference":"Patient/`+p1+`"}}`)

	pol, err := fhirauth.ParsePolicy([]any{
		map[string]any{"resourceType": "Patient", "criteria": "_id=" + p1, "readonly": true},
		map[string]any{"resourceType": "Observation", "criteria": "subject=Patient/" + p1},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := as(pol)
	status := func(err error) int {
		var oe *fhirrouter.Error
		if errors.As(err, &oe) {
			return oe.Status
		}
		if err != nil {
			return -1
		}
		return 200
	}
	exp := func(what string, err error, want int) {
		t.Helper()
		if got := status(err); got != want {
			t.Errorf("%s: %d, muốn %d (%v)", what, got, want, err)
		}
	}
	_, err = repo.ReadResource(ctx, "Observation", o1)
	exp("đọc obs của p1", err, 200)
	_, err = repo.ReadResource(ctx, "Observation", o2)
	exp("đọc obs của p2", err, 404)
	_, err = repo.ReadResource(ctx, "Patient", p2)
	exp("đọc p2", err, 404)
	_, err = repo.ReadResource(ctx, "Condition", "x")
	exp("loại ngoài chính sách", err, 403)
	_, err = repo.ReadHistory(ctx, "Observation", o2)
	exp("history obs p2", err, 404)

	// ghi
	_, err = repo.CreateResource(ctx, mustParse(t, obs(p1)))
	exp("tạo obs p1", err, 200)
	_, err = repo.CreateResource(ctx, mustParse(t, obs(p2)))
	exp("tạo obs p2", err, 403)
	_, err = repo.CreateResource(ctx, mustParse(t, `{"resourceType":"Patient"}`))
	exp("Patient chỉ đọc", err, 403)
	cur, _ := repo.ReadResource(ctx, "Observation", o1)
	_, err = repo.UpdateResource(ctx, cur, fhirrouter.UpdateOptions{})
	exp("sửa obs p1", err, 200)
	other := mustParse(t, `{"resourceType":"Observation","id":"`+o2+`","status":"final","code":{"text":"x"},"subject":{"reference":"Patient/`+p2+`"}}`)
	_, err = repo.UpdateResource(ctx, other, fhirrouter.UpdateOptions{})
	exp("sửa obs p2", err, 404)
	// sửa obs p1 thành của p2 (thoát khỏi tiêu chí)
	moved := mustParse(t, `{"resourceType":"Observation","id":"`+o1+`","status":"final","code":{"text":"x"},"subject":{"reference":"Patient/`+p2+`"}}`)
	_, err = repo.UpdateResource(ctx, moved, fhirrouter.UpdateOptions{})
	exp("đổi obs sang p2", err, 403)
	exp("xóa obs p2", repo.DeleteResource(ctx, "Observation", o2), 404)

	search := func(c context.Context, rt, q string) (*fhirrouter.SearchResult, error) {
		r, e := fhirsearch.ParseQuery(rt, q)
		if e != nil {
			t.Fatal(e)
		}
		return repo.Search(c, r)
	}
	res, err := search(ctx, "Observation", "")
	if err != nil || len(res.Matches) != 2 { // o1 và obs mới tạo cho p1
		t.Fatalf("search Observation: %v %v", res, err)
	}
	_, err = search(ctx, "Condition", "")
	exp("search Condition", err, 403)
	res, _ = search(ctx, "", "")
	for _, m := range res.Matches {
		if m.GetResourceType() == "Condition" || m.GetID() == o2 || m.GetID() == p2 {
			t.Errorf("search hệ thống lộ %s/%s", m.GetResourceType(), m.GetID())
		}
	}
	res, err = search(ctx, "Patient", "_revinclude=Observation:subject")
	if err != nil || len(res.Matches) != 1 || res.Matches[0].GetID() != p1 {
		t.Fatalf("search Patient: %v %v", res, err)
	}
	for _, m := range res.Included {
		if m.GetID() == o2 {
			t.Error("_revinclude lộ obs của p2")
		}
	}
	// chính sách rỗng: từ chối hết
	empty, _ := fhirauth.ParsePolicy([]any{})
	_, err = search(as(empty), "Patient", "")
	exp("chính sách rỗng", err, 403)
	if res, err = search(as(empty), "", ""); err != nil || len(res.Matches) != 0 {
		t.Fatalf("search hệ thống với chính sách rỗng: %v %v", res, err)
	}
	// admin op không dùng được với chính sách
	_, err = repo.Reindex(ctx, "Observation", o1, nil)
	exp("reindex", err, 403)
}

func TestCanUpdate(t *testing.T) {
	pol, err := fhirauth.ParsePolicy([]any{map[string]any{"resourceType": "Binary", "readonly": true}, map[string]any{"resourceType": "Patient"}})
	if err != nil {
		t.Fatal(err)
	}
	ro, err := fhirauth.ParsePolicy([]any{"Patient"})
	if err != nil {
		t.Fatal(err)
	}
	with := func(p *fhirauth.Principal) context.Context { return fhirauth.WithPrincipal(context.Background(), p) }
	for name, tc := range map[string]struct {
		ctx  context.Context
		rt   string
		want bool
	}{
		"không Principal":             {context.Background(), "Binary", false},
		"vai trò read":                {with(&fhirauth.Principal{Role: fhirauth.RoleRead}), "Binary", false},
		"vai trò none":                {with(&fhirauth.Principal{Role: fhirauth.RoleNone}), "Binary", false},
		"vai trò write":               {with(&fhirauth.Principal{Role: fhirauth.RoleWrite}), "Binary", true},
		"vai trò admin":               {with(&fhirauth.Principal{Role: fhirauth.RoleAdmin}), "Binary", true},
		"policy chỉ đọc Binary":       {with(&fhirauth.Principal{Role: fhirauth.RoleWrite, Policy: pol}), "Binary", false},
		"policy cho ghi Patient":      {with(&fhirauth.Principal{Role: fhirauth.RoleWrite, Policy: pol}), "Patient", true},
		"policy không liệt kê loại":   {with(&fhirauth.Principal{Role: fhirauth.RoleWrite, Policy: ro}), "Binary", false},
		"grant chỉ r":                 {with(&fhirauth.Principal{Role: fhirauth.RoleWrite, Grants: fhirauth.Grants{"Binary": fhirauth.PermRead}}), "Binary", false},
		"grant có u":                  {with(&fhirauth.Principal{Role: fhirauth.RoleWrite, Grants: fhirauth.Grants{"Binary": fhirauth.PermUpdate}}), "Binary", true},
		"grant chỉ c (không u)":       {with(&fhirauth.Principal{Role: fhirauth.RoleWrite, Grants: fhirauth.Grants{"Binary": fhirauth.PermCreate}}), "Binary", false},
		"grant loại khác":             {with(&fhirauth.Principal{Role: fhirauth.RoleWrite, Grants: fhirauth.Grants{"Patient": fhirauth.PermAll}}), "Binary", false},
		"grant ký tự đại diện đầy đủ": {with(&fhirauth.Principal{Role: fhirauth.RoleWrite, Grants: fhirauth.Grants{"*": fhirauth.PermAll}}), "Binary", true},
	} {
		if got := fhirauth.CanUpdate(tc.ctx, tc.rt); got != tc.want {
			t.Errorf("%s: %v, muốn %v", name, got, tc.want)
		}
	}
}
