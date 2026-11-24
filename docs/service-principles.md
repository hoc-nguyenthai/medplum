# Nguyên tắc tách service và triển khai theo kiến trúc microservice

Cập nhật 2026-11-22. Đây là tài liệu **nguyên tắc** (khi nào tách, mỗi service phải thỏa gì về code, dữ liệu, giao tiếp và triển khai). Kế hoạch từng đợt nằm ở nơi khác và được dẫn link: [plans/modules/split-scim-ws-services.md](plans/modules/split-scim-ws-services.md) (tách `ws-gateway`), [plans/microservices-shared-db.md](plans/microservices-shared-db.md) (dùng chung DB), [plans/service-independence.md](plans/service-independence.md) (test, docs, Dockerfile độc lập). Vận hành: [runbook.md](runbook.md).

Mỗi quy tắc dưới đây được đánh dấu **[ép]** (có test hoặc cơ chế làm hỏng build nếu vi phạm), **[quy ước]** (đang làm đúng nhưng chưa có gì ép) hoặc **[chưa]** (mục tiêu, hiện chưa đạt; ghi rõ ở mục 8). Điều gì không ghi, đừng coi là đã được kiểm.

## 1. Khi nào tách một service, khi nào gom

**Chỉ tách khi thỏa ít nhất một điều kiện, và điều kiện đó đo được hoặc chỉ ra được bằng bằng chứng trong code:**

| # | Điều kiện | Câu hỏi kiểm | Ví dụ trong repo |
|---|---|---|---|
| A | **Hồ sơ tải khác** | Có phải scale theo một đại lượng khác (số kết nối, dung lượng tải lên, hàng đợi) so với phần còn lại? | `ws-gateway` scale theo **số socket**, `fhir-server` theo **số yêu cầu**; `dicomweb` (kế hoạch) tải lên hàng trăm MB đến GB |
| B | **Ranh giới dữ liệu hoặc bí mật** | Có dữ liệu mà phần còn lại không được đọc? | `auth` giữ hash mật khẩu và khóa ký; role `fhir_app` không đọc được schema `identity` |
| C | **Miền lỗi và vòng đời deploy** | Deploy hay sự cố của phần này có được phép làm rớt phần kia? | deploy `fhir-server` không còn làm rớt websocket |
| D | **Đội sở hữu khác** | Có đội riêng, nhịp phát hành riêng? | chưa có trường hợp nào |

**Không tách khi:**

- Hai phần **dùng chung giao dịch hoặc cùng bộ bảng**, hoặc một thao tác phải xong trọn vẹn cả hai (ví dụ SCIM vô hiệu hóa người dùng phải thu hồi mọi `Login`). Tách thì hoặc chung DB (phá ranh giới B) hoặc phải mở API nội bộ chưa tồn tại. Vì vậy `scim` và `email` ở trong `auth`.
- Lý do chỉ là "nó là một module" hoặc "cho gọn". Module là đơn vị code (`pkg/`), không phải đơn vị triển khai.
- **Chưa có số đo** cho điều kiện A. Phải điều tra và đo trước (như P3a cho `fhir-worker`), không tách theo cảm giác.
- Tách theo **loại resource FHIR** (search, reference và transaction xuyên qua mọi loại).

**Thang tách rẻ → đắt (luôn thử bậc rẻ trước):**

1. Thư viện trong `pkg/` (không tốn triển khai gì).
2. **Cùng binary, vai trò khác** (cùng image, biến `*_ROLE` hoặc lệnh khác; Pod riêng, scale riêng, ingress riêng). Ví dụ đề xuất: `auth` cho SCIM, `fhir-server` cho `fhir-worker`.
3. Service riêng (thư mục `apps/<tên>` riêng, image riêng).

Chỉ lên bậc 3 khi bậc 2 không đủ (ví dụ cần ranh giới B, hoặc binary cùng lúc làm hai việc có hồ sơ bộ nhớ quá khác nhau).

**Mỗi service mới phải có mục "Vì sao tách" trong `docs/architecture.md` của nó**, nêu điều kiện A/B/C/D và bằng chứng; nếu không nêu được thì chưa được tách.

