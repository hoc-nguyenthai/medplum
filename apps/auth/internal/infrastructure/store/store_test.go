package store

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"trivita-fhir/apps/auth/internal/application"
	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/apps/auth/internal/infrastructure/store/storetest"
	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirrouter"
)

func ptr[T any](v T) *T { return &v }

func each(t *testing.T, fn func(t *testing.T, s *Store, r Repos)) {
	for _, b := range storetest.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			s := New(b.Repo(t))
			fn(t, s, s.Repos())
		})
	}
}

func mkUser(t *testing.T, r Repos, s *Store, email, project string) *fhir.User {
	t.Helper()
	u := &fhir.User{ResourceType: "User", Email: email, FirstName: "A", LastName: "B"}
	if project != "" {
		u.Project = &fhir.Reference{Reference: "Project/" + project}
	}
	out, err := create(context.Background(), s, u)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestUserLookup(t *testing.T) {
	each(t, func(t *testing.T, s *Store, r Repos) {
		ctx := context.Background()
		global := mkUser(t, r, s, "an@example.org", "")
		inP1 := mkUser(t, r, s, "an@example.org", "p1")
		mkUser(t, r, s, "other@example.org", "p1")

		got, err := r.Users.FindGlobal(ctx, "an@example.org")
		if err != nil || got == nil || got.ID != global.ID {
			t.Fatalf("FindGlobal: %v %v", got, err)
		}
		got, err = r.Users.FindInProject(ctx, "an@example.org", "p1")
		if err != nil || got == nil || got.ID != inP1.ID {
			t.Fatalf("FindInProject: %v %v", got, err)
		}
		if got, _ := r.Users.FindInProject(ctx, "an@example.org", "p2"); got != nil {
			t.Error("project khác không có user")
		}
		if got, _ := r.Users.FindGlobal(ctx, "other@example.org"); got != nil {
			t.Error("user thuộc project không được trả như user toàn cục")
		}
		if got, err := r.Users.FindGlobal(ctx, "nobody@example.org"); got != nil || err != nil {
			t.Errorf("không có thì nil, nil: %v %v", got, err)
		}
		byID, err := r.Users.Get(ctx, global.ID)
		if err != nil || byID.Email != "an@example.org" {
			t.Errorf("Get: %v %v", byID, err)
		}
		if _, err := r.Users.Get(ctx, "khong-co"); !domain.Is(err, domain.KindNotFound, "Not found") {
			t.Errorf("Get không có: %v", err)
		}
	})
}

// Email chứa `,` `|` `\` `$` không được hiểu là cú pháp search (OR, escape...).
func TestEmailWithSearchSyntaxCharacters(t *testing.T) {
	each(t, func(t *testing.T, s *Store, r Repos) {
		ctx := context.Background()
		tricky := []string{`a,b@example.org`, `a|b@example.org`, `a\b@example.org`, `a$b@example.org`, `x@example.org,y@example.org`}
		ids := map[string]string{}
		for _, e := range tricky {
			ids[e] = mkUser(t, r, s, e, "").ID
		}
		for _, e := range tricky {
			got, err := r.Users.FindGlobal(ctx, e)
			if err != nil || got == nil || got.ID != ids[e] {
				t.Errorf("%q: %v %v", e, got, err)
			}
		}
		// `a,b@...` KHÔNG được khớp "a" HOẶC "b@...": tìm email có dấu phẩy không trả user khác.
		if got, _ := r.Users.FindGlobal(ctx, "y@example.org"); got != nil {
			t.Error(`"x@..,y@.." không được tách thành hai giá trị OR`)
		}
	})
}

func TestLoginCRUDAndConflict(t *testing.T) {
	each(t, func(t *testing.T, s *Store, r Repos) {
		ctx := context.Background()
		l, err := r.Logins.Create(ctx, &fhir.Login{User: &fhir.Reference{Reference: "User/u1"}, AuthMethod: "password", AuthTime: "2026-01-01T00:00:00Z"})
		if err != nil || l.ID == "" || l.Meta == nil || l.Meta.VersionId == "" || l.ResourceType != "Login" {
			t.Fatalf("Create: %+v %v", l, err)
		}
		got, err := r.Logins.Get(ctx, l.ID)
		if err != nil || got.AuthMethod != "password" {
			t.Fatalf("Get: %v %v", got, err)
		}
		stale := *l
		first := *l
		first.Scope = "openid"
		upd, err := r.Logins.UpdateIfMatch(ctx, &first, l.Meta.VersionId)
		if err != nil || upd.Scope != "openid" || upd.Meta.VersionId == l.Meta.VersionId {
			t.Fatalf("UpdateIfMatch: %+v %v", upd, err)
		}
		stale.Scope = "profile"
		if _, err := r.Logins.UpdateIfMatch(ctx, &stale, l.Meta.VersionId); !errors.Is(err, domain.ErrVersionConflict) {
			t.Errorf("phiên bản cũ phải xung đột: %v", err)
		}
		if got, _ := r.Logins.Get(ctx, l.ID); got.Scope != "openid" {
			t.Errorf("bản ghi không được bị ghi đè: %q", got.Scope)
		}
	})
}

func TestMutate(t *testing.T) {
	each(t, func(t *testing.T, s *Store, r Repos) {
		ctx := context.Background()
		l, _ := r.Logins.Create(ctx, &fhir.Login{User: &fhir.Reference{Reference: "User/u1"}, AuthMethod: "password", AuthTime: "x"})

		// ErrNoChange: không ghi, phiên bản giữ nguyên
		same, err := r.Logins.Mutate(ctx, l.ID, func(*fhir.Login) error { return application.ErrNoChange })
		if err != nil || same.Meta.VersionId != l.Meta.VersionId {
			t.Errorf("ErrNoChange: %+v %v", same, err)
		}
		// lỗi từ fn đi qua nguyên vẹn, không ghi
		boom := domain.BadRequest("boom")
		if _, err := r.Logins.Mutate(ctx, l.ID, func(*fhir.Login) error { return boom }); !errors.Is(err, boom) {
			t.Errorf("lỗi của fn: %v", err)
		}
		if _, err := r.Logins.Mutate(ctx, "khong-co", func(*fhir.Login) error { return nil }); !domain.Is(err, domain.KindNotFound, "") {
			t.Errorf("không có: %v", err)
		}
		// Mutate không ghi đè thay đổi xen vào: revoke xen giữa đọc và ghi vẫn được giữ.
		var once sync.Once
		out, err := r.Logins.Mutate(ctx, l.ID, func(x *fhir.Login) error {
			once.Do(func() { // một writer khác ghi `revoked` sau khi ta đã đọc
				_, _ = r.Logins.Mutate(ctx, l.ID, func(y *fhir.Login) error { y.Revoked = ptr(true); return nil })
			})
			x.Scope = "openid"
			return nil
		})
		if err != nil || out.Scope != "openid" || !domain.IsTrue(out.Revoked) {
			t.Errorf("phải thử lại trên bản mới và giữ revoked: %+v %v", out, err)
		}
	})
}

// Nhiều goroutine cùng sửa một Login: mọi thay đổi đều được giữ (không mất cập nhật).
func TestMutateConcurrent(t *testing.T) {
	each(t, func(t *testing.T, s *Store, r Repos) {
		ctx := context.Background()
		l, _ := r.Logins.Create(ctx, &fhir.Login{User: &fhir.Reference{Reference: "User/u1"}, AuthMethod: "password", AuthTime: "x"})
		const n = 6
		var wg sync.WaitGroup
		errs := make(chan error, n)
		for i := range n {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := r.Logins.Mutate(ctx, l.ID, func(x *fhir.Login) error {
					x.Scope += fmt.Sprintf("t%d ", i)
					return nil
				})
				errs <- err
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Errorf("Mutate đồng thời: %v", err)
			}
		}
		got, _ := r.Logins.Get(ctx, l.ID)
		have := strings.Fields(got.Scope)
		sort.Strings(have)
		if len(have) != n {
			t.Errorf("mất cập nhật: %q", got.Scope)
		}
	})
}

