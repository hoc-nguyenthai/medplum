package bootstrap

import (
	"strings"
	"testing"
)

// Ghi secret của project qua HTTP: giá trị không bao giờ được đọc lại, giá trị che giữ nguyên secret, lỗi theo hợp đồng.
func TestHTTPAdminProjectSecrets(t *testing.T) {
	a, hdr := newAdminAPI(t)
	path := "/admin/projects/" + a.seed.Project.ID
	r := a.do("POST", path+"/secrets", `[{"name":"OPENAI_KEY","valueString":"sk-rat-bi-mat"},{"name":"retries","valueInteger":3}]`, hdr)
	if r.status != 200 || r.hdr.Get("Cache-Control") != "no-store" || strings.Contains(r.body, "sk-rat-bi-mat") {
		t.Fatalf("ghi: %d %s %q", r.status, r.body, r.hdr.Get("Cache-Control"))
	}
	got := a.do("GET", path, "", hdr)
	if strings.Contains(got.body, "sk-rat-bi-mat") || len(got.json()["project"].(map[string]any)["secret"].([]any)) != 2 {
		t.Fatalf("đọc: %s", got.body)
	}
	// giá trị che giữ nguyên secret cùng tên; secret không nêu bị xóa
	if r := a.do("POST", path+"/secrets", `[{"name":"OPENAI_KEY","valueString":"********"}]`, hdr); r.status != 200 {
		t.Fatalf("giữ: %d %s", r.status, r.body)
	}
	p, err := a.c.Auth.Projects.Get(t.Context(), a.seed.Project.ID)
	if err != nil || len(p.Secret) != 1 || p.Secret[0].ValueString != "sk-rat-bi-mat" {
		t.Fatalf("lưu: %+v %v", p.Secret, err)
	}
	for name, body := range map[string]string{
		"không phải mảng": `{"name":"x"}`,
		"rác":             `x`,
		"null":            `null`,
		"tên sai":         `[{"name":"a b","valueString":"v"}]`,
		"smtp khi tắt":    `[{"name":"smtpHost","valueString":"h"}]`,
		"giữ khi chưa có": `[{"name":"moi","valueString":"********"}]`,
	} {
		if r := a.do("POST", path+"/secrets", body, hdr); r.status != 400 {
			t.Errorf("%s: %d %s", name, r.status, r.body)
		}
	}
	if r := a.do("POST", path+"/secrets", `[]`, nil); r.status != 401 {
		t.Errorf("không Bearer: %d", r.status)
	}
	if r := a.do("POST", "/admin/projects/khac/secrets", `[]`, hdr); r.status != 403 {
		t.Errorf("project lệch: %d", r.status)
	}
}
