# 5. Luật nghiệp vụ, phân quyền, kịch bản, truy vết

Loại luật: `BM` nghiệp vụ · `SEC` bảo mật · `DATA` toàn vẹn · `OPS` vận hành. Dải mã: BR-01..09 REST và phiên bản; 10..19 tìm kiếm; 20..24 bundle; 25..29 kiểm dữ liệu khi ghi; 30..34 job, export, reindex, expunge; 35..40 Subscription; 41..45 Binary; 46..55 xác thực, scope, AccessPolicy, project, super-admin; 56..60 terminology; 61..65 vận hành.

## 5.1 Luật nghiệp vụ

### Nhóm REST và phiên bản (BR-01..09)

| Mã | Luật | Loại | F |
|---|---|---|---|
| BR-01 | Id của resource do server cấp khi tạo (UUID v4, id client gửi bị bỏ qua) và `meta.versionId`, `meta.lastUpdated` client gửi bị xóa ở create và update | DATA | F-01, F-02, F-03 |
| BR-02 | Mỗi lần ghi thành công (create, update, patch, delete) sinh `versionId` mới và một dòng lịch sử; `ETag` là `W/"<versionId>"`; update luôn sinh phiên bản mới kể cả khi nội dung không đổi | DATA | F-01, F-03, F-04, F-05, F-06 |
| BR-03 | `If-Match` khác phiên bản hiện tại thì 412 `Phiên bản không khớp: hiện tại <versionId>`; so sánh và ghi nằm trong cùng transaction có khóa dòng nên hai PUT đồng thời với cùng `If-Match` chỉ một thành công | DATA | F-03, F-04 |
| BR-04 | `If-None-Exist` tạo khi 0 khớp, trả 200 resource có sẵn khi 1 khớp, 412 khi nhiều khớp; hai yêu cầu đồng thời cùng tiêu chí được tuần tự hóa bằng khóa tư vấn (project, loại, tiêu chí) | DATA | F-01, F-09 |
| BR-05 | PUT vào id chưa có chỉ tạo khi `FHIR_UPDATE_AS_CREATE=true`, id phải khớp `^[A-Za-z0-9\-.]{1,64}$`, không có `If-Match`, và resource đã xóa (410) không bao giờ được tạo lại | BM | F-03 |
| BR-06 | Xóa là xóa mềm: phiên bản xóa vào lịch sử, chỉ mục bị gỡ, đọc trả 410, xóa lặp trả 204; chỉ `$expunge` (F-19) xóa vĩnh viễn | DATA | F-02, F-05, F-06 |
| BR-07 | Thao tác có điều kiện (PUT, PATCH, DELETE theo query; POST với If-None-Exist) cần tiêu chí search (không thì 400); kết quả: PUT 0 khớp tạo 201, 1 khớp cập nhật 200, nhiều khớp 412; PATCH 0 khớp 404, nhiều khớp 412; DELETE 0 khớp 204, nhiều khớp 412 | BM | F-01, F-03, F-04, F-05 |
| BR-08 | PATCH chỉ nhận `application/json-patch+json` (thiếu header vẫn qua), kết quả phải giữ nguyên `resourceType` và `id` và qua kiểm dữ liệu như một lần ghi thường | DATA | F-04 |
| BR-09 | Quyền chữ cái SMART cho mỗi tương tác: create `c`; read, vread, history `r`; update `u` (thêm `c` khi tạo bằng update-as-create); patch `r` và `u`; delete `d`; search `s`; thao tác có điều kiện thêm `s`; chỉ áp khi bật JWT, thiếu thì 403 `Token thiếu quyền <chữ> (SMART) trên <Type>` | SEC | F-01..F-08, F-14 |

### Nhóm tìm kiếm (BR-10..19)

| Mã | Luật | Loại | F |
|---|---|---|---|
| BR-10 | `_count` mặc định 20, trần 1000 (lớn hơn bị cắt thầm lặng), số âm hoặc không phải số là 400 `_count không hợp lệ: "<x>"`, `_count=0` trả Bundle không entry | BM | F-07, F-08, F-12 |
| BR-11 | `_total` mặc định `none` (Bundle không có `total`); `estimate` và `accurate` đều chạy `COUNT(*)` chính xác; `_summary=count` ép `_count=0` và `_total=accurate`; `total` không tính entry include | DATA | F-07, F-08 |
| BR-12 | Phân trang theo offset: có `next` khi còn dữ liệu (lấy `count+1` dòng), có `previous` khi `_offset` > 0; không có con trỏ ổn định nên ghi xen giữa các trang có thể làm lệch trang | DATA | F-07, F-08 |
| BR-13 | Tham số string so tiền tố không phân biệt hoa thường và bỏ dấu (NFD bỏ dấu kết hợp, `đ` thành `d`); `:exact` phân biệt hoa thường và dấu; `:contains` so chứa theo cùng chuẩn hóa; token `:text` so tiền tố văn bản đã chuẩn hóa | BM | F-07 |
| BR-14 | Prefix `eq ne gt lt ge le sa eb ap` áp cho date (so theo khoảng), number và quantity (độ chính xác theo số chữ số); `ap` lấy biên 10 phần trăm; ngày hoặc số sai là 400 | BM | F-07 |
| BR-15 | Tham số hoặc modifier không biết là 400 (`tham số search không hợp lệ`, `modifier không hợp lệ`); chưa hỗ trợ và bị từ chối: `_query`, `_contained`, `_containedType`, tham số `special`, `:identifier`, `:in`/`:not-in` trên tham số không phải token | BM | F-07, F-08, F-13 |
| BR-16 | Chained search và `_has` chạy bằng sub-search qua cùng repository (giữ nguyên tách project và quyền); sâu tối đa 4 cấp, mỗi sub-search tối đa 5000 resource khớp, vượt thì 400 `chained search/_has khớp quá nhiều resource X (> 5000); thu hẹp tiêu chí`; kết quả là ảnh chụp tại thời điểm tìm | OPS | F-07 |
| BR-17 | `:in`/`:not-in` mở rộng ValueSet và `:below`/`:above` đi theo cây concept của CodeSystem, đều chỉ dùng dữ liệu lưu trong server, tối đa 5000 mã mỗi điều kiện (vượt thì 400); mã lạ không khớp gì; `:not-in` với ValueSet rỗng thì điều kiện luôn đúng | BM | F-07 |
| BR-18 | `_sort` theo giá trị đầu tiên của tham số, thiếu giá trị luôn xếp cuối cả khi giảm dần, khóa phụ là loại rồi id; không sắp theo composite hoặc special (400) | DATA | F-07, F-08 |
| BR-19 | Kết quả search, `_include`, `_revinclude`, `$everything` và GraphQL chỉ chứa resource thuộc project và AccessPolicy của người gọi, thuộc loại người gọi có chữ `r` hoặc `s`; điều kiện project và chính sách được đẩy xuống SQL nên `total` và phân trang chỉ đếm phần được thấy; search hệ thống không `_type` bị giới hạn ở loại có chữ `s` | SEC | F-02, F-06, F-07, F-08, F-12, F-13 |

### Nhóm Bundle (BR-20..24)

| Mã | Luật | Loại | F |
|---|---|---|---|
| BR-20 | Bundle gửi vào `POST /` phải là `Bundle` có `type` là `batch` hoặc `transaction` và mọi entry có `request.method` và `request.url`; sai thì 400 trước khi ghi bất cứ thứ gì | DATA | F-09, F-10 |
| BR-21 | Batch xử lý entry tuần tự, lỗi từng entry độc lập và luôn trả 200; transaction nguyên tử trong một transaction CSDL, lỗi ở entry nào thì hủy tất cả và trả mã HTTP của entry đó kèm `Bundle.entry[<i>]` | DATA | F-09, F-10 |
| BR-22 | Transaction xử lý theo thứ tự DELETE, POST, PUT/PATCH, GET; id của POST được cấp trước và `urn:uuid:` trong `reference` được thay bằng `Type/id`; POST có `ifNoneExist` khớp 1 thì dùng lại resource cũ | BM | F-09 |
| BR-23 | Quyền kiểm theo từng entry như thao tác riêng; bundle cần vai trò WRITE ở tầng HTTP kể cả khi chỉ chứa GET; ở batch entry thiếu quyền thành `403` riêng của entry, ở transaction thành lỗi cả bundle; batch async chạy với quyền đã niêm phong của người gọi | SEC | F-09, F-10 |
| BR-24 | Batch async chỉ khi `FHIR_ASYNC_BATCH=true` và `Prefer: respond-async`; chỉ cho batch (transaction là 400); mỗi entry chạy ít nhất một lần nhờ checkpoint (mặc định mỗi 10 entry hoặc 5 giây); POST không điều kiện không tạo trùng khi chạy lại; hủy không hoàn tác entry đã ghi; kết quả giữ `FHIR_EXPORT_RETENTION` | OPS | F-10 |

### Nhóm kiểm dữ liệu khi ghi (BR-25..29)

| Mã | Luật | Loại | F |
|---|---|---|---|
| BR-25 | Mọi create, update, patch (kể cả entry bundle) qua `fhirvalidate`; issue mức `error` thì 400 OperationOutcome với `expression` là đường dẫn; `warning` không chặn ghi | DATA | F-01, F-03, F-04, F-09, F-11 |
| BR-26 | Phạm vi kiểm: cấu trúc, kiểu, cardinality, choice type, regex nguyên thủy, invariant FHIRPath; không có profile, slicing, fixed/pattern, binding terminology | DATA | F-11 |
| BR-27 | `FHIR_SKIP_VALIDATION=true` bỏ kiểm khi ghi và bị cấm ở staging/production (service từ chối khởi động); `FHIR_SKIP_CONSTRAINTS=true` chỉ bỏ invariant; `$validate` luôn kiểm | OPS | F-11 |
| BR-28 | Server không kiểm tồn tại của đích Reference khi ghi và không kiểm tham chiếu ngược khi xóa; tham chiếu treo là hợp lệ | DATA | F-05, F-11 |
| BR-29 | Khi bật JWT, `meta.project` của resource ghi luôn bị ghi đè bằng project của người gọi (giá trị client gửi bị bỏ); khi dùng token tĩnh không có tách project nên `meta.project` do client gửi được giữ nguyên | SEC | F-01, F-03, F-11 |

### Job nền, export, reindex, expunge (BR-30..34)

| Mã | Luật | Loại | F |
|---|---|---|---|
| BR-30 | Mỗi job là một AsyncJob chỉ có bốn trạng thái `accepted`, `completed`, `error`, `cancelled`; một replica chỉ chạy job khi giành được lease (`transactionTime` + FHIR_JOB_LEASE, cập nhật có `If-Match`), gia hạn mỗi JobLease/3, và job `accepted` có lease hết hạn cùng `requestTime` cũ hơn 30 giây sẽ được tiếp quản bởi vòng bảo trì. | OPS | F-20, F-17, F-18 |
| BR-31 | Khi JWT bật, job chạy với quyền dựng lại từ chữ ký job-seal (HMAC-SHA256 ràng buộc id job, project, request, ảnh chụp quyền); chữ ký thiếu, sai hoặc ảnh chụp phiên bản cũ thì job ghi `error` và không chạy; FHIR_JOB_SIGNING_KEY bắt buộc và >= 32 ký tự khi dùng JWKS. | SEC | F-20, F-17 |
| BR-32 | Bulk export cần `Prefer: respond-async`, chỉ nhận ndjson, `_type` hợp lệ, `_since` RFC3339, `_typeFilter` đúng `Type?query`, chỉ xuất dữ liệu người gọi được đọc (project, scope, AccessPolicy), và `_type` tường minh ngoài quyền làm job lỗi chứ không bỏ qua. | SEC | F-17 |
| BR-33 | `$reindex` và `$expunge` đòi RoleAdmin với mọi method; `$reindex` một resource chạy đồng bộ, cả loại chạy nền; `$expunge` xóa vĩnh viễn resource, lịch sử, chỉ mục (và concept, ánh xạ của CodeSystem, ConceptMap) trong một transaction và không hoàn tác; cả hai bị chặn với token có AccessPolicy ở đường một resource. | DATA | F-18, F-19 |
| BR-34 | Hủy job `accepted` đánh dấu `cancelled` và bỏ kết quả; hủy hoặc quá FHIR_EXPORT_RETENTION của job đã kết thúc xóa AsyncJob cùng mọi Binary kết quả (export và batch); job lỗi hoặc bị hủy giữa chừng không để lại Binary kết quả. | DATA | F-17, F-20 |

### Binary (BR-41..45)

