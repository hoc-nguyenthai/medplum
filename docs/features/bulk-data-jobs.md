# Bulk data và AsyncJob

Xuất dữ liệu lớn (`$export` ở mức hệ thống, Group, Patient) và các tác vụ chạy lâu, theo dõi qua AsyncJob.

## Module liên quan

- [`server/workers`](../modules/server/workers.md)
- [`server/fhir`](../modules/server/fhir.md)
- [`server/fhir/operations`](../modules/server/fhir-operations.md)

## API contract

- [fhir-operations.md](../api-contract/fhir-operations.md) (`$export`)
- [fhir-rest.md](../api-contract/fhir-rest.md) (`/bulkdata/export/:id`, `/job/:id/status`)

## Ghi chú kỹ thuật và di chuyển

- Worker `bulk-export` xử lý job; trạng thái và kết quả theo dõi qua resource `AsyncJob`/`BulkDataExport` và route `bulkdata/export/:id`.
- `AsyncJob/:id/$cancel` hủy job.

Sơ đồ liên quan: [business flow](../diagrams/business-flow.md), [user flow](../diagrams/user-flow.md).
