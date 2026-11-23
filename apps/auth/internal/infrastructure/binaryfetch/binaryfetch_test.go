package binaryfetch_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"trivita-fhir/apps/auth/internal/application"
	"trivita-fhir/apps/auth/internal/infrastructure/binaryfetch"
)

const token = "super-secret-caller-token"

func newSource(t *testing.T, h http.HandlerFunc, path string, timeout time.Duration) (*binaryfetch.Source, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	s, err := binaryfetch.New(srv.URL+path, timeout)
	if err != nil {
		t.Fatal(err)
	}
	return s, srv
}

func TestReadForwardsCallerTokenAndReturnsContent(t *testing.T) {
	var gotAuth, gotAccept, gotPath, gotMethod string
	s, _ := newSource(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotAccept, gotPath, gotMethod = r.Header.Get("Authorization"), r.Header.Get("Accept"), r.URL.Path, r.Method
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write([]byte("%PDF-1.7 ..."))
	}, "/fhir/R4/", 0) // dấu / cuối được bỏ
	data, ct, err := s.Read(context.Background(), token, "abc-1.2", 1<<20)
	if err != nil || string(data) != "%PDF-1.7 ..." || ct != "application/pdf" {
		t.Fatalf("%q %q %v", data, ct, err)
	}
	if gotAuth != "Bearer "+token || gotAccept != "application/octet-stream" || gotPath != "/fhir/R4/Binary/abc-1.2" || gotMethod != "GET" {
		t.Fatalf("yêu cầu: %q %q %q %q", gotAuth, gotAccept, gotPath, gotMethod)
	}
}

func TestReadMapsStatuses(t *testing.T) {
	for status, want := range map[int]error{404: application.ErrBinaryNotFound, 410: application.ErrBinaryNotFound, 401: application.ErrBinaryForbidden, 403: application.ErrBinaryForbidden} {
		s, _ := newSource(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) }, "", 0)
		if _, _, err := s.Read(context.Background(), token, "x", 100); !errors.Is(err, want) {
			t.Errorf("%d → %v, muốn %v", status, err, want)
		}
	}
	for _, status := range []int{400, 500, 502, 503, 304, 204 + 100} {
		s, _ := newSource(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte("secret server detail " + token))
		}, "", 0)
		_, _, err := s.Read(context.Background(), token, "x", 100)
		if err == nil || errors.Is(err, application.ErrBinaryNotFound) || errors.Is(err, application.ErrBinaryForbidden) || errors.Is(err, application.ErrBinaryTooLarge) {
			t.Errorf("%d: %v", status, err)
		} else if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), token) {
			t.Errorf("%d: lỗi lộ thân phản hồi hoặc token: %v", status, err)
		}
	}
}

func TestReadSizeLimit(t *testing.T) {
	for name, h := range map[string]http.HandlerFunc{
		"Content-Length": func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(strings.Repeat("a", 101))) },
		"chunked": func(w http.ResponseWriter, r *http.Request) {
			f := w.(http.Flusher)
			for i := 0; i < 11; i++ {
				_, _ = w.Write([]byte(strings.Repeat("a", 10)))
				f.Flush()
			}
		},
	} {
		s, _ := newSource(t, h, "", 0)
		if _, _, err := s.Read(context.Background(), token, "x", 100); !errors.Is(err, application.ErrBinaryTooLarge) {
			t.Errorf("%s: %v", name, err)
		}
	}
	s, _ := newSource(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(strings.Repeat("a", 100))) }, "", 0)
	if d, _, err := s.Read(context.Background(), token, "x", 100); err != nil || len(d) != 100 {
		t.Fatalf("đúng trần: %d %v", len(d), err)
	}
	s, _ = newSource(t, func(w http.ResponseWriter, r *http.Request) {}, "", 0)
	if d, _, err := s.Read(context.Background(), token, "x", 100); err != nil || len(d) != 0 {
		t.Fatalf("rỗng: %d %v", len(d), err)
	}
}

// Token không bao giờ tới host khác: redirect không được theo (kể cả sang host nội bộ khác).
func TestReadDoesNotFollowRedirects(t *testing.T) {
	var hits atomic.Int32
	var leaked atomic.Value
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		leaked.Store(r.Header.Get("Authorization"))
	}))
	t.Cleanup(evil.Close)
	for _, code := range []int{301, 302, 303, 307, 308} {
		s, _ := newSource(t, func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, evil.URL+"/steal", code) }, "", 0)
		_, _, err := s.Read(context.Background(), token, "x", 100)
		if err == nil || errors.Is(err, application.ErrBinaryNotFound) {
			t.Errorf("%d: %v", code, err)
		}
	}
	if hits.Load() != 0 {
		t.Fatalf("host khác nhận %d yêu cầu, Authorization=%v", hits.Load(), leaked.Load())
	}
}

func TestReadIgnoresEnvironmentProxy(t *testing.T) {
	var proxied atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { proxied.Add(1) }))
	t.Cleanup(proxy.Close)
	t.Setenv("HTTP_PROXY", proxy.URL)
	t.Setenv("http_proxy", proxy.URL)
	t.Setenv("NO_PROXY", "")
	s, _ := newSource(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("direct")) }, "", 0)
	if d, _, err := s.Read(context.Background(), token, "x", 100); err != nil || string(d) != "direct" {
		t.Fatalf("%q %v", d, err)
	}
	if proxied.Load() != 0 {
		t.Fatal("yêu cầu (kèm token) đã đi qua proxy của môi trường")
	}
}

func TestReadTimeoutAndNetworkErrorsHideDetails(t *testing.T) {
	s, _ := newSource(t, func(w http.ResponseWriter, r *http.Request) { time.Sleep(300 * time.Millisecond) }, "", 50*time.Millisecond)
	_, _, err := s.Read(context.Background(), token, "x", 100)
	if err == nil || !strings.Contains(err.Error(), "timed out") && !strings.Contains(err.Error(), "request failed") {
		t.Fatalf("%v", err)
	}
	// máy chủ đóng
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()
	dead, _ := binaryfetch.New(url+"/fhir/R4", time.Second)
	_, _, err = dead.Read(context.Background(), token, "x", 100)
	if err == nil || strings.Contains(err.Error(), url) || strings.Contains(err.Error(), "127.0.0.1") || strings.Contains(err.Error(), token) {
		t.Fatalf("lỗi mạng lộ địa chỉ hoặc token: %v", err)
	}
	// ctx hủy
	s2, _ := newSource(t, func(w http.ResponseWriter, r *http.Request) { time.Sleep(time.Second) }, "", 0)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, _, err := s2.Read(ctx, token, "x", 100); err == nil {
		t.Fatal("ctx hết hạn phải lỗi")
	}
}

func TestNewValidatesBaseURL(t *testing.T) {
	for _, bad := range []string{"", "/fhir/R4", "ftp://h/x", "http://", "https://u:p@h/x", "https://h/x?a=1", "https://h/x#f", "https://h/x?", "javascript:alert(1)"} {
		if _, err := binaryfetch.New(bad, 0); err == nil {
			t.Errorf("%q: không lỗi", bad)
		}
	}
	for _, ok := range []string{"http://fhir-server:8005/fhir/R4", "https://fhir.example.org/fhir/R4/", "http://127.0.0.1:1"} {
		if _, err := binaryfetch.New(ok, 0); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
}
