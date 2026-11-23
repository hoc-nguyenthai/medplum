# server/fhircast

> FHIRcast STU2/STU3: đồng bộ ngữ cảnh giữa ứng dụng lâm sàng.

| | |
|---|---|
| Đường dẫn hiện tại | `medplum/packages/server/src/fhircast` |
| Đường dẫn đích | `apps/medplum-server/src/fhircast` |
| Số file nguồn (không tính test) | 2 |
| Cụm | phụ |

## Phụ thuộc (số import)

- Gọi tới: `(root)` (6), `config` (1), `fhir` (1), `oauth` (1), `util` (1)
- Được gọi bởi: `ws` (2), `(root)` (1), `oauth` (1)

## Ghi chú tách

Cụm nhỏ, phụ thuộc một chiều vào `fhir` và root. Ứng viên tách sớm ở giai đoạn 2.

## File chính

- `fhircast/routes.ts`
- `fhircast/utils.ts`
