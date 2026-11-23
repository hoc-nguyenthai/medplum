# trivita-fhir

Monorepo Go (một module) của phần backend Medplum viết lại bằng Go: máy chủ FHIR R4 và công cụ dòng lệnh. Nghiệp vụ dùng chung nằm ở `pkg/`, mỗi ứng dụng nằm ở `apps/<tên>` và là **một chương trình độc lập**.

- [Các ứng dụng](#các-ứng-dụng)
- [Nguyên tắc monorepo](#nguyên-tắc-monorepo)
- [Thư viện dùng chung](#thư-viện-dùng-chung-pkg)
- [Bắt đầu nhanh](#bắt-đầu-nhanh)
- [Docker](#docker)
- [Test](#test)
- [Thêm ứng dụng hoặc thư viện](#thêm-ứng-dụng-hoặc-thư-viện)
- [Tài liệu](#tài-liệu)

## Các ứng dụng

| Ứng dụng | Làm gì | Phụ thuộc ngoài | Tài liệu |
|---|---|---|---|
| [`fhir-server`](apps/fhir-server) | Máy chủ FHIR R4 (cổng 8005): CRUD, history, search, patch, batch/transaction, Subscription, bulk export, GraphQL, xác thực JWT, nhiều project | PostgreSQL hoặc SQLite; Redis (tùy chọn) | [README](apps/fhir-server/README.md) |
| [`auth`](apps/auth) | Dịch vụ xác thực và cấp token, port từ Medplum (cổng 8006): đăng nhập, đăng ký và mật khẩu, MFA, OAuth2/OIDC/SMART, đăng nhập qua IdP ngoài. **Còn rà soát bảo mật B9**; trạng thái chi tiết ở [README](apps/auth/README.md), [kế hoạch](docs/plans/auth/README.md) | PostgreSQL (schema `identity` trong database dùng chung với `fhir-server`, hoặc database riêng) hoặc SQLite; Redis từ P5 | [README](apps/auth/README.md) |
| [`ws-gateway`](apps/ws-gateway) | Giữ websocket của Subscription (cổng 8007), tách khỏi `fhir-server`: nhận sự kiện do `fhir-server` khớp và phát qua Redis, kiểm quyền từng người nhận. `fhir-server` chạy `FHIR_WS_MODE=issuer` để cấp token và khớp sự kiện (`docker-compose.ws.yml`); xem [kế hoạch](docs/plans/modules/split-scim-ws-services.md) | Redis (không có database) | [README](apps/ws-gateway/README.md) |
| [`fhir-cli`](apps/fhir-cli) | Client dòng lệnh: read/search/create/update/delete/import/export/reindex | không (chỉ stdlib) | [README](apps/fhir-cli/README.md) |

## Nguyên tắc monorepo

Mỗi `apps/<tên>` là một ứng dụng rõ ràng:

```
apps/<tên>/
├── cmd/<tên>/main.go   điểm vào duy nhất của tiến trình (composition root)
├── internal/           mã riêng của app; app khác KHÔNG được import
├── README.md           endpoint/lệnh, cấu hình, vận hành
└── .env.example        (nếu app đọc biến môi trường)
```

- **Không app nào import app khác.** Cần dùng chung thì đưa phần đó vào `pkg/`.
- **`pkg/` không biết app**, và các thư viện chỉ phụ thuộc nhau theo hướng khai báo trong `architecture_test.go`.
- **Mỗi thư viện trong `pkg/` phải có app dùng.** Thư viện không còn app nào dùng thì chuyển ra khỏi repo (đã làm với `agents`, `llm`, `cache`, `storage`).
- `fhir-cli` chỉ dùng stdlib: không kéo thư viện server vào binary CLI.
- `fhir-server` giữ nghiệp vụ ở `pkg/fhir*`; `internal/` của nó chỉ gắn router vào Echo và đọc cấu hình.

**`architecture_test.go` làm `go test` fail nếu vi phạm**: tầng phụ thuộc của từng app, hướng phụ thuộc giữa các thư viện, app phụ thuộc app khác, app thiếu `cmd/<tên>/main.go`/`internal`/README/quy tắc, và thư viện mới thiếu quy tắc. Đừng nới lỏng; hãy chuyển code về đúng chỗ.

## Thư viện dùng chung (`pkg/`)

| Lib | Việc |
|---|---|
| `core` | ID, `Event`, lỗi nghiệp vụ có `Kind` |
| `messaging` | `Broker` publish/subscribe (memory, Redis Streams at-least-once, thử lại và dead-letter) và `Broadcaster` |
| `database` | pool dùng chung cho `database/sql` và GORM, migration goose (SQLite, PostgreSQL, MySQL) |
| `workflow` | `JobOrchestrator`, `StepExecutor` (inline, Temporal, asynq) |
| `envconfig` | đọc biến môi trường và `.env`, gom lỗi cấu hình một lần |
| `httpserve` | chạy `http.Handler` với vòng đời K8s: readiness, drain, tắt êm, `healthcheck` |
| `fhir` | kiểu Go cho FHIR R4 (sinh bởi `tools/fhirgen`), schema, bảng search parameter |
| `fhirpath` | engine FHIRPath |
| `fhirvalidate` | kiểm tra resource: cấu trúc, kiểu, cardinality, invariant |
| `fhirsearch` | phân tích và đối sánh yêu cầu search FHIR (gồm composite, `_filter`) |
| `fhirrouter` | REST FHIR trên interface `Repository`: interaction, operation, bulk, GraphQL, job |
| `fhirstore` | `Repository` SQL với chỉ mục search (PostgreSQL, SQLite) |
| `fhirsub` | Subscription rest-hook và websocket |
| `fhirauth` | JWT/JWKS, scope SMART, access policy, tách dữ liệu theo project |
| `fhirbinary` | nối `binarystore` với FHIR: nội dung `Binary` ra kho, handler `/storage` (URL ký) |
| `fhircast` | Hub FHIRcast: đồng bộ ngữ cảnh giữa các ứng dụng lâm sàng |
| `binarystore` | kho nội dung nhị phân theo khóa (Binary, DICOM, tệp đính kèm) và ký URL tải lên/xuống |
| `mailer` | cổng gửi thư (đặt lại mật khẩu, xác minh, MFA) |
| `httpsec` | middleware `net/http`: header bảo mật và CORS |
| `safehttp` | HTTP client ra ngoài chống SSRF, dùng khi gọi URL do người dùng cung cấp |
| `wsgroup` | phần dùng chung của endpoint WebSocket: kiểm Origin, giới hạn kết nối, đóng êm |
| `observability` | log JSON, trace, metric OTLP (bản nhúng của `trivita-observability-go`), adapter Echo v4 `echo4obs` |
| `testkit` | PostgreSQL, Redis và image service thật bằng testcontainers; chỉ dùng trong test |

## Bắt đầu nhanh

Yêu cầu: **Go ≥ 1.26**. Không cần CGO, không cần repo nào khác cạnh repo này.

```bash
make help                                          # liệt kê mọi lệnh
FHIR_AUTH_DISABLED=true make run-fhir-server       # :8005, chỉ dev; thật thì đặt FHIR_WRITE_TOKENS hoặc JWKS
make run-auth                                    # :8006, khung auth (health, swagger)
make run-ws-gateway                                # :8007, cần Redis và FHIR_WS_SIGNING_KEY giống fhir-server
make build APP=fhir-cli && bin/fhir-cli help       # build một app bất kỳ vào bin/
```

| Lệnh | Việc |
|---|---|
| `make run APP=<app>` | chạy một app (đọc `apps/<app>/.env` nếu có) |
| `make build APP=<app>` | binary tĩnh vào `bin/` |
| `make migrate MIGRATE=up\|down\|status` | migration của `fhir-server` |
| `make test` / `test-race` / `cover` | test, race detector, coverage |
| `make test-pg` | test PostgreSQL bằng container tạm (cần Docker) |
| `make lint` / `fmt` / `ci` | gofmt + vet / định dạng / những gì CI phải chạy |
| `make swagger APP=<app>` | sinh lại OpenAPI (`fhir-server`, `auth`) |
| `make docker APP=<app>` / `up` / `down` | build image / compose fhir-server + PostgreSQL / dừng và xóa volume |

## Docker

**Mỗi service có Dockerfile riêng** (`apps/auth/Dockerfile`, `apps/fhir-server/Dockerfile`, `apps/ws-gateway/Dockerfile`), build từ **gốc repo**: `docker build -f apps/<app>/Dockerfile -t trivita-<app> .` hoặc `make docker APP=<app>`. Image distroless, chạy non-root. Compose có `fhir-server`, `auth`, `ws-gateway`, `redis` và `postgres` (một database, schema `identity` cho `auth` và `fhir` cho `fhir-server`, tạo bởi `deploy/postgres-init`). Repo không kèm manifest K8s chính thức; riêng `ws-gateway` có bộ ví dụ ở [`deploy/k8s/ws-gateway`](deploy/k8s/ws-gateway) (kustomize, đã kiểm trên cụm thử). Image và compose đã được build và chạy thử (xem `docs/go-port-status.md`, 25a). Mỗi Dockerfile có file ignore riêng cạnh nó (`apps/<app>/Dockerfile.dockerignore`, danh sách cho phép: `go.mod`, `go.sum`, `pkg/` và `apps/<app>`, bỏ file test); container chỉ đọc cấu hình từ `apps/fhir-server/.env` (`--env-file` của compose chỉ dùng để thay biến trong file compose).

## Test

```bash
make test        # go test ./... (gồm test kiến trúc)
make test-race
```

Test mặc định không cần dịch vụ ngoài (SQLite, `miniredis`, `httptest`). **Bộ test PostgreSQL chưa từng được chạy** (cần Docker: `make test-pg`). Xem `docs/go-port-status.md` cho giới hạn đã biết.

## Thêm ứng dụng hoặc thư viện

**Ứng dụng mới**: tạo `apps/<tên>/{cmd/<tên>/main.go,internal,README.md}`, thêm quy tắc tầng vào `rules` trong `architecture_test.go` (test sẽ báo nếu quên), thêm `.env.example` nếu cần; nếu là service thì thêm `apps/<tên>/Dockerfile` (theo mẫu `apps/auth/Dockerfile`) và `Dockerfile.dockerignore` cạnh nó, rồi `make docker APP=<tên>`; danh sách kiểm đầy đủ ở [docs/service-principles.md](docs/service-principles.md) mục 7.

**Thư viện mới**: `pkg/<tên>/` và thêm quy tắc vào `pkgRules` (test sẽ báo nếu quên). Chỉ tạo khi có ít nhất một app dùng.

## Tài liệu

`docs/`: `go-port-status.md` (trạng thái từng giai đoạn), `go-port-checklist.md`, `runbook.md` (vận hành). Thư mục `medplum/` là bản tham chiếu, không commit.