| Mã | Luật | Loại | F |
|---|---|---|---|
| BR-41 | Khi FHIR_BINARY_STORAGE bật, nội dung Binary nằm trong kho theo khóa `binary/<id>/<versionId>` còn CSDL chỉ giữ metadata; `data` base64 ghi qua API bị chặn quá FHIR_BASE64_BINARY_MAX_BYTES (mặc định 1 MiB). | DATA | F-23 |
| BR-42 | Ghi Binary theo thứ tự CSDL trước, kho sau; lỗi kho thì bù trừ (POST xóa resource vừa tạo, transaction xóa các khóa đã ghi); nội dung mồ côi do tiến trình chết chỉ được dọn bằng `sweep-binary`. | DATA | F-23 |
| BR-43 | URL `/storage` hợp lệ chỉ khi chữ ký RSA-SHA256 đúng với method, phiên bản, project, chưa quá `Expires` (FHIR_STORAGE_URL_TTL mặc định 1h, tối đa 24h); thiếu chữ ký là 401, hết hạn là 410, phiên bản PUT cũ là 409; staging và production bắt buộc khóa ký cố định và https. | SEC | F-23 |
| BR-44 | Tải lên bị từ chối khi đuôi tên hoặc loại nội dung nằm trong danh sách chặn, khi `X-Security-Context` sai dạng, hoặc khi nội dung sau giải nén vượt FHIR_STORAGE_MAX_UPLOAD_BYTES (mặc định 256 MiB); đọc ra buộc tải xuống trừ bốn loại ảnh an toàn. | SEC | F-23 |
| BR-45 | `sweep-binary` chỉ xóa khóa quá ân hạn (>= 1h, mặc định 24h) mà CSDL trả 404 dứt khoát, tối đa `max-delete` mỗi lượt, và dừng khi quá nửa khóa là mồ côi trừ khi `-force`; expunge Binary xóa nội dung mọi phiên bản sau commit. | OPS | F-23, F-19 |

### Terminology (BR-56..60)

| Mã | Luật | Loại | F |
|---|---|---|---|
| BR-56 | Tra cứu terminology (`$lookup`, `$expand`, `$subsumes`, `$translate`, `$validate-code`) chỉ cần RoleRead (kể cả POST), chỉ dùng CodeSystem, ValueSet, ConceptMap trong server của project người gọi và không có thuật ngữ ngoài. | BM | F-15 |
| BR-57 | `$expand` tối đa 100000 mã (quá thì 422), `count` và `offset` không âm, `filter` văn bản không phân biệt hoa thường, chỉ hỗ trợ filter `is-a`, `descendent-of`, `=`, `regex` (kho dẫn xuất không có `regex`). | DATA | F-15 |
| BR-58 | Modifier `:in`, `:not-in`, `:below`, `:above` được giải trước khi truy vấn thành tối đa 5000 mã mỗi điều kiện (quá thì 400) và `:below`, `:above` chỉ dùng cây concept nằm trong JSON của CodeSystem. | DATA | F-15, F-07 |
| BR-59 | `$import` chỉ dành cho quản trị, nhận Parameters với tối đa 10000 phần tử mỗi loại mỗi lần gọi, chạy trong một transaction, property phải được khai báo và mã phải có sẵn, và gọi lại cùng dữ liệu là an toàn. | DATA | F-16 |
| BR-60 | Bảng dẫn xuất (`fhir_coding*`, `fhir_concept_mapping`) chỉ được tra khi CodeSystem có `content = not-present` (ConceptMap thì luôn cộng cả ánh xạ nạp vào); expunge CodeSystem hoặc ConceptMap xóa luôn bảng dẫn xuất của nó; `PackageRelease/$install` chỉ dành cho quản trị, chạy Bundle bằng quyền người gọi và ghi PackageInstallation `installing`, rồi `installed` hoặc `error`. | DATA | F-16, F-19 |

---

### Subscription (BR-35..BR-40)

| Mã | Luật | Loại | F |
|---|---|---|---|
| BR-35 | Sự kiện thay đổi chỉ được phát sau khi dữ liệu đã commit: transaction rollback không phát gì, và lỗi khi phát chỉ được log, không làm hỏng lệnh ghi. | DATA | F-21 |
| BR-36 | Một Subscription chỉ nhận thông báo khi `status=active`, kênh là `rest-hook` hoặc `websocket`, `criteria` dùng được (không chained, `_has`, terminology), endpoint rest-hook là http(s) không có userinfo, chưa quá `end`, interaction nằm trong extension (rỗng: cả ba), cùng `meta.project` với resource và resource khớp `criteria`. | BM | F-21, F-22 |
| BR-37 | Địa chỉ webhook thuộc loopback, mạng riêng, link-local (kể cả metadata đám mây), CGNAT, multicast hoặc dải dành riêng bị chặn ở bước kết nối trên IP đã phân giải (chống DNS rebinding), không dùng proxy môi trường, không theo redirect; `FHIR_WEBHOOK_ALLOW_PRIVATE=true` bị từ chối ở staging/production. | SEC | F-21 |
| BR-38 | Giao webhook thử lại với lỗi mạng, `408`, `429`, `5xx` tới `FHIR_WEBHOOK_MAX_ATTEMPTS` lần với thời gian chờ 1 giây gấp đôi mỗi lần và trần 1 phút; mọi mã khác (kể cả `3xx`, mọi `4xx` còn lại) và đích bị chặn thì bỏ cuộc ngay; thông báo có `X-Signature` HMAC-SHA256 của body khi Subscription có secret. | OPS | F-21 |
| BR-39 | Subscription giao thất bại lặp lại (đạt `maxConsecutiveFailures` trong `timeWindowSeconds` kể từ lần thành công cuối) được tự chuyển `off` kèm `error`, không ghi đè Subscription đã bị đổi trạng thái hoặc sửa song song; hàng đợi giao nằm trong bộ nhớ nên mất khi tiến trình dừng, và cache danh sách Subscription làm replica khác thấy thay đổi sau tối đa 30 giây. | OPS | F-21 |
| BR-40 | Token bind websocket hết hạn sau 5 phút, ký HMAC-SHA256 bằng khóa dùng chung, mang ảnh chụp quyền của người lấy token, và mỗi sự kiện chỉ tới kết nối nào có chữ `r` trên loại resource và resource thỏa AccessPolicy; token access gốc hết hạn thì binding thôi nhận. | SEC | F-22 |

### Xác thực, scope, AccessPolicy, project, super-admin (BR-46..BR-55)

| Mã | Luật | Loại | F |
|---|---|---|---|
| BR-46 | Chỉ một chế độ xác thực được bật: JWT loại trừ token tĩnh và `FHIR_AUTH_DISABLED`; token tĩnh dài tối thiểu 24 ký tự, được so khớp bằng băm SHA-256 với thời gian không phụ thuộc nội dung; staging/production cấm tắt xác thực và cấm `FHIR_JWT_HMAC_SECRET`. | SEC | F-24 |
| BR-47 | Token JWT phải đúng chữ ký (khóa `kid` trong JWKS, cache 10 phút, làm mới khi gặp `kid` lạ tối đa mỗi 30 giây), đúng `iss`, có `aud` đúng, chưa hết `exp` (lệch đồng hồ 30 giây) và có `sub`; sai thì `401`, đúng chữ ký nhưng không dùng được cho FHIR thì `403`. | SEC | F-24 |
| BR-48 | Token JWT phải có claim project (trừ super-admin) và ít nhất một scope cấp quyền, nếu không thì `403`; claim AccessPolicy sai định dạng, có khóa lạ hoặc kiểu sai cũng `403`. | SEC | F-24 |
| BR-49 | Mỗi chữ `c r u d s` của scope SMART chỉ cấp đúng thao tác của nó; scope có loại không biết, chữ lạ, tiền tố khác `system` và `user` (kể cả `patient/`) không cấp quyền. | SEC | F-24 |
| BR-50 | PATCH cần cả `r` và `u`; thao tác có điều kiện (PUT, PATCH, DELETE theo query, `If-None-Exist`) cần thêm `s`; trong batch mỗi entry bị kiểm theo thao tác của chính nó. | SEC | F-24 |
| BR-51 | AccessPolicy giới hạn theo loại: nhiều rule cùng loại là OR (tối đa 16), mảng rỗng từ chối mọi loại, `readonly` chặn ghi, `deny` cấm loại kể cả khi có rule `*`, rule `*` không bao giờ khớp các loại quản trị, và `_compartment=Patient/<id>` là điều kiện biểu thức duy nhất cho phép. | SEC | F-24 |
| BR-52 | AccessPolicy được áp ở mọi đường đọc và ghi (đọc, vread, history, search, `_include`, ghi, batch, websocket): resource ngoài tiêu chí trả `404` khi đọc và `403` khi ghi ra ngoài tiêu chí; token có policy bị chặn `$reindex` và `$expunge` một resource. | SEC | F-24, F-22 |
| BR-53 | `meta.project` do server gán từ token và ghi đè giá trị client gửi; resource của project khác trả `404` (riêng id đã xóa của project khác trả `410`). | DATA | F-25 |
| BR-54 | Chỉ khi `FHIR_JWT_SUPER_ADMIN=true` scope `fhir.super` mới có hiệu lực; super-admin phải chọn project bằng `X-Fhir-Project` hợp lệ (nếu không thì `400`), và mọi yêu cầu của họ (kể cả bị từ chối) ghi log `audit=true` không chứa dữ liệu hay token; người thường chọn project khác bị `403`. | SEC | F-25 |
| BR-55 | `assign-project` chỉ gán project cho resource chưa có `meta.project`, không đụng resource đã có project, mỗi resource thêm một phiên bản, chạy lại an toàn, và chỉ chạy trên database lưu bền đã migrate. | DATA | F-25, F-26 |

### Vận hành (BR-61..BR-65)

| Mã | Luật | Loại | F |
|---|---|---|---|
| BR-61 | `/healthz` luôn `200` khi tiến trình chạy; `/readyz` trả `503` khi database (trong `READINESS_TIMEOUT`) hoặc broker (khi bật Subscription) không phản hồi, hoặc khi đang tắt. | OPS | F-27 |
| BR-62 | Khi nhận SIGTERM, `/readyz` chuyển `503` ngay, server chờ `SHUTDOWN_DRAIN_DELAY` rồi mới dừng nhận kết nối và chờ yêu cầu đang chạy tối đa `SHUTDOWN_TIMEOUT`; job nền chưa xong nhả lease thay vì đánh dấu lỗi. | OPS | F-27 |
| BR-63 | Hạn mức theo IP (`FHIR_RATE_LIMIT`, mặc định 20 yêu cầu/giây ở staging/production nếu không đặt) đứng trước xác thực, trả `429` kèm `Retry-After`, đếm riêng từng instance, và chỉ tin `X-Forwarded-For` từ proxy khai trong `FHIR_TRUSTED_PROXIES`. | SEC | F-27 |
| BR-64 | Body vượt 16 MiB bị `413`; CORS chỉ cho origin trong `FHIR_ALLOWED_ORIGINS` (staging/production cấm `*` và `http`); mọi phản hồi, kể cả 401, 403, 429, mang header bảo mật; mọi lỗi dưới `/fhir/R4` là `OperationOutcome`. | SEC | F-27 |
| BR-65 | Cấu hình sai, hoặc schema chưa migrate khi `DATABASE_AUTO_MIGRATE=false`, làm server từ chối khởi động với thông báo nêu tên biến; `DATABASE_MIGRATE_URL` bắt buộc đi cùng `DATABASE_AUTO_MIGRATE=false`; các lệnh `assign-project`, `sweep-binary` từ chối chạy khi schema chưa migrate hoặc database là bộ nhớ. | OPS | F-26, F-27 |

### Tự tải attachment ngoài (BR-66..BR-71)

