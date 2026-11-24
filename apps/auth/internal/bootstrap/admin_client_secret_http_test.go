package bootstrap

import (
	"net/url"
	"strings"
	"testing"
)

func clientToken(a *codeAPI, id, secret string) int {
	return a.form("/oauth2/token", url.Values{"grant_type": {"client_credentials"}, "client_id": {id}, "client_secret": {secret}}, nil).status
}

// Xoay secret qua HTTP: secret cũ và mới cùng dùng được cho tới khi retire; sau retire chỉ secret mới; lỗi theo hợp đồng.
func TestHTTPAdminRotateClientSecret(t *testing.T) {
	a, hdr := newAdminAPI(t)
	base := "/admin/projects/" + a.seed.Project.ID
	created := a.do("POST", base+"/client", `{"name":"Máy đồng bộ","grantType":["client_credentials"]}`, hdr)
	if created.status != 201 {
		t.Fatalf("tạo client: %d %s", created.status, created.body)
	}
	cid, oldSecret := created.json()["id"].(string), created.json()["secret"].(string)
	path := base + "/client/" + cid + "/rotate-secret"
	if clientToken(a, cid, oldSecret) != 200 {
		t.Fatal("secret ban đầu phải dùng được")
	}

	r := a.do("POST", path, "", hdr) // không body
	if r.status != 200 || r.hdr.Get("Cache-Control") != "no-store" {
		t.Fatalf("xoay: %d %s %q", r.status, r.body, r.hdr.Get("Cache-Control"))
	}
	m := r.json()
	newSecret, _ := m["clientSecret"].(string)
	if m["clientId"] != cid || m["retiring"] != true || len(newSecret) != 64 || newSecret == oldSecret {
		t.Fatalf("phản hồi: %s", r.body)
	}
	if clientToken(a, cid, oldSecret) != 200 || clientToken(a, cid, newSecret) != 200 {
		t.Fatal("trong lúc xoay cả hai secret phải dùng được")
	}

	if r := a.do("POST", path, `{"retireOnly":true}`, hdr); r.status != 200 || r.json()["retiring"] != false || r.json()["clientSecret"] != nil {
		t.Fatalf("retireOnly: %d %s", r.status, r.body)
	}
	if clientToken(a, cid, oldSecret) == 200 || clientToken(a, cid, newSecret) != 200 {
		t.Fatal("sau retire chỉ secret mới dùng được")
	}

	mine := strings.Repeat("k", 40)
	if r := a.do("POST", path, `{"secret":"`+mine+`"}`, hdr); r.status != 200 || r.json()["clientSecret"] != mine {
		t.Fatalf("secret tự đặt: %d %s", r.status, r.body)
	}
	for name, body := range map[string]string{
		"không phải JSON": `x`,
		"trường lạ":       `{"retiringSecret":"abc"}`,
		"sai kiểu":        `{"retireOnly":"yes"}`,
		"secret ngắn":     `{"secret":"ngan"}`,
		"loại trừ":        `{"secret":"` + mine + `x","retireOnly":true}`,
	} {
		if r := a.do("POST", path, body, hdr); r.status != 400 {
			t.Errorf("%s: %d %s", name, r.status, r.body)
		}
	}
	if r := a.do("POST", path, "", nil); r.status != 401 {
		t.Errorf("không Bearer: %d", r.status)
	}
	if r := a.do("POST", base+"/client/khong-co/rotate-secret", "", hdr); r.status != 403 {
		t.Errorf("client không có: %d %s", r.status, r.body)
	}
	if r := a.do("POST", "/admin/projects/khac/client/"+cid+"/rotate-secret", "", hdr); r.status != 403 {
		t.Errorf("project lệch: %d %s", r.status, r.body)
	}
	// client_credentials không phải admin
	cc := map[string]string{"Authorization": "Bearer " + a.form("/oauth2/token", url.Values{"grant_type": {"client_credentials"}, "client_id": {a.seed.Client.ID}, "client_secret": {a.seed.Client.Secret}}, nil).json()["access_token"].(string)}
	if r := a.do("POST", path, "", cc); r.status != 403 {
		t.Errorf("không phải admin: %d", r.status)
	}
}
