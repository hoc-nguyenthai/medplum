# Tham chiếu API theo route (sinh từ swagger)

Mỗi tệp là một nhóm (`tag`) của `swagger.json`, **sinh tự động**; mỗi route có: tóm tắt, mô tả, xác thực, Content-Type yêu cầu/phản hồi, **bảng header**, **bảng tham số** (đường dẫn, query, form), **bảng từng trường** của body yêu cầu và của từng phản hồi (kiểu, bắt buộc, ràng buộc, mô tả, ví dụ) và JSON mẫu. Từng trường hợp phản hồi (điều kiện, thông điệp lỗi, header, tác dụng phụ) ở [../cases/](../cases/README.md); quy ước chung ở [../common.md](../common.md).

| Nhóm | Tệp | Route |
|---|---|---|
| Đăng ký, đăng nhập, MFA, mật khẩu, phiên, đăng nhập ngoài | [auth.md](auth.md) | 28 |
| OAuth2/OIDC (authorize, token, userinfo, introspect, logout, register) | [oauth.md](oauth.md) | 9 |
| Quản trị project, thành viên | [admin.md](admin.md) | 11 |
| SCIM 2.0 Users | [scim.md](scim.md) | 6 |
| Gửi thư | [email.md](email.md) | 1 |
| JWKS, discovery | [well-known.md](well-known.md) | 6 |
| Probe sức khỏe | [health.md](health.md) | 2 |

**Cách cập nhật:** sửa annotation hoặc struct DTO (mô tả ở bình luận cuối dòng của trường, `binding:"required"`, `example`, `enums`), `make swagger APP=auth`, rồi `UPDATE_DOCS=1 go test ./apps/auth/docs -run TestAPIReferenceIsUpToDate`. Test đỏ khi tệp lỗi thời.

**Ghi chú đọc bảng:** trường bắt đầu bằng `_` (phần tử mở rộng của kiểu nguyên thủy FHIR), `extension`, `modifierExtension`, `contained` được lược khỏi bảng vì không thuộc hợp đồng của route. Kiểu `fhir.*` (tài nguyên) chỉ khai triển một tầng; cấu trúc đầy đủ theo FHIR R4. JSON mẫu dựng từ `example` của từng trường nên với phản hồi có nhiều dạng (ví dụ `LoginResult`) nó gộp trường của mọi dạng: ví dụ đúng từng dạng ở cases.
