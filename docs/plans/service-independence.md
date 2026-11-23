# Kế hoạch: mỗi service độc lập (tests, docs, Dockerfile, e2e) và dùng testcontainers

Trạng thái: **đang làm** (2026-11-22): T0, T1a (mẫu `ws-gateway`) và Dockerfile riêng của `auth` (một phần T1b) đã xong, xem mục 7 và 8. Mục tiêu của người dùng: mỗi service có test, docs, Dockerfile riêng và rõ ràng; e2e rõ ràng; dùng testcontainers để dựng DB, Redis... khi cần; test đơn vị nằm cạnh mã. Tài liệu này gom điều đã điều tra, các quyết định, bố cục, và các bước. Nguyên tắc tổng quát: [../service-principles.md](../service-principles.md). Bổ sung cho [microservices-shared-db.md](microservices-shared-db.md) (cách dùng chung PostgreSQL) và [modules/split-scim-ws-services.md](modules/split-scim-ws-services.md). Số liệu "đã đo" là do tôi chạy lệnh ngày 2026-11-22; câu ghi "chưa kiểm" là chưa kiểm.

## 0. Kết luận

| Điều | Nội dung |
|---|---|
| Số service | **4 chắc chắn** (`auth`, `fhir-server`, `ws-gateway`, `dicomweb`); **5 khuyến nghị** nếu thêm `fhir-worker` (chỉ khi đo thấy cần); tối đa 9 nếu M1, M3, M4 đều chọn "có" (mục 2). |
| Bố cục test | Test đơn vị **cạnh mã** (`*_test.go`, **đã chốt**); `apps/<svc>/tests/{integration,contract,e2e,load,testdata}` cho phần còn lại; `tests/e2e` ở gốc cho e2e nhiều service. |
| Hạ tầng test | `pkg/testkit` bọc `testcontainers-go`: PostgreSQL (một container cho cả tiến trình test, mỗi test một schema), Redis (mỗi test một container), và `BuildImage` dựng image từ Dockerfile của service. |
| Một module Go | Giữ một `go.mod` (đề xuất, **chưa được xác nhận**): sửa `pkg/*` một lần cho mọi service. "Độc lập" nghĩa là build, test, deploy riêng, không phải module riêng. |

## 1. Hiện trạng (đã đo)

| Tiêu chí | Hiện trạng | Mức |
|---|---|---|
| Dockerfile | Một file gốc dùng `ARG APP`. `go.mod` có `replace github.com/TRIVITAAI/trivita-observability-go => ../trivita-observability-go` nên build phải lấy cả thư mục cha (`context: ..`) | Chặn build độc lập trong CI một repo |
| Test đơn vị | `auth` 9,0k dòng test / 14,7k dòng mã; `fhir-server` 2,6k / 11,4k; `ws-gateway` 1,9k / 1,2k; `fhir-cli` 0,4k / 0,8k; `pkg/*` 65,9k dòng mã | Có; `fhir-server` mỏng nhất |
| Test cần DB/Redis | `make test-pg` và `test-auth-pg` chạy `docker run` cổng cố định và 4 biến môi trường (`FHIRSTORE_TEST_POSTGRES_URL`, `FHIR_TEST_REDIS_URL`, `MESSAGING_TEST_REDIS_ADDR`, `FHIRCAST_TEST_REDIS_ADDR`); không đặt biến thì test **bị bỏ qua âm thầm**; `go.mod` không có testcontainers | Yếu |
| e2e | Một thư mục `e2e/` gốc: 12 test, 1129 dòng, dùng binary thật + SQLite + miniredis; chưa dùng PostgreSQL/Redis thật, chưa dựng bằng Dockerfile | Chưa đủ |
| Docs | Mỗi app có README; `auth` và `fhir-server` có `docs/`; `ws-gateway` và `fhir-cli` không có `docs/` | Không đều |
| Test white-box (cùng `package`, không chuyển được sang thư mục khác) | `auth` 29 file, `ws-gateway` 1, `pkg` 9; black-box: `auth` 10, `fhir-server` 11, `ws-gateway` 7, `fhir-cli` 2, `pkg` 45 | Lý do giữ test đơn vị cạnh mã |
| CI | Không thấy cấu hình CI trong repo (chưa kiểm bên ngoài) | Chưa biết |
| Docker trên máy dev | Docker 29.8.2 (OrbStack); engine từng treo trong phiên làm việc ngày 2026-11-22 | Test cần timeout rõ và cách chạy lại gọn |

