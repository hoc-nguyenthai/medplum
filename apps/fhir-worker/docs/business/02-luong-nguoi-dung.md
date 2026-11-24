# 2. Luồng người dùng

`fhir-worker` **không có màn hình hay route cho người dùng**. Người dùng chỉ thấy tác dụng của nó qua các yêu cầu gửi tới `fhir-server`: tạo job (`202`), hỏi trạng thái, tải kết quả, hủy; còn vận hành thấy nó qua cấu hình, log và probe. Tệp này mô tả hai nhóm đó và bảng mã trả về dẫn tới hành động. Cú pháp từng yêu cầu ở phía API xem [fhir-server business](../../../fhir-server/docs/business/03-luong-nguoi-dung.md).

## 2.1 Trạng thái job và điều người dùng thấy

| `AsyncJob.status` | Poll `GET bulkdata/{id}/status` (export) hoặc `GET job/{id}/status` (batch, reindex) | Ý nghĩa |
|---|---|---|
| `accepted`, chưa có worker nhận | `202` (export kèm `X-Progress: in-progress` và `Retry-After: 2`) | job chờ; nếu chờ quá lâu: không có worker chạy hoặc worker không cùng CSDL |
| `accepted`, đang chạy | `202` như trên | worker giữ lease và gia hạn |
| `completed` | `200` (export: manifest `output[]` với `type`, `url`, `count`; batch và reindex: `AsyncJob` có `output`) | xong |
| `error` | `500` OperationOutcome nêu nguyên nhân | job lỗi (kể cả chữ ký quyền sai) |
| `cancelled` | `410` | đã hủy |

## 2.2 Nhân vật: ứng dụng client (export lớn)

| Bước | Gọi gì | Thấy gì | Lỗi nào thì làm gì |
|---|---|---|---|
| 1. Tạo | `GET [base]/$export` (hoặc `Patient/$export`, `Group/{id}/$export`) với `Prefer: respond-async` | `202` + `Content-Location` tới `bulkdata/{id}/status` | `400` thiếu header `Prefer`, `_type` hoặc `_typeFilter` sai |
| 2. Chờ | `GET bulkdata/{id}/status` mỗi vài giây | `202` rồi `200` kèm manifest | `500`: đọc OperationOutcome; `410`: đã hủy; chờ quá lâu: báo vận hành (job kẹt) |
| 3. Lấy | `GET Binary/{id}` với `Accept: application/fhir+ndjson` cho từng `url` | file ndjson | `404`: kết quả đã hết hạn (`FHIR_EXPORT_RETENTION`, mặc định 24 giờ) hoặc khác project |
| 4. Dọn | `DELETE bulkdata/{id}/status` | `202` `Cancelled` nếu job còn `accepted` (đang chờ hoặc chạy), `202` `Deleted` nếu job đã xong, lỗi hoặc đã hủy (xóa job cùng Binary kết quả) | `404`: id sai hoặc khác loại |

## 2.3 Nhân vật: tích hợp viên (batch async)

| Bước | Gọi gì | Thấy gì | Lỗi nào thì làm gì |
|---|---|---|---|
| 1. Gửi | `POST [base]` Bundle `batch` với `Prefer: respond-async` | `202` + `Content-Location` tới `job/{id}/status` | `400` Bundle sai (kiểm trước khi tạo job); khi `FHIR_ASYNC_BATCH` tắt ở API: header bị bỏ qua, chạy đồng bộ |
| 2. Chờ | `GET job/{id}/status` | `202` rồi `200` có `output.parameter[results]` | `500`: lỗi job |
| 3. Lấy kết quả | `GET Binary/{id}` theo `results` | Bundle `batch-response` | `404`: hết hạn |
| 4. Hủy | `DELETE job/{id}/status` | dừng ở entry kế tiếp hoặc checkpoint kế tiếp | resource **đã ghi trước khi hủy không được hoàn tác** |

## 2.4 Nhân vật: quản trị (reindex cả loại)

| Bước | Gọi gì | Thấy gì | Lỗi nào thì làm gì |
|---|---|---|---|
| 1. Tạo | `POST [base]/{Loại}/$reindex` (token ADMIN) | `202` + `Content-Location` tới `job/{id}/status` | `403` thiếu quyền quản trị |
| 2. Chờ | `GET job/{id}/status` | `200` có `output.count` (số resource đã lập lại chỉ mục) | `500` kèm `403` trong OperationOutcome nếu token có AccessPolicy |

## 2.5 Nhân vật: vận hành

| Việc | Cách | Thấy gì | Lỗi nào thì làm gì |
|---|---|---|---|
| Khởi động | `fhir-worker` (hoặc image) với cấu hình trùng `fhir-server` | log "fhir-worker đang nhận job nền", `/health/ready` `200` | từ chối khởi động nêu tên biến: sửa cấu hình; `schema chưa được migrate`: chạy `fhir-server migrate up` |
| Kiểm sống và sẵn sàng | `GET /healthz`, `/health/live`, `/health/startup`, `/health/ready` | `/health/ready` `{ready, checks: {database}}`, `503` khi đang tắt hoặc DB hỏng | liveness luôn `200` khi tiến trình sống |
| Job kẹt ở `accepted` | xem `AsyncJob?status=accepted` ở API | job không có lease | worker chạy? cùng DB, schema? chu kỳ nhận job (`FHIR_JOB_MAINTENANCE_INTERVAL`)? |
| Job lỗi chữ ký | log ERROR `job lỗi`, `500` `job thiếu hoặc sai chữ ký quyền` | khóa `FHIR_JOB_SIGNING_KEY` lệch API (kể cả vừa xoay) | đồng bộ khóa, tạo lại job |
| Tắt | `SIGTERM` | `/health/ready` `503`, chờ `SHUTDOWN_DRAIN_DELAY`, job dở nhả lease, thoát mã 0 | job dở được worker khác chạy lại |

## 2.6 Bảng mã trả về dẫn tới hành động

| Mã (ở API) | Tình huống | Hành động của người dùng |
|---|---|---|
| `202` khi tạo | job đã nhận | lưu địa chỉ `Content-Location`, poll |
| `202` khi poll | job chờ hoặc đang chạy | chờ tiếp; quá lâu thì báo vận hành |
| `200` | xong | lấy kết quả |
| `500` khi poll | job lỗi | đọc OperationOutcome; sửa nguyên nhân; tạo lại |
| `410` khi poll | job đã hủy | tạo lại nếu cần |
| `404` khi lấy `Binary/{id}` | hết hạn hoặc khác project hoặc đã xóa | chạy lại job |

## 2.7 Độ trễ cần biết

| Sự kiện | Độ trễ |
|---|---|
| Tạo job tới khi worker nhận | tối đa `FHIR_JOB_MAINTENANCE_INTERVAL` (mặc định 5 giây) cộng `FHIR_JOB_RECOVERY_GRACE` (mặc định 2 giây) |
| Hủy job tới khi worker dừng | tối đa `FHIR_JOB_LEASE`/3 (mặc định khoảng 100 giây) với export; batch dừng tại checkpoint kế tiếp hoặc heartbeat, cái nào tới trước |
| Worker chết tới khi job được chạy lại | phần còn lại của lease (mặc định tối đa 5 phút) cộng chu kỳ nhận job |
| Tắt êm tới khi job dở được chạy lại | tối đa một chu kỳ nhận job (lease đã nhả) |
| Kết quả hết hạn tới khi bị dọn | `FHIR_EXPORT_RETENTION` (mặc định 24 giờ) cộng tối đa một chu kỳ bảo trì |
