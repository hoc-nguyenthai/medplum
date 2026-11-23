# @medplum/mock

> MockClient và repository in-memory dùng cho test, storybook, ví dụ. Chạy trên fhir-router.

| | |
|---|---|
| Loại | lib |
| Đường dẫn hiện tại | `medplum/packages/mock` |
| Đường dẫn đích | `libs/medplum-mock` |
| Số file nguồn (không tính test) | 20 |
| Thư mục `src/` | `client.test.ts`, `client.ts`, `constants.ts`, `index.ts`, `mocks`, `repo.test.ts`, `subscription-manager.test.ts`, `subscription-manager.ts`, `test-resources`, `test.setup.ts` |
| Entry point | `dist/cjs/index.cjs` |
| Lệnh CLI | — |
| Script npm | `build`, `lint`, `test` |
| Thứ tự di chuyển | Bước 3, đợt B |

## Phụ thuộc

- Runtime: [`core`](core.md), [`definitions`](definitions.md), [`fhir-router`](fhir-router.md), [`fhirtypes`](fhirtypes.md)
- Chỉ dev: —
- Được dùng bởi: —

## Ghi chú di chuyển

- Giữ nguyên tên package `@medplum/mock`; chỉ đổi thư mục. Import trong mã không đổi.
- Thư mục đích sâu 2 cấp nên `extends "../../tsconfig.json"` vẫn đúng.
- Sau khi chuyển: `turbo run build test --filter=@medplum/mock` phải xanh.
