# Kế hoạch port CDS Hooks và DICOMweb

Trạng thái: **bản nháp chờ duyệt, chưa có code.** Nguồn: Medplum v5.2.18 (`~/Downloads/medplum/packages/server/src/{cds,dicom,bots,workers/dicom.ts}`). Chi tiết: [cds.md](cds.md), [dicom.md](dicom.md). Số dòng trong hai file trên là do tôi tự đọc source; chưa có ai khác kiểm.

## 1. Tóm tắt

| | CDS Hooks | DICOMweb |
|---|---|---|
| Quy mô source | rất nhỏ (82 dòng) | vừa (khoảng 900 dòng không tính test, thêm worker 246 dòng) |
| Khó ở đâu | **Không nằm ở CDS**: nó chỉ gọi `executeBot`, mà Go **chưa có cách chạy Bot** | Đọc DICOM, luồng tệp lớn, **chưa có kho đối tượng cho Binary**, worker tách pixel data |
| Hiện trạng Go | có kiểu `Bot.cdsService` và search param `cds-hook`; không có route | có kiểu `Dicom*` và search param; không có route, không có thư viện DICOM, không có kho Binary |
| Chỗ đặt (khuyến nghị) | trong `fhir-server` (nhóm route `/cds-services`) | ứng dụng riêng `apps/dicomweb` |
| Cỡ | M (phương án executor HTTP) | L (D0 đến D6) |
| Quyết định chặn | bộ thực thi Bot (Q1 của cds.md) | thư viện DICOM, kho đối tượng (Q-D2, Q-D3) |

## 2. Việc nền dùng chung

| Việc | Cần cho | Ghi chú |
|---|---|---|
| `pkg/safehttp`: **trích** từ `pkg/fhirsub/ssrf.go` (đã có: chặn dải nội bộ ở bước kết nối trên IP đã phân giải nên chống cả DNS rebinding, không theo redirect, không dùng proxy từ môi trường) thành gói dùng chung; bổ sung chế độ chỉ-HTTPS, giới hạn kích thước body và cho phép cấu hình danh sách cho phép | CDS executor, đăng nhập ngoài của `auth` (P6), và chính `pkg/fhirsub` (refactor, không đổi hành vi) | Không viết mới: hiện các hàm `blocked`, `checkEndpoint`, `newClient` đều chưa xuất khẩu và nằm trong `fhirsub` |
| Đọc-sửa-ghi có điều kiện dùng chung (`Mutate`) | DICOM cập nhật số liệu tổng hợp; `auth` đã có bản riêng ở `internal/infrastructure/store` | Chuyển lên gói chung (`pkg/fhirrouter` hoặc `pkg/fhirstore`) để không nhân đôi |
| `pkg/binarystore` (ghi/đọc/xóa luồng; hệ thống tệp, sau đó S3) | DICOM | Có (2026-11-22): `Store`, FS, Memory; ví dụ tách luồng ở `example_dicom_test.go` |
| Xác thực: `pkg/fhirauth.Verifier` + `fhirauth.Scope(repo)` | cả hai | Token do `auth` cấp (P3); cần `auth` phát claim `fhir_project` và scope như đã ghi ở kế hoạch `auth` |

## 3. Thứ tự đề xuất

1. Trích `pkg/safehttp` từ `pkg/fhirsub` và `Mutate` dùng chung (nhỏ, mở khóa cả CDS lẫn `auth` P6).
2. CDS: C0 đến C4 (sau khi chốt Q1).
3. DICOM: D0 (spike, quyết định then chốt) rồi D1 đến D6.
Hai module độc lập nhau; có thể làm song song nếu có người. Cả hai cần **`auth` P3** (token) để chạy e2e thật; trước đó dùng token JWT dev (`FHIR_JWT_HMAC_SECRET`) như `fhir-server`.

## 4. Quyết định cần bạn chốt

| # | Quyết định | Khuyến nghị |
|---|---|---|
| H1 | CDS đặt trong `fhir-server` hay app riêng | Trong `fhir-server` (cần `pkg/cds`, thêm luật vào `pkgRules`) |
| H2 | DICOMweb là app riêng hay trong `fhir-server` | App riêng `apps/dicomweb` |
| H3 | Chạy Bot cho CDS: A (HTTP), B (JS nhúng), C (sidecar Node), D (port module Bot) | A trước |
| H4 | Kho đối tượng cho Binary DICOM | Giao diện + hệ thống tệp trước, S3 sau |
| H5 | Có sửa các lỗi của Medplum (route stub trả 501, `ReferringPhysicianName`, `RetrieveURL`...) hay giữ nguyên hành vi | Sửa (đã liệt kê ở dicom.md mục 3) |

## 5. Rủi ro chung

- **CDS phụ thuộc module Bot**: nếu mục tiêu là chạy các Bot JavaScript hiện có của Medplum thì CDS không thể "xong" nếu không có phương án C hoặc D; phương án A chỉ phục vụ service CDS viết riêng.
- **Thư viện DICOM chưa được kiểm**: tên và khả năng của thư viện Go nằm ngoài source đã đọc; spike D0 là bắt buộc trước khi cam kết cỡ của D3 và D5.
- **Dữ liệu y tế có định danh** (tên, mã, ngày sinh bệnh nhân trong DICOM): cần rà soát bảo mật và phân loại dữ liệu trước khi chạy thật.
- Một số điểm trong kế hoạch được ghi "chưa xác nhận" (tên khóa của `dcmjs`, hành vi tách frame); đừng coi là đã kiểm. Số ca test và tiền tố URL đã đối chiếu lại với source.
