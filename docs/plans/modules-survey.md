# Khảo sát và phân loại: email, fhir, fhircast, keyvalue, mcp, oauth, scim, storage, webhook, ws

Trạng thái: **khảo sát, chưa phải kế hoạch chi tiết.** Nguồn: Medplum v5.2.18 (`~/Downloads/medplum/packages/server/src`). Số dòng và số file do tôi đếm (không tính file test); các mô tả hành vi do tôi đọc source nhưng **chưa có ai khác kiểm**, và ước lượng cỡ chỉ mang tính thô. Cùng đợt khảo sát có sửa một điểm của kế hoạch CDS/DICOM (xem mục 4). Hai module `cds`, `dicom` đã có kế hoạch riêng ở [cds-dicom/](cds-dicom/README.md); `admin` là P7 của `auth` (hoãn, CLI thay thế phần tối thiểu).

## 1. Bảng tổng

| Module | Quy mô (source, không test) | Hiện trạng ở Go | Phụ thuộc chính | Cỡ thô | Nhóm |
|---|---|---|---|---|---|
| `keyvalue` | 2 file, 128 dòng | chưa có | đăng nhập (Bearer), Redis | S | B. nhỏ, độc lập |
| `email` | 3 file, 548 dòng | chưa có | SMTP (AWS SES: bỏ), cấu hình theo project | M | A. nền dùng chung |
| `storage` | 7 file, 577 dòng | chưa có (`fhir.Binary` chỉ giữ base64 trong CSDL) | khóa ký URL, hệ thống tệp (S3: sau) | M | A. nền dùng chung |
| `mcp` | 2 file, 220 dòng | chưa có | đăng nhập, FHIR, Redis pub/sub (SSE) | M | B. nhỏ, độc lập |
| `scim` | 3 file, 694 dòng | chưa có | dữ liệu của `auth` (User, ProjectMembership) | M | C. thuộc `auth` |
| `oauth` | 12 file, 4267 dòng | = `auth` P3 (đã có kế hoạch) | | L | C. thuộc `auth` |
| `webhook` | 1 file, 74 dòng | chưa có | **chạy Bot** | S (nhưng bị chặn) | D. cần runtime Bot |
| `ws` | 6 file, 1965 dòng | **một phần**: `subscriptions-r4` đã có (`pkg/fhirsub/ws.go`) | Agent, AI, FHIRcast cho các kênh còn lại | S đến L tùy kênh | E. một phần |
| `fhircast` | 2 file, 930 dòng | chưa có | WebSocket, Redis pub/sub | M đến L | E. độc lập, ưu tiên thấp |
| `fhir` | **142 file, 37352 dòng** | **phần lõi đã có** (`fhir-server`) | nhiều | cần kiểm kê trước | F. cần kiểm kê |

## 2. Từng module

### keyvalue (S)
Kho khóa-giá trị nhỏ cho mỗi **membership** (Bot dùng làm cache). `GET /keyvalue/v1/{key}` trả `text/plain` hoặc 404; `PUT` nhận chuỗi, trả 204; `DELETE` trả 204. Giới hạn: khóa tối đa 255 ký tự, giá trị tối đa 8192, tối đa **10 mục** mỗi membership (vượt thì 400 `Max items exceeded`); giá trị không phải chuỗi thì `Invalid value type`. Lưu bằng Redis hash `medplum:keyvalue:{membershipId}` (`store.ts`). Cần quyết định: dùng Redis (như Medplum) hay bảng SQL; dữ liệu cô lập theo membership, không theo project. Phụ thuộc Bearer của `auth` P3.

