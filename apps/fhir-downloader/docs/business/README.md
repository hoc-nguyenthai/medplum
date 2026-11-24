# fhir-downloader: tài liệu nghiệp vụ (góc nhìn BA kỹ thuật)

`fhir-downloader` là **service nền không có giao diện nghiệp vụ**: không có route nghiệp vụ, cổng `:8009` chỉ có probe. Việc nó làm (tải tệp đính kèm có đường dẫn ngoài về kho của hệ thống) bắt đầu từ một sự kiện ghi resource do `fhir-server` phát lên Redis và kết thúc bằng một `Binary` mới cùng lần cập nhật resource nguồn. Vì vậy "luồng người dùng" ở đây là *người dùng ghi tài liệu ở API rồi thấy đường dẫn đổi*, cộng phần của vận hành. "Hợp đồng" của nó là sự kiện trên Redis, schema `fhir`, kho Binary và các biến môi trường dùng chung với `fhir-server`.

| # | Tài liệu | Trả lời câu hỏi |
|---|---|---|
| 1 | [01-tong-quan-va-luong-nghiep-vu.md](01-tong-quan-va-luong-nghiep-vu.md) | Làm gì, cho ai, ranh giới với `fhir-server`, kho Binary và máy chủ ngoài, thuật ngữ, 5 luồng nghiệp vụ |
| 2 | [02-luong-nguoi-dung.md](02-luong-nguoi-dung.md) | Ứng dụng ghi tài liệu và vận hành đi qua bước nào, thấy gì, khi nào đường dẫn đổi, lỗi nào thì làm gì |
| 3 | [03-chuc-nang.md](03-chuc-nang.md) | Danh mục F-01..F-07: nhận sự kiện, chọn URL, tải, tạo Binary, đổi đường dẫn, xử lý lỗi, khởi động và tắt êm |
| 4 | [04-luat-va-phan-quyen.md](04-luat-va-phan-quyen.md) | Luật BR-xx, quyền và ranh giới an toàn (chống SSRF), vòng đời một lần tải, bảng thời hạn, kịch bản chấp nhận AT-xx, truy vết test, điểm lệch và rủi ro |

Mã truy vết: `F-xx` chức năng, `BR-xx` luật, `BF-x` luồng nghiệp vụ, `AT-xx` kịch bản chấp nhận. Kiến trúc, cấu hình, vận hành, hợp đồng dây: [../architecture.md](../architecture.md), [../config.md](../config.md), [../runbook.md](../runbook.md), [../api-contract.md](../api-contract.md).

**Nguồn sự thật:** mã (`pkg/fhirdownload/download.go`, `apps/fhir-downloader/internal`) và test (đường dẫn đầy đủ ở mục TRACE của tệp 4). Phía API (cờ `FHIR_AUTO_DOWNLOAD`, luật BR-66..BR-71 và kịch bản AT-65..AT-73) mô tả ở [fhir-server business](../../../fhir-server/docs/business/04-chuc-nang.md) (F-28); bộ này mô tả service **chạy** việc tải ở tiến trình riêng và thêm phần riêng của nó (sự kiện qua Redis, nhóm consumer, dead-letter).

**Đã kiểm với mã (2026-11-24):** mọi luật được đối chiếu với `download.go` và có test ở `pkg/fhirdownload` hoặc `apps/fhir-downloader` (đơn vị, tích hợp PostgreSQL và Redis thật, e2e image).

> **Điểm lệch và rủi ro phát hiện khi viết (chưa sửa mã, chi tiết ở mục 4.5):**
> 1. **Mọi mã `4xx` và `5xx` từ máy chủ ngoài bị coi là lỗi tạm** (kể cả `404`, `403`): sự kiện bị thử lại tới 5 lần cách nhau tối thiểu 30 giây rồi vào dead-letter; không có phân biệt lỗi vĩnh viễn của máy chủ ngoài (BR-12).
> 2. **Dead-letter không có công cụ xử lý lại**: sự kiện vào `...:dead` nằm đó; attachment giữ URL ngoài mãi, không có cách tự quét lại (BR-13).
> 3. **Không giới hạn số URL mỗi resource và tổng băng thông**: một resource có hàng trăm attachment ngoài sinh hàng trăm lần tải tuần tự trong một sự kiện (BR-18).
> 4. **Một URL lỗi tạm làm cả sự kiện được thử lại**, các URL đã tải xong ở lần trước được bỏ qua nhờ idempotent nhưng URL đứng sau URL lỗi bị trễ (BR-11).
> 5. **Giai đoạn chuyển**: nếu `fhir-server` còn `FHIR_AUTO_DOWNLOAD_WORKER=true` thì mỗi URL có thể bị tải hai lần (BR-19).