func TestMutateGivesUpAfterTooManyConflicts(t *testing.T) {
	each(t, func(t *testing.T, s *Store, r Repos) {
		ctx := context.Background()
		l, _ := r.Logins.Create(ctx, &fhir.Login{User: &fhir.Reference{Reference: "User/u1"}, AuthMethod: "password", AuthTime: "x"})
		// fn luôn tự gây xung đột bằng cách ghi một phiên bản mới trước khi mutate ghi.
		_, err := r.Logins.Mutate(ctx, l.ID, func(x *fhir.Login) error {
			_, e := r.Logins.Mutate(ctx, l.ID, func(y *fhir.Login) error { y.Nonce += "."; return nil })
			if e != nil && !domain.Is(e, domain.KindConflict, "") {
				t.Fatal(e)
			}
			x.Scope = "x"
			return nil
		})
		if !domain.Is(err, domain.KindConflict, "") {
			t.Errorf("phải bỏ cuộc với Conflict: %v", err)
		}
	})
}

func TestListLoginIDsByUserPaginates(t *testing.T) {
	old := pageSize
	pageSize = 4
	defer func() { pageSize = old }()
	each(t, func(t *testing.T, s *Store, r Repos) {
		ctx := context.Background()
		want := map[string]bool{}
		for i := range 11 {
			l, err := r.Logins.Create(ctx, &fhir.Login{User: &fhir.Reference{Reference: "User/u1"}, AuthMethod: "password", AuthTime: fmt.Sprint(i)})
			if err != nil {
				t.Fatal(err)
			}
			want[l.ID] = true
		}
		if _, err := r.Logins.Create(ctx, &fhir.Login{User: &fhir.Reference{Reference: "User/other"}, AuthMethod: "password", AuthTime: "x"}); err != nil {
			t.Fatal(err)
		}
		ids, err := r.Logins.ListIDsByUser(ctx, "User/u1")
		if err != nil || len(ids) != 11 {
			t.Fatalf("phải gom đủ 11 qua nhiều trang: %d %v", len(ids), err)
		}
		for _, id := range ids {
			if !want[id] {
				t.Errorf("id lạ %s", id)
			}
			delete(want, id)
		}
		if len(want) != 0 {
			t.Errorf("thiếu: %v", want)
		}
	})
}

