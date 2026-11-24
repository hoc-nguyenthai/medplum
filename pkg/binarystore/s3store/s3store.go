// Package s3store là adapter binarystore.Store cho S3 và kho tương thích S3 (MinIO, Ceph, R2...). Chỉ dùng stdlib: tự ký SigV4.
//
// Khóa đối tượng là Prefix + "binary/<id>/<versionId>". Put đệm nội dung (bộ nhớ, tràn ra tệp tạm) để tính SHA-256 và Content-Length
// trước khi gửi một yêu cầu PUT duy nhất, nên ghi là nguyên tử (S3 chỉ hiện đối tượng khi nhận đủ) và S3 tự kiểm toàn vẹn. Một PUT/COPY
// tối đa 5 GiB. Không theo redirect (sai vùng là lỗi cấu hình, không gửi khóa tới nơi khác). Không hỗ trợ khóa tạm thời từ IMDS/STS.
package s3store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"trivita-fhir/pkg/binarystore"
)

// maxSingleObject là trần của một PUT/COPY đơn trên S3.
const maxSingleObject = 5 << 30

// memSpool: dưới ngưỡng này nội dung đệm trong bộ nhớ, trên ngưỡng thì ra tệp tạm.
const memSpool = 4 << 20

var (
	bucketPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.\-]{1,61}[a-z0-9]$`)
	prefixPattern = regexp.MustCompile(`^[A-Za-z0-9\-._]+(/[A-Za-z0-9\-._]+)*/$`)
	regionPattern = regexp.MustCompile(`^[a-z0-9\-]{2,32}$`)
)

// Config là cấu hình của kho. Khóa truy cập không bao giờ vào log hay thông báo lỗi.
type Config struct {
	Bucket string
	// Prefix (tùy chọn) dạng "a/b/" đặt trước khóa; chỉ A-Za-z0-9 - . _ và '/'.
	Prefix string
	Region string
	// Endpoint (tùy chọn) là gốc http(s) của kho tương thích S3 (dùng kiểu path: <endpoint>/<bucket>/<key>). Trống: AWS, kiểu virtual-hosted.
	Endpoint string

	AccessKeyID, SecretAccessKey, SessionToken string

	// ServerSideEncryption: "", "AES256" hoặc "aws:kms" (khóa KMS mặc định của bucket).
	ServerSideEncryption string

	// TempDir là nơi đặt tệp tạm khi tải lớn (mặc định os.TempDir()).
	TempDir string
	// HTTPClient (tùy chọn) thay client mặc định (test). Client mặc định không theo redirect.
	HTTPClient *http.Client
	// Now (tùy chọn) thay đồng hồ (test).
	Now func() time.Time
}

// Store là binarystore.Store trên S3.
type Store struct {
	cfg    Config
	creds  credentials
	base   *url.URL // gốc yêu cầu (kèm "/<bucket>" khi kiểu path)
	client *http.Client
}

var _ binarystore.Store = (*Store)(nil)

// New kiểm cấu hình và dựng Store (không gọi mạng).
func New(cfg Config) (*Store, error) {
	if !bucketPattern.MatchString(cfg.Bucket) {
		return nil, errors.New("s3store: tên bucket không hợp lệ")
	}
	if cfg.Prefix != "" && !prefixPattern.MatchString(cfg.Prefix) {
		return nil, errors.New(`s3store: prefix phải có dạng "a/b/" (A-Za-z0-9 - . _ và '/', kết thúc bằng '/')`)
	}
	if strings.Contains("/"+cfg.Prefix, "/./") || strings.Contains("/"+cfg.Prefix, "/../") {
		return nil, errors.New("s3store: prefix không được chứa . hoặc .. làm thành phần")
	}
	if !regionPattern.MatchString(cfg.Region) {
		return nil, errors.New("s3store: region không hợp lệ")
	}
	if cfg.AccessKeyID == "" || cfg.SecretAccessKey == "" {
		return nil, errors.New("s3store: thiếu khóa truy cập")
	}
	switch cfg.ServerSideEncryption {
	case "", "AES256", "aws:kms":
	default:
		return nil, errors.New(`s3store: ServerSideEncryption chỉ nhận "", "AES256" hoặc "aws:kms"`)
	}
	var base *url.URL
	if cfg.Endpoint == "" {
		base = &url.URL{Scheme: "https", Host: cfg.Bucket + ".s3." + cfg.Region + ".amazonaws.com"}
		if strings.Contains(cfg.Bucket, ".") { // chứng chỉ wildcard không khớp host có dấu chấm: dùng kiểu path
			base = &url.URL{Scheme: "https", Host: "s3." + cfg.Region + ".amazonaws.com", Path: "/" + cfg.Bucket}
		}
	} else {
		u, err := url.Parse(cfg.Endpoint)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.Trim(u.Path, "/") != "" {
			return nil, errors.New("s3store: endpoint phải là http(s)://host[:port] tuyệt đối, không đường dẫn, query, fragment, userinfo")
		}
		base = &url.URL{Scheme: u.Scheme, Host: u.Host, Path: "/" + cfg.Bucket}
	}
	cl := cfg.HTTPClient
	if cl == nil {
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.DialContext = (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext
		tr.ResponseHeaderTimeout = 60 * time.Second
		cl = &http.Client{Transport: tr}
	}
	c := *cl
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Store{cfg: cfg, creds: credentials{cfg.AccessKeyID, cfg.SecretAccessKey, cfg.SessionToken}, base: base, client: &c}, nil
}

// objectPath trả đường dẫn đã mã hóa của khóa (đúng một lần).
func (s *Store) objectPath(key string) string {
	return strings.TrimRight(s.base.EscapedPath(), "/") + "/" + uriEncode(s.cfg.Prefix+key, true)
}

func (s *Store) newRequest(ctx context.Context, method, key string, body io.Reader, size int64) (*http.Request, error) {
	u := *s.base
	u.Path, u.RawPath = "", ""
	u.Opaque = ""
	ep := s.objectPath(key)
	raw, err := url.PathUnescape(ep)
	if err != nil {
		return nil, binarystore.ErrInvalidKey
	}
	u.Path, u.RawPath = raw, ep
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.ContentLength = size
	}
	if s.cfg.ServerSideEncryption != "" && (method == http.MethodPut) {
		req.Header.Set("X-Amz-Server-Side-Encryption", s.cfg.ServerSideEncryption)
	}
	return req, nil
}

func (s *Store) do(req *http.Request, payloadHash string) (*http.Response, error) {
	sign(req, s.creds, s.cfg.Region, payloadHash, s.cfg.Now())
	resp, err := s.client.Do(req)
	if err != nil {
		// url.Error chứa URL; không có khóa nhưng cắt bớt cho gọn và tránh lộ tên bucket/host không cần thiết.
		var ue *url.Error
		if errors.As(err, &ue) {
			return nil, fmt.Errorf("s3store: %s: %w", req.Method, ue.Err)
		}
		return nil, fmt.Errorf("s3store: %s: %w", req.Method, err)
	}
	return resp, nil
}

type s3Error struct {
	Code    string `xml:"Code"`
	Message string `xml:"Message"`
}

// apiError đọc thân lỗi (tối đa 64 KiB) và dựng lỗi; resp.Body được đóng.
func apiError(op string, resp *http.Response) error {
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var e s3Error
	_ = xml.Unmarshal(b, &e)
	code := sanitize(e.Code)
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return fmt.Errorf("s3store: %s: chuyển hướng %d (sai region hoặc endpoint?)", op, resp.StatusCode)
	}
	if code == "" {
		return fmt.Errorf("s3store: %s: HTTP %d", op, resp.StatusCode)
	}
	return fmt.Errorf("s3store: %s: HTTP %d %s: %s", op, resp.StatusCode, code, sanitize(e.Message))
}

// sanitize giữ thông điệp ngắn, in được, không xuống dòng (đi vào log).
func sanitize(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)
	if len(s) > 200 {
		s = s[:200]
	}
	return strings.TrimSpace(s)
}

// notFound: 404 với NoSuchKey (hoặc thân rỗng). NoSuchBucket... là lỗi cấu hình chứ không phải "khóa không có".
func notFound(status int, body []byte) bool {
	if status != http.StatusNotFound {
		return false
	}
	var e s3Error
	_ = xml.Unmarshal(body, &e)
	return e.Code == "" || e.Code == "NoSuchKey"
}

// spool đệm r tới tối đa max byte (max > 0), trả nội dung kèm SHA-256 và độ dài.
type spooled struct {
	r    io.ReadSeeker
	n    int64
	hash string
	done func()
}

func (s *Store) spool(ctx context.Context, r io.Reader, max int64) (*spooled, error) {
	src := io.Reader(ctxReader{ctx, r})
	limit := int64(maxSingleObject)
	if max > 0 && max < limit {
		limit = max
	}
	src = io.LimitReader(src, limit+1)
	h := sha256.New()
	var buf bytes.Buffer
	_, err := io.CopyN(io.MultiWriter(&buf, h), src, memSpool+1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if errors.Is(err, io.EOF) { // gọn trong bộ nhớ
		if int64(buf.Len()) > limit {
			return nil, binarystore.ErrTooLarge
		}
		return &spooled{r: bytes.NewReader(buf.Bytes()), n: int64(buf.Len()), hash: hex.EncodeToString(h.Sum(nil)), done: func() {}}, nil
	}
	f, err := os.CreateTemp(s.cfg.TempDir, "s3store-*")
	if err != nil {
		return nil, err
	}
	done := func() { _ = f.Close(); _ = os.Remove(f.Name()) }
	fail := func(err error) (*spooled, error) { done(); return nil, err }
	if _, err := f.Write(buf.Bytes()); err != nil {
		return fail(err)
	}
	m, err := io.Copy(io.MultiWriter(f, h), src)
	if err != nil {
		return fail(err)
	}
	total := int64(buf.Len()) + m
	if total > limit {
		return fail(binarystore.ErrTooLarge)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return fail(err)
	}
	return &spooled{r: f, n: total, hash: hex.EncodeToString(h.Sum(nil)), done: done}, nil
}

type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

// Put đệm rồi gửi một PUT. Vượt MaxBytes (hoặc 5 GiB) là ErrTooLarge và không gửi gì.
func (s *Store) Put(ctx context.Context, key string, r io.Reader, opts binarystore.PutOptions) (int64, error) {
	if _, _, err := binarystore.SplitKey(key); err != nil {
		return 0, err
	}
	if opts.ContentType != "" {
		if err := binarystore.CheckMetadata("", opts.ContentType); err != nil {
			return 0, err
		}
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	sp, err := s.spool(ctx, r, opts.MaxBytes)
	if err != nil {
		return 0, err
	}
	defer sp.done()
	req, err := s.newRequest(ctx, http.MethodPut, key, sp.r, sp.n)
	if err != nil {
		return 0, err
	}
	resp, err := s.do(req, sp.hash)
	if err != nil {
		return 0, err
	}
	if resp.StatusCode != http.StatusOK {
		return 0, apiError("PUT", resp)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()
	return sp.n, nil
}

// Open gửi GET; người gọi đóng thân phản hồi.
func (s *Store) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	rc, _, err := s.open(ctx, key, "")
	return rc, err
}

// OpenRange: xem binarystore.RangeOpener. Dùng GET với header Range (S3 trả 206; 416 khi offset không nhỏ hơn kích thước nội dung).
func (s *Store) OpenRange(ctx context.Context, key string, offset, length int64) (io.ReadCloser, error) {
	if offset < 0 || length <= 0 {
		if _, _, err := binarystore.SplitKey(key); err != nil {
			return nil, err
		}
		return nil, binarystore.ErrInvalidRange
	}
	rc, status, err := s.open(ctx, key, fmt.Sprintf("bytes=%d-%d", offset, offset+length-1))
	if err != nil || status == http.StatusPartialContent {
		return rc, err
	}
	// máy chủ bỏ qua Range và trả cả nội dung (200): tự bỏ qua offset và cắt độ dài, để không bao giờ trả sai byte
	if _, err := io.CopyN(io.Discard, rc, offset); err != nil {
		_ = rc.Close()
		if err == io.EOF {
			return nil, binarystore.ErrInvalidRange
		}
		return nil, err
	}
	first := make([]byte, 1)
	if _, err := io.ReadFull(rc, first); err != nil { // không còn byte nào từ offset: offset không nhỏ hơn kích thước nội dung
		_ = rc.Close()
		if err == io.EOF {
			return nil, binarystore.ErrInvalidRange
		}
		return nil, err
	}
	return limitedBody{io.LimitReader(io.MultiReader(bytes.NewReader(first), rc), length), rc}, nil
}

type limitedBody struct {
	io.Reader
	c io.Closer
}

func (l limitedBody) Close() error { return l.c.Close() }

var _ binarystore.RangeOpener = (*Store)(nil)

func (s *Store) open(ctx context.Context, key, rng string) (io.ReadCloser, int, error) {
	if _, _, err := binarystore.SplitKey(key); err != nil {
		return nil, 0, err
	}
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	req, err := s.newRequest(ctx, http.MethodGet, key, nil, 0)
	if err != nil {
		return nil, 0, err
	}
	if rng != "" {
		req.Header.Set("Range", rng)
	}
	resp, err := s.do(req, emptySHA256)
	if err != nil {
		return nil, 0, err
	}
	if resp.StatusCode == http.StatusOK || (rng != "" && resp.StatusCode == http.StatusPartialContent) {
		return resp.Body, resp.StatusCode, nil
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()
	if notFound(resp.StatusCode, b) {
		return nil, 0, binarystore.ErrNotFound
	}
	if rng != "" && resp.StatusCode == http.StatusRequestedRangeNotSatisfiable {
		return nil, 0, binarystore.ErrInvalidRange
	}
	resp.Body = io.NopCloser(bytes.NewReader(b))
	return nil, 0, apiError("GET", resp)
}

// Copy dùng CopyObject phía máy chủ. S3 có thể trả 200 kèm thân <Error>, nên thân luôn được kiểm.
func (s *Store) Copy(ctx context.Context, srcKey, dstKey string) error {
	if _, _, err := binarystore.SplitKey(srcKey); err != nil {
		return err
	}
	if _, _, err := binarystore.SplitKey(dstKey); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	req, err := s.newRequest(ctx, http.MethodPut, dstKey, nil, 0)
	if err != nil {
		return err
	}
	req.Header.Set("X-Amz-Copy-Source", "/"+s.cfg.Bucket+"/"+uriEncode(s.cfg.Prefix+srcKey, true))
	resp, err := s.do(req, emptySHA256)
	if err != nil {
		return err
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		var e s3Error
		if xml.Unmarshal(b, &e) == nil && e.Code != "" {
			return fmt.Errorf("s3store: COPY: %s: %s", sanitize(e.Code), sanitize(e.Message))
		}
		return nil
	}
	if resp.StatusCode == http.StatusNotFound {
		var e s3Error
		_ = xml.Unmarshal(b, &e)
		if e.Code == "NoSuchKey" {
			return binarystore.ErrNotFound
		}
	}
	resp.Body = io.NopCloser(bytes.NewReader(b))
	return apiError("COPY", resp)
}

// Delete gửi DELETE; S3 trả 204 cả khi khóa không có (idempotent).
func (s *Store) Delete(ctx context.Context, key string) error {
	if _, _, err := binarystore.SplitKey(key); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	req, err := s.newRequest(ctx, http.MethodDelete, key, nil, 0)
	if err != nil {
		return err
	}
	resp, err := s.do(req, emptySHA256)
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusOK {
		_ = resp.Body.Close()
		return nil
	}
	if resp.StatusCode == http.StatusNotFound {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		_ = resp.Body.Close()
		if notFound(resp.StatusCode, b) {
			return nil
		}
		resp.Body = io.NopCloser(bytes.NewReader(b))
	}
	return apiError("DELETE", resp)
}

var _ binarystore.Lister = (*Store)(nil)

type listResult struct {
	IsTruncated           bool   `xml:"IsTruncated"`
	NextContinuationToken string `xml:"NextContinuationToken"`
	Contents              []struct {
		Key          string `xml:"Key"`
		Size         int64  `xml:"Size"`
		LastModified string `xml:"LastModified"`
	} `xml:"Contents"`
}

// List duyệt ListObjectsV2 dưới Prefix+"binary/" theo trang 1000 khóa. Khóa không đúng dạng binary/<id>/<versionId> bị bỏ qua.
// Đối tượng được ghi đồng thời có thể xuất hiện hoặc không (S3 nhất quán mạnh cho từng trang, không cho cả lượt duyệt).
func (s *Store) List(ctx context.Context, fn func(binarystore.Entry) error) error {
	token := ""
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		q := url.Values{"list-type": {"2"}, "prefix": {s.cfg.Prefix + "binary/"}, "max-keys": {"1000"}}
		if token != "" {
			q.Set("continuation-token", token)
		}
		u := *s.base
		bp := strings.TrimRight(s.base.EscapedPath(), "/")
		if bp == "" {
			bp = "/"
		}
		raw, _ := url.PathUnescape(bp)
		u.Path, u.RawPath, u.RawQuery = raw, bp, canonicalQuery(q) // RawQuery trùng đúng chuỗi đã ký
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return err
		}
		resp, err := s.do(req, emptySHA256)
		if err != nil {
			return err
		}
		if resp.StatusCode != http.StatusOK {
			return apiError("LIST", resp)
		}
		var res listResult
		err = xml.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(&res)
		_ = resp.Body.Close()
		if err != nil {
			return fmt.Errorf("s3store: LIST: thân phản hồi không đọc được: %w", err)
		}
		for _, c := range res.Contents {
			key, ok := strings.CutPrefix(c.Key, s.cfg.Prefix)
			if !ok {
				continue
			}
			if _, _, err := binarystore.SplitKey(key); err != nil {
				continue
			}
			mt, err := time.Parse(time.RFC3339, c.LastModified)
			if err != nil {
				return fmt.Errorf("s3store: LIST: LastModified không hợp lệ cho %q", key)
			}
			if err := fn(binarystore.Entry{Key: key, Size: c.Size, ModTime: mt}); err != nil {
				return err
			}
		}
		if !res.IsTruncated {
			return nil
		}
		if res.NextContinuationToken == "" || res.NextContinuationToken == token {
			return errors.New("s3store: LIST: phân trang không tiến triển")
		}
		token = res.NextContinuationToken
	}
}
