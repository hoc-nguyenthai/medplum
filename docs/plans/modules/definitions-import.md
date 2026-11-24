# Kế hoạch nạp định nghĩa (#18): `PackageRelease/$install`, `CodeSystem/$import`, `ConceptMap/$import`, seed

Nguồn Medplum v5.2.18 (`~/Downloads/medplum/packages/server/src`, chỉ đọc): `fhir/operations/{packageinstall,codesystemimport,conceptmapimport}.ts`, `seed.ts`, `seeds/{searchparameters,structuredefinitions,valuesets}.ts`. Tài liệu này là kế hoạch, chưa có thay đổi mã. Lập 2026-11-23.

## 1. Hiện trạng trong Go

- Terminology (`pkg/fhirrouter/terminology*.go`, `termsearch.go`) **chỉ đọc concept nằm trong chính resource** `CodeSystem.concept[]` và `ValueSet.compose`; trần mở rộng `maxExpansion = 100000`. SNOMED/LOINC (hàng trăm nghìn đến triệu concept) không nạp được theo cách này.
- Medplum lưu concept ở bảng dẫn xuất `Coding`, `Coding_Property`, `CodeSystem_Property`; `$import` chèn thẳng vào các bảng đó (`codesystemimport.ts`), không sửa JSON của CodeSystem. Go chưa có bảng tương đương (`pkg/fhirstore/migrations` mới có `00001_init.sql`).
- `PackageRelease`, `PackageInstallation`, `ConceptMap`, `SearchParameter`, `StructureDefinition`, `ValueSet` đã có kiểu trong `pkg/fhir`. Batch/transaction đã có (`pkg/fhirrouter`).
- Định nghĩa gốc R4 (SearchParameter, StructureDefinition) trong Go nằm ở mã sinh (`searchparams_gen.go`, `schema_gen.go`, `registry_gen.go`), không phải resource trong kho. Medplum nạp chúng vào kho lúc seed để `GET /StructureDefinition?...` trả được.

## 2. Các việc và thứ tự đề xuất

| Mốc | Việc | Cần quyết định | Ghi chú |
|---|---|---|---|
| D1 | `PackageRelease/{id}/$install`: đọc `Binary` (`content.url`), chạy Bundle như batch, tạo `PackageInstallation` (`installing` → `installed`/`error`), chỉ admin project | không | Không cần bảng mới. Kiểm từng entry, lỗi thì `error` (như `validateBatchResponse`) |
| D2 | `CodeSystem/$import` (và `/{id}/$import`): `system`, `concept`, `property`, `designation`; chỉ admin; cần quyền UPDATE trên CodeSystem | **Q-D1** | Phụ thuộc cách lưu concept |
| D3 | Lookup, `$validate-code`, `$expand` đọc concept từ nơi lưu mới khi `CodeSystem.content = not-present` | Q-D1 | Chạy được trên cả ba kho (memory, SQLite, PostgreSQL) |
| D4 | `ConceptMap/$import` (+ `$translate` nếu chưa đủ) | Q-D1 | Cùng cách lưu với D2 |
| D5 | Seed định nghĩa dựng sẵn | **Q-D2** | Lệnh `fhir-cli seed definitions` hoặc bỏ |

## 3. Quyết định cần duyệt

**Q-D1: concept của CodeSystem lưu ở đâu?**
- **A (đề xuất): bảng dẫn xuất mới** `fhir.coding` (project, system, code, display, synonym_of), `coding_property`, `coding_designation`, migration `00002` cho PostgreSQL và SQLite, ghi/đọc qua một cổng nhỏ của `pkg/fhirstore`. Giống Medplum, nạp được SNOMED/LOINC, nhưng thêm migration và code ở cả hai backend. Thêm mã kiểm bằng `repotest` chạy trên memory và SQLite.
- B: nhồi concept vào `CodeSystem.concept[]` rồi `PUT`. Không cần bảng mới nhưng resource cỡ hàng chục MB, chậm, vướng trần 100000; chỉ hợp cho bảng mã nhỏ. Không đạt mục tiêu "điều kiện để có SNOMED/LOINC".

**Q-D2: seed định nghĩa gốc.** Go đã có định nghĩa trong mã sinh nên nạp vào kho chỉ phục vụ truy vấn `GET /StructureDefinition`, `/SearchParameter` của client. Đề xuất: làm sau cùng, tùy chọn qua `fhir-cli` (không tự chạy lúc khởi động), hoặc bỏ nếu chưa có người dùng cần.

## 4. Đã duyệt (2026-11-23)

Q-D1 = **A** (bảng dẫn xuất mới). Q-D2 = lệnh `fhir-cli` tùy chọn, làm sau cùng (D5). Thứ tự: D1 → D2 → D3 → D4 → D5.

## 5. Tiến độ

