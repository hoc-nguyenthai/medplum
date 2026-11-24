# dicomweb: test

Bố cục chung: [docs/plans/service-independence.md](../../../docs/plans/service-independence.md). Test đơn vị nằm **cạnh mã**. Ở D1 chưa có `tests/integration`, `tests/e2e` (image) và `tests/load`; chúng đến cùng D3 và D6 khi đã có đường STOW để kiểm.

| Tầng | Ở đâu | Cần Docker? | Chạy | Kiểm gì |
|---|---|---|---|---|
| Đơn vị | `internal/**/*_test.go`, `docs/` | không | `make test-unit` | cấu hình (kể cả `config.md` và `.env.example` không lỗi thời), route HTTP với bộ xác thực giả (401, 403, 501, bốn tiền tố, project, super-admin, health, giới hạn body và hạn mức, không lộ lỗi 5xx), hợp đồng API |
| Container | `internal/bootstrap/bootstrap_test.go` | không | cùng lệnh | Container thật trên SQLite tạm: JWT thật (HS256) qua `fhirauth.Verifier`, cô lập project của kho, kho Binary dùng được, từ chối khởi động khi schema chưa migrate hoặc kho không mở được, tắt êm (ready 503, thoát không lỗi) |
| Hệ thống | `e2e/` ở gốc repo | không | `make test-e2e` | sẽ thêm ca `auth` cấp token → dicomweb ở D6 |

## Quy tắc

- Test chống lỗi thời: `docs/config.md` sinh từ `internal/config/config.go`, `.env.example` phải nhắc mọi biến (`UPDATE_DOCS=1 go test ./apps/dicomweb/internal/config -run TestConfigDocIsUpToDate`).
- Hợp đồng API: `TestRoutesMatchSwaggerContract` so route thật với `docs/swagger.json`; `TestAllPrefixesServeTheSameRoutes` giữ bốn tiền tố cùng tập route; `docs` `TestAPIContractIsUpToDate` giữ bảng route (`UPDATE_DOCS=1 go test ./apps/dicomweb/docs`), cùng `Completeness` và `Structure` của `pkg/apicontract`. Sửa annotation ở `httpapi/swagger.go` rồi `make swagger APP=dicomweb`.
- Kiến trúc: luật phụ thuộc của `apps/dicomweb/**` ở `architecture_test.go` (`go test .` ở gốc).
- Đột biến đã thử: bỏ kiểm project khỏi `guard` làm `TestProjectPrefixMustMatchToken` và `TestRealJWTAuthentication` đỏ.
