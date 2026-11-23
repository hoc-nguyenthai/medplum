# P0 — Dựng khung `apps/auth`

Thuộc kế hoạch tổng [apps-auth.md](apps-auth.md). Pha này **không có nghiệp vụ**: chỉ dựng khung chạy được, có test kiến trúc, swagger rỗng, health và cấu hình, để các pha sau chỉ thêm code vào chỗ đã định.

## 1. Mục tiêu và tiêu chí hoàn thành

`make run APP=auth` chạy được; `GET /health/live`, `/health/ready` trả 200; `/swagger/index.html` mở được; `go test ./...` xanh gồm test kiến trúc mới; `docker build --build-arg APP=auth` thành công; không có route nghiệp vụ nào.

## 2. Cái có sẵn để bắt chước (đã kiểm tra trong repo)

| Việc | Mẫu trong `fhir-server` |
|---|---|
| Cấu hình từ env và `.env` | `apps/fhir-server/internal/config/config.go` (335 dòng): `Settings`, `envconfig`, cấm cấu hình nguy hiểm ở `staging` và `production` |
| Lắp ghép | `internal/bootstrap/container.go` (341 dòng): `Container`, `onStop`, `Readiness`, `Serve`, `ServeOptions` |
| Vòng đời HTTP (graceful shutdown, drain, healthcheck) | `pkg/httpserve` (`httpserve.HealthCheck`, tham số `SHUTDOWN_*`) |
| Entry point và lệnh con | `cmd/fhir-server/main.go`: `healthcheck`, `migrate`, quan sát qua `echo4obs` |
| Swagger | `internal/presentation/httpapi/swagger.go`, `.swaggo`, `make swagger` (đã làm ở phần fhir-server) |
| Docker | `Dockerfile` đã tham số hóa `ARG APP`; `docker-compose.yml` có 1 service mỗi app |
| Test kiến trúc | `architecture_test.go`: `rules` (theo thư mục), `pkgRules`, `TestEchoAndGormStayInTheirLayers` |

## 3. Cấu trúc thư mục tạo ra

```
apps/auth/
  README.md
  .env.example
  cmd/auth/main.go                      # serve | healthcheck | migrate up|down|status
  docs/                                   # swaggo (make swagger APP=auth)
  internal/
    config/config.go (+ _test)
    domain/doc.go                         # chỗ trống có package doc, chưa có kiểu
    application/ports.go                  # khai báo rỗng: Clock, Mailer... thêm dần ở các pha sau
    infrastructure/                       # rỗng, kèm doc.go
    presentation/httpapi/{httpapi.go,health.go,swagger.go,errors.go}
    bootstrap/{container.go,cli.go,e2e_test.go}
```

Quy ước đặt tên theo `trivita-identity-go` và `fhir-server`. `domain`, `application`, `infrastructure` để `doc.go` trống ở P0 để test kiến trúc có thư mục mà kiểm tra.

## 4. Các bước (mỗi bước là một commit review được)

| Bước | Việc | Tiêu chí hoàn thành |
|---|---|---|
| 0.1 | `config`: `Settings` gồm `AppName=auth`, `Addr`, `Environment`, `Debug`, thời gian shutdown và readiness, `DatabaseURL`, `Issuer`, `Audience`, `BaseURL`, đường dẫn khóa ký (chỉ khai báo trường, chưa dùng), cờ đăng ký (`RegisterEnabled`, `NewProjectRegistrationEnabled`). Kiểm tra: `staging` và `production` cấm SQLite, cấm `Debug` | Test bảng: giá trị mặc định, lỗi cấu hình, ràng buộc theo `Environment` |
| 0.2 | `bootstrap`: `Container` rỗng, `Readiness` (chỉ `accepting_traffic` tới khi có DB), `Serve` dùng `pkg/httpserve` | Khởi động và dừng đúng; SIGTERM làm `ready` về 503 rồi mới tắt |
| 0.3 | `presentation/httpapi`: Echo, `Recover`, `RequestID`, `BodyLimit`, `/health/{startup,live,ready}`, error handler trả `OperationOutcome` cho `/auth` và `/oauth2`, JSON `{"error"}` cho phần còn lại (theo cùng quy ước với `fhir-server`) | Test httptest cho ba route health và định dạng lỗi |
| 0.4 | `cmd/auth/main.go`: `healthcheck`, in cách dùng khi lệnh sai; gắn `echo4obs` như `fhir-server` | `auth healthcheck` thoát 0 khi server chạy |
| 0.5 | Swagger: `swagger.go` với `@title Trivita Auth API`, `@securityDefinitions` Bearer; `/swagger/*`. Quy tắc: chỉ bật ở `local`, `development`, `test` (khác `fhir-server` đang bật mọi nơi, xem mục 6) | Spec sinh được; ở `production` route `/swagger/*` trả 404 |
| 0.6 | `architecture_test.go`: thêm luật cho `apps/auth/internal/{domain,application,infrastructure,presentation,bootstrap,config}` (xem mục 5) | Test kiến trúc xanh; cố tình import sai thì đỏ |
| 0.7 | `Makefile`: `run-auth` (cổng `:8006`), `swagger` nhận `APP`; `docker-compose.yml`: service `auth` (cổng `${AUTH_PORT:-8006}`) chung `postgres` với database riêng; `.env.example`; `README.md` | `make build APP=auth`, `docker build --build-arg APP=auth` thành công |
| 0.8 | Cập nhật `README.md` gốc, `docs/api-contract/README.md` (thêm dòng `apps/auth`), `docs/UPSTREAM.md` (ghi phiên bản Medplum đã đối chiếu) | Tài liệu khớp thực tế |

