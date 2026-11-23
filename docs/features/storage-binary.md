# Lưu trữ file (Binary)

Lưu nội dung nhị phân trên filesystem, S3, Azure Blob hoặc Google Cloud Storage; presigned URL; quét Binary.

## Module liên quan

- [`server/storage`](../modules/server/storage.md)
- [`server/cloud`](../modules/server/cloud.md)
- [`server/fhir`](../modules/server/fhir.md)

## API contract

- [fhir-operations.md](../../apps/fhir-server/docs/api-contract/fhir-operations.md) (`Binary/:id/$presigned-url`, `$scan`)

## Ghi chú kỹ thuật và di chuyển

- Backend chọn qua cấu hình `binaryStorage` (`file:`, `s3:`, ...).

Sơ đồ liên quan: [business flow](../diagrams/business-flow.md), [user flow](../diagrams/user-flow.md).
