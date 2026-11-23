package database

import (
	"strings"
	"testing"
)

func TestOwnSchema(t *testing.T) {
	for in, want := range map[string]string{
		"identity":        "identity",
		" fhir ":          "fhir",
		`"fhir"`:          "fhir",
		`"Identity-Svc"`:  "Identity-Svc",
		"t_ab12cd":        "t_ab12cd",
		`  "my schema"  `: "my schema",
	} {
		got, err := ownSchema(in)
		if err != nil || got != want {
			t.Errorf("ownSchema(%q) = %q, %v; muốn %q", in, got, err, want)
		}
	}
	for in, wantMsg := range map[string]string{
		"":                   "rỗng",
		" , ":                "rỗng",
		"public":             "không phải schema riêng",
		`"public"`:           "không phải schema riêng",
		`"$user"`:            "không phải schema riêng",
		`"$user", public`:    "nhiều schema",
		"identity, public":   "nhiều schema",
		"identity,fhir":      "nhiều schema",
		"pg_catalog":         "không phải schema riêng",
		"information_schema": "không phải schema riêng",
		"$user":              "không phải schema riêng",
	} {
		if got, err := ownSchema(in); err == nil || !strings.Contains(err.Error(), wantMsg) {
			t.Errorf("ownSchema(%q) = %q, %v; muốn lỗi chứa %q", in, got, err, wantMsg)
		}
	}
}
