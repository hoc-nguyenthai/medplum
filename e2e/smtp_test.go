package e2e

import (
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"trivita-fhir/pkg/mailer/smtptest"
)

// Thư đặt lại mật khẩu đi thật qua SMTP (STARTTLS, AUTH, CA riêng) từ tiến trình auth tới một máy chủ SMTP giả.
func TestAuthSendsMailOverSMTPThroughRealProcess(t *testing.T) {
	srv := smtptest.Start(t, nil)
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, srv.CertPEM(), 0o600); err != nil {
		t.Fatal(err)
	}
	s := newStackCustom(t, false, map[string]string{
		"AUTH_MAIL_PROVIDER": "smtp", "AUTH_SMTP_HOST": "127.0.0.1", "AUTH_SMTP_PORT": strconv.Itoa(srv.Port()), "AUTH_SMTP_USERNAME": "e2e-user", "AUTH_SMTP_PASSWORD": "e2e-secret-pw",
		"AUTH_SMTP_FROM": "Trivita <no-reply@trivita.example.org>", "AUTH_SMTP_CA_FILE": ca,
	}, nil)
	if r := s.postJSON("/auth/resetpassword", map[string]any{"email": "a@example.org"}); r.status != 200 {
		t.Fatalf("resetpassword: %d %s", r.status, r.body)
	}
	snap := srv.Snapshot()
	if len(snap.Mails) != 1 || !snap.Mails[0].TLS || snap.Mails[0].From != "no-reply@trivita.example.org" || snap.Mails[0].To[0] != "a@example.org" {
		t.Fatalf("thư: %+v", snap.Mails)
	}
	if len(snap.Authed) != 1 || snap.Authed[0] != "PLAIN e2e-user:e2e-secret-pw" || !snap.SawTLS[0] {
		t.Errorf("auth: %v %v", snap.Authed, snap.SawTLS)
	}
	if !strings.Contains(strings.ReplaceAll(snap.Mails[0].Data, "=\r\n", ""), "/setpassword/") {
		t.Errorf("thiếu link: %s", snap.Mails[0].Data)
	}
	// email không tồn tại: vẫn 200 và KHÔNG có thư thứ hai (không lộ tài khoản)
	if r := s.postJSON("/auth/resetpassword", map[string]any{"email": "khong-co@example.org"}); r.status != 200 {
		t.Errorf("email lạ: %d", r.status)
	}
	if n := len(srv.Snapshot().Mails); n != 1 {
		t.Errorf("không được gửi thư cho email lạ: %d", n)
	}
}

// POST /email/v1/send trên tiến trình thật: admin project có tính năng email gửi thư qua SMTP; client không phải admin bị 403; không token 401.
func TestEmailRouteThroughRealProcess(t *testing.T) {
	srv := smtptest.Start(t, nil)
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, srv.CertPEM(), 0o600); err != nil {
		t.Fatal(err)
	}
	s := newStackCustom(t, false, map[string]string{
		"AUTH_MAIL_PROVIDER": "smtp", "AUTH_SMTP_HOST": "127.0.0.1", "AUTH_SMTP_PORT": strconv.Itoa(srv.Port()), "AUTH_SMTP_FROM": "Trivita <no-reply@trivita.example.org>",
		"AUTH_SMTP_CA_FILE": ca, "AUTH_EMAIL_ENABLED": "true", "AUTH_DEFAULT_PROJECT_FEATURES": "email",
	}, nil)
	admin := s.signIn("a@example.org", s.clientA)["access_token"].(string)
	body := map[string]any{"to": []string{"to@example.org"}, "cc": "cc@example.org", "subject": "Chào từ e2e", "text": "Xin chào", "attachments": []map[string]any{{"filename": "a.txt", "content": "hello"}}}
	if r := s.bearerJSON("", "/email/v1/send", body); r.status != 401 {
		t.Errorf("không token: %d", r.status)
	}
	r := s.bearerJSON(admin, "/email/v1/send", body)
	if r.status != 200 {
		t.Fatalf("gửi: %d %s", r.status, r.body)
	}
	snap := srv.Snapshot()
	if len(snap.Mails) != 1 || !snap.Mails[0].TLS || strings.Join(snap.Mails[0].To, ",") != "to@example.org,cc@example.org" || snap.Mails[0].From != "no-reply@trivita.example.org" {
		t.Fatalf("thư: %+v", snap.Mails)
	}
	if strings.Contains(snap.Mails[0].Data, "Bcc") {
		t.Error("không có header Bcc")
	}
	// header injection bị từ chối, không có thư thứ hai
	if r := s.bearerJSON(admin, "/email/v1/send", map[string]any{"to": "to@example.org", "subject": "a\r\nBcc: v@y.org"}); r.status != 400 {
		t.Errorf("CRLF: %d %s", r.status, r.body)
	}
	// client credentials (membership không admin): 403
	tk := s.clientCredentials(s.clientA, s.secretA, "")
	if r := s.bearerJSON(tk, "/email/v1/send", body); r.status != 403 {
		t.Errorf("không admin: %d %s", r.status, r.body)
	}
	if n := len(srv.Snapshot().Mails); n != 1 {
		t.Errorf("chỉ một thư: %d", n)
	}
}