| Mã | Luật | Loại | F |
|---|---|---|---|
| BR-66 | Tự tải mặc định TẮT (`FHIR_AUTO_DOWNLOAD=false`); bật phải có `FHIR_BINARY_STORAGE`; các cờ phụ (`ALLOWED_PREFIXES`, `ALLOW_PRIVATE`, ...) đặt khi chưa bật là lỗi cấu hình; `MAX_BYTES` âm hoặc `TIMEOUT` không dương bị từ chối; `ALLOW_PRIVATE` cấm ở staging/production. Mọi lỗi nêu tên biến. | OPS | F-28 |
| BR-67 | Chỉ tải `Attachment.url` tuyệt đối `http`/`https` nằm NGOÀI địa chỉ API và `/storage` của chính server; tiền tố bỏ qua thắng tiền tố cho phép; có danh sách cho phép thì chỉ URL khớp được tải; bỏ qua sự kiện `delete` và loại `Binary`, `AsyncJob`, `AuditEvent`, `Subscription`; URL đã thành `Binary/<id>` (tương đối) không bị tải lại. | DATA | F-28 |
| BR-68 | Chống SSRF: mặc định chặn URL tới địa chỉ nội bộ (kiểm trước khi gọi và kiểm lại lúc kết nối); URL bị chặn là lỗi vĩnh viễn (chỉ log, không thử lại); log chỉ ghi host, không ghi URL đầy đủ (có thể chứa token ký). | SEC | F-28 |
| BR-69 | `Binary` tải về mang `meta.project` của resource nguồn và `securityContext` trỏ về resource nguồn nên project khác không đọc được; loại nội dung lấy theo thứ tự phản hồi, `attachment.contentType`, `application/octet-stream`; loại bị cấm hoặc tệp quá `MaxBytes` bị bỏ và không để lại Binary hay tệp. | DATA | F-28 |
| BR-70 | Thay `url` bằng `Binary/<id>` ở MỌI attachment trùng, ghi bằng `If-Match`; thua ghi song song thì dọn Binary và tệp rồi thử lại trên phiên bản mới; resource đã xóa hoặc đã đổi URL thì dọn và bỏ; mỗi lần thay tạo một phiên bản mới của resource nguồn (có lịch sử và sự kiện). | DATA | F-28 |
| BR-71 | Lỗi tạm (HTTP từ 400, lỗi mạng, lỗi ghi kho hoặc CSDL) được thử lại, lỗi vĩnh viễn chỉ ghi log; chế độ nền dùng hàng đợi 1024, 2 worker, tối đa 3 lần (chờ 1s rồi 2s), mỗi lần tối đa 5 phút; hàng đầy thì bỏ; broker bộ nhớ mất hàng đợi khi khởi động lại, broker Redis thử lại và đưa vào dead-letter theo cơ chế broker. Việc tải không bao giờ làm lần ghi gốc thất bại. | OPS | F-28 |

### Ma trận phân quyền

Ô "ok" là qua được cổng vai trò (token tĩnh và JWT dùng chung `RequiredRole`); vai trò cao bao gồm vai trò thấp (ADMIN gồm WRITE gồm READ). Ở chế độ JWT, qua cổng chưa đủ: còn phải có chữ SMART tương ứng và thỏa AccessPolicy (cột "Kiểm thêm ở JWT"). Super-admin luôn có vai trò ADMIN nhưng phải gửi `X-Fhir-Project`. `403` là `Token không đủ quyền cho thao tác này`.

| F | Chức năng | READ | WRITE | ADMIN | Super-admin | Kiểm thêm ở JWT |
|---|---|---|---|---|---|---|
| F-01 | create | 403 | ok | ok | ok | `c` (+ `s` nếu `If-None-Exist`) |
| F-02 | read, vread | ok | ok | ok | ok | `r` |
| F-03 | update, PUT có điều kiện | 403 | ok | ok | ok | `u` (+ `s` nếu có điều kiện) |
| F-04 | patch | 403 | ok | ok | ok | `r` và `u` (+ `s` nếu có điều kiện) |
| F-05 | delete | 403 | ok | ok | ok | `d` (+ `s` nếu theo query) |
| F-06 | history | ok | ok | ok | ok | `r` |
| F-07 | search theo loại | ok | ok | ok | ok | `s` |
| F-08 | search hệ thống, `POST _search` | ok | ok | ok | ok | `s`; không có `_type` thì chỉ tìm các loại có `s` và có rule policy |
| F-09 | batch, transaction | 403 | ok | ok | ok | từng entry theo thao tác của nó |
| F-10 | batch bất đồng bộ; xem `/job/{id}/status` | 403 gửi; ok xem | ok | ok | ok | như F-09 |
| F-11 | `$validate` | ok | ok | ok | ok | `r` hoặc theo loại |
| F-12 | `Patient/$everything` | ok | ok | ok | ok | `r`; bỏ qua loại không đọc được khi không chỉ `_type` |
| F-13 | GraphQL | ok | ok | ok | ok | như đọc: project, scope, policy áp như REST |
| F-14 | metadata | công khai, không cần token | | | | không |
| F-15 | terminology tra cứu | ok | ok | ok | ok | `r` (hoặc `s`) trên loại tương ứng |
| F-16 | terminology nạp | 403 | ok (token tĩnh) | ok | ok | JWT: `RequireAdmin` (cần `fhir.admin` không kèm policy) cộng `u` trên loại (đọc mã, chưa kiểm cụm F-16) |
| F-17 | `$export` và theo dõi, hủy | ok | ok | ok | ok | `r` trên loại xuất; cần `Binary.read` để tải tệp |
| F-18 | `$reindex` | 403 | 403 | ok | ok | một resource: `u`, bị chặn với policy; cả loại: job nền trong project của người gọi, policy làm job lỗi |
| F-19 | `$expunge` | 403 | 403 | ok | ok | `d`, bị chặn với policy |
| F-20 | job nền, hủy job batch | ok xem; 403 hủy | ok | ok | ok | job chạy với ảnh chụp quyền đã ký |
| F-21 | tạo Subscription rest-hook | 403 | ok | ok | ok | như F-01 trên loại `Subscription` |
| F-22 | `$get-ws-binding-token` | ok | ok | ok | ok | đọc được Subscription |
| F-23 | Binary thô | ok đọc; 403 ghi | ok | ok | ok | `/storage/*` không dùng Bearer (chữ ký URL); `$presigned-url?upload=true` cần quyền `u` trên Binary |
| F-24 | xác thực | dùng chung | | | | |
| F-25 | tách project | dùng chung | | | | `X-Fhir-Project` khác project: 403 (người thường) |
| F-26 | CLI | không qua HTTP | | | | quyền do truy cập database, biến môi trường và kho |
| F-27 | `/healthz`, `/readyz` | công khai, không cần token | | | | không |
| F-28 | tự tải attachment ngoài | không qua HTTP, chạy nền | | | | Binary mang `meta.project` của resource nguồn; không qua kiểm scope vì worker dùng kho không tách project (xem 5.4) |

## 5.2 Kịch bản chấp nhận

| # | Kịch bản (Given When Then) | Luật |
|---|---|---|
| AT-01 | Given token WRITE, When POST Patient hợp lệ có `id` tự chọn, Then 201, `Location` có `_history/<vid>`, `ETag` dạng `W/"..."`, id trong kết quả khác id gửi | BR-01, BR-02 |
| AT-02 | Given Patient đã tạo, When PUT với `If-Match` đúng rồi PUT lại với `If-Match` cũ, Then lần đầu 200 (versionId mới), lần sau 412 `Phiên bản không khớp: hiện tại <vid>` | BR-03 |
| AT-03 | Given hai PUT đồng thời cùng `If-Match`, When cùng chạy, Then đúng một yêu cầu 200 và một 412 | BR-03 |
| AT-04 | Given đã có Patient identifier=s\|1, When POST kèm `If-None-Exist: identifier=s\|1`, Then 200 và cùng id, không tạo thêm; khi có 2 Patient cùng định danh thì 412 `Nhiều resource khớp If-None-Exist` | BR-04 |
| AT-05 | Given `FHIR_UPDATE_AS_CREATE` tắt, When PUT Patient/abc chưa có, Then 404; bật cờ thì 201 với id abc, PUT lần hai 200 | BR-05 |
| AT-06 | Given cờ bật, When PUT id `has_underscore` hoặc 65 ký tự, hoặc id đã xóa, Then lần lượt 400 và 410; có `If-Match` với id chưa có thì 404 | BR-05 |
| AT-07 | Given token JWT scope `system/Patient.u` (không có `c`) và cờ bật, When PUT id chưa có, Then 403 `Token thiếu quyền c (SMART) trên Patient` | BR-05, BR-09 |
| AT-08 | Given Patient đã DELETE, When GET, vread phiên bản xóa, DELETE lần nữa, Then 410, 410, 204; history vẫn liệt kê đủ phiên bản kể cả loại DELETE (status 204) | BR-06 |
| AT-09 | Given nhiều Patient khớp một tiêu chí, When PUT, PATCH, DELETE theo query, Then đều 412 `Nhiều resource khớp tiêu chí`; 0 khớp thì PUT tạo 201, PATCH 404, DELETE 204; không có query thì 400 | BR-07 |
| AT-10 | Given Patient, When PATCH có Content-Type `application/json`, body không phải mảng, đổi `/id`, hoặc làm `active:"yes"`, Then lần lượt 415, 400, 400 `Không được đổi id bằng PATCH`, 400 OperationOutcome | BR-08, BR-25 |
| AT-11 | Given token JWT chỉ có `system/Patient.r`, When PATCH, Then 403 thiếu `u`; token chỉ `u` cũng 403 thiếu `r`; cần cả `r` và `u` | BR-09 |
| AT-12 | Given 25 Patient, When GET `Patient?_count=10&_total=accurate&_sort=family`, Then 10 entry, `total=25`, có `next`, không `previous`; đi theo `next` hết 3 trang thu đủ 25; `_count=5000` bị cắt 1000, `_count=-1` là 400 | BR-10, BR-11, BR-12 |
| AT-13 | Given Patient tên "Nguyễn Đức", When tìm `name=nguyen`, `name:exact=nguyen`, `name:contains=duc`, Then khớp, không khớp, khớp | BR-13 |
| AT-14 | Given Patient có birthDate, When tìm `birthdate=ge1970-01-01`, `birthdate=lt1980`, Then đúng tập theo khoảng thời gian; ngày sai thì 400 | BR-14 |
| AT-15 | Given Observation tham chiếu Patient, When `Observation?subject:Patient.name=smith&_include=Observation:subject` và `Patient?_has:Observation:subject:code=x`, Then kết quả đúng, Patient entry có `search.mode=include` | BR-16, BR-19 |
| AT-16 | Given CodeSystem có cây cha con và ValueSet, When `Condition?code:below=sys\|root`, `code:in=<ValueSet>`, `code:below=sys\|ma-la`, Then con cháu gồm chính nó; theo ValueSet; mã lạ không khớp gì | BR-17 |
| AT-17 | Given tham số `_query=x`, `_sort=composite`, modifier `:zzz`, tham số không có, Then 400 với thông điệp tương ứng; system search có tham số mà không có `_type` thì 400 | BR-15, BR-18 |
| AT-18 | Given token JWT có `system/Patient.s` (không `r`), When GET Patient/{id} và GET Patient?..., Then 403 thiếu `r` và 200 tìm được; token chỉ `r` thì ngược lại; search hệ thống `?_type=Patient,Observation` với token chỉ `s` trên Patient thì 403 | BR-09, BR-19 |
| AT-19 | Given hai project A và B, When người gọi B đọc, search, PUT, DELETE, `_include` resource của A, Then 404 (hoặc không xuất hiện), `total` chỉ đếm project của B | BR-19, BR-29 |
| AT-20 | Given transaction `[POST Observation(subject urn:uuid:p), POST Patient(fullUrl urn:uuid:p), DELETE Patient/x]`, When gửi, Then DELETE chạy trước, Observation trỏ `Patient/<id mới>`, entry theo thứ tự gốc, 200 `transaction-response` | BR-21, BR-22 |
| AT-21 | Given transaction có entry thứ 3 không hợp lệ, When gửi, Then trả 400 OperationOutcome có `Bundle.entry[2]` và không resource nào được tạo hay xóa | BR-21 |
| AT-22 | Given batch `[POST, GET, GET thiếu, POST sai, PUT, DELETE, search]`, When gửi, Then 200 và mã từng entry `201,200,404,400,200,204,200`; token chỉ có `c`: entry PUT, DELETE là 403 và dữ liệu không đổi | BR-21, BR-23 |
| AT-23 | Given `FHIR_ASYNC_BATCH=true`, When POST batch kèm `Prefer: respond-async`, Then 202 + `Content-Location`; poll xong thì `output.results` trỏ Binary chứa `batch-response` cùng mã như chạy đồng bộ; transaction kèm respond-async là 400 và không tạo AsyncJob; cờ tắt thì chạy đồng bộ 200 | BR-20, BR-24 |
| AT-24 | Given job batch async bị ngắt sau checkpoint, When tiếp quản, Then chạy tiếp từ checkpoint, POST không điều kiện không tạo trùng; hủy khi đang chạy dừng ở entry kế tiếp (202), job khác project là 404 | BR-24 |

