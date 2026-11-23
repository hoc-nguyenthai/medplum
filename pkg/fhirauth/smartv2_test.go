package fhirauth_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirauth"
	"trivita-fhir/pkg/fhirrouter"
	"trivita-fhir/pkg/fhirsearch"
)

// letters trả các chữ SMART v2 mà g cấp cho loại rt, theo thứ tự cruds.
func letters(g fhirauth.Grants, rt string) string {
	var b strings.Builder
	for _, x := range []struct {
		c byte
		p fhirauth.Perm
	}{{'c', fhirauth.PermCreate}, {'r', fhirauth.PermRead}, {'u', fhirauth.PermUpdate}, {'d', fhirauth.PermDelete}, {'s', fhirauth.PermSearch}} {
		if g.Can(rt, x.p) {
			b.WriteByte(x.c)
		}
	}
	return b.String()
}

func verifier(t *testing.T) *fhirauth.Verifier {
	t.Helper()
	v, err := fhirauth.NewVerifier(fhirauth.Config{Issuer: iss, Audience: aud, HMACSecret: secret})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// Mỗi chữ của SMART v2 cấp ĐÚNG quyền của nó: không gộp c/u/d thành "ghi", không gộp r/s thành "đọc".
func TestSMARTv2LettersGrantExactlyThemselves(t *testing.T) {
	v := verifier(t)
	cases := []struct {
		scope, want string
		role        fhirauth.Role
	}{
		{"system/Patient.c", "c", fhirauth.RoleWrite},
		{"system/Patient.r", "r", fhirauth.RoleRead},
		{"system/Patient.u", "u", fhirauth.RoleWrite},
		{"system/Patient.d", "d", fhirauth.RoleWrite},
		{"system/Patient.s", "s", fhirauth.RoleRead},
		{"system/Patient.cu", "cu", fhirauth.RoleWrite},
		{"system/Patient.rs", "rs", fhirauth.RoleRead},
		{"system/Patient.cruds", "cruds", fhirauth.RoleWrite},
		{"user/Patient.c", "c", fhirauth.RoleWrite},
		{"system/Patient.sr", "rs", fhirauth.RoleRead}, // thứ tự chữ không quan trọng
		{"system/Patient.ccc", "c", fhirauth.RoleWrite},
		// dạng v1: read = rs, write = cud, * = cruds
		{"system/Patient.read", "rs", fhirauth.RoleRead},
		{"system/Patient.write", "cud", fhirauth.RoleWrite},
		{"system/Patient.*", "cruds", fhirauth.RoleWrite},
		// nhiều scope cộng dồn nhưng không nở thêm
		{"system/Patient.c system/Patient.s", "cs", fhirauth.RoleWrite},
		{"system/Patient.r system/Patient.d", "rd", fhirauth.RoleWrite},
		{"system/Patient.c user/Patient.c", "c", fhirauth.RoleWrite},
	}
	for _, c := range cases {
		p, err := v.Verify(context.Background(), hs(t, claims(map[string]any{"scope": c.scope})))
		if err != nil {
			t.Errorf("%q: %v", c.scope, err)
			continue
		}
		if got := letters(p.Grants, "Patient"); got != c.want {
			t.Errorf("%q cấp %q, mong %q", c.scope, got, c.want)
		}
		if p.Role != c.role {
			t.Errorf("%q role %v, mong %v", c.scope, p.Role, c.role)
		}
		if got := letters(p.Grants, "Observation"); got != "" {
			t.Errorf("%q không được cấp quyền trên loại khác: %q", c.scope, got)
		}
	}
	// wildcard loại
	p, _ := v.Verify(context.Background(), hs(t, claims(map[string]any{"scope": "system/*.c"})))
	if letters(p.Grants, "Observation") != "c" || letters(p.Grants, "Patient") != "c" || p.Grants.FullSearch() {
		t.Errorf("system/*.c: %v", p.Grants)
	}
	// quyền lạ, rỗng, scope tham số (chưa hỗ trợ) đều không cấp gì (fail đóng)
	for _, s := range []string{"system/Patient.x", "system/Patient.crux", "system/Patient.", "system/Patient.rs?category=x", "system/Patient.C", "system/Patient.read2"} {
		if _, err := v.Verify(context.Background(), hs(t, claims(map[string]any{"scope": s}))); !errors.Is(err, fhirauth.ErrForbidden) {
			t.Errorf("%q phải bị từ chối: %v", s, err)
		}
	}
}

// Scope vai trò giữ nguyên nghĩa: fhir.read = đọc và tìm kiếm mọi loại; fhir.write và fhir.admin = mọi quyền.
func TestRoleScopesKeepTheirMeaning(t *testing.T) {
	v := verifier(t)
	for scope, want := range map[string]string{"fhir.read": "rs", "fhir.write": "cruds", "fhir.admin": "cruds"} {
		p, err := v.Verify(context.Background(), hs(t, claims(map[string]any{"scope": scope})))
		if err != nil {
			t.Fatal(err)
		}
		if got := letters(p.Grants, "Patient"); got != want || !p.Grants.FullSearch() {
			t.Errorf("%s cấp %q, mong %q", scope, got, want)
		}
	}
}

func TestGrantsHelpers(t *testing.T) {
	g := fhirauth.Grants{"Patient": fhirauth.PermSearch, "Observation": fhirauth.PermRead, "*": fhirauth.PermCreate}
	if !g.CanSearch("Patient") || g.CanRead("Patient") || !g.CanRead("Observation") || g.CanSearch("Observation") {
		t.Errorf("%v", g)
	}
	if !g.CanSee("Patient") || !g.CanSee("Observation") || g.CanSee("Condition") {
		t.Error("CanSee = đọc hoặc tìm kiếm")
	}
	if !g.Can("Condition", fhirauth.PermCreate) || g.Can("Condition", fhirauth.PermCreate|fhirauth.PermUpdate) {
		t.Error("Can phải đủ MỌI bit")
	}
	if g.FullSearch() {
		t.Error("không có s trên *")
	}
	if got := strings.Join(g.Searchable(), ","); got != "Patient" {
		t.Errorf("Searchable: %q", got)
	}
	var nilG fhirauth.Grants
	if !nilG.Can("X", fhirauth.PermAll) || !nilG.CanSee("X") || !nilG.FullSearch() {
		t.Error("nil nghĩa là không giới hạn")
	}
	if fhirauth.PermWrite != fhirauth.PermCreate|fhirauth.PermUpdate|fhirauth.PermDelete || fhirauth.PermAllRead != fhirauth.PermRead|fhirauth.PermSearch {
		t.Error("tập quyền")
	}
}

// Ma trận thực thi ở Repo: một token chỉ có một chữ thì làm được ĐÚNG một thao tác.
func TestRepoEnforcesEachInteractionSeparately(t *testing.T) {
	base := newSQLite(t)
	repo := fhirauth.Scope(base)
	ctxWith := func(p fhirauth.Perm) context.Context {
		return fhirauth.WithPrincipal(context.Background(), &fhirauth.Principal{Subject: "u", Project: "p1", Role: fhirauth.RoleWrite, Grants: fhirauth.Grants{"Patient": p}})
	}
	admin := ctxWith(fhirauth.PermAll)
	newPatient := func() string { return mustCreate(t, repo, admin, `{"resourceType":"Patient","name":[{"family":"A"}]}`) }
	forbidden := func(err error) bool {
		var oe *fhirrouter.Error
		return errors.As(err, &oe) && oe.Status == http.StatusForbidden
	}
	existing := newPatient()

	ops := []struct {
		name string
		need fhirauth.Perm
		run  func(ctx context.Context) error
	}{
		{"create", fhirauth.PermCreate, func(ctx context.Context) error {
			_, err := repo.CreateResource(ctx, mustParse(t, `{"resourceType":"Patient"}`))
			return err
		}},
		{"read", fhirauth.PermRead, func(ctx context.Context) error { _, err := repo.ReadResource(ctx, "Patient", existing); return err }},
		{"vread", fhirauth.PermRead, func(ctx context.Context) error {
			_, err := repo.ReadVersion(ctx, "Patient", existing, "khong-co")
			if err != nil && !forbidden(err) {
				return nil // đã qua kiểm quyền (phiên bản không tồn tại là chuyện khác)
			}
			return err
		}},
		{"history", fhirauth.PermRead, func(ctx context.Context) error { _, err := repo.ReadHistory(ctx, "Patient", existing); return err }},
		{"update", fhirauth.PermUpdate, func(ctx context.Context) error {
			_, err := repo.UpdateResource(ctx, mustParse(t, `{"resourceType":"Patient","id":"`+existing+`","gender":"male"}`), fhirrouter.UpdateOptions{})
			return err
		}},
		{"delete", fhirauth.PermDelete, func(ctx context.Context) error { return repo.DeleteResource(ctx, "Patient", newPatient()) }},
		{"search", fhirauth.PermSearch, func(ctx context.Context) error {
			r, err := fhirsearch.ParseQuery("Patient", "")
			if err != nil {
				t.Fatal(err)
			}
			_, err = repo.Search(ctx, r)
			return err
		}},
	}
	for _, grant := range []fhirauth.Perm{fhirauth.PermCreate, fhirauth.PermRead, fhirauth.PermUpdate, fhirauth.PermDelete, fhirauth.PermSearch} {
		for _, op := range ops {
			err := op.run(ctxWith(grant))
			if op.need == grant {
				if forbidden(err) {
					t.Errorf("token chỉ có quyền #%d phải được %s: %v", grant, op.name, err)
				}
			} else if !forbidden(err) {
				t.Errorf("token chỉ có quyền #%d KHÔNG được %s (cần #%d), được %v", grant, op.name, op.need, err)
			}
		}
	}
	// quyền ở loại khác không cấp quyền ở Patient
	other := fhirauth.WithPrincipal(context.Background(), &fhirauth.Principal{Subject: "u", Project: "p1", Role: fhirauth.RoleWrite, Grants: fhirauth.Grants{"Observation": fhirauth.PermAll}})
	if _, err := repo.ReadResource(other, "Patient", existing); !forbidden(err) {
		t.Errorf("%v", err)
	}
}

// Thông báo lỗi nói rõ chữ SMART còn thiếu để người vận hành sửa scope.
func TestForbiddenMessageNamesTheMissingLetter(t *testing.T) {
	repo := fhirauth.Scope(newSQLite(t))
	ctx := fhirauth.WithPrincipal(context.Background(), &fhirauth.Principal{Subject: "u", Project: "p1", Role: fhirauth.RoleWrite, Grants: fhirauth.Grants{"Patient": fhirauth.PermCreate}})
	_, err := repo.UpdateResource(ctx, mustParse(t, `{"resourceType":"Patient","id":"x"}`), fhirrouter.UpdateOptions{})
	if err == nil || !strings.Contains(err.Error(), "quyền u") || !strings.Contains(err.Error(), "Patient") {
		t.Errorf("%v", err)
	}
}

// Kết quả kèm theo (_revinclude) chỉ lộ loại mà người gọi được đọc hoặc tìm kiếm.
func TestIncludedResultsFilteredBySeeableTypes(t *testing.T) {
	repo := fhirauth.Scope(newSQLite(t))
	ctxWith := func(g fhirauth.Grants) context.Context {
		return fhirauth.WithPrincipal(context.Background(), &fhirauth.Principal{Subject: "u", Project: "p1", Role: fhirauth.RoleWrite, Grants: g})
	}
	admin := ctxWith(fhirauth.Grants{"*": fhirauth.PermAll})
	pid := mustCreate(t, repo, admin, `{"resourceType":"Patient","name":[{"family":"A"}]}`)
	mustCreate(t, repo, admin, `{"resourceType":"Observation","status":"final","code":{"text":"x"},"subject":{"reference":"Patient/`+pid+`"}}`)
	mustCreate(t, repo, admin, `{"resourceType":"Condition","subject":{"reference":"Patient/`+pid+`"},"clinicalStatus":{"text":"a"}}`)
	// chỉ có `c` trên Condition (không đọc, không tìm): không được thấy Condition kèm theo; có `s` trên Observation thì được thấy
	g := fhirauth.Grants{"Patient": fhirauth.PermSearch, "Observation": fhirauth.PermSearch, "Condition": fhirauth.PermCreate}
	r, err := fhirsearch.ParseQuery("Patient", "_revinclude=Observation:subject&_revinclude=Condition:subject")
	if err != nil {
		t.Fatal(err)
	}
	res, err := repo.Search(ctxWith(g), r)
	if err != nil {
		t.Fatal(err)
	}
	var types []string
	for _, m := range res.Included {
		types = append(types, m.GetResourceType())
	}
	if strings.Join(types, ",") != "Observation" {
		t.Errorf("included = %v, mong chỉ Observation", types)
	}
	_ = fhir.ResourceTypes
}
