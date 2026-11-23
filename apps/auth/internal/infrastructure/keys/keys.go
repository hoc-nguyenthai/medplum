// Package keys giữ khóa ký JWT của auth: nạp khóa riêng từ thư mục PEM, ký, xác minh và công bố JWKS (chỉ phần công khai).
// Khóa riêng không bao giờ nằm trong kho FHIR (Medplum lưu cả tham số riêng `d` trong resource JsonWebKey).
package keys

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/golang-jwt/jwt/v5"
)

// Alg là thuật toán ký được hỗ trợ.
type Alg string

// Các thuật toán.
const (
	RS256 Alg = "RS256"
	ES256 Alg = "ES256"
	ES384 Alg = "ES384"
)

type key struct {
	kid  string
	alg  Alg
	priv crypto.Signer
	pub  crypto.PublicKey
}

// Store là tập khóa đã nạp: một khóa ký (signing) và các khóa chỉ xác minh (để xoay khóa mà token cũ vẫn hợp lệ).
type Store struct {
	signing *key
	byKID   map[string]*key
	now     func() time.Time
}

// Generate sinh khóa mới, ghi PEM (PKCS#8, quyền 0600) vào dir và trả kid (thumbprint RFC 7638).
func Generate(dir string, alg Alg) (string, error) {
	k, err := generate(alg)
	if err != nil {
		return "", err
	}
	der, err := x509.MarshalPKCS8PrivateKey(k.priv)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	f, err := os.OpenFile(filepath.Join(dir, k.kid+".pem"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if err := pem.Encode(f, &pem.Block{Type: "PRIVATE KEY", Bytes: der}); err != nil {
		return "", err
	}
	return k.kid, nil
}

func generate(alg Alg) (*key, error) {
	var priv crypto.Signer
	var err error
	switch alg {
	case RS256:
		priv, err = rsa.GenerateKey(rand.Reader, 3072)
	case ES256:
		priv, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	case ES384:
		priv, err = ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	default:
		return nil, fmt.Errorf("thuật toán %q không được hỗ trợ (RS256, ES256, ES384)", alg)
	}
	if err != nil {
		return nil, err
	}
	return newKey(priv)
}

func newKey(priv crypto.Signer) (*key, error) {
	k := &key{priv: priv, pub: priv.Public()}
	switch p := priv.(type) {
	case *rsa.PrivateKey:
		if p.N.BitLen() < 2048 {
			return nil, errors.New("khóa RSA phải từ 2048 bit")
		}
		k.alg = RS256
	case *ecdsa.PrivateKey:
		switch p.Curve {
		case elliptic.P256():
			k.alg = ES256
		case elliptic.P384():
			k.alg = ES384
		default:
			return nil, errors.New("chỉ hỗ trợ đường cong P-256 và P-384")
		}
	default:
		return nil, errors.New("loại khóa không được hỗ trợ")
	}
	tp, err := (&jose.JSONWebKey{Key: k.pub}).Thumbprint(crypto.SHA256)
	if err != nil {
		return nil, err
	}
	k.kid = base64.RawURLEncoding.EncodeToString(tp)
	return k, nil
}

// Load nạp mọi file *.pem (khóa riêng) trong dir. activeKID chọn khóa ký; rỗng thì chỉ được phép khi dir có đúng một khóa
// (nhiều khóa mà không chỉ rõ là lỗi, không chọn "phần tử đầu" như Medplum).
func Load(dir, activeKID string) (*Store, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.pem"))
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	s := &Store{byKID: map[string]*key{}, now: time.Now}
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		priv, err := parsePrivate(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(p), err)
		}
		k, err := newKey(priv)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(p), err)
		}
		s.byKID[k.kid] = k
	}
	return s.pick(activeKID)
}

// Ephemeral dựng Store với một khóa mới chỉ nằm trong bộ nhớ (dev/test: token mất hiệu lực khi khởi động lại).
func Ephemeral(alg Alg) (*Store, error) {
	k, err := generate(alg)
	if err != nil {
		return nil, err
	}
	return &Store{signing: k, byKID: map[string]*key{k.kid: k}, now: time.Now}, nil
}

