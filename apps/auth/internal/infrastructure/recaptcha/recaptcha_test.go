package recaptcha

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"trivita-fhir/apps/auth/internal/application"
)

var _ application.Recaptcha = (*Google)(nil)

func TestVerifyPostsSecretInBodyNotQuery(t *testing.T) {
	var gotQuery, gotBody, gotType, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery, gotType, gotMethod = r.URL.RawQuery, r.Header.Get("Content-Type"), r.Method
		_ = r.ParseForm()
		gotBody = r.PostForm.Get("secret") + "|" + r.PostForm.Get("response")
		_, _ = w.Write([]byte(`{"success":true,"hostname":"x"}`))
	}))
	defer srv.Close()
	ok, err := New(srv.URL, time.Second).Verify(context.Background(), "bi-mat", "token-1")
	if err != nil || !ok {
		t.Fatalf("%v %v", ok, err)
	}
	if gotMethod != "POST" || gotQuery != "" || gotBody != "bi-mat|token-1" || !strings.HasPrefix(gotType, "application/x-www-form-urlencoded") {
		t.Errorf("method=%s query=%q body=%q type=%q", gotMethod, gotQuery, gotBody, gotType)
	}
}

func TestVerifyResults(t *testing.T) {
	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
		want    bool
		wantErr bool
	}{
		{"không phải người", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"success":false}`)) }, false, false},
		{"thiếu success", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) }, false, false},
		{"500", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) }, false, true},
		{"JSON hỏng", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`<html>`)) }, false, true},
		{"rỗng", func(http.ResponseWriter, *http.Request) {}, false, true},
	} {
		srv := httptest.NewServer(tc.handler)
		ok, err := New(srv.URL, time.Second).Verify(context.Background(), "s", "t")
		srv.Close()
		if ok != tc.want || (err != nil) != tc.wantErr {
			t.Errorf("%s: ok=%v err=%v", tc.name, ok, err)
		}
	}
}

func TestVerifyFailsClosedOnNetworkErrorAndTimeoutWithoutLeakingURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { time.Sleep(300 * time.Millisecond) }))
	defer srv.Close()
	ok, err := New(srv.URL, 50*time.Millisecond).Verify(context.Background(), "s", "t")
	if ok || err == nil {
		t.Fatalf("timeout phải là lỗi: %v %v", ok, err)
	}
	if strings.Contains(err.Error(), srv.URL) {
		t.Errorf("lỗi lộ URL: %v", err)
	}
	dead := httptest.NewServer(http.NotFoundHandler())
	dead.Close()
	if ok, err := New(dead.URL, time.Second).Verify(context.Background(), "s", "t"); ok || err == nil {
		t.Errorf("không kết nối được phải là lỗi: %v %v", ok, err)
	}
}

func TestVerifyDoesNotFollowRedirects(t *testing.T) {
	var hits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	defer target.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	ok, err := New(srv.URL, time.Second).Verify(context.Background(), "s", "t")
	if ok || err == nil || hits.Load() != 0 {
		t.Errorf("không được theo chuyển hướng: ok=%v err=%v hits=%d", ok, err, hits.Load())
	}
}

func TestDefaults(t *testing.T) {
	g := New("", 0)
	if g.URL != DefaultURL || g.Client.Timeout != 3*time.Second {
		t.Errorf("%+v %v", g, g.Client.Timeout)
	}
}
