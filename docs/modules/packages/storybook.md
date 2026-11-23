# @medplum/storybook

> Storybook cho component react.

| | |
|---|---|
| Loại | tool |
| Đường dẫn hiện tại | `medplum/packages/storybook` |
| Đường dẫn đích | `tools/medplum-storybook` |
| Số file nguồn (không tính test) | 4 |
| Thư mục `src/` | `decorators.tsx`, `stories` |
| Entry point | `—` |
| Lệnh CLI | — |
| Script npm | `build`, `dev`, `lint` |
| Thứ tự di chuyển | Bước 4, sau cùng (không phải domain) |

## Phụ thuộc

- Runtime: —
- Chỉ dev: [`core`](core.md), [`definitions`](definitions.md), [`fhirtypes`](fhirtypes.md), [`mock`](mock.md), [`react`](react.md), [`react-hooks`](react-hooks.md), [`react-scheduling`](react-scheduling.md)
- Được dùng bởi: —

## Ghi chú di chuyển

- Giữ nguyên tên package `@medplum/storybook`; chỉ đổi thư mục. Import trong mã không đổi.
- Thư mục đích sâu 2 cấp nên `extends "../../tsconfig.json"` vẫn đúng.
- Sau khi chuyển: `turbo run build test --filter=@medplum/storybook` phải xanh.
