# Vận hành nền tảng (super admin, DB, observability)

Công cụ cho vận hành: migration, reindex, vacuum, kiểm tra index, thống kê, telemetry.

## Module liên quan

- [`server/admin`](../modules/server/admin.md)
- [`server/migrations`](../modules/server/migrations.md)
- [`server/otel`](../modules/server/otel.md)
- [`server/sharding`](../modules/server/sharding.md)
- [`server/(root)`](../modules/server/root.md)
- [`cdk`](../modules/packages/cdk.md)

## API contract

- [admin.md](../../apps/auth/docs/api-contract/admin.md) (`/admin/super/*`)
- [fhir-operations.md](../../apps/fhir-server/docs/api-contract/fhir-operations.md) (`$db-*`, `$explain`)

## Ghi chú kỹ thuật và di chuyển

- Healthcheck và heartbeat trong `server/src/healthcheck.ts`, `heartbeat.ts`.
- Hạ tầng AWS bằng CDK (`medplum-cdk`); Helm chart và Terraform nằm ở `medplum/charts`, `medplum/terraform`, chuyển sang `deploy/medplum/`.

Sơ đồ liên quan: [business flow](../diagrams/business-flow.md), [user flow](../diagrams/user-flow.md).
