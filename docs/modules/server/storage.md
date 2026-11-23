# server/storage

> Lưu file Binary: filesystem, S3, Azure Blob, GCS; presigned URL.

| | |
|---|---|
| Đường dẫn hiện tại | `medplum/packages/server/src/storage` |
| Đường dẫn đích | `apps/medplum-server/src/storage` |
| Số file nguồn (không tính test) | 7 |
| Cụm | lõi |

## Phụ thuộc (số import)

- Gọi tới: `config` (3), `cloud` (3), `fhir` (2), `(root)` (2)
- Được gọi bởi: `fhir/operations` (11), `cloud` (10), `fhir` (7), `(root)` (2), `bots` (2), `workers` (2), `dicom` (1), `email` (1)

## Ghi chú tách

Thuộc cụm lõi: phải chuyển cùng `fhir`, `migrations`, `workers`, `config`, `storage`, `cloud` vì phụ thuộc vòng.

## File chính

- `storage/base.ts`
- `storage/filesystem.ts`
- `storage/loader.ts`
- `storage/presign.ts`
- `storage/routes.ts`
- `storage/types.ts`
- `storage/utils.ts`
