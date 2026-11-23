# fhir-cli

Công cụ dòng lệnh cho [fhir-server](../fhir-server/README.md): đọc, tìm kiếm, ghi, xóa, nạp hàng loạt (import), bulk export và reindex. Chỉ dùng thư viện chuẩn của Go, không phụ thuộc gì ngoài HTTP API của server.

```bash
make build APP=fhir-cli            # bin/fhir-cli
export FHIR_BASE_URL=http://localhost:8005/fhir/R4
export FHIR_TOKEN=<bearer token>   # token tĩnh hoặc JWT
```

| Biến / cờ chung | Mặc định | Ý nghĩa |
|---|---|---|
| `-base` / `FHIR_BASE_URL` | `http://localhost:8005/fhir/R4` | gốc API |
| `-token` / `FHIR_TOKEN` | (rỗng) | bearer token |
| `-project` / `FHIR_PROJECT` | (rỗng) | project (header `X-Fhir-Project`) cho token super-admin |
| `-timeout` | `60s` | timeout mỗi yêu cầu HTTP |

## Lệnh

```bash
fhir-cli read Patient/<id> [-version V]
fhir-cli search Patient 'family=smith&_count=50' [-all -ndjson]     # -all đi hết các trang, cần -ndjson
fhir-cli create Patient -f patient.json            # -f - đọc stdin
fhir-cli update Patient/<id> -f patient.json
fhir-cli delete Patient/<id>
fhir-cli import -f data.ndjson [-mode batch|transaction] [-size 100]
fhir-cli export -o out/ [-level system|patient|group/<id>] [-type Patient,Observation] [-filter "Patient?active=true"]... [-since 2026-01-01T00:00:00Z] [-keep]
fhir-cli reindex Observation [id]                  # cần quyền admin
```

- **import** nhận NDJSON (mỗi dòng một resource), một resource đơn lẻ hoặc một Bundle (lấy `entry[].resource`). Resource không có `id` được tạo mới (`POST`); resource có `id` được cập nhật (`PUT`). Mã thoát khác 0 nếu có mục thất bại; ở chế độ `batch` các mục tốt vẫn được nạp, lỗi in theo từng mục. Chế độ `transaction` là tất cả hoặc không.
- **export** tạo job bulk export, chờ xong, tải mỗi file về `DIR/<Type>-<n>.ndjson` rồi (mặc định) xóa job và file trên máy chủ; `-keep` giữ lại.
- Mã thoát: `0` thành công, `1` lỗi (từ máy chủ hoặc I/O), `2` sai cách dùng.

## Giới hạn

- `import` giữ nguyên `id` của dữ liệu nguồn chỉ khi máy chủ đích bật `FHIR_UPDATE_AS_CREATE=true`. Mặc định (tắt), resource có `id` chưa tồn tại trên máy chủ đích báo 404 theo mục, còn resource không có `id` được cấp id mới (tham chiếu giữa các resource không tự được ánh xạ lại). Chuyển dữ liệu giữa hai máy chủ nên bật tùy chọn này ở máy chủ đích.
- Link phân trang và `Content-Location` chỉ được đi theo trên chính máy chủ trong `-base` (chỉ lấy path và query, không bao giờ gửi token tới host khác).
- Chưa có lệnh cho websocket subscription, `$validate`, terminology và quản trị project.
