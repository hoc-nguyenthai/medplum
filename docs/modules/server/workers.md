# server/workers

> Job nền BullMQ: subscription, download, reindex, batch, bulk-export, cron, dispatch, set-accounts.

| | |
|---|---|
| Đường dẫn hiện tại | `medplum/packages/server/src/workers` |
| Đường dẫn đích | `apps/medplum-server/src/workers` |
| Số file nguồn (không tính test) | 16 |
| Cụm | lõi |

## Phụ thuộc (số import)

- Gọi tới: `(root)` (34), `fhir` (24), `config` (11), `fhir/operations` (8), `migrations` (6), `util` (6), `oauth` (5), `cloud` (4), `bots` (4), `otel` (3), `storage` (2), `ws` (1)
- Được gọi bởi: `migrations` (70), `admin` (7), `fhir/operations` (7), `otel` (5), `(root)` (1), `fhir` (1)

## Ghi chú tách

Thuộc cụm lõi: phải chuyển cùng `fhir`, `migrations`, `workers`, `config`, `storage`, `cloud` vì phụ thuộc vòng.

## File chính

- `workers/base.ts`
- `workers/batch.ts`
- `workers/bulk-export.ts`
- `workers/cron.ts`
- `workers/dicom.ts`
- `workers/dispatch.ts`
- `workers/download.ts`
- `workers/index.ts`
- `workers/lambda-cleaner.ts`
- `workers/post-deploy-migration.ts`
- `workers/reindex.ts`
- `workers/set-accounts.ts`
- `workers/subscription-failure-tracker.ts`
- `workers/subscription.ts`
- `workers/test-utils.ts`
- `workers/utils.ts`
