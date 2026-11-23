# @medplum/cli

> Công cụ dòng lệnh `medplum` (đăng nhập, deploy bot, bulk, project).

| | |
|---|---|
| Loại | app |
| Đường dẫn hiện tại | `medplum/packages/cli` |
| Đường dẫn đích | `apps/medplum-cli` |
| Số file nguồn (không tính test) | 29 |
| Thư mục `src/` | `agent.test.ts`, `agent.ts`, `auth.test.ts`, `auth.ts`, `aws`, `bot.test.ts`, `bots.ts`, `bulk.test.ts`, `bulk.ts`, `dicomweb.test.ts`, `dicomweb.ts`, `hl7.test.ts`, `hl7.ts`, `index.test.ts`, `index.ts`, `profile-auth.test.ts`, `profiles.test.ts`, `profiles.ts`, `project-archive.test.ts`, `project-archive.ts`, `project.test.ts`, `project.ts`, `rest.test.ts`, `rest.ts`, `storage.test.ts`, `storage.ts`, `test.setup.ts`, `types.test.ts`, `types.ts`, `util`, `utils.test.ts`, `utils.ts` |
| Entry point | `dist/cjs/index.cjs` |
| Lệnh CLI | `medplum` |
| Script npm | `build`, `lint`, `test` |
| Thứ tự di chuyển | Bước 4, thứ tự 4/7 |

## Phụ thuộc

- Runtime: [`core`](core.md), [`hl7`](hl7.md)
- Chỉ dev: [`fhirtypes`](fhirtypes.md), [`mock`](mock.md)
- Được dùng bởi: [`cli-wrapper`](cli-wrapper.md)

## Ghi chú di chuyển

- Giữ nguyên tên package `@medplum/cli`; chỉ đổi thư mục. Import trong mã không đổi.
- Thư mục đích sâu 2 cấp nên `extends "../../tsconfig.json"` vẫn đúng.
- Sau khi chuyển: `turbo run build test --filter=@medplum/cli` phải xanh.
