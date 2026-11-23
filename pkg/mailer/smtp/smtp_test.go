package smtp

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log/slog"
	netsmtp "net/smtp"
	"strings"
	"testing"
	"time"
	"trivita-fhir/pkg/mailer/smtptest"

	"trivita-fhir/pkg/mailer"
	"trivita-fhir/pkg/safehttp"
)

func cfgOf(s *smtptest.Server, mut func(*Config)) Config {
	c := Config{Server: &mailer.SMTPConfig{Host: "127.0.0.1", Port: s.Port(), Username: "user", Password: "pa:ss w0rd"}, SupportEmail: "Hỗ trợ <support@corp.example.org>",
		ApprovedSenders: []string{"ok@corp.example.org"}, TLSConfig: &tls.Config{RootCAs: s.Pool}, ConnectTimeout: 2 * time.Second, IdleTimeout: 2 * time.Second, TotalTimeout: 5 * time.Second,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: func() time.Time { return time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC) }}
	if mut != nil {
		mut(&c)
	}
	return c
}

func mustNew(t *testing.T, c Config) *Mailer {
	t.Helper()
	m, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func basicMsg() mailer.Message {
	return mailer.Message{To: []mailer.Address{{Email: "to@x.org"}}, Bcc: []mailer.Address{{Email: "bcc@x.org"}}, Subject: "Mã xác thực 123456", Text: "Nội dung"}
}

func TestSendStartTLS(t *testing.T) {
	srv := smtptest.Start(t, nil)
	if err := mustNew(t, cfgOf(srv, nil)).Send(context.Background(), basicMsg()); err != nil {
		t.Fatal(err)
	}
	_, mails, authed, _ := snap(srv)
	if len(mails) != 1 || !mails[0].TLS || mails[0].From != "support@corp.example.org" || strings.Join(mails[0].To, ",") != "to@x.org,bcc@x.org" {
		t.Fatalf("thư: %+v", mails)
	}
	if strings.Contains(mails[0].Data, "bcc@x.org") || !strings.Contains(mails[0].Data, "From: =?utf-8?q?H=E1=BB=97_tr=E1=BB=A3?= <support@corp.example.org>") {
		t.Errorf("header: %s", mails[0].Data)
	}
	if len(authed) != 1 || authed[0] != "PLAIN user:pa:ss w0rd" {
		t.Errorf("auth: %v", authed)
	}
	if func() bool { s := srv.Snapshot(); return len(s.SawTLS) != 1 || !s.SawTLS[0] }() {
		t.Error("AUTH phải đi sau TLS")
	}
}

func TestSendImplicitTLS(t *testing.T) {
	srv := smtptest.Start(t, func(s *smtptest.Server) { s.Implicit, s.StartTLS = true, false })
	c := cfgOf(srv, func(c *Config) { c.Server.Secure = ptrBool(true) })
	if err := mustNew(t, c).Send(context.Background(), basicMsg()); err != nil {
		t.Fatal(err)
	}
	if _, mails, _, _ := snap(srv); len(mails) != 1 || !mails[0].TLS {
		t.Fatalf("%+v", mails)
	}
}

func ptrBool(b bool) *bool { return &b }

func TestTLSIsRequiredByDefault(t *testing.T) {
	srv := smtptest.Start(t, func(s *smtptest.Server) { s.StartTLS = false })
	err := mustNew(t, cfgOf(srv, nil)).Send(context.Background(), basicMsg())
	if !mailer.IsKind(err, mailer.KindTransport) {
		t.Fatalf("phải lỗi truyền tải: %v", err)
	}
	_, mails, authed, cmds := snap(srv)
	if len(mails) != 0 || len(authed) != 0 {
		t.Errorf("không được gửi thư hay mật khẩu khi thiếu TLS: %v %v", mails, authed)
	}
	for _, c := range cmds {
		if c == "AUTH" || c == "MAIL" || c == "DATA" {
			t.Errorf("không được tới lệnh %s", c)
		}
	}
	// AllowPlaintext (dev): gửi được, và dùng AUTH LOGIN khi chỉ có LOGIN
	srv2 := smtptest.Start(t, func(s *smtptest.Server) { s.StartTLS, s.Auths = false, "LOGIN" })
	if err := mustNew(t, cfgOf(srv2, func(c *Config) { c.AllowPlaintext = true })).Send(context.Background(), basicMsg()); err != nil {
		t.Fatal(err)
	}
	_, mails, authed, _ = snap(srv2)
	if len(mails) != 1 || mails[0].TLS || len(authed) != 1 || authed[0] != "LOGIN user:pa:ss w0rd" {
		t.Errorf("plaintext: %+v %v", mails, authed)
	}
}

func TestAuthRefusedWithoutTLSEvenIfMailWouldPass(t *testing.T) {
	// máy chủ không có STARTTLS, cấu hình KHÔNG cho plaintext: không lộ mật khẩu. (Đã kiểm ở trên); ở đây kiểm các nhánh AUTH.
	srv := smtptest.Start(t, func(s *smtptest.Server) { s.Auths = "CRAM-MD5" })
	err := mustNew(t, cfgOf(srv, nil)).Send(context.Background(), basicMsg())
	if !mailer.IsKind(err, mailer.KindTransport) {
		t.Fatalf("AUTH không hỗ trợ: %v", err)
	}
	srv2 := smtptest.Start(t, func(s *smtptest.Server) { s.Auths = "" })
	if err := mustNew(t, cfgOf(srv2, nil)).Send(context.Background(), basicMsg()); !mailer.IsKind(err, mailer.KindTransport) {
		t.Fatalf("không có AUTH: %v", err)
	}
	// không có username: bỏ qua AUTH
	srv3 := smtptest.Start(t, func(s *smtptest.Server) { s.Auths = "" })
	if err := mustNew(t, cfgOf(srv3, func(c *Config) { c.Server.Username, c.Server.Password = "", "" })).Send(context.Background(), basicMsg()); err != nil {
		t.Fatal(err)
	}
	if _, mails, authed, _ := snap(srv3); len(mails) != 1 || len(authed) != 0 {
		t.Errorf("không auth: %+v %v", mails, authed)
	}
}

func TestSenderSelection(t *testing.T) {
	srv := smtptest.Start(t, nil)
	m := mustNew(t, cfgOf(srv, nil))
	msg := basicMsg()
	// người gọi đề nghị địa chỉ được duyệt: dùng (kèm tên hiển thị người gọi đặt)
	msg.From = &mailer.Address{Name: "Người Gửi", Email: "OK@corp.example.org"}
	if err := m.Send(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	// không được duyệt: về mặc định
	msg.From = &mailer.Address{Email: "ceo@corp.example.org"}
	if err := m.Send(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	_, mails, _, _ := snap(srv)
	if mails[0].From != "OK@corp.example.org" || !strings.Contains(mails[0].Data, "<OK@corp.example.org>") || mails[1].From != "support@corp.example.org" {
		t.Errorf("người gửi: %q %q", mails[0].From, mails[1].From)
	}
	// SMTP server KHÔNG gắn appName vào tên hiển thị (chống mạo danh): dù Project có AppName
	msg.From = nil
	msg.Project = &mailer.ProjectContext{ID: "p1", AppName: "Phòng khám"}
	if err := m.Send(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	_, mails, _, _ = snap(srv)
	if strings.Contains(mails[2].Data, "Ph=C3=B2ng") || strings.Contains(mails[2].Data, "Phòng khám") {
		t.Errorf("không được gắn appName ở SMTP server: %s", mails[2].Data)
	}
}

func TestProjectSMTPRouting(t *testing.T) {
	srvServer := smtptest.Start(t, nil)
	srvProject := smtptest.Start(t, nil)
	proj := &mailer.ProjectContext{ID: "p1", AppName: "Phòng khám", SMTP: &mailer.ProjectSMTP{
		SMTPConfig:  mailer.SMTPConfig{Host: "127.0.0.1", Port: srvProject.Port(), Username: "pu", Password: "pp"},
		FromAddress: "noreply@project.example.org", ApprovedSenders: []string{"team@project.example.org"}}}
	cfg := cfgOf(srvServer, func(c *Config) { c.AllowProjectSMTP, c.AllowUnsafeOutbound = true, true })
	m := mustNew(t, cfg)
	msg := basicMsg()
	msg.Project = proj
	if err := m.Send(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	if c, _, _, _ := snap(srvServer); c != 0 {
		t.Errorf("SMTP server không được dùng khi project có SMTP riêng: %d kết nối", c)
	}
	_, mails, authed, _ := snap(srvProject)
	if len(mails) != 1 || mails[0].From != "noreply@project.example.org" || len(authed) != 1 || authed[0] != "PLAIN pu:pp" {
		t.Fatalf("project: %+v %v", mails, authed)
	}
	// tên hiển thị appName chỉ ở SMTP project; phong bì vẫn là địa chỉ trần
	if !strings.Contains(mails[0].Data, "From: =?utf-8?q?Ph=C3=B2ng_kh=C3=A1m?= <noreply@project.example.org>") {
		t.Errorf("From: %s", mails[0].Data)
	}
	// danh sách của SERVER không áp cho SMTP project, và ngược lại
	msg.From = &mailer.Address{Email: "ok@corp.example.org"}
	if err := m.Send(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	msg.From = &mailer.Address{Email: "team@project.example.org"}
	if err := m.Send(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	_, mails, _, _ = snap(srvProject)
	if mails[1].From != "noreply@project.example.org" || mails[2].From != "team@project.example.org" {
		t.Errorf("chọn người gửi theo chính sách project: %q %q", mails[1].From, mails[2].From)
	}
	// AllowProjectSMTP=false: dùng SMTP server
	off := mustNew(t, cfgOf(srvServer, nil))
	msg.From = nil
	if err := off.Send(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	if c, mails, _, _ := snap(srvServer); c != 1 || len(mails) != 1 {
		t.Errorf("tắt SMTP project: %d %d", c, len(mails))
	}
	// project có SMTP, server không cấu hình (Q-E5): vẫn gửi được
	only := mustNew(t, cfgOf(srvServer, func(c *Config) { c.Server, c.AllowProjectSMTP, c.AllowUnsafeOutbound = nil, true, true }))
	if err := only.Send(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	if !only.Configured(proj) || only.Configured(nil) || !off.Configured(nil) {
		t.Error("Configured")
	}
}

func TestProjectSMTPCannotReachInternalAddresses(t *testing.T) {
	srvProject := smtptest.Start(t, nil)
	proj := &mailer.ProjectContext{ID: "p1", SMTP: &mailer.ProjectSMTP{SMTPConfig: mailer.SMTPConfig{Host: "127.0.0.1", Port: srvProject.Port(), Username: "u", Password: "p"}, FromAddress: "n@p.org"}}
	msg := basicMsg()
	msg.Project = proj
	m := mustNew(t, Config{AllowProjectSMTP: true, TLSConfig: &tls.Config{RootCAs: srvProject.Pool}, ConnectTimeout: time.Second, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	err := m.Send(context.Background(), msg)
	var e *mailer.Error
	if !errors.As(err, &e) || e.Kind != mailer.KindConfig || e.Msg != "Project SMTP configuration is incomplete or invalid" || !errors.Is(err, safehttp.ErrBlockedAddress) {
		t.Fatalf("phải bị chặn: %v", err)
	}
	if c, _, _, _ := snap(srvProject); c != 0 {
		t.Errorf("máy chủ nội bộ không được bị nối: %d", c)
	}
	for _, host := range []string{"localhost", "10.0.0.5", "169.254.169.254", "::1"} {
		p := *proj
		sm := *proj.SMTP
		sm.Host = host
		p.SMTP = &sm
		msg.Project = &p
		if err := m.Send(context.Background(), msg); !mailer.IsKind(err, mailer.KindConfig) {
			t.Errorf("%s: %v", host, err)
		}
	}
	// SMTP của server là cấu hình quản trị (được tin): nối được tới địa chỉ nội bộ
	srv := smtptest.Start(t, nil)
	if err := mustNew(t, cfgOf(srv, nil)).Send(context.Background(), basicMsg()); err != nil {
		t.Errorf("server nội bộ: %v", err)
	}
}

func TestNotConfiguredSkips(t *testing.T) {
	var buf bytes.Buffer
	m := mustNew(t, Config{Logger: slog.New(slog.NewTextHandler(&buf, nil))})
	if err := m.Send(context.Background(), basicMsg()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "chưa cấu hình") || strings.Contains(buf.String(), "123456") || m.Configured(nil) {
		t.Errorf("log: %q", buf.String())
	}
}

func TestFailuresAreClassifiedAndLogsAreClean(t *testing.T) {
	var buf bytes.Buffer
	srv := smtptest.Start(t, func(s *smtptest.Server) { s.RejectTo = "bad@x.org" })
	m := mustNew(t, cfgOf(srv, func(c *Config) { c.Logger = slog.New(slog.NewTextHandler(&buf, nil)) }))
	msg := basicMsg()
	msg.To = []mailer.Address{{Email: "bad@x.org"}}
	err := m.Send(context.Background(), msg)
	var e *mailer.Error
	if !errors.As(err, &e) || e.Kind != mailer.KindTransport || e.Msg != "Failed to send email" {
		t.Fatalf("RCPT bị từ chối: %v", err)
	}
	if strings.Contains(e.Msg, "550") {
		t.Error("Msg trả cho client không được lộ phản hồi của máy chủ")
	}
	out := buf.String()
	for _, secret := range []string{"123456", "Nội dung", "bad@x.org", "pa:ss w0rd", "no such user", "user:"} {
		if strings.Contains(out, secret) {
			t.Errorf("log chứa %q: %s", secret, out)
		}
	}
	if !strings.Contains(out, "host=127.0.0.1") || !strings.Contains(out, "SMTP 550") {
		t.Errorf("log phải có host: %s", out)
	}
	// header injection / dữ liệu sai: Invalid, không nối mạng
	srv2 := smtptest.Start(t, nil)
	m2 := mustNew(t, cfgOf(srv2, nil))
	for name, mut := range map[string]func(*mailer.Message){
		"subject CRLF": func(m *mailer.Message) { m.Subject = "a\r\nBcc: v@y.org" },
		"to CRLF":      func(m *mailer.Message) { m.To = []mailer.Address{{Email: "a@x.org\r\nRCPT TO:<v@y.org>"}} },
		"không to":     func(m *mailer.Message) { m.To = nil },
		"from CRLF":    func(m *mailer.Message) { m.From = &mailer.Address{Email: "a@x.org\nX: y"} },
	} {
		mm := basicMsg()
		mut(&mm)
		if err := m2.Send(context.Background(), mm); !mailer.IsKind(err, mailer.KindInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if c, _, _, _ := snap(srv2); c != 0 {
		t.Errorf("thư sai không được tới mạng: %d kết nối", c)
	}
}

func TestLimitsAndDotStuffing(t *testing.T) {
	srv := smtptest.Start(t, nil)
	m := mustNew(t, cfgOf(srv, func(c *Config) { c.Limits = mailer.Limits{MaxRecipients: 2, MaxBytes: 4000} }))
	msg := basicMsg()
	msg.To = []mailer.Address{{Email: "a@x.org"}, {Email: "b@x.org"}}
	if err := m.Send(context.Background(), msg); !mailer.IsKind(err, mailer.KindInvalid) { // 2 To + 1 Bcc = 3 > 2
		t.Errorf("quá nhiều người nhận: %v", err)
	}
	msg = basicMsg()
	msg.Bcc = nil
	msg.Text = strings.Repeat("y", 5000)
	if err := m.Send(context.Background(), msg); !mailer.IsKind(err, mailer.KindInvalid) {
		t.Errorf("quá lớn: %v", err)
	}
	// dòng bắt đầu bằng dấu chấm không làm kết thúc DATA sớm hay bị mất
	msg = basicMsg()
	msg.Text = "đầu\n.chấm đầu dòng\n.\nsau dấu chấm đơn"
	if err := m.Send(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	_, mails, _, _ := snap(srv)
	if len(mails) != 1 || !strings.Contains(mails[0].Data, ".ch=C3=A1=\r\np") && !strings.Contains(mails[0].Data, "sau d=E1=BA=A5u") {
		t.Errorf("dot-stuffing: %q", mails[0].Data)
	}
	if !strings.Contains(mails[0].Data, "sau d") {
		t.Errorf("phần sau dấu chấm đơn bị mất: %q", mails[0].Data)
	}
}

func TestTimeoutsAndCancellation(t *testing.T) {
	// máy chủ chấp nhận nhưng không chào: hết ConnectTimeout
	slow := smtptest.Start(t, func(s *smtptest.Server) { s.Greet = 5 * time.Second })
	m := mustNew(t, cfgOf(slow, func(c *Config) { c.ConnectTimeout = 200 * time.Millisecond }))
	start := time.Now()
	err := m.Send(context.Background(), basicMsg())
	if !mailer.IsKind(err, mailer.KindTransport) || time.Since(start) > 3*time.Second {
		t.Fatalf("phải hết giờ nhanh: %v sau %v", err, time.Since(start))
	}
	// context hủy giữa chừng
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(150 * time.Millisecond); cancel() }()
	start = time.Now()
	m2 := mustNew(t, cfgOf(slow, func(c *Config) { c.ConnectTimeout = 4 * time.Second; c.IdleTimeout = 4 * time.Second }))
	if err := m2.Send(ctx, basicMsg()); !mailer.IsKind(err, mailer.KindTransport) || time.Since(start) > 3*time.Second {
		t.Errorf("hủy: %v sau %v", err, time.Since(start))
	}
	// tổng thời gian
	m3 := mustNew(t, cfgOf(slow, func(c *Config) {
		c.ConnectTimeout = 4 * time.Second
		c.IdleTimeout = 4 * time.Second
		c.TotalTimeout = 200 * time.Millisecond
	}))
	start = time.Now()
	if err := m3.Send(context.Background(), basicMsg()); err == nil || time.Since(start) > 3*time.Second {
		t.Errorf("tổng: %v sau %v", err, time.Since(start))
	}
	// không nối được
	closed := smtptest.Start(t, nil)
	cfg := cfgOf(closed, nil)
	closed.Close()
	if err := mustNew(t, cfg).Send(context.Background(), basicMsg()); !mailer.IsKind(err, mailer.KindTransport) {
		t.Errorf("không nối được: %v", err)
	}
}

func TestNewValidatesConfig(t *testing.T) {
	good := &mailer.SMTPConfig{Host: "h", Port: 587}
	for name, c := range map[string]Config{
		"thiếu host":      {Server: &mailer.SMTPConfig{Port: 587}, SupportEmail: "a@x.org"},
		"cổng sai":        {Server: &mailer.SMTPConfig{Host: "h", Port: 0}, SupportEmail: "a@x.org"},
		"cổng quá lớn":    {Server: &mailer.SMTPConfig{Host: "h", Port: 70000}, SupportEmail: "a@x.org"},
		"thiếu support":   {Server: good},
		"support hỏng":    {Server: good, SupportEmail: "khong-phai"},
		"support có CRLF": {Server: good, SupportEmail: "a@x.org\r\nBcc: v@y.org"},
	} {
		if _, err := New(c); err == nil {
			t.Errorf("%s phải lỗi", name)
		}
	}
	if _, err := New(Config{Server: good, SupportEmail: "Tên <a@x.org>"}); err != nil {
		t.Error(err)
	}
	if _, err := New(Config{}); err != nil {
		t.Errorf("không server (chỉ SMTP project): %v", err)
	}
	// TLS tối thiểu 1.2 dù TLSConfig yêu cầu thấp hơn
	m := mustNew(t, Config{Server: good, SupportEmail: "a@x.org", TLSConfig: &tls.Config{MinVersion: tls.VersionTLS10}})
	if m.tlsConfig("h").MinVersion < tls.VersionTLS12 || m.tlsConfig("h").ServerName != "h" {
		t.Error("TLS tối thiểu 1.2")
	}
}

// snap gom trạng thái của máy chủ giả theo dạng (số kết nối, thư, thông tin AUTH, các lệnh).
func snap(s *smtptest.Server) (int, []smtptest.Received, []string, []string) {
	x := s.Snapshot()
	return x.Conns, x.Mails, x.Authed, x.Cmds
}

// Không có username: không có mật khẩu để bảo vệ, nhưng NỘI DUNG thư vẫn không được đi ở dạng rõ.
func TestMailBodyNeverGoesInClearByDefault(t *testing.T) {
	srv := smtptest.Start(t, func(s *smtptest.Server) { s.StartTLS, s.Auths = false, "" })
	err := mustNew(t, cfgOf(srv, func(c *Config) { c.Server.Username, c.Server.Password = "", "" })).Send(context.Background(), basicMsg())
	if !mailer.IsKind(err, mailer.KindTransport) {
		t.Fatalf("phải lỗi: %v", err)
	}
	if _, mails, _, cmds := snap(srv); len(mails) != 0 {
		t.Errorf("thư đã đi ở dạng rõ: %v (lệnh %v)", mails, cmds)
	}
	if err := mustNew(t, cfgOf(srv, func(c *Config) { c.Server.Username, c.Server.Password, c.AllowPlaintext = "", "", true })).Send(context.Background(), basicMsg()); err != nil {
		t.Errorf("dev plaintext: %v", err)
	}
}

func TestAuthMechanismsRefuseClearConnections(t *testing.T) {
	clear := &netsmtp.ServerInfo{Name: "h", TLS: false}
	secure := &netsmtp.ServerInfo{Name: "h", TLS: true}
	for name, mk := range map[string]func(bool) netsmtp.Auth{
		"PLAIN": func(allow bool) netsmtp.Auth { return &plainAuth{user: "u", pass: "p", allowPlaintext: allow} },
		"LOGIN": func(allow bool) netsmtp.Auth { return &loginAuth{user: "u", pass: "p", allowPlaintext: allow} },
	} {
		if _, _, err := mk(false).Start(clear); err == nil {
			t.Errorf("%s: phải từ chối gửi mật khẩu trên kết nối rõ", name)
		}
		if _, _, err := mk(false).Start(secure); err != nil {
			t.Errorf("%s trên TLS: %v", name, err)
		}
		if _, _, err := mk(true).Start(clear); err != nil {
			t.Errorf("%s dev plaintext: %v", name, err)
		}
	}
	// thử thách lạ
	la := &loginAuth{user: "u", pass: "p"}
	if _, err := la.Next([]byte("Something:"), true); err == nil {
		t.Error("thử thách LOGIN lạ phải lỗi")
	}
	if resp, err := (&plainAuth{}).Next(nil, true); err == nil || resp != nil {
		t.Error("PLAIN không được nhận thêm thử thách")
	}
}

// ConnectTimeout chỉ áp cho nối và lời chào; sau đó là IdleTimeout (lệnh chậm hơn ConnectTimeout vẫn được phép).
func TestIdleTimeoutAppliesAfterGreeting(t *testing.T) {
	srv := smtptest.Start(t, func(s *smtptest.Server) { s.CmdDelay = 400 * time.Millisecond })
	m := mustNew(t, cfgOf(srv, func(c *Config) {
		c.ConnectTimeout, c.IdleTimeout, c.TotalTimeout = 150*time.Millisecond, 3*time.Second, 20*time.Second
	}))
	if err := m.Send(context.Background(), basicMsg()); err != nil {
		t.Fatalf("lệnh chậm hơn ConnectTimeout nhưng trong IdleTimeout phải thành công: %v", err)
	}
	// ngược lại IdleTimeout ngắn thì lỗi
	m2 := mustNew(t, cfgOf(srv, func(c *Config) {
		c.ConnectTimeout, c.IdleTimeout, c.TotalTimeout = time.Second, 150*time.Millisecond, 20*time.Second
	}))
	if err := m2.Send(context.Background(), basicMsg()); !mailer.IsKind(err, mailer.KindTransport) {
		t.Errorf("IdleTimeout ngắn phải lỗi: %v", err)
	}
}
