package fhirauth

import (
	"context"
	"crypto"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/golang-jwt/jwt/v5"
)

// Lỗi của Verify. ErrInvalidToken ứng với HTTP 401, ErrForbidden với 403.
var (
	ErrInvalidToken = errors.New("fhirauth: token không hợp lệ")
	ErrForbidden    = errors.New("fhirauth: token không có quyền truy cập FHIR")
)

// Tên scope cấp vai trò. Scope SMART hệ thống cũng được hiểu: "system/*.read" là đọc; "system/*.write" và
// "system/*.*" là ghi. Quyền admin chỉ cấp bằng "fhir.admin".
const (
	ScopeRead  = "fhir.read"
	ScopeWrite = "fhir.write"
	ScopeAdmin = "fhir.admin"
	// ScopeSuper cho phép hành động trong mọi project (chọn project bằng header khi gọi) và gồm quyền admin. Chỉ có tác
	// dụng khi Config.AllowSuperAdmin bật; token super-admin không cần claim project.
	ScopeSuper = "fhir.super"
)

// Config cấu hình Verifier. Phải có đúng một trong JWKSURL và HMACSecret.
type Config struct {
	Issuer   string // bắt buộc; so khớp claim iss
	Audience string // bắt buộc; phải nằm trong claim aud
	// JWKSURL là địa chỉ JWKS của identity provider (RS256, PS256, ES256, EdDSA).
	JWKSURL string
	// HMACSecret là khóa dùng chung (HS256) cho dev hoặc test; không dùng cho production.
	HMACSecret []byte

	ProjectClaim    string        // mặc định "fhir_project"
	AllowSuperAdmin bool          // bật scope fhir.super (mặc định tắt: scope này bị bỏ qua)
	PolicyClaim     string        // mặc định "fhir_access_policy": AccessPolicy dạng mảng rule (xem Policy)
	ScopeClaim      string        // mặc định "scope" (chuỗi cách nhau bằng dấu cách hoặc mảng)
	Leeway          time.Duration // chênh lệch đồng hồ cho exp/nbf; mặc định 30s
	JWKSCacheTTL    time.Duration // mặc định 10 phút
	HTTPClient      *http.Client  // mặc định timeout 5 giây
	Now             func() time.Time
}

// Verifier kiểm tra access token và trả Principal.
type Verifier struct {
	cfg  Config
	jwks *keySet
}

// NewVerifier kiểm tra cấu hình và dựng Verifier (không gọi mạng; JWKS được tải khi cần).
func NewVerifier(cfg Config) (*Verifier, error) {
	if cfg.Issuer == "" || cfg.Audience == "" {
		return nil, errors.New("fhirauth: cần Issuer và Audience")
	}
	if (cfg.JWKSURL == "") == (len(cfg.HMACSecret) == 0) {
		return nil, errors.New("fhirauth: cần đúng một trong JWKSURL và HMACSecret")
	}
	if len(cfg.HMACSecret) > 0 && len(cfg.HMACSecret) < 32 {
		return nil, errors.New("fhirauth: HMACSecret phải dài ít nhất 32 byte")
	}
	if cfg.ProjectClaim == "" {
		cfg.ProjectClaim = "fhir_project"
	}
	if cfg.PolicyClaim == "" {
		cfg.PolicyClaim = "fhir_access_policy"
	}
	if cfg.ScopeClaim == "" {
		cfg.ScopeClaim = "scope"
	}
	if cfg.Leeway == 0 {
		cfg.Leeway = 30 * time.Second
	}
	if cfg.JWKSCacheTTL == 0 {
		cfg.JWKSCacheTTL = 10 * time.Minute
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 5 * time.Second}
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	v := &Verifier{cfg: cfg}
	if cfg.JWKSURL != "" {
		v.jwks = &keySet{url: cfg.JWKSURL, client: cfg.HTTPClient, ttl: cfg.JWKSCacheTTL, now: cfg.Now}
	}
	return v, nil
}

// Claims là phần claim đã xác minh chữ ký (iss, aud, exp) nhưng CHƯA qua kiểm quyền FHIR: dành cho dịch vụ dùng chung
// verifier này với scope riêng của mình (ví dụ fhircast/*) thay vì scope fhir.* của API FHIR.
type Claims struct {
	Subject string
	Project string // claim project (rỗng nếu token không có)
	Scopes  []string
	Expires time.Time
}

// VerifyClaims kiểm tra chữ ký, iss, aud, exp và sự có mặt của sub; không đòi project hay scope nào. Lỗi bọc ErrInvalidToken.
func (v *Verifier) VerifyClaims(ctx context.Context, raw string) (*Claims, error) {
	c, err := v.parse(ctx, raw)
	if err != nil {
		return nil, err
	}
	out := &Claims{Subject: c.sub, Project: c.project, Scopes: c.scopes, Expires: c.expires}
	return out, nil
}

type parsedClaims struct {
	sub, project string
	scopes       []string
	expires      time.Time
	raw          jwt.MapClaims
}

