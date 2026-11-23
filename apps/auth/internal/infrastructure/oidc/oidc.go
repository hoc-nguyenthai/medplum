// Package oidc gọi IdP ngoài (đổi code lấy id_token) và xác minh id_token. Mọi cuộc gọi ra ngoài đi qua pkg/safehttp (chỉ https, không dải nội bộ, không theo
// redirect, kiểm lại địa chỉ SAU khi resolve DNS), có timeout và trần kích thước phản hồi; lỗi trả ra có thông điệp cố định, không kèm nội dung phản hồi của IdP.
package oidc

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"trivita-fhir/apps/auth/internal/application"
	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/apps/auth/internal/infrastructure/jwks"
	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/safehttp"
)

const (
	// maxResponse là trần kích thước phản hồi của IdP (Medplum không giới hạn).
	maxResponse = 1 << 20
	timeout     = 10 * time.Second

	failedToVerify = "Failed to verify code - check your identity provider configuration"
)

// Client cài application.OIDCClient.
type Client struct {
	http      *http.Client
	httpsOnly bool
	verifier  *IDVerifier
}

// NewClient dựng Client. allowUnsafe=true cho phép URL http hoặc trỏ vào dải nội bộ (chỉ dev/test).
func NewClient(allowUnsafe bool) *Client {
	return &Client{http: safehttp.NewClient(timeout, allowUnsafe), httpsOnly: !allowUnsafe, verifier: NewIDVerifier(allowUnsafe)}
}

