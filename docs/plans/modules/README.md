# Kế hoạch các module mức B (nâng từ "chỉ có khảo sát")

Trạng thái: **bản nháp chờ duyệt, chưa có code.** Bốn tài liệu chi tiết do bốn agent viết song song từ source Medplum v5.2.18 và từ mã Go trong repo; tôi đã **tự kiểm lại hai phát hiện bảo mật nặng nhất** (mục 2, đánh dấu "đã kiểm") và một lỗi tài liệu của chính tôi; còn lại là lời của agent kèm `file:dòng`, **tôi chưa đọc lại từng dòng**. Mỗi file có phụ lục "chưa xác nhận" riêng.

| File | Phạm vi | Dòng |
|---|---|---|
| [email-storage.md](email-storage.md) | `email`, `storage`, và thiết kế chung `pkg/mailer`, `pkg/binarystore`, `pkg/safehttp` | 895 |
| [keyvalue-mcp-webhook.md](keyvalue-mcp-webhook.md) | `keyvalue`, `mcp`, `webhook` | 567 |
| [scim-ws-fhircast.md](scim-ws-fhircast.md) | `scim`, `ws`, `fhircast` | 672 |
| [fhir-parity-audit.md](fhir-parity-audit.md) | kiểm kê tương đương `fhir`: 204 mục, **45 CÓ, 35 MỘT PHẦN, 97 CHƯA, 27 KHÔNG ÁP DỤNG** (27 là đề xuất chờ duyệt), chia vào 17 gói công việc WP-1 đến WP-15 | 540 |

## 1. Chỗ đặt từng module (đề xuất của các kế hoạch)

| Module | Đặt ở | Lý do chính |
|---|---|---|
| `email` (`/email/v1/send`) | `apps/auth` | `Project`, secret SMTP và quyền admin nằm trong CSDL của `auth`; đợt đầu chưa hỗ trợ đính kèm `Binary/<id>` |
| `scim` | `apps/auth` | dữ liệu User/ProjectMembership; cần `InviteUser` (P7.4) |
| `storage` (`/storage`) và Binary thô | `fhir-server` | `Binary` nằm ở kho `fhir-server`; phải ngoài giới hạn body 16 MB |
| `keyvalue`, `mcp`, `webhook` | `fhir-server` (gói ở `pkg/`) | gói `pkg/keyvalue`, `pkg/mcpserver`, `pkg/bots` (dùng chung với `cds`) |
| `fhircast`, `ws` | `fhir-server` (gói `pkg/fhircast`, `pkg/wsgroup`) | `subscriptions-r4` đã có ở `pkg/fhirsub` |
| `cds` | `fhir-server` | xem `cds-dicom/` |
| `dicom` | `apps/dicomweb` (app riêng) | xem `cds-dicom/` |

## 2. Việc nên làm sớm, ngoài lộ trình tính năng

Ba mục đầu là **lỗi bảo mật hoặc sai lệch trong code đã cài**, không phải tính năng mới.

