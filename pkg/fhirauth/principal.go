// Package fhirauth là xác thực và phân quyền cho API FHIR: kiểm tra JWT (OAuth2/OIDC) và tách dữ liệu theo project.
//
//   - Verifier kiểm tra access token (chữ ký theo JWKS của identity provider, iss, aud, exp) và trả Principal.
//   - Principal mang subject, project và vai trò; được truyền qua context.
//   - Scope bọc fhirrouter.Repository để mọi thao tác chỉ thấy và chỉ ghi resource của project của người gọi
//     (Meta.project), nên một deployment dùng chung được cho nhiều tenant.
//
// AccessPolicy từ claim (Policy) giới hạn theo loại và tiêu chí. Scope SMART theo loại resource (system/ và user/, xem Grants) được Scope kiểm ở mọi thao tác. Chưa làm: scope patient/
// (compartment), AccessPolicy của Medplum, super-admin xuyên project.
package fhirauth

import (
	"context"
	"time"
)

// Role là mức quyền trong một project.
type Role int

const (
	RoleNone Role = iota
	RoleRead
	RoleWrite
	RoleAdmin
)

func (r Role) String() string {
	switch r {
	case RoleRead:
		return "read"
	case RoleWrite:
		return "write"
	case RoleAdmin:
		return "admin"
	}
	return "none"
}

// Principal là người gọi đã xác thực.
type Principal struct {
	Subject string
	Project string // rỗng nghĩa là không tách theo project (token tĩnh, chế độ một tenant)
	Role    Role
	// Grants giới hạn theo loại resource (scope SMART). nil: không giới hạn theo loại.
	Grants Grants
	// Policy là AccessPolicy từ claim token (loại được truy cập, tiêu chí, chỉ đọc). nil: không có chính sách.
	Policy *Policy
	// Job đánh dấu Principal dựng lại cho job nền (từ chuỗi đã ký): được ghi Binary để lưu file kết quả.
	Job bool
	// Super là super-admin (scope fhir.super đã được bật): hành động trong project do header chọn. Mọi yêu cầu được ghi nhật ký kiểm toán.
	Super bool
	// Expires là lúc token hết hạn (claim exp). Zero: không giới hạn. Dùng cho kết nối sống lâu như websocket.
	Expires time.Time
}

type ctxKey struct{}

// WithPrincipal gắn Principal vào context.
func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}

// PrincipalFrom lấy Principal từ context.
func PrincipalFrom(ctx context.Context) (*Principal, bool) {
	p, ok := ctx.Value(ctxKey{}).(*Principal)
	return p, ok && p != nil
}
