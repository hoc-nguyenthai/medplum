# Kế hoạch: tách `apps/fhir-server` thành các service, mỗi service một vai trò

Trạng thái: **đề xuất; hướng tách đã chốt theo yêu cầu của người dùng (2026-11-24): các service riêng, mỗi service một vai trò, không dùng vai trò `FHIR_ROLE` trong cùng image**. Tiến độ: **S0, S1, S2, S3 và S4 xong (2026-11-24)**; S5 đang làm. Nguyên tắc chung: [../service-principles.md](../service-principles.md) (điều kiện A/B/C/D, danh sách kiểm service mới mục 7). Bổ sung cho [microservices-shared-db.md](microservices-shared-db.md) mục 14 (P3a: điều tra `fhir-worker`, D-DB-3 đã chốt "chưa tách, chờ đo tải") và [modules/split-scim-ws-services.md](modules/split-scim-ws-services.md) (đã tách `ws-gateway`).

> **Thay đổi so với bản trước của tài liệu này:** bản trước đề xuất bậc 2 của thang tách (cùng binary, vai trò `FHIR_ROLE`). Theo chỉ đạo, kế hoạch nay đi thẳng bậc 3 (`apps/<tên>` riêng, image riêng). Đây là **lệch có chủ ý** khỏi mục 1 của nguyên tắc ("chỉ lên bậc 3 khi bậc 2 không đủ"); hệ quả về chi phí ở mục 6 và 8.

**Phạm vi bằng chứng:** đã đọc mã lắp ráp (`internal/bootstrap/container.go`), `pkg/fhirrouter`, `pkg/fhirsub`, `pkg/fhirbinary`, `pkg/messaging`, `pkg/fhirdownload`, các kế hoạch hiện có. **Số đo tải còn rất hạn chế** (mục 9): chỉ có một lần đo #12c (export song song với API, 20.000 resource, một máy) cho `fhir-worker`; chưa có số đo nào cho `fhir-downloader`, `fhir-notifier`, Binary hay terminology, nên nhận định "đáng tách" của các cụm đó là suy luận từ mã. Câu ghi "(chưa kiểm)" là chưa chạy hoặc chưa đọc tới mã.

## 0. Kết luận

