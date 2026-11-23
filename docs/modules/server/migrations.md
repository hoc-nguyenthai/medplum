# server/migrations

> Schema Postgres và migration (phần lớn sinh tự động).

| | |
|---|---|
| Đường dẫn hiện tại | `medplum/packages/server/src/migrations` |
| Đường dẫn đích | `apps/medplum-server/src/migrations` |
| Số file nguồn (không tính test) | 191 |
| Cụm | lõi |

## Phụ thuộc (số import)

- Gọi tới: `workers` (70), `fhir` (21), `(root)` (15), `sharding` (1), `config` (1), `fhir/operations` (1), `util` (1), `seeds` (1)
- Được gọi bởi: `workers` (6), `fhir/operations` (5), `(root)` (4), `admin` (3)

## Ghi chú tách

Thuộc cụm lõi: phải chuyển cùng `fhir`, `migrations`, `workers`, `config`, `storage`, `cloud` vì phụ thuộc vòng.

## File chính

- `migrations/data/index.ts`
- `migrations/data/types.ts`
- `migrations/data/v1.ts`
- `migrations/data/v10.ts`
- `migrations/data/v11.ts`
- `migrations/data/v12.ts`
- `migrations/data/v13.ts`
- `migrations/data/v14.ts`
- `migrations/data/v15.ts`
- `migrations/data/v16.ts`
- `migrations/data/v17.ts`
- `migrations/data/v18.ts`
- `migrations/data/v19.ts`
- `migrations/data/v2.ts`
- `migrations/data/v20.ts`
- `migrations/data/v21.ts`
- `migrations/data/v22.ts`
- `migrations/data/v23.ts`
- `migrations/data/v24.ts`
- `migrations/data/v25.ts`
- `migrations/data/v26.ts`
- `migrations/data/v27.ts`
- `migrations/data/v28.ts`
- `migrations/data/v29.ts`
- `migrations/data/v3.ts`
- … và 166 file nữa
