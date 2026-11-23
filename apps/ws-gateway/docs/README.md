# ws-gateway: tài liệu

| Tài liệu | Nội dung |
|---|---|
| [api-contract.md](api-contract.md) | **Hợp đồng API**: bảng route (sinh từ swagger, có test chống lệch), giao thức WebSocket, mã đóng, Hub FHIRcast |
| `swagger.json`, `swagger.yaml`, `docs.go` | OpenAPI do swaggo sinh (`make swagger APP=ws-gateway`): probe, Hub FHIRcast và route nâng cấp WebSocket; service không phục vụ `/swagger` |
| [architecture.md](architecture.md) | Làm gì, không làm gì, các thành phần, luồng dữ liệu, hành vi khi lỗi |
| [config.md](config.md) | Mọi biến môi trường, mặc định và ý nghĩa (**sinh từ mã**, có test chống lỗi thời) |
| [testing.md](testing.md) | Bốn tầng test, cách chạy, yêu cầu Docker |
| [../README.md](../README.md) | Giao thức websocket, FHIRcast, vận hành, metric, K8s |
| [runbook.md](runbook.md) | **Vận hành** (tự chứa): sức khỏe, tắt êm, cấu hình production, triển khai, smoke test, định cỡ (số đo thật), metric, xoay khóa, sự cố, FHIRcast, reverse proxy |
| [../../../deploy/k8s/ws-gateway/](../../../deploy/k8s/ws-gateway) | Manifest K8s mẫu |
