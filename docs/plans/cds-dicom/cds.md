# Kế hoạch port CDS Hooks

> **Ngoài phạm vi (2026-11-24):** CDS phụ thuộc chạy Bot mà M1 đã chốt không làm (xem [coverage-matrix.md](../coverage-matrix.md) mục 3); H1 và H3 vô hiệu. Giữ tài liệu này để mở lại cùng M1.

Nguồn Medplum (v5.2.18, `~/Downloads/medplum/packages/server/src`): `cds/routes.ts` (82 dòng), `cds/README.md`, `cds/routes.test.ts` (5 test); phần thực thi nằm ở `bots/` (`execute.ts` 87 dòng, `utils.ts` 552, `vmcontext.ts` 175). Tổng quan và quyết định chung: [README.md](README.md).

## 1. Điều quan trọng nhất

CDS Hooks của Medplum **chỉ là lớp vỏ mỏng** trên cơ chế **chạy Bot**: không có logic CDS nào ngoài việc liệt kê Bot và gọi `executeBot`. Phần khó không nằm ở CDS mà ở chỗ **Go chưa có cách chạy Bot** (Medplum chạy mã JavaScript của người dùng qua `awslambda`, `vmcontext` hoặc `fission`: `bots/execute.ts:55-69`). Vì vậy kế hoạch này tập trung vào việc chọn **bộ thực thi (executor)** cho CDS, không phải vào hai route.

Hiện trạng trong repo Go (đã kiểm):
- Kiểu `Bot` và `Bot.cdsService` (`hook`, `title`, `description`, `usageRequirements`, `prefetch[{key,query}]`) đã có (`pkg/fhir/resource_bot.go:93-119`).
- Search parameter `cds-hook` (token trên `Bot.cdsService.hook`) đã có (`pkg/fhir/searchparams_gen.go:167`).
- **Không có** `/cds-services`, không có `Bot/$execute`, không có chạy Bot; `docs/api-contract/others.md` liệt kê `/cds-services/` là route Medplum chưa cài.

## 2. Hành vi Medplum cần giữ (đã đọc)

| Route | Hành vi | Nguồn |
|---|---|---|
| `GET /cds-services` | Bắt buộc đăng nhập (Bearer). Tìm `Bot` bằng repo **của người gọi** (chịu access policy) với `cds-hook:missing=false`, `count=100` (spec không có phân trang). Trả `{services:[{id, hook, title, description, usageRequirements, prefetch}]}`; `prefetch` là mảng `{key,query}` được làm phẳng thành object `{key: query}`, bỏ qua nếu không có | `routes.ts:16-45` |
| `POST /cds-services/{id}` | Đọc Bot bằng repo người gọi (không có/không được xem thì lỗi 404/403 do middleware chung). Bot **không có `cdsService`** thì `404` không body (`sendStatus(404)`). Đọc lại Bot bằng system repo để lấy đủ chi tiết. Gọi `executeBot` với `entryPoint:'cds'`, `runAs` = membership người gọi, `input` = body, `contentType` JSON, header của request **bỏ `Authorization`**, `defaultHeaders` chỉ có `Cookie` khi `bot.runAsUser`, kèm `traceId` | `routes.ts:48-82`, `bots/utils.ts:128-149` |
| Phản hồi | Qua `sendBotResponse`: có `returnValue` thì trả nguyên; không có thì `OperationOutcome` (`allOk` nếu thành công, `badRequest(log)` nếu lỗi); `returnValue` là `OperationOutcome` lỗi thì dùng mã của nó; số được đổi thành chuỗi; Binary không có data thì 400; resource thì trả như phản hồi FHIR | `bots/utils.ts:158-170,519-551` |
| `executeBot` | Project phải bật tính năng `bots`, không thì kết quả "Bots not enabled" (`isBotEnabledForProject`, `utils.ts:224`); runtime ngoài ba loại trên là "Unsupported bot runtime"; nạp access token và secret của bot; ghi `AuditEvent` và metric thời gian | `bots/execute.ts:30-87` |

Khác với đặc tả CDS Hooks (ghi nhận để không ngạc nhiên): đặc tả để discovery công khai và EHR ký JWT riêng gửi tới service; Medplum bắt **cả hai** endpoint dùng Bearer token của chính Medplum (`README.md` mục "Authentication"). Kế hoạch giữ hành vi của Medplum. Medplum **không kiểm** cấu trúc card (`summary` dưới 140 ký tự, `indicator`, `source.label`): chỉ nói trong README.

