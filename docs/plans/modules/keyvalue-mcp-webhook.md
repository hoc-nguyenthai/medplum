# Kế hoạch port `keyvalue`, `mcp`, `webhook` (Medplum v5.2.18 sang Go)

Trạng thái: **bản nháp chờ duyệt**, chưa có code. Nguồn Medplum (chỉ đọc): `/Users/hocnt84/Downloads/medplum/packages/server/src` (gọi tắt `S/`). Đường dẫn Go tính từ gốc repo `trivita-codebase-v1` (module `trivita-fhir`). Quy ước: chỉ ghi điều đã đọc kèm `file:dòng`; chỗ chưa kiểm ghi "chưa xác nhận". Kế hoạch này nối tiếp `docs/plans/modules-survey.md` (mục 2, 7), `docs/plans/coverage-matrix.md`, `docs/plans/cds-dicom/cds.md`, `apps/auth/docs/plans/`.

## 0. Tóm tắt và thứ tự làm

| Module | Quy mô Medplum | Cỡ port | Chặn bởi | Làm được ngay? |
|---|---|---|---|---|
| `keyvalue` | 128 dòng (`routes.ts` 41, `store.ts` 87) | S đến M | cần **membership** trong token (xem Q-K1) | Có, bằng token HMAC của test hiện có; chỉ phần phát claim chờ auth P3 |
| `mcp` | 220 dòng (`routes.ts` 99, `server.ts` 121) | M đến L | như trên; SDK MCP cho Go cần thêm lại vào `go.mod` | Có |
| `webhook` | 74 dòng (`routes.ts`) | S cho khung, L cho phần chạy Bot | **runtime Bot** (quyết định H3/M1 trong `cds.md` mục 3) | Khung (xác thực, tra membership, kiểm quyền, `BotExecutor`, nhập/xuất) làm được; **không chạy được Bot thật** |

Phát hiện quan trọng nhất, xem chi tiết ở từng mục:

1. **`pkg/fhirauth.Principal` không có membership** (`pkg/fhirauth/principal.go:37-49`; `Verify` chỉ đọc `sub`, `fhir_project`, scope, policy: `pkg/fhirauth/jwt.go:121-155`). Medplum cô lập `keyvalue` và kiểm thông điệp SSE theo **membership id** (`keyvalue/store.ts:86`, `mcp/routes.ts:37,43`). Auth P3 hiện chưa có claim chứa membership (bảng claim `p3-oauth2-tokens.md:300-310` có `login_id`, `profile`, không có membership id). Cần thêm claim và trường `Principal.Membership` (Q-K1).
2. **Webhook của Medplum KHÔNG kiểm header chữ ký** (bằng chứng ở mục 2.W.3): README mô tả sai ba điều (header chữ ký bắt buộc, hỗ trợ GET, mã lỗi 400), code làm khác (không kiểm chữ ký, chỉ POST, mã 403/404).
3. **`fhir-request` của MCP** trong Go không cần tải lại URL: gọi **in-process** vào đúng chuỗi xử lý của `fhir-server` (guard Bearer + `RequiredRole` + `fhirauth.Scope`). Gọi thẳng `Router.Handle` là **sai** vì bỏ qua `RequiredRole` (vai trò chỉ được kiểm ở guard HTTP: `apps/fhir-server/internal/presentation/httpapi/auth.go:96-118,162`).
4. `pkg/messaging.Broadcaster` **không dùng được** cho kênh SSE theo từng phiên (Redis Streams, topic cố định trước `Start`, không hủy đăng ký: `pkg/messaging/messaging.go:48-54`, `pkg/messaging/redis.go:103-107,118`); cần cổng `SessionBus` riêng dùng Pub/Sub của go-redis.

---

## Phần A. `keyvalue`

### A.1 Mục tiêu, phạm vi, hiện trạng Go

Mục tiêu: kho khóa-giá trị nhỏ, riêng cho từng membership, dùng làm cache cho Bot và dịch vụ khác (`keyvalue/README.md:3-5`). Phạm vi: ba route `GET|PUT|DELETE /keyvalue/v1/{key}`. Không gồm: Bot (không có Bot ở Go), API quản trị.

Hiện trạng Go (grep thật, chạy từ gốc repo):

| Lệnh | Kết quả |
|---|---|
| `grep -rniE 'keyvalue\|KeyValue' apps pkg --include='*.go' -l` | **không có file nào**: chưa có module |
| `grep -n "go-redis\|miniredis" go.mod` | `github.com/alicebob/miniredis/v2 v2.40.0` (dòng 7), `github.com/redis/go-redis/v9 v9.23.0` (dòng 11) đã có |
| `grep -n "REDIS_URL" apps/fhir-server/internal/config/config.go` | `REDIS_URL` đã có ở `fhir-server` (dòng 167), hiện chỉ dùng cho broker |
| `grep -rn "Membership" pkg/fhirauth/*.go` (đã đọc `principal.go`) | `Principal` chỉ có `Subject, Project, Role, Grants, Policy, Job, Super`; **không có membership** |

### A.2 Hành vi cần giữ

Router: `keyValueRouter.use(authenticateRequest)` (`keyvalue/routes.ts:10-11`), gắn ở `apiRouter.use('/keyvalue/v1/', keyValueRouter)` (`S/app.ts:287`) và nhân ra bốn tiền tố (`S/app.ts:297-300`). CORS có `/keyvalue/` trong danh sách đường dẫn cho phép (`S/cors.ts:65`).

`authenticateRequest` (`oauth/middleware.ts:42-57`): có `AuthenticatedRequestContext` thì qua (nếu URL có `/projects/{id}` khác project của token thì `403 Forbidden`, dòng 45-48); không thì đặt `WWW-Authenticate: <scheme> realm="<baseUrl>"` và trả `401 Unauthorized` (dòng 52-55).

| Route | Hành vi chính xác | Nguồn |
|---|---|---|
| `GET /keyvalue/v1/{key}` | `key = singularize(params.key) ?? ''` (lấy phần tử đầu nếu là mảng); `getValue`: `validateKey` rồi `HGET medplum:keyvalue:{membershipId} key`; thiếu thì **404** (`OperationOutcome` `notFound`, `details.text = "Not found"`, `core/outcomes.ts:69-81`); có thì `200`, `Content-Type: text/plain`, body là chuỗi (kể cả chuỗi rỗng) | `routes.ts:13-21`, `store.ts:17-25` |
| `PUT /keyvalue/v1/{key}` | `setValue(key, req.body)`: (1) `validateKey`; (2) `typeof value !== 'string'` thì `400 "Invalid value type"`; (3) `value.length > 8192` thì `400 "Value too long"`; (4) nếu khóa **chưa tồn tại** (`HEXISTS`) và `HLEN >= 10` thì `400 "Max items exceeded"`; (5) `HSET`. Thành công `204` không body | `routes.ts:23-31`, `store.ts:32-51` |
| `DELETE /keyvalue/v1/{key}` | `validateKey` rồi `HDEL`; `204` kể cả khi khóa không có | `routes.ts:33-41`, `store.ts:57-61` |
| Mọi lỗi | `sendOutcome(res, normalizeOperationOutcome(err))`: `Content-Type: application/fhir+json`, mã HTTP lấy từ outcome; `badRequest(msg)` là `issue[0] = {severity:'error', code:'invalid', details:{text:msg}}` | `routes.ts:18-20`, `fhir/outcomes.ts:39-61`, `core/outcomes.ts:279-292` |

Giới hạn số liệu: `MAX_KEY_LENGTH = 255`, `MAX_VALUE_LENGTH = 8192`, `MAX_ITEMS = 10` (`store.ts:8-10`). `validateKey`: khóa rỗng, không phải chuỗi hoặc dài hơn 255 thì `400 "Invalid key"` (dòng 69); không khớp `^[a-zA-Z0-9_-]+$` thì cũng `"Invalid key"` (dòng 72-74). Khóa Redis: `medplum:keyvalue:${ctx.membership.id}` (`store.ts:85-87`), kiểu **hash**; mọi cặp của membership là field trong hash đó. `ctx.membership` là `onBehalfOfMembership ?? membership` (`S/context.ts:117-119`), tức khi quản trị viên dùng header `X-Medplum-On-Behalf-Of` (`oauth/utils.ts:1369-1425`, chỉ admin: dòng 1379-1381) thì kho là của membership được thay mặt.

Đặc điểm cần biết (không phải lỗi nhưng lệch kỳ vọng "cache"):
- **Không có TTL** (`store.ts:50` chỉ `HSET`, không `EXPIRE`); README gọi là cache nhưng dữ liệu không bao giờ hết hạn, và không ai dọn khi membership bị xóa.
- **Không nguyên tử**: `HEXISTS`, `HLEN`, `HSET` là ba lệnh riêng (`store.ts:43-50`) nên hai `PUT` song song khóa mới có thể vượt 10 mục. Lỗi nhỏ của Medplum, Go sẽ sửa.
- `value.length` đếm đơn vị UTF-16 của JavaScript, không phải byte hay rune. Go phải đếm UTF-16 để cùng ngưỡng 8192.
- Body `text/plain` đi qua parser `text({type:[TEXT, HL7_V2]})` (`S/app.ts:262`); giới hạn kích thước của parser này là mặc định thư viện (**chưa xác nhận trong repo**). Body JSON qua parser `json` (giới hạn `maxJsonSize`, mặc định `1mb`: `S/config/utils.ts:32`) cho ra object nên thành `Invalid value type`.
- Lỗi hạ tầng (Redis chết) rơi vào `normalizeOperationOutcome` thành `400` kèm nội dung lỗi (`routes.ts:18-20`, `core/outcomes.ts:537-545`), tức lộ thông điệp nội bộ. Go trả 500/503 chung (cùng quyết định D-7 của auth: `auth/README.md` mục 5.1).

### A.3 Ánh xạ sang Go

Vị trí và lý do:

| Thành phần | Vị trí | Lý do |
|---|---|---|
| Luật nghiệp vụ, cổng `Store`, kiểm khóa/giá trị (đếm UTF-16), `MemoryStore`, `RedisStore` | **`pkg/keyvalue`** mới | `fhir-server` không có tầng `domain/application` (`architecture_test.go:30`), nghiệp vụ dùng chung nằm ở `pkg/` (cùng cách `pkg/fhirsub`). `pkg/` được dùng go-redis (đã có ở `pkg/messaging`), chỉ cấm `labstack/echo` (`architecture_test.go:155-171`) |
| Route Echo, ánh xạ lỗi sang `OperationOutcome`, lấy `Principal` | `apps/fhir-server/internal/presentation/httpapi` | Cùng nơi đã gắn `/fhir/R4`, dùng chung `guardJWT`; không tạo app mới vì cần Verifier và Redis đã cấu hình ở `fhir-server` |
| Cấu hình | `apps/fhir-server/internal/config` | thêm `KEYVALUE_ENABLED` (mặc định tắt), `KEYVALUE_TTL` |

Việc cần sửa trong `architecture_test.go`: thêm `"pkg/keyvalue": {}` vào `pkgRules` (dòng 59-75, bắt buộc bởi `TestEveryPkgHasARule`, dòng 214-231) và thêm `mod + "/pkg/keyvalue"` vào `allow` của `apps/fhir-server/internal/presentation` (dòng 31-33). Không import app khác (dòng 175-211).

