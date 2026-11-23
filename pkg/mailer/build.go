package mailer

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"mime/quotedprintable"
	"net/textproto"
	"strings"
	"time"
)

// Build dựng thư MIME (CRLF) từ m với người gửi đã chọn (from; không có thì lỗi). Bcc không xuất hiện trong header (người gửi thư dùng danh sách phong bì từ
// Envelope). Mọi giá trị header đã qua Validate; Build kiểm lại từng giá trị tự sinh. Nội dung văn bản mã hóa quoted-printable, tệp đính kèm base64 (dòng 76).
// Cấu trúc: multipart/mixed[ multipart/related[ multipart/alternative[text, html, calendar], phần cid ], tệp còn lại ]; bỏ các tầng không cần.
func Build(m Message, from Address, now time.Time, l Limits) ([]byte, error) {
	if from.Email == "" {
		return nil, &Error{Kind: KindConfig, Msg: "No sender address available"}
	}
	if err := checkAddress(from); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	h := func(k, v string) { fmt.Fprintf(&buf, "%s: %s\r\n", k, v) }
	h("Date", now.UTC().Format(time.RFC1123Z))
	h("From", from.String())
	h("To", joinAddrs(m.To))
	if len(m.Cc) > 0 {
		h("Cc", joinAddrs(m.Cc))
	}
	if len(m.ReplyTo) > 0 {
		h("Reply-To", joinAddrs(m.ReplyTo))
	}
	h("Subject", mime.QEncoding.Encode("utf-8", m.Subject))
	h("Message-ID", "<"+randHex(16)+"@"+domainOf(from.Email)+">")
	h("MIME-Version", "1.0")

	var inline, other []Attachment
	for _, a := range m.Attachments {
		if a.CID != "" {
			inline = append(inline, a)
		} else {
			other = append(other, a)
		}
	}
	bodyParts := textParts(m)
	root, err := nest(bodyParts, inline, other, l.bytes())
	if err != nil {
		return nil, err
	}
	buf.Write(root)
	if int64(buf.Len()) > l.bytes() {
		return nil, &Error{Kind: KindInvalid, Msg: "Message is too large"}
	}
	return buf.Bytes(), nil
}

// Envelope trả địa chỉ phong bì: người gửi (địa chỉ trần) và mọi người nhận gồm cả Bcc, bỏ trùng.
func Envelope(m Message, from Address) (sender string, rcpt []string) {
	seen := map[string]bool{}
	for _, list := range [][]Address{m.To, m.Cc, m.Bcc} {
		for _, a := range list {
			k := strings.ToLower(a.Email)
			if !seen[k] {
				seen[k] = true
				rcpt = append(rcpt, a.Email)
			}
		}
	}
	return from.Email, rcpt
}

func joinAddrs(list []Address) string {
	s := make([]string, len(list))
	for i, a := range list {
		s[i] = a.String()
	}
	return strings.Join(s, ", ")
}