### email (M)
`POST /email/v1/send` (JSON): `to`, `subject` bắt buộc; `text`, `html`, `attachments` (đối tượng, thuộc tính chuỗi), `icalEvent`. Điều kiện: project bật tính năng `email`, người gọi là **admin** của project, và server cấu hình được SMTP; trả `allOk`. Hỗ trợ SMTP theo project (`getProjectSmtpConfig`, timeout 20 giây kết nối, 120 giây socket), tên hiển thị theo `appName` của project, hoặc AWS SES. Tệp đính kèm theo URL đi qua `safeFetch` chặn địa chỉ nội bộ kể cả khi redirect (`email.ts:106-108`).
**Điểm chung với `auth`:** kế hoạch `auth` D-4 đã định một cổng `Mailer` (thư đặt lại mật khẩu, xác minh email, mã MFA). Nên làm **một gói `pkg/mailer`** dùng cho cả hai thay vì hai bản; chỉ SMTP (không SES, vì AWS SDK đã được gỡ khỏi `go.mod`). Thư viện gửi thư: `net/smtp` của stdlib hoặc một thư viện ngoài (chưa kiểm).

### storage (M)
Hai phần: (1) giao diện `BinaryStorage` (`writeBinary`, `readBinary`, copy, delete, `getPresignedUrl`) với `FileSystemStorage` và `S3Storage` chọn qua cấu hình `file:` hoặc `s3:` (`loader.ts`); (2) route `GET/PUT /storage/{id}[/{versionId}]` nhận URL đã ký: chữ ký RSA-SHA256 trên `METHOD + ' ' + URL`, tham số `Signature` và `Expires`; thiếu chữ ký thì 401, hết hạn thì 410, `Content-Type` lệch thì 400 (`routes.ts`). Phần Binary trong luồng FHIR (`fhir/binary.ts`, `$binary-presigned-url`, `$binary-scan`) thuộc module `fhir`.
**Đây là phần nền của DICOM** (`pkg/binarystore`, đã ghi ở kế hoạch DICOM). Quyết định: có chuyển Binary hiện đang nhúng base64 trong CSDL sang kho ngoài không, tương thích URL ký với client Medplum hay không, khóa ký lấy từ đâu (khóa riêng, không dùng chung với khóa JWT của `auth`).

### mcp (M)
Máy chủ MCP có hai transport: `ALL /mcp/stream` (streamable HTTP, không phiên) và `GET/POST /mcp/sse` (SSE, **cần Redis pub/sub** để chuyển thông điệp giữa các instance, có kiểm membership của từng thông điệp). Công cụ: `search` và `fetch` chỉ là **bản giả** trả tài liệu mẫu (ChatGPT yêu cầu mới kết nối được), và `fhir-request` gọi API FHIR bằng cách **tải lại URL** với token của người gọi, có kiểm chỉ cho phép cùng origin và dưới đường dẫn FHIR (`server.ts`). Chính source ghi chú nên thay bằng gọi router nội bộ; bản Go làm được ngay (gọi thẳng `fhirrouter`) và tránh SSRF/chuyển token. Từng có `github.com/modelcontextprotocol/go-sdk v1.8.0` trong `go.mod` (bị `go mod tidy` gỡ khi các app dùng nó được tách ra); số công cụ ngoài ba cái trên: chưa đọc hết `server.ts`.

### scim (M)
SCIM Users cho Okta: `GET/POST /scim/v2/Users`, `GET/PUT/PATCH/DELETE /scim/v2/Users/{id}`; xác thực Bearer và **bắt buộc admin project** (`verifyProjectAdmin`). Chuyển đổi hai chiều giữa SCIM user và `User` + `ProjectMembership` + hồ sơ (`Patient`/`Practitioner`/`RelatedPerson`), và đổi SCIM patch sang JSON Patch (`utils.ts`). Dữ liệu thuộc `auth`, nên làm cùng gói với P7 (quản trị project) hoặc ngay sau đó, trong app `auth`. Cần P3 (Bearer) và quyết định D-10 (nguồn hồ sơ, vì SCIM tạo hồ sơ lâm sàng).

### oauth (L)
Là **`auth` P3**; đã có kế hoạch chi tiết ở `docs/plans/auth/p3-oauth2-tokens.md`. Không lặp lại ở đây.