Guard: `Guard()` hiện chỉ gắn cho nhóm `/fhir/R4` và quyết định quyền theo `RequiredRole(method, path)` (`auth.go:96-118`, `httpapi.go:69-71`). Cần tách phần xác minh JWT (`guardJWT`, `auth.go:145-198`) thành hàm dùng lại được với một hàm vai trò tối thiểu truyền vào (keyvalue: GET cần `RoleRead`; PUT, DELETE cần `RoleWrite`, **đề xuất**; Medplum chỉ yêu cầu đăng nhập). Gắn middleware theo từng route, không dùng `Group("", mw)` (bài học của auth: `auth/README.md` mục 7, hàng "Echo").

Bearer middleware dùng chung: `auth` có `requireBearer` riêng (`apps/auth/internal/presentation/httpapi/auth.go:322-342`) dựa trên cổng `Authenticator` (`application/ports.go:170-173`) có trạng thái; `fhir-server` dùng `fhirauth.Verifier` không trạng thái. Hai cái khác nhau; keyvalue chạy trên `fhir-server` nên dùng Verifier (chấp nhận cửa sổ thu hồi theo TTL token, quyết định D-13).

### A.4 Mô hình dữ liệu và lưu trữ

Quyết định đề xuất: **Redis hash** như Medplum (không dùng SQL).

| Điểm | Thiết kế |
|---|---|
| Khóa Redis | `trivita:keyvalue:{project}:{membership}` (hash). Thêm `project` để cô lập hai lớp, phòng khi định dạng id membership không duy nhất toàn cục ở Go (Medplum dựa vào id membership duy nhất toàn cục; Go: chưa xác nhận) |
| Field | khóa người dùng (đã giới hạn `[A-Za-z0-9_-]{1,255}`, không bao giờ nằm trong tên khóa Redis nên không có tiêm khóa) |
| Giá trị | chuỗi UTF-8 |
| Giới hạn | 255 / 8192 (UTF-16) / 10 mục như Medplum (hằng số, không cấu hình) |
| Nguyên tử | `PUT` bằng một script Lua: nếu `HEXISTS` thì `HSET`; ngược lại `HLEN >= 10` trả mã "đầy", không thì `HSET`. (miniredis có hỗ trợ `EVAL` hay không: **chưa xác nhận trong repo**, kiểm ở bước K2; phương án dự phòng `WATCH`/`MULTI`) |
| TTL | tùy chọn `KEYVALUE_TTL` (mặc định `0` = không hết hạn để giữ hành vi Medplum); nếu đặt thì `EXPIRE` trên hash sau mỗi `PUT` (hết hạn trượt, áp cho cả hash). Khuyến nghị production: 30 ngày (Q-K3) |
| Cô lập theo membership | khóa Redis dựng **chỉ** từ `Principal.Project` và `Principal.Membership` lấy từ token đã xác minh, không bao giờ từ URL, header hay body |
| Membership vắng (token tĩnh, super-admin) | **từ chối 403** (đóng theo mặc định), không dùng `sub` làm thay thế ngầm (Q-K1 nêu phương án khác) |

Ghi chú Medplum `on-behalf-of`: Go **không port** header này (`fhirauth` không có khái niệm; auth D-22 không giữ super admin).

### A.5 Thuật toán phần khó

1. **Đếm độ dài UTF-16**: `n := 0; for _, r := range s { n += utf16.RuneLen(r) }` (rune không hợp lệ tính 1 như JS giữ nguyên đơn vị lỗi; chuỗi không phải UTF-8 hợp lệ thì từ chối `400 "Invalid value type"`, **đề xuất**). So `> 8192`. Test: chuỗi emoji (2 đơn vị mỗi ký tự).
2. **Đọc body PUT**: chỉ nhận `Content-Type: text/plain` (mọi charset); loại khác thì `400 "Invalid value type"`; đọc tối đa `32768` byte (đủ 8192 đơn vị UTF-16 = tối đa 24576 byte UTF-8), vượt thì `413`. Body rỗng là chuỗi rỗng hợp lệ (Medplum: **chưa xác nhận** vì parser trả `{}` khi không có body; đề xuất theo Go: rỗng = chuỗi rỗng, ghi ở mục 9).
3. **Thứ tự kiểm** giữ như Medplum: khóa, kiểu, độ dài, rồi mới đụng Redis.
4. **Mã lỗi và body**: dùng chung bộ dựng `OperationOutcome` của `fhir-server` (`errorHandler`, `httpapi.go:76-110`) để `Content-Type: application/fhir+json`; thông điệp `details.text` đúng chữ như bảng A.2.

### A.6 Cổng, thư viện, cấu hình

| Hạng mục | Chi tiết |
|---|---|
| Cổng (interface) | `keyvalue.Store { Get(ctx, scope, key) (string, error); Put(ctx, scope, key, value) error; Delete(ctx, scope, key) error }`; `Scope{Project, Membership string}`; lỗi `ErrNotFound`, `ErrMaxItems` |
| Adapter | `MemoryStore` (test, dev), `RedisStore` (`redis.UniversalClient`, dùng `NewRedisFromClient`-kiểu như `pkg/messaging/redis.go:62`) |
| Thư viện | không thêm mới (go-redis, miniredis đã có) |
| Cấu hình | `KEYVALUE_ENABLED=false`, `KEYVALUE_TTL=0`; dùng `REDIS_URL` có sẵn; khi bật mà `REDIS_URL` rỗng thì dùng `MemoryStore` chỉ ở `local`/`test` (production bắt buộc Redis, theo mẫu kiểm cấu hình ở `config.go:249-262`) |
| Tiền tố URL | chỉ `/keyvalue/v1/`; tiền tố `/api/`, `/projects/{id}/` chưa làm (Q chung về tiền tố, `modules-survey.md` mục 7.1) |

### A.7 Ca test từ `keyvalue/routes.test.ts`

| # | Tên gốc | Dòng | Kỳ vọng | Go |
|---|---|---|---|---|
| 1 | Set and get | 35-64 | PUT `text/plain` giá trị uuid: 204; GET: 200, body bằng giá trị, `Content-Type` chứa `text/plain`; DELETE: 204; GET lại: 404, `Content-Type` chứa `application/fhir+json`, `resourceType = OperationOutcome` | giữ |
| 2 | Key too long | 66-87 | khóa 2048 ký tự: PUT, GET, DELETE đều 400; `issue[0].details.text = "Invalid key"`, fhir+json | giữ (thêm biên 255 OK, 256 lỗi) |
| 3 | Invalid key | 89-99 | `key+with+spaces`, `key:with:colons` GET: 400 | giữ |
| 4 | Invalid body | 101-108 | PUT body JSON `{foo:'bar'}`: 400 | giữ (`"Invalid value type"`) |
| 5 | Body too long | 110-117 | PUT 10000 ký tự: 400 | giữ (`"Value too long"`; Go thêm ca đúng 8192 OK, 8193 lỗi) |
| 6 | Max items | 119-135 | PUT 11 khóa khác nhau: 10 khóa đầu 204; khóa thứ 11: 400, fhir+json, `"Max items exceeded"` | giữ |

Ca thêm cho Go (không có ở Medplum): 401 khi thiếu/sai token (cùng `WWW-Authenticate`); ghi đè khóa có sẵn khi đã đủ 10 mục thì 204; xóa một khóa rồi thêm khóa mới thì 204; hai membership cùng project không thấy khóa của nhau; cùng membership id ở project khác không thấy nhau; hai `PUT` song song khóa mới ở mức 9 mục chỉ một thành công; độ dài UTF-16; TTL bằng miniredis `FastForward`; token không có membership thì 403; Redis lỗi thì 503 không lộ chi tiết; khóa có `%2e%2e`, `%00`, khoảng trắng mã hóa thì 400; GET `/keyvalue/v1/` (khóa rỗng) thì 404.
Ca **bỏ**: không có (không ca nào phụ thuộc Bot hay on-behalf-of).

### A.8 Bảo mật (kiểm soát bắt buộc có test)

| Rủi ro | Kiểm soát | Test |
|---|---|---|
| Rò dữ liệu giữa membership/project | khóa Redis dựng từ `Principal` (không từ input); fail đóng khi thiếu membership | ca "hai membership", "hai project", "token không membership 403" |
| Tiêm khóa/lệnh Redis | khóa người dùng chỉ là field hash, regex chặt, không dùng trong tên khóa hay script ghép chuỗi (truyền bằng `ARGV`) | ca khóa chứa `*`, `\n`, `{`, `:` đều 400 |
| Lạm dụng dung lượng | 10 mục x 8192 đơn vị mỗi membership; body cắt ở 32 KiB; TTL tùy chọn | ca vượt; ghi nhận rủi ro số membership không giới hạn (Medplum cũng vậy) |
| Lộ lỗi nội bộ | lỗi Redis thành 503 chung | ca tắt miniredis |
| Đua điều kiện vượt 10 mục | script Lua nguyên tử | ca song song |
| Xác thực | Bearer bắt buộc ở mọi route; không có đường dẫn công khai | ca 401 từng method |

### A.9 Không port đợt này; điểm lệch chấp nhận được

Không port: header `X-Medplum-On-Behalf-Of`; tiền tố `/api` và `/projects/{id}`; chuyển dữ liệu từ Redis Medplum đang chạy (nếu có thì ngoài phạm vi, Q-K4).
Lệch chấp nhận: thêm `project` vào khóa Redis; tùy chọn TTL; `PUT` nguyên tử; Redis lỗi trả 503 thay vì 400; vai trò tối thiểu `RoleRead`/`RoleWrite` (Medplum chỉ cần đăng nhập); `Content-Type` ngoài `text/plain` bị từ chối.

### A.10 Rủi ro và câu hỏi mở

| # | Câu hỏi | Khuyến nghị |
|---|---|---|
| Q-K1 | Danh tính cô lập: thêm claim `fhir_membership` vào token (auth P3) và `Principal.Membership` (sửa `pkg/fhirauth/jwt.go`, `principal.go`, thêm `Config.MembershipClaim`), hay dùng `sub`+`project`? Một `User` có thể có nhiều membership trong cùng project (nhiều profile): chưa xác nhận Go có cho phép không | Thêm claim `fhir_membership`; fallback **không** dùng `sub` (fail đóng) cho tới khi có claim |
| Q-K2 | Keyvalue có bắt buộc đi cùng Bot không (Medplum thiết kế cho Bot)? Không có Bot ở Go thì ai là người dùng? | Giữ làm API chung cho ứng dụng của Trivita; bật bằng cờ |
| Q-K3 | TTL mặc định (không hết hạn như Medplum, hay 30 ngày) | 30 ngày ở production |
| Q-K4 | Có cần di trú dữ liệu keyvalue từ Medplum | Không (cache) |
| Q-K5 | Membership xóa thì dọn hash thế nào | TTL là cách dọn duy nhất đợt này |

### A.11 Các bước làm

| Bước | Việc | Cỡ | Tiêu chí hoàn thành | Phụ thuộc |
|---|---|---|---|---|
| K0 | Chốt Q-K1; thêm `Principal.Membership` và `Config.MembershipClaim` (mặc định `fhir_membership`) vào `pkg/fhirauth`, test `jwt_test.go` | S | token có claim thì `Principal.Membership` đúng; không có thì rỗng | quyết định Q-K1; việc phát claim thuộc auth P3 (B-bước claim) |
| K1 | `pkg/keyvalue`: kiểm khóa/giá trị, UTF-16, `MemoryStore`, test bảng khớp A.7 ca 1-6 | S | test xanh | K0 không bắt buộc |
| K2 | `RedisStore` với script Lua nguyên tử, test miniredis (kể cả song song, TTL) | M | ca song song và TTL xanh; nếu miniredis không hỗ trợ `EVAL` thì dùng `WATCH` | K1 |
| K3 | Tách guard JWT dùng lại; route `/keyvalue/v1/:key` trong presentation; cập nhật `architecture_test.go`; cấu hình | S đến M | `go test ./...` xanh cả test kiến trúc | K1, K2 |
| K4 | e2e với JWT HMAC (mẫu `apps/fhir-server/internal/bootstrap/jwt_e2e_test.go`), ca cô lập, swagger, sửa `docs/api-contract/others.md` (dòng 44-46 ghi "không guard ở route" gây hiểu nhầm: thực tế có `authenticateRequest` ở router) | S | e2e xanh | K3 |