| # | Kịch bản (Given When Then) | Luật |
|---|---|---|
| AT-25 | Given CodeSystem có mã `a` cha của `b`, token đọc. When `GET CodeSystem/$lookup?system=S&code=b`. Then 200 có `display` và property `parent`=`a`; với code lạ trả 404 `Không có mã x trong S`; thiếu code trả 400 `Thiếu tham số code (hoặc coding)`; token chỉ đọc vẫn gọi được cả bằng POST. | BR-56 |
| AT-26 | Given CodeSystem có cây a > b. When `$subsumes` với (a,b), (b,a), (a,a), (b,c khác nhánh). Then outcome lần lượt `subsumes`, `subsumed-by`, `equivalent`, `not-subsumed`; mã lạ 404; ConceptMap `$translate` mã không có ánh xạ trả 200 `result=false` kèm `Không có bản dịch cho <code>`. | BR-56 |
| AT-27 | Given ValueSet include CodeSystem 30 mã. When `$expand?count=10&offset=10&filter=abc`. Then `expansion.total` là tổng sau lọc, `contains` tối đa 10 phần tử từ vị trí 10; `count=-1` trả 400 `count và offset phải >= 0`; ValueSet mở ra quá 100000 mã trả 422 `ValueSet quá lớn để mở rộng (tối đa 100000 mã)`; ValueSet tham chiếu vòng trả 422. | BR-57 |
| AT-28 | Given Observation có code thuộc ValueSet V và con của mã M. When `Observation?code:in=<V>`, `code:below=S\|M`, `code:above=S\|<lá>`, `code:not-in=<V>`. Then kết quả đúng tập khớp; giá trị `:below` thiếu `\|` trả 400 `giá trị :below phải có dạng system\|code`; ValueSet vượt 5000 mã trả 400. | BR-58 |
| AT-29 | Given admin và CodeSystem `content=not-present` khai báo property `status`. When `$import` 10000 concept rồi gọi lại đúng body đó. Then cả hai lần 200, số concept không đổi; thêm 10001 concept trả 400 `Mỗi loại tối đa 10000 phần tử mỗi lần gọi`; `$lookup` đọc được từ bảng dẫn xuất. | BR-59, BR-60 |
| AT-30 | Given admin. When `$import` có property `foo` chưa khai báo, rồi property cho mã chưa nạp. Then 400 `Property không xác định: foo`; 400 `Concept chưa có: <code>`; body không phải Parameters 400 `Body phải là Parameters`; token JWT không phải admin trả 403 `Thao tác này chỉ dành cho quản trị project` và không có gì được ghi. | BR-59 |
| AT-31 | Given ConceptMap và gói PackageRelease trỏ Binary chứa Bundle. When `ConceptMap/$import` theo `url` rồi `$translate`; `PackageRelease/{id}/$install`. Then `$translate` trả bản dịch từ bảng ánh xạ; cài thành công ghi PackageInstallation `installed`; Bundle có entry lỗi trả 400 và PackageInstallation `error` (transaction thì rollback); `content.url` sai dạng trả 400. | BR-59, BR-60 |
| AT-32 | Given token đọc. When `GET $export` không `Prefer`; rồi với `Prefer: respond-async`, `_type=Patient`. Then 400 `Bulk export cần header Prefer: respond-async`; 202 + `Content-Location` `.../bulkdata/{id}/status`; poll trả 202 (`X-Progress: in-progress`, `Retry-After: 2`) rồi 200 manifest `requiresAccessToken: true` với `output[].url` tải được bằng `Accept: application/fhir+ndjson`; `_outputFormat=csv` trả 400. | BR-32 |
| AT-33 | Given project A có 3 Patient, B có 1. When A và B cùng `$export`. Then A xuất 3, B xuất 1, file của A tải bằng token B trả 404 và không token trả 401; token chỉ `system/Patient.read system/Binary.read` chỉ xuất Patient; `_type=Condition` ngoài quyền cho job `error` (poll không 200); không `_type` thì loại ngoài quyền bị bỏ qua; AccessPolicy giới hạn chỉ dữ liệu được phép. | BR-32 |
| AT-34 | Given export đang chạy. When `DELETE bulkdata/{id}/status`. Then 202 `Cancelled`, poll sau đó trả 410 và không còn Binary kết quả; export đã xong: DELETE trả 202 `Deleted`, tệp và AsyncJob biến mất (404); quá FHIR_EXPORT_RETENTION bảo trì xóa AsyncJob và Binary. | BR-34 |
| AT-35 | Given AsyncJob `accepted` ghi sẵn vào CSDL cũ hơn 30 giây và không có lease, process mới có FHIR_JOB_MAINTENANCE_INTERVAL > 0. When process khởi động. Then job được nhặt, chạy đúng một lần và `completed`; job có lease còn hạn hoặc mới hơn 30 giây thì chưa bị nhặt; hai replica cùng nhặt thì chỉ một giành được lease. | BR-30 |
| AT-36 | Given job đang chạy dở. When tắt êm (SIGTERM, hết hạn chờ). Then job không bị đánh dấu `error`, `transactionTime` được xóa, `status` vẫn `accepted`, lần khởi động sau (hoặc replica khác) chạy lại; lease được gia hạn mỗi JobLease/3 khi job còn chạy lâu. | BR-30 |
| AT-37 | Given JWT bật. When AsyncJob bị sửa `request` hoặc xóa extension job-seal rồi chạy. Then job `error` với `job thiếu hoặc sai chữ ký quyền`, không đọc dữ liệu nào; cấu hình JWKS thiếu FHIR_JOB_SIGNING_KEY hoặc dưới 32 ký tự thì service từ chối khởi động. | BR-31 |
| AT-38 | Given token admin và token write. When `POST Patient/$reindex`, `POST Patient/{id}/$reindex`, `POST Patient/{id}/$expunge`. Then token write nhận 403 ở cả ba; admin nhận 202 + job rồi `completed` kèm `count`, 200 `Reindexed ...`, 200 `Expunged ...`; token có AccessPolicy ở đường một resource nhận 403 `Reindex không dùng được với chính sách truy cập`. | BR-33 |
| AT-39 | Given Patient đã có 3 phiên bản và một CodeSystem đã nạp concept. When `$expunge` rồi đọc, `_history`, tìm theo chỉ mục cũ; expunge lần hai; expunge CodeSystem. Then GET 404, `_history` 404, tìm không còn, lần hai 404; bảng concept của CodeSystem bị xóa; resource đã xóa mềm vẫn expunge được. | BR-33, BR-60 |
| AT-40 | Given kho `file:` hoặc `s3:`. When `POST Binary` thân thô rồi `GET Binary/{id}`; `PUT` cập nhật chỉ metadata; ghi Binary JSON có `data` hơn 1 MiB. Then 201 kèm `url` ký, GET trả đúng byte và header `nosniff`, `Content-Disposition: attachment` (trừ ảnh png, jpeg, gif, webp); PUT metadata chép nội dung sang phiên bản mới; `data` quá trần trả 400 `base64Binary exceeds 1048576 bytes`. | BR-41 |
| AT-41 | Given kho báo lỗi khi ghi. When `POST Binary` thô. Then 500 `Failed to store binary content` và resource vừa tạo bị xóa (không Binary mồ côi); transaction bundle bị hủy xóa các khóa đã ghi; PUT lỗi kho giữ nội dung cũ. | BR-42 |
| AT-42 | Given URL ký từ `$presigned-url`. When GET đúng; sửa một ký tự; chờ quá hạn; đổi method sang PUT khi URL ký GET; dùng URL project khác; PUT URL trỏ phiên bản cũ. Then 200; 401 `Invalid signature`; 410 `URL has expired`; 401; 401; 409 `Version mismatch`; khởi động lại không đặt FHIR_STORAGE_SIGNING_KEY_PATH làm URL cũ mất hiệu lực. | BR-43 |
| AT-43 | Given token. When tải lên tệp `.exe`, `Content-Type: application/x-sh`, `X-Security-Context` sai dạng, thân gzip bung vượt FHIR_STORAGE_MAX_UPLOAD_BYTES, PUT `/storage` sai `Content-Type`. Then 400 `Invalid file extension`; 400 `Invalid content type`; 400 `Invalid X-Security-Context`; 413; 400 `Content-Type mismatch`; không Binary mồ côi; `$presigned-url?upload=true` thiếu quyền ghi trả 403. | BR-44 |
| AT-44 | Given kho có khóa mồ côi cũ 48 giờ, khóa mồ côi 1 phút và khóa còn dùng. When `sweep-binary -dry-run` rồi `sweep-binary`. Then dry-run liệt kê đúng khóa cũ và không xóa; lượt thật xóa khóa cũ, giữ khóa mới và khóa còn dùng; `-grace 30m` bị từ chối (tối thiểu 1h); quá nửa khóa mồ côi từ 20 khóa thì dừng với `số nội dung mồ côi bất thường` trừ `-force`; expunge Binary xóa nội dung mọi phiên bản sau commit. | BR-45 |

---