| # | Vấn đề | Trạng thái kiểm | Cỡ | Gói |
|---|---|---|---|---|
| 1 | **Scope SMART v2 bị gộp quyền quá rộng.** `pkg/fhirauth/grants.go:74-77` gộp `c`, `u`, `d` vào một bit Write và `r`, `s` vào một bit Read: một token chỉ có `system/Patient.c` (chỉ tạo) vẫn sửa và xóa được; `s` (chỉ tìm kiếm) đọc được bản ghi | **Đã sửa 2026-11-21** (xem mục 2.1) | S | WP-2a, xong |
| 2 | ~~**`subscriptions-r4` không kiểm quyền người nhận.**~~ **ĐÃ SỬA (2026-11-21), kiểm lúc phát.** Token binding mang ảnh chụp quyền (`fhirauth.Principal.MarshalSnapshot`); `Hub.notify` kiểm `CanReadResource` (chữ `r` + AccessPolicy) cho từng kết nối, bỏ qua kể cả thông báo xóa; access token hết hạn thì gỡ binding. Test: `pkg/fhirsub/ws_authz_test.go`, `pkg/fhirauth/snapshot_test.go`, e2e `TestWebSocketDeliveryIsAuthorizedPerRecipient`; đã kiểm đột biến (tắt kiểm thì test đỏ). Còn lại: ẩn trường (hidden fields) và quota bind theo user chưa làm | **Đã sửa và kiểm thử** | S | Q-WS-1 |
| 3 | **Tài liệu `fhir-server-go.md` sai** về `$validate` (ghi trả 200 với lỗi cấu trúc; thực tế **400**) | **Đã kiểm bằng server thật và đã sửa** | S | xong |
| 4 | `fhir-server` **chưa có `pkg/httpsec`** (CORS và header bảo mật), chưa có rate limit hay quota | đã biết từ trước, agent xác nhận | S đến M | WP-3 |
| 5 | Lệch hành vi REST có probe của agent: `PUT` không đổi vẫn tạo phiên bản mới; `DELETE` bỏ qua `If-Match`; `_count` của history bị bỏ qua; `identifier:of-type` trả bundle rỗng không báo lỗi; `GET /job/{id}/status` và `/bulkdata/.../status` trả 500/410 thay vì 200 hoặc 400/404; phản hồi lộ `meta.project` | lời agent, **chưa tự kiểm** | S mỗi mục | WP-1 |
| 6 | `apps/fhir-server/README.md` còn ghi chưa có `Group/$export`, `$subsumes`, `$translate`, scope theo loại và AccessPolicy trong khi đã cài; `go-port-status.md` còn câu "100/113 operation" | lời agent | S | tài liệu |
| 7 | Đính chính khảo sát: "đóng êm WebSocket 30 giây" **không đạt được ở Go** (`coder/websocket` đóng tối đa khoảng 5 giây mỗi kết nối; tổng bị chặn bởi `SHUTDOWN_TIMEOUT` 25 giây) | lời agent | | ghi nhận |

### 2.1 WP-2a đã làm: SMART v2 theo từng chữ

`pkg/fhirauth` tách quyền thành năm bit đúng với chữ SMART v2: `PermCreate` (c), `PermRead` (r: read, vread, history), `PermUpdate` (u: PUT, PATCH), `PermDelete` (d), `PermSearch` (s). Giữ `PermWrite` (cud), `PermAllRead` (rs) và `PermAll` (cruds) làm tập hợp, nên scope vai trò `fhir.read`/`fhir.write`/`fhir.admin` và dạng v1 `.read` (= rs), `.write` (= cud), `.*` (= cruds) giữ nguyên nghĩa. Ma trận thao tác ở `Repo`: create cần `c`; read, vread, history cần `r`; update (PUT) cần `u`; delete cần `d`; search (kể cả search hệ thống, `_type`, conditional) cần `s`; reindex cần `u`; expunge cần `d`. Hệ quả cần biết:
- **PATCH cần `r` và `u`** (đọc rồi mới sửa); PUT/PATCH/DELETE **có điều kiện** cần thêm `s`; update-as-create cần `c`.
- `s` (chỉ tìm kiếm) không còn đọc được theo id; `r` không còn tìm kiếm được. Thay đổi hành vi này làm client cũ chỉ có `.read` vẫn chạy (rs), nhưng client chỉ có `.r` mà dùng tìm kiếm sẽ bị 403.
- Resource kèm theo (`_include`, `_revinclude`) lọc theo `CanSee` (đọc hoặc tìm kiếm).
- **Job nền đã ký trước khi triển khai bản này bị từ chối một lần** ("ảnh chụp quyền phiên bản cũ", `sealVersion = 2`): bit cũ không được hiểu bằng nghĩa mới. Job chạy lại sau khi triển khai là bình thường.
- API Go đổi: `Grants.CanWrite` bị bỏ (mơ hồ), thay bằng `Can(rt, perm)`; `FullRead`/`Readable` đổi thành `FullSearch`/`Searchable`; thêm `CanSearch`, `CanSee`.
Kiểm chứng: test ma trận ở `pkg/fhirauth` (`smartv2_test.go`), test e2e qua HTTP với JWT thật (`apps/fhir-server/.../smartv2_e2e_test.go`, gồm batch), và một đột biến tạm (đưa lại hành vi gộp) làm hai test e2e đỏ đúng chỗ rồi đã khôi phục.

