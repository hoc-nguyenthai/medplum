package domain

import (
	"encoding/base64"
	"net/url"
	"testing"

	"trivita-fhir/pkg/fhir"
)

func TestResolveExternalIdentityTable(t *testing.T) {
	yes := true
	for name, tc := range map[string]struct {
		idp      fhir.IdentityProvider
		claims   map[string]any
		email    string
		external string
		err      string
	}{
		"mặc định: email":                       {fhir.IdentityProvider{}, map[string]any{"email": "An@Example.ORG"}, "an@example.org", "", ""},
		"mặc định thiếu email":                  {fhir.IdentityProvider{}, map[string]any{"sub": "s"}, "", "", "External token does not contain email address"},
		"email rỗng":                            {fhir.IdentityProvider{}, map[string]any{"email": ""}, "", "", "External token does not contain email address"},
		"email sai kiểu":                        {fhir.IdentityProvider{}, map[string]any{"email": 5}, "", "", "External token does not contain email address"},
		"useSubject: sub":                       {fhir.IdentityProvider{UseSubject: &yes}, map[string]any{"sub": "abc", "email": "x@y.z"}, "", "abc", ""},
		"useSubject thiếu sub":                  {fhir.IdentityProvider{UseSubject: &yes}, map[string]any{"email": "x@y.z"}, "", "", "External token does not contain subject"},
		"sub rỗng":                              {fhir.IdentityProvider{UseSubject: &yes}, map[string]any{"sub": ""}, "", "", "External token does not contain subject"},
		"tường minh email + user-email":         {fhir.IdentityProvider{IdentitySource: "email", IdentityMappingMode: "user-email"}, map[string]any{"email": "a@b.co"}, "a@b.co", "", ""},
		"tường minh subject + external-id":      {fhir.IdentityProvider{IdentitySource: "subject", IdentityMappingMode: "project-membership-external-id"}, map[string]any{"sub": "7"}, "", "7", ""},
		"subject + user-email không hỗ trợ":     {fhir.IdentityProvider{IdentitySource: "subject", IdentityMappingMode: "user-email"}, map[string]any{"sub": "7", "email": "a@b.co"}, "", "", "Unsupported identity provider configuration"},
		"email + external-id không hỗ trợ":      {fhir.IdentityProvider{IdentitySource: "email", IdentityMappingMode: "project-membership-external-id"}, map[string]any{"email": "a@b.co"}, "", "", "Unsupported identity provider configuration"},
		"fhir-user không hỗ trợ":                {fhir.IdentityProvider{IdentitySource: "fhir-user"}, map[string]any{"email": "a@b.co"}, "", "", "Unsupported identity provider configuration"},
		"danh sách email không bị tách":         {fhir.IdentityProvider{}, map[string]any{"email": "a@x.com,b@y.com"}, "a@x.com,b@y.com", "", ""},
		"chỉ nguồn đặt, chế độ theo useSubject": {fhir.IdentityProvider{IdentitySource: "email"}, map[string]any{"email": "a@b.co"}, "a@b.co", "", ""},
	} {
		idp := tc.idp
		email, ext, err := ResolveExternalIdentity(&idp, tc.claims)
		if tc.err != "" {
			if !Is(err, KindBadRequest, tc.err) {
				t.Errorf("%s: %v, mong %q", name, err, tc.err)
			}
			continue
		}
		if err != nil || email != tc.email || ext != tc.external {
			t.Errorf("%s: (%q,%q,%v), mong (%q,%q)", name, email, ext, err, tc.email, tc.external)
		}
	}
}

func TestIsExternalEmailUnverified(t *testing.T) {
	for name, tc := range map[string]struct {
		claims map[string]any
		want   bool
	}{
		"false":       {map[string]any{"email_verified": false}, true},
		"true":        {map[string]any{"email_verified": true}, false},
		"chuỗi false": {map[string]any{"email_verified": "false"}, true},
		"chuỗi true":  {map[string]any{"email_verified": "true"}, false},
		"vắng mặt":    {map[string]any{}, false},
		"kiểu lạ":     {map[string]any{"email_verified": 0}, false},
	} {
		if got := IsExternalEmailUnverified(tc.claims); got != tc.want {
			t.Errorf("%s: %v", name, got)
		}
	}
}

func TestValidReturnTo(t *testing.T) {
	allowed := []string{"https://app.example.org/signin", "http://localhost:3000/", "không phải url"}
	for name, tc := range map[string]struct {
		returnTo string
		want     bool
	}{
		"đúng đường dẫn":      {"https://app.example.org/signin", true},
		"đường dẫn con":       {"https://app.example.org/signin/next?x=1", true},
		"khác đường dẫn":      {"https://app.example.org/other", false},
		"khác host":           {"https://evil.example.org/signin", false},
		"host đánh lừa":       {"https://app.example.org.evil.com/signin", false},
		"khác protocol":       {"http://app.example.org/signin", false},
		"khác port":           {"https://app.example.org:8443/signin", false},
		"localhost đúng port": {"http://localhost:3000/anything", true},
		"localhost sai port":  {"http://localhost:3001/", false},
		"userinfo đánh lừa":   {"https://app.example.org@evil.com/signin", false},
		"không có scheme":     {"//app.example.org/signin", false},
		"đường dẫn tương đối": {"/signin", false},
		"rỗng":                {"", false},
		"không parse được":    {"https://%zz", false},
		"javascript":          {"javascript:alert(1)", false},
	} {
		if got := ValidReturnTo(tc.returnTo, allowed); got != tc.want {
			t.Errorf("%s (%q): %v", name, tc.returnTo, got)
		}
	}
	if ValidReturnTo("https://app.example.org/signin", nil) {
		t.Error("không có danh sách cho phép thì không hợp lệ")
	}
}

