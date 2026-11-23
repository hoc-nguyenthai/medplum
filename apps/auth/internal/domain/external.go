package domain

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strings"

	"trivita-fhir/pkg/fhir"
)

// Chế độ ánh xạ danh tính của IdP ngoài (A3.5 của kế hoạch P6).
const (
	IdentitySourceEmail   = "email"
	IdentitySourceSubject = "subject"
	MappingUserEmail      = "user-email"
	MappingMembershipExt  = "project-membership-external-id"
)

// ExternalState là nội dung `state` của luồng đăng nhập ngoài (auth/external.ts:34). Mọi trường tùy chọn.
type ExternalState struct {
	Domain              string `json:"domain,omitempty"`
	Issuer              string `json:"issuer,omitempty"`
	ProjectID           string `json:"projectId,omitempty"`
	ClientID            string `json:"clientId,omitempty"`
	Scope               string `json:"scope,omitempty"`
	Nonce               string `json:"nonce,omitempty"`
	Launch              string `json:"launch,omitempty"`
	CodeChallenge       string `json:"codeChallenge,omitempty"`
	CodeChallengeMethod string `json:"codeChallengeMethod,omitempty"`
	RedirectURI         string `json:"redirectUri,omitempty"`
	ReturnTo            string `json:"returnTo,omitempty"`
}

// ResolveExternalIdentity ánh xạ claim của id_token/userinfo sang danh tính để tìm user (auth/external.ts:274). Chỉ hai tổ hợp được hỗ trợ:
// (email, user-email) và (subject, project-membership-external-id); mặc định theo useSubject. Email được hạ chữ thường và KHÔNG tách dấu phẩy (một danh
// sách email không bao giờ khớp user nào).
func ResolveExternalIdentity(idp *fhir.IdentityProvider, claims map[string]any) (email, externalID string, err error) {
	useSubject := IsTrue(idp.UseSubject)
	source, mode := idp.IdentitySource, idp.IdentityMappingMode
	if source == "" {
		source = IdentitySourceEmail
		if useSubject {
			source = IdentitySourceSubject
		}
	}
	if mode == "" {
		mode = MappingUserEmail
		if useSubject {
			mode = MappingMembershipExt
		}
	}
	switch {
	case source == IdentitySourceEmail && mode == MappingUserEmail:
		e, ok := claims["email"].(string)
		if !ok || e == "" {
			return "", "", BadRequest("External token does not contain email address")
		}
		return strings.ToLower(e), "", nil
	case source == IdentitySourceSubject && mode == MappingMembershipExt:
		sub, ok := claims["sub"].(string)
		if !ok || sub == "" {
			return "", "", BadRequest("External token does not contain subject")
		}
		return "", sub, nil
	}
	return "", "", BadRequest("Unsupported identity provider configuration")
}

// IsExternalEmailUnverified: IdP báo rõ email CHƯA xác minh (`email_verified` là false hoặc chuỗi "false"). Vắng mặt không phải là chưa xác minh.
func IsExternalEmailUnverified(claims map[string]any) bool {
	switch v := claims["email_verified"].(type) {
	case bool:
		return !v
	case string:
		return v == "false"
	}
	return false
}

// ValidReturnTo kiểm returnTo theo allowedPostLoginRedirectUrls của domain (auth/external.ts:430): cả hai phải parse được, CÙNG protocol, hostname và port,
// và đường dẫn của returnTo bắt đầu bằng đường dẫn của URL cho phép.
func ValidReturnTo(returnTo string, allowed []string) bool {
	r, err := url.Parse(returnTo)
	if err != nil || r.Scheme == "" || r.Host == "" {
		return false
	}
	for _, a := range allowed {
		u, err := url.Parse(a)
		if err != nil || u.Scheme == "" || u.Host == "" {
			continue
		}
		if r.Scheme == u.Scheme && r.Hostname() == u.Hostname() && r.Port() == u.Port() && strings.HasPrefix(r.Path, u.Path) {
			return true
		}
	}
	return false
}

// MatchRedirectURIPartial khớp MỘT PHẦN: cùng origin và đường dẫn bắt đầu bằng đường dẫn đã đăng ký (oauth/clients.ts:50, chỉ khi project bật
// allow-dangerous-redirect). Trả URI đã đăng ký khớp, hoặc "".
func MatchRedirectURIPartial(c *fhir.ClientApplication, requested string) string {
	r, err := url.Parse(requested)
	if err != nil || r.Scheme == "" || r.Host == "" {
		return ""
	}
	for _, reg := range RedirectURIs(c) {
		u, err := url.Parse(reg)
		if err != nil {
			continue
		}
		if r.Scheme == u.Scheme && r.Host == u.Host && strings.HasPrefix(r.Path, u.Path) {
			return reg
		}
	}
	return ""
}

// BuildAuthorizeURL dựng URL gửi người dùng tới IdP: authorizeUrl cộng client_id, redirect_uri, response_type=code, scope và state (nếu có).
// authorizeUrl phải là URL đầy đủ có scheme: thiếu scheme là "Failed to construct URL for the domain".
func BuildAuthorizeURL(idp *fhir.IdentityProvider, redirectURI, state string) (string, error) {
	u, err := url.Parse(idp.AuthorizeUrl)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", BadRequest("Failed to construct URL for the domain")
	}
	q := u.Query()
	q.Set("client_id", idp.ClientId)
	q.Set("redirect_uri", redirectURI)
	q.Set("response_type", "code")
	q.Set("scope", "openid profile email")
	if state != "" {
		q.Set("state", state)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// SubjectTokenAudienceOK kiểm (Q11) token của IdP dùng để đổi lấy token của auth được cấp cho CHÍNH ứng dụng này: nếu là JWT thì `aud` phải chứa audience hoặc client id
// của IdP đã cấu hình, hoặc `azp` bằng client id. Medplum chỉ gọi userinfo bằng token đó nên token của ứng dụng khác cùng IdP đổi được. Token không phải JWT
// (opaque) không kiểm được ở đây (userinfo của IdP là nơi duy nhất xác nhận): trả true. Payload được đọc KHÔNG kiểm chữ ký: đủ vì IdP vẫn xác nhận token ở userinfo,
// và kẻ giả payload sẽ làm chữ ký sai.
func SubjectTokenAudienceOK(token string, idp *fhir.IdentityProvider) bool {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return true
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return true
	}
	var claims map[string]any
	if json.Unmarshal(raw, &claims) != nil {
		return true
	}
	want := map[string]bool{}
	for _, w := range []string{idp.Audience, idp.ClientId} {
		if w != "" {
			want[w] = true
		}
	}
	if len(want) == 0 {
		return false
	}
	switch aud := claims["aud"].(type) {
	case string:
		if want[aud] {
			return true
		}
	case []any:
		for _, a := range aud {
			if as, ok := a.(string); ok && want[as] {
				return true
			}
		}
	}
	azp, _ := claims["azp"].(string)
	return azp != "" && azp == idp.ClientId
}
