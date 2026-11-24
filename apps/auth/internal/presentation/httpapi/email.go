package httpapi

import (
	"mime"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	"trivita-fhir/apps/auth/internal/application"
	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/mailer"
)

// registerEmail đăng ký POST /email/v1/send (mặc định TẮT: AUTH_EMAIL_ENABLED). Chưa xác thực là 401 trước cả validator; hạn mức dùng chung với bước nhập thông tin đăng nhập.
func registerEmail(e *echo.Echo, dep Deps) {
	h := &authHandlers{svc: dep.Auth, cookieSecure: dep.CookieSecure}
	mw := []echo.MiddlewareFunc{requireBearer(dep.Authenticator)}
	if dep.LoginRateLimit > 0 {
		mw = append(mw, rateLimiter(dep.rateStore("login:email", dep.LoginRateLimit, dep.LoginRateBurst), nil))
	}
	e.POST("/email/v1/send", h.sendEmail, mw...)
}

// EmailAttachmentBody là một tệp đính kèm của SendEmailBody.
type EmailAttachmentBody struct {
	Filename           any    `json:"filename,omitempty" swaggertype:"string" example:"ketqua.pdf"`                // tên tệp hiển thị; phải là chuỗi nếu có
	Content            string `json:"content,omitempty" example:"SGVsbG8="`                                        // nội dung nhúng, mã hóa theo encoding; dùng content hoặc path
	Path               string `json:"path,omitempty" example:"https://example.org/file.pdf"`                       // URL https hoặc Binary/<id> (đọc từ FHIR bằng token người gọi); đường dẫn khác bị từ chối
	ContentType        string `json:"contentType,omitempty" example:"application/pdf"`                             // kiểu MIME của tệp
	ContentDisposition string `json:"contentDisposition,omitempty" enums:"attachment,inline" example:"attachment"` // attachment: tệp đính kèm; inline: nhúng trong thân thư (dùng với cid)
	CID                string `json:"cid,omitempty" example:"logo@example.org"`                                    // Content-ID để thân HTML tham chiếu (cid:...) khi inline
	Encoding           string `json:"encoding,omitempty" enums:"utf8,base64" example:"base64"`                     // cách mã hóa của content
}

// SendEmailBody là body của POST /email/v1/send. Mỗi địa chỉ (from, to, cc, bcc, replyTo) là chuỗi ("a@b" hoặc "Tên <a@b>", cho phép danh sách phân cách dấu phẩy),
// đối tượng {name, address} hoặc mảng của chúng. Trường ngoài danh sách (envelope, raw, headers, dkim, auth, sender...) bị bỏ qua.
type SendEmailBody struct {
	From        any                   `json:"from,omitempty" swaggertype:"string" example:"Trivita <no-reply@example.org>"` // người gửi, đúng một địa chỉ; chỉ dùng nếu thuộc danh sách được duyệt, không thì dùng địa chỉ mặc định
	To          any                   `json:"to" binding:"required" swaggertype:"string" example:"an@example.org"`          // người nhận; bắt buộc, không rỗng
	Cc          any                   `json:"cc,omitempty" swaggertype:"string" example:"cc@example.org"`                   // người nhận bản sao (tùy chọn)
	Bcc         any                   `json:"bcc,omitempty" swaggertype:"string" example:"bcc@example.org"`                 // người nhận ẩn (tùy chọn)
	ReplyTo     any                   `json:"replyTo,omitempty" swaggertype:"string" example:"hotro@example.org"`           // địa chỉ nhận trả lời (tùy chọn)
	Subject     string                `json:"subject" binding:"required" example:"Kết quả xét nghiệm"`                      // tiêu đề thư; bắt buộc, không rỗng, không chứa xuống dòng
	Text        string                `json:"text,omitempty" example:"Xin chào, kết quả đã sẵn sàng."`                      // thân thư dạng văn bản thuần (tùy chọn)
	HTML        string                `json:"html,omitempty" example:"<p>Xin chào</p>"`                                     // thân thư dạng HTML (tùy chọn)
	Attachments []EmailAttachmentBody `json:"attachments,omitempty"`                                                        // các tệp đính kèm (tùy chọn)
	ICalEvent   any                   `json:"icalEvent,omitempty" swaggertype:"string" example:"BEGIN:VCALENDAR..."`        // sự kiện iCal: chuỗi nội dung hoặc đối tượng {content, method, filename}
}

