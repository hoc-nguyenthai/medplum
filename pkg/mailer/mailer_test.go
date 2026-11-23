package mailer

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"testing"
	"time"
)

var _ Mailer = Nop{}
var _ Mailer = Log{}
var _ Mailer = (*Fake)(nil)

func ip(n int) *int   { return &n }
func bp(b bool) *bool { return &b }
func addrs(e ...string) []Address {
	var out []Address
	for _, x := range e {
		out = append(out, Address{Email: x})
	}
	return out
}

func TestAddressParsing(t *testing.T) {
	for in, want := range map[string]string{
		"a@x.org": "a@x.org", "  A@X.org ": "A@X.org", "Tên Người <a@x.org>": "a@x.org", `"<a@x.com>" <evil@y.com>`: "evil@y.com", "<a@x.org>": "a@x.org",
	} {
		if got := ExtractEmail(in); got != want {
			t.Errorf("%q: %q (mong %q)", in, got, want)
		}
	}
	for _, bad := range []string{"", "khong-phai-email", "a@x.org, b@x.org", "a@x.org\r\nBcc: v@y.org", "a@x.org\nX: y", "a\x00@x.org", "@x.org", "<>"} {
		if got := ExtractEmail(bad); got != "" {
			t.Errorf("%q phải bị từ chối, được %q", bad, got)
		}
		if _, err := ParseAddress(bad); err == nil {
			t.Errorf("ParseAddress(%q) phải lỗi", bad)
		}
	}
	a, err := ParseAddress("Nguyễn Văn A <a@x.org>")
	if err != nil || a.Name != "Nguyễn Văn A" || a.Email != "a@x.org" {
		t.Fatalf("%+v %v", a, err)
	}
	if s := a.String(); !strings.Contains(s, "a@x.org") || strings.ContainsAny(s, "\r\n") || !strings.Contains(s, "=?utf-8?") {
		t.Errorf("tên phải được mã hóa: %q", s)
	}
	if (Address{}).String() != "" {
		t.Error("địa chỉ rỗng")
	}
}

func TestResolveFrom(t *testing.T) {
	pol := FromPolicy{Approved: SplitApproved(" Ok@X.org , other@x.org ,, "), Default: "Hỗ trợ <support@x.org>"}
	if got := pol.Approved; len(got) != 2 || got[0] != "ok@x.org" || got[1] != "other@x.org" {
		t.Fatalf("SplitApproved: %v", got)
	}
	// được duyệt: giữ nguyên cả tên hiển thị người gọi đặt, so khớp không phân biệt hoa thường
	req := &Address{Name: "Tên Khác", Email: "OK@x.org"}
	if got := ResolveFrom(req, pol); got != *req {
		t.Errorf("được duyệt: %+v", got)
	}
	// mục thứ hai của danh sách có khoảng trắng vẫn khớp (Medplum lỗi ở đây)
	if got := ResolveFrom(&Address{Email: "other@x.org"}, pol); got.Email != "other@x.org" {
		t.Errorf("mục thứ hai: %+v", got)
	}
	// không được duyệt, rỗng, hoặc cố lách: mặc định
	for name, r := range map[string]*Address{"lạ": {Email: "evil@y.org"}, "nil": nil, "rỗng": {}, "đuôi giống": {Email: "ok@x.org.evil.com"}, "tên giống": {Name: "ok@x.org", Email: "evil@y.org"}} {
		got := ResolveFrom(r, pol)
		if got.Email != "support@x.org" || got.Name != "Hỗ trợ" {
			t.Errorf("%s: %+v", name, got)
		}
	}
	// Default là địa chỉ trần; Default hỏng thì giữ nguyên chuỗi
	if got := ResolveFrom(nil, FromPolicy{Default: "a@x.org"}); got != (Address{Email: "a@x.org"}) {
		t.Errorf("default trần: %+v", got)
	}
	if got := ResolveFrom(nil, FromPolicy{Default: "  rac "}); got.Email != "rac" {
		t.Errorf("default hỏng: %+v", got)
	}
	// danh sách rỗng: không ai được đề nghị
	if got := ResolveFrom(&Address{Email: "a@x.org"}, FromPolicy{Default: "d@x.org"}); got.Email != "d@x.org" {
		t.Errorf("không có danh sách: %+v", got)
	}
}

