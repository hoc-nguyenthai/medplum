# Coverage: Trivita FHIR Server (Apidog module 1533159)

## 1. Nguồn đã đọc
- Apidog: nhánh AI `ai/20261125-from-main-trivita-platform-tests` (project 1380475), 32 endpoint import từ `apps/fhir-server/docs/swagger.json`; `spec/endpoints.json`, `spec/openapi.json` (export 2026-11-25), `spec/endpoint-details/*.json` (`apidog endpoint get`).
- Mã/tài liệu của service (mã thắng khi lệch): `apps/fhir-server/docs/api-contract/common.md` và `cases/{fhir,operation,bulk,terminology,health}.md` (thông điệp nguyên văn từ mã/test, nhiều ca "đã đo"), `apps/auth/docs/api-contract/cases/{oauth,admin}.md` (token, scope, seed), `apps/auth/internal/domain/tokenscope.go` (scope vai trò), `tests/full_smoke/` + `docker-compose.full.yml` (cách dựng và seed).
- Không có server chạy trong lúc viết (chưa chạy docker): mọi kỳ vọng suy từ tài liệu/mã, chưa đo trực tiếp ở bước này.

## 2. Quy ước phản hồi
- Thành công: resource FHIR (`application/fhir+json`); GraphQL và manifest 200 của `bulkdata/status` là `application/json`; probe `/healthz`, `/readyz` JSON thường.
- Lỗi dưới `/fhir/R4`: `OperationOutcome` một `issue`: `code` theo HTTP (400 `invalid`, 401 `login`, 403 `forbidden`, 404 `not-found`, 405 `not-supported`, 409/412 `conflict`, 410 `deleted`, 422 `processing`, 415 `not-supported`); lỗi validate có nhiều `issue` (`structure`, `required`, `value`, `invariant`) kèm `expression`. Thông điệp ở `issue[0].details.text`. Ngoài `/fhir/R4`: `{"error": ...}`.
- GraphQL: lỗi cấp field vẫn 200 với `errors[]`; lỗi cú pháp/tham số 400 OperationOutcome.
- Xác thực: JWT của auth; `GET /fhir/R4/metadata` công khai. 401 `Thiếu bearer token`/`Token không hợp lệ` (kèm `WWW-Authenticate`), 403 `Token không đủ quyền cho thao tác này`. Vai trò: read (GET, `_search`, `$validate`, `$everything`, `$expand`, `$lookup`, `$graphql`, status), write (ghi), admin (`$reindex`, `$expunge`, `$import`, `$install`, AsyncJob). Chế độ JWT thêm SMART theo loại (`c/r/u/d/s`) và cách ly theo project.
- Biến test: `authUrl`, `clientId`, `clientSecret` (token), `projectId`, `otherClientId/otherClientSecret`; xem `environment.json`.

