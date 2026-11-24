package application_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"trivita-fhir/apps/auth/internal/application"
	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/apps/auth/internal/infrastructure/loginguard"
)

const (
	lockMax    = 3
	lockWindow = 10 * time.Minute
	lockFor    = 15 * time.Minute
)

func (e *env) enableLockout() {
	e.svc.LoginGuard = loginguard.NewMemory(func() time.Time { return e.clock.Now() })
	e.svc.Lockout = application.LockoutConfig{MaxFailures: lockMax, Window: lockWindow, Duration: lockFor}
}

func (e *env) wrong(email string) error {
	_, err := e.svc.TryLogin(context.Background(), req(func(r *domain.LoginRequest) { r.Email = email; r.Password = "mat-khau-sai-xxx" }))
	return err
}

func (e *env) right(email string) error {
	_, err := e.svc.TryLogin(context.Background(), req(func(r *domain.LoginRequest) { r.Email = email }))
	return err
}

func TestLockoutBlocksEvenTheCorrectPasswordWithTheSameErrorAndWithoutRealCompare(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		e.fixture()
		e.enableLockout()
		for i := 0; i < lockMax; i++ {
			wantErr(t, e.wrong("an@example.org"), domain.KindBadRequest, "Email or password is invalid")
		}
		if e.hasher.compares.Load() != lockMax {
			t.Fatalf("compares=%d", e.hasher.compares.Load())
		}
		// bị khóa: mật khẩu ĐÚNG cũng bị từ chối, cùng thông điệp, không so mật khẩu thật (chỉ hash giả: thời gian như user không tồn tại)
		wantErr(t, e.right("an@example.org"), domain.KindBadRequest, "Email or password is invalid")
		if e.hasher.compares.Load() != lockMax || e.hasher.dummies.Load() != 1 {
			t.Fatalf("khi khóa không được so mật khẩu thật: compares=%d dummies=%d", e.hasher.compares.Load(), e.hasher.dummies.Load())
		}
		// các lượt thử trong lúc khóa không kéo dài khóa
		e.clock.t = e.clock.t.Add(lockFor - time.Minute)
		wantErr(t, e.right("an@example.org"), domain.KindBadRequest, "Email or password is invalid")
		e.clock.t = e.clock.t.Add(2 * time.Minute)
		if err := e.right("an@example.org"); err != nil {
			t.Fatalf("hết khóa phải đăng nhập được: %v", err)
		}
	})
}

func TestLockoutSuccessResetsTheCounterAndUsersAreIndependent(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		e.fixture()
		e.enableLockout()
		for round := 0; round < 3; round++ { // mỗi vòng sai lockMax-1 lần rồi đúng: không bao giờ khóa
			for i := 0; i < lockMax-1; i++ {
				_ = e.wrong("an@example.org")
			}
			if err := e.right("an@example.org"); err != nil {
				t.Fatalf("vòng %d: %v", round, err)
			}
		}
		// khóa một user không ảnh hưởng user khác
		e.pwUser("bo@example.org", nil)
		for i := 0; i <= lockMax; i++ {
			_ = e.wrong("bo@example.org")
		}
		wantErr(t, e.right("bo@example.org"), domain.KindBadRequest, "Email or password is invalid")
		if err := e.right("an@example.org"); err != nil {
			t.Fatalf("user khác không bị khóa: %v", err)
		}
	})
}

func TestLockoutDoesNotAffectOtherAuthMethodsOrUnknownUsers(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		e.fixture()
		e.enableLockout()
		for i := 0; i <= lockMax; i++ {
			_ = e.wrong("an@example.org")
		}
		// đăng nhập ngoài (đã xác minh ở nơi khác) không bị chặn bởi khóa mật khẩu
		if _, err := e.svc.TryLogin(context.Background(), domain.LoginRequest{AuthMethod: "external", Email: "an@example.org", Scope: "openid"}); err != nil {
			t.Fatalf("external: %v", err)
		}
		// email không tồn tại không tạo trạng thái khóa nào (không có gì để lộ), vẫn cùng thông điệp
		for i := 0; i < 2*lockMax; i++ {
			wantErr(t, e.wrong("khong-co@example.org"), domain.KindBadRequest, "Email or password is invalid")
		}
	})
}