func TestMembershipListing(t *testing.T) {
	each(t, func(t *testing.T, s *Store, r Repos) {
		ctx := context.Background()
		mk := func(user, project string, active *bool) {
			m := &fhir.ProjectMembership{ResourceType: "ProjectMembership", User: &fhir.Reference{Reference: user},
				Project: &fhir.Reference{Reference: project}, Profile: &fhir.Reference{Reference: "Practitioner/x"}, Active: active}
			if _, err := create(ctx, s, m); err != nil {
				t.Fatal(err)
			}
		}
		mk("User/u1", "Project/p1", nil)
		mk("User/u1", "Project/p2", ptr(true))
		mk("User/u1", "Project/p3", ptr(false))
		mk("User/u2", "Project/p1", nil)
		all, err := r.Memberships.ListForUser(ctx, "User/u1", "", 100)
		if err != nil || len(all) != 2 {
			t.Fatalf("active khác false (gồm thiếu active): %d %v", len(all), err)
		}
		one, _ := r.Memberships.ListForUser(ctx, "User/u1", "Project/p2", 100)
		if len(one) != 1 || one[0].Project.Reference != "Project/p2" {
			t.Errorf("lọc theo project: %+v", one)
		}
		lim, _ := r.Memberships.ListForUser(ctx, "User/u1", "", 1)
		if len(lim) != 1 {
			t.Errorf("giới hạn: %d", len(lim))
		}
		none, _ := r.Memberships.ListForUser(ctx, "User/zzz", "", 100)
		if len(none) != 0 {
			t.Errorf("%d", len(none))
		}
	})
}

func TestReadByID(t *testing.T) {
	each(t, func(t *testing.T, s *Store, r Repos) {
		ctx := context.Background()
		p, _ := create(ctx, s, &fhir.Project{ResourceType: "Project", Name: "P"})
		c, _ := create(ctx, s, &fhir.ClientApplication{ResourceType: "ClientApplication", Name: "C"})
		l, _ := create(ctx, s, &fhir.SmartAppLaunch{ResourceType: "SmartAppLaunch", Patient: &fhir.Reference{Reference: "Patient/1"}})
		a, _ := create(ctx, s, &fhir.AccessPolicy{ResourceType: "AccessPolicy", Name: "A"})
		if got, err := r.Projects.Get(ctx, p.ID); err != nil || got.Name != "P" {
			t.Errorf("project: %v %v", got, err)
		}
		if got, err := r.Clients.Get(ctx, c.ID); err != nil || got.Name != "C" {
			t.Errorf("client: %v %v", got, err)
		}
		if got, err := r.Launches.Get(ctx, l.ID); err != nil || got.Patient.Reference != "Patient/1" {
			t.Errorf("launch: %v %v", got, err)
		}
		if got, err := r.AccessPolicies.Get(ctx, a.ID); err != nil || got.Name != "A" {
			t.Errorf("policy: %v %v", got, err)
		}
		// id của loại khác không đọc được như loại này
		for name, err := range map[string]error{
			"project": func() error { _, e := r.Projects.Get(ctx, "x"); return e }(),
			"client":  func() error { _, e := r.Clients.Get(ctx, "x"); return e }(),
			"launch":  func() error { _, e := r.Launches.Get(ctx, "x"); return e }(),
			"policy":  func() error { _, e := r.AccessPolicies.Get(ctx, "x"); return e }(),
		} {
			if !domain.Is(err, domain.KindNotFound, "Not found") {
				t.Errorf("%s: %v", name, err)
			}
		}
		if _, err := r.Projects.Get(ctx, c.ID); !domain.Is(err, domain.KindNotFound, "") {
			t.Errorf("id của ClientApplication không phải Project: %v", err)
		}
	})
}