### webhook (S, bị chặn)
`POST /webhook/{membershipId}` **không cần đăng nhập**, chạy một Bot với quyền của membership đó: membership phải thuộc một Bot, có access policy, Bot phải có `publicWebhook` và project phải đọc được Bot (`routes.ts`). README nói yêu cầu một trong năm header chữ ký (`X-Signature`, `X-HMAC-Signature`, `X-Cal-Signature-256`, Twilio...) và nói hỗ trợ cả GET, nhưng **handler không kiểm header chữ ký và router chỉ có POST**; có thể Bot tự kiểm, **chưa xác nhận ở nơi khác**. Toàn bộ giá trị nằm ở việc chạy Bot, nên phụ thuộc quyết định H3 (bộ thực thi) của kế hoạch CDS. Endpoint công khai chạy mã nên cần rà bảo mật riêng.

### ws (S đến L)
Một máy chủ WebSocket dispatch theo đường dẫn `/ws/{tên}`: `echo` (85 dòng), `agent` (350), `ai-realtime` (221), `fhircast` (241), `subscriptions-r4` (804); đóng êm với thời hạn 30 giây. **`subscriptions-r4` đã có ở Go** (`WebSocketPath = "/ws/subscriptions-r4"`, `pkg/fhirsub/ws.go`, có e2e). Còn lại: `echo` làm tức thì; `agent` cần module Agent (Medplum Agent, cầu nối HL7/DICOM; chưa có gì ở Go), `ai-realtime` cần dịch vụ AI, `fhircast` đi cùng module FHIRcast.

### fhircast (M đến L)
Hub FHIRcast hai phiên bản: `/fhircast/STU2/` và `/fhircast/STU3/` (cộng bí danh `/hub/`), gồm route công khai, route bảo vệ chung và route riêng từng phiên bản; đăng ký lưu ở Redis kèm hạn lease 3600 giây, sự kiện đẩy qua WebSocket `/ws/fhircast`. Độc lập với phần còn lại, ưu tiên thấp.

### fhir (cần kiểm kê trước)
37352 dòng trong 142 file: không thể "lập kế hoạch port" như một khối. Phần lõi (CRUD, tìm kiếm, lịch sử, patch, batch/transaction, GraphQL, `$export`, terminology cơ bản, job nền, Subscription rest-hook và websocket, nhiều project qua JWT) **đã có trong `fhir-server`**. Phần chưa có gồm khoảng 94 route operation ở `docs/api-contract/fhir-operations.md`, nhưng chúng thuộc nhiều nhóm rất khác nhau về giá trị và độ khó:

| Nhóm operation | Ví dụ | Ghi chú |
|---|---|---|
| Bot | `Bot/$execute`, `$deploy`, `botinit` | cần runtime Bot (H3) |
| Agent | `Agent/$status`, `$push`, `$upgrade`... (11 route) + `ws/agent` | cần module Agent |
| Lịch hẹn | `Appointment/$find`, `$book`, `$hold`, `$cancel`, `$confirm`, `$reschedule` | nghiệp vụ độc lập, port được |
| Thanh toán, bảo hiểm | `Claim/$submit`, `$export`, `CoverageEligibilityRequest/$submit`, `ChargeItemDefinition/$apply` | gọi dịch vụ ngoài |
| Lâm sàng | `PlanDefinition/$apply`, `Measure/$evaluate-measure`, `QuestionnaireResponse/$extract`, `Patient/$match`, `$summary`, CCDA, SMART Health Cards/Links | mỗi cái là một dự án nhỏ |
| Terminology | `CodeSystem/$import`, `ConceptMap/$import`, và các biến thể còn thiếu | một phần đã có |
| Hạ tầng riêng của Medplum | `$db-*`, `$explain`, `$rebuild-base-definitions`, `$restore`, `$clear-all-ws-subs`, project clone/archive/rotate-secret | gắn chặt với PostgreSQL và vận hành của Medplum, đa số **không áp dụng** cho kho Go |
| Khác | `$csv`, `$ai`, `$graph`, `$set-accounts`, `$refresh-reference-display`, Binary (`$binary-presigned-url`, `$binary-scan`) | |

