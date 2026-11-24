# dicomweb: ERD và từ điển dữ liệu

Trả lời **"dicomweb lưu gì, ở đâu, bảng và cột nào để làm gì, nối với nhau thế nào, nhạy cảm cỡ nào, xóa thì sao và ảnh nằm lại ở đâu"**. Kiến trúc ở [../architecture.md](../architecture.md); nghiệp vụ ở [../business/](../business/README.md); cấu hình ở [../config.md](../config.md); vận hành ở [../runbook.md](../runbook.md).

| # | Tài liệu | Nội dung |
|---|---|---|
| 1 | [01-bang-vat-ly.md](01-bang-vat-ly.md) | dicomweb dùng bảng nào, schema `fhir`, role `fhir_app`, `DATABASE_REQUIRE_SCHEMA`, một instance để lại bao nhiêu dòng (đã đo), đường ghi, bảng lớn nhanh. **Bảng và cột chi tiết viết một lần** ở [docs/erd/fhirstore-physical.md](../../../../docs/erd/fhirstore-physical.md) |
| 2 | [02-mo-hinh-logic.md](02-mo-hinh-logic.md) | `DicomStudy`, `DicomSeries`, `DicomInstance` (từng trường, nguồn tag DICOM, ai ghi, chỉ mục), `Binary` (tệp và frame), extension chỉ mục frame, mức nhạy cảm, ERD logic |
| 3 | [03-quan-he-va-toan-ven.md](03-quan-he-va-toan-ven.md) | Quan hệ (bản số, cách nối, ai giữ toàn vẹn, hành vi khi xóa), khóa và transaction, **xóa mềm so với Expunge và ảnh còn lại ở kho (đã đo)**, mồ côi, vòng đời instance |
| 4 | [04-kho-khac.md](04-kho-khac.md) | Kho Binary (khóa, phạm vi, mã hóa), không Redis và không sự kiện, bộ nhớ và đĩa tạm, bảng "bí mật nào lưu thế nào", bản đồ nơi PHI nằm |

## Kiểm kê kho lưu trữ

| Kho | Loại | Vị trí | Ghi bởi | Đọc bởi | Bắt buộc ở staging/production |
|---|---|---|---|---|---|
| `DicomStudy`, `DicomSeries`, `DicomInstance`, `Binary` | PostgreSQL | schema **`fhir`**, role `fhir_app` (DML); `fhir_migrate` (DDL) chỉ khi tách role, **chỉ `fhir-server migrate up` dùng** | dicomweb (STOW, `pixeldata`, `reindex`); `fhir-server` (API FHIR chung, **không qua luật STOW**) | dicomweb (QIDO, WADO), `fhir-server` | có (SQLite bị cấm) |
| Dữ liệu lâm sàng khác (Patient...) | PostgreSQL | cùng schema `fhir` | `fhir-server` | `fhir-server`; **role `fhir_app` mà dicomweb dùng đọc được cả schema** | có |
| Nội dung DICOM và frame | kho Binary | `FHIR_BINARY_STORAGE` (`file:<thư mục>` hoặc `s3:<bucket>[/<prefix>]`), khóa `binary/<id>/<versionId>` | dicomweb | dicomweb, `fhir-server` | **có, ở mọi môi trường** (khác `fhir-server`), phải trùng `fhir-server` |
| Dữ liệu dev | SQLite | `DATABASE_URL=sqlite://./fhir.db` | dicomweb | dicomweb | không (cấm ở production) |
| Tệp tạm STOW dạng metadata | đĩa cục bộ | `TMPDIR` (`dicomstow-*`) | dicomweb | dicomweb | khi dùng STOW dạng metadata |
| Hàng đợi `pixelData` | bộ nhớ | trong tiến trình | dicomweb | dicomweb | không (chỉ khi `DICOMWEB_PIXELDATA_FRAMES`) |

**Không** có Redis, không có hàng đợi bền, không phát sự kiện thay đổi resource ([04](04-kho-khac.md) mục 4.2).

