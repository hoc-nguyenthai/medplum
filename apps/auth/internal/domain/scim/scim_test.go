package scim

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func tr(b bool) *bool     { return &b }
func sp(s string) *string { return &s }

func base() User {
	return User{Schemas: []string{SchemaUser}, ID: "m1", UserName: "alice", UserType: "Practitioner", Name: &Name{GivenName: "A", FamilyName: "L"},
		Emails: []Email{{Value: "a@x.org"}}, Active: tr(true), ExternalID: sp("e1")}
}

func op(schemas bool, ops ...PatchOperation) PatchOp {
	p := PatchOp{Operations: ops}
	if schemas {
		p.Schemas = []string{SchemaPatch}
	}
	return p
}

func raw(s string) json.RawMessage { return json.RawMessage(s) }

func wantProblem(t *testing.T, err error, scimType, detailSub string) {
	t.Helper()
	var pe *ProblemError
	if !errors.As(err, &pe) || pe.Status != 400 || pe.ScimType != scimType || !strings.Contains(pe.Detail, detailSub) {
		t.Fatalf("mong 400 %s chứa %q, được %#v", scimType, detailSub, err)
	}
}

func TestFilter(t *testing.T) {
	for in, want := range map[string]string{
		`userName eq "Alice@Atko.com"`: "alice@atko.com", `USERNAME EQ "x"`: "x", `  userName   eq   "a b"  `: "a b", `userName eq "a\"b"`: `a"b`, `userName eq "a\\b"`: `a\b`,
		`userName eq "a,b"`: "a,b", `userName eq "a|b"`: "a|b", `userName eq "a$b"`: "a$b",
	} {
		v, has, err := ParseFilter(in)
		if err != nil || !has || v != want {
			t.Errorf("%q: %q %v %v (mong %q)", in, v, has, err, want)
		}
	}
	if v, has, err := ParseFilter("  "); err != nil || has || v != "" {
		t.Errorf("rỗng: %q %v %v", v, has, err)
	}
	for _, bad := range []string{`userName ne "x"`, `userName eq x`, `userName eq ""`, `emails.value eq "x"`, `userName eq "x" and active eq true`, `userName eq "a"b"`, `userName eq "a\n"`, `userName eq`, `foo`, `userName co "x"`, `userName eq "x`} {
		if _, _, err := ParseFilter(bad); err == nil {
			t.Errorf("%q phải bị từ chối", bad)
		} else {
			wantProblem(t, err, "invalidFilter", "Unsupported filter")
		}
	}
}

