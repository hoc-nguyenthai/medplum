package fhirrouter

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseGraphQL(t *testing.T) {
	doc, err := parseGraphQL(`
	# chú thích
	query Q($id: ID!, $n: Int = 5, $l: [String!] = ["a", "b"]) @dir(x: 1) {
	  a: Patient(id: $id, flag: true, nothing: null, ratio: -1.5e2, mode: ENUM, obj: {k: [1, 2], s: "x\nyA"}) @skip(if: false) {
	    id ...F ... on Patient { name { family } } ... { gender }
	  }
	}
	fragment F on Patient { birthDate }`)
	if err != nil {
		t.Fatal(err)
	}
	op := doc.ops[0]
	if op.name != "Q" || len(op.vars) != 3 || op.vars["n"] != int64(5) {
		t.Fatalf("op: %+v", op)
	}
	f := op.sel[0]
	if f.alias != "a" || f.name != "Patient" || len(f.args) != 6 || len(f.sel) != 4 {
		t.Fatalf("field: %+v", f)
	}
	args := map[string]any{}
	for _, a := range f.args {
		args[a.name] = a.val
	}
	if args["id"] != gqlVar("id") || args["flag"] != true || args["nothing"] != nil || args["ratio"] != -150.0 || args["mode"] != gqlEnum("ENUM") {
		t.Errorf("args: %v", args)
	}
	obj := args["obj"].(map[string]gqlValue)
	if obj["s"] != "x\nyA" || len(obj["k"].([]gqlValue)) != 2 {
		t.Errorf("obj: %v", obj)
	}
	if doc.frags["F"] == nil || doc.frags["F"].typeCond != "Patient" {
		t.Errorf("fragment: %+v", doc.frags)
	}
	if d, err := parseGraphQL(`{ Patient(id: "1") { id } }`); err != nil || len(d.ops) != 1 { // query rút gọn
		t.Errorf("rút gọn: %v", err)
	}
	if d, err := parseGraphQL(`{ a(s: """nhiều
dòng""") }`); err != nil || d.ops[0].sel[0].args[0].val != "nhiều\ndòng" {
		t.Errorf("chuỗi nhiều dòng: %v", err)
	}
}

func TestParseGraphQLErrors(t *testing.T) {
	for name, q := range map[string]string{
		"rỗng":             ``,
		"chỉ chú thích":    `# không có gì`,
		"mutation":         `mutation { a { b } }`,
		"subscription":     `subscription { a { b } }`,
		"thiếu ngoặc":      `{ a { b }`,
		"selection rỗng":   `{ a { } }`,
		"chuỗi không đóng": `{ a(x: "abc) { b } }`,
		"escape sai":       `{ a(x: "\q") { b } }`,
		"ký tự lạ":         "{ a ^ }",
		"biến ở mặc định":  `query Q($a: Int = $b) { x }`,
		"fragment lặp":     `{ a } fragment F on T { b } fragment F on T { c }`,
		"dấu chấm":         `{ .. }`,
		"quá sâu":          strings.Repeat("{ a ", 15) + strings.Repeat("}", 15),
		"quá dài":          `{ a(x: "` + strings.Repeat("x", maxGraphQLQueryBytes) + `") }`,
		"giá trị lồng sâu": `{ a(x: ` + strings.Repeat("[", 12) + strings.Repeat("]", 12) + `) }`,
	} {
		if _, err := parseGraphQL(q); err == nil {
			t.Errorf("%s: muốn lỗi", name)
		}
	}
}

func newGQLRouter(t *testing.T) (*Router, func(query string) map[string]any) {
	t.Helper()
	r := New(NewMemoryRepository(), Options{})
	t.Cleanup(func() { _ = r.Shutdown(context.Background()) })
	h := r.HTTPHandler()
	return r, func(query string) map[string]any {
		body, _ := json.Marshal(map[string]any{"query": query})
		req := httptest.NewRequest("POST", "/$graphql", strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return out
	}
}

func TestGraphQLResourceBudget(t *testing.T) {
	defer func(old int) { maxGraphQLResources = old }(maxGraphQLResources)
	r, gql := newGQLRouter(t)
	ctx := context.Background()
	for i := 0; i < 6; i++ {
		res, _ := toResource(map[string]any{"resourceType": "Patient", "id": "p" + string(rune('a'+i))})
		if _, err := r.repo.CreateResource(ctx, res); err != nil {
			t.Fatal(err)
		}
	}
	maxGraphQLResources = 4
	out := gql(`{ PatientList(_count: 10) { id } }`)
	list, _ := out["data"].(map[string]any)["PatientList"].([]any)
	if len(list) != 4 || out["errors"] == nil {
		t.Fatalf("ngân sách phải cắt kết quả và báo lỗi: %v", out)
	}
	// ngân sách là của cả truy vấn, không phải từng field
	maxGraphQLResources = 5
	out = gql(`{ a: PatientList(_count: 3) { id } b: PatientList(_count: 3) { id } }`)
	if out["errors"] == nil {
		t.Fatalf("hai danh sách vượt ngân sách chung phải báo lỗi: %v", out)
	}
}

func TestGraphQLListCountIsCapped(t *testing.T) {
	r, gql := newGQLRouter(t)
	ctx := context.Background()
	for i := 0; i < graphQLListMax+5; i++ {
		res, _ := toResource(map[string]any{"resourceType": "Observation", "status": "final", "code": map[string]any{"text": "x"}})
		if _, err := r.repo.CreateResource(ctx, res); err != nil {
			t.Fatal(err)
		}
	}
	out := gql(`{ ObservationList(_count: 100000) { id } }`)
	if l, _ := out["data"].(map[string]any)["ObservationList"].([]any); len(l) != graphQLListMax {
		t.Fatalf("_count phải bị cắt về %d, được %d", graphQLListMax, len(l))
	}
	out = gql(`{ ObservationList { id } }`)
	if l, _ := out["data"].(map[string]any)["ObservationList"].([]any); len(l) != graphQLListDefault {
		t.Fatalf("mặc định %d, được %d", graphQLListDefault, len(l))
	}
	if out := gql(`{ ObservationList(_count: -1) { id } }`); out["errors"] == nil {
		t.Error("_count âm phải báo lỗi")
	}
}

func TestGraphQLFragmentCycleAndSelfReference(t *testing.T) {
	r, gql := newGQLRouter(t)
	res, _ := toResource(map[string]any{"resourceType": "Patient", "id": "p1"})
	if _, err := r.repo.CreateResource(context.Background(), res); err != nil {
		t.Fatal(err)
	}
	out := gql(`{ PatientList { id ...A } } fragment A on Patient { ...B } fragment B on Patient { ...A }`)
	if out["errors"] == nil {
		t.Fatalf("fragment vòng phải báo lỗi (và không lặp vô hạn): %v", out)
	}
	if out := gql(`{ PatientList { id ...Missing } }`); out["errors"] == nil {
		t.Fatalf("fragment không tồn tại phải báo lỗi: %v", out)
	}
}