## 3. Bảng endpoint → scenario
| # | endpointId | Method Path | Auth | Quy tắc chính (từ mã) | Scenario phủ |
|---|---|---|---|---|---|
| 1 | 47548521 | GET /healthz | không | 200 `{"status":"ok"}`, không kiểm phụ thuộc | smoke |
| 2 | 47548522 | GET /readyz | không | 200 `ready:true` / 503 khi DB, broker hỏng hoặc đang tắt | smoke (503 chỉ thiết kế: cần làm hỏng DB) |
| 3 | 47548493 | GET /fhir/R4/metadata | công khai | CapabilityStatement R4 4.0.1, bỏ qua token | smoke, authn-negative |
| 4 | 47548496 | POST /{rt} | write | 201 + Location/ETag; If-None-Exist 200/412/400; validate 400; 415; id/meta do server ghi đè | crud-lifecycle, write-validation, conditional-*, authz-matrix, mọi setup |
| 5 | 47548498 | GET /{rt}/{id} | read | 200/304 (If-None-Match), 404, 410 | crud-lifecycle, update-rules, project-isolation |
| 6 | 47548499 | PUT /{rt}/{id} | write | 200; If-Match 412; id lệch 400; 404 (update-as-create tắt); Prefer | crud-lifecycle, update-rules, authz-matrix |
| 7 | 47548500 | DELETE /{rt}/{id} | write | 204 (lặp lại vẫn 204), 404 | crud-lifecycle, authz-matrix, everything |
| 8 | 47548501 | PATCH /{rt}/{id} | write | json-patch+json; 415/400; versionId mới | crud-lifecycle, patch-negative |
| 9 | 47548495 | PUT /{rt}?query | write | 0 khớp 201, 1 khớp 200, nhiều 412, thiếu tiêu chí 400 | conditional-write-flow, conditional-update-negative |
| 10 | 47548502 | GET /{rt}/{id}/_history | read | Bundle history, tombstone cuối | crud-lifecycle, project-isolation |
| 11 | 47548503 | GET /{rt}/{id}/_history/{vid} | read | 200 bản cũ, 410 tombstone | crud-lifecycle |
| 12 | 47548494 | GET /{rt} | read | search, phân trang, sắp xếp, modifier, lỗi tham số 400 | search, smoke, authn-negative, authz-project-header |
| 13 | 47548497 | POST /{rt}/_search | read | form-urlencoded; JSON bị bỏ qua | search-post |
| 14 | 47548491 | POST /fhir/R4 | write | batch (lỗi theo entry, 200), transaction (rollback), urn:uuid, 400 Bundle sai | batch-transaction-flow, bundle-negative |
| 15 | 47548492 | POST /fhir/R4/$graphql | read | 200 + errors; 400 cú pháp; giới hạn 64 KiB, 12 cấp | graphql |
| 16 | 47548516 | POST /{rt}/$validate | read | 200 All OK/cảnh báo; 400 lỗi; 415; 404 loại lạ | validate-operation |
| 17 | 47548512 | GET /Patient/{id}/$everything | read | `_count` 1..10000, `_since` RFC3339, `_type`; 404/410 | everything, project-isolation |
| 18 | 47548504 | GET /$export | read | 202 + Content-Location khi có Prefer; 400 tham số | bulk-export-flow, bulk-export-negative |
| 19 | 47548505 | GET /bulkdata/{id}/status | read (chủ job/admin) | 202 chạy, 200 manifest, 410/404 | bulk-export-flow, status-unknown-id |
| 20 | 47548506 | DELETE /bulkdata/{id}/status | read (chủ job/admin) | 202 hủy/xóa, 404 | bulk-export-flow, status-unknown-id |
| 21 | 47548514 | GET /job/{id}/status | read (chủ job/admin) | 202/200/500/410/404 | status-unknown-id (đường lạ); job thật: chỉ thiết kế (batch async) |
| 22 | 47548519 | GET /CodeSystem/$lookup | read | system+code bắt buộc; 404 | terminology-lookup |
| 23 | 47548520 | GET /ValueSet/$expand | read | url, count, offset, filter; 404/422 | terminology-expand |
| 24 | 47548508 | POST /CodeSystem/{id}/$import | admin | Parameters; 200; 400 các lỗi nội dung | code-system-import (lô nhỏ) |
| 25 | 47548507 | POST /CodeSystem/$import | admin | theo `system`, 404 | Chỉ thiết kế (cùng lõi route 24) |
| 26 | 47548510 | POST /ConceptMap/{id}/$import | admin | Parameters.mapping | Chỉ thiết kế |
| 27 | 47548509 | POST /ConceptMap/$import | admin | theo `url` | Chỉ thiết kế |
| 28 | 47548511 | POST /PackageRelease/{id}/$install | admin | ghi hàng loạt, không idempotent | Chỉ thiết kế |
| 29 | 47548515 | POST /{rt}/$reindex | admin | 202 job quét cả loại | Chỉ thiết kế |
| 30 | 47548518 | POST /{rt}/{id}/$reindex | admin | 200 `Reindexed ...` | authz-matrix |
| 31 | 47548517 | POST /{rt}/{id}/$expunge | admin | xóa vĩnh viễn; lặp 404 | crud-lifecycle, authz-matrix (dọn) |
| 32 | 47548513 | GET /Project/{id}/$rate-limits | admin đúng project | 200 Parameters; 403 project khác | rate-limits-flow |

