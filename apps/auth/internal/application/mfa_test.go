package application_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pquerna/otp"
	pq "github.com/pquerna/otp/totp"

	"trivita-fhir/apps/auth/internal/application"
	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/apps/auth/internal/infrastructure/store/storetest"
	"trivita-fhir/pkg/fhir"
)

// ---- helper

// code sinh mã TOTP đúng cho secret tại now+offset (độc lập với adapter đang test, dùng thư viện gốc).
func (e *env) code(secret string, offset time.Duration) string {
	c, err := pq.GenerateCodeCustom(secret, e.clock.Now().Add(offset), pq.ValidateOpts{Period: 30, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1})
	if err != nil {
		e.t.Fatal(err)
	}
	return c
}

// nextStepCode trả mã của bước kế tiếp VÀ tiến đồng hồ tới đó, để lấy mã chưa từng dùng.
func (e *env) nextStepCode(secret string) string {
	e.clock.t = e.clock.t.Add(30 * time.Second)
	return e.code(secret, 0)
}

func secretOf(t *testing.T, uri string) string {
	t.Helper()
	u, err := url.Parse(uri)
	if err != nil || u.Query().Get("secret") == "" {
		t.Fatalf("enrollUri không có secret: %q %v", uri, err)
	}
	return u.Query().Get("secret")
}

// freshLogin là login mật khẩu vừa đăng nhập (authTime = bây giờ), chưa xác minh MFA.
func (e *env) freshLogin(u *fhir.User, mut func(*fhir.Login)) *fhir.Login {
	return e.login(u, func(l *fhir.Login) {
		l.AuthTime = e.clock.Now().UTC().Format("2006-01-02T15:04:05.000Z")
		if mut != nil {
			mut(l)
		}
	})
}

// totpUser là user đã đăng ký TOTP, trả kèm secret thô (trong kho là bản đã mã hóa).
func (e *env) totpUser(email string) (*fhir.User, string) {
	e.t.Helper()
	secret, err := e.svc.TOTP.GenerateSecret()
	if err != nil {
		e.t.Fatal(err)
	}
	sealed, err := e.svc.MFASecrets.Seal(secret)
	if err != nil {
		e.t.Fatal(err)
	}
	return e.user(email, func(u *fhir.User) { u.MfaEnrolled, u.MfaMethod, u.MfaSecret = ptr(true), []string{"totp"}, sealed }), secret
}

func (e *env) emailUser(email string) *fhir.User {
	return e.user(email, func(u *fhir.User) { u.MfaEnrolled, u.MfaMethod = ptr(true), []string{"email"} })
}

func (e *env) bearer(u *fhir.User, l *fhir.Login, p *fhir.Project) *application.AuthState {
	return &application.AuthState{Login: l, Project: p, Membership: &fhir.ProjectMembership{User: &fhir.Reference{Reference: "User/" + u.ID}}}
}

func (e *env) reload(u *fhir.User) *fhir.User { return e.getUser(u.ID) }

func (e *env) getLogin(id string) *fhir.Login {
	e.t.Helper()
	l, err := e.r.Logins.Get(context.Background(), id)
	if err != nil {
		e.t.Fatal(err)
	}
	return l
}

var mfaMailCode = regexp.MustCompile(`verification code: (\d{6})$`)

func (e *env) lastMFACode() string {
	e.t.Helper()
	for i := len(e.mail.Sent()) - 1; i >= 0; i-- {
		if m := mfaMailCode.FindStringSubmatch(e.mail.Sent()[i].Subject); m != nil {
			return m[1]
		}
	}
	e.t.Fatal("chưa có thư mã MFA")
	return ""
}

func (e *env) project2(settings ...fhir.ProjectSetting) *fhir.Project {
	return e.project(func(p *fhir.Project) { p.Setting = settings })
}

func setting(name string, v bool) fhir.ProjectSetting {
	return fhir.ProjectSetting{Name: name, ValueBoolean: ptr(v)}
}

func strSetting(name, v string) fhir.ProjectSetting {
	return fhir.ProjectSetting{Name: name, ValueString: v}
}

// ---- URI và mã QR

func TestMFAEnrollmentURIIsStablePerUserAndLoginAndBranded(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		u := e.user("an@example.org", func(u *fhir.User) { u.MfaRequired = ptr(true) })
		l1, l2 := e.freshLogin(u, nil), e.freshLogin(u, nil)
		r1, err := e.svc.BuildLoginResult(ctx, l1)
		if err != nil || !r1.MfaEnrollRequired || r1.Code != "" || !strings.HasPrefix(r1.EnrollQRCode, "data:image/png;base64,") {
			t.Fatalf("%+v %v", r1, err)
		}
		again, _ := e.svc.BuildLoginResult(ctx, l1)
		other, _ := e.svc.BuildLoginResult(ctx, l2)
		if again.EnrollURI != r1.EnrollURI {
			t.Error("cùng (user, login) phải nhận cùng URI trong hạn offer")
		}
		if secretOf(t, other.EnrollURI) == secretOf(t, r1.EnrollURI) {
			t.Error("mỗi login một secret riêng")
		}
		if !regexp.MustCompile(`^[A-Z2-7]+$`).MatchString(secretOf(t, r1.EnrollURI)) {
			t.Error("secret phải là base32")
		}
		if r1.AllowedMfaMethods[0] != "totp" || len(r1.AllowedMfaMethods) != 1 {
			t.Errorf("mặc định chỉ totp: %v", r1.AllowedMfaMethods)
		}
		// secret được mời CHƯA thành secret của user
		if got := e.reload(u); got.MfaSecret != "" || domain.IsTrue(got.MfaEnrolled) {
			t.Error("mời chưa phải đăng ký")
		}
		// thương hiệu: appName của project, bỏ dấu ":" khỏi issuer
		branded := e.project2(strSetting("appName", "  Acme: Health "))
		u2 := e.user("b@example.org", func(u *fhir.User) { u.MfaRequired = ptr(true) })
		l := e.freshLogin(u2, func(l *fhir.Login) { l.Project = &fhir.Reference{Reference: "Project/" + branded.ID} })
		rb, _ := e.svc.BuildLoginResult(ctx, l)
		parsed, _ := url.Parse(rb.EnrollURI)
		if parsed.Query().Get("issuer") != "Acme Health" || strings.TrimPrefix(parsed.Path, "/") != "Acme Health:b@example.org" {
			t.Errorf("issuer/nhãn: %s", rb.EnrollURI)
		}
	})
}

// ---- đăng ký khi đăng nhập

func TestMFALoginEnrollTotpEndToEnd(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		u := e.user("an@example.org", func(u *fhir.User) { u.MfaRequired = ptr(true) })
		l := e.freshLogin(u, nil)
		pending, _ := e.svc.BuildLoginResult(ctx, l)
		secret := secretOf(t, pending.EnrollURI)

		_, _, err := e.svc.MFALoginEnroll(ctx, l.ID, "totp", "")
		wantErr(t, err, domain.KindBadRequest, "Missing token")
		_, _, err = e.svc.MFALoginEnroll(ctx, l.ID, "totp", "000000")
		wantErr(t, err, domain.KindBadRequest, "Invalid MFA token") // không phải "Invalid token"
		if got := e.reload(u); domain.IsTrue(got.MfaEnrolled) {
			t.Fatal("mã sai không được đăng ký")
		}
		res, _, err := e.svc.MFALoginEnroll(ctx, l.ID, "totp", e.code(secret, 0))
		if err != nil {
			t.Fatal(err)
		}
		if res.MfaRequired || res.MfaEnrollRequired {
			t.Errorf("đã xác minh thì không còn đòi MFA: %+v", res)
		}
		got := e.reload(u)
		if !domain.IsTrue(got.MfaEnrolled) || len(got.MfaMethod) != 1 || got.MfaMethod[0] != "totp" {
			t.Fatalf("user: %+v", got)
		}
		if got.MfaSecret == secret || strings.Contains(got.MfaSecret, secret) || !strings.HasPrefix(got.MfaSecret, "enc:v1:k1:") {
			t.Errorf("secret phải lưu dạng mã hóa: %q", got.MfaSecret)
		}
		if plain, err := e.svc.MFASecrets.Open(got.MfaSecret); err != nil || plain != secret {
			t.Errorf("giải mã: %q %v", plain, err)
		}
		if !domain.IsTrue(e.getLogin(l.ID).MfaVerified) {
			t.Error("login phải được đánh dấu đã xác minh")
		}
		if v, _ := e.svc.MFAState.OfferGet(ctx, u.ID, l.ID); v != "" {
			t.Error("secret được mời phải bị xóa sau khi đăng ký")
		}
		// đã đăng ký rồi thì không đăng ký lại
		_, _, err = e.svc.MFALoginEnroll(ctx, e.freshLogin(u, nil).ID, "totp", "123456")
		wantErr(t, err, domain.KindBadRequest, "Already enrolled")
	})
}

func TestMFALoginEnrollOrderOfChecks(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		u := e.user("an@example.org", func(u *fhir.User) { u.MfaRequired = ptr(true) })
		l := e.freshLogin(u, nil)
		// chưa có offer: "Secret not found" đến TRƯỚC "Missing token"
		_, _, err := e.svc.MFALoginEnroll(ctx, l.ID, "totp", "")
		wantErr(t, err, domain.KindBadRequest, "Secret not found")
		// method không có trong project: chỉ totp mặc định
		_, _, err = e.svc.MFALoginEnroll(ctx, l.ID, "email", "")
		wantErr(t, err, domain.KindBadRequest, "MFA method not allowed")
		_, _, err = e.svc.MFALoginEnroll(ctx, domain.NewID(), "totp", "123456")
		wantErr(t, err, domain.KindNotFound, "")
		// method lạ hay sai kiểu được coi là totp
		_, _, err = e.svc.MFALoginEnroll(ctx, l.ID, 42, "")
		wantErr(t, err, domain.KindBadRequest, "Secret not found")
		_, _, err = e.svc.MFALoginEnroll(ctx, l.ID, "sms", "")
		wantErr(t, err, domain.KindBadRequest, "Secret not found")
	})
}

