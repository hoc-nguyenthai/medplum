package bootstrap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"trivita-fhir/apps/auth/internal/config"
	"trivita-fhir/pkg/mailer/smtptest"
)

func smtpSettings(srv *smtptest.Server, caPath string) func(*config.Settings) {
	return func(s *config.Settings) {
		s.MailProvider, s.SMTPHost, s.SMTPPort = "smtp", "127.0.0.1", srv.Port()
		s.SMTPUsername, s.SMTPPassword, s.SMTPFrom, s.SMTPCAFile = "smtp-user", "smtp-secret-pw", "Trivita <no-reply@trivita.example.org>", caPath
		s.SMTPTimeout = 2 * time.Second
	}
}

func writeCA(t *testing.T, srv *smtptest.Server) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(p, srv.CertPEM(), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// Đặt lại mật khẩu gửi thư thật qua SMTP (STARTTLS, AUTH, người gửi lấy từ cấu hình) tới máy chủ giả.
func TestHTTPResetPasswordDeliversMailOverSMTP(t *testing.T) {
	srv := smtptest.Start(t, nil)
	a := newCodeAPI(t, smtpSettings(srv, writeCA(t, srv)))
	if r := a.do("POST", "/auth/resetpassword", `{"email":"admin@example.org"}`, nil); r.status != 200 {
		t.Fatalf("resetpassword: %d %s", r.status, r.body)
	}
	snap := srv.Snapshot()
	if len(snap.Mails) != 1 {
		t.Fatalf("số thư: %d", len(snap.Mails))
	}
	m := snap.Mails[0]
	if !m.TLS || m.From != "no-reply@trivita.example.org" || len(m.To) != 1 || m.To[0] != "admin@example.org" {
		t.Errorf("phong bì: %+v", m)
	}
	if !strings.Contains(m.Data, "Subject: Trivita Password Reset") || !strings.Contains(m.Data, "From: \"Trivita\" <no-reply@trivita.example.org>") && !strings.Contains(m.Data, "From: Trivita <no-reply@trivita.example.org>") {
		t.Errorf("header: %s", m.Data)
	}
	if len(snap.Authed) != 1 || snap.Authed[0] != "PLAIN smtp-user:smtp-secret-pw" || len(snap.SawTLS) != 1 || !snap.SawTLS[0] {
		t.Errorf("auth phải đi sau TLS: %v %v", snap.Authed, snap.SawTLS)
	}
	// nội dung mã hóa quoted-printable: link đặt mật khẩu có mặt sau khi giải mã dòng gấp
	flat := strings.ReplaceAll(strings.ReplaceAll(m.Data, "=\r\n", ""), "=3D", "=")
	if !strings.Contains(flat, "/setpassword/") {
		t.Errorf("thiếu link đặt mật khẩu: %s", m.Data)
	}
}

// Gửi lỗi (máy chủ từ chối người nhận) không làm hỏng yêu cầu công khai và log không lộ mật khẩu SMTP.
func TestHTTPResetPasswordSurvivesSMTPFailure(t *testing.T) {
	srv := smtptest.Start(t, func(s *smtptest.Server) { s.RejectTo = "admin@example.org" })
	a := newCodeAPI(t, smtpSettings(srv, writeCA(t, srv)))
	if r := a.do("POST", "/auth/resetpassword", `{"email":"admin@example.org"}`, nil); r.status != 200 {
		t.Fatalf("lỗi gửi thư không được làm hỏng yêu cầu: %d %s", r.status, r.body)
	}
	if n := len(srv.Snapshot().Mails); n != 0 {
		t.Errorf("không có thư nào được nhận: %d", n)
	}
	// không bao giờ ghi nhận mật khẩu SMTP vào bất kỳ nơi nào của Settings in ra
}

func TestNewSMTPFromSettings(t *testing.T) {
	srv := smtptest.Start(t, nil)
	good := config.Settings{SMTPHost: "127.0.0.1", SMTPPort: srv.Port(), SMTPFrom: "a@x.org", SMTPTimeout: time.Second, BaseURL: "https://auth.example.org"}
	if _, err := newSMTP(good); err != nil {
		t.Fatal(err)
	}
	if got := smtpLocalName(good); got != "auth.example.org" {
		t.Errorf("EHLO: %q", got)
	}
	if got := smtpLocalName(config.Settings{}); got != "localhost" {
		t.Errorf("EHLO mặc định: %q", got)
	}
	bad := map[string]func(*config.Settings){
		"CA không tồn tại": func(s *config.Settings) { s.SMTPCAFile = filepath.Join(t.TempDir(), "khong-co.pem") },
		"CA không phải PEM": func(s *config.Settings) {
			p := filepath.Join(t.TempDir(), "rac.pem")
			_ = os.WriteFile(p, []byte("không phải pem"), 0o600)
			s.SMTPCAFile = p
		},
		"secure sai": func(s *config.Settings) { s.SMTPSecure = "co" },
		"from sai":   func(s *config.Settings) { s.SMTPFrom = "khong-phai" },
		"port sai":   func(s *config.Settings) { s.SMTPPort = 0 },
	}
	for name, mut := range bad {
		s := good
		mut(&s)
		if _, err := newSMTP(s); err == nil {
			t.Errorf("%s phải lỗi", name)
		}
	}
	// secure=true chọn TLS ngay từ đầu: máy chủ chỉ có STARTTLS nên thư không đi được
	s := good
	s.SMTPSecure = "true"
	if _, err := newSMTP(s); err != nil {
		t.Errorf("secure true: %v", err)
	}
}
