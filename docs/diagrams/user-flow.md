# User flow

Luồng theo góc nhìn người dùng và hệ thống gọi API. Endpoint và dạng phản hồi lấy từ `server/src/auth/*`, `oauth/*`, `admin/*`; xem [api-contract](../api-contract/README.md).

## 1. Đăng ký và tạo project đầu tiên

```mermaid
flowchart TD
  A[Mở trang đăng ký] --> B[POST /auth/newuser<br/>firstName, lastName, email, password<br/>+ reCAPTCHA]
  B --> C[Nhận login id]
  C --> D[POST /auth/newproject<br/>login, projectName]
  D --> E[Nhận code]
  E --> F[POST /oauth2/token<br/>authorization_code]
  F --> G[Có access_token, vào ứng dụng]
```

Ghi chú: đăng ký mở được bật hoặc tắt bằng cấu hình (`projectRegistrationAllowed`), có kiểm tra reCAPTCHA.

## 2. Đăng nhập (mật khẩu, MFA, chọn profile)

```mermaid
flowchart TD
  A[Nhập email] --> B[POST /auth/method<br/>biết dùng mật khẩu hay Google]
  B --> C[POST /auth/login<br/>email, password, clientId, scope, codeChallenge]
  C --> D{Phản hồi}
  D -- mfaEnrollRequired --> E[Quét QR TOTP<br/>POST /auth/mfa/login-enroll]
  D -- mfaRequired --> F[Nhập mã<br/>POST /auth/mfa/verify]
  D -- memberships --> G[Chọn project và profile<br/>POST /auth/profile]
  D -- code --> H[POST /oauth2/token]
  E --> D
  F --> D
  G --> D
  H --> I[access_token, refresh_token, id_token]
```

Dạng phản hồi chính xác nằm ở [auth.md](../../apps/auth/docs/api-contract/auth.md#luồng-phản-hồi-đăng-nhập).

## 3. Ứng dụng bên thứ ba đăng nhập bằng OAuth2 (authorization code + PKCE)

```mermaid
sequenceDiagram
  participant U as Người dùng
  participant A as Ứng dụng ngoài
  participant S as medplum-server
  A->>U: chuyển hướng tới /oauth2/authorize<br/>client_id, redirect_uri, code_challenge
  U->>S: đăng nhập, đồng ý
  S-->>A: redirect_uri?code=...
  A->>S: POST /oauth2/token (code, code_verifier)
  S-->>A: access_token, id_token
  A->>S: GET /oauth2/userinfo
  S-->>A: thông tin người dùng
```

## 4. Quên mật khẩu

```mermaid
flowchart LR
  A[POST /auth/resetpassword<br/>email + reCAPTCHA] --> B[Email chứa liên kết]
  B --> C[POST /auth/setpassword<br/>id, secret, password]
  C --> D[Đăng nhập lại]
```

## 5. Quản trị viên mời thành viên và cấp quyền

```mermaid
flowchart TD
  A[Admin mở trang Invite] --> B[POST /admin/projects/:projectId/invite]
  B --> C[Server xử lý lời mời<br/>admin/invite.ts]
  C --> D[Người được mời hoàn tất tài khoản]
  D --> E[Admin gán AccessPolicy cho membership<br/>POST /admin/projects/:projectId/members/:id]
```

## 6. Nhà phát triển tạo, triển khai và chạy bot

```mermaid
flowchart TD
  A[POST /admin/projects/:projectId/bot hoặc POST /fhir/R4/Bot/$init] --> B[Viết mã bot]
  B --> C[medplum bot deploy<br/>hoặc POST /fhir/R4/Bot/:id/$deploy]
  C --> D{runtime}
  D -- vmcontext --> E[Chạy trong tiến trình server]
  D -- awslambda --> F[Lambda được tạo hoặc cập nhật]
  D -- fission --> G[Fission function]
  E --> H[POST /fhir/R4/Bot/:id/$execute]
  F --> H
  G --> H
  H --> I[Xem log và kết quả]
```

## 7. Nhân viên y tế tìm và cập nhật bệnh nhân (ứng dụng)

```mermaid
flowchart LR
  A[Đăng nhập] --> B[Tìm Patient<br/>GET /fhir/R4/Patient?name=...]
  B --> C[Mở hồ sơ<br/>GET /fhir/R4/Patient/:id/$everything]
  C --> D[Sửa thông tin<br/>PUT /fhir/R4/Patient/:id]
  D --> E[Subscription tương ứng chạy nền]
```

## 8. Đặt lịch hẹn

```mermaid
flowchart LR
  A[Chọn dịch vụ] --> B[GET /fhir/R4/Appointment/$find]
  B --> C[Giữ chỗ<br/>POST /Appointment/$hold]
  C --> D[Đặt<br/>POST /Appointment/$book]
  D --> E[Xác nhận, hủy hoặc đổi lịch<br/>$confirm, $cancel, $reschedule]
```

## 9. Thiết bị tại chỗ gửi tin HL7 v2 qua Agent

```mermaid
sequenceDiagram
  participant D as Thiết bị / HIS (HL7 v2, MLLP)
  participant AG as medplum-agent
  participant S as medplum-server
  D->>AG: tin HL7 v2
  AG->>S: chuyển tiếp qua WebSocket /ws/agent
  S->>S: bot xử lý, tạo resource FHIR
  S-->>AG: phản hồi (ACK)
  AG-->>D: ACK
```

Ghi chú: luồng bot trong bước giữa phụ thuộc cấu hình `Agent` và `Endpoint`; sơ đồ là mức khái quát.

## 10. Xuất dữ liệu lớn

```mermaid
flowchart LR
  A[Gọi $export] --> B[Nhận URL trạng thái]
  B --> C[Hỏi lại GET /bulkdata/export/:id]
  C --> D[Tải file kết quả]
```