func TestMFALoginEnrollEmailSendsCodeThenVerifyConfirmsEmail(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		p := e.project2(setting("mfaRequired", true), strSetting("allowedMfaMethods", "totp, email"))
		u := e.user("an@example.org", nil)
		l := e.freshLogin(u, func(l *fhir.Login) { l.Project = &fhir.Reference{Reference: "Project/" + p.ID} })
		res, _, err := e.svc.MFALoginEnroll(ctx, l.ID, "email", "")
		if err != nil || !res.MfaRequired || len(res.MfaMethods) != 1 || res.MfaMethods[0] != "email" || res.Email != "an@example.org" || res.Code != "" {
			t.Fatalf("%+v %v", res, err)
		}
		if n := len(e.mail.Sent()); n != 1 {
			t.Fatalf("phải gửi đúng một thư mã: %d", n)
		}
		m := e.mail.Sent()[0]
		code := e.lastMFACode()
		if m.To != "an@example.org" || m.Subject != "Your Trivita verification code: "+code || !strings.Contains(m.Text, "\n\n"+code+"\n\n") ||
			!strings.Contains(m.Text, "This code will expire in 20 minutes.") || !strings.HasSuffix(m.Text, "Thank you,\nThe Trivita Team\n") {
			t.Errorf("thư: %+v", m)
		}
		if got := e.reload(u); !domain.IsTrue(got.MfaEnrolled) || len(got.MfaMethod) != 1 || got.MfaMethod[0] != "email" || domain.IsTrue(got.EmailVerified) {
			t.Fatalf("user: %+v", got)
		}
		// mã lưu dạng băm bcrypt, không phải bản rõ
		if em := e.getLogin(l.ID).EmailMfa; em == nil || em.CodeHash == code || !strings.HasPrefix(em.CodeHash, "$2") {
			t.Fatalf("emailMfa: %+v", em)
		}
		// gọi lại BuildLoginResult không gửi thêm thư (chỉ một lần mỗi login)
		if _, err := e.svc.BuildLoginResult(ctx, e.getLogin(l.ID)); err != nil || len(e.mail.Sent()) != 1 {
			t.Errorf("không gửi lại: %v thư=%d", err, len(e.mail.Sent()))
		}
		_, _, err = e.svc.MFAVerify(ctx, l.ID, "000000")
		wantErr(t, err, domain.KindBadRequest, "Invalid MFA token")
		if _, _, err := e.svc.MFAVerify(ctx, l.ID, code); err != nil {
			t.Fatal(err)
		}
		got := e.reload(u)
		if !domain.IsTrue(got.EmailVerified) {
			t.Error("nhập đúng mã email chứng minh quyền kiểm soát hộp thư: email phải được xác minh")
		}
		lg := e.getLogin(l.ID)
		if !domain.IsTrue(lg.MfaVerified) || lg.EmailMfa != nil {
			t.Errorf("login: %+v", lg)
		}
		// mã chỉ dùng một lần
		_, _, err = e.svc.MFAVerify(ctx, l.ID, code)
		wantErr(t, err, domain.KindBadRequest, "Login already verified")
	})
}

// ---- verify: TOTP

func TestMFAVerifyTotpSingleUseAndWindow(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		u, secret := e.totpUser("an@example.org")
		l1 := e.freshLogin(u, nil)
		pending, err := e.svc.BuildLoginResult(ctx, l1)
		if err != nil || !pending.MfaRequired || pending.Code != "" || len(pending.MfaMethods) != 1 || pending.MfaMethods[0] != "totp" || pending.Email != "an@example.org" {
			t.Fatalf("%+v %v", pending, err)
		}
		code := e.code(secret, 0)
		res, _, err := e.svc.MFAVerify(ctx, l1.ID, code)
		if err != nil || res.MfaRequired {
			t.Fatalf("%+v %v", res, err)
		}
		if !domain.IsTrue(e.getLogin(l1.ID).MfaVerified) {
			t.Fatal("login phải mfaVerified")
		}
		// cùng mã dùng cho login khác của cùng user: bị từ chối (mỗi mã một lần cho mỗi user)
		l2 := e.freshLogin(u, nil)
		_, _, err = e.svc.MFAVerify(ctx, l2.ID, code)
		wantErr(t, err, domain.KindBadRequest, "Invalid MFA token")
		// mã của bước kế tiếp (trong cửa sổ window=1) được chấp nhận ngay
		if _, _, err := e.svc.MFAVerify(ctx, l2.ID, e.code(secret, 30*time.Second)); err != nil {
			t.Errorf("mã bước kế tiếp: %v", err)
		}
		// mã lệch hai bước thì không
		l3 := e.freshLogin(u, nil)
		_, _, err = e.svc.MFAVerify(ctx, l3.ID, e.code(secret, 90*time.Second))
		wantErr(t, err, domain.KindBadRequest, "Invalid MFA token")
		// mã không đúng định dạng: sai, không panic
		for _, bad := range []string{"1234567890", "INVALID_TOKEN", " 12345", "1"} {
			l := e.freshLogin(u, nil)
			_, _, err := e.svc.MFAVerify(ctx, l.ID, bad)
			wantErr(t, err, domain.KindBadRequest, "Invalid MFA token")
		}
	})
}

func TestMFAVerifyIgnoresStoredSecretOfUnenrolledUser(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		secret, _ := e.svc.TOTP.GenerateSecret()
		sealed, _ := e.svc.MFASecrets.Seal(secret)
		// user CHƯA đăng ký nhưng có secret lưu sẵn (ví dụ bị gỡ dở): không phải yếu tố
		u := e.user("an@example.org", func(u *fhir.User) { u.MfaSecret = sealed })
		l := e.freshLogin(u, nil)
		_, _, err := e.svc.MFAVerify(ctx, l.ID, e.code(secret, 0))
		wantErr(t, err, domain.KindBadRequest, "User not enrolled in MFA")
	})
}

func TestMFAVerifyEnrollsWithTheLoginOwnOffer(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		u := e.user("an@example.org", func(u *fhir.User) { u.MfaRequired = ptr(true) })
		l1, l2 := e.freshLogin(u, nil), e.freshLogin(u, nil)
		r1, _ := e.svc.BuildLoginResult(ctx, l1)
		r2, _ := e.svc.BuildLoginResult(ctx, l2)
		s1, s2 := secretOf(t, r1.EnrollURI), secretOf(t, r2.EnrollURI)
		// mã của offer của login KHÁC không dùng được cho login này
		_, _, err := e.svc.MFAVerify(ctx, l1.ID, e.code(s2, 0))
		wantErr(t, err, domain.KindBadRequest, "Invalid MFA token")
		if _, _, err := e.svc.MFAVerify(ctx, l1.ID, e.code(s1, 0)); err != nil {
			t.Fatal(err)
		}
		got := e.reload(u)
		if plain, _ := e.svc.MFASecrets.Open(got.MfaSecret); plain != s1 {
			t.Errorf("secret của user phải là offer của login đã xác nhận")
		}
		// login thứ hai (offer khác) không thể ghi đè: user đã đăng ký
		_, _, err = e.svc.MFAVerify(ctx, l2.ID, e.nextStepCode(s2))
		if err == nil {
			t.Fatal("login thua cuộc không được xác minh bằng offer của nó")
		}
		if domain.IsTrue(e.getLogin(l2.ID).MfaVerified) {
			t.Error("login thua cuộc không được mfaVerified")
		}
		// nhưng nó xác minh được bằng secret ĐÃ đăng ký của user
		if _, _, err := e.svc.MFAVerify(ctx, l2.ID, e.nextStepCode(s1)); err != nil {
			t.Errorf("secret đã đăng ký: %v", err)
		}
	})
}

func TestMFAOnlyOneOfTwoLoginsEnrollsAtOnce(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		u := e.user("an@example.org", func(u *fhir.User) { u.MfaRequired = ptr(true) })
		const n = 6
		type lg struct {
			id, secret string
		}
		var logins []lg
		for i := 0; i < n; i++ {
			l := e.freshLogin(u, nil)
			r, _ := e.svc.BuildLoginResult(ctx, l)
			logins = append(logins, lg{l.ID, secretOf(t, r.EnrollURI)})
		}
		var ok atomic.Int32
		var wg sync.WaitGroup
		start := make(chan struct{})
		for _, l := range logins {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				if _, _, err := e.svc.MFALoginEnroll(ctx, l.id, "totp", e.code(l.secret, 0)); err == nil {
					ok.Add(1)
				}
			}()
		}
		close(start)
		wg.Wait()
		if ok.Load() != 1 {
			t.Fatalf("đúng một login đăng ký được, được %d", ok.Load())
		}
		verified := 0
		for _, l := range logins {
			if domain.IsTrue(e.getLogin(l.id).MfaVerified) {
				verified++
			}
		}
		if verified != 1 {
			t.Errorf("chỉ login thắng được mfaVerified: %d", verified)
		}
	})
}

// ---- hạn mức thử

func TestMFAFifthWrongCodeRevokesTheLogin(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		u, secret := e.totpUser("an@example.org")
		l := e.freshLogin(u, nil)
		for i := 1; i <= 5; i++ {
			_, _, err := e.svc.MFAVerify(ctx, l.ID, "000000")
			wantErr(t, err, domain.KindBadRequest, "Invalid MFA token")
			if want := i == 5; domain.IsTrue(e.getLogin(l.ID).Revoked) != want {
				t.Fatalf("sau lần sai thứ %d: revoked=%v", i, !want)
			}
		}
		_, _, err := e.svc.MFAVerify(ctx, l.ID, e.code(secret, 0))
		wantErr(t, err, domain.KindBadRequest, "Login revoked")
		if domain.IsTrue(e.getLogin(l.ID).MfaVerified) {
			t.Error("login đã thu hồi không được xác minh")
		}
	})
}

