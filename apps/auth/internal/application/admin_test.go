package application_test

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"trivita-fhir/apps/auth/internal/application"
	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirsearch"
)

var hex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)

func (e *env) count(rt string) int {
	e.t.Helper()
	res, err := e.repo.Search(context.Background(), &fhirsearch.Request{ResourceType: rt, Count: 1000, Total: "none"})
	if err != nil {
		e.t.Fatal(err)
	}
	return len(res.Matches)
}

func TestPrepareUserAndCreateUser(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		u, err := e.svc.CreateUser(ctx, application.NewUser{FirstName: "An", LastName: "Nguyen", Email: "AN@Example.ORG", Password: "mat-khau-tot-123"})
		if err != nil || u.Email != "an@example.org" || u.Project != nil || u.Meta == nil {
			t.Fatalf("%+v %v", u, err)
		}
		if !e.hasher.Compare("mat-khau-tot-123", u.PasswordHash) {
			t.Error("mật khẩu phải được băm")
		}
		_, err = e.svc.CreateUser(ctx, application.NewUser{Email: "an@example.org", Password: "mat-khau-tot-123"})
		wantErr(t, err, domain.KindBadRequest, "Email already registered")

		// cùng email nhưng thuộc project khác thì được
		p := e.project(nil)
		inP, err := e.svc.CreateUser(ctx, application.NewUser{Email: "an@example.org", Password: "mat-khau-tot-123", ProjectID: p.ID})
		if err != nil || inP.Project == nil || inP.Project.Reference != "Project/"+p.ID || inP.Meta.Project != p.ID {
			t.Fatalf("%+v %v", inP, err)
		}
		// "new" coi như không gắn project
		prep, err := e.svc.PrepareUser(ctx, application.NewUser{Email: "x@example.org", Password: "mat-khau-tot-123", ProjectID: "new"})
		if err != nil || prep.Project != nil {
			t.Errorf("%+v %v", prep, err)
		}

		_, err = e.svc.PrepareUser(ctx, application.NewUser{Email: "  ", Password: "mat-khau-tot-123"})
		wantErr(t, err, domain.KindBadRequest, "Email is required")
		_, err = e.svc.PrepareUser(ctx, application.NewUser{Email: "a@b.c", Password: "ngan"})
		wantErr(t, err, domain.KindBadRequest, "Password must be at least 8 characters")
		e.pwned.bad["password123"] = 1
		_, err = e.svc.PrepareUser(ctx, application.NewUser{Email: "a@b.c", Password: "password123"})
		var de *domain.Error
		if !errors.As(err, &de) || de.Message != domain.PwnedMessage || de.Expression != "password" {
			t.Errorf("pwned phải gắn trường password: %v", err)
		}
		e.pwned.err = errors.New("mạng")
		_, err = e.svc.PrepareUser(ctx, application.NewUser{Email: "a@b.c", Password: "mat-khau-tot-123"})
		wantErr(t, err, domain.KindInternal, "")
	})
}

