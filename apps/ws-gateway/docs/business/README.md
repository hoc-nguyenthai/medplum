# ws-gateway: tài liệu nghiệp vụ (góc nhìn BA kỹ thuật)

Service không có giao diện: giao diện là giao thức WebSocket và vài route HTTP của Hub FHIRcast. Các tài liệu:

| # | Tài liệu | Trả lời câu hỏi |
|---|---|---|
| 1 | [01-tong-quan-va-luong-nghiep-vu.md](01-tong-quan-va-luong-nghiep-vu.md) | Làm gì, ranh giới, tác nhân, thuật ngữ, 6 luồng (BF-1..BF-6) |
| 2 | [02-luong-nguoi-dung.md](02-luong-nguoi-dung.md) | Client và vận hành đi qua bước nào, thấy mã đóng và mã HTTP nào |
| 3 | [03-chuc-nang.md](03-chuc-nang.md) | F-01..F-16 |
| 4 | [04-luat-va-phan-quyen.md](04-luat-va-phan-quyen.md) | BR-01..BR-75, AT-01..AT-40, truy vết test, điểm lệch và rủi ro |

Mã truy vết: `F-xx` chức năng, `BR-xx` luật, `BF-x` luồng, `AT-xx` kịch bản chấp nhận. Hợp đồng dây: [../api-contract.md](../api-contract.md) và [../api-contract/](../api-contract/common.md). Kiến trúc, cấu hình, vận hành: [../architecture.md](../architecture.md), [../config.md](../config.md), [../runbook.md](../runbook.md).

**Nguồn sự thật:** mã (`pkg/wsgroup`, `pkg/fhirsub/ws*.go`, `pkg/fhircast`, `apps/ws-gateway/internal`) và test (đường dẫn ở mục 4.3 của tệp 4).

**Đã kiểm riêng với mã** (ngoài việc agent viết): `hub.lease_seconds` luôn là hằng `DefaultLease` (`pkg/fhircast/message.go`), lỗi JWKS bị bọc thành 401 (`pkg/fhirauth/jwt.go`), `FHIRCAST_REQUIRE_SCOPE=false` bị cấm ở staging/production, chú thích `pkg/fhircast/ws/ws.go` nhắc test không tồn tại. Các khẳng định còn lại (Redis chết không ngắt socket, hết hạn token chỉ phát hiện khi có sự kiện, `Feed.ensure` giữ khóa lúc gọi Redis) do agent đọc mã; mục chưa kiểm ghi `(chưa kiểm)`.

> **Điểm lệch và rủi ro quan trọng (chưa sửa mã, chi tiết ở mục 4.4):**
> 1. `eventsSinceSubscriptionStart` và heartbeat **không phát hiện được mất sự kiện**; `api-contract.md` mục 3 và `runbook.md` nói ngược lại.
> 2. Redis chết không ngắt socket đã bind: client vẫn nhận heartbeat nhưng không nhận sự kiện.
> 3. Token bind là bearer dùng lại được 5 phút; thu hồi quyền không ngắt binding đã có.
> 4. FHIRcast: `hub.lease_seconds` báo sai khi cấu hình `FHIRCAST_LEASE`; thu hồi token không cắt socket; cô lập chỉ theo project, không theo người; route HTTP của Hub không có hạn mức tốc độ.
> 5. `ping` trước bind không dừng đồng hồ 10 giây (mô tả cũ nói tin đầu phải là bind).