func TestSecurityRequests(t *testing.T) {
	each(t, func(t *testing.T, s *Store, r Repos) {
		ctx := context.Background()
		mk := func(user, typ string) *fhir.UserSecurityRequest {
			sr, err := create(ctx, s, &fhir.UserSecurityRequest{ResourceType: "UserSecurityRequest", Type: typ, User: &fhir.Reference{Reference: user}, Secret: "s"})
			if err != nil {
				t.Fatal(err)
			}
			return sr
		}
		a, b := mk("User/u1", "reset"), mk("User/u1", "verify-email")
		mk("User/u2", "reset")

		list, err := r.SecurityRequests.ListSince(ctx, "User/u1", time.Now().Add(-time.Hour))
		if err != nil || len(list) != 2 {
			t.Fatalf("ListSince: %d %v", len(list), err)
		}
		if list, _ := r.SecurityRequests.ListSince(ctx, "User/u1", time.Now().Add(time.Hour)); len(list) != 0 {
			t.Errorf("mốc ở tương lai thì rỗng: %d", len(list))
		}
		if got, err := r.SecurityRequests.Get(ctx, a.ID); err != nil || got.Type != "reset" {
			t.Errorf("Get: %v %v", got, err)
		}
		// MarkUsedIfMatch: đúng phiên bản thì được, dùng lại phiên bản cũ thì xung đột
		if err := r.SecurityRequests.MarkUsedIfMatch(ctx, a, a.Meta.VersionId); err != nil {
			t.Fatal(err)
		}
		if err := r.SecurityRequests.MarkUsedIfMatch(ctx, a, a.Meta.VersionId); !errors.Is(err, domain.ErrVersionConflict) {
			t.Errorf("tiêu thụ lần hai phải xung đột: %v", err)
		}
		if err := r.SecurityRequests.MarkUsedIfMatch(ctx, b, ""); !domain.Is(err, domain.KindInternal, "") {
			t.Errorf("thiếu versionId phải từ chối: %v", err)
		}
		// MarkUsed không ghi đè trường khác
		if err := r.SecurityRequests.MarkUsed(ctx, b.ID); err != nil {
			t.Fatal(err)
		}
		got, _ := r.SecurityRequests.Get(ctx, b.ID)
		if !domain.IsTrue(got.Used) || got.Secret != "s" || got.Type != "verify-email" {
			t.Errorf("%+v", got)
		}
		if err := r.SecurityRequests.MarkUsed(ctx, "khong-co"); !domain.Is(err, domain.KindNotFound, "") {
			t.Errorf("%v", err)
		}
	})
}

func TestInTxRollsBackOnError(t *testing.T) {
	each(t, func(t *testing.T, s *Store, r Repos) {
		ctx := context.Background()
		u := mkUser(t, r, s, "tx@example.org", "")
		boom := errors.New("boom")
		err := s.InTx(ctx, func(tx application.TxRepos) error {
			if _, e := tx.Users.Mutate(ctx, u.ID, func(x *fhir.User) error { x.PasswordHash = "changed"; return nil }); e != nil {
				return e
			}
			return boom
		})
		if !errors.Is(err, boom) {
			t.Fatalf("lỗi của fn phải đi qua: %v", err)
		}
		if got, _ := r.Users.Get(ctx, u.ID); got.PasswordHash != "" {
			t.Error("lỗi giữa chừng phải hủy mọi thay đổi")
		}
		err = s.InTx(ctx, func(tx application.TxRepos) error {
			_, e := tx.Users.Mutate(ctx, u.ID, func(x *fhir.User) error { x.PasswordHash = "ok"; return nil })
			return e
		})
		if err != nil {
			t.Fatal(err)
		}
		if got, _ := r.Users.Get(ctx, u.ID); got.PasswordHash != "ok" {
			t.Error("thành công thì giữ thay đổi")
		}
	})
}

