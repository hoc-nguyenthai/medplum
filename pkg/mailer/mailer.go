// Package mailer là cổng gửi thư dùng chung (auth: đặt lại mật khẩu, xác minh, MFA; sau này route /email/v1/send) cùng các hàm thuần để chọn người gửi, tên hiển thị,
// đọc cấu hình SMTP theo project và dựng MIME. Gói gốc chỉ dùng stdlib và không gọi mạng; adapter thật nằm ở gói con smtp.
//
// Nguyên tắc an toàn: mọi giá trị đi vào header bị từ chối nếu chứa CR, LF hay NUL (chống chèn header); không bao giờ ghi subject hay nội dung ra log của
// adapter thật (subject có thể chứa mã MFA); người gửi do người gọi đề nghị chỉ được dùng khi nằm trong danh sách được duyệt.
package mailer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/mail"
	"regexp"
	"strings"
	"sync"
)

// Address là một địa chỉ thư; Email bắt buộc.
type Address struct{ Name, Email string }

// String trả dạng RFC 5322 (tên được mã hóa khi cần). Rỗng nếu Email rỗng.
func (a Address) String() string {
	if a.Email == "" {
		return ""
	}
	return (&mail.Address{Name: a.Name, Address: a.Email}).String()
}

// ParseAddress đọc một địa chỉ ("a@b.org" hoặc "Tên <a@b.org>"); từ chối chuỗi chứa CR, LF, NUL hoặc nhiều địa chỉ.
func ParseAddress(s string) (Address, error) {
	if err := CheckHeaderValue(s); err != nil {
		return Address{}, err
	}
	a, err := mail.ParseAddress(strings.TrimSpace(s))
	if err != nil {
		return Address{}, &Error{Kind: KindInvalid, Msg: "Invalid email address"}
	}
	return Address{Name: a.Name, Email: a.Address}, nil
}

var emptyListItem = regexp.MustCompile(`(^|,)\s*(,|$)`)

// ParseAddressList đọc danh sách địa chỉ phân cách bằng dấu phẩy ("a@x.org, Tên <b@x.org>"); từ chối chuỗi rỗng, chứa CR, LF, NUL hoặc phần tử không hợp lệ.
func ParseAddressList(s string) ([]Address, error) {
	if err := CheckHeaderValue(s); err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(s)
	// net/mail chấp nhận phần tử rỗng (obs-addr-list): "a@x.org," hay "a@x.org,,b@x.org"; ở đây coi là sai.
	if emptyListItem.MatchString(trimmed) {
		return nil, &Error{Kind: KindInvalid, Msg: "Invalid email address"}
	}
	list, err := mail.ParseAddressList(trimmed)
	if err != nil || len(list) == 0 {
		return nil, &Error{Kind: KindInvalid, Msg: "Invalid email address"}
	}
	out := make([]Address, len(list))
	for i, a := range list {
		out[i] = Address{Name: a.Name, Email: a.Address}
	}
	return out, nil
}

// ExtractEmail trả phần địa chỉ trần ("Tên <a@b.org>" thành "a@b.org"), rỗng nếu không đọc được. Dùng net/mail nên chuỗi như `"<a@x.com>" <evil@y.com>` cho
// ra evil@y.com (địa chỉ thật sẽ nhận thư), không bị đánh lừa bởi tên hiển thị.
func ExtractEmail(s string) string {
	a, err := ParseAddress(s)
	if err != nil {
		return ""
	}
	return a.Email
}

// CheckHeaderValue từ chối giá trị chứa CR, LF hoặc NUL.
func CheckHeaderValue(s string) error {
	if strings.ContainsAny(s, "\r\n\x00") {
		return &Error{Kind: KindInvalid, Msg: "Header values must not contain line breaks"}
	}
	return nil
}

// Attachment là tệp đính kèm; Body đã qua giới hạn byte của người gọi (Build vẫn áp giới hạn tổng).
type Attachment struct {
	Filename, ContentType, Disposition, CID string
	Body                                    io.Reader
}

// ICalEvent là sự kiện lịch gửi kèm (phần text/calendar).
type ICalEvent struct{ Method, Filename, Content string }

// ProjectContext là thông tin theo project mà người gọi đã tra sẵn; nil là dùng cấu hình server.
type ProjectContext struct {
	ID string
	// AppName đã TrimSpace; rỗng là chưa white-label. Chỉ gắn vào tên hiển thị khi dùng SMTP riêng của project.
	AppName string
	// SMTP nil: project chưa cấu hình SMTP riêng.
	SMTP *ProjectSMTP
}

// SMTPConfig là cấu hình nối SMTP. Secure nil là suy ra từ cổng (465 là TLS ngay từ đầu).
type SMTPConfig struct {
	Host               string
	Port               int
	Username, Password string
	Secure             *bool
}

// ImplicitTLS cho biết nối TLS ngay từ đầu (cổng 465 hoặc Secure=true) thay vì STARTTLS.
func (c SMTPConfig) ImplicitTLS() bool {
	if c.Secure != nil {
		return *c.Secure
	}
	return c.Port == 465
}

// ProjectSMTP là SMTP riêng của project.
type ProjectSMTP struct {
	SMTPConfig
	// FromAddress bắt buộc: người gửi mặc định (và phong bì).
	FromAddress string
	// ApprovedSenders đã cắt khoảng trắng, chữ thường.
	ApprovedSenders []string
}

// Message là một thư.
type Message struct {
	// From chỉ là đề nghị: người gửi cuối cùng do ResolveFrom chọn.
	From                 *Address
	To, Cc, Bcc, ReplyTo []Address
	Subject, Text, HTML  string
	Attachments          []Attachment
	ICal                 *ICalEvent
	Project              *ProjectContext
}

