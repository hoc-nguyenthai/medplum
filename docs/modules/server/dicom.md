# server/dicom

> DICOMweb: QIDO-RS, STOW-RS, WADO-RS.

| | |
|---|---|
| Đường dẫn hiện tại | `medplum/packages/server/src/dicom` |
| Đường dẫn đích | `apps/medplum-server/src/dicom` |
| Số file nguồn (không tính test) | 5 |
| Cụm | phụ |

## Phụ thuộc (số import)

- Gọi tới: `(root)` (7), `fhir` (3), `oauth` (1), `storage` (1)
- Được gọi bởi: `(root)` (1)

## Route

| Method | Path | Xác thực |
|---|---|---|
| GET | `/dicomweb/:bulkdataUriReference` | không guard ở route (xem middleware cha) |
| GET | `/dicomweb/studies` | không guard ở route (xem middleware cha) |
| POST | `/dicomweb/studies` | không guard ở route (xem middleware cha) |
| GET | `/dicomweb/studies/:studyUid` | không guard ở route (xem middleware cha) |
| POST | `/dicomweb/studies/:studyUid` | không guard ở route (xem middleware cha) |
| GET | `/dicomweb/studies/:studyUid/metadata` | không guard ở route (xem middleware cha) |
| GET | `/dicomweb/studies/:studyUid/rendered` | không guard ở route (xem middleware cha) |
| GET | `/dicomweb/studies/:studyUid/series` | không guard ở route (xem middleware cha) |
| GET | `/dicomweb/studies/:studyUid/series/:series` | không guard ở route (xem middleware cha) |
| GET | `/dicomweb/studies/:studyUid/series/:seriesUid/instances` | không guard ở route (xem middleware cha) |
| GET | `/dicomweb/studies/:studyUid/series/:seriesUid/instances/:instance` | không guard ở route (xem middleware cha) |
| GET | `/dicomweb/studies/:studyUid/series/:seriesUid/instances/:instance/metadata` | không guard ở route (xem middleware cha) |
| GET | `/dicomweb/studies/:studyUid/series/:seriesUid/instances/:instance/rendered` | không guard ở route (xem middleware cha) |
| GET | `/dicomweb/studies/:studyUid/series/:seriesUid/instances/:instanceUid/frames/:frame` | không guard ở route (xem middleware cha) |
| GET | `/dicomweb/studies/:studyUid/series/:seriesUid/metadata` | không guard ở route (xem middleware cha) |
| GET | `/dicomweb/studies/:studyUid/series/:seriesUid/rendered` | không guard ở route (xem middleware cha) |

## Ghi chú tách

Cụm nhỏ, phụ thuộc một chiều vào `fhir` và root. Ứng viên tách sớm ở giai đoạn 2.

## File chính

- `dicom/qido-rs.ts`
- `dicom/routes.ts`
- `dicom/stow-rs.ts`
- `dicom/utils.ts`
- `dicom/wado-rs.ts`