func TestMapErr(t *testing.T) {
	if mapErr(nil) != nil {
		t.Error("nil")
	}
	for _, c := range []struct {
		err  error
		want func(error) bool
	}{
		{fhirrouter.NotFound(), func(e error) bool { return domain.Is(e, domain.KindNotFound, "Not found") }},
		{fhirrouter.Gone(), func(e error) bool { return domain.Is(e, domain.KindNotFound, "") }},
		{fhirrouter.PreconditionFailed("x"), func(e error) bool { return errors.Is(e, domain.ErrVersionConflict) }},
		{fhirrouter.BadRequest("lạ"), func(e error) bool {
			return domain.KindOf(e) == domain.KindInternal && !strings.Contains(e.Error(), "---")
		}},
		{errors.New("mạng"), func(e error) bool { return domain.Is(e, domain.KindInternal, "") }},
		{domain.BadRequest("giữ"), func(e error) bool { return domain.Is(e, domain.KindBadRequest, "giữ") }},
		{application.ErrNoChange, func(e error) bool { return errors.Is(e, application.ErrNoChange) }},
	} {
		if got := mapErr(c.err); !c.want(got) {
			t.Errorf("mapErr(%v)=%v", c.err, got)
		}
	}
}

func TestFilterPanicsOnUnknownParam(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("tham số không tồn tại phải panic (lỗi lập trình)")
		}
	}()
	filter("User", "khong-ton-tai", "")
}

// Mọi search parameter mà kho dùng phải tồn tại (bắt lỗi chính tả khi sinh lại pkg/fhir).
func TestSearchParametersExist(t *testing.T) {
	for rt, codes := range map[string][]string{
		"User": {"email", "project"}, "Login": {"user"}, "ProjectMembership": {"user", "active", "project"},
		"UserSecurityRequest": {"user", "_lastUpdated"},
	} {
		for _, c := range codes {
			filter(rt, c, "")
		}
	}
}

// Repository nền không hỗ trợ xóa vĩnh viễn thì Expunge báo lỗi rõ ràng, không âm thầm bỏ qua.
func TestExpungeUnsupportedBackend(t *testing.T) {
	var onlyInterface struct{ fhirrouter.Repository } // che mất phương thức Expunge của MemoryRepository
	onlyInterface.Repository = fhirrouter.NewMemoryRepository()
	err := New(onlyInterface).Repos().Logins.Expunge(context.Background(), "x")
	if !domain.Is(err, domain.KindInternal, "") {
		t.Errorf("%v", err)
	}
}

func TestResourcesCreateAndListLoginsBefore(t *testing.T) {
	each(t, func(t *testing.T, s *Store, r Repos) {
		ctx := context.Background()
		out, err := r.Resources.Create(ctx, &fhir.Project{ResourceType: "Project", ID: "dat-truoc", Name: "P"})
		if err != nil || out.GetID() != "dat-truoc" {
			t.Fatalf("id đặt trước phải được giữ: %v %v", out, err)
		}
		if _, err := r.Resources.Create(ctx, &fhir.Project{ResourceType: "Project", ID: "dat-truoc", Name: "P2"}); err == nil {
			t.Error("trùng id phải lỗi")
		}
		l, _ := r.Logins.Create(ctx, &fhir.Login{User: &fhir.Reference{Reference: "User/u"}, AuthMethod: "password", AuthTime: "x"})
		ids, err := r.Logins.ListIDsUpdatedBefore(ctx, time.Now().Add(time.Hour))
		if err != nil || len(ids) != 1 || ids[0] != l.ID {
			t.Errorf("%v %v", ids, err)
		}
		if ids, _ := r.Logins.ListIDsUpdatedBefore(ctx, time.Now().Add(-time.Hour)); len(ids) != 0 {
			t.Errorf("%v", ids)
		}
	})
}