## 2. Bản đồ service hiện tại

| Service | Thư mục | Cổng | Dữ liệu | Phụ thuộc chạy | Scale theo | Vì sao tách (điều kiện) |
|---|---|---|---|---|---|---|
| `auth` | `apps/auth` | 8006 | schema `identity` (User, Login, UserSecurityRequest, Project...) | PostgreSQL; Redis tùy chọn (jti, topic FHIRcast) | số lượt đăng nhập và cấp token | B (bí mật), C |
| `fhir-server` | `apps/fhir-server` | 8005 | schema `fhir` (resource, lịch sử, chỉ mục, AsyncJob, Binary) | PostgreSQL; JWKS của `auth`; Redis nếu bật Subscription websocket | số yêu cầu | lõi |
| `ws-gateway` | `apps/ws-gateway` | 8007 | **không có DB** | Redis, khóa ký token; JWKS của `auth` nếu bật FHIRcast | số socket (bộ nhớ) | A, C |
| `fhir-cli` | `apps/fhir-cli` | không có | không có | gọi API | không áp dụng | công cụ, không phải service |
| `dicomweb` | `apps/dicomweb` | 8008 | **dùng chung schema `fhir`** (DicomStudy/Series/Instance là resource FHIR) + kho Binary dùng chung; không tự migrate | PostgreSQL; JWKS của `auth`; kho tệp hoặc S3 | dung lượng và thời gian tải lên | A, C (D4 xong: STOW-RS, QIDO-RS, WADO-RS chạy; xem [apps/dicomweb/docs/architecture.md](../apps/dicomweb/docs/architecture.md)) |
| `fhir-worker` | chưa quyết | cùng binary `fhir-server` | schema `fhir` | | hàng đợi job | A (cần số đo, D-DB-3) |

Quy tắc đếm: thư mục mới trong `apps/` chỉ khi lên bậc 3 của thang ở mục 1. Một vai trò chạy từ image có sẵn **không** thêm thư mục.

## 3. Nguyên tắc về code

| # | Quy tắc | Trạng thái |
|---|---|---|
| C-1 | **App không import app khác**, kể cả `cmd` và `bootstrap` (`TestEveryAppIsAClearApplication`). Chia sẻ code chỉ qua `pkg/` | [ép] |
| C-2 | `pkg/*` không biết app nào; chỉ phụ thuộc nhau theo hướng khai báo trong `pkgRules` (`TestSharedLibrariesDoNotKnowTheApp`); gói mới phải có luật | [ép] |
| C-3 | Echo chỉ ngoài `pkg`; adapter cụ thể (gorm, redis...) chỉ trong `pkg/` hoặc `infrastructure`/composition root | [ép] |
| C-4 | Mỗi app có `cmd/<tên>/main.go`, `internal/`, `README.md`; mọi app phải có luật trong `architecture_test.go` | [ép] |
| C-5 | **Tầng theo độ phức tạp:** app có nghiệp vụ riêng dùng đủ `domain`/`application`/`infrastructure`/`presentation`/`bootstrap` (`auth`); app chỉ lắp ráp thư viện dùng "vỏ mỏng" (`config`, `presentation`, `bootstrap`; `fhir-server`, `ws-gateway`). Nghiệp vụ dùng chung ở `pkg/` | [ép] |
| C-6 | **Hợp đồng giữa hai service là định dạng dây được ghim bằng test**, không phải import: token bind websocket (`v`, HMAC với tiền tố miền chữ ký), claim JWT (`fhir_project`, `scope`, `fhir_access_policy`), thông điệp kênh Redis. Đổi định dạng phải tăng phiên bản (`v`), bên nhận chấp nhận cả phiên bản cũ trong giai đoạn chuyển | [ép] một phần (token bind có test dựng tay; JWT có test với bộ xác minh thật) |
| C-7 | `pkg/testkit` (Docker, testcontainers) chỉ từ test hoặc `tests/`; `apps/<svc>/tests` không import app khác; `tests/` ở gốc không import app nào | [ép] |
| C-8 | Gói không được có đường gọi ra ngoài trái ý: `pkg/fhircast` cấm import `net` và client HTTP (`TestFhircastNeverDialsOut`); gọi ra ngoài (webhook...) phải qua `pkg/safehttp` (chặn dải nội bộ, DNS rebinding, redirect) | [ép] một phần |
| C-9 | Mỗi service **xác minh token cục bộ bằng JWKS**, không gọi `auth` mỗi yêu cầu. Hệ quả chấp nhận: thu hồi có độ trễ tối đa bằng hạn access token (mặc định 15 phút, `AUTH_ACCESS_TTL`) | [quy ước] có e2e ghi nhận cửa sổ này |

