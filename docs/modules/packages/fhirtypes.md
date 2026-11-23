# @medplum/fhirtypes

> Kiểu TypeScript (.d.ts) cho toàn bộ resource FHIR R4. Không có logic chạy.

| | |
|---|---|
| Loại | lib |
| Đường dẫn hiện tại | `medplum/packages/fhirtypes` |
| Đường dẫn đích | `libs/medplum-fhirtypes` |
| Số file nguồn (không tính test) | 0 |
| Thư mục `src/` | — |
| Entry point | `—` |
| Lệnh CLI | — |
| Script npm | `build`, `test` |
| Thứ tự di chuyển | Bước 3, đợt A |

## Phụ thuộc

- Runtime: —
- Chỉ dev: —
- Được dùng bởi: [`dosespot-core`](dosespot-core.md), [`dosespot-react`](dosespot-react.md), [`fhir-router`](fhir-router.md), [`health-gorilla-core`](health-gorilla-core.md), [`health-gorilla-react`](health-gorilla-react.md), [`mock`](mock.md), [`react-scheduling`](react-scheduling.md), [`scriptsure-react`](scriptsure-react.md)

## Ghi chú di chuyển

- Giữ nguyên tên package `@medplum/fhirtypes`; chỉ đổi thư mục. Import trong mã không đổi.
- Thư mục đích sâu 2 cấp nên `extends "../../tsconfig.json"` vẫn đúng.
- Sau khi chuyển: `turbo run build test --filter=@medplum/fhirtypes` phải xanh.
