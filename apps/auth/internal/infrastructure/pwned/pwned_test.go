package pwned

import (
	"context"
	"crypto/sha1" //nolint:gosec
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func sha(pw string) (string, string) {
	s := sha1.Sum([]byte(pw)) //nolint:gosec
	h := strings.ToUpper(hex.EncodeToString(s[:]))
	return h[:5], h[5:]
}

func TestCount(t *testing.T) {
	prefix, suffix := sha("password123")
	var gotPath, gotPad string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotPad = r.URL.Path, r.Header.Get("Add-Padding")
		_, _ = w.Write([]byte("0018A45C4D1DEF81644B54AB7F969B88D65:3\r\n" + strings.ToLower(suffix) + ":42\r\n00D4F6E8FA6EECAD2A3AA415EEC418D38EC:0\r\n"))
	}))
	defer srv.Close()
	h := NewHIBP(srv.URL, time.Second)
	n, err := h.Count(context.Background(), "password123")
	if err != nil || n != 42 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if gotPath != "/"+prefix || gotPad != "true" {
		t.Errorf("chỉ gửi 5 ký tự đầu và Add-Padding: path=%s pad=%s", gotPath, gotPad)
	}
	if strings.Contains(gotPath, suffix) {
		t.Error("không được gửi phần còn lại của hash")
	}
	if n, err := h.Count(context.Background(), "mật-khẩu-không-có-trong-danh-sách"); err != nil || n != 0 {
		t.Errorf("không có thì 0: %d %v", n, err)
	}
}

func TestPaddingLineMeansZero(t *testing.T) {
	_, suffix := sha("x-padding")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(suffix + ":0\n")) }))
	defer srv.Close()
	if n, err := NewHIBP(srv.URL, time.Second).Count(context.Background(), "x-padding"); err != nil || n != 0 {
		t.Errorf("dòng padding là 0: %d %v", n, err)
	}
}

func TestFailClosed(t *testing.T) {
	for name, handler := range map[string]http.HandlerFunc{
		"500":     func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) },
		"429":     func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(429) },
		"hết giờ": func(w http.ResponseWriter, _ *http.Request) { time.Sleep(300 * time.Millisecond) },
	} {
		srv := httptest.NewServer(handler)
		h := NewHIBP(srv.URL, 100*time.Millisecond)
		if _, err := h.Count(context.Background(), "abc"); err == nil {
			t.Errorf("%s: lỗi dịch vụ phải trả lỗi, không được coi là sạch", name)
		}
		srv.Close()
	}
	// không kết nối được
	if _, err := NewHIBP("http://127.0.0.1:1", 100*time.Millisecond).Count(context.Background(), "abc"); err == nil {
		t.Error("không kết nối được phải lỗi")
	}
	// context hủy
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewHIBP("http://127.0.0.1:1", time.Second).Count(ctx, "abc"); err == nil {
		t.Error("context hủy phải lỗi")
	}
	// số đếm hỏng
	_, suffix := sha("bad-count")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(suffix + ":abc\n")) }))
	defer srv.Close()
	if _, err := NewHIBP(srv.URL, time.Second).Count(context.Background(), "bad-count"); err == nil {
		t.Error("số đếm hỏng phải lỗi")
	}
}

func TestURLNormalizationAndDisabled(t *testing.T) {
	if h := NewHIBP("", time.Second); h.URL != DefaultURL {
		t.Errorf("mặc định: %s", h.URL)
	}
	if h := NewHIBP("http://x/range", time.Second); h.URL != "http://x/range/" {
		t.Errorf("thêm dấu /: %s", h.URL)
	}
	if n, err := (Disabled{}).Count(context.Background(), "x"); n != 0 || err != nil {
		t.Error("Disabled")
	}
	if _, err := NewHIBP("http://bad host/", time.Second).Count(context.Background(), "x"); err == nil {
		t.Error("URL hỏng phải lỗi")
	}
}
