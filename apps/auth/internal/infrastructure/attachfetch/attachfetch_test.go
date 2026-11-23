package attachfetch

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"trivita-fhir/pkg/safehttp"
)

func TestFetchSuccessAndLimits(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch r.URL.Path {
		case "/ok":
			_, _ = w.Write([]byte("hello world"))
		case "/big":
			_, _ = w.Write([]byte(strings.Repeat("x", 100)))
		case "/404":
			http.NotFound(w, r)
		case "/redirect":
			http.Redirect(w, r, "https://169.254.169.254/latest", http.StatusFound)
		case "/slow":
			time.Sleep(400 * time.Millisecond)
			_, _ = w.Write([]byte("late"))
		}
	}))
	defer srv.Close()
	// client tin chứng chỉ của máy chủ thử; bỏ chặn nội bộ vì máy chủ ở 127.0.0.1 (việc chặn được kiểm riêng ở dưới)
	f := New(5*time.Second, true) // dư dả để không flaky khi máy bận (bắt tay TLS đầu tiên)
	f.client = srv.Client()
	f.client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	ctx := context.Background()
	if got, err := f.Fetch(ctx, srv.URL+"/ok", 1024); err != nil || string(got) != "hello world" {
		t.Fatalf("%q %v", got, err)
	}
	for name, tc := range map[string]struct{ path, want string }{
		"quá lớn":  {"/big", "body too large"},
		"404":      {"/404", "HTTP 404"},
		"redirect": {"/redirect", "HTTP 302"},
	} {
		_, err := f.Fetch(ctx, srv.URL+tc.path, 50)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v (mong %q)", name, err, tc.want)
		}
	}
	// quá hạn: timeout ngắn riêng, máy chủ ngủ lâu hơn nhiều
	slow := New(100*time.Millisecond, true)
	sc := *srv.Client() // bản sao: không đổi client dùng chung
	sc.Timeout = 100 * time.Millisecond
	slow.client = &sc
	if _, err := slow.Fetch(ctx, srv.URL+"/slow", 50); err == nil || !strings.Contains(err.Error(), "request") {
		t.Errorf("chậm: %v", err)
	}
	// đúng bằng giới hạn thì được
	if _, err := f.Fetch(ctx, srv.URL+"/big", 100); err != nil {
		t.Errorf("bằng giới hạn: %v", err)
	}
	// không phải 3xx theo: máy chủ đích của redirect không bao giờ bị gọi (hits chỉ đếm máy chủ thử)
	if hits.Load() == 0 {
		t.Error("máy chủ thử phải được gọi")
	}
}

func TestFetchBlocksUnsafeTargetsWithoutConnecting(t *testing.T) {
	f := New(time.Second, false) // chặn thật
	ctx := context.Background()
	for _, tc := range []struct{ url, want string }{
		{"https://169.254.170.2/v2/metadata", "Outbound request to unsafe hostname 169.254.170.2 is blocked"},
		{"https://127.0.0.1/x", "unsafe hostname 127.0.0.1"},
		{"https://localhost/x", "unsafe hostname localhost"},
		{"https://[::1]/x", "unsafe hostname ::1"},
		{"https://10.0.0.5:8443/x", "unsafe hostname 10.0.0.5"},
		{"https://198.18.0.1/x", "unsafe hostname 198.18.0.1"},
		{"https://db.localhost/x", "unsafe hostname db.localhost"},
	} {
		_, err := f.Fetch(ctx, tc.url, 1024)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v", tc.url, err)
		}
	}
	for _, bad := range []string{"http://example.org/x", "ftp://example.org/x", "https://user:pass@example.org/x", "https:///x", "not a url", ""} {
		if _, err := f.Fetch(ctx, bad, 1024); err == nil || !strings.Contains(err.Error(), "invalid URL") {
			t.Errorf("%q phải bị từ chối: %v", bad, err)
		}
	}
	// lỗi không chứa chi tiết mạng
	_, err := New(500*time.Millisecond, true).Fetch(ctx, "http://127.0.0.1:1/x", 10)
	if err == nil || strings.Contains(err.Error(), "refused") || strings.Contains(err.Error(), "127.0.0.1:1") {
		t.Errorf("không được lộ chi tiết kết nối: %v", err)
	}
}

func TestDefaultTimeout(t *testing.T) {
	if New(0, false).timeoutMax != 30*time.Second {
		t.Error("mặc định 30s")
	}
}

type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Chặn lúc nối (DNS trỏ vào địa chỉ nội bộ) cho ra cùng thông điệp "unsafe address"; lỗi mạng khác không lộ chi tiết.
func TestFetchMapsDialTimeBlock(t *testing.T) {
	f := New(time.Second, false)
	f.client = &http.Client{Transport: rtFunc(func(*http.Request) (*http.Response, error) { return nil, safehttp.ErrBlockedAddress })}
	_, err := f.Fetch(context.Background(), "https://tricky.example.org/x", 10)
	if err == nil || !strings.Contains(err.Error(), "Outbound request to unsafe address tricky.example.org is blocked") {
		t.Errorf("%v", err)
	}
	f.client = &http.Client{Transport: rtFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("dial tcp 10.1.2.3:443: connection refused")
	})}
	if _, err := f.Fetch(context.Background(), "https://tricky.example.org/x", 10); err == nil || strings.Contains(err.Error(), "10.1.2.3") || err.Error() != "request failed" {
		t.Errorf("không lộ chi tiết: %v", err)
	}
}