func TestApplyDisplayName(t *testing.T) {
	bare := Address{Email: "a@x.org"}
	named := Address{Name: "Có tên", Email: "a@x.org"}
	for name, tc := range map[string]struct {
		in   Address
		app  string
		want Address
	}{
		"gắn tên":          {bare, "Phòng khám", Address{Name: "Phòng khám", Email: "a@x.org"}},
		"cắt khoảng trắng": {bare, "  Phòng khám  ", Address{Name: "Phòng khám", Email: "a@x.org"}},
		"giữ tên có sẵn":   {named, "Phòng khám", named},
		"appName rỗng":     {bare, "   ", bare},
		"appName có CRLF":  {bare, "A\r\nBcc: v@y.org", bare},
	} {
		if got := ApplyDisplayName(tc.in, tc.app); got != tc.want {
			t.Errorf("%s: %+v", name, got)
		}
	}
}

func TestProjectSMTPFromSecrets(t *testing.T) {
	full := []Setting{{Name: "smtpHost", String: " smtp.p.org "}, {Name: "smtpPort", Int: ip(465)}, {Name: "smtpUsername", String: "u"}, {Name: "smtpPassword", String: "p w"},
		{Name: "smtpFromAddress", String: "Gửi <from@p.org>"}, {Name: "smtpApprovedSenders", String: "A@p.org, b@p.org"}}
	got, err := ProjectSMTPFromSecrets(full, true)
	if err != nil || got == nil || got.Host != "smtp.p.org" || got.Port != 465 || got.Username != "u" || got.Password != "p w" || got.FromAddress != "Gửi <from@p.org>" ||
		len(got.ApprovedSenders) != 2 || got.ApprovedSenders[0] != "a@p.org" || !got.ImplicitTLS() {
		t.Fatalf("%+v %v", got, err)
	}
	// secure tường minh thắng suy ra theo cổng
	sec := append(append([]Setting{}, full...), Setting{Name: "smtpSecure", Bool: bp(false)})
	if got, _ := ProjectSMTPFromSecrets(sec, true); got.ImplicitTLS() {
		t.Error("smtpSecure=false trên cổng 465 phải là STARTTLS")
	}
}

func TestProjectSMTPDisabledAndIncomplete(t *testing.T) {
	full := []Setting{{Name: "smtpHost", String: "h"}, {Name: "smtpPort", Int: ip(587)}, {Name: "smtpUsername", String: "u"}, {Name: "smtpPassword", String: "p"}, {Name: "smtpFromAddress", String: "f@p.org"}}
	if got, err := ProjectSMTPFromSecrets(full, true); err != nil || got == nil || got.ImplicitTLS() {
		t.Fatalf("587: %+v %v", got, err)
	}
	if got, err := ProjectSMTPFromSecrets(full, false); got != nil || err != nil {
		t.Errorf("tắt: %+v %v", got, err)
	}
	for name, ss := range map[string][]Setting{"không có": nil, "host rỗng": {{Name: "smtpHost", String: "  "}}} {
		if got, err := ProjectSMTPFromSecrets(ss, true); got != nil || err != nil {
			t.Errorf("%s: %+v %v", name, got, err)
		}
	}
	drop := func(name string) []Setting {
		var out []Setting
		for _, s := range full {
			if s.Name != name {
				out = append(out, s)
			}
		}
		return out
	}
	for name, tc := range map[string]struct {
		ss    []Setting
		field string
	}{
		"thiếu port":     {drop("smtpPort"), "smtpPort"},
		"thiếu username": {drop("smtpUsername"), "smtpUsername"},
		"thiếu password": {drop("smtpPassword"), "smtpPassword"},
		"thiếu from":     {drop("smtpFromAddress"), "smtpFromAddress"},
		"port 0":         {append(drop("smtpPort"), Setting{Name: "smtpPort", Int: ip(0)}), "smtpPort"},
		"port quá lớn":   {append(drop("smtpPort"), Setting{Name: "smtpPort", Int: ip(70000)}), "smtpPort"},
		"from hỏng":      {append(drop("smtpFromAddress"), Setting{Name: "smtpFromAddress", String: "khong-phai"}), "smtpFromAddress"},
	} {
		got, err := ProjectSMTPFromSecrets(tc.ss, true)
		var e *Error
		if got != nil || !errors.As(err, &e) || e.Kind != KindConfig || e.Msg != "Project SMTP configuration is incomplete or invalid" || len(e.Fields) != 1 || e.Fields[0] != tc.field {
			t.Errorf("%s: %+v %v", name, got, err)
		}
		if err != nil && strings.Contains(err.Error(), "p") && strings.Contains(strings.Join(e.Fields, ","), "p@ss") {
			t.Error("lộ giá trị")
		}
	}
	// nhiều trường thiếu cùng lúc: liệt kê đủ, chỉ tên
	_, err := ProjectSMTPFromSecrets([]Setting{{Name: "smtpHost", String: "h"}}, true)
	var e *Error
	if !errors.As(err, &e) || len(e.Fields) != 4 {
		t.Errorf("nhiều trường: %v", err)
	}
}

