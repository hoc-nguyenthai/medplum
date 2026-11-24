# Tham chiếu API: nhóm `health`

> **Tệp này được SINH từ `swagger.json`** (Trivita DICOMweb API); đừng sửa tay. Cập nhật: `UPDATE_DOCS=1 go test` ở thư mục `docs`. Điều kiện nào ra phản hồi nào (từng trường hợp, ví dụ JSON, header) ở [../cases/health.md](../cases/health.md).

Probe liveness, startup và readiness cho orchestrator.

**Header phản hồi chung (mọi route):** `Cache-Control: no-store`, `Content-Security-Policy: default-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none';`, `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`, `X-Request-Id`; `Strict-Transport-Security` khi `DICOMWEB_BASE_URL` là https. Bốn tiền tố URL, định dạng lỗi `{"error"}`, hạn mức: [../common.md](../common.md).

| Route | Tóm tắt |
|---|---|
| [`GET /health/live`](#get-health-live) | Liveness probe |
| [`GET /health/ready`](#get-health-ready) | Readiness probe |
| [`GET /health/startup`](#get-health-startup) | Startup probe |
| [`GET /healthz`](#get-healthz) | Healthcheck của Docker |

---

<a id="get-health-live"></a>
## `GET /health/live`

**Liveness probe**

Tiến trình còn sống; không kiểm phụ thuộc ngoài để orchestrator không restart pod vì lỗi của cơ sở dữ liệu.

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

Sẵn sàng nhận yêu cầu: cơ sở dữ liệu trả lời trong READINESS_TIMEOUT. 503 khi đang tắt (SIGTERM) hoặc phụ thuộc lỗi.

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
| `checks` | object tự do | có |  | kết quả từng phụ thuộc (database) |  |
| `ready` | boolean | có |  | true khi mọi phụ thuộc chính sẵn sàng và không đang tắt | `true` |

```json
{
  "ready": true
}
```

**`503`** Chưa sẵn sàng hoặc đang tắt

Schema: `httpapi.ReadyResponse`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `checks` | object tự do | có |  | kết quả từng phụ thuộc (database) |  |
| `ready` | boolean | có |  | true khi mọi phụ thuộc chính sẵn sàng và không đang tắt | `true` |

```json
{
  "ready": true
}
```

---

<a id="get-health-startup"></a>
## `GET /health/startup`

**Startup probe**

Đã khởi động xong (route đã sẵn sàng); không kiểm phụ thuộc ngoài.

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

**Healthcheck của Docker**

Giống liveness; dùng cho HEALTHCHECK của image (`/app healthcheck`).

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