| Service mới | Thư mục | Cổng (đề xuất) | Vai trò | Điều kiện tách | Gắn kết | Thứ tự |
|---|---|---|---|---|---|---|
| **`fhir-downloader`** | `apps/fhir-downloader` | `:8009` (chỉ probe) | tự tải attachment URL ngoài về kho Binary (`pkg/fhirdownload`) | **C mạnh** (bề mặt SSRF, ra Internet), A | thấp | 1 |
| **`fhir-notifier`** | `apps/fhir-notifier` | `:8010` (chỉ probe) | giao rest-hook, khớp sự kiện cho websocket (`fhirsub.Manager`) | C, A | trung bình | 2 (kèm làm rest-hook bền) |
| **`fhir-worker`** | `apps/fhir-worker` | `:8011` (chỉ probe) | job nền: bulk export, `$reindex`, batch async, vòng bảo trì job | C một phần; A **chưa xác nhận** (đo #12c: API không bị ảnh hưởng ở 20.000 resource, xem 3.3) | trung bình đến thấp | 3, **ưu tiên thấp nhất** (sau P0-2, P0-3) |
| **`fhir-server` = API** (giữ nguyên tên, xem 3.0) | `apps/fhir-server` | `:8005` | **API FHIR**: REST, tìm kiếm, xác thực, terminology, Binary, GraphQL; sở hữu migration; **phát** sự kiện; nhận yêu cầu tạo job | lõi | | không đổi |

**Không tách:** terminology (nằm trong đường tìm kiếm), Binary và `/storage` (chưa), REST, kho, tìm kiếm, xác thực (không tách theo loại resource). Websocket đã là `ws-gateway`.

Điểm chung: **không service nào đạt điều kiện B** (dữ liệu hoặc bí mật riêng): cùng schema `fhir`; vì bảng của `fhirstore` là kho tài liệu chung (`fhir_resource` chứa mọi loại), **không thể cấp quyền DB hẹp theo loại resource**, nên chia role DB không thu hẹp được gì. Lợi ích thật của việc tách là: (1) **chính sách mạng** (chỉ `fhir-downloader` ra Internet; chỉ `fhir-notifier` gọi webhook), (2) **tách tải và miền lỗi**, (3) **tách bí mật** (`fhir-notifier` và `fhir-downloader` không cần `FHIR_JOB_SIGNING_KEY` hay `FHIR_WS_SIGNING_KEY` của job). Phải ghi trung thực như vậy ở mục "Vì sao tách" của từng service (DoD mục 7.1).

## 0.1 Thống kê đề xuất

Số liệu lấy từ tài liệu này và đếm trực tiếp trong mã ngày 2026-11-24 (quy mô mã đếm bỏ dòng trống và dòng chú thích). **Không phải số đo tải.** Số đo tải duy nhất là #12c (mục 3.3), chỉ cho `fhir-worker`: chưa có số đo cho điều kiện A của service nào khác.

**Số lượng**

| Chỉ tiêu | Số |
|---|---|
| Service hiện có | 4 (`auth`, `fhir-server`, `ws-gateway`, `dicomweb`) cộng công cụ `fhir-cli` |
| Service đề xuất tách mới | 3 (`fhir-downloader`, `fhir-notifier`, `fhir-worker`) |
| Tổng sau khi tách | 7 service |
| Cụm đã đánh giá | 8: 3 tách; 2 không tách (Binary và `/storage`, terminology); 1 đã tách (websocket); 1 lõi; 1 không phải ứng viên |
| Điều kiện tiên quyết (P0) | 5 |
| Quyết định (D-SP) | 8: D-SP-1 đã chốt; D-SP-7 khớp D-DB-3 đã chốt; 6 còn mở |
| Bước (S0 đến S6) | 7 |

**Theo service**

| | `fhir-downloader` | `fhir-notifier` | `fhir-worker` |
|---|---|---|---|
| Vai trò | tự tải attachment URL ngoài | giao rest-hook, khớp sự kiện websocket | job nền: export, `$reindex`, batch async |
| Thư mục, cổng (đề xuất) | `apps/fhir-downloader`, `:8009` | `apps/fhir-notifier`, `:8010` | `apps/fhir-worker`, `:8011` |
| Điều kiện tách | C mạnh, A | C, A | C một phần, A chưa xác nhận (đo #12c) |
| Mức gắn kết | thấp | trung bình | trung bình đến thấp |
| Thứ tự | 1 | 2 | 3 (có thể hoãn; #12c chưa cho thấy API bị ảnh hưởng) |
| Mã dùng lại (dòng, không tính test) | khoảng 395 (`pkg/fhirdownload`) | khoảng 1.637 (`pkg/fhirsub`, gồm Hub và token websocket) | khoảng 1.267 (`jobs*`, `bulk`, `batch_async`) cộng 98 (`pkg/workflow`) |
| Test hiện có của phần mã đó (dòng) | 409 | 2.172 | chưa đếm |
| Biến cấu hình liên quan | 6 (`FHIR_AUTO_DOWNLOAD*`) | khoảng 17 (webhook, Subscription, broker, websocket) | khoảng 8 (job, export retention, batch async) |
| Redis | bắt buộc | bắt buộc | không bắt buộc (nhận job qua DB) |
| Bí mật cần | không cần `FHIR_JOB_SIGNING_KEY`, JWKS | `FHIR_WS_SIGNING_KEY` nếu có websocket | `FHIR_JOB_SIGNING_KEY` dùng chung |
| Ra Internet | có (chỉ service này) | có (gọi webhook) | không |
| Dữ liệu | schema `fhir`, role `fhir_app` | như trái | như trái |
| Điều kiện tiên quyết | P0-5 | P0-1, P0-4 | P0-2, P0-3 |
| Cỡ thực hiện | M | L | L |

**Khối lượng việc theo cỡ**

| Cỡ | Việc |
|---|---|
| S | P0-3, P0-4, P0-5; S0, S6 |
| S đến M | S4 (đã xong một phần) |
| M | P0-1, P0-2; S1, S2 |
| L | S3 (`fhir-notifier`), S5 (`fhir-worker`) |

Mỗi service mới phải thỏa danh sách kiểm 10 mục của [service-principles.md](../service-principles.md) mục 7. Cách đọc số: số dòng của `fhir-notifier` gồm cả phần websocket nhúng nên phần thật sự chuyển sang service mới nhỏ hơn con số trên; số biến cấu hình là ước lượng theo tiền tố tên biến trong `config.md` (khoảng 87 biến tổng, một số dùng chung giữa các cụm); `pkg/fhirdownload` đã commit (`b709f7e`), số dòng đếm trên bản đã commit.

## 0.2 Thống kê toàn hệ thống (hiện có và đề xuất)

Đếm trực tiếp trong mã ngày 2026-11-24. Mã tính bỏ dòng trống, dòng chú thích, `docs.go` (sinh tự động) và `*_gen.go`; test của `e2e/` ở gốc repo chưa tính. **Các dòng của service đề xuất là ước lượng**, suy từ `ws-gateway` (1.027 dòng) và `dicomweb` (1.380 dòng) cho phần vỏ; chưa kiểm cho các service mới.

**Bảng A: tất cả service**

| Service | Trạng thái | Cổng | Dữ liệu | Redis | Route | Biến cấu hình | Mã (dòng) | Test (dòng, tệp) |
|---|---|---|---|---|---|---|---|---|
| `auth` | có | 8006 | schema `identity`, role `identity_app` và `identity_migrate` | bắt buộc ở production | 63 | 106 | 13.887 | 20.563 (88) |
| `fhir-server` (API) | có | 8005 | schema `fhir`, role `fhir_app` và `fhir_migrate`, sở hữu migration | tùy chọn (cần khi Subscription nhiều replica hoặc websocket `issuer`) | 31 | 87 | 1.910 | 3.908 (21) |
| `ws-gateway` | có | 8007 | không có DB | bắt buộc | 18 | 37 | 1.027 | 2.584 (14) |
| `dicomweb` | có | 8008 | dùng chung schema `fhir`, không tự migrate | không | 23 | 39 | 1.380 | 3.460 (17) |
| `fhir-cli` | có (công cụ) | không | không | không | 0 | 0 | 754 | 361 (2) |
| `fhir-downloader` | **đề xuất** | 8009 | dùng chung schema `fhir` | **bắt buộc** | 0 (chỉ probe) | ước 6 đến 10 | ước 1.000 đến 1.500 phần vỏ, cộng dùng lại khoảng 395 từ `pkg/fhirdownload` | chưa có |
| `fhir-notifier` | **đề xuất** | 8010 | dùng chung schema `fhir` | **bắt buộc** | 0 (chỉ probe) | ước 17 đến 22 | ước 1.000 đến 1.500 phần vỏ, cộng dùng lại khoảng 1.637 từ `pkg/fhirsub` | chưa có |
| `fhir-worker` | **đề xuất** | 8011 | dùng chung schema `fhir` | không bắt buộc | 0 (chỉ probe) | ước 8 đến 12 | ước 1.000 đến 1.500 phần vỏ, cộng dùng lại khoảng 1.365 (`jobs*`, `bulk`, `batch_async`, `workflow`) | chưa có |

**Bảng B: bố cục kiểm thử và tài liệu hiện có**

| Service | Dockerfile riêng | `tests/` | Tài liệu nghiệp vụ | ERD | Hợp đồng API chi tiết (`cases/`) |
|---|---|---|---|---|---|
| `auth` | có | `e2e` | có | có | có |
| `fhir-server` | có | `e2e` | có | không | không |
| `ws-gateway` | có | `e2e`, `integration`, `load` | không | không | không |
| `dicomweb` | có | `load` (cộng e2e ở gốc repo) | có | không | có |
| `fhir-cli` | không (công cụ) | không | có | không | không |
| 3 service đề xuất | phải có (DoD mục 7) | phải có bốn tầng | phải có | nếu đổi dữ liệu | không áp dụng (không có route nghiệp vụ) |

**Bảng C: tổng cộng**

| Chỉ tiêu | Hiện tại (4 service và 1 công cụ) | Sau khi tách (7 service và 1 công cụ) |
|---|---|---|
| Số service | 4 | 7 |
| Route nghiệp vụ | 135 (63 + 31 + 18 + 23) | 135 (ba service mới chỉ có probe) |
| Biến cấu hình (có trùng giữa service) | 269 (106 + 87 + 37 + 39) | khoảng 300 trở lên (biến dùng chung lặp lại ở mỗi service) |
| Mã ở `apps/` | 18.958 | thêm khoảng 3.000 đến 4.500 dòng vỏ (ước) |
| Test ở `apps/` | 30.876 | tăng theo bốn tầng |
| Mã dùng chung `pkg/` | 51.272 mã, 21.963 test | gần như không đổi (ba service mới dùng lại mã đã có) |
| Service có Redis bắt buộc | 2 (`auth`, `ws-gateway`) | 4 (thêm `fhir-downloader`, `fhir-notifier`) |
| Service dùng chung schema `fhir` | 2 (`fhir-server`, `dicomweb`) | 5 (thêm ba service mới; rủi ro ở D-SP-8) |

**Nhận xét từ số liệu**

- `auth` chiếm 73% mã ở `apps/` (13.887 trên 18.958); `fhir-server` chỉ 1.910 dòng vì là vỏ mỏng, nghiệp vụ nằm ở `pkg/` (hơn 51.000 dòng).
- Tỉ lệ test trên mã ở `apps/`: `ws-gateway` và `dicomweb` khoảng 2,5; `fhir-server` 2,0; `auth` 1,5; `fhir-cli` 0,5 (và `cmd/fhir-cli` không có test).
- Chỉ `ws-gateway` có đủ bốn tầng trong `tests/`; ba service đề xuất phải theo khuôn đó.
- Số biến cấu hình lấy từ `docs/config.md` của từng app (dòng bắt đầu bằng biến viết hoa); ước lượng cho service đề xuất theo số biến liên quan đã đếm ở mục 0.1.

## 1. Hiện trạng (bằng chứng)

`fhir-server` là một tiến trình lắp ráp các thành phần sau trong `container.go` (`build`); thành phần có điều kiện bật bằng cờ:

| Thành phần | Bật khi | Cách nối | Bằng chứng |
|---|---|---|---|
| Kho `fhirstore` và router REST | luôn | `fhirrouter.New(routerRepo, Options{...})` | `container.go` |
| Xác thực, tách project | luôn (JWT hoặc token tĩnh) | `fhirauth.Scope` bọc kho; job nền dựng lại `Principal` từ `AsyncJob.Meta.Project` | `container.go` (`scopedJobRepo`, `jobContext`) |
| Job nền | luôn | `NewOrchestrator` luôn trả `workflow.NewInline(run, FHIR_JOB_CONCURRENCY)`; adapter `asynq` có trong `pkg/workflow` nhưng không chỗ nào ngoài test gọi | `container.go`; P3a |
| Broker sự kiện | `FHIR_SUBSCRIPTIONS` **hoặc** `FHIR_AUTO_DOWNLOAD` | `messaging.New`: `memory` hoặc `redis` (Redis Streams, consumer group `trivita`, at-least-once, ACK, poison message sang `{stream}:dead`) | `container.go`; `pkg/messaging/redis.go` |
| Phát sự kiện sau commit | broker có | `fhirsub.Wrap(store, broker)` bọc kho | `container.go` |
| Giao rest-hook | `FHIR_SUBSCRIPTIONS` | `fhirsub.NewManager(store, ...)` đọc Subscription từ **kho gốc**; hàng đợi giao là `chan` trong tiến trình, **sức chứa 1024, đầy thì bỏ (`Dropped`)**, 4 worker mặc định, thử lại `FHIR_WEBHOOK_MAX_ATTEMPTS` (5) bằng hẹn giờ trong bộ nhớ | `pkg/fhirsub/manager.go:106,116,366`; `config.md` |
| Khớp sự kiện websocket | `FHIR_WS_MODE` | `embedded`: Hub trong tiến trình; `issuer`: `Manager.AttachRemote` (khớp một lần cho cả cụm, đẩy lên kênh Redis; cần `BROKER_DRIVER=redis`) | `container.go` |
| Binary thô | `FHIR_BINARY_STORAGE` | `fhirbinary.Wrap(store, blobs, ...)` **bọc ghi Binary, nằm dưới sự kiện Subscription**; `RawBinary` bọc cả router FHIR; `/storage` là handler riêng đọc kho gốc | `container.go` |
| Tự tải attachment | `FHIR_AUTO_DOWNLOAD` (cần `FHIR_BINARY_STORAGE`) | `fhirdownload.New(...)`; `Register(broker)` nghe `fhirsub.EventType`, tải URL ngoài qua `pkg/safehttp`, ghi `Binary` rồi đổi `url` attachment thành `Binary/<id>`; `Async` khi broker là `memory` (hàng đợi trong tiến trình, thử lại 3 lần lùi 1 giây, 2 giây) | `container.go`; `pkg/fhirdownload/download.go` (đã commit ở `b709f7e`; 9 test trong `download_test.go`) |
| Terminology | luôn | `resolveTerminology` nằm **trong đường tìm kiếm** (`:below`, `:above`, `:in`); `$expand`, `$lookup`... dùng bảng dẫn xuất `fhir_coding*` cùng schema | `pkg/fhirrouter/chain.go:37`, `termsearch.go` |
| Kết quả bulk export | luôn | `Binary` base64 **trong CSDL** (không vào kho `file:`/`s3:` dù đã bật), mỗi tệp khoảng 8 MiB | `pkg/fhirrouter/bulk.go:178`; `container.go` |

## 2. Đối chiếu tiêu chí tách

| Service | A. Hồ sơ tải khác | B. Dữ liệu/bí mật riêng | C. Miền lỗi, vòng đời deploy | D. Đội khác |
|---|---|---|---|---|
| `fhir-downloader` | Có thể: tải tệp lớn, I/O ra ngoài (chưa đo) | không (cùng schema; cần ghi `Binary` và đọc kho) | **Có**: API không nên có đường ra Internet; sự cố máy chủ ngoài không ảnh hưởng API | không |
| `fhir-notifier` | Có thể: tải theo số sự kiện × số Subscription, chờ I/O ngoài (chưa đo) | không (nhưng giữ `subscription-secret` và `channel.header`: tách khỏi API) | **Có một phần**: webhook chậm chiếm worker và bộ nhớ của API; restart API mất hàng đợi | không |
| `fhir-worker` | **chưa xác nhận**: đo #12c ở 20.000 resource cho p95 API ×1,04 khi export song song | không | Một phần: chưa quan sát API bị chậm ở cỡ đã đo; chưa đo cỡ lớn hơn | không |

## 3. Từng service

### 3.0 `fhir-server`: vai trò API (giữ nguyên tên và thư mục)

`fhir-server` **là service API** trong mô hình mới; không đổi tên thành `fhir-api` (đổi tên tốn công: import `trivita-fhir/apps/fhir-server/...`, Dockerfile, compose, `FHIR_*` trong mọi tài liệu và e2e; chưa có lý do). Nó vẫn là lõi, **sở hữu schema `fhir` và migration**, và là service duy nhất nhận yêu cầu của người dùng.

| | Nội dung |
|---|---|
| **Giữ** | REST FHIR (create, read, vread, update, patch, delete, history, search, batch, transaction), GraphQL, `metadata`, `$validate`, `$everything`, terminology (`$lookup`, `$expand`, `$subsumes`, `$translate`, `$import`, `$install`), Binary thô và `/storage`, xác thực và phân quyền (JWT/JWKS, token tĩnh, scope SMART, AccessPolicy, tách project, super-admin), cấp token bind websocket (`$get-ws-binding-token`), lệnh `migrate`, `assign-project`, `sweep-binary`, `healthcheck`, probe `/healthz` và `/readyz` |
| **Vẫn phát** | Sự kiện sau commit lên broker Redis (`fhirsub.Wrap`), để `fhir-notifier` và `fhir-downloader` nghe |
| **Không còn làm khi các service kia được bật** | Giao rest-hook và khớp sự kiện websocket (sang `fhir-notifier`); tự tải attachment (sang `fhir-downloader`); thực thi job nền export, `$reindex` cả loại, batch async (sang `fhir-worker`). API vẫn **nhận yêu cầu** và tạo `AsyncJob`, rồi trả `202` |
| **Việc cần đổi (S1)** | Công tắc tắt phía nghe và giao (rest-hook, tự tải), tùy chọn để API không tự chạy job; mặc định như cũ nên khi chưa tách thì không có gì khác |
| **Scale theo** | số yêu cầu đọc, ghi, tìm kiếm |
| **Không đổi** | Cổng `:8005`, đường dẫn `/fhir/R4`, hợp đồng API, tên biến cấu hình, hợp đồng dây với `auth` (JWKS) và `ws-gateway` (token bind, kênh Redis) |

Tách thành công nghĩa là: một lần ghi hồ sơ trả lời nhanh (`201`), còn việc giao webhook, tải tệp ngoài, chạy export diễn ra ở service khác.

### 3.1 `fhir-downloader` (ưu tiên số 1)

- **Vì sao tách:** tải URL do người dùng cung cấp là bề mặt SSRF và chi phí; mặc định đang tắt vì vậy. Service riêng cho phép **API chạy không có quyền ra Internet** (chính sách mạng egress chỉ mở cho service này). Gắn kết thấp: chỉ nghe sự kiện từ broker, đọc resource, ghi `Binary` và cập nhật resource bằng đúng project của resource.
- **Dữ liệu:** dùng chung schema `fhir`, role `fhir_app`, và kho Binary (tiền lệ `dicomweb`, D-DW-1: hai cửa vào của cùng một miền dữ liệu); không tự migrate, từ chối khởi động khi `fhirstore.Pending > 0`.
- **Phụ thuộc chạy:** PostgreSQL, **Redis (bắt buộc, broker `redis`)**, kho Binary, egress Internet. **Không cần:** `FHIR_JOB_SIGNING_KEY`, `FHIR_WS_SIGNING_KEY`, JWKS của `auth`, không phục vụ token người dùng.
- **Scale theo:** số sự kiện ghi có attachment ngoài, và thời gian tải.
- **Độ bền:** thừa hưởng broker Redis (consumer group, at-least-once, thử lại, dead-letter); cần kiểm idempotency khi nhận lại cùng sự kiện (chưa kiểm trong `download.go`).
- **Việc cần có ở `fhir-server`:** `fhir-server` **vẫn phát** sự kiện (`fhirsub.Wrap`, cần broker) nhưng **không** đăng ký worker tải: một công tắc tắt phía nghe ở API (tên chốt ở S1; mặc định giữ hành vi hiện tại để không đổi gì khi chưa tách).

### 3.2 `fhir-notifier` (ưu tiên số 2)

- **Vì sao tách:** webhook chậm hoặc chết chiếm worker và hàng đợi 1024 trong bộ nhớ của API; khớp `criteria` mỗi sự kiện tốn CPU theo số Subscription. Về mã, bên nghe **có thể ở tiến trình khác** khi dùng Redis: `fhirsub.Wrap` đã phát sự kiện sau commit và Manager đọc Subscription từ kho gốc.
- **Điểm yếu service riêng không sửa hộ:** hàng đợi giao nằm trong bộ nhớ, restart là mất thông báo đang chờ và đang hẹn thử lại; đầy thì bỏ (P3a). Phải **làm bền cùng lúc** (P0-1), nếu không chỉ chuyển nơi mất dữ liệu.
- **Cách làm bền (đề xuất, chưa thử):** (a) giao **đồng bộ trong handler của Redis Stream**, chỉ ACK khi giao xong hoặc bỏ cuộc theo `MaxAttempts` (dùng PEL, `ClaimInterval`, dead-letter có sẵn); (b) bảng outbox trong schema `fhir`; (c) `asynq`. (a) rẻ nhất vì không thêm hạ tầng; đổi lại một webhook chậm giữ một message PEL lâu.
- **Phần `ws`:** `AttachRemote` (khớp một lần cho cả cụm, đẩy lên kênh) chuyển sang `fhir-notifier`. Cấp token bind (`$get-ws-binding-token`) **vẫn ở `fhir-server`** vì cần kiểm quyền người gọi theo kho đã tách project.
- **Dữ liệu:** schema `fhir`, role `fhir_app`; cần đọc Subscription và cập nhật `status=off` khi tự tắt (ghi có `If-Match`).
- **Phụ thuộc chạy:** PostgreSQL, **Redis (bắt buộc)**, egress tới endpoint webhook (qua `pkg/safehttp`), `FHIR_WS_SIGNING_KEY` nếu còn websocket. **Không cần:** JWKS, `FHIR_JOB_SIGNING_KEY`.
- **Việc cần có ở `fhir-server`:** công tắc tắt phía giao và khớp (như 3.1).

### 3.3 `fhir-worker` (ưu tiên số 3)

- **Trạng thái:** D-DB-3 đã chốt "chưa tách, chờ đo". Kết luận P3a còn đúng: job `AsyncJob` đã bền nhờ lease trong DB (nhận bằng If-Match, gia hạn, tiếp quản sau `JobRecoveryGrace` và lease hết hạn), nên **thiếu động lực kỹ thuật cấp bách**.
- **Kết quả đo #12c (một lần, 2026-11-24; theo `task-list.md`):** 20.000 resource (4.000 Patient và 16.000 Observation), 16 client, `FHIR_JOB_CONCURRENCY=2`, PostgreSQL 16 trong Docker, server, PG và bộ tạo tải chung một máy phát triển. Khi export chạy song song với tải API: p95 API ×1,04, p99 ×1,01, thông lượng ×0,98 so với đường cơ sở không có export; export mất 6,5 giây khi song song so với 3,6 giây một mình (export nhường CPU, API không chậm); RSS server đỉnh từ 73 MB lên 220 MB (tăng khoảng 150 MB). Ngưỡng "bị ảnh hưởng" đã dùng: p95 hoặc p99 > 1,5× đường cơ sở. **Kết luận tạm của #12c: chưa cần mở `fhir-worker`.**
- **Kết quả đo S4 (2026-11-24, hai lần, cùng máy: server, PostgreSQL 16 trong Docker/OrbStack 6 CPU và bộ tạo tải chung một máy phát triển; công cụ `make bench-export`, thêm `EXPORT_LOAD_EXPORTS`):**

  | Lần | Dữ liệu | Job đồng thời / số export ở pha C | p95 API | p99 API | Thông lượng API | Export (một mình, song song) | RSS server đỉnh |
  |---|---|---|---|---|---|---|---|
  | 1 | 200.000 resource (40.000 Patient, 160.000 Observation), 16 client | 2 / 1 | 192,9 ms, 196,4 ms (x1,02) | 238,2 ms, 243,6 ms (x1,02) | 382, 369 req/s (x0,97) | 4 phút 4 giây, 18 phút 47 giây | 69 MB, 285 MB |
  | 2 | 50.000 resource (10.000 Patient, 40.000 Observation), 16 client | 4 / 4 | 37,2 ms, 50,3 ms (x1,35) | 47,9 ms, 71,0 ms (x1,48) | 1.570, 1.192 req/s (x0,76) | 17 giây, 1 phút 1 giây (4 export) | 71 MB, 395 MB |

  Đọc số đo: (1) với **một export** (`FHIR_JOB_CONCURRENCY=2`) API không bị ảnh hưởng ở cả 200.000 resource: PostgreSQL đã gần bão hòa bởi chính tải API (khoảng 580% CPU ở cả baseline và khi có export) nên export chỉ chịu chậm (4,6 lần, chạy nhường), không làm API chậm. (2) với **bốn export đồng thời** API chịu ảnh hưởng thật nhưng chưa vượt ngưỡng 1,5: p99 x1,48 (sát ngưỡng), thông lượng giảm 24%, RSS server đỉnh 395 MB. Kết luận cho D-SP-7: điều kiện A **chưa bị chứng minh** ở một export, và **có dấu hiệu** khi nhiều export đồng thời; giải pháp rẻ hơn tách service là giữ `FHIR_JOB_CONCURRENCY` thấp (mặc định 2). Mỗi cấu hình chỉ một lần đo.
- **Chưa kiểm:** máy riêng cho server và PostgreSQL (cả hai lần đo chung một máy, nên tranh chấp CPU có thể làm ảnh hưởng thật lớn hơn hoặc nhỏ hơn khi tách máy), bốn export ở cỡ 200.000 resource, resource lớn (Binary, DocumentReference), nhiều lần lặp để có phân bố.
- **Hệ quả cho kế hoạch:** điều kiện A và C của `fhir-worker` **không được số đo xác nhận** ở cỡ đã đo. Theo chỉ đạo, kế hoạch vẫn đặt service này (D-SP-1), nhưng nên là việc **cuối cùng và có thể hoãn**: chỉ làm khi đo ở cỡ lớn cho thấy API bị ảnh hưởng, hoặc vì lý do vận hành (ví dụ cô lập bộ nhớ đỉnh của export) mà ghi rõ không phải điều kiện A (DoD 7.1, 7.10).
- **Hai việc phải xong trước:**
  1. Kết quả export đang là `Binary` base64 trong CSDL (`bulk.go:178`): đưa job sang service khác **không giảm tải DB**, và CSDL phình theo `FHIR_EXPORT_RETENTION`. Cần ghi kết quả ra kho đối tượng (P0-2).
  2. `$reindex` cả loại từng dùng kho thô (`reindexType` dùng `r.repo`, admin một project reindex được mọi project): **đã sửa ở S0 (P0-3)**, job `reindex` nay là job có phạm vi, chỉ chạm project của người gọi.
- **Cách chạy (suy luận, chưa thử):** `fhir-server` nhận yêu cầu, tạo `AsyncJob` ở `accepted` rồi **không chạy** (cần thêm tùy chọn trong `fhirrouter.Options`, mặc định giữ như cũ: chạy tại chỗ); `fhir-worker` chạy vòng `RecoverJobs` và nhận job bằng lease. Độ trễ nhận job ≥ `JobRecoveryGrace` cộng chu kỳ bảo trì; có thể thêm `asynq` sau (adapter đã có chưa nối).
- **Dữ liệu và bí mật:** schema `fhir`, role `fhir_app`; cần **`FHIR_JOB_SIGNING_KEY` dùng chung** với `fhir-server` (job-seal: job không có chữ ký hợp lệ báo lỗi chứ không chạy); đọc kho Binary nếu export ghi ra kho.
- **Phụ thuộc chạy:** PostgreSQL, kho Binary; Redis không bắt buộc cho việc nhận job (nhận qua DB).

### 3.4 Không tách: Binary và `/storage`, terminology

- **Binary và `/storage`:** `/storage` tách được rẻ, nhưng phần Binary trong đường FHIR gắn chặt: `fhirbinary.Wrap` nằm dưới sự kiện Subscription, `RawBinary` bọc cả router, rewriter đổi tham chiếu thành URL ký ở mọi phản hồi; tách cần một API nội bộ chưa có. Tiền lệ `dicomweb` (dùng chung qua thư viện và DB) là mô hình đã chạy. [microservices-shared-db.md](microservices-shared-db.md) mục 3: dịch vụ nền chỉ tách khi có hơn một nơi gọi; chưa thành lý do. Xem lại nếu đo cho thấy tải lớn làm nghẽn API.
- **Terminology:** `resolveTerminology` chạy **trước mỗi truy vấn có `:below`, `:above`, `:in`** (`chain.go:37`) và đọc `fhir_coding*` cùng schema; tách là thêm một bước mạng vào đường tìm kiếm mà DB vẫn dùng chung. Nếu `$expand` lớn hay `$import` làm nghẽn thật, chỉ đẩy **riêng tác vụ nặng** sang `fhir-worker`.

## 4. Khuôn chung của mỗi service mới

Theo [service-principles.md](../service-principles.md) mục 3, 6, 7; tiền lệ gần nhất là `apps/dicomweb` (dùng chung schema `fhir`, vỏ mỏng) và `apps/ws-gateway` (mẫu DoD):

| Việc | Nội dung |
|---|---|
| Cấu trúc | `cmd/<tên>/main.go`, `internal/{config,presentation,bootstrap}` (vỏ mỏng, C-5); **không import `apps/fhir-server`** (C-1): phần lắp ráp riêng được viết lại trong `bootstrap` của service (khoảng vài chục dòng mỗi cụm), nghiệp vụ dùng chung đã ở `pkg/` |
| Kiến trúc | Thêm luật cho `apps/<tên>` vào `architecture_test.go` (C-4); `go test .` xanh |
| Cấu hình | `internal/config` dùng `pkg/envconfig`; **giữ nguyên tên biến đang có** (`FHIR_WEBHOOK_*`, `FHIR_AUTO_DOWNLOAD*`, `FHIR_BINARY_STORAGE`, `DATABASE_URL`, `REDIS_URL`) để dùng chung một tệp môi trường như `dicomweb`; `docs/config.md` sinh từ mã; `.env.example`; từ chối cấu hình sai và gom mọi lỗi |
| Dữ liệu | Không migration riêng: **`fhir-server` sở hữu migration**; service từ chối khởi động khi `fhirstore.Pending > 0` (như `dicomweb`); `DATABASE_REQUIRE_SCHEMA=true` |
| Probe và tắt êm | `/healthz`, `/health/live`, `/health/startup`, `/health/ready` (kiểm DB và broker); tắt êm có test (SIGTERM, mã thoát 0; nhả lease/PEL để nơi khác tiếp quản) |
| Image | `apps/<tên>/Dockerfile` riêng, người dùng không root, `HEALTHCHECK`, `Dockerfile.dockerignore`; service compose (`docker-compose.<tên>.yml` hoặc gộp) |
| Test | Bốn tầng theo [service-independence.md](service-independence.md): đơn vị cạnh mã, `tests/integration` (Redis và PostgreSQL thật), `tests/e2e` (dựng image, chạy cùng `fhir-server`), đột biến vào đường quan trọng |
| Tài liệu | `README.md`, `docs/{architecture,config,testing,runbook}.md` với mục "Vì sao tách" (mục 3 của tài liệu này là nguyên liệu), mục trong bản đồ service ở `service-principles.md` mục 2 |
| Hợp đồng | Không có route nghiệp vụ nên không có swagger/api-contract; ghim bằng test **hợp đồng dây**: định dạng sự kiện trên broker (`fhirsub.EventType`, C-6), claim và chữ ký `job-seal` (cho `fhir-worker`) |

## 5. Điều kiện tiên quyết

Các việc dưới đây độc lập với chuyện tách nhưng **làm việc tách mất ý nghĩa nếu chưa xong** (đã đối chiếu trong mã, trừ điểm ghi khác):

| Mã | Việc | Vì sao | Cỡ |
|---|---|---|---|
| P0-1 **[xong, S3; chọn (a)]** | Rest-hook bền (một trong a, b, c ở 3.2) | Tách `fhir-notifier` không sửa mất dữ liệu khi chết hoặc đầy hàng đợi | M |
| P0-2 | Kết quả bulk export ghi ra kho đối tượng | `fhir-worker` không giảm tải DB; CSDL phình theo retention | M |
| P0-3 **[xong, S0]** | `$reindex` cả loại dùng kho đã tách project; **chưa** kiểm AccessPolicy ở đường tạo job (policy làm job lỗi `403` khi chạy) | `fhir-worker` thực thi lỗi xuyên project | S |
| P0-4 | Cấm broker `memory` khi bật phía nghe bên ngoài | Các service tách chỉ hoạt động qua Redis | S |
| P0-5 **[xong, S0]** | Hoàn thiện `pkg/fhirdownload`: mã và test đã commit (`b709f7e`); đã thêm mô tả `FHIR_AUTO_DOWNLOAD_IGNORED_PREFIXES` và test `TestRedeliveredEventDoesNotDownloadTwice` | `fhir-downloader` là việc đầu tiên; còn hai việc nhỏ trước khi tách | S |

## 6. Quyết định

| Mã | Câu hỏi | Quyết định / đề xuất | Hệ quả nếu chọn khác |
|---|---|---|---|
| D-SP-1 **[đã chốt 2026-11-24 theo yêu cầu của người dùng]** | Tách bằng gì? | **Các service riêng (`apps/fhir-downloader`, `apps/fhir-notifier`, `apps/fhir-worker`), mỗi service một vai trò**; không dùng `FHIR_ROLE` | Vai trò trong cùng image rẻ hơn (một image, một CI) và đủ cho điều kiện C, nhưng không cho image, network policy và vòng đời deploy riêng |
| D-SP-2 | Thứ tự | `fhir-downloader`, rồi `fhir-notifier` (kèm P0-1), rồi `fhir-worker` (sau P0-2, P0-3; **có thể hoãn** vì #12c chưa cho thấy API bị ảnh hưởng) | `fhir-notifier` không có P0-1 chuyển nơi mất dữ liệu; `fhir-worker` trước khi đo thì thiếu bằng chứng điều kiện A |
| D-SP-3 | Redis | **Bắt buộc** cho `fhir-downloader` và `fhir-notifier`; broker `memory` bị cấm khi phía nghe ở ngoài (P0-4) | Broker `memory` chỉ chạy một tiến trình |
| D-SP-4 **[đã làm theo (a) ở S3, 2026-11-24]** | Cách làm rest-hook bền | (a) giao đồng bộ trong handler Redis Stream, ACK sau khi giao (`fhirsub.Config.Durable`; thêm `messaging` Concurrency để webhook chậm không chặn hàng đợi) | (b) outbox (thêm bảng, thêm bảo trì); (c) `asynq` (thêm phụ thuộc vận hành, đã có adapter chưa nối) |
| D-SP-5 | Role DB của các service mới | **Dùng chung `fhir_app`** như `dicomweb` (D-DW-1): kho tài liệu chung nên không cấp hẹp theo loại resource được | Role riêng cùng quyền chỉ giúp thống kê kết nối và thu hồi riêng: có thể làm sau, cần sửa `01-schemas.sh` |
| D-SP-6 | Binary và `/storage`, terminology | Không tách (3.4) | Thêm API nội bộ và một bước mạng vào đường tìm kiếm |
| D-SP-7 **[đã đo ở S4, 2026-11-24; vẫn do người dùng quyết]** | Đo trước khi tách `fhir-worker` (#12c)? | **Đã đo** (xem 3.3): một export không ảnh hưởng API ở 200.000 resource (p95 x1,02); bốn export đồng thời ảnh hưởng nhưng chưa quá ngưỡng (p99 x1,48, thông lượng x0,76, RSS 395 MB); chưa đo máy riêng. Khớp D-DB-3 ở chỗ chưa có bằng chứng điều kiện A cho trường hợp thông thường; tách `fhir-worker` vẫn theo D-SP-1 như việc cuối, vì lý do vận hành (cô lập bộ nhớ đỉnh và nhiều export đồng thời), không phải vì điều kiện A đã được chứng minh | Tách mà không có số đo cỡ lớn thì phải ghi đó là lý do vận hành, không phải điều kiện A |
| D-SP-8 | Dùng chung schema `fhir` cho 5 app (`fhir-server`, `dicomweb`, `fhir-notifier`, `fhir-downloader`, `fhir-worker`) | Chấp nhận như ngoại lệ D-DW-1 (cùng một miền dữ liệu), kèm quy tắc đổi schema ở mục 8 | Một schema chia cho nhiều app làm mọi thay đổi `fhirstore` ảnh hưởng cả năm |

## 7. Các bước

| Bước | Việc | Cỡ | Tiêu chí hoàn thành | Phụ thuộc |
|---|---|---|---|---|
| S0 **[xong 2026-11-24]** | P0-3 (sửa `$reindex` xuyên project: job `reindex` thành job có phạm vi, liệt kê id của project rồi reindex từng resource, test `TestReindexTypeStaysInsideCallerProject`) và P0-5 (mô tả `IGNORED_PREFIXES`, test nhận lại sự kiện cho `fhirdownload`) | S | test chặn `$reindex` xuyên project (token một project không ghi lại chỉ mục project khác); `fhirdownload` có test idempotency và mô tả đủ biến cấu hình | không |
| S1 **[xong 2026-11-24]** | Chuẩn bị `fhir-server` (đã làm: ba công tắc `FHIR_SUBSCRIPTION_DELIVERY`, `FHIR_AUTO_DOWNLOAD_WORKER`, `FHIR_JOB_EXECUTION` mặc định `true`; `fhirrouter.Options.DisableJobExecution`; cấu hình từ chối khi tắt phía giao hoặc phía tải mà broker không phải `redis`, hoặc `FHIR_WS_MODE=embedded` với giao tắt; test `TestSplitSwitches`, `split_e2e_test.go`): công tắc tắt phía nghe/giao (rest-hook và tự tải) ở API, mặc định như cũ; tùy chọn `fhirrouter.Options` để API không tự chạy job (cho S5); P0-4; thống nhất tên biến dùng chung | M | không bật công tắc thì hành vi và test cũ không đổi; từ chối khởi động nếu tắt phía nghe nội bộ mà broker là `memory` | S0 |
| S2 **[xong 2026-11-24]** | **`apps/fhir-downloader`** theo khuôn mục 4 (đã làm: `apps/fhir-downloader`, cổng 8009, group Redis `fhir-downloader` khác `trivita`, đột biến đã chạy ở `docs/testing.md`; e2e kiểm giết rồi chạy lại xử lý sự kiện ghi lúc dừng; **chưa** có e2e ghép `fhir-server` thật và cấu hình mạng chặn egress của API, compose để S6) | M | e2e: `fhir-server` (tắt phía tải) và `fhir-downloader` ở tiến trình khác; attachment ngoài thành `Binary/<id>`; giết `fhir-downloader` giữa chừng rồi chạy lại vẫn xử lý phần còn trong PEL; API không có đường ra Internet trong cấu hình e2e | S1 |
| S3 **[xong 2026-11-24]** | P0-1, rồi **`apps/fhir-notifier`** theo khuôn mục 4 | L | e2e: giết `fhir-notifier` giữa chừng, sự kiện chưa giao được giao lại sau khi chạy lại; webhook chậm không làm chậm API (đo p95); websocket vẫn nhận thông báo qua `ws-gateway` | S1, P0-1 (đã làm: giao bền ACK sau khi giao, `Durable` + `Concurrency`; `apps/fhir-notifier` :8010, group Redis `fhir-notifier`; e2e kiểm replica khác giao nốt phần dở dang và Redis chết rồi sống lại; **chưa** đo "webhook chậm không làm chậm API (p95)"; websocket mới kiểm tới mức phát lên kênh Redis khi có sổ hiện diện, chưa có e2e với `ws-gateway` thật; chưa có e2e ghép `fhir-server` thật; compose để S6) |
| S4 **[xong 2026-11-24]** | Đo tải #12c cỡ lớn: 200.000 resource (một export) và bốn export đồng thời ở 50.000 resource, `make bench-export` (kết quả ở 3.3; còn lại máy riêng, resource lớn) | S đến M | số đo cỡ lớn ghi vào tài liệu này; chốt D-SP-7 (xem mục 6) | không |
| S5 | P0-2, rồi **`apps/fhir-worker`** theo khuôn mục 4 | L | e2e: tạo export ở `fhir-server`, `fhir-worker` chạy job, API không thực thi job; giết `fhir-worker` giữa chừng thì job được tiếp quản; job-seal sai không chạy | S1, S4, P0-2 |
| S6 | Tài liệu: bản đồ service (`service-principles.md` mục 2 và 8), `fhir-server/docs/architecture.md` ("không còn làm" gì), runbook, compose, Makefile (`run-<tên>`), `docs/erd` nếu đổi dữ liệu; cập nhật `task-list.md` | S | | các bước trên |

## 8. Rủi ro

| Điều | Ghi chú |
|---|---|
| **Chi phí vận hành tăng** | Từ 4 service (`auth`, `fhir-server`, `ws-gateway`, `dicomweb`) lên 7: mỗi service thêm image, Dockerfile, e2e, CI, manifest, mục runbook (DoD 10 mục). Đây là hệ quả trực tiếp của việc chọn bậc 3; ước lượng cỡ L cho cả ba |
| **Một schema, năm app** | Mọi đổi schema `fhirstore` ảnh hưởng cả năm. Quy tắc đề xuất: migration chỉ **mở rộng rồi thu hẹp** (expand/contract); `fhir-server migrate up` trước, rồi `fhir-server`, rồi các service khác; mỗi service từ chối khởi động khi còn migration chưa chạy (đã có cho `dicomweb`) |
| Lệch phiên bản giữa các service | Cùng một module Go nên build cùng lúc, nhưng triển khai có thể lệch; hợp đồng dây (định dạng sự kiện, job-seal) phải chấp nhận cả phiên bản cũ trong giai đoạn chuyển (C-6) |
| Hai tiến trình xử lý cùng một sự kiện | Redis Streams consumer group giao mỗi message cho một consumer nhưng at-least-once: cả `fhir-downloader` và `fhir-notifier` phải idempotent (chưa kiểm cho `fhirdownload`; rest-hook "có thể trùng" theo tài liệu hiện có) |
| Giai đoạn chuyển giao | Khi vừa có `fhir-notifier` vừa còn phía nghe ở API, hai bên cùng nghe một consumer group sẽ chia sự kiện (đúng chức năng nhưng API vẫn gánh tải): phải tắt phía nghe ở API ngay khi bật service mới (công tắc S1) |
| Service nghe sự kiện sập | API vẫn nhận ghi và phát sự kiện lên Redis; sự kiện tích trong stream tới khi service chạy lại. Cần giới hạn độ dài stream và cảnh báo (chưa quy định) |
| Cửa sổ trễ Subscription | Replica khác thấy Subscription mới sau tối đa 30 giây (cache danh sách); `fhir-notifier` giữ nguyên hành vi |
| Không có chính sách mạng riêng | Nếu `fhir-downloader` không được đặt ở vùng mạng có egress khác API thì lợi ích an ninh chính của S2 chưa đạt (chỉ còn tách tải) |
| Số đo hạn chế | Mọi ưu tiên ở mục 0 dựa trên suy luận từ mã; chỗ duy nhất có số đo là #12c (một lần, 20.000 resource, một máy) và nó **không** ủng hộ việc tách `fhir-worker` |

## 9. Chưa làm hoặc chưa kiểm

- **Chưa đo tải:** webhook chậm, tải Binary lớn, `$expand` lớn. Export song song với API đã đo **một lần** (#12c, 20.000 resource); còn thiếu cỡ 200.000 resource, máy riêng, nhiều job đồng thời, resource lớn.
- **Chưa kiểm:** `fhirdownload` có idempotent khi nhận lại sự kiện (sau khi tải xong `url` đã thành `Binary/<id>` nên về mã sự kiện nhận lại không còn URL ngoài để tải; chưa có test riêng, chỉ có test dọn rồi thử lại khi xung đột phiên bản); cách (a) giao rest-hook trong handler Stream có chịu được webhook chậm không (PEL, `ClaimInterval`); tùy chọn "API không tự chạy job" chưa tồn tại trong `fhirrouter`; công tắc tắt phía nghe ở `fhir-server` chưa tồn tại; cổng `:8009` đến `:8011` chỉ là đề xuất (chưa kiểm xung đột trong `Makefile` và compose); hành vi `$expunge` có phát sự kiện Subscription không.
- **Tài liệu liên quan chưa cập nhật:** `service-principles.md` mục 2 vẫn liệt kê `fhir-worker` là "chưa quyết"; (tài liệu nghiệp vụ `apps/fhir-server/docs/business` đã nói về `FHIR_AUTO_DOWNLOAD`, F-28, commit `049f1e0`); `task-list.md` chưa có các bước S0 đến S6.
