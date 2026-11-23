package domain

import (
	"strings"
	"time"

	"trivita-fhir/pkg/fhir"
)

// Phương thức MFA.
const (
	MFATotp  = "totp"
	MFAEmail = "email"
)

// IsMFARequired: project bắt buộc MFA (setting `mfaRequired`) hoặc user tự bắt buộc (auth/utils.ts:205).
func IsMFARequired(user *fhir.User, project *fhir.Project) bool {
	if s := ProjectSetting(project, "mfaRequired"); s != nil && IsTrue(s.ValueBoolean) {
		return true
	}
	return user != nil && IsTrue(user.MfaRequired)
}

// AllowedMFAMethods đọc setting `allowedMfaMethods` (chuỗi phân tách bằng dấu phẩy). Chưa đặt hoặc không có giá trị hợp lệ
// thì chỉ cho phép totp (hành vi lịch sử, auth/utils.ts:114).
func AllowedMFAMethods(project *fhir.Project) []string {
	s := ProjectSetting(project, "allowedMfaMethods")
	if s == nil || s.ValueString == "" {
		return []string{MFATotp}
	}
	var out []string
	for _, part := range strings.Split(s.ValueString, ",") {
		if m := strings.TrimSpace(part); m == MFATotp || m == MFAEmail {
			out = append(out, m)
		}
	}
	if len(out) == 0 {
		return []string{MFATotp}
	}
	return out
}

// EnrolledMFAMethods: danh sách `mfaMethod`; rỗng mà `mfaEnrolled` thì coi là totp (tương thích ngược); còn lại rỗng.
func EnrolledMFAMethods(user *fhir.User) []string {
	if user == nil {
		return nil
	}
	if len(user.MfaMethod) > 0 {
		return user.MfaMethod
	}
	if IsTrue(user.MfaEnrolled) {
		return []string{MFATotp}
	}
	return nil
}

// Hằng số MFA (constants.ts:37-53). Các giá trị này là mặc định; cấu hình ghi đè được.
const (
	DefaultMFALoginExpiration      = 10 * time.Minute // tuổi tối đa của login chờ MFA và cửa sổ đếm lần thử
	DefaultMFALoginAttemptLimit    = 5
	DefaultMFAUserAttemptLimit     = 20
	DefaultMFAEmailCodeExpiration  = 20 * time.Minute
	DefaultMFAEnrollmentExpiration = 20 * time.Minute
	DefaultMFAWindow               = 1 // số bước TOTP chấp nhận mỗi bên
	// TOTPStep là độ dài một bước TOTP (RFC 6238).
	TOTPStep = 30 * time.Second
)

// ParseMFAMethod: chỉ "email" thành email, mọi giá trị khác (kể cả thiếu hoặc sai kiểu) thành totp (auth/mfa.ts:43).
func ParseMFAMethod(v any) string {
	if s, ok := v.(string); ok && s == MFAEmail {
		return MFAEmail
	}
	return MFATotp
}

// AddMFAMethod trả hợp của phương thức hiện có và m, giữ thứ tự (auth/utils.ts:297).
func AddMFAMethod(user *fhir.User, m string) []string {
	cur := EnrolledMFAMethods(user)
	for _, x := range cur {
		if x == m {
			return append([]string(nil), cur...)
		}
	}
	return append(append([]string(nil), cur...), m)
}

// HasMFAMethod cho biết user đã đăng ký phương thức m.
func HasMFAMethod(user *fhir.User, m string) bool {
	for _, x := range EnrolledMFAMethods(user) {
		if x == m {
			return true
		}
	}
	return false
}

// MFAMethodAllowed cho biết project cho phép phương thức m (project rỗng: chỉ totp, như Medplum khi login chưa có project cụ thể).
func MFAMethodAllowed(project *fhir.Project, m string) bool {
	for _, x := range AllowedMFAMethods(project) {
		if x == m {
			return true
		}
	}
	return false
}

// AssertMFALoginActive kiểm login còn dùng được để xác minh MFA, theo đúng thứ tự thông báo của Medplum (mfalimit.ts:152): đã thu hồi, đã cấp code,
// đã xác minh, không phải đăng nhập mật khẩu, quá hạn (authTime không đọc được cũng là quá hạn). Mọi lỗi là 400.
func AssertMFALoginActive(login *fhir.Login, now time.Time, expiration time.Duration) error {
	switch {
	case IsTrue(login.Revoked):
		return BadRequest("Login revoked")
	case IsTrue(login.Granted):
		return BadRequest("Login granted")
	case IsTrue(login.MfaVerified):
		return BadRequest("Login already verified")
	case login.AuthMethod != AuthPassword:
		return BadRequest("Invalid login authentication method")
	}
	t, ok := parseTime(login.AuthTime)
	if !ok || now.Sub(t) >= expiration {
		return BadRequest("Login expired")
	}
	return nil
}

// MFAAppName là tên hiển thị trong thư và nhãn TOTP: Project.setting[appName] đã cắt khoảng trắng, rỗng thì dùng fallback.
func MFAAppName(project *fhir.Project, fallback string) string {
	if s := ProjectSetting(project, "appName"); s != nil {
		if v := strings.TrimSpace(s.ValueString); v != "" {
			return v
		}
	}
	return fallback
}

// MFAIssuer là issuer trong URI otpauth: tên ứng dụng bỏ mọi dấu ":" (ứng dụng xác thực tách nhãn ở dấu ":" đầu tiên).
func MFAIssuer(project *fhir.Project, fallback string) string {
	return strings.ReplaceAll(MFAAppName(project, fallback), ":", "")
}