## 4. Nguyên tắc về dữ liệu

Chi tiết và lý do: [plans/microservices-shared-db.md](plans/microservices-shared-db.md).

| # | Quy tắc | Trạng thái |
|---|---|---|
| D-1 | Một cụm PostgreSQL, **một database, mỗi service một schema và một role**; role không có quyền gì trên schema của service khác (kể cả `USAGE`) và không tạo được bảng ở `public` | [ép] (`tests/dbmodel` chạy script init thật; `DATABASE_REQUIRE_SCHEMA`) |
| D-2 | **Không khóa ngoại, không JOIN xuyên schema**; tham chiếu chéo chỉ lưu ID | [ép] (test FK; lint SQL) |
| D-3 | SQL trong mã và migration **không gắn tên schema**, không `SET search_path` trần; schema do `search_path` của role quyết định | [ép] (`TestSQLIsSchemaAgnostic`) |
| D-4 | Đọc dữ liệu của service khác chỉ qua API của nó hoặc view công bố trong schema `shared` (chưa có view nào) | [quy ước] |
| D-5 | Mỗi service tự chạy migration của mình, bảng phiên bản nằm trong schema của nó. Nhiều replica: `DATABASE_AUTO_MIGRATE=false` và chạy `migrate up` ở bước deploy | [ép] (test migrate độc lập) |
| D-6 | Production: role migrate (`*_migrate`, có DDL) tách khỏi role chạy (`*_app`, chỉ DML); `DATABASE_MIGRATE_URL` cho lệnh `migrate` | [ép] khi bật (tùy chọn; D-DB-4) |
| D-7 | Tổng kết nối của mọi replica của mọi service phải nhỏ hơn `max_connections` (mỗi app mặc định 20) | [quy ước], cần tính tay |
| D-8 | Sự kiện giữa service đi qua Redis (`pkg/messaging`) hoặc API, không "poll bảng của nhau" | [quy ước] |

## 5. Nguyên tắc về giao tiếp và độ bền

1. **Xác thực:** Bearer JWT do `auth` cấp, mọi service tự kiểm bằng JWKS (có cache, nên đã tải khóa thì vẫn xác minh khi `auth` tạm tắt). Lỗi xác thực **fail đóng**: sai token là 401, đúng token mà thiếu project hoặc scope là 403, lỗi hạ tầng không bị đổi thành 401.
2. **Phụ thuộc phụ thì suy giảm có kiểm soát, phụ thuộc chính thì fail đóng.** Ví dụ không lấy được topic FHIRcast thì `auth` không cấp token có scope `fhircast/` (token thường vẫn cấp). Ghi rõ chọn hướng nào trong `architecture.md` của service.
3. **Mỗi lời gọi ra ngoài có hạn thời gian đặt NGAY trên client**, không chỉ qua context: go-redis không áp deadline của context ở bước khởi tạo kết nối (đo được: treo 5 giây dù context 2 giây).
4. **Sự kiện:** Redis Pub/Sub là at-most-once (mất khi đứt); chỉ dùng cho thông báo chấp nhận mất. Việc phải bền (job, ngữ cảnh lâm sàng) lưu ở DB hoặc kho có TTL và phải idempotent.
5. **Thứ tự bật phụ thuộc không phụ thuộc thời điểm:** service phải khởi động được khi phụ thuộc chưa sẵn sàng (Redis chưa lên thì `ready` 503, không thoát), trừ cấu hình sai thì thoát ngay với thông báo rõ biến nào sai.
6. **Xoay khóa không ngắt phiên:** mọi khóa dùng chung giữa hai service hỗ trợ khóa hiện hành + tối đa vài khóa cũ chỉ để xác minh (ví dụ `FHIR_WS_SIGNING_KEY_PREVIOUS`); thứ tự: bên xác minh nhận khóa mới trước, bên ký đổi sau.
7. **Hạn mức theo instance:** hạn mức đếm trong bộ nhớ (theo IP, theo người, số socket mỗi endpoint) là **theo từng instance**; cả cụm cho tối đa N x số replica. Ghi điều này ở nơi cấu hình.

