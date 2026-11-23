# server/sharding

> Chọn shard DB theo project.

| | |
|---|---|
| Đường dẫn hiện tại | `medplum/packages/server/src/sharding` |
| Đường dẫn đích | `apps/medplum-server/src/sharding` |
| Số file nguồn (không tính test) | 1 |
| Cụm | phụ |

## Phụ thuộc (số import)

- Gọi tới: `fhir` (1)
- Được gọi bởi: `(root)` (1), `migrations` (1), `fhir` (1)

## Ghi chú tách

Cụm nhỏ, phụ thuộc một chiều vào `fhir` và root. Ứng viên tách sớm ở giai đoạn 2.

## File chính

- `sharding/connection-shard-id.ts`
