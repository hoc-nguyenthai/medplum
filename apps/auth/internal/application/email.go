package application

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"log/slog"
	"mime"
	"regexp"
	"strings"

	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/mailer"
)

// Route gửi thư POST /email/v1/send (E4, auth/email/routes.ts). Chỉ admin của project có tính năng `email`. Khác Medplum (có chủ ý): thông điệp "not configured" bỏ SES,
// phần tử địa chỉ sai là 400, trường lạ bị bỏ (như Medplum) nhưng không thể ghi vào header (kiểu tĩnh), tệp đính kèm Binary/<id> đọc từ dịch vụ FHIR bằng token của người gọi (E5, cần AUTH_EMAIL_FHIR_URL), tải URL chỉ https
// và không theo redirect, có giới hạn người nhận/kích thước, lỗi SMTP là 502 với thông điệp chung (Q-E9), SMTP riêng của project mặc định TẮT (mật khẩu nằm dạng rõ trong
// Project.secret, Q-E7).

// EmailSender gửi thư và cho biết có đường gửi hay không; *smtp.Mailer cài giao diện này.
type EmailSender interface {
	Send(ctx context.Context, m mailer.Message) error
	Configured(p *mailer.ProjectContext) bool
}

// AttachmentFetcher tải tệp đính kèm theo URL (adapter chặn SSRF). Lỗi trả ra có thông điệp an toàn để trả cho client.
type AttachmentFetcher interface {
	Fetch(ctx context.Context, rawURL string, maxBytes int64) ([]byte, error)
}

// Lỗi của BinarySource (adapter bọc/ trả đúng các giá trị này để use case chọn thông điệp; mọi lỗi khác là lỗi hạ tầng).
var (
	ErrBinaryNotFound  = errors.New("binary not found")
	ErrBinaryForbidden = errors.New("binary access denied")
	ErrBinaryTooLarge  = errors.New("binary too large")
)

// BinarySource đọc nội dung Binary/<id> từ dịch vụ FHIR bằng chính Bearer token của người gọi (nên theo quyền, project và securityContext của
// họ, như Medplum đọc bằng repo của người gọi). Trả nội dung (không quá maxBytes, vượt thì ErrBinaryTooLarge) và Content-Type của Binary.
type BinarySource interface {
	Read(ctx context.Context, bearerToken, id string, maxBytes int64) (content []byte, contentType string, err error)
}

// EmailConfig là cấu hình route gửi thư.
type EmailConfig struct {
	// AllowProjectSMTP cho phép dùng SMTP riêng khai ở Project.secret (mặc định tắt).
	AllowProjectSMTP bool
	// MaxAttachmentBytes là cỡ tối đa của MỖI tệp tải theo URL (mặc định 10 MiB); MaxAttachments là số tệp tối đa (mặc định 20).
	MaxAttachmentBytes int64
	MaxAttachments     int
}

func (c EmailConfig) attachBytes() int64 {
	if c.MaxAttachmentBytes > 0 {
		return c.MaxAttachmentBytes
	}
	return 10 << 20
}

func (c EmailConfig) attachCount() int {
	if c.MaxAttachments > 0 {
		return c.MaxAttachments
	}
	return 20
}

// EmailAttachment là tệp đính kèm theo yêu cầu: nội dung nhúng (Content + Encoding) hoặc URL https (Path).
type EmailAttachment struct {
	Filename, Content, Path, ContentType, Disposition, CID, Encoding string
}

// EmailRequest là yêu cầu gửi thư đã được presentation kiểm kiểu. Message không chứa Attachments và Project (use case tự dựng).
type EmailRequest struct {
	Message     mailer.Message
	Attachments []EmailAttachment
	// BearerToken là token của người gọi, chỉ để chuyển cho BinarySource khi có tệp đính kèm Binary/<id>. Bí mật: không được log hay trả ra.
	BearerToken string
}

const (
	msgEmailFeature   = "Email feature is not enabled for this project"
	msgEmailAdminOnly = "Only project administrators can send emails"
	msgEmailNoSMTP    = "Email is not configured on this server. Set up SMTP."
)

func toMailerSettings(in []fhir.ProjectSetting) []mailer.Setting {
	out := make([]mailer.Setting, len(in))
	for i, s := range in {
		out[i] = mailer.Setting{Name: s.Name, String: s.ValueString, Int: s.ValueInteger, Bool: s.ValueBoolean}
	}
	return out
}