## 6. Hợp đồng triển khai: mọi service phải thỏa

### 6.1 Image và tiến trình

- Binary tĩnh trong `gcr.io/distroless/static-debian12:nonroot`, `USER 65532:65532` **dạng số** (để `runAsNonRoot` của K8s xác minh được), `HEALTHCHECK ["/app","healthcheck"]`. [ép] bằng e2e của `ws-gateway` (cho các service khác: [quy ước]).
- Một lệnh duy nhất làm điểm vào (`/app`), lệnh con cho việc khác: `serve` (mặc định), `healthcheck`, `migrate`, `seed`... Không cần shell trong image.
- Image **không chứa bí mật và không chứa cấu hình môi trường**; mọi thứ qua biến môi trường.

### 6.2 Cấu hình

- **Chỉ từ biến môi trường** (và `.env` cho dev, không ghi đè biến thật), đọc bằng `pkg/envconfig`; gom mọi lỗi và **từ chối khởi động** nếu sai, nói rõ biến nào.
- Mặc định **an toàn cho local**, không an toàn-hóa nhầm cho production: ở `staging`/`production` cấm khóa tạm, HMAC, `http://`, `*` ở origin, SQLite; bắt buộc nơi lưu dùng chung (Redis cho nhiều instance) khi tính năng đòi hỏi.
- `.env.example` đầy đủ và có chú thích; tốt nhất tài liệu cấu hình **sinh từ mã** và có test chống lỗi thời (đã có cho `ws-gateway`, `auth` và `fhir-server`: `docs/config.md`; `auth` và `fhir-server` còn có test bắt mọi biến phải có trong `.env.example`). [quy ước]; [ép] ở `ws-gateway`, `auth` và `fhir-server`.
- Tính năng tùy chọn mặc định **tắt** (`FHIRCAST_ENABLED`, `FHIR_SUBSCRIPTIONS`...); đặt biến của tính năng mà chưa bật là lỗi cấu hình.

### 6.3 Probe (bộ chuẩn)

| Đường dẫn | Ý nghĩa | Phụ thuộc ngoài? |
|---|---|---|
| `/health/live` | tiến trình còn sống | **không** (K8s không được restart pod vì lỗi của Redis hay DB) |
| `/health/startup` | đã khởi động xong | không |
| `/health/ready` | sẵn sàng nhận traffic: kiểm phụ thuộc chính, **503 khi đang tắt** | có, với hạn `READINESS_TIMEOUT` |
| `/healthz` | `HEALTHCHECK` của Docker | không |

### 6.4 Tắt êm (bắt buộc)

`SIGTERM` → `/health/ready` 503 → chờ `SHUTDOWN_DRAIN_DELAY` (K8s gỡ pod khỏi endpoints, vẫn phục vụ) → đóng cổng và chờ việc đang chạy tối đa `SHUTDOWN_TIMEOUT` → đóng phụ thuộc theo thứ tự ngược → thoát mã 0 (`pkg/httpserve.Run`). **`terminationGracePeriodSeconds` phải lớn hơn `SHUTDOWN_DRAIN_DELAY + SHUTDOWN_TIMEOUT`** (mặc định 5 + 25 giây; `ws-gateway` dùng 45). Thứ tự đóng: nhận kết nối vào → việc nền → phụ thuộc mượn nhau (bus trước Redis). [ép] bằng e2e của `ws-gateway`.

