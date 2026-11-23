package s3store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"trivita-fhir/pkg/binarystore"
	"trivita-fhir/pkg/binarystore/storetest"
)

// Bộ test hợp đồng chung cho mọi adapter.
func TestContract(t *testing.T) {
	storetest.Run(t, func(t testing.TB) binarystore.Store {
		tt := t.(*testing.T)
		return newFakeS3(tt).store(tt, nil)
	})
}

func TestContractWithPrefixAndSSE(t *testing.T) {
	storetest.Run(t, func(t testing.TB) binarystore.Store {
		tt := t.(*testing.T)
		return newFakeS3(tt).store(tt, func(c *Config) { c.Prefix = "env/prod/"; c.ServerSideEncryption = "AES256" })
	})
}

// Vector chính thức của AWS ("Example: GET Object", Range) cho chữ ký SigV4 của S3.
func TestSignAWSVector(t *testing.T) {
	req, _ := http.NewRequest("GET", "https://examplebucket.s3.amazonaws.com/test.txt", nil)
	req.Header.Set("Range", "bytes=0-9")
	now := time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC)
	sign(req, credentials{AccessKeyID: "AKIAIOSFODNN7EXAMPLE", SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"}, "us-east-1", emptySHA256, now)
	want := "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request, SignedHeaders=host;range;x-amz-content-sha256;x-amz-date, Signature=f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41"
	if got := req.Header.Get("Authorization"); got != want {
		t.Fatalf("Authorization:\n got %s\nwant %s", got, want)
	}
}

func TestUriEncode(t *testing.T) {
	for in, want := range map[string]string{"a b/c": "a%20b/c", "binary/id.1-x/v_1~": "binary/id.1-x/v_1~", "é$": "%C3%A9%24", "a+b": "a%2Bb"} {
		if got := uriEncode(in, true); got != want {
			t.Errorf("uriEncode(%q)=%q, want %q", in, got, want)
		}
	}
	if got := uriEncode("a/b", false); got != "a%2Fb" {
		t.Errorf("không giữ '/': %q", got)
	}
}

func TestNewValidation(t *testing.T) {
	ok := Config{Bucket: "bkt-1", Region: "us-east-1", AccessKeyID: "a", SecretAccessKey: "b"}
	cases := map[string]func(*Config){
		"bucket hoa":        func(c *Config) { c.Bucket = "Bad_Bucket" },
		"bucket ngắn":       func(c *Config) { c.Bucket = "ab" },
		"prefix thiếu /":    func(c *Config) { c.Prefix = "a/b" },
		"prefix ..":         func(c *Config) { c.Prefix = "a/../b/" },
		"prefix .":          func(c *Config) { c.Prefix = "./a/" },
		"prefix //":         func(c *Config) { c.Prefix = "a//b/" },
		"prefix ký tự lạ":   func(c *Config) { c.Prefix = "a b/" },
		"region":            func(c *Config) { c.Region = "" },
		"thiếu key":         func(c *Config) { c.AccessKeyID = "" },
		"thiếu secret":      func(c *Config) { c.SecretAccessKey = "" },
		"sse":               func(c *Config) { c.ServerSideEncryption = "aes" },
		"endpoint scheme":   func(c *Config) { c.Endpoint = "ftp://x" },
		"endpoint path":     func(c *Config) { c.Endpoint = "http://x/y" },
		"endpoint userinfo": func(c *Config) { c.Endpoint = "http://u:p@x" },
		"endpoint query":    func(c *Config) { c.Endpoint = "http://x?a=1" },
	}
	if _, err := New(ok); err != nil {
		t.Fatal(err)
	}
	for name, mod := range cases {
		c := ok
		mod(&c)
		if _, err := New(c); err == nil {
			t.Errorf("%s: phải bị từ chối", name)
		}
	}
}

func TestNewURLs(t *testing.T) {
	s, _ := New(Config{Bucket: "bkt-1", Region: "eu-west-1", AccessKeyID: "a", SecretAccessKey: "b"})
	if s.base.String() != "https://bkt-1.s3.eu-west-1.amazonaws.com" {
		t.Errorf("virtual-hosted: %s", s.base)
	}
	s, _ = New(Config{Bucket: "my.bucket", Region: "eu-west-1", AccessKeyID: "a", SecretAccessKey: "b"})
	if s.base.String() != "https://s3.eu-west-1.amazonaws.com/my.bucket" {
		t.Errorf("bucket có dấu chấm dùng kiểu path: %s", s.base)
	}
	s, _ = New(Config{Bucket: "bkt-1", Region: "us-east-1", Endpoint: "http://minio:9000/", Prefix: "p/", AccessKeyID: "a", SecretAccessKey: "b"})
	if got := s.objectPath("binary/x/1"); got != "/bkt-1/p/binary/x/1" {
		t.Errorf("objectPath: %s", got)
	}
}

func key(t *testing.T, id, v string) string {
	t.Helper()
	k, err := binarystore.Key(id, v)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestPrefixAppliedToObjectAndCopySource(t *testing.T) {
	f := newFakeS3(t)
	s := f.store(t, func(c *Config) { c.Prefix = "env/prod/"; c.ServerSideEncryption = "aws:kms" })
	ctx := context.Background()
	if _, err := s.Put(ctx, key(t, "a", "1"), strings.NewReader("x"), binarystore.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := s.Copy(ctx, key(t, "a", "1"), key(t, "a", "2")); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"env/prod/binary/a/1", "env/prod/binary/a/2"} {
		if string(f.objects[k]) != "x" {
			t.Errorf("thiếu %s: %v", k, f.objects)
		}
	}
	if len(f.sse) != 2 || f.sse[0] != "aws:kms" || f.sse[1] != "aws:kms" {
		t.Errorf("SSE phải đi kèm cả PUT lẫn COPY: %v", f.sse)
	}
}

func TestSessionToken(t *testing.T) {
	f := newFakeS3(t)
	f.token = "tok-123"
	s := f.store(t, func(c *Config) { c.SessionToken = "tok-123" })
	if _, err := s.Put(context.Background(), key(t, "a", "1"), strings.NewReader("x"), binarystore.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store(t, nil).Put(context.Background(), key(t, "a", "1"), strings.NewReader("x"), binarystore.PutOptions{}); err == nil {
		t.Error("thiếu token phải lỗi")
	}
}

func TestLargeSpoolsToTempFileAndCleansUp(t *testing.T) {
	f := newFakeS3(t)
	dir := t.TempDir()
	s := f.store(t, func(c *Config) { c.TempDir = dir })
	data := bytes.Repeat([]byte("0123456789abcdef"), (memSpool/16)*3)
	n, err := s.Put(context.Background(), key(t, "big", "1"), bytes.NewReader(data), binarystore.PutOptions{})
	if err != nil || n != int64(len(data)) {
		t.Fatalf("Put: %d %v", n, err)
	}
	if !bytes.Equal(f.objects["binary/big/1"], data) {
		t.Error("nội dung lệch")
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "*")); len(left) != 0 {
		t.Errorf("tệp tạm còn lại: %v", left)
	}
}

func TestSpoolBoundaries(t *testing.T) {
	f := newFakeS3(t)
	s := f.store(t, nil)
	ctx := context.Background()
	for _, size := range []int{0, 1, memSpool - 1, memSpool, memSpool + 1} {
		data := bytes.Repeat([]byte{'z'}, size)
		k := key(t, "b", "1")
		n, err := s.Put(ctx, k, bytes.NewReader(data), binarystore.PutOptions{})
		if err != nil || n != int64(size) || !bytes.Equal(f.objects["binary/b/1"], data) {
			t.Errorf("size %d: n=%d err=%v", size, n, err)
		}
	}
	// trần: đúng bằng thì qua, hơn một byte thì ErrTooLarge ở cả hai nhánh (bộ nhớ và tệp) và không gửi gì
	for _, max := range []int64{10, memSpool + 5} {
		f.objects = map[string][]byte{}
		before := len(f.calls)
		if _, err := s.Put(ctx, key(t, "m", "1"), bytes.NewReader(bytes.Repeat([]byte{'a'}, int(max))), binarystore.PutOptions{MaxBytes: max}); err != nil {
			t.Errorf("max %d đúng bằng: %v", max, err)
		}
		before = len(f.calls)
		if _, err := s.Put(ctx, key(t, "m", "2"), bytes.NewReader(bytes.Repeat([]byte{'a'}, int(max)+1)), binarystore.PutOptions{MaxBytes: max}); !errors.Is(err, binarystore.ErrTooLarge) {
			t.Errorf("max %d vượt 1: %v", max, err)
		}
		if len(f.calls) != before || f.objects["binary/m/2"] != nil {
			t.Errorf("max %d: không được gửi gì khi vượt trần", max)
		}
	}
	if left, _ := os.ReadDir(s.cfg.TempDir); len(left) != 0 {
		t.Errorf("tệp tạm còn lại: %d", len(left))
	}
}

func TestSpoolReadErrorLeavesNothing(t *testing.T) {
	f := newFakeS3(t)
	s := f.store(t, nil)
	boom := errors.New("boom")
	r := io.MultiReader(bytes.NewReader(bytes.Repeat([]byte{'a'}, memSpool+10)), errReader{boom})
	if _, err := s.Put(context.Background(), key(t, "e", "1"), r, binarystore.PutOptions{}); !errors.Is(err, boom) {
		t.Fatalf("err=%v", err)
	}
	if len(f.calls) != 0 {
		t.Error("lỗi đọc nguồn thì không được gửi PUT")
	}
	if left, _ := os.ReadDir(s.cfg.TempDir); len(left) != 0 {
		t.Errorf("tệp tạm còn lại: %d", len(left))
	}
}

type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }

func TestErrorMapping(t *testing.T) {
	f := newFakeS3(t)
	ctx := context.Background()
	k := key(t, "a", "1")

	// sai khóa: lỗi rõ ràng, không phải ErrNotFound, và không lộ secret
	bad := f.store(t, func(c *Config) { c.SecretAccessKey = "sai-secret-xyz" })
	_, err := bad.Open(ctx, k)
	if err == nil || errors.Is(err, binarystore.ErrNotFound) || !strings.Contains(err.Error(), "SignatureDoesNotMatch") {
		t.Fatalf("sai khóa: %v", err)
	}
	if strings.Contains(err.Error(), "sai-secret-xyz") || strings.Contains(err.Error(), testAK) {
		t.Errorf("lỗi lộ khóa: %v", err)
	}
	if err := bad.Delete(ctx, k); err == nil {
		t.Error("Delete với khóa sai phải lỗi")
	}

	// bucket không tồn tại: lỗi cấu hình, KHÔNG được coi là "không có khóa" (Delete cũng không được nuốt)
	nb := f.store(t, func(c *Config) { c.Bucket = "other-bucket" })
	if _, err := nb.Open(ctx, k); err == nil || errors.Is(err, binarystore.ErrNotFound) || !strings.Contains(err.Error(), "NoSuchBucket") {
		t.Errorf("NoSuchBucket khi Open: %v", err)
	}
	if err := nb.Delete(ctx, k); err == nil || !strings.Contains(err.Error(), "NoSuchBucket") {
		t.Errorf("NoSuchBucket khi Delete phải là lỗi: %v", err)
	}
	if err := nb.Copy(ctx, k, key(t, "a", "2")); err == nil || errors.Is(err, binarystore.ErrNotFound) {
		t.Errorf("NoSuchBucket khi Copy: %v", err)
	}
	if _, err := nb.Put(ctx, k, strings.NewReader("x"), binarystore.PutOptions{}); err == nil || !strings.Contains(err.Error(), "NoSuchBucket") {
		t.Errorf("NoSuchBucket khi Put: %v", err)
	}
}

func TestCopyOKBodyWithErrorIsError(t *testing.T) {
	f := newFakeS3(t)
	s := f.store(t, nil)
	f.hook = func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("X-Amz-Copy-Source") != "" {
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`<Error><Code>InternalError</Code><Message>We encountered an internal error.</Message></Error>`))
			return true
		}
		return false
	}
	if err := s.Copy(context.Background(), key(t, "a", "1"), key(t, "a", "2")); err == nil || !strings.Contains(err.Error(), "InternalError") {
		t.Fatalf("COPY 200 kèm <Error> phải là lỗi: %v", err)
	}
}

