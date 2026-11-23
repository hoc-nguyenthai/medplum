// Package mailer cài cổng application.Mailer: Log (dev, in thư ra log), None (bỏ qua) và Fake (test ghi lại thư).
package mailer

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"trivita-fhir/apps/auth/internal/application"
	pkgmailer "trivita-fhir/pkg/mailer"
)

// Log in toàn bộ thư ra log, KÈM link chứa secret: chỉ dùng ở local, development, test (config từ chối ở staging/production).
type Log struct{ Logger *slog.Logger }

// Send ghi thư ra log.
func (l Log) Send(ctx context.Context, m application.Message) error {
	lg := l.Logger
	if lg == nil {
		lg = slog.Default()
	}
	lg.InfoContext(ctx, "mail (provider=log, KHÔNG dùng ở production)", "to", m.To, "subject", m.Subject, "text", m.Text)
	return nil
}

// None bỏ qua mọi thư (như Medplum khi chưa cấu hình email); ghi log cảnh báo không kèm nội dung.
type None struct{}

// Send bỏ qua thư.
func (None) Send(ctx context.Context, m application.Message) error {
	slog.WarnContext(ctx, "mail (provider=none): bỏ qua thư", "subject", m.Subject)
	return nil
}

// Fake ghi lại thư để test đối chiếu; Err (nếu đặt) được trả cho mọi lần gửi.
type Fake struct {
	mu   sync.Mutex
	sent []application.Message
	Err  error
}

// Send ghi thư (kể cả khi trả lỗi, để test biết lần gửi đã được thử).
func (f *Fake) Send(_ context.Context, m application.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, m)
	return f.Err
}

// Sent trả bản sao các thư đã gửi.
func (f *Fake) Sent() []application.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]application.Message(nil), f.sent...)
}

// Adapter cắm một pkg/mailer.Mailer (SMTP...) vào cổng application.Mailer của auth. Lỗi trả ra chỉ mang thông điệp an toàn (không có phản hồi của máy chủ SMTP,
// vốn thường lặp lại địa chỉ người nhận): chi tiết đã được adapter thật ghi log (mã SMTP, host, cổng; không có subject hay địa chỉ).
type Adapter struct{ Inner pkgmailer.Mailer }

// Send chuyển thư văn bản thuần sang pkg/mailer. Người gửi do adapter thật chọn (SupportEmail của cấu hình).
func (a Adapter) Send(ctx context.Context, m application.Message) error {
	err := a.Inner.Send(ctx, pkgmailer.Message{To: []pkgmailer.Address{{Email: m.To}}, Subject: m.Subject, Text: m.Text})
	if err == nil {
		return nil
	}
	var e *pkgmailer.Error
	if errors.As(err, &e) {
		return errors.New("gửi thư: " + e.Msg)
	}
	return errors.New("gửi thư thất bại")
}

// LogSender dùng pkg/mailer.Log làm đường gửi của route /email/v1/send ở dev (in thư ra log); luôn "đã cấu hình". Chỉ local/development/test (provider=log bị cấm ở production).
type LogSender struct{ pkgmailer.Log }

// Configured luôn đúng.
func (LogSender) Configured(*pkgmailer.ProjectContext) bool { return true }
