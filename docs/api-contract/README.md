# API Contract (tham chiếu Medplum gốc)

> **Hợp đồng của từng service Go nằm cạnh service**, chỉ gồm phần ĐÃ CÀI và có test chống lệch: [auth](../../apps/auth/docs/api-contract.md), [fhir-server](../../apps/fhir-server/docs/api-contract.md), [ws-gateway](../../apps/ws-gateway/docs/api-contract.md). Thư mục này là tham chiếu Medplum gốc (nhiều thứ chưa cài) và các diễn giải, ví dụ JSON dài.

Hợp đồng API của `medplum-server`. Các bảng route được **sinh tự động từ mã nguồn** (`docs/_generator/gen_docs.py`, chạy lại được khi còn thư mục `medplum/`); không sửa tay.

Bản đầy đủ, luôn khớp code, do chính server phát ra lúc chạy:

- `GET /.well-known/api-catalog`: danh mục API.
- OpenAPI: sinh từ `server/src/openapi.ts` và `OperationDefinition`.
- `GET /fhir/R4/metadata`: CapabilityStatement.

| Tài liệu | Phạm vi |
|---|---|
| [fhir-server-go.md](fhir-server-go.md) | **Đã cài trong Go**: DTO request/response và ví dụ JSON thật (viết tay, không sinh) |
| [fhir-rest.md](fhir-rest.md) | CRUD, search, history, batch, GraphQL |
| [fhir-operations.md](fhir-operations.md) | 113 operation `$...` |
| [auth-dto.md](auth-dto.md) | DTO request/response của `/auth` (viết tay, đã đối chiếu source Medplum) |
| [auth.md](auth.md) | Đăng ký, đăng nhập, MFA, mật khẩu |
| [oauth.md](oauth.md) | OAuth2/OIDC, token, well-known của `apps/auth` (ĐÃ CÀI) và cách nối với fhir-server |
| [admin.md](admin.md) | Quản trị project và super admin |
| [others.md](others.md) | CDS, email, keyvalue, webhook, MCP, SCIM, FHIRcast, WebSocket, DICOM |

## Quy ước chung

- Gốc có thể kèm tiền tố `/api/` hoặc `/projects/:projectId/` (cùng một router).
- Xác thực: `Authorization: Bearer <access_token>` (OAuth2). Tài khoản máy dùng `client_credentials`.
- Lỗi FHIR: `OperationOutcome`. Lỗi OAuth: `{error, error_description}`. Lỗi auth/admin: `OperationOutcome`.
- Giới hạn: rate limit theo IP và theo project (`429`), kích thước JSON theo `maxJsonSize`.

> `fhir-server-go.md` viết tay nên không bị `gen_docs.py` ghi đè; các file còn lại có thể bị ghi đè khi chạy lại generator (cần thư mục `medplum/`), khi đó phải gắn lại nhãn "CHƯA CÀI".

> `apps/auth` (port từ Medplum, đang làm): hợp đồng đích là [auth-dto.md](auth-dto.md); swagger của app ở `apps/auth/docs`; OAuth2/OIDC xem [oauth.md](oauth.md).