## 2. Danh mục service

| # | Service | Trạng thái | Chắc chắn? | Lý do là service riêng |
|---|---|---|---|---|
| 1 | `auth` | có | chắc | Giữ bí mật và khóa ký; vòng đời khác kho lâm sàng |
| 2 | `fhir-server` | có | chắc | Lõi FHIR |
| 3 | `ws-gateway` (gồm Hub FHIRcast) | có | chắc | Kết nối dài, scale theo số socket |
| 4 | `dicomweb` | chưa có mã (kế hoạch L, D0 đến D6) | chắc theo H2 | Tải lên rất lớn |
| 5 | `fhir-worker` | chưa có | **có điều kiện** | Cùng binary `fhir-server`; chưa kiểm đã nối `asynq` chưa |
| 6 | `bot-runner` | chưa có | phụ thuộc M1 | Chạy Bot cho `cds`, `webhook`, `cron`, `$execute`; phương án A (HTTP ngoài) thì không cần |
| 7 | `mcp-gateway` | chưa có | tùy chọn | SSE dài; kế hoạch hiện đặt trong `fhir-server` |
| 8 | `ai-gateway` | chưa có | phụ thuộc M4 | `$ai`, `ws/ai-realtime` |
| 9 | `agent-gateway` (Agent/HL7 của Medplum) | chưa có | phụ thuộc M3 | Cầu nối tại chỗ |

Không phải service riêng (theo kế hoạch): `scim` và `email` trong `auth`; `storage`, `keyvalue`, `cds` trong `fhir-server`; `webhook` chờ `bot-runner`; `cloud` không port nguyên; chuyển dữ liệu từ Medplum (M2) là công cụ chạy một lần.

**Sáu app đã bị gỡ khỏi thư mục làm việc nhưng còn trong index git** (`agent-gateway`, `authz-service`, `drawler`, `pdf-ocr`, `admission-agent`, `agent-demo`; khoảng 270 file staged): không kế hoạch nào nhắc tới; **chưa rõ có thuộc phạm vi**. Nếu thuộc, tổng thành 10 đến 15 service. Tôi không đụng vào chúng.

## 3. Quyết định

| Mã | Quyết định | Trạng thái |
|---|---|---|
| D-SI-1 | Test đơn vị cạnh mã; `tests/` chỉ chứa integration, contract, e2e, load | **Đã chốt 2026-11-22** |
| D-SI-2 | Một module Go chung, mỗi service build/test/deploy riêng | Đề xuất, chưa xác nhận; việc đang làm không phụ thuộc |
| D-SI-3 | Cách gỡ `replace ../trivita-observability-go`: publish module có tag (đề xuất), `go mod vendor`, hoặc `docker build --build-context` | **Đã giải 2026-11-23**: nhúng thư viện vào `pkg/observability`, bỏ `replace` và build context phụ (commit `80e36d0`) |
| D-SI-4 | Sáu app bị gỡ có thuộc phạm vi | **Mở** |
| D-SI-5 | Chấp nhận mục tiêu 4 service chắc chắn + `fhir-worker` có điều kiện; hoãn các service 6 đến 9 tới khi chốt M1, M3, M4 | Đề xuất, chưa xác nhận |

## 4. Bố cục

```
apps/<svc>/
  Dockerfile  .dockerignore  Makefile  README.md  docs/{architecture,config,runbook,api}
  internal/...                    test đơn vị cạnh mã: *_test.go
  tests/
    integration/                  PostgreSQL/Redis thật (testkit), chạy trong process
    contract/                     khóa định dạng dây (token, OperationOutcome, thông điệp websocket)
    e2e/                          dựng image từ Dockerfile của chính service, chạy cùng PG/Redis
    load/                         đo tải
    testdata/
tests/e2e/                        e2e nhiều service (đặt ở gốc repo)
pkg/testkit/                      helper testcontainers dùng chung
```

Quy ước:

- `tests/*` là gói riêng: chỉ dùng API công khai và `internal/` của **chính app** (Go cho phép vì `tests/` nằm dưới thư mục cha của `internal/`); không import app khác (`architecture_test.go` kiểm).
- `pkg/testkit` chỉ được import từ file `_test.go` hoặc từ `tests/`; `apps/*/internal` (không phải test) và `pkg/*` khác không được import.
- Test đơn vị chạy bằng `go test -short ./...`, **không cần Docker**. Test cần Docker gọi `testkit.RequireDocker(t)`: không có Docker thì **bỏ qua có thông báo rõ**, trừ khi `TRIVITA_REQUIRE_DOCKER=1` (CI) thì **đỏ**.
- Target Makefile theo app: `make test-unit APP=x`, `test-integration`, `test-e2e`, `test-load`; `make test` chạy mọi tầng.
- PostgreSQL: một container cho cả tiến trình test, mỗi test một schema (khớp D-DB-1) và, khi test quyền, một role riêng; Redis: mỗi test một container (cô lập, an toàn khi chạy song song). Container được dọn bởi Ryuk (reaper) của testcontainers khi tiến trình thoát.
- `BuildImage` gọi `docker build` (không dùng bộ dựng trong thư viện) vì cần BuildKit và `Dockerfile.dockerignore`; context mặc định là thư mục cha của repo cho tới khi D-SI-3 được chốt.

## 5. Các bước

| Bước | Việc | Cỡ | Tiêu chí hoàn thành | Phụ thuộc |
|---|---|---|---|---|
| T0 **[x]** | `pkg/testkit` (Postgres, Redis, BuildImage, RequireDocker) + luật kiến trúc cho `tests/` và `testkit` | S đến M | Test của testkit xanh trên Docker thật; luật kiến trúc bắt được vi phạm giả lập | không |
| T1a **[x]** | Làm mẫu với `ws-gateway`: `tests/integration` (chuyển test bootstrap dùng Redis giả sang testcontainers), `tests/load` (chuyển `e2e/ws_load_test.go`), `tests/e2e` (image + Redis thật, gồm FHIRcast), Makefile/README/docs riêng | M | Mọi tầng xanh; đột biến vào mã ws-gateway làm test đỏ | T0 |
| T1b | Dockerfile riêng cho từng app (`apps/<svc>/Dockerfile`) và gỡ `replace` | M | `docker build` chạy được từ context của repo; CI path filter | D-SI-3 |
| T2 | Lặp lại T1a cho `fhir-server`, rồi `auth`, rồi `fhir-cli` | M mỗi app | Như T1a | T1a |
| T3 | Chuyển các test dùng `docker run` và biến môi trường trong `pkg/*` sang testkit; bỏ `test-pg`, `test-auth-pg` | M | Không còn `docker run` trong Makefile; không test nào bị bỏ qua âm thầm | T0 |
| T4 | e2e nhiều service (`tests/e2e`): `authen_fhir_test.go`, `ws_split_test.go` dựng bằng image thật + PostgreSQL + Redis thật | M đến L | Chạy được bằng `make test-e2e`; có `TRIVITA_REQUIRE_DOCKER=1` ở CI | T1b |
| T5 | CI: path filter theo app; lịch chạy e2e | S | Đổi `apps/ws-gateway` chỉ chạy test và build của nó | T1b |
| T6 | Service mới (`dicomweb`, `fhir-worker`...) theo đúng bộ khung này ngay từ đầu | theo kế hoạch | | T1a |

Cỡ chỉ là ước lượng thô, chưa đo.

## 6. Rủi ro

| Điều | Ghi chú |
|---|---|
| Docker là điều kiện của test tích hợp và e2e | Docker engine từng treo trong phiên 2026-11-22. Mọi helper có timeout; bước khởi động container có giới hạn thời gian và in log của container khi thất bại. |
| `go.mod` phình ra | `testcontainers-go` kéo theo nhiều phụ thuộc (client Docker...). Chỉ bị biên dịch khi được import (từ test), nhưng `go mod download` trong Dockerfile vẫn tải. Chấp nhận; nếu thành vấn đề thì tách `pkg/testkit` thành module riêng. |
| Tốc độ | Container PostgreSQL mất vài giây để khởi động; một container dùng chung cho cả tiến trình test và mỗi test một schema để giữ tốc độ. Chưa đo. |
| Mất độ phủ khi chuyển test | Mỗi lần chuyển test sang `tests/integration` phải chạy trước và sau, và chạy đột biến để chắc test cũ và mới bắt cùng lỗi. |
| Chạy song song | Redis mỗi test một container nên an toàn; PostgreSQL dùng schema riêng nên an toàn; test đo tải (`load`) không chạy song song với test khác. |

