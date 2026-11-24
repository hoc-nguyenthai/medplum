package bootstrap

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"trivita-fhir/apps/auth/internal/application"
	"trivita-fhir/apps/auth/internal/config"
	"trivita-fhir/apps/auth/internal/infrastructure/loginguard"
)

func wrongBody() string {
	return `{"email":"admin@example.org","password":"mat-khau-sai-xxx-1"}`
}

// Qua HTTP với Redis thật (miniredis): sai quá ngưỡng thì khóa, mật khẩu đúng cũng nhận ĐÚNG phản hồi của sai mật khẩu (cùng status, cùng nội dung),
// hết khóa thì đăng nhập được; user khác không bị ảnh hưởng.
func TestHTTPLockoutAfterRepeatedWrongPasswords(t *testing.T) {
	mr := miniredis.RunT(t)
	a := newAPI(t, func(s *config.Settings) {
		s.RedisURL = "redis://" + mr.Addr() + "/0"
		s.LockoutMaxFailures, s.LockoutWindow, s.LockoutDuration = 3, 10*time.Minute, 15*time.Minute
	}, nil)
	if _, ok := a.c.Auth.LoginGuard.(*loginguard.Redis); !ok {
		t.Fatalf("có AUTH_REDIS_URL phải dùng Redis: %T", a.c.Auth.LoginGuard)
	}
	wrong := a.login(wrongBody(), nil)
	if wrong.status != 400 || !strings.Contains(wrong.body, "Email or password is invalid") {
		t.Fatalf("sai mật khẩu: %d %s", wrong.status, wrong.body)
	}
	for i := 0; i < 2; i++ {
		a.login(wrongBody(), nil)
	}
	locked := a.login(loginBody(""), nil) // mật khẩu ĐÚNG nhưng đã sai 3 lần
	if locked.status != wrong.status || locked.body != wrong.body {
		t.Fatalf("khóa phải trả đúng phản hồi của sai mật khẩu:\n khóa: %d %s\n sai:  %d %s", locked.status, locked.body, wrong.status, wrong.body)
	}
	mr.FastForward(15*time.Minute + time.Second)
	if r := a.login(loginBody(""), nil); r.status != 200 {
		t.Fatalf("hết khóa phải đăng nhập được: %d %s", r.status, r.body)
	}
}

func TestLockoutWiring(t *testing.T) {
	c := newContainer(t, nil)
	if _, ok := c.Auth.LoginGuard.(*loginguard.Memory); !ok || c.Auth.Lockout != (application.LockoutConfig{MaxFailures: 10, Window: 15 * time.Minute, Duration: 15 * time.Minute}) {
		t.Fatalf("mặc định: bộ nhớ, 10 lần, 15 phút: %T %+v", c.Auth.LoginGuard, c.Auth.Lockout)
	}
	custom := newContainer(t, func(s *config.Settings) {
		s.LockoutMaxFailures, s.LockoutWindow, s.LockoutDuration = 4, 7*time.Minute, 11*time.Minute
	})
	if custom.Auth.Lockout != (application.LockoutConfig{MaxFailures: 4, Window: 7 * time.Minute, Duration: 11 * time.Minute}) {
		t.Fatalf("cấu hình phải đi đúng vào Service: %+v", custom.Auth.Lockout)
	}
	off := newContainer(t, func(s *config.Settings) { s.LockoutMaxFailures = 0 })
	if off.Auth.LoginGuard != nil {
		t.Fatalf("MaxFailures=0 phải tắt: %T", off.Auth.LoginGuard)
	}
}

// `auth user unlock`: mở khóa qua Redis dùng chung với service; không có Redis thì từ chối (trạng thái trong bộ nhớ của service khác tiến trình).
func TestUserUnlockCommand(t *testing.T) {
	mr := miniredis.RunT(t)
	s := devSettings(t)
	s.RedisURL = "redis://" + mr.Addr() + "/0"
	seed(t, s)
	c, err := New(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Stop(context.Background())
	u, err := c.Auth.FindUserByEmail(context.Background(), "a@example.org", "")
	if err != nil || u == nil {
		t.Fatalf("%v %v", u, err)
	}
	for i := 0; i <= s.LockoutMaxFailures; i++ { // vượt ngưỡng: khóa
		_, _ = c.Auth.LoginGuard.Reserve(context.Background(), u.ID, s.LockoutMaxFailures, s.LockoutWindow, s.LockoutDuration)
	}
	if r, _ := c.Auth.LoginGuard.Reserve(context.Background(), u.ID, s.LockoutMaxFailures, s.LockoutWindow, s.LockoutDuration); r.Allowed {
		t.Fatal("phải đang khóa")
	}
	out, err := run(t, s, env(nil), "", "user", "unlock", "-email", "A@example.org")
	if err != nil || !strings.Contains(out, "đã mở khóa") {
		t.Fatalf("%v\n%s", err, out)
	}
	if r, _ := c.Auth.LoginGuard.Reserve(context.Background(), u.ID, s.LockoutMaxFailures, s.LockoutWindow, s.LockoutDuration); !r.Allowed {
		t.Fatalf("sau unlock phải thử lại được: %+v", r)
	}
	for name, args := range map[string][]string{
		"thiếu -email":  {"user", "unlock"},
		"user không có": {"user", "unlock", "-email", "khong-co@example.org"},
	} {
		if _, err := run(t, s, env(nil), "", args...); err == nil {
			t.Errorf("%s: phải lỗi", name)
		}
	}
	noRedis := devSettings(t)
	seed(t, noRedis)
	if _, err := run(t, noRedis, env(nil), "", "user", "unlock", "-email", "a@example.org"); err == nil || !strings.Contains(err.Error(), "AUTH_REDIS_URL") {
		t.Fatalf("không có Redis phải từ chối: %v", err)
	}
}