func TestResetPasswordAndAdminUnlockClearTheLock(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.fixture()
		e.enableLockout()
		lock := func() {
			for i := 0; i <= lockMax; i++ {
				_ = e.wrong("an@example.org")
			}
			wantErr(t, e.right("an@example.org"), domain.KindBadRequest, "Email or password is invalid")
		}
		lock()
		if err := e.svc.SetUserPassword(context.Background(), "an@example.org", "", "mat-khau-moi-dai-456"); err != nil {
			t.Fatal(err)
		}
		if _, err := e.svc.TryLogin(context.Background(), req(func(r *domain.LoginRequest) { r.Password = "mat-khau-moi-dai-456" })); err != nil {
			t.Fatalf("đặt lại mật khẩu phải mở khóa: %v", err)
		}
		_ = f
		// mở khóa bằng lệnh quản trị
		lock()
		if err := e.svc.UnlockUserByEmail(context.Background(), "an@example.org", ""); err != nil {
			t.Fatal(err)
		}
		if _, err := e.svc.TryLogin(context.Background(), req(func(r *domain.LoginRequest) { r.Password = "mat-khau-moi-dai-456" })); err != nil {
			t.Fatalf("lệnh unlock phải mở khóa: %v", err)
		}
		wantErr(t, e.svc.UnlockUserByEmail(context.Background(), "khong-co@example.org", ""), domain.KindNotFound, "")
	})
}

func TestLockoutIsOffWithoutGuardOrWhenDisabled(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		e.fixture()
		for i := 0; i < 5*lockMax; i++ { // mặc định (không có LoginGuard): không bao giờ khóa
			_ = e.wrong("an@example.org")
		}
		if err := e.right("an@example.org"); err != nil {
			t.Fatalf("không có guard: %v", err)
		}
		e.enableLockout()
		e.svc.Lockout.MaxFailures = 0
		for i := 0; i < 5*lockMax; i++ {
			_ = e.wrong("an@example.org")
		}
		if err := e.right("an@example.org"); err != nil {
			t.Fatalf("MaxFailures=0 là tắt: %v", err)
		}
	})
}

type brokenGuard struct{}

func (brokenGuard) Reserve(context.Context, string, int, time.Duration, time.Duration) (application.LockResult, error) {
	return application.LockResult{}, errors.New("redis chết")
}
func (brokenGuard) Release(context.Context, string) error { return errors.New("redis chết") }
func (brokenGuard) Unlock(context.Context, string) error  { return errors.New("redis chết") }

// Kho trạng thái hỏng: đăng nhập vẫn chạy (mở khi lỗi) và mật khẩu vẫn được kiểm thật.
func TestLockoutFailsOpenWhenTheGuardIsBroken(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		e.fixture()
		e.svc.LoginGuard = brokenGuard{}
		e.svc.Lockout = application.LockoutConfig{MaxFailures: lockMax, Window: lockWindow, Duration: lockFor}
		wantErr(t, e.wrong("an@example.org"), domain.KindBadRequest, "Email or password is invalid")
		if err := e.right("an@example.org"); err != nil {
			t.Fatalf("guard hỏng không được chặn đăng nhập đúng: %v", err)
		}
		if err := e.svc.UnlockUserByEmail(context.Background(), "an@example.org", ""); err == nil {
			t.Fatal("lệnh unlock phải báo lỗi kho trạng thái (không nuốt)")
		}
	})
}

type spyGuard struct{ calls int }

func (g *spyGuard) Reserve(context.Context, string, int, time.Duration, time.Duration) (application.LockResult, error) {
	g.calls++
	return application.LockResult{Allowed: true}, nil
}
func (g *spyGuard) Release(context.Context, string) error { g.calls++; return nil }
func (g *spyGuard) Unlock(context.Context, string) error  { g.calls++; return nil }

// Tắt (MaxFailures 0, hoặc thiếu cửa sổ/thời gian khóa) thì đăng nhập không đụng tới kho trạng thái.
func TestLockoutDisabledNeverTouchesTheGuard(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		e.fixture()
		spy := &spyGuard{}
		e.svc.LoginGuard = spy
		for _, cfg := range []application.LockoutConfig{
			{MaxFailures: 0, Window: lockWindow, Duration: lockFor},
			{MaxFailures: lockMax, Window: 0, Duration: lockFor},
			{MaxFailures: lockMax, Window: lockWindow, Duration: 0},
		} {
			e.svc.Lockout = cfg
			_ = e.wrong("an@example.org")
			if err := e.right("an@example.org"); err != nil {
				t.Fatalf("%+v: %v", cfg, err)
			}
		}
		if spy.calls != 0 {
			t.Fatalf("tắt thì không được gọi kho trạng thái: %d lần", spy.calls)
		}
		e.svc.Lockout = application.LockoutConfig{MaxFailures: lockMax, Window: lockWindow, Duration: lockFor}
		_ = e.right("an@example.org")
		if spy.calls != 2 { // Reserve và Release
			t.Fatalf("bật: cần Reserve và Release, có %d lần gọi", spy.calls)
		}
	})
}