Ngoài operation còn các **tính năng hạ tầng của lớp `fhir/`** mà tôi **chưa kiểm** Go đã có hay chưa: SearchParameter tùy biến (`searchparameter.ts`), kiểm hồ sơ StructureDefinition (`structure.ts`), `checkReferencesOnWrite` (`references.ts`), hạn mức và giới hạn (`fhirquota.ts`, `resource-cap.ts`), ghi lại tệp đính kèm thành URL ký (`rewrite.ts`), chia shard, AuditEvent khi ghi (`repo-auditevent`), quyền `onBehalfOf`. Việc đúng là một **bản kiểm kê tương đương (parity audit)**: liệt kê từng tính năng, đánh dấu đã có, chưa có, không áp dụng, rồi mới lập kế hoạch cho nhóm chưa có.

## 3. Phụ thuộc giữa các module

```
auth P3 (oauth) ──► keyvalue, mcp, scim, email, cds, dicom, webhook   (cần Bearer)
pkg/mailer ◄── email ──► auth P4/P5 (thư đặt lại mật khẩu, xác minh, MFA)
pkg/binarystore ◄── storage ──► dicom (và Binary trong fhir)
pkg/safehttp (trích từ pkg/fhirsub/ssrf.go) ──► email (đính kèm theo URL), cds executor, auth P6
Runtime Bot (H3) ──► cds, webhook, fhir Bot operations, cron/subscription-bot
Module Agent ──► ws/agent, 11 route Agent
```

## 4. Đính chính kế hoạch CDS/DICOM

Kế hoạch trước đề xuất dựng `pkg/safehttp` mới. Thực tế `pkg/fhirsub/ssrf.go` đã có bộ chống SSRF (chặn dải nội bộ ở bước kết nối trên IP đã phân giải nên chống cả DNS rebinding, không theo redirect, không dùng proxy từ môi trường), nhưng các hàm chưa xuất khẩu. Việc đúng là **trích** nó thành `pkg/safehttp` (refactor, giữ test của `fhirsub`) và bổ sung chế độ chỉ-HTTPS, giới hạn body. Đã sửa trong `cds-dicom/README.md` và `cds.md`.

## 5. Thứ tự đề xuất

| Thứ | Việc | Lý do |
|---|---|---|
| 1 | Trích `pkg/safehttp`; làm `pkg/mailer`; làm `pkg/binarystore` | ba nền dùng chung, mỗi cái mở khóa nhiều module; `mailer` cũng là việc P4/P5 của `auth` phải làm |
| 2 | `keyvalue` | nhỏ, trọn vẹn, ít rủi ro |
| 3 | `email` (route `/email/v1/send`) | dựa trên `mailer` vừa làm |
| 4 | `mcp` | giá trị cao với hệ sinh thái AI, nhỏ, gọi router nội bộ thay vì tải lại URL |
| 5 | `scim` | cùng đợt với quản trị project của `auth` (P7) |
| 6 | Bản kiểm kê tương đương cho `fhir` | trước khi bất kỳ ai ước lượng việc "port `fhir`" |
| 7 | Quyết định H3 (runtime Bot) rồi `cds`, `webhook` | chặn bởi một quyết định lớn |
| 8 | `fhircast`, `ws` (kênh agent, ai-realtime) | ưu tiên thấp, phụ thuộc module khác |

Hầu hết cần `auth` P3 (token) để chạy thật; trước đó dùng JWT dev (`FHIR_JWT_HMAC_SECRET`) như `fhir-server`.

## 6. Cần bạn chọn

1. Module nào cần kế hoạch chi tiết đầy đủ (như `auth`, `cds`, `dicom`) trước? Đề xuất: `email` + `storage` (cùng nền dùng chung), `keyvalue`, `mcp`.
2. Có làm bản kiểm kê tương đương cho `fhir` không (chỉ liệt kê, chưa port).
3. `webhook` và `cds` cùng chờ quyết định H3 về runtime Bot; bạn muốn quyết định bây giờ hay để sau.
4. `scim`, `oauth`: giữ trong `auth` như kế hoạch.

