# Tham chiếu API theo route (sinh từ swagger)

Mỗi tệp là một nhóm (`tag`) của `swagger.json`, **sinh tự động**: tóm tắt, mô tả, xác thực, Content-Type, **bảng header**, **bảng tham số**, bảng trường và JSON mẫu. Từng trường hợp phản hồi ở [../cases/](../cases/README.md); quy ước chung (bốn tiền tố, lỗi, hạn mức) ở [../common.md](../common.md).

| Nhóm | Tệp | Route |
|---|---|---|
| STOW-RS, QIDO-RS, WADO-RS, WADO-URI | [dicomweb.md](dicomweb.md) | 19 |
| Probe sức khỏe | [health.md](health.md) | 4 |

**Cập nhật:** sửa annotation ở `internal/presentation/httpapi/swagger.go`, `make swagger APP=dicomweb`, rồi `UPDATE_DOCS=1 go test ./apps/dicomweb/docs -run TestAPIReferenceIsUpToDate`.

**Lưu ý:** swagger chỉ liệt kê tiền tố `/dicomweb`; ba tiền tố còn lại phục vụ cùng tập route (xem common.md). Route DICOMweb nhận body dạng `multipart/related` (STOW) không có DTO nên phần "Body yêu cầu" mô tả bằng văn bản ở mô tả route và ở cases.