## Mô hình sở hữu và quy tắc

- Một cụm PostgreSQL, một database, **mỗi schema một role** (D-DB-1, D-DB-2). dicomweb **dùng chung schema `fhir` và role `fhir_app` với `fhir-server`** (D-DW-1): `DicomStudy`, `DicomSeries`, `DicomInstance` phải tìm được qua `GET /fhir/R4/DicomStudy?...` của `fhir-server`. Ngoại lệ so với "mỗi service một role" được nêu ở architecture; hệ quả là **không thu hẹp được quyền** (role đọc được mọi loại resource của schema `fhir`). Role `fhir_app` **không đọc được schema `identity`**; `DATABASE_REQUIRE_SCHEMA=true` từ chối chạy nếu `search_path` không phải đúng một schema riêng.
- **`fhir-server` sở hữu migration**: dicomweb không có lệnh `migrate`, và từ chối khởi động khi `fhirstore.Pending > 0` (`schema chưa được migrate`).
- **Không có khóa ngoại và không có ràng buộc duy nhất** (kể cả UID DICOM): quan hệ là `Reference` trong JSON (có chỉ mục cho `study`, `series`) và con trỏ `raw`/`pixelData` tới `Binary` (không chỉ mục). Có test cấm FK chéo schema (`tests/dbmodel`).
- Cô lập project bằng `meta.project` (chuỗi trong JSON, **có chỉ mục token `_project`**, đã đo) qua `fhirauth.Scope`; `dicomweb reindex` và `pixeldata` ở CLI dùng kho **chưa tách project** (duyệt mọi project).

## Nguồn sự thật và cách kiểm

- Mã: `pkg/dicomstow/stow.go` (`storeOne`, `persist`, `updateStudy`, `dropBinary`), `pkg/dicomweb/{study,aggregates,dataset,frameindex}.go`, `pkg/dicomretrieve/{search,pixeldata,reindex,download}.go`, `apps/dicomweb/internal/bootstrap/container.go`, `pkg/fhirbinary/{sweep,wrap,access}.go`, `pkg/fhirauth/scope.go`; struct `pkg/fhir/resource_dicom{study,series,instance}.go`.
- **Số liệu đo bằng chạy thật** (SQLite và kho `file:`, 2026-11-24, `dicomstow.Store` với tệp do `pkg/dicomtest` dựng, một chương trình thăm dò viết ở thư mục tạm, **không** thêm vào repo): số dòng `fhir_history` mỗi loại, dòng `fhir_idx_*`, chỉ mục `_project`, tác dụng của xóa mềm và Expunge lên kho, kết quả `fhirbinary.Sweep` dry-run. Chưa đo trên PostgreSQL, S3 hay tệp DICOM thật (lớn hơn nhiều).
- Kiểm: `python3 -I ~/.claude/skills/service-erd-docs/scripts/check_erd_docs.py --docs apps/dicomweb/docs/erd --migrations pkg/fhirstore/migrations/sqlite --resources DicomStudy,DicomSeries,DicomInstance,Binary --repo .`; 13 dòng "thiếu mục cho bảng `fhir_*`" là **chủ ý** (bảng được mô tả một lần ở tài liệu dùng chung, như `apps/auth/docs/erd` và `apps/fhir-worker/docs/erd`).

## Điểm cần chú ý (tóm tắt; chi tiết ở các tệp)

