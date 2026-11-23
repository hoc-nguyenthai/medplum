# @medplum/agent

> Agent cài tại chỗ: cầu nối HL7 v2, DICOM, thiết bị với Medplum qua WebSocket. Có bản cài Windows, Linux, macOS.

| | |
|---|---|
| Loại | app |
| Đường dẫn hiện tại | `medplum/packages/agent` |
| Đường dẫn đích | `apps/medplum-agent` |
| Số file nguồn (không tính test) | 33 |
| Thư mục `src/` | `@types`, `__mocks__`, `agent-main.test.ts`, `agent-main.ts`, `app-fetch-logs.test.ts`, `app-stats.test.ts`, `app.test.ts`, `app.ts`, `bytestream.test.ts`, `bytestream.ts`, `channel-stats-tracker.test.ts`, `channel-stats-tracker.ts`, `channel.test.ts`, `channel.ts`, `constants.ts`, `dicom.test.ts`, `dicom.ts`, `enhanced-hl7-client.test.ts`, `enhanced-hl7-client.ts`, `hl7-client-pool.test.ts`, `hl7-client-pool.ts`, `hl7-connection.test.ts`, `hl7-message-tracker.test.ts`, `hl7-message-tracker.ts`, `hl7.test.ts`, `hl7.ts`, `logger.test.ts`, `logger.ts`, `logical-channel.test.ts`, `logical-channel.ts`, `main.test.ts`, `main.ts`, `net-utils.test.ts`, `pid.test.ts`, `pid.ts`, `queue`, `reconnecting-websocket-real-ws.test.ts`, `stats.ts`, `test-utils.test.ts`, `test-utils.ts`, `test.setup.ts`, `types.ts`, `upgrader-test-utils.ts`, `upgrader-utils.test.ts`, `upgrader-utils.ts`, `upgrader.test.ts`, `upgrader.ts` |
| Entry point | `—` |
| Lệnh CLI | — |
| Script npm | `build`, `lint`, `test` |
| Thứ tự di chuyển | Bước 4, thứ tự 3/7 |

## Phụ thuộc

- Runtime: [`core`](core.md), [`hl7`](hl7.md)
- Chỉ dev: [`fhirtypes`](fhirtypes.md), [`mock`](mock.md)
- Được dùng bởi: —

## Ghi chú di chuyển

- Giữ nguyên tên package `@medplum/agent`; chỉ đổi thư mục. Import trong mã không đổi.
- Thư mục đích sâu 2 cấp nên `extends "../../tsconfig.json"` vẫn đúng.
- Sau khi chuyển: `turbo run build test --filter=@medplum/agent` phải xanh.
