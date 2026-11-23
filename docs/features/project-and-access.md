# Project, thành viên và phân quyền

Mỗi project là một tenant. Quản lý thành viên, mời user, client application, bot, secret, access policy theo resource.

## Module liên quan

- [`server/admin`](../modules/server/admin.md)
- [`server/fhir`](../modules/server/fhir.md)
- [`server/scim`](../modules/server/scim.md)
- [`server/auth`](../modules/server/auth.md)

## API contract

- [admin.md](../api-contract/admin.md)
- [others.md](../api-contract/others.md) (SCIM)

## Ghi chú kỹ thuật và di chuyển

- Access policy xử lý trong `fhir/accesspolicy.ts` và `default-access-policies.ts`.
- Operation vòng đời project: `Project/$init`, `$clone`, `$archive`, `$restore`, `$rate-limits`.
- Rate limit tính theo project (cấu hình `defaultLoginRateLimit`, `defaultMfaRateLimit`, ... trong `ratelimit.ts`).
- SCIM v2 dùng cho provisioning user từ IdP bên ngoài.

Sơ đồ liên quan: [business flow](../diagrams/business-flow.md), [user flow](../diagrams/user-flow.md).