func TestMFAAttemptLimitAcrossLoginsOfOneUser(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		u, secret := e.totpUser("an@example.org")
		// mỗi login sai đúng một lần: 20 lần thì login thứ 21 bị 429 dù nó chưa sai lần nào
		for i := 0; i < 20; i++ {
			_, _, err := e.svc.MFAVerify(ctx, e.freshLogin(u, nil).ID, "000000")
			wantErr(t, err, domain.KindBadRequest, "Invalid MFA token")
		}
		last := e.freshLogin(u, nil)
		_, _, err := e.svc.MFAVerify(ctx, last.ID, e.code(secret, 0))
		wantErr(t, err, domain.KindTooManyRequests, "")
		var de *domain.Error
		if !errors.As(err, &de) || de.RetryAfter <= 0 || de.RetryAfter > 10*time.Minute {
			t.Errorf("phải kèm thời gian chờ: %+v", de)
		}
		// user khác không bị ảnh hưởng
		other, os := e.totpUser("khac@example.org")
		if _, _, err := e.svc.MFAVerify(ctx, e.freshLogin(other, nil).ID, e.code(os, 0)); err != nil {
			t.Errorf("user khác: %v", err)
		}
		// hết cửa sổ 10 phút thì dùng lại được (login phải còn mới: tạo login mới)
		e.clock.t = e.clock.t.Add(10*time.Minute + time.Second)
		if _, _, err := e.svc.MFAVerify(ctx, e.freshLogin(u, nil).ID, e.code(secret, 0)); err != nil {
			t.Errorf("sau cửa sổ: %v", err)
		}
	})
}

func TestMFASuccessReturnsOneUserSlot(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		u, secret := e.totpUser("an@example.org")
		// 19 lượt sai rải trên các login khác, rồi một lần đúng: lượt thứ 20 được trả lại, nên login kế tiếp vẫn thử được
		for i := 0; i < 19; i++ {
			e.svc.MFAVerify(ctx, e.freshLogin(u, nil).ID, "000000")
		}
		if _, _, err := e.svc.MFAVerify(ctx, e.freshLogin(u, nil).ID, e.code(secret, 0)); err != nil {
			t.Fatal(err)
		}
		_, _, err := e.svc.MFAVerify(ctx, e.freshLogin(u, nil).ID, "000000")
		wantErr(t, err, domain.KindBadRequest, "Invalid MFA token") // không phải 429
	})
}

func TestMFAAttemptsAreAtomicUnderConcurrency(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		u, _ := e.totpUser("an@example.org")
		l := e.freshLogin(u, nil)
		var invalid, throttled, other atomic.Int32
		var wg sync.WaitGroup
		for i := 0; i < 30; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, _, err := e.svc.MFAVerify(ctx, l.ID, "000000")
				switch {
				case domain.Is(err, domain.KindBadRequest, "Invalid MFA token"):
					invalid.Add(1)
				case domain.Is(err, domain.KindTooManyRequests, ""):
					throttled.Add(1)
				case domain.Is(err, domain.KindBadRequest, "Login revoked"):
					other.Add(1) // lời gọi bắt đầu sau khi login đã bị thu hồi
				default:
					t.Errorf("lỗi lạ: %v", err)
				}
			}()
		}
		wg.Wait()
		if invalid.Load() > 5 {
			t.Fatalf("chỉ tối đa 5 lượt thử được chấm điểm, được %d", invalid.Load())
		}
		if !domain.IsTrue(e.getLogin(l.ID).Revoked) {
			t.Error("login phải bị thu hồi")
		}
	})
}

// ---- login hết hạn và điều kiện của login

func TestMFALoginMustBeActive(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		u, secret := e.totpUser("an@example.org")
		cases := []struct {
			name string
			mut  func(*fhir.Login)
			want string
		}{
			{"đã thu hồi", func(l *fhir.Login) { l.Revoked = ptr(true) }, "Login revoked"},
			{"đã cấp code", func(l *fhir.Login) { l.Granted = ptr(true) }, "Login granted"},
			{"đã xác minh", func(l *fhir.Login) { l.MfaVerified = ptr(true) }, "Login already verified"},
			{"không phải mật khẩu (google)", func(l *fhir.Login) { l.AuthMethod = domain.AuthGoogle }, "Invalid login authentication method"},
			{"không phải mật khẩu (ngoài)", func(l *fhir.Login) { l.AuthMethod = domain.AuthExternal }, "Invalid login authentication method"},
			{"quá 10 phút", func(l *fhir.Login) {
				l.AuthTime = e.clock.Now().Add(-10 * time.Minute).UTC().Format("2006-01-02T15:04:05.000Z")
			}, "Login expired"},
			{"authTime hỏng", func(l *fhir.Login) { l.AuthTime = "hôm qua" }, "Login expired"},
			{"authTime rỗng", func(l *fhir.Login) { l.AuthTime = "" }, "Login expired"},
		}
		for _, tc := range cases {
			l := e.freshLogin(u, tc.mut)
			_, _, err := e.svc.MFAVerify(ctx, l.ID, e.code(secret, 0))
			wantErr(t, err, domain.KindBadRequest, tc.want)
			if tc.want != "Login already verified" && tc.want != "Login revoked" && domain.IsTrue(e.getLogin(l.ID).Revoked) != (tc.want == "Login revoked") {
				t.Errorf("%s: trạng thái revoked bị đổi", tc.name)
			}
		}
		// 9 phút 59 giây vẫn còn hạn
		l := e.freshLogin(u, func(l *fhir.Login) {
			l.AuthTime = e.clock.Now().Add(-(10*time.Minute - time.Second)).UTC().Format("2006-01-02T15:04:05.000Z")
		})
		if _, _, err := e.svc.MFAVerify(ctx, l.ID, e.code(secret, 0)); err != nil {
			t.Errorf("còn hạn: %v", err)
		}
		// thứ tự: đã thu hồi được báo trước mọi lỗi khác
		both := e.freshLogin(u, func(l *fhir.Login) {
			l.Revoked, l.Granted, l.MfaVerified, l.AuthMethod = ptr(true), ptr(true), ptr(true), "google"
		})
		_, _, err := e.svc.MFAVerify(ctx, both.ID, "123456")
		wantErr(t, err, domain.KindBadRequest, "Login revoked")
		wantErr(t, e.svc.MFASendEmail(ctx, both.ID), domain.KindBadRequest, "Login revoked")
	})
}

// ---- mã email

func TestMFAEmailCodeExpiryAndQ6(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		expire := func(l *fhir.Login) *fhir.Login {
			out, err := e.r.Logins.Mutate(ctx, l.ID, func(x *fhir.Login) error {
				x.EmailMfa = &fhir.LoginEmailMfa{CodeHash: "$2a$04$" + strings.Repeat("x", 53), ExpiresAt: e.clock.Now().Add(-time.Second).UTC().Format("2006-01-02T15:04:05.000Z")}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			return out
		}
		// chỉ có email: mã hết hạn thì báo "MFA code expired" (và không tốn lượt thử)
		ue := e.emailUser("email@example.org")
		le := expire(e.freshLogin(ue, nil))
		_, _, err := e.svc.MFAVerify(ctx, le.ID, "123456")
		wantErr(t, err, domain.KindBadRequest, "MFA code expired")
		// Q6: user có cả TOTP lẫn email: mã email hết hạn KHÔNG chặn mã TOTP đúng (Medplum chặn cả mã TOTP tới khi gửi mã mới)
		both, secret := e.totpUser("ca-hai@example.org")
		if _, err := e.r.Users.Mutate(ctx, both.ID, func(u *fhir.User) error { u.MfaMethod = []string{"totp", "email"}; return nil }); err != nil {
			t.Fatal(err)
		}
		lb := expire(e.freshLogin(both, nil))
		if _, _, err := e.svc.MFAVerify(ctx, lb.ID, e.code(secret, 0)); err != nil {
			t.Fatalf("TOTP đúng phải qua dù mã email đã hết hạn: %v", err)
		}
		// mã email còn hạn thì bị bỏ qua khi sai, và TOTP vẫn thử được
		l2 := e.freshLogin(both, nil)
		if err := e.svc.MFASendEmail(ctx, l2.ID); err != nil {
			t.Fatal(err)
		}
		if _, _, err := e.svc.MFAVerify(ctx, l2.ID, e.nextStepCode(secret)); err != nil {
			t.Errorf("TOTP khi có mã email còn hạn: %v", err)
		}
	})
}

func TestMFASendEmail(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		totpOnly, _ := e.totpUser("totp@example.org")
		wantErr(t, e.svc.MFASendEmail(ctx, e.freshLogin(totpOnly, nil).ID), domain.KindBadRequest, "User not enrolled in email MFA")
		wantErr(t, e.svc.MFASendEmail(ctx, e.freshLogin(e.user("chua@example.org", nil), nil).ID), domain.KindBadRequest, "User not enrolled in email MFA")
		wantErr(t, e.svc.MFASendEmail(ctx, domain.NewID()), domain.KindNotFound, "")

		u := e.emailUser("email@example.org")
		l := e.freshLogin(u, nil)
		if err := e.svc.MFASendEmail(ctx, l.ID); err != nil {
			t.Fatal(err)
		}
		first := e.lastMFACode()
		// gửi lại GHI ĐÈ mã cũ: mã cũ không còn dùng được
		if err := e.svc.MFASendEmail(ctx, l.ID); err != nil {
			t.Fatal(err)
		}
		second := e.lastMFACode()
		if first != second {
			_, _, err := e.svc.MFAVerify(ctx, l.ID, first)
			wantErr(t, err, domain.KindBadRequest, "Invalid MFA token")
		}
		if _, _, err := e.svc.MFAVerify(ctx, l.ID, second); err != nil {
			t.Errorf("mã mới: %v", err)
		}
	})
}

