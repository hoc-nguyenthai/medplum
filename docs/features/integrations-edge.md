# Tích hợp thiết bị và hệ thống ngoài (Agent, HL7, DICOM, FHIRcast, MCP)

Kết nối hệ thống tại chỗ (HL7 v2, DICOM), đồng bộ ngữ cảnh lâm sàng và công cụ AI.

## Module liên quan

- [`agent`](../modules/packages/agent.md)
- [`hl7`](../modules/packages/hl7.md)
- [`server/dicom`](../modules/server/dicom.md)
- [`server/fhircast`](../modules/server/fhircast.md)
- [`server/mcp`](../modules/server/mcp.md)
- [`server/ai`](../modules/server/ai.md)
- [`server/ws`](../modules/server/ws.md)
- [`server/agent`](../modules/server/agent.md)

## API contract

- [others.md](../api-contract/others.md)
- [fhir-operations.md](../../apps/fhir-server/docs/api-contract/fhir-operations.md) (`Agent/$*`)

## Ghi chú kỹ thuật và di chuyển

- Agent kết nối server qua WebSocket `/ws/agent`; server điều khiển bằng `Agent/$status|$stats|$upgrade|$fetch-logs|$reload-config|$push`.
- DICOMweb mới cài `STOW-RS`, `QIDO-RS` (study, series) và metadata series; các route còn lại là stub.
- FHIRcast hỗ trợ STU2 và STU3 (cả `/hub/`).
- MCP phục vụ công cụ AI qua `/mcp/stream` và `/mcp/sse` (bật theo cấu hình).

Sơ đồ liên quan: [business flow](../diagrams/business-flow.md), [user flow](../diagrams/user-flow.md).
