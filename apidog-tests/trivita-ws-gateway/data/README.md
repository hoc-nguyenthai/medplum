# Test data — Trivita WS Gateway

Sinh bởi `generate.py` (đừng sửa tay). Mỗi dòng = một lần chạy scenario; cột `expected_status` / `expected_message` (thông điệp `issue[0].details.text`) là kỳ vọng theo `apps/ws-gateway/docs/api-contract/cases/fhircast.md`.

| File | Scenario | Dòng | Kỹ thuật |
|---|---|---|---|
| hub-lifecycle-stu3/stu2/hub-alias.json | hub-lifecycle-* | 3 mỗi file | phân vùng tương đương theo loại ngữ cảnh (Patient/ImagingStudy/Encounter) |
| hub-validation-subscribe.json | hub-validation-subscribe | 34 | bảng quyết định theo thứ tự kiểm của `Hub.Subscribe`; biên topic 256/257 byte (ASCII và UTF-8 `é`), ký tự điều khiển, events không phân biệt hoa thường, trường bị bỏ qua (`hub.callback`...), bơm chuỗi |
| hub-validation-event.json | hub-validation-event | 21 | thứ tự `ValidateEvent`, sự kiện không đổi ngữ cảnh, biên topic |
| hub-topic-path.json | hub-topic-path | 6 | khớp topic đường dẫn/thân, mã hóa `%2F` |
| hub-get-topic.json | hub-get-topic | 9 | topic mã hóa đặc biệt, biên 256/257, `%00` |
| hub-authz-bad-token.json | hub-authz-bad-token | 6 | token rỗng/giả/dài/tiêm (cột `credential`) |
| hub-authz-scope.json | hub-authz-scope | 3 | scope token (`oauth_scope`) |

Nhóm: Positive/Negative/Boundary/Security đều có. Cảnh báo `Security case expects success` của checker là chủ ý: chuỗi tiêm/path traversal chỉ là chuỗi topic hợp lệ nên API trả 202/200 và không phản chiếu.

**Không có dữ liệu nhạy cảm/credential trong data.** `credential` chỉ chứa token GIẢ; không có JWT thật hay dạng giống JWT (checker chặn); `clientId`/`clientSecret` đưa vào lúc chạy bằng `--env-var`.

**Cố ý bỏ qua** (xem `scenarios/scenarios.md` mục Chỉ thiết kế): WebSocket, cô lập project/người dùng (cần seed thứ hai), 413 (>1 MiB), lỗi Redis/429/503, cờ cấu hình khác mặc định, 409 đồng thời, token alg=none/hết hạn.

Topic dùng dòng data cố định `qa-*` (an toàn chạy lại: ghi đè ngữ cảnh); các scenario lifecycle dùng topic ngẫu nhiên `qa-<epoch>-<rand>` do script đặt.
