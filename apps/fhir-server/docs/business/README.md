# fhir-server: tài liệu nghiệp vụ (góc nhìn BA kỹ thuật)

Trả lời **"fhir-server làm gì cho ai, theo luồng nào, theo luật nào"** trước khi vào API. Chi tiết route ở [../api-contract.md](../api-contract.md) và [../api-contract/](../api-contract/); kiến trúc ở [../architecture.md](../architecture.md); vận hành ở [../runbook.md](../runbook.md); cấu hình ở [../config.md](../config.md); cú pháp và ví dụ ở [../../README.md](../../README.md).

| # | Tài liệu | Trả lời câu hỏi |
|---|---|---|
| 1 | [01-tong-quan.md](01-tong-quan.md) | Dùng để làm gì, cho ai, ranh giới với `auth`, `ws-gateway`, `dicomweb`; tác nhân; bản đồ năng lực; thuật ngữ; mô hình dữ liệu |
| 2 | [02-luong-nghiep-vu.md](02-luong-nghiep-vu.md) | 13 luồng nghiệp vụ đầu-cuối BF-1..BF-13: ghi và phiên bản, tìm kiếm, batch, bulk export, job nền, bảo trì chỉ mục, terminology, Binary, Subscription, truy cập, super-admin, triển khai |
| 3 | [03-luong-nguoi-dung.md](03-luong-nguoi-dung.md) | Từng nhân vật (ứng dụng lâm sàng, dịch vụ máy, tích hợp viên, quản trị, vận hành) đi qua bước nào, gặp lỗi gì, làm gì tiếp |
| 4 | [04-chuc-nang.md](04-chuc-nang.md) | Danh mục chức năng F-01..F-27 |
| 5 | [05-luat-va-phan-quyen.md](05-luat-va-phan-quyen.md) | Luật BR-xx, ma trận phân quyền, thứ tự kiểm tra một yêu cầu, kịch bản chấp nhận AT-xx, truy vết test |

Mã truy vết: `F-xx` chức năng, `BR-xx` luật, `BF-x` luồng nghiệp vụ, `AT-xx` kịch bản chấp nhận. Dải mã: F-01..F-14 (hồ sơ, tìm kiếm, bundle), F-15..F-20 và F-23 (terminology, export, job, Binary), F-21..F-22 và F-24..F-27 (Subscription, truy cập, vận hành).

**Nguồn sự thật:** mã và test (nghiệp vụ nằm ở `pkg/fhirrouter`, `fhirsearch`, `fhirstore`, `fhirvalidate`, `fhirauth`, `fhirsub`, `fhirbinary`, `binarystore`, `workflow`; app chỉ lắp ráp). Bộ này **không có test chống lệch tự động**; khi lệch, mã đúng. Mô hình dữ liệu ở [docs/erd/fhirstore-physical.md](../../../../docs/erd/fhirstore-physical.md).

## Điểm đáng chú ý đã kiểm trong mã

Chi tiết đầy đủ (kèm mục chưa kiểm) ở [05-luat-va-phan-quyen.md mục 5.4](05-luat-va-phan-quyen.md). Các điểm dưới đây tôi đã tự đối chiếu trong mã ngoài báo cáo của từng cụm.

**Bảo mật**
1. **`CodeSystem/$import`, `ConceptMap/$import`, `PackageRelease/$install` chỉ đòi admin khi bật JWT.** Route `POST` này rơi vào `RoleWrite` ở cổng (`httpapi/auth.go`), còn kiểm admin nằm ở `AdminChecker` chỉ có ở kho bọc `fhirauth` (`conceptmapimport.go:27`); chế độ token tĩnh thì token ghi gọi được, trái với swagger.
2. **`$reindex` cả loại chạy xuyên project:** `reindexType` dùng `r.repo` (kho thô), trong khi `reindexOne` dùng `repoOf(ctx)` (kho đã tách project). Admin của một project khởi động được reindex mọi project (tải, không lộ dữ liệu).
3. **Search hệ thống (không `_type`) với token không có chữ `s`:** `Searchable()` rỗng làm `Types` rỗng, kho hiểu là "mọi loại", rồi chỉ lọc theo chữ `r` (`fhirauth/scope.go:315`): token chỉ có `r` liệt kê được. Chưa có test.
4. **Secret của Subscription** (extension `subscription-secret`) và `channel.header` lưu bản rõ trong resource.

**Mất dữ liệu và toàn vẹn**
5. **Không có kiểm tra dữ liệu kiểu Medplum:** không đọc `strictMode`, `checkReferencesOnWrite`, không kiểm profile, binding hay đích Reference (README và brief ngụ ý có). `meta.project` do client gửi được giữ nguyên ở chế độ token tĩnh.
6. **Tệp bulk export và kết quả batch async là `Binary` base64 trong CSDL** (`bulk.go:178`, `batch_async.go:243`), không vào kho `file:`/`s3:` dù đã bật; CSDL phình theo `FHIR_EXPORT_RETENTION`.
7. **Không có outbox cho sự kiện Subscription:** tiến trình chết giữa commit và phát thì mất sự kiện; hàng đợi giao webhook và các lần thử lại đã hẹn nằm trong bộ nhớ; không đảm bảo thứ tự, có thể trùng; websocket không replay.
8. **Job `reindex` không bị dọn theo retention và không có API hủy.** Binary mồ côi (chết giữa tạo Binary và ghi nội dung) không được `sweep-binary` phát hiện.

**Tài liệu cũ lệch mã (chưa sửa)**
- `README.md` và `runbook.md` mục 13 còn ghi compartment Patient "chưa làm" (P8 đã xong).
- CapabilityStatement luôn ghi `updateCreate=false` kể cả khi bật `FHIR_UPDATE_AS_CREATE`, và không khai báo operation nào; `_total=estimate` chạy như `accurate`; `_include:iterate` được phân tích nhưng không lặp.
- Comment đầu `terminology.go` và `request.go` còn nói `$subsumes`, `$translate`, `:in/:above/:below` chưa hỗ trợ (đã có).
