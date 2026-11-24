# dicomweb

DICOMweb (STOW-RS, QIDO-RS, WADO-RS) cho kho hình ảnh DICOM của Trivita, tương đương phần `dicom/` của Medplum nhưng đã sửa các lỗi đã biết (xem [kế hoạch](../../docs/plans/cds-dicom/dicom.md) mục 3). Dữ liệu là resource FHIR (`DicomStudy`, `DicomSeries`, `DicomInstance`) ở **cùng schema `fhir` với `fhir-server`**, nội dung tệp DICOM ở **cùng kho Binary** (`FHIR_BINARY_STORAGE`), nên tìm được qua API FHIR.

> **Trạng thái: D1 đến D11 đã xong (2026-11-24).** Đã chạy, đều theo luồng và qua cả bốn tiền tố URL: STOW-RS tệp Part 10 và dạng metadata `application/dicom+json` kèm bulkdata (D3, D10); QIDO-RS ba cấp study, series, instance với bộ lọc thuộc tính và phân trang (D4, D7); WADO-RS tải instance, series, study, metadata, frame đọc theo khoảng byte nhờ chỉ mục frame (D4, D5, D7), `rendered` JPEG/PNG cho instance và frame (D7), bulkdata PixelData (D9); WADO-URI (D9); lệnh `dicomweb reindex` (D8) và `dicomweb pixeldata` cùng worker nền dựng `DicomInstance.pixelData` tùy chọn, mặc định tắt (D11). Route DICOMweb chưa cài (rendered cấp series và study, xóa, đường dẫn lạ dưới tiền tố) trả `501`; bulkdata ngoài PixelData trả `404`. Tiến độ: [task-list.md](../../docs/plans/task-list.md) #10.

## Chạy

```
make run-dicomweb         # :8008, đọc apps/dicomweb/.env nếu có (mẫu: .env.example)
```

Cần: (1) schema đã migrate bởi `fhir-server migrate up` (dicomweb **không tự migrate** và từ chối khởi động nếu chưa), cùng `DATABASE_URL` với fhir-server; (2) JWT của `auth` (`FHIR_JWT_ISSUER`, `FHIR_JWT_AUDIENCE`, `FHIR_JWKS_URL`), cùng giá trị với fhir-server; (3) `FHIR_BINARY_STORAGE` **trùng** với fhir-server (`file:<thư mục>` hoặc `s3:<bucket>`).

```
make -C apps/dicomweb image    # trivita-dicomweb:local từ apps/dicomweb/Dockerfile
docker run --rm -p 8008:8008 --env-file apps/dicomweb/.env trivita-dicomweb:local
```

## Docker compose

```
FHIR_JOB_SIGNING_KEY=$(openssl rand -hex 24) \
docker compose -f docker-compose.yml -f docker-compose.auth.yml -f docker-compose.dicomweb.yml --env-file apps/fhir-server/.env \
  up --build fhir-server auth dicomweb postgres
```

dicomweb dùng chung schema `fhir` (role `fhir_app`) và kho Binary với fhir-server, chờ fhir-server healthy vì fhir-server sở hữu migration. **Chưa chạy thử bằng Docker**; xem ghi chú đầu `docker-compose.dicomweb.yml`.

## Tài liệu

| Tệp | Nội dung |
|---|---|
| [docs/architecture.md](docs/architecture.md) | Vì sao tách, tầng, dữ liệu, bảo mật, tắt êm |
| [docs/config.md](docs/config.md) | Mọi biến môi trường (sinh từ mã); mẫu ở [.env.example](.env.example) |
| [docs/api-contract.md](docs/api-contract.md) | Route, quy ước chung, bảng route sinh từ `swagger.json`; chi tiết từng route (header, từng trường, từng trường hợp phản hồi) ở [docs/api-contract/](docs/api-contract/common.md) |
| [docs/business/](docs/business/README.md) | Tài liệu nghiệp vụ (góc nhìn BA kỹ thuật): tác nhân, luồng nghiệp vụ và người dùng, danh mục chức năng, luật, kịch bản chấp nhận |
| [docs/testing.md](docs/testing.md) | Các tầng test và cách chạy |
| [docs/runbook.md](docs/runbook.md) | Vận hành, sự cố thường gặp, định cỡ |

## Kiểm tra nhanh

```
curl -s localhost:8008/health/ready            # {"ready":true,"checks":{"database":true}}
curl -s -o /dev/null -w '%{http_code}\n' localhost:8008/dicomweb/studies                       # 401
# STOW-RS: tải một tệp DICOM (multipart/related)
curl -s -H "Authorization: Bearer $TOKEN" -H 'Content-Type: multipart/related; type="application/dicom"; boundary=b' \
  --data-binary $'--b\r\nContent-Type: application/dicom\r\n\r\n'"$(cat file.dcm)"$'\r\n--b--\r\n' localhost:8008/dicomweb/studies
# (tải tệp lớn: dùng client DICOMweb, ví dụ dcm4che `dcm4che-tools storescu`/stowrs, hoặc curl -F kiểu multipart/related)
```