func TestEmptyNotFoundBodyIsNotFound(t *testing.T) {
	f := newFakeS3(t)
	s := f.store(t, nil)
	f.hook = func(w http.ResponseWriter, r *http.Request) bool { w.WriteHeader(404); return true }
	if _, err := s.Open(context.Background(), key(t, "a", "1")); !errors.Is(err, binarystore.ErrNotFound) {
		t.Errorf("Open: %v", err)
	}
	if err := s.Delete(context.Background(), key(t, "a", "1")); err != nil {
		t.Errorf("Delete: %v", err)
	}
}

func TestRedirectNotFollowed(t *testing.T) {
	other := newFakeS3(t)
	f := newFakeS3(t)
	f.hook = func(w http.ResponseWriter, r *http.Request) bool {
		http.Redirect(w, r, other.srv.URL+r.URL.Path, http.StatusTemporaryRedirect)
		return true
	}
	s := f.store(t, nil)
	_, err := s.Open(context.Background(), key(t, "a", "1"))
	if err == nil || !strings.Contains(err.Error(), "chuyển hướng 307") {
		t.Fatalf("err=%v", err)
	}
	if len(other.calls) != 0 {
		t.Error("không được theo redirect")
	}
	if _, err := s.Put(context.Background(), key(t, "a", "1"), strings.NewReader("x"), binarystore.PutOptions{}); err == nil {
		t.Error("PUT bị redirect phải lỗi")
	}
}