func TestCreateProjectStructure(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		admin := e.user("admin@example.org", nil)
		res, err := e.svc.CreateProject(ctx, e.r.Resources, application.ProjectInit{Name: "Bệnh viện A", Admin: admin, Features: []string{"bots"}})
		if err != nil {
			t.Fatal(err)
		}
		p := res.Project
		if p.Name != "Bệnh viện A" || !domain.IsTrue(p.StrictMode) || p.Owner == nil || p.Owner.Reference != "User/"+admin.ID || len(p.Features) != 1 {
			t.Errorf("project: %+v", p)
		}
		// 6 policy mặc định, đúng thứ tự, mỗi cái thuộc project này
		if len(p.DefaultAccessPolicies) != 6 {
			t.Fatalf("defaultAccessPolicies: %+v", p.DefaultAccessPolicies)
		}
		for i, pt := range domain.DefaultPolicyProfileTypes {
			d := p.DefaultAccessPolicies[i]
			pol, err := e.r.AccessPolicies.Get(ctx, strings.TrimPrefix(d.AccessPolicy.Reference, "AccessPolicy/"))
			if d.ProfileType != pt || err != nil || pol.Meta == nil || pol.Meta.Project != p.ID || pol.Name != "Default "+pt+" Access Policy" {
				t.Errorf("%s: %+v %v", pt, d, err)
			}
		}
		// client mặc định
		c := res.Client
		if c.Name != "Bệnh viện A Default Client" || !hex64.MatchString(c.Secret) || c.Meta.Project != p.ID {
			t.Errorf("client: %+v", c)
		}
		// membership của client: user = profile = client, policy ClientApplication mặc định
		ms, _ := e.r.Memberships.ListForUser(ctx, "ClientApplication/"+c.ID, "", 10)
		if len(ms) != 1 || ms[0].Profile.Reference != "ClientApplication/"+c.ID || ms[0].Project.Reference != "Project/"+p.ID ||
			ms[0].AccessPolicy == nil || ms[0].AccessPolicy.Reference != p.DefaultAccessPolicies[5].AccessPolicy.Reference {
			t.Errorf("membership client: %+v", ms)
		}
		// admin: Practitioner và membership admin
		if res.Profile == nil || res.Profile.Name[0].Family != "Nguyen" || res.Profile.Telecom[0].Value != "admin@example.org" || res.Profile.Meta.Project != p.ID {
			t.Errorf("profile: %+v", res.Profile)
		}
		m := res.Membership
		if !domain.IsTrue(m.Admin) || m.User.Reference != "User/"+admin.ID || m.Profile.Reference != "Practitioner/"+res.Profile.ID || m.Project.Reference != "Project/"+p.ID {
			t.Errorf("membership admin: %+v", m)
		}
		got, _ := e.r.Memberships.ListForUser(ctx, "User/"+admin.ID, "", 10)
		if len(got) != 1 {
			t.Errorf("admin có đúng một membership: %d", len(got))
		}
	})
}

func TestCreateProjectWithoutAdminAndValidation(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		res, err := e.svc.CreateProject(ctx, e.r.Resources, application.ProjectInit{Name: "  Dự án  "})
		if err != nil || res.Profile != nil || res.Membership != nil || res.Project.Owner != nil || res.Project.Name != "Dự án" || res.Client == nil {
			t.Fatalf("%+v %v", res, err)
		}
		for _, name := range []string{"", "   ", strings.Repeat("a", 73)} {
			_, err := e.svc.CreateProject(ctx, e.r.Resources, application.ProjectInit{Name: name})
			wantErr(t, err, domain.KindBadRequest, "")
		}
		if got := strings.Repeat("a", 72); true {
			if _, err := e.svc.CreateProject(ctx, e.r.Resources, application.ProjectInit{Name: got}); err != nil {
				t.Errorf("72 ký tự hợp lệ: %v", err)
			}
		}
	})
}

// failingWriter hỏng ở lần Create thứ n để kiểm tra transaction hoàn tác mọi thứ đã ghi.
type failingWriter struct {
	application.ResourceWriter
	n     int32
	calls atomic.Int32
}

func (f *failingWriter) Create(ctx context.Context, r fhir.Resource) (fhir.Resource, error) {
	if f.calls.Add(1) >= f.n {
		return nil, domain.Internal("hỏng giữa chừng")
	}
	return f.ResourceWriter.Create(ctx, r)
}

type failingTx struct {
	application.Transactor
	n int32
}

func (f failingTx) InTx(ctx context.Context, fn func(application.TxRepos) error) error {
	return f.Transactor.InTx(ctx, func(tx application.TxRepos) error {
		tx.Resources = &failingWriter{ResourceWriter: tx.Resources, n: f.n}
		return fn(tx)
	})
}

