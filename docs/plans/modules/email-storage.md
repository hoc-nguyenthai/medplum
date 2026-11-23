# Kế hoạch port `email` và `storage` của Medplum sang Go

Nguồn: Medplum v5.2.18, `~/Downloads/medplum/packages/server/src` (chỉ đọc). Mọi `file:dòng` bên dưới là **tương đối với thư mục đó** nếu không ghi khác; `apps/...` và `pkg/...` là đường dẫn trong repo này. Đích: monorepo Go `trivita-fhir`.

Quy ước: điều **đọc thấy trong source** ghi kèm `file:dòng`; điều **đề xuất của tôi** ghi rõ "đề xuất"; điều không xác nhận được ghi "chưa xác nhận". Tài liệu này là kế hoạch, không có thay đổi mã nào đi kèm. Tài liệu nền đã đọc: `docs/plans/modules-survey.md` (mục 2, 7), `docs/plans/coverage-matrix.md`, `docs/plans/cds-dicom/README.md` và `dicom.md`, `docs/plans/auth/README.md` (D-4) và `p4-p5-register-mfa.md`, `architecture_test.go`.

Phạm vi Medplum đã đọc đủ: `email/{email,routes,utils}.ts` + 3 file test; `storage/{base,filesystem,loader,presign,routes,types,utils}.ts` + `routes.test.ts`, `filesystem.test.ts`; `cloud/aws/{storage,email,config}.ts`, `cloud/azure/storage.ts`, `cloud/gcp/storage.ts` (đọc đủ để quyết định bỏ); `fhir/binary.ts` + `binary.test.ts`; `fhir/operations/binary-presigned-url.ts` + test; `fhir/operations/binary-scan.ts`; `fhir/rewrite.ts` + `rewrite.test.ts`; `config/{types,utils,loader}.ts` (các khóa liên quan); `util/url.ts`, `util/validator.ts`; phần liên quan trong `app.ts`, `fhir/response.ts`, `fhir/repo.ts` (Binary).

---

## 0. Quyết định bố cục và tóm tắt

### 0.1 Bốn gói dùng chung mới (chi tiết giao diện ở mục 12)

| Gói | Vai trò | Người dùng |
|---|---|---|
| `pkg/safehttp` | Trích từ `pkg/fhirsub/ssrf.go`; thêm chỉ-HTTPS, dialer "tất cả địa chỉ phải an toàn", giới hạn body, kiểm lại mỗi lần redirect | email (đính kèm URL, kết nối SMTP), CDS, `auth` P6, `pkg/fhirsub` |
| `pkg/mailer` | Cổng `Mailer`, kiểu `Message`, cấu hình SMTP, quy tắc ghép địa chỉ gửi, adapter `Nop`/`Log`/`Fake`/SMTP | `auth` P4/P5, route `/email/v1/send` |
| `pkg/binarystore` | `Store` (ghi/đọc/xóa/chép theo luồng), khóa đối tượng, kiểm tra tên/loại tệp bị chặn, adapter hệ thống tệp, bộ ký URL RSA-SHA256 | route `/storage`, Binary thô, DICOM, bulk export |
| `pkg/fhirbinary` | Lớp HTTP (net/http thuần) cho `/storage/{id}[/{ver}]`, Binary thô POST/PUT/GET, `$presigned-url`, decorator Repository (đưa `data` base64 ra kho, chép khi cập nhật, xóa khi expunge), viết lại tệp đính kèm | `apps/fhir-server`, `apps/dicomweb` |

### 0.2 Chỗ đặt route (cần người duyệt: Q-E1, Q-S1)

Sự thật đã kiểm trong repo:
- Medplum `email` cần `ctx.project` (đọc `features`, `secret`, `setting`) và `ctx.membership.admin` (`email/routes.ts:57,62`) và `ctx.repo` để đọc `Binary/<id>` (`email/email.ts:97`).
- Bản Go: `Project` do `apps/auth` giữ trong **CSDL riêng** của auth (`apps/auth/.env.example`: "Database RIÊNG của auth"; `apps/auth/internal/application/ports.go` có `ProjectRepository.Get`); `fhir-server` **không** lưu `Project`/`ProjectMembership` (`docs/go-port-status.md:115`) và `fhirauth.Principal` chỉ có `Subject`, `Project` (id), `Role`, `Grants`, `Policy` (`pkg/fhirauth/principal.go:40-52`), không có `features` hay secret SMTP.
- `Binary` và nội dung lâm sàng nằm ở CSDL của `fhir-server`; auth không đọc được.

Đề xuất (chưa chốt):
- **Email:** thư viện `pkg/mailer`; route `POST /email/v1/send` đặt trong `apps/auth` (nơi có Project, secret SMTP, membership), **đợt đầu không hỗ trợ tệp đính kèm `Binary/<id>`** (trả 400 rõ ràng); hỗ trợ đính kèm nội dung nhúng và URL https. `Binary/<id>` chờ Q-E2.
- **Storage:** `pkg/binarystore` + `pkg/fhirbinary`; route `/storage/*` và Binary thô gắn vào `apps/fhir-server` (có sẵn repo Binary, `fhirauth`), tách khỏi `BodyLimit("16M")` (`apps/fhir-server/internal/presentation/httpapi/httpapi.go:50`) giống Medplum gắn `binaryRouter` và `storageRouter` **trước** các body parser (`app.ts:223-239`). `apps/dicomweb` (kế hoạch DICOM) dùng thẳng `pkg/binarystore`.

### 0.3 Việc phải sửa trong `architecture_test.go` (khi triển khai, không làm trong đợt lập kế hoạch)

- `pkgRules` thêm: `pkg/safehttp: {}`, `pkg/mailer: {pkg/safehttp}` (gói không import `pkg/fhir`: hàm đọc cấu hình nhận kiểu `Setting` của riêng gói, adapter của app chuyển từ `fhir.ProjectSetting`; xem 12.1), `pkg/binarystore: {}`, `pkg/fhirbinary: {pkg/fhir, pkg/fhirrouter, pkg/binarystore}`; `pkg/fhirsub` thêm `pkg/safehttp`. `TestEveryPkgHasARule` sẽ đỏ nếu quên.
- `rules`: `apps/auth/internal/application` allow thêm `pkg/mailer` (để không có hai bản cổng Mailer, mục 12.1); `apps/auth/internal/infrastructure` allow thêm `pkg/mailer`, `pkg/safehttp`; `apps/fhir-server/internal/presentation` allow thêm `pkg/fhirbinary`.
- `TestEchoAndGormStayInTheirLayers` cấm `labstack/echo` trong `pkg/`: `pkg/fhirbinary` phải xuất `http.Handler`/hàm `net/http` và presentation bọc bằng `echo.WrapHandler` (tiền lệ: `pkg/httpsec`, "middleware net/http thuần").

### 0.4 Phát hiện đáng chú ý của Medplum (chi tiết ở từng mục)

1. Nhật ký gửi thư ghi **subject** (`email/email.ts:53`) và subject của thư mã MFA chứa **mã xác minh** (`auth/utils.ts:365`): lộ mã vào log.
2. Nhật ký truy cập ghi `req.originalUrl` (`app.ts:356`) gồm `?Signature=...&Expires=...`: URL ký hợp lệ 1 giờ nằm trong log.
3. Route `/storage` **bỏ qua `versionId` trong đường dẫn**: luôn đọc/ghi bản **hiện tại** của Binary (`storage/routes.ts:94-98`); PUT lên URL ký cũ ghi đè nội dung của bản mới.
4. `PUT /storage` mọi lỗi ghi (kể cả hỏng đĩa) trả 400 rỗng (`storage/routes.ts:49-54`).
5. `uploadBinaryData` **tạo resource Binary trước**, kiểm tra tên/loại tệp sau (`fhir/binary.ts:154-157`): tệp bị chặn vẫn để lại Binary mồ côi.
6. Chặn loại nội dung so khớp nguyên chuỗi, không bỏ tham số (`storage/utils.ts:111`): `application/x-sh; charset=x` lọt.
7. `isEmailConfigured()` chỉ xét cấu hình server (`email/utils.ts:128-131`, `email/routes.ts:74`): project có SMTP riêng vẫn bị 400 nếu server không cấu hình.
8. Danh sách người gửi được duyệt tách bằng `split(',')` không cắt khoảng trắng, so khớp phân biệt hoa thường (`email/utils.ts:151`).
9. Tệp đính kèm URL được đọc hết vào bộ nhớ không giới hạn (`email/email.ts:117`); `gzip`/`deflate` của Binary không giới hạn sau giải nén (`fhir/binary.ts:107-117`).
10. Khóa ký: tạo khóa **mã hóa bằng mật khẩu** (`config/utils.ts:76-91`) nhưng bên xác minh bắt buộc có cả khóa và mật khẩu (`storage/routes.ts:115-117`) còn bên ký chỉ cần khóa (`storage/presign.ts:32-36`).

---

# PHẦN A. MODULE `email`

## A1. Mục tiêu, phạm vi, hiện trạng Go

**Mục tiêu:** (a) một cổng `Mailer` duy nhất cho `auth` (D-4) và mọi nơi khác; (b) route `POST /email/v1/send` cho admin project; (c) SMTP theo project với chống SSRF; (d) ghép địa chỉ gửi và tên hiển thị an toàn.

**Phạm vi port:** `email/email.ts`, `email/routes.ts`, `email/utils.ts` (trừ SES). **Không port:** `cloud/aws/email.ts` (SES), `buildRawMessage`/`MailComposer` dành cho SES (mục A9).

**Hiện trạng Go** (lệnh đã chạy ở thư mục gốc repo, kết quả thật):

| Câu hỏi | Lệnh | Kết quả |
|---|---|---|
| Có `net/smtp`? | `grep -rn 'net/smtp' --include='*.go' apps pkg` | 0 dòng |
| Có gói mailer, SMTP? | `grep -rniE 'mailer\|smtp\|go-mail\|gomail' --include='*.go' apps pkg` | 1 dòng, chỉ là chú thích: `apps/auth/internal/bootstrap/container.go:2` ("Hạ tầng mới (kho, hasher, mailer...)") |
| Có thư viện gửi thư trong `go.mod`/`go.sum`? | `grep -n -iE 'go-mail\|gomail\|jordan-wright\|aws-sdk\|minio\|cloud.google\|azure' go.mod go.sum` | 0 dòng. Thư viện `wneessen/go-mail` (ứng viên trong `docs/plans/auth/p4-p5-register-mfa.md:294`) **ngoài source, chưa kiểm** |
| Có chống SSRF dùng lại được? | đọc `pkg/fhirsub/ssrf.go` | Có `blocked` (loopback, private, link-local, multicast, unspecified, CGNAT `100.64.0.0/10`), `checkEndpoint`, `newClient` (chặn ở `Dialer.Control` trên IP đã phân giải, không proxy, `CheckRedirect` trả `ErrUseLastResponse`): **đều chưa xuất khẩu**; thiếu `198.18.0.0/15` mà Medplum chặn riêng (`util/url.ts:196-197`), thiếu `0.0.0.0/8`, `240.0.0.0/4`, dải tài liệu |
| Có route email? | `grep -rn 'email/v1' --include='*.go' apps pkg` (0 dòng; đối chiếu thêm `docs/plans/modules-survey.md:10` ghi "chưa có") | không |

Kết luận: **không có gì** để dùng lại ngoài ý tưởng `Dialer.Control`.

## A2. Hành vi cần giữ

### A2.1 `POST /email/v1/send` (`email/routes.ts`)

Mount: `apiRouter.use('/email/v1/', emailRouter)` (`app.ts:280`); `emailRouter.use(authenticateRequest)` (`email/routes.ts:18`), nên **chưa xác thực thì 401 trước cả validator** (test `routes.test.ts:50`). `authenticateRequest` còn trả 403 khi tiền tố `/projects/{id}` lệch project của token (`oauth/middleware.ts:45-48`).

Thứ tự kiểm tra (quan trọng, vì thông điệp lỗi lộ thứ tự):

| # | Kiểm tra | Mã | Thông điệp chính xác | Nguồn |
|---|---|---|---|---|
| 1 | Chưa xác thực | 401 | (OperationOutcome `unauthorized`) | `oauth/middleware.ts:55` |
| 2 | Header `content-type` phải đúng `application/json` | 400 | OperationOutcome code `invalid`, `details.text` mặc định của `equals` (chưa xác nhận chuỗi chính xác, thuộc express-validator) | `routes.ts:21` |
| 3 | `to` không rỗng | 400 | `To is required` | `routes.ts:22` |
| 4 | `subject` không rỗng | 400 | `Subject is required` | `routes.ts:23` |
| 5 | `subject` là chuỗi | 400 | `Subject must be a string` | `routes.ts:24` |
| 6 | `text` (nếu có, `null` coi như không có) là chuỗi | 400 | `Text must be a string` | `routes.ts:25` |
| 7 | `html` tương tự | 400 | `HTML must be a string` | `routes.ts:26` |
| 8 | `attachments` là mảng | 400 | `Attachments must be an array` | `routes.ts:27` |
| 9 | Mỗi phần tử là object | 400 | `Attachment must be an object` | `routes.ts:28` |
| 10 | `attachments.*.{content,path,contentType,contentDisposition,cid,encoding}` là chuỗi nếu có | 400 | `Attachment properties must be strings` | `routes.ts:29-39` |
| 11 | `attachments.*.filename` là chuỗi hoặc `false` | 400 | `Attachment filename must be a string` | `routes.ts:40-43` |
| 12 | `icalEvent` là chuỗi hoặc object có `content` chuỗi | 400 | `icalEvent must be a string or an object with string content` | `routes.ts:44-47` |
| 13 | `icalEvent.method`, `icalEvent.filename` là chuỗi | 400 | `icalEvent properties must be strings` | `routes.ts:48-51` |
| 14 | `ctx.project.features` có `email` | 400 | `Email feature is not enabled for this project` | `routes.ts:57-60` |
| 15 | `ctx.membership.admin` | 403 | OperationOutcome `id: 'forbidden'`, `code: 'forbidden'`, `details.text = 'Only project administrators can send emails'` | `routes.ts:62-72` |
| 16 | `isEmailConfigured()` (server có `smtp` hoặc `emailProvider === 'awsses'`) | 400 | `Email is not configured on this server. Set up SMTP or AWS SES.` | `routes.ts:74-77`, `utils.ts:128-131` |
| 17 | Gửi, thành công | 200 | `allOk` | `routes.ts:80-81` |

Mọi lỗi validator gộp thành **một OperationOutcome nhiều issue**, mỗi issue `severity: error`, `code: invalid`, `expression: [path]`, `details.text: msg` (`fhir/outcomes.ts:10-28`); chạy **song song** tất cả luật rồi gộp (`util/validator.ts:9-19`), nên một body sai nhiều chỗ trả đủ các thông điệp.

Chỗ validator **không kiểm** (đọc `routes.ts:20-52`): kiểu của `to` (chỉ `notEmpty`), và `from`, `sender`, `cc`, `bcc`, `replyTo` hoàn toàn không có luật. Kết quả với giá trị lạ thuộc về nodemailer (chưa xác nhận).

`buildMailOptions` (`routes.ts:92-117`) chỉ sao các trường `from, sender, to, cc, bcc, replyTo, subject, text, html`, `attachments[].{filename, content, path, contentType, contentDisposition, cid, encoding}` và `icalEvent` (chuỗi, hoặc `{method, filename, content}`); mọi khóa khác bị **bỏ và ghi log cảnh báo** (`:112-114`). Test xác nhận các khóa nguy hiểm bị bỏ: `envelope`, `raw`, `headers`, `dkim`, `auth`, `disableFileAccess`, `disableUrlAccess`, `__proto__` (`routes.test.ts:192-294`).

### A2.2 `sendEmail` (`email/email.ts:34-64`)

1. `projectSmtp = project ? getProjectSmtpConfig(project) : undefined` (`:36`); có thể **ném** 400 nếu cấu hình dở (A2.3).
2. `fromAddress = getFromAddress(options, projectSmtp)` (`:37`) (A5.2).
3. `options.from = applyFromDisplayName(fromAddress, projectSmtp ? getProjectAppName(project) : undefined)`; `options.sender = fromAddress` (`:41-42`): người gửi phong bì luôn là địa chỉ trần; tên hiển thị chỉ khi dùng SMTP riêng của project.
4. Xử lý tệp đính kèm (A2.4).
5. `options.disableFileAccess = true; options.disableUrlAccess = true` (`:50-51`).
6. Log `Sending email` với `to`, `subject`, `projectId` (`:53`). **Lỗi bảo mật:** subject có thể chứa mã MFA (`auth/utils.ts:365`). Bản Go không được ghi subject.
7. Chọn đường gửi (`:55-63`): SMTP của project → SMTP của server (`config.smtp`) → SES nếu `emailProvider === 'awsses'` → **không cấu hình thì chỉ log cảnh báo `Email not configured — skipping send` và trả thành công** (`:62`). Giá trị `emailProvider = 'smtp'` không được đọc ở đây; chỉ sự tồn tại của `config.smtp` quyết định (`:57`).

