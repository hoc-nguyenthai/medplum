# server/seeds

> Dữ liệu khởi tạo: ValueSet, StructureDefinition, SearchParameter.

| | |
|---|---|
| Đường dẫn hiện tại | `medplum/packages/server/src/seeds` |
| Đường dẫn đích | `apps/medplum-server/src/seeds` |
| Số file nguồn (không tính test) | 3 |
| Cụm | lõi |

## Phụ thuộc (số import)

- Gọi tới: `fhir/operations` (3), `fhir` (3)
- Được gọi bởi: `(root)` (3), `admin` (3), `migrations` (1)

## Ghi chú tách

Thuộc cụm lõi: phải chuyển cùng `fhir`, `migrations`, `workers`, `config`, `storage`, `cloud` vì phụ thuộc vòng.

## File chính

- `seeds/searchparameters.ts`
- `seeds/structuredefinitions.ts`
- `seeds/valuesets.ts`