| # | Kịch bản (Given When Then) | Luật |
|---|---|---|
| AT-45 | Given Subscription `active` kênh rest-hook cho `Observation` có secret và đích công khai, When tạo một Observation, Then đích nhận một `POST` có `Content-Type: application/fhir+json`, `X-Medplum-Subscription`, `X-Medplum-Interaction: create`, `X-Signature` đúng HMAC-SHA256 của body; xóa thì body `{}` và có `X-Medplum-Deleted-Resource`. | BR-36, BR-38 |
| AT-46 | Given Subscription active, When một transaction tạo resource rồi rollback, Then đích không nhận gì; When transaction commit, Then đích nhận thông báo sau commit; When broker lỗi lúc phát, Then lệnh ghi vẫn thành công. | BR-35 |
| AT-47 | Given Subscription trỏ tới `127.0.0.1` và `FHIR_WEBHOOK_ALLOW_PRIVATE=false`, When tạo resource khớp, Then đích không nhận gì, không thử lại, `Failed` tăng một; Given `ENVIRONMENT=production` và `FHIR_WEBHOOK_ALLOW_PRIVATE=true`, Then server từ chối khởi động. | BR-37 |
| AT-48 | Given đích trả lần lượt `500` rồi `503` rồi `200`, Then có 3 lần gọi và `Delivered=1`; Given đích trả `400`, Then chỉ một lần gọi và bỏ cuộc; Given đích trả `302`, Then không gọi địa chỉ chuyển hướng và bỏ cuộc; Given đích trả `500` liên tục, Then dừng ở `FHIR_WEBHOOK_MAX_ATTEMPTS`. | BR-38 |
| AT-49 | Given `FHIR_SUBSCRIPTION_AUTO_DISABLE=[{"maxConsecutiveFailures":N,"timeWindowSeconds":S}]` và đích luôn lỗi, When đủ N sự kiện bỏ cuộc trong S giây, Then Subscription thành `off` với `error` `Automatically disabled after ...`; When có một lần giao thành công giữa chừng, Then bộ đếm về 0; Given Subscription đã bị người sửa sang trạng thái khác, Then không bị ghi đè. | BR-39 |
| AT-50 | Given Subscription và resource ở hai project khác nhau, When resource của project B thay đổi, Then Subscription của project A không nhận thông báo (rest-hook và websocket). | BR-36, BR-53 |
| AT-51 | Given Subscription websocket active, When client gọi `$get-ws-binding-token`, Then nhận `token`, `expiration` sau 5 phút và `websocket-url`; When bind quá 10 giây, Then bị đóng 1008; When token sai khóa hoặc hết hạn, Then bị từ chối; When Subscription là rest-hook, Then `400` `Subscription không dùng kênh websocket`. | BR-40 |
| AT-52 | Given hai người nhận bind cùng Subscription, một người có `r` trên loại và một người không (hoặc resource ngoài AccessPolicy), When có sự kiện khớp, Then chỉ người có quyền nhận, người kia không nhận kể cả thông báo xóa và `Denied` tăng; When access token gốc hết hạn, Then binding thôi nhận. | BR-40, BR-52 |
| AT-53 | Given chế độ token tĩnh, When gọi không token hoặc sai token, Then `401` `Thiếu hoặc sai bearer token`; When token READ gọi `POST`, Then `403` `Token không đủ quyền cho thao tác này`; When token WRITE gọi `$reindex`, Then `403`; When token ADMIN gọi `$reindex`, Then `202`; `GET /metadata` không cần token. | BR-46 |
| AT-54 | Given chế độ JWT, When token hết hạn, sai `aud` hoặc là chuỗi rác, Then `401` kèm `WWW-Authenticate` chứa `invalid_token`; When token thiếu claim project hoặc chỉ có scope `openid email`, Then `403` `Token không có quyền truy cập FHIR`; Given cấu hình vừa JWT vừa token tĩnh, Then server từ chối khởi động. | BR-46, BR-47, BR-48 |
| AT-55 | Given token chỉ có `system/Patient.c`, When POST Patient thì `201`, When GET, PUT, PATCH, DELETE theo id thì `403`; Given `system/Patient.ru`, When PATCH thì `200`, When DELETE thì `403`; Given `system/Patient.u`, When PUT có điều kiện thì `403` (thiếu `s`); Given `system/Patient.s`, When GET theo id thì `403`; Given `system/Patient.write`, When GET theo id thì `403`; When scope loại khác, Then `403`. | BR-49, BR-50 |
| AT-56 | Given policy `[{Patient, criteria "_id=p1", readonly}, "Observation?subject=Patient/p1"]`, When đọc Patient p1 thì `200`, Patient p2 thì `404`, `GET /Condition` thì `403`, PUT Patient p1 thì `403`, POST Observation của p2 thì `403`; When gọi `$reindex` hoặc `$expunge` một resource, Then `403` `... không dùng được với chính sách truy cập`. | BR-51, BR-52 |
| AT-57 | Given policy có hai rule `Communication?sender=Patient/1` và `Communication?recipient=Patient/1`, When search, Then thấy resource thỏa một trong hai; Given 17 rule cùng một loại, hoặc một rule không criteria đi cùng rule khác, hoặc `deny` đi cùng rule khác, hoặc các rule khác `readonly`, Then token bị `403`. | BR-51 |
| AT-58 | Given claim `fhir_access_policy` là chuỗi `"Patient"`, hoặc object có khóa lạ, hoặc `readonly` không phải boolean, Then `403`; Given mảng rỗng `[]`, Then mọi thao tác theo loại bị `403` `Chính sách truy cập không cho phép <Loại>`; Given `_compartment=Patient/p1`, Then token của bệnh nhân chỉ thấy resource trong compartment kể cả qua `_include`, `total` và phân trang. | BR-48, BR-51, BR-52 |
| AT-59 | Given token project A gửi `meta.project="B"` khi tạo Patient, Then lưu `meta.project="A"`; When token project B đọc, sửa, xóa Patient đó, Then `404`; When search ở B, Then không thấy; When B đọc id đã bị xóa của A, Then `410`. | BR-53 |
| AT-60 | Given `FHIR_JWT_SUPER_ADMIN=true` và token `fhir.super` không project, When thiếu `X-Fhir-Project` hoặc giá trị sai định dạng, Then `400`; When header `A` thì đọc được dữ liệu A và `404` với dữ liệu A khi chọn `B`; When ghi với header `B`, Then dữ liệu mang project B; And log có `audit=true`, `target_project`, không chứa dữ liệu hay token; When token thường gửi project khác, Then `403`; Given cờ tắt, Then token `fhir.super` bị `403`. | BR-54 |
| AT-61 | Given database có resource không `meta.project`, When chạy `assign-project -project A -dry-run`, Then chỉ in số lượng; When chạy thật, Then resource nhận `meta.project="A"` và một phiên bản mới; When chạy lại, Then `cho 0 resource`; When schema chưa migrate hoặc thiếu `-project`, Then lỗi. | BR-55, BR-65 |
| AT-62 | Given `DATABASE_AUTO_MIGRATE=false` và còn migration, When khởi động server, Then từ chối `schema chưa được migrate`; When `fhir-server migrate status` thì in `migration chưa áp dụng: N`, `migrate up` rồi `status` thì 0; Given `DATABASE_MIGRATE_URL` đặt mà `DATABASE_AUTO_MIGRATE=true`, Then từ chối cấu hình; Given `sweep-binary -grace 30m`, Then lỗi ân hạn tối thiểu 1 giờ. | BR-65 |
| AT-63 | Given server chạy, Then `/healthz` `200` và `/readyz` `200` có `database`; Given broker hỏng khi bật Subscription, Then `/readyz` `503` với `broker:false`; When nhận SIGTERM, Then `/readyz` `503` ngay trong lúc drain còn `/healthz` `200`, yêu cầu đang chạy hoàn tất, rồi tiến trình thoát không lỗi. | BR-61, BR-62 |
| AT-64 | Given `FHIR_RATE_LIMIT=N` thấp, When vượt, Then `429` kèm `Retry-After` và `OperationOutcome`, kể cả với token sai; `/healthz` không bị giới hạn; When client giả `X-Forwarded-For` mà không khai `FHIR_TRUSTED_PROXIES`, Then vẫn bị chặn; When body trên 16 MiB, Then `413`; When preflight từ origin không có trong `FHIR_ALLOWED_ORIGINS`, Then không có `Access-Control-Allow-Origin`; mọi phản hồi (kể cả 401, 429) có `X-Frame-Options: DENY`. | BR-63, BR-64 |
| AT-65 | Given bật `FHIR_AUTO_DOWNLOAD` và kho Binary, máy chủ ngoài trả PDF, When tạo `DocumentReference` có `content[].attachment.url` trỏ máy chủ đó, Then (sau ít giây) `url` thành `Binary/<id>` của đúng project, nội dung đúng từng byte, `contentType` lấy từ phản hồi, `Binary.securityContext` trỏ `DocumentReference/<id>`; resource có thêm một phiên bản | BR-67, BR-69, BR-70 |
| AT-66 | Given `FHIR_AUTO_DOWNLOAD` mặc định (tắt), When tạo `DocumentReference` có URL ngoài, Then máy chủ ngoài không bị gọi, `url` giữ nguyên; Given đã tải xong, When project khác đọc `Binary/<id>`, Then `404` | BR-66, BR-69 |
| AT-67 | Given URL cùng địa chỉ API hoặc `/storage`, URL không khớp danh sách cho phép, hoặc khớp tiền tố bỏ qua; Given sự kiện `delete` hay resource loại `Binary`/`Subscription`, When ghi, Then không gọi ra ngoài | BR-67 |
| AT-68 | Given mặc định (không `ALLOW_PRIVATE`), When attachment trỏ `127.0.0.1` hoặc địa chỉ nội bộ, Then không kết nối, không tạo Binary, `url` giữ nguyên, log chỉ có host | BR-68 |
| AT-69 | Given máy chủ ngoài trả 5xx hoặc ngắt kết nối, When tải, Then lỗi tạm được thử lại, không để lại Binary hay tệp; Given trả loại nội dung bị cấm hoặc tệp quá `MaxBytes`, Then chỉ log và không thử lại, không để lại Binary hay tệp | BR-69, BR-71 |
| AT-70 | Given resource bị sửa trong lúc đang tải (thua `If-Match`), When thay URL, Then Binary vừa tạo bị dọn và lần thử sau thành công trên phiên bản mới; Given resource đã xóa hoặc đã đổi URL, Then dọn và bỏ, không lỗi | BR-70 |
| AT-71 | Given hai attachment cùng URL trong một resource, When tải, Then chỉ một lần gọi ra ngoài và cả hai `url` đều thành cùng `Binary/<id>` | BR-67, BR-70 |
| AT-72 | Given chế độ nền, When ghi resource, Then phản hồi ghi không chờ việc tải; lỗi tạm được thử lại tới 3 lần; hàng đợi đầy thì bỏ và log | BR-71 |
| AT-73 | Given `FHIR_AUTO_DOWNLOAD=true` thiếu `FHIR_BINARY_STORAGE`, hoặc cờ phụ khi chưa bật, `MAX_BYTES` âm, `TIMEOUT=0s`, When khởi động, Then từ chối và thông báo nêu tên biến `FHIR_AUTO_DOWNLOAD...` | BR-66 |

## 5.3 Truy vết luật đến bằng chứng kiểm thử

| Nhóm luật | Test |
|---|---|
| BR-01..09 (ghi, phiên bản, điều kiện, PATCH) | `pkg/fhirrouter/repotest/suite.go` (bộ chung, chạy trên bộ nhớ: `TestMemoryRepository` ở `pkg/fhirrouter/router_test.go`; SQLite: `TestSQLiteRepository` ở `pkg/fhirstore/store_test.go`; PostgreSQL: `TestPostgresRepository` ở `pkg/fhirstore/postgres_test.go`), nhóm con `CRUDAndHistory`, `ConditionalCreate`, `ConditionalUpdateDeletePatch`, `JSONPatch`, `ConditionalRead`, `PreferHeader`, `ConcurrentIfMatch`, `ConcurrentConditionalCreate`, `ConcurrentUpdateAsCreate`, `ConcurrentConditionalUpdate`, `ConcurrentConditionalDelete`, `ErrorsAndRouting` |
| BR-05 update-as-create | `pkg/fhirrouter/updateascreate_test.go`: `TestUpdateAsCreateIsOffByDefault`, `TestUpdateAsCreate`; `pkg/fhirauth/scope_test.go`: `TestUpdateAsCreateAcrossProjects` |
| BR-09 chữ SMART từng tương tác | `pkg/fhirauth/smartv2_test.go`: `TestSMARTv2LettersGrantExactlyThemselves`, `TestRepoEnforcesEachInteractionSeparately`, `TestForbiddenMessageNamesTheMissingLetter`; `apps/fhir-server/internal/bootstrap/smartv2_e2e_test.go`: `TestSMARTv2EachLetterGrantsOnlyItsInteraction` |
| BR-10..19 (tìm kiếm) | `pkg/fhirsearch/search_test.go` (`TestMatch`, `TestParseErrors`, `TestParseControlParams`, `TestSort`, `TestParseChainedAndHas`, `TestTerminologyModifiersParseButCannotMatchDirectly`), `pkg/fhirsearch/filterexpr_test.go`, `pkg/fhirsearch/compartment_test.go`, `pkg/fhirsearch/composite_test.go`; bộ chung `repotest/suite.go` nhóm con `SearchPaging`, `SearchPostAndSystem`, `Includes`, `ChainedSearchAndHas`, `CompositeSearch`, `TokenTerminology`, `FilterParam`; `pkg/fhirstore/differential_test.go` (`TestDifferentialAgainstMemory`), `pkg/fhirstore/search_plan_test.go` |
| BR-19 tách project và AccessPolicy trong search | `pkg/fhirauth/scope_test.go` (`TestScopedParity`, `TestProjectIsolation`), `pkg/fhirauth/smartv2_test.go` (`TestIncludedResultsFilteredBySeeableTypes`), `pkg/fhirauth/compartment_test.go` (`TestCompartmentPolicySearchTotalAndPaging`, `TestCompartmentPolicyIncludesAndEverything`) |
| BR-20..24 (bundle, async) | `repotest/suite.go` nhóm con `Batch`, `TransactionUUIDReferences`, `TransactionRollback`, `TransactionOrderAndConditional`, `TransactionRequiresTransactionalRepo`; `pkg/fhirrouter/batch_async_test.go` (`TestAsyncBatchOffRunsSynchronously`, `TestAsyncBatchMatchesSync`, `TestAsyncBatchRejectsBeforeCreatingJob`, `TestAsyncBatchResumesFromCheckpoint`, `TestAsyncBatchCancelWhileRunning`, `TestAsyncBatchExpires`); `apps/fhir-server/internal/bootstrap/batch_async_e2e_test.go` (`TestAsyncBatchRunsWithCallerGrants`); `apps/fhir-server/internal/bootstrap/smartv2_e2e_test.go` (`TestSMARTv2PerEntryEnforcementInBatch`); `pkg/fhirauth/compartment_test.go` (`TestCompartmentPolicyWriteAndBatch`) |
| BR-25..29 (kiểm dữ liệu) | `pkg/fhirvalidate/validate_test.go` (`TestOfficialExamplesAreValid`, `TestInvalidResources`, `TestValidMinimalResources`, `TestSkipConstraints`, `TestOutcome`); `repotest/suite.go` nhóm con `ErrorsAndRouting`, `SkipValidationOption`, `ValidateOperation`; `pkg/fhirauth/scope_test.go` (`TestProjectIsolation`, ghi đè `meta.project`) |
| F-12 `$everything` | `repotest/suite.go` nhóm con `Everything`; `pkg/fhirauth/compartment_test.go` (`TestEverythingSkipsForbiddenTypesOnlyWithoutExplicitType`) |
| F-13 GraphQL | `pkg/fhirrouter/graphql_test.go` (`TestParseGraphQL`, `TestParseGraphQLErrors`, `TestGraphQLResourceBudget`, `TestGraphQLListCountIsCapped`, `TestGraphQLFragmentCycleAndSelfReference`); `repotest/suite.go` nhóm con `GraphQL` |
| F-14 metadata, vai trò HTTP | `repotest/suite.go` nhóm con `Capabilities`; `apps/fhir-server/internal/presentation/httpapi/auth_test.go`; `apps/fhir-server/internal/bootstrap/e2e_test.go` (`TestHealthAndPublicMetadata`, `TestAuthentication`, `TestFHIRFlowOverHTTP`, `TestRoutingErrors`, `TestOperationsAndAdminRole`) |

