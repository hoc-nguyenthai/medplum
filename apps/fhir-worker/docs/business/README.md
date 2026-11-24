# fhir-worker: tài liệu nghiệp vụ (góc nhìn BA kỹ thuật)

`fhir-worker` là **service nền không có giao diện nghiệp vụ**: không có route nghiệp vụ nào, cổng `:8011` chỉ có probe. Việc nó làm (chạy job nền của `fhir-server`: bulk export, `$reindex` cả loại, batch async) bắt đầu từ một yêu cầu của người dùng ở `fhir-server` và kết thúc bằng một `AsyncJob` ở cơ sở dữ liệu; vì vậy "luồng người dùng" ở đây là chuỗi *tạo job ở API, hỏi trạng thái, lấy kết quả, hủy*, và phần của vận hành (bật service, xử lý job kẹt). "Hợp đồng" của nó là schema `AsyncJob`, chữ ký quyền của job và các biến môi trường dùng chung với `fhir-server`.

| # | Tài liệu | Trả lời câu hỏi |
|---|---|---|
| 1 | [01-tong-quan-va-luong-nghiep-vu.md](01-tong-quan-va-luong-nghiep-vu.md) | Làm gì, cho ai, ranh giới với `fhir-server`, kho Binary và Redis, thuật ngữ, 6 luồng nghiệp vụ (export lớn, batch async có checkpoint, reindex cả loại, tiếp quản khi worker chết, hủy và dọn kết quả, chuyển công việc từ API sang worker) |
| 2 | [02-luong-nguoi-dung.md](02-luong-nguoi-dung.md) | Ứng dụng client, tích hợp viên, quản trị và vận hành đi qua bước nào, gặp mã lỗi gì, làm gì tiếp; bảng trạng thái job và độ trễ |
| 3 | [03-chuc-nang.md](03-chuc-nang.md) | Danh mục F-01..F-10: nhận job, mở chữ ký quyền, export, reindex, batch async, hủy, dọn hết hạn, lưu kết quả, phát sự kiện, khởi động và tắt êm |
| 4 | [04-luat-va-phan-quyen.md](04-luat-va-phan-quyen.md) | Vòng đời `AsyncJob`, quyền của job, luật BR-xx, bảng thời hạn, kịch bản chấp nhận AT-xx, truy vết test, điểm lệch và rủi ro |

Mã truy vết: `F-xx` chức năng, `BR-xx` luật, `BF-x` luồng nghiệp vụ, `AT-xx` kịch bản chấp nhận. Kiến trúc, cấu hình, vận hành: [../architecture.md](../architecture.md), [../config.md](../config.md), [../runbook.md](../runbook.md).

**Nguồn sự thật:** mã (`pkg/fhirrouter/jobs*.go`, `bulk.go`, `batch_async.go`, `operation.go`, `pkg/fhirauth/seal.go`, `apps/fhir-worker/internal`) và test (đường dẫn đầy đủ ở mục TRACE của tệp 4). Nghiệp vụ job ở phía API (cú pháp `$export`, `Prefer: respond-async`) mô tả ở [fhir-server business](../../../fhir-server/docs/business/04-chuc-nang.md) (F-17, F-18, F-20); bộ này nêu phần **chạy** job, không lặp lại phần nhận yêu cầu.

**Đã kiểm với mã (2026-11-24) và chạy test của chính service:** các luật về nhận job, lease, chữ ký, tách project, kết quả ra kho, phát sự kiện, khởi động và tắt êm được đối chiếu với mã và có test ở `apps/fhir-worker` (đơn vị, tích hợp PostgreSQL, e2e image); các luật về lease, tiếp quản, hủy, batch checkpoint, hết hạn dựa vào test của `pkg/fhirrouter` (chạy chung bộ test, không riêng worker).

> **Điểm lệch và rủi ro phát hiện khi viết (chưa sửa mã, ghi để người dùng quyết; chi tiết ở mục 4.5):**
> 1. **Hủy job có độ trễ tối đa `FHIR_JOB_CANCEL_POLL` (mặc định 2 giây) khi API và worker là hai tiến trình** [ĐÃ SỬA 2026-11-24]: API chỉ đánh dấu `cancelled`, worker thấy ở vòng thăm dò hủy riêng (tách khỏi nhịp gia hạn lease); trước đó độ trễ lên tới `FHIR_JOB_LEASE`/3 (mặc định khoảng 100 giây). Batch còn dừng ở điểm checkpoint; export không có checkpoint nên dựa vào vòng thăm dò này (BR-22).
> 2. ~~Job `$reindex` cả loại không bao giờ bị dọn hết hạn~~ **(đã sửa 2026-11-24)**: `ExpireJobs` nay dọn cả `reindex?` (BR-24). Còn lại: không có API hủy hoặc xóa job `reindex`.
> 3. **Mỗi chu kỳ nhận job nộp lại mọi job đang chờ chỗ** (chưa có lease vì chưa chạy) vào bộ điều phối: mỗi lần là một goroutine chờ chỗ; job nhiều hơn `FHIR_JOB_CONCURRENCY` làm số goroutine chờ tăng theo từng chu kỳ (suy từ mã `RecoverJobs` và `workflow.Inline.Submit`, chưa kiểm bằng test; xem mục 4.5).
> 4. **Điều kiện A (hồ sơ tải khác) của việc tách chưa được chứng minh**: một export không làm chậm API ở 200.000 resource, bốn export đồng thời làm API chậm sát ngưỡng (xem [../architecture.md](../architecture.md)).
> 5. **Kết quả batch async ở worker giữ tham chiếu `Binary/<id>`** thay vì URL ký của `/storage` như khi API tự chạy (worker không có khóa ký `/storage`).
