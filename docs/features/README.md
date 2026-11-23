# Tính năng

| Tính năng | Module chính |
|---|---|
| [API dữ liệu FHIR (CRUD, search, history, batch, GraphQL)](fhir-data-api.md) | [`server/fhir`](../modules/server/fhir.md), [`server/migrations`](../modules/server/migrations.md), [`server/(root)`](../modules/server/root.md) |
| [Xác thực và phiên đăng nhập (auth, MFA, OAuth2/OIDC)](authentication.md) | [`server/auth`](../modules/server/auth.md), [`server/oauth`](../modules/server/oauth.md), [`server/email`](../modules/server/email.md) |
| [Project, thành viên và phân quyền](project-and-access.md) | [`server/admin`](../modules/server/admin.md), [`server/fhir`](../modules/server/fhir.md), [`server/scim`](../modules/server/scim.md) |
| [Bot và tự động hóa (bot, CDS Hooks, webhook)](bots-automation.md) | [`server/bots`](../modules/server/bots.md), [`server/cloud`](../modules/server/cloud.md), [`server/cds`](../modules/server/cds.md) |
| [Subscription và job nền](subscriptions-events.md) | [`server/workers`](../modules/server/workers.md), [`server/ws`](../modules/server/ws.md), [`server/fhir`](../modules/server/fhir.md) |
| [Bulk data và AsyncJob](bulk-data-jobs.md) | [`server/workers`](../modules/server/workers.md), [`server/fhir`](../modules/server/fhir.md), [`server/fhir/operations`](../modules/server/fhir-operations.md) |
| [Terminology (ValueSet, CodeSystem, ConceptMap)](terminology.md) | [`server/fhir/operations`](../modules/server/fhir-operations.md), [`server/seeds`](../modules/server/seeds.md), [`definitions`](../modules/packages/definitions.md) |
| [Nghiệp vụ lâm sàng (everything, summary, C-CDA, lịch hẹn, SMART Health)](clinical-operations.md) | [`server/fhir/operations`](../modules/server/fhir-operations.md), [`ccda`](../modules/packages/ccda.md), [`react-scheduling`](../modules/packages/react-scheduling.md) |
| [Tích hợp thiết bị và hệ thống ngoài (Agent, HL7, DICOM, FHIRcast, MCP)](integrations-edge.md) | [`agent`](../modules/packages/agent.md), [`hl7`](../modules/packages/hl7.md), [`server/dicom`](../modules/server/dicom.md) |
| [Lưu trữ file (Binary)](storage-binary.md) | [`server/storage`](../modules/server/storage.md), [`server/cloud`](../modules/server/cloud.md), [`server/fhir`](../modules/server/fhir.md) |
| [Vận hành nền tảng (super admin, DB, observability)](platform-ops.md) | [`server/admin`](../modules/server/admin.md), [`server/migrations`](../modules/server/migrations.md), [`server/otel`](../modules/server/otel.md) |
| [Frontend (admin app, thư viện React, tích hợp kê đơn và xét nghiệm)](frontend.md) | [`app`](../modules/packages/app.md), [`react`](../modules/packages/react.md), [`react-hooks`](../modules/packages/react-hooks.md) |
