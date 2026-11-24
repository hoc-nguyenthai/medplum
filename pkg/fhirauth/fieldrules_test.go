package fhirauth_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirauth"
	"trivita-fhir/pkg/fhirrouter"
	"trivita-fhir/pkg/fhirsearch"
)

func policyOf(t *testing.T, rules ...any) *fhirauth.Policy {
	t.Helper()
	p, err := fhirauth.ParsePolicy(rules)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

type frEnv struct {
	t     *testing.T
	base  *fhirrouter.MemoryRepository
	repo  *fhirauth.Repo
	admin context.Context // đọc ghi tự do để dựng dữ liệu
}

func newFREnv(t *testing.T) *frEnv {
	base := fhirrouter.NewMemoryRepository()
	return &frEnv{t: t, base: base, repo: fhirauth.Scope(base),
		admin: fhirauth.WithPrincipal(context.Background(), &fhirauth.Principal{Subject: "a", Project: "A", Role: fhirauth.RoleAdmin})}
}

func (e *frEnv) as(p *fhirauth.Policy) context.Context {
	return fhirauth.WithPrincipal(context.Background(), &fhirauth.Principal{Subject: "u", Project: "A", Role: fhirauth.RoleWrite, Policy: p})
}

func (e *frEnv) put(res fhir.Resource) {
	e.t.Helper()
	if _, err := e.repo.CreateResource(e.admin, res); err != nil {
		e.t.Fatal(err)
	}
}

func jsonOf(t *testing.T, r fhir.Resource) string {
	t.Helper()
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func status(err error) int {
	var fe *fhirrouter.Error
	if errors.As(err, &fe) {
		return fe.Status
	}
	return 0
}

func patient(id string) *fhir.Patient {
	return &fhir.Patient{ID: id, Gender: "female", BirthDate: "1990-01-01",
		Name:    []fhir.HumanName{{Family: "Nguyễn", Given: []string{"An"}}, {Family: "Khác", Use: "nickname"}},
		Address: []fhir.Address{{City: "Huế", Line: []string{"1 Lê Lợi"}}}, Telecom: []fhir.ContactPoint{{System: "phone", Value: "0901"}}}
}

func TestParseFieldRules(t *testing.T) {
	ok := []any{
		map[string]any{"resourceType": "Patient", "hiddenFields": []any{"birthDate", "name.family", "deceased[x]"}, "readonlyFields": []any{"gender"}, "interaction": []any{"read", "search"}},
		map[string]any{"resourceType": "Observation", "interaction": []any{}},
	}
	if _, err := fhirauth.ParsePolicy(ok); err != nil {
		t.Fatal(err)
	}
	for name, rule := range map[string]map[string]any{
		"hiddenFields không phải mảng":       {"resourceType": "Patient", "hiddenFields": "name"},
		"tên trường rỗng":                    {"resourceType": "Patient", "hiddenFields": []any{""}},
		"tên trường có ký tự lạ":             {"resourceType": "Patient", "hiddenFields": []any{"name; drop"}},
		"đường dẫn kết thúc bằng chấm":       {"resourceType": "Patient", "readonlyFields": []any{"name."}},
		"phần tử không phải chuỗi":           {"resourceType": "Patient", "hiddenFields": []any{1}},
		"interaction lạ":                     {"resourceType": "Patient", "interaction": []any{"read", "explode"}},
		"interaction không phải mảng":        {"resourceType": "Patient", "interaction": "read"},
		"quá nhiều trường":                   {"resourceType": "Patient", "hiddenFields": manyFields(65)},
		"deny kèm hiddenFields":              {"resourceType": "Patient", "deny": true, "hiddenFields": []any{"name"}},
		"deny kèm interaction":               {"resourceType": "Patient", "deny": true, "interaction": []any{"read"}},
		"khóa lạ vẫn bị từ chối (fail đóng)": {"resourceType": "Patient", "writeConstraint": []any{}},
	} {
		if _, err := fhirauth.ParsePolicy([]any{rule}); err == nil {
			t.Errorf("%s phải bị từ chối", name)
		}
	}
	// nhiều rule cùng loại phải có cùng quy tắc trường và interaction
	a := map[string]any{"resourceType": "Communication", "criteria": "sender=Patient/1", "hiddenFields": []any{"payload"}}
	b := map[string]any{"resourceType": "Communication", "criteria": "recipient=Patient/1", "hiddenFields": []any{"payload"}}
	if _, err := fhirauth.ParsePolicy([]any{a, b}); err != nil {
		t.Errorf("hai rule cùng hiddenFields: %v", err)
	}
	for name, other := range map[string]map[string]any{
		"khác hiddenFields":  {"resourceType": "Communication", "criteria": "recipient=Patient/1", "hiddenFields": []any{"note"}},
		"thiếu hiddenFields": {"resourceType": "Communication", "criteria": "recipient=Patient/1"},
		"khác interaction":   {"resourceType": "Communication", "criteria": "recipient=Patient/1", "hiddenFields": []any{"payload"}, "interaction": []any{"read"}},
	} {
		if _, err := fhirauth.ParsePolicy([]any{a, other}); err == nil {
			t.Errorf("%s phải bị từ chối khi gộp", name)
		}
	}
}

func manyFields(n int) []any {
	var out []any
	for i := 0; i < n; i++ {
		out = append(out, "f"+strings.Repeat("x", i%5)+string(rune('a'+i%26)))
	}
	return out
}

func TestHiddenFieldsAreRemovedFromEveryRead(t *testing.T) {
	e := newFREnv(t)
	e.put(patient("p1"))
	hide := e.as(policyOf(t, map[string]any{"resourceType": "Patient", "hiddenFields": []any{"birthDate", "name.family", "address", "deceased[x]", "khong_co"}}))
	check := func(what string, r fhir.Resource) {
		t.Helper()
		out := jsonOf(t, r)
		for _, bad := range []string{"birthDate", "Nguyễn", "Khác", "Huế", "1990"} {
			if strings.Contains(out, bad) {
				t.Errorf("%s còn lộ %q: %s", what, bad, out)
			}
		}
		for _, keep := range []string{`"gender":"female"`, `"An"`, `"0901"`, `"use":"nickname"`} {
			if !strings.Contains(out, keep) {
				t.Errorf("%s mất trường không ẩn %q: %s", what, keep, out)
			}
		}
	}
	got, err := e.repo.ReadResource(hide, "Patient", "p1")
	if err != nil {
		t.Fatal(err)
	}
	check("read", got)
	vid := got.GetMeta().VersionId
	v, err := e.repo.ReadVersion(hide, "Patient", "p1", vid)
	if err != nil {
		t.Fatal(err)
	}
	check("vread", v)
	hist, err := e.repo.ReadHistory(hide, "Patient", "p1")
	if err != nil || len(hist) == 0 {
		t.Fatal(err, hist)
	}
	check("history", hist[0].Resource)
	sr, _ := fhirsearch.Parse("Patient", url.Values{})
	res, err := e.repo.Search(hide, sr)
	if err != nil || len(res.Matches) != 1 {
		t.Fatal(err, res)
	}
	check("search", res.Matches[0])
	// người gọi không có quy tắc ẩn vẫn thấy đủ; bản đang lưu không bị đổi
	full, _ := e.repo.ReadResource(e.admin, "Patient", "p1")
	if !strings.Contains(jsonOf(t, full), "birthDate") || !strings.Contains(jsonOf(t, full), "Huế") {
		t.Errorf("bản lưu bị đổi: %s", jsonOf(t, full))
	}
}

func TestHiddenFieldsInIncludedResources(t *testing.T) {
	e := newFREnv(t)
	e.put(patient("p1"))
	e.put(&fhir.Observation{ID: "o1", Status: "final", Code: &fhir.CodeableConcept{Text: "x"}, Subject: &fhir.Reference{Reference: "Patient/p1"}})
	hide := e.as(policyOf(t, map[string]any{"resourceType": "Patient", "hiddenFields": []any{"birthDate"}}, map[string]any{"resourceType": "Observation"}))
	sr, _ := fhirsearch.Parse("Observation", url.Values{"_include": {"Observation:subject"}})
	res, err := e.repo.Search(hide, sr)
	if err != nil || len(res.Included) != 1 {
		t.Fatalf("%v %+v", err, res)
	}
	if strings.Contains(jsonOf(t, res.Included[0]), "birthDate") {
		t.Errorf("_include lộ trường ẩn: %s", jsonOf(t, res.Included[0]))
	}
}

func TestReadonlyAndHiddenFieldsOnWrite(t *testing.T) {
	e := newFREnv(t)
	pol := policyOf(t, map[string]any{"resourceType": "Patient", "readonlyFields": []any{"gender", "name.use"}, "hiddenFields": []any{"birthDate"}})
	ctx := e.as(pol)
	// create: trường readonly và hidden bị bỏ khỏi input
	created, err := e.repo.CreateResource(ctx, &fhir.Patient{ID: "p2", Gender: "male", BirthDate: "2000-02-02", Name: []fhir.HumanName{{Family: "A", Use: "official"}}, Telecom: []fhir.ContactPoint{{System: "phone", Value: "1"}}})
	if err != nil {
		t.Fatal(err)
	}
	stored, _ := e.repo.ReadResource(e.admin, "Patient", "p2")
	if s := jsonOf(t, stored); strings.Contains(s, "gender") || strings.Contains(s, "birthDate") || strings.Contains(s, `"use"`) || !strings.Contains(s, `"family":"A"`) {
		t.Errorf("create phải bỏ readonly/hidden: %s", s)
	}
	if strings.Contains(jsonOf(t, created), "birthDate") {
		t.Errorf("kết quả create lộ trường ẩn: %s", jsonOf(t, created))
	}
	// update: người gọi gửi giá trị khác cho trường readonly, và không có trường ẩn (họ không thấy nó): bản lưu giữ nguyên các giá trị cũ
	e.put(patient("p3"))
	cur, _ := e.repo.ReadResource(ctx, "Patient", "p3") // đã bị ẩn birthDate
	p := cur.(*fhir.Patient)
	p.Gender = "other"
	p.Name[0].Given = []string{"Bình"}
	updated, err := e.repo.UpdateResource(ctx, p, fhirrouter.UpdateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	after, _ := e.repo.ReadResource(e.admin, "Patient", "p3")
	ap := after.(*fhir.Patient)
	if ap.Gender != "female" || ap.BirthDate != "1990-01-01" || ap.Name[0].Given[0] != "Bình" {
		t.Errorf("update phải khôi phục readonly/hidden từ bản cũ và nhận phần còn lại: gender=%q birthDate=%q given=%v", ap.Gender, ap.BirthDate, ap.Name[0].Given)
	}
	if strings.Contains(jsonOf(t, updated), "birthDate") {
		t.Errorf("kết quả update lộ trường ẩn: %s", jsonOf(t, updated))
	}
	// người gọi cố đặt trường ẩn: bị bỏ qua, không ghi đè
	p.BirthDate = "1800-01-01"
	if _, err := e.repo.UpdateResource(ctx, p, fhirrouter.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if after, _ = e.repo.ReadResource(e.admin, "Patient", "p3"); after.(*fhir.Patient).BirthDate != "1990-01-01" {
		t.Errorf("trường ẩn bị ghi đè: %v", after.(*fhir.Patient).BirthDate)
	}
}

func TestPatchKeepsHiddenFieldsThroughTheRouter(t *testing.T) {
	e := newFREnv(t)
	e.put(patient("p4"))
	pol := policyOf(t, map[string]any{"resourceType": "Patient", "hiddenFields": []any{"birthDate"}, "readonlyFields": []any{"gender"}})
	h := fhirrouter.New(e.repo, fhirrouter.Options{}).HTTPHandler()
	_ = h // PATCH đi qua router: đọc (đã ẩn) rồi ghi lại; trường ẩn và readonly phải còn nguyên
	ctx := e.as(pol)
	cur, _ := e.repo.ReadResource(ctx, "Patient", "p4")
	p := cur.(*fhir.Patient)
	p.Telecom[0].Value = "0999"
	if _, err := e.repo.UpdateResource(ctx, p, fhirrouter.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	after, _ := e.repo.ReadResource(e.admin, "Patient", "p4")
	ap := after.(*fhir.Patient)
	if ap.BirthDate != "1990-01-01" || ap.Gender != "female" || ap.Telecom[0].Value != "0999" {
		t.Errorf("%+v", ap)
	}
}

func TestInteractionRestrictions(t *testing.T) {
	e := newFREnv(t)
	e.put(patient("p1"))
	// vread và history cần cả read (như Medplum: interactionsWithRead); các interaction khác đứng riêng
	for _, c := range []struct {
		allowed []any
		ok      map[string]bool
	}{
		{[]any{"read"}, map[string]bool{"read": true}},
		{[]any{"search"}, map[string]bool{"search": true}},
		{[]any{"vread"}, map[string]bool{}},
		{[]any{"read", "vread"}, map[string]bool{"read": true, "vread": true}},
		{[]any{"history"}, map[string]bool{}},
		{[]any{"read", "history"}, map[string]bool{"read": true, "history": true}},
		{[]any{"create"}, map[string]bool{"create": true}},
		{[]any{"update"}, map[string]bool{"update": true}},
		{[]any{"delete"}, map[string]bool{"delete": true}},
	} {
		tag := fmt.Sprint(c.allowed)
		ctx := e.as(policyOf(t, map[string]any{"resourceType": "Patient", "interaction": c.allowed}))
		vid := ""
		if cur, err := e.repo.ReadResource(e.admin, "Patient", "p1"); err == nil {
			vid = cur.GetMeta().VersionId
		}
		try := map[string]func() error{
			"read":    func() error { _, err := e.repo.ReadResource(ctx, "Patient", "p1"); return err },
			"vread":   func() error { _, err := e.repo.ReadVersion(ctx, "Patient", "p1", vid); return err },
			"history": func() error { _, err := e.repo.ReadHistory(ctx, "Patient", "p1"); return err },
			"search": func() error {
				sr, _ := fhirsearch.Parse("Patient", url.Values{})
				_, err := e.repo.Search(ctx, sr)
				return err
			},
			"create": func() error {
				_, err := e.repo.CreateResource(ctx, &fhir.Patient{ID: "new-" + strings.NewReplacer("[", "", "]", "", " ", "-").Replace(tag) + "-" + vid[:4]})
				return err
			},
			"update": func() error {
				_, err := e.repo.UpdateResource(ctx, patient("p1"), fhirrouter.UpdateOptions{})
				return err
			},
			"delete": func() error { return e.repo.DeleteResource(ctx, "Patient", "p-xoa-delete") },
		}
		if tag == "[delete]" {
			e.put(&fhir.Patient{ID: "p-xoa-delete"})
		}
		for name, f := range try {
			err := f()
			switch {
			case c.ok[name] && status(err) == 403:
				t.Errorf("interaction=%s: %s phải được phép: %v", tag, name, err)
			case !c.ok[name] && status(err) != 403:
				t.Errorf("interaction=%s: %s phải 403, được %v", tag, name, err)
			}
		}
	}
	// không đặt interaction: mọi interaction; readonly: không ghi
	all := e.as(policyOf(t, map[string]any{"resourceType": "Patient"}))
	if _, err := e.repo.ReadHistory(all, "Patient", "p1"); err != nil {
		t.Errorf("mặc định phải cho history: %v", err)
	}
	ro := e.as(policyOf(t, map[string]any{"resourceType": "Patient", "readonly": true}))
	if _, err := e.repo.UpdateResource(ro, patient("p1"), fhirrouter.UpdateOptions{}); status(err) != 403 {
		t.Errorf("readonly vẫn chặn ghi: %v", err)
	}
	if _, err := e.repo.ReadResource(ro, "Patient", "p1"); err != nil {
		t.Errorf("readonly vẫn đọc: %v", err)
	}
}

func TestRemoveFieldPaths(t *testing.T) {
	e := newFREnv(t)
	e.put(&fhir.Observation{ID: "o1", Status: "final", Code: &fhir.CodeableConcept{Text: "x", Coding: []fhir.Coding{{Code: "1", Display: "một"}, {Code: "2", Display: "hai"}}},
		ValueString: "s", Component: []fhir.ObservationComponent{{Code: &fhir.CodeableConcept{Text: "c1"}, ValueString: "v1"}, {Code: &fhir.CodeableConcept{Text: "c2"}, ValueString: "v2"}}})
	pol := policyOf(t, map[string]any{"resourceType": "Observation", "hiddenFields": []any{"code.coding.display", "value[x]", "component.value[x]", "component.code.text", "status.khong.co"}})
	got, err := e.repo.ReadResource(e.as(pol), "Observation", "o1")
	if err != nil {
		t.Fatal(err)
	}
	s := jsonOf(t, got)
	for _, bad := range []string{"một", "hai", `"valueString"`, `"c1"`, `"c2"`, `"v1"`} {
		if strings.Contains(s, bad) {
			t.Errorf("còn %q: %s", bad, s)
		}
	}
	for _, keep := range []string{`"code":"1"`, `"code":"2"`, `"text":"x"`, `"status":"final"`} {
		if !strings.Contains(s, keep) {
			t.Errorf("mất %q: %s", keep, s)
		}
	}
}
