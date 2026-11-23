package application

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirauth"
)

const (
	tokenUseAccess  = "access"
	tokenUseID      = "id"
	tokenUseRefresh = "refresh"
	defaultMaxClaim = 16384
)

// refreshAudience là aud của refresh token: khác aud của access token nên fhirauth chắc chắn từ chối nó nếu bị đưa nhầm.
func (c TokenConfig) refreshAudience() string {
	return strings.TrimRight(c.Issuer, "/") + "/oauth2/token"
}

// TokenResponse là body JSON của /oauth2/token (RFC 6749 §5.1 cùng phần mở rộng của Medplum, token.ts:1157).
type TokenResponse map[string]any

// accessPolicies đọc các AccessPolicy của membership (cùng kiểm tra như ResolveIPAccessRules: đọc lỗi hoặc policy của project khác
// là lỗi chung, không lộ cấu hình).
func (s *Service) accessPolicies(ctx context.Context, m *fhir.ProjectMembership) ([]fhir.AccessPolicy, error) {
	projectID := idOf(refOf(m.Project))
	var out []fhir.AccessPolicy
	for _, ref := range accessPolicyRefs(m) {
		p, err := s.AccessPolicies.Get(ctx, idOf(ref.Reference))
		if err != nil || p == nil || (p.Meta != nil && p.Meta.Project != "" && p.Meta.Project != projectID) {
			return nil, domain.BadRequest(invalidAccessPolicy)
		}
		out = append(out, *p)
	}
	return out, nil
}

// tokenClaimsFor dựng phần claim phụ thuộc quyền (scope vai trò, fhir_access_policy) và kiểm tra trước khi ghi bất cứ thứ gì: chính sách
// không hỗ trợ hoặc quá lớn thì từ chối cấp token (fail đóng).
func (s *Service) authorization(ctx context.Context, loginScope string, m *fhir.ProjectMembership) (scope string, policy []map[string]any, oerr *domain.OAuthError) {
	pols, err := s.accessPolicies(ctx, m)
	if err != nil {
		return "", nil, domain.ErrAccessDenied(invalidAccessPolicy)
	}
	claim, narrowed, err := domain.PolicyClaim(pols)
	if len(narrowed) > 0 {
		s.Logger.InfoContext(ctx, "access policy được thu hẹp khi dịch sang claim", "membership", m.ID, "changes", narrowed)
	}
	if err == nil && claim != nil {
		generic := make([]any, len(claim))
		for i, c := range claim {
			generic[i] = c
		}
		if _, perr := fhirauth.ParsePolicy(generic); perr != nil { // cùng bộ phân tích mà fhir-server sẽ dùng
			err = errors.New(domain.ErrUnsupportedPolicy + ": " + perr.Error())
		}
	}
	if err == nil && claim != nil {
		limit := s.Tokens.MaxPolicyBytes
		if limit <= 0 {
			limit = defaultMaxClaim
		}
		if raw, _ := json.Marshal(claim); len(raw) > limit {
			err = errors.New("claim chính sách quá lớn")
		}
	}
	if err != nil {
		s.Logger.WarnContext(ctx, "access policy cannot be issued as a token claim", "membership", m.ID, "error", err.Error())
		return "", nil, domain.ErrAccessDenied(domain.ErrUnsupportedPolicy)
	}
	terms := append(domain.ScopeTerms(loginScope), domain.RoleScopes(loginScope, m, pols)...)
	return strings.Join(terms, " "), claim, nil
}

func (s *Service) lifetime(override string, def time.Duration) time.Duration {
	if override != "" {
		if d, err := domain.ParseTokenDuration(override); err == nil {
			return d
		}
		s.Logger.Warn("thời hạn token của client không hợp lệ, dùng mặc định", "value", override)
	}
	return def
}

func nowSeconds(t time.Time) int64 { return t.Unix() }

func (s *Service) audienceClaim() any {
	if len(s.Tokens.Audiences) == 1 {
		return s.Tokens.Audiences[0]
	}
	return s.Tokens.Audiences
}