| Nhóm luật | Test |
|---|---|
| BR-56..58 (tra cứu, expand, modifier) | `pkg/fhirrouter/repotest/suite.go` (nhóm con `Terminology`, `StoredTerminology`, `TokenTerminology`, `SubsumesAndTranslate`; chạy bởi `pkg/fhirrouter/router_test.go` `TestMemoryRepository` và bộ kiểm SQL `pkg/fhirstore/store_test.go`, `pkg/fhirstore/postgres_test.go`); `apps/fhir-server/internal/bootstrap/jwt_e2e_test.go` (`TestTerminologyNeedsOnlyReadRole`, `TestSubsumesAndTranslateNeedOnlyReadRole`); `apps/fhir-server/internal/presentation/httpapi/auth_test.go` (`TestRequiredRole`) |
| BR-59, BR-60 (import, install, bảng dẫn xuất) | `pkg/fhirrouter/repotest/suite.go` (nhóm con `CodeSystemImport`, `ConceptMapImport`, `Expunge`); `pkg/fhirrouter/packageinstall_test.go` (`TestPackageInstallRunsBundleAndRecordsInstallation`, `TestPackageInstallFailedEntryMarksErrorAndTransactionRollsBack`, `TestPackageInstallRejectsBadPackages`, `TestPackageInstallRequiresAdmin`); `pkg/fhirstore/coding_scale_test.go` (`TestConceptBatchInsertSemantics`, `TestConceptExistenceCheckSpansBatches`, `TestConceptTreeQueriesAreFastOnLargeFreshTables`); `e2e/definitions_import_test.go` (admin, cách ly project, cài gói) |
| BR-32, BR-34 (export, hủy, retention) | `pkg/fhirrouter/repotest/suite.go` (nhóm con `BulkExport`, `GroupExport`, `ExportTypeFilter`); `pkg/fhirrouter/jobs_durable_test.go` (`TestExportCancelWhileRunningAndDelete`, `TestExportDeleteRemovesCompletedFiles`, `TestExpireJobs`); `apps/fhir-server/internal/bootstrap/jwt_e2e_test.go` (`TestBulkExportIsScopedToCallerProject`, `TestExportHonoursGrantsAndPolicy`, `TestGroupExportIsScopedToCallerProject`); `apps/fhir-server/internal/bootstrap/storage_e2e_test.go` (`TestStorageDoesNotBreakBulkExport`) |
| BR-30, BR-31 (lease, tiếp quản, job-seal) | `pkg/fhirrouter/jobs_durable_test.go` (`TestRecoverOrphanedJobRunsOnce`, `TestRecoverRespectsLeaseAndGrace`, `TestShutdownReleasesLeaseAndNextProcessResumes`, `TestHeartbeatKeepsLeaseWhileRunning`, `TestMaintenanceLoopRecoversAutomatically`); `pkg/fhirstore/jobs_test.go` (`TestRecoverAndExpireJobsOnSQL`); `pkg/fhirauth/seal_test.go` (`TestSealRoundTripAndTamper`), `pkg/fhirauth/seal_version_test.go`; `apps/fhir-server/internal/bootstrap/e2e_test.go` (`TestOrphanedExportResumesAfterRestart`); `apps/fhir-server/internal/bootstrap/replicas_e2e_test.go` (`TestTwoReplicasShareJobs`); `apps/fhir-server/internal/bootstrap/jwt_e2e_test.go` (`TestJWTJobsAndSubscriptionsAcrossProjects`); `apps/fhir-server/internal/config/config_test.go` (kiểm cấu hình FHIR_JOB_*) |
| BR-33 (reindex, expunge) | `pkg/fhirauth/scope_test.go`: `TestReindexTypeStaysInsideCallerProject`; `pkg/fhirrouter/repotest/suite.go` (nhóm con `ReindexAndJobs`, `Expunge`, `JobFailure`); `apps/fhir-server/internal/bootstrap/e2e_test.go` (`TestOperationsAndAdminRole`); `apps/fhir-server/internal/presentation/httpapi/auth_test.go` (`TestRequiredRole`) |
| BR-41, BR-42 (nội dung ở kho, thứ tự ghi, bù trừ) | `pkg/fhirbinary/wrap_test.go` (`TestWrapCreateMovesDataToStore`, `TestWrapBase64DefaultLimitIsOneMiB`, `TestWrapStoreFailureOnCreateRollsBack`, `TestWrapStoreFailureOnUpdateAndCopy`, `TestWrapTransactionRollbackRemovesWrittenContent`, `TestWrapTransactionCommitKeepsContent`); `pkg/fhirbinary/raw_test.go` (`TestRawCreateAndRead`, `TestRawStoreFailureOnPost`, `TestRawStoreFailureOnPutKeepsOldContent`); `apps/fhir-server/internal/bootstrap/storage_e2e_test.go` (`TestStorageEndToEnd`, `TestStorageOffByDefault`, `TestStorageOnS3` trong `storage_s3_test.go`) |
| BR-43 (URL ký) | `pkg/binarystore/signer_test.go` (`TestSignerRoundTrip`, `TestVerifyRejectsTampering`, `TestVerifyExpiry`, `TestKeyRotationAndVerifyOnly`, `TestTTLControlsExpiry`); `pkg/fhirbinary/storage_test.go` (`TestStorageSignatureErrors`, `TestStorageExpiry`, `TestStorageVersions`, `TestStorageProjectIsolation`, `TestStorageSignatureIsMethodBound`); `pkg/fhirbinary/presign_test.go` (`TestPresignedURLFullCycle`, `TestPresignedUploadNeedsUpdatePermission`); `apps/fhir-server/internal/bootstrap/storage_e2e_test.go` (`TestPresignedURLOperation`, `TestStorageEphemeralKeyInvalidatesURLsOnRestart`, `TestStorageSurvivesRestartWithSameKey`) |
| BR-44 (chặn tệp, trần tải lên) | `pkg/binarystore/key_test.go` (`TestCheckMetadata`); `pkg/fhirbinary/raw_test.go` (`TestRawBlockedTypesCreateNoOrphan`, `TestRawSizeLimits`, `TestRawCompressionBombIsRejected`, `TestRawDownloadHeadersByType`, `TestRawSecurityContextHeader`); `pkg/fhirbinary/storage_test.go` (`TestStoragePutContentType`, `TestStoragePutSizeLimitKeepsOldContent`); `apps/fhir-server/internal/bootstrap/storage_e2e_test.go` (`TestStorageUploadBeyondGlobalBodyLimit`, `TestStorageUploadLimitIsConfigurable`, `TestSecurityContextProtectsRawRead`) |
| BR-45 (sweep, expunge nội dung) | `pkg/fhirbinary/sweep_test.go` (`TestSweepRemovesOnlyOrphans`, `TestSweepGraceBoundary`, `TestSweepDryRunAndMaxDelete`, `TestSweepAbortsWhenMostKeysLookOrphaned`, `TestSweepKeepsKeysWhenLookupFails`); `apps/fhir-server/internal/bootstrap/sweep_cli_test.go` (`TestSweepBinaryCommand`); `pkg/fhirbinary/wrap_test.go` (`TestWrapExpungeRemovesAllVersionsContent`, `TestWrapTransactionExpungeDeletesAfterCommitOnly`); `pkg/binarystore/fs_list_test.go` (`TestFSCleanTemp`) |

---

| Nhóm luật | Test |
|---|---|
| BR-35 (sau commit, lỗi phát không hỏng ghi) | `pkg/fhirsub/fhirsub_test.go`: `TestEventsOnlyAfterCommit`, `TestPublishFailureDoesNotFailWrites`, `TestDeliveryForCreateUpdateDelete` |
| BR-36 (điều kiện xét, interaction, project) | `pkg/fhirsub/fhirsub_test.go`: `TestInteractionFilter`, `TestSkippedSubscriptions`, `TestSubscriptionChangesApplyImmediately`, `TestSubscriptionOnlySeesOwnProject`; `apps/fhir-server/internal/bootstrap/jwt_e2e_test.go`: `TestJWTJobsAndSubscriptionsAcrossProjects` |
| BR-37 (SSRF) | `pkg/safehttp/safehttp_test.go`: `TestBlocked`, `TestCheckURL`, `TestClientBlocksLoopbackAtConnectTimeAndDoesNotFollowRedirects`, `TestHostBlocked`; `pkg/safehttp/dial_test.go`; `pkg/fhirsub/ssrf_test.go`: `TestBlockedAddresses`, `TestCheckEndpoint`; `pkg/fhirsub/fhirsub_test.go`: `TestSSRFGuardBlocksInternalTargets`; `apps/fhir-server/internal/bootstrap/e2e_test.go`: `TestWebhookSSRFGuardOnByDefault`; `apps/fhir-server/internal/config/config_test.go`: `TestValidationErrorsAreAggregated` |
| BR-38 (retry, 4xx, redirect) | `pkg/fhirsub/fhirsub_test.go`: `TestRetries`, `TestQueueOverflowDropsInsteadOfBlocking`; `apps/fhir-server/internal/bootstrap/e2e_test.go`: `TestSubscriptionsEndToEnd` |
| BR-39 (tự tắt, cache, nhiều replica) | `pkg/fhirsub/failure_test.go`: `TestAutoDisable`, `TestFailureTrackers`, `TestRedisFailureTrackerIsShared`; `pkg/fhirsub/failure_internal_test.go`: `TestAutoDisableKeepsChangedStatus`; `apps/fhir-server/internal/config/config_test.go`: `TestSubscriptionAutoDisable`; `apps/fhir-server/internal/bootstrap/replicas_e2e_test.go`: `TestTwoReplicasShareDataAndDeliverWebhookOnce` |
| BR-40 (token bind, quyền theo người nhận) | `pkg/fhirsub/ws_token_test.go`, `pkg/fhirsub/ws_authz_test.go`, `pkg/fhirsub/ws_bind_test.go`, `pkg/fhirsub/ws_test.go`, `pkg/fhirsub/ws_remote_test.go`; `apps/fhir-server/internal/bootstrap/ws_e2e_test.go`: `TestWebSocketSubscriptionEndToEnd`, `TestWebSocketSubscriptionIsolatedByProject`, `TestWebSocketDeliveryIsAuthorizedPerRecipient`; `apps/fhir-server/internal/bootstrap/ws_issuer_test.go`: `TestIssuerModeDeliversThroughTheGateway`, `TestIssuerModeKeepsTokenEndpointProtected`, `TestIssuerModeTokensSurviveGatewayKeyRotation`; `e2e/ws_split_test.go` |
| BR-46 (chế độ xác thực, token tĩnh) | `apps/fhir-server/internal/presentation/httpapi/auth_test.go`: `TestRequiredRole`, `TestAuthenticateAndGuard`, `TestStaticTokenGuardAttachesRolePrincipal`; `apps/fhir-server/internal/bootstrap/e2e_test.go`: `TestAuthentication`, `TestOperationsAndAdminRole`, `TestServeRefusesWithoutTokens`; `apps/fhir-server/internal/config/config_test.go`: `TestParseTokens`, `TestJWTSettingsValidation`, `TestAuthDisabledAllowedOutsideProduction` |
| BR-47, BR-48 (JWT, JWKS, claim) | `pkg/fhirauth/jwt_test.go`: `TestVerifyHMAC`, `TestVerifierConfig`, `TestVerifyJWKS`, `TestVerifyClaimsDoesNotRequireFHIRRoleOrProject`; `apps/fhir-server/internal/bootstrap/jwt_e2e_test.go`: `TestJWTAuthenticationAndRoles` |
| BR-49, BR-50 (scope SMART) | `pkg/fhirauth/smartv2_test.go`: `TestSMARTv2LettersGrantExactlyThemselves`, `TestRoleScopesKeepTheirMeaning`, `TestRepoEnforcesEachInteractionSeparately`, `TestForbiddenMessageNamesTheMissingLetter`; `pkg/fhirauth/grants_test.go`: `TestSMARTScopesToGrants`, `TestGrantsEnforcedByScope`; `apps/fhir-server/internal/bootstrap/smartv2_e2e_test.go`: `TestSMARTv2EachLetterGrantsOnlyItsInteraction`, `TestSMARTv2PerEntryEnforcementInBatch`; `apps/fhir-server/internal/bootstrap/jwt_e2e_test.go`: `TestJWTSMARTScopesPerResourceType` |
| BR-51, BR-52 (AccessPolicy) | `pkg/fhirauth/policy_test.go`: `TestParsePolicy`, `TestVerifyReadsPolicyClaim`, `TestAccessPolicyEnforced`; `pkg/fhirauth/policy_or_test.go`: `TestParsePolicyAlternatives`, `TestPolicyAlternativesEnforced`; `pkg/fhirauth/snapshot_test.go`: `TestPolicyWildcardAndDeny`, `TestPolicyWildcardRejectsBadForms`, `TestCanReadResource`; `pkg/fhirauth/compartment_test.go`: `TestCompartmentPolicyRead`, `TestCompartmentPolicySearchTotalAndPaging`, `TestCompartmentPolicyParsing`, `TestStaticPolicyCriteriaArePushedDown`; `pkg/fhirauth/requireadmin_test.go`: `TestRequireAdmin`; `apps/fhir-server/internal/bootstrap/jwt_e2e_test.go`: `TestJWTAccessPolicyClaim`, `TestExportHonoursGrantsAndPolicy`; `e2e/patient_compartment_test.go` |
| BR-53 (tách project) | `pkg/fhirauth/scope_test.go`: `TestProjectIsolation`, `TestScopedParity`, `TestUpdateAsCreateAcrossProjects`; `apps/fhir-server/internal/bootstrap/jwt_e2e_test.go`: `TestJWTProjectIsolationOverHTTP` |
| BR-54 (super-admin) | `pkg/fhirauth/grants_test.go`: `TestSuperScope`; `apps/fhir-server/internal/bootstrap/jwt_e2e_test.go`: `TestSuperAdminActsInChosenProjectWithAudit`, `TestSuperScopeIgnoredUnlessEnabled` |
| BR-55 (assign-project) | `pkg/fhirauth/assign_test.go`: `TestAssignProject`; `apps/fhir-server/internal/bootstrap/e2e_test.go`: `TestAssignProjectCommand` |
| BR-61, BR-62 (probe, tắt êm) | `apps/fhir-server/internal/bootstrap/e2e_test.go`: `TestHealthAndPublicMetadata`, `TestReadinessReflectsDraining`, `TestServeGracefulShutdown`, `TestServeGracefulShutdownWithSubscriptions`; `pkg/httpserve/httpserve_test.go`: `TestRunGracefulShutdownOrder`, `TestRunListenFailureStillStops`, `TestRunReportsStopAndShutdownErrors`, `TestHealthCheckFailures`; `apps/fhir-server/internal/bootstrap/ws_e2e_test.go`: `TestWebSocketGracefulShutdownClosesConnections` |
| BR-63, BR-64 (hạn mức, body, CORS, header, lỗi) | `apps/fhir-server/internal/bootstrap/httpsec_e2e_test.go`: `TestSecurityHeadersAndCORSOnFHIR`, `TestNoCORSWithoutAllowedOrigins`, `TestRateLimitPerIP`, `TestRateLimitIgnoresSpoofedForwardedFor`, `TestRateLimitUsesForwardedForFromTrustedProxy`; `apps/fhir-server/internal/bootstrap/e2e_test.go`: `TestRoutingErrors`; `pkg/httpsec/httpsec_test.go`; `apps/fhir-server/internal/config/httpsec_test.go`: `TestHTTPSecuritySettings`, `TestHTTPSecuritySettingsRejected`, `TestRateLimitDefaultsOnInProduction` |
| BR-65 (cấu hình, migrate, CLI) | `apps/fhir-server/internal/config/config_test.go`: `TestValidationErrorsAreAggregated`, `TestDatabaseRequireSchema`, `TestDatabaseMigrateURL`, `TestWSModeSettings`; `apps/fhir-server/internal/config/docs_test.go`: `TestConfigDocIsUpToDate`; `apps/fhir-server/internal/bootstrap/e2e_test.go`: `TestPersistenceAndMigrateCLI`, `TestAssignProjectCommand`; `apps/fhir-server/internal/bootstrap/sweep_cli_test.go`: `TestSweepBinaryCommand`; `apps/fhir-server/internal/bootstrap/schema_pg_test.go`: `TestStartsOnOwnSchemaAndRefusesPublic`, `TestSeparateMigrationRolesFlow` (cần PostgreSQL thật); `apps/fhir-server/internal/bootstrap/replicas_e2e_test.go`: `TestReplicasMigrateConcurrentlyOnEmptyDatabase` |
| Broker (memory, redis, dead-letter) | `pkg/messaging/messaging_test.go`: `TestInMemoryDispatchesAndSwallowsHandlerErrors`, `TestRedisBrokerDeliversAndAcks`, `TestRedisRetriesFailedMessageUntilItSucceeds`, `TestRedisPoisonMessageGoesToDeadLetter`, `TestRedisReclaimsMessagesOfDeadConsumer`, `TestRedisBroadcastReachesEveryInstanceWhileGroupSplits` |
| BR-66 (bật, cấu hình) | `apps/fhir-server/internal/config/config_test.go`: `TestAutoDownloadSettings`; `pkg/fhirdownload/download_test.go`: `TestNewValidatesConfig` |
| BR-67 (chọn URL) | `pkg/fhirdownload/download_test.go`: `TestSkipsUrlsThatAreNotExternalOrNotAllowed`, `TestIgnoresDeletesAndUnrelatedTypes` |
| BR-68 (SSRF) | `pkg/fhirdownload/download_test.go`: `TestBlocksPrivateAddressesByDefault`; `apps/fhir-server/internal/bootstrap/autodownload_e2e_test.go`: `TestAutoDownloadEndToEnd` |
| BR-69 (Binary đúng project, loại, kích thước) | `pkg/fhirdownload/download_test.go`: `TestDownloadsExternalAttachmentIntoProjectBinary`, `TestPermanentRefusalsLeaveNothing`; `apps/fhir-server/internal/bootstrap/autodownload_e2e_test.go`: `TestAutoDownloadEndToEnd` |
| BR-70 (thay URL, xung đột phiên bản) | `pkg/fhirdownload/download_test.go`: `TestVersionConflictCleansUpAndRetrySucceeds`, `TestResourceChangedOrDeletedWhileDownloading` |
| BR-71 (lỗi tạm, hàng đợi nền) | `pkg/fhirdownload/download_test.go`: `TestTemporaryFailuresAreReturnedAndLeaveNothing`, `TestAsyncModeDoesNotBlockTheWriteAndRetries` |

