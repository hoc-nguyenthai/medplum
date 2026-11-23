package fhirrouter_test

import (
	"context"
	"encoding/base64"
	"net/http"
	"strings"
	"testing"

	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirrouter"
)

const installTx = `{"resourceType":"Bundle","type":"transaction","entry":[
 {"resource":{"resourceType":"ValueSet","id":"vs1","url":"http://example.org/vs1","status":"active"},"request":{"method":"PUT","url":"ValueSet/vs1"}},
 {"resource":{"resourceType":"CodeSystem","id":"cs1","url":"http://example.org/cs1","status":"active","content":"complete"},"request":{"method":"PUT","url":"CodeSystem/cs1"}}]}`

// installFixture lưu Binary chứa Bundle và một PackageRelease trỏ tới nó.
func installFixture(t *testing.T, repo fhirrouter.Repository, content string) {
	t.Helper()
	ctx := context.Background()
	if _, err := repo.CreateResource(ctx, &fhir.Binary{ID: "pkgbin", ContentType: "application/fhir+json", Data: base64.StdEncoding.EncodeToString([]byte(content))}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateResource(ctx, &fhir.PackageRelease{ID: "rel1", Version: "1.0.0", Package: &fhir.Reference{Reference: "Package/p1"},
		Content: &fhir.Attachment{URL: "Binary/pkgbin"}}); err != nil {
		t.Fatal(err)
	}
}

func installations(t *testing.T, repo fhirrouter.Repository) []string {
	t.Helper()
	var out []string
	h := fhirrouter.New(repo, fhirrouter.Options{UpdateAsCreate: true}).HTTPHandler()
	code, body := putTo(h, "GET", "PackageInstallation", "")
	if code != 200 {
		t.Fatalf("search PackageInstallation: %d %s", code, body)
	}
	for _, part := range strings.Split(body, `"status":"`)[1:] {
		if strings.HasPrefix(part, "installed") {
			out = append(out, "installed")
		} else if strings.HasPrefix(part, "error") {
			out = append(out, "error")
		} else if strings.HasPrefix(part, "installing") {
			out = append(out, "installing")
		}
	}
	return out
}

func TestPackageInstallRunsBundleAndRecordsInstallation(t *testing.T) {
	repo := fhirrouter.NewMemoryRepository()
	installFixture(t, repo, installTx)
	h := fhirrouter.New(repo, fhirrouter.Options{UpdateAsCreate: true}).HTTPHandler()

	if code, body := putTo(h, "POST", "PackageRelease/rel1/$install", ""); code != 200 {
		t.Fatalf("install: %d %s", code, body)
	}
	for _, p := range []string{"ValueSet/vs1", "CodeSystem/cs1"} {
		if code, body := putTo(h, "GET", p, ""); code != 200 {
			t.Fatalf("%s sau khi cài: %d %s", p, code, body)
		}
	}
	if got := installations(t, repo); len(got) != 1 || got[0] != "installed" {
		t.Fatalf("PackageInstallation: %v", got)
	}
	// cài lại là idempotent với PUT và ghi thêm một PackageInstallation
	if code, body := putTo(h, "POST", "PackageRelease/rel1/$install", ""); code != 200 {
		t.Fatalf("cài lại: %d %s", code, body)
	}
	if got := installations(t, repo); len(got) != 2 {
		t.Fatalf("sau cài lại: %v", got)
	}
}

func TestPackageInstallFailedEntryMarksErrorAndTransactionRollsBack(t *testing.T) {
	bad := strings.Replace(installTx, `]}`, `,{"request":{"method":"GET","url":"Patient/khong-co"}}]}`, 1)
	repo := fhirrouter.NewMemoryRepository()
	installFixture(t, repo, bad)
	h := fhirrouter.New(repo, fhirrouter.Options{UpdateAsCreate: true}).HTTPHandler()

	code, body := putTo(h, "POST", "PackageRelease/rel1/$install", "")
	if code < 400 {
		t.Fatalf("entry lỗi phải làm hỏng cả lần cài: %d %s", code, body)
	}
	if c, _ := putTo(h, "GET", "ValueSet/vs1", ""); c != 404 {
		t.Fatalf("transaction phải hoàn tác, ValueSet/vs1 trả %d", c)
	}
	if got := installations(t, repo); len(got) != 1 || got[0] != "error" {
		t.Fatalf("PackageInstallation phải ở trạng thái error: %v", got)
	}
}

func TestPackageInstallBatchFailureStillReportsError(t *testing.T) {
	bad := strings.Replace(strings.Replace(installTx, `"type":"transaction"`, `"type":"batch"`, 1), `]}`, `,{"request":{"method":"GET","url":"Patient/khong-co"}}]}`, 1)
	repo := fhirrouter.NewMemoryRepository()
	installFixture(t, repo, bad)
	h := fhirrouter.New(repo, fhirrouter.Options{UpdateAsCreate: true}).HTTPHandler()
	if code, body := putTo(h, "POST", "PackageRelease/rel1/$install", ""); code < 400 {
		t.Fatalf("batch có entry lỗi phải báo lỗi: %d %s", code, body)
	}
	if got := installations(t, repo); len(got) != 1 || got[0] != "error" {
		t.Fatalf("PackageInstallation: %v", got)
	}
}

func TestPackageInstallRejectsBadPackages(t *testing.T) {
	cases := map[string]string{
		"không phải Bundle": `{"resourceType":"Patient"}`,
		"không phải JSON":   `xin chao`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			repo := fhirrouter.NewMemoryRepository()
			installFixture(t, repo, content)
			h := fhirrouter.New(repo, fhirrouter.Options{UpdateAsCreate: true}).HTTPHandler()
			if code, body := putTo(h, "POST", "PackageRelease/rel1/$install", ""); code != 400 {
				t.Fatalf("%d %s", code, body)
			}
			if got := installations(t, repo); len(got) != 0 {
				t.Fatalf("không được ghi PackageInstallation khi gói hỏng: %v", got)
			}
		})
	}
	t.Run("content.url sai dạng", func(t *testing.T) {
		repo := fhirrouter.NewMemoryRepository()
		_, _ = repo.CreateResource(context.Background(), &fhir.PackageRelease{ID: "rel1", Content: &fhir.Attachment{URL: "http://evil.example/p.json"}})
		h := fhirrouter.New(repo, fhirrouter.Options{UpdateAsCreate: true}).HTTPHandler()
		if code, _ := putTo(h, "POST", "PackageRelease/rel1/$install", ""); code != 400 {
			t.Fatalf("url ngoài phải bị từ chối: %d", code)
		}
	})
	t.Run("content.url trỏ loại khác", func(t *testing.T) {
		repo := fhirrouter.NewMemoryRepository()
		_, _ = repo.CreateResource(context.Background(), &fhir.PackageRelease{ID: "rel1", Content: &fhir.Attachment{URL: "Observation-abc123"}})
		h := fhirrouter.New(repo, fhirrouter.Options{UpdateAsCreate: true}).HTTPHandler()
		if code, _ := putTo(h, "POST", "PackageRelease/rel1/$install", ""); code != 400 {
			t.Fatalf("Observation-abc123 không phải Binary, phải 400: %d", code)
		}
	})
	t.Run("không có PackageRelease", func(t *testing.T) {
		h := fhirrouter.New(fhirrouter.NewMemoryRepository(), fhirrouter.Options{UpdateAsCreate: true}).HTTPHandler()
		if code, _ := putTo(h, "POST", "PackageRelease/nope/$install", ""); code != 404 {
			t.Fatalf("%d", code)
		}
	})
}

