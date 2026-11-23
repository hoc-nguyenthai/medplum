# Subscription và job nền

Phát hiện thay đổi resource và kích hoạt thông báo hoặc bot; xử lý tác vụ nặng bất đồng bộ bằng BullMQ.

## Module liên quan

- [`server/workers`](../modules/server/workers.md)
- [`server/ws`](../modules/server/ws.md)
- [`server/fhir`](../modules/server/fhir.md)
- [`server/otel`](../modules/server/otel.md)

## API contract

- [others.md](../api-contract/others.md) (WebSocket `/ws/subscriptions-r4`)

## Ghi chú kỹ thuật và di chuyển

- Sau khi ghi resource, `repo.ts` gọi `addBackgroundJobs` và `addSubscriptionJobs`; job chạy ở `workers/subscription.ts` (`execSubscriptionJob`).
- Các worker hiện có: `subscription`, `download`, `reindex`, `batch`, `bulk-export`, `dispatch`, `cron`, `set-accounts`, `dicom`, `lambda-cleaner`, `post-deploy-migration`, `subscription-failure-tracker`.
- Điểm khó: `workers → migrations → fhir → workers` tạo vòng; phải tách trước khi có `medplum-worker`.

Sơ đồ liên quan: [business flow](../diagrams/business-flow.md), [user flow](../diagrams/user-flow.md).