## 5.4 Lệch, rủi ro và điểm chưa kiểm

Gom từ ba cụm; đây là các điểm tài liệu cũ lệch mã, hành vi bất thường hoặc rủi ro (đặc biệt bảo mật và mất dữ liệu), và mục chưa kiểm. Chưa tự sửa mã hay tài liệu cũ.

### Từ cụm REST, tìm kiếm, bundle

- `pkg/fhirsearch/request.go` (đầu tệp) còn ghi "Chưa hỗ trợ ... các modifier :in/:above/:below của token", nhưng mã đã hỗ trợ chúng qua `pkg/fhirrouter/termsearch.go` (giải thành danh sách mã trước khi vào Repository). Gọi thẳng `Repository.Search` với các modifier này trả 501.
- `_include:iterate` và `_revinclude:iterate` được phân tích (trường `Iterate`) nhưng KHÔNG có mã nào dùng; hành vi thực tế là include một lượt như bản không `:iterate`. Mô tả "iterate" của bản Medplum không đúng ở đây.
- `_total=estimate` được xử lý giống `accurate` (luôn `COUNT(*)`), có thể chậm trên bảng lớn; `_summary` ngoài `count` bị bỏ qua.
- CapabilityStatement luôn ghi `updateCreate=false` kể cả khi bật `FHIR_UPDATE_AS_CREATE=true`; không khai báo operation (`$validate`, `$everything`, `$export`, ...), `_filter`, `_has`, GraphQL hay terminology; `conditionalDelete=single` đúng với mã (nhiều khớp là 412). `search-type` ghi cho mọi loại kể cả loại bị scope cấm.
- Rủi ro bảo mật (đọc mã, chưa kiểm bằng test): search hệ thống không `_type` với token JWT KHÔNG có chữ `s` nào mà chỉ có chữ `r` (ví dụ `system/Patient.r`): `Searchable()` rỗng nên `scoped.Types` nil nghĩa là "mọi loại" ở kho; sau đó kết quả bị lọc theo `CanSee` (chữ `r` hoặc `s`) nên người gọi vẫn liệt kê được Patient bằng search hệ thống dù không có `s`, và `total` đếm cả loại không thấy được (rò số lượng trong project của chính họ). (chưa kiểm)
- Ở chế độ token tĩnh (`FHIR_READ_TOKENS`, `FHIR_WRITE_TOKENS`, `FHIR_ADMIN_TOKENS`) không có `fhirauth.Scope`: không tách project, không chữ SMART, không AccessPolicy, `meta.project` do client gửi được lưu nguyên và dữ liệu của mọi người dùng chung một không gian. Mọi bộ quyền chữ cái trong mục F chỉ có hiệu lực ở chế độ JWT.
- Bundle batch cần vai trò WRITE kể cả chỉ chứa GET (token READ không dùng được batch để đọc hàng loạt).
- Một entry batch với `request.url` rỗng hoặc `/` dẫn tới route `POST ""` (chính là xử lý bundle), nên bundle lồng nhau có thể được chạy đệ quy; chưa thấy test hay giới hạn độ sâu. (chưa kiểm)
- Update luôn tạo phiên bản mới kể cả khi nội dung không đổi (không có phát hiện thay đổi rỗng), nên PUT lặp lại làm phình lịch sử; lịch sử không phân trang (`_count`, `_since` bị bỏ qua) và không có history mức loại hay mức hệ thống.
- `If-Match: *` không được hiểu là "bất kỳ phiên bản"; nó bị coi là một versionId lạ và trả 412.
- Resource đã xóa của project khác trả 410 thay vì 404 (kho chỉ có nội dung rỗng để so project): lộ sự tồn tại trước đó của id (id là UUID nên khó đoán). Ngoại lệ do chính mã ghi chú.
- Không có kiểm tham chiếu (BR-28) và không có profile, strict mode, `checkReferencesOnWrite` như Medplum; ai kỳ vọng các cơ chế này (theo tài liệu Medplum) sẽ không có chúng ở đây.
- Tìm kiếm chained và terminology là ảnh chụp tại thời điểm tìm: ghi đồng thời có thể làm kết quả lệch; phân trang offset không ổn định khi dữ liệu thay đổi giữa các trang.
- `GraphQL` trả HTTP 200 kể cả khi lỗi quyền (403 nằm trong `errors`); `Content-Type` là `application/json`, khác phần còn lại của API (`application/fhir+json`).
- `Prefer: return=minimal` và `return=OperationOutcome` áp cho mọi POST (kể cả POST bundle batch/transaction và `$validate`/operation POST), nên có thể làm mất Bundle phản hồi. (chưa kiểm bằng test cho bundle)
- Chưa kiểm: hành vi chính xác của số liệu `total` khi search hệ thống có `_type` gồm loại bị `AccessPolicy` loại một phần. Ghi chú: `LockKey` trên PostgreSQL là advisory lock theo transaction, trên SQLite là chiếm khóa ghi sớm (writer khác chờ theo busy_timeout), nên cả hai đều tuần tự hóa tạo và sửa có điều kiện.

### Từ cụm job, export, terminology, Binary

