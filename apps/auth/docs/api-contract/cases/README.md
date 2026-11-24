# Hợp đồng API theo từng trường hợp

Mỗi route có một mục (neo `post-auth-login`...) mô tả **thứ tự kiểm tra**, **bảng trường hợp** (điều kiện → HTTP → header riêng → body → tác dụng phụ) và **ví dụ yêu cầu/phản hồi** từng dạng. **Viết tay** nhưng có test: `TestCasesCoverEveryRoute` làm đỏ khi một route trong `swagger.json` thiếu mục hoặc còn mục của route đã bỏ. Thông điệp lỗi trích nguyên văn từ mã và test; mục ghi "(chưa kiểm)" là chỗ chưa đối chiếu.

| Nhóm | Tệp | Nội dung nổi bật |
|---|---|---|
| auth | [auth.md](auth.md) | Sơ đồ chuyển giữa các route, 5 dạng `LoginResult`, đăng ký, đăng nhập, MFA và hạn mức thử, mật khẩu, phiên, đăng nhập ngoài, exchange, preauthorize |
| oauth | [oauth.md](oauth.md) | `token` theo từng grant, xác thực client, claim token, authorize, userinfo, introspect, logout, register |
| admin | [admin.md](admin.md) | Quyền, quy tắc bảo vệ admin cuối cùng, settings, client, sites, invite, members |
| scim | [scim.md](scim.md) | Xác thực Basic/Bearer, ánh xạ PUT/PATCH, filter, phân trang |
| email | [email.md](email.md) | Thứ tự kiểm tra, tệp đính kèm, người gửi |
| well-known | [well-known.md](well-known.md) | JWKS, discovery |
| health | [health.md](health.md) | Probe |

Đọc cùng: [../common.md](../common.md) (header, cookie, envelope lỗi, hạn mức), [../reference/](../reference/README.md) (bảng từng trường).