func (v *Verifier) parse(ctx context.Context, raw string) (*parsedClaims, error) {
	methods := []string{"RS256", "RS384", "RS512", "PS256", "PS384", "PS512", "ES256", "ES384", "ES512", "EdDSA"}
	if v.jwks == nil {
		methods = []string{"HS256"}
	}
	claims := jwt.MapClaims{}
	_, err := jwt.NewParser(
		jwt.WithValidMethods(methods), jwt.WithIssuer(v.cfg.Issuer), jwt.WithAudience(v.cfg.Audience),
		jwt.WithExpirationRequired(), jwt.WithLeeway(v.cfg.Leeway), jwt.WithTimeFunc(v.cfg.Now),
	).ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
		if v.jwks == nil {
			return v.cfg.HMACSecret, nil
		}
		kid, _ := t.Header["kid"].(string)
		return v.jwks.key(ctx, kid)
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	sub, _ := claims["sub"].(string)
	if sub == "" {
		return nil, fmt.Errorf("%w: thiếu sub", ErrInvalidToken)
	}
	project, _ := claims[v.cfg.ProjectClaim].(string)
	var expires time.Time
	if t, err := claims.GetExpirationTime(); err == nil && t != nil {
		expires = t.Time
	}
	return &parsedClaims{sub: sub, project: project, scopes: scopesOf(claims[v.cfg.ScopeClaim]), expires: expires, raw: claims}, nil
}

// Verify kiểm tra chữ ký, iss, aud, exp và các claim bắt buộc (sub, project, ít nhất một scope cấp quyền).
// Lỗi bọc ErrInvalidToken (token sai) hoặc ErrForbidden (token đúng nhưng không dùng được cho FHIR).
func (v *Verifier) Verify(ctx context.Context, raw string) (*Principal, error) {
	pc, err := v.parse(ctx, raw)
	if err != nil {
		return nil, err
	}
	claims, sub, project, scopes := pc.raw, pc.sub, pc.project, pc.scopes
	super := false
	if v.cfg.AllowSuperAdmin {
		for _, sc := range scopes {
			super = super || sc == ScopeSuper
		}
	}
	if project == "" && !super {
		return nil, fmt.Errorf("%w: thiếu claim %q", ErrForbidden, v.cfg.ProjectClaim)
	}
	grants := grantsFromScopes(scopes)
	role := roleFromScopes(scopes)
	if super {
		grants["*"] |= PermAll
		role = RoleAdmin
	}
	if g := roleFromGrants(grants); role < g {
		role = g // chỉ có scope theo loại: đủ để qua cổng HTTP, từng loại do Scope kiểm tiếp
	}
	if role == RoleNone {
		return nil, fmt.Errorf("%w: thiếu scope %s, %s, %s hoặc scope SMART hợp lệ", ErrForbidden, ScopeRead, ScopeWrite, ScopeAdmin)
	}
	var policy *Policy
	if raw, ok := claims[v.cfg.PolicyClaim]; ok {
		var err error
		if policy, err = ParsePolicy(raw); err != nil {
			return nil, fmt.Errorf("%w: claim %q: %v", ErrForbidden, v.cfg.PolicyClaim, err)
		}
	}
	var expires time.Time
	if t, err := claims.GetExpirationTime(); err == nil && t != nil {
		expires = t.Time
	}
	return &Principal{Subject: sub, Project: project, Role: role, Grants: grants, Policy: policy, Super: super, Expires: expires}, nil
}

func scopesOf(v any) []string {
	switch x := v.(type) {
	case string:
		return strings.Fields(x)
	case []any:
		var out []string
		for _, e := range x {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func roleFromScopes(scopes []string) Role {
	role := RoleNone
	for _, s := range scopes {
		r := RoleNone
		switch s {
		case ScopeAdmin:
			r = RoleAdmin
		case ScopeWrite, "system/*.write", "system/*.*", "system/*.cruds":
			r = RoleWrite
		case ScopeRead, "system/*.read":
			r = RoleRead
		}
		if r > role {
			role = r
		}
	}
	return role
}

// keySet là bộ nhớ đệm JWKS: làm mới khi hết hạn hoặc gặp kid lạ (tối đa mỗi 30 giây, để token giả không biến
// service thành bộ khuếch đại yêu cầu tới identity provider).
type keySet struct {
	url    string
	client *http.Client
	ttl    time.Duration
	now    func() time.Time

	mu        sync.Mutex
	set       jose.JSONWebKeySet
	fetchedAt time.Time
	tried     time.Time
}

const minRefresh = 30 * time.Second

func (k *keySet) key(ctx context.Context, kid string) (crypto.PublicKey, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if kid == "" {
		return nil, errors.New("token không có kid")
	}
	find := func() (crypto.PublicKey, bool) {
		for _, jk := range k.set.Key(kid) {
			if jk.IsPublic() && jk.Valid() {
				return jk.Key, true
			}
		}
		return nil, false
	}
	fresh := k.now().Sub(k.fetchedAt) < k.ttl
	if key, ok := find(); ok && fresh {
		return key, nil
	}
	if k.now().Sub(k.tried) >= minRefresh {
		k.tried = k.now()
		if err := k.fetch(ctx); err != nil {
			if key, ok := find(); ok { // dùng bản cũ khi IdP tạm lỗi
				return key, nil
			}
			return nil, err
		}
	}
	if key, ok := find(); ok {
		return key, nil
	}
	return nil, fmt.Errorf("không tìm thấy khóa kid=%q", kid)
}

func (k *keySet) fetch(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, k.url, nil)
	if err != nil {
		return err
	}
	resp, err := k.client.Do(req)
	if err != nil {
		return fmt.Errorf("tải JWKS: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("tải JWKS: HTTP %d", resp.StatusCode)
	}
	var set jose.JSONWebKeySet
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&set); err != nil {
		return fmt.Errorf("giải mã JWKS: %w", err)
	}
	k.set, k.fetchedAt = set, k.now()
	return nil
}
