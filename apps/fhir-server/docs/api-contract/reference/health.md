# Tham chiếu API: nhóm `health`

> **Tệp này được SINH từ `swagger.json`** (Trivita FHIR Server API); đừng sửa tay. Cập nhật: `UPDATE_DOCS=1 go test` ở thư mục `docs`. Điều kiện nào ra phản hồi nào (từng trường hợp, ví dụ JSON, header) ở [../cases/health.md](../cases/health.md).

Probe liveness và readiness cho orchestrator (ngoài /fhir/R4, lỗi dạng JSON thường).

**Header phản hồi chung (mọi route):** `Cache-Control: no-store, no-cache, must-revalidate`, `Pragma: no-cache`, `Content-Security-Policy: default-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none';`, `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`, `X-Request-Id`. Định dạng lỗi, xác thực, hạn mức: [../common.md](../common.md).

| Route | Tóm tắt |
|---|---|
| [`GET /healthz`](#get-healthz) | Liveness probe |
| [`GET /readyz`](#get-readyz) | Readiness probe (kiểm tra phụ thuộc) |

---

<a id="get-healthz"></a>
## `GET /healthz`

**Liveness probe**

Tiến trình còn sống; luôn 200 khi server chạy. Không kiểm phụ thuộc nên orchestrator không khởi động lại tiến trình vì lỗi DB hay Redis.

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

---

<a id="get-readyz"></a>
## `GET /readyz`

**Readiness probe (kiểm tra phụ thuộc)**

200 khi nhận được lưu lượng; 503 khi đang tắt (drain) hoặc một phụ thuộc hỏng (database trong READINESS_TIMEOUT, broker khi bật Subscription). Body cho biết từng phụ thuộc.

Các trường hợp phản hồi: [../cases/health.md#get-readyz](../cases/health.md#get-readyz)

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
| `checks` | object tự do | có |  | kết quả từng phụ thuộc: accepting_traffic, database, broker (chỉ khi bật Subscription) |  |
| `ready` | boolean | có |  | true khi nhận được lưu lượng (không đang tắt, mọi phụ thuộc sẵn sàng) | `true` |

```json
{
  "ready": true
}
```

**`503`** Service Unavailable

Schema: `httpapi.ReadyResponse`

| Trường | Kiểu | Bắt buộc | Ràng buộc | Mô tả | Ví dụ |
|---|---|---|---|---|---|
| `checks` | object tự do | có |  | kết quả từng phụ thuộc: accepting_traffic, database, broker (chỉ khi bật Subscription) |  |
| `ready` | boolean | có |  | true khi nhận được lưu lượng (không đang tắt, mọi phụ thuộc sẵn sàng) | `true` |

```json
{
  "ready": true
}
```
