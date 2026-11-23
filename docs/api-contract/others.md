# API khác

> **Trạng thái: CHƯA CÀI trong fhir-server Go.** Tài liệu này mô tả server Medplum bản TypeScript gốc, chưa phải hợp đồng của server đang chạy. Route Go thật xem `fhir-rest.md` và `/swagger/index.html`. Không viết DTO cho các route dưới đây cho tới khi chúng được port sang Go.

CDS Hooks, email, key-value, webhook, MCP, SCIM, FHIRcast, well-known. Nguồn: các `routes.ts` trong từng module.

| Method | Path | Xác thực |
|---|---|---|
| GET | `/.well-known/api-catalog` | không guard ở route (xem middleware cha) |
| GET | `/.well-known/jwks.json` | không guard ở route (xem middleware cha) |
| GET | `/.well-known/oauth-authorization-server` | không guard ở route (xem middleware cha) |
| GET | `/.well-known/oauth-protected-resource` | không guard ở route (xem middleware cha) |
| GET | `/.well-known/openid-configuration` | không guard ở route (xem middleware cha) |
| GET | `/.well-known/smart-configuration` | không guard ở route (xem middleware cha) |
| GET | `/.well-known/smart-styles.json` | không guard ở route (xem middleware cha) |
| GET | `/cds-services/` | không guard ở route (xem middleware cha) |
| POST | `/cds-services/:id` | không guard ở route (xem middleware cha) |
| GET | `/dicomweb/:bulkdataUriReference` | không guard ở route (xem middleware cha) |
| GET | `/dicomweb/studies` | không guard ở route (xem middleware cha) |
| POST | `/dicomweb/studies` | không guard ở route (xem middleware cha) |
| GET | `/dicomweb/studies/:studyUid` | không guard ở route (xem middleware cha) |
| POST | `/dicomweb/studies/:studyUid` | không guard ở route (xem middleware cha) |
| GET | `/dicomweb/studies/:studyUid/metadata` | không guard ở route (xem middleware cha) |
| GET | `/dicomweb/studies/:studyUid/rendered` | không guard ở route (xem middleware cha) |
| GET | `/dicomweb/studies/:studyUid/series` | không guard ở route (xem middleware cha) |
| GET | `/dicomweb/studies/:studyUid/series/:series` | không guard ở route (xem middleware cha) |
| GET | `/dicomweb/studies/:studyUid/series/:seriesUid/instances` | không guard ở route (xem middleware cha) |
| GET | `/dicomweb/studies/:studyUid/series/:seriesUid/instances/:instance` | không guard ở route (xem middleware cha) |
| GET | `/dicomweb/studies/:studyUid/series/:seriesUid/instances/:instance/metadata` | không guard ở route (xem middleware cha) |
| GET | `/dicomweb/studies/:studyUid/series/:seriesUid/instances/:instance/rendered` | không guard ở route (xem middleware cha) |
| GET | `/dicomweb/studies/:studyUid/series/:seriesUid/instances/:instanceUid/frames/:frame` | không guard ở route (xem middleware cha) |
| GET | `/dicomweb/studies/:studyUid/series/:seriesUid/metadata` | không guard ở route (xem middleware cha) |
| GET | `/dicomweb/studies/:studyUid/series/:seriesUid/rendered` | không guard ở route (xem middleware cha) |
| POST | `/email/v1/send` | không guard ở route (xem middleware cha) |
| GET | `/fhir/R4/.well-known/smart-configuration` | không guard ở route (xem middleware cha) |
| GET | `/fhir/R4/.well-known/smart-styles.json` | không guard ở route (xem middleware cha) |
| POST | `/fhir/R4/Agent/$push` | Bearer |
| POST | `/fhir/R4/Agent/:id/$push` | Bearer |
| DELETE | `/fhir/R4/bulkdata/export/:id` | không guard ở route (xem middleware cha) |
| GET | `/fhir/R4/bulkdata/export/:id` | không guard ở route (xem middleware cha) |
| DELETE | `/fhir/R4/job/:id/status` | không guard ở route (xem middleware cha) |
| GET | `/fhir/R4/job/:id/status` | không guard ở route (xem middleware cha) |
| GET | `/fhir/R4/metadata` | không guard ở route (xem middleware cha) |
| DELETE | `/keyvalue/v1/:key` | không guard ở route (xem middleware cha) |
| GET | `/keyvalue/v1/:key` | không guard ở route (xem middleware cha) |
| PUT | `/keyvalue/v1/:key` | không guard ở route (xem middleware cha) |
| GET | `/mcp/sse` | không guard ở route (xem middleware cha) |
| POST | `/mcp/sse` | không guard ở route (xem middleware cha) |
| ALL | `/mcp/stream` | không guard ở route (xem middleware cha) |
| GET | `/scim/v2/Users` | không guard ở route (xem middleware cha) |
| POST | `/scim/v2/Users` | không guard ở route (xem middleware cha) |
| DELETE | `/scim/v2/Users/:id` | không guard ở route (xem middleware cha) |
| GET | `/scim/v2/Users/:id` | không guard ở route (xem middleware cha) |
| PATCH | `/scim/v2/Users/:id` | không guard ở route (xem middleware cha) |
| PUT | `/scim/v2/Users/:id` | không guard ở route (xem middleware cha) |
| POST | `/webhook/:id` | không guard ở route (xem middleware cha) |