Cỡ tổng: M (2 đến 4 ngày).

---

## Phần B. `mcp`

### B.1 Mục tiêu, phạm vi, hiện trạng Go

Mục tiêu: máy chủ MCP cho phép trợ lý AI (Claude, ChatGPT) gọi API FHIR bằng ba công cụ. Phạm vi: `ALL /mcp/stream` (streamable HTTP không phiên), `GET|POST /mcp/sse` (SSE, có Redis pub/sub), ba công cụ `search`, `fetch`, `fhir-request`. Chỉ gắn khi cấu hình `mcpEnabled` (`S/app.ts:293-295`, `S/config/types.ts:177`).

Hiện trạng Go (grep thật, từ gốc repo):

| Lệnh | Kết quả |
|---|---|
| `grep -rniE 'modelcontextprotocol' apps pkg --include='*.go' -l` | **không có** |
| `grep -rniE 'mcp\|MCP' apps pkg --include='*.go' -l` | chỉ `apps/auth/internal/bootstrap/auth_test.go` (client OAuth tên "mcp" trong test, dòng 34, 42: không liên quan) |
| `grep -n "modelcontextprotocol\|go-sdk" go.mod go.sum` | **không có**: SDK đã bị gỡ khỏi `go.mod` |
| `ls ~/go/pkg/mod/github.com/modelcontextprotocol/` | module cache cục bộ **có** `go-sdk@v0.2.0`, `v0.8.0`, `v1.4.1`, `v1.6.0`, `v1.8.0`; `~/go/pkg/mod/cache/download/.../@v/list` liệt kê cùng các bản (v1.8.0 là bản mới nhất có sẵn) |
| `grep -rniE 'SubscribeBroadcast' apps pkg --include='*.go' -l` | `pkg/fhirsub/manager.go`, `pkg/messaging/{memory,messaging,redis}.go` |
| `grep -rn "Subscribe(" pkg/messaging/messaging_test.go` và `grep -rn miniredis pkg -l` | miniredis dùng ở `pkg/fhirsub/ws_test.go`, `pkg/messaging/messaging_test.go`, `pkg/messaging/redis_env_test.go`, `pkg/workflow/asynq/asynq_test.go`; **chưa thấy test nào dùng `PUBLISH/SUBSCRIBE` với miniredis**, nên hỗ trợ Pub/Sub của miniredis v2.40.0 là **chưa xác nhận** (kiểm ở bước M0) |

Về API của SDK: yêu cầu của bạn là ghi "chưa kiểm" vì không có mạng. Thực tế **mã nguồn v1.8.0 đọc được cục bộ** nên tôi đã đọc phần liên quan (chỉ đọc, không `go get`, không sửa `go.mod`): đây là điều đã thấy, vẫn cần kiểm lại khi thêm vào `go.mod` (đặc biệt các phụ thuộc gián tiếp của SDK: `golang-jwt/jwt/v5` đã có trong repo, còn `google/jsonschema-go`, `segmentio/encoding`, `yosida95/uritemplate/v3`, `golang.org/x/oauth2`, `golang.org/x/time` có trong cache hay không: **chưa xác nhận**; riêng `jsonschema-go` có v0.3.0, v0.4.2, v0.4.3 trong cache).

### B.2 Hành vi cần giữ

`mcpRouter = Router().use(authenticateRequest)` (`mcp/routes.ts:17`): mọi route cần Bearer (thiếu thì `401`, xem A.2).

| Route | Hành vi chính xác | Nguồn |
|---|---|---|
| `ALL /mcp/stream` | mọi method; mỗi request tạo `McpServer` mới + `StreamableHTTPServerTransport({sessionIdGenerator: undefined})` (không phiên); khi `res` đóng thì đóng transport và server | `routes.ts:21-30` |
| `GET /mcp/sse` | tạo `SSEServerTransport('/mcp/sse', res)`; `membershipId = ctx.membership.id` (dòng 37); tạo **subscriber Redis riêng** (`getPubSubRedisSubscriber`, bản `duplicate()` của kết nối pub/sub: `redis.ts:165`); handler `message`: parse `SseEnvelope`, nếu `envelope.membershipId !== membershipId` thì log `warn "Rejected MCP SSE message from a different membership"` và **bỏ** (dòng 43-46), ngược lại chuyển `envelope.message` vào `transport.handlePostMessage` với req/res giả (dòng 47-52); **đăng ký kênh `medplum:mcp:sse:{sessionId}` trước** (dòng 57) rồi mới `server.connect(transport)` (dòng 59-60, lúc này sự kiện `endpoint` mới được gửi cho client); ping theo nhịp `heartbeat` (dòng 62-63); khi `res` đóng: bỏ heartbeat, `redisSubscriber.disconnect()`, đóng transport và server (dòng 65-70) | `routes.ts:35-71` |
| `POST /mcp/sse?sessionId=` | validate `query('sessionId').isUUID()` thông điệp `"Invalid sessionId"` và `body('jsonrpc').isString()` thông điệp `"Invalid JSON-RPC body"` (dòng 77-78); sai thì `400` body `{errors: [...]}` của express-validator (dòng 80-84; dạng từng phần tử: **chưa xác nhận**, chưa đọc phiên bản express-validator); đúng thì đóng gói `{membershipId: ctx.membership.id, message: req.body}` (dòng 86), `PUBLISH` lên `medplum:mcp:sse:{sessionId}` (dòng 87, `pubsub.ts:9-11`) rồi `202` body `Accepted` (dòng 88). **Không kiểm phiên có tồn tại** (kênh không ai nghe vẫn `202`) | `routes.ts:75-90`, `pubsub.ts:9-11` |
| Heartbeat | `heartbeat` là `TypedEventTarget` bắn sự kiện mỗi `heartbeatMilliseconds` (mặc định **10 giây**: `heartbeat.ts:14,24-27`), có thể tắt bằng `heartbeatEnabled=false` (dòng 21-23); mỗi sự kiện gọi `server.server.ping()` | `heartbeat.ts:10-28`, `routes.ts:62-63` |
| Thông tin server | tên `medplum`, phiên bản `MEDPLUM_VERSION` | `server.ts:11-14` |

Công cụ (`mcp/server.ts`):

| Công cụ | Đầu vào | Kết quả | Nguồn |
|---|---|---|---|
| `search` | `{query: string}` | luôn trả tài liệu giả `{type:'text', id:'dummy-doc', text:'This is a dummy document used for testing purposes.', uri:'https://example.com/dummy-doc'}`; log debug `Performing search for: "<query>"` | `server.ts:23-33` |
| `fetch` | `{id: string}` (mô tả "The ID of the resource to fetch, obtained from a search result.") | cùng tài liệu giả; log debug | `server.ts:35-44` |
| `fhir-request` | `{method: string, path: string, body?: any}` | xem dưới | `server.ts:50-118` |

Ghi chú: hai công cụ `search`/`fetch` chỉ để ChatGPT chịu kết nối ("If these tools are not implemented, ChatGPT will not connect", `server.ts:16-21`).

`fhir-request` từng bước (`server.ts:59-116`):
1. `baseFhirUrl = concatUrls(config.baseUrl, 'fhir/R4')`, `fhirUrl = concatUrls(baseFhirUrl, path)` (`concatUrls` = `new URL(ensureNoLeadingSlash(path), ensureTrailingSlash(base))`: `core/utils.ts:1574-1576`, nên URL tuyệt đối trong `path` **thay thế** cả base).
2. **Chống SSRF** (dòng 65-73, vá cho GHSA-fjgc-c3pj-xx2c theo test dòng 171): `new URL(fhirUrl)`; nếu `origin` khác origin của base, hoặc `pathname` không bằng và không bắt đầu bằng `basePathname + '/'` thì `throw Error('Invalid path: must be relative to the FHIR base URL')`.
3. Header on-behalf-of: nếu `authState.onBehalfOfMembership` thì thêm `X-Medplum-On-Behalf-Of: <ref>` (dòng 75-79).
4. Tạo `MedplumClient({baseUrl, accessToken, defaultHeaders, fetch})` mang **token của người gọi** (dòng 80): đây là lý do guard ở bước 2 quan trọng ("The proxy request carries the caller's token", dòng 65).
5. Nếu `body` là chuỗi thì `JSON.parse` (Claude gửi body dạng chuỗi: dòng 82-85); chuỗi không phải JSON thì ném `SyntaxError`.
6. `switch(method.toLowerCase())`: `get`, `delete` (truyền `body`), `patch`, `post`, `put`; còn lại `throw Error('Unsupported method: ' + method)` (dòng 88-106).
7. Trả `{content: [{type:'text', text: JSON.stringify(response), uri: fhirUrl}]}` (dòng 108-116). Lỗi ném ra được SDK gói thành kết quả `isError: true` (test dòng 278 kỳ vọng `isError === true`; cơ chế gói nằm trong thư viện, **chưa đọc**).

Chính tác giả ghi chú (`server.ts:46-49`): cách tải lại URL là "very suboptimal", nên thay bằng `FhirRouter`; còn lại "a few FHIR endpoints not yet available in FhirRouter". Go không có lý do giữ cách tải lại URL.

### B.3 Ánh xạ sang Go

| Thành phần | Vị trí | Lý do |
|---|---|---|
| Công cụ, trình kiểm đường dẫn, cổng `Dispatcher`, cầu SSE (`SessionBus`, kiểm membership) | **`pkg/mcpserver`** mới (không import echo, không import app) | dùng chung được, kiểm thử được không cần Echo; `pkg/` được dùng go-redis |
| Adapter `Dispatcher` (gọi in-process vào chuỗi xử lý FHIR), route Echo `/mcp/stream`, `/mcp/sse`, đọc `Principal` | `apps/fhir-server/internal/presentation/httpapi` | chuỗi guard + `RequiredRole` + `fhirauth.Scope` đã ở đây; cùng `Verifier`; cùng Redis |
| Cờ bật | `apps/fhir-server/internal/config`: `FHIR_MCP_ENABLED=false` | tương đương `mcpEnabled` |

`architecture_test.go`: thêm `"pkg/mcpserver": {}` vào `pkgRules` (cho phép `pkg/core` nếu cần log/ID), thêm `mod + "/pkg/mcpserver"` vào `allow` của presentation của `fhir-server`. Thêm một test kiến trúc riêng cho `pkg/mcpserver`: không dùng `http.Client`, `http.Get`, `http.Post`, `http.DefaultClient`, `net.Dial*` (quét AST); đây là rào chắn SSRF ở mức cấu trúc (mục B.8).

