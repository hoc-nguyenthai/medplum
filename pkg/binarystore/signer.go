package binarystore

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var (
	// ErrMissingSignature: URL không có Signature (HTTP 401).
	ErrMissingSignature = errors.New("binarystore: missing signature")
	// ErrExpired: thiếu/sai Expires hoặc đã quá hạn (HTTP 410 như Medplum).
	ErrExpired = errors.New("binarystore: URL has expired")
	// ErrBadSignature: chữ ký không khớp, query trùng tham số, hay phương thức không hỗ trợ (HTTP 401).
	ErrBadSignature = errors.New("binarystore: invalid signature")
	// ErrBadPath: đường dẫn thô không đúng dạng <tiền tố>/<id>[/<versionId>] (HTTP 401).
	ErrBadPath = errors.New("binarystore: invalid path")
	// ErrNoSigningKey: Signer chỉ có khóa xác minh nên không ký được.
	ErrNoSigningKey = errors.New("binarystore: no signing key")
)

// Clock cho phép test đặt giờ.
type Clock interface{ Now() time.Time }

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

const (
	// DefaultTTL là hạn URL ký (như Medplum); MaxTTL là trần cấu hình.
	DefaultTTL = time.Hour
	MaxTTL     = 24 * time.Hour
	minKeyBits = 2048
)

// SignerConfig cấu hình Signer.
//   - BaseURL: địa chỉ công khai của route lưu trữ, ví dụ https://api.example.com/storage (không query, không userinfo).
//   - Key: khóa ký (nil: chỉ xác minh). VerifyKeys: khóa công khai bổ sung để xoay khóa; khóa công khai của Key luôn được thử.
//   - TTL: hạn URL (0: DefaultTTL, tối đa MaxTTL). LegacyGET: GET chấp nhận thêm chữ ký không có tiền tố "GET " (URL cũ của Medplum).
type SignerConfig struct {
	BaseURL    string
	Key        *rsa.PrivateKey
	VerifyKeys []*rsa.PublicKey
	TTL        time.Duration
	Clock      Clock
	LegacyGET  bool
}

// Claims là nội dung đã xác minh của một URL ký; việc đọc Binary và đối chiếu ProjectID thuộc bên gọi.
type Claims struct {
	ID, VersionID, ProjectID, Method string
	Expires                          time.Time
}

// Signer ký và xác minh URL RSA-SHA256 tương thích Medplum: chuỗi ký là "<GET|PUT> <url không có Signature>".
type Signer struct {
	origin, basePath string // "https://host", "/storage"
	key              *rsa.PrivateKey
	pubs             []*rsa.PublicKey
	ttl              time.Duration
	clock            Clock
	legacyGET        bool
}