// IssueTokens ký id token, access token và (nếu login có refreshSecret) refresh token cho một login ĐÃ được cấp (granted) và có
// membership. scope và policy lấy từ authorization(); ghi login (granted) là việc của người gọi.
func (s *Service) IssueTokens(ctx context.Context, login *fhir.Login, client *fhir.ClientApplication, m *fhir.ProjectMembership, project *fhir.Project, launch *fhir.SmartAppLaunch, scopeClaim string, policy []map[string]any) (TokenResponse, *domain.OAuthError) {
	if s.Signer == nil {
		return nil, domain.ErrServer()
	}
	now := s.Clock.Now()
	var clientApp *fhir.ClientApplication
	if client != nil {
		clientApp = client
	}
	accessTTL, refreshTTL := s.Tokens.AccessTTL, s.Tokens.RefreshTTL
	if clientApp != nil {
		accessTTL = s.lifetime(clientApp.AccessTokenLifetime, accessTTL)
		refreshTTL = s.lifetime(clientApp.RefreshTokenLifetime, refreshTTL)
	}
	userRef := refOf(login.User)
	sub := idOf(userRef)
	clientID := ""
	if login.Client != nil {
		clientID = idOf(login.Client.Reference)
	}
	base := func(ttl time.Duration, use string) map[string]any {
		return map[string]any{
			"iss": s.Tokens.Issuer, "iat": nowSeconds(now), "nbf": nowSeconds(now), "exp": nowSeconds(now.Add(ttl)),
			"jti": domain.GenerateSecret(16), "token_use": use,
		}
	}
	email := ""
	if domain.HasScopeTerm(login.Scope, "email") && strings.HasPrefix(userRef, "User/") {
		if u, err := s.Users.Get(ctx, sub); err == nil {
			email = u.Email
		}
	}

	access := base(accessTTL, tokenUseAccess)
	access["aud"] = s.audienceClaim()
	access["sub"], access["username"], access["login_id"], access["scope"] = sub, sub, login.ID, scopeClaim
	access["fhir_project"] = project.ID
	if clientID != "" {
		access["client_id"] = clientID
	}
	if profile := refOf(m.Profile); profile != "" {
		access["profile"] = profile
	}
	if email != "" {
		access["email"] = email
	}
	if policy != nil {
		access["fhir_access_policy"] = policy
	}

	id := base(s.Tokens.IDTTL, tokenUseID)
	id["sub"], id["login_id"], id["nonce"] = sub, login.ID, login.Nonce
	if authTime, err := time.Parse(time.RFC3339Nano, login.AuthTime); err == nil {
		id["auth_time"] = authTime.Unix()
	}
	if clientID != "" {
		id["aud"], id["client_id"] = clientID, clientID
	}
	if profile := refOf(m.Profile); profile != "" {
		id["fhirUser"] = profile
	}
	if email != "" {
		id["email"] = email
	}

	accessJWT, err1 := s.Signer.Sign(access)
	idJWT, err2 := s.Signer.Sign(id)
	if err1 != nil || err2 != nil {
		s.Logger.ErrorContext(ctx, "không ký được token", "access", err1, "id", err2)
		return nil, domain.ErrServer()
	}
	resp := TokenResponse{
		"token_type": "Bearer", "expires_in": int64(accessTTL / time.Second), "scope": login.Scope,
		"id_token": idJWT, "access_token": accessJWT,
		"project": map[string]string{"reference": "Project/" + project.ID},
	}
	if profile := refOf(m.Profile); profile != "" {
		resp["profile"] = map[string]string{"reference": profile}
	}
	if login.RefreshSecret != "" {
		rc := base(refreshTTL, tokenUseRefresh)
		rc["aud"], rc["login_id"], rc["refresh_secret"] = s.Tokens.refreshAudience(), login.ID, login.RefreshSecret
		if clientID != "" {
			rc["client_id"] = clientID
		}
		refreshJWT, err := s.Signer.Sign(rc)
		if err != nil {
			s.Logger.ErrorContext(ctx, "không ký được refresh token", "error", err)
			return nil, domain.ErrServer()
		}
		resp["refresh_token"] = refreshJWT
	}
	s.addLaunchContext(resp, m, launch)
	if oerr := s.addHub(ctx, resp, sub, scopeClaim); oerr != nil {
		return nil, oerr
	}
	if s.Tokens.BaseURL != "" {
		resp["smart_style_url"] = strings.TrimRight(s.Tokens.BaseURL, "/") + "/.well-known/smart-styles.json"
	}
	return resp, nil
}

// ---- client_credentials

