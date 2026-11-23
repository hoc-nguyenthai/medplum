package application

import (
	"context"
	"strings"

	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
)

// AddDomainRequest là dữ liệu tạo DomainConfiguration (IdP ngoài cho một tên miền email).
type AddDomainRequest struct {
	Domain                                                         string
	AuthorizeURL, TokenURL, UserInfoURL, JWKSURL, Issuer, Audience string
	ClientID, ClientSecret                                         string
	ClientSecretPost, UsePKCE, UseSubject                          bool
	AllowedPostLoginRedirectURLs                                   []string
	// AllowUnsafeURLs cho phép URL http hoặc trỏ vào dải nội bộ và IdP không có jwksUrl: chỉ dev/test.
	AllowUnsafeURLs bool
}

// AddDomainConfiguration tạo DomainConfiguration sau khi kiểm URL bằng cùng bộ kiểm SSRF của lúc gọi ra ngoài (chỉ https, không userinfo, không dải nội bộ theo
// literal): cấu hình xấu bị chặn ngay khi lưu, không đợi tới lúc kết nối (Medplum chỉ chặn lúc kết nối). Mỗi tên miền chỉ một cấu hình (FindByDomain lấy cấu hình đầu tiên).
func (s *Service) AddDomainConfiguration(ctx context.Context, req AddDomainRequest) (*fhir.DomainConfiguration, error) {
	d := strings.ToLower(strings.TrimSpace(req.Domain))
	if d == "" || strings.ContainsAny(d, "@/ ") {
		return nil, domain.BadRequestAt("Domain must be a bare domain name, for example example.org", "domain")
	}
	httpsOnly := !req.AllowUnsafeURLs
	for field, u := range map[string]string{"authorizeUrl": req.AuthorizeURL, "tokenUrl": req.TokenURL} {
		if u == "" {
			return nil, domain.BadRequestAt("Missing "+field, field)
		}
	}
	for field, u := range map[string]string{"authorizeUrl": req.AuthorizeURL, "tokenUrl": req.TokenURL, "userInfoUrl": req.UserInfoURL, "jwksUrl": req.JWKSURL} {
		if u == "" {
			continue
		}
		if s.OutboundURL == nil {
			return nil, domain.Internal("chưa có bộ kiểm URL gọi ra ngoài")
		}
		// authorizeUrl chỉ là đích chuyển hướng của trình duyệt (auth không gọi tới), nên không chặn dải nội bộ ở đó; các URL auth tự gọi thì chặn
		if err := s.OutboundURL(u, httpsOnly, field != "authorizeUrl" && !req.AllowUnsafeURLs); err != nil {
			return nil, domain.BadRequestAt("Invalid "+field+": "+err.Error(), field)
		}
	}
	if req.ClientID == "" || req.ClientSecret == "" {
		return nil, domain.BadRequest("Missing client ID or client secret for external identity provider")
	}
	if req.JWKSURL == "" && !req.AllowUnsafeURLs {
		return nil, domain.BadRequestAt("Missing jwksUrl: id_token phải được xác minh chữ ký", "jwksUrl")
	}
	if req.JWKSURL != "" && req.Issuer == "" {
		return nil, domain.BadRequestAt("Missing issuer for external identity provider", "issuer")
	}
	existing, err := s.DomainConfigs.FindByDomain(ctx, d)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, domain.Conflict("Domain already configured")
	}
	idp := &fhir.IdentityProvider{AuthorizeUrl: req.AuthorizeURL, TokenUrl: req.TokenURL, UserInfoUrl: req.UserInfoURL, JwksUrl: req.JWKSURL, Issuer: req.Issuer,
		Audience: req.Audience, ClientId: req.ClientID, ClientSecret: req.ClientSecret}
	if req.ClientSecretPost {
		idp.TokenAuthMethod = "client_secret_post"
	}
	if req.UsePKCE {
		idp.UsePkce = ptrTrue()
	}
	if req.UseSubject {
		idp.UseSubject = ptrTrue()
	}
	out, err := s.Resources.Create(ctx, &fhir.DomainConfiguration{ResourceType: "DomainConfiguration", Domain: d, IdentityProvider: idp, AllowedPostLoginRedirectUrls: req.AllowedPostLoginRedirectURLs})
	if err != nil {
		return nil, err
	}
	return out.(*fhir.DomainConfiguration), nil
}