## 3. Phát hiện về Medplum cần quyết định có sửa không

Gom từ bốn tài liệu (các agent khuyến nghị sửa trong bản Go; cần bạn duyệt):

- **Webhook:** README sai (không hỗ trợ GET; **không bắt buộc** header chữ ký, vì handler và test đều không kiểm; mã lỗi là 403/404 chứ không phải 400). Bảo mật thực tế chỉ dựa vào `membershipId` khó đoán, và còn: không kiểm `active` của membership/project, log của Bot lọt tới người gọi ẩn danh khi lỗi, `Authorization` chuyển nguyên cho Bot.
- **SCIM:** admin đọc, tắt, xóa được membership của Bot, ClientApplication, chủ project và chính mình (không chặn theo loại profile); không phân trang; filter lạ trả toàn bộ user; `userName` không cập nhật được.
- **FHIRcast:** ngữ cảnh lưu Redis không có TTL (dữ liệu bệnh nhân sống vô hạn); không đòi quyền theo topic (mọi member trong project đọc/ghi mọi topic); không có callback nên **không có bề mặt SSRF** (kế hoạch cấm `pkg/fhircast` dùng HTTP client).
- **Email và storage:** log subject (thư MFA chứa mã); log `originalUrl` kèm `Signature` còn hạn 1 giờ; `/storage` bỏ qua `versionId` trong đường dẫn (URL ký cũ phục vụ bản mới, PUT lên URL cũ ghi đè bản mới); Binary mồ côi khi bị chặn; kiểm loại tệp không bỏ tham số (`application/x-sh; charset=x` lọt); đính kèm URL và giải nén không có giới hạn kích thước; project có SMTP riêng vẫn bị 400 nếu server không cấu hình.
- **MCP:** `POST /mcp/sse` trả 202 cho phiên không tồn tại; chỉ kiểm membership, không kiểm project.
- **KeyValue:** không TTL; giới hạn 10 mục không nguyên tử (Go dùng script Lua); độ dài đếm theo UTF-16.

## 4. Điểm chặn và phụ thuộc xuyên kế hoạch

| Điều | Ảnh hưởng | Cần |
|---|---|---|
| **`Principal` không có membership** (`pkg/fhirauth/principal.go:37-49`); `auth` P3 chưa phát claim membership | `keyvalue` và kiểm SSE của `mcp` cô lập theo membership; `webhook` cần `runAs` | Thêm claim `fhir_membership` vào kế hoạch P3 và `Principal.Membership` (Q-K1); fail đóng khi thiếu |
| Okta đăng nhập SCIM bằng **Basic** (client id, secret) | `auth` P3 ghi không port Basic; Q14 của P7 chưa chốt | Quyết định Q-SC-1 |
| `pkg/messaging` không hợp cho kênh động theo topic hoặc phiên SSE (Redis Streams, topic cố định đăng ký trước `Start`, không hủy được) | `mcp` SSE, `fhircast` | Cổng `SessionBus` dùng Pub/Sub của go-redis (miniredis v2.40 có hỗ trợ không: chưa xác nhận) |
| Runtime Bot (M1) | `webhook`, `cds`, `WP-12`, `WP-11` | Quyết định M1; `webhook` làm được trước các phần xác thực, tra membership, kiểm quyền, giao diện `BotExecutor` |
| Kho `auth` (Project, ProjectMembership) tách khỏi kho `fhir-server` (Bot) | `webhook` cần cả hai | Quyết định Q-W1 (ranh giới) |
| `fhir-request` của MCP phải gọi **qua đúng chuỗi guard HTTP** (`RequiredRole` chỉ nằm ở guard của `fhir-server`, `auth.go:96-118`), không gọi thẳng `Router.Handle` | `mcp` | thiết kế đã ghi |
| Hồ sơ lâm sàng ở kho nào (D-10) | `scim` create, `/auth/me` | cổng `ProfileWriter` gọi HTTP sang `fhir-server` (đề xuất) |
| `architecture_test.go` cần thêm luật cho các gói mới (`pkg/mailer`, `pkg/binarystore`, `pkg/safehttp`, `pkg/keyvalue`, `pkg/mcpserver`, `pkg/bots`, `pkg/wsgroup`, `pkg/fhircast`) | mọi kế hoạch | làm cùng từng gói |