// sendEmail godoc
//
//	@Summary		Gửi thư (admin project)
//	@Description	Chỉ admin của project có tính năng `email`. Thứ tự lỗi: 401 chưa xác thực, 400 sai kiểu (gộp mọi lỗi), 400 `Email feature is not enabled for this project`, 403 `Only project administrators can send emails`, 400 cấu hình SMTP của project dở, 400 `Email is not configured on this server. Set up SMTP.`, 400 tệp đính kèm lỗi, 502 SMTP lỗi (thông điệp chung). Người gửi `from` chỉ được dùng nếu thuộc danh sách được duyệt, không thì dùng địa chỉ mặc định. Đính kèm: nội dung nhúng (content + encoding) hoặc URL https (path); `Binary/<id>` đọc từ dịch vụ FHIR bằng token của người gọi (cần AUTH_EMAIL_FHIR_URL, không thì 400 chưa hỗ trợ; không tồn tại hoặc không có quyền thì 400 và không gửi); đường dẫn khác bị từ chối. Mọi giá trị header chứa xuống dòng bị từ chối.
//	@Tags			email
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		application/fhir+json
//	@Param			body	body		SendEmailBody	true	"Thư"
//	@Success		200		{object}	fhir.OperationOutcome
//	@Failure		400		{object}	fhir.OperationOutcome
//	@Failure		401		{object}	fhir.OperationOutcome
//	@Failure		403		{object}	fhir.OperationOutcome
//	@Failure		502		{object}	fhir.OperationOutcome
//	@Router			/email/v1/send [post]
func (h *authHandlers) sendEmail(c echo.Context) error {
	if mt, _, err := mime.ParseMediaType(c.Request().Header.Get(echo.HeaderContentType)); err != nil || mt != "application/json" {
		return writeError(c, domain.BadRequest("Content-Type must be application/json"))
	}
	b, err := readBody(c)
	if err != nil {
		return writeError(c, err)
	}
	req, verr := parseEmailBody(b)
	if verr != nil {
		return writeError(c, verr)
	}
	req.BearerToken, _ = bearerToken(c.Request()) // chỉ để đọc Binary/<id> bằng quyền của người gọi
	if err := h.svc.SendEmail(c.Request().Context(), authState(c), req); err != nil {
		return writeError(c, err)
	}
	return writeOutcome(c, http.StatusOK, outcome("information", "informational", "All OK"))
}