## 3. Quyết định then chốt: bộ thực thi

| Phương án | Mô tả | Ưu | Nhược |
|---|---|---|---|
| **A. Executor HTTP (khuyến nghị làm trước)** | Bot có runtime mới `http` trỏ tới một endpoint CDS bên ngoài (bất kỳ ngôn ngữ nào); `auth`/`fhir-server` chuyển tiếp body và trả lại JSON | Nhỏ, an toàn, không cần chạy mã lạ trong tiến trình | Không tương thích Bot JavaScript đang có của Medplum; cần chỗ lưu URL (xem Q2) |
| B. Nhúng engine JS (ví dụ goja) | Chạy mã Bot trong tiến trình Go | Có thể chạy mã đơn giản | Bot Medplum dùng `MedplumClient`, `fetch`, API Node: không tương thích, rủi ro an ninh cao (chạy mã người dùng trong tiến trình) |
| C. Sidecar Node chạy Bot | Giữ runtime `vmcontext` của Medplum ở một dịch vụ Node riêng, Go gọi sang | Tương thích Bot hiện có | Giữ lại TypeScript, ngược mục tiêu migration (chưa quyết định) |
| D. Port đầy đủ module Bot | `Bot/$execute`, `$deploy`, cron, subscription... | Đủ tính năng | Rất lớn, **ngoài phạm vi** kế hoạch này; cần kế hoạch riêng |

Khuyến nghị: làm CDS trên cổng `BotExecutor` với adapter **A** trước; các phương án C hoặc D, nếu sau này chọn, chỉ là thêm adapter, không đổi route. Quyết định này thuộc người duyệt (Q1).

## 4. Thiết kế đích

Đặt trong `fhir-server` (cùng kho dữ liệu clinical, cùng xác thực `pkg/fhirauth`, cùng `fhirauth.Scope` cô lập project), như một nhóm route `/cds-services`, không tạo app mới (xem README.md Q-H1). Phân tầng:

| Tầng | Nội dung |
|---|---|
| `domain` | `Service` (view của `Bot.cdsService`), làm phẳng `prefetch`, luật chọn bot là CDS service (có `cdsService`), (tùy chọn) kiểm card |
| `application` | `DiscoverServices(ctx)`, `InvokeService(ctx, id, body, caller)`; cổng `BotReader` (qua repo của người gọi), `BotExecutor` (`Execute(ctx, req) (Result, error)`), `AuditSink` |
| `infrastructure` | Executor `http` (dùng `pkg/safehttp` trích từ `pkg/fhirsub`, xem README.md), ghi `AuditEvent` |
| `presentation` | Route `GET /cds-services`, `POST /cds-services/{id}`; ánh xạ `Result` sang phản hồi như `sendBotResponse` |

Lưu ý kiến trúc: `fhir-server` hiện **không có** tầng `application`/`domain` riêng (`architecture_test.go`: nghiệp vụ nằm ở `pkg/fhir*`). CDS thêm nghiệp vụ nhỏ nên nên đặt vào một gói dùng chung `pkg/cds` (thuần, không biết Echo), để `fhir-server` chỉ gắn route; cần thêm luật vào `pkgRules`.

## 5. Các bước

| Bước | Việc | Cỡ | Tiêu chí hoàn thành |
|---|---|---|---|
| C0 | Chốt Q1 đến Q4; trích `pkg/safehttp` từ `pkg/fhirsub/ssrf.go` (dùng chung với `auth` P6, giữ test hiện có của `fhirsub`) | S đến M | Quyết định ghi vào README |
| C1 | `pkg/cds`: kiểu, làm phẳng prefetch, `Discover` qua repo của người gọi (`cds-hook:missing=false`, count 100), test bảng | S | Test khớp ví dụ `routes.test.ts:116-135` |
| C2 | `BotExecutor` + fake; `Invoke`: 404 nếu Bot không có `cdsService`, bỏ `Authorization` khỏi header chuyển tiếp, kiểm project bật `bots`, ánh xạ phản hồi như `sendBotResponse` | S đến M | Các ca `routes.test.ts:137-164,217-264` chạy với fake |
| C3 | Executor `http` trên `pkg/safehttp`: thêm chỉ-HTTPS, timeout, giới hạn kích thước, không rò secret; audit | M | Test SSRF (`localhost`, IP riêng, redirect, giới hạn body) |
| C4 | Route trong `fhir-server` (`/cds-services`, cả tiền tố `/api` nếu giữ), swagger, hợp đồng `docs/api-contract/others.md`, e2e với JWT từ `auth` | S | e2e xanh |

