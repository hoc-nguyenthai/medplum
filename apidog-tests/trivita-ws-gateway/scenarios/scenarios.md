# Scenarios — Trivita WS Gateway

Mọi scenario có xác thực mở đầu bằng bước **Chuẩn bị** (GET /healthz + script `pm.sendRequest` lấy token `client_credentials` từ `{{authUrl}}/oauth2/token` bằng `{{clientId}}`/`{{clientSecret}}`, đặt `access_token` và `topic` duy nhất). Base URL của module là `{{wsUrl}}`.

## S-01 health-smoke — Health smoke
P0. 4 probe công khai: live/startup/healthz luôn 200, ready kiểm Redis.
Data: không (bước cố định)
1. GET /health/live — `GET /health/live` (ep 47548558); status == 200, $.status == ok
2. GET /health/startup — `GET /health/startup` (ep 47548560); status == 200, $.status == ok
3. GET /healthz — `GET /healthz` (ep 47548561); status == 200, $.status == ok
4. GET /health/ready (Redis ping được) — `GET /health/ready` (ep 47548559); status == 200, $.ready == true, $.checks.redis == true, $.checks.accepting_traffic == true

## S-02 discovery — Discovery well-known
P0. Cấu hình Hub công khai: STU2 không có getCurrentSupport, STU3 và /hub có; không cần token.
Data: không (bước cố định)
1. STU2 well-known — `GET /fhircast/STU2/.well-known/fhircast-configuration` (ep 47548547); status == 200, $.fhircastVersion == STU2, $.getCurrentSupport not exists
2. STU3 well-known — `GET /fhircast/STU3/.well-known/fhircast-configuration` (ep 47548551); status == 200, $.fhircastVersion == STU3, $.getCurrentSupport == true
3. /hub well-known (bí danh STU3: fhircastVersion vẫn STU3) — `GET /hub/.well-known/fhircast-configuration` (ep 47548555); status == 200, $.fhircastVersion == STU3, $.getCurrentSupport == true

## S-03 hub-lifecycle-stu3 — Hub lifecycle STU3
P0. Đăng ký → mở ngữ cảnh → GET → heartbeat → đóng → hủy trên /fhircast/STU3 (data: Patient/ImagingStudy/Encounter).
Data: `data/hub-lifecycle-stu3.json` (3 dòng)
1. Chuẩn bị: lấy token client_credentials từ auth và đặt topic — `GET /healthz` (ep 47548561); status == 200
2. well-known công khai — `GET /fhircast/STU3/.well-known/fhircast-configuration` (ep 47548551); status == 200
3. Đăng ký kênh websocket (JSON) — `POST /fhircast/STU3` (ep 47548550); status == 202, $['hub.channel.endpoint'] exists
4. GET ngữ cảnh khi chưa có — `GET /fhircast/STU3/{{topic}}` (ep 47548552); status == 200
5. Mở ngữ cảnh qua POST topic — `POST /fhircast/STU3/{{topic}}` (ep 47548553); status == 202, $.success == true, $.event.event['context.versionId'] exists
6. GET ngữ cảnh hiện tại ({{open_type}}) — `GET /fhircast/STU3/{{topic}}` (ep 47548552); status == 200, $['context.type'] == {{open_type}}, $['context.versionId'] == {{ctx_version}}
7. POST {v} (không topic ở đường dẫn): heartbeat không đổi ngữ cảnh — `POST /fhircast/STU3` (ep 47548550); status == 202, $.success == true
8. Đóng ngữ cảnh — `POST /fhircast/STU3/{{topic}}` (ep 47548553); status == 202, $.success == true, $.event.event['context.versionId'] not exists
9. GET sau khi đóng — `GET /fhircast/STU3/{{topic}}` (ep 47548552); status == 200
10. Hủy đăng ký — `POST /fhircast/STU3` (ep 47548550); status == 202, $['hub.channel.endpoint'] exists
11. Hủy lần hai: endpoint không còn → 400 Invalid endpoint — `POST /fhircast/STU3` (ep 47548550); status == 400

