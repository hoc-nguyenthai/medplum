# Auth API

> **Trạng thái: CHƯA CÀI trong fhir-server Go.** Tài liệu này mô tả server Medplum bản TypeScript gốc, chưa phải hợp đồng của server đang chạy. Route Go thật xem `fhir-server-go.md` và `/swagger/index.html`. DTO request/response của auth (đã đối chiếu source Medplum): [auth-dto.md](auth-dto.md). Không viết DTO cho các route dưới đây cho tới khi chúng được port sang Go.

Gốc: `/auth/`. Nguồn: `server/src/auth/routes.ts`.

| Method | Path | Xác thực |
|---|---|---|
| POST | `/auth/changepassword` | Bearer |
| GET | `/auth/clientinfo/:clientId` | không guard ở route (xem middleware cha) |
| POST | `/auth/exchange` | không guard ở route (xem middleware cha) |
| GET | `/auth/external` | không guard ở route (xem middleware cha) |
| GET | `/auth/external/:clientId` | không guard ở route (xem middleware cha) |
| POST | `/auth/google` | không guard ở route (xem middleware cha) |
| POST | `/auth/login` | không guard ở route (xem middleware cha) |
| GET | `/auth/login/:login` | không guard ở route (xem middleware cha) |
| GET | `/auth/me` | Bearer |
| POST | `/auth/method` | không guard ở route (xem middleware cha) |
| POST | `/auth/mfa/disable` | Bearer |
| POST | `/auth/mfa/enroll` | Bearer |
| POST | `/auth/mfa/login-enroll` | không guard ở route (xem middleware cha) |
| POST | `/auth/mfa/send-email` | không guard ở route (xem middleware cha) |
| POST | `/auth/mfa/send-email-challenge` | Bearer |
| GET | `/auth/mfa/status` | Bearer |
| POST | `/auth/mfa/verify` | không guard ở route (xem middleware cha) |
| POST | `/auth/newpatient` | không guard ở route (xem middleware cha) |
| POST | `/auth/newproject` | không guard ở route (xem middleware cha) |
| POST | `/auth/newuser` | không guard ở route (xem middleware cha) |
| POST | `/auth/preauthorize` | Bearer |
| POST | `/auth/profile` | không guard ở route (xem middleware cha) |
| POST | `/auth/resetpassword` | không guard ở route (xem middleware cha) |
| POST | `/auth/revoke` | Bearer |
| POST | `/auth/scope` | không guard ở route (xem middleware cha) |
| POST | `/auth/setpassword` | không guard ở route (xem middleware cha) |
| POST | `/auth/verifyemail` | không guard ở route (xem middleware cha) |

## Body theo endpoint (trích từ validator)

Bảng chỉ liệt kê trường có khai báo validator; handler có thể đọc thêm. Ví dụ `/auth/login` còn nhận `clientId`, `scope`, `nonce`, `codeChallenge`, `codeChallengeMethod`, `launch`, `resourceType`, `remember` (xem `auth/login.ts`); các route có `validateRecaptcha` (`newuser`, `resetpassword`) còn đọc `recaptchaSiteKey` và token reCAPTCHA từ body (xem `validateRecaptcha` trong `auth/utils.ts`).

### POST `/auth/method`

| Trường | |
|---|---|
| `email` | bắt buộc |

### POST `/auth/newuser`

| Trường | |
|---|---|
| `firstName` | bắt buộc |
| `lastName` | bắt buộc |
| `email` | bắt buộc |
| `password` | bắt buộc |

### POST `/auth/newproject`

| Trường | |
|---|---|
| `login` | bắt buộc |
| `projectName` | bắt buộc |

### POST `/auth/newpatient`

| Trường | |
|---|---|
| `login` | bắt buộc |
| `projectId` | bắt buộc |

### POST `/auth/login`

| Trường | |
|---|---|
| `email` | bắt buộc |
| `projectId` | tùy chọn |
| `password` | bắt buộc |
| `redirectUri` | tùy chọn |

### POST `/auth/profile`

| Trường | |
|---|---|
| `login` | bắt buộc |
| `profile` | bắt buộc |

### POST `/auth/scope`

| Trường | |
|---|---|
| `login` | bắt buộc |
| `scope` | bắt buộc |

### POST `/auth/changepassword`

| Trường | |
|---|---|
| `oldPassword` | bắt buộc |
| `newPassword` | bắt buộc |

### POST `/auth/resetpassword`

| Trường | |
|---|---|
| `email` | bắt buộc |

### POST `/auth/setpassword`

| Trường | |
|---|---|
| `id` | bắt buộc |
| `secret` | bắt buộc |
| `password` | bắt buộc |

### POST `/auth/verifyemail`

| Trường | |
|---|---|
| `id` | bắt buộc |
| `secret` | bắt buộc |

### POST `/auth/google`

| Trường | |
|---|---|
| `googleClientId` | bắt buộc |
| `googleCredential` | bắt buộc |
| `redirectUri` | tùy chọn |

### POST `/auth/exchange`

| Trường | |
|---|---|
| `externalAccessToken` | bắt buộc |
| `clientId` | bắt buộc |

### POST `/auth/revoke`

| Trường | |
|---|---|
| `loginId` | bắt buộc |

### POST `/auth/preauthorize`

| Trường | |
|---|---|
| `clientId` | bắt buộc |
| `scope` | tùy chọn |
| `nonce` | tùy chọn |
| `expiresIn` | tùy chọn |

## Luồng phản hồi đăng nhập

Nguồn: `sendLoginResult` trong `server/src/auth/utils.ts`. `/auth/login` (và các bước sau) trả một trong các dạng:

| Tình huống | Phản hồi |
|---|---|
| Project yêu cầu MFA nhưng user chưa đăng ký | `{ login, mfaEnrollRequired: true, enrollUri, enrollQrCode, allowedMfaMethods }` |
| User đã đăng ký MFA, chưa xác minh | `{ login, mfaRequired: true, mfaMethods, email }` (gọi `/auth/mfa/...` để xác minh) |
| Đang tạo project mới (`projectId = new`) | `{ login }` rồi gọi `/auth/newproject` |
| Chỉ có một profile | `{ login, code }` (kèm cookie đăng nhập với client OAuth) |
| Nhiều profile | `{ login, memberships: [{ id, project, profile, identifier }] }` rồi gọi `/auth/profile` |

Sau đó đổi `code` lấy token ở `POST /oauth2/token` (`grant_type=authorization_code`, kèm `code_verifier` nếu đã gửi `codeChallenge`).
