package domain

import "net/http"

// OAuthError là lỗi của /oauth2/token theo RFC 6749 §5.2 ({"error","error_description"}). Như Medplum, lỗi client thường là
// 400 `invalid_request` chứ không phải 401 `invalid_client` (quyết định Q3: giữ kiểu Medplum cho tương thích client).
type OAuthError struct {
	Code        string
	Description string
	Status      int
}

func (e *OAuthError) Error() string { return e.Code + ": " + e.Description }

func oauthErr(code, desc string, status int) *OAuthError {
	return &OAuthError{Code: code, Description: desc, Status: status}
}

// ErrInvalidRequest dựng `invalid_request` (400).
func ErrInvalidRequest(desc string) *OAuthError {
	return oauthErr("invalid_request", desc, http.StatusBadRequest)
}

// ErrInvalidGrant dựng `invalid_grant` (400).
func ErrInvalidGrant(desc string) *OAuthError {
	return oauthErr("invalid_grant", desc, http.StatusBadRequest)
}

// ErrInvalidScope dựng `invalid_scope` (400).
func ErrInvalidScope(desc string) *OAuthError {
	return oauthErr("invalid_scope", desc, http.StatusBadRequest)
}

// ErrAccessDenied dựng `access_denied` (400, như Medplum).
func ErrAccessDenied(desc string) *OAuthError {
	return oauthErr("access_denied", desc, http.StatusBadRequest)
}

// ErrServer dựng `server_error` (500); desc không chứa chi tiết nội bộ.
func ErrServer() *OAuthError {
	return oauthErr("server_error", "Internal server error", http.StatusInternalServerError)
}

// ErrInvalidClient dựng `invalid_client` (400 như Medplum, vẫn không phải 401 của RFC 6749 §5.2): chỉ dùng cho pre-authorized_code.
func ErrInvalidClient(desc string) *OAuthError {
	return oauthErr("invalid_client", desc, http.StatusBadRequest)
}