## S-04 hub-lifecycle-stu2 — Hub lifecycle STU2
P1. Đăng ký → mở ngữ cảnh → GET → heartbeat → đóng → hủy trên /fhircast/STU2 (data: Patient/ImagingStudy/Encounter).
Data: `data/hub-lifecycle-stu2.json` (3 dòng)
1. Chuẩn bị: lấy token client_credentials từ auth và đặt topic — `GET /healthz` (ep 47548561); status == 200
2. well-known công khai — `GET /fhircast/STU2/.well-known/fhircast-configuration` (ep 47548547); status == 200
3. Đăng ký kênh websocket (JSON) — `POST /fhircast/STU2` (ep 47548546); status == 202, $['hub.channel.endpoint'] exists
4. GET ngữ cảnh khi chưa có — `GET /fhircast/STU2/{{topic}}` (ep 47548548); status == 200
5. Mở ngữ cảnh qua POST topic — `POST /fhircast/STU2/{{topic}}` (ep 47548549); status == 202, $.success == true, $.event.event['context.versionId'] exists
6. GET ngữ cảnh hiện tại ({{open_type}}) — `GET /fhircast/STU2/{{topic}}` (ep 47548548); status == 200
7. POST {v} (không topic ở đường dẫn): heartbeat không đổi ngữ cảnh — `POST /fhircast/STU2` (ep 47548546); status == 202, $.success == true
8. Đóng ngữ cảnh — `POST /fhircast/STU2/{{topic}}` (ep 47548549); status == 202, $.success == true, $.event.event['context.versionId'] not exists
9. GET sau khi đóng — `GET /fhircast/STU2/{{topic}}` (ep 47548548); status == 200
10. Hủy đăng ký — `POST /fhircast/STU2` (ep 47548546); status == 202, $['hub.channel.endpoint'] exists
11. Hủy lần hai: endpoint không còn → 400 Invalid endpoint — `POST /fhircast/STU2` (ep 47548546); status == 400

## S-05 hub-lifecycle-hub-alias — Hub lifecycle HUB
P1. Đăng ký → mở ngữ cảnh → GET → heartbeat → đóng → hủy trên /hub (data: Patient/ImagingStudy/Encounter).
Data: `data/hub-lifecycle-hub-alias.json` (3 dòng)
1. Chuẩn bị: lấy token client_credentials từ auth và đặt topic — `GET /healthz` (ep 47548561); status == 200
2. well-known công khai — `GET /hub/.well-known/fhircast-configuration` (ep 47548555); status == 200
3. Đăng ký kênh websocket (JSON) — `POST /hub` (ep 47548554); status == 202, $['hub.channel.endpoint'] exists
4. GET ngữ cảnh khi chưa có — `GET /hub/{{topic}}` (ep 47548556); status == 200
5. Mở ngữ cảnh qua POST topic — `POST /hub/{{topic}}` (ep 47548557); status == 202, $.success == true, $.event.event['context.versionId'] exists
6. GET ngữ cảnh hiện tại ({{open_type}}) — `GET /hub/{{topic}}` (ep 47548556); status == 200, $['context.type'] == {{open_type}}, $['context.versionId'] == {{ctx_version}}
7. POST {v} (không topic ở đường dẫn): heartbeat không đổi ngữ cảnh — `POST /hub` (ep 47548554); status == 202, $.success == true
8. Đóng ngữ cảnh — `POST /hub/{{topic}}` (ep 47548557); status == 202, $.success == true, $.event.event['context.versionId'] not exists
9. GET sau khi đóng — `GET /hub/{{topic}}` (ep 47548556); status == 200
10. Hủy đăng ký — `POST /hub` (ep 47548554); status == 202, $['hub.channel.endpoint'] exists
11. Hủy lần hai: endpoint không còn → 400 Invalid endpoint — `POST /hub` (ep 47548554); status == 400

## S-06 hub-diagnosticreport-flow — Hub DiagnosticReport open/update/close
P1. DiagnosticReport-open → update đúng phiên bản → update phiên bản cũ (409/400) → close. Phiên bản context.versionId theo FHIRcast STU3.
Data: không (bước cố định)
1. Chuẩn bị: lấy token client_credentials từ auth và đặt topic — `GET /healthz` (ep 47548561); status == 200
2. Mở DiagnosticReport — `POST /fhircast/STU3/{{topic}}` (ep 47548553); status == 202, $.event.event['context.versionId'] exists
3. GET: context.type DiagnosticReport — `GET /fhircast/STU3/{{topic}}` (ep 47548552); status == 200, $['context.type'] == DiagnosticReport, $['context.versionId'] == {{ctx_version}}
4. Update sai phiên bản → 400 Expected event.context.versionId — `POST /fhircast/STU3/{{topic}}` (ep 47548553); status == 400
5. Update thiếu bundle updates → 400 — `POST /fhircast/STU3/{{topic}}` (ep 47548553); status == 400
6. Update có entry POST → 400 (chỉ PUT/DELETE) — `POST /fhircast/STU3/{{topic}}` (ep 47548553); status == 400
7. Update hợp lệ → 202, có priorVersionId và versionId mới — `POST /fhircast/STU3/{{topic}}` (ep 47548553); status == 202, $.event.event['context.priorVersionId'] == {{ctx_version}}, $.event.event['context.versionId'] exists
8. Update lại bằng phiên bản CŨ → 400 (không còn khớp) — `POST /fhircast/STU3/{{topic}}` (ep 47548553); status == 400
9. Đóng DiagnosticReport — `POST /fhircast/STU3/{{topic}}` (ep 47548553); status == 202
10. Update khi đã đóng → 400 No DiagnosticReport currently open — `POST /fhircast/STU3/{{topic}}` (ep 47548553); status == 400