func TestSeedProjectRollsBackOnFailure(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		for _, failAt := range []int32{1, 4, 7, 8, 9, 10, 11} { // hỏng ở các bước khác nhau của chuỗi tạo
			svc := application.NewService(application.Deps{
				Users: e.r.Users, Logins: e.r.Logins, Memberships: e.r.Memberships, Projects: e.r.Projects, Clients: e.r.Clients,
				Launches: e.r.Launches, AccessPolicies: e.r.AccessPolicies, SecurityRequests: e.r.SecurityRequests, Resources: e.r.Resources,
				Tx: failingTx{e.r.Tx, failAt}, Hasher: e.hasher, Pwned: e.pwned, Clock: e.clock,
			})
			_, err := svc.SeedProject(ctx, application.SeedProjectRequest{Name: "P", AdminEmail: "seed@example.org", AdminFirst: "A", AdminLast: "B", AdminPassword: "mat-khau-tot-123"})
			wantErr(t, err, domain.KindInternal, "hỏng giữa chừng")
			for _, rt := range []string{"User", "Project", "AccessPolicy", "ClientApplication", "Practitioner", "ProjectMembership"} {
				if n := e.count(rt); n != 0 {
					t.Errorf("hỏng ở lần ghi %d: còn %d %s (phải hoàn tác hết)", failAt, n, rt)
				}
			}
		}
	})
}

func TestSeedProject(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		req := application.SeedProjectRequest{Name: "Dự án 1", AdminEmail: "Admin@Example.org", AdminFirst: "Hoc", AdminLast: "Nguyen", AdminPassword: "mat-khau-tot-123", Features: []string{"bots"}}
		res, err := e.svc.SeedProject(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		admin, _ := e.svc.FindUserByEmail(ctx, "admin@example.org", "")
		if admin == nil || admin.Email != "admin@example.org" || !e.hasher.Compare("mat-khau-tot-123", admin.PasswordHash) || admin.Project != nil {
			t.Fatalf("admin: %+v", admin)
		}
		if res.Project.Owner.Reference != "User/"+admin.ID || len(res.Project.Features) != 1 {
			t.Errorf("%+v", res.Project)
		}

		// chạy lần hai cùng admin: dùng lại user, KHÔNG đổi mật khẩu (kể cả khi truyền mật khẩu khác)
		req.Name, req.AdminPassword = "Dự án 2", "mat-khau-khac-xxx"
		res2, err := e.svc.SeedProject(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		if res2.Project.ID == res.Project.ID || e.count("User") != 1 || e.count("Project") != 2 {
			t.Errorf("hai project, một user: users=%d projects=%d", e.count("User"), e.count("Project"))
		}
		after, _ := e.r.Users.Get(ctx, admin.ID)
		if !e.hasher.Compare("mat-khau-tot-123", after.PasswordHash) {
			t.Error("mật khẩu admin đã có không được đổi")
		}
		if ms, _ := e.r.Memberships.ListForUser(ctx, "User/"+admin.ID, "", 10); len(ms) != 2 {
			t.Errorf("admin có 2 membership: %d", len(ms))
		}
	})
}

func TestSeedProjectValidation(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		base := application.SeedProjectRequest{Name: "P", AdminEmail: "a@example.org", AdminFirst: "A", AdminLast: "B", AdminPassword: "mat-khau-tot-123"}
		for name, mut := range map[string]func(*application.SeedProjectRequest){
			"không email":   func(r *application.SeedProjectRequest) { r.AdminEmail = "" },
			"mật khẩu ngắn": func(r *application.SeedProjectRequest) { r.AdminPassword = "ngan" },
			"mật khẩu rò rỉ": func(r *application.SeedProjectRequest) {
				r.AdminPassword = "password123"
				e.pwned.bad["password123"] = 5
			},
			"tên project rỗng": func(r *application.SeedProjectRequest) { r.Name = "" },
		} {
			r := base
			mut(&r)
			if _, err := e.svc.SeedProject(ctx, r); err == nil {
				t.Errorf("%s: phải lỗi", name)
			}
			for _, rt := range []string{"User", "Project"} {
				if n := e.count(rt); n != 0 {
					t.Errorf("%s: không được tạo gì, còn %d %s", name, n, rt)
				}
			}
		}
	})
}