- Quyền admin của `$import` và `PackageRelease/$install` chỉ được ép ở `RequireAdmin` của repository có Scope (khi JWT bật). Guard HTTP (`RequiredRole`) xếp các route POST này vào RoleWrite, và ở chế độ token tĩnh (FHIR_*_TOKENS) repository không cài `AdminChecker`, nên token ghi (không phải admin) gọi được `$import` và `$install`, trái với swagger ("cần quyền admin"). Suy từ mã (`pkg/fhirrouter/codesystemimport.go`, `packageinstall.go`, `apps/fhir-server/internal/presentation/httpapi/auth.go`, `container.go`); (chưa kiểm) bằng test ở chế độ token tĩnh. `TestPackageInstallRequiresAdmin` và `e2e/definitions_import_test.go` chỉ phủ chế độ JWT.
- **Đã sửa (S0, P0-3):** `$reindex` cả loại nay là job có phạm vi, chạy trong project của người gọi (`TestReindexTypeStaysInsideCallerProject`). Còn lại: AccessPolicy chỉ bị chặn khi job chạy (AsyncJob lỗi `403`), không chặn ngay ở đường tạo job (`202`).
- Chỉ AsyncJob có `request` bắt đầu `export?` hoặc `batch?` bị `ExpireJobs` dọn; AsyncJob `reindex` (và loại job tự đăng ký khác) nằm lại vĩnh viễn. Route `DELETE job/{id}/status` chỉ nhận job `batch?`, nên không có API hủy hoặc xóa job `reindex` (404).
- Nếu `Submit` thất bại sau khi tạo AsyncJob (ví dụ orchestrator đã tắt), client nhận 500 nhưng AsyncJob `accepted` vẫn còn và sẽ được vòng bảo trì chạy sau 30 giây, tức là thao tác "lỗi" vẫn thực hiện.
- Ngữ nghĩa giao job là "ít nhất một lần": nếu heartbeat không ghi được lâu hơn FHIR_JOB_LEASE (DB treo) replica khác tiếp quản và hai bên cùng chạy; ghi kết quả cuối không dùng `If-Match`, bản ghi sau cùng thắng. Export không có checkpoint nên chạy lại từ đầu; Binary kết quả của lần chạy bị bỏ (chết đột ngột) không nằm trong `output` nên không bị dọn bởi hủy hay `ExpireJobs`: rò dần trong CSDL (suy từ mã; chưa kiểm bằng test).
- JobRecoveryGrace (30 giây) không có biến môi trường. FHIR_JOB_MAINTENANCE_INTERVAL = 0 tắt cả việc nhặt job mồ côi lẫn xóa kết quả hết hạn. Orchestrator mặc định là `Inline` (hàng đợi trong bộ nhớ); adapter asynq và Temporal có trong `pkg/workflow` nhưng bootstrap không dùng.
- Hàm job `reindex` truyền `progress = nil`, nên không có tiến độ trong AsyncJob; không có tham số lọc (chỉ theo loại).
- Chú thích đầu `pkg/fhirrouter/terminology.go` vẫn ghi "Không hỗ trợ: ... $subsumes, $translate" nhưng hai operation này có trong `terminology2.go` và được đăng ký. Swagger mô tả `/job/{id}/status` có trạng thái `in-progress` trong khi dữ liệu chỉ có `accepted`, `completed`, `error`, `cancelled`.
- `$subsumes` và `:below`, `:above` chỉ đọc cây concept nằm trong JSON của CodeSystem; với CodeSystem `content=not-present` đã nạp qua `$import` thì `$subsumes` trả 404 `Không có mã ...` và `:below`/`:above` không khớp gì, dù `$lookup`, `$expand` (filter `is-a`, `descendent-of`, `=`), `$validate-code` đọc được từ bảng dẫn xuất. Nạp vào CodeSystem có `content` khác `not-present` thì dữ liệu vào bảng dẫn xuất nhưng KHÔNG bao giờ được tra (không có cảnh báo). (suy từ mã; chưa kiểm bằng test)
- `$expand` tính toàn bộ trong bộ nhớ, không bộ nhớ đệm, trần 100000 mã; `count` không có trần riêng. `$translate` với nhiều ConceptMap chỉ lấy tối đa 50 bản đồ đầu theo thứ tự tìm.
- `$import` ConceptMap: ánh xạ trùng (nguồn, đích) gộp một dòng (khác Medplum, đã ghi trong mã). CodeSystem `$import` không xóa concept không có trong lần nạp (chỉ upsert); muốn thay thế sạch phải expunge CodeSystem rồi tạo lại.
- **Đã sửa (S5, P0-2):** khi có `FHIR_BINARY_STORAGE`, tệp export (khoảng 8 MiB) và đầu vào, đoạn kết quả của batch async ra kho `file:`/`s3:` không trần base64 và xóa hết hạn bằng Expunge (xóa cả dòng lẫn tệp); trước đây nằm base64 trong CSDL và chỉ bị xóa mềm. Không có kho thì vẫn base64 trong CSDL (`TestExportResultsGoToObjectStorageAndAreExpungedOnExpiry`). Chạy job ở service riêng: `fhir-worker` (`FHIR_JOB_EXECUTION=false` ở API).
- Tiến trình chết giữa tạo Binary và ghi nội dung (đường POST thô) để lại Binary không nội dung: GET trả 404 và `sweep-binary` KHÔNG phát hiện (chỉ dọn nội dung mồ côi trong kho, không dọn Binary thiếu nội dung).
- Khóa ký tạm (không đặt FHIR_STORAGE_SIGNING_KEY_PATH) chỉ được phép ở dev; ở dev nhiều replica sẽ không xác minh được URL của nhau. Kho Azure và GCS chưa có.
- `PackageInstallation` kẹt ở `installing` nếu tiến trình chết giữa `$install` (không có tiến trình dọn).
- `$expunge` không dọn tham chiếu từ resource khác, không phát sự kiện Subscription (chưa kiểm hành vi của lớp `fhirsub.Wrap` với Expunge), không ghi nhật ký kiểm toán riêng; hành động không thể hoàn tác và không có bước xác nhận.
- Chưa có test HTTP cho `DELETE job/{id}/status` của job `reindex` (route trả 404, suy từ mã) và cho `$import` bằng token ghi ở chế độ token tĩnh (chưa kiểm).

### Từ cụm Subscription, truy cập, vận hành

- **Chưa kiểm bằng chạy test:** toàn bộ bảng TRACE được đối chiếu bằng tên hàm và đọc mã, chưa chạy lại bộ test (các test PostgreSQL và Redis cần `FHIRSTORE_TEST_POSTGRES_URL` hoặc Redis thật, chưa kiểm là có chạy trong CI cục bộ). Chưa có CI trong repo (runbook mục 13).
- **README còn ghi lỗi thời về compartment:** README nói "chưa có scope `patient/<Loại>.read` và `%patient` (kế hoạch P8)" và runbook mục 13 ghi "Token Patient/RelatedPerson chưa đọc được dữ liệu lâm sàng"; mã và `apps/auth/docs/plans/p8-compartment.md` cho thấy `_compartment` và OR nhiều rule đã làm xong (M0..M5). Scope `patient/...` vẫn KHÔNG cấp quyền (bị bỏ qua); quyền compartment đi bằng AccessPolicy.
- **Comment `RequiredRole` lệch mã:** chú thích hàm liệt kê `$export` trong danh sách operation đọc, nhưng danh sách `strings.HasSuffix` không có `$export`; `$export` vẫn là READ nhờ route chỉ đăng ký `GET` (test `TestRequiredRole`). Nếu sau này thêm `POST $export`, nó sẽ rơi về WRITE.
- **`$reindex` cả loại (đã sửa ở S0):** trước đây job chạy trên `JobRepository` không tách project nên admin một project reindex được mọi project; nay job có phạm vi và chỉ chạm project của người gọi. Token `fhir.admin` kèm AccessPolicy vẫn nhận `202` rồi job lỗi `403` (chưa có test HTTP cho trường hợp này). (chưa kiểm bằng test)
- **Search hệ thống lọc sau:** với `GET /?_type=...` hoặc search không chỉ loại, `fhirauth.Repo.Search` không đẩy `criteria` của policy xuống kho (chỉ giao danh sách loại), nên `total` và phân trang có thể tính cả resource ngoài tiêu chí rồi mới bị `filterPolicy` loại; trang có thể thiếu bản ghi và `total` có thể lộ số lượng. Search theo loại không bị (đã đẩy xuống kho, test `TestStaticPolicyCriteriaArePushedDown`). (chưa kiểm bằng test)
- **Mất sự kiện giữa commit và phát:** `fhirsub.Repo` phát sự kiện ngay sau khi kho ghi thành công, không có outbox; tiến trình chết đúng giữa hai bước thì Subscription không bao giờ nhận thông báo. Hàng đợi giao và các lần thử lại đã hẹn ở bộ nhớ nên cũng mất khi tiến trình dừng (tài liệu cũ đã nêu). Với broker `memory`, handler chạy đồng bộ trong luồng ghi (khớp criteria tốn thời gian của request ghi).
- **Giao bền (S3, 2026-11-24):** `FHIR_WEBHOOK_DURABLE=true` (hoặc service `fhir-notifier`) giao đồng bộ trong handler và chỉ ACK sau khi giao xong hoặc bỏ cuộc, nên hai điểm "mất sự kiện khi restart" và "hàng đợi 1024 đầy thì bỏ" của chế độ mặc định không còn; đổi lại giao ít nhất một lần (bên nhận có thể thấy trùng), thông báo đã hết lượt thử chỉ ghi log (không dead-letter) và instance chết thì thông báo dở dang trễ tới `ClaimIdle` (tối thiểu 1 phút).
- **Webhook không bảo đảm thứ tự và có thể trùng:** nhiều worker và retry xen kẽ; at-least-once ở tầng Redis. Bên nhận phải khử trùng lặp.
- **Secret và header xác thực của Subscription lưu rõ trong resource:** extension `subscription-secret` và `channel.header` (có thể chứa `Authorization`) nằm trong nội dung Subscription, người đọc được Subscription trong project đều thấy. (rủi ro bảo mật)
- **Không kiểm Subscription lúc tạo:** endpoint sai, criteria chained hoặc kênh lạ được lưu bình thường (`201`) rồi âm thầm bị bỏ qua khi nạp (chỉ có log WARN); `$validate` không phát hiện. Địa chỉ nội bộ chỉ bị chặn khi giao, không lúc tạo.
- **`BaseDelay` và `QueueSize` của webhook không có biến cấu hình** (1 giây và 1024 là mặc định cứng trong thư viện); README chỉ nêu `FHIR_WEBHOOK_MAX_ATTEMPTS`, `WORKERS`, `TIMEOUT`.
- **`DEBUG` ghi "cấm ở staging/production" nhưng không có kiểm ở `validate()`** (`config.md` và `docs_test.go` ghi cấm, mã không từ chối). (chưa kiểm sâu)
- **Giới hạn kiểm toán:** super-admin chỉ để lại dòng log (`audit=true`), chưa có `AuditEvent` bền; yêu cầu thiếu `X-Fhir-Project` (400) không có dòng audit; tự tắt Subscription cũng chưa tạo `AuditEvent`.
- **Hạn mức theo IP đếm trong bộ nhớ từng instance**; bucket hết hạn sau 3 phút; không khai `FHIR_TRUSTED_PROXIES` sau proxy thì mọi người chung một hạn mức (dễ gây `429` hàng loạt).
- **Websocket:** token bind dùng lại được tới 5 phút (không thu hồi); quyền cố định theo ảnh chụp tới khi access token gốc hết hạn; Subscription bị tắt hoặc xóa không ngắt socket đang mở; hạn mức bind theo người đếm theo instance (cả cụm cho tối đa N nhân số replica).
- **Quyền ghi của policy compartment Patient:** theo kế hoạch P8 ghi chú, AccessPolicy Patient mặc định do `auth` phát cho phép ghi trong compartment (tạo Observation của mình `201`, sửa Patient của mình `200`); muốn chỉ đọc phải đặt `readonly: true`.
- **`seed definitions`** có trong `cmd/fhir-server` và runbook nhưng không thuộc F-26 đã chốt; chưa rõ cụm nào mô tả. (chưa kiểm)
- **Token tĩnh không có tách project và scope SMART:** `Principal` chỉ có tên và vai trò, kho không bọc `fhirauth.Scope`; ở chế độ này AdminChecker không có nên các thao tác như `CodeSystem/$import` chỉ đòi WRITE ở cổng (đọc mã `AdminChecker`, chưa có test riêng cho chế độ tĩnh). (chưa kiểm)
- **Cache JWKS dùng HTTP client thường (không chống SSRF):** `FHIR_JWKS_URL` do vận hành đặt nên ít rủi ro, staging/production bắt buộc https.

### Từ cụm tự tải attachment ngoài (F-28)

- **`FHIR_AUTO_DOWNLOAD_IGNORED_PREFIXES`:** đã có mô tả trong `config.md` (sửa ở S0).
- **Số worker, cỡ hàng đợi và số lần thử không cấu hình được:** 2 worker, hàng đợi 1024, 3 lần (chờ 1s, 2s), 5 phút mỗi lần là mặc định cứng trong thư viện; chỉ có `TIMEOUT`, `MAX_BYTES` và các tiền tố.
- **Mất việc khi dùng broker bộ nhớ:** hàng đợi nằm trong tiến trình; khởi động lại hoặc hàng đầy thì attachment giữ URL ngoài, không có cách tự quét lại (cần ghi lại resource để kích hoạt lại). Broker Redis thử lại và dead-letter nhưng hàng đợi nội bộ của worker vẫn là bộ nhớ.
- **Giao lại sự kiện:** đã có test nhận lại cùng sự kiện cũ không tải lại (`TestRedeliveredEventDoesNotDownloadTwice`, S0); chưa có test redelivery thật của broker Redis sau khi consumer chết. (chưa kiểm)
- **Bề mặt ra Internet:** worker gọi URL do người dùng cung cấp; chống SSRF dựa vào `safehttp` (chặn địa chỉ nội bộ); không giới hạn số URL mỗi resource hay tổng băng thông; nên dùng `ALLOWED_PREFIXES` ở production. `ALLOW_PRIVATE` bị cấm ở staging/production nhưng vẫn mở ở dev.
- **Worker dùng kho không tách project:** Binary và cập nhật resource nguồn đi qua repo không bọc `fhirauth.Scope`, project do worker tự gán từ `meta.project` của resource nguồn. Resource không có `meta.project` (kho dùng chung chưa `assign-project`) tạo Binary không project.
- **Sinh phiên bản mới ngầm:** mỗi lần tải xong làm resource nguồn lên một phiên bản (có sự kiện), nên Subscription và lịch sử thấy thêm một bản update không do người dùng thực hiện.