func TestNormalizeUserType(t *testing.T) {
	for in, want := range map[string]string{"Patient": "Patient", "RelatedPerson": "RelatedPerson", "Practitioner": "Practitioner", "": "Practitioner", "Bot": "Practitioner", "patient": "Practitioner"} {
		if got := NormalizeUserType(in); got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
}

func TestPatchOktaAndPathForms(t *testing.T) {
	// Okta: replace không path, value object
	got, err := ApplyPatch(base(), op(true, PatchOperation{Op: "replace", Value: raw(`{"active":false}`)}), 50)
	if err != nil || *got.Active {
		t.Fatalf("okta: %+v %v", got, err)
	}
	// op không phân biệt hoa thường; path không phân biệt hoa thường
	got, err = ApplyPatch(base(), op(true, PatchOperation{Op: "Replace", Path: "ACTIVE", Value: raw(`false`)}), 50)
	if err != nil || *got.Active {
		t.Fatalf("path hoa: %+v %v", got, err)
	}
	got, err = ApplyPatch(base(), op(true,
		PatchOperation{Op: "replace", Path: "name.givenName", Value: raw(`"B"`)},
		PatchOperation{Op: "replace", Path: "name.familyName", Value: raw(`"M"`)},
		PatchOperation{Op: "replace", Path: "externalId", Value: raw(`"e2"`)},
		PatchOperation{Op: "add", Path: "emails", Value: raw(`[{"value":"n@x.org"}]`)}), 50)
	if err != nil || got.Name.GivenName != "B" || got.Name.FamilyName != "M" || *got.ExternalID != "e2" || len(got.Emails) != 1 || got.Emails[0].Value != "n@x.org" {
		t.Fatalf("nhiều thao tác: %+v %v", got, err)
	}
	// name nguyên khối và remove
	got, err = ApplyPatch(base(), op(true, PatchOperation{Op: "replace", Path: "name", Value: raw(`{"givenName":"Z"}`)}), 50)
	if err != nil || got.Name.GivenName != "Z" || got.Name.FamilyName != "" {
		t.Fatalf("name: %+v %v", got, err)
	}
	got, err = ApplyPatch(base(), op(true, PatchOperation{Op: "remove", Path: "externalId"}, PatchOperation{Op: "remove", Path: "name.givenName"}), 50)
	if err != nil || *got.ExternalID != "" || got.Name.GivenName != "" || got.Name.FamilyName != "L" {
		t.Fatalf("remove: %+v %v", got, err)
	}
	// không sửa bản gốc (bản sao)
	orig := base()
	if _, err := ApplyPatch(orig, op(true, PatchOperation{Op: "replace", Path: "name.givenName", Value: raw(`"X"`)}, PatchOperation{Op: "replace", Path: "emails", Value: raw(`[{"value":"q@x.org"}]`)}), 50); err != nil {
		t.Fatal(err)
	}
	if orig.Name.GivenName != "A" || orig.Emails[0].Value != "a@x.org" {
		t.Errorf("bản gốc bị đổi: %+v", orig)
	}
}

func TestPatchIgnoresBenignAttributes(t *testing.T) {
	got, err := ApplyPatch(base(), op(true,
		PatchOperation{Op: "replace", Path: "displayName", Value: raw(`"X"`)},
		PatchOperation{Op: "replace", Path: "userName", Value: raw(`"bob"`)},
		PatchOperation{Op: "replace", Path: "userType", Value: raw(`"Patient"`)},
		PatchOperation{Op: "replace", Path: "name.formatted", Value: raw(`"F"`)},
		PatchOperation{Op: "replace", Value: raw(`{"phoneNumbers":[],"title":"t","nickName":"n"}`)}), 50)
	if err != nil || got.UserName != "alice" || got.UserType != "Practitioner" || got.Name.GivenName != "A" {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestPatchRejections(t *testing.T) {
	cases := map[string]struct {
		p       PatchOp
		typ, in string
	}{
		"thiếu schema":        {op(false, PatchOperation{Op: "replace", Value: raw(`{"active":false}`)}), "invalidSyntax", "missing required schema"},
		"schema sai":          {PatchOp{Schemas: []string{"x"}}, "invalidSyntax", "missing required schema"},
		"op lạ":               {op(true, PatchOperation{Op: "move", Path: "active"}), "invalidSyntax", "unsupported operation"},
		"path bắt đầu /":      {op(true, PatchOperation{Op: "replace", Path: "/active", Value: raw(`false`)}), "invalidPath", `path must not start with "/"`},
		"remove không path":   {op(true, PatchOperation{Op: "remove"}), "noTarget", "missing required path"},
		"value không object":  {op(true, PatchOperation{Op: "add", Value: raw(`true`)}), "invalidValue", "value must be an object"},
		"value mảng":          {op(true, PatchOperation{Op: "add", Value: raw(`[1]`)}), "invalidValue", "value must be an object"},
		"value rỗng":          {op(true, PatchOperation{Op: "add", Value: raw(`{}`)}), "invalidValue", "value must be an object"},
		"không value":         {op(true, PatchOperation{Op: "add"}), "invalidValue", "value must be an object"},
		"quá nhiều thao tác":  {op(true, make([]PatchOperation, 3)...), "tooMany", "too many operations"},
		"active sai kiểu":     {op(true, PatchOperation{Op: "replace", Path: "active", Value: raw(`"no"`)}), "invalidValue", "active must be a boolean"},
		"xóa active":          {op(true, PatchOperation{Op: "remove", Path: "active"}), "mutability", "active cannot be removed"},
		"xóa emails":          {op(true, PatchOperation{Op: "remove", Path: "emails"}), "mutability", "emails cannot be removed"},
		"emails rỗng":         {op(true, PatchOperation{Op: "replace", Path: "emails", Value: raw(`[]`)}), "invalidValue", "non-empty"},
		"emails thiếu value":  {op(true, PatchOperation{Op: "replace", Path: "emails", Value: raw(`[{"value":""}]`)}), "invalidValue", "non-empty"},
		"externalId sai kiểu": {op(true, PatchOperation{Op: "replace", Path: "externalId", Value: raw(`5`)}), "invalidValue", "externalId must be a string"},
		"name sai kiểu":       {op(true, PatchOperation{Op: "replace", Path: "name", Value: raw(`"x"`)}), "invalidValue", "name must be an object"},
		"givenName sai kiểu":  {op(true, PatchOperation{Op: "replace", Path: "name.givenName", Value: raw(`5`)}), "invalidValue", "must be a string"},
	}
	for name, tc := range cases {
		_, err := ApplyPatch(base(), tc.p, 2)
		if err == nil {
			t.Errorf("%s: phải lỗi", name)
			continue
		}
		wantProblem(t, err, tc.typ, tc.in)
	}
	// các đường dẫn nguy hiểm hoặc lạ: invalidPath, kể cả dạng không path
	for _, path := range []string{"admin", "accessPolicy", "access", "profile", "project", "user", "meta", "id", "groups", "roles", "schemas", "urn:ext:enterprise:2.0:User:manager", "name.unknown", "emails.value", "foo"} {
		_, err := ApplyPatch(base(), op(true, PatchOperation{Op: "replace", Path: path, Value: raw(`true`)}), 50)
		wantProblem(t, err, "invalidPath", "unsupported path")
		_, err = ApplyPatch(base(), op(true, PatchOperation{Op: "replace", Value: raw(`{"` + path + `":true}`)}), 50)
		wantProblem(t, err, "invalidPath", "unsupported path")
	}
	// maxOps 0 là không giới hạn
	if _, err := ApplyPatch(base(), op(true, PatchOperation{Op: "replace", Path: "active", Value: raw(`true`)}), 0); err != nil {
		t.Error(err)
	}
}

func TestUserJSONNeverContainsSecrets(t *testing.T) {
	b, _ := json.Marshal(base())
	for _, bad := range []string{"passwordHash", "mfaSecret", "secret", "password"} {
		if strings.Contains(string(b), bad) {
			t.Errorf("lộ %s: %s", bad, b)
		}
	}
	// body có trường lạ khi decode vào User: bị bỏ qua
	var u User
	if err := json.Unmarshal([]byte(`{"userName":"x","admin":true,"accessPolicy":{"reference":"AccessPolicy/1"},"groups":[{"value":"g"}],"meta":{"resourceType":"Hack"}}`), &u); err != nil {
		t.Fatal(err)
	}
	if u.UserName != "x" || u.Meta == nil || u.Meta.ResourceType != "Hack" { // meta được decode nhưng application không dùng
		t.Errorf("%+v", u)
	}
}