// Mailer gửi thư. Lỗi trả ra là *Error (người gọi dùng Kind để ánh xạ mã trạng thái).
type Mailer interface {
	Send(ctx context.Context, m Message) error
}

// ErrorKind phân loại lỗi để lớp HTTP ánh xạ: Invalid và Config là 400, Transport là 5xx.
type ErrorKind int

// Các loại lỗi.
const (
	KindInvalid   ErrorKind = iota + 1 // yêu cầu sai (địa chỉ, header, quá giới hạn)
	KindConfig                         // cấu hình thiếu hoặc sai
	KindTransport                      // không gửi được (mạng, máy chủ SMTP)
)

// Error là lỗi có phân loại. Msg an toàn để trả cho client; Err là nguyên nhân (chỉ để ghi log); Fields là tên các trường cấu hình thiếu (không có giá trị).
type Error struct {
	Kind   ErrorKind
	Msg    string
	Err    error
	Fields []string
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Msg, e.Err)
	}
	return e.Msg
}

// Unwrap trả nguyên nhân.
func (e *Error) Unwrap() error { return e.Err }

// IsKind cho biết err là *Error loại k.
func IsKind(err error, k ErrorKind) bool {
	var e *Error
	return errors.As(err, &e) && e.Kind == k
}

// Limits là giới hạn mỗi thư (Q-E8).
type Limits struct {
	// MaxRecipients là tổng To, Cc, Bcc (mặc định 50).
	MaxRecipients int
	// MaxBytes là cỡ tối đa của thư đã dựng (mặc định 10 MiB).
	MaxBytes int64
}

func (l Limits) recipients() int {
	if l.MaxRecipients > 0 {
		return l.MaxRecipients
	}
	return 50
}

func (l Limits) bytes() int64 {
	if l.MaxBytes > 0 {
		return l.MaxBytes
	}
	return 10 << 20
}

// Validate kiểm thư trước khi dựng: có người nhận, địa chỉ và header sạch, không vượt số người nhận.
func (m Message) Validate(l Limits) error {
	if len(m.To) == 0 {
		return &Error{Kind: KindInvalid, Msg: "To is required"}
	}
	if n := len(m.To) + len(m.Cc) + len(m.Bcc); n > l.recipients() {
		return &Error{Kind: KindInvalid, Msg: fmt.Sprintf("Too many recipients (max %d)", l.recipients())}
	}
	for _, list := range [][]Address{m.To, m.Cc, m.Bcc, m.ReplyTo} {
		for _, a := range list {
			if err := checkAddress(a); err != nil {
				return err
			}
		}
	}
	if m.From != nil && m.From.Email != "" {
		if err := checkAddress(*m.From); err != nil {
			return err
		}
	}
	if strings.TrimSpace(m.Subject) == "" {
		return &Error{Kind: KindInvalid, Msg: "Subject is required"}
	}
	if err := CheckHeaderValue(m.Subject); err != nil {
		return err
	}
	for _, a := range m.Attachments {
		for _, v := range []string{a.Filename, a.ContentType, a.Disposition, a.CID} {
			if err := CheckHeaderValue(v); err != nil {
				return err
			}
		}
	}
	if m.ICal != nil {
		if err := CheckHeaderValue(m.ICal.Method); err != nil {
			return err
		}
		if err := CheckHeaderValue(m.ICal.Filename); err != nil {
			return err
		}
	}
	return nil
}

func checkAddress(a Address) error {
	if err := CheckHeaderValue(a.Name); err != nil {
		return err
	}
	if err := CheckHeaderValue(a.Email); err != nil {
		return err
	}
	if _, err := mail.ParseAddress("<" + a.Email + ">"); err != nil || strings.ContainsAny(a.Email, " ,;<>\"") {
		return &Error{Kind: KindInvalid, Msg: "Invalid email address"}
	}
	return nil
}

// ---- adapter không cần mạng

// Nop bỏ qua mọi thư (như Medplum khi chưa cấu hình email); chỉ ghi cảnh báo, không kèm subject.
type Nop struct{ Logger *slog.Logger }

// Send bỏ qua thư.
func (n Nop) Send(ctx context.Context, _ Message) error {
	lg := n.Logger
	if lg == nil {
		lg = slog.Default()
	}
	lg.WarnContext(ctx, "mail (provider=none): bỏ qua thư")
	return nil
}

// Log in thư ra log (cả subject và nội dung, có thể kèm link chứa secret): CHỈ dùng ở local/development/test.
type Log struct{ Logger *slog.Logger }

// Send ghi thư ra log.
func (l Log) Send(ctx context.Context, m Message) error {
	lg := l.Logger
	if lg == nil {
		lg = slog.Default()
	}
	var to []string
	for _, a := range m.To {
		to = append(to, a.Email)
	}
	lg.InfoContext(ctx, "mail (provider=log, KHÔNG dùng ở production)", "to", to, "subject", m.Subject, "text", m.Text)
	return nil
}

// Fake ghi lại thư để test đối chiếu; Err (nếu đặt) được trả cho mọi lần gửi.
type Fake struct {
	mu   sync.Mutex
	sent []Message
	Err  error
}

// Send ghi thư (kể cả khi trả lỗi, để test biết lần gửi đã được thử).
func (f *Fake) Send(_ context.Context, m Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, m)
	return f.Err
}

// Sent trả bản sao danh sách thư đã gửi.
func (f *Fake) Sent() []Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Message(nil), f.sent...)
}
