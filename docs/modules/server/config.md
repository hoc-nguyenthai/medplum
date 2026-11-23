# server/config

> Nạp cấu hình (file JSON, biến môi trường, AWS SSM, GCP, Azure secret) thành MedplumServerConfig.

| | |
|---|---|
| Đường dẫn hiện tại | `medplum/packages/server/src/config` |
| Đường dẫn đích | `apps/medplum-server/src/config` |
| Số file nguồn (không tính test) | 4 |
| Cụm | lõi |

## Phụ thuộc (số import)

- Gọi tới: `cloud` (3), `(root)` (2), `fhir` (1)
- Được gọi bởi: `fhir` (24), `(root)` (18), `fhir/operations` (18), `cloud` (15), `workers` (11), `auth` (10), `oauth` (7), `email` (4), `util` (3), `storage` (3), `ws` (3), `bots` (2), `admin` (2), `scim` (1), `migrations` (1), `mcp` (1), `otel` (1), `fhircast` (1)

## Ghi chú tách

Thuộc cụm lõi: phải chuyển cùng `fhir`, `migrations`, `workers`, `config`, `storage`, `cloud` vì phụ thuộc vòng.

## File chính

- `config/capabilitystatement.ts`
- `config/loader.ts`
- `config/types.ts`
- `config/utils.ts`