## 7. Đối chiếu với `app.ts` (đọc đủ 389 dòng) và đối chiếu với Go

Các kiểm tra "Go chưa có" dưới đây là kết quả `grep` trong `apps/` và `pkg/` (không có CORS, header bảo mật, `respond-async` cho batch, rate limit, nén, tiền tố `/projects/`); phần còn lại do tôi đọc source. `app.ts` tự nó là một **điểm lắp ráp** nên phần lớn thứ "thiếu" là việc xuyên suốt (cross-cutting), không phải module.

### 7.1 Route và router chưa có trong khảo sát hoặc kế hoạch

| Hạng mục | Nguồn | Ghi chú |
|---|---|---|
| `POST /fhir/R4` bất đồng bộ | `app.ts` (`asyncBatchHandler`), `async-batch.ts` (51 dòng), worker `batch` | `Prefer: respond-async` thì đưa batch vào hàng đợi; Go chỉ có batch đồng bộ. Thêm vào bản kiểm kê `fhir` |
| `/shl/` (SMART Health Links, công khai) | `app.ts` (`smartHealthLinkRouter`), `fhir/operations/smarthealthlinks` | Có nhắc chung "SMART Health Cards/Links" ở nhóm lâm sàng nhưng chưa tính router công khai này |
| Router Binary tải/tải lên thô | `app.ts` (`binaryRouter` gắn **trước** các body parser để stream) | Thuộc `storage` + `fhir`; Binary Go hiện nhúng base64 |
| `/openapi.json`, `/healthcheck`, `/`, `/robots.txt` | `app.ts`, `openapi.ts` (500 dòng), `healthcheck.ts` (150) | Go có `/healthz`, `/readyz`, swagger theo từng app; ưu tiên thấp, chỉ cần nếu client Medplum gọi |
| `/mcp` chỉ gắn khi `config.mcpEnabled` | `app.ts` | `mcp` cần cờ cấu hình này |
| **Tiền tố URL** | `app.ts`: `apiRouter` được gắn ở `/`, `/api/`, `/projects/{id}/`, `/api/projects/{id}/` | Go chỉ có `/fhir/R4`, `/auth`, `/ws/...`. **Chưa có quyết định chung**: giữ tiền tố `projects/{id}` cho tương thích client Medplum (và buộc khớp project của token), hay chỉ dùng claim `fhir_project` |

### 7.2 Middleware xuyên suốt mà Medplum có và Go chưa có