// ClientCredentialsRequest là yêu cầu grant client_credentials đã được lớp presentation tách (Basic hoặc body).
type ClientCredentialsRequest struct {
	// Authenticated là client đã xác thực bằng private_key_jwt (thay cho ClientID/ClientSecret); nil nếu dùng secret.
	Authenticated          *fhir.ClientApplication
	ClientID, ClientSecret string
	Scope                  string
	RemoteAddress          string
	UserAgent              string
}

// AuthenticateClient kiểm client đang hoạt động và secret khớp (token.ts:135-165).
func (s *Service) AuthenticateClient(ctx context.Context, clientID, secret string) (*fhir.ClientApplication, *domain.OAuthError) {
	if clientID == "" {
		return nil, domain.ErrInvalidRequest("Missing client_id")
	}
	if secret == "" {
		return nil, domain.ErrInvalidRequest("Missing client_secret")
	}
	client, err := s.Clients.Get(ctx, clientID)
	if err != nil {
		if domain.KindOf(err) == domain.KindNotFound {
			return nil, domain.ErrInvalidRequest("Invalid client")
		}
		s.Logger.ErrorContext(ctx, "đọc client lỗi", "error", err)
		return nil, domain.ErrServer()
	}
	if client.Status != "" && client.Status != "active" {
		return nil, domain.ErrInvalidRequest("Invalid client")
	}
	if client.Secret == "" {
		return nil, domain.ErrInvalidRequest("Invalid client")
	}
	if !domain.MatchClientSecret(client, secret) {
		return nil, domain.ErrInvalidRequest("Invalid secret")
	}
	return client, nil
}

// clientMembership chọn membership của client (D-17): ưu tiên membership của project chứa client; không có hoặc mơ hồ thì lỗi.
func (s *Service) clientMembership(ctx context.Context, client *fhir.ClientApplication) (*fhir.ProjectMembership, *domain.OAuthError) {
	projectRef := ""
	if client.Meta != nil && client.Meta.Project != "" {
		projectRef = "Project/" + client.Meta.Project
	}
	list, err := s.Memberships.ListForUser(ctx, "ClientApplication/"+client.ID, projectRef, 2)
	if err != nil {
		s.Logger.ErrorContext(ctx, "đọc membership của client lỗi", "error", err)
		return nil, domain.ErrServer()
	}
	if len(list) != 1 { // không có, hoặc nhiều membership mà không chọn được duy nhất một
		return nil, domain.ErrInvalidRequest("Invalid client")
	}
	return &list[0], nil
}

// ClientCredentials xử lý grant client_credentials (token.ts:135-215). Khác Medplum: scope do client khai bị lọc (cấm fhir.*),
// chính sách không hỗ trợ bị từ chối, và Login chỉ được tạo SAU khi mọi kiểm tra (IP, chính sách) đã qua.
func (s *Service) ClientCredentials(ctx context.Context, req ClientCredentialsRequest) (TokenResponse, *domain.OAuthError) {
	client := req.Authenticated
	if client == nil {
		var oerr *domain.OAuthError
		if client, oerr = s.AuthenticateClient(ctx, req.ClientID, req.ClientSecret); oerr != nil {
			return nil, oerr
		}
	}
	m, oerr := s.clientMembership(ctx, client)
	if oerr != nil {
		return nil, oerr
	}
	project, err := s.Projects.Get(ctx, idOf(refOf(m.Project)))
	if err != nil {
		if domain.KindOf(err) == domain.KindNotFound {
			return nil, domain.ErrInvalidRequest("Invalid client")
		}
		return nil, domain.ErrServer()
	}
	if reason := domain.InactiveReason(project, m); reason != "" {
		return nil, domain.ErrAccessDenied(reason)
	}
	scope := req.Scope
	if scope == "" {
		scope = "openid"
	}
	if strings.Contains(scope, "patient/") {
		return nil, domain.ErrInvalidScope("Cannot use client credentials with patient scope")
	}
	if oerr := domain.ValidateRequestedScope(scope); oerr != nil {
		return nil, oerr
	}
	rules, err := s.ResolveIPAccessRules(ctx, m)
	if err != nil {
		return nil, domain.ErrAccessDenied(invalidAccessPolicy)
	}
	if err := domain.CheckIPAccessRules(req.RemoteAddress, rules); err != nil {
		return nil, domain.ErrInvalidRequest("IP address not allowed")
	}
	scopeClaim, policy, oerr := s.authorization(ctx, scope, m)
	if oerr != nil {
		return nil, oerr
	}

	clientRef := &fhir.Reference{Reference: "ClientApplication/" + client.ID}
	login, err := s.Logins.Create(ctx, &fhir.Login{
		ResourceType: "Login", AuthMethod: "client", User: clientRef, Client: clientRef,
		Project: newRef("Project", project.ID, project.Name), Membership: newRef("ProjectMembership", m.ID, ""),
		AuthTime: s.Clock.Now().UTC().Format("2006-01-02T15:04:05.000Z"), Granted: ptrTrue(), Scope: scope,
		RemoteAddress: req.RemoteAddress, UserAgent: req.UserAgent,
	})
	if err != nil {
		s.Logger.ErrorContext(ctx, "tạo Login lỗi", "error", err)
		return nil, domain.ErrServer()
	}
	return s.IssueTokens(ctx, login, client, m, project, nil, scopeClaim, policy)
}

