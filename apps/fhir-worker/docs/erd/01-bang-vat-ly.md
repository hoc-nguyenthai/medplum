# 1. Bảng vật lý của fhir-worker

`fhir-worker` **không có bảng riêng**. Nó chạy trên bộ bảng "kho tài liệu FHIR" của `pkg/fhirstore` trong schema `fhir` (cùng schema và role với `fhir-server`). **Mọi bảng và cột được mô tả một lần, đầy đủ ghi chú chức năng**, ở [docs/erd/fhirstore-physical.md](../../../../docs/erd/fhirstore-physical.md); tệp này chỉ nêu phần riêng của worker.

## 1.1 Worker dùng bảng nào

| Bảng | Worker dùng? | Ghi chú riêng của worker |
|---|---|---|
| `fhir_resource` | **Có, là nơi chứa mọi dữ liệu** | `AsyncJob` (hàng đợi và kết quả job), `Binary` kết quả, và dữ liệu lâm sàng mà job đọc hoặc ghi ([02](02-mo-hinh-logic.md)). Dòng `AsyncJob` được ghi lại nhiều lần trong đời một job (lease, checkpoint, kết quả) |
| `fhir_history` | **Có (tự động)** | Mỗi lần ghi `AsyncJob` thêm một dòng; job hết hạn nay bị Expunge nên lịch sử của nó cũng bị xóa (từ 2026-11-24; trước đó xóa mềm giữ nội dung cũ, mục 1.4) |
| `fhir_idx_token` | **Có** | Chỉ mục `status` (và `type`) của `AsyncJob`: vòng nhận job tìm `AsyncJob?status=accepted`, vòng dọn tìm `status=completed,error,cancelled` |
| `fhir_idx_reference` | Có, rất ít | `AsyncJob.requester` không bao giờ được router đặt nên không có dòng; dòng chỉ có từ resource lâm sàng mà batch ghi |
| `fhir_idx_string`, `_date`, `_number`, `_quantity`, `_uri` | Chỉ từ **resource lâm sàng** mà batch async ghi hoặc `$reindex` lập lại | `AsyncJob` và `Binary` không có tham số thuộc các loại này |
| `fhir_coding`, `fhir_coding_property`, `fhir_coding_designation`, `fhir_concept_mapping` | **Không dùng** | Worker không nạp terminology |
| `fhirstore_schema_version` | Chỉ đọc (kiểm `Pending`) | Bảng phiên bản goose do `fhir-server migrate` tạo; worker từ chối chạy khi còn migration chưa chạy |

## 1.2 Schema, role, migration

| Chủ đề | Giá trị |
|---|---|
| Schema PostgreSQL | `fhir` (dùng chung với `fhir-server` và `dicomweb`; `search_path` đặt ở mức role) |
| Role chạy ứng dụng | `fhir_app`: `USAGE` schema, `SELECT/INSERT/UPDATE/DELETE`, kể cả bảng do migration tương lai tạo; không DDL |
| Role migration | `fhir_migrate` (chỉ khi `DB_SEPARATE_MIGRATION_ROLES=true`): sở hữu schema, chạy DDL; **chỉ `fhir-server migrate up` dùng** |
| Chế độ mặc định (dev) | SQLite hoặc một role sở hữu schema; worker **không** tự migrate |
| Từ chối khởi động | `fhirstore.Pending > 0` (`schema chưa được migrate`), hoặc `DATABASE_REQUIRE_SCHEMA=true` mà `search_path` không phải đúng một schema riêng |
| Backend khác | SQLite (dev, cột `content` là text, không schema); MySQL: **chưa hỗ trợ** cho `fhirstore` |
| Dọn dẹp | vòng bảo trì của worker xóa job hết hạn (`FHIR_EXPORT_RETENTION`): Binary kết quả và **dòng `AsyncJob` đều bị Expunge** (từ 2026-11-24; trước đó `AsyncJob` bị xóa mềm; mục 1.4, [03](03-quan-he-va-toan-ven.md) mục 3.4) |

## 1.3 Đường ghi của một job

```mermaid
sequenceDiagram
    autonumber
    participant API as "fhir-server"
    participant W as "fhir-worker"
    participant DB as "Schema fhir"
    participant K as "Kho Binary"
    API->>DB: "POST AsyncJob accepted: fhir_resource v1 + fhir_history + fhir_idx_token(status)"
    W->>DB: "claim: PUT AsyncJob (If-Match), transactionTime = lease: version mới"
    loop "mỗi lease/3 khi job còn chạy"
        W->>DB: "heartbeat: PUT AsyncJob gia hạn lease: version mới"
    end
    opt "batch async"
        W->>DB: "mỗi 10 entry hoặc 5 giây: PUT AsyncJob extension job-state (checkpoint): version mới"
    end
    W->>K: "ghi nội dung file kết quả: binary/id/versionId"
    W->>DB: "POST Binary (metadata, meta.project)"
    W->>DB: "PUT AsyncJob completed + output: version cuối"
    Note over DB: "mỗi PUT/POST là một transaction: fhir_resource + fhir_history + xóa và ghi lại fhir_idx_*"
    W->>DB: "hết hạn: Expunge Binary (dòng, lịch sử, chỉ mục) rồi Expunge AsyncJob (từ 2026-11-24; trước đó DeleteResource: tombstone + version DELETE)"
    W->>K: "xóa nội dung các phiên bản của Binary"
```

