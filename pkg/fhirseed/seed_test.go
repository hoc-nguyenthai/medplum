package fhirseed_test

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"trivita-fhir/pkg/fhirauth"
	"trivita-fhir/pkg/fhirrouter"
	"trivita-fhir/pkg/fhirsearch"
	"trivita-fhir/pkg/fhirseed"
)

func sum(m map[string]int) (n int) {
	for _, v := range m {
		n += v
	}
	return
}

// count đếm resource của một loại mà project nhìn thấy.
func count(t *testing.T, repo fhirrouter.Repository, project, rt string, q url.Values) int {
	t.Helper()
	ctx := fhirauth.WithPrincipal(context.Background(), &fhirauth.Principal{Subject: "t", Project: project, Role: fhirauth.RoleAdmin})
	req, err := fhirsearch.Parse(rt, q)
	if err != nil {
		t.Fatal(err)
	}
	req.Total = "accurate"
	req.Count = 1
	res, err := repo.Search(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if res.Total == nil {
		t.Fatal("thiếu total")
	}
	return *res.Total
}

func TestBuiltinSearchParametersIdempotentAndPerProject(t *testing.T) {
	repo := fhirauth.Scope(fhirrouter.NewMemoryRepository())
	ctx := context.Background()

	dry, err := fhirseed.Seed(ctx, repo, fhirseed.Options{Project: "p1", DryRun: true})
	if err != nil || sum(dry.Created) == 0 || sum(dry.Failed) != 0 {
		t.Fatalf("dry-run: %+v %v", dry, err)
	}
	if n := count(t, repo, "p1", "SearchParameter", nil); n != 0 {
		t.Fatalf("dry-run không được ghi: %d", n)
	}

	first, err := fhirseed.Seed(ctx, repo, fhirseed.Options{Project: "p1"})
	if err != nil || sum(first.Failed) != 0 {
		t.Fatalf("seed: %+v %v", first, err)
	}
	if first.Created["SearchParameter"] != dry.Created["SearchParameter"] {
		t.Errorf("dry-run %d khác thật %d", dry.Created["SearchParameter"], first.Created["SearchParameter"])
	}
	if n := count(t, repo, "p1", "SearchParameter", url.Values{"code": {"birthdate"}, "base": {"Patient"}}); n != 1 {
		t.Errorf("Patient birthdate: %d", n)
	}

	again, err := fhirseed.Seed(ctx, repo, fhirseed.Options{Project: "p1"})
	if err != nil || sum(again.Created) != 0 || sum(again.Updated) != 0 || again.Unchanged["SearchParameter"] != first.Created["SearchParameter"] {
		t.Fatalf("chạy lại phải không đổi gì: %+v %v", again, err)
	}

	// project khác có bản riêng, không thấy và không va chạm id
	if n := count(t, repo, "p2", "SearchParameter", nil); n != 0 {
		t.Fatalf("p2 thấy dữ liệu của p1: %d", n)
	}
	other, err := fhirseed.Seed(ctx, repo, fhirseed.Options{Project: "p2", Types: []string{"SearchParameter"}})
	if err != nil || other.Created["SearchParameter"] != first.Created["SearchParameter"] || sum(other.Failed) != 0 {
		t.Fatalf("seed p2: %+v %v", other, err)
	}
}

const sdBundle = `{"resourceType":"Bundle","entry":[
 {"resource":{"resourceType":"StructureDefinition","id":"x","url":"http://example.org/sd/a","name":"A","status":"active","kind":"resource","abstract":false,"type":"Patient",
  "text":{"status":"generated","div":"<div>x</div>"},"differential":{"element":[{"id":"Patient","path":"Patient"}]},"meta":{"versionId":"9"}}},
 {"resource":{"resourceType":"OperationDefinition","url":"http://example.org/op/a","name":"opa","status":"active","kind":"operation","code":"opa","system":true,"type":false,"instance":false}},
 {"resource":{"resourceType":"StructureDefinition","name":"no-url","status":"active"}}]}`

const vsBundle = `{"resourceType":"Bundle","entry":[
 {"resource":{"resourceType":"ValueSet","url":"http://example.org/vs/a","status":"active","name":"One"}},
 {"resource":{"resourceType":"CodeSystem","url":"http://example.org/cs/a","status":"active","content":"not-present"}}]}`

func TestSeedFromDirectory(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("profiles-resources.json", sdBundle)
	write("valuesets.json", vsBundle)
	repo := fhirauth.Scope(fhirrouter.NewMemoryRepository())
	ctx := context.Background()

	rep, err := fhirseed.Seed(ctx, repo, fhirseed.Options{Project: "p1", Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Created["StructureDefinition"] != 1 || rep.Created["OperationDefinition"] != 1 || rep.Created["ValueSet"] != 1 || rep.Created["CodeSystem"] != 1 {
		t.Fatalf("created: %+v", rep.Created)
	}
	if rep.Failed["StructureDefinition"] != 1 || len(rep.Errors) != 1 || !strings.Contains(rep.Errors[0], "thiếu url") {
		t.Fatalf("resource không url phải báo lỗi: %+v", rep)
	}
	if len(rep.Missing) == 0 || !slicesContains(rep.Missing, "search-parameters.json") {
		t.Errorf("Missing: %v", rep.Missing)
	}
	if n := count(t, repo, "p1", "StructureDefinition", url.Values{"url": {"http://example.org/sd/a"}}); n != 1 {
		t.Errorf("StructureDefinition: %d", n)
	}
	// text và differential bị bỏ
	ctxp := fhirauth.WithPrincipal(ctx, &fhirauth.Principal{Subject: "t", Project: "p1", Role: fhirauth.RoleAdmin})
	req, _ := fhirsearch.Parse("StructureDefinition", url.Values{"url": {"http://example.org/sd/a"}})
	res, _ := repo.Search(ctxp, req)
	if raw := marshal(t, res.Matches[0]); strings.Contains(raw, "differential") || strings.Contains(raw, `"text"`) {
		t.Errorf("còn text/differential: %s", raw)
	}

	// chạy lại không đổi; sửa nguồn thì cập nhật đúng một resource
	again, _ := fhirseed.Seed(ctx, repo, fhirseed.Options{Project: "p1", Dir: dir})
	if sum(again.Created)+sum(again.Updated) != 0 || sum(again.Unchanged) != 4 {
		t.Fatalf("chạy lại: %+v", again)
	}
	write("valuesets.json", strings.Replace(vsBundle, `"name":"One"`, `"name":"Two"`, 1))
	upd, _ := fhirseed.Seed(ctx, repo, fhirseed.Options{Project: "p1", Dir: dir})
	if upd.Updated["ValueSet"] != 1 || sum(upd.Updated) != 1 || sum(upd.Created) != 0 {
		t.Fatalf("sau khi sửa: %+v", upd)
	}

	// giới hạn loại
	only, _ := fhirseed.Seed(ctx, repo, fhirseed.Options{Project: "p2", Dir: dir, Types: []string{"ValueSet"}})
	if sum(only.Created) != 1 || only.Created["ValueSet"] != 1 {
		t.Fatalf("Types: %+v", only)
	}
}

func TestSeedErrors(t *testing.T) {
	repo := fhirauth.Scope(fhirrouter.NewMemoryRepository())
	ctx := context.Background()
	if _, err := fhirseed.Seed(ctx, repo, fhirseed.Options{Project: "p", Types: []string{"Patient"}}); err == nil {
		t.Error("loại không hỗ trợ phải lỗi")
	}
	if _, err := fhirseed.Seed(ctx, repo, fhirseed.Options{Project: "p", Dir: filepath.Join(t.TempDir(), "none")}); err == nil {
		t.Error("thư mục không có phải lỗi")
	}
	if _, err := fhirseed.Seed(ctx, repo, fhirseed.Options{Project: "p", Dir: t.TempDir()}); err == nil {
		t.Error("thư mục rỗng phải lỗi")
	}
	bad := t.TempDir()
	_ = os.WriteFile(filepath.Join(bad, "search-parameters.json"), []byte("{not json"), 0o600)
	if _, err := fhirseed.Seed(ctx, repo, fhirseed.Options{Project: "p", Dir: bad}); err == nil {
		t.Error("JSON hỏng phải lỗi")
	}
}

func TestSeedWithoutProjectWritesDirectly(t *testing.T) {
	mem := fhirrouter.NewMemoryRepository()
	rep, err := fhirseed.Seed(context.Background(), mem, fhirseed.Options{Types: []string{"SearchParameter"}})
	if err != nil || rep.Created["SearchParameter"] == 0 || sum(rep.Failed) != 0 {
		t.Fatalf("%+v %v", rep, err)
	}
	if n := count(t, mem, "", "SearchParameter", nil); n != rep.Created["SearchParameter"] {
		t.Errorf("số lượng: %d", n)
	}
}

func slicesContains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func marshal(t *testing.T, v any) string {
	t.Helper()
	b, err := jsonMarshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

var jsonMarshal = json.Marshal

// Cùng url ở nhiều tệp: tệp sau thắng, và chạy lại không cập nhật qua lại.
func TestSeedDuplicateURLLastFileWins(t *testing.T) {
	dir := t.TempDir()
	mk := func(name string) string {
		return `{"resourceType":"Bundle","entry":[{"resource":{"resourceType":"ValueSet","url":"http://example.org/vs/dup","status":"active","name":"` + name + `"}}]}`
	}
	_ = os.WriteFile(filepath.Join(dir, "valuesets.json"), []byte(mk("First")), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "valuesets-medplum.json"), []byte(mk("Second")), 0o600)
	repo := fhirauth.Scope(fhirrouter.NewMemoryRepository())
	for i := 0; i < 2; i++ {
		rep, err := fhirseed.Seed(context.Background(), repo, fhirseed.Options{Project: "p1", Dir: dir})
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 && (rep.Created["ValueSet"] != 1 || sum(rep.Updated) != 0) || i == 1 && (sum(rep.Created)+sum(rep.Updated) != 0 || rep.Unchanged["ValueSet"] != 1) {
			t.Fatalf("lượt %d: %+v", i, rep)
		}
	}
	ctx := fhirauth.WithPrincipal(context.Background(), &fhirauth.Principal{Subject: "t", Project: "p1", Role: fhirauth.RoleAdmin})
	req, _ := fhirsearch.Parse("ValueSet", url.Values{"url": {"http://example.org/vs/dup"}})
	res, _ := repo.Search(ctx, req)
	if !strings.Contains(marshal(t, res.Matches[0]), `"Second"`) {
		t.Errorf("phải giữ bản của tệp sau: %s", marshal(t, res.Matches[0]))
	}
}
