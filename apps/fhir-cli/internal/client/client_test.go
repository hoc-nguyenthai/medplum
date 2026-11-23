package client_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"trivita-fhir/apps/fhir-cli/internal/client"
)

func TestResolveKeepsRequestsOnOwnServer(t *testing.T) {
	c, err := client.New("http://localhost:8005/fhir/R4/", "", "", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	for ref, want := range map[string]string{
		"Patient/1":               "http://localhost:8005/fhir/R4/Patient/1",
		"Patient?name=x&_count=2": "http://localhost:8005/fhir/R4/Patient?name=x&_count=2",
		"":                        "http://localhost:8005/fhir/R4/",
		"http://public.example/fhir/R4/Patient?p=1": "http://localhost:8005/fhir/R4/Patient?p=1", // host do máy chủ khai báo bị bỏ
		"https://evil.example/fhir/R4/Binary/1":     "http://localhost:8005/fhir/R4/Binary/1",    // không bao giờ gửi token tới host khác
		"/fhir/R4/bulkdata/1/status":                "http://localhost:8005/fhir/R4/bulkdata/1/status",
	} {
		got, err := c.Resolve(ref)
		if err != nil || got != want {
			t.Errorf("Resolve(%q) = %q %v, muốn %q", ref, got, err, want)
		}
	}
	for _, ref := range []string{"http://x/other/path", "/admin", "/Patient/1", "https://evil.example/"} {
		if _, err := c.Resolve(ref); err == nil {
			t.Errorf("Resolve(%q) phải lỗi (ngoài gốc API)", ref)
		}
	}
	for _, base := range []string{"", "localhost:8005", "ftp://x/y"} {
		if _, err := client.New(base, "", "", time.Second); err == nil {
			t.Errorf("New(%q) phải lỗi", base)
		}
	}
}

func TestHeadersAndErrors(t *testing.T) {
	var gotAuth, gotProject string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotProject = r.Header.Get("Authorization"), r.Header.Get("X-Fhir-Project")
		switch r.URL.Path {
		case "/fhir/R4/outcome":
			w.WriteHeader(422)
			_, _ = w.Write([]byte(`{"resourceType":"OperationOutcome","issue":[{"severity":"error","diagnostics":"sai rồi"}]}`))
		case "/fhir/R4/plain":
			w.WriteHeader(502)
			_, _ = w.Write([]byte("bad gateway"))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()
	c, _ := client.New(srv.URL+"/fhir/R4", "tok", "P1", time.Second)
	if _, err := c.Do(t.Context(), "GET", "ok", nil); err != nil || gotAuth != "Bearer tok" || gotProject != "P1" {
		t.Fatalf("header: %q %q %v", gotAuth, gotProject, err)
	}
	_, err := c.Do(t.Context(), "GET", "outcome", nil)
	if ae, ok := err.(*client.APIError); !ok || ae.Status != 422 || ae.Message != "sai rồi" || !client.IsStatus(err, 422) {
		t.Fatalf("outcome: %v", err)
	}
	if _, err := c.Do(t.Context(), "GET", "plain", nil); err == nil || err.(*client.APIError).Message != "bad gateway" {
		t.Fatalf("plain: %v", err)
	}
}