### A2.3 Cấu hình SMTP theo project (`email/utils.ts:31-70`)

- Bị tắt nếu `config.allowProjectSmtp === false` (`:32`): rơi xuống cấu hình server (test `email.test.ts:630`).
- Không có `smtpHost` → `undefined`, dùng server (`:37-40`).
- Có host mà thiếu/<=0 `smtpPort`, hoặc thiếu `smtpUsername`, `smtpPassword`, `smtpFromAddress` → log cảnh báo liệt kê **tên** trường thiếu (không lộ giá trị) và ném 400 `Project SMTP configuration is incomplete or invalid` (`:46-58`).
- `secure = smtpSecure.valueBoolean ?? (port === 465)` (`:66`).
- `approvedSenderEmails = smtpApprovedSenders.valueString` (`:68`).
- Khi gửi bằng SMTP project: `connectionTimeout = greetingTimeout = 20 000 ms`, `socketTimeout = 120 000 ms` (`email.ts:146-148`, `utils.ts:16-17`); nếu **không** `allowUnsafeOutbound` thì dùng socket chống SSRF (`email.ts:143-145`). Lỗi gửi được log kèm `projectId`, host, port, thông điệp rồi ném lại (`email.ts:151-162`).
- SMTP **của server** không có timeout riêng, không chống SSRF (cấu hình do quản trị, tin cậy) và truyền `secure` nguyên trạng, kể cả `undefined` (`email.ts:133-141`) dù chú thích kiểu nói "inferred from `port === 465`" (`config/types.ts:397`). Test chỉ khẳng định `{host, port, auth}` (`email.test.ts:387-391`). Việc nodemailer có tự suy ra 465 hay không: **chưa xác nhận** (ngoài source).

### A2.4 Tệp đính kèm (`email/email.ts:71-120`)

- Không có `path` hoặc `path` không phải chuỗi: coi là nội dung nhúng, giữ nguyên (`:89-92`).
- `path` bắt đầu `Binary/`: đọc `repo.readReference({reference: path})` (kiểm quyền đọc theo **repo của người gọi**, không phải system), rồi `attachment.content = stream` từ kho; xóa `path` (`:94-104`). Binary không tồn tại: ném "Not found" và **không gửi gì** (test `email.test.ts:262`).
- `path` khớp `/^https?:\/\//i`: `safeFetch(path, {signal: timeout 30 s})`; lỗi mạng thành 400 `Failed to fetch attachment: <cause>`; HTTP không ok thành 400 `Failed to fetch attachment: HTTP <status>`; thành công thì `content = Buffer` (đọc hết, **không giới hạn**), xóa `path` (`:107-119`). `safeFetch` chặn host/IP nội bộ kể cả sau redirect (`util/url.ts:70-114`; chỉ HTTPS: `:72-75`; từ chối hostname: `:77-80`; từ chối nếu **bất kỳ** địa chỉ phân giải nào không an toàn: `:88-91`).
- `path` khác (ví dụ `./package.json`) được để nguyên; sau đó `disableFileAccess` khiến nodemailer từ chối: `Invalid email options: File access rejected for ./package.json` (test `email.test.ts:281-298`). Thông điệp này chỉ do đường **SES** gói lại thành 400 (`cloud/aws/email.ts:23-26`); đường SMTP để lỗi nodemailer đi thẳng, kết quả HTTP là 500 (`app.ts:190`). Thông điệp `href` bị chặn: `Invalid email options: Url access rejected for ...` (`email.test.ts:336-345`).

### A2.5 Địa chỉ và tên hiển thị (`email/utils.ts:143-249`)

Chi tiết thuật toán ở A5. Hàm phụ: `addressToString` (mảng thì lấy phần tử **đầu**, object thì `.address`; không có thì `undefined`), `buildAddresses` (mảng thì map từng phần tử; phần tử `null` hoặc `{}` cho ra `undefined` trong mảng, test `email.test.ts:200-219`), `extractEmailFromAddress` (lấy phần giữa `<` và `>` rồi `trim`, nếu không có ngoặc thì `trim` cả chuỗi), `getProjectAppName` (`setting[name='appName'].valueString` đã `trim`, rỗng thì `undefined`, `utils.ts:172-174`).

## A3. Ánh xạ sang Go

| Medplum | Go | Ghi chú |
|---|---|---|
| `email/email.ts` `sendEmail`, `sendEmailViaSmtp` | `pkg/mailer/smtp` (`smtp.Mailer.Send`) | chọn đường gửi theo `Message.Project` |
| `email/utils.ts` hàm thuần (`getFromAddress`, `applyFromDisplayName`, `extractEmailFromAddress`, `getProjectAppName`, `getProjectSmtpConfig`) | `pkg/mailer` (thuần, stdlib) | test chạy không mạng |
| `getProjectSmtpSocket` | `pkg/safehttp.DialContext` + dùng trong `smtp.Mailer` | A5.4 |
| `email/routes.ts` | `apps/auth/internal/presentation/httpapi/email.go` (DTO + validate), `apps/auth/internal/application/email/send.go` (use case), cổng `ProjectMailSettings` (đọc Project) | Q-E1; use case import `pkg/mailer` (cần sửa rule, 0.3) |
| `processAttachment` URL | `pkg/mailer` (hàm `ResolveAttachments`) gọi `safehttp.Client` | |
| `processAttachment` `Binary/` | cổng `AttachmentSource` trong use case; đợt đầu không có adapter | Q-E2 |
| `isEmailConfigured` | `smtp.Mailer.Configured(project *ProjectContext) bool` | A8: **khác** Medplum (tính cả SMTP project), Q-E5 |

## A4. Mô hình dữ liệu và cấu hình theo project

| Dữ liệu | Đọc/ghi | Nguồn | Go (đã kiểm có trường) |
|---|---|---|---|
| `Project.features` chứa `email` | đọc | `routes.ts:57` | `fhir.Project.Features []string` (`pkg/fhir/resource_project.go:61`) |
| `Project.secret[smtpHost]` (`valueString`) | đọc | `utils.ts:37` | `fhir.Project.Secret []ProjectSetting` (`:69`), `ProjectSetting.ValueString` |
| `smtpPort` (`valueInteger`) | đọc | `:42` | `ProjectSetting.ValueInteger *int` (`:107`) |
| `smtpUsername`, `smtpPassword`, `smtpFromAddress` (`valueString`) | đọc | `:43-45` | |
| `smtpSecure` (`valueBoolean`) | đọc | `:66` | `ProjectSetting.ValueBoolean *bool` (`:99`) |
| `smtpApprovedSenders` (`valueString`, danh sách phân cách bằng dấu phẩy) | đọc | `:68` | |
| `Project.setting[appName]` (`valueString`) | đọc | `:173` | `fhir.Project.Setting` (`:67`) |
| `ProjectMembership.admin` | đọc | `routes.ts:62` | claim/membership do auth |
| Không ghi gì vào Project | | | |

Cấu hình server: `smtp{host,port,username,password,secure?}` (`config/types.ts:392-399`), `emailProvider: none|awsses|smtp` (`:57`), `supportEmail` (bắt buộc, `:23`), `approvedSenderEmails` (`:24`), `allowProjectSmtp` (mặc định true, `:60`), `allowUnsafeOutbound` (`:258`).

Tôi **không đề xuất** đổi tên khóa `smtp*` trong `Project.secret` (giữ tương thích dữ liệu Medplum). Mật khẩu SMTP nằm **dạng rõ** trong `Project.secret` ở Medplum; xem Q-E7.

## A5. Thuật toán cho phần khó

### A5.1 Chọn đường gửi (từ `email.ts:55-63`)

```
proj := ProjectSMTPFromSecrets(project)   // có thể trả lỗi Config (400)
switch {
case proj != nil:            gửi qua proj, bật timeout 20s/20s/120s và dialer chống SSRF (trừ AllowUnsafeOutbound)
case server.SMTP != nil:     gửi qua server (không timeout riêng; đề xuất: vẫn đặt timeout)
default:                     log cảnh báo (không có subject), trả nil
```
Kết quả "bỏ qua" khi chưa cấu hình giữ nguyên cho `auth` (L5 của p4-p5 đã đề xuất lệch: lỗi gửi không làm hỏng đăng ký).

### A5.2 Ghép địa chỉ gửi (`getFromAddress`, `utils.ts:143-162`)

Đầu vào: `requestedFrom` (từ body, có thể rỗng), `proj` (nếu dùng SMTP project).
1. `approved = proj ? proj.approvedSenders : server.approvedSenders`; `default = proj ? proj.fromAddress : server.supportEmail` (`:145-146`). Danh sách của server **không** được áp cho SMTP project và ngược lại (test `email.test.ts:499`).
2. Nếu có `requestedFrom`: `s = addressToString(requestedFrom)`; `e = extractEmail(s)`; nếu `s` và `e` khác rỗng và `e` thuộc `approved` thì **trả `s` nguyên vẹn** (kể cả tên hiển thị người gọi đặt, test `email.test.ts:107-130`); ngược lại log cảnh báo (`from`, danh sách, `usingProjectSmtp`) rồi rơi xuống mặc định (`:148-159`).
3. Trả `default`.

Điểm lệch cần sửa khi port (đề xuất, vì là lỗi nhỏ của Medplum):
- Tách danh sách bằng `,` **rồi `TrimSpace`**, so khớp **không phân biệt hoa thường** phần local-domain (Medplum: `split(',')` không cắt, so `includes` đúng chuỗi, `:151`). Ca "`a@x.com, b@x.com`" ở Medplum làm `b@x.com` không bao giờ khớp.
- Chuẩn hóa `requestedFrom` bằng `net/mail.ParseAddress` thay vì cắt chuỗi `<`...`>` (`:238-249` bị đánh lừa bởi `"<a@x.com>" <evil@y.com>`: lấy phần giữa dấu `<` **đầu** và `>` đầu; ca này chưa có test ở Medplum, đánh dấu "chưa xác nhận hành vi"). Từ chối địa chỉ chứa CR/LF.

### A5.3 Tên hiển thị (`applyFromDisplayName`, `utils.ts:190-195`; `getProjectAppName`, `:172-174`)

```
func ApplyDisplayName(from string, appName string) Address
  if appName == "" || strings.Contains(from, "<") { return Parse(from) }   // đã có tên thì giữ
  return Address{Name: appName, Email: from}
```
- Chỉ gọi khi **dùng SMTP riêng của project** (`email.ts:41`): tên hiển thị khác miền người gửi là dấu hiệu lừa đảo nên không gắn `appName` vào địa chỉ của server (chú thích `utils.ts:176-189`, test `email.test.ts:548`).
- Phong bì (`MAIL FROM`) luôn là địa chỉ trần `fromAddress` (`email.ts:42`, test `email.test.ts:530`).
- Mã hóa tên khi dựng header: dùng `mime.WordEncoder`/`net/mail.Address.String()`; loại CR, LF; nodemailer lo việc này ở Medplum (`utils.ts:177-178`), bản Go phải tự lo và có test.

### A5.4 Kết nối SMTP chống SSRF (`getProjectSmtpSocket`, `utils.ts:79-122`)

Hành vi cần giữ: (1) host là IP literal không an toàn thì từ chối ngay (`:101-104`); (2) host là tên thì phân giải **mọi** địa chỉ (`all: true`), nếu rỗng hoặc **bất kỳ** địa chỉ không an toàn thì từ chối (`:88-92`), nên kẻ tấn công không dùng được tên trả cả IP công cộng lẫn nội bộ; (3) thử lần lượt các địa chỉ (`autoSelectFamily`, `:85,106`); (4) tổng thời gian phân giải + kết nối bị chặn bởi `connectionTimeout` (mặc định 20 s), mã lỗi `ETIMEDOUT` (`:107-109`); (5) lỗi phân giải đi nguyên (`ENOTFOUND`); (6) lỗi do host không an toàn luôn là 400 `Project SMTP configuration is incomplete or invalid`, và log chỉ ghi `invalid: 'smtpHost'` (`:80-83`).

Go (đề xuất): `safehttp.DialContext(opts)` làm đúng 6 điểm trên: `net.Resolver.LookupNetIP`, kiểm **toàn bộ** kết quả bằng `safehttp.Blocked`, quay vòng địa chỉ, bọc `context.WithTimeout`, kèm `Dialer.Control` làm lưới an toàn thứ hai (chống DNS rebinding giữa kiểm và nối). Truyền vào thư viện SMTP qua tùy chọn dial tùy chỉnh (kiểm khả năng ở E0).

### A5.5 Đính kèm theo URL (A2.4) với chống SSRF

```
Fetch(ctx, rawURL):
  u := safehttp.ParseURL(rawURL)  // chỉ https (Medplum chặn http ở lớp kết nối: util/url.ts:72-75), có host, không userinfo
  client := safehttp.NewClient(Options{Timeout:30s, HTTPSOnly:true, MaxRedirects:5, MaxBodyBytes: cfg.AttachmentMaxBytes})
  mỗi hop redirect: kiểm lại scheme https và địa chỉ đích ở bước dial
  lỗi mạng  -> 400 "Failed to fetch attachment: " + nguyên nhân
  status!=2xx -> 400 "Failed to fetch attachment: HTTP <status>"
  vượt MaxBodyBytes -> 400 "Failed to fetch attachment: body too large"  (Medplum không có giới hạn: lệch có chủ ý)
```
Thông điệp lỗi chặn phải giữ tiền tố `Failed to fetch attachment: ` và chứa `Outbound request to unsafe hostname <host> is blocked` hoặc `Outbound request to unsafe address <host> is blocked` (`util/url.ts:78,89`; test `email.test.ts:331`).

### A5.6 Dựng thư

Yêu cầu: `text`, `html`, tệp đính kèm (`filename`, `content` + `encoding` `base64`/`utf8`, `contentType`, `contentDisposition`, `cid`), `icalEvent` (`method` mặc định do thư viện, `filename`, `content`; test `routes.test.ts:157-190` kỳ vọng thêm phần `application/ics` và giữ HTML có `cid:`), `cc`, `bcc`, `replyTo`. Bắt buộc từ chối CR/LF trong mọi giá trị đi vào header (đề xuất; Medplum dựa vào nodemailer, ca `prepared header` ở `routes.test.ts:200` chứng tỏ họ lo chuyện này). Không hỗ trợ: `headers`, `raw`, `envelope`, `dkim`, `auth`, `alternatives`, `href`, `list`, `watchHtml`...

## A6. Cổng, thư viện, biến cấu hình

**Cổng:** xem mục 12.1 (`Mailer`, `Message`). Cổng phụ của route: `ProjectMailSettings{ GetProject(ctx, id) (*fhir.Project, error) }` (adapter dùng `ProjectRepository` có sẵn của auth), `AttachmentFetcher` (adapter `safehttp`), `AttachmentSource` (`Binary/`, hoãn).

**Thư viện (đề xuất, ngoài source, chưa kiểm):**
- Dựng MIME + SMTP: `github.com/wneessen/go-mail` **hoặc** tự viết bộ dựng thư bằng stdlib (`mime/multipart`, `mime/quotedprintable`, `net/mail`, `mime`) rồi gửi bằng `net/smtp`. Stdlib không có composer; `net/smtp` là gói đóng băng (frozen) và `PlainAuth` từ chối gửi mật khẩu qua kết nối không mã hóa trừ localhost (hành vi của Go, tốt cho an toàn). Chọn ở bước E0 sau spike: (a) tùy chọn dial tùy chỉnh để cắm `safehttp.DialContext`; (b) chính sách TLS (STARTTLS bắt buộc); (c) `icalEvent`; (d) chống chèn header; (e) AUTH `PLAIN`/`LOGIN`.
- Test SMTP: máy chủ SMTP giả viết bằng `net` + `net/textproto` ngay trong test (khoảng 100 dòng), không thêm thư viện.
- Đọc thư để kiểm: `net/mail`, `mime/multipart` (stdlib).

