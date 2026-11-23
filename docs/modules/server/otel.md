# server/otel

> OpenTelemetry: trace, metric.

| | |
|---|---|
| Đường dẫn hiện tại | `medplum/packages/server/src/otel` |
| Đường dẫn đích | `apps/medplum-server/src/otel` |
| Số file nguồn (không tính test) | 2 |
| Cụm | lõi |

## Phụ thuộc (số import)

- Gọi tới: `workers` (5), `(root)` (2), `config` (1), `fhir` (1)
- Được gọi bởi: `fhir` (5), `ws` (5), `(root)` (3), `workers` (3), `bots` (1)

## Ghi chú tách

Thuộc cụm lõi: phải chuyển cùng `fhir`, `migrations`, `workers`, `config`, `storage`, `cloud` vì phụ thuộc vòng.

## File chính

- `otel/instrumentation.ts`
- `otel/otel.ts`