func TestMFAEmailSendIsRateLimitedPerLogin(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		u := e.emailUser("email@example.org")
		l, other := e.freshLogin(u, nil), e.freshLogin(u, nil)
		for i := 0; i < 5; i++ {
			if err := e.svc.MFASendEmail(ctx, l.ID); err != nil {
				t.Fatalf("lần %d: %v", i+1, err)
			}
		}
		err := e.svc.MFASendEmail(ctx, l.ID)
		wantErr(t, err, domain.KindTooManyRequests, "")
		if len(e.mail.Sent()) != 5 {
			t.Errorf("lần bị chặn không được gửi thư: %d", len(e.mail.Sent()))
		}
		if err := e.svc.MFASendEmail(ctx, other.ID); err != nil {
			t.Errorf("login khác độc lập: %v", err)
		}
		e.clock.t = e.clock.t.Add(21 * time.Minute)
		if err := e.svc.MFASendEmail(ctx, e.freshLogin(u, nil).ID); err != nil {
			t.Errorf("sau cửa sổ: %v", err)
		}
	})
}

func TestMFAEmailDeliveryFailureFailsClosed(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		u := e.emailUser("email@example.org")
		l := e.freshLogin(u, nil)
		e.mail.Err = errors.New("smtp down")
		wantErr(t, e.svc.MFASendEmail(ctx, l.ID), domain.KindInternal, "")
		// BuildLoginResult (đăng nhập) cũng lỗi: không trả code, không im lặng bỏ qua yếu tố
		res, err := e.svc.BuildLoginResult(ctx, e.freshLogin(u, nil))
		if err == nil || (res != nil && res.Code != "") {
			t.Errorf("gửi mã lỗi phải làm đăng nhập lỗi: %+v %v", res, err)
		}
		// không có kênh gửi thư: cũng lỗi
		svc := e.serviceWithMail(nil, application.MailConfig{})
		svc.TOTP, svc.QR, svc.MFAState, svc.MFASecrets = e.svc.TOTP, e.svc.QR, e.svc.MFAState, e.svc.MFASecrets
		if err := svc.UseMFA(); err != nil {
			t.Fatal(err)
		}
		wantErr(t, svc.MFASendEmail(ctx, e.freshLogin(u, nil).ID), domain.KindInternal, "")
	})
}

// ---- Bearer: status, enroll, disable

func TestMFAStatus(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		p := e.project2(setting("mfaRequired", true), strSetting("allowedMfaMethods", "totp,email"))
		u := e.user("an@example.org", nil)
		l := e.freshLogin(u, nil)
		a := e.bearer(u, l, p)
		st, err := e.svc.MFAStatus(ctx, a)
		if err != nil || st.Enrolled || len(st.EnrolledMethods) != 0 || st.EnrolledMethods == nil || !st.MfaRequired || st.Email != "an@example.org" ||
			len(st.AllowedMethods) != 2 || !strings.HasPrefix(st.EnrollURI, "otpauth://totp/") || !strings.HasPrefix(st.EnrollQRCode, "data:image/png;base64,") {
			t.Fatalf("%+v %v", st, err)
		}
		again, _ := e.svc.MFAStatus(ctx, a)
		if again.EnrollURI != st.EnrollURI {
			t.Error("hai lần gọi cho cùng enrollUri")
		}
		// đã đăng ký TOTP: không còn enrollUri
		ue, _ := e.totpUser("da-dang-ky@example.org")
		le := e.freshLogin(ue, nil)
		st, err = e.svc.MFAStatus(ctx, e.bearer(ue, le, nil))
		if err != nil || !st.Enrolled || len(st.EnrolledMethods) != 1 || st.EnrollURI != "" || st.EnrollQRCode != "" || st.MfaRequired || len(st.AllowedMethods) != 1 {
			t.Fatalf("%+v %v", st, err)
		}
		// chỉ đăng ký email: vẫn được mời đăng ký TOTP
		em := e.emailUser("email@example.org")
		st, _ = e.svc.MFAStatus(ctx, e.bearer(em, e.freshLogin(em, nil), nil))
		if !st.Enrolled || st.EnrollURI == "" {
			t.Errorf("%+v", st)
		}
	})
}

func TestMFAAccountGuards(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		u := e.user("an@example.org", nil)
		l := e.freshLogin(u, nil)
		clientState := &application.AuthState{Login: &fhir.Login{AuthMethod: domain.AuthClient}, Membership: &fhir.ProjectMembership{User: &fhir.Reference{Reference: "ClientApplication/c1"}}}
		smart := &application.AuthState{Login: &fhir.Login{AuthMethod: domain.AuthPassword, Scope: "patient/Patient.rs"}, Membership: &fhir.ProjectMembership{User: &fhir.Reference{Reference: "User/" + u.ID}}}
		ext := &application.AuthState{Login: &fhir.Login{AuthMethod: domain.AuthExternal}, Membership: &fhir.ProjectMembership{User: &fhir.Reference{Reference: "User/" + u.ID}}}
		for name, a := range map[string]*application.AuthState{"ClientApplication": clientState, "scope SMART": smart, "user toàn cục đăng nhập ngoài": ext} {
			_, err := e.svc.MFAStatus(ctx, a)
			wantErr(t, err, domain.KindForbidden, "")
			wantErr(t, e.svc.MFAEnroll(ctx, a, "totp", "123456"), domain.KindForbidden, "")
			wantErr(t, e.svc.MFASendEmailChallenge(ctx, a), domain.KindForbidden, "")
			wantErr(t, e.svc.MFADisable(ctx, a, application.MFADisableRequest{Token: "123456"}), domain.KindForbidden, "")
			_ = name
		}
		_ = l
	})
}

func TestMFAEnrollTotpAsLoggedInUser(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		u := e.user("an@example.org", nil)
		l := e.freshLogin(u, nil)
		a := e.bearer(u, l, nil)
		// chưa gọi status: không có secret được mời
		wantErr(t, e.svc.MFAEnroll(ctx, a, "totp", "123456"), domain.KindBadRequest, "Secret not found")
		st, _ := e.svc.MFAStatus(ctx, a)
		secret := secretOf(t, st.EnrollURI)
		wantErr(t, e.svc.MFAEnroll(ctx, a, "totp", ""), domain.KindBadRequest, "Missing token")
		wantErr(t, e.svc.MFAEnroll(ctx, a, "totp", "000000"), domain.KindBadRequest, "Invalid token") // enroll giữ "Invalid token"
		wantErr(t, e.svc.MFAEnroll(ctx, a, "email", "123456"), domain.KindBadRequest, "MFA method not allowed")
		if domain.IsTrue(e.reload(u).MfaEnrolled) {
			t.Fatal("mã sai không được đăng ký")
		}
		// secret của login KHÁC không dùng được (offer theo cặp user-login, không nhận secret từ client)
		other := e.freshLogin(u, nil)
		os, _ := e.svc.MFAStatus(ctx, e.bearer(u, other, nil))
		wantErr(t, e.svc.MFAEnroll(ctx, a, "totp", e.code(secretOf(t, os.EnrollURI), 0)), domain.KindBadRequest, "Invalid token")
		code := e.code(secret, 0)
		if err := e.svc.MFAEnroll(ctx, a, "totp", code); err != nil {
			t.Fatal(err)
		}
		got := e.reload(u)
		if !domain.IsTrue(got.MfaEnrolled) || !domain.HasMFAMethod(got, "totp") || got.MfaSecret == secret {
			t.Fatalf("%+v", got)
		}
		wantErr(t, e.svc.MFAEnroll(ctx, a, "totp", code), domain.KindBadRequest, "Already enrolled")
		// mã dùng để đăng ký KHÔNG bị tiêu thụ: vẫn dùng được để đăng nhập trong cùng cửa sổ
		l2 := e.freshLogin(u, nil)
		if _, _, err := e.svc.MFAVerify(ctx, l2.ID, code); err != nil {
			t.Errorf("mã đăng ký chưa bị tiêu thụ: %v", err)
		}
	})
}

func TestMFAEnrollEmailAsLoggedInUser(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		p := e.project2(strSetting("allowedMfaMethods", "totp,email"))
		u := e.user("an@example.org", nil)
		l := e.freshLogin(u, nil)
		a := e.bearer(u, l, p)
		wantErr(t, e.svc.MFAEnroll(ctx, a, "email", ""), domain.KindBadRequest, "Missing token")
		wantErr(t, e.svc.MFAEnroll(ctx, a, "email", "123456"), domain.KindBadRequest, "Invalid token") // chưa có mã
		if err := e.svc.MFASendEmailChallenge(ctx, a); err != nil {
			t.Fatal(err)
		}
		code := e.lastMFACode()
		wantErr(t, e.svc.MFAEnroll(ctx, a, "email", "000000"), domain.KindBadRequest, "Invalid token")
		if err := e.svc.MFAEnroll(ctx, a, "email", code); err != nil {
			t.Fatal(err)
		}
		got := e.reload(u)
		if !domain.IsTrue(got.MfaEnrolled) || !domain.HasMFAMethod(got, "email") || !domain.IsTrue(got.EmailVerified) {
			t.Fatalf("%+v", got)
		}
		if e.getLogin(l.ID).EmailMfa != nil {
			t.Error("mã email phải bị xóa sau khi dùng")
		}
		wantErr(t, e.svc.MFAEnroll(ctx, a, "email", code), domain.KindBadRequest, "Already enrolled")
	})
}

