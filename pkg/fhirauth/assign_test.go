package fhirauth_test

import (
	"context"
	"testing"

	"trivita-fhir/pkg/fhirauth"
	"trivita-fhir/pkg/fhirsearch"
)

func TestAssignProject(t *testing.T) {
	base := newSQLite(t)
	ctx := context.Background()
	// dữ liệu cũ: ghi thẳng, không có project
	var legacy []string
	for i := 0; i < 3; i++ {
		legacy = append(legacy, mustCreate(t, base, ctx, `{"resourceType":"Patient"}`))
	}
	obsLegacy := mustCreate(t, base, ctx, `{"resourceType":"Observation","status":"final","code":{"text":"x"}}`)
	owned := mustCreate(t, base, ctx, `{"resourceType":"Patient","meta":{"project":"other"}}`)
	ownedV, _ := base.ReadResource(ctx, "Patient", owned)

	rep, err := fhirauth.AssignProject(ctx, base, "p1", nil, true)
	if err != nil || rep.Total() != 4 || rep.Assigned["Patient"] != 3 || rep.Assigned["Observation"] != 1 {
		t.Fatalf("dry-run: %+v %v", rep, err)
	}
	repo := fhirauth.Scope(base)
	p1 := fhirauth.WithPrincipal(ctx, &fhirauth.Principal{Subject: "u", Project: "p1", Role: fhirauth.RoleRead})
	if _, err := repo.ReadResource(p1, "Patient", legacy[0]); err == nil {
		t.Fatal("dry-run không được ghi")
	}

	// giới hạn theo loại
	rep, err = fhirauth.AssignProject(ctx, base, "p1", []string{"Observation"}, false)
	if err != nil || rep.Total() != 1 {
		t.Fatalf("theo loại: %+v %v", rep, err)
	}
	if _, err := repo.ReadResource(p1, "Observation", obsLegacy); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ReadResource(p1, "Patient", legacy[0]); err == nil {
		t.Fatal("Patient chưa được gán")
	}

	rep, err = fhirauth.AssignProject(ctx, base, "p1", nil, false)
	if err != nil || rep.Total() != 3 || rep.Failed != 0 {
		t.Fatalf("gán: %+v %v", rep, err)
	}
	for _, id := range legacy {
		if _, err := repo.ReadResource(p1, "Patient", id); err != nil {
			t.Errorf("p1 không thấy %s: %v", id, err)
		}
	}
	// resource của project khác không bị đụng, project khác không thấy dữ liệu vừa gán
	if r, _ := base.ReadResource(ctx, "Patient", owned); r.GetMeta().Project != "other" || r.GetMeta().VersionId != ownedV.GetMeta().VersionId {
		t.Errorf("đụng vào resource của project khác: %+v", r.GetMeta())
	}
	p2 := fhirauth.WithPrincipal(ctx, &fhirauth.Principal{Subject: "u", Project: "p2", Role: fhirauth.RoleRead})
	req, _ := fhirsearch.ParseQuery("Patient", "")
	if res, err := repo.Search(p2, req); err != nil || len(res.Matches) != 0 {
		t.Errorf("p2 thấy dữ liệu của p1: %v %v", res, err)
	}
	// idempotent
	if rep, err = fhirauth.AssignProject(ctx, base, "p1", nil, false); err != nil || rep.Total() != 0 {
		t.Fatalf("chạy lại: %+v %v", rep, err)
	}
	// lỗi đầu vào
	if _, err := fhirauth.AssignProject(ctx, base, "", nil, false); err == nil {
		t.Error("thiếu project phải lỗi")
	}
	if _, err := fhirauth.AssignProject(ctx, base, "p1", []string{"Nope"}, false); err == nil {
		t.Error("loại lạ phải lỗi")
	}
}
