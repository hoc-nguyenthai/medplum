# auth: tài liệu nghiệp vụ (góc nhìn BA kỹ thuật)

Bộ tài liệu này trả lời **"hệ thống làm gì cho ai, theo luồng nào, theo luật nào"** trước khi đi vào API. Đọc theo thứ tự; mỗi tệp tự đứng được. Chi tiết byte-by-byte của API vẫn ở [../api-contract/](../api-contract/), kiến trúc ở [../architecture.md](../architecture.md).

| # | Tài liệu | Trả lời câu hỏi | Người đọc chính |
|---|---|---|---|
| 1 | [01-tong-quan.md](01-tong-quan.md) | auth phục vụ ai, ranh giới, thuật ngữ, bản đồ năng lực, mô hình dữ liệu nghiệp vụ | Mọi người |
| 2 | [02-luong-nghiep-vu.md](02-luong-nghiep-vu.md) | Chuỗi nghiệp vụ đầu-cuối: mở tổ chức, mời người dùng, đăng nhập tới truy cập FHIR, thu hồi, nghỉ việc | PO, BA, QA |
| 3 | [03-luong-nguoi-dung.md](03-luong-nguoi-dung.md) | Từng nhân vật đi qua màn hình/bước nào, rẽ nhánh ra sao (đăng nhập, đăng ký, MFA, quên mật khẩu, đăng nhập ngoài) | UX, FE, QA |
| 4 | [04-chuc-nang.md](04-chuc-nang.md) | **Danh mục chức năng F-01..F-nn**: mục đích, tác nhân, tiền điều kiện, luồng chính/phụ, luật, lỗi, route | BA, Dev, QA |
| 5 | [05-luat-va-phan-quyen.md](05-luat-va-phan-quyen.md) | Danh mục luật nghiệp vụ BR-xx, ma trận phân quyền, vòng đời trạng thái, hạn mức, truy vết | BA, QA, bảo mật |

Quy ước: sơ đồ viết bằng Mermaid (GitHub, GoLand và Claude đều hiển thị). Mã `F-xx` (chức năng) và `BR-xx` (luật) là khóa truy vết; test và tài liệu khác nên trích dẫn các mã này.

**Nguồn sự thật:** hành vi thật nằm ở mã và test (`internal/bootstrap/*_test.go`, `e2e/`). Khi tài liệu này lệch mã, mã đúng; sửa tài liệu cùng PR. Các tệp này **không** có test chống lệch tự động như `api-contract.md`; mục "Truy vết" ở tệp 5 chỉ ra test nào bảo chứng luật nào.
