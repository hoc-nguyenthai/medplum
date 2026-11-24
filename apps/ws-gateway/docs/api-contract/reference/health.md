# Tham chiếu API: nhóm `health`

> **Tệp này được SINH từ `swagger.json`** (Trivita WS Gateway API); đừng sửa tay. Cập nhật: `UPDATE_DOCS=1 go test` ở thư mục `docs`. Điều kiện nào ra phản hồi nào (từng trường hợp, ví dụ JSON, header) ở [../cases/health.md](../cases/health.md).

Probe liveness và readiness cho orchestrator.

**Header phản hồi chung (mọi route):** `Cache-Control: no-store, no-cache, must-revalidate`, `Content-Security-Policy: default-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none';`, `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`, `X-Request-Id`. Định dạng lỗi, xác thực, hạn mức: [../common.md](../common.md).

| Route | Tóm tắt |
|---|---|
| [`GET /health/live`](#get-health-live) | Liveness probe |
| [`GET /health/ready`](#get-health-ready) | Readiness probe |
| [`GET /health/startup`](#get-health-startup) | Liveness probe |
| [`GET /healthz`](#get-healthz) | Liveness probe |

---

<a id="get-health-live"></a>
## `GET /health/live`

**Liveness probe**

Tiến trình còn sống. Không kiểm Redis nên orchestrator không khởi động lại tiến trình vì lỗi Redis. /healthz dùng cho HEALTHCHECK của Docker; /health/startup trả như /health/live.

Các trường hợp phản hồi: [../cases/health.md#get-health-live](../cases/health.md#get-health-live)

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

Schema: `httpapi.HealthResponse`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `status` | string | có |  | luôn "ok" khi tiến trình còn sống | `"ok"` |

```json
{
  "status": "ok"
}
```

---

<a id="get-health-ready"></a>
## `GET /health/ready`

**Readiness probe**

200 khi nhận được lưu lượng; 503 khi đang tắt (drain) hoặc Redis chưa kết nối được (Redis chưa lên lúc khởi động không làm tiến trình thoát, chỉ giữ readiness 503). Body cho biết từng phụ thuộc.

Các trường hợp phản hồi: [../cases/health.md#get-health-ready](../cases/health.md#get-health-ready)

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

Schema: `httpapi.ReadyResponse`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `checks` | object tự do | có |  | kết quả từng phụ thuộc, ví dụ redis; rỗng khi không có phụ thuộc nào được kiểm |  |
| `ready` | boolean | có |  | true khi nhận được lưu lượng (không đang tắt và Redis kết nối được) | `true` |

```json
{
  "ready": true
}
```

**`503`** Service Unavailable

Schema: `httpapi.ReadyResponse`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `checks` | object tự do | có |  | kết quả từng phụ thuộc, ví dụ redis; rỗng khi không có phụ thuộc nào được kiểm |  |
| `ready` | boolean | có |  | true khi nhận được lưu lượng (không đang tắt và Redis kết nối được) | `true` |

```json
{
  "ready": true
}
```

---

<a id="get-health-startup"></a>
## `GET /health/startup`

**Liveness probe**

Tiến trình còn sống. Không kiểm Redis nên orchestrator không khởi động lại tiến trình vì lỗi Redis. /healthz dùng cho HEALTHCHECK của Docker; /health/startup trả như /health/live.

Các trường hợp phản hồi: [../cases/health.md#get-health-startup](../cases/health.md#get-health-startup)

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

Schema: `httpapi.HealthResponse`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `status` | string | có |  | luôn "ok" khi tiến trình còn sống | `"ok"` |

```json
{
  "status": "ok"
}
```

---

<a id="get-healthz"></a>
## `GET /healthz`

**Liveness probe**

Tiến trình còn sống. Không kiểm Redis nên orchestrator không khởi động lại tiến trình vì lỗi Redis. /healthz dùng cho HEALTHCHECK của Docker; /health/startup trả như /health/live.

Các trường hợp phản hồi: [../cases/health.md#get-healthz](../cases/health.md#get-healthz)

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

Schema: `httpapi.HealthResponse`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `status` | string | có |  | luôn "ok" khi tiến trình còn sống | `"ok"` |

```json
{
  "status": "ok"
}
```