// SendEmail kiểm theo thứ tự của Medplum rồi gửi: tính năng email (400), admin (403), cấu hình SMTP của project (400 nếu dở), có đường gửi (400), tệp đính kèm (400), gửi (502 nếu SMTP lỗi).
func (s *Service) SendEmail(ctx context.Context, a *AuthState, req EmailRequest) error {
	if a == nil || a.Project == nil || a.Membership == nil {
		return domain.Unauthorized("Unauthorized")
	}
	project, err := s.Projects.Get(ctx, a.Project.ID)
	if err != nil {
		return err
	}
	if !domain.ProjectHasFeature(project, "email") {
		return domain.BadRequest(msgEmailFeature)
	}
	if !domain.IsTrue(a.Membership.Admin) {
		return domain.Forbidden(msgEmailAdminOnly)
	}
	pc := &mailer.ProjectContext{ID: project.ID, AppName: mailer.ProjectAppName(toMailerSettings(project.Setting))}
	smtpCfg, err := mailer.ProjectSMTPFromSecrets(toMailerSettings(project.Secret), s.Email.AllowProjectSMTP)
	if err != nil {
		var me *mailer.Error
		if errors.As(err, &me) {
			s.Logger.WarnContext(ctx, "email: cấu hình SMTP của project thiếu hoặc sai", "project_id", project.ID, "fields", me.Fields)
			return domain.BadRequest(me.Msg)
		}
		return err
	}
	pc.SMTP = smtpCfg
	if s.EmailSender == nil || !s.EmailSender.Configured(pc) {
		return domain.BadRequest(msgEmailNoSMTP)
	}
	msg := req.Message
	if len(req.Attachments) > s.Email.attachCount() {
		return domain.BadRequestAt("Too many attachments", "attachments")
	}
	for _, at := range req.Attachments {
		resolved, err := s.resolveAttachment(ctx, req.BearerToken, at)
		if err != nil {
			return err
		}
		msg.Attachments = append(msg.Attachments, resolved)
	}
	msg.Project = pc
	if err := s.EmailSender.Send(ctx, msg); err != nil {
		var me *mailer.Error
		if errors.As(err, &me) {
			switch me.Kind {
			case mailer.KindInvalid, mailer.KindConfig:
				return domain.BadRequest(me.Msg)
			}
		}
		s.Logger.ErrorContext(ctx, "email: gửi thất bại", "project_id", project.ID, "recipients", len(msg.To)+len(msg.Cc)+len(msg.Bcc))
		return domain.BadGateway("Failed to send email")
	}
	s.Logger.InfoContext(ctx, "email: đã gửi", slog.String("project_id", project.ID), slog.Int("recipients", len(msg.To)+len(msg.Cc)+len(msg.Bcc)), slog.Bool("audit", true))
	return nil
}

// resolveAttachment đổi một EmailAttachment thành mailer.Attachment: không có path là nội dung nhúng; path https:// được tải bằng client chặn SSRF; Binary/ chưa hỗ trợ;
// mọi path khác bị từ chối (không bao giờ đọc tệp cục bộ).
func (s *Service) resolveAttachment(ctx context.Context, token string, at EmailAttachment) (mailer.Attachment, error) {
	out := mailer.Attachment{Filename: at.Filename, ContentType: at.ContentType, Disposition: at.Disposition, CID: at.CID}
	if at.Path == "" {
		var data []byte
		switch strings.ToLower(at.Encoding) {
		case "", "utf8", "utf-8":
			data = []byte(at.Content)
		case "base64":
			var err error
			if data, err = base64.StdEncoding.DecodeString(at.Content); err != nil {
				return out, domain.BadRequestAt("Invalid attachment content: not valid base64", "attachments")
			}
		default:
			return out, domain.BadRequestAt("Unsupported attachment encoding", "attachments")
		}
		if int64(len(data)) > s.Email.attachBytes() {
			return out, domain.BadRequestAt("Attachment is too large", "attachments")
		}
		out.Body = strings.NewReader(string(data))
		return out, nil
	}
	lower := strings.ToLower(at.Path)
	switch {
	case strings.HasPrefix(at.Path, "Binary/"):
		return s.binaryAttachment(ctx, token, at, out)
	case strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "http://"):
		if s.EmailAttachments == nil {
			return out, domain.BadRequestAt("Failed to fetch attachment: URL attachments are not enabled", "attachments")
		}
		data, err := s.EmailAttachments.Fetch(ctx, at.Path, s.Email.attachBytes())
		if err != nil {
			return out, domain.BadRequestAt("Failed to fetch attachment: "+err.Error(), "attachments")
		}
		out.Body = strings.NewReader(string(data))
		return out, nil
	}
	return out, domain.BadRequestAt("Invalid email options: File access rejected for "+safePath(at.Path), "attachments")
}

var binaryID = regexp.MustCompile(`^[A-Za-z0-9\-.]{1,64}$`)

// binaryAttachment đọc Binary/<id> (đúng dạng này, không version, không query) bằng quyền của người gọi. Không tồn tại hoặc không có quyền thì thư KHÔNG được gửi.
// Content-Type của Binary được dùng khi yêu cầu không nêu contentType.
func (s *Service) binaryAttachment(ctx context.Context, token string, at EmailAttachment, out mailer.Attachment) (mailer.Attachment, error) {
	id := strings.TrimPrefix(at.Path, "Binary/")
	if s.EmailBinaries == nil {
		return out, domain.BadRequestAt("Binary attachments are not supported", "attachments")
	}
	if !binaryID.MatchString(id) || id == "." || id == ".." {
		return out, domain.BadRequestAt("Invalid email options: File access rejected for "+safePath(at.Path), "attachments")
	}
	data, ct, err := s.EmailBinaries.Read(ctx, token, id, s.Email.attachBytes())
	switch {
	case errors.Is(err, ErrBinaryNotFound):
		return out, domain.BadRequestAt("Attachment not found: Binary/"+id, "attachments")
	case errors.Is(err, ErrBinaryForbidden):
		return out, domain.BadRequestAt("Not authorized to read attachment: Binary/"+id, "attachments")
	case errors.Is(err, ErrBinaryTooLarge):
		return out, domain.BadRequestAt("Attachment is too large", "attachments")
	case err != nil:
		s.Logger.WarnContext(ctx, "email: không đọc được Binary đính kèm", "binary", "Binary/"+id, "error", err.Error())
		return out, domain.BadGateway("Failed to read attachment")
	}
	if out.ContentType == "" {
		if mt, _, perr := mime.ParseMediaType(ct); perr == nil {
			out.ContentType = mt
		}
	}
	out.Body = bytes.NewReader(data)
	return out, nil
}

// safePath rút gọn và bỏ ký tự điều khiển để đưa vào thông điệp lỗi.
func safePath(p string) string {
	if len(p) > 100 {
		p = p[:100]
	}
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return '?'
		}
		return r
	}, p)
}