func TestCreateClient(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		seed, err := e.svc.SeedProject(ctx, application.SeedProjectRequest{Name: "P1", AdminEmail: "a@example.org", AdminFirst: "A", AdminLast: "B", AdminPassword: "mat-khau-tot-123"})
		if err != nil {
			t.Fatal(err)
		}
		other, _ := e.svc.SeedProject(ctx, application.SeedProjectRequest{Name: "P2", AdminEmail: "a@example.org"})
		c, err := e.svc.CreateClient(ctx, seed.Project.ID, application.NewClient{
			Name: "SPA", Description: "ứng dụng web", RedirectURIs: []string{"https://app.example.org/cb"}, GrantTypes: []string{"refresh_token"}, PkceOptional: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		if !hex64.MatchString(c.Secret) || c.Meta.Project != seed.Project.ID || c.RedirectUris[0] != "https://app.example.org/cb" || !domain.IsTrue(c.PkceOptional) || c.GrantType[0] != "refresh_token" {
			t.Errorf("%+v", c)
		}
		c2, _ := e.svc.CreateClient(ctx, seed.Project.ID, application.NewClient{Name: "khác"})
		if c2.Secret == c.Secret || domain.IsTrue(c2.PkceOptional) {
			t.Error("secret ngẫu nhiên khác nhau; PKCE bắt buộc mặc định")
		}
		ms, _ := e.r.Memberships.ListForUser(ctx, "ClientApplication/"+c.ID, "", 10)
		if len(ms) != 1 || ms[0].AccessPolicy.Reference != seed.Project.DefaultAccessPolicies[5].AccessPolicy.Reference {
			t.Errorf("dùng policy ClientApplication mặc định: %+v", ms)
		}

		// chỉ định policy: phải thuộc đúng project
		own := seed.Project.DefaultAccessPolicies[3].AccessPolicy
		c3, err := e.svc.CreateClient(ctx, seed.Project.ID, application.NewClient{Name: "dùng policy riêng", AccessPolicy: own})
		if err != nil {
			t.Fatal(err)
		}
		if ms, _ := e.r.Memberships.ListForUser(ctx, "ClientApplication/"+c3.ID, "", 10); len(ms) != 1 || ms[0].AccessPolicy.Reference != own.Reference {
			t.Errorf("%+v", ms)
		}
		foreign := other.Project.DefaultAccessPolicies[3].AccessPolicy
		before := e.count("ClientApplication")
		_, err = e.svc.CreateClient(ctx, seed.Project.ID, application.NewClient{Name: "x", AccessPolicy: foreign})
		wantErr(t, err, domain.KindBadRequest, "Invalid access policy")
		_, err = e.svc.CreateClient(ctx, seed.Project.ID, application.NewClient{Name: "x", AccessPolicy: &fhir.Reference{Reference: "AccessPolicy/khong-co"}})
		wantErr(t, err, domain.KindBadRequest, "Invalid access policy")
		_, err = e.svc.CreateClient(ctx, "khong-co", application.NewClient{Name: "x"})
		wantErr(t, err, domain.KindNotFound, "")
		_, err = e.svc.CreateClient(ctx, seed.Project.ID, application.NewClient{Name: "  "})
		wantErr(t, err, domain.KindBadRequest, "Client name is required")
		if e.count("ClientApplication") != before {
			t.Error("lỗi thì không được tạo client")
		}
	})
}

