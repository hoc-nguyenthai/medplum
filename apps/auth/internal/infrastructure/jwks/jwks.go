// Package jwks tải JWKS từ xa (qua pkg/safehttp: https, không dải nội bộ, không theo redirect), giới hạn kích thước, có bộ nhớ đệm và giới hạn tần suất tải lại.
// Dùng chung cho client_assertion (jwks_uri của client) và id_token của IdP ngoài (jwksUrl của IdP).
package jwks

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/go-jose/go-jose/v4"

	"trivita-fhir/pkg/safehttp"
)

// MaxBytes là kích thước tối đa của một JWKS (phục vụ test).
const MaxBytes = 256 << 10

const (
	maxJWKSBytes = MaxBytes
	maxJWKSKeys  = 20
	cacheTTL     = 5 * time.Minute
	// minRefetch là khoảng tối thiểu giữa hai lần tải lại ngoài hạn cache khi gặp kid lạ (xoay khóa), chặn việc ép auth tải liên tục.
	minRefetch = 30 * time.Second
)

// AllowedAlgs là các thuật toán ký được chấp nhận: RS* và ES*. Cấm none và HS* (khóa đối xứng không thể công khai trong JWKS).
var AllowedAlgs = []string{"RS256", "RS384", "RS512", "ES256", "ES384", "ES512"}

// Allowed cho biết alg có trong AllowedAlgs.
func Allowed(alg string) bool {
	for _, a := range AllowedAlgs {
		if a == alg {
			return true
		}
	}
	return false
}

type entry struct {
	keys    []jose.JSONWebKey
	fetched time.Time
}

// Fetcher tải và nhớ JWKS theo URL.
type Fetcher struct {
	// Client mặc định là safehttp.NewClient; test gán client riêng để kiểm lớp kết nối.
	Client *http.Client
	// HTTPSOnly yêu cầu URL là https (false chỉ cho dev/test).
	HTTPSOnly bool
	// Now là đồng hồ (test điều khiển được).
	Now func() time.Time

	mu    sync.Mutex
	cache map[string]entry
}

// New dựng Fetcher. allowPrivate=true cho phép URL http hoặc trỏ vào dải nội bộ (chỉ dev/test).
func New(allowPrivate bool) *Fetcher {
	return &Fetcher{Client: safehttp.NewClient(5*time.Second, allowPrivate), HTTPSOnly: !allowPrivate, Now: time.Now, cache: map[string]entry{}}
}

// CacheSize và ResetCache phục vụ test.
func (f *Fetcher) CacheSize() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.cache) }

// ResetCache xóa bộ nhớ đệm.
func (f *Fetcher) ResetCache() { f.mu.Lock(); f.cache = map[string]entry{}; f.mu.Unlock() }

// Keys trả các khóa công khai dùng để ký của JWKS tại uri. force=true tải lại ngoài hạn cache (nhưng không thường xuyên hơn minRefetch).
func (f *Fetcher) Keys(ctx context.Context, uri string, force bool) ([]jose.JSONWebKey, error) {
	f.mu.Lock()
	e, ok := f.cache[uri]
	f.mu.Unlock()
	age := f.Now().Sub(e.fetched)
	if ok && ((!force && age < cacheTTL) || (force && age < minRefetch)) {
		return e.keys, nil
	}
	if _, err := safehttp.CheckURL(uri, f.HTTPSOnly); err != nil {
		return nil, fmt.Errorf("jwks_uri không dùng được: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	res, err := f.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("không tải được JWKS: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("JWKS trả %d", res.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, maxJWKSBytes+1))
	if err != nil || len(body) > maxJWKSBytes {
		return nil, errors.New("JWKS quá lớn hoặc không đọc được")
	}
	var set jose.JSONWebKeySet
	if err := json.Unmarshal(body, &set); err != nil || len(set.Keys) == 0 || len(set.Keys) > maxJWKSKeys {
		return nil, errors.New("JWKS không hợp lệ")
	}
	var usable []jose.JSONWebKey
	for _, k := range set.Keys {
		if k.IsPublic() && k.Valid() && (k.Use == "" || k.Use == "sig") {
			usable = append(usable, k)
		}
	}
	f.mu.Lock()
	f.cache[uri] = entry{keys: usable, fetched: f.Now()}
	f.mu.Unlock()
	return usable, nil
}

// Candidates chọn khóa theo kid của header (nhiều khóa trùng kid thì thử từng cái) và loại khóa hợp với alg.
func Candidates(keys []jose.JSONWebKey, kid, alg string) []jose.JSONWebKey {
	var out []jose.JSONWebKey
	for _, k := range keys {
		if (kid != "" && k.KeyID != kid) || (k.Algorithm != "" && k.Algorithm != alg) {
			continue
		}
		if KeyMatchesAlg(k, alg) {
			out = append(out, k)
		}
	}
	return out
}

// KeyMatchesAlg: RS* cần khóa RSA, ES* cần khóa EC đúng đường cong (golang-jwt báo lỗi loại khóa như lỗi "khác chữ ký" nên phải lọc trước).
func KeyMatchesAlg(k jose.JSONWebKey, alg string) bool {
	switch pub := k.Key.(type) {
	case *rsa.PublicKey:
		return alg == "RS256" || alg == "RS384" || alg == "RS512"
	case *ecdsa.PublicKey:
		switch pub.Curve {
		case elliptic.P256():
			return alg == "ES256"
		case elliptic.P384():
			return alg == "ES384"
		case elliptic.P521():
			return alg == "ES512"
		}
	}
	return false
}