Vì sao **không** gọi `Router.Handle` trực tiếp: `Router.Handle(ctx, *Request)` (`pkg/fhirrouter/router.go:252-265`) không kiểm vai trò. Vai trò nằm ở guard: `RequiredRole` (`auth.go:96-118`) quyết định `$reindex`, `$expunge` cần `RoleAdmin`, ghi cần `RoleWrite`; áp ở `guardJWT` (`auth.go:162`). `Scope` chỉ kiểm project, grants, policy (`pkg/fhirauth/scope.go:36-90`). Gọi thẳng router sẽ cho token `fhir.read` chạy được `POST`/`$expunge` nếu `Scope` cho phép (chưa kiểm tổ hợp này; ở đây coi là rủi ro cần chặn bằng thiết kế). Thiết kế: adapter dựng `http.Request` nội bộ (`method`, URL `/fhir/R4/<path>`, `Authorization` của phiên) và gọi `e.ServeHTTP(recorder, req)` của chính Echo của `fhir-server` (đi qua mọi middleware, guard, `RequiredRole`, `Scope`), đọc kết quả từ recorder. Không có kết nối mạng nào.

Thư viện MCP: `github.com/modelcontextprotocol/go-sdk` (đã dùng trước đây theo `modules-survey.md:33`, bản `v1.8.0` có sẵn trong cache). API đã thấy ở v1.8.0 (đọc source, chưa biên dịch):
- `mcp.NewServer(impl *Implementation, options *ServerOptions) *Server` (`mcp/server.go:211`); `ServerOptions.KeepAlive time.Duration` (`server.go:100`, bật ping định kỳ, mặc định 0 = tắt; ở `server.go:1464` được dùng khi `Connect`).
- `mcp.AddTool[In, Out any](s *Server, t *Tool, h ToolHandlerFor[In, Out])` (`server.go:603`); `ToolHandlerFor` trả `(*CallToolResult, Out, error)`; lỗi trả về được gói thành `IsError` (`mcp/tool.go:27-57`).
- Streamable: `mcp.NewStreamableHTTPHandler(getServer func(*http.Request) *Server, opts *StreamableHTTPOptions)` (`streamable.go:231`); `Stateless bool` (`streamable.go:128-148`: **ở chế độ stateless, GET và DELETE trả 405**, và yêu cầu server-gửi-client bị từ chối), `JSONResponse`, `MaxRequestBodyBytes` (mặc định `4 MiB`: `streamable.go:224`).
- SSE: `mcp.NewSSEHandler(...)` có sẵn **nhưng giữ phiên trong bản đồ bộ nhớ của tiến trình** (`sse.go:49-57` `sessions map[string]*SSEServerTransport`; POST tới instance khác thì `404 "session not found"`, `sse.go:246-261`), và tham số truy vấn tên là `sessionid` viết thường (`sse.go:246`) khác `sessionId` của Medplum. Vì vậy **không dùng** `SSEHandler`; dùng thẳng `mcp.SSEServerTransport` (xuất công khai: trường `Endpoint`, `Response`, `MaxRequestBodyBytes`; `ServeHTTP` nhận POST; `Connect` gửi sự kiện `endpoint`: `sse.go:124-221`) cùng `Server.Connect(ctx, transport, nil)` (`server.go:1432`) và `ServerSession.Wait/Close` (`server.go:2136,2168`).
- Danh sách công cụ của SDK Go **sắp theo tên** (`mcp/features.go:19,75`: "a set", "sorted by unique ID"), nên `tools/list` trả `fetch, fhir-request, search`, khác thứ tự đăng ký `search, fetch, fhir-request` mà test Medplum kỳ vọng (`routes.test.ts:107-109`). Test Go so sánh tập, không so thứ tự.
- `TextContent` của SDK Go chỉ có `Text` (+ `Annotations`, `Meta`): không mang được trường `id`/`uri` mà `server.ts:23-28,110-114` thêm vào. Chưa xác nhận ChatGPT có cần hai trường này (mục 9).

### B.4 Mô hình dữ liệu và lưu trữ

Không có dữ liệu bền. Chỉ có kênh tạm:

| Thành phần | Thiết kế |
|---|---|
| Kênh | `trivita:mcp:sse:{sessionId}` (Pub/Sub thường của Redis, không dùng Streams); `sessionId` là UUID v4 sinh bằng `crypto/rand` (hoặc `google/uuid` v1.6.0 đã là phụ thuộc gián tiếp: `go.mod:68`), được kiểm định dạng trước khi ghép tên kênh |
| Thông điệp | `{"project": "...", "membership": "...", "message": <JSON-RPC>}`; `project` và `membership` lấy từ `Principal` đã xác minh ở **lúc POST**, không từ body |
| Kích thước | thân POST giới hạn (mặc định 4 MiB như SDK, **đề xuất** 1 MiB khớp `maxJsonSize`) |
| Phiên ở bộ nhớ | `sessionId` giữ bởi instance đang giữ luồng GET (không cần lưu ngoài) |
| Cổng | `SessionBus { Publish(ctx, sessionID string, payload []byte) error; Subscribe(ctx, sessionID string) (<-chan []byte, func(), error) }`; adapter `memory` (một tiến trình, cho test và dev) và `redis` (go-redis `Subscribe`/`Publish`) |

Lý do không dùng `pkg/messaging.Broadcaster`: (1) `Subscribe*` phải gọi **trước `Start`** (`messaging.go:41,51`), trong khi mỗi phiên SSE sinh kênh động; (2) topic là `EventType()` cố định, mỗi topic một Redis Stream (`redis.go:93,103-107`), không có API hủy đăng ký; (3) `Publish` dùng `XADD` **không cắt dài** (`redis.go:118`; `grep XTrim|MaxLen pkg/messaging/*.go` rỗng), nên đẩy mọi thông điệp JSON-RPC vào đó làm stream phình mãi; (4) Medplum dùng Pub/Sub tức thời, không bền, đúng bản chất của SSE.

### B.5 Thuật toán phần khó

**B.5.1 SSE nhiều instance, giữ thứ tự và kiểm membership.**

GET `/mcp/sse` (instance A):
1. Guard Bearer, lấy `Principal` (`Project`, `Membership`); thiếu membership thì 403.
2. Sinh `sessionID` (UUID v4). `unsubscribe := bus.Subscribe(ctx, sessionID)` **trước** khi gửi sự kiện `endpoint` (giữ đúng thứ tự `routes.ts:57` rồi `59-60`, nếu không client có thể POST trước khi A nghe và mất thông điệp).
3. `transport := &mcp.SSEServerTransport{Endpoint: "/mcp/sse?sessionId=" + sessionID, Response: w}`; thiết lập header `Content-Type: text/event-stream`, `Cache-Control: no-cache` (SDK đặt trong `SSEHandler` chứ không trong `SSEServerTransport`, nên adapter phải tự đặt: `sse.go:278-280`), `X-Accel-Buffering: no`.
4. `server := NewMCPServer(deps)` mới cho mỗi phiên với `KeepAlive: 10s` (khớp mặc định heartbeat 10 giây). `deps` giữ `Principal` và **mã Bearer của GET** để công cụ gọi `Dispatcher`.
5. `ss, err := server.Connect(ctx, transport, nil)`; goroutine đọc kênh: với mỗi `payload`: giải mã; nếu `env.Membership != principal.Membership` **hoặc** `env.Project != principal.Project` thì log `warn "Rejected MCP SSE message from a different membership"` (kèm `sessionId`) và bỏ; ngược lại tạo request POST giả (`Content-Type: application/json`, body = `message`) và gọi `transport.ServeHTTP(dummyWriter, fakeReq)`; lỗi chỉ log (đúng `routes.ts:53-55`).
6. Chờ `ctx.Done()` (client ngắt) hoặc `ss.Wait()` trả; dọn: `unsubscribe()`, `ss.Close()`.

POST `/mcp/sse?sessionId=` (instance bất kỳ): kiểm `sessionId` là UUID (400 `"Invalid sessionId"`), body là JSON có trường `jsonrpc` kiểu chuỗi (400 `"Invalid JSON-RPC body"`), body có giới hạn; `bus.Publish(sessionID, {Principal.Project, Principal.Membership, message})`; trả `202` body `Accepted`. Trường `membership`/`project` nếu client nhét vào body bị bỏ qua vì envelope do server dựng.

Điểm chặn: công cụ chạy ở instance A bằng token của GET; token hết hạn giữa chừng thì `Dispatcher` trả 401 (guard xác minh lại mỗi lần): hành vi tốt, ghi vào mục 9.
Cần máy chủ HTTP không có `WriteTimeout`: `pkg/httpserve` chỉ đặt `ReadHeaderTimeout` (`httpserve.go:18,44`), phù hợp SSE.
`BodyLimit("16M")` toàn cục (`httpapi.go:50`) áp lên POST, không ảnh hưởng luồng GET.

**B.5.2 `fhir-request` bằng gọi nội bộ.**

