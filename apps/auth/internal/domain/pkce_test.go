package domain

import (
	"strings"
	"testing"

	"trivita-fhir/pkg/fhir"
)

func TestChallengeS256MatchesRFC7636Example(t *testing.T) {
	// RFC 7636 Appendix B
	if got := ChallengeS256("dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"); got != "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM" {
		t.Errorf("S256 = %s", got)
	}
}

func TestVerifyPKCE(t *testing.T) {
	v := strings.Repeat("a", 43)
	s := ChallengeS256(v)
	cases := []struct {
		name                      string
		challenge, method, verify string
		allowPlain, want          bool
	}{
		{"S256 đúng", s, "S256", v, false, true},
		{"S256 sai", s, "S256", strings.Repeat("b", 43), false, false},
		{"S256 verifier rỗng", s, "S256", "", false, false},
		{"S256 quá ngắn", ChallengeS256("abc"), "S256", "abc", false, false},
		{"S256 quá dài", ChallengeS256(strings.Repeat("a", 129)), "S256", strings.Repeat("a", 129), false, false},
		{"S256 ký tự cấm", ChallengeS256(strings.Repeat("a", 42) + "!"), "S256", strings.Repeat("a", 42) + "!", false, false},
		{"không có challenge", "", "S256", v, false, false},
		{"plain bị tắt", v, "plain", v, false, false},
		{"plain bật, đúng", v, "plain", v, true, true},
		{"plain bật, sai", v, "plain", strings.Repeat("b", 43), true, false},
		{"method rỗng coi là plain (bị tắt)", v, "", v, false, false},
		{"method lạ", s, "S512", v, true, false},
		{"S256 nhưng gửi challenge làm verifier", s, "S256", s, false, false},
	}
	for _, c := range cases {
		if got := VerifyPKCE(c.challenge, c.method, c.verify, c.allowPlain); got != c.want {
			t.Errorf("%s: %v, muốn %v", c.name, got, c.want)
		}
	}
}

func TestHashSecretAndRedirectExtension(t *testing.T) {
	if h := HashSecret("abc"); h != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" || len(HashSecret("x")) != 64 {
		t.Errorf("HashSecret = %s", h)
	}
	l := &fhir.Login{Extension: []fhir.Extension{{URL: "khac", ValueUri: "x"}}}
	if LoginRedirectURI(l) != "" {
		t.Error("chưa có")
	}
	SetLoginRedirectURI(l, "https://a/cb")
	SetLoginRedirectURI(l, "https://b/cb") // thay, không thêm
	if LoginRedirectURI(l) != "https://b/cb" || len(l.Extension) != 2 {
		t.Errorf("%+v", l.Extension)
	}
}

func TestValidAudienceRedirectAndMaxAge(t *testing.T) {
	allowed := []string{"https://auth.example.org", "", "https://fhir.example.org/fhir/R4"}
	for _, aud := range []string{"", "https://auth.example.org", "https://AUTH.example.org/x/y", "https://fhir.example.org/anything"} {
		if !ValidAudience(aud, allowed...) {
			t.Errorf("%q phải hợp lệ", aud)
		}
	}
	for _, aud := range []string{"http://auth.example.org", "https://evil.example.org", "https://auth.example.org.evil.com", "không-phải-url", "://x", "https://"} {
		if ValidAudience(aud, allowed...) {
			t.Errorf("%q không được hợp lệ", aud)
		}
	}
	if ValidAudience("https://x", "") {
		t.Error("không có địa chỉ cho phép thì chỉ aud rỗng hợp lệ")
	}
	for _, ok := range []string{"https://a/cb", "http://localhost:9615", "myapp://callback"} {
		if !ValidRedirectURI(ok) {
			t.Errorf("%q", ok)
		}
	}
	for _, bad := range []string{"", "://x", "app.example.org/cb", "/relative"} {
		if ValidRedirectURI(bad) {
			t.Errorf("%q không hợp lệ", bad)
		}
	}
	for in, want := range map[string]int64{"": 3600, "0": 0, "600": 600} {
		if got, ok := ParseMaxAge(in); !ok || got != want {
			t.Errorf("%q = %d %v", in, got, ok)
		}
	}
	for _, bad := range []string{"abc", "-1", "1.5", "1e3", " 5", "99999999999999999999999"} {
		if _, ok := ParseMaxAge(bad); ok {
			t.Errorf("%q phải bị từ chối", bad)
		}
	}
}
