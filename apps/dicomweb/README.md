# dicomweb

DICOMweb (STOW-RS, QIDO-RS, WADO-RS) cho kho hình ảnh DICOM của Trivita, tương đương phần `dicom/` của Medplum nhưng đã sửa các lỗi đã biết (xem [kế hoạch](../../docs/plans/cds-dicom/dicom.md) mục 3). Dữ liệu là resource FHIR (`DicomStudy`, `DicomSeries`, `DicomInstance`) ở **cùng schema `fhir` với `fhir-server`**, nội dung tệp DICOM ở **cùng kho Binary** (`FHIR_BINARY_STORAGE`), nên tìm được qua API FHIR.

> **Trạng thái: D1, mới có khung.** Cấu hình, probe, xác thực JWT, cô lập project, bốn tiền tố URL, giới hạn body, lỗi `{"error"}` đã chạy; mọi route DICOMweb trả `501` cho tới khi D3 (STOW-RS) và D4 (QIDO-RS, WADO-RS) cài handler. Tiến độ: [task-list.md](../../docs/plans/task-list.md) #10.

## Chạy

```
make run-dicomweb         # :8008, đọc apps/dicomweb/.env nếu có (mẫu: .env.example)
```

Cần: (1) schema đã migrate bởi `fhir-server migrate up` (dicomweb **không tự migrate** và từ chối khởi động nếu chưa), cùng `DATABASE_URL` với fhir-server; (2) JWT của `auth` (`FHIR_JWT_ISSUER`, `FHIR_JWT_AUDIENCE`, `FHIR_JWKS_URL`), cùng giá trị với fhir-server; (3) `FHIR_BINARY_STORAGE` **trùng** với fhir-server (`file:<thư mục>` hoặc `s3:<bucket>`).

```
make -C apps/dicomweb image    # trivita-dicomweb:local từ apps/dicomweb/Dockerfile
docker run --rm -p 8008:8008 --env-file apps/dicomweb/.env trivita-dicomweb:local
```

## Tài liệu

| Tệp | Nội dung |
|---|---|
| [docs/architecture.md](docs/architecture.md) | Vì sao tách, tầng, dữ liệu, bảo mật, tắt êm |
| [docs/config.md](docs/config.md) | Mọi biến môi trường (sinh từ mã); mẫu ở [.env.example](.env.example) |
| [docs/api-contract.md](docs/api-contract.md) | Route, quy ước chung, bảng route sinh từ `swagger.json` |
| [docs/testing.md](docs/testing.md) | Các tầng test và cách chạy |
| [docs/runbook.md](docs/runbook.md) | Vận hành, sự cố thường gặp, định cỡ |

## Kiểm tra nhanh

```
curl -s localhost:8008/health/ready            # {"ready":true,"checks":{"database":true}}
curl -s -o /dev/null -w '%{http_code}\n' localhost:8008/dicomweb/studies                       # 401
curl -s -H "Authorization: Bearer $TOKEN" localhost:8008/dicomweb/studies                     # {"error":"Chưa cài đặt"} (501) cho tới D4
```