func TestSetUserPassword(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		p := e.project(nil)
		global := e.user("a@example.org", nil)
		inP := e.user("a@example.org", func(u *fhir.User) { u.Project = &fhir.Reference{Reference: "Project/" + p.ID} })
		l := e.login(global, nil)

		if err := e.svc.SetUserPassword(ctx, "A@Example.org", "", "mat-khau-moi-tot"); err != nil {
			t.Fatal(err)
		}
		g, _ := e.r.Users.Get(ctx, global.ID)
		i, _ := e.r.Users.Get(ctx, inP.ID)
		if !e.hasher.Compare("mat-khau-moi-tot", g.PasswordHash) || i.PasswordHash != "" {
			t.Error("chỉ đổi user toàn cục")
		}
		if got, _ := e.r.Logins.Get(ctx, l.ID); !domain.IsTrue(got.Revoked) {
			t.Error("phải thu hồi login")
		}
		if err := e.svc.SetUserPassword(ctx, "a@example.org", p.ID, "mat-khau-trong-project"); err != nil {
			t.Fatal(err)
		}
		if i, _ = e.r.Users.Get(ctx, inP.ID); !e.hasher.Compare("mat-khau-trong-project", i.PasswordHash) {
			t.Error("đổi user của project")
		}
		wantErr(t, e.svc.SetUserPassword(ctx, "khong-co@example.org", "", "mat-khau-moi-tot"), domain.KindNotFound, "")
		wantErr(t, e.svc.SetUserPassword(ctx, "a@example.org", "", "ngan"), domain.KindBadRequest, "Password must be at least 8 characters")
		e.pwned.bad["password123"] = 1
		wantErr(t, e.svc.SetUserPassword(ctx, "a@example.org", "", "password123"), domain.KindBadRequest, domain.PwnedMessage)
	})
}

func TestPurgeLogins(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		u := e.user("a@example.org", nil)
		l1, l2 := e.login(u, nil), e.login(u, nil)
		e.clock.t = time.Now().Add(time.Hour) // "bây giờ" là 1 giờ sau
		before := time.Now().Add(30 * time.Minute)

		wantErr2 := func(_ int, err error) { wantErr(t, err, domain.KindBadRequest, "before phải ở quá khứ") }
		wantErr2(e.svc.PurgeLogins(ctx, e.clock.t.Add(time.Minute), true))
		wantErr2(e.svc.PurgeLogins(ctx, e.clock.t, false))

		n, err := e.svc.PurgeLogins(ctx, before, true)
		if err != nil || n != 2 {
			t.Fatalf("dry-run phải đếm 2: %d %v", n, err)
		}
		if e.count("Login") != 2 {
			t.Fatal("dry-run không được xóa")
		}
		if n, _ := e.svc.PurgeLogins(ctx, time.Now().Add(-time.Hour), true); n != 0 {
			t.Errorf("mốc trước khi tạo thì không khớp: %d", n)
		}

		n, err = e.svc.PurgeLogins(ctx, before, false)
		if err != nil || n != 2 {
			t.Fatalf("xóa: %d %v", n, err)
		}
		for _, id := range []string{l1.ID, l2.ID} {
			if _, err := e.r.Logins.Get(ctx, id); !domain.Is(err, domain.KindNotFound, "") {
				t.Errorf("login %s phải bị xóa vĩnh viễn: %v", id, err)
			}
		}
	})
}

// Giới hạn tên project tính theo ký tự: tên tiếng Việt 72 ký tự (216 byte) hợp lệ, 73 ký tự thì không.
func TestCreateProjectNameLimitCountsCharacters(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		if _, err := e.svc.CreateProject(ctx, e.r.Resources, application.ProjectInit{Name: strings.Repeat("ệ", 72)}); err != nil {
			t.Errorf("72 ký tự nhiều byte: %v", err)
		}
		if _, err := e.svc.CreateProject(ctx, e.r.Resources, application.ProjectInit{Name: strings.Repeat("ệ", 73)}); err == nil {
			t.Error("73 ký tự phải bị từ chối")
		}
	})
}
