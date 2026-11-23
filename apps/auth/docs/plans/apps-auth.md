# Kế hoạch port `apps/auth` từ Medplum

Trạng thái: **bản nháp chờ duyệt**. Chưa có code. Kế hoạch chi tiết từng pha và bản tổng hợp (lộ trình đã chỉnh, sổ quyết định): [auth/README.md](README.md). Khác với bản này: **P5 cần Redis**, và có thêm bước 7.0 (CLI vận hành) ngay sau P1. Nguồn tham chiếu: `/Users/hocnt84/Downloads/medplum/packages/server/src/{auth,oauth}` (auth khoảng 15,7k dòng gồm test; oauth khoảng 12,3k dòng gồm test). Hợp đồng đã đối chiếu: `apps/auth/docs/api-contract/auth-dto.md`.

## 1. Mục tiêu và phạm vi

Mục tiêu: một app Go `apps/auth` cấp danh tính (đăng nhập, token, MFA, đăng ký) theo hành vi của Medplum, để `fhir-server` xác thực người dùng bằng token do chính `auth` phát hành.

| Nhóm | Route | Trong phạm vi |
|---|---|---|
| Đăng nhập | `/auth/login`, `profile`, `scope`, `method`, `login/{id}`, `me`, `revoke` | Có (P2) |
| OAuth2/OIDC | `/oauth2/token`, `authorize`, `userinfo`, `introspect`, `logout`, `register`, `/.well-known/*` | Có (P3) |
| Đăng ký, mật khẩu, email | `newuser`, `newproject`, `newpatient`, `resetpassword`, `setpassword`, `verifyemail`, `changepassword` | Có (P4) |
| MFA | `/auth/mfa/*` | Có (P5) |
| Đăng nhập ngoài | `external`, `google`, `exchange`, `clientinfo`, `preauthorize` | Sau (P6) |
| Quản trị project | `/admin/projects/*`, `/admin/super/*` | **Ngoài phạm vi** đợt này (P7, quyết định sau) |
| Rate limit, audit, Redis | | Dùng `pkg/httpserve` và observability hiện có; tách việc riêng |

## 2. Cái đã có, tái dùng được

| Thành phần | Nơi | Dùng để |
|---|---|---|
| Kiểu FHIR của Medplum: `User`, `Login`, `ProjectMembership`, `Project`, `ClientApplication`, `AccessPolicy`, `UserSecurityRequest`, `DomainConfiguration`, `UserConfiguration` | `pkg/fhir/resource_*.go` | Mô hình dữ liệu y hệt Medplum, không phải định nghĩa lại |
| Repository FHIR (SQL, bộ nhớ) và router | `pkg/fhirstore`, `pkg/fhirrouter` | Lưu và truy vấn các resource trên |
| Verifier JWT (JWKS, `iss`, `aud`, claim `fhir_project`, `fhir_access_policy`, scope) | `pkg/fhirauth` | `fhir-server` đã sẵn sàng nhận token từ `auth`; `auth` chỉ cần phát token đúng claim |
| Khung app: Echo, config, bootstrap, swaggo, kiến trúc có test | `apps/fhir-server`, `architecture_test.go`, `Makefile` | Mẫu cho `apps/auth` |
| `go-jose`, `golang-jwt` | `go.mod` | Ký và xác minh JWT |
| OIDC engine (PKCE, xoay refresh token, phát hiện dùng lại code, client_credentials, JWT profile) | `../echo-oauth2-server` | **Ứng viên** cho P3, xem quyết định D1 |
| Identity provider Go đang chạy (login/consent, bảng user riêng) | `../trivita-identity-go` | Tham khảo cách làm, **không** cùng mô hình dữ liệu với Medplum |

Cần thêm: `bcrypt` (`golang.org/x/crypto`), TOTP (ví dụ `pquerna/otp`), kiểm tra mật khẩu rò rỉ (HIBP k-anonymity), gửi email (cổng nối, chưa có trong repo), reCAPTCHA.

## 3. Quyết định cần chốt trước khi viết code