### 6.5 Không trạng thái và co giãn

- Không sticky session; trạng thái ở DB hoặc Redis; token tự chứa khi cần (token bind websocket).
- Chọn chỉ số scale theo hồ sơ tải (A): `ws-gateway` theo bộ nhớ và CPU, `fhir-server` theo yêu cầu. **Định cỡ bằng số đo thật** (`make bench-ws`: 100 KB mỗi socket + 24 MB nền, đo trên máy dev) rồi mới đặt requests/limits.
- `GOMEMLIMIT` thấp hơn limit bộ nhớ; HPA có `behavior` thu nhỏ chậm cho workload kết nối dài.

### 6.6 Manifest K8s mẫu (tham chiếu: `deploy/k8s/ws-gateway`)

Deployment: `replicas` >= 2; `maxSurge: 1`, **`maxUnavailable: 0`**; `minReadySeconds`; 3 probe; `runAsNonRoot`, `readOnlyRootFilesystem`, bỏ mọi capability; requests/limits; `terminationGracePeriodSeconds` theo 6.4. Kèm Service, HPA, **PodDisruptionBudget**, ConfigMap cho cấu hình thường và Secret mẫu cho bí mật, ingress với timeout websocket dài hơn heartbeat, NetworkPolicy (tùy chọn). Chỉ `ws-gateway` có bộ này; kiểm trên cụm thật: tắt êm, Redis chết và sống lại, rolling update; **chưa kiểm**: HPA tự scale, NetworkPolicy thực thi, ingress/Istio thật, nhiều node.

### 6.7 Migration khi triển khai

`DATABASE_AUTO_MIGRATE=false` khi nhiều replica; chạy `<app> migrate up` như một bước riêng (Job hoặc tác vụ một lần) **trước** khi rollout; production tách role migrate (D-6); app chưa migrate phải từ chối khởi động với thông báo "schema chưa được migrate". Thử bằng `docker-compose.db-roles.yml`.

### 6.8 Quan sát

Log JSON (`LOG_JSON=true` ở container), có request id; telemetry qua `pkg/observability` (OTLP, bản nhúng của `trivita-observability-go`); metric **tắt mặc định**, bật bằng `OTEL_METRICS_ENABLED=true`; không bao giờ ghi bí mật, token, hay endpoint đầy đủ vào log hoặc thông báo lỗi trả client (lỗi 5xx không lộ chi tiết nội bộ).

### 6.9 Bảo mật mặc định

Rate limit theo IP **bật mặc định ở staging/production**; Origin websocket chỉ cùng host trừ khi cấu hình; endpoint bí mật dùng 122 bit ngẫu nhiên từ `crypto/rand`; không có callback ra ngoài trừ qua `pkg/safehttp`.

## 7. Tạo một service mới: danh sách kiểm (Definition of Done)

Một service chỉ được coi là "triển khai được" khi thỏa **tất cả**. Tham chiếu mẫu: `apps/ws-gateway`.

1. Có mục "Vì sao tách" nêu điều kiện A/B/C/D và bằng chứng (mục 1); đã thử bậc rẻ hơn.
2. Thư mục `apps/<tên>` đúng cấu trúc C-4/C-5, luật trong `architecture_test.go`, chạy `go test .` xanh.
3. Cấu hình theo 6.2 (kèm `.env.example`, từ chối cấu hình sai, tài liệu cấu hình sinh từ mã nếu có thể).
4. Probe theo 6.3; tắt êm theo 6.4 **có test** (SIGTERM, mã thoát 0, client nhận tín hiệu đóng).
5. Image theo 6.1; `docker build` chạy được; e2e dựng image rồi chạy.
6. Dữ liệu theo mục 4: schema và role riêng (hoặc không DB); migration riêng; test quyền.
7. Hợp đồng dây với service khác được ghim bằng test (C-6), có phiên bản.
8. Test theo bốn tầng: đơn vị cạnh mã, `tests/integration` (Docker thật), `tests/e2e` (image), `tests/load` nếu có SLO về tải; đột biến vào đường quan trọng đều làm test đỏ.
9. Tài liệu: `README.md`, `docs/{architecture,config,testing}.md`, mục trong `runbook.md` (sự cố thường gặp, định cỡ), manifest K8s mẫu, mục trong bản đồ ở mục 2.
10. Hợp đồng API: `docs/api-contract.md` có bảng route sinh từ `swagger.json`; `docs/contract_test.go` gọi `pkg/apicontract` (`SyncDoc`, `Completeness`, `Structure`) và khai báo phần riêng của service trong `apicontract.Options` (DTO tùy chọn có lý do, phản hồi không body, Content-Type thật); thêm test so route thật với spec bằng `apicontract.DiffRoutes`. Mẫu: `apps/auth/docs/contract_test.go`. [ép]
10. Kèm số đo cho định cỡ (nếu điều kiện tách là A).