func (s *Store) pick(activeKID string) (*Store, error) {
	switch {
	case len(s.byKID) == 0:
		return nil, errors.New("không có khóa nào (*.pem): chạy `auth keys generate`")
	case activeKID != "":
		k, ok := s.byKID[activeKID]
		if !ok {
			return nil, fmt.Errorf("AUTH_ACTIVE_KID %q không có trong thư mục khóa", activeKID)
		}
		s.signing = k
	case len(s.byKID) == 1:
		for _, k := range s.byKID {
			s.signing = k
		}
	default:
		return nil, errors.New("có nhiều khóa: đặt AUTH_ACTIVE_KID để chọn khóa ký")
	}
	return s, nil
}

func parsePrivate(raw []byte) (crypto.Signer, error) {
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, errors.New("không phải PEM")
	}
	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		if s, ok := k.(crypto.Signer); ok {
			return s, nil
		}
		return nil, errors.New("loại khóa không được hỗ trợ")
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	if k, err := x509.ParseECPrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	return nil, errors.New("không đọc được khóa riêng (PKCS#8, PKCS#1 hoặc EC)")
}

// SigningKID trả kid của khóa đang ký.
func (s *Store) SigningKID() string { return s.signing.kid }

// SigningAlg trả thuật toán của khóa đang ký.
func (s *Store) SigningAlg() string { return string(s.signing.alg) }

// Sign ký claims (header có kid, alg, typ=JWT).
func (s *Store) Sign(claims map[string]any) (string, error) {
	tok := jwt.NewWithClaims(jwt.GetSigningMethod(string(s.signing.alg)), jwt.MapClaims(claims))
	tok.Header["kid"] = s.signing.kid
	return tok.SignedString(s.signing.priv)
}

// Verify kiểm chữ ký bằng khóa theo `kid` của header (bắt buộc), thuật toán phải đúng với khóa, và `exp` còn hạn. Từ chối
// `none` và HS*. Việc kiểm iss, aud, token_use thuộc người gọi.
func (s *Store) Verify(raw string) (map[string]any, error) {
	claims := jwt.MapClaims{}
	_, err := jwt.NewParser(
		jwt.WithValidMethods([]string{string(RS256), string(ES256), string(ES384)}),
		jwt.WithExpirationRequired(), jwt.WithTimeFunc(s.now),
	).ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		k, ok := s.byKID[kid]
		if !ok {
			return nil, errors.New("kid không rõ")
		}
		if t.Method.Alg() != string(k.alg) {
			return nil, errors.New("thuật toán không khớp khóa")
		}
		return k.pub, nil
	})
	if err != nil {
		return nil, err
	}
	return claims, nil
}

// JWKS trả JSON của bộ khóa công khai ({"keys":[...]}): chỉ tham số công khai, kèm kid, alg, use=sig.
func (s *Store) JWKS() []byte {
	kids := make([]string, 0, len(s.byKID))
	for kid := range s.byKID {
		kids = append(kids, kid)
	}
	sort.Strings(kids)
	set := jose.JSONWebKeySet{}
	for _, kid := range kids {
		k := s.byKID[kid]
		set.Keys = append(set.Keys, jose.JSONWebKey{Key: k.pub, KeyID: k.kid, Algorithm: string(k.alg), Use: "sig"})
	}
	b, _ := json.Marshal(set)
	return b
}

// KeyCount là số khóa trong JWKS.
func (s *Store) KeyCount() int { return len(s.byKID) }

// ParseAlg đổi chuỗi cấu hình thành Alg.
func ParseAlg(v string) (Alg, error) {
	switch strings.ToUpper(v) {
	case "", "RS256":
		return RS256, nil
	case "ES256":
		return ES256, nil
	case "ES384":
		return ES384, nil
	}
	return "", fmt.Errorf("thuật toán %q không được hỗ trợ (RS256, ES256, ES384)", v)
}