**Biến cấu hình (đề xuất; nhất quán với `p4-p5-register-mfa.md:378`):**

| Biến | Ý nghĩa | Mặc định |
|---|---|---|
| `MAIL_PROVIDER` | `none` \| `log` \| `smtp` | `log` ở local, `none` ở test, bắt buộc rõ ở production |
| `SMTP_HOST`, `SMTP_PORT`, `SMTP_USERNAME`, `SMTP_PASSWORD`, `SMTP_SECURE` | SMTP server | `SMTP_SECURE` rỗng thì suy ra `port==465` (cho cả server và project) |
| `MAIL_SUPPORT_ADDRESS` | địa chỉ gửi mặc định (`supportEmail`) | bắt buộc nếu `smtp` |
| `MAIL_APPROVED_SENDERS` | danh sách phân cách bằng dấu phẩy | rỗng |
| `MAIL_ALLOW_PROJECT_SMTP` | cờ `allowProjectSmtp` | `true` |
| `MAIL_ALLOW_UNSAFE_OUTBOUND` | cờ `allowUnsafeOutbound`; **cấm** ở staging/production | `false` |
| `MAIL_ATTACHMENT_MAX_BYTES`, `MAIL_MAX_RECIPIENTS` | giới hạn mới (Medplum không có) | 10 MiB, 50 |
| `MAIL_REQUIRE_TLS` | bắt buộc TLS/STARTTLS khi gửi | `true` (Q-E4) |

## A7. Danh sách ca test (từ `*.test.ts`)

Ký hiệu: **Giữ** = port gần như nguyên; **Đổi** = port có chỉnh; **Bỏ** = không port. "FT" = cột kỳ vọng ở Go.

### `email/routes.test.ts`

| Ca gốc | Dòng | Kỳ vọng | Quyết định |
|---|---|---|---|
| Unauthenticated | 50 | 401, mailer không được gọi | Giữ |
| Forbidden for non project admin | 60 | 403, `issue[0].details.text = 'Only project administrators can send emails'` | Giữ |
| Email feature not enabled for project | 75 | 400, `Email feature is not enabled for this project` | Giữ |
| Email not configured on server | 93 | 400, `Email is not configured on this server. Set up SMTP or AWS SES.` | Đổi: bỏ "or AWS SES" khỏi thông điệp (đề xuất `...Set up SMTP.`); ghi nhận thành lệch ở A9 |
| Wrong content type | 123 | 400 | Giữ |
| Send email as project admin | 133 | 200; người nhận đúng; subject, text đúng | Đổi: kiểm bằng `net/mail` trên thư thu được, không dùng SES |
| Send email with supported options | 157 | 200; cc, bcc, replyTo đúng; html giữ `cid`; hai tệp đính kèm (`hello.txt` nội dung `hello`, `logo.png` `cid=logo`); phần lịch `application/ics` | Giữ |
| Ignore unsupported option: envelope / raw message / raw attachment / raw alternative / custom headers / prepared header / header name / attachment headers (8 ca) | 192-226 | 200; `from` = địa chỉ hỗ trợ; thư thu được **không chứa** `other@example.com` và không có header `X-Test`; chỉ một header `From` | Giữ |
| Reject invalid option: text object / html object / subject object / attachments object / attachment string / attachment content object / attachment path object / icalEvent href / icalEvent content object (9 ca) | 228-247 | 400, mailer không được gọi | Giữ |
| Send email via server SMTP only passes supported options | 249 | `from` và `sender` = `supportEmail`; không có envelope/raw/headers/dkim/auth; `__proto__` trong body không ảnh hưởng | Đổi: kiểm trên `Message` nhận được bởi `Fake`, và test riêng thư viện không thể nhận trường lạ (kiểu tĩnh) |
| Send email via project SMTP | 296 | dùng host/port/`secure=false` của project; `from` = `smtpFromAddress` | Giữ |
| Misconfigured project SMTP returns bad request | 332 | 400, `Project SMTP configuration is incomplete or invalid`; không mở kết nối | Giữ |
| Handle SES error | 365 | 400 `Error sending email: ...` | **Bỏ** (SES). Thay bằng ca "lỗi SMTP" (A8) |

### `email/email.test.ts`

| Ca gốc | Dòng | Kỳ vọng | Quyết định |
|---|---|---|---|
| Send text email | 57 | `from` không được duyệt thì dùng `supportEmail`; to/cc đúng; subject, text | Giữ |
| Send text email from approved sender | 82 | giữ `from` đã duyệt | Giữ |
| Send with display string | 107 | `Display Test <no-reply@example.com>` được duyệt theo email trong ngoặc, giữ nguyên chuỗi | Giữ |
| Send with attachments | 132 | 1 tệp `text1.txt` | Giữ |
| Send with replyTo | 158 | `ReplyTo` đúng, `from` mặc định | Giữ |
| Array of addresses | 180 | `to: ['alice@...', {name:'Bob', address:'bob@...'}]` thành 2 người nhận | Giữ |
| Handle null addresses | 200 | `cc: null`, `bcc: [null, {}]` không làm hỏng | Đổi: ở tầng thư viện bỏ phần tử rỗng; ở route trả 400 cho phần tử không hợp lệ (đề xuất, Medplum không kiểm) |
| Attach binary | 221 | đính kèm `Binary/<id>` đọc từ kho | Hoãn (Q-E2) |
| Block invalid binary | 262 | `Binary/<uuid lạ>` thì lỗi `Not found`, không gửi | Hoãn (Q-E2) |
| Block file path | 281 | `./package.json` → `Invalid email options: File access rejected for ./package.json`, không gửi | Giữ (400 cho cả đường SMTP, đề xuất) |
| Fetch URL attachment via safeFetch | 300 | tải bằng client chống SSRF; nội dung `hello world` | Giữ (dùng `httptest.NewTLSServer` + tùy chọn `AllowPrivate` của test) |
| Block metadata URL attachment | 323 | `https://169.254.170.2/v2/metadata` → `Failed to fetch attachment: Outbound request to unsafe hostname 169.254.170.2 is blocked` | Giữ |
| Block nodemailer URL access | 336 | `href` bị từ chối | **Bỏ** (Go không có trường `href`); thay bằng ca "JSON có `href` bị bỏ qua" |
| Catch invalid options | 347 | `content` là object thì `Invalid email options` | Giữ ở route (đã là 400 do validator); ca thư viện: kiểu tĩnh nên không áp dụng |
| Send via SMTP | 366 | dùng SMTP server; không đụng SES | Giữ (không còn SES: kiểm không gọi đường project) |
| Uses project SMTP transport when configured | 424 | host, port 587, `secure=false`, auth, dialer riêng, 3 timeout 20 s/20 s/120 s | Giữ |
| allowUnsafeOutbound uses the default project SMTP socket | 445 | không có dialer chống SSRF | Giữ |
| Infers secure for port 465 | 453 | `secure=true` | Giữ |
| Explicit smtpSecure overrides port inference | 466 | `smtpSecure=false` thắng | Giữ |
| From address approved by project sender list | 480 | `from` = địa chỉ trong `smtpApprovedSenders` | Giữ |
| Server-approved sender not accepted under project list | 499 | địa chỉ chỉ được server duyệt thì rơi về `smtpFromAddress` | Giữ |
| appName becomes the From display name | 519 | `from = {name:'Acme Health', address}`; `sender` là địa chỉ trần | Giữ |
| A display name in smtpFromAddress overrides appName | 533 | `"Acme Billing" <noreply@...>` giữ nguyên | Giữ |
| appName does not rebrand the server sender | 548 | không SMTP project thì không đặt tên hiển thị | Giữ (đi đường SMTP server) |
| Missing smtpFromAddress fails loudly | 561 | ném `Project SMTP configuration is incomplete or invalid` | Giữ |
| Falls back to server transport when project SMTP not configured | 576 | secret không liên quan (`OPENAI_API_KEY`) thì dùng server | Giữ |
| Misconfigured project SMTP fails loudly | 590 | thiếu `smtpPassword` thì lỗi | Giữ |
| Logs and rethrows on project SMTP send failure | 606 | log `Project SMTP send failed` với `projectId`, host, port, `err`; ném lại | Giữ (không log mật khẩu, không log subject) |
| Kill-switch disables project SMTP | 630 | `allowProjectSmtp=false` thì dùng đường server | Đổi: Medplum rơi xuống SES; Go rơi xuống SMTP server hoặc "bỏ qua" |

### `email/utils.test.ts`

| Ca gốc | Dòng | Kỳ vọng | Quyết định |
|---|---|---|---|
| getProjectAppName returns undefined when unset | 30 | `undefined`/rỗng cho project vắng setting | Giữ |
| getProjectAppName treats a blank app name as unset | 36 | `''`, `'   '` thì rỗng | Giữ |
| getProjectAppName returns the trimmed app name | 41 | cắt khoảng trắng | Giữ |
| addressToString | 46 | 5 dạng đầu vào | Đổi: hàm thuần trên `[]Address` |
| applyFromDisplayName | 54 | 3 nhánh (có tên, không tên, đã có `<`) | Giữ |
| Rejects a project SMTP host with a private address / a public and a private address / no addresses / an unsafe IP literal (4 ca) | 100-117 | `Project SMTP configuration is incomplete or invalid` | Giữ |
| Connects through any resolved address | 119 | `::1` lỗi rồi nối `127.0.0.1` (khi cho phép địa chỉ nội bộ) | Giữ |
| Passes through lookup errors | 140 | lỗi phân giải nguyên (`ENOTFOUND`) | Giữ |
| Times out when resolving and connecting take too long | 147 | `ETIMEDOUT` ở 50 ms | Giữ |

### Ca MỚI nên thêm (Medplum thiếu)

CRLF trong `subject`, tên hiển thị, `to`, `cc`, tên tệp bị từ chối; `from` kiểu `"<a@x.com>" <evil@y.com>`; danh sách người gửi có khoảng trắng/hoa thường; `to` là object lạ thì 400; vượt `MAIL_MAX_RECIPIENTS`; tệp URL vượt giới hạn byte; redirect `https` sang `http`/IP nội bộ bị chặn ở từng hop; log không chứa subject/mật khẩu; `Secure` mặc định theo cổng 465 cho cả SMTP server; TLS bắt buộc khi `MAIL_REQUIRE_TLS`; SMTP gửi lỗi cho 5xx rõ ràng (A8).

## A8. Bảo mật: kiểm soát bắt buộc có test

| # | Kiểm soát | Test |
|---|---|---|
| E-SEC1 | Chỉ admin của project có tính năng `email` được gọi route; thứ tự 400/403 như A2.1 | ca routes 50/60/75 + ca đủ thứ tự |
| E-SEC2 | Từ chối mọi trường ngoài danh sách cho phép; không có `headers`/`raw`/`envelope`/`dkim`/`auth`/`disable*` | 8 ca "Ignore unsupported" + 1 ca tổng |
| E-SEC3 | `from` chỉ được dùng nếu thuộc danh sách duyệt tương ứng (project vs server không trộn) | email.test 499 + ca khoảng trắng/hoa thường |
| E-SEC4 | Không tin tên hiển thị của `appName` trên địa chỉ của server | email.test 548 |
| E-SEC5 | Không đọc tệp cục bộ; `path` ngoài `Binary/` và `https://` bị từ chối | email.test 281 |
| E-SEC6 | Đính kèm URL: chỉ https, chặn dải nội bộ ở mỗi hop, có timeout và giới hạn byte | email.test 300/323 + ca mới |
| E-SEC7 | SMTP project: kết nối chỉ tới địa chỉ công cộng, **mọi** địa chỉ phân giải phải an toàn, timeout 20 s | utils.test 100-147 |
| E-SEC8 | Header injection: CR/LF bị từ chối ở mọi trường; mã hóa tên hiển thị | ca mới |
| E-SEC9 | Không ghi subject, nội dung, mật khẩu SMTP, địa chỉ đầy đủ vào log; chỉ ghi `projectId`, số người nhận, mã kết quả | ca kiểm bộ ghi log |
| E-SEC10 | TLS: không gửi `AUTH` qua kết nối không mã hóa; `MAIL_REQUIRE_TLS` | ca SMTP giả |
| E-SEC11 | `Project.secret` không lộ qua API đọc cho người không phải admin; lưu mã hóa (Q-E7) | ca ở auth |
| E-SEC12 | Giới hạn số người nhận, kích thước, và hạn mức gửi theo project (Medplum không có; admin vẫn có thể lạm dụng máy chủ làm relay) | ca mới |

## A9. KHÔNG port đợt này và điểm lệch chấp nhận được

| Hạng mục | Lý do |
|---|---|
| AWS SES (`cloud/aws/email.ts`, `emailProvider='awsses'`, `buildRawMessage`, `buildAddresses`) | AWS SDK đã gỡ khỏi `go.mod` (chứng cứ: grep ở A1); `docs/plans/modules-survey.md:27` đã chốt "chỉ SMTP". Ghi nhận: thông điệp 400 `Email is not configured on this server. Set up SMTP or AWS SES.` đổi bỏ chữ SES |
| `cloud/aws/config.ts` (nạp cấu hình từ SSM/Secrets Manager) | Không liên quan Go; cấu hình qua biến môi trường (`pkg/envconfig`) |
| Tệp đính kèm `Binary/<id>` | Hoãn đến khi route có repo lâm sàng (Q-E2) |
| `icalEvent` kiểu `href`, `headers`, `alternatives`, `raw`... | Medplum cũng bỏ ở route (`routes.ts:93-110`) |
| DKIM, OAuth2 SMTP | Medplum cũng bỏ (test `routes.test.ts:273-274`) |

Lệch chấp nhận được: (1) route trả 400 cho `to`/`cc`/`bcc`/`replyTo` sai kiểu (Medplum để thư viện quyết định); (2) lỗi SMTP khi gửi trả OperationOutcome `exception` (500) thay vì `{"msg":"Internal Server Error"}` (`app.ts:190`); (3) danh sách người gửi được duyệt cắt khoảng trắng, không phân biệt hoa thường; (4) project có SMTP riêng gửi được kể cả khi server không cấu hình (Q-E5); (5) có giới hạn kích thước/số người nhận; (6) không log subject.

## A10. Rủi ro và câu hỏi mở (Q-E)

| # | Câu hỏi / rủi ro | Đề xuất |
|---|---|---|
| Q-E1 | Route đặt ở `apps/auth`, app mới, hay `fhir-server`? (xem 0.2) | `apps/auth` |
| Q-E2 | Có hỗ trợ `Binary/<id>` làm tệp đính kèm trong đợt này? Cần đọc repo lâm sàng theo quyền người gọi, mà auth không có | Hoãn; trả 400 `Binary attachments are not supported` |
| Q-E3 | `wneessen/go-mail` hay tự dựng MIME + `net/smtp`? (bước E0) | Quyết sau spike; mặc định `go-mail` nếu đủ 5 tiêu chí ở A6 |
| Q-E4 | TLS bắt buộc hay cơ hội (như nodemailer: nâng cấp STARTTLS nếu máy chủ có, không thì gửi rõ) | Bắt buộc, có cờ tắt cho dev |
| Q-E5 | Project có SMTP riêng gửi được khi server không cấu hình? Medplum chặn (`routes.ts:74`) | Cho phép |
| Q-E6 | Cờ `features` (`email`) và `admin`: đọc từ `Project`/membership trong CSDL auth hay từ claim token | Từ CSDL auth (nguồn sự thật); claim chỉ để tăng tốc |
| Q-E7 | Mật khẩu SMTP lưu rõ trong `Project.secret` (như Medplum). Mã hóa at-rest (giống D-19 mã hóa `mfaSecret`) và che khi đọc? | Mã hóa AES-GCM; chi tiết cùng D-19 |
| Q-E8 | Giới hạn số người nhận, kích thước, hạn mức theo project; giá trị mặc định | 50 người, 10 MiB, hạn mức do `ratelimit` nền |
| Q-E9 | Lỗi SMTP: 500 hay 502; thông điệp cho client có gồm lý do không (rò thông tin máy chủ) | 502 với thông điệp chung, chi tiết trong log |
| Q-E10 | `icalEvent`, `cid`, `contentDisposition` có cần ngay đợt đầu? | Có (test `routes.test.ts:157` đòi) nhưng sau E4 |
| Rủi ro | Route gọi thư viện chặn mạng trong request: SMTP chậm 20-120 s giữ kết nối HTTP. Medplum cũng đồng bộ. | Giữ đồng bộ; đặt `context` theo timeout |
| Rủi ro | Khóa `smtp*` dạng rõ trong `Project.secret` đi theo mọi bản sao lưu | Q-E7 |