Khác với [đường ghi chung](../../../../docs/erd/fhirstore-physical.md): các lần ghi `AsyncJob` ở đây do nhiều tiến trình tranh nhau; chống ghi đè bằng `If-Match` (mục 3.2 của [03](03-quan-he-va-toan-ven.md)).

## 1.4 Một job để lại bao nhiêu dòng (đã đo)

Đo bằng chạy thật trên SQLite (2026-11-24), cấu hình mặc định ngoại trừ ghi chú; đếm bằng `ReadHistory("AsyncJob", id)`.

| Job | Phiên bản `AsyncJob` khi xong | Sau khi hết hạn (`FHIR_EXPORT_RETENTION`) | `Binary` còn lại sau khi hết hạn |
|---|---|---|---|
| Export 3 Patient (xong dưới một chu kỳ heartbeat) | **3**: tạo (`POST`), nhận (`PUT`), hoàn tất (`PUT`) | **4**: thêm một bản `DELETE`; 3 bản cũ **còn nguyên nội dung** | 0 (Expunge) |
| Batch async 35 entry (`FHIR_BATCH_CHECKPOINT_ENTRIES=10`) | **8**: tạo, nhận, checkpoint đầu, 4 checkpoint theo đoạn (10, 20, 30, 35 entry), hoàn tất | **9** | 0 (Expunge: Bundle đầu vào, đoạn tạm và kết quả đều bị xóa) |

Quy luật suy ra từ mã (job dài chưa đo): phiên bản = 2 (tạo, nhận) + 1 (hoàn tất hoặc lỗi) + số lần heartbeat (`FHIR_JOB_LEASE`/3, mặc định khoảng 100 giây, mỗi lần nếu job còn `accepted`) + với batch: 1 + số checkpoint (mỗi 10 entry hoặc 5 giây).

**Dung lượng lịch sử của batch tăng gần bình phương theo số entry** (đã đo, SQLite, tổng JSON của mọi phiên bản `AsyncJob`): 100 entry POST không điều kiện: 14 phiên bản, khoảng 52 KB; 400 entry: 44 phiên bản, khoảng 710 KB (gấp 4 lần entry cho gấp khoảng 13,5 lần dung lượng). Nguyên nhân: mỗi checkpoint (extension `job-state`) chép **toàn bộ** mảng `ids` (một id cấp sẵn cho mỗi POST không điều kiện, theo vị trí entry) và mảng `chunks`, trong khi số checkpoint tỉ lệ với số entry. Ngoại suy (chưa chạy): batch 10.000 entry POST ở nhịp 10 entry là khoảng 1.000 phiên bản, cỡ vài trăm MB JSON trong `fhir_history`, và **không được giải phóng** khi job hết hạn (mục 1.5).

## 1.5 Bảng lớn nhanh và hệ quả vận hành

| Bảng | Vì sao lớn | Hệ quả |
|---|---|---|
| `fhir_history` | mỗi lần ghi `AsyncJob` thêm một dòng chứa **cả nội dung** (kèm `output`, chữ ký quyền, checkpoint); job hết hạn nay bị Expunge nên lịch sử cũng bị xóa (từ 2026-11-24; trước đó xóa mềm không xóa dòng cũ) | lịch sử chỉ tồn tại trong đời job (tới `FHIR_EXPORT_RETENTION`); job hết hạn từ trước bản sửa còn lại lịch sử ([03](03-quan-he-va-toan-ven.md) mục 3.4) |
| `fhir_resource` | job hết hạn nay bị Expunge (không còn tombstone, từ 2026-11-24); job hết hạn từ trước còn tombstone `{resourceType}` (nhỏ) | không tích lũy thêm |
| `fhir_idx_token` | mỗi `AsyncJob` có dòng `status` (và `type` nếu đặt); bị xóa khi xóa mềm | nhỏ, tự dọn |
| `Binary` (trong `fhir_resource`, nếu không dùng kho) | file export tới 8 MiB base64 mỗi dòng | dùng `FHIR_BINARY_STORAGE` để đưa nội dung ra kho; xóa hết hạn bằng Expunge nên giải phóng được |
