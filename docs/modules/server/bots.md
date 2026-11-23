# server/bots

> Chạy bot: vmcontext, AWS Lambda, Fission.

| | |
|---|---|
| Đường dẫn hiện tại | `medplum/packages/server/src/bots` |
| Đường dẫn đích | `apps/medplum-server/src/bots` |
| Số file nguồn (không tính test) | 4 |
| Cụm | lõi |

## Phụ thuộc (số import)

- Gọi tới: `fhir` (9), `util` (5), `cloud` (3), `config` (2), `storage` (2), `(root)` (2), `oauth` (1), `otel` (1)
- Được gọi bởi: `fhir/operations` (6), `cloud` (4), `workers` (4), `webhook` (2), `cds` (2), `fhir` (2), `util` (1), `ws` (1)

## Ghi chú tách

Thuộc cụm lõi: phải chuyển cùng `fhir`, `migrations`, `workers`, `config`, `storage`, `cloud` vì phụ thuộc vòng.

## File chính

- `bots/execute.ts`
- `bots/types.ts`
- `bots/utils.ts`
- `bots/vmcontext.ts`
