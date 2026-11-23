# Xác thực và phiên đăng nhập (auth, MFA, OAuth2/OIDC)

Đăng ký user và project, đăng nhập mật khẩu hoặc Google, MFA (TOTP, email), chọn profile, đặt lại mật khẩu, cấp token OAuth2/OIDC.

## Module liên quan

- [`server/auth`](../modules/server/auth.md)
- [`server/oauth`](../modules/server/oauth.md)
- [`server/email`](../modules/server/email.md)
- [`server/config`](../modules/server/config.md)

## API contract

- [auth.md](../api-contract/auth.md)
- [oauth.md](../api-contract/oauth.md)

## Ghi chú kỹ thuật và di chuyển

- `/oauth2/token` hỗ trợ 5 grant: `authorization_code`, `refresh_token`, `client_credentials`, token-exchange, `pre-authorized_code`.
- PKCE qua `codeChallenge` khi đăng nhập và `code_verifier` khi đổi token.
- Login/membership/user là resource FHIR (`Login`, `ProjectMembership`, `User`) lưu qua `fhir`; vì vậy `auth` phụ thuộc `fhir` 48 lần.
- Trivita đã có `authz-service` và skill `identity-service-go`. Cần quyết định giữ nguyên module này hay thay bằng service Go (ảnh hưởng đến toàn bộ giai đoạn 2).

Sơ đồ liên quan: [business flow](../diagrams/business-flow.md), [user flow](../diagrams/user-flow.md).