## S-07 hub-validation-subscribe — Hub validation đăng ký/hủy
P1. POST /fhircast/STU3 dạng đăng ký/hủy theo data: thứ tự kiểm, biên topic 256 byte, events, bí danh endpoint, bơm chuỗi.
Data: `data/hub-validation-subscribe.json` (34 dòng)
1. Chuẩn bị: lấy token client_credentials từ auth và đặt topic — `GET /healthz` (ep 47548561); status == 200
2. POST /fhircast/STU3 theo data — `POST /fhircast/STU3` (ep 47548550); status == {{expected_status}}

## S-08 hub-validation-event — Hub validation đổi ngữ cảnh
P1. POST /fhircast/STU3 dạng thông điệp (có id/event) theo data: thứ tự ValidateEvent, biên topic, sự kiện không đổi ngữ cảnh.
Data: `data/hub-validation-event.json` (21 dòng)
1. Chuẩn bị: lấy token client_credentials từ auth và đặt topic — `GET /healthz` (ep 47548561); status == 200
2. POST /fhircast/STU3 thông điệp theo data — `POST /fhircast/STU3` (ep 47548550); status == {{expected_status}}

## S-09 hub-topic-path — Hub POST {v}/{topic} khớp topic
P1. POST /fhircast/STU3/{topic}: kiểm khớp topic (400 Topic mismatch), thân sai dạng, topic mã hóa.
Data: `data/hub-topic-path.json` (6 dòng)
1. Chuẩn bị: lấy token client_credentials từ auth và đặt topic — `GET /healthz` (ep 47548561); status == 200
2. POST /fhircast/STU3/{topic} theo data — `POST /fhircast/STU3/{{topic_path}}` (ep 47548553); status == {{expected_status}}

## S-10 hub-get-topic — Hub GET topic (STU3)
P1. GET /fhircast/STU3/{topic} với topic đặc biệt: mã hóa đúng một lần, biên 256 byte, ký tự điều khiển.
Data: `data/hub-get-topic.json` (9 dòng)
1. Chuẩn bị: lấy token client_credentials từ auth và đặt topic — `GET /healthz` (ep 47548561); status == 200
2. GET /fhircast/STU3/{topic} theo data — `GET /fhircast/STU3/{{topic_enc}}` (ep 47548552); status == {{expected_status}}

## S-11 hub-content-types — Hub content-type form
P2. Đăng ký/hủy dạng application/x-www-form-urlencoded; thân form hỏng; dạng đổi ngữ cảnh gửi bằng form.
Data: không (bước cố định)
1. Chuẩn bị: lấy token client_credentials từ auth và đặt topic — `GET /healthz` (ep 47548561); status == 200
2. Đăng ký bằng form (events có khoảng trắng) — `POST /fhircast/STU3` (ep 47548550); status == 202, $['hub.channel.endpoint'] exists
3. Form channel.type=webhook → 400 — `POST /fhircast/STU3` (ep 47548550); status == 400
4. Form có id+timestamp → rẽ sang đổi ngữ cảnh: Missing event payload — `POST /fhircast/STU3` (ep 47548550); status == 400
5. Hủy bằng form — `POST /fhircast/STU3` (ep 47548550); status == 202
6. Hủy lại bằng form → 400 Invalid endpoint — `POST /fhircast/STU3` (ep 47548550); status == 400

## S-12 hub-requires-auth-all-routes — Hub từ chối khi không có token (9 route)
P1. Mỗi route Hub (STU2, STU3, /hub × POST đăng ký, GET topic, POST topic) trả 401 + WWW-Authenticate khi không có Authorization.
Data: không (bước cố định)
1. POST /fhircast/STU2 không token → 401 — `POST /fhircast/STU2` (ep 47548546); status == 401
2. GET /fhircast/STU2/qa-noauth không token → 401 — `GET /fhircast/STU2/qa-noauth` (ep 47548548); status == 401
3. POST /fhircast/STU2/qa-noauth không token → 401 — `POST /fhircast/STU2/qa-noauth` (ep 47548549); status == 401
4. POST /fhircast/STU3 không token → 401 — `POST /fhircast/STU3` (ep 47548550); status == 401
5. GET /fhircast/STU3/qa-noauth không token → 401 — `GET /fhircast/STU3/qa-noauth` (ep 47548552); status == 401
6. POST /fhircast/STU3/qa-noauth không token → 401 — `POST /fhircast/STU3/qa-noauth` (ep 47548553); status == 401
7. POST /hub không token → 401 — `POST /hub` (ep 47548554); status == 401
8. GET /hub/qa-noauth không token → 401 — `GET /hub/qa-noauth` (ep 47548556); status == 401
9. POST /hub/qa-noauth không token → 401 — `POST /hub/qa-noauth` (ep 47548557); status == 401