## 8. Hiện trạng đối chiếu (trung thực)

| Nguyên tắc | `auth` | `fhir-server` | `ws-gateway` |
|---|---|---|---|
| C-1 đến C-4 (kiến trúc) | ép | ép | ép |
| D-1 đến D-3 (schema, role, lint SQL) | ép | ép | không có DB |
| D-5/D-6 migration và role migrate | có (`DATABASE_MIGRATE_URL`) | có | không áp dụng |
| 6.1 image không root, HEALTHCHECK | **ép** (e2e dựng image riêng) | quy ước | **ép** (e2e) |
| 6.2 tài liệu cấu hình sinh từ mã | chưa | chưa | **ép** |
| 6.3 probe chuẩn | có (`/health/*` + `/healthz`) | **lệch**: chỉ `/healthz` và `/readyz` | có |
| 6.4 tắt êm có test | quy ước | quy ước | **ép** |
| 6.6 manifest K8s mẫu | **chưa** | **chưa** | có |
| 7.8 bố cục test bốn tầng (`tests/`) | chưa (T2) | chưa (T2) | **có** |
| Dockerfile riêng cho từng app | **có** (`apps/auth/Dockerfile`, build từ gốc repo, e2e dựng chính image) | **có** (`apps/fhir-server/Dockerfile`, e2e dựng chính image) | **có** (`apps/ws-gateway/Dockerfile`, e2e dựng chính image; không `VOLUME`, chạy được với root filesystem chỉ đọc) |
| CI | không thấy cấu hình trong repo | | |

Việc tiếp theo theo mức ưu tiên cho triển khai thật: (1) manifest K8s cho `auth` và `fhir-server`, (2) thêm `/health/*` vào `fhir-server`, (3) bố cục `tests/` cho hai app còn lại (T2), (4) CI. (Việc trước đây đứng đầu, CI phải checkout `trivita-observability-go` cạnh repo, đã hết cần từ khi thư viện được nhúng vào `pkg/observability`.)

## 9. Điều không làm (anti-pattern đã gặp hoặc đã loại)

- Hai service chung bảng hoặc chung schema; service đọc thẳng bảng của service khác.
- Gọi `auth` mỗi yêu cầu để kiểm token.
- Tách theo loại resource, hoặc tách vì "một module một service".
- Bí mật trong image hoặc trong thông báo lỗi; mặc định bí mật yếu ở production.
- `timeout` chỉ dựa vào context khi client có bước khởi tạo riêng (xem mục 5 điều 3).
- Liveness phụ thuộc DB hoặc Redis (làm K8s restart hàng loạt khi phụ thuộc lỗi).
- Chạy `migrate` tự động ở nhiều replica cùng lúc, hoặc role app có DDL ở production.

## 10. Quyết định còn mở liên quan

[D-SI-3](plans/service-independence.md) (gỡ `replace`, Dockerfile riêng), D-SI-2 (một module Go chung), D-DB-3 (tách `fhir-worker`), [D-DB-4](plans/microservices-shared-db.md) (role migrate: đã làm tùy chọn, chưa có xác nhận bằng lời), sáu app đã bị gỡ khỏi thư mục làm việc (D-SI-4).
