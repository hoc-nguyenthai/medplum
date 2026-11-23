# server/auth

> Đăng ký, đăng nhập, MFA, đổi/đặt lại mật khẩu, Google, token exchange, profile, scope.

| | |
|---|---|
| Đường dẫn hiện tại | `medplum/packages/server/src/auth` |
| Đường dẫn đích | `apps/medplum-server/src/auth` |
| Số file nguồn (không tính test) | 26 |
| Cụm | danh tính |

## Phụ thuộc (số import)

- Gọi tới: `fhir` (48), `oauth` (26), `(root)` (23), `util` (18), `config` (10), `email` (6), `fhir/operations` (2)
- Được gọi bởi: `fhir/operations` (8), `oauth` (7), `admin` (7), `(root)` (2), `scim` (2), `ws` (1)

## Route

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

## Ghi chú tách

Cụm danh tính (`auth`, `oauth`, `admin`, `scim`): phụ thuộc `fhir` nặng. Giai đoạn 1 chuyển cùng server; giai đoạn 2 có thể thay bằng service Go identity.

## File chính

- `auth/changepassword.ts`
- `auth/clientinfo.ts`
- `auth/exchange.ts`
- `auth/external.ts`
- `auth/google.ts`
- `auth/login.ts`
- `auth/me.ts`
- `auth/method.ts`
- `auth/mfa.ts`
- `auth/mfalimit.ts`
- `auth/newpatient.ts`
- `auth/newproject.ts`
- `auth/newuser.ts`
- `auth/preauthorize.ts`
- `auth/profile.ts`
- `auth/register.ts`
- `auth/resetpassword.ts`
- `auth/revoke.ts`
- `auth/routes.ts`
- `auth/scope.ts`
- `auth/securityrequest.ts`
- `auth/setpassword.ts`
- `auth/status.ts`
- `auth/utils.ts`
- `auth/verifyemail.ts`
- … và 1 file nữa
