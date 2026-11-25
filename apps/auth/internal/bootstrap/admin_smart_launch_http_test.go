package bootstrap

import (
	"context"
	"net/url"
	"testing"

	"trivita-fhir/apps/auth/internal/config"
)

func TestHTTPAdminSmartLaunch(t *testing.T) {
	a := newCodeAPI(t, func(s *config.Settings) { s.AdminEnabled, s.FHIRBaseURL = true, "https://fhir.example.org/fhir/R4" })
	hdr := adminBearer(a)
	base := "/admin/projects/" + a.seed.Project.ID
	created := a.do("POST", base+"/client", `{"name":"EHR app","grantType":["authorization_code"],"redirectUris":["https://app.example.org/cb"]}`, hdr)
	if created.status != 201 {
		t.Fatalf("tạo client: %d %s", created.status, created.body)
	}
	cid := created.json()["id"].(string)
	path := base + "/client/" + cid + "/smart-launch"
	body := `{"patient":"Patient/p1","encounter":"Encounter/e1"}`
	if r := a.do("POST", path, body, hdr); r.status != 400 { // chưa có launchUri
		t.Fatalf("thiếu launchUri: %d %s", r.status, r.body)
	}
	if r := a.do("POST", base+"/client", `{"name":"x","launchUri":"ftp://x.example/l"}`, hdr); r.status != 400 {
		t.Fatalf("launchUri sai khi tạo client: %d %s", r.status, r.body)
	}
	withURI := a.do("POST", base+"/client", `{"name":"EHR app 2","grantType":["authorization_code"],"launchUri":"https://app.example.org/launch"}`, hdr)
	if withURI.status != 201 || withURI.json()["launchUri"] != "https://app.example.org/launch" {
		t.Fatalf("tạo client có launchUri: %d %s", withURI.status, withURI.body)
	}
	cid = withURI.json()["id"].(string)
	path = base + "/client/" + cid + "/smart-launch"
	r := a.do("POST", path, body, hdr)
	if r.status != 201 || r.hdr.Get("Cache-Control") != "no-store" {
		t.Fatalf("tạo launch: %d %s", r.status, r.body)
	}
	id := r.json()["launchId"].(string)
	u, err := url.Parse(r.json()["launchUrl"].(string))
	if err != nil || u.Query().Get("launch") != id || u.Query().Get("iss") != "https://fhir.example.org/fhir/R4" || u.Path != "/launch" {
		t.Fatalf("launchUrl: %s", r.body)
	}
	l, err := a.c.Auth.Launches.Get(context.Background(), id)
	if err != nil || l.Patient.Reference != "Patient/p1" || l.Encounter.Reference != "Encounter/e1" {
		t.Fatalf("launch lưu: %+v %v", l, err)
	}
	for name, b := range map[string]string{
		"rác": `x`, "không phải object": `[]`, "trường lạ": `{"patient":"Patient/p1","x":1}`, "sai kiểu": `{"patient":5}`,
		"rỗng": `{}`, "sai loại": `{"patient":"Encounter/p1"}`,
	} {
		if r := a.do("POST", path, b, hdr); r.status != 400 {
			t.Errorf("%s: %d %s", name, r.status, r.body)
		}
	}
	if r := a.do("POST", path, body, nil); r.status != 401 {
		t.Errorf("không Bearer: %d", r.status)
	}
	if r := a.do("POST", base+"/client/khong-co/smart-launch", body, hdr); r.status != 403 {
		t.Errorf("client lạ: %d", r.status)
	}
	if r := a.do("POST", "/admin/projects/khac/client/"+cid+"/smart-launch", body, hdr); r.status != 403 {
		t.Errorf("project lệch: %d", r.status)
	}
}