Chuẩn hóa `path` (thay cho kiểm origin và tiền tố của Medplum), thứ tự:
1. `path` không được chứa ký tự điều khiển (`< 0x20`, `0x7f`), `\`, `#`, `@`, hay chuỗi `://`; `len <= 2048`.
2. `url.Parse("/" + trimLeftOne(path, "/"))` (bỏ **một** `/` đầu); từ chối nếu kết quả có `Scheme`, `Host`, `User`, hoặc chuỗi gốc bắt đầu `//`.
3. Giải mã phần trăm **một lần** trên `Path` rồi tách theo `/`; từ chối nếu có đoạn `.` hoặc `..` (cũng sau giải mã, nên `%2e%2e` bị chặn), hoặc đoạn rỗng giữa chừng; giải mã lần hai thấy `%` hay `\` thì từ chối.
4. URL gửi đi là `Prefix + "/" + <path đã làm sạch>` + `?RawQuery` (đã `url.ParseQuery` hợp lệ), **không bao giờ** ghép từ chuỗi người dùng vào host.
5. Method: `strings.ToLower` thuộc `{get,post,put,patch,delete}`, còn lại lỗi `Unsupported method: <method>` (giữ nguyên chữ của Medplum).
6. Body: nếu là chuỗi thì `json.Unmarshal` (lỗi thì tool error); `PATCH` đặt `Content-Type: application/json-patch+json`, còn lại `application/fhir+json`; `GET` bỏ body; giới hạn 1 MiB.
7. Gọi `Dispatcher.Do(ctx, method, relPath, rawQuery, body)` với `context.WithTimeout(30s)`.
8. Kết quả: `2xx` thì `text` = thân JSON (khớp `JSON.stringify(response)`); `204` không thân (DELETE ở Go: `pkg/fhirrouter/handlers.go:372-381` trả 204 không thân) thì **tổng hợp `allOk`** `{"resourceType":"OperationOutcome","id":"ok","issue":[{"severity":"information","code":"informational","details":{"text":"All OK"}}]}` để khớp test `routes.test.ts:164-165` (`deleteResult.id === 'ok'`; `allOk` định nghĩa ở `core/outcomes.ts:27-39`); không `2xx` thì `IsError: true` và `text` là thân `OperationOutcome` (chữ có `severity: "error"` để khớp test dòng 169, 176...). Cắt thân phản hồi ở 4 MiB (Medplum không cắt: chưa xác nhận; ghi ở mục 9).
9. Không đưa token, URL tuyệt đối hay header vào `text`.

**B.5.3 Kiểm membership** (đã nêu ở B.5.1): kiểm ở **bên nhận** (subscriber), không chỉ bên gửi, vì bên gửi (instance bất kỳ) không biết chủ phiên. Medplum kiểm một khóa (membership); Go kiểm hai khóa (project và membership).

### B.6 Cổng, thư viện, cấu hình

| Hạng mục | Chi tiết |
|---|---|
| Thư viện thêm | `github.com/modelcontextprotocol/go-sdk` (thử `v1.8.0` offline ở bước M0) |
| Cổng | `Dispatcher` (adapter in-process), `SessionBus` (memory, redis), `Clock` nếu cần |
| Cấu hình `fhir-server` | `FHIR_MCP_ENABLED` (mặc định `false`), `FHIR_MCP_KEEPALIVE` (mặc định `10s`), `FHIR_MCP_MAX_BODY` (1 MiB), `FHIR_MCP_TOOL_TIMEOUT` (30s); dùng `REDIS_URL` có sẵn; `memory` bus chỉ cho phép khi `Environment` là `local`/`test` hoặc một replica (kiểm như `config.go:249-262`) |
| CORS | **không** thêm `/mcp` vào danh sách CORS (Medplum cũng không có: `S/cors.ts` danh sách (quanh dòng 65) không chứa `/mcp`; đã grep `mcp` trong `cors.ts`: rỗng) |
| Tiền tố URL | chỉ `/mcp/stream`, `/mcp/sse` |

### B.7 Ca test từ `mcp/routes.test.ts`

| # | Tên gốc | Dòng | Kỳ vọng | Go |
|---|---|---|---|---|
| 1 | Unauthenticated streamable HTTP | 59-62 | `GET /mcp/stream` không token: 401 | giữ |
| 2 | Unauthenticated SSE | 64-67 | `GET /mcp/sse` không token: 401 | giữ |
| 3 | SSE missing sessionId query param | 69-75 | `POST /mcp/sse` có token, không `sessionId`: 400 | giữ |
| 4 | SSE missing JSON-RPC body | 77-83 | `POST ?sessionId=<uuid>` body `{foo:'bar'}`: 400 | giữ |
| 5 | MCP with stream/sse transport (x2) | 85-201 | kết nối client; `tools/list` có `search`, `fetch`, `fhir-request` (dòng 107-109); gọi `search`, `fetch` có kết quả (111-115); chuỗi `fhir-request`: (1) POST Patient tạo (131-136), (2) GET đọc (138-140), (3) PUT cập nhật có `address` (142-148), (4) PATCH JSON Patch `test`+`add` (150-156), (5) GET `Patient` tìm thấy bản ghi (158-161), (6) DELETE trả `OperationOutcome` `id='ok'` (163-165), (7) method `UNKNOWN` lỗi `severity='error'` (167-169) | giữ cho cả hai transport; ca `tools/list` so tập, không so thứ tự |
| 6 | (trong 5) SSRF/token-exfiltration | 171-177 | path `http://169.254.169.254/latest/meta-data/` với mọi method GET, POST, PUT, PATCH, DELETE: lỗi trước khi gọi ra ngoài | giữ, thêm khẳng định "không có kết nối ra ngoài" |
| 7 | (trong 5) host ngoài https | 179-181 | `https://attacker.example/collect`: lỗi | giữ |
| 8 | (trong 5) protocol-relative | 183-186 | `//169.254.169.254/latest/meta-data/` được xử lý như request cùng origin (trả `OperationOutcome`) | **đổi**: Go từ chối hẳn (đều ra `OperationOutcome` lỗi nên khẳng định `resourceType` vẫn đúng); ghi ở mục 9 |
| 9 | (trong 5) thoát khỏi `/fhir/R4` | 188-197 | `../../auth/me`, `%2e%2e/%2e%2e/auth/me`, `http://localhost:<port>/auth/me`, `../R4x/Patient`: lỗi | giữ |
| 10 | SSE ignores messages posted by a different membership | 203-240 | owner nối SSE; `other` POST tới `sessionId` của owner: `202`; sau đó owner gọi `GET Patient?name=<family>`: 0 kết quả (message của `other` không chạy) | giữ; thêm cùng membership khác project, và test hai instance (hai `Handler` chung một `SessionBus`) |
| 11 | MCP ... On-Behalf-Of (x2) | 242-282 | admin với header `X-Medplum-On-Behalf-Of` tạo `Observation` thành công (policy cho phép), `GET Patient` bị từ chối `isError=true` | **bỏ** (không port on-behalf-of); thay bằng ca "token có `fhir_access_policy` chỉ Observation: tạo Observation được, GET Patient `isError`" (dùng `fhirauth.Policy`, đã có) |

Ca thêm cho Go: token `fhir.read` gọi `POST Patient` và `$expunge` bị `isError` (khẳng định dùng guard, không gọi thẳng router); `Dispatcher` giả ghi nhận request để khẳng định `Authorization` đúng bằng token của phiên; thân phản hồi không chứa token; POST tới `sessionId` không tồn tại: `202` (giữ hành vi Medplum); `sessionId` không phải UUID: 400; client ngắt thì goroutine và đăng ký kênh được dọn (kiểm số subscriber về 0); thân POST quá lớn: 413; DELETE trả `allOk` tổng hợp; `body` dạng chuỗi JSON và chuỗi hỏng; method hoa/thường; `KeepAlive` bắn ping (dùng `Server` thật và client SDK trong test).

### B.8 Bảo mật (kiểm soát bắt buộc có test)

| Rủi ro | Kiểm soát | Test |
|---|---|---|
| **SSRF** qua `fhir-request` | không có kết nối ra ngoài: cổng `Dispatcher` in-process, URL dựng từ hằng `Prefix` + đoạn đã làm sạch; test kiến trúc cấm `http.Client`/`net.Dial` trong `pkg/mcpserver` | ca B.7 #6-9 + test AST; vòng lặp 5 method x danh sách payload (IPv6 `[::1]`, `0x7f000001`, `http:\\evil`, `%2f%2f`, CRLF `%0d%0a`, null `%00`, userinfo `a@b`) |
| **Chuyển token** sang đích khác | token chỉ đi vào request nội bộ; không có đích nào khác; không ghi vào log, không đưa vào kết quả | ca "thân không chứa token", ca log redaction |
| **Giả mạo membership** (POST vào phiên của người khác) | envelope do server dựng từ `Principal` lúc POST; kiểm `project` và `membership` ở bên nhận; `sessionId` 122 bit ngẫu nhiên, định dạng được kiểm | ca B.7 #10 và biến thể |
| Leo quyền qua công cụ | đi qua đúng guard `RequiredRole` + `Scope` (không gọi `Router.Handle`) | ca token đọc-chỉ gọi POST/`$expunge` |
| Rò dữ liệu giữa project | `fhirauth.Scope` áp qua chuỗi guard; thêm ca token project A đọc id của project B: 404 | ca chéo project |
| Rò dữ liệu lâm sàng ra bên thứ ba (kênh MCP chuyển thẳng PHI cho LLM bên ngoài) | tắt mặc định (`FHIR_MCP_ENABLED=false`); mỗi lần gọi công cụ ghi log kiểm toán (subject, project, method, đường dẫn đã chuẩn hóa, mã trạng thái) | ca cờ tắt thì `/mcp/*` là 404; ca log |
| Lạm dụng tài nguyên (SSE treo, thông điệp lớn) | giới hạn thân POST; giới hạn số phiên SSE đồng thời theo membership (mặc định 5, **đề xuất**; Medplum không có); dọn tài nguyên khi client ngắt | ca vượt số phiên (429), ca rò goroutine |
| DNS rebinding trên localhost | giữ `DisableLocalhostProtection=false` mặc định của SDK (`sse.go:228-234`, `streamable.go:319`) nếu dùng `SSEHandler`/`StreamableHTTPHandler`; với `SSEServerTransport` tự dùng, kiểm `Host` ở adapter | ca `Host` lạ trên loopback |
| Prompt injection từ dữ liệu FHIR vào LLM | ngoài phạm vi kỹ thuật của server; ghi vào rủi ro | không |

### B.9 Không port đợt này; điểm lệch chấp nhận được

Không port: `X-Medplum-On-Behalf-Of`; công cụ nào ngoài ba công cụ trên (đã đọc hết `server.ts`: chỉ có ba); `search`/`fetch` thật (Medplum cũng chỉ là bản giả); tiền tố `/api`, `/projects/{id}`.
Lệch chấp nhận: thứ tự `tools/list`; `TextContent` không có `id`/`uri`; stateless GET/DELETE trả 405 (Medplum dùng SDK TypeScript, hành vi GET ở stateless: chưa xác nhận); `//host` bị từ chối thay vì xử lý như cùng origin; giới hạn thân/phiên/thời gian mới; `sessionId` viết `sessionId` (không `sessionid`); `DELETE` tổng hợp `allOk`; lỗi hạ tầng trả chung.

### B.10 Rủi ro và câu hỏi mở

| # | Câu hỏi | Khuyến nghị |
|---|---|---|
| Q-M1 | Thêm SDK vào `go.mod`: bản nào, các phụ thuộc gián tiếp có đủ trong cache không (chưa xác nhận), có chấp nhận đưa vào `go.mod` chính hay tách module riêng như lần trước (survey ghi "bị `go mod tidy` gỡ khi các app dùng nó được tách ra") | `v1.8.0`, kiểm offline ở M0 |
| Q-M2 | Pub/Sub của miniredis v2.40.0 có đủ để test cầu SSE không (chưa xác nhận); nếu không thì dùng Redis thật qua biến môi trường như `pkg/messaging/redis_env_test.go` | kiểm ở M0 |
| Q-M3 | `ChatGPT` có cần `id`/`uri` trong nội dung `search`/`fetch` (chưa xác nhận) | thử với client thật khi có môi trường |
| Q-M4 | Có cần `streamable` có trạng thái (GET mở luồng) cho client nào không | không, chỉ stateless như Medplum |
| Q-M5 | Claim membership (xem Q-K1) | dùng chung quyết định K0 |
| Q-M6 | `mcp` đặt ở `fhir-server` hay app riêng: app riêng cần gọi `fhir-server` qua mạng (mất lợi thế in-process) | `fhir-server` |
| Q-M7 | Giới hạn số phiên SSE theo membership và có ghi kiểm toán từng lần gọi công cụ | có, 5 phiên |

### B.11 Các bước làm

| Bước | Việc | Cỡ | Tiêu chí hoàn thành | Phụ thuộc |
|---|---|---|---|---|
| M0 | Thêm SDK offline (`GOFLAGS=-mod=mod GOPROXY=off go get github.com/modelcontextprotocol/go-sdk@v1.8.0`, **chưa chạy**) và `go build ./...`; thử PUBLISH/SUBSCRIBE với miniredis | S | biên dịch được; kết luận Q-M1, Q-M2 | không |
| M1 | `pkg/mcpserver`: ba công cụ, trình làm sạch đường dẫn, cổng `Dispatcher`, test bảng SSRF; test AST cấm client HTTP | M | B.7 #5-9 chạy với `Dispatcher` giả | M0 |
| M2 | `/mcp/stream` stateless trong presentation, cờ cấu hình, `architecture_test.go` | S | B.7 #1, #5 (stream) xanh | M1 |
| M3 | `SessionBus` memory + redis; cầu SSE (GET/POST), kiểm membership + project, dọn tài nguyên, KeepAlive | L | B.7 #2-4, #5 (sse), #10 xanh, kể cả hai instance | M1, K0 (`Principal.Membership`) |
| M4 | Adapter `Dispatcher` in-process (Echo `ServeHTTP`), ánh xạ `allOk` cho DELETE, giới hạn kích thước; e2e với JWT và `fhirauth.Scope` | M | ca "token đọc-chỉ không POST", chéo project, policy xanh | M1, M2 |
| M5 | Giới hạn phiên, log kiểm toán, tài liệu (`docs/api-contract/others.md` dòng 47-49), swagger nếu áp dụng | S | tài liệu cập nhật | M3, M4 |