WebSocket (`server/src/ws/routes.ts`): handler chọn theo phân đoạn thứ hai của URL, gồm `/ws/subscriptions-r4`, `/ws/agent`, `/ws/fhircast`, `/ws/ai-realtime`, `/ws/echo` (tiền tố `/api` và `/projects/:projectId` được chuẩn hóa).

DICOMweb (`server/src/dicom`): gốc `/dicomweb/` (cũng qua `/api/dicomweb/` và `/projects/:projectId/dicomweb/`).

| Method | Path | Trạng thái |
|---|---|---|
| POST | `/dicomweb/studies`, `/dicomweb/studies/:studyUid` | STOW-RS lưu instance |
| GET | `/dicomweb/studies` | QIDO-RS tìm study |
| GET | `/dicomweb/studies/:studyUid/series` | QIDO-RS tìm series |
| GET | `/dicomweb/studies/:studyUid/series/:seriesUid/metadata` | WADO-RS metadata series |
| GET | các route còn lại (`rendered`, `instances`, ...) | chưa cài (stub `notImplemented`) |

---

## `POST /email/v1/send` (hợp đồng thật của `apps/auth`, E4, 2026-11-22)

Mặc định **TẮT** (`AUTH_EMAIL_ENABLED=true` để bật; tắt thì 404). DTO ở swagger của auth (tag `email`). Bearer bắt buộc; người gọi phải là admin của project và project phải có tính năng `email` (`auth project feature -project <id> -feature email`, hoặc `AUTH_DEFAULT_PROJECT_FEATURES=email` lúc seed). Hạn mức chung với bước đăng nhập (`AUTH_LOGIN_RATE_LIMIT`).

Thứ tự kiểm tra (thông điệp lộ thứ tự): 401 chưa xác thực; 400 `Content-Type must be application/json`; 400 sai kiểu (gộp mọi lỗi thành một OperationOutcome nhiều issue: `To is required`, `Subject is required`, `Subject must be a string`, `Text must be a string`, `HTML must be a string`, `Attachments must be an array`, `Attachment must be an object`, `Attachment properties must be strings`, `Attachment filename must be a string`, `icalEvent must be a string or an object with string content`, `icalEvent properties must be strings`, và `<Trường> must be an email address, an object with an address, or an array of them`); 400 `Email feature is not enabled for this project`; 403 `Only project administrators can send emails`; 400 `Project SMTP configuration is incomplete or invalid` (SMTP project khai dở); 400 `Email is not configured on this server. Set up SMTP.`; 400 lỗi tệp đính kèm; 200 `All OK`; 502 `Failed to send email` (SMTP lỗi, không lộ phản hồi của máy chủ).

