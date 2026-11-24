package bootstrap_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"trivita-fhir/apps/fhir-server/internal/config"
)

// Tự tải attachment ngoài (FHIR_AUTO_DOWNLOAD) qua server thật: tạo DocumentReference trỏ tới một máy chủ ngoài, sau đó URL thành Binary/<id> của đúng project, nội dung đúng từng byte,
// project khác không đọc được; mặc định TẮT thì không gọi ra ngoài; chống SSRF mặc định chặn địa chỉ nội bộ; chạy được cả khi Subscription bật hay tắt.
func TestAutoDownloadEndToEnd(t *testing.T) {
	pdf := bytes.Repeat([]byte("%PDF nội dung tài liệu "), 500)
	var hits atomic.Int32
	ext := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write(pdf)
	}))
	defer ext.Close()

	docRef := func(url string) string {
		return `{"resourceType":"DocumentReference","status":"current","content":[{"attachment":{"url":"` + url + `","title":"t"}}]}`
	}
	attachmentURL := func(t *testing.T, srv *httptest.Server, token, id string) string {
		t.Helper()
		return want(t, call(t, srv, "GET", "/fhir/R4/DocumentReference/"+id, token, nil), 200).str(t, "content", "0", "attachment", "url")
	}
	waitBinary := func(t *testing.T, srv *httptest.Server, token, id string) string {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if u := attachmentURL(t, srv, token, id); strings.Contains(u, "/storage/") { // đọc trả URL ký của Binary thay cho tham chiếu
				return u
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("URL không được đổi thành URL kho Binary: %s", attachmentURL(t, srv, token, id))
		return ""
	}

	for _, subs := range []bool{false, true} {
		name := "không Subscription"
		if subs {
			name = "có Subscription"
		}
		t.Run(name, func(t *testing.T) {
			mut, _ := storageSettings(t, func(s *config.Settings) {
				s.AutoDownload, s.AutoDownloadAllowPrivate, s.Subscriptions = true, true, subs // máy chủ thử ở 127.0.0.1
			})
			_, srv := newServer(t, jwtSettings(mut))
			a, b := tok(t, "A", "fhir.write"), tok(t, "B", "fhir.write")

			hits.Store(0)
			id := want(t, call(t, srv, "POST", "/fhir/R4/DocumentReference", a, docRef(ext.URL+"/tai-lieu.pdf?token=bimat")), 201).str(t, "id")
			url := waitBinary(t, srv, a, id)
			if hits.Load() != 1 {
				t.Errorf("máy chủ ngoài bị gọi %d lần, muốn 1", hits.Load())
			}
			binID := strings.SplitN(url[strings.Index(url, "/storage/")+len("/storage/"):], "/", 2)[0]
			// Binary thuộc project A, có securityContext, nội dung đúng từng byte
			meta := want(t, call(t, srv, "GET", "/fhir/R4/Binary/"+binID, a, nil), 200)
			if !bytes.Equal(meta.body, pdf) || meta.header.Get("Content-Type") != "application/pdf" {
				t.Errorf("Binary: %d byte, %q", len(meta.body), meta.header.Get("Content-Type"))
			}
			j := rawCall(t, srv, "GET", "/fhir/R4/Binary/"+binID, a, map[string]string{"Accept": "application/fhir+json"}, nil)
			if j.str(t, "meta", "project") != "A" || j.str(t, "securityContext", "reference") != "DocumentReference/"+id {
				t.Errorf("Binary resource: %s", j.body)
			}
			want(t, call(t, srv, "GET", "/fhir/R4/Binary/"+binID, b, nil), 404)
			// lần ghi do worker gây ra không tải lại
			time.Sleep(200 * time.Millisecond)
			if hits.Load() != 1 {
				t.Errorf("tải lặp: %d lần", hits.Load())
			}
		})
	}

	t.Run("tắt mặc định thì không gọi ra ngoài", func(t *testing.T) {
		mut, _ := storageSettings(t)
		_, srv := newServer(t, jwtSettings(mut))
		a := tok(t, "A", "fhir.write")
		hits.Store(0)
		id := want(t, call(t, srv, "POST", "/fhir/R4/DocumentReference", a, docRef(ext.URL+"/x.pdf")), 201).str(t, "id")
		time.Sleep(300 * time.Millisecond)
		if hits.Load() != 0 || attachmentURL(t, srv, a, id) != ext.URL+"/x.pdf" {
			t.Errorf("tính năng tắt mà vẫn tải: %d lần, %q", hits.Load(), attachmentURL(t, srv, a, id))
		}
	})

	t.Run("chống SSRF mặc định chặn địa chỉ nội bộ", func(t *testing.T) {
		mut, _ := storageSettings(t, func(s *config.Settings) { s.AutoDownload = true }) // không bật AllowPrivate
		_, srv := newServer(t, jwtSettings(mut))
		a := tok(t, "A", "fhir.write")
		hits.Store(0)
		id := want(t, call(t, srv, "POST", "/fhir/R4/DocumentReference", a, docRef(ext.URL+"/x.pdf")), 201).str(t, "id")
		time.Sleep(500 * time.Millisecond)
		if hits.Load() != 0 || attachmentURL(t, srv, a, id) != ext.URL+"/x.pdf" {
			t.Errorf("URL nội bộ không được tải: %d lần, %q", hits.Load(), attachmentURL(t, srv, a, id))
		}
	})

	t.Run("danh sách cho phép và bỏ qua", func(t *testing.T) {
		mut, _ := storageSettings(t, func(s *config.Settings) {
			s.AutoDownload, s.AutoDownloadAllowPrivate = true, true
			s.AutoDownloadAllowedPrefixes = ext.URL + "/ok/," + ext.URL + "/ign/"
			s.AutoDownloadIgnoredPrefixes = ext.URL + "/ign/"
		})
		_, srv := newServer(t, jwtSettings(mut))
		a := tok(t, "A", "fhir.write")
		hits.Store(0)
		bad := want(t, call(t, srv, "POST", "/fhir/R4/DocumentReference", a, docRef(ext.URL+"/khac/x.pdf")), 201).str(t, "id")
		ign := want(t, call(t, srv, "POST", "/fhir/R4/DocumentReference", a, docRef(ext.URL+"/ign/x.pdf")), 201).str(t, "id")
		ok := want(t, call(t, srv, "POST", "/fhir/R4/DocumentReference", a, docRef(ext.URL+"/ok/x.pdf")), 201).str(t, "id")
		waitBinary(t, srv, a, ok)
		time.Sleep(200 * time.Millisecond)
		if hits.Load() != 1 || strings.Contains(attachmentURL(t, srv, a, bad), "/storage/") || strings.Contains(attachmentURL(t, srv, a, ign), "/storage/") {
			t.Errorf("lọc tiền tố: %d lần gọi", hits.Load())
		}
	})

	t.Run("URL của chính server không được tải", func(t *testing.T) {
		mut, _ := storageSettings(t, func(s *config.Settings) {
			s.AutoDownload, s.AutoDownloadAllowPrivate = true, true
			s.BaseURL = ext.URL + "/fhir/R4" // gốc FHIR của server nằm trên máy chủ thử để đếm số lần bị gọi
		})
		_, srv := newServer(t, jwtSettings(mut))
		a := tok(t, "A", "fhir.write")
		hits.Store(0)
		own := ext.URL + "/fhir/R4/Binary/da-co"
		id := want(t, call(t, srv, "POST", "/fhir/R4/DocumentReference", a, docRef(own)), 201).str(t, "id")
		time.Sleep(400 * time.Millisecond)
		if hits.Load() != 0 {
			t.Errorf("URL của chính server bị tải %d lần", hits.Load())
		}
		_ = id
	})

	t.Run("tải chậm không chặn yêu cầu ghi (broker bộ nhớ giao đồng bộ)", func(t *testing.T) {
		release := make(chan struct{})
		slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-release; _, _ = w.Write([]byte("chậm")) }))
		defer slow.Close()
		defer close(release)
		mut, _ := storageSettings(t, func(s *config.Settings) { s.AutoDownload, s.AutoDownloadAllowPrivate = true, true })
		_, srv := newServer(t, jwtSettings(mut))
		a := tok(t, "A", "fhir.write")
		t0 := time.Now()
		want(t, call(t, srv, "POST", "/fhir/R4/DocumentReference", a, docRef(slow.URL+"/cham")), 201)
		if d := time.Since(t0); d > 3*time.Second {
			t.Errorf("yêu cầu ghi bị chặn %v bởi việc tải ngoài", d)
		}
	})
}