Cỡ tổng: M đến L (5 đến 8 ngày). Phụ thuộc auth P3 chỉ ở việc phát claim membership; khi chưa có, e2e dùng token HMAC tự dựng.

---

## Phần W. `webhook`

### W.1 Mục tiêu, phạm vi, hiện trạng Go

Mục tiêu: endpoint **công khai, không đăng nhập** để dịch vụ ngoài (Cal.com, eFax, Twilio, SendGrid: `webhook/README.md:3`) gọi tới và chạy một Bot với quyền của một membership. Phạm vi: `POST /webhook/{membershipId}`. Toàn bộ giá trị nằm ở việc **chạy Bot**, nên bị chặn bởi quyết định runtime Bot (`cds.md` mục 3; `coverage-matrix.md` M1).

Hiện trạng Go (grep thật, từ gốc repo):

| Lệnh | Kết quả |
|---|---|
| `grep -rniE 'webhook\|Webhook' apps pkg --include='*.go' -l` | `pkg/fhirsub/{ssrf,fhirsub_test}.go` và `apps/fhir-server/internal/{bootstrap,config}/*`: đều là **Subscription rest-hook gửi đi** (`FHIR_WEBHOOK_*`: `config.go:88-91,168-171`), **không phải** webhook nhận vào |
| `grep -rniE 'publicWebhook\|PublicWebhook' apps pkg --include='*.go' -l` | chỉ `pkg/fhir/resource_bot.go`, `pkg/fhir/schema_gen.go`: có kiểu `Bot.PublicWebhook`, `Bot.RawBody`, `Bot.RunAsUser` (`resource_bot.go:65-75`) |
| `grep -rniE 'BotExecutor\|executeBot\|ExecuteBot' apps pkg --include='*.go' -l` | **không có**: chưa có cổng thực thi Bot |
| `grep -rniE 'X-Signature\|hmac\.' apps pkg --include='*.go' -l` | `pkg/fhirsub/{event,ws,deliver,fhirsub_test}.go`, `pkg/fhirauth/seal.go`: HMAC dùng cho **ký gửi đi** và niêm phong job, không phải xác thực webhook nhận vào |
| Nơi lưu `ProjectMembership`/`Project` | thuộc kho của `auth` (quyết định D-2, `auth/README.md` mục 5.1): `fhir-server` **không** đọc được trực tiếp; `Bot` nằm ở kho lâm sàng của `fhir-server` (`cds.md` mục 1, 4) |

### W.2 Hành vi cần giữ

#### W.2.1 Từng bước của `webhookHandler` (`webhook/routes.ts:17-71`)

| Bước | Hành vi | Mã và thông điệp | Dòng |
|---|---|---|---|
| 1 | `membershipId = singularize(req.params.id) ?? ''`; đọc `ProjectMembership` bằng **global system repo** (không giới hạn project) | không tồn tại: ném `OperationOutcomeError` NotFound qua error handler chung: **404** (test `routes.test.ts:174-181`; dạng body: chưa xác nhận, test chỉ kiểm mã) | 18-20 |
| 2 | `runAs.profile.reference` phải bắt đầu `Bot/` | `403`, body chuỗi `ProjectMembership must be for a Bot resource` | 23-26 |
| 3 | membership phải có `access` hoặc `accessPolicy` | `403`, `ProjectMembership must have an Access Policy` | 29-32 |
| 4 | `systemRepo = getProjectSystemRepo(runAs.project)`; đọc `Bot` theo `runAs.profile`; đọc `Project` theo `runAs.project` | lỗi đọc: lỗi NotFound/khác qua handler chung (chưa xác nhận) | 34-38 |
| 5 | `canProjectReadBot(project, bot)`: bot phải có `meta.project`; bằng project của membership, hoặc project được liên kết (`Project.link`) có xuất `Bot` (`getPermittedProjectIds`: `fhir/repo.ts:260-290`; `getAllowedProjects`: `fhir/accesspolicy.ts:139-167`) | `403`, `Bot is not accessible from the ProjectMembership project` | 39-42; `bots/utils.ts:239-250` |
| 6 | `bot.publicWebhook` phải truthy | `403`, `Bot is not configured for public webhook access` | 45-48 |
| 7 | `headers = req.headers` **nguyên vẹn** (kể cả `Authorization`, `Cookie`) | | 50 |
| 8 | Nếu `POST` thì `input = getBotBodyInput(bot, req.body, res.locals.rawBody)`; nhánh `GET` dùng `req.query` nhưng **không bao giờ chạy** vì router chỉ có POST | | 55 |
| 9 | `executeBot({bot, runAs, input, contentType: req.header('content-type'), headers})` | | 57-63 |
| 10 | kết quả là `OperationOutcome` thì `sendOutcome`; còn lại `sendBotResponse(req, res, result)` | | 65-70 |
| Router | `webhookRouter.post('/:id', webhookHandler)` (chỉ POST); gắn `apiRouter.use('/webhook/', webhookRouter)` (`S/app.ts:291`), nhân ra bốn tiền tố (`app.ts:297-300`); **không** có `authenticateRequest` | | `routes.ts:73-74` |

`getBotBodyInput` (`bots/utils.ts:91-93`): `bot.rawBody && rawBody ? rawBody.toString('utf8') : body`. Raw body chỉ được bắt cho `JSON_TYPE` = `application/json` và `application/*+json` (`app.ts:69,249-259`, parser JSON riêng `limit: config.maxJsonSize` mặc định `1mb`: `config/utils.ts:32`); các kiểu khác dùng parser chung: `urlencoded({extended:false})`, `text({type:[text/plain, hl7-v2]})`, `json` (`app.ts:261-263`).

`executeBot` (`bots/execute.ts:30-87`): đọc project của `runAs` (dòng 39-42); **project phải có tính năng `bots`** (`isBotEnabledForProject`: `bots/utils.ts:224-226`) nếu không thì kết quả `{success:false, logResult:'Bots not enabled'}` (dòng 70-72); runtime ngoài `awslambda|vmcontext|fission` thì `'Unsupported bot runtime'` (dòng 55-69); nạp access token và secret (dòng 51-52); ghi `AuditEvent` (dòng 79-85).

`sendBotResponse` (`bots/utils.ts:519-551`): `returnValue` thiếu thì dùng `allOk`/`badRequest(logResult)` (dòng 158-170); số đổi thành chuỗi (dòng 163-166); `OperationOutcome` lỗi thì dùng mã của nó (dòng 532-535); `outcome = success ? allOk : badRequest(logResult)` (dòng 537); `Binary` không có `data` thì 400 `Bot returned a Binary without data` (dòng 539-542; `isBinaryWithoutData`: dòng 505-507); là resource thì `sendFhirResponse(..., {forceRawBinaryResponse:true})` (dòng 544-547); còn lại `res.status(getStatus(outcome)).type(getResponseContentType(req)).send(responseBody)` (dòng 551).

#### W.2.2 Điều README nói nhưng code không làm (kết luận về chữ ký)

Câu hỏi: webhook Medplum có kiểm header chữ ký không? **Không.** Bằng chứng:

- `grep -rniE "x-signature|x-hmac|x-cal-signature|x-twilio" --include='*.ts' S/` (đã chạy, loại file test): chỉ có `S/workers/subscription.ts:965` (`headers['X-Signature'] = createHmac('sha256', secret).update(body).digest('hex')`, tức Medplum **ký khi gửi đi** cho Subscription rest-hook, theo extension `subscription-secret` ở dòng 961-963). Không có dòng nào ở `webhook/`, `bots/`, `oauth/`, `app.ts`.
- `grep -rniIE "x-hmac-signature|x-cal-signature|x-twilio-signature|x-twilio-email" packages --exclude-dir=node_modules` chỉ ra `server/src/webhook/README.md` và `docs/content/docs/integration/twilio-sms/receiving-sms.md`; `grep -rniI "x-signature"` thêm `webhook/routes.test.ts`, `workers/subscription.ts`, `workers/subscription.test.ts`, `docs/content/docs/subscriptions/subscription-extensions.md`.
- Trong `webhook/routes.test.ts` mọi request gửi header `x-signature: signature` (dòng 178, 187, 197, 206, 215, 227, 262, 308) nhưng **không có test nào gửi thiếu header và kỳ vọng 400**; nhiều test khác (dòng 115-119, 131-135, 143-148, 155-158) **không gửi** `x-signature` và vẫn 200. Vậy header chỉ là hình thức trong test.
- `docs/content/docs/integration/twilio-sms/receiving-sms.md:46-48`: "The integration also independently validates Twilio's HMAC-SHA1 `x-twilio-signature` header"; tức **Bot tự kiểm** chữ ký của vendor, Medplum không làm hộ.
- `webhook/README.md` lệch ba điểm: dòng 8 `GET/POST` (code chỉ POST: `routes.ts:74`; nhánh GET ở dòng 55 là mã chết), dòng 15-17 và 21-27 "request phải có một header chữ ký" (code không kiểm), dòng 33-35 mã trả (`400` thiếu chữ ký hoặc membership sai, `404`; code thật `403` kèm thông điệp ở W.2.1 và `404`); dòng 17 "Bot must be deployed and active": code không kiểm `deployed` hay `active`.
- Kết luận: **bảo mật của webhook Medplum nằm ở tính không đoán được của `membershipId`** (id ngẫu nhiên, đóng vai "URL bí mật") cộng với việc Bot tự xác thực chữ ký vendor. Tin tưởng vào README sẽ dẫn tới đánh giá sai mức an toàn.

Thêm các điểm lệch bảo mật khác của handler Medplum (từ `routes.ts` và `bots/`):
- Không kiểm `membership.active === false` hay `project.active === false` (`getInactiveReason`: `fhir/projectmembership.ts:33-44`) trong `webhook/routes.ts:17-71` hay `bots/execute.ts:30-87`; `grep -n "active\|Inactive" S/bots/*.ts` (loại test) rỗng. `getBotAccessToken` cũng tạo `Login` mới `granted: true` mà không xét `active` (`bots/utils.ts:359-389`); việc vô hiệu hóa chỉ phát huy khi Bot gọi API (đường Bearer có kiểm active).
- Khi Bot lỗi, `sendBotResponse` trả `badRequest(result.logResult)` (`bots/utils.ts:162,537`): **log của Bot (có thể có stack trace, dữ liệu, secret in ra) lọt tới người gọi ẩn danh**.
- Header `Authorization` được chuyển nguyên cho Bot (`routes.ts:50`; test `routes.test.ts:141-151`; `docs/content/docs/bots/bot-basics.md:92` ghi "Authorization is removed, except on /webhook requests"), trong khi đường xác thực dùng `getBotRequestHeaders` bỏ nó (`bots/utils.ts:128-131`). Cố ý (Bot cần xác thực vendor) nhưng cần ghi nhận.
- Không thấy hạn mức riêng cho `/webhook`: `grep -n "webhook\|keyvalue\|mcp" S/ratelimit.ts` rỗng (chỉ `cors.ts:65` nhắc `/keyvalue/`); hạn mức chung theo IP, chi tiết: chưa xác nhận.
- `projectId` trong tiền tố URL `/projects/{id}/webhook/{membershipId}` không được so với project của membership trong `webhookHandler` (không có `authenticateRequest` ở đường này: `routes.ts:73-74`, `app.ts:291`); test `routes.test.ts:111-123` chỉ kiểm rằng bốn tiền tố đều chạy.

### W.3 Ánh xạ sang Go

Điểm khó kiến trúc: dữ liệu nằm ở **hai kho**: `ProjectMembership`, `Project` ở `auth`; `Bot` ở `fhir-server`. App không được import app khác (`architecture_test.go:175-211`). Đề xuất:

| Thành phần | Vị trí | Lý do |
|---|---|---|
| Cổng thực thi Bot và ánh xạ phản hồi (`BotExecutor`, `BotInvocation`, `BotResult`, `InputFor`, `Respond`, `CanProjectReadBot`) | **`pkg/bots`** mới (thuần, không echo), **dùng chung với `cds`** | `cds.md` mục 4 đề xuất `pkg/cds` và cổng `BotExecutor`; webhook cần cùng cổng và cùng `sendBotResponse`; để một chỗ tránh hai bản. Phải thống nhất với kế hoạch CDS (Q-W5) |
| Nghiệp vụ webhook (tra, kiểm, ghép đầu vào) | `pkg/bots` hoặc `pkg/webhook` (nhỏ, nên gộp vào `pkg/bots`) | như trên |
| Cổng tra cứu `Directory` (đọc `ProjectMembership`, `Project`) và `BotReader` (đọc `Bot` không giới hạn project, **chỉ sau** khi đã có membership) | cổng ở `pkg/bots`; adapter ở `apps/fhir-server/internal/bootstrap` (composition root) | quyết định cách đi qua ranh giới hai kho ở Q-W1 |
| Route Echo công khai `POST /webhook/:id`, đọc body, ánh xạ phản hồi | `apps/fhir-server/internal/presentation/httpapi` | cùng chỗ với `/cds-services` (`cds.md` mục 4); không qua `Guard` |

Cách qua ranh giới (Q-W1), chưa chốt: (A) `fhir-server` mở kết nối **chỉ đọc** tới CSDL của `auth` (cùng kiểu phương án C của D-10 trong `auth/README.md`); (B) `auth` cung cấp endpoint nội bộ đọc membership/project bằng token dịch vụ (thêm bề mặt tấn công và phụ thuộc mạng); (C) đặt webhook trong `auth` và để `auth` gọi `fhir-server` đọc Bot. Đề xuất **A** (đơn giản, nhất quán với D-10), giữ qua cổng để đổi được.

`architecture_test.go`: thêm `"pkg/bots": {mod + "/pkg/fhir"}` vào `pkgRules`; thêm `mod + "/pkg/bots"` vào `allow` của presentation `fhir-server`.

Hành vi mặc định khi **chưa có executor thật**: `FHIR_WEBHOOK_PUBLIC_ENABLED=false`; bật mà không có executor thì route trả `503` body chung (đóng theo mặc định), không bao giờ im lặng trả 200.

### W.4 Mô hình dữ liệu và lưu trữ

Webhook không có dữ liệu riêng. Đọc: `ProjectMembership` (trường `profile`, `access`, `accessPolicy`, `project`, `active`), `Project` (`features`, `link`, `exportedResourceType`, `active`), `Bot` (`publicWebhook`, `rawBody`, `runAsUser`, `meta.project`). Ghi (khi có runtime): `AuditEvent` qua cổng `AuditSink` (Medplum: `createBotAuditEvent`, `bots/execute.ts:79-85`). Hạn mức: bộ đếm theo IP và theo `membershipId` (Redis nếu có, bộ nhớ nếu không; dùng chung với việc gói rate limit nền, `modules-survey.md` mục 7.4).

### W.5 Thuật toán phần khó: xác thực và quyền của webhook công khai

Chuỗi kiểm (giữ thứ tự và thông điệp của Medplum, thêm các kiểm soát an toàn đánh dấu **[+]**):

1. **[+]** Hạn mức theo IP trước mọi thứ (429).
2. **[+]** `id` là chuỗi hợp lệ theo `[A-Za-z0-9._-]{1,64}`; sai thì 404 (không chạm kho).
3. `Directory.Membership(id)`: không có thì **404**.
4. `profile` bắt đầu `Bot/` nếu không `403 ProjectMembership must be for a Bot resource`.
5. `access` hoặc `accessPolicy` có nếu không `403 ProjectMembership must have an Access Policy`.
6. **[+]** `membership.active == false` hoặc `project.active == false` thì `403` (Medplum không kiểm; xem W.2.2).
7. Đọc Bot qua `BotReader` bằng id lấy từ `profile`; đọc `Project`.
8. `CanProjectReadBot(project, bot)` nếu không `403 Bot is not accessible from the ProjectMembership project`. Go: `bot.meta.project == project.id`, hoặc `Project.link` có phần tử `project` là chủ của Bot và (`exportedResourceType` rỗng hoặc chứa `Bot`) (`pkg/fhir/resource_project.go:77`, `ProjectLink` dòng 141; trường `exportedResourceType`: **chưa xác nhận** có trong kiểu Go).
9. `bot.publicWebhook != true` thì `403 Bot is not configured for public webhook access`.
10. **[+]** Tính năng `bots` của project: kiểm trong cổng `BotExecutor` hay ngay đây (Medplum kiểm trong `executeBot`, nên khi thiếu thì kết quả "Bots not enabled" mang mã 400 qua `sendBotResponse`; Go giữ ánh xạ đó).
11. Ghép đầu vào: `InputFor(bot, contentType, rawBytes, parsed)`; chỉ `application/json` và `application/*+json` có raw body, `bot.rawBody == true` thì trả chuỗi raw; JSON hỏng thì **400** trước khi gọi executor (test `handler.test.ts:91-96`); `text/plain` thì chuỗi; `x-www-form-urlencoded` thì object chuỗi (`routes.test.ts:153-161`); các kiểu khác: theo Medplum chưa xác nhận (`defaultBodyParser` chưa đọc).
12. Header chuyển cho Bot: **tất cả**, kể cả `Authorization`, như Medplum, nhưng **[+]** không bao giờ ghi header nhạy cảm vào log và không dùng chúng để xác thực Medplum.
13. `executor.Execute(ctx, invocation)` với `context.WithTimeout`.
14. Ánh xạ kết quả bằng `Respond` (port của `sendBotResponse`), **[+]** với thất bại (Medplum trả `logResult`): thân là thông điệp chung `Bot execution failed`, chi tiết chỉ vào log máy chủ (khác Medplum; mục 9).

**Chữ ký (quyết định, Q-W3):** baseline giữ như Medplum (không kiểm chữ ký, Bot tự kiểm; bảo mật dựa vào id không đoán được và các kiểm soát [+]). Mở rộng **tùy chọn, mặc định tắt**: nếu Bot có secret `webhookSecret` (lưu theo project, cùng chỗ với Q3 của `cds.md`) thì bắt buộc `X-Signature` = hex(HMAC-SHA256(secret, raw body)) so bằng `hmac.Equal` (cùng định dạng Medplum ký đi ở `workers/subscription.ts:965`); không tự làm chữ ký đặc thù từng vendor (Twilio là HMAC-SHA1 trên URL và tham số, SendGrid là ECDSA) vì cần `rawBody` và URL công khai chính xác: để Bot làm như Medplum. **Không** làm kiểu README ("có một trong năm header" là vô nghĩa về mật mã).

Phần chặn bởi runtime Bot: bước 13 (và các phụ thuộc của nó: token cho `runAs` giống `getBotAccessToken` `bots/utils.ts:343-399` tạo `Login` và JWT, cần auth P3; secret `getBotSecrets` `bots/utils.ts:433-470`; `AuditEvent`; `storeBotInput`). Mọi thứ còn lại làm được trước.

### W.6 Cổng, thư viện, cấu hình

| Hạng mục | Chi tiết |
|---|---|
| Cổng | `BotExecutor { Execute(ctx, BotInvocation) (BotResult, error) }`; `Directory { Membership(ctx, id) (*fhir.ProjectMembership, error); Project(ctx, ref) (*fhir.Project, error) }`; `BotReader { Bot(ctx, ref) (*fhir.Bot, error) }`; `AuditSink`; `RateLimiter` |
| `BotInvocation` | `Bot`, `RunAs` (membership), `Project`, `Input any`, `ContentType`, `Headers http.Header`, `EntryPoint = "webhook"`, `TraceID` |
| Thư viện | không thêm mới ở khung |
| Cấu hình | `FHIR_WEBHOOK_PUBLIC_ENABLED=false`; `FHIR_WEBHOOK_MAX_BODY` (mặc định 1 MiB khớp `maxJsonSize`); `FHIR_WEBHOOK_RATE_IP`, `FHIR_WEBHOOK_RATE_MEMBERSHIP`; `FHIR_WEBHOOK_TIMEOUT` (không trùng `FHIR_WEBHOOK_TIMEOUT` của Subscription gửi đi `config.go:170`: **đổi tên** thành `FHIR_INBOUND_WEBHOOK_*` để tránh nhầm với rest-hook gửi đi) |
| Tiền tố URL | chỉ `/webhook/{id}`; bỏ ba tiền tố còn lại (nếu có tiền tố project thì phải bằng project của membership, nếu không 404; thuộc quyết định chung về tiền tố) |

### W.7 Ca test (từ `webhook/*.test.ts`)

`webhook/routes.test.ts` (tích hợp; mỗi ca dựng Bot qua `/admin/projects/{id}/bot`, `$deploy`, PATCH: các thao tác này **bỏ**, thay bằng fixture trực tiếp trong `Directory`/`BotReader` giả và `BotExecutor` giả có hành vi "trả `event.input`, và `{input, authorization}` nếu header `x-test-return-event`", mô phỏng `cjsCode` ở dòng 13-18):

| # | Tên gốc | Dòng | Kỳ vọng | Go |
|---|---|---|---|---|
| 1 | Preserves parsed JSON by default ... at %s (x4 tiền tố) | 111-123 | POST JSON có ký tự Unicode/escape; thân phản hồi `{input: <JSON đã parse>}` | giữ **chỉ** `/webhook/`; ba tiền tố còn lại **bỏ** |
| 2 | Bot.rawBody selects raw or parsed JSON input | 125-139 | bot thường: `{value:1}` (`1.00` thành `1`); bot `rawBody:true`: chuỗi gốc `'{ "value": 1.00 }\n'` | giữ |
| 3 | Does not capture raw body for non-JSON webhooks | 141-151 | `text/plain` body `hello` + `Authorization: Bearer webhook-secret`: 200, `Content-Type` text, `{input:'hello', authorization:'Bearer webhook-secret'}` | giữ (Authorization được chuyển) |
| 4 | Falls back to parsed body when rawBody is enabled but content type is not JSON | 153-161 | bot `rawBody:true` + `x-www-form-urlencoded` `value=1.00`: `{input:{value:'1.00'}}` | giữ |
| 5 | Authenticated execute parses the body by default | 163-172 | `POST /fhir/R4/Bot/{id}/$execute` | **bỏ** (`Bot/$execute` ngoài phạm vi) |
| 6 | Missing invalid ID | 174-181 | UUID ngẫu nhiên: 404 | giữ |
| 7 | Non-bot project membership | 183-191 | membership của admin: 403, thân đúng `ProjectMembership must be for a Bot resource` | giữ |
| 8 | Success with default result | 193-200 | 200 | giữ |
| 9 | Success with OperationOutcome | 202-209 | gửi `allOk` dạng `application/fhir+json`: 200 | giữ |
| 10 | Response contains a body | 211-220 | JSON vào, JSON ra, `Content-Type` chứa `application/json` | giữ |
| 11 | Response as a binary with content type text/xml | 222-232 | Bot trả `Binary` (`contentType:'text/xml'`, `data` base64): 200, `Content-Type` `text/xml`, thân `<Response><Say>Hello, world!</Say></Response>` | giữ (qua `Respond`) |
| 12 | Bot without publicWebhook flag | 234-266 | 403, `Bot is not configured for public webhook access` | giữ |
| 13 | Bot without access policy | 268-312 | 403, `ProjectMembership must have an Access Policy` | giữ |

