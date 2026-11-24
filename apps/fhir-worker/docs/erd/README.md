# fhir-worker: ERD và từ điển dữ liệu

Trả lời **"fhir-worker lưu gì, ở đâu, bảng và cột nào để làm gì, nối với nhau thế nào, nhạy cảm cỡ nào, xóa thì sao"**. Kiến trúc ở [../architecture.md](../architecture.md); nghiệp vụ job ở [../business/](../business/README.md); cấu hình ở [../config.md](../config.md).

| # | Tài liệu | Nội dung |
|---|---|---|
| 1 | [01-bang-vat-ly.md](01-bang-vat-ly.md) | Worker dùng bảng nào, schema và role, một job để lại bao nhiêu dòng (đã đo), đường ghi, bảng lớn nhanh. **Bảng và cột chi tiết viết một lần** ở [docs/erd/fhirstore-physical.md](../../../../docs/erd/fhirstore-physical.md) |
| 2 | [02-mo-hinh-logic.md](02-mo-hinh-logic.md) | `AsyncJob` (từng trường, hai extension, dạng `output`), `Binary` kết quả, dữ liệu lâm sàng do job đọc và ghi; mức nhạy cảm; ERD logic |
| 3 | [03-quan-he-va-toan-ven.md](03-quan-he-va-toan-ven.md) | Quan hệ (bản số, cách nối, ai giữ toàn vẹn, hành vi khi xóa), lease và `If-Match`, **xóa mềm giữ lại lịch sử job**, dọn dẹp, mồ côi |
| 4 | [04-kho-khac.md](04-kho-khac.md) | Kho Binary (khóa), Redis (chỉ phát sự kiện), bộ nhớ và bí mật cấu hình; bảng "bí mật nào lưu thế nào" |

## Kiểm kê kho lưu trữ

| Kho | Loại | Vị trí | Ghi bởi | Đọc bởi | Bắt buộc ở staging/production |
|---|---|---|---|---|---|
| Job và kết quả (`AsyncJob`, `Binary`) | PostgreSQL | schema **`fhir`**, role `fhir_app` (DML), `fhir_migrate` (DDL, chỉ khi tách role) | `fhir-server` (tạo, hủy, xóa job) và `fhir-worker` (nhận, chạy, ghi kết quả, dọn) | cả hai | có (SQLite bị cấm) |
| Dữ liệu lâm sàng mà job đọc và ghi | PostgreSQL | cùng schema `fhir` | `fhir-server`; `fhir-worker` ghi khi chạy batch async, lập lại chỉ mục khi `$reindex` | cả hai | có |
| Job và kết quả (dev) | SQLite | tệp `DATABASE_URL=sqlite://...` | cả hai | cả hai | không (cấm ở production) |
| Nội dung file kết quả | kho Binary | `FHIR_BINARY_STORAGE` (`file:<thư mục>` hoặc `s3:<bucket>[/<prefix>]`), khóa `binary/<id>/<versionId>` | `fhir-worker` (và `fhir-server` cho Binary thường) | `fhir-server` (`GET Binary/{id}`), `fhir-worker` (đọc Bundle đầu vào và đoạn kết quả của batch) | khi API dùng kho (phải trùng) |
| Sự kiện ghi resource | Redis Streams | stream `trivita:events:fhir.resource.changed` (chỉ `XADD`) | `fhir-worker` (resource do batch async ghi) | `fhir-notifier`, `fhir-downloader`, `fhir-server` | khi API bật Subscription hoặc tự tải |
| Khóa ký job | biến môi trường | `FHIR_JOB_SIGNING_KEY` (hoặc suy từ `FHIR_JWT_HMAC_SECRET` ở dev) | vận hành | `fhir-worker`, `fhir-server` | khi bật JWT |

**Không** có Redis dùng làm kho bền của worker, không có hàng đợi ngoài CSDL (hàng đợi là `AsyncJob` ở trạng thái `accepted` trong PostgreSQL), không có tệp cục bộ ngoài kho `file:` (nếu dùng).