- **D1 xong (2026-11-23):** `POST /fhir/R4/PackageRelease/{id}/$install` (`pkg/fhirrouter/packageinstall.go`). Chỉ admin project không bị chính sách thu hẹp, hoặc super-admin (`fhirauth.Repo.RequireAdmin` qua `fhirrouter.AdminChecker`). Bundle trong Binary chạy như batch/transaction với quyền và project của người gọi; `PackageInstallation` đi `installing` → `installed`/`error`; entry lỗi làm hỏng cả lần cài (transaction hoàn tác). Nội dung Binary đọc qua `Options.BinaryContent` (container nối với kho đối tượng, `fhirbinary.ReadContent`), trần 64 MiB; `content.url` chỉ nhận `Binary/<id>` (không tải URL ngoài). Test: `pkg/fhirrouter/packageinstall_test.go`, `pkg/fhirauth/requireadmin_test.go` (memory và SQLite); đã kiểm đột biến cho cổng admin, kiểm lỗi entry và kiểm `content.url`. Chưa có e2e (làm cùng D2). `installedBy` chưa ghi (router không biết profile của người gọi).
- **D2 xong (2026-11-24):** `POST /fhir/R4/CodeSystem/$import` và `/CodeSystem/{id}/$import` (`pkg/fhirrouter/codesystemimport.go`). Chỉ admin. Concept, thuộc tính, designation lưu ở bảng `fhir_coding`, `fhir_coding_property`, `fhir_coding_designation` (migration `00002_coding.sql` cho PostgreSQL và SQLite), khóa theo id CodeSystem; cổng `fhirrouter.ConceptStore` (`conceptstore.go`: cài trong bộ nhớ; `pkg/fhirstore/coding.go`: SQL), các lớp bọc `fhirsub`/`fhirbinary` chuyển tiếp, `fhirauth` kiểm quyền sửa/đọc CodeSystem và project. Property phải khai báo ở `CodeSystem.property` (hoặc `parent`/`hierarchyMeaning`), code phải có; lỗi thì hoàn tác cả lần gọi. Mỗi loại tối đa 10000 phần tử mỗi lần gọi; display NFC; nạp lại là idempotent. `$expunge` CodeSystem xóa luôn concept. Khác Medplum: designation lưu bảng riêng chứ không thành dòng `Coding` synonym; chưa có `synonymOf`, chưa có cột `target` cho thuộc tính kiểu code (D3 dựng khi cần cho `$expand`/`is-a`). Test: `repotest` `CodeSystemImport` (memory, SQLite, và PostgreSQL khi có), `fhirauth` `TestCodeSystemImportRespectsRoleAndProject`. Chưa có e2e hai tiến trình.
- **D3 xong (2026-11-24):** khi `CodeSystem.content = not-present` và repository cài `ConceptStore`, `CodeSystem/$lookup` (display, designation, thuộc tính, `parent`, `child` tối đa 1000), `CodeSystem/$validate-code`, `ValueSet/$expand` (include: cả bảng mã, danh sách concept, filter `is-a`, `descendent-of`, `=` theo code hoặc thuộc tính; `filter` văn bản, `count`/`offset`, giao với ValueSet khác) và `ValueSet/$validate-code` đọc concept từ bảng mới (`terminologystored.go`, `QueryConcepts` ở `conceptstore.go` và `pkg/fhirstore/codingquery.go`; is-a dùng `WITH RECURSIVE`). `$validate-code` trên ValueSet chỉ gồm include trên bảng mã lưu ở kho (không exclude, không tham chiếu ValueSet) thì kiểm thẳng bằng một truy vấn, không mở rộng cả tập; ValueSet khác vẫn mở rộng như cũ. Phân cấp theo thuộc tính `hierarchyMeaning` (hoặc `parent`). Giới hạn: filter `regex` trả 422 trên kho; mở rộng trọn một bảng mã vượt `maxExpansion` (100000) trả 422, phải thu hẹp bằng filter hoặc danh sách concept (phân trang trọn bảng mã lớn chưa làm); danh sách concept chỉ trả code có trong kho; CodeSystem `not-present` chưa nạp concept nào vẫn báo 422 khi expand. `$subsumes`, `$translate` và `termsearch` (tìm theo token `:in`, `:below`) vẫn chỉ đọc concept trong resource, chưa dùng kho. Test: `repotest` `StoredTerminology` (memory, SQLite; **chưa chạy trên PostgreSQL** vì máy này không có PostgreSQL/Docker: đặt `FHIRSTORE_TEST_POSTGRES_URL` để chạy).
- D4, D5: chưa làm.

## 6. Đề xuất bắt đầu ban đầu

Làm **D1 trước** (không phụ thuộc quyết định nào, đóng được một nửa #18), trong lúc chờ duyệt Q-D1 và Q-D2.

Kiểm thử mỗi mốc theo quy ước repo: test đơn vị cạnh mã, HTTP-level trên memory và SQLite (PostgreSQL khi có `FHIRSTORE_TEST_POSTGRES_URL`), một e2e hai tiến trình khi xong D1 và D2, kèm kiểm đột biến cho các nhánh quyền.
