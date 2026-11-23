package bootstrap

import (
	"context"
	"errors"
	"testing"

	"trivita-fhir/apps/ws-gateway/internal/presentation/httpapi"
	"trivita-fhir/pkg/fhirauth"
)

type fakeVerifier struct {
	c   *fhirauth.Claims
	err error
}

func (f fakeVerifier) VerifyClaims(context.Context, string) (*fhirauth.Claims, error) {
	return f.c, f.err
}

func TestAuthenticator(t *testing.T) { // F-2
	ok := &fhirauth.Claims{Subject: "alice", Project: "p1", Scopes: []string{"openid", "fhircast/*"}}
	for name, c := range map[string]struct {
		v     fakeVerifier
		scope bool
		want  error
		who   httpapi.Caller
	}{
		"hợp lệ":                     {fakeVerifier{c: ok}, true, nil, httpapi.Caller{Subject: "alice", Project: "p1"}},
		"scope con của fhircast":     {fakeVerifier{c: &fhirauth.Claims{Subject: "a", Project: "p", Scopes: []string{"fhircast/patient-open"}}}, true, nil, httpapi.Caller{Subject: "a", Project: "p"}},
		"thiếu scope":                {fakeVerifier{c: &fhirauth.Claims{Subject: "a", Project: "p", Scopes: []string{"fhir.write", "openid"}}}, true, httpapi.ErrForbidden, httpapi.Caller{}},
		"scope giống tiền tố":        {fakeVerifier{c: &fhirauth.Claims{Subject: "a", Project: "p", Scopes: []string{"fhircastx", "xfhircast/*"}}}, true, httpapi.ErrForbidden, httpapi.Caller{}},
		"tắt đòi scope":              {fakeVerifier{c: &fhirauth.Claims{Subject: "a", Project: "p", Scopes: []string{"fhir.write"}}}, false, nil, httpapi.Caller{Subject: "a", Project: "p"}},
		"thiếu project":              {fakeVerifier{c: &fhirauth.Claims{Subject: "a", Scopes: []string{"fhircast/*"}}}, true, httpapi.ErrForbidden, httpapi.Caller{}},
		"thiếu project dù tắt scope": {fakeVerifier{c: &fhirauth.Claims{Subject: "a"}}, false, httpapi.ErrForbidden, httpapi.Caller{}},
		"token sai":                  {fakeVerifier{err: errors.Join(fhirauth.ErrInvalidToken, errors.New("x"))}, true, httpapi.ErrUnauthorized, httpapi.Caller{}},
	} {
		got, err := authenticator(c.v, c.scope)(context.Background(), "tok")
		if !errors.Is(err, c.want) && !(c.want == nil && err == nil) {
			t.Errorf("%s: lỗi %v, muốn %v", name, err, c.want)
		}
		if got != c.who {
			t.Errorf("%s: caller %+v, muốn %+v", name, got, c.who)
		}
	}
	boom := errors.New("jwks down")
	if _, err := authenticator(fakeVerifier{err: boom}, true)(context.Background(), "t"); !errors.Is(err, boom) || errors.Is(err, httpapi.ErrUnauthorized) {
		t.Errorf("lỗi hạ tầng không được đổi thành 401: %v", err)
	}
}
