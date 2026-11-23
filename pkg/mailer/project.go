package mailer

import "strings"

// Setting là một mục Project.secret hoặc Project.setting; gói không import pkg/fhir nên adapter của app chuyển từ fhir.ProjectSetting.
type Setting struct {
	Name   string
	String string
	Int    *int
	Bool   *bool
}

func find(ss []Setting, name string) *Setting {
	for i := range ss {
		if ss[i].Name == name {
			return &ss[i]
		}
	}
	return nil
}

// Thông điệp lỗi cấu hình SMTP của project (giữ nguyên chuỗi của Medplum).
const projectSMTPInvalid = "Project SMTP configuration is incomplete or invalid"

// ProjectSMTPFromSecrets đọc cấu hình SMTP riêng của project từ Project.secret (smtpHost, smtpPort, smtpUsername, smtpPassword, smtpFromAddress, smtpSecure,
// smtpApprovedSenders). Trả (nil, nil) khi allow=false hoặc không có smtpHost (dùng cấu hình server). Có host mà thiếu hoặc không hợp lệ cổng, username, password,
// fromAddress thì trả *Error KindConfig, trong Fields chỉ có TÊN trường thiếu (không bao giờ có giá trị).
func ProjectSMTPFromSecrets(secrets []Setting, allow bool) (*ProjectSMTP, error) {
	if !allow {
		return nil, nil
	}
	host := find(secrets, "smtpHost")
	if host == nil || strings.TrimSpace(host.String) == "" {
		return nil, nil
	}
	get := func(name string) string {
		if s := find(secrets, name); s != nil {
			return strings.TrimSpace(s.String)
		}
		return ""
	}
	port := 0
	if s := find(secrets, "smtpPort"); s != nil && s.Int != nil {
		port = *s.Int
	}
	cfg := SMTPConfig{Host: strings.TrimSpace(host.String), Port: port, Username: get("smtpUsername")}
	if s := find(secrets, "smtpPassword"); s != nil {
		cfg.Password = s.String
	}
	var missing []string
	if port <= 0 || port > 65535 {
		missing = append(missing, "smtpPort")
	}
	if cfg.Username == "" {
		missing = append(missing, "smtpUsername")
	}
	if cfg.Password == "" {
		missing = append(missing, "smtpPassword")
	}
	from := get("smtpFromAddress")
	if from == "" || ExtractEmail(from) == "" {
		missing = append(missing, "smtpFromAddress")
	}
	if len(missing) > 0 {
		return nil, &Error{Kind: KindConfig, Msg: projectSMTPInvalid, Fields: missing}
	}
	if s := find(secrets, "smtpSecure"); s != nil && s.Bool != nil {
		v := *s.Bool
		cfg.Secure = &v
	}
	return &ProjectSMTP{SMTPConfig: cfg, FromAddress: from, ApprovedSenders: SplitApproved(get("smtpApprovedSenders"))}, nil
}

// ProjectAppName trả Project.setting[appName] đã cắt khoảng trắng; rỗng nếu chưa đặt hoặc chứa CR/LF.
func ProjectAppName(settings []Setting) string {
	s := find(settings, "appName")
	if s == nil {
		return ""
	}
	n := strings.TrimSpace(s.String)
	if CheckHeaderValue(n) != nil {
		return ""
	}
	return n
}

// Configured cho biết có đường gửi thật: SMTP của server hoặc SMTP riêng của project (khác Medplum chỉ tính server, Q-E5).
func Configured(server *SMTPConfig, p *ProjectContext) bool {
	return (server != nil && server.Host != "") || (p != nil && p.SMTP != nil)
}
