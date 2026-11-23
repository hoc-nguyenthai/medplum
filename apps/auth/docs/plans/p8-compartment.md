# P8 — Compartment Patient (`patient/` scope, `%patient`, `%profile`) qua hai service `auth` và `fhir-server`

Thuộc task-list #16. Nguyên tắc: [../../../../docs/service-principles.md](../../../../docs/service-principles.md). Trạng thái: **đã duyệt 2026-11-23** (user: "đồng ý"): M0 làm trước, TTL token Patient giữ 15 phút, đợt này **chỉ đọc**. Tiến độ ở cuối file.

## 1. Vấn đề

`domain.PolicyClaim` (`apps/auth/internal/domain/tokenscope.go`) thấy `%patient`, `%profile`, `compartment`, `interaction`, `hiddenFields` thì **cấm cả loại**. Chính sách mặc định của Patient và RelatedPerson (`accesspolicy_defaults.go`) dùng `_compartment=%patient` và hai rule `Communication` (sender hoặc recipient), nên token của họ không đọc được dữ liệu lâm sàng. `pkg/fhirauth.Policy` chỉ nhận tiêu chí tĩnh, một rule mỗi loại.

## 2. Phân chia theo service

| Phần | Nơi | Vì sao |
|---|---|---|
| Giải `%patient`, `%profile` từ `ProjectMembership.access.parameter` | `apps/auth` | dữ liệu schema `identity`; `auth` không kiểm Patient có tồn tại (D-2) |
| `_compartment`, OR nhiều criteria, kiểm từng kết quả | `pkg/fhirsearch`, `pkg/fhirauth` (chạy trong `fhir-server`) | lọc cạnh dữ liệu lâm sàng, không gọi `auth` (C-9) |
| Hợp đồng | claim `fhir_access_policy` | C-6, ghim bằng golden test |

Không thêm service. Không đổi schema.

## 3. Rủi ro triển khai lệch phiên bản (S-C1)

`ParsePolicy` **bỏ qua khóa lạ** trong mỗi rule (đã đọc mã). Một rule dạng mới do `auth` mới phát (ví dụ `criteriaAny`) mà `fhir-server` cũ đọc thành rule không criteria = **đọc cả loại**. Do đó:

1. M0 làm `ParsePolicy` **từ chối khóa lạ**, deploy trước khi `auth` đổi gì.
2. Dạng claim mới phải bị bên đọc cũ **từ chối** (ví dụ OR = nhiều rule cùng loại: bản cũ báo "nhiều hơn một rule").
3. Lỗi parse claim ở `fhir-server` trả **403** (`jwt.go:189`, `ErrForbidden`), không phải 500. Đã kiểm.
4. Thứ tự deploy: `fhir-server` trước, `auth` sau (ghi vào runbook, M5).

## 4. Các bước

| Bước | Service | Nội dung | Kiểm thu |
|---|---|---|---|
| M0 | `fhir-server` (pkg) | `ParsePolicy` strict: từ chối khóa lạ ở rule; kiểm kiểu | unit; đột biến |
| M1 | `fhir-server` (pkg) | `_compartment=Patient/<id>` ở search và `Match` | so với `$everything`; PG thật qua testkit |
| M2 | `fhir-server` (pkg) | OR nhiều criteria cùng loại (`Communication`) | unit, test âm |
| M3 | `auth` | `PolicyClaim` giải tham số trong danh sách trắng; thiếu giá trị thì cấm loại; kiểm định dạng `Patient/<id>` | unit; chèn `&`, `,`; golden claim |
| M4 | cả hai | e2e hai tiến trình: Patient A không đọc được B qua `_include`, `_revinclude`, history, `$everything`, batch | `e2e/`; đột biến |
| M5 | tài liệu | `apps/auth/docs/api-contract.md` (claim), runbook (thứ tự deploy), docs fhir-server | |

M3 chỉ bật khi M0..M2 đã chạy ở môi trường đích.

## 5. Hệ quả vận hành

