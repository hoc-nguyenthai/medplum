package fhirrouter_test

import (
	"context"
	"net/url"
	"testing"

	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirrouter"
	"trivita-fhir/pkg/fhirsearch"
)

func guardEnv(t *testing.T) (fhirrouter.Repository, *fhirrouter.MemoryRepository) {
	t.Helper()
	base := fhirrouter.NewMemoryRepository()
	owned := func(id, owner string) {
		b := &fhir.Binary{ID: id, ContentType: "application/fhir+ndjson", Data: "eA=="}
		if owner != "" {
			b.SecurityContext = &fhir.Reference{Identifier: &fhir.Identifier{System: fhirrouter.JobOwnerSystem, Value: owner}}
		}
		if _, err := base.CreateResource(context.Background(), b); err != nil {
			t.Fatal(err)
		}
	}
	owned("job-file", "alice")
	owned("plain", "")
	return fhirrouter.NewGuard(base, func(ctx context.Context, owner string) bool {
		u, _ := ctx.Value(userKey{}).(string)
		return u == "" || u == "root" || owner == u
	}), base
}

func as(user string) context.Context { return context.WithValue(context.Background(), userKey{}, user) }

func isNotFound(err error) bool {
	e, ok := err.(*fhirrouter.Error)
	return ok && e.Status == 404
}

func TestGuardHidesJobBinariesFromOthers(t *testing.T) {
	g, base := guardEnv(t)
	for _, c := range []struct {
		user string
		ok   bool
	}{{"alice", true}, {"root", true}, {"", true}, {"bob", false}} {
		_, err := g.ReadResource(as(c.user), "Binary", "job-file")
		if (err == nil) != c.ok || (!c.ok && !isNotFound(err)) {
			t.Errorf("read %q: err=%v, muốn ok=%v (404 khi bị ẩn)", c.user, err, c.ok)
		}
	}
	if _, err := g.ReadResource(as("bob"), "Binary", "plain"); err != nil {
		t.Errorf("Binary thường phải đọc được: %v", err)
	}
	cur, _ := base.ReadResource(context.Background(), "Binary", "job-file")
	vid := cur.GetMeta().VersionId
	if _, err := g.ReadVersion(as("bob"), "Binary", "job-file", vid); !isNotFound(err) {
		t.Errorf("vread bob: %v", err)
	}
	if _, err := g.ReadVersion(as("alice"), "Binary", "job-file", vid); err != nil {
		t.Errorf("vread alice: %v", err)
	}
	if _, err := g.ReadHistory(as("bob"), "Binary", "job-file"); !isNotFound(err) {
		t.Errorf("history bob: %v", err)
	}
	if h, err := g.ReadHistory(as("alice"), "Binary", "job-file"); err != nil || len(h) == 0 {
		t.Errorf("history alice: %v %v", h, err)
	}
	// ghi vào Binary của người khác: không sửa, không xóa, không expunge
	upd := &fhir.Binary{ID: "job-file", ContentType: "text/plain", Data: "eQ=="}
	if _, err := g.UpdateResource(as("bob"), upd, fhirrouter.UpdateOptions{}); !isNotFound(err) {
		t.Errorf("update bob: %v", err)
	}
	if err := g.DeleteResource(as("bob"), "Binary", "job-file"); !isNotFound(err) {
		t.Errorf("delete bob: %v", err)
	}
	if err := g.(fhirrouter.Expunger).Expunge(as("bob"), "Binary", "job-file"); !isNotFound(err) {
		t.Errorf("expunge bob: %v", err)
	}
	after, err := base.ReadResource(context.Background(), "Binary", "job-file")
	if err != nil || after.(*fhir.Binary).ContentType != "application/fhir+ndjson" {
		t.Errorf("Binary bị đổi dù bob không được phép: %v %v", after, err)
	}
	if _, err := g.UpdateResource(as("alice"), &fhir.Binary{ID: "job-file", ContentType: "application/fhir+ndjson", Data: "eQ=="}, fhirrouter.UpdateOptions{}); err != nil {
		t.Errorf("update alice: %v", err)
	}
	if err := g.DeleteResource(as("root"), "Binary", "job-file"); err != nil {
		t.Errorf("delete root: %v", err)
	}
}