| # | Quyết định | Khuyến nghị | Lý do và cái giá |
|---|---|---|---|
| D1 | Engine OAuth2: tự port flow của Medplum hay bọc `echo-oauth2-server` | **Tự port**, mượn ý tưởng bảo mật của echo-oauth2-server (xoay refresh token, phát hiện dùng lại code, claim nguyên tử) | Flow Medplum không chuẩn: `/auth/login` trả `code`, rồi `/oauth2/token` đổi code; trạng thái nằm trong resource `Login`. Bọc engine ngoài buộc ánh xạ hai mô hình khác nhau. Cái giá: phải tự cài và tự test phần token (khoảng 1200 dòng `token.ts`) |
| D2 | Lưu trữ: resource FHIR qua `pkg/fhirstore` hay bảng SQL riêng | **`pkg/fhirstore`**, tách khỏi kho dữ liệu lâm sàng (schema hoặc database riêng) | Khớp mô hình Medplum, tái dùng `pkg/fhir`. Cái giá: `User` chứa `passwordHash`, `Login` chứa `code` và `refreshSecret`; **không** được lộ qua `/fhir/R4` của `fhir-server` (Medplum chỉ cho super admin). Phải có test chặn |
| D3 | Có làm `/admin/*` (mời user, tạo bot/client, quản lý member) | **Không**, đợt sau | Nhỏ về logic nhưng nhiều về bề mặt; client OAuth tạm tạo bằng seed hoặc CLI |
| D4 | Gửi email (reset mật khẩu, xác minh, mã MFA) | Định nghĩa cổng `Mailer`, adapter đầu tiên là SMTP hoặc log | Repo chưa có dịch vụ email; chốt nhà cung cấp sau |
| D5 | Super admin và nhiều project (`projectId`, `new`, `Project/new`) | **Giữ** tối thiểu: `projectId` tùy chọn và `new` | `auth-dto.md` đã mô tả; bỏ sẽ làm lệch hợp đồng với client Medplum |
| D6 | Loại token | JWT RS256, claim tương thích `pkg/fhirauth`; JWKS ở `/.well-known/jwks.json` | `fhir-server` bật `JWKSURL`, `Issuer`, `Audience` là chạy |

## 4. Kiến trúc đích

Theo kiểu `apps/fhir-server`, thêm tầng domain vì có nghiệp vụ thật:

```
apps/auth/
  cmd/auth/main.go
  docs/                      # swaggo sinh ra (make swagger)
  internal/
    config/                  # envconfig: issuer, audience, base URL, key path, bật/tắt đăng ký
    domain/                  # luật thuần: LoginResult, chính sách mật khẩu, MFA, scope, PKCE; không import hạ tầng
    application/             # use case: Login, SelectProfile, Register, ResetPassword, VerifyMFA, IssueTokens...
      ports.go               # cổng: Store, Mailer, Pwned, Clock, KeyStore, Recaptcha
    infrastructure/          # adapter: fhirstore, bcrypt, totp, smtp, hibp, jwks/keys
    presentation/httpapi/    # Echo, validator, DTO, swagger, error mapping (OperationOutcome)
    bootstrap/               # lắp ghép, migrate, e2e test
```

Việc kèm theo:
- Thêm luật vào `architecture_test.go` cho `apps/auth/internal/*` (domain không import hạ tầng; presentation chỉ gọi application).
- Cùng `Makefile`: `make run APP=auth`, `make swagger APP=auth`, `docker-compose.yml`, `Dockerfile`.
- Lỗi trả `OperationOutcome` như Medplum (`allOk`, `badRequest`...) để client Medplum không đổi.

## 5. Lộ trình theo pha

Mỗi pha kết thúc bằng: test xanh, swagger khớp `auth-dto.md`, và hợp đồng được cập nhật. Cỡ: S là dưới 1 ngày, M là 2 đến 4 ngày, L là 1 đến 2 tuần (một người, ước lượng thô).