// ExchangeCode đổi authorization code lấy id_token tại tokenUrl (auth/external.ts:324). Mọi lỗi mạng, mã trạng thái hay JSON hỏng đều là cùng một thông báo.
func (c *Client) ExchangeCode(ctx context.Context, idp *fhir.IdentityProvider, in application.ExchangeCodeInput) (string, error) {
	if idp.TokenUrl == "" {
		return "", domain.BadRequest("Missing token URL for external identity provider")
	}
	if idp.ClientId == "" || idp.ClientSecret == "" {
		// khác Medplum: nhánh Basic của Medplum gửi cả chuỗi "undefined:undefined" khi thiếu cấu hình
		return "", domain.BadRequest("Missing client ID or client secret for external identity provider")
	}
	if _, err := safehttp.CheckURL(idp.TokenUrl, c.httpsOnly); err != nil {
		return "", domain.BadRequest(failedToVerify)
	}
	form := url.Values{"grant_type": {"authorization_code"}, "redirect_uri": {in.RedirectURI}, "code": {in.Code}}
	if in.CodeVerifier != "" {
		form.Set("code_verifier", in.CodeVerifier)
	}
	basic := ""
	if idp.TokenAuthMethod == "client_secret_post" {
		form.Set("client_id", idp.ClientId)
		form.Set("client_secret", idp.ClientSecret)
	} else {
		basic = "Basic " + base64.StdEncoding.EncodeToString([]byte(idp.ClientId+":"+idp.ClientSecret))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, idp.TokenUrl, strings.NewReader(form.Encode()))
	if err != nil {
		return "", domain.BadRequest(failedToVerify)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if basic != "" {
		req.Header.Set("Authorization", basic)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return "", domain.BadRequest(failedToVerify)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", domain.BadRequest(failedToVerify)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, maxResponse+1))
	if err != nil || len(body) > maxResponse {
		return "", domain.BadRequest(failedToVerify)
	}
	var out struct {
		IDToken any `json:"id_token"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", domain.BadRequest(failedToVerify)
	}
	tok, ok := out.IDToken.(string)
	if !ok || tok == "" {
		return "", domain.BadRequest("Missing id_token in external identity provider response")
	}
	return tok, nil
}

// IDVerifier cài application.IDTokenVerifier.
type IDVerifier struct {
	f   *jwks.Fetcher
	now func() time.Time
}

// NewIDVerifier dựng IDVerifier; allowUnsafe cho phép jwksUrl http hoặc nội bộ (chỉ dev/test).
func NewIDVerifier(allowUnsafe bool) *IDVerifier {
	v := &IDVerifier{f: jwks.New(allowUnsafe), now: time.Now}
	v.f.Now = func() time.Time { return v.now() }
	return v
}

// ErrSignature báo không khóa nào trong JWKS xác minh được chữ ký.
var ErrSignature = errors.New("oidc: chữ ký id_token không hợp lệ")

// VerifyJWKS kiểm chữ ký bằng JWKS, chỉ nhận RS*/ES* (cấm none và HS*: khác Medplum, nơi alg do JWKS quyết định), iss, aud và hạn (exp bắt buộc).
func (v *IDVerifier) VerifyJWKS(ctx context.Context, idToken, jwksURL, issuer, audience string) (map[string]any, error) {
	header, _, err := jwt.NewParser().ParseUnverified(idToken, jwt.MapClaims{})
	if err != nil {
		return nil, err
	}
	alg, _ := header.Header["alg"].(string)
	kid, _ := header.Header["kid"].(string)
	if !jwks.Allowed(alg) {
		return nil, errors.New("oidc: thuật toán không được hỗ trợ")
	}
	keys, err := v.f.Keys(ctx, jwksURL, false)
	if err != nil {
		return nil, err
	}
	cands := jwks.Candidates(keys, kid, alg)
	if len(cands) == 0 { // khóa mới của IdP chưa có trong cache: tải lại một lần (có giới hạn tần suất)
		if keys, err = v.f.Keys(ctx, jwksURL, true); err != nil {
			return nil, err
		}
		cands = jwks.Candidates(keys, kid, alg)
	}
	for _, k := range cands {
		claims := jwt.MapClaims{}
		_, err := jwt.NewParser(jwt.WithValidMethods([]string{alg}), jwt.WithIssuer(issuer), jwt.WithAudience(audience), jwt.WithExpirationRequired(), jwt.WithTimeFunc(v.now)).
			ParseWithClaims(idToken, claims, func(*jwt.Token) (any, error) { return k.Key, nil })
		switch {
		case err == nil:
			return claims, nil
		case errors.Is(err, jwt.ErrTokenSignatureInvalid):
			continue
		default:
			return nil, err
		}
	}
	return nil, ErrSignature
}

// ParseUnsigned chỉ giải mã payload của JWT, KHÔNG kiểm chữ ký, iss, aud hay hạn: chỉ cho dev/test.
func (v *IDVerifier) ParseUnsigned(idToken string) (map[string]any, error) {
	claims := jwt.MapClaims{}
	if _, _, err := jwt.NewParser().ParseUnverified(idToken, claims); err != nil {
		return nil, err
	}
	return claims, nil
}

// CheckURL cài application.OutboundURLCheck bằng pkg/safehttp.
func CheckURL(raw string, httpsOnly, blockInternal bool) error {
	u, err := safehttp.CheckURL(raw, httpsOnly)
	if err != nil {
		return err
	}
	if blockInternal && safehttp.HostBlocked(u.Hostname()) {
		return errors.New("địa chỉ nội bộ")
	}
	return nil
}

// UserInfo gọi userInfoUrl bằng token của IdP (oauth/utils.ts:1030). Khác Medplum: phản hồi application/jwt phải xác minh được chữ ký bằng JWKS của IdP (Q7; Medplum
// chấp nhận JWT không ký), và chế độ userInfoMode=gcip chưa hỗ trợ (Q9). Mọi lỗi mạng, mã trạng thái hay JSON hỏng là cùng một thông báo cố định; IdP trả 429 thì 429.
func (c *Client) UserInfo(ctx context.Context, idp *fhir.IdentityProvider, token string) (map[string]any, error) {
	if idp.UserInfoUrl == "" {
		return nil, domain.BadRequest("Missing user info URL")
	}
	if idp.UserInfoMode == "gcip" {
		return nil, domain.BadRequest("Unsupported user info mode")
	}
	if _, err := safehttp.CheckURL(idp.UserInfoUrl, c.httpsOnly); err != nil {
		return nil, domain.BadRequest(failedToVerify)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, idp.UserInfoUrl, nil)
	if err != nil {
		return nil, domain.BadRequest(failedToVerify)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "identity")
	res, err := c.http.Do(req)
	if err != nil {
		return nil, domain.BadRequest(failedToVerify)
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusTooManyRequests {
		return nil, domain.TooManyRequests(0)
	}
	if res.StatusCode != http.StatusOK {
		return nil, domain.BadRequest(failedToVerify)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, maxResponse+1))
	if err != nil || len(body) > maxResponse {
		return nil, domain.BadRequest(failedToVerify)
	}
	ct, _, _ := mime.ParseMediaType(res.Header.Get("Content-Type"))
	switch ct {
	case "application/json":
		var claims map[string]any
		if err := json.Unmarshal(body, &claims); err != nil || claims == nil {
			return nil, domain.BadRequest(failedToVerify)
		}
		return claims, nil
	case "application/jwt":
		if idp.JwksUrl == "" || idp.Issuer == "" {
			return nil, domain.BadRequest("Failed to verify code - unsigned user info response")
		}
		aud := idp.Audience
		if aud == "" {
			aud = idp.ClientId
		}
		claims, err := c.verifier.VerifyJWKS(ctx, strings.TrimSpace(string(body)), idp.JwksUrl, idp.Issuer, aud)
		if err != nil {
			return nil, domain.BadRequest(failedToVerify)
		}
		return claims, nil
	}
	return nil, domain.BadRequest("Failed to verify code - unsupported content type")
}
