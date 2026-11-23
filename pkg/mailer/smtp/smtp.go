// Package smtp là adapter gửi thư thật của pkg/mailer qua SMTP (chỉ stdlib net/smtp, crypto/tls và pkg/safehttp).
//
// An toàn: TLS là bắt buộc mặc định (TLS ngay từ đầu, hoặc STARTTLS): thư và mật khẩu không bao giờ đi ở dạng rõ trừ khi AllowPlaintext (chỉ dev); SMTP riêng của
// project (cấu hình do người dùng nhập) nối qua safehttp.DialContext nên không tới được địa chỉ nội bộ; có timeout nối, timeout nhàn rỗi và tổng; log không bao giờ chứa
// subject, nội dung hay địa chỉ người nhận (subject có thể chứa mã MFA).
package smtp

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"time"

	"trivita-fhir/pkg/mailer"
	"trivita-fhir/pkg/safehttp"
)

// Config là cấu hình Mailer.
type Config struct {
	// Server là SMTP của server (do quản trị cấu hình, được tin: không chặn địa chỉ nội bộ); nil nếu chỉ dùng SMTP riêng của project.
	Server *mailer.SMTPConfig
	// SupportEmail là người gửi mặc định của SMTP server ("a@b" hoặc "Tên <a@b>"); bắt buộc khi có Server.
	SupportEmail string
	// ApprovedSenders là các địa chỉ trần mà người gọi được đề nghị làm người gửi qua SMTP server (chữ thường).
	ApprovedSenders []string
	// AllowProjectSMTP cho phép dùng mailer.ProjectContext.SMTP; AllowUnsafeOutbound tắt chặn địa chỉ nội bộ cho SMTP của project (chỉ dev/test).
	AllowProjectSMTP, AllowUnsafeOutbound bool
	// AllowPlaintext cho phép nối không mã hóa khi máy chủ không có STARTTLS (chỉ dev). Mặc định false: bắt buộc TLS.
	AllowPlaintext bool
	// ConnectTimeout: nối và nhận lời chào (mặc định 20s); IdleTimeout: im lặng tối đa giữa hai thao tác (mặc định 120s); TotalTimeout: cả lần gửi (mặc định 5 phút).
	ConnectTimeout, IdleTimeout, TotalTimeout time.Duration
	Limits                                    mailer.Limits
	// LocalName là tên trong EHLO (mặc định "localhost").
	LocalName string
	// TLSConfig là cấu hình TLS gốc (ví dụ RootCAs cho test); ServerName được đặt theo host. MinVersion luôn >= TLS 1.2.
	TLSConfig *tls.Config
	Logger    *slog.Logger
	Now       func() time.Time
}

// Mailer cài mailer.Mailer qua SMTP.
type Mailer struct{ cfg Config }

var _ mailer.Mailer = (*Mailer)(nil)

// New kiểm cấu hình và dựng Mailer.
func New(cfg Config) (*Mailer, error) {
	if cfg.Server != nil {
		if cfg.Server.Host == "" || cfg.Server.Port <= 0 || cfg.Server.Port > 65535 {
			return nil, errors.New("smtp: Server cần host và cổng hợp lệ")
		}
		if mailer.ExtractEmail(cfg.SupportEmail) == "" {
			return nil, errors.New("smtp: SupportEmail hợp lệ là bắt buộc khi có SMTP server")
		}
	}
	if cfg.ConnectTimeout <= 0 {
		cfg.ConnectTimeout = 20 * time.Second
	}
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = 120 * time.Second
	}
	if cfg.TotalTimeout <= 0 {
		cfg.TotalTimeout = 5 * time.Minute
	}
	if cfg.LocalName == "" {
		cfg.LocalName = "localhost"
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Mailer{cfg: cfg}, nil
}

// Configured cho biết có đường gửi thật (SMTP server, hoặc SMTP riêng của project khi được phép).
func (m *Mailer) Configured(p *mailer.ProjectContext) bool {
	if m.cfg.Server != nil {
		return true
	}
	return m.cfg.AllowProjectSMTP && p != nil && p.SMTP != nil
}

// route là đường gửi đã chọn.
type route struct {
	smtp     mailer.SMTPConfig
	policy   mailer.FromPolicy
	project  *mailer.ProjectContext // khác nil khi dùng SMTP của project
	hostSafe bool                   // true: chặn địa chỉ nội bộ khi nối
}

