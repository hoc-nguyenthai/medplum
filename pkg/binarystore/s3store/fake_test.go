package s3store

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	testBucket = "test-bucket"
	testAK     = "AKIDEXAMPLE"
	testSK     = "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY"
	testRegion = "eu-west-1"
)

// fakeS3 là máy chủ S3 tối thiểu kiểu path: xác thực SigV4 (dựng lại chữ ký từ yêu cầu nhận được) và kiểm x-amz-content-sha256.
type fakeS3 struct {
	t   *testing.T
	srv *httptest.Server

	mu       sync.Mutex
	objects  map[string][]byte // khóa đầy đủ: path đã giải mã sau "/<bucket>/"
	calls    []string
	sse      []string // giá trị X-Amz-Server-Side-Encryption của các PUT
	pageSize int      // 0: 1000
	times    map[string]time.Time
	token    string // nếu đặt: bắt buộc X-Amz-Security-Token
	hook     func(w http.ResponseWriter, r *http.Request) bool
	// ignoreRange: giả lập máy chủ/proxy bỏ qua header Range và trả 200 với cả nội dung
	ignoreRange bool
}

func newFakeS3(t *testing.T) *fakeS3 {
	f := &fakeS3{t: t, objects: map[string][]byte{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeS3) store(t *testing.T, mod func(*Config)) *Store {
	cfg := Config{Bucket: testBucket, Region: testRegion, Endpoint: f.srv.URL, AccessKeyID: testAK, SecretAccessKey: testSK, TempDir: t.TempDir()}
	if mod != nil {
		mod(&cfg)
	}
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func s3Err(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?><Error><Code>%s</Code><Message>%s</Message></Error>`, code, msg)
}

func (f *fakeS3) authOK(r *http.Request) bool {
	date, err := time.Parse("20060102T150405Z", r.Header.Get("X-Amz-Date"))
	if err != nil {
		return false
	}
	c := credentials{AccessKeyID: testAK, SecretAccessKey: testSK, SessionToken: f.token}
	u := &url.URL{Path: r.URL.Path, RawPath: r.URL.RawPath, RawQuery: r.URL.RawQuery}
	re, _ := http.NewRequest(r.Method, "http://"+r.Host+u.String(), nil)
	re.Host = r.Host
	for k, v := range r.Header {
		if strings.EqualFold(k, "Authorization") || strings.EqualFold(k, "X-Amz-Date") || strings.EqualFold(k, "X-Amz-Content-Sha256") || strings.EqualFold(k, "X-Amz-Security-Token") {
			continue
		}
		re.Header[k] = v
	}
	sign(re, c, testRegion, r.Header.Get("X-Amz-Content-Sha256"), date)
	return re.Header.Get("Authorization") == r.Header.Get("Authorization")
}

func (f *fakeS3) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, r.Method+" "+r.URL.EscapedPath())
	if f.hook != nil && f.hook(w, r) {
		return
	}
	if !f.authOK(r) {
		s3Err(w, 403, "SignatureDoesNotMatch", "bad signature")
		return
	}
	if f.token != "" && r.Header.Get("X-Amz-Security-Token") != f.token {
		s3Err(w, 403, "InvalidToken", "token")
		return
	}
	if r.Method == http.MethodGet && r.URL.Path == "/"+testBucket && r.URL.Query().Get("list-type") == "2" {
		f.list(w, r)
		return
	}
	rest, ok := strings.CutPrefix(r.URL.Path, "/"+testBucket+"/")
	if !ok {
		s3Err(w, 404, "NoSuchBucket", "The specified bucket does not exist")
		return
	}
	switch r.Method {
	case http.MethodPut:
		if src := r.Header.Get("X-Amz-Copy-Source"); src != "" {
			p, _ := url.PathUnescape(src)
			b, ok := f.objects[strings.TrimPrefix(p, "/"+testBucket+"/")]
			if !ok {
				s3Err(w, 404, "NoSuchKey", "src")
				return
			}
			f.objects[rest] = append([]byte(nil), b...)
			f.sse = append(f.sse, r.Header.Get("X-Amz-Server-Side-Encryption"))
			fmt.Fprint(w, `<CopyObjectResult><ETag>x</ETag></CopyObjectResult>`)
			return
		}
		b, _ := io.ReadAll(r.Body)
		h := sha256.Sum256(b)
		if hex.EncodeToString(h[:]) != r.Header.Get("X-Amz-Content-Sha256") || int64(len(b)) != r.ContentLength {
			s3Err(w, 400, "XAmzContentSHA256Mismatch", "mismatch")
			return
		}
		f.objects[rest] = b
		f.sse = append(f.sse, r.Header.Get("X-Amz-Server-Side-Encryption"))
	case http.MethodGet:
		b, ok := f.objects[rest]
		if !ok {
			s3Err(w, 404, "NoSuchKey", "The specified key does not exist.")
			return
		}
		if rng := r.Header.Get("Range"); rng != "" && !f.ignoreRange {
			var from, to int64
			if _, err := fmt.Sscanf(rng, "bytes=%d-%d", &from, &to); err != nil {
				s3Err(w, 400, "InvalidArgument", "range")
				return
			}
			if from >= int64(len(b)) {
				s3Err(w, 416, "InvalidRange", "The requested range is not satisfiable")
				return
			}
			to = min(to, int64(len(b))-1)
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", from, to, len(b)))
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(b[from : to+1])
			return
		}
		_, _ = w.Write(b)
	case http.MethodDelete:
		delete(f.objects, rest)
		w.WriteHeader(204)
	default:
		s3Err(w, 405, "MethodNotAllowed", "x")
	}
}

// list trả ListObjectsV2 có phân trang bằng continuation-token (là chỉ số bắt đầu).
func (f *fakeS3) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	prefix := q.Get("prefix")
	var keys []string
	for k := range f.objects {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	size := f.pageSize
	if size <= 0 {
		size = 1000
	}
	start := 0
	if tk := q.Get("continuation-token"); tk != "" {
		fmt.Sscanf(tk, "t%d", &start)
	}
	end := start + size
	trunc := end < len(keys)
	if end > len(keys) {
		end = len(keys)
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<?xml version="1.0"?><ListBucketResult><Prefix>%s</Prefix><IsTruncated>%v</IsTruncated>`, prefix, trunc)
	if trunc {
		fmt.Fprintf(&b, "<NextContinuationToken>t%d</NextContinuationToken>", end)
	}
	for _, k := range keys[start:end] {
		mt := time.Now().UTC()
		if t, ok := f.times[k]; ok {
			mt = t.UTC()
		}
		fmt.Fprintf(&b, "<Contents><Key>%s</Key><Size>%d</Size><LastModified>%s</LastModified></Contents>", k, len(f.objects[k]), mt.Format("2006-01-02T15:04:05.000Z"))
	}
	b.WriteString("</ListBucketResult>")
	w.Header().Set("Content-Type", "application/xml")
	_, _ = w.Write([]byte(b.String()))
}
