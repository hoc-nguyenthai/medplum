package application_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
)

func TestHashPassword(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		h, err := e.svc.HashPassword(strings.Repeat("a", 72))
		if err != nil || !strings.HasPrefix(h, "$2") {
			t.Fatalf("72 byte: %q %v", h, err)
		}
		_, err = e.svc.HashPassword(strings.Repeat("a", 73))
		wantErr(t, err, domain.KindBadRequest, "Password must be no more than 72 characters")
	})
}

func TestVerifyPassword(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		hash, _ := e.hasher.Hash("correct-horse")
		u := &fhir.User{PasswordHash: hash}
		if !e.svc.VerifyPassword(u, "correct-horse") || e.svc.VerifyPassword(u, "wrong") {
			t.Error("so khớp thật")
		}
		if e.hasher.dummies.Load() != 0 || e.hasher.compares.Load() != 2 {
			t.Errorf("user có hash: không dùng hash giả (compares=%d dummies=%d)", e.hasher.compares.Load(), e.hasher.dummies.Load())
		}
		// không có user, hoặc user chưa có hash: so với hash giả (đúng một lần) và trả false
		if e.svc.VerifyPassword(nil, "x") || e.svc.VerifyPassword(&fhir.User{}, "x") {
			t.Error("không có hash thì không bao giờ đúng")
		}
		if e.hasher.dummies.Load() != 2 || e.hasher.compares.Load() != 2 {
			t.Errorf("phải so hash giả mỗi lần: compares=%d dummies=%d", e.hasher.compares.Load(), e.hasher.dummies.Load())
		}
	})
}

func TestCheckPwned(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		e.pwned.bad["password123"] = 9
		if err := e.svc.CheckPwned(ctx, "tot-nhat-tran-doi"); err != nil {
			t.Errorf("%v", err)
		}
		wantErr(t, e.svc.CheckPwned(ctx, "password123"), domain.KindBadRequest, "Password found in breach database")
		e.pwned.err = errors.New("mạng lỗi") // dịch vụ lỗi: fail đóng, KHÔNG cho qua
		err := e.svc.CheckPwned(ctx, "tot-nhat-tran-doi")
		wantErr(t, err, domain.KindInternal, "")
	})
}

func TestSetPasswordWithoutRequest(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		u := e.user("a@example.org", nil)
		l1, l2 := e.login(u, nil), e.login(u, nil)
		otherLogin := e.login(e.user("b@example.org", nil), nil)
		reset := e.secReq(u, "reset")
		verify := e.secReq(u, "verify-email")

		if err := e.svc.SetPassword(ctx, u, "mat-khau-moi-tot", nil); err != nil {
			t.Fatal(err)
		}
		got, _ := e.r.Users.Get(ctx, u.ID)
		if !e.hasher.Compare("mat-khau-moi-tot", got.PasswordHash) {
			t.Error("mật khẩu mới phải khớp")
		}
		if domain.IsTrue(got.EmailVerified) {
			t.Error("không có request thì không đánh dấu emailVerified")
		}
		for _, id := range []string{l1.ID, l2.ID} {
			if l, _ := e.r.Logins.Get(ctx, id); !domain.IsTrue(l.Revoked) {
				t.Errorf("login %s phải bị thu hồi", id)
			}
		}
		if l, _ := e.r.Logins.Get(ctx, otherLogin.ID); domain.IsTrue(l.Revoked) {
			t.Error("login của user khác không được động tới")
		}
		if !e.used(reset.ID) || e.used(verify.ID) {
			t.Error("request đặt lại cũ bị vô hiệu, verify-email giữ nguyên")
		}
	})
}