Cỡ tổng: **M** (3 đến 6 ngày) cho phương án A. Phương án C hoặc D cộng thêm cỡ L trở lên, ngoài kế hoạch này.

## 6. Ca test (từ `cds/routes.test.ts`)

| Ca gốc | Dòng | Ghi chú Go |
|---|---|---|
| Discovery returns CDS hooks | 116 | `prefetch` làm phẳng; chỉ Bot có `cdsService` |
| Call CDS hook bot | 137 | phản hồi có `cards`; header `application/json` |
| CDS hook bot run by a caller with SMART scopes is logged | 166 | Medplum ghi log cảnh báo (throttle 10 phút, `callerSmartScopesLogThrottle`); cần chốt có port log này không |
| Cannot call normal bot as CDS service | 217 | 404 |
| Request headers passed to bot without Authorization | 229 | `authorization` bị bỏ, `cookie` và header khác giữ nguyên; ca này dùng `$deploy` của Bot nên **chỉ port phần header** |
Thêm ca riêng: không phải người dùng của project thì không thấy Bot của project khác; project chưa bật `bots`; executor lỗi; SSRF.

## 7. Bảo mật

- Executor `http` gọi ra ngoài theo cấu hình của người dùng: bắt buộc chống SSRF (dialer kiểm IP sau resolve, chỉ HTTPS, kiểm lại mỗi lần redirect, timeout, giới hạn body), URL chỉ do admin project đặt.
- Không chuyển `Authorization` của người gọi sang service ngoài (Medplum cũng bỏ); nếu service cần xác thực thì dùng secret của project (cách lưu: Q3).
- Mặc định từ chối khi project chưa bật `bots`.
- Kiểm quyền đọc Bot theo access policy của người gọi trước khi chạy (Medplum đọc bằng repo người gọi, sau đó mới dùng system repo).

## 8. Không port đợt này

`Bot/$execute`, `$deploy`, cron, subscription kích hoạt Bot, runtime `awslambda`/`vmcontext`/`fission`, `storeBotInput`, streaming bot (`streamingEnabled`); tất cả thuộc module Bot (phương án D).

## 9. Câu hỏi mở

| # | Câu hỏi | Khuyến nghị |
|---|---|---|
| Q1 | Chọn bộ thực thi: A, B, C hay D | A trước |
| Q2 | Với phương án A, URL endpoint lưu ở đâu: extension trên `Bot` (ví dụ `https://trivita.ai/fhir/StructureDefinition/bot-endpoint`), `Bot.identifier`, hay trường mới | Extension; kiểu `Bot` hiện không có trường URL. Giá trị `runtimeVersion` mới `http` vượt ngoài tập giá trị của Medplum (server hiện không kiểm binding code, đã thấy khi thử `gender` lạ) |
| Q3 | Cách xác thực tới service ngoài: không, API key trong secret của project, hay JWT ký bởi `auth` | Secret của project ở bước đầu |
| Q4 | Có kiểm cấu trúc card (summary dưới 140 ký tự, `indicator` hợp lệ, `source.label`) | Có, nhưng chỉ cảnh báo log, không từ chối |
| Q5 | Tiền tố URL: `apiRouter` (chứa `/cds-services`) được Medplum gắn ở 4 tiền tố: `/`, `/api/`, `/projects/{projectId}/`, `/api/projects/{projectId}/` (`app.ts:297-300`, đã kiểm) | Chỉ `/cds-services`; thêm các tiền tố còn lại nếu client Medplum cần (tiền tố `projectId` phải khớp project của token) |
| Q6 | Có đặt `fhirServer` và `fhirAuthorization` (đặc tả) vào request chuyển tiếp | Không ở bước đầu: Medplum không làm; EHR tự gửi trong body |