func TestErrorTextIsSanitized(t *testing.T) {
	f := newFakeS3(t)
	s := f.store(t, nil)
	f.hook = func(w http.ResponseWriter, r *http.Request) bool {
		s3Err(w, 500, "Boom", "line1\nline2\tx\x7fy"+strings.Repeat("a", 500))
		return true
	}
	_, err := s.Open(context.Background(), key(t, "a", "1"))
	if err == nil || !strings.Contains(err.Error(), "Boom: line1 line2 x y") {
		t.Fatalf("thông điệp phải được đọc và làm sạch: %v", err)
	}
	if strings.ContainsAny(err.Error(), "\n\t\x7f") || len(err.Error()) > 300 || strings.Contains(err.Error(), strings.Repeat("a", 201)) {
		t.Fatalf("err=%q", err)
	}
}

func TestDeleteAccepts200(t *testing.T) {
	f := newFakeS3(t)
	s := f.store(t, nil)
	f.hook = func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == http.MethodDelete {
			w.WriteHeader(200) // một số kho tương thích S3 trả 200
			return true
		}
		return false
	}
	if err := s.Delete(context.Background(), key(t, "a", "1")); err != nil {
		t.Fatal(err)
	}
}

func TestInvalidKeyNeverReachesNetwork(t *testing.T) {
	f := newFakeS3(t)
	s := f.store(t, nil)
	ctx := context.Background()
	for _, k := range []string{"", "binary/../x", "binary/a/../b", "x/a/1", "binary/a", "binary//1"} {
		if _, err := s.Put(ctx, k, strings.NewReader("x"), binarystore.PutOptions{}); !errors.Is(err, binarystore.ErrInvalidKey) {
			t.Errorf("Put %q: %v", k, err)
		}
		if _, err := s.Open(ctx, k); !errors.Is(err, binarystore.ErrInvalidKey) {
			t.Errorf("Open %q: %v", k, err)
		}
		if err := s.Delete(ctx, k); !errors.Is(err, binarystore.ErrInvalidKey) {
			t.Errorf("Delete %q: %v", k, err)
		}
		if err := s.Copy(ctx, k, key(t, "a", "1")); !errors.Is(err, binarystore.ErrInvalidKey) {
			t.Errorf("Copy src %q: %v", k, err)
		}
		if err := s.Copy(ctx, key(t, "a", "1"), k); !errors.Is(err, binarystore.ErrInvalidKey) {
			t.Errorf("Copy dst %q: %v", k, err)
		}
	}
	if _, err := s.Put(ctx, key(t, "a", "1"), strings.NewReader("x"), binarystore.PutOptions{ContentType: "application/x-msdownload"}); !errors.Is(err, binarystore.ErrBlockedContentType) {
		t.Errorf("loại bị chặn: %v", err)
	}
	if len(f.calls) != 0 {
		t.Errorf("không được gọi mạng: %v", f.calls)
	}
}