## A11. Các bước làm

Quy ước cỡ: S ≤ 1 ngày, M 2-3 ngày, L 4-5 ngày.

| Bước | Việc | Cỡ | Phụ thuộc | Tiêu chí hoàn thành |
|---|---|---|---|---|
| E0 | Spike thư viện SMTP/MIME (A6), chốt Q-E3, Q-E4; ghi quyết định vào bảng này | S | không | Báo cáo kèm 5 tiêu chí đạt/không; `go vet`, `go test -race` của spike xanh |
| E1 | `pkg/safehttp` (nếu chưa làm ở cds-dicom bước 1: **dùng chung, làm một lần**): xuất `Blocked`, `ParseURL`, `NewClient`, `DialContext`; thêm dải IANA, chỉ-HTTPS, giới hạn byte, kiểm lại mỗi redirect; `pkg/fhirsub` đổi sang gói mới, **không đổi hành vi** | M | không (chặn email E3, CDS, auth P6) | Test `fhirsub` hiện có vẫn xanh; ca chặn `198.18.0.1`, `169.254.170.2`, `::ffff:10.0.0.1`, tên trỏ cả IP công cộng và riêng; ca redirect |
| E2 | `pkg/mailer`: `Message`, `Address`, `Mailer`, `Nop`/`Log`/`Fake`, hàm thuần A5.2/A5.3, `ProjectSMTPFromSecrets`, `ProjectAppName`; cập nhật `architecture_test.go` (0.3) | M | không | Toàn bộ `utils.test.ts` (trừ socket) và các ca từ `email.test.ts` 424-630 phần thuần chạy với `Fake`; `go test ./... -race` xanh; **mở khóa auth 4.2** |
| E3 | `pkg/mailer/smtp`: dựng MIME, STARTTLS/TLS, timeout, dialer an toàn (E1), đính kèm URL (E1), `Configured()` | M | E0, E1, E2 | Ca SMTP giả: từ chối nối tới IP nội bộ, timeout, TLS bắt buộc, header injection, ca `Fetch URL`/`Block metadata URL`; log không lộ subject |
| E4 | Route `/email/v1/send` (DTO, 17 bước A2.1, use case, cổng `ProjectMailSettings`) | M | E2, E3, auth P3 (Bearer) | Toàn bộ `routes.test.ts` trừ ca bỏ; thứ tự lỗi đúng; `httpapi_test` của auth xanh; OpenAPI cập nhật |
| E5 | (Hoãn, Q-E2) Đính kèm `Binary/<id>` | S | E4, S3 của storage | Ca `email.test.ts:221,262` |
| E6 | Cấu hình: `MAIL_*`, `.env.example`, README app, CORS tiền tố `/email/` trong `httpsec` | S | E4 | `make test` xanh; cấm `MAIL_ALLOW_UNSAFE_OUTBOUND` ở production có test |

**Thứ tự với `auth`:** E2 phải xong **trước hoặc cùng** auth bước 4.2 (cổng `Mailer` + `log`/`fake`), vì nếu auth dựng cổng riêng sẽ có hai bản; **không** chờ E3 (D-4: adapter thật chốt sau). auth P4/P5 chỉ cần `Send(ctx, Message{To, Subject, Text, Project})` (`p4-p5-register-mfa.md:294`), tập con của `Message` ở 12.1.

---

# PHẦN B. MODULE `storage`

## B1. Mục tiêu, phạm vi, hiện trạng Go

**Mục tiêu:** (a) kho đối tượng cho nội dung Binary theo luồng (hệ thống tệp trước); (b) URL ký (RSA-SHA256) để tải/tải lên không cần Bearer; (c) Binary thô qua `/fhir/R4/Binary`; (d) nền cho DICOM, bulk export, và về sau S3.

**Phạm vi port:** `storage/{base,filesystem,loader,presign,routes,types,utils}.ts`, `fhir/binary.ts`, `fhir/operations/binary-presigned-url.ts`, `fhir/rewrite.ts`; hook Binary trong `fhir/repo.ts:1222-1272,1895-1909`.

**Hiện trạng Go** (lệnh đã chạy ở thư mục gốc):

| Câu hỏi | Lệnh | Kết quả |
|---|---|---|
| Có kho đối tượng, ghi tệp? | `grep -rnE 'os\.Create\|os\.OpenFile\|MkdirAll\|multipart' --include='*.go' apps pkg` | 1 dòng: `apps/fhir-cli/internal/cli/bulk.go:248` (client ghi thư mục xuất). Không có gói kho; thư mục `storage/` ở gốc repo rỗng (`docs/plans/cds-dicom/dicom.md:7`) |
| Có chữ ký URL RSA? | `grep -rniE 'presign\|signingKey\|crypto/rsa\|SignPKCS1' --include='*.go' apps pkg` | Chỉ khóa ký JWT/job/ws (`apps/auth/internal/config/config.go:45-46`, `apps/fhir-server/internal/config/config.go:62,81`); **không** `crypto/rsa`, **không** presign |
| Binary xử lý ngoài base64? | `grep -rn 'Binary' --include='*.go' -l apps pkg` rồi đọc | Chỉ `pkg/fhirrouter`: `bulk.go:178` tạo `fhir.Binary{..., Data: base64}`; `bulk.go:356-371` `binaryRaw` giải mã base64 trong `Data` và chỉ trả thô khi `Accept` (media type đầu) **bằng** `contentType` hoặc `application/octet-stream` (Medplum trả thô cho mọi `Accept` không bắt đầu bằng `application/fhir+json`, `fhir/response.ts:79-81`); `handlers.go:274-275` gọi `binaryRaw`. Không có tải lên thô, không có `$presigned-url` |
| Giới hạn body hiện tại | `grep -rn 'BodyLimit' --include='*.go' apps pkg` | `fhir-server`: `16M` (`httpapi.go:50`); `auth`: `1M` (`httpapi.go:68`) |
| `pkg/storage` cũ | `ls ../trivita-codebase-v1-removed-apps/pkg/storage` | Có `storage.go`, `local.go` (dùng `os.OpenRoot`, ghi `tmp` rồi `rename`), `s3.go`, `drive.go`; **ngoài repo, chỉ đọc**; ý tưởng dùng lại, không chép (tên tạm `.tmp-<pid>` đụng nhau khi ghi đồng thời cùng khóa; port `FileStorage` theo `path` chứ không theo Binary; kéo AWS SDK) |
| `Binary` có trường cần? | đọc `pkg/fhir/resource_binary.go` | `ContentType`, `SecurityContext`, `Data`, `URL`, `Meta` (có `Meta.Project`, `types_gen.go:1824`) |

## B2. Hành vi cần giữ

### B2.1 Khóa đối tượng và hệ thống tệp

- Khóa: `binary/<id>/<versionId>` (`storage/base.ts:57-59`); **mỗi phiên bản Binary là một đối tượng riêng**.
- `readBinary`/`writeBinary`/`copyBinary`/`getKey` (`base.ts:25-45`); `writeBinary` gọi `checkFileMetadata(filename, contentType)` trước khi ghi (`:35`).
- Hệ thống tệp (`storage/filesystem.ts`): đường dẫn = `<baseDir>/<khóa bỏ 'binary/' đầu>` (`:101-108`), kiểm `startsWith(baseDir + sep)` thì mới `Invalid storage key` (`:104-106`); `writeFile` `mkdir -p` rồi `createWriteStream(flags:'w')` + `pipeline` (`:38-52`); `readFile` mọi lỗi `access` thành `Error('File not found')` (`:54-63`); `copyFile` tạo thư mục đích (`:76-81`); `deleteFile` dùng `rm(force: true)` nên **idempotent** (`:83-86`).
- `initBinaryStorage(type)`: tiền tố `file:` | `s3:` | `azure:` | `gs:`; không khớp thì kho = `undefined` và `getBinaryStorage()` ném `Binary storage not initialized` (`loader.ts:17-36`, test `filesystem.test.ts:15`). Cấu hình khóa là `binaryStorage` (`config/types.ts:18`; test gán `'file:' + mkdtemp`, `config/loader.ts:204`).
- `deleteBinaryStorageObjects(keys)`: không có kho thì bỏ qua; lỗi từng khóa **chỉ log cảnh báo**, không làm hỏng expunge (`loader.ts:46-60`); chạy **sau commit** (`fhir/repo.ts:1905-1909`).

### B2.2 Kiểm tra tên và loại tệp (`storage/utils.ts`)

- `checkFileMetadata` ném 400 `Invalid file extension` (`:79`) hoặc `Invalid content type` (`:82`).
- Đuôi bị chặn (so `endsWith` trên tên viết thường, `:91-102`): `.ade .adp .apk .appx .appxbundle .bat .cab .chm .cmd .com .cpl .dll .dmg .ex .ex_ .exe .hta .ins .isp .iso .jar .jse .lib .lnk .mde .msc .msi .msix .msixbundle .msp .mst .nsh .php .pif .ps1 .scr .sct .shb .sys .vb .vbe .vbs .vxd .wsc .wsf .wsh` (`:10-57`).
- Loại bị chặn (so khớp **nguyên chuỗi viết thường**, `:109-115`): `application/java-archive`, `application/x-msdownload`, `application/x-sh`, `application/vnd.apple.installer+xml`, `application/vnd.microsoft.portable-executable` (`:63-69`). Tham số (`; charset=`) **không** được bỏ nên có thể lách: lỗi của Medplum, Go phải bỏ tham số trước khi so.
- Tên tệp chỉ đến từ tham số `_filename` của POST/PUT Binary (`fhir/binary.ts:83`); đường URL ký không có tên (`storage/routes.ts:50`).

### B2.3 URL ký (`storage/presign.ts`)