| Pha | Nội dung | Nguồn Medplum cần port | Cỡ |
|---|---|---|---|
| P0 | Chốt D1 đến D6; dựng khung `apps/auth` (config, health, swagger, kiến trúc test, Dockerfile, Makefile) | `auth/routes.ts` | S |
| P1 | Lõi dữ liệu và an toàn: repository cho `User`, `Login`, `ProjectMembership`, `Project`, `ClientApplication`, `UserSecurityRequest`, `DomainConfiguration`; bcrypt, sinh secret, so sánh thời gian cố định, kiểm tra pwned; chặn lộ qua `/fhir/R4` | `oauth/utils.ts` (phần user, membership), `auth/securityrequest.ts` | M |
| P2 | Đăng nhập lõi: `tryLogin`, `sendLoginResult` (5 dạng `LoginResult`), `/auth/login`, `profile`, `scope`, `method`, `login/{id}`, `revoke`, `me` | `auth/{login,profile,scope,method,status,revoke,me}.ts`, `utils.ts` | L |
| P3 | Token và OAuth2: quản lý khóa và JWKS, `/oauth2/token` (authorization_code + PKCE, refresh_token, client_credentials, pre-authorized_code), `authorize`, `userinfo`, `introspect`, `logout`, `.well-known/*`; **tích hợp `fhir-server`** | `oauth/{token,authorize,keys,cert,userinfo,introspect,logout,middleware}.ts` | L |
| P4 | Đăng ký và mật khẩu: `newuser`, `newproject`, `newpatient`, `resetpassword`, `setpassword`, `verifyemail`, `changepassword`, reCAPTCHA, cổng `Mailer` | `auth/{newuser,newproject,newpatient,resetpassword,setpassword,verifyemail,changepassword,securityrequest,welcomeemail}.ts` | M |
| P5 | MFA: TOTP, email MFA, giới hạn thử, `/auth/mfa/*` | `auth/{mfa,mfalimit}.ts`, `verifyMfaToken` | M |
| P6 | Đăng nhập ngoài: `external`, `google`, `exchange`, `clientinfo`, `preauthorize` | `auth/{external,google,exchange,clientinfo,preauthorize}.ts` | M đến L |
| P7 | (Tùy chọn) `/admin/*` | `admin/*.ts` | L |

Thứ tự phụ thuộc: P0 → P1 → P2 → P3; P4 và P5 cần P2; P6 cần P3. Mốc dùng được tối thiểu: **hết P3** (đăng nhập bằng mật khẩu và cấp token cho `fhir-server`), với tài khoản và client tạo bằng seed.

## 6. Chiến lược kiểm thử

- Medplum có sẵn file test cho từng handler (`login.test.ts`, `token.test.ts`, `mfa.test.ts`...). **Dùng chúng làm đặc tả**: port từng ca test sang Go (bảng ca test), giữ nguyên tên ca để đối chiếu.
- Mỗi endpoint: test thành công, từng ca lỗi `400` đã liệt kê trong `auth-dto.md`, và ca biên (độ dài mật khẩu 8 và 72 byte, UUID sai, secret hết hạn).
- E2E (kiểu `apps/fhir-server/internal/bootstrap/e2e_test.go`): đăng ký → đăng nhập → `code` → `token` → gọi `fhir-server` bằng token đó.
- Hợp đồng: test so khớp response với schema swagger sinh ra.
- Bảo mật bắt buộc có test: không lộ `passwordHash`, `Login.code`, `refreshSecret` qua `/fhir/R4`; `code` chỉ dùng một lần; refresh token bị dùng lại thì thu hồi; đặt mật khẩu mới thu hồi mọi login; hạn mức thử MFA.
- Chất lượng: theo chuẩn repo (`go test -race`, SonarQube coverage như các service Trivita khác).

## 7. Rủi ro

| Rủi ro | Giảm thiểu |
|---|---|
| Lộ dữ liệu nhạy cảm qua FHIR API khi dùng chung `pkg/fhirstore` | Kho riêng (D2) và test chặn ở P1 |
| Phần token là lớp quan trọng về an ninh, dễ sai chi tiết (PKCE, reuse, scope) | Port test của Medplum, mượn kiểm thử đua (race) từ echo-oauth2-server, review bảo mật trước khi mở ra ngoài |
| Medplum đổi phiên bản làm lệch hành vi | Ghi rõ phiên bản Medplum đã port vào `docs/UPSTREAM.md` |
| Phạm vi phình (admin, external, SMART) | Cắt cứng theo bảng mục 1; mọi việc ngoài bảng cần quyết định riêng |
| Phụ thuộc dịch vụ ngoài (HIBP, reCAPTCHA, email) khi test | Cổng và fake trong `application/ports.go`; không gọi mạng trong test |

## 8. Việc kế tiếp (sau khi duyệt)

1. Chốt D1 đến D6 (đặc biệt D1, D2, D3).
2. Làm P0 và P1 trong một PR đầu, kèm test kiến trúc.
3. Đọc nốt phần chưa đọc trong `auth-dto.md` (`external.ts`, `mfalimit.ts`, `exchangeExternalAuthToken`) trước khi vào P5 và P6; đọc `oauth/token.ts` và `oauth/utils.ts` trước khi vào P3 để bổ sung `oauth.md` thành hợp đồng chắc chắn.
