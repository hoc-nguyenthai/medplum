# @medplum/hl7

> Parse và serialize HL7 v2, client/server MLLP.

| | |
|---|---|
| Loại | lib |
| Đường dẫn hiện tại | `medplum/packages/hl7` |
| Đường dẫn đích | `libs/medplum-hl7` |
| Số file nguồn (không tính test) | 10 |
| Thư mục `src/` | `base.ts`, `client.test.ts`, `client.ts`, `connection.test.ts`, `connection.ts`, `constants.ts`, `events.ts`, `free-port.ts`, `index.ts`, `server.test.ts`, `server.ts`, `test-utils.ts` |
| Entry point | `dist/cjs/index.cjs` |
| Lệnh CLI | — |
| Script npm | `build`, `lint`, `test` |
| Thứ tự di chuyển | Bước 3, đợt B |

## Phụ thuộc

- Runtime: [`core`](core.md)
- Chỉ dev: [`fhirtypes`](fhirtypes.md)
- Được dùng bởi: [`agent`](agent.md), [`cli`](cli.md)

## Ghi chú di chuyển

- Giữ nguyên tên package `@medplum/hl7`; chỉ đổi thư mục. Import trong mã không đổi.
- Thư mục đích sâu 2 cấp nên `extends "../../tsconfig.json"` vẫn đúng.
- Sau khi chuyển: `turbo run build test --filter=@medplum/hl7` phải xanh.
