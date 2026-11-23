// Package infrastructure chứa adapter cụ thể cho các cổng của application: store (kho trên pkg/fhirstore), password
// (bcrypt), pwned (kiểm tra mật khẩu rò rỉ), và các adapter sau này (TOTP, thư, JWT...). Là nơi duy nhất được dùng thư viện
// bên thứ ba tùy ý (xem architecture_test.go).
package infrastructure