- Thu hồi chậm tối đa `AUTH_ACCESS_TTL` (15 phút, C-9): gỡ liên kết bệnh nhân thì token cũ còn đọc đến hết hạn.
- `_compartment` quét nhiều tham số tham chiếu: đo trên PG thật ở M1, xem cần chỉ mục thêm (chỉ trong schema `fhir`).
- Hạn mức vẫn theo instance.

## 6. Ngoài phạm vi đợt này

Ghi (tạo `QuestionnaireResponse`, `Communication`), `hiddenFields`, `interaction` đầy đủ, TTL riêng cho token Patient.

## Tiến độ

- **M0 xong:** `ParsePolicy` (`pkg/fhirauth/policy.go`) từ chối khóa lạ trong rule (test `khóa lạ` ở `policy_test.go`), nên bản `fhir-server` cũ không cấp rộng hơn ý định khi `auth` mới phát claim dạng mới.
- **M1 xong (2026-11-23):** `_compartment=Patient/<id>` ở `pkg/fhirsearch` (`compartment.go`: dựng biểu thức OR của các tham số compartment, Patient thêm chính nó; chỉ nhận đúng một `Patient/<id>`, từ chối nhiều giá trị, loại khác, search hệ thống, loại ngoài compartment). Dùng được ở search của người dùng, ở `criteria` của AccessPolicy (`ParsePolicy` cho phép biểu thức do `_compartment` sinh ra, vẫn cấm `_filter`) và trong `Match`; `Request.Compartment` giữ giá trị để link phân trang dựng lại đúng. Kiểm bằng `pkg/fhirsearch/compartment_test.go` và `pkg/fhirauth/compartment_test.go` (read, history, search kèm total và phân trang, `_include`, `_revinclude`, `$everything`, batch, ghi bị chặn, giao hai compartment; chạy trên memory và SQLite, thêm PostgreSQL khi đặt `FHIRSTORE_TEST_POSTGRES_URL`; **chưa chạy trên PostgreSQL thật** vì máy dev không có Docker). So khớp với `$everything`: tập Observation của `Patient/<id>/$everything` bằng tập của `Observation?_compartment=Patient/<id>`.
- **Hai lỗi có sẵn tìm thấy và sửa khi làm M1:** (1) `fhirauth.Repo.Search` làm rơi điều kiện của chính sách khi đẩy xuống kho, nên `_total=accurate` trả tổng của cả project (lộ số lượng resource ngoài chính sách) và trang bị hụt (`_count=2` trả 1 kết quả); nay điều kiện (cả Filters lẫn Exprs) được đẩy xuống kho, `filterPolicy` vẫn là lớp lọc thứ hai. (2) `$everything` lỗi `403` toàn bộ với token có chính sách vì thử tìm cả loại không được đọc; nay bỏ qua loại bị cấm khi không chỉ định `_type` (như `$export`), có chỉ định `_type` bị cấm thì vẫn `403`.
- **M2 xong (2026-11-23):** `ParsePolicy` (`pkg/fhirauth/policy.go`, `mergeRules`) cho phép nhiều rule cùng loại, resource được thấy khi thỏa **bất kỳ** rule nào (OR); mỗi rule vẫn là AND các điều kiện của nó. Một rule giữ đường Filters như cũ; nhiều rule thành một biểu thức OR của các AND, đẩy xuống kho qua `Request.Exprs` (cùng đường với `_compartment`). Fail đóng với cấu hình mơ hồ: rule không criteria không được đi cùng rule khác, `deny` không đi cùng rule khác, mọi rule của một loại phải cùng `readonly`, tối đa 16 rule mỗi loại. Phiên bản cũ vẫn từ chối dạng này ("nhiều hơn một rule"), đúng như mục 3 yêu cầu. Kiểm bằng `pkg/fhirauth/policy_or_test.go` (Communication gửi hoặc nhận, tổng và phân trang, history, ghi trong và ra ngoài phạm vi, AND trong một rule; memory và SQLite). Đã làm hỏng cố ý (OR thành AND, bỏ kiểm readonly) để thử: test đỏ đúng.
- **M3 xong (2026-11-23), sau cờ `AUTH_PATIENT_COMPARTMENT_ENABLED` (mặc định tắt):** `domain.PolicyClaimWith` (`apps/auth/internal/domain/tokenscope.go`) giải `%patient`, `%patient.id`, `%profile`, `%profile.id` bằng biến của từng AccessPolicy (`application.policyVars`: `profile` là hồ sơ của membership, không ai ghi đè được; `patient` lấy từ `ProjectMembership.access[i].parameter` tên `patient`, nếu không có thì là chính hồ sơ khi hồ sơ là Patient). Danh sách trắng chỉ gồm `patient` và `profile`, đường dẫn chỉ `.id`; giá trị phải đúng `Patient/<id>` (hoặc `Patient|Practitioner|RelatedPerson/<id>` cho `profile`, id ký tự FHIR tối đa 64) nên `&`, `,`, `|`, `$`, khoảng trắng, URL tuyệt đối đều bị từ chối. Thiếu, sai định dạng hoặc biến lạ thì **cấm cả loại** (fail đóng). Nhiều criteria khác nhau cùng loại thành nhiều rule (OR, tối đa 16, readonly lấy mức hẹp nhất) thay vì cấm loại. Cờ tắt giữ nguyên hành vi cũ (cấm các loại dùng `%`). Claim chính sách mặc định của Patient được ghim bằng golden `apps/auth/internal/domain/testdata/patient_policy_claim.golden.json`. Kiểm bằng `domain/policy_compartment_test.go` (bảng giải biến, chèn, fail đóng, nhiều rule, golden) và `bootstrap/patient_compartment_test.go` (token cấp ra được `fhirauth` nhận, Communication gửi hoặc nhận, cờ tắt thì cấm, tham số chèn thì cấm); đã làm hỏng cố ý (nới định dạng, bỏ chuẩn hóa query) để thử: test đỏ đúng.
- **Lỗi có sẵn tìm thấy khi làm M3 và sửa:** `AccessPolicy.resource.criteria` có dạng `Loại?query` (Medplum) nhưng `fhirauth.ParsePolicy` nhận criteria CHỈ gồm query, nên mọi chính sách có criteria bị `ParsePolicy` từ chối ngay khi cấp token (fail đóng, token `access_denied`): chính sách có criteria chưa bao giờ dùng được từ đầu đến cuối. Nay `PolicyClaim` bỏ tiền tố `Loại?` (tiền tố khác loại của rule thì cấm loại), áp cho cả khi cờ tắt.
- **M4 xong (2026-11-23):** `e2e/patient_compartment_test.go` chạy `auth` và `fhir-server` thật (hai binary, SQLite tạm; `make test-e2e`). Bệnh nhân A và B được mời qua `/admin/projects/{id}/invite` (chính sách mặc định của Patient, tham số `patient`), đăng nhập, đổi code lấy token (cờ `AUTH_PATIENT_COMPARTMENT_ENABLED` bật). Token của A: đọc được Patient, Observation (kể cả subject B nhưng performer A), Condition, Communication của mình; **không** đọc được của B (`404`) qua đọc trực tiếp, search (total và phân trang chỉ tính phần được thấy), `_include`, `_revinclude`, `history`, `$everything` và `batch`; loại ngoài chính sách `403`; không tạo được resource trỏ sang B (`403`). Kịch bản thứ hai kiểm cờ tắt: bệnh nhân bị chặn (mọi loại dùng `%patient` bị cấm). Đã làm hỏng cố ý (`matchDoc` luôn true) để thử: e2e đỏ ở đọc trực tiếp, `_include`, history và `$everything`.
- **Điều e2e làm lộ ra (cho M5):** `invite` tạo hồ sơ Patient **trong schema của `auth`**, còn dữ liệu lâm sàng ở `fhir-server` là nơi riêng; compartment chỉ có nghĩa khi Patient có cùng id tồn tại trong `fhir-server`. Chưa có bước tự động tạo hoặc đồng bộ hồ sơ đó (auth không kiểm Patient có tồn tại, D-2). Test tạo bằng `PUT Patient/<id>` với `FHIR_UPDATE_AS_CREATE=true`. Vận hành thật cần quy trình tạo Patient ở `fhir-server` với đúng id của hồ sơ trong `auth` (ghi vào runbook ở M5).
- M5: chưa làm. Cập nhật 2026-11-23.
