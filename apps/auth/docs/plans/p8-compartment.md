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
- M1 đến M5: chưa làm (`_compartment` chưa có trong `pkg/fhirsearch`). Cập nhật 2026-11-23.
