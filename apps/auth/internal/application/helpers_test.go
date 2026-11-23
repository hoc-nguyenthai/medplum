package application_test

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"trivita-fhir/apps/auth/internal/application"
	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/apps/auth/internal/infrastructure/mailer"
	"trivita-fhir/apps/auth/internal/infrastructure/mfastate"
	"trivita-fhir/apps/auth/internal/infrastructure/oidc"
	"trivita-fhir/apps/auth/internal/infrastructure/password"
	"trivita-fhir/apps/auth/internal/infrastructure/secretbox"
	"trivita-fhir/apps/auth/internal/infrastructure/store"
	"trivita-fhir/apps/auth/internal/infrastructure/store/storetest"
	"trivita-fhir/apps/auth/internal/infrastructure/totp"
	"trivita-fhir/apps/auth/internal/infrastructure/useragent"
	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirrouter"
)

func ptr[T any](v T) *T { return &v }

// countingHasher bọc bcrypt thật và đếm số lần so khớp, để kiểm hash giả.
type countingHasher struct {
	*password.Bcrypt
	compares, dummies atomic.Int32
}

func (h *countingHasher) Compare(pw, hash string) bool {
	h.compares.Add(1)
	return h.Bcrypt.Compare(pw, hash)
}
func (h *countingHasher) CompareDummy(pw string) { h.dummies.Add(1); h.Bcrypt.CompareDummy(pw) }

type fakePwned struct {
	bad map[string]int
	err error
}

func (f *fakePwned) Count(_ context.Context, pw string) (int, error) { return f.bad[pw], f.err }

type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time { return c.t }

type env struct {
	t       *testing.T
	repo    fhirrouter.Repository
	r       store.Repos
	svc     *application.Service
	hasher  *countingHasher
	pwned   *fakePwned
	clock   *fakeClock
	mail    *mailer.Fake
	captcha *fakeCaptcha
}

func newEnv(t *testing.T, b storetest.Backend) *env {
	t.Helper()
	repo := b.Repo(t)
	st := store.New(repo)
	r := st.Repos()
	bc, err := password.NewBcrypt(bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, repo: repo, r: r, hasher: &countingHasher{Bcrypt: bc}, pwned: &fakePwned{bad: map[string]int{}}, clock: &fakeClock{time.Now()}, mail: &mailer.Fake{}, captcha: &fakeCaptcha{ok: true}}
	e.svc = application.NewService(application.Deps{
		Users: r.Users, Logins: r.Logins, Memberships: r.Memberships, DomainConfigs: r.DomainConfigs, UserConfigs: r.UserConfigs, Projects: r.Projects, Clients: r.Clients,
		Launches: r.Launches, AccessPolicies: r.AccessPolicies, SecurityRequests: r.SecurityRequests, Resources: r.Resources, Tx: r.Tx,
		Hasher: e.hasher, UserAgents: useragent.Parser{}, Pwned: e.pwned, Clock: e.clock, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		StandardClients: domain.StandardClients(true, nil),
		Recaptcha:       e.captcha,
		OutboundURL:     oidc.CheckURL,
		TOTP:            totp.Authenticator{}, QR: totp.QRCode{}, MFAState: mfastate.NewMemory(func() time.Time { return e.clock.Now() }), MFASecrets: mustBox(),
		Mailer: e.mail, Mail: application.MailConfig{AppBaseURL: "https://app.example.org", AppName: "Trivita"},
	})
	return e
}

func eachEnv(t *testing.T, fn func(t *testing.T, e *env)) {
	for _, b := range storetest.Backends() {
		t.Run(b.Name, func(t *testing.T) { fn(t, newEnv(t, b)) })
	}
}

func put[T fhir.Resource](e *env, r T) T {
	e.t.Helper()
	out, err := e.repo.CreateResource(context.Background(), r)
	if err != nil {
		e.t.Fatal(err)
	}
	return out.(T)
}

func (e *env) project(mut func(*fhir.Project)) *fhir.Project {
	p := &fhir.Project{ResourceType: "Project", Name: "Dự án"}
	if mut != nil {
		mut(p)
	}
	return put(e, p)
}

func (e *env) user(email string, mut func(*fhir.User)) *fhir.User {
	u := &fhir.User{ResourceType: "User", Email: email, FirstName: "An", LastName: "Nguyen"}
	if mut != nil {
		mut(u)
	}
	return put(e, u)
}

func (e *env) membership(user *fhir.User, project *fhir.Project, mut func(*fhir.ProjectMembership)) *fhir.ProjectMembership {
	m := &fhir.ProjectMembership{
		ResourceType: "ProjectMembership",
		User:         &fhir.Reference{Reference: "User/" + user.ID},
		Project:      &fhir.Reference{Reference: "Project/" + project.ID},
		Profile:      &fhir.Reference{Reference: "Practitioner/p1"},
	}
	if mut != nil {
		mut(m)
	}
	return put(e, m)
}

func (e *env) login(user *fhir.User, mut func(*fhir.Login)) *fhir.Login {
	l := &fhir.Login{ResourceType: "Login", User: &fhir.Reference{Reference: "User/" + user.ID}, AuthMethod: "password", AuthTime: "2026-01-01T00:00:00Z", Scope: "openid"}
	if mut != nil {
		mut(l)
	}
	return put(e, l)
}

func (e *env) policy(project *fhir.Project, rules ...fhir.AccessPolicyIpAccessRule) *fhir.AccessPolicy {
	return put(e, &fhir.AccessPolicy{ResourceType: "AccessPolicy", Name: "AP", IpAccessRule: rules, Meta: &fhir.Meta{Project: project.ID}})
}

func wantErr(t *testing.T, err error, kind domain.Kind, msg string) {
	t.Helper()
	if !domain.Is(err, kind, msg) {
		t.Fatalf("mong lỗi kind=%d %q, được %v", kind, msg, err)
	}
}

func isConflictErr(err error) bool { return errors.Is(err, domain.ErrVersionConflict) }

// codeStoredAs kiểm kết quả có code và cookie, và Login CHỈ lưu băm của chúng (không có bản rõ), kèm mốc hết hạn.
func (e *env) codeStoredAs(t *testing.T, loginID string, res *application.LoginResult) bool {
	t.Helper()
	if res == nil || res.Code == "" || res.Cookie == "" {
		return false
	}
	l, err := e.svc.Logins.Get(context.Background(), loginID)
	if err != nil {
		t.Fatal(err)
	}
	return l.Code == domain.HashSecret(res.Code) && l.Cookie == domain.HashSecret(res.Cookie) && l.Code != res.Code && l.Cookie != res.Cookie && l.ExpiresAt != ""
}

// fakeCaptcha ghi lại secret và token được xác minh.
type fakeCaptcha struct {
	mu      sync.Mutex
	ok      bool
	err     error
	secrets []string
	tokens  []string
}

func (f *fakeCaptcha) Verify(_ context.Context, secret, token string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.secrets, f.tokens = append(f.secrets, secret), append(f.tokens, token)
	return f.ok, f.err
}

func mustBox() application.SecretBox {
	b, err := secretbox.ParseKeys("k1:" + base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	if err != nil {
		panic(err)
	}
	return b
}