## S-13 hub-authz-bad-token — Hub authz: token sai
P1. Token sai/giả/quá dài trên POST đăng ký, GET topic và POST /hub: luôn 401 + WWW-Authenticate, không lộ chi tiết.
Data: `data/hub-authz-bad-token.json` (6 dòng)
1. POST /fhircast/STU3 token sai — `POST /fhircast/STU3` (ep 47548550); status == {{expected_status}}
2. GET /fhircast/STU3/qa-noauth token sai — `GET /fhircast/STU3/qa-noauth` (ep 47548552); status == {{expected_status}}
3. POST /hub token sai — `POST /hub` (ep 47548554); status == {{expected_status}}

## S-14 hub-authz-scope — Hub authz: scope
P1. Token client_credentials xin scope khác nhau: có fhircast/* → 202, không có → 403 (cần auth cấp được token với các scope đó, xem G2).
Data: `data/hub-authz-scope.json` (3 dòng)
1. Lấy token với scope {{oauth_scope}} — `GET /healthz` (ep 47548561); status == 200
2. POST /fhircast/STU3 đăng ký theo scope — `POST /fhircast/STU3` (ep 47548550); status == {{expected_status}}

## Chỉ thiết kế (không build)

| Flow | Lý do |
|---|---|
| `GET /ws/fhircast/{endpoint}` và `GET /ws/subscriptions-r4` (nâng cấp WebSocket, khung handshake/event-notification/heartbeat, đóng 1001/1009) | Test Scenario của Apidog CLI chạy HTTP, không có bước WebSocket. Đã có tests/integration và tests/e2e của ws-gateway (gorilla/coder websocket) và `make smoke-full` (helper Go) phủ phần này. Vẫn nên khai 2 endpoint trong module để tài liệu đủ. |
| Thông điệp đổi ngữ cảnh được phát tới socket đang nối sau `POST` (E1) | cần client WebSocket đồng thời; xem trên |
| Cô lập theo project (project khác không đọc/đổi/hủy được) và người dùng khác cùng project hủy đăng ký của bạn → `Invalid endpoint` | cần token của project thứ hai / user thứ hai (seed thêm một project và một client); chưa có dữ liệu seed |
| 413 khi thân > `FHIRCAST_MAX_BODY_BYTES` (1 MiB) | cần cell dữ liệu > 1 MiB; để cho test Go (`TestFhircastBodyTooLarge...`). Có thể thêm 1 dòng nếu muốn |
| 429 hạn mức kết nối WS, 503 `/health/ready` khi Redis chết hoặc đang tắt, 500 do Redis lỗi | cần phá hạ tầng (dừng Redis) — không làm trên stack chung; đã phủ ở tests/integration |
| `FHIRCAST_ALLOW_TOPIC_MISMATCH=true` (T3), `FHIRCAST_REQUIRE_SCOPE=false`, FHIRCAST tắt (404) | cần khởi động lại gateway với cấu hình khác |
| 409 xung đột update đồng thời (E14) | cần 2 yêu cầu song song; chỉ test ở tầng Hub (`TestHubConcurrentUpdatesExactlyOneWins`) |
| Token alg=none / hết hạn / sai issuer-audience | cần token tự ký; `check_test_assets.py` chặn chuỗi giống JWT trong data; đã có test `fhirauth` |

## Phủ data theo nhóm

| Nhóm | Số dòng | Scenario |
|---|---|---|
| Positive | 20 | hub-authz-scope, hub-get-topic, hub-lifecycle-hub-alias, hub-lifecycle-stu2, hub-lifecycle-stu3, hub-topic-path, hub-validation-event, hub-validation-subscribe |
| Negative | 38 | hub-get-topic, hub-topic-path, hub-validation-event, hub-validation-subscribe |
| Boundary | 17 | hub-get-topic, hub-topic-path, hub-validation-event, hub-validation-subscribe |
| Security | 13 | hub-authz-bad-token, hub-authz-scope, hub-get-topic, hub-validation-event, hub-validation-subscribe |
| Other | 0 | — |