func TestSetPasswordWithRequestConsumesAndVerifiesEmail(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		u := e.user("a@example.org", nil)
		sr := e.secReq(u, "reset")
		if err := e.svc.SetPassword(ctx, u, "mat-khau-moi-tot", sr); err != nil {
			t.Fatal(err)
		}
		got, _ := e.r.Users.Get(ctx, u.ID)
		if !domain.IsTrue(got.EmailVerified) || !e.hasher.Compare("mat-khau-moi-tot", got.PasswordHash) {
			t.Errorf("%+v", got)
		}
		if !e.used(sr.ID) {
			t.Error("request phải bị tiêu thụ")
		}
		// dùng lại cùng request: Already used, mật khẩu không đổi
		err := e.svc.SetPassword(ctx, u, "mat-khau-khac-nua", sr)
		wantErr(t, err, domain.KindBadRequest, "Already used")
		got, _ = e.r.Users.Get(ctx, u.ID)
		if e.hasher.Compare("mat-khau-khac-nua", got.PasswordHash) {
			t.Error("lỗi giữa chừng không được đổi mật khẩu (transaction hoàn tác)")
		}
	})
}

func TestSetPasswordRejectsBeforeConsuming(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		u := e.user("a@example.org", nil)
		l := e.login(u, nil)
		sr := e.secReq(u, "reset")
		e.pwned.bad["password123"] = 1

		wantErr(t, e.svc.SetPassword(ctx, u, "password123", sr), domain.KindBadRequest, "Password found in breach database")
		wantErr(t, e.svc.SetPassword(ctx, u, strings.Repeat("a", 73), sr), domain.KindBadRequest, "Password must be no more than 72 characters")
		e.pwned.err = errors.New("mạng lỗi")
		wantErr(t, e.svc.SetPassword(ctx, u, "mat-khau-moi-tot", sr), domain.KindInternal, "")

		if e.used(sr.ID) {
			t.Error("mật khẩu bị từ chối thì request KHÔNG được tiêu thụ (người dùng thử lại được)")
		}
		if got, _ := e.r.Users.Get(ctx, u.ID); got.PasswordHash != "" {
			t.Error("mật khẩu không được đổi")
		}
		if got, _ := e.r.Logins.Get(ctx, l.ID); domain.IsTrue(got.Revoked) {
			t.Error("login không bị thu hồi khi đặt mật khẩu thất bại")
		}
	})
}

// Hai người cùng dùng một link đặt lại mật khẩu: đúng một người thành công.
func TestSetPasswordConcurrentSameRequest(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		u := e.user("a@example.org", nil)
		sr := e.secReq(u, "reset")
		const n = 6
		var ok, already atomic.Int32
		var wg sync.WaitGroup
		for i := range n {
			wg.Add(1)
			go func() {
				defer wg.Done()
				pw := "mat-khau-so-" + strings.Repeat(string(rune('a'+i)), 4)
				switch err := e.svc.SetPassword(ctx, u, pw, sr); {
				case err == nil:
					ok.Add(1)
				case domain.Is(err, domain.KindBadRequest, "Already used"):
					already.Add(1)
				default:
					t.Errorf("lỗi lạ: %v", err)
				}
			}()
		}
		wg.Wait()
		if ok.Load() != 1 || already.Load() != n-1 {
			t.Errorf("thắng=%d thua=%d", ok.Load(), already.Load())
		}
	})
}

func TestRevokeUserLogins(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		u, other := e.user("a@example.org", nil), e.user("b@example.org", nil)
		fresh := e.login(u, nil)
		already := e.login(u, func(l *fhir.Login) { l.Revoked = ptr(true) })
		o := e.login(other, nil)
		before, _ := e.r.Logins.Get(ctx, already.ID)

		if err := e.svc.RevokeUserLogins(ctx, u); err != nil {
			t.Fatal(err)
		}
		if l, _ := e.r.Logins.Get(ctx, fresh.ID); !domain.IsTrue(l.Revoked) {
			t.Error("login chưa thu hồi phải bị thu hồi")
		}
		if l, _ := e.r.Logins.Get(ctx, o.ID); domain.IsTrue(l.Revoked) {
			t.Error("login của user khác giữ nguyên")
		}
		if l, _ := e.r.Logins.Get(ctx, already.ID); l.Meta.VersionId != before.Meta.VersionId {
			t.Error("login đã thu hồi không được ghi lại")
		}
		// user không có login nào
		if err := e.svc.RevokeUserLogins(ctx, e.user("c@example.org", nil)); err != nil {
			t.Errorf("%v", err)
		}
	})
}