func TestMatchRedirectURIPartial(t *testing.T) {
	c := &fhir.ClientApplication{RedirectUris: []string{"https://app.example.org/cb", "http://localhost:9615"}}
	for name, tc := range map[string]struct{ in, want string }{
		"đúng":              {"https://app.example.org/cb", "https://app.example.org/cb"},
		"con của đường dẫn": {"https://app.example.org/cb/deep?x=1", "https://app.example.org/cb"},
		"khác đường dẫn":    {"https://app.example.org/other", ""},
		"khác origin":       {"https://evil.example.org/cb", ""},
		"khác port":         {"https://app.example.org:444/cb", ""},
		"khác scheme":       {"http://app.example.org/cb", ""},
		"localhost":         {"http://localhost:9615/anything", "http://localhost:9615"},
		"không có scheme":   {"app.example.org/cb", ""},
		"rỗng":              {"", ""},
	} {
		if got := MatchRedirectURIPartial(c, tc.in); got != tc.want {
			t.Errorf("%s: %q, mong %q", name, got, tc.want)
		}
	}
}

func TestBuildAuthorizeURL(t *testing.T) {
	idp := &fhir.IdentityProvider{AuthorizeUrl: "https://idp.example.org/authorize?x=1", ClientId: "cid"}
	got, err := BuildAuthorizeURL(idp, "https://auth.example.org/auth/external", "STATE.SIG")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(got)
	q := u.Query()
	if u.Host != "idp.example.org" || u.Path != "/authorize" || q.Get("x") != "1" || q.Get("client_id") != "cid" || q.Get("response_type") != "code" ||
		q.Get("scope") != "openid profile email" || q.Get("redirect_uri") != "https://auth.example.org/auth/external" || q.Get("state") != "STATE.SIG" {
		t.Errorf("%s", got)
	}
	noState, _ := BuildAuthorizeURL(idp, "https://a/b", "")
	if nu, _ := url.Parse(noState); nu.Query().Has("state") {
		t.Error("không state thì không có tham số state")
	}
	for _, bad := range []string{"", "idp.example.org/authorize", "/authorize", "https://", "://x"} {
		if _, err := BuildAuthorizeURL(&fhir.IdentityProvider{AuthorizeUrl: bad, ClientId: "c"}, "https://a/b", ""); !Is(err, KindBadRequest, "Failed to construct URL for the domain") {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

func TestSubjectTokenAudienceOKTable(t *testing.T) {
	jwt := func(payload string) string {
		enc := base64.RawURLEncoding.EncodeToString
		return enc([]byte(`{"alg":"RS256"}`)) + "." + enc([]byte(payload)) + ".sig"
	}
	idp := &fhir.IdentityProvider{ClientId: "cid", Audience: "api://x"}
	for name, tc := range map[string]struct {
		token string
		idp   *fhir.IdentityProvider
		want  bool
	}{
		"opaque":                  {"abcdef", idp, true},
		"hai phần":                {"a.b", idp, true},
		"payload không base64":    {"a.%%%.c", idp, true},
		"payload không json":      {"a." + base64.RawURLEncoding.EncodeToString([]byte("xx")) + ".c", idp, true},
		"aud chuỗi = client":      {jwt(`{"aud":"cid"}`), idp, true},
		"aud chuỗi = audience":    {jwt(`{"aud":"api://x"}`), idp, true},
		"aud mảng":                {jwt(`{"aud":["a","cid"]}`), idp, true},
		"aud mảng không khớp":     {jwt(`{"aud":["a","b"]}`), idp, false},
		"aud lạ":                  {jwt(`{"aud":"khac"}`), idp, false},
		"azp = client":            {jwt(`{"aud":"khac","azp":"cid"}`), idp, true},
		"azp lạ":                  {jwt(`{"aud":"khac","azp":"khac"}`), idp, false},
		"azp = audience không đủ": {jwt(`{"aud":"khac","azp":"api://x"}`), idp, false},
		"không aud":               {jwt(`{}`), idp, false},
		"aud sai kiểu":            {jwt(`{"aud":5}`), idp, false},
		"idp không client/aud":    {jwt(`{"aud":"cid"}`), &fhir.IdentityProvider{}, false},
		"chỉ có audience":         {jwt(`{"aud":"api://x"}`), &fhir.IdentityProvider{Audience: "api://x"}, true},
	} {
		if got := SubjectTokenAudienceOK(tc.token, tc.idp); got != tc.want {
			t.Errorf("%s: got %v want %v", name, got, tc.want)
		}
	}
}