1. **Xóa resource không xóa ảnh.** Không có đường xóa trong dicomweb; xóa mềm hay `$expunge` `DicomInstance` ở `fhir-server` **để nguyên `Binary` và tệp DICOM ở kho** (đã đo). Ảnh nguyên văn (PHI) ở lại vĩnh viễn trừ khi ai đó `$expunge` từng `Binary`. [03](03-quan-he-va-toan-ven.md) mục 3.4.
2. **`sweep-binary` không bắt được mồ côi của dicomweb** (đã đo: 0 mồ côi trên 11 khóa gồm Binary mồ côi giả lập, blob của instance đã Expunge, và blob của Binary xóa mềm). Vì Binary luôn tạo trước nội dung nên mọi khóa đều "có dòng". Tiến trình chết giữa STOW, hoặc lỗi `Blobs.Delete` lúc bù trừ, để lại ảnh ở kho mà **không công cụ nào dọn**.
3. **PHI nằm ở nhiều nơi**: tệp ở kho, `DicomInstance.metadata` (toàn bộ header), `DicomStudy` (tên, mã, ngày sinh), `fhir_idx_string` (bản rõ để tìm), và **`fhir_history` giữ mọi bản cũ**; `pixeldata` và `reindex` **mỗi lần chép lại `metadata` thành bản mới** (đã đo: lịch sử instance gấp đôi). [04](04-kho-khac.md) mục 4.5.
4. **Lịch sử study và series tăng theo số yêu cầu STOW** (đã đo: 5 instance trong 4 yêu cầu thành 5 phiên bản study và series); client gửi một instance mỗi yêu cầu làm tăng tuyến tính.
5. **Không có ràng buộc duy nhất ở CSDL**; study, series nối bằng UID mà không kiểm chéo (cùng `SeriesInstanceUID` khác study nối chéo), và trường bệnh nhân của study đóng băng theo instance đầu. [03](03-quan-he-va-toan-ven.md) mục 3.3.
6. **Không liên kết với `Patient` và `ImagingStudy`**; **không `meta.author`**; **không phát sự kiện** (Subscription không kích hoạt cho dữ liệu do dicomweb ghi).
7. **Bí mật**: dicomweb không lưu bí mật nào ở CSDL; khóa S3 (đọc được mọi ảnh mọi project) và `DATABASE_URL` là biến môi trường bản rõ. [04](04-kho-khac.md) mục 4.4.

## Lệch phát hiện (tài liệu cũ nói khác mã)

| Tài liệu cũ nói | Mã và số đo cho thấy |
|---|---|
| [../architecture.md](../architecture.md) (mục STOW-RS, "công cụ quét `fhir-server sweep-binary` xử lý nội dung mồ côi trong kho") và [../business/05-luat-va-phan-quyen.md](../business/05-luat-va-phan-quyen.md) BR-09, [../business/02-luong-nghiep-vu.md](../business/02-luong-nghiep-vu.md) | `fhirbinary.Sweep` chỉ xóa khóa mà CSDL trả `404` cho `Binary`; dicomweb luôn tạo `Binary` **trước** nội dung nên không khóa nào thỏa. **Đã đo 0 mồ côi** cho cả ba trường hợp. Tiến trình chết giữa STOW để lại `Binary` sống + blob, không ai dọn |
| `apps/auth/docs/erd/03` mục 3.3 và `apps/fhir-worker/docs/erd/03` mục 3.2: "`meta.project` không có chỉ mục" | **Có**: `fhir.SearchParams` luôn thêm `ProjectParam` (`_project`, token) và `fhirstore.reindex` lập chỉ mục mọi tham số; đã đo trên `DicomStudy`: mỗi resource có một dòng `fhir_idx_token` với `code='_project'`. Cùng cơ chế nên áp dụng cho loại của `auth` và `AsyncJob` (chưa đo trên các loại đó). Các bản ERD kia **chưa sửa** (ngoài phạm vi tệp này) |
| [../architecture.md](../architecture.md) mục Dữ liệu: "Ghi theo thứ tự CSDL trước, kho sau, bù trừ khi lỗi ... sẽ làm ở D3" | Đã làm ở D3 (`dropBinary`); câu còn ở thì tương lai |
| architecture: "dicomweb kết nối bằng cùng role ứng dụng của fhir-server ... không có role riêng" | Khớp mã và `01-schemas.sh`; ghi lại ở đây vì khác mô hình "mỗi service một role" của `docs/service-principles.md` |