func TestProjectAppNameAndConfigured(t *testing.T) {
	if got := ProjectAppName([]Setting{{Name: "appName", String: "  Phòng khám  "}}); got != "Phòng khám" {
		t.Errorf("%q", got)
	}
	for name, ss := range map[string][]Setting{"không có": nil, "rỗng": {{Name: "appName", String: "  "}}, "CRLF": {{Name: "appName", String: "A\nB"}}, "tên khác": {{Name: "x", String: "A"}}} {
		if got := ProjectAppName(ss); got != "" {
			t.Errorf("%s: %q", name, got)
		}
	}
	srv := &SMTPConfig{Host: "h"}
	proj := &ProjectContext{SMTP: &ProjectSMTP{}}
	for name, tc := range map[string]struct {
		s    *SMTPConfig
		p    *ProjectContext
		want bool
	}{"server": {srv, nil, true}, "project": {nil, proj, true}, "không": {nil, nil, false}, "server rỗng": {&SMTPConfig{}, nil, false}, "project không smtp": {nil, &ProjectContext{}, false}} {
		if got := Configured(tc.s, tc.p); got != tc.want {
			t.Errorf("%s: %v", name, got)
		}
	}
}

func TestValidate(t *testing.T) {
	ok := Message{To: addrs("a@x.org"), Subject: "Chào"}
	if err := ok.Validate(Limits{}); err != nil {
		t.Fatal(err)
	}
	bad := map[string]func(*Message){
		"không người nhận":  func(m *Message) { m.To = nil },
		"subject rỗng":      func(m *Message) { m.Subject = "  " },
		"subject CRLF":      func(m *Message) { m.Subject = "a\r\nBcc: v@y.org" },
		"email CRLF":        func(m *Message) { m.To = addrs("a@x.org\r\nBcc: v@y.org") },
		"tên CRLF":          func(m *Message) { m.Cc = []Address{{Name: "A\nB", Email: "c@x.org"}} },
		"email sai":         func(m *Message) { m.Bcc = addrs("khong-phai") },
		"email có dấu phẩy": func(m *Message) { m.ReplyTo = addrs("a@x.org,b@x.org") },
		"from CRLF":         func(m *Message) { m.From = &Address{Email: "a@x.org\n"} },
		"filename CRLF":     func(m *Message) { m.Attachments = []Attachment{{Filename: "a\r\n.txt"}} },
		"cid CRLF":          func(m *Message) { m.Attachments = []Attachment{{CID: "a\nb"}} },
		"ical method CRLF":  func(m *Message) { m.ICal = &ICalEvent{Method: "REQUEST\r\nX: y"} },
		"quá nhiều người":   func(m *Message) { m.To = addrs("1@x.org", "2@x.org", "3@x.org") },
	}
	for name, mut := range bad {
		m := ok
		mut(&m)
		err := m.Validate(Limits{MaxRecipients: 2})
		if !IsKind(err, KindInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// ---- Build

func parse(t *testing.T, raw []byte) *mail.Message {
	t.Helper()
	m, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("không đọc được thư: %v\n%s", err, raw)
	}
	return m
}

func decodeBody(t *testing.T, header map[string][]string, body io.Reader) string {
	t.Helper()
	raw, _ := io.ReadAll(body)
	enc := ""
	if v := header["Content-Transfer-Encoding"]; len(v) > 0 {
		enc = strings.ToLower(v[0])
	}
	switch enc {
	case "quoted-printable":
		b, err := io.ReadAll(quotedprintable.NewReader(bytes.NewReader(raw)))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	case "base64":
		b, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(string(raw), "\r\n", ""))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	return string(raw)
}

// flatten duyệt cây MIME và trả mọi phần lá: kiểu, header, thân đã giải mã.
type leaf struct {
	ctype string
	hdr   map[string][]string
	body  string
}

func flatten(t *testing.T, hdr map[string][]string, body io.Reader, kinds *[]string) []leaf {
	t.Helper()
	ct, params, err := mime.ParseMediaType(first(hdr["Content-Type"]))
	if err != nil {
		t.Fatalf("Content-Type: %v", err)
	}
	if strings.HasPrefix(ct, "multipart/") {
		*kinds = append(*kinds, ct)
		var out []leaf
		mr := multipart.NewReader(body, params["boundary"])
		for {
			p, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, flatten(t, p.Header, p, kinds)...)
		}
		return out
	}
	return []leaf{{ct, hdr, decodeBody(t, hdr, body)}}
}

func first(v []string) string {
	if len(v) == 0 {
		return ""
	}
	return v[0]
}

var now = time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)

