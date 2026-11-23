# API Contract

**Hợp đồng API nằm cạnh từng service** và chỉ mô tả phần service đó đang phục vụ. Thư mục này chỉ còn chỗ cho phần **chưa có service nào nhận**.

| Service | Hợp đồng (bảng route sinh từ swagger, có test chống lệch) | Tài liệu chi tiết (tham chiếu Medplum, DTO, ví dụ) |
|---|---|---|
| `auth` | [api-contract.md](../../apps/auth/docs/api-contract.md) | [docs/](../../apps/auth/docs/api-contract/): `auth.md` (đăng ký, đăng nhập, MFA, mật khẩu), `auth-dto.md` (DTO của `/auth`), `oauth.md` (OAuth2/OIDC, token, well-known), `admin.md` (quản trị project và SCIM), `email.md` (`/email/v1/send`) |
| `fhir-server` | [api-contract.md](../../apps/fhir-server/docs/api-contract.md) | [docs/](../../apps/fhir-server/docs/api-contract/): `fhir-rest.md` (CRUD, search, history, batch, GraphQL), `fhir-operations.md` (113 operation `$...`), `fhir-server-go.md` (DTO và ví dụ JSON của phần đã cài) |
| `ws-gateway` | [api-contract.md](../../apps/ws-gateway/docs/api-contract.md) (viết tay, không có OpenAPI) | (không có) |
| Chưa thuộc service nào | [others.md](others.md) | CDS Hooks, DICOMweb, key-value, webhook, MCP, Agent (CHƯA CÀI) |

Các tệp chi tiết ở cột phải **mô tả server Medplum TypeScript gốc** là chính, có chỗ đánh dấu phần đã cài; hợp đồng của server Go đang chạy là tệp ở cột giữa.

Bản đầy đủ, luôn khớp code của từng service, do chính service phát ra lúc chạy: `GET /swagger/doc.json` (auth, fhir-server, trừ staging/production), `GET /fhir/R4/metadata` (CapabilityStatement của fhir-server).

## Quy ước chung

- Xác thực: `Authorization: Bearer <access_token>` (OAuth2, token do `auth` cấp). Tài khoản máy dùng `client_credentials`.
- Lỗi FHIR: `OperationOutcome`. Lỗi OAuth: `{error, error_description}`. Lỗi auth/admin: `OperationOutcome`.
- Giới hạn: rate limit theo IP (`429`), kích thước JSON giới hạn.

> Các tệp chi tiết trước đây do `docs/_generator/gen_docs.py` sinh từ thư mục `medplum/`; sau khi tách ra từng service chúng là tài liệu viết tay, **không còn được sinh tự động** (chạy lại generator sẽ không ghi vào vị trí mới).
