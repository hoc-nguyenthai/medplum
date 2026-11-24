# Tham chiếu API: nhóm `health`

> **Tệp này được SINH từ `swagger.json`** (Trivita Auth API); đừng sửa tay. Cập nhật: `UPDATE_DOCS=1 go test` ở thư mục `docs`. Điều kiện nào ra phản hồi nào (từng trường hợp, ví dụ JSON, header) ở [../cases/health.md](../cases/health.md).

Probe liveness và readiness cho orchestrator.

**Header phản hồi chung (mọi route):** `Cache-Control: no-store, no-cache, must-revalidate`, `Pragma: no-cache`, `Content-Security-Policy: default-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none';`, `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`, `X-Request-Id`; `Strict-Transport-Security` khi `AUTH_BASE_URL` là https. Chi tiết và các envelope lỗi dùng chung: [../common.md](../common.md).

| Route | Tóm tắt |
|---|---|
| [`GET /health/live`](#get-health-live) | Liveness probe |
| [`GET /health/ready`](#get-health-ready) | Readiness probe (kiểm tra phụ thuộc) |

---

<a id="get-health-live"></a>
## `GET /health/live`

**Liveness probe**

Trả {"status":"ok"} khi tiến trình còn sống; không kiểm tra phụ thuộc. /healthz và /health/startup trả cùng nội dung.

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
| `status` | string | có |  | luôn là "ok" khi tiến trình còn sống | `"ok"` |

```json
{
  "status": "ok"
}
```

---

<a id="get-health-ready"></a>
## `GET /health/ready`

**Readiness probe (kiểm tra phụ thuộc)**

Kiểm tra từng phụ thuộc đã khai báo (ví dụ DB, Redis); 200 khi tất cả sẵn sàng, 503 kèm cùng body khi có phụ thuộc chưa sẵn sàng.

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
| `checks` | object tự do | có |  | kết quả từng phụ thuộc theo tên (true = sẵn sàng); rỗng khi chưa khai báo phụ thuộc nào |  |
| `ready` | boolean | có |  | true khi mọi phụ thuộc sẵn sàng (HTTP 200); false kèm HTTP 503 | `true` |

```json
{
  "ready": true
}
```

**`503`** Service Unavailable

Schema: `httpapi.ReadyResponse`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `checks` | object tự do | có |  | kết quả từng phụ thuộc theo tên (true = sẵn sàng); rỗng khi chưa khai báo phụ thuộc nào |  |
| `ready` | boolean | có |  | true khi mọi phụ thuộc sẵn sàng (HTTP 200); false kèm HTTP 503 | `true` |

```json
{
  "ready": true
}
```
