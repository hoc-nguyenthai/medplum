# API chưa thuộc service nào (tham chiếu Medplum, CHƯA CÀI)

> **Trạng thái: CHƯA CÀI trong bất kỳ service Go nào.** Tài liệu này mô tả server Medplum bản TypeScript gốc cho các route chưa có service nhận (CDS Hooks, DICOMweb, key-value, webhook, MCP, Agent, WebSocket agent/ai-realtime/echo). Route đã cài nằm ở hợp đồng của từng service: [auth](../../apps/auth/docs/api-contract.md) (kể cả well-known, SCIM, email), [fhir-server](../../apps/fhir-server/docs/api-contract.md), [ws-gateway](../../apps/ws-gateway/docs/api-contract.md) (WebSocket Subscription, FHIRcast). Không viết DTO cho các route dưới đây cho tới khi chúng được port sang Go.

CDS Hooks, DICOMweb, key-value, webhook, MCP, Agent. Nguồn: các `routes.ts` trong từng module.

| Method | Path | Xác thực |
|---|---|---|
| GET | `/cds-services/` | không guard ở route (xem middleware cha) |
| POST | `/cds-services/:id` | không guard ở route (xem middleware cha) |
| GET | `/dicomweb/:bulkdataUriReference` | không guard ở route (xem middleware cha) |
| GET | `/dicomweb/studies` | không guard ở route (xem middleware cha) |
| POST | `/dicomweb/studies` | không guard ở route (xem middleware cha) |
| GET | `/dicomweb/studies/:studyUid` | không guard ở route (xem middleware cha) |
| POST | `/dicomweb/studies/:studyUid` | không guard ở route (xem middleware cha) |
| GET | `/dicomweb/studies/:studyUid/metadata` | không guard ở route (xem middleware cha) |
| GET | `/dicomweb/studies/:studyUid/rendered` | không guard ở route (xem middleware cha) |
| GET | `/dicomweb/studies/:studyUid/series` | không guard ở route (xem middleware cha) |
| GET | `/dicomweb/studies/:studyUid/series/:series` | không guard ở route (xem middleware cha) |
| GET | `/dicomweb/studies/:studyUid/series/:seriesUid/instances` | không guard ở route (xem middleware cha) |
| GET | `/dicomweb/studies/:studyUid/series/:seriesUid/instances/:instance` | không guard ở route (xem middleware cha) |
| GET | `/dicomweb/studies/:studyUid/series/:seriesUid/instances/:instance/metadata` | không guard ở route (xem middleware cha) |
| GET | `/dicomweb/studies/:studyUid/series/:seriesUid/instances/:instance/rendered` | không guard ở route (xem middleware cha) |
| GET | `/dicomweb/studies/:studyUid/series/:seriesUid/instances/:instanceUid/frames/:frame` | không guard ở route (xem middleware cha) |
| GET | `/dicomweb/studies/:studyUid/series/:seriesUid/metadata` | không guard ở route (xem middleware cha) |
| GET | `/dicomweb/studies/:studyUid/series/:seriesUid/rendered` | không guard ở route (xem middleware cha) |
| POST | `/fhir/R4/Agent/$push` | Bearer |
| POST | `/fhir/R4/Agent/:id/$push` | Bearer |
| DELETE | `/keyvalue/v1/:key` | không guard ở route (xem middleware cha) |
| GET | `/keyvalue/v1/:key` | không guard ở route (xem middleware cha) |
| PUT | `/keyvalue/v1/:key` | không guard ở route (xem middleware cha) |
| GET | `/mcp/sse` | không guard ở route (xem middleware cha) |
| POST | `/mcp/sse` | không guard ở route (xem middleware cha) |
| ALL | `/mcp/stream` | không guard ở route (xem middleware cha) |
| POST | `/webhook/:id` | không guard ở route (xem middleware cha) |

WebSocket (`server/src/ws/routes.ts`): handler chọn theo phân đoạn thứ hai của URL, gồm `/ws/subscriptions-r4`, `/ws/agent`, `/ws/fhircast`, `/ws/ai-realtime`, `/ws/echo` (tiền tố `/api` và `/projects/:projectId` được chuẩn hóa).

DICOMweb (`server/src/dicom`): gốc `/dicomweb/` (cũng qua `/api/dicomweb/` và `/projects/:projectId/dicomweb/`).

| Method | Path | Trạng thái |
|---|---|---|
| POST | `/dicomweb/studies`, `/dicomweb/studies/:studyUid` | STOW-RS lưu instance |
| GET | `/dicomweb/studies` | QIDO-RS tìm study |
| GET | `/dicomweb/studies/:studyUid/series` | QIDO-RS tìm series |
| GET | `/dicomweb/studies/:studyUid/series/:seriesUid/metadata` | WADO-RS metadata series |
| GET | các route còn lại (`rendered`, `instances`, ...) | chưa cài (stub `notImplemented`) |