## 7. Đã làm (2026-11-22)

**T0, `pkg/testkit`** (testcontainers-go v0.45.0; `go get` chỉ thêm 38 module mới, không nâng phiên bản nào có sẵn; binary production không chứa testcontainers: `go list -deps` của ba service cho 0 kết quả):
- `RequireDocker` (bỏ qua có thông báo, hoặc thất bại khi `TRIVITA_REQUIRE_DOCKER=1`; `-short` luôn bỏ qua), `Postgres` (một container cho cả tiến trình) với `NewSchema` và `NewServiceRole` (role chỉ có quyền trên schema của nó), `StartRedis` (mỗi test một container; `Stop`/`Start` mô phỏng Redis chết), `NewNetwork`, `BuildImage` (gọi `docker build`), `RunService` (chờ probe, `Logs`, `Exec`, `Terminate` kiểm tắt êm), `RunToExit` (kiểm từ chối khởi động).
- Luật kiến trúc: `pkg/testkit` chỉ được import từ `_test.go` hoặc `tests/` (`TestTestkitOnlyUsedByTests`); `apps/<svc>/tests` không import app khác và `tests/` ở gốc không import app nào (`TestTestsDirsStayInTheirApp`); đã thử chèn vi phạm giả lập, cả ba luật đều đỏ.
- 11 test chạy trên Docker thật (11 giây khi image đã có; lần đầu kéo image mất khoảng 2 phút); đột biến 11 chỗ đều làm test đỏ. Đường lỗi của `BuildImage` và `RunService` (Dockerfile hỏng, container không mở cổng) chưa có test tự động.
- Phát hiện khi làm: đầu ra `Exec` của testcontainers là luồng ghép của Docker (8 byte tiêu đề), phải dùng `Multiplexed()`; PID 1 trong container phải tự bắt SIGTERM (image thử của testkit dùng `trap`), giống một service Go thật.

**T1a, mẫu `ws-gateway`:**
- `tests/integration` (21 test): chuyển từ `internal/bootstrap` (test black-box), thay Redis giả bằng Redis thật qua testkit. Hai ca "Redis chết và sống lại" trước đây **bị bỏ qua** khi dùng Redis thật, nay chạy thật.
- `tests/e2e` (7 test): dựng **image từ Dockerfile** rồi chạy cùng Redis thật trong một mạng Docker: người dùng 65532 (đã bắt lại lỗi `USER nonroot` của WS-8 khi thử đột biến), `HEALTHCHECK`, probe, Redis chết và sống lại, nhận sự kiện phát qua Redis, sổ hiện diện, token giả, FHIRcast qua HTTP và websocket (xác thực, cách ly project, hủy đăng ký), tắt êm (1001, mã thoát 0), từ chối cấu hình sai.
- `tests/load`: chuyển từ `e2e/ws_load_test.go`, dùng Redis thật (500 socket: 66 KB/socket đo từ RSS, p99 12 ms; số đo ở máy dev).
- `Makefile` riêng của service (`make help`) và các target gốc `test-unit`, `test-integration`, `test-service`, `test-app` theo `APP=`; `docs/{README,architecture,config,testing}.md`; `docs/config.md` được **sinh từ mã** và có test chống lỗi thời (đột biến đổi tên biến làm test đỏ).
- Đột biến vào mã service và Dockerfile (USER, HEALTHCHECK, readiness không kiểm Redis, Hub không đóng khi Stop, bỏ kiểm scope...) đều làm test e2e hoặc tích hợp đỏ; còn 3 đột biến sống sót đều tương đương về hành vi hoặc đã được test tích hợp bắt (ghi trong lịch sử phiên làm việc).
- Còn lại của bước này: `test-pg` và `test-e2e` ở gốc vẫn dùng `docker run` và miniredis (thuộc T3 và T4); các test `internal/**` của `ws-gateway` vẫn chạy bằng `go test`, không cần Docker.

