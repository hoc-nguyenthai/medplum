package clientassertion

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
	"trivita-fhir/apps/auth/internal/infrastructure/jwks"
)

type countingTransport struct{ calls atomic.Int32 }

func (c *countingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	c.calls.Add(1)
	return nil, http.ErrNotSupported
}

// Lớp 1 (CheckURL): URL không đạt (http, userinfo, scheme lạ) bị chặn TRƯỚC khi có bất kỳ yêu cầu mạng nào.
func TestKeysRejectsBadJWKSURLBeforeAnyRequest(t *testing.T) {
	ct := &countingTransport{}
	v := New(false)
	v.f.Client = &http.Client{Transport: ct}
	for _, uri := range []string{"http://example.org/jwks.json", "https://user:pw@example.org/jwks.json", "ftp://example.org/x", "//example.org/x", ""} {
		if _, err := v.keys(context.Background(), uri, false); err == nil {
			t.Errorf("%q phải bị từ chối", uri)
		}
	}
	if ct.calls.Load() != 0 {
		t.Errorf("không được có yêu cầu mạng nào (%d)", ct.calls.Load())
	}
	// allowPrivate (dev/test) cho phép http
	dev := New(true)
	dev.f.Client = &http.Client{Transport: ct}
	_, _ = dev.keys(context.Background(), "http://example.org/jwks.json", false)
	if ct.calls.Load() != 1 {
		t.Errorf("allowPrivate phải cho phép http: %d", ct.calls.Load())
	}
}

// Lớp 2 (kết nối): URL hợp lệ về cú pháp nhưng trỏ vào loopback bị chặn lúc kết nối, máy chủ không nhận được kết nối nào.
func TestKeysBlocksLoopbackAtConnectTime(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer srv.Close()
	v := New(false)
	v.f.HTTPSOnly = false // chỉ để kiểm riêng lớp kết nối: lớp CheckURL (https) đã có test riêng ở trên
	if _, err := v.keys(context.Background(), srv.URL+"/jwks.json", false); err == nil {
		t.Fatal("loopback phải bị chặn")
	}
	if hits.Load() != 0 {
		t.Errorf("máy chủ nội bộ không được nhận yêu cầu (%d)", hits.Load())
	}
	// cache: lần tải thất bại không để lại mục
	if v.f.CacheSize() != 0 {
		t.Error("không được cache lần tải lỗi")
	}
}

func TestKeysRejectsOversizedAndMalformedJWKS(t *testing.T) {
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(body) }))
	defer srv.Close()
	v := New(true)
	v.now = func() time.Time { return time.Now() }
	huge := make([]byte, jwks.MaxBytes+10)
	for i := range huge {
		huge[i] = ' '
	}
	for name, b := range map[string][]byte{"quá lớn": huge, "không phải JSON": []byte("abc"), "không có khóa": []byte(`{"keys":[]}`)} {
		body = b
		v.f.ResetCache()
		if _, err := v.keys(context.Background(), srv.URL, false); err == nil {
			t.Errorf("%s phải bị từ chối", name)
		}
	}
}
