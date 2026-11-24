# dicomweb: test

Bố cục chung: [docs/plans/service-independence.md](../../../docs/plans/service-independence.md). Test đơn vị nằm **cạnh mã**. Chưa có `tests/integration`, `tests/e2e` (image) và `tests/load`; chúng đến ở D6 (STOW đã chạy từ D3, test STOW hiện nằm ở đơn vị và Container).

| Tầng | Ở đâu | Cần Docker? | Chạy | Kiểm gì |
|---|---|---|---|---|
| Đơn vị | `internal/**/*_test.go`, `docs/` | không | `make test-unit` | cấu hình (kể cả `config.md` và `.env.example` không lỗi thời), route HTTP với bộ xác thực giả (401, 403, 501, bốn tiền tố, project, super-admin, health, giới hạn body và hạn mức, không lộ lỗi 5xx), hợp đồng API |
| Container | `internal/bootstrap/bootstrap_test.go` | không | cùng lệnh | Container thật trên SQLite tạm: JWT thật (HS256) qua `fhirauth.Verifier`, cô lập project của kho, kho Binary dùng được, từ chối khởi động khi schema chưa migrate hoặc kho không mở được, tắt êm (ready 503, thoát không lỗi) |
| STOW (D3) | `pkg/dicomweb/scan_test.go`, `pkg/dicomread`, `pkg/dicomstow/stow_test.go`, `internal/bootstrap/stow_http_test.go` | không | cùng lệnh | bộ quét (SQ lồng sâu, trần phần đầu, độ dài giả, transfer syntax), đọc metadata, dịch vụ trên kho thật và kho nội dung theo dõi khóa (idempotent, song song, tệp hỏng và cắt không để lại gì, trần kích thước, quyền, cách ly project, thân đứt giữa chừng), và qua HTTP thật với JWT thật: phản hồi chuẩn, 100 MB truyền luồng có đo bộ nhớ, 8 yêu cầu song song trên SQLite thật |
| QIDO/WADO (D4) | `pkg/dicomweb/frame_test.go`, `pkg/dicomretrieve/retrieve_test.go`, `internal/bootstrap/retrieve_http_test.go` | không | cùng lệnh | `CopyFrame` và Content-Type theo transfer syntax; dịch vụ trên dữ liệu do STOW ghi: phân trang không trùng không sót, series lớn hơn một trang (1230 instance), frame không nén khớp từng byte, JPEG, frame nhiều fragment, danh sách frame, mọi mã lỗi, cách ly project, thiếu quyền Binary, kho mất nội dung; qua HTTP thật: round trip STOW → QIDO → WADO, Warning 299, Content-Length đúng, ngắt kết nối khi lỗi giữa chừng, frame của tệp 104 MB đúng từng byte với đỉnh heap 8 MB |
| Hệ thống | `e2e/` ở gốc repo | không | `make test-e2e` | sẽ thêm ca `auth` cấp token → dicomweb ở D6 |

## Quy tắc

- Test chống lỗi thời: `docs/config.md` sinh từ `internal/config/config.go`, `.env.example` phải nhắc mọi biến (`UPDATE_DOCS=1 go test ./apps/dicomweb/internal/config -run TestConfigDocIsUpToDate`).
- Hợp đồng API: `TestRoutesMatchSwaggerContract` so route thật với `docs/swagger.json`; `TestAllPrefixesServeTheSameRoutes` giữ bốn tiền tố cùng tập route; `docs` `TestAPIContractIsUpToDate` giữ bảng route (`UPDATE_DOCS=1 go test ./apps/dicomweb/docs`), cùng `Completeness` và `Structure` của `pkg/apicontract`. Sửa annotation ở `httpapi/swagger.go` rồi `make swagger APP=dicomweb`.
- Kiến trúc: luật phụ thuộc của `apps/dicomweb/**` ở `architecture_test.go` (`go test .` ở gốc).
- Đột biến D4 (đều làm test đỏ): sai chỉ số frame, cắt phân trang metadata sau trang đầu, bỏ lọc study khi tìm series, bỏ kiểm 416 (có test với kho hỏng để biết kiểm xảy ra trước khi mở nội dung), bỏ `Content-Length`, bỏ `Warning`, không ngắt kết nối khi lỗi giữa chừng.
- Đột biến đã thử (đều làm test đỏ): bỏ kiểm project khỏi `guard`; bỏ kiểm độ sâu SQ, kiểm trần phần đầu, kiểm tệp bị cắt, kiểm study của yêu cầu, bước bỏ qua instance đã có (đếm số lần ghi vào kho), bước dọn bên thua cuộc đua, khóa theo UID (chỉ lộ ra trên SQLite thật, kho bộ nhớ tuần tự hóa transaction), tính lại số liệu series, trần `MaxInstanceBytes`.
- Test song song trên SQLite từng chập chờn (khoảng 30% lần chạy): dọn Binary bên thua gặp `SQLITE_BUSY` khi nâng khóa đọc thành ghi; đã sửa bằng thử lại vài lần và chạy 25 lần liên tiếp không lỗi.