`webhook/handler.test.ts` (đơn vị, `executeBot` bị mock):

| # | Tên gốc | Dòng | Kỳ vọng | Go |
|---|---|---|---|---|
| 14 | Applies raw-body forwarding flag %s before runtime dispatch (x3: undefined, true, false) | 65-76 | `rawBody` true: `executeBot` nhận chuỗi `'{ "value": 1.00 }\n'`; còn lại `{value:1}`; 200 | giữ |
| 15 | Setting %s sends a large Lambda input only once (x3) | 78-89 | `buildLambdaPayload` không nhân đôi đầu vào 3 MiB | **bỏ** (AWS Lambda ngoài phạm vi); giữ phần "đầu vào 3 MiB vẫn chạy nếu `FHIR_WEBHOOK_MAX_BODY` cho phép" như ca giới hạn |
| 16 | Malformed JSON is rejected regardless of rawBody setting %s (x3) | 91-96 | `{invalid` với `application/json`: 400, `executeBot` **không** được gọi | giữ |

Ca thêm cho Go (bảo mật, không có ở Medplum): membership `active=false`; project `active=false`; bot của project khác không có liên kết thì 403; project liên kết có `exportedResourceType` không chứa `Bot` thì 403; id có ký tự lạ thì 404 không chạm kho; thân vượt giới hạn thì 413 và executor không được gọi; vượt hạn mức theo IP và theo membership thì 429; executor lỗi/timeout thì thân chung, log chi tiết chỉ ở máy chủ; không có executor (cờ bật) thì 503; cờ tắt thì 404; header `Authorization` tới executor nhưng không xuất hiện trong log; GET `/webhook/{id}` thì 405 (README nói hỗ trợ, code không); chữ ký tùy chọn đúng/sai/thiếu (khi bật); không rò log Bot trong thân lỗi; thông điệp 403 thân `text/plain` (Medplum dùng `res.send(string)`, nên `text/html` theo hành vi Express: chưa xác nhận bằng test; Go dùng `text/plain; charset=utf-8` và `X-Content-Type-Options: nosniff`).

### W.8 Bảo mật (kiểm soát bắt buộc có test)

| Rủi ro | Kiểm soát | Test |
|---|---|---|
| **Endpoint công khai chạy mã** | tắt mặc định; fail đóng khi thiếu executor; chỉ membership của Bot có `publicWebhook`; hạn mức IP và membership; thân và thời gian có trần; id không đoán được (và không liệt kê được) | các ca "thêm" ở W.7 |
| Chạy Bot ngoài project được phép | `CanProjectReadBot` + Bot đọc chỉ bằng id từ membership (không từ input người dùng) | ca chéo project, project liên kết, `exportedResourceType` |
| Giả mạo membership/đổi `runAs` | `runAs` luôn là membership tra bằng id trong URL; không đọc từ body, header hay query | ca body có `membership`/`runAs`/`project` bị bỏ qua |
| Rò dữ liệu giữa membership/project | executor chỉ nhận membership đã tra; token cấp cho Bot (khi có runtime) mang đúng `fhir_project` của membership | ca token Bot không đọc được project khác (khi có runtime) |
| Lộ log/lỗi của Bot cho người ẩn danh | thân lỗi chung | ca executor trả `logResult` chứa chuỗi bí mật |
| Membership/Bot ngừng hoạt động vẫn chạy | kiểm `active` | ca `active=false` |
| Lộ trạng thái cấu hình qua thông điệp 403/404 | chấp nhận (id ngẫu nhiên, thông điệp giữ để tương thích); hạn mức chống dò | ca hạn mức |
| XSS qua thân lỗi `text/html` | `text/plain` + `nosniff` | kiểm header |
| Thân khổng lồ, JSON hỏng | trần kích thước; 400 trước executor | ca 400/413 |
| Chữ ký giả (khi bật tùy chọn) | `hmac.Equal`, lấy từ raw body nguyên bản | ca sai/thiếu |
| SSRF | webhook không gọi ra ngoài; (executor `http` của `cds.md` mục 7 phải có chống SSRF riêng) | thuộc kế hoạch executor |

### W.9 Không port đợt này; điểm lệch chấp nhận được

Không port: `GET /webhook/{id}`; ba tiền tố URL phụ; header chữ ký "bắt buộc" của README; Bot runtime thật (`awslambda`, `vmcontext`, `fission`), `buildLambdaPayload`, `storeBotInput`, `Bot/$execute`, `$deploy`; `BotExecutionRequest` các trường streaming.
Lệch chấp nhận: kiểm `active`; thân lỗi chung thay `logResult`; hạn mức; trần body/thời gian; thân 403 `text/plain`; tắt mặc định; đổi tên biến cấu hình; cổng `Directory` tách kho.

### W.10 Rủi ro và câu hỏi mở

| # | Câu hỏi | Khuyến nghị |
|---|---|---|
| Q-W1 | Cách đọc `ProjectMembership` và `Project` từ `fhir-server` khi chúng thuộc kho `auth` (A đọc DB chỉ đọc, B API nội bộ, C đặt webhook trong `auth`) | A, qua cổng `Directory` |
| Q-W2 | Có làm webhook trước khi có executor thật (khung + fake) hay đợi quyết định H3/M1 | làm khung, không bật |
| Q-W3 | Chữ ký: giữ như Medplum (Bot tự kiểm) hay thêm kiểm tùy chọn HMAC-SHA256 `X-Signature` | giữ + tùy chọn tắt mặc định |
| Q-W4 | Thân lỗi cho người gọi ẩn danh: chung hay như Medplum (`logResult`) | chung |
| Q-W5 | Gộp `pkg/bots` với `pkg/cds` của `cds.md` (một cổng `BotExecutor`, một `Respond`) | gộp |
| Q-W6 | `runAs` dùng token nào khi có runtime: cần `auth` phát token dịch vụ cho membership của Bot (`getBotAccessToken` tạo `Login` `authMethod:'execute'`: `bots/utils.ts:359-389`); chưa có đường tương ứng ở `auth` P3 | thêm vào kế hoạch P3 khi chọn runtime |
| Q-W7 | Tiền tố `/projects/{id}/webhook` | bỏ |
| Q-W8 | Kiểu Go có `Project.exportedResourceType` không (chưa xác nhận) | kiểm ở W1 |

### W.11 Các bước làm

| Bước | Việc | Cỡ | Tiêu chí hoàn thành | Phụ thuộc | Trạng thái |
|---|---|---|---|---|---|
| W0 | Chốt Q-W1, Q-W3, Q-W4, Q-W5; tạo `pkg/bots` (cổng `BotExecutor`, kiểu, `fake`) thống nhất với `cds` | S | quyết định ghi vào README | quyết định | làm được ngay |
| W1 | `Resolve` (bước 2-9 W.5) + `Directory`/`BotReader` giả + `CanProjectReadBot`; test W.7 #6, #7, #12, #13 và ca chéo project, `active` | M | test xanh, thông điệp đúng từng chữ | W0 | **làm được trước runtime** |
| W2 | `InputFor`, đọc body (trần, JSON hỏng 400, raw body, `urlencoded`, `text/plain`); test #1-#4, #14, #16 với executor giả | S | test xanh | W0 | **làm được trước runtime** |
| W3 | `Respond` (port `sendBotResponse`, dùng chung với `cds` C2): số, `OperationOutcome`, `Binary` (`text/xml`), resource, lỗi chung; test #8-#11 | S đến M | test xanh | W0 | **làm được trước runtime** |
| W4 | Route công khai trong presentation, hạn mức, cờ tắt mặc định, 503 khi thiếu executor, `architecture_test.go`; e2e với executor giả | M | e2e xanh; cờ tắt thì 404 | W1-W3 | **làm được trước runtime** |
| W5 | Adapter `Directory`/`BotReader` thật (Q-W1), test với DB của `auth` ở chế độ chỉ đọc | M | e2e với dữ liệu seed của `auth` (CLI 7.0) | W4, quyết định Q-W1 | làm được trước runtime, cần `auth` đã cài đặt phần dữ liệu (P1 xong) |
| W6 | (tùy chọn) kiểm chữ ký HMAC `X-Signature` theo Bot | S đến M | test đúng/sai/thiếu | W4, nơi lưu secret (Q3 của `cds.md`) | làm được trước runtime |
| W7 | **Executor thật** (HTTP ngoài hoặc runtime khác): token `runAs`, secret, audit, tính năng `bots` | L | webhook chạy Bot thật end-to-end | **bị chặn**: quyết định H3/M1, auth P3 (token cho Bot), phương án A-D của `cds.md` | **bị chặn** |

Cỡ tổng: khung W0-W5 là M (4 đến 6 ngày); W7 là L trở lên và thuộc kế hoạch Bot/CDS.

---

## Phần C. Việc dùng chung và thứ tự đề xuất

| Việc | Thuộc | Ghi chú |
|---|---|---|
| `Principal.Membership` + claim `fhir_membership` | K0, M3, auth P3 | điều kiện chung cho `keyvalue` và `mcp`; thêm vào danh sách claim của `p3-oauth2-tokens.md` mục 4.3 |
| Tách guard JWT dùng lại cho route ngoài `/fhir/R4` | K3, M2, W4 (W4 không dùng, công khai) | một chỗ, có tham số vai trò tối thiểu |
| Gói rate limit | W4, M5 | `modules-survey.md` mục 7.4 đã đề xuất; làm tối thiểu bộ đếm theo IP trước khi bật webhook |
| Quyết định tiền tố URL | cả ba | giữ `/keyvalue/v1`, `/mcp`, `/webhook` không tiền tố đợt này |
| Sửa `docs/api-contract/others.md` (dòng 44-49, 56 ghi "không guard ở route") | K4, M5 | thực tế `keyvalue` và `mcp` có `authenticateRequest` ở router (`keyvalue/routes.ts:11`, `mcp/routes.ts:17`); `webhook` thật sự công khai |

Thứ tự: K0, K1-K4 (`keyvalue`), rồi M0-M5 (`mcp`, dùng lại K0 và guard), song song W0-W5 (khung `webhook`); W7 chờ quyết định runtime Bot.

## Phần D. Tóm tắt điểm lệch và lỗi phát hiện ở Medplum

1. Webhook không kiểm chữ ký; README sai ba chỗ (GET, header chữ ký, mã 400); nhánh GET là mã chết (`webhook/routes.ts:55,74`).
2. Webhook không kiểm `active` của membership/project; log của Bot lọt tới người gọi ẩn danh khi lỗi (`bots/utils.ts:162,537`); `Authorization` được chuyển nguyên cho Bot.
3. `keyvalue`: không TTL; kiểm giới hạn 10 mục không nguyên tử (`store.ts:43-50`); lỗi hạ tầng thành 400 lộ chi tiết.
4. `mcp`: `POST /mcp/sse` trả `202` cho phiên không tồn tại và cho người không phải chủ phiên (chỉ bị bỏ ở bên nhận); chỉ kiểm membership chứ không kiểm project; `fhir-request` tải lại URL với token người gọi (đã vá bằng kiểm origin/đường dẫn, GHSA-fjgc-c3pj-xx2c), chính tác giả ghi nên thay bằng router nội bộ (`server.ts:46-49`).
5. `docs/api-contract/others.md` ghi "không guard ở route" cho `keyvalue` và `mcp` (thực tế có guard ở router).