func TestMFASendEmailChallengeAvailability(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		u := e.user("an@example.org", nil)
		// project chỉ cho totp và user chưa đăng ký email: không có
		wantErr(t, e.svc.MFASendEmailChallenge(ctx, e.bearer(u, e.freshLogin(u, nil), nil)), domain.KindBadRequest, "Email MFA not available")
		// user đã đăng ký email thì luôn gửi được, kể cả khi project không cho email nữa
		em := e.emailUser("email@example.org")
		if err := e.svc.MFASendEmailChallenge(ctx, e.bearer(em, e.freshLogin(em, nil), nil)); err != nil {
			t.Error(err)
		}
	})
}

func TestMFAEnrollAndDisableAreAttemptLimited(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		// enroll: 5 mã sai thu hồi phiên (Medplum không giới hạn ở đây, L-M1)
		u := e.user("an@example.org", nil)
		l := e.freshLogin(u, nil)
		a := e.bearer(u, l, nil)
		e.svc.MFAStatus(ctx, a)
		for i := 0; i < 5; i++ {
			wantErr(t, e.svc.MFAEnroll(ctx, a, "totp", "000000"), domain.KindBadRequest, "Invalid token")
		}
		if !domain.IsTrue(e.getLogin(l.ID).Revoked) {
			t.Error("lần sai thứ 5 ở enroll phải thu hồi phiên")
		}
		wantErr(t, e.svc.MFAEnroll(ctx, a, "totp", "000000"), domain.KindTooManyRequests, "")
		// disable
		ud, secret := e.totpUser("disable@example.org")
		ld := e.freshLogin(ud, nil)
		ad := e.bearer(ud, ld, nil)
		for i := 0; i < 5; i++ {
			wantErr(t, e.svc.MFADisable(ctx, ad, application.MFADisableRequest{Token: "000000"}), domain.KindBadRequest, "Invalid token")
		}
		if !domain.IsTrue(e.getLogin(ld.ID).Revoked) || !domain.IsTrue(e.reload(ud).MfaEnrolled) {
			t.Error("phiên bị thu hồi và MFA vẫn còn")
		}
		wantErr(t, e.svc.MFADisable(ctx, ad, application.MFADisableRequest{Token: e.code(secret, 0)}), domain.KindTooManyRequests, "")
	})
}

func TestMFADisable(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		// chưa đăng ký: báo TRƯỚC khi kiểm kiểu của body
		none := e.user("chua@example.org", nil)
		wantErr(t, e.svc.MFADisable(ctx, e.bearer(none, e.freshLogin(none, nil), nil), application.MFADisableRequest{Token: 123}), domain.KindBadRequest, "User not enrolled in MFA")

		u, secret := e.totpUser("an@example.org")
		l := e.freshLogin(u, nil)
		a := e.bearer(u, l, nil)
		wantErr(t, e.svc.MFADisable(ctx, a, application.MFADisableRequest{Token: 123}), domain.KindBadRequest, "Token must be a string")
		wantErr(t, e.svc.MFADisable(ctx, a, application.MFADisableRequest{Method: 5, Token: "1"}), domain.KindBadRequest, "Method must be a string")
		wantErr(t, e.svc.MFADisable(ctx, a, application.MFADisableRequest{Method: "sms", Token: "1"}), domain.KindBadRequest, "Invalid method")
		wantErr(t, e.svc.MFADisable(ctx, a, application.MFADisableRequest{Method: "email", Token: "1"}), domain.KindBadRequest, "User not enrolled in MFA method")
		wantErr(t, e.svc.MFADisable(ctx, a, application.MFADisableRequest{}), domain.KindBadRequest, "Missing token")
		wantErr(t, e.svc.MFADisable(ctx, a, application.MFADisableRequest{Token: ""}), domain.KindBadRequest, "Missing token")
		wantErr(t, e.svc.MFADisable(ctx, a, application.MFADisableRequest{Token: "000000"}), domain.KindBadRequest, "Invalid token")
		if !domain.IsTrue(e.reload(u).MfaEnrolled) {
			t.Fatal("chưa gỡ được")
		}
		code := e.code(secret, 0)
		if err := e.svc.MFADisable(ctx, a, application.MFADisableRequest{Token: code}); err != nil {
			t.Fatal(err)
		}
		got := e.reload(u)
		if domain.IsTrue(got.MfaEnrolled) || len(got.MfaMethod) != 0 {
			t.Fatalf("phải gỡ hết: %+v", got)
		}
		// secret cũ bị thay bằng secret ngẫu nhiên mới (kể cả khi không còn yếu tố nào)
		if got.MfaSecret == "" {
			t.Error("phải có secret mới")
		} else if plain, _ := e.svc.MFASecrets.Open(got.MfaSecret); plain == secret || plain == "" {
			t.Error("secret cũ phải bị thay")
		}
		// mã đã dùng để gỡ không dùng lại được; secret cũ không còn là yếu tố
		_, _, err := e.svc.MFAVerify(ctx, e.freshLogin(got, nil).ID, e.nextStepCode(secret))
		wantErr(t, err, domain.KindBadRequest, "User not enrolled in MFA")
	})
}

func TestMFADisableRefusesTheLastFactorWhenRequired(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		for name, tc := range map[string]struct {
			project *fhir.Project
			user    func(*fhir.User)
		}{
			"project bắt buộc":           {e.project2(setting("mfaRequired", true)), nil},
			"user tự bắt buộc":           {nil, func(u *fhir.User) { u.MfaRequired = ptr(true) }},
			"project cấm cả phương thức": {e.project2(setting("mfaRequired", true), strSetting("allowedMfaMethods", "email")), nil},
		} {
			u, secret := e.totpUser(name + "@example.org")
			if tc.user != nil {
				if _, err := e.r.Users.Mutate(ctx, u.ID, func(x *fhir.User) error { tc.user(x); return nil }); err != nil {
					t.Fatal(err)
				}
			}
			l := e.freshLogin(u, nil)
			a := e.bearer(u, l, tc.project)
			wantErr(t, e.svc.MFADisable(ctx, a, application.MFADisableRequest{Token: e.nextStepCode(secret)}), domain.KindBadRequest, "Cannot remove the last MFA factor because MFA is required")
			wantErr(t, e.svc.MFADisable(ctx, a, application.MFADisableRequest{Method: "totp", Token: e.nextStepCode(secret)}), domain.KindBadRequest, "Cannot remove the last MFA factor because MFA is required")
			if !domain.IsTrue(e.reload(u).MfaEnrolled) {
				t.Errorf("%s: không được gỡ", name)
			}
		}
	})
}

func TestMFADisableOneOfTwoFactors(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		p := e.project2(setting("mfaRequired", true), strSetting("allowedMfaMethods", "totp,email"))
		u, secret := e.totpUser("an@example.org")
		if _, err := e.r.Users.Mutate(ctx, u.ID, func(x *fhir.User) error { x.MfaMethod = []string{"totp", "email"}; return nil }); err != nil {
			t.Fatal(err)
		}
		l := e.freshLogin(u, nil)
		a := e.bearer(u, l, p)
		// gỡ email bằng mã TOTP: còn totp nên được dù MFA bắt buộc, secret TOTP giữ nguyên
		before := e.reload(u).MfaSecret
		if err := e.svc.MFADisable(ctx, a, application.MFADisableRequest{Method: "email", Token: e.code(secret, 0)}); err != nil {
			t.Fatal(err)
		}
		got := e.reload(u)
		if !domain.IsTrue(got.MfaEnrolled) || len(got.MfaMethod) != 1 || got.MfaMethod[0] != "totp" || got.MfaSecret != before {
			t.Fatalf("%+v", got)
		}
		// đăng ký email lại rồi gỡ TOTP bằng mã email: secret TOTP bị thay
		if err := e.svc.MFASendEmailChallenge(ctx, a); err != nil {
			t.Fatal(err)
		}
		if err := e.svc.MFAEnroll(ctx, a, "email", e.lastMFACode()); err != nil {
			t.Fatal(err)
		}
		if err := e.svc.MFASendEmailChallenge(ctx, a); err != nil {
			t.Fatal(err)
		}
		code := e.lastMFACode()
		if err := e.svc.MFADisable(ctx, a, application.MFADisableRequest{Method: "totp", Token: code}); err != nil {
			t.Fatal(err)
		}
		got = e.reload(u)
		if !domain.IsTrue(got.MfaEnrolled) || len(got.MfaMethod) != 1 || got.MfaMethod[0] != "email" || got.MfaSecret == before {
			t.Fatalf("%+v", got)
		}
		// mã email dùng một lần: gỡ nốt bằng cùng mã thì sai
		wantErr(t, e.svc.MFADisable(ctx, a, application.MFADisableRequest{Method: "email", Token: code}), domain.KindBadRequest, "Cannot remove the last MFA factor because MFA is required")
	})
}

// ---- tương thích ngược, cấu hình, fail đóng, log

func TestMFALegacyUserWithPlainSecretAndNoMethodList(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		secret, _ := e.svc.TOTP.GenerateSecret()
		// người dùng nhập từ Medplum: mfaEnrolled + secret THÔ, không có mfaMethod
		u := e.user("cu@example.org", func(u *fhir.User) { u.MfaEnrolled, u.MfaSecret = ptr(true), secret })
		if m := domain.EnrolledMFAMethods(u); len(m) != 1 || m[0] != "totp" {
			t.Fatalf("phải coi là totp: %v", m)
		}
		l := e.freshLogin(u, nil)
		if _, _, err := e.svc.MFAVerify(ctx, l.ID, e.code(secret, 0)); err != nil {
			t.Fatalf("secret thô cũ vẫn xác minh được: %v", err)
		}
		st, err := e.svc.MFAStatus(ctx, e.bearer(u, e.freshLogin(u, nil), nil))
		if err != nil || !st.Enrolled || st.EnrolledMethods[0] != "totp" || st.EnrollURI != "" {
			t.Errorf("%+v %v", st, err)
		}
	})
}

