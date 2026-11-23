# fhir-server: kiểm thử

Cập nhật 2026-11-23. Các tầng test theo [service-independence.md](../../../docs/plans/service-independence.md), cộng các test bảo vệ tài liệu và hợp đồng. Số lượng là số hàm `Test*` đếm được ngày cập nhật, không phải độ phủ.

| Tầng | Ở đâu | Số hàm test | Cần Docker | Chạy |
|---|---|---|---|---|
| Đơn vị (app) | `*_test.go` cạnh mã trong `internal/` | khoảng 100 | Không | `make test-unit APP=fhir-server` |
| Nghiệp vụ FHIR | `pkg/fhir*` (router, store, search, validate, auth, sub, binary) | nhiều trăm | Không (PostgreSQL tùy chọn) | `go test ./pkg/...` |
| PostgreSQL thật | Cùng các test khi đặt `FHIRSTORE_TEST_POSTGRES_URL` | (chung với trên) | Có | `make test-pg` |
| Service (image thật) | `apps/fhir-server/tests/e2e/` | 4 | Có | `make test-service APP=fhir-server` |
| Hệ thống (nhiều service) | `e2e/` ở gốc repo | 24 (toàn bộ thư mục) | Không (build binary, SQLite tạm, miniredis) | `make test-e2e` |

`make test-app APP=fhir-server` chạy mọi tầng của app. Không có Docker thì test cần Docker **bị bỏ qua có thông báo**; CI đặt `TRIVITA_REQUIRE_DOCKER=1` để coi là lỗi.

## 1. Đơn vị và e2e trong tiến trình (`internal/`)

Phần lớn nằm ở `internal/bootstrap` (dựng `Container` thật trên SQLite bộ nhớ hoặc PostgreSQL rồi gọi HTTP qua `httptest`):

| Tệp | Phạm vi |
|---|---|
| `e2e_test.go` | CRUD, search, history, batch/transaction, GraphQL, operation, health |
| `jwt_e2e_test.go` | JWT qua JWKS và HMAC, scope, tách project, AccessPolicy, super-admin |
| `smartv2_e2e_test.go` | Scope SMART v2 |
| `batch_async_e2e_test.go`, `replicas_e2e_test.go` | Batch bất đồng bộ và checkpoint; nhiều replica tranh job |
| `ws_e2e_test.go`, `ws_issuer_test.go` | Subscription websocket nhúng và chế độ issuer |
| `storage_e2e_test.go`, `storage_s3_test.go`, `sweep_cli_test.go` | Kho Binary (`file:`, `s3:`), URL ký, quét mồ côi |
| `httpsec_e2e_test.go` | Header bảo mật, CORS, hạn mức |
| `pg_test.go`, `schema_pg_test.go` | Chạy trên PostgreSQL thật khi có `FHIRSTORE_TEST_POSTGRES_URL` (mỗi test tạo và xóa schema riêng); schema riêng và role migrate |
| `contract_routes_test.go` | Route Echo ngoài `/fhir/R4` khớp `docs/swagger.json` (xem mục 4) |

Cấu hình: `internal/config/config_test.go` và `httpsec_test.go` kiểm giá trị mặc định và các ca lỗi của `FromEnv`.

## 2. PostgreSQL thật

`make test-pg` dựng PostgreSQL 16 và Redis 7 tạm bằng Docker rồi chạy bộ test store, auth, router, sub, messaging, fhircast và `apps/fhir-server/...` trên đó. Hoặc đặt sẵn `FHIRSTORE_TEST_POSTGRES_URL` trỏ tới một PostgreSQL có sẵn rồi chạy `go test ./pkg/fhirstore/... ./apps/fhir-server/...`. Mô hình một database nhiều schema kiểm riêng bằng `make test-db` (script init thật, role, quyền, không FK chéo).

## 3. Service (image thật)

`apps/fhir-server/tests/e2e/fhir_test.go` dựng chính image từ `apps/fhir-server/Dockerfile` và kiểm:

| Test | Kiểm gì |
|---|---|
| `TestImageIsHardenedAndHealthcheckWorks` | Chạy non-root, không shell, `HEALTHCHECK` hoạt động |
| `TestServesFHIRFromTheImage` | Image phục vụ API FHIR thật |
| `TestGracefulShutdownExitsZero` | SIGTERM, drain, thoát mã 0 |
| `TestRefusesToStartOnBadConfiguration` | Cấu hình sai thì từ chối khởi động và nói rõ biến nào sai |

## 4. Test bảo vệ

- **Kiến trúc** (`architecture_test.go`, gốc repo): `go test .` kiểm hướng phụ thuộc của `internal/presentation` và `internal/config`, và `pkg/` không được biết app.
- **Hợp đồng API** (`docs/contract_test.go`, `internal/bootstrap/contract_routes_test.go`): `TestRoutesMatchSwaggerContract` so route Echo thật với `swagger.json`; `TestAPIContractIsUpToDate` giữ bảng route trong `api-contract.md` khớp spec (`UPDATE_DOCS=1 go test ./apps/fhir-server/docs -run TestAPIContractIsUpToDate`); `TestSwaggerDTOsAreComplete` và `TestSwaggerIsStructurallyValid` kiểm DTO đủ mô tả/`required`/ví dụ, tham số path, `$ref`, `produces` đúng Content-Type thật. **Giới hạn:** router FHIR (`/fhir/R4/...`) gắn bằng wildcard nên Echo không liệt kê được từng route; phần đó chỉ đúng nhờ annotation và test của `pkg/fhirrouter`.
- **Tài liệu cấu hình** (`internal/config/docs_test.go`): `config.md` được sinh từ `config.go`; thêm hoặc đổi biến mà không cập nhật thì test đỏ. Cập nhật:

```bash
UPDATE_DOCS=1 go test ./apps/fhir-server/internal/config -run TestConfigDocIsUpToDate
```

  Biến chưa có bình luận ở `config.go` thì mô tả nằm trong `extraDesc` của `docs_test.go`; test báo nếu mục đó thừa hoặc không khớp biến nào.
- **`.env.example`** (cùng test trên): mọi biến của `config.go` phải có dòng `VAR=` hoặc `# VAR=` trong `apps/fhir-server/.env.example`.

## 5. Khi thêm tính năng

1. Nghiệp vụ FHIR mới: test ở `pkg/` tương ứng trước (router, search, store, validate), có ca dùng chung chạy trên mọi backend (`repotest`).
2. Route mới ngoài `/fhir/R4`: annotation ở `internal/presentation/httpapi/swagger.go`, rồi `make swagger APP=fhir-server`.
3. Biến cấu hình mới: bình luận trên trường ở `config.go`, ca lỗi ở `config_test.go`, dòng ở `.env.example`, rồi chạy lệnh sinh `config.md` ở trên.
4. Ca cần PostgreSQL hoặc Redis thật: đặt vào `internal/bootstrap` với điều kiện bỏ qua khi thiếu `FHIRSTORE_TEST_POSTGRES_URL`, hoặc dùng `pkg/testkit` (testcontainers) ở `tests/`.