Body: `to` (bắt buộc), `cc`, `bcc`, `replyTo`, `from`, `subject` (bắt buộc), `text`, `html`, `attachments[]`, `icalEvent`. Địa chỉ: chuỗi (`a@b`, `Tên <a@b>`, hoặc danh sách phân cách dấu phẩy), đối tượng `{name, address}`, hoặc mảng của chúng; phần tử null, rỗng hay sai kiểu là 400; `null` ở cả trường được coi như vắng. Trường ngoài danh sách (`envelope`, `raw`, `headers`, `dkim`, `auth`, `sender`, `disableFileAccess`, `__proto__`...) bị **bỏ qua** (không thể vào thư). Mọi giá trị đi vào header chứa CR/LF/NUL là 400.

- `from`: chỉ được dùng nếu địa chỉ trần thuộc danh sách được duyệt (`AUTH_SMTP_APPROVED_SENDERS` cho SMTP server, `smtpApprovedSenders` cho SMTP project; so khớp không phân biệt hoa thường, cắt khoảng trắng; hai danh sách không trộn); không thì dùng người gửi mặc định (`AUTH_SMTP_FROM` hoặc `smtpFromAddress`). Phong bì luôn là địa chỉ trần. Tên hiển thị `appName` của project chỉ gắn khi dùng SMTP riêng của project.
- `attachments[]`: `filename` (chuỗi hoặc `false`), `content` (+ `encoding` `utf8`|`base64`), `contentType`, `contentDisposition`, `cid`, hoặc `path`: URL **https** (tải bằng client chặn SSRF, không theo redirect, timeout 30s, tối đa `AUTH_EMAIL_MAX_BYTES` mỗi tệp; lỗi: `Failed to fetch attachment: ...`, chặn host nội bộ: `Outbound request to unsafe hostname <host> is blocked`). `Binary/<id>` (đúng dạng này: không version, không query; id `[A-Za-z0-9.-]{1,64}`) được đọc từ dịch vụ FHIR **bằng chính Bearer token của người gọi** (nên theo quyền, project và `securityContext` của họ; token phải được fhir-server chấp nhận) khi cấu hình `AUTH_EMAIL_FHIR_URL`; lỗi (đều 400 và **không gửi thư**, kể cả khi các tệp khác tốt): `Attachment not found: Binary/<id>` (không có, đã xóa, hoặc thuộc project khác), `Not authorized to read attachment: Binary/<id>`, `Attachment is too large`; dịch vụ FHIR lỗi hoặc không với tới: 502 `Failed to read attachment`; `contentType` lấy từ Binary nếu yêu cầu không nêu; chưa cấu hình `AUTH_EMAIL_FHIR_URL` thì 400 `Binary attachments are not supported`; mọi `path` khác trả 400 `Invalid email options: File access rejected for <path>` (không bao giờ đọc tệp cục bộ). Tối đa 20 tệp. Body request tối đa 1 MiB.
- `icalEvent`: chuỗi (nội dung) hoặc `{content, method, filename}`; thành phần `text/calendar`.
- Giới hạn: tổng người nhận (`to`+`cc`+`bcc`) `AUTH_EMAIL_MAX_RECIPIENTS` (50), cỡ thư `AUTH_EMAIL_MAX_BYTES` (10 MiB): vượt là 400.
- SMTP riêng của project (`Project.secret`: `smtpHost`, `smtpPort`, `smtpUsername`, `smtpPassword`, `smtpFromAddress`, tùy chọn `smtpSecure`, `smtpApprovedSenders`) chỉ dùng khi `AUTH_EMAIL_ALLOW_PROJECT_SMTP=true` (mặc định tắt: mật khẩu nằm dạng rõ trong `Project.secret`, Q-E7), nối qua dialer chặn SSRF (host nội bộ: 400 chung, không nối), trừ `AUTH_EMAIL_ALLOW_UNSAFE_OUTBOUND` (dev; cấm ở production). Chưa có API đặt `Project.secret` ở auth.

Khác Medplum (có chủ ý): bỏ SES khỏi thông điệp; địa chỉ sai là 400; `Binary/<id>` đọc qua fhir-server bằng token của người gọi (cần `AUTH_EMAIL_FHIR_URL`); tải URL không theo redirect và có giới hạn byte; có giới hạn người nhận/kích thước; lỗi SMTP là 502 chung; SMTP project tắt mặc định; subject không bao giờ được ghi vào log.