## 5. Luật kiến trúc cần thêm (đề xuất cho bước 0.6)

| Tầng | Được import nội bộ | Bên thứ ba |
|---|---|---|
| `domain` | chỉ chính nó, `pkg/fhir` | không có (chỉ stdlib) |
| `application` | `domain`, `pkg/fhir` | không có (chỉ stdlib) |
| `infrastructure` | `domain`, `application`, `pkg/fhir`, `pkg/fhirstore`, `pkg/database` | tùy ý (adapter): bcrypt, TOTP, JWT, gorm... |
| `presentation` | `application`, `domain`, `apps/auth/docs`, `pkg/fhir` | `labstack/echo`, `swaggo/echo-swagger` |
| `config` | `pkg/envconfig` | không có |
| `bootstrap` | tất cả của auth, `pkg/*` | tùy ý (composition root) |

Cần mở rộng `TestEchoAndGormStayInTheirLayers`: `gorm.io` được dùng trong `apps/auth/internal/infrastructure` (hiện chỉ cho `apps/fhir-server`). Thêm test: `domain` và `application` không import `infrastructure` hay `presentation`.

## 6. Quyết định nhỏ cần chốt trong P0

| # | Câu hỏi | Đề xuất |
|---|---|---|
| Q1 | Swagger bật ở mọi môi trường như `fhir-server` hay chỉ dev | Chỉ dev và test, vì `auth` có route nhạy cảm; đồng thời hỏi lại có nên siết cả `fhir-server` |
| Q2 | Cổng mặc định | `:8006` (fhir-server dùng `:8005`) |
| Q3 | Database của `auth`: chung server PostgreSQL với fhir-server hay riêng | Chung server, **database riêng** (và tài khoản DB riêng), để không lộ `User` và `Login` qua kết nối của `fhir-server` (D2) |
| Q4 | Có để `migrate` trong `auth` hay dùng chung | Có, cùng cơ chế goose như `fhir-server`; chỉ thêm bảng ở P1 |
| Q5 | Thư mục `docs/` của app chứa swagger sinh ra | Giống `apps/fhir-server/docs`; `go:generate` không dùng, chạy `make swagger` |

## 7. Rủi ro

| Rủi ro | Giảm thiểu |
|---|---|
| Khung chép từ `fhir-server` mang theo cấu hình không cần (token tĩnh, FHIR base URL) | Chỉ chép cơ chế (vòng đời, readiness, quy ước lỗi), không chép `Settings` của FHIR |
| Test kiến trúc quá lỏng hoặc quá chặt làm chặn P1 | Viết luật theo mục 5 và thử bằng một import sai cố ý trước khi merge |
| Đổi `Dockerfile` hoặc `Makefile` làm hỏng build của `fhir-server` | Chạy lại `make build APP=fhir-server` và `docker build` của `fhir-server` ở bước 0.7 |

## 8. Ước lượng

Cỡ S (dưới 1 ngày). Thứ tự: 0.1 → 0.2 → 0.3 → 0.4 → 0.5 → 0.6 → 0.7 → 0.8; các bước 0.5 và 0.6 độc lập nhau.