func TestMembershipGetDomainConfigUserConfigAndLoginsSince(t *testing.T) {
	each(t, func(t *testing.T, s *Store, r Repos) {
		ctx := context.Background()
		m, _ := create(ctx, s, &fhir.ProjectMembership{ResourceType: "ProjectMembership", User: &fhir.Reference{Reference: "User/u1"}, Project: &fhir.Reference{Reference: "Project/p1"}})
		if got, err := r.Memberships.Get(ctx, m.ID); err != nil || got.User.Reference != "User/u1" {
			t.Errorf("%v %v", got, err)
		}
		if _, err := r.Memberships.Get(ctx, "khong-co"); !domain.Is(err, domain.KindNotFound, "") {
			t.Errorf("%v", err)
		}

		if _, err := create(ctx, s, &fhir.DomainConfiguration{ResourceType: "DomainConfiguration", Domain: "corp.example.org",
			IdentityProvider: &fhir.IdentityProvider{ClientId: "cid"}}); err != nil {
			t.Fatal(err)
		}
		if got, err := r.DomainConfigs.FindByDomain(ctx, "corp.example.org"); err != nil || got == nil || got.IdentityProvider.ClientId != "cid" {
			t.Errorf("%v %v", got, err)
		}
		if got, err := r.DomainConfigs.FindByDomain(ctx, "khac.example.org"); err != nil || got != nil {
			t.Errorf("không có thì nil: %v %v", got, err)
		}

		uc, _ := create(ctx, s, &fhir.UserConfiguration{ResourceType: "UserConfiguration", Name: "cfg"})
		if got, err := r.UserConfigs.Get(ctx, uc.ID); err != nil || got.Name != "cfg" {
			t.Errorf("%v %v", got, err)
		}
		if _, err := r.UserConfigs.Get(ctx, "khong-co"); !domain.Is(err, domain.KindNotFound, "") {
			t.Errorf("%v", err)
		}

		l1, _ := r.Logins.Create(ctx, &fhir.Login{User: &fhir.Reference{Reference: "User/u1"}, AuthMethod: "password", AuthTime: "x"})
		_, _ = r.Logins.Create(ctx, &fhir.Login{User: &fhir.Reference{Reference: "User/khac"}, AuthMethod: "password", AuthTime: "x"})
		got, err := r.Logins.ListByUserSince(ctx, "User/u1", time.Now().Add(-time.Hour))
		if err != nil || len(got) != 1 || got[0].ID != l1.ID {
			t.Errorf("%v %v", got, err)
		}
		if got, _ := r.Logins.ListByUserSince(ctx, "User/u1", time.Now().Add(time.Hour)); len(got) != 0 {
			t.Errorf("%v", got)
		}
	})
}

// FindByExternalID: khớp CHÍNH XÁC externalId (không LIKE, không tiền tố, không phân biệt hoa thường) và đúng project.
func TestMembershipFindByExternalID(t *testing.T) {
	each(t, func(t *testing.T, s *Store, r Repos) {
		ctx := context.Background()
		mk := func(project, ext string) *fhir.ProjectMembership {
			m, err := create(ctx, s, &fhir.ProjectMembership{ResourceType: "ProjectMembership", ExternalId: ext,
				Project: &fhir.Reference{Reference: "Project/" + project}, User: &fhir.Reference{Reference: "User/u-" + project + ext}, Profile: &fhir.Reference{Reference: "Practitioner/p"}})
			if err != nil {
				t.Fatal(err)
			}
			return m
		}
		a, b := mk("p1", "ext-123"), mk("p2", "ext-123")
		mk("p1", "ext-1234")
		mk("p1", "EXT-9")
		got, err := r.Memberships.FindByExternalID(ctx, "ext-123", "Project/p1")
		if err != nil || got == nil || got.ID != a.ID {
			t.Fatalf("p1: %+v %v", got, err)
		}
		if got, _ := r.Memberships.FindByExternalID(ctx, "ext-123", "Project/p2"); got == nil || got.ID != b.ID {
			t.Errorf("p2: %+v", got)
		}
		for name, tc := range map[string][2]string{
			"tiền tố":          {"ext-12", "Project/p1"},
			"hậu tố":           {"xt-123", "Project/p1"},
			"khác hoa thường":  {"EXT-123", "Project/p1"},
			"có khoảng trắng":  {"ext-123 ", "Project/p1"},
			"project không có": {"ext-123", "Project/p3"},
			"externalId rỗng":  {"", "Project/p1"},
			"project rỗng":     {"ext-123", ""},
			"ký tự đặc biệt":   {"ext-123,ext-1234", "Project/p1"},
			"dấu phần trăm":    {"%", "Project/p1"},
		} {
			if got, err := r.Memberships.FindByExternalID(ctx, tc[0], tc[1]); err != nil || got != nil {
				t.Errorf("%s: %+v %v", name, got, err)
			}
		}
		if got, _ := r.Memberships.FindByExternalID(ctx, "EXT-9", "Project/p1"); got == nil {
			t.Error("khớp chính xác kể cả khi viết hoa")
		}
	})
}