- URL gốc: `concatUrls(storageBaseUrl, '<id>/<versionId>')` (`:21`), `storageBaseUrl` mặc định `<baseUrl>/storage` (`config/utils.ts:31`).
- Tham số theo thứ tự: `Expires` = (now + **1 giờ**) tính bằng **giây unix** (`:23-25`), rồi `Project` nếu `binary.meta.project` có (`:27-29`), rồi `Signature` ở **cuối** (`:34-36`). Test xác nhận `Project` đứng trước `Signature` (`routes.test.ts:195-204`) và vắng khi Binary không thuộc project (`:206-210`).
- Chuỗi ký = `('PUT ' nếu upload, ngược lại 'GET ') + result.toString()` **trước khi** có `Signature` (`:31`); thuật toán `createSign('sha256')` (RSA, đệm PKCS#1 v1.5 mặc định của Node), kết quả base64 (`:32-35`). Nếu không có `signingKey` thì **không ký** (URL không có `Signature`, sẽ bị 401, `:32`).
- Chọn URL (`storage/loader.ts:62-75`): nếu `storageBaseUrl` bắt đầu bằng `baseUrl` thì ký nội bộ; ngược lại giao cho kho (`S3` presign, CloudFront...). Go chỉ có nhánh nội bộ.
- Tự sinh khóa tạm nếu dùng kho nội bộ mà chưa có khóa: RSA 2048, khóa riêng PKCS#1 PEM **mã hóa `aes-256-cbc`** với mật khẩu `randomUUID()`, cảnh báo "Storage URLs will not work in cluster environments, and will be invalid after server restart" (`config/utils.ts:71-92`).

### B2.4 Route `GET|PUT /storage/:id{/:versionId}` (`storage/routes.ts`)

Mount **trước** body parser và **không qua** `authenticateRequest`; bốn tiền tố `/storage`, `/api/storage`, `/projects/:projectId/storage`, `/api/projects/:projectId/storage` (`app.ts:237-240`). Vẫn đi qua `standardHeaders` (`Cache-Control: no-store`, CSP `default-src 'none'`, `nosniff`... `app.ts:85-106,213`) và CORS (tiền tố `/storage/`, `cors.ts:67`) và rate limit (`app.ts:222`).

`verifyAndLoadBinary(req, res, method)` (`routes.ts:57-104`), **đúng thứ tự**:

| # | Kiểm tra | Mã | Thân | Dòng |
|---|---|---|---|---|
| 1 | Thiếu `Signature` | 401 | `Unauthorized` (`sendStatus`) | `:60-63` |
| 2 | Thiếu `Expires`, hoặc `now(s) > parseInt(Expires)` | 410 | `URL has expired` | `:65-69` |
| 3 | `originalUrl.pathname !== req.baseUrl + req.path` (chống `..\`, `%2e%2e`) | 401 | `Invalid signature` | `:71-75` |
| 4 | Xác minh RSA-SHA256 trên `method + ' ' + (URL bỏ Signature)`; với GET thử thêm dạng **cũ không có method** | 401 | `Invalid signature` | `:77-92` |
| 5 | Đọc Binary bằng **system repo** (project nếu có `Project`, ngược lại shard hệ thống); không có thì lỗi `notFound` | 404 (qua error handler) | OperationOutcome | `:94-98` |
| 6 | Có `Project` mà `binary.meta.project !== Project` | 401 | `Invalid signature` | `:99-102` |

Ghi chú: (a) kiểm tra 2 chạy **trước** xác minh chữ ký (test `routes.test.ts:65` kỳ vọng `410` với `Signature=xyz`; `:70` dùng `Expires` đơn vị ms (tương lai xa) nên qua bước 2 rồi `401` ở bước 4); (b) URL dựng từ `req.protocol` và `Host` (`:58`) nên chữ ký gắn với **host**: sau proxy phải đúng `trust proxy` (`app.ts:211` đặt 1); (c) khóa công khai suy ra từ khóa riêng và **cache vĩnh viễn** (`:20,106-109`), bắt buộc cả `signingKey` lẫn `signingKeyPassphrase` (`:115-117`), thiếu thì `Error('Signing key or passphrase is not configured')` (500).

`GET` (`:22-35`): đọc luồng của **bản hiện tại** (`readBinary(binary)`), `200` + `Content-Type = binary.contentType`, `pump(stream, res)`; **mọi lỗi** (kể cả giữa chừng) thành `res.sendStatus(404)`. Không có `Content-Length`, `Content-Disposition`, `Range`, `ETag`.

`PUT` (`:37-55`): nếu header `Content-Type` có mà **khác** `binary.contentType` (so chuỗi nguyên) thì `400` `Content-Type mismatch`; thiếu header thì cho qua. Ghi `writeBinary(binary, undefined, contentType, req)` rồi `200`; **mọi lỗi** (cả `Invalid content type` lẫn lỗi đĩa) thành `400` rỗng. Nội dung ghi vào khóa của bản **hiện tại**, đè nội dung cũ.

Hệ quả (lỗi của Medplum, `versionId` trong đường dẫn bị bỏ qua: `:94` chỉ lấy `req.params.id`): (i) URL ký cho phiên bản cũ phục vụ **bản mới**; (ii) PUT lên URL phát hành trước khi Binary được cập nhật ghi đè nội dung bản **mới**. Xem Q-S5.

### B2.5 Binary thô (`fhir/binary.ts`)

Mount tại `/fhir/R4/Binary` (4 tiền tố), **trước** body parser, có `authenticateRequest` (`app.ts:227-236`, `binary.ts:20`).

- `POST /` và `PUT /:id` dùng chung `handleBinaryWriteRequest` (`:22-26,36-88`); `GET /:id` (`:29-34`).
- `Content-Encoding`: không có thì dùng `req`; `deflate` → `zlib.createInflate`, `gzip` → `createGunzip` (không phân biệt hoa thường); khác (kể cả `identity`, `br`) → `400` `Unsupported content encoding` (`:42-46,101-120`). Không giới hạn kích thước sau giải nén (lỗi: bom nén).
- Nếu `Content-Type === 'application/fhir+json'` (so **nguyên chuỗi**, `:49`): đọc cả thân vào chuỗi (`:122-130`, không giới hạn), thử `JSON.parse` (lỗi thì bỏ qua, coi là dữ liệu thô); nếu là resource `Binary` và (không có `id` trong URL hoặc `body.id === id`) thì đây là **resource**: `createResource`/`updateResource` và trả `201`/`200` (`:63-77`). Ngược lại lưu nội dung thô (test `binary.test.ts:303`: PUT `{"resourceType":"Patient"}` rồi GET ra đúng chuỗi đó).
- Còn lại `uploadBinaryData` (`:143-161`): tạo/cập nhật resource `{contentType (mặc định application/octet-stream), securityContext (header X-Security-Context)}` **trước**, rồi `writeBinary(binary, _filename, contentType, source)` (kiểm tên/loại), rồi đặt `binary.url = getPresignedUrl(binary)` trong phản hồi (không lưu). Trả `201` khi POST, `200` khi PUT.
- `GET /:id` trả **thô** nếu `Accept` không bắt đầu bằng `application/fhir+json` (`fhir/response.ts:79-81`), bằng `Content-Type = binary.contentType`; có `data` (dữ liệu cũ) thì giải base64 (`:44-48`); ngược lại luồng từ kho (`:51-54`). Lỗi trước khi gửi header: gỡ `ETag`, `Last-Modified`, `Content-Type`, log lỗi `Error reading Binary content` kèm `err`, `binary: Binary/<id>`, `versionId`, trả `404` OperationOutcome (`:55-67`; test `binary.test.ts:61`); lỗi giữa chừng thì ném (`:68`).
- Hook trong repository (`fhir/repo.ts:1222-1262`): khi **ghi** resource Binary có `data` base64: giải mã, kiểm `base64BinaryMaxBytes` (mặc định **1 MiB**, `config/utils.ts:54`; vượt thì `400 base64Binary exceeds N bytes`), ghi ra kho, **xóa `data`** khỏi resource; không có `data` mà là cập nhật thì `copyBinary(existing, resource)`. Test: Binary trong transaction Bundle (`binary.test.ts:196`: `data: 'SGVsbG8gV29ybGQh'` ra `Hello World!` trong kho).
- `Binary.contentType`: `Binary` JSON có `data` không hợp lệ base64 → `400` (test `binary.test.ts:276`).

### B2.6 `GET /fhir/R4/Binary/:id/$presigned-url[?upload=true]` (`fhir/operations/binary-presigned-url.ts`)

`readResource` bằng repo của người gọi (nên **theo quyền đọc**, gồm cả quyền đọc `securityContext`, test `binary-presigned-url.test.ts:150`); nếu `upload` thì phải `canPerformInteractionById(UPDATE)` nếu không `403` `forbidden` (`:32-35`; test `:77`, và project liên kết không được upload `:110`). Trả `Parameters{ url: valueUri }` (`:37-38`). Hợp đồng tham số: `upload` boolean (0..1), `url` uri (1..1) (`:14-19`).

### B2.7 Viết lại tệp đính kèm (`fhir/rewrite.ts`)

- Chế độ `PRESIGNED_URL` (khi **đọc**, `fhir/response.ts:94-97`; cũng ở `subscription`, `auth/me`, `clientinfo`): thay thuộc tính chuỗi tên `url` hoặc `path` trỏ tới Binary bằng URL ký; chỉ khi repo của người gọi **đọc được** Binary (và `securityContext`), không thì **giữ nguyên** (`rewrite.ts:14-17,199-213`; test `rewrite.test.ts:292` với 4 repo khác nhau). Binary không tồn tại thì đổi thành `Binary/<id>` (`:207-210`, test `:166`).
- Chế độ `REFERENCE` (khi **ghi**, `fhir/repo.ts:1139`): đổi mọi dạng URL Binary thành `Binary/<id>` **không kiểm quyền** (`:181-184`).
- Nhận dạng URL Binary (`normalizeBinaryUrl`, `:228-249`): `<baseUrl>fhir/R4/Binary/...`, `<storageBaseUrl>...`, hoặc `Binary/...`; dạng `<id>/_history/<ver>` cho `versionId`. Cache theo giá trị URL trong một lần viết lại để một Binary ra cùng một URL (`:96,179-189`, test `:268`). Không viết lại bên trong chính resource `Binary` (`:71,124-130`). Trả đúng đối tượng đầu vào nếu không có URL Binary (`:48-50`; test `:65` kiểm `toBe`), nên người gọi muốn sửa phải sao chép; không đột biến đầu vào (test `:76`).
- Lỗi nhỏ: `normalizeBinaryUrl` cắt `refStr` theo `/` mà **không bỏ query** nên URL kho dạng `.../storage/<id>?Expires=...` cho `id` chứa `?...` (chưa xác nhận ảnh hưởng, vì URL do chính hệ thống phát luôn có `/<versionId>`).

### B2.8 `$scan` (`fhir/operations/binary-scan.ts`)

Chỉ chạy với `S3Storage` và GuardDuty (`:76-81`): **không port** (B9).

## B3. Ánh xạ sang Go

| Medplum | Go | Ghi chú |
|---|---|---|
| `storage/types.ts`, `base.ts` | `pkg/binarystore` (`Store`, `Key`, `ErrNotFound`) | khóa `binary/<id>/<ver>` giữ nguyên |
| `storage/utils.ts` | `pkg/binarystore.CheckMetadata` | sửa lỗi bỏ tham số (B2.2) |
| `storage/filesystem.ts` | `pkg/binarystore` (adapter `FS`, `os.Root`) | ghi nguyên tử, bỏ lỗi `replace` (B5.3) |
| `storage/presign.ts` + phần xác minh trong `routes.ts` | `pkg/binarystore` (`Signer`) | thuật toán B5.1 |
| `storage/loader.ts` | `pkg/binarystore.Open(spec)`, và `fhirbinary.PresignedURL` | chỉ `file:` |
| `storage/routes.ts` | `pkg/fhirbinary.StorageHandler` (net/http) | gắn vào `fhir-server` bằng `echo.WrapHandler`, nằm ngoài `BodyLimit("16M")` |
| `fhir/binary.ts` | `pkg/fhirbinary.BinaryHandler` (POST/PUT/GET thô) | thay `binaryRaw` ở `pkg/fhirrouter/bulk.go:356` bằng cổng đọc nội dung |
| `fhir/repo.ts` hook Binary | `pkg/fhirbinary.Wrap(repo, store)` (decorator `fhirrouter.Repository`) | B5.5 |
| `binary-presigned-url.ts` | `pkg/fhirbinary` đăng ký thao tác qua router hiện có (vị trí đăng ký: chưa xác nhận API của `fhirrouter`) | |
| `rewrite.ts` | `pkg/fhirbinary.Rewrite` + điểm gắn trong `fhirrouter` (Q-S10) | giai đoạn sau |

## B4. Mô hình dữ liệu và cấu hình

| Trường | Đọc/ghi | Ghi chú |
|---|---|---|
| `Binary.id`, `Binary.meta.versionId` | đọc | thành khóa kho |
| `Binary.meta.project` | đọc (kiểm khớp tham số `Project`) | `fhir.Meta.Project` có sẵn |
| `Binary.contentType` | đọc/ghi; so khớp `Content-Type` của PUT | bắt buộc theo FHIR; mặc định `application/octet-stream` khi tải thô |
| `Binary.securityContext` | ghi từ header `X-Security-Context`; đọc để suy quyền | |
| `Binary.data` | ghi (đầu vào) rồi **xóa**; đọc (dữ liệu cũ) | Go hiện giữ trong CSDL: Q-S6 |
| `Binary.url` | chỉ trong **phản hồi** tải lên và `$presigned-url`; không lưu | |
| Không có cấu hình theo project | | khác `email` |

Cấu hình server Medplum liên quan: `binaryStorage`, `storageBaseUrl`, `signingKey`, `signingKeyId` (chỉ CloudFront), `signingKeyPassphrase`, `baseUrl`, `base64BinaryMaxBytes` (mặc định 1 MiB), `inlineAttachmentsMaxTotalBytes` (không thuộc phạm vi) (`config/types.ts:18-22,92`, `config/utils.ts:31,54-55`). **Không tìm thấy** `maxBinary...` nào: `grep -rn "maxBinary\|maxUpload" packages/server/src` cho 0 dòng, tức Medplum **không có giới hạn kích thước tải lên Binary trong mã đã đọc** (nếu có là ở proxy/ALB, chưa xác nhận; Q-S7).

## B5. Thuật toán cho phần khó

### B5.1 URL ký RSA-SHA256

**Ký** (`Signer.Sign(method, id, versionID, projectID)`):
```
base  = StorageBaseURL + "/" + id + "/" + versionID           // concatUrls, không '//' thừa
q     = url.Values{}; q.Set("Expires", strconv.FormatInt(now.Add(ttl).Unix(), 10))
if projectID != "" { q.Set("Project", projectID) }          // thứ tự chèn: Expires, Project
canon = method + " " + base + "?" + encodeInOrder(q)         // KHÔNG dùng url.Values.Encode (sắp xếp); dùng thứ tự chèn
sig   = base64.StdEncoding(rsa.SignPKCS1v15(rand, key, SHA256, sha256(canon)))
return base + "?" + encodeInOrder(q) + "&Signature=" + url.QueryEscape(sig)
```
`method` ∈ {`GET`, `PUT`}. Tương thích Medplum: chữ ký `RSA-SHA256`, đệm PKCS#1 v1.5, base64 chuẩn (có `+`, `/`, `=` nên bắt buộc percent-encode khi đặt vào query). Định dạng chuỗi ký của Medplum là `result.toString()` của WHATWG `URL`; với `id`/`versionId` là UUID/số và `Project` là UUID thì chuỗi này khớp với dựng thủ công (khác biệt chuẩn hóa chỉ xuất hiện với ký tự cần mã hóa: **chưa xác nhận**, kiểm bằng vector chéo ở S2).

**Xác minh** (`Signer.Verify(method string, r *http.Request) (Claims, error)`), theo thứ tự B2.4:
1. `Signature` rỗng → `ErrMissingSignature` (401).
2. `Expires` rỗng/không phải số → `ErrExpired` (410, như Medplum `:66`; đề xuất: **không phải số** cũng 410 thay vì để `NaN` lọt qua bước kiểm hạn, `routes.ts:66`; vì `Expires` nằm trong chuỗi ký nên không khai thác được, chỉ là vệ sinh); `now.Unix() > expires` → `ErrExpired` (410 `URL has expired`).
3. Đường dẫn: lấy đường dẫn **thô** (`r.URL.EscapedPath()` hoặc `RequestURI`), so với `mountPrefix + "/" + id[+ "/" + ver]` dựng lại; khác → 401. Từ chối khi `id`/`ver` không khớp `^[A-Za-z0-9\-.]{1,64}$` (FHIR id; chặn `..`, `\`, `%2e`).
4. Dựng `canon` = `method + " " + <gốc công khai cấu hình> + path + "?" + query bỏ tham số Signature, giữ thứ tự gốc`. **Đề xuất lệch an toàn hơn:** dùng gốc (scheme+host) từ `StorageBaseURL` cấu hình, không lấy từ `Host` request (Medplum lấy `req.protocol`+`Host`, `routes.ts:58`), để không phụ thuộc cấu hình proxy; chữ ký vẫn gắn host nhờ nằm trong chuỗi ký.
5. `rsa.VerifyPKCS1v15(pub, SHA256, sha256(canon), sig)`; GET thử thêm `canon` không tiền tố method **chỉ khi** `LegacyGETSignature` bật (Medplum luôn thử, `:85-88`; Go mặc định tắt vì không có URL cũ do Go phát hành, Q-S2).
6. Trả `Claims{ID, VersionID, ProjectID, Method, Expires}`; việc đọc Binary và kiểm `Project` thuộc `fhirbinary`.

Khóa: PEM; `x509.ParsePKCS8PrivateKey`/`ParsePKCS1PrivateKey`; khóa công khai suy ra từ khóa riêng (nạp một lần khi khởi động, **không cache vĩnh viễn kiểu lỗi**; hỗ trợ danh sách khóa xác minh để xoay, Q-S3). Mật khẩu PEM mã hóa kiểu cũ (`aes-256-cbc`) của Medplum: `x509.DecryptPEMBlock` (đã deprecated, **chưa xác nhận** còn trong Go 1.26): hỗ trợ tùy chọn để dùng khóa Medplum sẵn có, mặc định khuyến nghị khóa PKCS#8 không mật khẩu lưu bằng quyền tệp 0600.

### B5.2 Hết hạn URL

TTL cố định 1 giờ ở Medplum (`presign.ts:23-25`, không cấu hình). Go: `STORAGE_URL_TTL` (mặc định `1h`), tối đa 24 h; đồng hồ qua `Clock` để test (không `time.Sleep`). Kiểm hạn dùng giây nguyên, `>` (hết hạn khi `now > expires`; đúng tại giây `expires` còn hợp lệ).

### B5.3 Ghi dạng luồng

```
Put(ctx, key, r, opts):
  p := safePath(key)               // os.Root.MkdirAll(dir, 0o750); từ chối khóa ngoài gốc, ký tự NUL, segment ".." hoặc rỗng
  tmp := os.CreateTemp-equivalent trong cùng thư mục (tên ngẫu nhiên, KHÔNG dùng pid)
  n := io.Copy(tmp, io.LimitReader(r, opts.MaxBytes+1))   // vượt thì ErrTooLarge và xóa tmp
  tmp.Sync(); tmp.Close(); root.Rename(tmp, p)           // người đọc không bao giờ thấy tệp dở
  lỗi ở bất kỳ bước nào: xóa tmp, trả lỗi
```
Khác Medplum: Medplum ghi **đè tại chỗ** (`flags:'w'`, `filesystem.ts:42`) nên khi lỗi để lại tệp cụt và người đọc thấy nội dung dở. `Open` trả `ErrNotFound` cho `fs.ErrNotExist`; lỗi khác (quyền) là lỗi nội bộ, không gộp thành "not found" (Medplum gộp, `:59-62`). `Delete` idempotent. `Copy` ghi tạm rồi rename. Quyền thư mục 0750, tệp 0640 (đề xuất; ý tưởng từ `pkg/storage` cũ).

Cách ánh xạ khóa → đường dẫn: giữ bố cục Medplum `<base>/<id>/<versionId>` (bỏ **tiền tố** `binary/`, dùng `strings.CutPrefix`, không `Replace` như `filesystem.ts:103`: `'a/binary/b'.replace('binary/','')` ra `a/b`, một lỗi nhỏ), để thư mục dữ liệu Medplum hiện hữu đọc được (Q-S2).

### B5.4 Phục vụ GET/PUT

- GET: nạp Binary bằng **repo không tách project** (đường công khai, quyền do chữ ký), kiểm `meta.project == Project` (khi Binary có project thì **bắt buộc có** `Project` khớp; Medplum bỏ kiểm khi URL thiếu `Project`, `routes.ts:97-102`: đề xuất chặt hơn), mở `Store.Open` rồi stream; lỗi trước byte đầu → 404 (`Not Found`), sau byte đầu → hủy kết nối. Đặt `Content-Type` từ `binary.contentType`; thêm `X-Content-Type-Options: nosniff` (đã có ở `httpsec`), `Content-Security-Policy: default-src 'none'; sandbox` (đề xuất), `Content-Length` nếu biết, `Content-Disposition: attachment` hay không: Q-S8.
- PUT: bước kiểm 1-4 như trên, `Content-Type` (đã bỏ tham số, so với `binary.contentType` đã bỏ tham số; đề xuất; Medplum so nguyên chuỗi `:44`) → 400 `Content-Type mismatch`; `CheckMetadata(contentType)` → 400; `http.MaxBytesReader` theo `STORAGE_MAX_UPLOAD_BYTES` → 413; lỗi kho → **500** (Medplum 400, `:52-54`). Với Go **đề xuất** PUT chỉ chấp nhận khi `versionId` trong URL **bằng** `meta.versionId` hiện tại (nếu không, 409), tránh ghi đè nội dung bản mới (Q-S5). 200 khi thành công, thân rỗng.

### B5.5 Decorator Repository cho Binary (thay `fhir/repo.ts:1222-1262`)

`Wrap(inner fhirrouter.Repository, store Store, opt)` bọc `CreateResource`/`UpdateResource`/`Expunge`:
- Ghi Binary có `Data`: giải base64, kiểm `Base64MaxBytes`, **sau** khi `inner` cấp `versionId` ghi vào kho khóa `binary/<id>/<ver>` rồi **xóa `Data`**; nếu ghi kho lỗi thì bù trừ: xóa/hủy bản vừa tạo hoặc để Binary không nội dung (đọc ra 404, như ca `binary.test.ts:61`). Khác Medplum: Medplum biết `versionId` **trước** khi ghi DB và ghi kho trước (`repo.ts` gọi `handleBinaryUpdate` trong luồng ghi), Go chưa xác nhận `versionId` do kho cấp (`pkg/fhirrouter/router.go:80`: "chỉ cần gán meta (versionId, lastUpdated)"). Q-S9.
- Cập nhật không có `Data`: `Copy(existingKey, newKey)` (`repo.ts:1234-1237`); nếu bản cũ không có nội dung (404) thì bỏ qua chứ không làm hỏng cập nhật metadata (Medplum ném; **chưa xác nhận** phản ứng `copyFile` khi nguồn không tồn tại ở nhánh FS: `copyFile` của Node ném ENOENT, nên cập nhật Binary không nội dung thất bại; Go: đề xuất bỏ qua).
- Expunge: thu khóa mọi phiên bản (`binary/<id>/<ver>` cho từng dòng lịch sử, `repo.ts:1905-1909`), xóa **sau commit**, lỗi chỉ log (`loader.ts:46-60`). Go đã có `Expunger` (`pkg/fhirstore/crud.go:518-544`).

### B5.6 Viết lại tệp đính kèm

Giữ hai chế độ, đệ quy qua map/slice, khóa `url` và `path` kiểu chuỗi, bỏ qua resource `Binary`, cache theo giá trị, trả nguyên đầu vào nếu không có URL Binary, **không đột biến**. Nhận dạng: `<publicBase>/fhir/R4/Binary/`, `<StorageBaseURL>/`, `Binary/`; bỏ phần `?query` trước khi tách (sửa lỗi nhỏ ở B2.7). `PRESIGNED_URL` gọi `ReadVersion` khi có `_history/<ver>`, nếu không `ReadResource`, bằng **repo của người gọi** (kiểm quyền gồm `securityContext`), lỗi thì `Binary/<id>`. Hoãn (Q-S10).

## B6. Cổng, thư viện, biến cấu hình

**Cổng:** mục 12.2 (`Store`, `Signer`, `Clock`); trong `fhirbinary`: `BinaryRepo` (thực chất `fhirrouter.Repository`), `Authorizer{CanUpdate(ctx, id) bool}` cho `$presigned-url?upload=true`, `SystemRepo` (repo không tách project cho đường `/storage`).

**Thư viện:** stdlib (`crypto/rsa`, `crypto/sha256`, `crypto/x509`, `encoding/pem`, `os` (`os.Root`), `mime`, `compress/gzip`, `compress/flate`, `net/http`). **Không cần thư viện ngoài** cho đợt này. `os.Root` có từ Go 1.24 (`go.mod` ghi `go 1.26.0`); `pkg/storage` cũ đã dùng nó (chỉ đọc, ngoài repo). S3 sau này: `github.com/aws/aws-sdk-go-v2` (ngoài source, chưa kiểm; đã gỡ khỏi `go.mod`).

**Biến cấu hình (đề xuất):**

| Biến | Ý nghĩa | Mặc định |
|---|---|---|
| `BINARY_STORAGE` | `file:<thư mục>` (giữ định dạng Medplum); tiền tố khác bị từ chối khi khởi động với lỗi rõ ràng | bắt buộc nếu bật Binary thô, `/storage`, hoặc DICOM |
| `STORAGE_BASE_URL` | gốc công khai của URL ký (host phải là host mà client nhìn thấy) | `<FHIR_BASE_URL>/storage` |
| `STORAGE_SIGNING_KEY_PATH` | PEM khóa riêng RSA | production: bắt buộc; local: tự sinh khóa tạm có cảnh báo |
| `STORAGE_SIGNING_KEY_PASSPHRASE` | mật khẩu PEM mã hóa (tùy chọn) | rỗng |
| `STORAGE_SIGNING_PREVIOUS_KEYS` | danh sách PEM công khai/riêng chỉ để **xác minh** (xoay khóa) | rỗng |
| `STORAGE_URL_TTL` | thời hạn URL | `1h`, tối đa `24h` |
| `STORAGE_MAX_UPLOAD_BYTES` | trần tải lên (PUT và POST/PUT Binary, sau giải nén) | Q-S7 (đề xuất 256 MiB) |
| `STORAGE_LEGACY_GET_SIGNATURE` | chấp nhận chữ ký GET kiểu cũ | `false` |
| `FHIR_BASE64_BINARY_MAX_BYTES` | trần `data` base64 khi ghi | 1 MiB (như Medplum) |

## B7. Danh sách ca test (từ `*.test.ts`)

### `storage/routes.test.ts`

| Ca gốc | Dòng | Kỳ vọng | Quyết định |
|---|---|---|---|
| Missing signature | 60 | GET có `Expires`, không `Signature` → 401 | Giữ |
| Missing expires | 65 | GET `Signature=xyz` không `Expires` → 410 | Giữ |
| Invalid signature | 70 | `Expires` xa + `Signature=xyz` → 401 | Giữ |
| Success | 77 | URL ký thật → 200 | Giữ |
| Binary not found | 83 | URL cho id lạ → 404 | Giữ |
| File not found | 93 | Binary có nhưng chưa có nội dung → 404 | Giữ |
| Write success | 108 | PUT `text/plain` → 200, đọc lại đúng `foo bar baz quux` (và body parser toàn cục không nuốt thân) | Giữ (kiểm route nằm ngoài `BodyLimit`) |
| Write mismatched content type | 116 | `application/json` khác `text/plain` → 400 | Giữ |
| Write missing signature | 126 | PUT không `Signature` → 401 | Giữ |
| Write missing expires | 131 | → 410 | Giữ |
| Write invalid signature | 136 | → 401 | Giữ |
| Request path must match the signed URL: GET/PUT `..\`, GET `%2e%2e\`, GET `/api/storage/.../..\..\..\storage\` (4 ca) | 153-170 | luôn 401, gửi đường dẫn thô bằng `http.NewRequest` + `net` (không chuẩn hóa) | Giữ |
| Presigned URL includes Project before Signature | 195 | thứ tự `Project` < `Signature` | Giữ |
| Presigned URL omits Project when binary has no project | 206 | vắng `Project` | Giữ |
| GET success with Project param | 212 | → 200 | Giữ |
| GET with Project of another project fails | 218 | Binary thuộc project A, URL ký với project B → 401 | Giữ |
| GET tampered Project fails signature | 227 | sửa `Project` → 401 | Giữ |
| PUT success with Project param | 233 | → 200 | Giữ |
| PUT tampered Project fails signature | 242 | → 401 | Giữ |

### `storage/filesystem.test.ts`

| Ca gốc | Dòng | Kỳ vọng | Quyết định |
|---|---|---|---|
| Undefined binary storage | 15 | cấu hình `binary` (không có tiền tố) → lấy kho lỗi | Đổi: khởi động báo lỗi rõ ràng |
| File system storage | 20 | ghi `foo` rồi đọc lại | Giữ |
| deleteFile removes a file and is idempotent | 51 | xóa rồi đọc lỗi `File not found`; xóa lần hai không lỗi | Giữ (`ErrNotFound`) |
| Should throw an error when file is not found in readBinary() | 70 | `File not found` | Giữ |
| Should throw an error when the key is outside of the base directory | 88 | id `..` + version `package.json`, `../outside.txt` cho read/write/copy/delete → `Invalid storage key` | Giữ (+ symlink, NUL) |

### `fhir/binary.test.ts` (14 ca)

| Ca gốc | Dòng | Kỳ vọng | Quyết định |
|---|---|---|---|
| Create and read binary | 30 | POST `text/plain` → 201; GET → `Hello world`; GET `Accept: application/fhir+json` → resource `Binary` | Giữ |
| Read binary not found | 54 | 404 | Giữ |
| Read binary without stored content | 61 | 404 OperationOutcome, `Content-Type` FHIR JSON, không `ETag`/`Last-Modified`; log `Error reading Binary content` với `binary`, `err`, `versionId` | Giữ |
| Update and read binary | 94 | PUT thay nội dung | Giữ |
| Binary CORS | 117 | `Access-Control-Allow-Origin` đúng origin | Giữ (gắn `httpsec.CORS`) |
| Unsupported content encoding | 128 | `Content-Encoding: fake` → 400 | Giữ |
| Deflate | 138 | → 201, GET ra `Hello world` | Giữ |
| GZIP | 155 | tương tự | Giữ |
| Update with GZIP | 172 | PUT gzip | Giữ |
| Upload binary in batch | 196 | transaction: Binary có `data` base64, DocumentReference tham chiếu `urn:uuid`; kho có `Hello World!` | Giữ (cần B5.5) |
| Update JSON | 242 | PUT JSON `Binary` với `securityContext` → 200, `securityContext.reference` giữ | Giữ |
| Invalid Binary JSON | 276 | `data` không phải base64 → 400, GET không thấy `data` | Giữ |
| Handle non-binary JSON | 303 | `{"resourceType":"Patient"}` lưu như dữ liệu thô | Giữ (phụ thuộc Q-S4) |
| Error for disallowed file type | 328 | `application/x-msdownload` → 400 `invalid`; `?_filename=foo.exe` → 400 `invalid` | Giữ (+ ca mới: **không để lại Binary mồ côi**) |

### `fhir/operations/binary-presigned-url.test.ts`

| Ca gốc | Dòng | Kỳ vọng | Quyết định |
|---|---|---|---|
| Full write-read cycle | 43 | POST Binary, `$presigned-url?upload=true`, PUT đến URL ký → 200 | Giữ |
| Requires update permission to generate upload link | 77 | người chỉ `create`,`read`: URL đọc 200, URL tải lên 403 | Giữ |
| Linked project cannot generate upload link | 110 | project liên kết đọc được, upload 403 | Hoãn (Go chưa có liên kết project) |
| Requires securityContext read access to generate presigned URL | 150 | không đọc được `Patient` (securityContext) → 403; người có quyền → 200 | Giữ |

### `fhir/rewrite.test.ts` (16 ca; chỉ khi làm Q-S10)

| Ca gốc | Dòng | Kỳ vọng | Quyết định |
|---|---|---|---|
| Null / Undefined | 39 / 44 | trả nguyên | Giữ |
| Binary | 49 | không viết lại trong resource `Binary` | Giữ |
| Returns input unchanged when there are no attachments | 65 | cùng một đối tượng | Giữ |
| Does not mutate the input when rewriting | 76 | đầu vào không đổi | Giữ |
| Finds attachments nested below a non-binary url property | 89 | tìm sâu dưới `url` không phải Binary | Giữ |
| Other URL | 109 | URL ngoài giữ nguyên | Giữ |
| Reference string / with version | 124 / 145 | `Binary/<id>[/_history/<v>]` thành URL ký có `Expires` | Giữ |
| Reference not found | 166 | giữ `Binary/<id>` | Giữ |
| URL / URL with version | 188 / 209 | `<base>fhir/R4/Binary/...` thành URL ký | Giữ |
| FHIR URL to reference | 230 | chế độ `REFERENCE` | Giữ |
| Storage URL to reference | 249 | URL kho (không version) thành `Binary/<id>` | Giữ |
| Consistent results | 268 | cùng URL cho cùng Binary | Giữ |
| Security context | 292 | 4 repo: đọc được cả hai → viết lại; thiếu quyền `Patient` → giữ; policy giới hạn đúng patient → viết lại; policy patient khác → giữ | Giữ |

### `fhir/operations/binary-scan.test.ts` (15 ca): **Bỏ** (S3/GuardDuty).

### Ca MỚI nên thêm

Hợp đồng chung `storetest` cho mọi `Store` (ghi/đọc/chép/xóa, đồng thời, tệp rỗng, tệp lớn, hủy giữa chừng không để lại tệp dở); vector chữ ký chéo với Node (S2); URL hết hạn đúng biên (`now == expires` còn hạn, `now == expires+1` hết); `Expires` không phải số → 410; khóa PEM sai/thiếu → khởi động lỗi; GET phục vụ đúng **phiên bản** trong URL (Q-S5); PUT lên URL phiên bản cũ → 409; PUT vượt trần → 413, không để lại tệp dở; `Content-Type` có tham số (`text/plain; charset=utf-8`) khớp `text/plain`; loại bị chặn kèm tham số (`application/x-sh; charset=x`) bị chặn; **tệp bị chặn không tạo Binary mồ côi**; giải nén vượt trần → 413; log truy cập không chứa `Signature`; tên tệp có NUL/`..`; ca ghi đồng thời cùng khóa; ca đọc khi đang ghi (thấy bản cũ hoặc bản mới đầy đủ, không cụt); `data` base64 vượt `FHIR_BASE64_BINARY_MAX_BYTES` → 400.

## B8. Bảo mật: kiểm soát bắt buộc có test

| # | Kiểm soát | Test |
|---|---|---|
| S-SEC1 | Chữ ký gắn với **phương thức** (GET/PUT), **đường dẫn đầy đủ**, `Expires`, `Project`, host công khai | routes 60-136, 195-249 + ca mới (đổi host, đổi method) |
| S-SEC2 | Chống chèn đường dẫn: bắt buộc đường dẫn thô khớp đúng; id/version theo regex; `os.Root` chống symlink | routes 153-170, filesystem 88 |
| S-SEC3 | Cách ly project: `Binary.meta.project` phải khớp `Project`; không dùng URL ký của project này cho Binary của project khác | routes 218, 227, 242 |
| S-SEC4 | Hạn URL, kiểm bằng `Clock`; URL không có chữ ký không dùng được | ca biên |
| S-SEC5 | Trần kích thước tải lên và sau giải nén (chống đầy đĩa, bom nén) | ca mới |
| S-SEC6 | Tên/loại tệp bị chặn, bỏ tham số trước khi so | `storage/utils` + ca mới |
| S-SEC7 | Phản hồi tải xuống không thể thực thi script trong origin API: `nosniff`, `Content-Security-Policy: sandbox`, quyết định `Content-Disposition` (Q-S8) | ca kiểm header |
| S-SEC8 | **Không ghi `Signature`** (và toàn bộ query) vào nhật ký truy cập; Medplum ghi `originalUrl` (`app.ts:356`) | ca với bộ ghi log giả |
| S-SEC9 | Khóa ký: nạp từ tệp quyền 0600, không từ biến môi trường dạng rõ ở production; không tự sinh khóa ở staging/production; hỗ trợ xoay khóa | ca cấu hình |
| S-SEC10 | Thư mục dữ liệu quyền 0750/tệp 0640; ghi nguyên tử | ca FS |
| S-SEC11 | `$presigned-url` kiểm quyền đọc (gồm `securityContext`) và quyền cập nhật khi `upload=true` | binary-presigned-url 77, 150 |
| S-SEC12 | Lỗi kho không lộ đường dẫn thật ra ngoài; không phân biệt "Binary không có" và "tệp không có" trên đường công khai ngoài 404 | ca mới |
| S-SEC13 | Rate limit cho `/storage` (đường công khai không có Bearer); Medplum đặt limiter chung trước (`app.ts:222`) | ca khi có gói rate limit nền |
| S-SEC14 | CORS chỉ origin được phép, tiền tố `/storage/` | ca `httpsec` |

## B9. KHÔNG port đợt này và điểm lệch chấp nhận được

| Hạng mục | Lý do |
|---|---|
| `cloud/aws/storage.ts` (S3, CloudFront signer, SSE-C, tagging) | AWS SDK đã gỡ khỏi `go.mod` (grep ở A1); quyết định M5 (`coverage-matrix.md:50`) "S3 sau". Cần cho prod nếu nhiều replica (hệ thống tệp cục bộ không chia sẻ được): **rủi ro triển khai**, Q-S12 |
| `cloud/azure/storage.ts`, `cloud/gcp/storage.ts` | Không có nhu cầu nêu ra; cùng giao diện `Store` nên thêm sau được |
| `$scan` (`binary-scan.ts`) | Chỉ S3 + GuardDuty (`:76-81`) |
| `config/ loadAwsConfig` | Không liên quan |
| `readFileByUrlForTests` (`filesystem.ts:65-74`) | Chỉ cho test |
| Chế độ giao URL ký cho kho ngoài (`getPresignedUrl` của kho, `loader.ts:70-74`) | Chỉ có kho nội bộ |
| `signingKeyId`, URL CloudFront | Chỉ S3 |
| `projectclone`, `projectrestore`, `botinit`, `deploy`, `packageinstall`, `smarthealthlinks`, `claimexport`, `textract`, `bots`... gọi `getBinaryStorage()` | Thuộc module khác; chỉ dùng `Store` khi các module đó được port |
| Liên kết project (`Linked project cannot generate upload link`) | Go chưa có liên kết |

Lệch chấp nhận được: (1) phục vụ đúng phiên bản trong URL, PUT chỉ trên bản hiện tại (Q-S5); (2) lỗi ghi kho thành 500 thay vì 400; (3) ghi nguyên tử thay vì đè tại chỗ; (4) có trần kích thước; (5) so `Content-Type` sau khi bỏ tham số; (6) kiểm `Project` chặt hơn; (7) không có chữ ký GET kiểu cũ mặc định; (8) host trong chữ ký lấy từ cấu hình thay vì header `Host`; (9) không tự sinh khóa ở production.

## B10. Rủi ro và câu hỏi mở (Q-S)

| # | Câu hỏi / rủi ro | Đề xuất |
|---|---|---|
| Q-S1 | Chỗ đặt `/storage` và Binary thô: `fhir-server` (cần tách khỏi `BodyLimit("16M")`) hay app riêng | `fhir-server` |
| Q-S2 | Có cần **tương thích dữ liệu/URL với Medplum đang chạy** (M2 của `coverage-matrix.md:47`): bố cục `<base>/<id>/<ver>`, đọc khóa PEM mã hóa, chữ ký GET kiểu cũ | Giữ bố cục khóa; chữ ký cũ tắt; PEM mã hóa tùy chọn |
| Q-S3 | Định dạng và xoay khóa ký | PKCS#8 không mật khẩu; danh sách khóa xác minh |
| Q-S4 | Quy tắc đặc biệt "thân là resource Binary": chỉ `application/fhir+json` nguyên chuỗi (Medplum, `binary.ts:49`) hay bỏ tham số và thêm `application/json`? Cách `pkg/fhirrouter` hiện xử lý `application/json` cho Binary: chưa xác nhận | Bỏ tham số, nhận cả `application/fhir+json` và `application/json` |
| Q-S5 | Phục vụ phiên bản đúng như URL, và 409 khi PUT lên URL cũ (lệch với Medplum) | Có |
| Q-S6 | `data` base64 hiện nằm trong CSDL Go (bulk export, `bulk.go:178`): chuyển sang kho khi ghi (như Medplum `repo.ts:1230`) và di trú dữ liệu cũ? Khi chuyển, đọc dữ liệu cũ vẫn phải giải base64 | Chuyển từ nay; dữ liệu cũ đọc được (giữ đường giải base64) |
| Q-S7 | Trần tải lên (Medplum không có trong mã đã đọc) và trần giải nén | 256 MiB, cấu hình được |
| Q-S8 | Tải xuống `inline` hay `attachment`; CSP `sandbox` có làm hỏng xem PDF/ảnh trong trình duyệt (chưa xác nhận) | Spike nhỏ; mặc định `attachment` cho loại không phải ảnh/PDF |
| Q-S9 | Thứ tự ghi nội dung và ghi DB (xem B5.5): mồ côi khi một bên lỗi; có cần việc quét dọn | Ghi DB rồi kho, bù trừ khi lỗi; công cụ quét sau |
| Q-S10 | `rewriteAttachments`: làm đợt này hay sau; vị trí gắn trong `fhirrouter` (tầng ghi và tầng phản hồi) | Sau, khi có client tải tệp qua `Attachment.url` |
| Q-S11 | Nhật ký truy cập hiện do `trivita-observability-go` (`echo4obs`): có thể cấu hình bỏ query không | Cần kiểm; nếu không, che ở middleware riêng |
| Q-S12 | Khi nào làm S3 (nhiều replica) và bằng SDK nào | Trước khi chạy nhiều replica ở production |
| Rủi ro | Hệ thống tệp cục bộ không chia sẻ giữa replica; `Signature` gắn host nên đổi `STORAGE_BASE_URL` làm mọi URL cũ vô hiệu | Ghi vào README triển khai |
| Rủi ro | `fhir-server` hiện không có luồng xác thực cho `/storage` (đường công khai bằng chữ ký), cần "repo hệ thống" không tách project | Thêm tham số `SystemRepo` có kiểm soát, chỉ dùng trong `StorageHandler` |
| Rủi ro | Binary hiện có trong CSDL Go (`Data`) và hợp đồng bulk export (`$export` ghi Binary ndjson) đổi theo Q-S6 | Làm sau khi có test hồi quy export |

## B11. Các bước làm

| Bước | Việc | Cỡ | Phụ thuộc | Tiêu chí hoàn thành |
|---|---|---|---|---|
| S0 | Chốt Q-S2..Q-S5, Q-S7, Q-S8 (spike CSP/PDF 0,5 ngày); sinh **vector chéo** chữ ký bằng một script Node chạy ngoại tuyến (`createSign('sha256')` trên `GET <url>`), lưu vector vào `testdata` | S | không | Bảng quyết định + tệp vector (khóa thử, chuỗi ký, chữ ký) |
| S1 | `pkg/binarystore`: `Store`, `Key`, `CheckMetadata`, adapter `FS` (`os.Root`, ghi nguyên tử, trần), bộ test hợp đồng `storetest`; cập nhật `architecture_test.go` | M | không | Ca B7 filesystem + ca mới; `go test -race` xanh; **mở khóa DICOM D0** (`cds-dicom/dicom.md:62`) |
| S2 | `Signer` (Sign/Verify, khóa PEM, `Clock`, TTL) trong `pkg/binarystore` | M | S0, S1 | Xác minh được vector Node; ca biên hạn; khóa sai báo lỗi khởi động |
| S3a | `pkg/fhirbinary.StorageHandler` (`GET|PUT /storage/{id}[/{ver}]`) + gắn vào `fhir-server` ngoài `BodyLimit`, header, CORS | M | S1, S2, E1 không cần | Toàn bộ `routes.test.ts` (A) và 4 ca đường dẫn thô; e2e qua HTTP thật |
| S3b | Binary thô POST/PUT/GET (`Content-Encoding`, quy tắc `fhir+json`, `_filename`, `X-Security-Context`, trần) + decorator `Wrap` (B5.5) + thay `binaryRaw` | L | S1, S3a | Toàn bộ `binary.test.ts` (14 ca) + ca mới (không mồ côi, bom nén); hồi quy `$export` xanh |
| S3c | `$presigned-url` (đọc theo quyền, upload cần quyền cập nhật) | S | S3a, S3b | 3 ca `binary-presigned-url.test.ts` |
| S4 | Cấu hình `BINARY_STORAGE`, `STORAGE_*`, `.env.example`, README, OpenAPI; kiểm khởi động (production cấm khóa tạm) | S | S3a | Ca cấu hình; `make test` xanh |
| S5 | Bàn giao DICOM: ví dụ tách luồng (`io.TeeReader`/`io.Pipe`) vào `Store.Put` và đọc 100 KB đầu bằng `Open` + `io.LimitReader` | S | S1 | Ví dụ chạy trong test của `binarystore` |
| S6 | (Sau) `rewriteAttachments` (Q-S10) | M | S3b | 16 ca `rewrite.test.ts` |
| S7 | (Sau) Adapter S3 theo `storetest` (Q-S12) | M | S1 | `storetest` xanh trên S3 giả |

**Thứ tự với DICOM:** S1 là điều kiện để bắt đầu D0. S2-S3 không chặn DICOM (DICOM ghi/đọc nội dung bằng `Store`, không cần URL ký), trừ khi DICOM trả URL ký cho client (chưa có trong kế hoạch DICOM). **Thứ tự với `auth`:** không phụ thuộc; hai phần chỉ gặp nhau ở `pkg/safehttp` (E1, dùng cho email, không dùng cho storage).

---

# PHẦN C. THIẾT KẾ GIAO DIỆN DÙNG CHUNG (MỘT BẢN DUY NHẤT)

Đây là **đề xuất giao diện**, không phải mã. Tên và chữ ký có thể chỉnh khi cài đặt, nhưng **chỉ có một định nghĩa** mỗi loại; `auth`, email, DICOM, storage đều dùng.

## 12.1 `pkg/mailer`

Gói gốc chỉ dùng stdlib; `ProjectSMTPFromSecrets` nhận `[]Setting` (`Name`, `String`, `Int *int`, `Bool *bool`) của riêng gói để không import `pkg/fhir`; chỉ gói con `smtp` import `pkg/safehttp` (rule `pkg/mailer: {pkg/safehttp}`).

```go
package mailer

type Address struct{ Name, Email string } // Email bắt buộc

type Attachment struct {
    Filename, ContentType, Disposition, CID string
    Body io.Reader // luồng; đã qua giới hạn byte
}
type ICalEvent struct{ Method, Filename string; Content string }

// ProjectContext: thông tin theo project mà người gọi đã tra sẵn. nil = dùng cấu hình server.
type ProjectContext struct {
    ID      string
    AppName string       // đã TrimSpace; rỗng = chưa white-label
    SMTP    *ProjectSMTP // nil = project chưa cấu hình SMTP riêng
}
type ProjectSMTP struct {
    SMTPConfig
    FromAddress     string   // bắt buộc
    ApprovedSenders []string // đã cắt khoảng trắng, chữ thường
}
type SMTPConfig struct{ Host string; Port int; Username, Password string; Secure *bool } // nil => Port==465

type Message struct {
    From    *Address // chỉ là đề nghị; chọn cuối cùng bởi ResolveFrom
    To, Cc, Bcc, ReplyTo []Address
    Subject, Text, HTML string
    Attachments []Attachment
    ICal        *ICalEvent
    Project     *ProjectContext
}

type Mailer interface{ Send(ctx context.Context, m Message) error }

// Lỗi có phân loại, để lớp HTTP ánh xạ mã trạng thái.
type ErrorKind int // Invalid (400), Config (400), Transport (5xx)
type Error struct{ Kind ErrorKind; Msg string; Err error }
```
Hàm thuần: `ResolveFrom(requested *Address, p FromPolicy) Address`, `ApplyDisplayName`, `ExtractEmail`, `ProjectSMTPFromSecrets(secrets []Setting, allow bool) (*ProjectSMTP, error)` (trả `Kind=Config` đúng thông điệp A2.3), `ProjectAppName(settings []Setting) string`, `Configured(server *SMTPConfig, p *ProjectContext) bool`.
Adapter: `mailer.Nop` (bỏ qua như `email.ts:62`), `mailer.Log` (dev: in nội dung, **chỉ ở local**), `mailer.Fake` (ghi lại thư để test), `mailer/smtp.Mailer` (thật; nhận `safehttp` dialer, `RequireTLS`, danh sách người gửi được duyệt, `SupportAddress`, `AllowProjectSMTP`, `AllowUnsafeOutbound`).

**Cách `auth` dùng:** use case của auth chỉ gọi `Send(ctx, mailer.Message{To: ..., Subject: ..., Text: ..., Project: ...})`; tập con khớp `Send(ctx, Message{To, Subject, Text, Project})` ở `p4-p5-register-mfa.md:294`. Không tạo cổng `Mailer` thứ hai trong `apps/auth/internal/application/ports.go` (dùng `mailer.Mailer`, cần sửa rule ở 0.3). Với test auth: dùng `mailer.Fake`.

## 12.2 `pkg/binarystore`

```go
package binarystore

var ErrNotFound, ErrTooLarge, ErrInvalidKey, ErrBlockedFileExtension, ErrBlockedContentType error

// Key trả "binary/<id>/<versionID>"; trả ErrInvalidKey nếu id/versionID không khớp ^[A-Za-z0-9\-.]{1,64}$.
func Key(id, versionID string) (string, error)

type PutOptions struct{ ContentType string; MaxBytes int64 } // MaxBytes <= 0: không trần

type Store interface {
    Put(ctx context.Context, key string, r io.Reader, opts PutOptions) (written int64, err error) // ghi nguyên tử; lỗi thì không để tệp dở
    Open(ctx context.Context, key string) (io.ReadCloser, error)                                   // ErrNotFound nếu không có
    Copy(ctx context.Context, srcKey, dstKey string) error
    Delete(ctx context.Context, key string) error                                                  // idempotent
}

// Tùy chọn, adapter ngoài (S3...) có thể cài: cấp URL ký phía kho.
type URLPresigner interface{ PresignURL(ctx context.Context, key, method string, ttl time.Duration) (string, error) }

func CheckMetadata(filename, contentType string) error // bỏ tham số của contentType; đuôi theo danh sách B2.2

type Clock interface{ Now() time.Time }
type SignerConfig struct{ BaseURL string; Key *rsa.PrivateKey; VerifyKeys []*rsa.PublicKey; TTL time.Duration; Clock Clock; LegacyGET bool }
type Signer struct{ /* ... */ }
func (s *Signer) Sign(method, id, versionID, projectID string) (string, error)
func (s *Signer) Verify(method string, r *http.Request) (Claims, error) // ErrMissingSignature | ErrExpired | ErrBadSignature | ErrBadPath
type Claims struct{ ID, VersionID, ProjectID, Method string; Expires time.Time }
```
Adapter: `binarystore.FS` (`NewFS(dir string) (*FS, error)`), `binarystore/storetest` (bộ test hợp đồng, dùng chung cho mọi adapter, gồm S3 sau này), `binarystore.Memory` (cho test của DICOM/email).

**Cách DICOM dùng:** `Put(ctx, Key(instanceBinaryID, ver), io.TeeReader(part, headBuf), PutOptions{ContentType:"application/dicom"})`, đọc 100 KB đầu qua `Open` + `io.LimitReader`; `Delete` khi Binary bị xóa. **Cách email dùng (hoãn):** `Open` để đính kèm. **Cách `fhirbinary` dùng:** mọi đường đọc/ghi Binary.

## 12.3 `pkg/safehttp` (trích từ `pkg/fhirsub/ssrf.go`)

```go
package safehttp

var ErrBlocked error // văn bản gồm "unsafe hostname <h> is blocked" / "unsafe address <h> is blocked"

func Blocked(ip netip.Addr) bool // dải IANA đặc biệt + 198.18.0.0/15; Unmap IPv4-mapped IPv6
func ParseURL(raw string, o URLOptions) (*url.URL, error) // HTTPSOnly, cấm userinfo, cấm host localhost/*.localhost/IP không an toàn

type Options struct{ Timeout time.Duration; AllowPrivate, HTTPSOnly bool; MaxRedirects int; MaxBodyBytes int64 }
func NewClient(o Options) *http.Client // dialer kiểm TẤT CẢ địa chỉ phân giải + Control; CheckRedirect kiểm lại; không proxy từ môi trường
func DialContext(o DialOptions) func(ctx context.Context, network, addr string) (net.Conn, error) // cho SMTP
```
Hành vi `pkg/fhirsub` hiện có được giữ khi `Options{HTTPSOnly:false, MaxRedirects:0}` (webhook).

## 12.4 Ai dùng gì

| Bên dùng | `mailer` | `binarystore` | `safehttp` | `fhirbinary` |
|---|---|---|---|---|
| `auth` P4/P5 (thư đặt lại, xác minh, mã MFA) | `Mailer`, `Message`, `Fake` | | | |
| `auth` P6 (đăng nhập ngoài) | | | `NewClient` | |
| Route `/email/v1/send` | toàn bộ | (hoãn) `Open` | `NewClient`, `DialContext` | |
| `fhir-server` Binary thô, `/storage`, `$presigned-url` | | `Store`, `Signer`, `CheckMetadata` | | toàn bộ |
| `dicomweb` | | `Store` | | `Wrap` (Binary), tùy chọn |
| bulk export (`pkg/fhirrouter`) | | qua cổng `BinaryContent` trong `fhirrouter.Options` | | `Wrap` |
| CDS executor | | | `NewClient` | |
| `pkg/fhirsub` (webhook) | | | `NewClient` (refactor) | |

Cổng đọc nội dung Binary trong `fhirrouter` (thay `binaryRaw` ở `bulk.go:356`) khai báo **trong `pkg/fhirrouter`** (`BinaryContent interface{ Open(ctx, *fhir.Binary) (io.ReadCloser, error) }`) để `pkg/fhirrouter` **không** import `pkg/binarystore` hay `pkg/fhirbinary` (`pkg/fhirbinary` đã import `fhirrouter`, import ngược sẽ vòng).

---

# PHẦN D. THỨ TỰ TOÀN CỤC VÀ PHỤ THUỘC GIỮA CÁC DỰ ÁN

```
E1 pkg/safehttp ──► E3 SMTP adapter ──► E4 route email
                └─► (cds-dicom bước 1, auth P6)
E2 pkg/mailer ──► auth 4.2 (log/fake)  ──► auth P4/P5
              └─► E3
S1 pkg/binarystore ──► DICOM D0
                   └─► S2 Signer ──► S3a /storage ──► S3b Binary thô + Wrap ──► S3c $presigned-url ──► S4
                                                        └─► E5 (đính kèm Binary/, hoãn)
```
Việc đầu tiên nên làm (không phụ thuộc, mở khóa nhiều bên): **E1, E2, S1**. Tổng thô: email ≈ 1 tuần (E0 S + E1 M + E2 M + E3 M + E4 M + E6 S, với E1 dùng chung), storage ≈ 2 đến 2,5 tuần (S0 S + S1 M + S2 M + S3a M + S3b L + S3c S + S4 S + S5 S).

# PHẦN E. PHỤ LỤC: ĐIỂM CẦN KIỂM THÊM TRƯỚC KHI CODE (chưa xác nhận)

1. Chuỗi thông điệp mặc định của `check('content-type').equals(...)` (express-validator, ngoài source).
2. Cách nodemailer xử lý `secure` khi `undefined` ở cổng 465 (ngoài source).
3. Danh sách đầy đủ dải mà `ipaddr.js range() !== 'unicast'` coi là không an toàn (ngoài source); đề xuất dùng bảng IANA special-purpose và đối chiếu bằng test.
4. Số redirect tối đa của `fetch` (undici) khi tải tệp đính kèm (ngoài source).
5. `versionId` của Go do `fhirstore` hay router cấp (`pkg/fhirstore/crud.go`: grep `VersionId` không thấy; đọc `pkg/fhirrouter/router.go:80`: "chỉ cần gán meta") — quyết định B5.5.
6. API đăng ký thao tác `$presigned-url` trên `pkg/fhirrouter` (vị trí thêm thao tác theo instance cho `Binary`).
7. Cấu hình bỏ query khỏi nhật ký truy cập của `trivita-observability-go` (Q-S11).
8. `x509.DecryptPEMBlock` còn dùng được trong Go 1.26.
9. `go-mail`: tùy chọn dial tùy chỉnh, chính sách TLS, `icalEvent`, chống chèn header, AUTH LOGIN (E0).
10. Claim `fhir_project`/vai trò admin mà auth phát (P3) đủ cho route email hay phải đọc membership từ CSDL (Q-E6).

## Tiến độ `pkg/mailer` (2026-11-22)

**Xong E2 và E3** (bám mục 12.1 và A5; không làm E4 `/email/v1/send`, E5, E6 phần route):
- `pkg/mailer` (stdlib): `Address`, `Message`, `Attachment`, `ICalEvent`, `ProjectContext`/`ProjectSMTP`/`SMTPConfig`, `Mailer`, `Error` có phân loại (`KindInvalid`/`KindConfig`/`KindTransport`), `Limits` (50 người nhận, 10 MiB), `Message.Validate` (từ chối CR/LF/NUL trong mọi giá trị đi vào header), hàm thuần `ResolveFrom` (danh sách được duyệt cắt khoảng trắng, không phân biệt hoa thường, dùng `net/mail` nên không bị lừa bởi `"<a@x.com>" <evil@y.com>`), `ApplyDisplayName`, `ProjectSMTPFromSecrets` (thông điệp lỗi của Medplum, chỉ liệt kê TÊN trường thiếu), `ProjectAppName`, `Configured`, bộ dựng MIME `Build`/`Envelope` (quoted-printable, base64, alternative/related/mixed, Bcc không vào header), `Nop`/`Log`/`Fake`.
- `pkg/mailer/smtp`: adapter thật chỉ dùng `net/smtp`, `crypto/tls`, `pkg/safehttp`. Q-E3: tự dựng MIME + `net/smtp`, không thêm thư viện. Q-E4: TLS bắt buộc mặc định (TLS ngay từ đầu hoặc STARTTLS), `AllowPlaintext` chỉ cho dev, TLS tối thiểu 1.2. Q-E5: project có SMTP riêng gửi được khi server không cấu hình. SMTP server được tin (relay nội bộ), SMTP project nối qua `safehttp.DialContext`. Timeout nối/lời chào 20s, nhàn rỗi 120s, tổng 5 phút; hủy context đóng kết nối. AUTH PLAIN hoặc LOGIN, chỉ sau TLS. Log không bao giờ chứa subject, nội dung, địa chỉ người nhận hay mật khẩu (chỉ host, cổng, mã SMTP).
- `pkg/safehttp.DialContext` (mới): từ chối tên nếu BẤT KỲ địa chỉ phân giải nào bị chặn, kiểm lại địa chỉ thật lúc nối (chống DNS rebinding), timeout tổng.
- `pkg/mailer/smtptest`: máy chủ SMTP giả (TLS tự ký, STARTTLS, TLS ngay từ đầu, AUTH, trễ, từ chối người nhận) chỉ cho test; `architecture_test.go` cấm import từ code không phải test.
- `architecture_test.go`: `pkgRules["pkg/mailer"]={pkg/safehttp}`; `apps/auth/internal/infrastructure` được import `pkg/mailer`.
- **auth**: `AUTH_MAIL_PROVIDER=smtp` + `AUTH_SMTP_*` (host, port, from, approved senders, username/password, secure, CA file, timeout, allow plaintext). Lệch so với kế hoạch: auth vẫn giữ cổng `application.Mailer` (thư văn bản thuần, nhiều test dùng `To string`) và dùng `infrastructure/mailer.Adapter` chuyển sang `pkg/mailer`; adapter chỉ trả thông điệp an toàn (không có phản hồi máy chủ). `application` không import `pkg/mailer`.
- Test: `go test -race ./pkg/mailer/...`, đột biến 55 chỗ ở `pkg/mailer`, `smtp`, `safehttp/dial.go` (các mutant còn sống sót là tương đương: kiểm lại giới hạn đính kèm trong `attachmentPart` và kẹp hạn chót trong `deadlineConn` đều bị che bởi kiểm tổng và việc đóng kết nối theo context; `checkAddress` có hai lớp). Test auth: config, adapter, HTTP với máy chủ SMTP giả, e2e hai tiến trình (`e2e/smtp_test.go`).

**Chưa làm:** E4 (`POST /email/v1/send`), E5 (`Binary/<id>`), SMTP riêng theo project ở `auth` (cần mã hóa `Project.secret`, Q-E7), hàng đợi thử lại, thử với nhà cung cấp SMTP thật.

**E4 xong (2026-11-22): `POST /email/v1/send`** ở `apps/auth` (Q-E1). Quyết định áp dụng theo đề xuất của kế hoạch: Q-E2 `Binary/<id>` trả 400 rõ ràng; Q-E5 project có SMTP riêng gửi được khi server không cấu hình; Q-E6 tính năng `email` và quyền admin đọc từ CSDL auth; Q-E8 50 người nhận, 10 MiB; Q-E9 lỗi SMTP là 502 với thông điệp chung; Q-E10 có `icalEvent`, `cid`, `contentDisposition`. Q-E7 (mã hóa `Project.secret`) vẫn mở nên SMTP riêng của project **tắt mặc định** (`AUTH_EMAIL_ALLOW_PROJECT_SMTP`). Route cũng tắt mặc định (`AUTH_EMAIL_ENABLED`). Mã: `application/email.go` (use case, cổng `EmailSender`, `AttachmentFetcher`), `presentation/httpapi/email.go` (validator gộp lỗi, parser địa chỉ), `infrastructure/attachfetch` (tải URL chặn SSRF), `domain.KindBadGateway`. `application` và `presentation` của auth được import `pkg/mailer` (stdlib, không phụ thuộc app). `pkg/mailer.ParseAddressList` mới. Hợp đồng chi tiết ở `docs/api-contract/others.md`. Test: application, adapter, config, HTTP (`bootstrap/email_http_test.go`, SMTP giả thật), e2e (`e2e/smtp_test.go`); đột biến toàn bộ phần mới (các mutant sống sót ban đầu đã được bổ sung test; chỉ còn lớp kiểm trùng lặp có chủ ý giữa validator của route và `Message.Validate`).

**Còn lại của email:** E5 (`Binary/<id>`, chờ `storage`), API đặt `Project.secret` kèm mã hóa (Q-E7) rồi mới nên bật SMTP project ở production, hạn mức gửi theo project (hiện chỉ có hạn mức IP), hàng đợi thử lại.


## Tiến độ `pkg/binarystore` (2026-11-22)

Xong S1, S2, S5 (chưa S0 đầy đủ, S3a trở đi):
- `Store` (`Put` nguyên tử có trần và kiểm loại chặn, `Open`, `Copy`, `Delete` idempotent), `Key`/`SplitKey` (từ chối `.`, `..`, `\`, `%`, NUL), `CheckMetadata` (bỏ tham số loại nội dung, bỏ dấu cách/chấm cuối tên).
- Adapter `FS` (`os.Root`, tệp tạm trong `_tmp/`, bố cục `<dir>/<id>/<ver>` như Medplum, symlink không thoát gốc) và `Memory`; bộ hợp đồng `storetest` chạy trên cả hai.
- `Signer` RSA-SHA256 (Sign/Verify, đường dẫn thô, gốc lấy từ `BaseURL` không từ `Host`, hạn `>`, xoay khóa bằng `VerifyKeys`, `LegacyGET` mặc định tắt, TTL mặc định 1h tối đa 24h); khóa PKCS#8 không mật khẩu (PEM mã hóa kiểu Medplum chưa hỗ trợ). Vector chéo sinh bằng Node: `testdata/gen_vectors.js`; Go ký ra đúng từng byte.
- `architecture_test.go`: `pkg/binarystore: {}`, `storetest` chỉ cho test.
- Còn: S3a (`/storage`), S3b (Binary thô, `Wrap`), S3c, S4 (cấu hình), S7 (S3).

## Tiến độ S3a, S3b (2026-11-22)

`pkg/fhirbinary` (luật kiến trúc: `pkg/fhir`, `pkg/fhirrouter`, `pkg/binarystore`) và nối vào `apps/fhir-server` (bật bằng `FHIR_BINARY_STORAGE`, mặc định tắt, hành vi cũ giữ nguyên):
- `Wrap`: decorator `fhirrouter.Repository` (Transactional, KeyLocker, Reindexer, Expunger được chuyển tiếp). Q-S9: ghi CSDL trước rồi kho; lỗi kho thì xóa resource vừa tạo (ngoài transaction) hoặc xóa các khóa đã ghi khi transaction hủy. Expunge xóa nội dung mọi phiên bản sau commit.
- `StorageHandler`: `GET|PUT /storage/{id}/{ver}` đúng thứ tự B2.4; áp Q-S5 (đúng phiên bản trong URL, 409 khi PUT URL cũ), project phải khớp tuyệt đối, so `Content-Type` sau khi bỏ tham số.
- `RawBinary`: POST/PUT/GET Binary thô theo B2.5 (gzip/deflate, trần sau giải nén, kiểm tên/loại trước khi tạo resource, `X-Security-Context`, `url` ký trong phản hồi); thân JSON Binary đi qua router.
- Quyết định tự áp dụng (mặc định của kế hoạch, chưa duyệt riêng): Q-S4 nhận cả `application/fhir+json` và `application/json`; Q-S7 trần 256 MiB; Q-S8 chỉ ảnh png/jpeg/gif/webp inline, còn lại `attachment`; GET thô là mặc định khi `Accept` không phải JSON (lệch hành vi cũ của `fhirrouter`, chỉ khi bật kho); job bulk export giữ repo không bọc `Wrap` (file kết quả > 1 MiB vẫn nằm trong CSDL).
- Còn: S3c, S6 (`rewriteAttachments`), S7 (S3), quét nội dung mồ côi, kiểm nhật ký truy cập không ghi `Signature` (S-SEC8, Q-S11), `/api/storage` và `/projects/{id}/storage`.

## Tiến độ S3c, S6 (2026-11-22)

- `fhirbinary.PresignedURL` (`Binary/:id/$presigned-url[?upload=true]`, đăng ký bằng `Router.Add` ở `fhir-server`): đọc theo quyền đọc của người gọi, tải lên cần `fhirauth.CanUpdate` (hàm mới: vai trò ≥ write, chính sách không chỉ-đọc, SMART có `u`). Vì thế Guard token tĩnh nay gắn `Principal` mang vai trò (không project) vào context.
- `securityContext` (Q ngầm của B2.6, test `binary-presigned-url.test.ts:150`): Go chưa có kiểm quyền theo `securityContext` ở repository, nên `fhirbinary` tự kiểm ở các đường nội dung: đọc thô, `$presigned-url`, viết lại URL. Binary đọc qua JSON (`GET Binary/{id}` kèm `Accept` JSON) vẫn chỉ cần quyền đọc Binary (không lộ nội dung).
- `fhirbinary.Rewriter` (S6, Q-S10): `ToReference` (ghi, trong `Wrap` nên phủ cả batch/transaction) và `ToPresigned` (đọc). Điểm móc mới trong `pkg/fhirrouter`: `Options.ResponseHook` (chạy ở `Router.Handle`, cho mọi phản hồi thành công; không sửa đối tượng gốc). Bỏ qua `Binary` và `Bundle.entry.request.url`, chỉ khóa `url`/`path`, bỏ `?query`, cache theo chuỗi trong một lần.
- Lệch chủ ý so với Medplum: Medplum viết lại cả ở `subscription`, `auth/me`, `clientinfo` (chưa làm: chỉ có ở phản hồi FHIR); `REFERENCE` đổi về `Binary/<id>` (bỏ version).
- Còn: S7 (S3), quét mồ côi, đo chi phí mã hóa thêm mỗi phản hồi, S-SEC8.

## Tiến độ E5 (2026-11-22)

Quyết định Q-E2: auth **không** đọc kho hay CSDL của fhir-server (hai service, hai schema, kho là ổ đĩa của fhir-server); thay vào đó `auth` gọi `GET <AUTH_EMAIL_FHIR_URL>/Binary/<id>` bằng **chính Bearer token của người gọi** (`Accept: application/octet-stream`). Nhờ vậy quyền đọc, cách ly project và `securityContext` (đã làm ở `fhirbinary`) do fhir-server quyết định, đúng tinh thần Medplum đọc bằng repo của người gọi; không thêm hợp đồng dữ liệu nào giữa hai service ngoài REST FHIR hiện có.
- Cổng `application.BinarySource` (+ `ErrBinaryNotFound/Forbidden/TooLarge`), adapter `infrastructure/binaryfetch`: chỉ gửi token tới đúng địa chỉ cấu hình, không theo redirect, không dùng proxy môi trường, giới hạn byte và timeout, lỗi không chứa token/URL/thân phản hồi. Chỉ chấp nhận `Binary/<id>` đúng dạng (không version, không query); id kiểm lại trước khi gọi.
- Lỗi đều 400 và không gửi thư (kể cả khi các tệp khác tốt): không tồn tại/khác project (`Attachment not found`), thiếu quyền (`Not authorized to read attachment`), quá lớn; fhir-server lỗi là 502 chung.
- Cấu hình `AUTH_EMAIL_FHIR_URL`, `AUTH_EMAIL_FHIR_TIMEOUT` (production bắt buộc https). Tắt mặc định: giữ 400 `Binary attachments are not supported`.
- Điều kiện vận hành: token của người gọi phải được fhir-server chấp nhận (cùng issuer/audience/JWKS) và có quyền đọc Binary. Token chỉ dành cho auth (không có scope FHIR) sẽ nhận `Not authorized to read attachment`.
- Test: use case (fake), adapter (fhir-server giả: redirect, proxy, kích thước, trạng thái), HTTP của auth (fhir-server giả + SMTP giả, kiểm Bearer chuyển tiếp), e2e hai tiến trình thật (`TestEmailBinaryAttachmentThroughRealProcesses`: Binary của project khác là 404).
- Còn: hạn mức gửi theo project, hàng đợi thử lại, Q-E7 (mã hóa `Project.secret`), tổng kích thước thư vẫn do `pkg/mailer` chặn (10 MiB) sau khi đã tải các tệp.

### Tiến độ S7 (2026-11-23)
- Xong: `pkg/binarystore/s3store` (stdlib, tự ký SigV4; kiểm bằng vector chính thức của AWS), bật bằng `FHIR_BINARY_STORAGE=s3:<bucket>[/<prefix>]` + `FHIR_S3_*`. Put đệm (bộ nhớ ≤4 MiB, tràn ra tệp tạm) để có SHA-256 và Content-Length, một PUT duy nhất (nguyên tử, tối đa 5 GiB); Copy dùng CopyObject (kiểm cả thân `<Error>` của 200); Delete idempotent; 404 chỉ là `ErrNotFound` khi `NoSuchKey` (NoSuchBucket là lỗi cấu hình); không theo redirect; lỗi không lộ khóa. `storetest` xanh trên S3 giả có xác thực chữ ký.
- Chưa: khóa tạm IMDS/STS/IRSA, multipart (>5 GiB), `URLPresigner` phía S3, kiểm thử với S3/MinIO thật (chỉ có S3 giả), quét mồ côi.