func TestGuardFiltersSearchAndAdjustsTotal(t *testing.T) {
	g, _ := guardEnv(t)
	sr, _ := fhirsearch.Parse("Binary", url.Values{"_total": {"accurate"}})
	res, err := g.Search(as("bob"), sr)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Matches) != 1 || res.Matches[0].GetID() != "plain" || res.Total == nil || *res.Total != 1 {
		t.Errorf("bob chỉ thấy Binary thường, total=1: %+v total=%v", res.Matches, res.Total)
	}
	res, _ = g.Search(as("alice"), sr)
	if len(res.Matches) != 2 || *res.Total != 2 {
		t.Errorf("alice thấy cả hai: %+v", res.Matches)
	}
}

// includedStub trả kết quả dựng sẵn để kiểm phần Included (_include/_revinclude) mà MemoryRepository không tạo ra.
type includedStub struct {
	*fhirrouter.MemoryRepository
	res *fhirrouter.SearchResult
}

func (s includedStub) Search(context.Context, *fhirsearch.Request) (*fhirrouter.SearchResult, error) {
	return s.res, nil
}

func TestGuardFiltersIncludedBinaries(t *testing.T) {
	mine := &fhir.Binary{ID: "j", SecurityContext: &fhir.Reference{Identifier: &fhir.Identifier{System: fhirrouter.JobOwnerSystem, Value: "alice"}}}
	plain := &fhir.Binary{ID: "p"}
	stub := includedStub{fhirrouter.NewMemoryRepository(), &fhirrouter.SearchResult{Matches: []fhir.Resource{&fhir.Patient{ID: "x"}}, Included: []fhir.Resource{mine, plain}}}
	g := fhirrouter.NewGuard(stub, func(ctx context.Context, owner string) bool { u, _ := ctx.Value(userKey{}).(string); return u == owner })
	sr, _ := fhirsearch.Parse("Patient", url.Values{})
	res, _ := g.Search(as("bob"), sr)
	if len(res.Included) != 1 || res.Included[0].GetID() != "p" || len(res.Matches) != 1 {
		t.Errorf("bob: Included=%v Matches=%v", res.Included, res.Matches)
	}
	res, _ = g.Search(as("alice"), sr)
	if len(res.Included) != 2 {
		t.Errorf("alice phải thấy cả hai: %v", res.Included)
	}
	if len(stub.res.Included) != 2 {
		t.Errorf("kết quả gốc của repository bị sửa tại chỗ: %v", stub.res.Included)
	}
}

func TestGuardKeepsOptionalInterfacesAndTransactions(t *testing.T) {
	g, base := guardEnv(t)
	if _, ok := g.(fhirrouter.Transactional); !ok {
		t.Fatal("Guard phải giữ Transactional")
	}
	for name, ok := range map[string]bool{
		"Reindexer": func() bool { _, ok := g.(fhirrouter.Reindexer); return ok }(), "Expunger": func() bool { _, ok := g.(fhirrouter.Expunger); return ok }(),
		"KeyLocker": func() bool { _, ok := g.(fhirrouter.KeyLocker); return ok }(), "AdminChecker": func() bool { _, ok := g.(fhirrouter.AdminChecker); return ok }(),
		"ConceptStore": func() bool { _, ok := g.(fhirrouter.ConceptStore); return ok }(), "MappingStore": func() bool { _, ok := g.(fhirrouter.MappingStore); return ok }(),
	} {
		if !ok {
			t.Errorf("Guard mất interface %s", name)
		}
	}
	// repository bên trong không có AdminChecker: RequireAdmin cho qua (như router khi không xác thực)
	if err := g.(fhirrouter.AdminChecker).RequireAdmin(as("bob")); err != nil {
		t.Errorf("RequireAdmin không có AdminChecker bên trong phải cho qua: %v", err)
	}
	// đọc trong transaction cũng bị lọc
	err := g.(fhirrouter.Transactional).RunInTransaction(as("bob"), func(rp fhirrouter.Repository) error {
		if _, err := rp.ReadResource(as("bob"), "Binary", "job-file"); !isNotFound(err) {
			t.Errorf("đọc trong transaction: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// không có hàm allowed: không bọc gì
	if got := fhirrouter.NewGuard(base, nil); got != fhirrouter.Repository(base) {
		t.Errorf("NewGuard(nil) phải trả repository gốc")
	}
}
