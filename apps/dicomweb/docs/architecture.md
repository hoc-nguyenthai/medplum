# dicomweb: kiến trúc

Cập nhật 2026-11-24 (D1).

## Vì sao tách (docs/service-principles.md mục 1)

Điều kiện **A, hồ sơ tải khác**. Tải lên DICOM là hàng chục MB đến hàng GB mỗi instance, một yêu cầu STOW có thể kéo dài hàng phút và phải truyền luồng; `fhir-server` đặt `BodyLimit("16M")` cho mọi yêu cầu, co giãn theo số yêu cầu ngắn và có `WriteTimeout` ngắn. Nhét STOW vào `fhir-server` buộc nới giới hạn và timeout cho cả API FHIR, và để rủi ro nhị phân lớn (bộ nhớ, tệp độc: [d0-spike.md](../../../docs/plans/cds-dicom/d0-spike.md) mục 3) ảnh hưởng cả API lâm sàng. Đo được: đọc tệp 120 MB bằng thư viện mặc định tốn 127 MB bộ nhớ, bằng đường STOW của dicomweb (luồng, `SkipPixelData`) tốn 2 MB. Cũng thỏa điều kiện **C** (deploy và sự cố của kênh tải ảnh không được làm rớt API FHIR).

Đã thử bậc rẻ hơn: **cùng binary, vai trò khác** (`fhir-server` với một chế độ `DICOMWEB`) bị loại vì hai hồ sơ bộ nhớ và timeout trái nhau trong một tiến trình, và vì ranh giới deploy (C) không đạt. Không thỏa B (không có bí mật riêng: dùng cùng schema và kho với `fhir-server`) và D.

## Tầng

Như `fhir-server` và `ws-gateway` ("vỏ mỏng", C-5): `config`, `presentation/httpapi`, `bootstrap`, không có `domain`/`application`/`infrastructure`. Nghiệp vụ DICOM nằm ở `pkg/dicomweb` (phần thuần đã có từ D2: DICOM JSON, chuyển đổi, ánh xạ study/series, làm sạch metadata, số liệu tổng hợp; đọc tệp và tách frame thêm ở D3, D5) để dùng lại và test độc lập. Luật phụ thuộc ở `architecture_test.go`.

```
cmd/dicomweb            serve | healthcheck
internal/config         biến môi trường, kiểm hợp lệ, tài liệu sinh từ mã
internal/presentation   Echo: probe, bốn tiền tố, guard JWT, giới hạn body và hạn mức, lỗi JSON, Swagger
internal/bootstrap      lắp DB, fhirstore, kho Binary, Verifier; vòng đời và tắt êm
```

## Dữ liệu

- **Dùng chung schema `fhir` của fhir-server** (quyết định D-DW-1, 2026-11-24): `DicomStudy`, `DicomSeries`, `DicomInstance` là resource FHIR tìm được qua `GET /fhir/R4/DicomStudy?...` của fhir-server (tương thích Medplum), nên không thể nằm ở schema khác. Hệ quả: (a) **fhir-server sở hữu migration**; dicomweb không migrate và từ chối khởi động khi `fhirstore.Pending > 0` (có test); (b) dicomweb kết nối bằng cùng role ứng dụng của fhir-server (`fhir_app`, chỉ DML), không có role riêng; ngoại lệ so với D-1 của `service-principles.md` ("mỗi service một schema và một role") vì hai service là hai cửa vào của **cùng một miền dữ liệu**; (c) cô lập theo project bằng `fhirauth.Scope`, như fhir-server.
- **Kho Binary dùng chung:** nội dung DICOM (`application/dicom`) ghi vào `binarystore` (tệp hoặc S3) với khóa theo Binary; `fhir-server` đọc cùng kho để phục vụ `Binary/{id}` và `/storage`. Hai service phải trỏ cùng `FHIR_BINARY_STORAGE` (và cùng bucket/prefix). Ghi theo thứ tự CSDL trước, kho sau, bù trừ khi lỗi (như `fhirbinary`, S9) sẽ làm ở D3.
- Không có Redis, không có hàng đợi ở D1. Worker pixel data (D5) dùng `pkg/workflow`.

## Xác thực và cô lập project

Bearer JWT do `auth` cấp, kiểm cục bộ bằng JWKS (`fhirauth.Verifier`, C-9); không có token tĩnh và không có chế độ tắt xác thực. 401 cho thiếu hoặc sai token, 403 cho đúng token nhưng thiếu project, scope hoặc quyền (fail đóng; lỗi hạ tầng không bị đổi thành 401). Quyền (Q-D1): `GET` cần đọc, `POST` cần ghi. Tiền tố `/projects/{id}/dicomweb` phải khớp project của token (Medplum có ca "Cannot use another project scope"); header `X-Fhir-Project` cũng không được chọn project khác. Super-admin (cờ `FHIR_JWT_SUPER_ADMIN`) hành động trong project chọn, và mọi yêu cầu của họ được ghi nhật ký kiểm toán (cùng cách với fhir-server). Truy cập từng resource đi qua `fhirauth.Scope(store)` (`Container.Repo`), nên quyền theo loại `Dicom*`, `Binary` và chính sách truy cập có hiệu lực khi D3, D4 dùng kho.

Lỗi trả `{"error": "..."}` (JSON thường), không dùng OperationOutcome vì DICOMweb không phải API FHIR. Lỗi 5xx không lộ chi tiết nội bộ (có test: panic trong xác thực chỉ ra "Internal Server Error").

## Độ bền, tắt êm, quan sát

- Probe theo mục 6.3: `/health/live`, `/health/startup` không phụ thuộc ngoài; `/health/ready` kiểm database trong `READINESS_TIMEOUT` và 503 khi đang tắt; `/healthz` cho Docker.
- Tắt êm theo mục 6.4: SIGTERM, ready 503, chờ `SHUTDOWN_DRAIN_DELAY`, đóng cổng và chờ yêu cầu đang chạy tối đa `SHUTDOWN_TIMEOUT`, đóng kho và DB, mã thoát 0 (`pkg/httpserve.Run`; có test). Lưu ý riêng của tải lên lớn: một STOW đang chạy dài hơn `SHUTDOWN_TIMEOUT` sẽ bị cắt khi tắt; STOW phải idempotent (tải lại cùng SOPInstanceUID không tạo bản trùng) để client tải lại được. `terminationGracePeriodSeconds` phải lớn hơn `SHUTDOWN_DRAIN_DELAY + SHUTDOWN_TIMEOUT`.
- Hạn mức theo IP đếm theo từng instance (mục 5 điều 7). Log JSON; telemetry qua `pkg/observability`. Không bao giờ ghi tên bệnh nhân, mã bệnh nhân hay metadata DICOM vào log.

## Chưa có (làm ở các bước sau)

D3 STOW-RS (kèm đọc tệp DICOM thật và đọc-sửa-ghi số liệu tổng hợp trên `pkg/dicomweb` đã có từ D2) (tách luồng ghi kho và đọc metadata, chốt chặn SQ lồng sâu và tệp cắt giữa PixelData); D4 QIDO-RS, WADO-RS; D5 worker pixel data; D6 compose và e2e. Manifest K8s mẫu chưa có (dự án hiện bỏ qua K8s).