## 5. Gói dùng chung cần dựng trước

| Gói | Dùng cho | Ghi chú |
|---|---|---|
| `pkg/safehttp` (trích từ `pkg/fhirsub/ssrf.go`) | email (đính kèm URL), cds executor, `auth` P6 | bản hiện tại thiếu dải `198.18.0.0/15` và các dải IANA |
| `pkg/mailer` | `auth` P4/P5 (thư đặt lại, xác minh, MFA), email | **một bản**; `auth` 4.2 chờ nó |
| `pkg/binarystore` | storage, DICOM, Binary | hệ thống tệp trước; S3 sau (kho cục bộ không chạy nhiều replica) |
| `Mutate` đọc-sửa-ghi có điều kiện | DICOM, fhirsub, auth | chuyển bản của `auth` lên gói chung |
| Cổng `SessionBus` (Redis Pub/Sub) | mcp, fhircast | |

## 6. Thứ tự đề xuất (tổng hợp)

1. **WP-2a** (sửa SMART v2) và **mục 2.2** (kiểm quyền người nhận `subscriptions-r4`): lỗi bảo mật, nhỏ đến vừa.
2. Gói dùng chung ở mục 5, ưu tiên `pkg/safehttp`, `pkg/mailer`, `pkg/binarystore`.
3. WP-3 (vận hành trước khi mở Internet: `httpsec` cho `fhir-server`, rate limit) cùng WP-1 (sửa lệch REST).
4. `keyvalue`, rồi `email`, rồi `mcp` (cần claim membership từ `auth` P3).
5. `scim` cùng đợt quản trị project của `auth` (P7).
6. Phần chặn bởi quyết định: Bot (WP-12), thanh toán (WP-11), Agent (WP-13), AI (WP-14), Storage ngoài (WP-4, M5).

Hầu hết cần `auth` P3 (token) để chạy thật.

## 7. Câu hỏi mở nổi bật

Danh sách đầy đủ ở mục "Câu hỏi mở" của từng file. Quyết định sớm nhất cho cả nhóm:

| # | Câu hỏi | Ở đâu |
|---|---|---|
| Q-F1, Q-F2 | Mức tương thích với client Medplum; có dùng app web Medplum không | audit |
| Q-F7 | Duyệt sửa SMART v2 ngay | audit |
| Q-F21, Q-F22 | `PUT` không đổi có ghi phiên bản không; `:of-type` trả 400 khi chưa cài | audit |
| Q-WS-1 | ~~Chặn phát hành `subscriptions-r4` đến khi có kiểm quyền người nhận không~~ Đã làm (kiểm lúc phát) | scim-ws-fhircast |
| Q-K1 | Claim membership và `Principal.Membership` | keyvalue-mcp-webhook |
| Q-SC-1 | Basic cho SCIM | scim-ws-fhircast |
| Q-E1, Q-E2 | Chỗ đặt route email; hỗ trợ đính kèm `Binary/<id>` | email-storage |
| Q-S2 | Tương thích dữ liệu và URL với Medplum đang chạy | email-storage |
| M1 đến M5 | Runtime Bot, chuyển dữ liệu, `agent`/`ccda`/`hl7`, AI, kho đối tượng | [../coverage-matrix.md](../coverage-matrix.md) |