func TestMFAWithoutDependenciesFailsClosed(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		u, secret := e.totpUser("an@example.org")
		s := *e.svc
		s.TOTP, s.MFA = nil, nil // thiếu một phụ thuộc (và bỏ cổng MFA đã gắn vào service gốc)
		svc := application.NewService(s.Deps)
		l := e.freshLogin(u, nil)
		_, _, err := svc.MFAVerify(ctx, l.ID, e.code(secret, 0))
		wantErr(t, err, domain.KindInternal, "")
		// user đã đăng ký: chỉ trả cờ yêu cầu, tuyệt đối không có code
		if res, err := svc.BuildLoginResult(ctx, l); err != nil || res.Code != "" || !res.MfaRequired {
			t.Errorf("phải đòi MFA và không có code: %+v %v", res, err)
		}
		// user bị bắt buộc nhưng chưa đăng ký: cần dựng QR, không có MFA thật thì lỗi (không âm thầm cho qua)
		need := e.user("can-dang-ky@example.org", func(u *fhir.User) { u.MfaRequired = ptr(true) })
		if res, err := svc.BuildLoginResult(ctx, e.freshLogin(need, nil)); err == nil || (res != nil && res.Code != "") {
			t.Errorf("không có MFA thật thì nhánh cần đăng ký phải lỗi: %+v %v", res, err)
		}
		for _, call := range []func() error{
			func() error { _, err := svc.MFAStatus(ctx, e.bearer(u, l, nil)); return err },
			func() error { return svc.MFAEnroll(ctx, e.bearer(u, l, nil), "totp", "1") },
			func() error {
				return svc.MFADisable(ctx, e.bearer(u, l, nil), application.MFADisableRequest{Token: "1"})
			},
			func() error { return svc.MFASendEmail(ctx, l.ID) },
			func() error { return svc.MFASendEmailChallenge(ctx, e.bearer(u, l, nil)) },
		} {
			wantErr(t, call(), domain.KindInternal, "")
		}
		if domain.IsTrue(e.getLogin(l.ID).MfaVerified) {
			t.Error("không được xác minh khi MFA không hoạt động")
		}
		if err := application.NewService(s.Deps).UseMFA(); err == nil {
			t.Error("UseMFA thiếu phụ thuộc phải lỗi")
		}
	})
}

func TestMFAStateFailureFailsClosed(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		u, secret := e.totpUser("an@example.org")
		l := e.freshLogin(u, nil)
		svc := e.serviceWithMail(e.mail, application.MailConfig{AppBaseURL: "https://app.example.org"})
		svc.TOTP, svc.QR, svc.MFASecrets, svc.MFAState = e.svc.TOTP, e.svc.QR, e.svc.MFASecrets, brokenState{}
		if err := svc.UseMFA(); err != nil {
			t.Fatal(err)
		}
		_, _, err := svc.MFAVerify(ctx, l.ID, e.code(secret, 0))
		wantErr(t, err, domain.KindInternal, "")
		if domain.IsTrue(e.getLogin(l.ID).MfaVerified) {
			t.Error("kho trạng thái hỏng: không được cho qua")
		}
	})
}

type brokenState struct{}

var errBroken = errors.New("redis down")

func (brokenState) OfferGetOrCreate(context.Context, string, string, string, time.Duration) (string, error) {
	return "", errBroken
}
func (brokenState) OfferGet(context.Context, string, string) (string, error) { return "", errBroken }
func (brokenState) OfferDelete(context.Context, string, string) error        { return errBroken }
func (brokenState) ReserveAttempt(context.Context, string, string, int, int, time.Duration) (application.AttemptResult, error) {
	return application.AttemptResult{}, errBroken
}
func (brokenState) ReleaseAttempt(context.Context, string, string) error { return errBroken }
func (brokenState) ClaimTOTP(context.Context, string, string, time.Duration) (bool, error) {
	return false, errBroken
}
func (brokenState) Hit(context.Context, string, int, time.Duration) (int, time.Duration, bool, error) {
	return 0, 0, false, errBroken
}

// S-M13: log không chứa mã, secret, enrollUri hay mã QR ở mọi luồng MFA (kể cả đường lỗi).
func TestMFAFlowsNeverLogCodesSecretsOrURIs(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		var buf bytes.Buffer
		e.svc.Logger = slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
		ctx := context.Background()
		p := e.project2(setting("mfaRequired", true), strSetting("allowedMfaMethods", "totp,email"))
		u := e.user("nhat-ky-mfa@example.org", nil)
		l := e.freshLogin(u, func(l *fhir.Login) { l.Project = &fhir.Reference{Reference: "Project/" + p.ID} })
		pending, err := e.svc.BuildLoginResult(ctx, l)
		if err != nil {
			t.Fatal(err)
		}
		secret := secretOf(t, pending.EnrollURI)
		e.svc.MFALoginEnroll(ctx, l.ID, "totp", "000000") // sai
		if _, _, err := e.svc.MFALoginEnroll(ctx, l.ID, "totp", e.code(secret, 0)); err != nil {
			t.Fatal(err)
		}
		a := e.bearer(u, e.freshLogin(u, nil), p)
		e.svc.MFASendEmailChallenge(ctx, a)
		emailCode := e.lastMFACode()
		e.svc.MFAEnroll(ctx, a, "email", emailCode)
		e.svc.MFADisable(ctx, a, application.MFADisableRequest{Token: "000000"})
		for i := 0; i < 5; i++ {
			e.svc.MFAVerify(ctx, e.freshLogin(u, nil).ID, "111111")
		}
		log := buf.String()
		for name, secretVal := range map[string]string{"secret TOTP": secret, "enrollUri": pending.EnrollURI, "mã QR": pending.EnrollQRCode, "mã email": emailCode,
			"mã sai": "111111", "email": "nhat-ky-mfa@example.org"} {
			if secretVal != "" && strings.Contains(log, secretVal) {
				t.Errorf("log chứa %s", name)
			}
		}
	})
}

// spyState ghi lại tham số các lời gọi kho trạng thái để kiểm các hằng số (TTL nhớ mã, giới hạn gửi mã, giới hạn thử).
type spyState struct {
	application.MFAState
	mu                    sync.Mutex
	claimTTL              time.Duration
	hitKey                string
	hitLimit              int
	hitWindow             time.Duration
	loginLimit, userLimit int
	window                time.Duration
	offerTTL              time.Duration
	offerUser, offerLogin string
}

func (s *spyState) ClaimTOTP(ctx context.Context, u, tok string, ttl time.Duration) (bool, error) {
	s.mu.Lock()
	s.claimTTL = ttl
	s.mu.Unlock()
	return s.MFAState.ClaimTOTP(ctx, u, tok, ttl)
}

func (s *spyState) Hit(ctx context.Context, key string, limit int, w time.Duration) (int, time.Duration, bool, error) {
	s.mu.Lock()
	s.hitKey, s.hitLimit, s.hitWindow = key, limit, w
	s.mu.Unlock()
	return s.MFAState.Hit(ctx, key, limit, w)
}

func (s *spyState) ReserveAttempt(ctx context.Context, u, l string, ll, ul int, w time.Duration) (application.AttemptResult, error) {
	s.mu.Lock()
	s.loginLimit, s.userLimit, s.window = ll, ul, w
	s.mu.Unlock()
	return s.MFAState.ReserveAttempt(ctx, u, l, ll, ul, w)
}

func (s *spyState) OfferGetOrCreate(ctx context.Context, u, l, c string, ttl time.Duration) (string, error) {
	s.mu.Lock()
	s.offerTTL, s.offerUser, s.offerLogin = ttl, u, l
	s.mu.Unlock()
	return s.MFAState.OfferGetOrCreate(ctx, u, l, c, ttl)
}

func (e *env) withSpy() (*spyState, *application.Service) {
	spy := &spyState{MFAState: e.svc.MFAState}
	s := *e.svc
	s.MFAState, s.MFA = spy, nil
	svc := application.NewService(s.Deps)
	return spy, svc
}

