package httpapi

// Tệp này chỉ chứa annotation swaggo để sinh OpenAPI (xem `make swagger APP=auth`).
// Sửa annotation xong phải chạy lại `make swagger APP=auth` để cập nhật apps/auth/docs.

// @title						Trivita Auth API
// @version					1.0
// @description				Dịch vụ xác thực và cấp token (port từ Medplum). Lỗi dưới /auth và /admin trả OperationOutcome (application/fhir+json); lỗi /oauth2 theo RFC 6749.
// @BasePath					/
// @securityDefinitions.apikey	BearerAuth
// @in							header
// @name						Authorization
// @description				Dạng "Bearer <access_token>".