func (m *Mailer) choose(p *mailer.ProjectContext) (*route, bool) {
	if m.cfg.AllowProjectSMTP && p != nil && p.SMTP != nil {
		return &route{smtp: p.SMTP.SMTPConfig, policy: mailer.FromPolicy{Approved: p.SMTP.ApprovedSenders, Default: p.SMTP.FromAddress}, project: p, hostSafe: !m.cfg.AllowUnsafeOutbound}, true
	}
	if m.cfg.Server != nil {
		return &route{smtp: *m.cfg.Server, policy: mailer.FromPolicy{Approved: m.cfg.ApprovedSenders, Default: m.cfg.SupportEmail}}, true
	}
	return nil, false
}

// Send gửi thư. Chưa cấu hình đường gửi nào thì ghi cảnh báo (không kèm subject) và trả nil (như Medplum). Lỗi trả ra là *mailer.Error.
func (m *Mailer) Send(ctx context.Context, msg mailer.Message) error {
	r, ok := m.choose(msg.Project)
	if !ok {
		m.cfg.Logger.WarnContext(ctx, "mail: chưa cấu hình SMTP, bỏ qua thư")
		return nil
	}
	if err := msg.Validate(m.cfg.Limits); err != nil {
		return err
	}
	// Người gửi: địa chỉ trần của chính sách tương ứng (danh sách của server không áp cho SMTP project và ngược lại); tên hiển thị appName chỉ khi dùng SMTP project.
	from := mailer.ResolveFrom(msg.From, r.policy)
	header := from
	if r.project != nil {
		header = mailer.ApplyDisplayName(from, r.project.AppName)
	}
	raw, err := mailer.Build(msg, header, m.cfg.Now(), m.cfg.Limits)
	if err != nil {
		return err
	}
	sender, rcpt := mailer.Envelope(msg, from)
	ctx, cancel := context.WithTimeout(ctx, m.cfg.TotalTimeout)
	defer cancel()
	if err := m.deliver(ctx, r, sender, rcpt, raw); err != nil {
		// Phản hồi của máy chủ SMTP thường lặp lại địa chỉ người nhận: chỉ ghi mã, không ghi lời.
		logErr := err
		var te *textproto.Error
		if errors.As(err, &te) {
			logErr = fmt.Errorf("SMTP %d", te.Code)
		}
		attrs := []any{"host", r.smtp.Host, "port", r.smtp.Port, "recipients", len(rcpt), "error", logErr}
		if r.project != nil {
			attrs = append(attrs, "project_id", r.project.ID)
		}
		m.cfg.Logger.ErrorContext(ctx, "mail: gửi thất bại", attrs...)
		if errors.Is(err, safehttp.ErrBlockedAddress) {
			return &mailer.Error{Kind: mailer.KindConfig, Msg: "Project SMTP configuration is incomplete or invalid", Err: err}
		}
		return &mailer.Error{Kind: mailer.KindTransport, Msg: "Failed to send email", Err: err}
	}
	return nil
}

func (m *Mailer) tlsConfig(host string) *tls.Config {
	c := &tls.Config{}
	if m.cfg.TLSConfig != nil {
		c = m.cfg.TLSConfig.Clone()
	}
	c.ServerName = host
	if c.MinVersion < tls.VersionTLS12 {
		c.MinVersion = tls.VersionTLS12
	}
	return c
}