func TestMFAPassesTheDocumentedConstantsToTheStateStore(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		spy, svc := e.withSpy()
		u, secret := e.totpUser("an@example.org")
		l := e.freshLogin(u, nil)
		if _, _, err := svc.MFAVerify(ctx, l.ID, e.code(secret, 0)); err != nil {
			t.Fatal(err)
		}
		// mã TOTP được nhớ (2*window+2) bước = 4 bước x 30 giây = 120 giây khi window=1
		if spy.claimTTL != 120*time.Second {
			t.Errorf("TTL nhớ mã: %v", spy.claimTTL)
		}
		if spy.loginLimit != 5 || spy.userLimit != 20 || spy.window != 10*time.Minute {
			t.Errorf("hạn mức: %d %d %v", spy.loginLimit, spy.userLimit, spy.window)
		}
		// offer 20 phút, theo đúng (user, login)
		ue := e.user("moi@example.org", func(u *fhir.User) { u.MfaRequired = ptr(true) })
		le := e.freshLogin(ue, nil)
		if _, err := svc.BuildLoginResult(ctx, le); err != nil {
			t.Fatal(err)
		}
		if spy.offerTTL != 20*time.Minute || spy.offerUser != ue.ID || spy.offerLogin != le.ID {
			t.Errorf("offer: %v %q %q", spy.offerTTL, spy.offerUser, spy.offerLogin)
		}
		// gửi mã email: khóa theo login, 5 lần trong 20 phút; mã hết hạn sau 20 phút
		em := e.emailUser("email@example.org")
		lm := e.freshLogin(em, nil)
		if err := svc.MFASendEmail(ctx, lm.ID); err != nil {
			t.Fatal(err)
		}
		if spy.hitKey != "send:"+lm.ID || spy.hitLimit != 5 || spy.hitWindow != 20*time.Minute {
			t.Errorf("giới hạn gửi: %q %d %v", spy.hitKey, spy.hitLimit, spy.hitWindow)
		}
		exp, err := time.Parse(time.RFC3339Nano, e.getLogin(lm.ID).EmailMfa.ExpiresAt)
		if d := exp.Sub(e.clock.Now()); err != nil || d > 20*time.Minute || d <= 20*time.Minute-time.Millisecond { // định dạng cắt tới mili giây
			t.Errorf("hạn mã email: %v %v", exp.Sub(e.clock.Now()), err)
		}
		// cấu hình ghi đè được
		cfg := application.MFAConfig{Window: 2, LoginAttemptLimit: 3, UserAttemptLimit: 7, LoginExpiration: 5 * time.Minute, EmailCodeExpiration: 7 * time.Minute, EnrollmentExpiration: 9 * time.Minute, EmailSendLimit: 2}
		s2 := *e.svc
		s2.MFAState, s2.MFA, s2.MFACfg = spy, nil, cfg
		svc2 := application.NewService(s2.Deps)
		u2, sec2 := e.totpUser("cauhinh@example.org")
		if _, _, err := svc2.MFAVerify(ctx, e.freshLogin(u2, nil).ID, e.code(sec2, 0)); err != nil {
			t.Fatal(err)
		}
		if spy.claimTTL != 6*30*time.Second || spy.loginLimit != 3 || spy.userLimit != 7 || spy.window != 5*time.Minute {
			t.Errorf("cấu hình: %v %d %d %v", spy.claimTTL, spy.loginLimit, spy.userLimit, spy.window)
		}
		l2 := e.freshLogin(em, nil)
		if err := svc2.MFASendEmail(ctx, l2.ID); err != nil {
			t.Fatal(err)
		}
		if spy.hitLimit != 2 || spy.hitWindow != 7*time.Minute {
			t.Errorf("gửi theo cấu hình: %d %v", spy.hitLimit, spy.hitWindow)
		}
		if _, err := svc2.BuildLoginResult(ctx, e.freshLogin(ue, nil)); err != nil || spy.offerTTL != 9*time.Minute {
			t.Errorf("offer theo cấu hình: %v %v", spy.offerTTL, err)
		}
	})
}

func TestMFAEveryDependencyIsRequired(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		for name, drop := range map[string]func(*application.Deps){
			"TOTP": func(d *application.Deps) { d.TOTP = nil }, "QR": func(d *application.Deps) { d.QR = nil },
			"MFAState": func(d *application.Deps) { d.MFAState = nil }, "MFASecrets": func(d *application.Deps) { d.MFASecrets = nil },
		} {
			d := e.svc.Deps
			d.MFA = nil
			drop(&d)
			svc := application.NewService(d)
			if err := svc.UseMFA(); err == nil {
				t.Errorf("thiếu %s: UseMFA phải lỗi", name)
			}
			need := e.user(name+"@example.org", func(u *fhir.User) { u.MfaRequired = ptr(true) })
			if res, err := svc.BuildLoginResult(context.Background(), e.freshLogin(need, nil)); err == nil || (res != nil && res.Code != "") {
				t.Errorf("thiếu %s: nhánh cần đăng ký phải lỗi: %+v %v", name, res, err)
			}
		}
	})
}

func TestMFAEnrollmentLabelFallsBackToUserIDWithoutEmail(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		u := e.user("an@example.org", func(u *fhir.User) { u.MfaRequired = ptr(true) })
		if _, err := e.r.Users.Mutate(context.Background(), u.ID, func(x *fhir.User) error { x.Email = ""; return nil }); err != nil {
			t.Fatal(err)
		}
		res, err := e.svc.BuildLoginResult(context.Background(), e.freshLogin(e.reload(u), nil))
		if err != nil {
			t.Fatal(err)
		}
		parsed, _ := url.Parse(res.EnrollURI)
		if strings.TrimPrefix(parsed.Path, "/") != "Trivita:"+u.ID {
			t.Errorf("nhãn: %s", res.EnrollURI)
		}
		// user không có email không nhận được mã email
		eu := e.emailUser("email@example.org")
		if _, err := e.r.Users.Mutate(context.Background(), eu.ID, func(x *fhir.User) error { x.Email = ""; return nil }); err != nil {
			t.Fatal(err)
		}
		wantErr(t, e.svc.MFASendEmail(context.Background(), e.freshLogin(e.reload(eu), nil).ID), domain.KindBadRequest, "User has no email address")
	})
}

// hookState cho phép chèn hành vi vào một lời gọi của kho trạng thái (mô phỏng lỗi từng phần hoặc việc xảy ra đồng thời).
type hookState struct {
	application.MFAState
	claimErr   error
	onReserve  func()
	releases   atomic.Int32
	offerAfter func()
}

func (h *hookState) ClaimTOTP(ctx context.Context, u, t string, ttl time.Duration) (bool, error) {
	if h.claimErr != nil {
		return false, h.claimErr
	}
	return h.MFAState.ClaimTOTP(ctx, u, t, ttl)
}

func (h *hookState) ReserveAttempt(ctx context.Context, u, l string, ll, ul int, w time.Duration) (application.AttemptResult, error) {
	if h.onReserve != nil {
		h.onReserve()
	}
	return h.MFAState.ReserveAttempt(ctx, u, l, ll, ul, w)
}

func (h *hookState) ReleaseAttempt(ctx context.Context, u, l string) error {
	h.releases.Add(1)
	return h.MFAState.ReleaseAttempt(ctx, u, l)
}

func (e *env) withHook() (*hookState, *application.Service) {
	h := &hookState{MFAState: e.svc.MFAState}
	d := e.svc.Deps
	d.MFAState, d.MFA = h, nil
	return h, application.NewService(d)
}

// Lỗi của chỉ riêng phần chống dùng lại mã là lỗi hạ tầng (500), KHÔNG phải "mã sai": không cho qua và không đổi thành thông báo thường.
func TestMFAReplayGuardFailureIsAnInfrastructureError(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		h, svc := e.withHook()
		h.claimErr = errors.New("redis down")
		u, secret := e.totpUser("an@example.org")
		l := e.freshLogin(u, nil)
		_, _, err := svc.MFAVerify(context.Background(), l.ID, e.code(secret, 0))
		wantErr(t, err, domain.KindInternal, "")
		if domain.IsTrue(e.getLogin(l.ID).MfaVerified) {
			t.Error("không được xác minh khi không ghi nhận được mã đã dùng")
		}
	})
}

func TestMFAEmailCodeValidityIsCheckedEverywhere(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		setExpiry := func(l *fhir.Login, expires string) {
			if _, err := e.r.Logins.Mutate(ctx, l.ID, func(x *fhir.Login) error { x.EmailMfa.ExpiresAt = expires; return nil }); err != nil {
				t.Fatal(err)
			}
		}
		// enroll email bằng mã ĐÚNG nhưng hết hạn: từ chối
		p := e.project2(strSetting("allowedMfaMethods", "totp,email"))
		u := e.user("an@example.org", nil)
		l := e.freshLogin(u, nil)
		a := e.bearer(u, l, p)
		if err := e.svc.MFASendEmailChallenge(ctx, a); err != nil {
			t.Fatal(err)
		}
		code := e.lastMFACode()
		setExpiry(l, e.clock.Now().Add(-time.Second).UTC().Format("2006-01-02T15:04:05.000Z"))
		wantErr(t, e.svc.MFAEnroll(ctx, a, "email", code), domain.KindBadRequest, "Invalid token")
		if domain.IsTrue(e.reload(u).MfaEnrolled) {
			t.Fatal("mã hết hạn không được đăng ký")
		}
		// hạn hỏng (không đọc được) coi là đã hết hạn, kể cả khi mã đúng
		setExpiry(l, "không phải ngày")
		wantErr(t, e.svc.MFAEnroll(ctx, a, "email", code), domain.KindBadRequest, "Invalid token")
		// đăng nhập: hạn hỏng cũng là "MFA code expired"
		eu := e.emailUser("email@example.org")
		el := e.freshLogin(eu, nil)
		if err := e.svc.MFASendEmail(ctx, el.ID); err != nil {
			t.Fatal(err)
		}
		setExpiry(el, "không phải ngày")
		_, _, err := e.svc.MFAVerify(ctx, el.ID, e.lastMFACode())
		wantErr(t, err, domain.KindBadRequest, "MFA code expired")
	})
}

// Mã xác nhận đăng ký TOTP KHÔNG được là mã email: nếu không, ai có mã email của login (không có TOTP) sẽ thành đăng ký secret mà chưa chứng minh giữ nó.
func TestMFAEnrollmentConfirmationCannotBeSatisfiedByAnEmailCode(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		p := e.project2(setting("mfaRequired", true), strSetting("allowedMfaMethods", "totp,email"))
		u := e.user("an@example.org", func(u *fhir.User) { u.MfaRequired = ptr(true) })
		l := e.freshLogin(u, func(l *fhir.Login) { l.Project = &fhir.Reference{Reference: "Project/" + p.ID} })
		if _, err := e.svc.BuildLoginResult(ctx, l); err != nil { // tạo secret được mời
			t.Fatal(err)
		}
		if err := e.svc.MFASendEmailChallenge(ctx, e.bearer(u, l, p)); err != nil { // login có mã email hợp lệ
			t.Fatal(err)
		}
		emailCode := e.lastMFACode()
		_, _, err := e.svc.MFAVerify(ctx, l.ID, emailCode)
		wantErr(t, err, domain.KindBadRequest, "Invalid MFA token")
		_, _, err = e.svc.MFALoginEnroll(ctx, l.ID, "totp", emailCode)
		wantErr(t, err, domain.KindBadRequest, "Invalid MFA token")
		if got := e.reload(u); domain.IsTrue(got.MfaEnrolled) || got.MfaSecret != "" || domain.IsTrue(e.getLogin(l.ID).MfaVerified) {
			t.Fatalf("mã email không được đăng ký TOTP hay xác minh login: %+v", got)
		}
	})
}