// NewSigner kiểm cấu hình (lỗi khởi động rõ ràng thay vì lỗi lúc chạy): BaseURL, khóa ≥ 2048 bit, TTL trong khoảng.
func NewSigner(c SignerConfig) (*Signer, error) {
	u, err := url.Parse(c.BaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.Contains(c.BaseURL, "?") || strings.Contains(c.BaseURL, "#") {
		return nil, errors.New("binarystore: BaseURL phải là URL http(s) tuyệt đối, không query/fragment/userinfo")
	}
	s := &Signer{origin: u.Scheme + "://" + u.Host, basePath: strings.TrimRight(u.EscapedPath(), "/"), ttl: c.TTL, clock: c.Clock, legacyGET: c.LegacyGET}
	if s.ttl == 0 {
		s.ttl = DefaultTTL
	}
	if s.ttl < 0 || s.ttl > MaxTTL {
		return nil, fmt.Errorf("binarystore: TTL phải trong (0, %s]", MaxTTL)
	}
	if s.clock == nil {
		s.clock = systemClock{}
	}
	if c.Key != nil {
		if c.Key.N.BitLen() < minKeyBits {
			return nil, fmt.Errorf("binarystore: khóa ký phải từ %d bit", minKeyBits)
		}
		s.key = c.Key
		s.pubs = append(s.pubs, &c.Key.PublicKey)
	}
	for _, k := range c.VerifyKeys {
		if k == nil || k.N.BitLen() < minKeyBits {
			return nil, fmt.Errorf("binarystore: khóa xác minh phải từ %d bit", minKeyBits)
		}
		s.pubs = append(s.pubs, k)
	}
	if len(s.pubs) == 0 {
		return nil, errors.New("binarystore: cần ít nhất một khóa (ký hoặc xác minh)")
	}
	return s, nil
}

func checkMethod(m string) bool { return m == http.MethodGet || m == http.MethodPut }

// Sign trả URL ký: <BaseURL>/<id>/<versionID>?Expires=<giây unix>[&Project=<id>]&Signature=<base64>.
// Thứ tự tham số cố định (Expires, Project, Signature) vì chuỗi ký chính là URL đứng trước Signature.
func (s *Signer) Sign(method, id, versionID, projectID string) (string, error) {
	if s.key == nil {
		return "", ErrNoSigningKey
	}
	if !checkMethod(method) {
		return "", fmt.Errorf("binarystore: phương thức %q không được ký", method)
	}
	if !validID(id) || !validID(versionID) || (projectID != "" && !validID(projectID)) {
		return "", ErrInvalidKey
	}
	base := s.origin + s.basePath + "/" + id + "/" + versionID
	q := "Expires=" + strconv.FormatInt(s.clock.Now().Add(s.ttl).Unix(), 10)
	if projectID != "" {
		q += "&Project=" + url.QueryEscape(projectID)
	}
	h := sha256.Sum256([]byte(method + " " + base + "?" + q))
	sig, err := rsa.SignPKCS1v15(rand.Reader, s.key, crypto.SHA256, h[:])
	if err != nil {
		return "", err
	}
	return base + "?" + q + "&Signature=" + url.QueryEscape(base64.StdEncoding.EncodeToString(sig)), nil
}

type rawParam struct{ key, raw string }

// splitQuery tách query thô giữ nguyên thứ tự và dạng mã hóa; từ chối khóa không giải mã được.
func splitQuery(q string) ([]rawParam, error) {
	if q == "" {
		return nil, nil
	}
	var out []rawParam
	for _, part := range strings.Split(q, "&") {
		k, _, _ := strings.Cut(part, "=")
		dk, err := url.QueryUnescape(k)
		if err != nil {
			return nil, ErrBadSignature
		}
		out = append(out, rawParam{dk, part})
	}
	return out, nil
}

func paramValue(p rawParam) (string, error) {
	_, v, _ := strings.Cut(p.raw, "=")
	d, err := url.QueryUnescape(v)
	if err != nil {
		return "", ErrBadSignature
	}
	return d, nil
}

func requestPath(r *http.Request) string {
	if ru := r.RequestURI; strings.HasPrefix(ru, "/") {
		p, _, _ := strings.Cut(ru, "?")
		return p
	}
	return r.URL.EscapedPath()
}

// Verify kiểm yêu cầu theo thứ tự: Signature → Expires/hạn → đường dẫn thô → chữ ký. Trả Claims khi hợp lệ.
// Chữ ký gắn với gốc (scheme+host) và tiền tố cấu hình ở BaseURL, không lấy từ Host của yêu cầu, để không phụ thuộc cấu hình proxy
// (proxy không được viết lại đường dẫn).
func (s *Signer) Verify(method string, r *http.Request) (Claims, error) {
	if !checkMethod(method) {
		return Claims{}, ErrBadSignature
	}
	params, err := splitQuery(r.URL.RawQuery)
	if err != nil {
		return Claims{}, err
	}
	var sigP, expP, projP *rawParam
	var kept []string
	for i := range params {
		p := &params[i]
		slot := map[string]**rawParam{"Signature": &sigP, "Expires": &expP, "Project": &projP}[p.key]
		if slot != nil {
			if *slot != nil {
				return Claims{}, ErrBadSignature // tham số ký trùng lặp: mơ hồ
			}
			*slot = p
		}
		if p.key != "Signature" {
			kept = append(kept, p.raw)
		}
	}
	if sigP == nil {
		return Claims{}, ErrMissingSignature
	}
	sigV, err := paramValue(*sigP)
	if err != nil || sigV == "" {
		return Claims{}, ErrMissingSignature
	}
	if expP == nil {
		return Claims{}, ErrExpired
	}
	expV, err := paramValue(*expP)
	exp, perr := strconv.ParseInt(expV, 10, 64)
	if err != nil || perr != nil {
		return Claims{}, ErrExpired
	}
	if s.clock.Now().Unix() > exp {
		return Claims{}, ErrExpired
	}
	path := requestPath(r)
	rest, ok := strings.CutPrefix(path, s.basePath+"/")
	if !ok {
		return Claims{}, ErrBadPath
	}
	segs := strings.Split(rest, "/")
	if len(segs) < 1 || len(segs) > 2 || !validID(segs[0]) || (len(segs) == 2 && !validID(segs[1])) {
		return Claims{}, ErrBadPath
	}
	claims := Claims{ID: segs[0], Method: method, Expires: time.Unix(exp, 0)}
	if len(segs) == 2 {
		claims.VersionID = segs[1]
	}
	if projP != nil {
		if claims.ProjectID, err = paramValue(*projP); err != nil {
			return Claims{}, err
		}
	}
	sig, err := base64.StdEncoding.DecodeString(sigV)
	if err != nil {
		return Claims{}, ErrBadSignature
	}
	signed := s.origin + path + "?" + strings.Join(kept, "&")
	candidates := []string{method + " " + signed}
	if method == http.MethodGet && s.legacyGET {
		candidates = append(candidates, signed)
	}
	for _, c := range candidates {
		h := sha256.Sum256([]byte(c))
		for _, pub := range s.pubs {
			if rsa.VerifyPKCS1v15(pub, crypto.SHA256, h[:], sig) == nil {
				return claims, nil
			}
		}
	}
	return Claims{}, ErrBadSignature
}

// GenerateKey sinh khóa RSA 2048 bit (cho môi trường dev; production dùng khóa cố định để URL còn hiệu lực sau khi khởi động lại).
func GenerateKey() (*rsa.PrivateKey, error) { return rsa.GenerateKey(rand.Reader, minKeyBits) }

// MarshalPrivateKeyPEM mã hóa khóa riêng ở dạng PKCS#8 PEM không mật khẩu (lưu với quyền tệp 0600).
func MarshalPrivateKeyPEM(k *rsa.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

// ParsePrivateKeyPEM đọc khóa RSA riêng ở dạng PKCS#8 hoặc PKCS#1; khóa mã hóa bằng mật khẩu bị từ chối với thông báo rõ.
func ParsePrivateKeyPEM(data []byte) (*rsa.PrivateKey, error) {
	blk, _ := pem.Decode(data)
	if blk == nil {
		return nil, errors.New("binarystore: khóa không phải PEM")
	}
	if _, enc := blk.Headers["DEK-Info"]; enc || blk.Type == "ENCRYPTED PRIVATE KEY" {
		return nil, errors.New("binarystore: khóa mã hóa bằng mật khẩu chưa được hỗ trợ; dùng PKCS#8 không mật khẩu")
	}
	var key any
	var err error
	switch blk.Type {
	case "PRIVATE KEY":
		key, err = x509.ParsePKCS8PrivateKey(blk.Bytes)
	case "RSA PRIVATE KEY":
		key, err = x509.ParsePKCS1PrivateKey(blk.Bytes)
	default:
		return nil, fmt.Errorf("binarystore: loại PEM %q không được hỗ trợ", blk.Type)
	}
	if err != nil {
		return nil, fmt.Errorf("binarystore: đọc khóa: %w", err)
	}
	rk, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("binarystore: khóa phải là RSA")
	}
	return rk, nil
}

// ParsePublicKeyPEM đọc khóa RSA công khai (PKIX "PUBLIC KEY") để xoay khóa.
func ParsePublicKeyPEM(data []byte) (*rsa.PublicKey, error) {
	blk, _ := pem.Decode(data)
	if blk == nil || blk.Type != "PUBLIC KEY" {
		return nil, errors.New("binarystore: khóa công khai phải là PEM 'PUBLIC KEY'")
	}
	k, err := x509.ParsePKIXPublicKey(blk.Bytes)
	if err != nil {
		return nil, err
	}
	rk, ok := k.(*rsa.PublicKey)
	if !ok {
		return nil, errors.New("binarystore: khóa phải là RSA")
	}
	return rk, nil
}