// ---- Bearer

var errBearer = domain.Unauthorized("Unauthorized")

// Authenticate xác minh Bearer access token do auth ký (cài đặt application.Authenticator). Có trạng thái: kiểm Login chưa bị thu hồi
// và membership, project còn hoạt động mỗi lần gọi, nên thu hồi có hiệu lực ngay ở các route của auth.
func (s *Service) Authenticate(ctx context.Context, raw string) (*AuthState, error) {
	if s.Verifier == nil || raw == "" {
		return nil, errBearer
	}
	claims, err := s.Verifier.Verify(raw)
	if err != nil {
		return nil, errBearer
	}
	if iss, _ := claims["iss"].(string); iss != s.Tokens.Issuer {
		return nil, errBearer
	}
	if use, _ := claims["token_use"].(string); use != tokenUseAccess {
		return nil, errBearer
	}
	if _, has := claims["refresh_secret"]; has || !audienceMatches(claims["aud"], s.Tokens.Audiences) {
		return nil, errBearer
	}
	loginID, _ := claims["login_id"].(string)
	if loginID == "" {
		return nil, errBearer
	}
	login, err := s.Logins.Get(ctx, loginID)
	if err != nil || domain.IsTrue(login.Revoked) || !domain.IsTrue(login.Granted) || login.Membership == nil {
		return nil, errBearer
	}
	m, err := s.Memberships.Get(ctx, idOf(login.Membership.Reference))
	if err != nil {
		return nil, errBearer
	}
	project, err := s.Projects.Get(ctx, idOf(refOf(m.Project)))
	if err != nil || domain.InactiveReason(project, m) != "" {
		return nil, errBearer
	}
	return &AuthState{Login: login, Membership: m, Project: project}, nil
}

func audienceMatches(claim any, allowed []string) bool {
	has := func(a string) bool {
		for _, x := range allowed {
			if x == a {
				return true
			}
		}
		return false
	}
	switch v := claim.(type) {
	case string:
		return has(v)
	case []any:
		for _, e := range v {
			if s, ok := e.(string); ok && has(s) {
				return true
			}
		}
	}
	return false
}

// launchValue là giá trị `patient`/`encounter` trong phản hồi: ưu tiên identifier.value, không thì id của tham chiếu (token.ts:1128).
func launchValue(r *fhir.Reference) string {
	if r == nil {
		return ""
	}
	if r.Identifier != nil && r.Identifier.Value != "" {
		return r.Identifier.Value
	}
	return idOf(r.Reference)
}

// addLaunchContext thêm ngữ cảnh SMART App Launch (patient, encounter, fhirContext). Profile của membership là Patient thì patient
// là chính nó (ghi đè theo token.ts:1137).
func (s *Service) addLaunchContext(resp TokenResponse, m *fhir.ProjectMembership, launch *fhir.SmartAppLaunch) {
	patient := ""
	if launch != nil {
		patient = launchValue(launch.Patient)
		if v := launchValue(launch.Encounter); v != "" {
			resp["encounter"] = v
		}
		if len(launch.FhirContext) > 0 {
			resp["fhirContext"] = launch.FhirContext
		}
	}
	if profile := refOf(m.Profile); strings.HasPrefix(profile, "Patient/") {
		patient = idOf(profile)
	}
	if patient != "" {
		resp["patient"], resp["need_patient_banner"] = patient, true
	}
}