// Project không cho phương thức totp thì secret được mời không bao giờ thành yếu tố qua /verify.
func TestMFAVerifyDoesNotEnrollWhenProjectDisallowsTotp(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		p := e.project2(setting("mfaRequired", true), strSetting("allowedMfaMethods", "email"))
		u := e.user("an@example.org", func(u *fhir.User) { u.MfaRequired = ptr(true) })
		l := e.freshLogin(u, func(l *fhir.Login) { l.Project = &fhir.Reference{Reference: "Project/" + p.ID} })
		res, err := e.svc.BuildLoginResult(ctx, l)
		if err != nil || !res.MfaEnrollRequired {
			t.Fatalf("%+v %v", res, err)
		}
		_, _, err = e.svc.MFAVerify(ctx, l.ID, e.code(secretOf(t, res.EnrollURI), 0))
		wantErr(t, err, domain.KindBadRequest, "User not enrolled in MFA")
		if domain.IsTrue(e.reload(u).MfaEnrolled) {
			t.Fatal("project cấm totp: không được đăng ký TOTP")
		}
	})
}

func TestMFALoginEnrollChecksTheLoginIsActive(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		u := e.user("an@example.org", func(u *fhir.User) { u.MfaRequired = ptr(true) })
		for name, tc := range map[string]struct {
			mut  func(*fhir.Login)
			want string
		}{
			"đã thu hồi":  {func(l *fhir.Login) { l.Revoked = ptr(true) }, "Login revoked"},
			"đã cấp code": {func(l *fhir.Login) { l.Granted = ptr(true) }, "Login granted"},
			"hết hạn": {func(l *fhir.Login) {
				l.AuthTime = e.clock.Now().Add(-11 * time.Minute).UTC().Format("2006-01-02T15:04:05.000Z")
			}, "Login expired"},
			"không phải mật khẩu": {func(l *fhir.Login) { l.AuthMethod = domain.AuthGoogle }, "Invalid login authentication method"},
		} {
			l := e.freshLogin(u, tc.mut)
			_, _, err := e.svc.MFALoginEnroll(ctx, l.ID, "email", "")
			wantErr(t, err, domain.KindBadRequest, tc.want)
			if domain.IsTrue(e.reload(u).MfaEnrolled) {
				t.Fatalf("%s: không được đăng ký", name)
			}
		}
	})
}

// Login bị thu hồi ĐỒNG THỜI (giữa lúc kiểm tra và lúc ghi) không được sống lại bằng việc đánh dấu mfaVerified.
func TestMFAVerifyDoesNotReviveALoginRevokedMeanwhile(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		h, svc := e.withHook()
		u, secret := e.totpUser("an@example.org")
		l := e.freshLogin(u, nil)
		h.onReserve = func() {
			if _, err := e.r.Logins.Mutate(ctx, l.ID, func(x *fhir.Login) error { x.Revoked = ptr(true); return nil }); err != nil {
				t.Error(err)
			}
		}
		_, _, err := svc.MFAVerify(ctx, l.ID, e.code(secret, 0))
		wantErr(t, err, domain.KindBadRequest, "Login revoked")
		if got := e.getLogin(l.ID); !domain.IsTrue(got.Revoked) || domain.IsTrue(got.MfaVerified) {
			t.Errorf("login thu hồi phải giữ nguyên, không mfaVerified: %+v", got)
		}
	})
}

func TestMFASuccessfulEnrollAndDisableReturnTheirAttempt(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		h, svc := e.withHook()
		u := e.user("an@example.org", nil)
		l := e.freshLogin(u, nil)
		a := e.bearer(u, l, nil)
		st, err := svc.MFAStatus(ctx, a)
		if err != nil {
			t.Fatal(err)
		}
		secret := secretOf(t, st.EnrollURI)
		if err := svc.MFAEnroll(ctx, a, "totp", e.code(secret, 0)); err != nil {
			t.Fatal(err)
		}
		if h.releases.Load() != 1 {
			t.Errorf("enroll thành công phải trả lại lượt: %d", h.releases.Load())
		}
		// secret được mời bị xóa sau khi đăng ký
		if v, _ := e.svc.MFAState.OfferGet(ctx, u.ID, l.ID); v != "" {
			t.Error("offer phải bị xóa")
		}
		if err := svc.MFADisable(ctx, a, application.MFADisableRequest{Token: e.nextStepCode(secret)}); err != nil {
			t.Fatal(err)
		}
		if h.releases.Load() != 2 {
			t.Errorf("disable thành công phải trả lại lượt: %d", h.releases.Load())
		}
	})
}

func TestMFADisableWithEmailFactorClearsTheCodeAndOnlyTheCurrentFactorsCount(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		p := e.project2(strSetting("allowedMfaMethods", "totp,email"))
		// 1) gỡ bằng mã email: mã bị xóa khỏi login
		em := e.emailUser("email@example.org")
		l := e.freshLogin(em, nil)
		a := e.bearer(em, l, p)
		if err := e.svc.MFASendEmailChallenge(ctx, a); err != nil {
			t.Fatal(err)
		}
		if err := e.svc.MFADisable(ctx, a, application.MFADisableRequest{Token: e.lastMFACode()}); err != nil {
			t.Fatal(err)
		}
		if e.getLogin(l.ID).EmailMfa != nil {
			t.Error("mã email phải bị xóa sau khi gỡ MFA")
		}
		// 2) user CHỈ có TOTP: mã email hợp lệ của login KHÔNG phải yếu tố (chưa đăng ký email)
		tu, secret := e.totpUser("totp@example.org")
		tl := e.freshLogin(tu, nil)
		ta := e.bearer(tu, tl, p)
		if err := e.svc.MFASendEmailChallenge(ctx, ta); err != nil {
			t.Fatal(err)
		}
		wantErr(t, e.svc.MFADisable(ctx, ta, application.MFADisableRequest{Token: e.lastMFACode()}), domain.KindBadRequest, "Invalid token")
		if !domain.IsTrue(e.reload(tu).MfaEnrolled) {
			t.Fatal("mã email không phải yếu tố của user chỉ có TOTP")
		}
		_ = secret
	})
}

// Mã TOTP đã dùng ở bước khác (ví dụ verify) không dùng lại được để tắt MFA.
func TestMFADisableCannotReuseACodeAlreadyConsumed(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		u, secret := e.totpUser("an@example.org")
		if _, err := e.r.Users.Mutate(ctx, u.ID, func(x *fhir.User) error { x.MfaMethod = []string{"totp", "email"}; return nil }); err != nil {
			t.Fatal(err)
		}
		code := e.code(secret, 0)
		l := e.freshLogin(u, nil)
		if _, _, err := e.svc.MFAVerify(ctx, l.ID, code); err != nil { // tiêu thụ mã
			t.Fatal(err)
		}
		a := e.bearer(u, e.freshLogin(u, nil), e.project2(strSetting("allowedMfaMethods", "totp,email")))
		wantErr(t, e.svc.MFADisable(ctx, a, application.MFADisableRequest{Method: "email", Token: code}), domain.KindBadRequest, "Invalid token")
		if !domain.HasMFAMethod(e.reload(u), "email") {
			t.Error("không được gỡ bằng mã đã dùng")
		}
	})
}

// Service đang có MFA thật mà một phụ thuộc bị gỡ, rồi UseMFA: phải LỖI và quay về fail đóng (không giữ cổng cũ chạy với phụ thuộc thiếu).
func TestMFAUseMFAAfterLosingADependencyFailsClosed(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		svc := application.NewService(e.svc.Deps)
		svc.MFA = nil
		if err := svc.UseMFA(); err != nil {
			t.Fatalf("đủ phụ thuộc: %v", err)
		}
		svc.TOTP = nil
		if err := svc.UseMFA(); err == nil {
			t.Fatal("thiếu phụ thuộc phải lỗi")
		}
		need := e.user("an@example.org", func(u *fhir.User) { u.MfaRequired = ptr(true) })
		if res, err := svc.BuildLoginResult(context.Background(), e.freshLogin(need, nil)); err == nil || (res != nil && res.Code != "") {
			t.Errorf("phải fail đóng: %+v %v", res, err)
		}
	})
}

// S-M4: mã email luôn đúng 6 chữ số (kể cả đệm số 0 đầu), ngẫu nhiên (không cố định) và đa dạng.
func TestMFAEmailCodesAreSixDigitsPaddedAndVaried(t *testing.T) {
	e := newEnv(t, storetest.Backends()[0]) // bộ nhớ: 300 vòng không cần SQLite
	ctx := context.Background()
	u := e.emailUser("email@example.org")
	re := regexp.MustCompile(`^\d{6}$`)
	seen, leadingZero := map[string]bool{}, 0
	for i := 0; i < 300; i++ {
		l := e.freshLogin(u, nil) // mỗi login giới hạn 5 lần gửi, nên dùng login mới
		if err := e.svc.MFASendEmail(ctx, l.ID); err != nil {
			t.Fatal(err)
		}
		c := e.lastMFACode()
		if !re.MatchString(c) {
			t.Fatalf("mã không đúng 6 chữ số: %q", c)
		}
		if c[0] == '0' {
			leadingZero++
		}
		seen[c] = true
	}
	if len(seen) < 250 {
		t.Errorf("mã phải ngẫu nhiên và đa dạng: %d giá trị khác nhau trên 300 lần", len(seen))
	}
	if leadingZero == 0 {
		t.Errorf("sau 300 mã phải có mã bắt đầu bằng 0 (khoảng 10%%): đệm số 0 bị mất")
	}
}
