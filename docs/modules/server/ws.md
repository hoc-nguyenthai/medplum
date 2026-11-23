# server/ws

> WebSocket: subscription, agent, fhircast, AI realtime, echo.

| | |
|---|---|
| Đường dẫn hiện tại | `medplum/packages/server/src/ws` |
| Đường dẫn đích | `apps/medplum-server/src/ws` |
| Số file nguồn (không tính test) | 6 |
| Cụm | phụ |

## Phụ thuộc (số import)

- Gọi tới: `(root)` (21), `oauth` (5), `otel` (5), `fhir` (5), `config` (3), `fhir/operations` (3), `util` (2), `agent` (2), `fhircast` (2), `auth` (1), `bots` (1)
- Được gọi bởi: `(root)` (1), `workers` (1), `fhir` (1)

## Ghi chú tách

Cụm nhỏ, phụ thuộc một chiều vào `fhir` và root. Ứng viên tách sớm ở giai đoạn 2.

## File chính

- `ws/agent.ts`
- `ws/ai-realtime.ts`
- `ws/echo.ts`
- `ws/fhircast.ts`
- `ws/routes.ts`
- `ws/subscriptions.ts`
