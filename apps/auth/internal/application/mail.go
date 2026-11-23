package application

import (
	"context"
	"strings"

	"trivita-fhir/pkg/fhir"
)

// Message là một thư văn bản thuần.
type Message struct {
	To      string
	Subject string
	Text    string
}

// Mailer gửi thư. Lỗi gửi do người gọi quyết định xử lý (reset: nuốt và ghi log, verify: ghi log).
type Mailer interface {
	Send(ctx context.Context, m Message) error
}

// MailConfig là phần cấu hình dựng thư.
type MailConfig struct {
	// AppBaseURL là gốc URL ứng dụng web, nơi người dùng bấm link; rỗng thì KHÔNG dựng link và không gửi thư.
	AppBaseURL string
	// AppName là tên hiển thị trong thư (Q1: mặc định Trivita).
	AppName string
	// SupportEmail là địa chỉ hỗ trợ trong thư chào (địa chỉ thuần, không kèm tên hiển thị); rỗng thì bỏ dòng này.
	SupportEmail string
}

func (c MailConfig) appName() string {
	if c.AppName == "" {
		return "Trivita"
	}
	return c.AppName
}

// link nối gốc ứng dụng với đường dẫn, đúng một dấu / ở giữa.
func (c MailConfig) link(path string) string {
	return strings.TrimRight(c.AppBaseURL, "/") + "/" + strings.TrimLeft(path, "/")
}

func (s *Service) mailReady() bool { return s.Mailer != nil && s.Mail.AppBaseURL != "" }

func verifyEmailMessage(c MailConfig, to, url string) Message {
	n := c.appName()
	return Message{To: to, Subject: n + " Email Verification", Text: "We received a request to create a " + n + " account using this email address.\n\n" +
		"Please click the following link to verify your email address:\n\n" + url + "\n\n" +
		"If you received this in error, you can safely ignore it.\n\nThank you,\n" + n + "\n"}
}

func resetPasswordMessage(c MailConfig, to, url string) Message {
	n := c.appName()
	return Message{To: to, Subject: n + " Password Reset", Text: "Someone requested to reset your " + n + " password.\n\n" +
		"Please click on the following link:\n\n" + url + "\n\n" +
		"If you received this in error, you can safely ignore it.\n\nThank you,\n" + n + "\n"}
}

// welcomeMessage là thư chào cho chủ project lần đầu (auth/welcomeemail.ts). Nội dung quảng bá riêng của Medplum (tài liệu, cộng đồng,
// agent AI) không mang sang: chỉ giữ lời chào, tên project, link đăng nhập, link mời đồng đội và địa chỉ hỗ trợ.
// projectName không bao giờ rỗng (NewProject kiểm 4 đến 72 ký tự), nên không cần tên thay thế như "your project" của Medplum.
func welcomeMessage(c MailConfig, user *fhir.User, projectName string) Message {
	n := c.appName()
	greeting := "Hi,"
	if user.FirstName != "" {
		greeting = "Hi " + user.FirstName + ","
	}
	text := greeting + "\n\n" +
		"Welcome to " + n + "! Your new project \"" + projectName + "\" is ready to go.\n\n" +
		"Here are a few things to help you get started:\n\n" +
		"- Sign in to your project: " + c.link("signin") + "\n" +
		"- Invite your teammates from the Admin panel: " + c.link("admin/users") + "\n\n"
	if c.SupportEmail != "" {
		text += "If you have any questions, just reply to this email or reach out to us at\n" + c.SupportEmail + ".\n\n"
	}
	text += "Thank you,\nThe " + n + " Team\n"
	return Message{To: user.Email, Subject: "Welcome to " + n, Text: text}
}

// inviteMessage là thư mời vào project. needsPassword: user mới, link dẫn tới trang đặt mật khẩu; ngược lại là thông báo đã được thêm vào project.
func inviteMessage(c MailConfig, to, projectName, url string, needsPassword bool) Message {
	n := c.appName()
	if needsPassword {
		return Message{To: to, Subject: "Welcome to " + n, Text: "You've been invited to the project " + projectName + " on " + n + ".\n\n" +
			"Please click the following link to set your password:\n\n" + url + "\n\n" +
			"If you received this in error, you can safely ignore it.\n\nThank you,\n" + n + "\n"}
	}
	return Message{To: to, Subject: "You've been added to " + projectName, Text: "You've been added to the project " + projectName + " on " + n + ".\n\n" +
		"You can sign in here:\n\n" + url + "\n\nThank you,\n" + n + "\n"}
}
