package bootstrap

import (
	"context"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"strings"
	"testing"

	"trivita-fhir/apps/auth/internal/config"
	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/mailer/smtptest"
)

// emailAPI dựng auth với route gửi thư bật, SMTP server là máy chủ giả (STARTTLS), project có tính năng email; trả kèm Bearer admin.
func emailAPI(t *testing.T, mut func(*config.Settings)) (*codeAPI, *smtptest.Server, map[string]string) {
	t.Helper()
	srv := smtptest.Start(t, nil)
	ca := writeCA(t, srv)
	a := newCodeAPI(t, func(s *config.Settings) {
		smtpSettings(srv, ca)(s)
		s.EmailEnabled = true
		s.SMTPApprovedSenders = "approved@trivita.example.org"
		if mut != nil {
			mut(s)
		}
	})
	if _, err := a.c.Auth.Projects.Mutate(context.Background(), a.seed.Project.ID, func(p *fhir.Project) error {
		p.Features = append(p.Features, "email")
		p.Setting = append(p.Setting, fhir.ProjectSetting{Name: "appName", ValueString: "Phòng khám"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return a, srv, adminBearer(a)
}

func jsonHdr(h map[string]string) map[string]string {
	out := map[string]string{"Content-Type": "application/json"}
	for k, v := range h {
		out[k] = v
	}
	return out
}

func parseMail(t *testing.T, data string) *mail.Message {
	t.Helper()
	m, err := mail.ReadMessage(strings.NewReader(data))
	if err != nil {
		t.Fatalf("thư không đọc được: %v\n%s", err, data)
	}
	return m
}

func issueTexts(t *testing.T, r resp) []string {
	t.Helper()
	var out []string
	for _, i := range issues(r) {
		out = append(out, i["details"].(map[string]any)["text"].(string))
	}
	return out
}

func TestHTTPEmailRouteDisabledByDefault(t *testing.T) {
	a := newCodeAPI(t, nil)
	if r := a.do("POST", "/email/v1/send", `{}`, jsonHdr(adminBearer(a))); r.status != 404 {
		t.Errorf("tắt thì 404: %d", r.status)
	}
}

func TestHTTPEmailAuthAndOrderOfChecks(t *testing.T) {
	a, srv, hdr := emailAPI(t, nil)
	body := `{"to":"to@x.org","subject":"S","text":"T"}`
	// chưa xác thực: 401 trước cả validator
	if r := a.do("POST", "/email/v1/send", `{}`, map[string]string{"Content-Type": "application/json"}); r.status != 401 {
		t.Errorf("không Bearer: %d", r.status)
	}
	// sai content type
	if r := a.do("POST", "/email/v1/send", body, map[string]string{"Authorization": hdr["Authorization"], "Content-Type": "text/plain"}); r.status != 400 {
		t.Errorf("content-type: %d %s", r.status, r.body)
	}
	// validator chạy trước kiểm quyền/tính năng: gộp mọi lỗi
	r := a.do("POST", "/email/v1/send", `{"subject":5,"text":1,"html":[],"attachments":"x","icalEvent":3}`, jsonHdr(hdr))
	want := map[string]bool{"To is required": true, "Subject must be a string": true, "Text must be a string": true, "HTML must be a string": true,
		"Attachments must be an array": true, "icalEvent must be a string or an object with string content": true}
	got := issueTexts(t, r)
	if r.status != 400 || len(got) != len(want) {
		t.Fatalf("gộp lỗi: %d %v", r.status, got)
	}
	for _, g := range got {
		if !want[g] {
			t.Errorf("issue lạ: %q", g)
		}
	}
	// tính năng email tắt: 400; không admin: 403 (client mặc định không phải admin)
	cc := a.form("/oauth2/token", map[string][]string{"grant_type": {"client_credentials"}, "client_id": {a.seed.Client.ID}, "client_secret": {a.seed.Client.Secret}}, nil)
	ccHdr := jsonHdr(map[string]string{"Authorization": "Bearer " + cc.json()["access_token"].(string)})
	if r := a.do("POST", "/email/v1/send", body, ccHdr); r.status != 403 || issueTexts(t, r)[0] != "Only project administrators can send emails" {
		t.Errorf("không admin: %d %s", r.status, r.body)
	}
	if _, err := a.c.Auth.Projects.Mutate(context.Background(), a.seed.Project.ID, func(p *fhir.Project) error { p.Features = nil; return nil }); err != nil {
		t.Fatal(err)
	}
	if r := a.do("POST", "/email/v1/send", body, jsonHdr(hdr)); r.status != 400 || issueTexts(t, r)[0] != "Email feature is not enabled for this project" {
		t.Errorf("thiếu tính năng: %d %s", r.status, r.body)
	}
	if n := len(srv.Snapshot().Mails); n != 0 {
		t.Errorf("không có thư nào được gửi trong các ca lỗi: %d", n)
	}
}

func TestHTTPEmailNotConfigured(t *testing.T) {
	a := newCodeAPI(t, func(s *config.Settings) { s.EmailEnabled = true }) // provider mặc định là log ở test: đổi sang none
	a.c.Auth.EmailSender = nil
	if _, err := a.c.Auth.Projects.Mutate(context.Background(), a.seed.Project.ID, func(p *fhir.Project) error { p.Features = append(p.Features, "email"); return nil }); err != nil {
		t.Fatal(err)
	}
	r := a.do("POST", "/email/v1/send", `{"to":"to@x.org","subject":"S"}`, jsonHdr(adminBearer(a)))
	if r.status != 400 || issueTexts(t, r)[0] != "Email is not configured on this server. Set up SMTP." {
		t.Errorf("chưa cấu hình: %d %s", r.status, r.body)
	}
}

func TestHTTPEmailSendsOverSMTPWithAllSupportedOptions(t *testing.T) {
	a, srv, hdr := emailAPI(t, nil)
	body := `{"to":["a@x.org",{"name":"Bob Trần","address":"bob@x.org"}],"cc":"c@x.org, d@x.org","bcc":["secret-bcc@x.org"],"replyTo":{"address":"reply@x.org"},
	 "subject":"Chào — Mã 123456","text":"Xin chào","html":"<img src=\"cid:logo\">",
	 "attachments":[{"filename":"hello.txt","content":"hello"},{"filename":"logo.png","content":"aGVsbG8=","encoding":"base64","contentType":"image/png","cid":"logo"}],
	 "icalEvent":{"method":"request","filename":"invite.ics","content":"BEGIN:VCALENDAR\r\nEND:VCALENDAR"},
	 "envelope":{"from":"other@example.com","to":"other@example.com"},"raw":"From: x","headers":{"X-Test":"1"},"dkim":{"domainName":"x"},"sender":"evil@x.org","disableFileAccess":false,"__proto__":{"polluted":true}}`
	r := a.do("POST", "/email/v1/send", body, jsonHdr(hdr))
	if r.status != 200 {
		t.Fatalf("gửi: %d %s", r.status, r.body)
	}
	snap := srv.Snapshot()
	if len(snap.Mails) != 1 {
		t.Fatalf("số thư: %d", len(snap.Mails))
	}
	m := snap.Mails[0]
	if !m.TLS || m.From != "no-reply@trivita.example.org" || strings.Join(m.To, ",") != "a@x.org,bob@x.org,c@x.org,d@x.org,secret-bcc@x.org" {
		t.Errorf("phong bì: %q %v", m.From, m.To)
	}
	if strings.Contains(m.Data, "other@example.com") || strings.Contains(m.Data, "X-Test") || strings.Contains(m.Data, "evil@x.org") || strings.Contains(m.Data, "secret-bcc") || strings.Contains(m.Data, "polluted") {
		t.Errorf("trường không được hỗ trợ lọt vào thư: %s", m.Data)
	}
	msg := parseMail(t, m.Data)
	if f := msg.Header["From"]; len(f) != 1 {
		t.Errorf("chỉ một header From: %v", f)
	}
	if cc, _ := msg.Header.AddressList("Cc"); len(cc) != 2 {
		t.Errorf("cc: %v", cc)
	}
	// SMTP server không gắn appName vào tên hiển thị
	if strings.Contains(m.Data, "Ph=C3=B2ng") || strings.Contains(m.Data, "Phòng khám") {
		t.Errorf("appName không được gắn ở SMTP server: %s", m.Data)
	}
	// cấu trúc: có phần ics và tệp đính kèm giải mã đúng
	mt, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil || mt != "multipart/mixed" {
		t.Fatalf("content-type: %s %v", mt, err)
	}
	var types, bodies []string
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
		types = append(types, ct)
		bodies = append(bodies, string(raw))
	}
	walk(mt, params, msg.Body)
	joined := strings.Join(types, ",")
	for _, w := range []string{"text/plain", "text/html", "text/calendar", "image/png"} {
		if !strings.Contains(joined, w) {
			t.Errorf("thiếu phần %s: %s", w, joined)
		}
	}
	hasHello := false
	for _, b := range bodies {
		if d, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(b, "\r\n", "")); err == nil && string(d) == "hello" {
			hasHello = true
		}
	}
	if !hasHello {
		t.Errorf("tệp hello.txt không giải mã được: %v", bodies)
	}
}

func TestHTTPEmailFromSelectionAndInjection(t *testing.T) {
	a, srv, hdr := emailAPI(t, nil)
	send := func(body string) resp { return a.do("POST", "/email/v1/send", body, jsonHdr(hdr)) }
	// được duyệt (kèm khoảng trắng/hoa thường): giữ nguyên chuỗi người gọi đặt
	if r := send(`{"to":"to@x.org","subject":"S","from":"Tên Khác <APPROVED@trivita.example.org>"}`); r.status != 200 {
		t.Fatalf("approved: %d %s", r.status, r.body)
	}
	// không được duyệt: về mặc định, kể cả khi cố lách bằng tên hiển thị
	for _, from := range []string{`evil@y.org`, `\"<approved@trivita.example.org>\" <evil@y.org>`, `approved@trivita.example.org.evil.com`} {
		if r := send(`{"to":"to@x.org","subject":"S","from":"` + from + `"}`); r.status != 200 {
			t.Fatalf("%s: %d %s", from, r.status, r.body)
		}
	}
	mails := srv.Snapshot().Mails
	if len(mails) != 4 || mails[0].From != "APPROVED@trivita.example.org" {
		t.Fatalf("thư: %+v", mails)
	}
	for _, m := range mails[1:] {
		if m.From != "no-reply@trivita.example.org" {
			t.Errorf("người gửi phải là mặc định: %q", m.From)
		}
	}
	n := len(srv.Snapshot().Mails)
	// chèn header / dữ liệu sai: 400 và không có thư mới
	for name, b := range map[string]string{
		"subject CRLF":     `{"to":"to@x.org","subject":"a\r\nBcc: v@y.org"}`,
		"to CRLF":          `{"to":"to@x.org\r\nBcc: v@y.org","subject":"S"}`,
		"to object CRLF":   `{"to":{"name":"A\r\nBcc: v@y.org","address":"to@x.org"},"subject":"S"}`,
		"cc phần tử null":  `{"to":"to@x.org","cc":["c@x.org",null],"subject":"S"}`,
		"bcc rỗng {}":      `{"to":"to@x.org","bcc":[{}],"subject":"S"}`,
		"to sai kiểu":      `{"to":5,"subject":"S"}`,
		"to []":            `{"to":[],"subject":"S"}`,
		"address có tên":   `{"to":{"address":"Tên <a@x.org>"},"subject":"S"}`,
		"name sai kiểu":    `{"to":{"name":5,"address":"a@x.org"},"subject":"S"}`,
		"address thiếu":    `{"to":{"name":"A"},"subject":"S"}`,
		"to rỗng":          `{"to":"","subject":"S"}`,
		"from nhiều":       `{"to":"to@x.org","from":"a@x.org, b@x.org","subject":"S"}`,
		"filename CRLF":    `{"to":"to@x.org","subject":"S","attachments":[{"filename":"a\r\nX: y","content":"c"}]}`,
		"cid CRLF":         `{"to":"to@x.org","subject":"S","attachments":[{"cid":"a\nb","content":"c"}]}`,
		"ical method CRLF": `{"to":"to@x.org","subject":"S","icalEvent":{"method":"REQUEST\r\nX: y","content":"c"}}`,
	} {
		if r := send(b); r.status != 400 {
			t.Errorf("%s: %d %s", name, r.status, r.body)
		}
	}
	if len(srv.Snapshot().Mails) != n {
		t.Error("không thư nào được gửi từ yêu cầu sai")
	}
}

func TestHTTPEmailAttachmentValidationAndPaths(t *testing.T) {
	a, srv, hdr := emailAPI(t, nil)
	send := func(att string) resp {
		return a.do("POST", "/email/v1/send", `{"to":"to@x.org","subject":"S","attachments":`+att+`}`, jsonHdr(hdr))
	}
	cases := map[string]struct{ att, want string }{
		"không phải object": {`["str"]`, "Attachment must be an object"},
		"content là object": {`[{"content":{}}]`, "Attachment properties must be strings"},
		"path là object":    {`[{"path":{}}]`, "Attachment properties must be strings"},
		"contentType số":    {`[{"contentType":5}]`, "Attachment properties must be strings"},
		"filename số":       {`[{"filename":5}]`, "Attachment filename must be a string"},
		"filename true":     {`[{"filename":true}]`, "Attachment filename must be a string"},
		"Binary":            {`[{"path":"Binary/123"}]`, "Binary attachments are not supported"},
		"tệp cục bộ":        {`[{"path":"./package.json"}]`, "File access rejected for ./package.json"},
		"URL metadata":      {`[{"path":"https://169.254.170.2/v2/metadata"}]`, "Failed to fetch attachment: Outbound request to unsafe hostname 169.254.170.2 is blocked"},
		"URL localhost":     {`[{"path":"https://localhost/x"}]`, "unsafe hostname localhost"},
		"URL http":          {`[{"path":"http://example.org/x"}]`, "Failed to fetch attachment"},
		"base64 sai":        {`[{"content":"@@@","encoding":"base64"}]`, "not valid base64"},
	}
	for name, tc := range cases {
		r := send(tc.att)
		got := strings.Join(issueTexts(t, r), "|")
		if r.status != 400 || !strings.Contains(got, tc.want) {
			t.Errorf("%s: %d %s (mong %q)", name, r.status, r.body, tc.want)
		}
	}
	// filename: false hợp lệ (nhúng không tên)
	if r := send(`[{"filename":false,"content":"x"}]`); r.status != 200 {
		t.Errorf("filename false: %d %s", r.status, r.body)
	}
	if n := len(srv.Snapshot().Mails); n != 1 {
		t.Errorf("chỉ ca hợp lệ được gửi: %d", n)
	}
}

func TestHTTPEmailSMTPFailureIs502WithoutDetails(t *testing.T) {
	a, _, hdr := emailAPI(t, nil)
	// người nhận bị máy chủ từ chối: 502, thông điệp chung
	srv2 := smtptest.Start(t, func(s *smtptest.Server) { s.RejectTo = "bad@x.org" })
	a2 := newCodeAPI(t, func(s *config.Settings) { smtpSettings(srv2, writeCA(t, srv2))(s); s.EmailEnabled = true })
	if _, err := a2.c.Auth.Projects.Mutate(context.Background(), a2.seed.Project.ID, func(p *fhir.Project) error { p.Features = append(p.Features, "email"); return nil }); err != nil {
		t.Fatal(err)
	}
	r := a2.do("POST", "/email/v1/send", `{"to":"bad@x.org","subject":"Mã 123456"}`, jsonHdr(adminBearer(a2)))
	if r.status != 502 || issueTexts(t, r)[0] != "Failed to send email" || strings.Contains(r.body, "550") || strings.Contains(r.body, "bad@x.org") || strings.Contains(r.body, "no such user") {
		t.Errorf("502: %d %s", r.status, r.body)
	}
	// vượt số người nhận: 400 (Validate của smtp.Mailer)
	a3, _, hdr3 := emailAPI(t, func(s *config.Settings) { s.EmailMaxRecipients = 2 })
	if r := a3.do("POST", "/email/v1/send", `{"to":["a@x.org","b@x.org"],"cc":"c@x.org","subject":"S"}`, jsonHdr(hdr3)); r.status != 400 || !strings.Contains(r.body, "Too many recipients") {
		t.Errorf("quá nhiều người nhận: %d %s", r.status, r.body)
	}
	_ = a
	_ = hdr
}

// SMTP riêng của project: chỉ khi AUTH_EMAIL_ALLOW_PROJECT_SMTP; nối qua dialer chống SSRF trừ khi AUTH_EMAIL_ALLOW_UNSAFE_OUTBOUND; tên hiển thị = appName.
func TestHTTPEmailProjectSMTP(t *testing.T) {
	projSrv := smtptest.Start(t, nil)
	setSecrets := func(a *codeAPI, port int) {
		secrets := []fhir.ProjectSetting{{Name: "smtpHost", ValueString: "127.0.0.1"}, {Name: "smtpPort", ValueInteger: &port}, {Name: "smtpUsername", ValueString: "pu"},
			{Name: "smtpPassword", ValueString: "pp"}, {Name: "smtpFromAddress", ValueString: "noreply@proj.example.org"}, {Name: "smtpApprovedSenders", ValueString: "team@proj.example.org"}}
		if _, err := a.c.Auth.Projects.Mutate(context.Background(), a.seed.Project.ID, func(p *fhir.Project) error { p.Secret = secrets; return nil }); err != nil {
			t.Fatal(err)
		}
	}
	// tắt (mặc định): dùng SMTP server, SMTP project không bị đụng
	off, offSrv, offHdr := emailAPI(t, nil)
	setSecrets(off, projSrv.Port())
	if r := off.do("POST", "/email/v1/send", `{"to":"to@x.org","subject":"S"}`, jsonHdr(offHdr)); r.status != 200 {
		t.Fatalf("tắt: %d %s", r.status, r.body)
	}
	if len(offSrv.Snapshot().Mails) != 1 || projSrv.Snapshot().Conns != 0 {
		t.Errorf("tắt SMTP project: server=%d project=%d", len(offSrv.Snapshot().Mails), projSrv.Snapshot().Conns)
	}
	// bật nhưng KHÔNG cho địa chỉ nội bộ: 127.0.0.1 bị chặn, 400 chung, máy chủ không bị nối
	on, onSrv, onHdr := emailAPI(t, func(s *config.Settings) { s.EmailAllowProjectSMTP = true })
	setSecrets(on, projSrv.Port())
	r := on.do("POST", "/email/v1/send", `{"to":"to@x.org","subject":"S"}`, jsonHdr(onHdr))
	if r.status != 400 || issueTexts(t, r)[0] != "Project SMTP configuration is incomplete or invalid" {
		t.Fatalf("SSRF: %d %s", r.status, r.body)
	}
	if projSrv.Snapshot().Conns != 0 || len(onSrv.Snapshot().Mails) != 0 {
		t.Error("máy chủ nội bộ không được bị nối")
	}
	// cho phép địa chỉ nội bộ (dev): gửi qua SMTP project, người gửi + tên hiển thị appName, danh sách của project
	dev, devSrv, devHdr := emailAPI(t, func(s *config.Settings) { s.EmailAllowProjectSMTP, s.EmailAllowUnsafeOutbound = true, true })
	setSecrets(dev, projSrv.Port())
	if r := dev.do("POST", "/email/v1/send", `{"to":"to@x.org","subject":"S","from":"team@proj.example.org"}`, jsonHdr(devHdr)); r.status != 200 {
		t.Fatalf("project smtp: %d %s", r.status, r.body)
	}
	if r := dev.do("POST", "/email/v1/send", `{"to":"to@x.org","subject":"S","from":"approved@trivita.example.org"}`, jsonHdr(devHdr)); r.status != 200 {
		t.Fatalf("project smtp 2: %d %s", r.status, r.body)
	}
	pm := projSrv.Snapshot()
	if len(pm.Mails) != 2 || pm.Mails[0].From != "team@proj.example.org" || pm.Mails[1].From != "noreply@proj.example.org" || len(pm.Authed) != 2 || pm.Authed[0] != "PLAIN pu:pp" {
		t.Fatalf("project: %+v %v", pm.Mails, pm.Authed)
	}
	if !strings.Contains(pm.Mails[1].Data, "From: =?utf-8?q?Ph=C3=B2ng_kh=C3=A1m?= <noreply@proj.example.org>") {
		t.Errorf("From có appName: %s", pm.Mails[1].Data)
	}
	if len(devSrv.Snapshot().Mails) != 0 {
		t.Error("SMTP server không được dùng khi project có SMTP riêng")
	}
	// cấu hình dở: 400 thông điệp Medplum, không nối mạng
	bad, _, badHdr := emailAPI(t, func(s *config.Settings) { s.EmailAllowProjectSMTP, s.EmailAllowUnsafeOutbound = true, true })
	if _, err := bad.c.Auth.Projects.Mutate(context.Background(), bad.seed.Project.ID, func(p *fhir.Project) error {
		p.Secret = []fhir.ProjectSetting{{Name: "smtpHost", ValueString: "127.0.0.1"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if r := bad.do("POST", "/email/v1/send", `{"to":"to@x.org","subject":"S"}`, jsonHdr(badHdr)); r.status != 400 || issueTexts(t, r)[0] != "Project SMTP configuration is incomplete or invalid" {
		t.Errorf("dở: %d %s", r.status, r.body)
	}
}

func TestHTTPEmailRateLimitSharedWithLogin(t *testing.T) {
	a, _, hdr := emailAPI(t, func(s *config.Settings) { s.LoginRateLimit, s.LoginRateBurst = 0.001, 3 })
	_ = a.login(loginBody(""), nil) // dùng một lượt của hạn mức đăng nhập? adminBearer đã dùng; kiểm trực tiếp bằng cách bắn đến khi 429
	got429 := false
	for i := 0; i < 10 && !got429; i++ {
		if r := a.do("POST", "/email/v1/send", `{}`, jsonHdr(hdr)); r.status == 429 {
			got429 = true
		}
	}
	if !got429 {
		t.Error("route gửi thư phải chịu hạn mức")
	}
}

// Thông điệp validator: to null/vắng/rỗng là "To is required"; subject vắng/null/rỗng là "Subject is required"; sai kiểu thì thông điệp khác.
func TestHTTPEmailValidatorMessages(t *testing.T) {
	a, srv, hdr := emailAPI(t, nil)
	check := func(b, want string) {
		t.Helper()
		r := a.do("POST", "/email/v1/send", b, jsonHdr(hdr))
		if r.status != 400 || len(issueTexts(t, r)) != 1 || issueTexts(t, r)[0] != want {
			t.Errorf("%s: %d %s (mong %q)", b, r.status, r.body, want)
		}
	}
	for _, b := range []string{`{"subject":"S"}`, `{"to":null,"subject":"S"}`, `{"to":"","subject":"S"}`, `{"to":[],"subject":"S"}`} {
		check(b, "To is required")
	}
	for _, b := range []string{`{"to":"a@x.org"}`, `{"to":"a@x.org","subject":null}`, `{"to":"a@x.org","subject":""}`} {
		check(b, "Subject is required")
	}
	check(`{"to":"a@x.org","subject":[]}`, "Subject must be a string")
	check(`{"to":"a@x.org","subject":"S","text":{}}`, "Text must be a string")
	check(`{"to":"a@x.org","subject":"S","html":5}`, "HTML must be a string")
	check(`{"to":5,"subject":"S"}`, "To must be an email address, an object with an address, or an array of them")
	check(`{"to":"a@x.org","subject":"S","icalEvent":{"content":5}}`, "icalEvent must be a string or an object with string content")
	check(`{"to":"a@x.org","subject":"S","icalEvent":{"content":"c","method":5}}`, "icalEvent properties must be strings")
	// null được coi như vắng (text, html, cc, attachments, icalEvent)
	if r := a.do("POST", "/email/v1/send", `{"to":"a@x.org","subject":"S","text":null,"html":null,"cc":null,"attachments":null,"icalEvent":null,"from":null}`, jsonHdr(hdr)); r.status != 200 {
		t.Errorf("null như vắng: %d %s", r.status, r.body)
	}
	// icalEvent dạng chuỗi
	if r := a.do("POST", "/email/v1/send", `{"to":"a@x.org","subject":"S","icalEvent":"BEGIN:VCALENDAR\r\nEND:VCALENDAR"}`, jsonHdr(hdr)); r.status != 200 {
		t.Errorf("icalEvent chuỗi: %d %s", r.status, r.body)
	}
	if n := len(srv.Snapshot().Mails); n != 2 {
		t.Errorf("chỉ hai ca hợp lệ được gửi: %d", n)
	}
}

// Với đường gửi không tự kiểm (provider=log), validator của route là lớp kiểm duy nhất cho subject.
func TestHTTPEmailValidatorIsSelfSufficientWithLogProvider(t *testing.T) {
	a := newCodeAPI(t, func(s *config.Settings) { s.EmailEnabled, s.MailProvider = true, "log" })
	if _, err := a.c.Auth.Projects.Mutate(context.Background(), a.seed.Project.ID, func(p *fhir.Project) error { p.Features = append(p.Features, "email"); return nil }); err != nil {
		t.Fatal(err)
	}
	hdr := jsonHdr(adminBearer(a))
	if r := a.do("POST", "/email/v1/send", `{"to":"a@x.org"}`, hdr); r.status != 400 || issueTexts(t, r)[0] != "Subject is required" {
		t.Errorf("thiếu subject: %d %s", r.status, r.body)
	}
	if r := a.do("POST", "/email/v1/send", `{"to":"a@x.org","subject":"S","text":"T"}`, hdr); r.status != 200 {
		t.Errorf("provider log gửi được: %d %s", r.status, r.body)
	}
}