// E5 trên tiến trình thật: auth đọc Binary/<id> từ fhir-server (kho bật) bằng chính token của admin; Binary của project khác là 404 nên không gửi thư.
func TestEmailBinaryAttachmentThroughRealProcesses(t *testing.T) {
	srv := smtptest.Start(t, nil)
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, srv.CertPEM(), 0o600); err != nil {
		t.Fatal(err)
	}
	s := newStackFull(t, false, map[string]string{
		"AUTH_MAIL_PROVIDER": "smtp", "AUTH_SMTP_HOST": "127.0.0.1", "AUTH_SMTP_PORT": strconv.Itoa(srv.Port()), "AUTH_SMTP_FROM": "Trivita <no-reply@trivita.example.org>",
		"AUTH_SMTP_CA_FILE": ca, "AUTH_EMAIL_ENABLED": "true", "AUTH_DEFAULT_PROJECT_FEATURES": "email", "AUTH_EMAIL_FHIR_URL": "{FHIR}/fhir/R4",
	}, map[string]string{"FHIR_BINARY_STORAGE": "file:{DIR}/blobs"}, nil)
	adminA := s.signIn("a@example.org", s.clientA)["access_token"].(string)
	adminB := s.signIn("b@example.org", s.clientB)["access_token"].(string)

	up := do(t, "POST", s.fhir+"/fhir/R4/Binary", adminA, "text/plain", strings.NewReader("Nội dung từ kho fhir-server"))
	if up.status != 201 {
		t.Fatalf("tải Binary: %d %s", up.status, up.body)
	}
	id := up.json(t)["id"].(string)
	send := func(token, path string) resp {
		return s.bearerJSON(token, "/email/v1/send", map[string]any{"to": "to@example.org", "subject": "Có tệp", "text": "x", "attachments": []map[string]any{{"filename": "kho.txt", "path": path}}})
	}

	if r := send(adminA, "Binary/"+id); r.status != 200 {
		t.Fatalf("đính kèm Binary: %d %s", r.status, r.body)
	}
	mails := srv.Snapshot().Mails
	if len(mails) != 1 {
		t.Fatalf("%d thư", len(mails))
	}
	data := mails[0].Data
	if !strings.Contains(data, "kho.txt") || !strings.Contains(data, "text/plain") {
		t.Errorf("thiếu tên/loại tệp: %s", data)
	}
	if got := strings.Join(decodedParts(t, data), "|"); !strings.Contains(got, "Nội dung từ kho fhir-server") {
		t.Errorf("nội dung tệp không có trong thư: %q", got)
	}

	// project khác: fhir-server trả 404, không gửi
	if r := send(adminB, "Binary/"+id); r.status != 400 || !strings.Contains(string(r.body), "Attachment not found") {
		t.Errorf("Binary của project khác: %d %s", r.status, r.body)
	}
	// không tồn tại, và không token
	if r := send(adminA, "Binary/khong-co"); r.status != 400 {
		t.Errorf("không tồn tại: %d", r.status)
	}
	if r := send("", "Binary/"+id); r.status != 401 {
		t.Errorf("không token: %d", r.status)
	}
	if n := len(srv.Snapshot().Mails); n != 1 {
		t.Errorf("chỉ thư hợp lệ được gửi: %d", n)
	}
}

// decodedParts giải mã mọi phần base64 của thư (tệp đính kèm).
func decodedParts(t *testing.T, data string) []string {
	t.Helper()
	msg, err := mail.ReadMessage(strings.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	mt, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	var walk func(ct string, p map[string]string, r io.Reader)
	walk = func(ct string, p map[string]string, r io.Reader) {
		if strings.HasPrefix(ct, "multipart/") {
			mr := multipart.NewReader(r, p["boundary"])
			for {
				part, err := mr.NextPart()
				if err != nil {
					return
				}
				pct, pp, _ := mime.ParseMediaType(part.Header.Get("Content-Type"))
				walk(pct, pp, part)
			}
		}
		raw, _ := io.ReadAll(r)
		if d, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(string(raw), "\r\n", "")); err == nil {
			out = append(out, string(d))
		}
	}
	walk(mt, params, msg.Body)
	return out
}