func TestPackageInstallUsesBinaryContentHook(t *testing.T) {
	repo := fhirrouter.NewMemoryRepository()
	ctx := context.Background()
	_, _ = repo.CreateResource(ctx, &fhir.Binary{ID: "pkgbin", ContentType: "application/fhir+json"}) // data nằm ngoài resource
	_, _ = repo.CreateResource(ctx, &fhir.PackageRelease{ID: "rel1", Content: &fhir.Attachment{URL: "Binary/pkgbin"}})
	var asked string
	h := fhirrouter.New(repo, fhirrouter.Options{UpdateAsCreate: true, BinaryContent: func(_ context.Context, b *fhir.Binary) ([]byte, error) {
		asked = b.ID
		return []byte(installTx), nil
	}}).HTTPHandler()
	if code, body := putTo(h, "POST", "PackageRelease/rel1/$install", ""); code != 200 || asked != "pkgbin" {
		t.Fatalf("%d %s asked=%q", code, body, asked)
	}
	if code, _ := putTo(h, "GET", "ValueSet/vs1", ""); code != 200 {
		t.Fatalf("ValueSet/vs1: %d", code)
	}
}

type denyAdmin struct{ fhirrouter.Repository }

func (denyAdmin) RequireAdmin(context.Context) error { return fhirrouter.Forbidden("chỉ admin") }

func TestPackageInstallRequiresAdmin(t *testing.T) {
	mem := fhirrouter.NewMemoryRepository()
	installFixture(t, mem, installTx)
	h := fhirrouter.New(denyAdmin{mem}, fhirrouter.Options{UpdateAsCreate: true}).HTTPHandler()
	if code, body := putTo(h, "POST", "PackageRelease/rel1/$install", ""); code != http.StatusForbidden {
		t.Fatalf("%d %s", code, body)
	}
	if c, _ := putTo(h, "GET", "ValueSet/vs1", ""); c != 404 {
		t.Fatalf("không được chạy Bundle khi bị từ chối: %d", c)
	}
	if got := installations(t, mem); len(got) != 0 {
		t.Fatalf("không được ghi PackageInstallation: %v", got)
	}
}
