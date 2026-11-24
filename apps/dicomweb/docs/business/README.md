# dicomweb: tài liệu nghiệp vụ (góc nhìn BA kỹ thuật)

Trả lời **"hệ thống làm gì cho ai, theo luồng nào, theo luật nào"** trước khi vào API. Chi tiết route ở [../api-contract.md](../api-contract.md), kiến trúc ở [../architecture.md](../architecture.md), vận hành ở [../runbook.md](../runbook.md).

| # | Tài liệu | Trả lời câu hỏi |
|---|---|---|
| 1 | [01-tong-quan.md](01-tong-quan.md) | dicomweb phục vụ ai, ranh giới với `fhir-server`/`auth`/kho Binary, thuật ngữ DICOM, mô hình dữ liệu |
| 2 | [02-luong-nghiep-vu.md](02-luong-nghiep-vu.md) | Chuỗi đầu-cuối: nhận ảnh từ thiết bị, đọc phim, tích hợp client cũ, sửa chỉ mục, sự cố |
| 3 | [03-luong-nguoi-dung.md](03-luong-nguoi-dung.md) | Từng nhân vật (bác sĩ đọc phim, kỹ thuật viên, tích hợp viên, vận hành) đi qua bước nào, gặp lỗi gì |
| 4 | [04-chuc-nang.md](04-chuc-nang.md) | Danh mục chức năng F-01..F-14 |
| 5 | [05-luat-va-phan-quyen.md](05-luat-va-phan-quyen.md) | Luật BR-xx, phân quyền, vòng đời trạng thái, hạn mức, kịch bản chấp nhận AT-xx, truy vết test |

Mã truy vết: `F-xx` chức năng, `BR-xx` luật, `BF-x` luồng nghiệp vụ, `AT-xx` kịch bản chấp nhận.

**Cập nhật theo D11 (2026-11-24):** thêm `DicomInstance.pixelData` tùy chọn (F-14, BF-10, BR-33..BR-37, AT-23..AT-26); khớp commit `49cbd76`.

**Nguồn sự thật:** mã và test. Bộ này không có test chống lệch tự động; khi lệch, mã đúng.

> **Đã đồng bộ (2026-11-24):** ba tài liệu cũ từng lệch mã (`../../README.md` ghi "D5, route còn lại trả 501", `../api-contract.md` ghi "bước D1, mọi route 501", mục "Chưa có" của `../architecture.md` còn liệt kê D3) đã được sửa theo mã D1 đến D11; chỉ còn `501` cho rendered cấp series/study, xóa và đường dẫn lạ dưới tiền tố.