## Mô hình sở hữu và quy tắc

- Một cụm PostgreSQL, một database, **mỗi schema một role** (D-DB-1, D-DB-2). `fhir-worker` **dùng chung schema `fhir` và role `fhir_app` với `fhir-server`** (D-SP-5, D-SP-8, tiền lệ `dicomweb`): lợi ích của việc tách không nằm ở ranh giới dữ liệu (không thu hẹp được quyền vì `fhir_resource` chứa mọi loại). Role `fhir_app` **không đọc được schema `identity`**; `DATABASE_REQUIRE_SCHEMA=true` từ chối chạy nếu `search_path` không phải đúng một schema riêng.
- **`fhir-server` sở hữu migration**: worker không migrate, còn migration chưa chạy thì từ chối khởi động (`schema chưa được migrate`).
- **Không có khóa ngoại**: thực thể nghiệp vụ là JSON trong `fhir_resource`; quan hệ là id trong JSON (`output`, `request`, checkpoint) và cặp `(resource_type, id)`; có test cấm FK chéo schema (`tests/dbmodel`).
- Dữ liệu của project A không lẫn project B nhờ `meta.project` (chuỗi trong JSON) và kho tách project; xem [03](03-quan-he-va-toan-ven.md).

## Nguồn sự thật và cách kiểm

- Mã: `pkg/fhirrouter/{jobs,jobs_maint,jobs_state,bulk,batch_async,operation}.go`, `pkg/fhirauth/seal.go`, `pkg/fhirbinary/wrap.go`, `pkg/fhirstore/crud.go`, `apps/fhir-worker/internal/bootstrap/container.go`; struct `pkg/fhir` (`AsyncJob`, `Binary`).
- Số liệu "một job để lại bao nhiêu phiên bản" **đo bằng chạy thật** (SQLite, 2026-11-24; xem 01 mục 1.4).
- Kiểm: `python3 -I ~/.claude/skills/service-erd-docs/scripts/check_erd_docs.py --docs apps/fhir-worker/docs/erd --migrations pkg/fhirstore/migrations/sqlite --resources AsyncJob,Binary --repo .`; các dòng "thiếu mục cho bảng `fhir_*`" là **chủ ý** (bảng được mô tả một lần ở tài liệu dùng chung, như `apps/auth/docs/erd`).

> **Điểm phát hiện khi viết (chưa sửa mã, chi tiết ở [03](03-quan-he-va-toan-ven.md) mục 3.4):**
> 1. **Xóa job hết hạn là xóa mềm**: dòng `AsyncJob` thành tombstone nhưng mọi phiên bản cũ (kèm `output`, chữ ký quyền, checkpoint) **nằm lại vĩnh viễn trong `fhir_history`**; chỉ Binary kết quả được Expunge. Đã đo: export để lại 3 phiên bản rồi thêm 1 bản `DELETE`; batch 35 entry để lại 8 rồi 9.
> 2. **Mỗi lần gia hạn lease và mỗi checkpoint là một phiên bản mới của `AsyncJob`**: job dài tạo nhiều phiên bản; checkpoint của batch chép cả mảng `ids` và `chunks` nên lịch sử tăng gần bình phương theo số entry (đã đo: 100 entry khoảng 52 KB, 400 entry khoảng 710 KB; 10.000 entry ngoại suy cỡ vài trăm MB, chưa chạy).
> 3. **Chữ ký quyền (`job-seal`) lưu trong `AsyncJob`** gồm id người gọi, grants và AccessPolicy ở dạng đọc được (base64, không mã hóa; được bảo vệ bằng HMAC chống giả mạo, không chống đọc), và nằm lại trong lịch sử.
> 4. **Stream sự kiện không có giới hạn độ dài** (`XADD` không `MAXLEN`).
