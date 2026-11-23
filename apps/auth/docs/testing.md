# auth: kiểm thử

Cập nhật 2026-11-23. Bốn tầng test theo [service-independence.md](../../../docs/plans/service-independence.md), cộng hai test bảo vệ tài liệu và kiến trúc. Số lượng dưới đây là số hàm `Test*` đếm được ngày cập nhật, không phải độ phủ.

| Tầng | Ở đâu | Số hàm test | Cần Docker | Chạy |
|---|---|---|---|---|
| Đơn vị | `*_test.go` cạnh mã trong `internal/` | khoảng 640 (cả `bootstrap`, `httpapi`) | Không | `make test-unit APP=auth` |
| PostgreSQL thật | Cùng các test `internal/` khi đặt `AUTH_TEST_POSTGRES_URL` (`store` chạy bộ `storetest` trên mọi backend); `bootstrap/schema_pg_test.go` | (chung với trên) | Có | `make test-auth-pg` |
| Service (image thật) | `apps/auth/tests/e2e/` | 4 | Có | `make test-service APP=auth` |
| Hệ thống (nhiều service) | `e2e/` ở gốc repo | 24 (toàn bộ thư mục, không riêng auth) | Không (build binary, SQLite tạm, miniredis) | `make test-e2e` |

`make test-app APP=auth` chạy mọi tầng của app. Không có Docker thì test cần Docker bị **bỏ qua có thông báo**; CI đặt `TRIVITA_REQUIRE_DOCKER=1` để coi là lỗi.

## 1. Đơn vị

- `domain`: luật thuần (mật khẩu, PKCE, scope SMART, IP rules, vòng đời `UserSecurityRequest`, MFA helper, SCIM).
- `application`: use case trên cổng giả và kho bộ nhớ (đăng nhập, membership, token, refresh, MFA, đăng ký, SCIM, đăng nhập ngoài, email).
- `infrastructure`: `store` có bộ test dùng chung ở `store/storetest` (chạy được trên bộ nhớ, SQLite, PostgreSQL).
- `bootstrap` và `presentation/httpapi`: test HTTP qua Echo (`*_http_test.go`, `oauth_*_test.go`), gồm các test **bảo mật** (`bootstrap/security_controls_test.go`, `application/security_p4_test.go`; README của app gọi bộ cho đăng ký, mật khẩu, thư là S1 đến S17, kế hoạch ở [p4-p5-register-mfa.md](plans/p4-p5-register-mfa.md)) và test khởi động (`container_test.go`).

Cấu hình: `internal/config/config_test.go` (22 hàm) kiểm giá trị mặc định và các ca lỗi của `FromEnv`.

## 2. PostgreSQL thật

`make test-auth-pg` dựng PostgreSQL 16 tạm bằng Docker rồi chạy `go test -race ./apps/auth/...` trên đó (cổng `PG_TEST_PORT`, mặc định 54329; Makefile đặt `AUTH_TEST_POSTGRES_URL`, có thể trỏ vào PostgreSQL sẵn có bằng chính biến này). Mô hình một database nhiều schema được kiểm riêng bằng `make test-db` (script init thật, role, quyền, không FK chéo; `TestStartsOnOwnSchema`, `TestSeparateMigrationRoles` ở `bootstrap`).

## 3. Service (image thật)

`apps/auth/tests/e2e/auth_test.go` dựng chính image từ `apps/auth/Dockerfile` (không cần build context phụ) và kiểm:

| Test | Kiểm gì |
|---|---|
| `TestImageIsHardenedAndHealthcheckWorks` | Chạy non-root, không shell, `HEALTHCHECK` hoạt động |
| `TestSeedTokenAndJWKSInsideTheImage` | Discovery trả đúng issuer và token endpoint, JWKS có khóa, ngay trong image |
| `TestGracefulShutdownExitsZero` | SIGTERM, drain, thoát mã 0 |
| `TestRefusesToStartOnBadConfiguration` | Cấu hình sai thì từ chối khởi động và nói rõ biến nào sai |

## 4. Hệ thống

`make test-e2e` (`TRIVITA_E2E=1 go test ./e2e`) chạy nhiều tiến trình thật. Phần liên quan `auth`:

| Tệp | Phạm vi |
|---|---|
| `auth_fhir_test.go` | `auth` cấp token, `fhir-server` nhận qua JWKS, gọi API FHIR |
| `signup_test.go` | Đăng ký, đặt lại mật khẩu, xác minh email |
| `mfa_test.go` | MFA qua HTTP |
| `external_test.go`, `exchange_google_preauth_test.go` | Đăng nhập ngoài, Google, `exchange`, `preauthorize` |
| `admin_test.go`, `smtp_test.go` | `/admin/projects/*`, gửi thư qua SMTP |
| `fhircast_test.go`, `ws_split_test.go` | `hub.url`/`hub.topic` và quan hệ với `ws-gateway` |

## 5. Test bảo vệ

- **Kiến trúc** (`architecture_test.go`, gốc repo): `go test .` kiểm hướng phụ thuộc giữa các tầng của `auth`, và `pkg/` không được biết app.
- **Hợp đồng API** (`bootstrap` `TestRoutesMatchSwaggerContract`, `docs` `TestAPIContractIsUpToDate`): route phục vụ thật khớp `swagger.json`, và bảng route trong `api-contract.md` khớp `swagger.json` (`UPDATE_DOCS=1 go test ./apps/auth/docs -run TestAPIContractIsUpToDate`).
- **Tài liệu cấu hình** (`internal/config/docs_test.go`): `config.md` được sinh từ `config.go`; thêm hoặc đổi biến mà không cập nhật thì test đỏ. Cập nhật:

```bash
UPDATE_DOCS=1 go test ./apps/auth/internal/config -run TestConfigDocIsUpToDate
```

Biến nào chưa có bình luận ở `config.go` thì mô tả nằm trong `extraDesc` của `docs_test.go`; test báo nếu mục đó thừa hoặc không khớp biến nào.

## 6. Khi thêm tính năng

1. Luật nghiệp vụ mới: test ở `domain` hoặc `application` trước.
2. Route mới: test HTTP ở `bootstrap`/`httpapi`, kèm ca bảo mật (không đăng nhập, sai project, sai scope, hạn mức) nếu route công khai.
3. Biến cấu hình mới: thêm vào `config.go` kèm bình luận trên trường, thêm ca lỗi vào `config_test.go`, chạy lệnh sinh `config.md` ở trên.
4. Đổi annotation swagger: `make swagger APP=auth` để sinh lại `docs.go`, `swagger.json`, `swagger.yaml`.