func TestCanceledContext(t *testing.T) {
	f := newFakeS3(t)
	s := f.store(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	k := key(t, "a", "1")
	if _, err := s.Put(ctx, k, strings.NewReader("x"), binarystore.PutOptions{}); !errors.Is(err, context.Canceled) {
		t.Errorf("Put: %v", err)
	}
	if _, err := s.Open(ctx, k); !errors.Is(err, context.Canceled) {
		t.Errorf("Open: %v", err)
	}
	if err := s.Copy(ctx, k, key(t, "a", "2")); !errors.Is(err, context.Canceled) {
		t.Errorf("Copy: %v", err)
	}
	if err := s.Delete(ctx, k); !errors.Is(err, context.Canceled) {
		t.Errorf("Delete: %v", err)
	}
	if len(f.calls) != 0 {
		t.Errorf("không được gọi mạng: %v", f.calls)
	}
}

func TestAuthorizationHeaderShape(t *testing.T) {
	f := newFakeS3(t)
	s := f.store(t, nil)
	var auth, hash string
	f.hook = func(w http.ResponseWriter, r *http.Request) bool {
		auth, hash = r.Header.Get("Authorization"), r.Header.Get("X-Amz-Content-Sha256")
		return false
	}
	fixed := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	s.cfg.Now = func() time.Time { return fixed }
	if _, err := s.Put(context.Background(), key(t, "a", "1"), strings.NewReader("hi"), binarystore.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(auth, "Credential="+testAK+"/20260102/"+testRegion+"/s3/aws4_request") || !strings.Contains(auth, "SignedHeaders=host;x-amz-content-sha256;x-amz-date") {
		t.Errorf("Authorization: %s", auth)
	}
	if hash != "8f434346648f6b96df89dda901c5176b10a6d83961dd3c1ac88b59b2dc327aa4" { // sha256("hi")
		t.Errorf("hash: %s", hash)
	}
}

func TestPathEncodingSingleOnce(t *testing.T) {
	f := newFakeS3(t)
	s := f.store(t, func(c *Config) { c.Prefix = "a.b/c_d/" })
	if _, err := s.Put(context.Background(), key(t, "x.y-1", "v.2"), strings.NewReader("x"), binarystore.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := f.calls[0]; got != "PUT /"+testBucket+"/a.b/c_d/binary/x.y-1/v.2" {
		t.Errorf("đường dẫn: %s", got)
	}
}

func TestListerContract(t *testing.T) {
	storetest.RunLister(t, func(t testing.TB) binarystore.Store {
		tt := t.(*testing.T)
		return newFakeS3(tt).store(tt, func(c *Config) { c.Prefix = "p/" })
	})
}

func TestListPaginatesAndFiltersForeignKeys(t *testing.T) {
	f := newFakeS3(t)
	f.pageSize = 2
	s := f.store(t, func(c *Config) { c.Prefix = "env/" })
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if _, err := s.Put(ctx, key(t, "a", fmt.Sprintf("v%d", i)), strings.NewReader("x"), binarystore.PutOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	f.objects["env/binary/bad/"] = []byte("x")      // khóa thiếu versionId
	f.objects["env/binary/a/b/c"] = []byte("x")     // lồng thêm
	f.objects["env/binary/we ird/v1"] = []byte("x") // id sai
	f.objects["other/binary/a/v9"] = []byte("x")    // ngoài prefix
	f.times = map[string]time.Time{"env/binary/a/v0": time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)}
	f.calls = nil
	got := map[string]time.Time{}
	if err := s.List(ctx, func(e binarystore.Entry) error { got[e.Key] = e.ModTime; return nil }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 5 || !got["binary/a/v0"].Equal(time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)) {
		t.Fatalf("got %v", got)
	}
	if len(f.calls) < 3 {
		t.Errorf("phải phân trang (>=3 yêu cầu): %v", f.calls)
	}
}

func TestListErrors(t *testing.T) {
	ctx := context.Background()
	f := newFakeS3(t)
	s := f.store(t, nil)
	bad := f.store(t, func(c *Config) { c.SecretAccessKey = "x" })
	if err := bad.List(ctx, func(binarystore.Entry) error { return nil }); err == nil || !strings.Contains(err.Error(), "SignatureDoesNotMatch") {
		t.Errorf("sai khóa: %v", err)
	}
	for name, body := range map[string]string{
		"xml hỏng":         `<ListBucketResult><Contents>`,
		"thời gian hỏng":   `<ListBucketResult><IsTruncated>false</IsTruncated><Contents><Key>binary/a/v1</Key><Size>1</Size><LastModified>hôm qua</LastModified></Contents></ListBucketResult>`,
		"không tiến triển": `<ListBucketResult><IsTruncated>true</IsTruncated></ListBucketResult>`,
	} {
		body := body
		f.hook = func(w http.ResponseWriter, r *http.Request) bool { _, _ = w.Write([]byte(body)); return true }
		if err := s.List(ctx, func(binarystore.Entry) error { return nil }); err == nil {
			t.Errorf("%s: phải lỗi", name)
		}
	}
	f.hook = func(w http.ResponseWriter, r *http.Request) bool {
		_, _ = w.Write([]byte(`<ListBucketResult><IsTruncated>true</IsTruncated><NextContinuationToken>same</NextContinuationToken></ListBucketResult>`))
		return true
	}
	if err := s.List(ctx, func(binarystore.Entry) error { return nil }); err == nil || !strings.Contains(err.Error(), "tiến triển") {
		t.Errorf("token lặp: %v", err)
	}
}
