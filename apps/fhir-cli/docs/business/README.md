# fhir-cli: tài liệu nghiệp vụ (góc nhìn BA kỹ thuật)

`fhir-cli` là **công cụ dòng lệnh** của `fhir-server`, không phải service. Giao diện của nó là *dòng lệnh, biến môi trường, `stdin`/`stdout`/`stderr` và mã thoát*; vì vậy "luồng người dùng" ở đây là chuỗi lệnh, và "hợp đồng API" là bảng tham số và mã thoát ở [03-chuc-nang.md](03-chuc-nang.md). Công cụ nhỏ (826 dòng mã) nên bộ tài liệu gộp gọn thành 4 tệp.

| # | Tài liệu | Trả lời câu hỏi |
|---|---|---|
| 1 | [01-tong-quan-va-luong-nghiep-vu.md](01-tong-quan-va-luong-nghiep-vu.md) | Dùng để làm gì, cho ai, ranh giới với `fhir-server` và `auth`, thuật ngữ, 5 luồng nghiệp vụ (tra cứu và ghi, nạp hàng loạt, export, chuyển dữ liệu giữa hai máy chủ, reindex) |
| 2 | [02-luong-nguoi-dung.md](02-luong-nguoi-dung.md) | Người vận hành gõ gì, thấy gì, lỗi nào thì làm gì; quy ước `stdout`/`stderr` để viết script; bảng mã thoát |
| 3 | [03-chuc-nang.md](03-chuc-nang.md) | Danh mục F-01..F-10: lệnh, tham số, luồng, thông điệp lỗi nguyên văn, luật liên quan |
| 4 | [04-luat-va-phan-quyen.md](04-luat-va-phan-quyen.md) | Luật BR-xx, quyền cần có ở `fhir-server`, vòng đời job export và lô import, giới hạn, kịch bản chấp nhận AT-xx, truy vết test |

Mã truy vết: `F-xx` chức năng, `BR-xx` luật, `BF-x` luồng nghiệp vụ, `AT-xx` kịch bản chấp nhận. Mô tả cú pháp ngắn gọn nằm ở [../../README.md](../../README.md).

**Nguồn sự thật:** mã (`internal/cli`, `internal/client`) và test (`cli_test.go`, `client_test.go`). Bộ này không có test chống lệch; khi lệch, mã đúng.

> **Lệch phát hiện giữa tài liệu cũ và mã (đối chiếu 2026-11-24, chưa sửa tài liệu cũ):**
> 1. `README.md` ghi chế độ `transaction` là "tất cả hoặc không". Theo mã, `import` **chia lô** theo `-size` (mặc định 100) và mỗi lô là một Bundle riêng: lô lỗi hoàn tác riêng lô đó, các lô **đã gửi trước đó vẫn được ghi**, các lô sau **không được gửi** (lệnh dừng ở lô lỗi). Chỉ khi toàn bộ đầu vào không vượt `-size` thì "tất cả hoặc không" đúng cho cả tệp (BR-07).
> 2. Cờ `-wait` (thời gian chờ job, mặc định 30 phút) của `export` và `reindex` **không có** trong `README.md` lẫn trong trợ giúp `fhir-cli help`.
> 3. `-timeout` (mặc định 60s) áp cho **từng yêu cầu HTTP kể cả tải tệp export**: một tệp tải quá 60s bị hủy (BR-14).
> 4. Nội dung phản hồi được đọc tối đa 1 GiB và **bị cắt âm thầm** nếu lớn hơn (`io.LimitReader`, không báo lỗi); mọi tệp export và Bundle kết quả đều nằm trọn trong bộ nhớ (BR-15). Với tệp export lớn hơn 1 GiB, dữ liệu ghi ra bị thiếu mà lệnh vẫn thoát 0.
