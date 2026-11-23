package httpapi

// Tệp này chỉ chứa annotation swaggo để sinh OpenAPI (xem `make swagger APP=auth`).
// Sửa annotation xong phải chạy lại `make swagger APP=auth` để cập nhật apps/auth/docs.

// @title						Trivita Auth API
// @version					1.0
// @description				Dịch vụ xác thực và cấp token (port từ Medplum). Lỗi dưới /auth và /admin trả OperationOutcome (application/fhir+json); lỗi /oauth2 theo RFC 6749.
// @BasePath					/
// @tag.name					auth
// @tag.description			Đăng ký, đăng nhập nhiều bước (login, profile, scope), MFA, mật khẩu, xác minh email, đăng nhập ngoài (IdP, Google), exchange và preauthorize. Công khai trừ khi route ghi Bearer.
// @tag.name					oauth
// @tag.description			OAuth2/OIDC: authorize, token, userinfo, introspect (RFC 7662), logout, register (tra client dựng sẵn theo redirect URI). Lỗi /oauth2/token theo RFC 6749 §5.2.
// @tag.name					well-known
// @tag.description			Discovery và khóa công khai: JWKS, openid-configuration, oauth-authorization-server, SMART configuration, protected resource metadata (RFC 9728).
// @tag.name					admin
// @tag.description			Quản trị project qua HTTP (tắt mặc định, AUTH_ADMIN_ENABLED): project, client, thành viên, mời user, setting, site. Chỉ Bearer của admin project.
// @tag.name					scim
// @tag.description			SCIM 2.0 /scim/v2/Users cho IdP (tắt mặc định, AUTH_SCIM_ENABLED và tính năng scim của project). Lỗi theo RFC 7644.
// @tag.name					email
// @tag.description			Gửi thư POST /email/v1/send (tắt mặc định, AUTH_EMAIL_ENABLED). Chỉ admin project có tính năng email.
// @tag.name					health
// @tag.description			Probe liveness và readiness cho orchestrator.
// @securityDefinitions.apikey	BearerAuth
// @in							header
// @name						Authorization
// @description				Dạng "Bearer <access_token>".