## 8. Đã làm: Dockerfile riêng của `auth` và đổi tên `authen` thành `auth` (2026-11-22)

**D-SI-3 (cách gỡ `replace ../trivita-observability-go`) được giải cho `auth` bằng build context có tên của BuildKit**, không đổi `go.mod`, không phát hành module, không vendor: `apps/auth/Dockerfile` build từ **gốc repo**, thư viện quan sát vào qua `--build-context observability=../trivita-observability-go` rồi `COPY --from=observability . /src/trivita-observability-go` (đường dẫn khớp `replace`). Hệ quả: hết hack context ở thư mục cha, hết phụ thuộc vào việc repo tên `trivita-codebase-v1`, và `apps/auth/Dockerfile.dockerignore` (danh sách cho phép: `go.mod`, `go.sum`, `pkg`, `apps/auth`) thu nhỏ context. Giới hạn: vẫn cần thư mục `trivita-observability-go` nằm cạnh repo ở nơi build (CI phải checkout nó); `go mod download` vẫn tải cả phụ thuộc test (testcontainers) vào lớp cache; cả ba service (`auth`, `fhir-server`, `ws-gateway`) đều có Dockerfile riêng, Dockerfile chung ở gốc đã bị xoá.
- Kiểm chứng: dựng lạnh (`--no-cache`) 61 giây, image 51,9 MB, `USER 65532:65532`, `HEALTHCHECK`, `ENTRYPOINT ["/app"]`. `apps/auth/tests/e2e` (4 test, Docker thật) dựng chính image và kiểm: người dùng không root và `/app healthcheck`, bốn probe, discovery và JWKS, **seed project bằng lệnh con của image rồi xin token và xác minh token bằng JWKS do chính container công bố** (thư mục `/data` phải ghi được bởi UID 65532), tắt êm mã 0, từ chối cấu hình sai (production dùng SQLite, `DATABASE_REQUIRE_SCHEMA` với SQLite, base URL sai, và biến `AUTHEN_*` của tên cũ). Đột biến 10 chỗ ở Dockerfile, dockerignore và kiểm biến cũ (USER dạng tên, USER root, bỏ HEALTHCHECK, `/data` thuộc root, `DATABASE_URL` sai chỗ, ENTRYPOINT sai, thiếu `pkg` trong context, không kiểm biến cũ, tiền tố cũ sai) đều làm test đỏ.
- `make docker APP=<app>` dùng `apps/<app>/Dockerfile` và báo lỗi (exit 2) nếu app chưa có. `docker-compose.yml` và `docker-compose.db-roles.yml` dùng `additional_contexts`.
- `testkit.BuildSpec.NamedContexts` và `Service.ExecEnv` (thêm cho e2e này).

> **Cập nhật 2026-11-23:** cách build context có tên ở trên đã được thay. Thư viện quan sát nay nằm trong repo ở `pkg/observability`; `go.mod` không còn `replace`, Dockerfile, Makefile và compose không còn `--build-context`/`additional_contexts`, và CI không cần checkout repo nào khác.

**Đổi tên `authen` thành `auth`** (theo yêu cầu, giống Medplum): thư mục `apps/auth`, `cmd/auth`, import `trivita-fhir/apps/auth/...`, binary và lệnh, image `trivita-auth`, service compose `auth` và `auth-migrate`, target Make `run-auth` và `test-auth-pg`, docs `apps/auth/docs/plans/` và `apps-auth.md`, tên telemetry, tệp SQLite dev `./auth.db`, và **tiền tố biến môi trường `AUTHEN_*` thành `AUTH_*` (71 biến)**; 130 tệp được sửa bằng thay thế theo nguyên từ (không đụng `authenticate`/`Authenticator`). **Phá tương thích có chủ ý:** một `.env` hay manifest cũ còn `AUTHEN_*` sẽ bị bỏ qua nếu không có gì chặn (cấu hình rơi về mặc định, issuer sai), nên `config.Load` **từ chối khởi động** và liệt kê tên mới (có test và e2e). **Không đổi:** schema `identity` và các role `identity_app`, `identity_migrate` (tên miền nghiệp vụ; đổi sẽ cần di chuyển dữ liệu), đường dẫn HTTP. Tệp `authen.db` ở gốc repo (dữ liệu dev, bị gitignore) **không được đổi tên**.
