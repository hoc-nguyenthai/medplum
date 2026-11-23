# FHIR REST (R4)

Gốc: `/fhir/R4/` (cũng truy cập được qua `/api/projects/:projectId/fhir/R4/` và `/projects/:projectId/fhir/R4/`).
Nguồn: `packages/fhir-router/src/fhirrouter.ts`. Mọi route dưới đây cần `Authorization: Bearer <access_token>` (trừ `metadata` và `.well-known`).

| Method | Path | Interaction FHIR |
|---|---|---|
| GET | `/metadata` | capabilities (công khai) |
| GET | `/$versions` | versions (công khai) |
| GET | `/.well-known/smart-configuration` | SMART (công khai) |
| GET | `/` | search-system |
| POST | `/` | batch / transaction (body: Bundle) |
| GET | `/:resourceType` | search-type |
| POST | `/:resourceType/_search` | search-type (form-encoded) |
| POST | `/:resourceType` | create |
| GET | `/:resourceType/:id` | read |
| GET | `/:resourceType/:id/_history` | history-instance |
| GET | `/:resourceType/:id/_history/:vid` | vread |
| PUT | `/:resourceType/:id` | update |
| PUT | `/:resourceType?search` | conditional update |
| PATCH | `/:resourceType/:id` | patch (JSON Patch) |
| PATCH | `/:resourceType?search` | conditional patch |
| DELETE | `/:resourceType/:id` | delete |
| DELETE | `/:resourceType?search` | conditional delete |
| POST | `/$graphql` | GraphQL |
| GET | `/:resourceType/$csv` | xuất CSV |
| POST | `/$ai` | gọi AI |
| POST | `/Agent/$push`, `/Agent/:id/$push` | đẩy tin cho Agent |
| GET/POST | `/Bot/$execute`, `/Bot/:id/$execute`, `/Bot/:id/$execute/*` | chạy bot (cho phép thêm path phía sau) |
| GET | `/bulkdata/export/:id` | trạng thái và kết quả bulk export |
| GET | `/job/:id/status` | trạng thái AsyncJob |

## Quy ước

- Content-Type: `application/fhir+json` (hoặc `application/json`); `text/x-hl7-ft` cho HL7 v2 trên `$execute`.
- Lỗi trả `OperationOutcome` với HTTP 400, 401, 403, 404, 409, 410, 412, 422, 429, 500.
- Rate limit: mọi response có header `RateLimit: "requests";r=<còn lại>;t=<giây tới reset>`. Khi vượt hạn mức trả `429` với `OperationOutcome` (diagnostics chứa JSON hạn mức, kèm thời gian reset). Giới hạn theo nhóm (login, MFA, FHIR) và theo project.
- Phân trang search: `_count`, `_offset`, `link[rel=next]`; sắp xếp `_sort`; `_total`. Các tham số này theo chuẩn FHIR; chi tiết hỗ trợ xem `GET /fhir/R4/metadata`.
- Header chuẩn FHIR (`ETag`, `Location`, `Prefer`, `If-Match`, `If-None-Exist`) được hỗ trợ ở mức router; xác minh từng header trong `fhir-router` trước khi dựa vào.

> DTO request/response của fhir-server Go (đã cài): xem [fhir-server-go.md](fhir-server-go.md).
