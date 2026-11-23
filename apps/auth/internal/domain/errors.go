package domain

import (
	"errors"
	"fmt"
	"time"
)

// Kind phân loại lỗi nghiệp vụ; tầng presentation ánh xạ sang mã HTTP (BadRequest 400, Unauthorized 401, Forbidden 403,
// NotFound 404, Conflict 409, Internal 500).
type Kind int

// Các loại lỗi.
const (
	KindBadRequest Kind = iota + 1
	KindUnauthorized
	KindForbidden
	KindNotFound
	KindConflict
	KindInternal
	KindTooManyRequests
	// KindBadGateway: phụ thuộc bên ngoài (ví dụ máy chủ SMTP) lỗi; HTTP 502, thông điệp chung không lộ chi tiết.
	KindBadGateway
)

// Error là lỗi nghiệp vụ. Message là thông điệp giữ đúng chữ của Medplum (client dựa vào đó); Expression (tùy chọn) là
// đường dẫn trường gây lỗi, ví dụ "password".
type Error struct {
	Kind       Kind
	Message    string
	Expression string
	// RetryAfter chỉ có ở KindTooManyRequests: thời gian tới khi bộ đếm được đặt lại (làm tròn lên giây khi trả cho client).
	RetryAfter time.Duration
}

func (e *Error) Error() string { return e.Message }

// BadRequest dựng lỗi 400.
func BadRequest(msg string) *Error { return &Error{Kind: KindBadRequest, Message: msg} }

// BadRequestAt dựng lỗi 400 gắn với một trường.
func BadRequestAt(msg, expression string) *Error {
	return &Error{Kind: KindBadRequest, Message: msg, Expression: expression}
}

// Unauthorized dựng lỗi 401.
func Unauthorized(msg string) *Error { return &Error{Kind: KindUnauthorized, Message: msg} }

// Forbidden dựng lỗi 403.
func Forbidden(msg string) *Error { return &Error{Kind: KindForbidden, Message: msg} }

// NotFound dựng lỗi 404 với thông điệp "Not found" như Medplum.
func NotFound() *Error { return &Error{Kind: KindNotFound, Message: "Not found"} }

// Conflict dựng lỗi 409.
func Conflict(msg string) *Error { return &Error{Kind: KindConflict, Message: msg} }

// BadGateway dựng lỗi 502 (msg an toàn để trả cho client).
func BadGateway(msg string) *Error { return &Error{Kind: KindBadGateway, Message: msg} }

// Internal dựng lỗi 500; msg chỉ để ghi log, không bao giờ trả cho client.
func Internal(msg string) *Error { return &Error{Kind: KindInternal, Message: msg} }

// InternalErr bọc lỗi hạ tầng thành lỗi 500.
func InternalErr(what string, err error) *Error {
	return &Error{Kind: KindInternal, Message: fmt.Sprintf("%s: %v", what, err)}
}

// ErrVersionConflict do kho trả về khi cập nhật có điều kiện (IfMatch) thất bại vì phiên bản đã đổi (HTTP 412 ở Medplum).
// Use case quyết định đổi thành lỗi nghiệp vụ nào ("Already used", "Login profile already set"...).
var ErrVersionConflict = errors.New("domain: phiên bản resource đã thay đổi")

// KindOf trả loại lỗi nghiệp vụ của err, hoặc KindInternal nếu không phải *Error.
func KindOf(err error) Kind {
	var de *Error
	if errors.As(err, &de) {
		return de.Kind
	}
	return KindInternal
}

// Is cho biết err là lỗi nghiệp vụ loại kind và (nếu msg khác rỗng) có đúng thông điệp.
func Is(err error, kind Kind, msg string) bool {
	var de *Error
	return errors.As(err, &de) && de.Kind == kind && (msg == "" || de.Message == msg)
}

// TooManyRequests dựng lỗi 429 kèm thời gian chờ.
func TooManyRequests(retryAfter time.Duration) *Error {
	return &Error{Kind: KindTooManyRequests, Message: "Too many requests", RetryAfter: retryAfter}
}
