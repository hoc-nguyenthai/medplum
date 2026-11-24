package fhirrouter_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"trivita-fhir/pkg/fhirrouter"
	"trivita-fhir/pkg/fhirsearch"
)

const routerProfile = `{"resourceType":"StructureDefinition","id":"vn-patient","url":"http://example.org/StructureDefinition/vn-patient",
 "name":"VNPatient","status":"active","kind":"resource","abstract":false,"type":"Patient",
 "snapshot":{"element":[
  {"id":"Patient","path":"Patient"},
  {"id":"Patient.birthDate","path":"Patient.birthDate","min":1,"max":"1"},
  {"id":"Patient.identifier","path":"Patient.identifier","min":1,"max":"*",
   "slicing":{"discriminator":[{"type":"value","path":"system"}],"rules":"open"}},
  {"id":"Patient.identifier:cccd","path":"Patient.identifier","sliceName":"cccd","min":1,"max":"1"},
  {"id":"Patient.identifier:cccd.system","path":"Patient.identifier.system","min":1,"fixedUri":"http://vn.example/cccd"}
 ]}}`

func profileRouter(t *testing.T, opts fhirrouter.Options) http.Handler {
	t.Helper()
	repo := fhirrouter.NewMemoryRepository()
	opts.UpdateAsCreate = true
	// nạp profile qua router không kiểm (bản rút gọn không đủ định nghĩa/base cho sdf-3, sdf-8b)
	seed := fhirrouter.New(repo, fhirrouter.Options{UpdateAsCreate: true, SkipValidation: true}).HTTPHandler()
	h := fhirrouter.New(repo, opts).HTTPHandler()
	if code, out := putTo(seed, "PUT", "StructureDefinition/vn-patient", routerProfile); code != 200 && code != 201 {
		t.Fatalf("nạp profile: %d %s", code, out)
	}
	return h
}

const profiled = `{"resourceType":"Patient","meta":{"profile":["http://example.org/StructureDefinition/vn-patient"]}%s}`

func TestWriteValidatesAgainstProfileWhenEnabled(t *testing.T) {
	h := profileRouter(t, fhirrouter.Options{ValidateProfiles: true})
	good := strings.Replace(profiled, "%s", `,"birthDate":"1990-01-01","identifier":[{"system":"http://vn.example/cccd","value":"1"}]`, 1)
	if code, out := putTo(h, "POST", "Patient", good); code != 201 {
		t.Fatalf("hợp lệ phải 201: %d %s", code, out)
	}
	// thiếu birthDate, thiếu slice cccd: 400 kèm tên profile
	bad := strings.Replace(profiled, "%s", `,"identifier":[{"system":"http://other","value":"1"}]`, 1)
	for _, c := range []struct{ method, path string }{{"POST", "Patient"}, {"PUT", "Patient/x1"}} {
		code, out := putTo(h, c.method, c.path, bad)
		if code != 400 || !strings.Contains(out, "vn-patient") || !strings.Contains(out, "cccd") {
			t.Fatalf("%s %s: %d %s", c.method, c.path, code, out)
		}
	}
	// $validate cũng áp profile
	if code, out := putTo(h, "POST", "Patient/$validate", bad); code != 400 || !strings.Contains(out, "birthDate") {
		t.Fatalf("$validate: %d %s", code, out)
	}
	// batch: entry hỏng làm cả bundle bị từ chối bằng 400 hoặc entry 400
	bundle := `{"resourceType":"Bundle","type":"batch","entry":[{"request":{"method":"POST","url":"Patient"},"resource":` + bad + `}]}`
	if code, out := putTo(h, "POST", "", bundle); code != 200 || !strings.Contains(out, `"status":"400`) {
		t.Fatalf("batch: %d %s", code, out)
	}
}

func TestWriteIgnoresProfileWhenDisabled(t *testing.T) {
	h := profileRouter(t, fhirrouter.Options{})
	bad := strings.Replace(profiled, "%s", ``, 1)
	if code, out := putTo(h, "POST", "Patient", bad); code != 201 {
		t.Fatalf("mặc định không kiểm profile: %d %s", code, out)
	}
}

func TestUnknownProfileWarnsOrRejects(t *testing.T) {
	doc := `{"resourceType":"Patient","meta":{"profile":["http://example.org/none"]}}`
	h := profileRouter(t, fhirrouter.Options{ValidateProfiles: true})
	if code, out := putTo(h, "POST", "Patient", doc); code != 201 {
		t.Fatalf("profile lạ chỉ cảnh báo: %d %s", code, out)
	}
	h = profileRouter(t, fhirrouter.Options{ValidateProfiles: true, ProfileUnknownError: true})
	if code, out := putTo(h, "POST", "Patient", doc); code != 400 || !strings.Contains(out, "Không tìm thấy profile") {
		t.Fatalf("ProfileUnknownError phải 400: %d %s", code, out)
	}
}

// failingSearch làm mọi tìm kiếm StructureDefinition lỗi như kho hỏng.
type failingSearch struct{ fhirrouter.Repository }

func (f failingSearch) Search(ctx context.Context, sr *fhirsearch.Request) (*fhirrouter.SearchResult, error) {
	if sr.ResourceType == "StructureDefinition" {
		return nil, errors.New("kho hỏng")
	}
	return f.Repository.Search(ctx, sr)
}

// Lỗi kho khi tra profile không được biến thành "profile không tồn tại" (kể cả khi ProfileUnknownError bật): chỉ cảnh báo, không chặn ghi.
func TestProfileLookupFailureIsWarningNotUnknown(t *testing.T) {
	repo := failingSearch{fhirrouter.NewMemoryRepository()}
	h := fhirrouter.New(repo, fhirrouter.Options{UpdateAsCreate: true, ValidateProfiles: true, ProfileUnknownError: true}).HTTPHandler()
	doc := strings.Replace(profiled, "%s", "", 1)
	if code, out := putTo(h, "POST", "Patient", doc); code != 201 {
		t.Fatalf("lỗi kho phải chỉ là cảnh báo: %d %s", code, out)
	}
}