func domainOf(email string) string {
	if i := strings.LastIndex(email, "@"); i >= 0 && i+1 < len(email) {
		return email[i+1:]
	}
	return "localhost"
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// part là một phần MIME đã mã hóa, kèm header.
type part struct {
	header textproto.MIMEHeader
	body   []byte
}

func qp(contentType string, text string) part {
	var b bytes.Buffer
	w := quotedprintable.NewWriter(&b)
	_, _ = w.Write([]byte(strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\n", "\r\n")))
	_ = w.Close()
	return part{header: textproto.MIMEHeader{"Content-Type": {contentType + "; charset=utf-8"}, "Content-Transfer-Encoding": {"quoted-printable"}}, body: b.Bytes()}
}

func textParts(m Message) []part {
	var out []part
	if m.Text != "" || (m.HTML == "" && m.ICal == nil) {
		out = append(out, qp("text/plain", m.Text))
	}
	if m.HTML != "" {
		out = append(out, qp("text/html", m.HTML))
	}
	if m.ICal != nil {
		ct := "text/calendar"
		if m.ICal.Method != "" {
			ct += "; method=" + strings.ToUpper(m.ICal.Method)
		}
		p := qp(ct, m.ICal.Content)
		if m.ICal.Filename != "" {
			p.header.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": m.ICal.Filename}))
		}
		out = append(out, p)
	}
	return out
}

func attachmentPart(a Attachment, limit int64) (part, error) {
	var data []byte
	if a.Body != nil {
		var err error
		if data, err = io.ReadAll(io.LimitReader(a.Body, limit+1)); err != nil {
			return part{}, &Error{Kind: KindInvalid, Msg: "Failed to read attachment", Err: err}
		}
		if int64(len(data)) > limit {
			return part{}, &Error{Kind: KindInvalid, Msg: "Attachment is too large"}
		}
	}
	ct := a.ContentType
	if ct == "" {
		ct = "application/octet-stream"
	}
	if mt, params, err := mime.ParseMediaType(ct); err != nil {
		return part{}, &Error{Kind: KindInvalid, Msg: "Invalid attachment content type"}
	} else {
		ct = mime.FormatMediaType(mt, params)
	}
	disp := a.Disposition
	if disp == "" {
		disp = "attachment"
		if a.CID != "" {
			disp = "inline"
		}
	}
	if disp != "attachment" && disp != "inline" {
		return part{}, &Error{Kind: KindInvalid, Msg: "Invalid attachment disposition"}
	}
	params := map[string]string{}
	if a.Filename != "" {
		params["filename"] = a.Filename
	}
	hd := textproto.MIMEHeader{"Content-Type": {ct}, "Content-Transfer-Encoding": {"base64"}, "Content-Disposition": {mime.FormatMediaType(disp, params)}}
	if a.CID != "" {
		hd.Set("Content-ID", "<"+strings.Trim(a.CID, "<>")+">")
	}
	enc := base64.StdEncoding.EncodeToString(data)
	var b bytes.Buffer
	for len(enc) > 76 {
		b.WriteString(enc[:76] + "\r\n")
		enc = enc[76:]
	}
	b.WriteString(enc)
	return part{header: hd, body: b.Bytes()}, nil
}

// writeSingle ghi một phần (header theo thứ tự cố định, dòng trống, thân).
func writeSingle(p part) []byte {
	var b bytes.Buffer
	keys := []string{"Content-Type", "Content-Transfer-Encoding", "Content-Disposition", "Content-Id"}
	for _, k := range keys {
		for _, v := range p.header[textproto.CanonicalMIMEHeaderKey(k)] {
			fmt.Fprintf(&b, "%s: %s\r\n", k, v)
		}
	}
	b.WriteString("\r\n")
	b.Write(p.body)
	b.WriteString("\r\n")
	return b.Bytes()
}

func multipartOf(kind string, children [][]byte) []byte {
	boundary := "=_" + randHex(12)
	// multipart.Writer không cho ghi sẵn thân đã có header, nên ghép tay theo RFC 2046.
	out := &bytes.Buffer{}
	fmt.Fprintf(out, "Content-Type: multipart/%s; boundary=\"%s\"\r\n\r\n", kind, boundary)
	for _, c := range children {
		fmt.Fprintf(out, "--%s\r\n", boundary)
		out.Write(c)
	}
	fmt.Fprintf(out, "--%s--\r\n", boundary)
	return out.Bytes()
}

// nest dựng cây MIME; mỗi nút trả về bytes gồm header của nó và thân.
func nest(text []part, inline, other []Attachment, budget int64) ([]byte, error) {
	var node []byte
	if len(text) == 1 {
		node = writeSingle(text[0])
	} else {
		var kids [][]byte
		for _, p := range text {
			kids = append(kids, writeSingle(p))
		}
		node = multipartOf("alternative", kids)
	}
	if len(inline) > 0 {
		kids := [][]byte{node}
		for _, a := range inline {
			p, err := attachmentPart(a, budget)
			if err != nil {
				return nil, err
			}
			kids = append(kids, writeSingle(p))
		}
		node = multipartOf("related", kids)
	}
	if len(other) > 0 {
		kids := [][]byte{node}
		for _, a := range other {
			p, err := attachmentPart(a, budget)
			if err != nil {
				return nil, err
			}
			kids = append(kids, writeSingle(p))
		}
		node = multipartOf("mixed", kids)
	}
	return node, nil
}
