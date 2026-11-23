package bootstrap_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"trivita-fhir/apps/fhir-server/internal/bootstrap"
	"trivita-fhir/apps/fhir-server/internal/config"
)

// miniS3 là máy chủ S3 tối thiểu (kiểu path) để kiểm việc nối adapter S3 vào fhir-server; chữ ký SigV4 đã được kiểm ở pkg/binarystore/s3store.
type miniS3 struct {
	mu   sync.Mutex
	objs map[string][]byte
	auth int
}

func (m *miniS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=AKTEST/") {
		m.auth++
	}
	key := strings.TrimPrefix(r.URL.Path, "/bkt/")
	switch r.Method {
	case http.MethodPut:
		if src := r.Header.Get("X-Amz-Copy-Source"); src != "" {
			b, ok := m.objs[strings.TrimPrefix(src, "/bkt/")]
			if !ok {
				w.WriteHeader(404)
				_, _ = w.Write([]byte(`<Error><Code>NoSuchKey</Code></Error>`))
				return
			}
			m.objs[key] = b
			_, _ = w.Write([]byte(`<CopyObjectResult/>`))
			return
		}
		b, _ := io.ReadAll(r.Body)
		m.objs[key] = b
	case http.MethodGet:
		b, ok := m.objs[key]
		if !ok {
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`<Error><Code>NoSuchKey</Code></Error>`))
			return
		}
		_, _ = w.Write(b)
	case http.MethodDelete:
		delete(m.objs, key)
		w.WriteHeader(204)
	}
}

func TestStorageOnS3(t *testing.T) {
	m := &miniS3{objs: map[string][]byte{}}
	s3 := httptest.NewServer(m)
	t.Cleanup(s3.Close)
	mut, _ := storageSettings(t, func(s *config.Settings) {
		s.BinaryStorage = "s3:bkt/env/"
		s.S3Region, s.S3Endpoint, s.S3AccessKeyID, s.S3SecretAccessKey = "eu-west-1", s3.URL, "AKTEST", "SKTEST"
	})
	_, srv := newServer(t, jwtSettings(mut))
	a := tok(t, "A", "fhir.write")

	up := want(t, rawCall(t, srv, "POST", "/fhir/R4/Binary", a, map[string]string{"Content-Type": "text/plain"}, []byte("lưu trên S3")), 201)
	id := up.str(t, "id")
	if g := want(t, call(t, srv, "GET", "/fhir/R4/Binary/"+id, a, nil), 200); string(g.body) != "lưu trên S3" {
		t.Fatalf("GET: %q", g.body)
	}
	if g := want(t, call(t, srv, "GET", pathOf(up.str(t, "url")), "", nil), 200); string(g.body) != "lưu trên S3" {
		t.Fatalf("url ký: %q", g.body)
	}
	// nội dung nằm đúng khóa có prefix; không có gì rơi ra thư mục cục bộ
	n := 0
	for k := range m.objs {
		if strings.HasPrefix(k, "env/binary/"+id+"/") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("khóa trong S3: %v", m.objs)
	}
	// sửa metadata: phiên bản mới có nội dung (Copy phía máy chủ)
	j := rawCall(t, srv, "GET", "/fhir/R4/Binary/"+id, a, map[string]string{"Accept": "application/fhir+json"}, nil)
	body := j.json(t)
	body["securityContext"] = nil
	delete(body, "securityContext")
	want(t, call(t, srv, "PUT", "/fhir/R4/Binary/"+id, a, body), 200)
	if g := want(t, call(t, srv, "GET", "/fhir/R4/Binary/"+id, a, nil), 200); string(g.body) != "lưu trên S3" {
		t.Fatalf("sau cập nhật metadata: %q", g.body)
	}
	if m.auth == 0 {
		t.Error("yêu cầu tới S3 phải có chữ ký SigV4")
	}
}

func TestStorageS3BadConfigFailsAtStartup(t *testing.T) {
	mut, _ := storageSettings(t, func(s *config.Settings) {
		s.BinaryStorage = "s3:Bad_Bucket"
		s.S3Region, s.S3AccessKeyID, s.S3SecretAccessKey = "eu-west-1", "a", "b"
	})
	if _, err := bootstrap.New(context.Background(), jwtSettings(mut)); err == nil {
		t.Fatal("bucket sai phải làm khởi động thất bại")
	}
}
