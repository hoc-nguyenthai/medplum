package domain

import "trivita-fhir/pkg/fhir"

// ResultKind là dạng phản hồi sau một bước đăng nhập (auth/utils.ts:474 sendLoginResult), xét theo thứ tự.
type ResultKind int

// Các dạng phản hồi.
const (
	ResultMFAEnroll     ResultKind = iota + 1 // phải đăng ký MFA trước
	ResultMFARequired                         // đã đăng ký MFA, chưa xác minh
	ResultNewProject                          // đang tạo project mới: chỉ trả login
	ResultCode                                // đã có membership: trả code
	ResultChooseProfile                       // nhiều profile: trả danh sách để chọn
)

// DecideLoginResult quyết định dạng phản hồi từ trạng thái user, project và login. Luật MFA chỉ áp cho đăng nhập bằng mật khẩu.
func DecideLoginResult(user *fhir.User, project *fhir.Project, login *fhir.Login) ResultKind {
	pwNoMFA := login.AuthMethod == AuthPassword && !IsTrue(login.MfaVerified)
	if IsMFARequired(user, project) && user != nil && !IsTrue(user.MfaEnrolled) && pwNoMFA {
		return ResultMFAEnroll
	}
	if user != nil && IsTrue(user.MfaEnrolled) && pwNoMFA {
		return ResultMFARequired
	}
	if login.Project != nil && login.Project.Reference == "Project/new" {
		return ResultNewProject
	}
	if login.Membership != nil {
		return ResultCode
	}
	return ResultChooseProfile
}

// CanManageUserAccount: chỉ login bằng mật khẩu hoặc Google được quản lý tài khoản (đổi mật khẩu, MFA, xem phiên).
func CanManageUserAccount(login *fhir.Login) bool {
	return login.AuthMethod == AuthPassword || login.AuthMethod == AuthGoogle
}