Chéo: **xác thực/phân quyền** (authn-negative, authz-matrix, authz-project-header), **cô lập project** (project-isolation).

## 4. Lệch spec ↔ mã (M#)
- M1: Apidog gắn securityScheme `apiKey` header `Authorization` với `{{apiKey}}` cho mọi route, còn service dùng `Bearer <JWT>`. Bước test đặt `Bearer {{bearerToken}}` tường minh (builder dùng auth `bearer`); `apiKey` chỉ là biến placeholder.
- M2: swagger chỉ có 31-32 route; router còn phục vụ `PATCH`/`DELETE` có điều kiện theo query, `GET /fhir/R4` (tìm hệ thống), `$validate-code`, `$subsumes`, `$translate`, `$get-ws-binding-token`, `GET $graphql`. Không có endpoint trong Apidog nên không dựng step bound; PATCH/DELETE điều kiện chưa phủ.
- M3: tài liệu cũ nói `$import`/`$install` trả `404` cho token `write` tĩnh; mã mới (2026-11-24) trả `403`. Scenario theo mã mới.
- M4: `POST /{rt}/_search` với thân JSON bị bỏ qua im lặng (không lọc, không lỗi); không assert.
- M5: `Prefer: handling=strict|lenient` không được cài (tham số lạ luôn 400).

## 5. Câu hỏi mở / giả định (G#)
- G1: ràng buộc từ vựng (binding) không được kiểm (`gender:"robot"` vẫn 201). FHIR R4 yêu cầu binding `required`. Cố ý chưa tạo ca assert 400; chờ chủ service xác nhận đây là giới hạn chấp nhận được.
- G2: client_credentials có cho xin scope SMART `system/Patient.rs` (authz-matrix) không? Tài liệu auth chỉ chặn `fhir.*` và `patient/`; chưa đo. Nếu bị từ chối, hàng authz tương ứng sẽ lỗi ở bước token (lỗi dữ liệu, không phải bug).
- G3: scope SMART thay hoàn toàn scope vai trò `fhir.*` (theo `RoleScopes`); vai trò suy từ chữ cái quyền (`r`, `cruds`). Vai trò `admin` của token SMART chưa rõ; authz-matrix chỉ assert 403 cho `$reindex` ở các hàng có quyền hẹp.
- G4: Apidog gửi `Content-Type` ghi đè qua header cho body JSON (PATCH `json-patch+json`, `text/plain`, `fhir+xml`)? Chưa kiểm; nếu không, các ca PATCH/415 đổi sang kiểu body khác.
- G5: customHttp có chấp nhận `path` bắt đầu bằng biến `{{authUrl}}/oauth2/token` (URL tuyệt đối) không? Nếu không, đổi bước token sang `authUrl` làm base URL của một environment riêng.
- G6: tên tham số query là biến (`{{sys_name}}`, `{{url_name}}`, ...) ở terminology: giả định Apidog thay biến trong tên; tham số đệm `_s/_c/_p/_u/_o` được server bỏ qua.
- G7: `pm.variables.set/get` trong customScript hoạt động (dùng để lấy `job_id` từ `Content-Location`, so sánh versionId). Nếu không, dùng `pm.environment`/extractor regex.
- G8: client seed (`seed project`) là admin (`fhir.admin`): cần cho `$expunge`, `$reindex`, `$rate-limits`. Theo smoke: tạo được Patient và chạy `$export`; quyền admin chưa xác nhận.
- G9: `FHIR_RATE_LIMIT` của compose đầy đủ để trống (dev) nên không có 429; staging/production mặc định 20 req/giây/IP: chạy `search` (38 dòng × 5 bước) có thể chạm. Chạy local.
- G10: `FHIR_BASE_URL` trong `Content-Location` của `$export`: script chỉ tách id bằng regex `bulkdata/<id>/status`, không phụ thuộc host.