func (m *Mailer) deliver(ctx context.Context, r *route, sender string, rcpt []string, raw []byte) error {
	addr := net.JoinHostPort(r.smtp.Host, strconv.Itoa(r.smtp.Port))
	var conn net.Conn
	var err error
	if r.hostSafe {
		conn, err = safehttp.DialContext(false, m.cfg.ConnectTimeout)(ctx, "tcp", addr)
	} else {
		conn, err = (&net.Dialer{Timeout: m.cfg.ConnectTimeout}).DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return err
	}
	dc := &deadlineConn{Conn: conn, idle: m.cfg.ConnectTimeout, hard: time.Now().Add(m.cfg.TotalTimeout)}
	if d, ok := ctx.Deadline(); ok && d.Before(dc.hard) {
		dc.hard = d
	}
	stop := context.AfterFunc(ctx, func() { _ = dc.Close() })
	defer stop()
	defer dc.Close()

	var c *smtp.Client
	if r.smtp.ImplicitTLS() {
		tc := tls.Client(dc, m.tlsConfig(r.smtp.Host))
		if err := tc.HandshakeContext(ctx); err != nil {
			return fmt.Errorf("TLS: %w", err)
		}
		c, err = smtp.NewClient(tc, r.smtp.Host)
	} else {
		c, err = smtp.NewClient(dc, r.smtp.Host)
	}
	if err != nil {
		return err
	}
	dc.idle = m.cfg.IdleTimeout // đã nhận lời chào
	if err := c.Hello(m.cfg.LocalName); err != nil {
		return err
	}
	if !r.smtp.ImplicitTLS() {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(m.tlsConfig(r.smtp.Host)); err != nil {
				return fmt.Errorf("STARTTLS: %w", err)
			}
		} else if !m.cfg.AllowPlaintext {
			return errors.New("máy chủ SMTP không hỗ trợ TLS (STARTTLS) và cấu hình không cho gửi không mã hóa")
		}
	}
	if r.smtp.Username != "" {
		auth, err := chooseAuth(c, r.smtp, m.cfg.AllowPlaintext)
		if err != nil {
			return err
		}
		if err := c.Auth(auth); err != nil {
			return err
		}
	}
	if err := c.Mail(sender); err != nil {
		return err
	}
	for _, to := range rcpt {
		if err := c.Rcpt(to); err != nil {
			return err
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(raw); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

// deadlineConn gia hạn hạn chót mỗi thao tác theo idle nhưng không vượt hard (tổng).
type deadlineConn struct {
	net.Conn
	idle time.Duration
	hard time.Time
}

func (d *deadlineConn) refresh() {
	t := time.Now().Add(d.idle)
	if t.After(d.hard) {
		t = d.hard
	}
	_ = d.Conn.SetDeadline(t)
}

func (d *deadlineConn) Read(p []byte) (int, error)  { d.refresh(); return d.Conn.Read(p) }
func (d *deadlineConn) Write(p []byte) (int, error) { d.refresh(); return d.Conn.Write(p) }

// ---- xác thực

func chooseAuth(c *smtp.Client, cfg mailer.SMTPConfig, allowPlaintext bool) (smtp.Auth, error) {
	ok, param := c.Extension("AUTH")
	if !ok {
		return nil, errors.New("máy chủ SMTP không hỗ trợ AUTH")
	}
	methods := strings.Fields(strings.ToUpper(param))
	has := func(m string) bool {
		for _, x := range methods {
			if x == m {
				return true
			}
		}
		return false
	}
	switch {
	case has("PLAIN"):
		return &plainAuth{user: cfg.Username, pass: cfg.Password, allowPlaintext: allowPlaintext}, nil
	case has("LOGIN"):
		return &loginAuth{user: cfg.Username, pass: cfg.Password, allowPlaintext: allowPlaintext}, nil
	}
	return nil, errors.New("máy chủ SMTP không hỗ trợ AUTH PLAIN hay LOGIN")
}

var errPlainAuth = errors.New("từ chối gửi mật khẩu trên kết nối không mã hóa")

type plainAuth struct {
	user, pass     string
	allowPlaintext bool
}

func (a *plainAuth) Start(s *smtp.ServerInfo) (string, []byte, error) {
	if !s.TLS && !a.allowPlaintext {
		return "", nil, errPlainAuth
	}
	return "PLAIN", []byte("\x00" + a.user + "\x00" + a.pass), nil
}

func (a *plainAuth) Next(_ []byte, more bool) ([]byte, error) {
	if more {
		return nil, errors.New("AUTH PLAIN: máy chủ gửi thêm thử thách")
	}
	return nil, nil // net/smtp vẫn gọi Next sau phản hồi 235
}

type loginAuth struct {
	user, pass     string
	allowPlaintext bool
}

func (a *loginAuth) Start(s *smtp.ServerInfo) (string, []byte, error) {
	if !s.TLS && !a.allowPlaintext {
		return "", nil, errPlainAuth
	}
	return "LOGIN", nil, nil
}

func (a *loginAuth) Next(challenge []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	switch strings.ToLower(strings.TrimSpace(string(challenge))) {
	case "username:", "user name":
		return []byte(a.user), nil
	case "password:":
		return []byte(a.pass), nil
	}
	return nil, errors.New("AUTH LOGIN: thử thách lạ")
}
