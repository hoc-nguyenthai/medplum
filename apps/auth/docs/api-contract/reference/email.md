# Tham chiếu API: nhóm `email`

> **Tệp này được SINH từ `swagger.json`** (Trivita Auth API); đừng sửa tay. Cập nhật: `UPDATE_DOCS=1 go test` ở thư mục `docs`. Điều kiện nào ra phản hồi nào (từng trường hợp, ví dụ JSON, header) ở [../cases/email.md](../cases/email.md).

Gửi thư POST /email/v1/send (tắt mặc định, AUTH_EMAIL_ENABLED). Chỉ admin project có tính năng email.

**Header phản hồi chung (mọi route):** `Cache-Control: no-store, no-cache, must-revalidate`, `Pragma: no-cache`, `Content-Security-Policy: default-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none';`, `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`, `X-Request-Id`; `Strict-Transport-Security` khi `AUTH_BASE_URL` là https. Chi tiết và các envelope lỗi dùng chung: [../common.md](../common.md).

| Route | Tóm tắt |
|---|---|
| [`POST /email/v1/send`](#post-email-v1-send) | Gửi thư (admin project) |

---

<a id="post-email-v1-send"></a>
## `POST /email/v1/send`

**Gửi thư (admin project)**

Chỉ admin của project có tính năng `email`. Thứ tự lỗi: 401 chưa xác thực, 400 sai kiểu (gộp mọi lỗi), 400 `Email feature is not enabled for this project`, 403 `Only project administrators can send emails`, 400 cấu hình SMTP của project dở, 400 `Email is not configured on this server. Set up SMTP.`, 400 tệp đính kèm lỗi, 502 SMTP lỗi (thông điệp chung). Người gửi `from` chỉ được dùng nếu thuộc danh sách được duyệt, không thì dùng địa chỉ mặc định. Đính kèm: nội dung nhúng (content + encoding) hoặc URL https (path); `Binary/<id>` đọc từ dịch vụ FHIR bằng token của người gọi (cần AUTH_EMAIL_FHIR_URL, không thì 400 chưa hỗ trợ; không tồn tại hoặc không có quyền thì 400 và không gửi); đường dẫn khác bị từ chối. Mọi giá trị header chứa xuống dòng bị từ chối.

Các trường hợp phản hồi: [../cases/email.md#post-email-v1-send](../cases/email.md#post-email-v1-send)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | `application/json` |
| Content-Type phản hồi | `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Content-Type` | có | `application/json` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Body yêu cầu** (Thư)

Schema: `httpapi.SendEmailBody`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `subject` | string | có |  | tiêu đề thư; bắt buộc, không rỗng, không chứa xuống dòng | `"Kết quả xét nghiệm"` |
| `to` | string | có |  | người nhận; bắt buộc, không rỗng | `"an@example.org"` |
| `attachments` | mảng `httpapi.EmailAttachmentBody` | không |  | các tệp đính kèm (tùy chọn) |  |
| `attachments[].cid` | string | không |  | Content-ID để thân HTML tham chiếu (cid:...) khi inline | `"logo@example.org"` |
| `attachments[].content` | string | không |  | nội dung nhúng, mã hóa theo encoding; dùng content hoặc path | `"SGVsbG8="` |
| `attachments[].contentDisposition` | string | không | một trong: `attachment`, `inline` | attachment: tệp đính kèm; inline: nhúng trong thân thư (dùng với cid) | `"attachment"` |
| `attachments[].contentType` | string | không |  | kiểu MIME của tệp | `"application/pdf"` |
| `attachments[].encoding` | string | không | một trong: `utf8`, `base64` | cách mã hóa của content | `"base64"` |
| `attachments[].filename` | string | không |  | tên tệp hiển thị; phải là chuỗi nếu có | `"ketqua.pdf"` |
| `attachments[].path` | string | không |  | URL https hoặc Binary/<id> (đọc từ FHIR bằng token người gọi); đường dẫn khác bị từ chối | `"https://example.org/file.pdf"` |
| `bcc` | string | không |  | người nhận ẩn (tùy chọn) | `"bcc@example.org"` |
| `cc` | string | không |  | người nhận bản sao (tùy chọn) | `"cc@example.org"` |
| `from` | string | không |  | người gửi, đúng một địa chỉ; chỉ dùng nếu thuộc danh sách được duyệt, không thì dùng địa chỉ mặc định | `"Trivita \u003cno-reply@example.org\u003e"` |
| `html` | string | không |  | thân thư dạng HTML (tùy chọn) | `"\u003cp\u003eXin chào\u003c/p\u003e"` |
| `icalEvent` | string | không |  | sự kiện iCal: chuỗi nội dung hoặc đối tượng {content, method, filename} | `"BEGIN:VCALENDAR..."` |
| `replyTo` | string | không |  | địa chỉ nhận trả lời (tùy chọn) | `"hotro@example.org"` |
| `text` | string | không |  | thân thư dạng văn bản thuần (tùy chọn) | `"Xin chào, kết quả đã sẵn sàng."` |

```json
{
  "attachments": [
    {
      "cid": "logo@example.org",
      "content": "SGVsbG8=",
      "contentDisposition": "attachment",
      "contentType": "application/pdf",
      "encoding": "base64",
      "filename": "ketqua.pdf",
      "path": "https://example.org/file.pdf"
    }
  ],
  "bcc": "bcc@example.org",
  "cc": "cc@example.org",
  "from": "Trivita \u003cno-reply@example.org\u003e",
  "html": "\u003cp\u003eXin chào\u003c/p\u003e",
  "icalEvent": "BEGIN:VCALENDAR...",
  "replyTo": "hotro@example.org",
  "subject": "Kết quả xét nghiệm",
  "text": "Xin chào, kết quả đã sẵn sàng.",
  "to": "an@example.org"
}
```

**Phản hồi**

**`200`** OK

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`400`** Bad Request

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`401`** Unauthorized

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`403`** Forbidden

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).

**`502`** Bad Gateway

Schema: `fhir.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-operationoutcome)).
