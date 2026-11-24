# Tham chiếu API: nhóm `fhircast`

> **Tệp này được SINH từ `swagger.json`** (Trivita WS Gateway API); đừng sửa tay. Cập nhật: `UPDATE_DOCS=1 go test` ở thư mục `docs`. Điều kiện nào ra phản hồi nào (từng trường hợp, ví dụ JSON, header) ở [../cases/fhircast.md](../cases/fhircast.md).

Hub FHIRcast đồng bộ ngữ cảnh giữa ứng dụng lâm sàng; có khi FHIRCAST_ENABLED=true. Ba tiền tố: /fhircast/STU2, /fhircast/STU3 và /hub (bí danh của STU3). Bearer JWT do auth cấp, cần claim project và scope fhircast/*.

**Header phản hồi chung (mọi route):** `Cache-Control: no-store, no-cache, must-revalidate`, `Content-Security-Policy: default-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none';`, `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`, `X-Request-Id`. Định dạng lỗi, xác thực, hạn mức: [../common.md](../common.md).

| Route | Tóm tắt |
|---|---|
| [`POST /fhircast/STU2`](#post-fhircast-stu2) | Đăng ký, hủy đăng ký hoặc đổi ngữ cảnh |
| [`GET /fhircast/STU2/.well-known/fhircast-configuration`](#get-fhircast-stu2-well-known-fhircast-configuration) | Cấu hình Hub FHIRcast |
| [`GET /fhircast/STU2/{topic}`](#get-fhircast-stu2-topic) | Ngữ cảnh hiện tại (STU2) |
| [`POST /fhircast/STU2/{topic}`](#post-fhircast-stu2-topic) | Đổi ngữ cảnh của một topic |
| [`POST /fhircast/STU3`](#post-fhircast-stu3) | Đăng ký, hủy đăng ký hoặc đổi ngữ cảnh |
| [`GET /fhircast/STU3/.well-known/fhircast-configuration`](#get-fhircast-stu3-well-known-fhircast-configuration) | Cấu hình Hub FHIRcast |
| [`GET /fhircast/STU3/{topic}`](#get-fhircast-stu3-topic) | Ngữ cảnh hiện tại (STU3) |
| [`POST /fhircast/STU3/{topic}`](#post-fhircast-stu3-topic) | Đổi ngữ cảnh của một topic |
| [`POST /hub`](#post-hub) | Đăng ký, hủy đăng ký hoặc đổi ngữ cảnh |
| [`GET /hub/.well-known/fhircast-configuration`](#get-hub-well-known-fhircast-configuration) | Cấu hình Hub FHIRcast |
| [`GET /hub/{topic}`](#get-hub-topic) | Ngữ cảnh hiện tại (STU3) |
| [`POST /hub/{topic}`](#post-hub-topic) | Đổi ngữ cảnh của một topic |

---

<a id="post-fhircast-stu2"></a>
## `POST /fhircast/STU2`

**Đăng ký, hủy đăng ký hoặc đổi ngữ cảnh**

Một route, hai dạng theo nội dung body. (1) Có trường id hoặc event: đổi ngữ cảnh như POST {v}/{topic} (không kiểm khớp topic đường dẫn); phản hồi 202 có success và event. (2) Không có: đăng ký (hub.mode=subscribe) hoặc hủy (hub.mode=unsubscribe) kênh websocket; phản hồi 202 có hub.channel.endpoint. Đăng ký cần hub.channel.type=websocket, hub.mode, hub.topic, hub.events (14 sự kiện được công bố); hủy chỉ cho đúng người, project và topic đã đăng ký, sai bất kỳ điều nào cũng là 400 "Invalid endpoint". Nhận application/json hoặc application/x-www-form-urlencoded (dạng đăng ký). Body vượt FHIRCAST_MAX_BODY_BYTES là 413.

Các trường hợp phản hồi: [../cases/fhircast.md#post-fhircast-stu2](../cases/fhircast.md#post-fhircast-stu2)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | `application/json`, `application/x-www-form-urlencoded` |
| Content-Type phản hồi | `application/json`, `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Content-Type` | có | `application/json`, `application/x-www-form-urlencoded` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Body yêu cầu** (Yêu cầu đăng ký/hủy hoặc thông điệp đổi ngữ cảnh)

Schema: `httpapi.FhircastHubRequest`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `endpoint` | string | không |  | dạng hủy, tên trường cũ của client trước @medplum/core 5.1.29; chỉ dùng khi thiếu hub.channel.endpoint | `"baa8974b-807b-490d-9e87-334f99a72b6d"` |
| `event` | object `httpapi.FhircastEventPayload` | không |  | dạng đổi ngữ cảnh: nội dung sự kiện |  |
| `event.context` | mảng `httpapi.FhircastContextItem` | có |  | ngữ cảnh của sự kiện; mảng (có thể rỗng) các phần tử {key, resource} |  |
| `event.context[].key` | string | có |  | vai trò của resource trong ngữ cảnh (patient, encounter, study, report...) | `"patient"` |
| `event.context[].resource` | object tự do | có |  | resource FHIR dạng JSON (luôn có resourceType); với sự kiện update là Bundle |  |
| `event.hub.event` | string | có | một trong: `Patient-open`, `Patient-close`, `ImagingStudy-open`, `ImagingStudy-close`, `Encounter-open`, `Encounter-close`, `DiagnosticReport-open`, `DiagnosticReport-close`, `DiagnosticReport-select`, `DiagnosticReport-update`, `userlogout`, `userhibernate`, `syncerror`, `heartbeat` | tên sự kiện | `"Patient-open"` |
| `event.hub.topic` | string | có |  | topic của phiên làm việc; không rỗng, tối đa 256 byte, không ký tự điều khiển | `"7f2c4e1a-9b3d-4c5e-8a1f-2d6b9e0c3a47"` |
| `event.context.versionId` | string | không |  | phiên bản ngữ cảnh: Hub cấp khi mở; sự kiện DiagnosticReport-update phải gửi đúng phiên bản hiện tại | `"c8fa1e88-c131-4848-8467-9a0b2d1f5e33"` |
| `hub.channel.endpoint` | string | không |  | dạng hủy: URL hoặc endpoint đã được cấp lúc đăng ký | `"ws://gw.example.org:8007/ws/fhircast/baa8974b-807b-490d-9e87-334f99a72b6d"` |
| `hub.channel.type` | string | không | một trong: `websocket` | dạng đăng ký: kênh, chỉ "websocket" (webhook, rest-hook, email bị từ chối) | `"websocket"` |
| `hub.events` | string | không |  | dạng đăng ký, bắt buộc khi subscribe: tên sự kiện cách nhau bằng dấu phẩy, mỗi tên phải thuộc 14 sự kiện được công bố | `"Patient-open,Patient-close"` |
| `hub.mode` | string | không | một trong: `subscribe`, `unsubscribe` | subscribe: tạo endpoint mới; unsubscribe: hủy endpoint đã cấp | `"subscribe"` |
| `hub.topic` | string | không |  | dạng đăng ký: topic muốn theo dõi | `"7f2c4e1a-9b3d-4c5e-8a1f-2d6b9e0c3a47"` |
| `id` | string | không |  | dạng đổi ngữ cảnh: id thông điệp | `"m-0192f3a4"` |
| `timestamp` | string | không | định dạng date-time | dạng đổi ngữ cảnh: thời điểm phát sinh | `"2026-11-23T08:30:00Z"` |

```json
{
  "endpoint": "baa8974b-807b-490d-9e87-334f99a72b6d",
  "event": {
    "context": [
      {
        "key": "patient"
      }
    ],
    "context.versionId": "c8fa1e88-c131-4848-8467-9a0b2d1f5e33",
    "hub.event": "Patient-open",
    "hub.topic": "7f2c4e1a-9b3d-4c5e-8a1f-2d6b9e0c3a47"
  },
  "hub.channel.endpoint": "ws://gw.example.org:8007/ws/fhircast/baa8974b-807b-490d-9e87-334f99a72b6d",
  "hub.channel.type": "websocket",
  "hub.events": "Patient-open,Patient-close",
  "hub.mode": "subscribe",
  "hub.topic": "7f2c4e1a-9b3d-4c5e-8a1f-2d6b9e0c3a47",
  "id": "m-0192f3a4",
  "timestamp": "2026-11-23T08:30:00Z"
}
```

**Phản hồi**

**`202`** Accepted

Schema: `httpapi.FhircastHubResponse`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `event` | object `httpapi.FhircastEventBody` | không |  | dạng đổi ngữ cảnh: thông điệp đã phát (đã bổ sung context.versionId) |  |
| `event.event` | object `httpapi.FhircastEventPayload` | có |  | nội dung sự kiện |  |
| `event.event.context` | mảng `httpapi.FhircastContextItem` | có |  | ngữ cảnh của sự kiện; mảng (có thể rỗng) các phần tử {key, resource} |  |
| `event.event.context[].key` | string | có |  | vai trò của resource trong ngữ cảnh (patient, encounter, study, report...) | `"patient"` |
| `event.event.context[].resource` | object tự do | có |  | resource FHIR dạng JSON (luôn có resourceType); với sự kiện update là Bundle |  |
| `event.event.hub.event` | string | có | một trong: `Patient-open`, `Patient-close`, `ImagingStudy-open`, `ImagingStudy-close`, `Encounter-open`, `Encounter-close`, `DiagnosticReport-open`, `DiagnosticReport-close`, `DiagnosticReport-select`, `DiagnosticReport-update`, `userlogout`, `userhibernate`, `syncerror`, `heartbeat` | tên sự kiện | `"Patient-open"` |
| `event.event.hub.topic` | string | có |  | topic của phiên làm việc; không rỗng, tối đa 256 byte, không ký tự điều khiển | `"7f2c4e1a-9b3d-4c5e-8a1f-2d6b9e0c3a47"` |
| `event.event.context.versionId` | string | không |  | phiên bản ngữ cảnh: Hub cấp khi mở; sự kiện DiagnosticReport-update phải gửi đúng phiên bản hiện tại | `"c8fa1e88-c131-4848-8467-9a0b2d1f5e33"` |
| `event.id` | string | có |  | id thông điệp do bên gửi đặt, không rỗng | `"m-0192f3a4"` |
| `event.timestamp` | string | có | định dạng date-time | thời điểm phát sinh (RFC 3339), không rỗng | `"2026-11-23T08:30:00Z"` |
| `hub.channel.endpoint` | string | không |  | dạng đăng ký/hủy: URL WebSocket để nhận sự kiện (mỗi lần đăng ký sinh endpoint mới); với hủy là URL đã hủy | `"ws://gw.example.org:8007/ws/fhircast/baa8974b-807b-490d-9e87-334f99a72b6d"` |
| `success` | boolean | không |  | dạng đổi ngữ cảnh: true khi sự kiện đã được chấp nhận và phát cho người đăng ký | `true` |

```json
{
  "event": {
    "event": {
      "context": [
        {
          "key": "patient"
        }
      ],
      "context.versionId": "c8fa1e88-c131-4848-8467-9a0b2d1f5e33",
      "hub.event": "Patient-open",
      "hub.topic": "7f2c4e1a-9b3d-4c5e-8a1f-2d6b9e0c3a47"
    },
    "id": "m-0192f3a4",
    "timestamp": "2026-11-23T08:30:00Z"
  },
  "hub.channel.endpoint": "ws://gw.example.org:8007/ws/fhircast/baa8974b-807b-490d-9e87-334f99a72b6d",
  "success": true
}
```

**`400`** Thiếu hoặc sai trường (ví dụ Missing hub.mode, Invalid hub.channel.type, Invalid hub.events, Invalid endpoint, Missing event ID)

Schema: `httpapi.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`401`** Thiếu hoặc sai Bearer token (kèm WWW-Authenticate: Bearer realm=\"fhircast\")

Schema: `httpapi.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`403`** Token thiếu project hoặc scope fhircast/*

Schema: `httpapi.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`413`** Body vượt FHIRCAST_MAX_BODY_BYTES

Schema: `httpapi.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`500`** Lỗi hạ tầng (Redis); thông điệp chung, không lộ chi tiết

Schema: `httpapi.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

---

<a id="get-fhircast-stu2-well-known-fhircast-configuration"></a>
## `GET /fhircast/STU2/.well-known/fhircast-configuration`

**Cấu hình Hub FHIRcast**

Công khai, không cần token. Hub chỉ hỗ trợ kênh websocket (webhookSupport luôn false). STU3 thêm getCurrentSupport.

Các trường hợp phản hồi: [../cases/fhircast.md#get-fhircast-stu2-well-known-fhircast-configuration](../cases/fhircast.md#get-fhircast-stu2-well-known-fhircast-configuration)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | công khai (không cần header xác thực) |
| Content-Type yêu cầu | không có body |
| Content-Type phản hồi | `application/json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Accept` | không | nên khớp Content-Type phản hồi |

**Body yêu cầu:** không có.

**Phản hồi**

**`200`** OK

Schema: `httpapi.FhircastWellKnown`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `eventsSupported` | mảng string | có |  | 14 sự kiện Hub chấp nhận, đúng thứ tự của Medplum | `["Patient-open","Patient-close"]` |
| `fhircastVersion` | string | có | một trong: `STU2`, `STU3` | phiên bản của tiền tố đang gọi | `"STU3"` |
| `webhookSupport` | boolean | có |  | luôn false: Hub không bao giờ gọi ra ngoài | `false` |
| `websocketSupport` | boolean | có |  | luôn true: kênh duy nhất của Hub | `true` |
| `getCurrentSupport` | boolean | không |  | chỉ có ở STU3: Hub hỗ trợ GET ngữ cảnh hiện tại | `true` |

```json
{
  "eventsSupported": [
    "Patient-open",
    "Patient-close"
  ],
  "fhircastVersion": "STU3",
  "getCurrentSupport": true,
  "webhookSupport": false,
  "websocketSupport": true
}
```

---

<a id="get-fhircast-stu2-topic"></a>
## `GET /fhircast/STU2/{topic}`

**Ngữ cảnh hiện tại (STU2)**

Mảng các phần tử context của topic trong project của token; chưa có ngữ cảnh thì là mảng rỗng (mở rộng không chuẩn cho Nuance PowerCast Hub). Project khác không thấy.

Các trường hợp phản hồi: [../cases/fhircast.md#get-fhircast-stu2-topic](../cases/fhircast.md#get-fhircast-stu2-topic)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | không có body |
| Content-Type phản hồi | `application/json`, `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Tham số**

| Tên | Vị trí | Kiểu | Bắt buộc | Mô tả |
|---|---|---|---|---|
| `topic` | đường dẫn | string | có | Topic (đã mã hóa URL) |

**Phản hồi**

**`200`** OK

Mảng các phần tử `httpapi.FhircastContextItem`.

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `key` | string | có |  | vai trò của resource trong ngữ cảnh (patient, encounter, study, report...) | `"patient"` |
| `resource` | object tự do | có |  | resource FHIR dạng JSON (luôn có resourceType); với sự kiện update là Bundle |  |

```json
{
  "key": "patient"
}
```

**`401`** Thiếu hoặc sai Bearer token

Schema: `httpapi.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`403`** Token thiếu project hoặc scope fhircast/*

Schema: `httpapi.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`500`** Lỗi hạ tầng (Redis)

Schema: `httpapi.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

---

<a id="post-fhircast-stu2-topic"></a>
## `POST /fhircast/STU2/{topic}`

**Đổi ngữ cảnh của một topic**

Gửi thông điệp đổi ngữ cảnh (sự kiện open, close, select hoặc update) cho topic ở đường dẫn. event["hub.topic"] phải trùng topic đường dẫn, nếu không 400 "Topic mismatch" (đặt FHIRCAST_ALLOW_TOPIC_MISMATCH=true để như Medplum). Sự kiện update phải gửi đúng context.versionId hiện tại; ngữ cảnh đổi trong lúc cập nhật là 409. Ngữ cảnh vượt FHIRCAST_MAX_CONTEXT_BYTES hoặc quá nhiều ngữ cảnh đã lưu là 400. Hub phát sự kiện cho mọi người đăng ký của topic trong cùng project.

Các trường hợp phản hồi: [../cases/fhircast.md#post-fhircast-stu2-topic](../cases/fhircast.md#post-fhircast-stu2-topic)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | `application/json` |
| Content-Type phản hồi | `application/json`, `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Content-Type` | có | `application/json` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Tham số**

| Tên | Vị trí | Kiểu | Bắt buộc | Mô tả |
|---|---|---|---|---|
| `topic` | đường dẫn | string | có | Topic (đã mã hóa URL, chứa được dấu / hoặc :) |

**Body yêu cầu** (Thông điệp đổi ngữ cảnh)

Schema: `httpapi.FhircastEventBody`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `event` | object `httpapi.FhircastEventPayload` | có |  | nội dung sự kiện |  |
| `event.context` | mảng `httpapi.FhircastContextItem` | có |  | ngữ cảnh của sự kiện; mảng (có thể rỗng) các phần tử {key, resource} |  |
| `event.context[].key` | string | có |  | vai trò của resource trong ngữ cảnh (patient, encounter, study, report...) | `"patient"` |
| `event.context[].resource` | object tự do | có |  | resource FHIR dạng JSON (luôn có resourceType); với sự kiện update là Bundle |  |
| `event.hub.event` | string | có | một trong: `Patient-open`, `Patient-close`, `ImagingStudy-open`, `ImagingStudy-close`, `Encounter-open`, `Encounter-close`, `DiagnosticReport-open`, `DiagnosticReport-close`, `DiagnosticReport-select`, `DiagnosticReport-update`, `userlogout`, `userhibernate`, `syncerror`, `heartbeat` | tên sự kiện | `"Patient-open"` |
| `event.hub.topic` | string | có |  | topic của phiên làm việc; không rỗng, tối đa 256 byte, không ký tự điều khiển | `"7f2c4e1a-9b3d-4c5e-8a1f-2d6b9e0c3a47"` |
| `event.context.versionId` | string | không |  | phiên bản ngữ cảnh: Hub cấp khi mở; sự kiện DiagnosticReport-update phải gửi đúng phiên bản hiện tại | `"c8fa1e88-c131-4848-8467-9a0b2d1f5e33"` |
| `id` | string | có |  | id thông điệp do bên gửi đặt, không rỗng | `"m-0192f3a4"` |
| `timestamp` | string | có | định dạng date-time | thời điểm phát sinh (RFC 3339), không rỗng | `"2026-11-23T08:30:00Z"` |

```json
{
  "event": {
    "context": [
      {
        "key": "patient"
      }
    ],
    "context.versionId": "c8fa1e88-c131-4848-8467-9a0b2d1f5e33",
    "hub.event": "Patient-open",
    "hub.topic": "7f2c4e1a-9b3d-4c5e-8a1f-2d6b9e0c3a47"
  },
  "id": "m-0192f3a4",
  "timestamp": "2026-11-23T08:30:00Z"
}
```

**Phản hồi**

**`202`** Accepted

Schema: `httpapi.FhircastChangeResponse`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `event` | object `httpapi.FhircastEventBody` | có |  | thông điệp đã phát (đã bổ sung context.versionId) |  |
| `event.event` | object `httpapi.FhircastEventPayload` | có |  | nội dung sự kiện |  |
| `event.event.context` | mảng `httpapi.FhircastContextItem` | có |  | ngữ cảnh của sự kiện; mảng (có thể rỗng) các phần tử {key, resource} |  |
| `event.event.context[].key` | string | có |  | vai trò của resource trong ngữ cảnh (patient, encounter, study, report...) | `"patient"` |
| `event.event.context[].resource` | object tự do | có |  | resource FHIR dạng JSON (luôn có resourceType); với sự kiện update là Bundle |  |
| `event.event.hub.event` | string | có | một trong: `Patient-open`, `Patient-close`, `ImagingStudy-open`, `ImagingStudy-close`, `Encounter-open`, `Encounter-close`, `DiagnosticReport-open`, `DiagnosticReport-close`, `DiagnosticReport-select`, `DiagnosticReport-update`, `userlogout`, `userhibernate`, `syncerror`, `heartbeat` | tên sự kiện | `"Patient-open"` |
| `event.event.hub.topic` | string | có |  | topic của phiên làm việc; không rỗng, tối đa 256 byte, không ký tự điều khiển | `"7f2c4e1a-9b3d-4c5e-8a1f-2d6b9e0c3a47"` |
| `event.event.context.versionId` | string | không |  | phiên bản ngữ cảnh: Hub cấp khi mở; sự kiện DiagnosticReport-update phải gửi đúng phiên bản hiện tại | `"c8fa1e88-c131-4848-8467-9a0b2d1f5e33"` |
| `event.id` | string | có |  | id thông điệp do bên gửi đặt, không rỗng | `"m-0192f3a4"` |
| `event.timestamp` | string | có | định dạng date-time | thời điểm phát sinh (RFC 3339), không rỗng | `"2026-11-23T08:30:00Z"` |
| `success` | boolean | có |  | true khi sự kiện đã được chấp nhận và phát cho người đăng ký | `true` |

```json
{
  "event": {
    "event": {
      "context": [
        {
          "key": "patient"
        }
      ],
      "context.versionId": "c8fa1e88-c131-4848-8467-9a0b2d1f5e33",
      "hub.event": "Patient-open",
      "hub.topic": "7f2c4e1a-9b3d-4c5e-8a1f-2d6b9e0c3a47"
    },
    "id": "m-0192f3a4",
    "timestamp": "2026-11-23T08:30:00Z"
  },
  "success": true
}
```

**`400`** Sự kiện sai (Missing event ID, Invalid event[\"hub.event\"], Topic mismatch, No DiagnosticReport currently open for this topic...)

Schema: `httpapi.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`401`** Thiếu hoặc sai Bearer token

Schema: `httpapi.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`403`** Token thiếu project hoặc scope fhircast/*

Schema: `httpapi.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`409`** Ngữ cảnh đổi trong lúc cập nhật, không còn ở phiên bản đã gửi

Schema: `httpapi.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`413`** Body vượt FHIRCAST_MAX_BODY_BYTES

Schema: `httpapi.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`500`** Lỗi hạ tầng (Redis)

Schema: `httpapi.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

---

<a id="post-fhircast-stu3"></a>
## `POST /fhircast/STU3`

**Đăng ký, hủy đăng ký hoặc đổi ngữ cảnh**

Một route, hai dạng theo nội dung body. (1) Có trường id hoặc event: đổi ngữ cảnh như POST {v}/{topic} (không kiểm khớp topic đường dẫn); phản hồi 202 có success và event. (2) Không có: đăng ký (hub.mode=subscribe) hoặc hủy (hub.mode=unsubscribe) kênh websocket; phản hồi 202 có hub.channel.endpoint. Đăng ký cần hub.channel.type=websocket, hub.mode, hub.topic, hub.events (14 sự kiện được công bố); hủy chỉ cho đúng người, project và topic đã đăng ký, sai bất kỳ điều nào cũng là 400 "Invalid endpoint". Nhận application/json hoặc application/x-www-form-urlencoded (dạng đăng ký). Body vượt FHIRCAST_MAX_BODY_BYTES là 413.

Các trường hợp phản hồi: [../cases/fhircast.md#post-fhircast-stu3](../cases/fhircast.md#post-fhircast-stu3)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | `application/json`, `application/x-www-form-urlencoded` |
| Content-Type phản hồi | `application/json`, `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Content-Type` | có | `application/json`, `application/x-www-form-urlencoded` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Body yêu cầu** (Yêu cầu đăng ký/hủy hoặc thông điệp đổi ngữ cảnh)

Schema: `httpapi.FhircastHubRequest`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `endpoint` | string | không |  | dạng hủy, tên trường cũ của client trước @medplum/core 5.1.29; chỉ dùng khi thiếu hub.channel.endpoint | `"baa8974b-807b-490d-9e87-334f99a72b6d"` |
| `event` | object `httpapi.FhircastEventPayload` | không |  | dạng đổi ngữ cảnh: nội dung sự kiện |  |
| `event.context` | mảng `httpapi.FhircastContextItem` | có |  | ngữ cảnh của sự kiện; mảng (có thể rỗng) các phần tử {key, resource} |  |
| `event.context[].key` | string | có |  | vai trò của resource trong ngữ cảnh (patient, encounter, study, report...) | `"patient"` |
| `event.context[].resource` | object tự do | có |  | resource FHIR dạng JSON (luôn có resourceType); với sự kiện update là Bundle |  |
| `event.hub.event` | string | có | một trong: `Patient-open`, `Patient-close`, `ImagingStudy-open`, `ImagingStudy-close`, `Encounter-open`, `Encounter-close`, `DiagnosticReport-open`, `DiagnosticReport-close`, `DiagnosticReport-select`, `DiagnosticReport-update`, `userlogout`, `userhibernate`, `syncerror`, `heartbeat` | tên sự kiện | `"Patient-open"` |
| `event.hub.topic` | string | có |  | topic của phiên làm việc; không rỗng, tối đa 256 byte, không ký tự điều khiển | `"7f2c4e1a-9b3d-4c5e-8a1f-2d6b9e0c3a47"` |
| `event.context.versionId` | string | không |  | phiên bản ngữ cảnh: Hub cấp khi mở; sự kiện DiagnosticReport-update phải gửi đúng phiên bản hiện tại | `"c8fa1e88-c131-4848-8467-9a0b2d1f5e33"` |
| `hub.channel.endpoint` | string | không |  | dạng hủy: URL hoặc endpoint đã được cấp lúc đăng ký | `"ws://gw.example.org:8007/ws/fhircast/baa8974b-807b-490d-9e87-334f99a72b6d"` |
| `hub.channel.type` | string | không | một trong: `websocket` | dạng đăng ký: kênh, chỉ "websocket" (webhook, rest-hook, email bị từ chối) | `"websocket"` |
| `hub.events` | string | không |  | dạng đăng ký, bắt buộc khi subscribe: tên sự kiện cách nhau bằng dấu phẩy, mỗi tên phải thuộc 14 sự kiện được công bố | `"Patient-open,Patient-close"` |
| `hub.mode` | string | không | một trong: `subscribe`, `unsubscribe` | subscribe: tạo endpoint mới; unsubscribe: hủy endpoint đã cấp | `"subscribe"` |
| `hub.topic` | string | không |  | dạng đăng ký: topic muốn theo dõi | `"7f2c4e1a-9b3d-4c5e-8a1f-2d6b9e0c3a47"` |
| `id` | string | không |  | dạng đổi ngữ cảnh: id thông điệp | `"m-0192f3a4"` |
| `timestamp` | string | không | định dạng date-time | dạng đổi ngữ cảnh: thời điểm phát sinh | `"2026-11-23T08:30:00Z"` |

```json
{
  "endpoint": "baa8974b-807b-490d-9e87-334f99a72b6d",
  "event": {
    "context": [
      {
        "key": "patient"
      }
    ],
    "context.versionId": "c8fa1e88-c131-4848-8467-9a0b2d1f5e33",
    "hub.event": "Patient-open",
    "hub.topic": "7f2c4e1a-9b3d-4c5e-8a1f-2d6b9e0c3a47"
  },
  "hub.channel.endpoint": "ws://gw.example.org:8007/ws/fhircast/baa8974b-807b-490d-9e87-334f99a72b6d",
  "hub.channel.type": "websocket",
  "hub.events": "Patient-open,Patient-close",
  "hub.mode": "subscribe",
  "hub.topic": "7f2c4e1a-9b3d-4c5e-8a1f-2d6b9e0c3a47",
  "id": "m-0192f3a4",
  "timestamp": "2026-11-23T08:30:00Z"
}
```

**Phản hồi**

**`202`** Accepted

Schema: `httpapi.FhircastHubResponse`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `event` | object `httpapi.FhircastEventBody` | không |  | dạng đổi ngữ cảnh: thông điệp đã phát (đã bổ sung context.versionId) |  |
| `event.event` | object `httpapi.FhircastEventPayload` | có |  | nội dung sự kiện |  |
| `event.event.context` | mảng `httpapi.FhircastContextItem` | có |  | ngữ cảnh của sự kiện; mảng (có thể rỗng) các phần tử {key, resource} |  |
| `event.event.context[].key` | string | có |  | vai trò của resource trong ngữ cảnh (patient, encounter, study, report...) | `"patient"` |
| `event.event.context[].resource` | object tự do | có |  | resource FHIR dạng JSON (luôn có resourceType); với sự kiện update là Bundle |  |
| `event.event.hub.event` | string | có | một trong: `Patient-open`, `Patient-close`, `ImagingStudy-open`, `ImagingStudy-close`, `Encounter-open`, `Encounter-close`, `DiagnosticReport-open`, `DiagnosticReport-close`, `DiagnosticReport-select`, `DiagnosticReport-update`, `userlogout`, `userhibernate`, `syncerror`, `heartbeat` | tên sự kiện | `"Patient-open"` |
| `event.event.hub.topic` | string | có |  | topic của phiên làm việc; không rỗng, tối đa 256 byte, không ký tự điều khiển | `"7f2c4e1a-9b3d-4c5e-8a1f-2d6b9e0c3a47"` |
| `event.event.context.versionId` | string | không |  | phiên bản ngữ cảnh: Hub cấp khi mở; sự kiện DiagnosticReport-update phải gửi đúng phiên bản hiện tại | `"c8fa1e88-c131-4848-8467-9a0b2d1f5e33"` |
| `event.id` | string | có |  | id thông điệp do bên gửi đặt, không rỗng | `"m-0192f3a4"` |
| `event.timestamp` | string | có | định dạng date-time | thời điểm phát sinh (RFC 3339), không rỗng | `"2026-11-23T08:30:00Z"` |
| `hub.channel.endpoint` | string | không |  | dạng đăng ký/hủy: URL WebSocket để nhận sự kiện (mỗi lần đăng ký sinh endpoint mới); với hủy là URL đã hủy | `"ws://gw.example.org:8007/ws/fhircast/baa8974b-807b-490d-9e87-334f99a72b6d"` |
| `success` | boolean | không |  | dạng đổi ngữ cảnh: true khi sự kiện đã được chấp nhận và phát cho người đăng ký | `true` |

```json
{
  "event": {
    "event": {
      "context": [
        {
          "key": "patient"
        }
      ],
      "context.versionId": "c8fa1e88-c131-4848-8467-9a0b2d1f5e33",
      "hub.event": "Patient-open",
      "hub.topic": "7f2c4e1a-9b3d-4c5e-8a1f-2d6b9e0c3a47"
    },
    "id": "m-0192f3a4",
    "timestamp": "2026-11-23T08:30:00Z"
  },
  "hub.channel.endpoint": "ws://gw.example.org:8007/ws/fhircast/baa8974b-807b-490d-9e87-334f99a72b6d",
  "success": true
}
```

**`400`** Thiếu hoặc sai trường (ví dụ Missing hub.mode, Invalid hub.channel.type, Invalid hub.events, Invalid endpoint, Missing event ID)

Schema: `httpapi.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`401`** Thiếu hoặc sai Bearer token (kèm WWW-Authenticate: Bearer realm=\"fhircast\")

Schema: `httpapi.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`403`** Token thiếu project hoặc scope fhircast/*

Schema: `httpapi.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`413`** Body vượt FHIRCAST_MAX_BODY_BYTES

Schema: `httpapi.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`500`** Lỗi hạ tầng (Redis); thông điệp chung, không lộ chi tiết

Schema: `httpapi.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

---

<a id="get-fhircast-stu3-well-known-fhircast-configuration"></a>
## `GET /fhircast/STU3/.well-known/fhircast-configuration`

**Cấu hình Hub FHIRcast**

Công khai, không cần token. Hub chỉ hỗ trợ kênh websocket (webhookSupport luôn false). STU3 thêm getCurrentSupport.

Các trường hợp phản hồi: [../cases/fhircast.md#get-fhircast-stu3-well-known-fhircast-configuration](../cases/fhircast.md#get-fhircast-stu3-well-known-fhircast-configuration)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | công khai (không cần header xác thực) |
| Content-Type yêu cầu | không có body |
| Content-Type phản hồi | `application/json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Accept` | không | nên khớp Content-Type phản hồi |

**Body yêu cầu:** không có.

**Phản hồi**

**`200`** OK

Schema: `httpapi.FhircastWellKnown`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `eventsSupported` | mảng string | có |  | 14 sự kiện Hub chấp nhận, đúng thứ tự của Medplum | `["Patient-open","Patient-close"]` |
| `fhircastVersion` | string | có | một trong: `STU2`, `STU3` | phiên bản của tiền tố đang gọi | `"STU3"` |
| `webhookSupport` | boolean | có |  | luôn false: Hub không bao giờ gọi ra ngoài | `false` |
| `websocketSupport` | boolean | có |  | luôn true: kênh duy nhất của Hub | `true` |
| `getCurrentSupport` | boolean | không |  | chỉ có ở STU3: Hub hỗ trợ GET ngữ cảnh hiện tại | `true` |

```json
{
  "eventsSupported": [
    "Patient-open",
    "Patient-close"
  ],
  "fhircastVersion": "STU3",
  "getCurrentSupport": true,
  "webhookSupport": false,
  "websocketSupport": true
}
```

---

<a id="get-fhircast-stu3-topic"></a>
## `GET /fhircast/STU3/{topic}`

**Ngữ cảnh hiện tại (STU3)**

Ngữ cảnh đang mở của topic trong project của token. Project khác không thấy. Chưa có ngữ cảnh: context.type rỗng và context là mảng rỗng.

Các trường hợp phản hồi: [../cases/fhircast.md#get-fhircast-stu3-topic](../cases/fhircast.md#get-fhircast-stu3-topic)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | không có body |
| Content-Type phản hồi | `application/json`, `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Tham số**

| Tên | Vị trí | Kiểu | Bắt buộc | Mô tả |
|---|---|---|---|---|
| `topic` | đường dẫn | string | có | Topic (đã mã hóa URL) |

**Phản hồi**

**`200`** OK

Schema: `httpapi.FhircastContextSTU3`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `context` | mảng `httpapi.FhircastContextItem` | có |  | các phần tử của ngữ cảnh; mảng rỗng khi chưa có |  |
| `context[].key` | string | có |  | vai trò của resource trong ngữ cảnh (patient, encounter, study, report...) | `"patient"` |
| `context[].resource` | object tự do | có |  | resource FHIR dạng JSON (luôn có resourceType); với sự kiện update là Bundle |  |
| `context.type` | string | có |  | loại resource của ngữ cảnh đang mở; rỗng khi chưa có ngữ cảnh | `"Patient"` |
| `context.versionId` | string | không |  | phiên bản ngữ cảnh; vắng khi chưa có ngữ cảnh | `"c8fa1e88-c131-4848-8467-9a0b2d1f5e33"` |

```json
{
  "context": [
    {
      "key": "patient"
    }
  ],
  "context.type": "Patient",
  "context.versionId": "c8fa1e88-c131-4848-8467-9a0b2d1f5e33"
}
```

**`401`** Thiếu hoặc sai Bearer token

Schema: `httpapi.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`403`** Token thiếu project hoặc scope fhircast/*

Schema: `httpapi.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`500`** Lỗi hạ tầng (Redis)

Schema: `httpapi.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

---

<a id="post-fhircast-stu3-topic"></a>
## `POST /fhircast/STU3/{topic}`

**Đổi ngữ cảnh của một topic**

Gửi thông điệp đổi ngữ cảnh (sự kiện open, close, select hoặc update) cho topic ở đường dẫn. event["hub.topic"] phải trùng topic đường dẫn, nếu không 400 "Topic mismatch" (đặt FHIRCAST_ALLOW_TOPIC_MISMATCH=true để như Medplum). Sự kiện update phải gửi đúng context.versionId hiện tại; ngữ cảnh đổi trong lúc cập nhật là 409. Ngữ cảnh vượt FHIRCAST_MAX_CONTEXT_BYTES hoặc quá nhiều ngữ cảnh đã lưu là 400. Hub phát sự kiện cho mọi người đăng ký của topic trong cùng project.

Các trường hợp phản hồi: [../cases/fhircast.md#post-fhircast-stu3-topic](../cases/fhircast.md#post-fhircast-stu3-topic)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | `application/json` |
| Content-Type phản hồi | `application/json`, `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Content-Type` | có | `application/json` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Tham số**

| Tên | Vị trí | Kiểu | Bắt buộc | Mô tả |
|---|---|---|---|---|
| `topic` | đường dẫn | string | có | Topic (đã mã hóa URL, chứa được dấu / hoặc :) |

**Body yêu cầu** (Thông điệp đổi ngữ cảnh)

Schema: `httpapi.FhircastEventBody`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `event` | object `httpapi.FhircastEventPayload` | có |  | nội dung sự kiện |  |
| `event.context` | mảng `httpapi.FhircastContextItem` | có |  | ngữ cảnh của sự kiện; mảng (có thể rỗng) các phần tử {key, resource} |  |
| `event.context[].key` | string | có |  | vai trò của resource trong ngữ cảnh (patient, encounter, study, report...) | `"patient"` |
| `event.context[].resource` | object tự do | có |  | resource FHIR dạng JSON (luôn có resourceType); với sự kiện update là Bundle |  |
| `event.hub.event` | string | có | một trong: `Patient-open`, `Patient-close`, `ImagingStudy-open`, `ImagingStudy-close`, `Encounter-open`, `Encounter-close`, `DiagnosticReport-open`, `DiagnosticReport-close`, `DiagnosticReport-select`, `DiagnosticReport-update`, `userlogout`, `userhibernate`, `syncerror`, `heartbeat` | tên sự kiện | `"Patient-open"` |
| `event.hub.topic` | string | có |  | topic của phiên làm việc; không rỗng, tối đa 256 byte, không ký tự điều khiển | `"7f2c4e1a-9b3d-4c5e-8a1f-2d6b9e0c3a47"` |
| `event.context.versionId` | string | không |  | phiên bản ngữ cảnh: Hub cấp khi mở; sự kiện DiagnosticReport-update phải gửi đúng phiên bản hiện tại | `"c8fa1e88-c131-4848-8467-9a0b2d1f5e33"` |
| `id` | string | có |  | id thông điệp do bên gửi đặt, không rỗng | `"m-0192f3a4"` |
| `timestamp` | string | có | định dạng date-time | thời điểm phát sinh (RFC 3339), không rỗng | `"2026-11-23T08:30:00Z"` |

```json
{
  "event": {
    "context": [
      {
        "key": "patient"
      }
    ],
    "context.versionId": "c8fa1e88-c131-4848-8467-9a0b2d1f5e33",
    "hub.event": "Patient-open",
    "hub.topic": "7f2c4e1a-9b3d-4c5e-8a1f-2d6b9e0c3a47"
  },
  "id": "m-0192f3a4",
  "timestamp": "2026-11-23T08:30:00Z"
}
```

**Phản hồi**

**`202`** Accepted

Schema: `httpapi.FhircastChangeResponse`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `event` | object `httpapi.FhircastEventBody` | có |  | thông điệp đã phát (đã bổ sung context.versionId) |  |
| `event.event` | object `httpapi.FhircastEventPayload` | có |  | nội dung sự kiện |  |
| `event.event.context` | mảng `httpapi.FhircastContextItem` | có |  | ngữ cảnh của sự kiện; mảng (có thể rỗng) các phần tử {key, resource} |  |
| `event.event.context[].key` | string | có |  | vai trò của resource trong ngữ cảnh (patient, encounter, study, report...) | `"patient"` |
| `event.event.context[].resource` | object tự do | có |  | resource FHIR dạng JSON (luôn có resourceType); với sự kiện update là Bundle |  |
| `event.event.hub.event` | string | có | một trong: `Patient-open`, `Patient-close`, `ImagingStudy-open`, `ImagingStudy-close`, `Encounter-open`, `Encounter-close`, `DiagnosticReport-open`, `DiagnosticReport-close`, `DiagnosticReport-select`, `DiagnosticReport-update`, `userlogout`, `userhibernate`, `syncerror`, `heartbeat` | tên sự kiện | `"Patient-open"` |
| `event.event.hub.topic` | string | có |  | topic của phiên làm việc; không rỗng, tối đa 256 byte, không ký tự điều khiển | `"7f2c4e1a-9b3d-4c5e-8a1f-2d6b9e0c3a47"` |
| `event.event.context.versionId` | string | không |  | phiên bản ngữ cảnh: Hub cấp khi mở; sự kiện DiagnosticReport-update phải gửi đúng phiên bản hiện tại | `"c8fa1e88-c131-4848-8467-9a0b2d1f5e33"` |
| `event.id` | string | có |  | id thông điệp do bên gửi đặt, không rỗng | `"m-0192f3a4"` |
| `event.timestamp` | string | có | định dạng date-time | thời điểm phát sinh (RFC 3339), không rỗng | `"2026-11-23T08:30:00Z"` |
| `success` | boolean | có |  | true khi sự kiện đã được chấp nhận và phát cho người đăng ký | `true` |

```json
{
  "event": {
    "event": {
      "context": [
        {
          "key": "patient"
        }
      ],
      "context.versionId": "c8fa1e88-c131-4848-8467-9a0b2d1f5e33",
      "hub.event": "Patient-open",
      "hub.topic": "7f2c4e1a-9b3d-4c5e-8a1f-2d6b9e0c3a47"
    },
    "id": "m-0192f3a4",
    "timestamp": "2026-11-23T08:30:00Z"
  },
  "success": true
}
```

**`400`** Sự kiện sai (Missing event ID, Invalid event[\"hub.event\"], Topic mismatch, No DiagnosticReport currently open for this topic...)

Schema: `httpapi.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`401`** Thiếu hoặc sai Bearer token

Schema: `httpapi.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`403`** Token thiếu project hoặc scope fhircast/*

Schema: `httpapi.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`409`** Ngữ cảnh đổi trong lúc cập nhật, không còn ở phiên bản đã gửi

Schema: `httpapi.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`413`** Body vượt FHIRCAST_MAX_BODY_BYTES

Schema: `httpapi.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`500`** Lỗi hạ tầng (Redis)

Schema: `httpapi.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

---

<a id="post-hub"></a>
## `POST /hub`

**Đăng ký, hủy đăng ký hoặc đổi ngữ cảnh**

Một route, hai dạng theo nội dung body. (1) Có trường id hoặc event: đổi ngữ cảnh như POST {v}/{topic} (không kiểm khớp topic đường dẫn); phản hồi 202 có success và event. (2) Không có: đăng ký (hub.mode=subscribe) hoặc hủy (hub.mode=unsubscribe) kênh websocket; phản hồi 202 có hub.channel.endpoint. Đăng ký cần hub.channel.type=websocket, hub.mode, hub.topic, hub.events (14 sự kiện được công bố); hủy chỉ cho đúng người, project và topic đã đăng ký, sai bất kỳ điều nào cũng là 400 "Invalid endpoint". Nhận application/json hoặc application/x-www-form-urlencoded (dạng đăng ký). Body vượt FHIRCAST_MAX_BODY_BYTES là 413.

Các trường hợp phản hồi: [../cases/fhircast.md#post-hub](../cases/fhircast.md#post-hub)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | `application/json`, `application/x-www-form-urlencoded` |
| Content-Type phản hồi | `application/json`, `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Content-Type` | có | `application/json`, `application/x-www-form-urlencoded` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Body yêu cầu** (Yêu cầu đăng ký/hủy hoặc thông điệp đổi ngữ cảnh)

Schema: `httpapi.FhircastHubRequest`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `endpoint` | string | không |  | dạng hủy, tên trường cũ của client trước @medplum/core 5.1.29; chỉ dùng khi thiếu hub.channel.endpoint | `"baa8974b-807b-490d-9e87-334f99a72b6d"` |
| `event` | object `httpapi.FhircastEventPayload` | không |  | dạng đổi ngữ cảnh: nội dung sự kiện |  |
| `event.context` | mảng `httpapi.FhircastContextItem` | có |  | ngữ cảnh của sự kiện; mảng (có thể rỗng) các phần tử {key, resource} |  |
| `event.context[].key` | string | có |  | vai trò của resource trong ngữ cảnh (patient, encounter, study, report...) | `"patient"` |
| `event.context[].resource` | object tự do | có |  | resource FHIR dạng JSON (luôn có resourceType); với sự kiện update là Bundle |  |
| `event.hub.event` | string | có | một trong: `Patient-open`, `Patient-close`, `ImagingStudy-open`, `ImagingStudy-close`, `Encounter-open`, `Encounter-close`, `DiagnosticReport-open`, `DiagnosticReport-close`, `DiagnosticReport-select`, `DiagnosticReport-update`, `userlogout`, `userhibernate`, `syncerror`, `heartbeat` | tên sự kiện | `"Patient-open"` |
| `event.hub.topic` | string | có |  | topic của phiên làm việc; không rỗng, tối đa 256 byte, không ký tự điều khiển | `"7f2c4e1a-9b3d-4c5e-8a1f-2d6b9e0c3a47"` |
| `event.context.versionId` | string | không |  | phiên bản ngữ cảnh: Hub cấp khi mở; sự kiện DiagnosticReport-update phải gửi đúng phiên bản hiện tại | `"c8fa1e88-c131-4848-8467-9a0b2d1f5e33"` |
| `hub.channel.endpoint` | string | không |  | dạng hủy: URL hoặc endpoint đã được cấp lúc đăng ký | `"ws://gw.example.org:8007/ws/fhircast/baa8974b-807b-490d-9e87-334f99a72b6d"` |
| `hub.channel.type` | string | không | một trong: `websocket` | dạng đăng ký: kênh, chỉ "websocket" (webhook, rest-hook, email bị từ chối) | `"websocket"` |
| `hub.events` | string | không |  | dạng đăng ký, bắt buộc khi subscribe: tên sự kiện cách nhau bằng dấu phẩy, mỗi tên phải thuộc 14 sự kiện được công bố | `"Patient-open,Patient-close"` |
| `hub.mode` | string | không | một trong: `subscribe`, `unsubscribe` | subscribe: tạo endpoint mới; unsubscribe: hủy endpoint đã cấp | `"subscribe"` |
| `hub.topic` | string | không |  | dạng đăng ký: topic muốn theo dõi | `"7f2c4e1a-9b3d-4c5e-8a1f-2d6b9e0c3a47"` |
| `id` | string | không |  | dạng đổi ngữ cảnh: id thông điệp | `"m-0192f3a4"` |
| `timestamp` | string | không | định dạng date-time | dạng đổi ngữ cảnh: thời điểm phát sinh | `"2026-11-23T08:30:00Z"` |

```json
{
  "endpoint": "baa8974b-807b-490d-9e87-334f99a72b6d",
  "event": {
    "context": [
      {
        "key": "patient"
      }
    ],
    "context.versionId": "c8fa1e88-c131-4848-8467-9a0b2d1f5e33",
    "hub.event": "Patient-open",
    "hub.topic": "7f2c4e1a-9b3d-4c5e-8a1f-2d6b9e0c3a47"
  },
  "hub.channel.endpoint": "ws://gw.example.org:8007/ws/fhircast/baa8974b-807b-490d-9e87-334f99a72b6d",
  "hub.channel.type": "websocket",
  "hub.events": "Patient-open,Patient-close",
  "hub.mode": "subscribe",
  "hub.topic": "7f2c4e1a-9b3d-4c5e-8a1f-2d6b9e0c3a47",
  "id": "m-0192f3a4",
  "timestamp": "2026-11-23T08:30:00Z"
}
```

**Phản hồi**

**`202`** Accepted

Schema: `httpapi.FhircastHubResponse`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `event` | object `httpapi.FhircastEventBody` | không |  | dạng đổi ngữ cảnh: thông điệp đã phát (đã bổ sung context.versionId) |  |
| `event.event` | object `httpapi.FhircastEventPayload` | có |  | nội dung sự kiện |  |
| `event.event.context` | mảng `httpapi.FhircastContextItem` | có |  | ngữ cảnh của sự kiện; mảng (có thể rỗng) các phần tử {key, resource} |  |
| `event.event.context[].key` | string | có |  | vai trò của resource trong ngữ cảnh (patient, encounter, study, report...) | `"patient"` |
| `event.event.context[].resource` | object tự do | có |  | resource FHIR dạng JSON (luôn có resourceType); với sự kiện update là Bundle |  |
| `event.event.hub.event` | string | có | một trong: `Patient-open`, `Patient-close`, `ImagingStudy-open`, `ImagingStudy-close`, `Encounter-open`, `Encounter-close`, `DiagnosticReport-open`, `DiagnosticReport-close`, `DiagnosticReport-select`, `DiagnosticReport-update`, `userlogout`, `userhibernate`, `syncerror`, `heartbeat` | tên sự kiện | `"Patient-open"` |
| `event.event.hub.topic` | string | có |  | topic của phiên làm việc; không rỗng, tối đa 256 byte, không ký tự điều khiển | `"7f2c4e1a-9b3d-4c5e-8a1f-2d6b9e0c3a47"` |
| `event.event.context.versionId` | string | không |  | phiên bản ngữ cảnh: Hub cấp khi mở; sự kiện DiagnosticReport-update phải gửi đúng phiên bản hiện tại | `"c8fa1e88-c131-4848-8467-9a0b2d1f5e33"` |
| `event.id` | string | có |  | id thông điệp do bên gửi đặt, không rỗng | `"m-0192f3a4"` |
| `event.timestamp` | string | có | định dạng date-time | thời điểm phát sinh (RFC 3339), không rỗng | `"2026-11-23T08:30:00Z"` |
| `hub.channel.endpoint` | string | không |  | dạng đăng ký/hủy: URL WebSocket để nhận sự kiện (mỗi lần đăng ký sinh endpoint mới); với hủy là URL đã hủy | `"ws://gw.example.org:8007/ws/fhircast/baa8974b-807b-490d-9e87-334f99a72b6d"` |
| `success` | boolean | không |  | dạng đổi ngữ cảnh: true khi sự kiện đã được chấp nhận và phát cho người đăng ký | `true` |

```json
{
  "event": {
    "event": {
      "context": [
        {
          "key": "patient"
        }
      ],
      "context.versionId": "c8fa1e88-c131-4848-8467-9a0b2d1f5e33",
      "hub.event": "Patient-open",
      "hub.topic": "7f2c4e1a-9b3d-4c5e-8a1f-2d6b9e0c3a47"
    },
    "id": "m-0192f3a4",
    "timestamp": "2026-11-23T08:30:00Z"
  },
  "hub.channel.endpoint": "ws://gw.example.org:8007/ws/fhircast/baa8974b-807b-490d-9e87-334f99a72b6d",
  "success": true
}
```

**`400`** Thiếu hoặc sai trường (ví dụ Missing hub.mode, Invalid hub.channel.type, Invalid hub.events, Invalid endpoint, Missing event ID)

Schema: `httpapi.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`401`** Thiếu hoặc sai Bearer token (kèm WWW-Authenticate: Bearer realm=\"fhircast\")

Schema: `httpapi.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`403`** Token thiếu project hoặc scope fhircast/*

Schema: `httpapi.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`413`** Body vượt FHIRCAST_MAX_BODY_BYTES

Schema: `httpapi.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`500`** Lỗi hạ tầng (Redis); thông điệp chung, không lộ chi tiết

Schema: `httpapi.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

---

<a id="get-hub-well-known-fhircast-configuration"></a>
## `GET /hub/.well-known/fhircast-configuration`

**Cấu hình Hub FHIRcast**

Công khai, không cần token. Hub chỉ hỗ trợ kênh websocket (webhookSupport luôn false). STU3 thêm getCurrentSupport.

Các trường hợp phản hồi: [../cases/fhircast.md#get-hub-well-known-fhircast-configuration](../cases/fhircast.md#get-hub-well-known-fhircast-configuration)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | công khai (không cần header xác thực) |
| Content-Type yêu cầu | không có body |
| Content-Type phản hồi | `application/json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Accept` | không | nên khớp Content-Type phản hồi |

**Body yêu cầu:** không có.

**Phản hồi**

**`200`** OK

Schema: `httpapi.FhircastWellKnown`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `eventsSupported` | mảng string | có |  | 14 sự kiện Hub chấp nhận, đúng thứ tự của Medplum | `["Patient-open","Patient-close"]` |
| `fhircastVersion` | string | có | một trong: `STU2`, `STU3` | phiên bản của tiền tố đang gọi | `"STU3"` |
| `webhookSupport` | boolean | có |  | luôn false: Hub không bao giờ gọi ra ngoài | `false` |
| `websocketSupport` | boolean | có |  | luôn true: kênh duy nhất của Hub | `true` |
| `getCurrentSupport` | boolean | không |  | chỉ có ở STU3: Hub hỗ trợ GET ngữ cảnh hiện tại | `true` |

```json
{
  "eventsSupported": [
    "Patient-open",
    "Patient-close"
  ],
  "fhircastVersion": "STU3",
  "getCurrentSupport": true,
  "webhookSupport": false,
  "websocketSupport": true
}
```

---

<a id="get-hub-topic"></a>
## `GET /hub/{topic}`

**Ngữ cảnh hiện tại (STU3)**

Ngữ cảnh đang mở của topic trong project của token. Project khác không thấy. Chưa có ngữ cảnh: context.type rỗng và context là mảng rỗng.

Các trường hợp phản hồi: [../cases/fhircast.md#get-hub-topic](../cases/fhircast.md#get-hub-topic)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | không có body |
| Content-Type phản hồi | `application/json`, `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Tham số**

| Tên | Vị trí | Kiểu | Bắt buộc | Mô tả |
|---|---|---|---|---|
| `topic` | đường dẫn | string | có | Topic (đã mã hóa URL) |

**Phản hồi**

**`200`** OK

Schema: `httpapi.FhircastContextSTU3`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `context` | mảng `httpapi.FhircastContextItem` | có |  | các phần tử của ngữ cảnh; mảng rỗng khi chưa có |  |
| `context[].key` | string | có |  | vai trò của resource trong ngữ cảnh (patient, encounter, study, report...) | `"patient"` |
| `context[].resource` | object tự do | có |  | resource FHIR dạng JSON (luôn có resourceType); với sự kiện update là Bundle |  |
| `context.type` | string | có |  | loại resource của ngữ cảnh đang mở; rỗng khi chưa có ngữ cảnh | `"Patient"` |
| `context.versionId` | string | không |  | phiên bản ngữ cảnh; vắng khi chưa có ngữ cảnh | `"c8fa1e88-c131-4848-8467-9a0b2d1f5e33"` |

```json
{
  "context": [
    {
      "key": "patient"
    }
  ],
  "context.type": "Patient",
  "context.versionId": "c8fa1e88-c131-4848-8467-9a0b2d1f5e33"
}
```

**`401`** Thiếu hoặc sai Bearer token

Schema: `httpapi.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`403`** Token thiếu project hoặc scope fhircast/*

Schema: `httpapi.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`500`** Lỗi hạ tầng (Redis)

Schema: `httpapi.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

---

<a id="post-hub-topic"></a>
## `POST /hub/{topic}`

**Đổi ngữ cảnh của một topic**

Gửi thông điệp đổi ngữ cảnh (sự kiện open, close, select hoặc update) cho topic ở đường dẫn. event["hub.topic"] phải trùng topic đường dẫn, nếu không 400 "Topic mismatch" (đặt FHIRCAST_ALLOW_TOPIC_MISMATCH=true để như Medplum). Sự kiện update phải gửi đúng context.versionId hiện tại; ngữ cảnh đổi trong lúc cập nhật là 409. Ngữ cảnh vượt FHIRCAST_MAX_CONTEXT_BYTES hoặc quá nhiều ngữ cảnh đã lưu là 400. Hub phát sự kiện cho mọi người đăng ký của topic trong cùng project.

Các trường hợp phản hồi: [../cases/fhircast.md#post-hub-topic](../cases/fhircast.md#post-hub-topic)

| Thuộc tính | Giá trị |
|---|---|
| Xác thực | bắt buộc: header `Authorization: Bearer <access_token>` (BearerAuth) |
| Content-Type yêu cầu | `application/json` |
| Content-Type phản hồi | `application/json`, `application/fhir+json` |

**Header yêu cầu**

| Header | Bắt buộc | Mô tả |
|---|---|---|
| `Authorization` | có | `Bearer <access_token>` |
| `Content-Type` | có | `application/json` |
| `Accept` | không | nên khớp Content-Type phản hồi |

**Tham số**

| Tên | Vị trí | Kiểu | Bắt buộc | Mô tả |
|---|---|---|---|---|
| `topic` | đường dẫn | string | có | Topic (đã mã hóa URL, chứa được dấu / hoặc :) |

**Body yêu cầu** (Thông điệp đổi ngữ cảnh)

Schema: `httpapi.FhircastEventBody`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `event` | object `httpapi.FhircastEventPayload` | có |  | nội dung sự kiện |  |
| `event.context` | mảng `httpapi.FhircastContextItem` | có |  | ngữ cảnh của sự kiện; mảng (có thể rỗng) các phần tử {key, resource} |  |
| `event.context[].key` | string | có |  | vai trò của resource trong ngữ cảnh (patient, encounter, study, report...) | `"patient"` |
| `event.context[].resource` | object tự do | có |  | resource FHIR dạng JSON (luôn có resourceType); với sự kiện update là Bundle |  |
| `event.hub.event` | string | có | một trong: `Patient-open`, `Patient-close`, `ImagingStudy-open`, `ImagingStudy-close`, `Encounter-open`, `Encounter-close`, `DiagnosticReport-open`, `DiagnosticReport-close`, `DiagnosticReport-select`, `DiagnosticReport-update`, `userlogout`, `userhibernate`, `syncerror`, `heartbeat` | tên sự kiện | `"Patient-open"` |
| `event.hub.topic` | string | có |  | topic của phiên làm việc; không rỗng, tối đa 256 byte, không ký tự điều khiển | `"7f2c4e1a-9b3d-4c5e-8a1f-2d6b9e0c3a47"` |
| `event.context.versionId` | string | không |  | phiên bản ngữ cảnh: Hub cấp khi mở; sự kiện DiagnosticReport-update phải gửi đúng phiên bản hiện tại | `"c8fa1e88-c131-4848-8467-9a0b2d1f5e33"` |
| `id` | string | có |  | id thông điệp do bên gửi đặt, không rỗng | `"m-0192f3a4"` |
| `timestamp` | string | có | định dạng date-time | thời điểm phát sinh (RFC 3339), không rỗng | `"2026-11-23T08:30:00Z"` |

```json
{
  "event": {
    "context": [
      {
        "key": "patient"
      }
    ],
    "context.versionId": "c8fa1e88-c131-4848-8467-9a0b2d1f5e33",
    "hub.event": "Patient-open",
    "hub.topic": "7f2c4e1a-9b3d-4c5e-8a1f-2d6b9e0c3a47"
  },
  "id": "m-0192f3a4",
  "timestamp": "2026-11-23T08:30:00Z"
}
```

**Phản hồi**

**`202`** Accepted

Schema: `httpapi.FhircastChangeResponse`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `event` | object `httpapi.FhircastEventBody` | có |  | thông điệp đã phát (đã bổ sung context.versionId) |  |
| `event.event` | object `httpapi.FhircastEventPayload` | có |  | nội dung sự kiện |  |
| `event.event.context` | mảng `httpapi.FhircastContextItem` | có |  | ngữ cảnh của sự kiện; mảng (có thể rỗng) các phần tử {key, resource} |  |
| `event.event.context[].key` | string | có |  | vai trò của resource trong ngữ cảnh (patient, encounter, study, report...) | `"patient"` |
| `event.event.context[].resource` | object tự do | có |  | resource FHIR dạng JSON (luôn có resourceType); với sự kiện update là Bundle |  |
| `event.event.hub.event` | string | có | một trong: `Patient-open`, `Patient-close`, `ImagingStudy-open`, `ImagingStudy-close`, `Encounter-open`, `Encounter-close`, `DiagnosticReport-open`, `DiagnosticReport-close`, `DiagnosticReport-select`, `DiagnosticReport-update`, `userlogout`, `userhibernate`, `syncerror`, `heartbeat` | tên sự kiện | `"Patient-open"` |
| `event.event.hub.topic` | string | có |  | topic của phiên làm việc; không rỗng, tối đa 256 byte, không ký tự điều khiển | `"7f2c4e1a-9b3d-4c5e-8a1f-2d6b9e0c3a47"` |
| `event.event.context.versionId` | string | không |  | phiên bản ngữ cảnh: Hub cấp khi mở; sự kiện DiagnosticReport-update phải gửi đúng phiên bản hiện tại | `"c8fa1e88-c131-4848-8467-9a0b2d1f5e33"` |
| `event.id` | string | có |  | id thông điệp do bên gửi đặt, không rỗng | `"m-0192f3a4"` |
| `event.timestamp` | string | có | định dạng date-time | thời điểm phát sinh (RFC 3339), không rỗng | `"2026-11-23T08:30:00Z"` |
| `success` | boolean | có |  | true khi sự kiện đã được chấp nhận và phát cho người đăng ký | `true` |

```json
{
  "event": {
    "event": {
      "context": [
        {
          "key": "patient"
        }
      ],
      "context.versionId": "c8fa1e88-c131-4848-8467-9a0b2d1f5e33",
      "hub.event": "Patient-open",
      "hub.topic": "7f2c4e1a-9b3d-4c5e-8a1f-2d6b9e0c3a47"
    },
    "id": "m-0192f3a4",
    "timestamp": "2026-11-23T08:30:00Z"
  },
  "success": true
}
```

**`400`** Sự kiện sai (Missing event ID, Invalid event[\"hub.event\"], Topic mismatch, No DiagnosticReport currently open for this topic...)

Schema: `httpapi.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`401`** Thiếu hoặc sai Bearer token

Schema: `httpapi.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`403`** Token thiếu project hoặc scope fhircast/*

Schema: `httpapi.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`409`** Ngữ cảnh đổi trong lúc cập nhật, không còn ở phiên bản đã gửi

Schema: `httpapi.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`413`** Body vượt FHIRCAST_MAX_BODY_BYTES

Schema: `httpapi.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).

**`500`** Lỗi hạ tầng (Redis)

Schema: `httpapi.OperationOutcome` (cấu trúc dùng chung, xem [common.md mục 3](../common.md#3-định-dạng-lỗi)).