| Hạng mục | Medplum làm gì | Hệ quả cho bản Go |
|---|---|---|
| **CORS** (`cors.ts`, 72 dòng) | Chỉ cho origin của `appBaseUrl` hoặc `allowedOrigins` (`*` hoặc danh sách), chỉ với một danh sách tiền tố đường dẫn (`/auth/`, `/oauth2/`, `/fhir/`, `/.well-known/`, `/storage/`, `/dicomweb/`, `/cds-services`, `/keyvalue/`, `/email/`, `/fhircast/`, `/hub`...); `credentials: true`, `maxAge: 600`; lộ header `Content-Location`, `ETag`, `Last-Modified`, `Location`, `RateLimit`, `X-Request-Id`, `X-Trace-Id` | **Ảnh hưởng ngay `auth`**: không có CORS thì trình duyệt (ứng dụng web Medplum) không gọi được `/auth/login` từ origin khác, kể cả preflight. Cũng đúng với `fhir-server` |
| **Header bảo mật** (`standardHeaders`) | `Cache-Control: no-store, no-cache, must-revalidate`, `Pragma: no-cache`, HSTS hai năm và `preload` khi base URL là https, CSP `default-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'`, `Permissions-Policy`, `Referrer-Policy: no-referrer`, `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `X-XSS-Protection` | **Ảnh hưởng `auth`**: phản hồi `/auth/login` (có `code`), và sau này token, phải `no-store`; hiện chưa có |
| **Rate limit** (`ratelimit.ts`, 197 dòng) và hạn mức FHIR (`fhirquota`) | Theo IP và theo nhóm route (`login`, `newuser`, `newproject` 5 lần/phút; `mfa/verify`, `login-enroll` 10; còn lại 160), theo project; header `RateLimit` | Kế hoạch `auth` đã ghi "phải có trước khi mở ra Internet" nhưng chưa có việc cụ thể; thành một hạng mục nền |
| `trust proxy = 1` | Tin **một** proxy | Trả lời câu "chưa xác nhận" ở kế hoạch P2: Medplum tin 1 hop mặc định. `auth` mặc định **không tin** (an toàn hơn), cấu hình bằng `AUTH_TRUSTED_PROXIES` |
| Body parser | JSON giới hạn `maxJsonSize`, nhận cả `application/*+json`; `urlencoded`; `text` (cả HL7 v2) kèm parser HL7; giữ raw body cho Bot có `rawBody` | HL7 và raw body chỉ cần khi có Bot |
| `errorHandler` | Lỗi parse thành 400 `Content could not be parsed`; quá lớn thành 413 `Request body too large`; media type lạ thành 415; `URIError` thành 400; lỗi lạ thành 500 `{"msg":"Internal Server Error"}` | `auth` đã gần giống (khóa `error` thay vì `msg`); cần kiểm lại 413/415 |
| Nén `compression()` | gzip | ưu tiên thấp |
| Nhật ký request (`config.logRequests`) | Ghi thời lượng, IP, đường dẫn, trạng thái, `User-Agent` | Go đã có qua `echo4obs` |

### 7.3 Khởi tạo và hàng đợi (`initAppServices`)

| Medplum | Go |
|---|---|
| `loadStructureDefinitions`, Redis, DB, `initKeys`, `initBinaryStorage` | có (kiểu và định nghĩa nhúng trong `pkg/fhir`), có, có, `auth` P3, `storage` |
| `seedDatabase` (`seed.ts`, 143 dòng) | CLI `auth seed project` thay phần cần thiết (không tự seed khi khởi động) |
| 11 worker (`workers/index.ts`): `dispatch`, `subscription`, `download`, `cron`, `reindex`, `batch`, `bulk-export`, `post-deploy-migration`, `set-accounts`, `lambda-cleaner`, `dicom` | Đã có: `subscription` (`pkg/fhirsub`), `reindex`, `bulk-export`. Chưa có: `batch` (async batch), `download`, `dispatch`, `set-accounts`, `cron` (Bot), `dicom` (đã lên kế hoạch). Không áp dụng: `post-deploy-migration`, `lambda-cleaner` (hạ tầng Medplum) |
| `initHeartbeat` (36 dòng), `initOtelHeartbeat`, `initServerRegistryHeartbeatListener` (147 dòng) | Heartbeat phục vụ ping của `ws`/`mcp`; **chưa đọc** `server-registry.ts` nên chưa biết nó cần cho tính năng nào |

### 7.4 Việc đề xuất thêm vào lộ trình

| Việc | Cỡ thô | Ghi chú |
|---|---|---|
| Gói middleware dùng chung (header bảo mật, CORS cấu hình được theo bảng tiền tố, giới hạn body, ánh xạ lỗi parse 400/413/415) cho `fhir-server`, `auth`, `dicomweb` | S đến M | **Đã làm `pkg/httpsec` (header bảo mật + CORS) và gắn vào `auth`** (origin mặc định `http://localhost:3000` ở local). Còn lại: gắn vào `fhir-server`, `dicomweb` khi có; ánh xạ lỗi 415 chưa làm |
| Gói rate limit (Redis, có fallback bộ nhớ) | M | Điều kiện trước khi mở `auth` ra Internet |
| Hai hạng mục vào bản kiểm kê `fhir`: async batch, `/shl/` | | |
| Quyết định chung về tiền tố URL | | Ảnh hưởng `cds`, `dicom`, `auth`, `fhir-server` |