func TestBuildPlainText(t *testing.T) {
	m := Message{To: []Address{{Name: "Nguyễn Văn A", Email: "a@x.org"}}, Cc: addrs("c@x.org"), Bcc: addrs("secret-bcc@x.org"), ReplyTo: addrs("r@x.org"), Subject: "Mã xác thực: 123456 — Chào", Text: "Xin chào\nDòng hai có tiếng Việt: ăâê"}
	raw, err := Build(m, Address{Name: "Hỗ trợ", Email: "s@x.org"}, now, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	msg := parse(t, raw)
	if strings.Contains(string(raw), "secret-bcc") {
		t.Error("Bcc không được xuất hiện trong thư")
	}
	dec := new(mime.WordDecoder)
	subj, err := dec.DecodeHeader(msg.Header.Get("Subject"))
	if err != nil || subj != m.Subject {
		t.Errorf("subject: %q %v", subj, err)
	}
	if f, _ := msg.Header.AddressList("From"); len(f) != 1 || f[0].Address != "s@x.org" || f[0].Name != "Hỗ trợ" {
		t.Errorf("from: %v", f)
	}
	if to, _ := msg.Header.AddressList("To"); len(to) != 1 || to[0].Name != "Nguyễn Văn A" {
		t.Errorf("to: %v", to)
	}
	if msg.Header.Get("Cc") != "<c@x.org>" && !strings.Contains(msg.Header.Get("Cc"), "c@x.org") || !strings.Contains(msg.Header.Get("Reply-To"), "r@x.org") {
		t.Errorf("cc/reply-to: %q %q", msg.Header.Get("Cc"), msg.Header.Get("Reply-To"))
	}
	if d, err := msg.Header.Date(); err != nil || !d.Equal(now) {
		t.Errorf("date: %v %v", d, err)
	}
	if id := msg.Header.Get("Message-Id"); !strings.HasPrefix(id, "<") || !strings.HasSuffix(id, "@x.org>") || msg.Header.Get("Mime-Version") != "1.0" {
		t.Errorf("message-id/mime-version: %q", id)
	}
	var kinds []string
	leaves := flatten(t, msg.Header, msg.Body, &kinds)
	if len(kinds) != 0 || len(leaves) != 1 || leaves[0].ctype != "text/plain" || strings.TrimSuffix(leaves[0].body, "\r\n") != "Xin chào\r\nDòng hai có tiếng Việt: ăâê" {
		t.Errorf("thân: %+v kinds=%v", leaves, kinds)
	}
	// không dòng nào quá 998 ký tự và toàn bộ dùng CRLF
	for _, line := range strings.Split(string(raw), "\r\n") {
		if len(line) > 998 || strings.Contains(line, "\n") {
			t.Errorf("dòng sai: %q", line)
		}
	}
	// Envelope: người gửi trần, người nhận gồm cả Bcc, bỏ trùng không phân biệt hoa thường
	m.To = append(m.To, Address{Email: "A@x.org"})
	sender, rcpt := Envelope(m, Address{Name: "Hỗ trợ", Email: "s@x.org"})
	if sender != "s@x.org" || len(rcpt) != 3 || rcpt[0] != "a@x.org" || rcpt[2] != "secret-bcc@x.org" {
		t.Errorf("envelope: %q %v", sender, rcpt)
	}
}

func TestBuildMultipartStructure(t *testing.T) {
	att := func(name string, content string, mut func(*Attachment)) Attachment {
		a := Attachment{Filename: name, ContentType: "text/plain", Body: strings.NewReader(content)}
		if mut != nil {
			mut(&a)
		}
		return a
	}
	cases := map[string]struct {
		m     Message
		kinds []string
		types []string
	}{
		"text+html":  {Message{Text: "t", HTML: "<b>h</b>"}, []string{"multipart/alternative"}, []string{"text/plain", "text/html"}},
		"chỉ html":   {Message{HTML: "<b>h</b>"}, nil, []string{"text/html"}},
		"không thân": {Message{}, nil, []string{"text/plain"}},
		"đính kèm":   {Message{Text: "t", Attachments: []Attachment{att("a.txt", "dữ liệu", nil)}}, []string{"multipart/mixed"}, []string{"text/plain", "text/plain"}},
		"html+cid+tệp": {Message{HTML: "<img src=cid:logo>", Attachments: []Attachment{att("logo.png", "PNG", func(a *Attachment) { a.CID = "logo"; a.ContentType = "image/png" }), att("b.txt", "B", nil)}},
			[]string{"multipart/mixed", "multipart/related"}, []string{"text/html", "image/png", "text/plain"}},
		"ical": {Message{Text: "t", ICal: &ICalEvent{Method: "request", Content: "BEGIN:VCALENDAR\r\nEND:VCALENDAR"}}, []string{"multipart/alternative"}, []string{"text/plain", "text/calendar"}},
	}
	for name, tc := range cases {
		m := tc.m
		m.To, m.Subject = addrs("a@x.org"), "s"
		raw, err := Build(m, Address{Email: "f@x.org"}, now, Limits{})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		msg := parse(t, raw)
		var kinds []string
		leaves := flatten(t, msg.Header, msg.Body, &kinds)
		var types []string
		for _, l := range leaves {
			types = append(types, l.ctype)
		}
		if strings.Join(kinds, ",") != strings.Join(tc.kinds, ",") || strings.Join(types, ",") != strings.Join(tc.types, ",") {
			t.Errorf("%s: kinds=%v types=%v", name, kinds, types)
		}
	}
	// nội dung đính kèm giải mã đúng, tên có dấu mã hóa RFC 2231, cid có ngoặc
	m := Message{To: addrs("a@x.org"), Subject: "s", Text: "t", Attachments: []Attachment{
		att("báo cáo.txt", strings.Repeat("dữ liệu ", 100), nil),
		att("", "x", func(a *Attachment) { a.CID = "<c1>"; a.ContentType = "image/png" })}}
	raw, err := Build(m, Address{Email: "f@x.org"}, now, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	msg := parse(t, raw)
	var kinds []string
	leaves := flatten(t, msg.Header, msg.Body, &kinds)
	// cấu trúc mixed[related[text, cid], tệp]: lá theo thứ tự text, cid, tệp
	if len(leaves) != 3 || leaves[2].body != strings.Repeat("dữ liệu ", 100) {
		t.Fatalf("đính kèm: %d leaves", len(leaves))
	}
	if _, p, err := mime.ParseMediaType(first(leaves[2].hdr["Content-Disposition"])); err != nil || p["filename"] != "báo cáo.txt" {
		t.Errorf("filename: %v %v", p, err)
	}
	if first(leaves[1].hdr["Content-Id"]) != "<c1>" || !strings.HasPrefix(first(leaves[1].hdr["Content-Disposition"]), "inline") {
		t.Errorf("cid: %v", leaves[1].hdr)
	}
	if ct := first(leaves[0].hdr["Content-Type"]); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("leaf 0: %s", ct)
	}
	// ical có method viết hoa và filename
	m2 := Message{To: addrs("a@x.org"), Subject: "s", ICal: &ICalEvent{Method: "request", Filename: "mời.ics", Content: "BEGIN:VCALENDAR"}}
	raw, _ = Build(m2, Address{Email: "f@x.org"}, now, Limits{})
	if !strings.Contains(string(raw), "method=REQUEST") {
		t.Errorf("method: %s", raw)
	}
}

func TestBuildRejections(t *testing.T) {
	base := Message{To: addrs("a@x.org"), Subject: "s", Text: "t"}
	if _, err := Build(base, Address{}, now, Limits{}); !IsKind(err, KindConfig) {
		t.Errorf("không có người gửi: %v", err)
	}
	if _, err := Build(base, Address{Email: "f@x.org\r\nBcc: v@y.org"}, now, Limits{}); !IsKind(err, KindInvalid) {
		t.Errorf("from CRLF: %v", err)
	}
	if _, err := Build(base, Address{Name: "A\nB", Email: "f@x.org"}, now, Limits{}); !IsKind(err, KindInvalid) {
		t.Errorf("tên CRLF: %v", err)
	}
	big := base
	big.Attachments = []Attachment{{Filename: "a", Body: strings.NewReader(strings.Repeat("x", 2000))}}
	if _, err := Build(big, Address{Email: "f@x.org"}, now, Limits{MaxBytes: 1000}); !IsKind(err, KindInvalid) {
		t.Errorf("đính kèm quá lớn: %v", err)
	}
	if _, err := Build(Message{To: addrs("a@x.org"), Subject: "s", Text: strings.Repeat("y", 5000)}, Address{Email: "f@x.org"}, now, Limits{MaxBytes: 1000}); !IsKind(err, KindInvalid) {
		t.Errorf("thư quá lớn: %v", err)
	}
	for name, a := range map[string]Attachment{"content type sai": {ContentType: "không phải;;;"}, "disposition lạ": {Disposition: "x"}} {
		m := base
		a.Body = strings.NewReader("x")
		m.Attachments = []Attachment{a}
		if _, err := Build(m, Address{Email: "f@x.org"}, now, Limits{}); !IsKind(err, KindInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	m := base
	m.Attachments = []Attachment{{Body: errReader{}}}
	if _, err := Build(m, Address{Email: "f@x.org"}, now, Limits{}); !IsKind(err, KindInvalid) {
		t.Errorf("đọc lỗi: %v", err)
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("hỏng") }

func TestAdaptersNeverLeakSubjectInNop(t *testing.T) {
	var buf bytes.Buffer
	lg := slog.New(slog.NewTextHandler(&buf, nil))
	if err := (Nop{Logger: lg}).Send(context.Background(), Message{Subject: "Mã MFA 654321"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "654321") || buf.Len() == 0 {
		t.Errorf("Nop phải ghi cảnh báo mà không kèm subject: %q", buf.String())
	}
	buf.Reset()
	if err := (Log{Logger: lg}).Send(context.Background(), Message{To: addrs("a@x.org"), Subject: "S", Text: "nội dung"}); err != nil || !strings.Contains(buf.String(), "a@x.org") || !strings.Contains(buf.String(), "nội dung") {
		t.Errorf("Log: %q %v", buf.String(), err)
	}
	f := &Fake{}
	_ = f.Send(context.Background(), Message{Subject: "1"})
	f.Err = errors.New("lỗi")
	if err := f.Send(context.Background(), Message{Subject: "2"}); err == nil || len(f.Sent()) != 2 {
		t.Errorf("Fake: %v %d", err, len(f.Sent()))
	}
	got := f.Sent()
	got[0].Subject = "đã sửa"
	if f.Sent()[0].Subject != "1" {
		t.Error("Sent phải trả bản sao")
	}
}

func TestErrorHelpers(t *testing.T) {
	e := &Error{Kind: KindTransport, Msg: "m", Err: io.EOF}
	if !errors.Is(e, io.EOF) || !IsKind(e, KindTransport) || IsKind(e, KindConfig) || IsKind(nil, KindConfig) || !strings.Contains(e.Error(), "EOF") || (&Error{Msg: "x"}).Error() != "x" {
		t.Error("Error helpers")
	}
	if !IsKind(&Error{Kind: KindInvalid}, KindInvalid) {
		t.Error("IsKind")
	}
	if !IsKind(errors.Join(errors.New("a"), &Error{Kind: KindConfig}), KindConfig) {
		t.Error("IsKind qua errors.Join")
	}
	if (SMTPConfig{Port: 465}).ImplicitTLS() != true || (SMTPConfig{Port: 587}).ImplicitTLS() || !(SMTPConfig{Port: 25, Secure: bp(true)}).ImplicitTLS() {
		t.Error("ImplicitTLS")
	}
}

func TestParseAddressList(t *testing.T) {
	got, err := ParseAddressList(" a@x.org , Tên Người <b@x.org>,c@x.org ")
	if err != nil || len(got) != 3 || got[0].Email != "a@x.org" || got[1].Name != "Tên Người" || got[2].Email != "c@x.org" {
		t.Fatalf("%+v %v", got, err)
	}
	for _, bad := range []string{"", "  ", "a@x.org,", "a@x.org, khong-phai", "a@x.org\r\nBcc: v@y.org", "a@x.org\n", "a\x00@x.org", ","} {
		if _, err := ParseAddressList(bad); !IsKind(err, KindInvalid) {
			t.Errorf("%q phải bị từ chối: %v", bad, err)
		}
	}
}