// parseEmailBody kiểm kiểu mọi trường (gộp lỗi thành một OperationOutcome nhiều issue, như Medplum) và dựng yêu cầu. Trường lạ bị bỏ qua.
func parseEmailBody(b body) (application.EmailRequest, error) {
	v := &validationError{}
	var req application.EmailRequest
	m := &req.Message

	addrs := func(field, label string, required bool) []mailer.Address {
		raw, ok := b[field]
		if !ok || raw == nil {
			if required {
				v.add(label+" is required", field)
			}
			return nil
		}
		out, good := parseAddresses(raw)
		if !good || (required && len(out) == 0) {
			if required && (raw == "" || isEmptyList(raw)) {
				v.add(label+" is required", field)
			} else {
				v.add(label+" must be an email address, an object with an address, or an array of them", field)
			}
			return nil
		}
		return out
	}
	m.To = addrs("to", "To", true)
	m.Cc, m.Bcc, m.ReplyTo = addrs("cc", "Cc", false), addrs("bcc", "Bcc", false), addrs("replyTo", "ReplyTo", false)
	if raw, ok := b["from"]; ok && raw != nil {
		out, good := parseAddresses(raw)
		if !good || len(out) != 1 {
			v.add("From must be a single email address", "from")
		} else {
			m.From = &out[0]
		}
	}
	// subject
	switch sv, has := b["subject"]; {
	case !has || sv == nil || sv == "":
		v.add("Subject is required", "subject")
	default:
		if s, ok := sv.(string); !ok {
			v.add("Subject must be a string", "subject")
		} else {
			m.Subject = s
		}
	}
	for _, f := range []struct {
		key, msg string
		dst      *string
	}{{"text", "Text must be a string", &m.Text}, {"html", "HTML must be a string", &m.HTML}} {
		if raw, ok := b[f.key]; ok && raw != nil {
			if s, ok := raw.(string); !ok {
				v.add(f.msg, f.key)
			} else {
				*f.dst = s
			}
		}
	}
	// attachments
	if raw, ok := b["attachments"]; ok && raw != nil {
		list, isList := raw.([]any)
		if !isList {
			v.add("Attachments must be an array", "attachments")
		}
		for _, it := range list {
			obj, isObj := it.(map[string]any)
			if !isObj {
				v.add("Attachment must be an object", "attachments")
				continue
			}
			at := application.EmailAttachment{}
			propsOK := true
			for _, p := range []struct {
				key string
				dst *string
			}{{"content", &at.Content}, {"path", &at.Path}, {"contentType", &at.ContentType}, {"contentDisposition", &at.Disposition}, {"cid", &at.CID}, {"encoding", &at.Encoding}} {
				if pv, has := obj[p.key]; has && pv != nil {
					s, ok := pv.(string)
					if !ok {
						propsOK = false
						continue
					}
					*p.dst = s
				}
			}
			if !propsOK {
				v.add("Attachment properties must be strings", "attachments")
			}
			if fv, has := obj["filename"]; has && fv != nil {
				switch f := fv.(type) {
				case string:
					at.Filename = f
				case bool:
					if f { // chỉ false hợp lệ (như Medplum: chuỗi hoặc false)
						v.add("Attachment filename must be a string", "attachments")
					}
				default:
					v.add("Attachment filename must be a string", "attachments")
				}
			}
			req.Attachments = append(req.Attachments, at)
		}
	}
	// icalEvent
	if raw, ok := b["icalEvent"]; ok && raw != nil {
		switch ic := raw.(type) {
		case string:
			m.ICal = &mailer.ICalEvent{Content: ic}
		case map[string]any:
			content, cok := ic["content"].(string)
			if !cok {
				v.add("icalEvent must be a string or an object with string content", "icalEvent")
				break
			}
			ev := &mailer.ICalEvent{Content: content}
			good := true
			for _, p := range []struct {
				key string
				dst *string
			}{{"method", &ev.Method}, {"filename", &ev.Filename}} {
				if pv, has := ic[p.key]; has && pv != nil {
					s, ok := pv.(string)
					if !ok {
						good = false
						continue
					}
					*p.dst = s
				}
			}
			if !good {
				v.add("icalEvent properties must be strings", "icalEvent")
			} else {
				m.ICal = ev
			}
		default:
			v.add("icalEvent must be a string or an object with string content", "icalEvent")
		}
	}
	if err := v.orNil(); err != nil {
		return application.EmailRequest{}, err
	}
	return req, nil
}

func isEmptyList(v any) bool {
	l, ok := v.([]any)
	return ok && len(l) == 0
}

// parseAddresses đọc chuỗi (một hoặc nhiều địa chỉ), đối tượng {name, address} hoặc mảng; phần tử null, rỗng hay sai kiểu làm cả trường sai.
func parseAddresses(v any) ([]mailer.Address, bool) {
	switch x := v.(type) {
	case string:
		out, err := mailer.ParseAddressList(x)
		return out, err == nil
	case map[string]any:
		email, ok := x["address"].(string)
		if !ok {
			return nil, false
		}
		name, _ := x["name"].(string)
		if _, has := x["name"]; has && x["name"] != nil {
			if _, isStr := x["name"].(string); !isStr {
				return nil, false
			}
		}
		a, err := mailer.ParseAddress(email)
		if err != nil || a.Name != "" {
			return nil, false
		}
		if strings.ContainsAny(name, "\r\n\x00") {
			return nil, false
		}
		return []mailer.Address{{Name: name, Email: a.Email}}, true
	case []any:
		var out []mailer.Address
		for _, it := range x {
			sub, ok := parseAddresses(it)
			if !ok {
				return nil, false
			}
			out = append(out, sub...)
		}
		return out, true
	}
	return nil, false
}
