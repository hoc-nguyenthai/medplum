package domain

import "trivita-fhir/pkg/fhir"

// Phương thức xác thực của một lần đăng nhập.
const (
	AuthPassword = "password"
	AuthGoogle   = "google"
	AuthExternal = "external"
	AuthExchange = "exchange"
	AuthClient   = "client"
)

// LoginRequest là yêu cầu đăng nhập (oauth/utils.ts:76). P2 chỉ dùng nhóm mật khẩu; các trường của P6 (ExternalID, MembershipID,
// GoogleCredentials, ForceUseFirstMembership) có sẵn để chữ ký không đổi khi nối đăng nhập ngoài.
type LoginRequest struct {
	Email        string
	ExternalID   string
	AuthMethod   string
	Password     string
	Scope        string
	Nonce        string
	ResourceType string // loại profile muốn chọn (Patient, Practitioner...)
	ProjectID    string
	MembershipID string
	ClientID     string
	LaunchID     string

	CodeChallenge       string
	CodeChallengeMethod string
	RedirectURI         string

	RemoteAddress string
	UserAgent     string
	Origin        string
	PictureURL    string

	HasGoogleCredentials    bool
	AllowNoMembership       bool
	ForceUseFirstMembership bool
	Remember                bool
}

// ValidateLoginRequest kiểm tra yêu cầu theo đúng chuỗi else-if của Medplum (oauth/utils.ts:258); mỗi lỗi là 400 kèm đường dẫn trường.
func ValidateLoginRequest(r LoginRequest) error {
	if r.ProjectID != "" && r.ProjectID != "new" && !IsUUID(r.ProjectID) {
		return BadRequestAt("Invalid projectId", "projectId")
	}
	switch {
	case r.AuthMethod == AuthExternal || r.AuthMethod == AuthExchange:
		if r.ExternalID == "" && r.Email == "" {
			return BadRequestAt("Missing email or externalId", "externalId")
		}
		if r.ExternalID != "" && r.ProjectID == "" {
			return BadRequestAt("Project ID is required for external ID", "projectId")
		}
	case r.Email == "":
		return BadRequestAt("Invalid email", "email")
	case r.AuthMethod == "":
		return BadRequestAt("Invalid authentication method", "authMethod")
	case r.AuthMethod == AuthPassword && r.Password == "":
		return BadRequestAt("Invalid password", "password")
	case r.AuthMethod == AuthGoogle && !r.HasGoogleCredentials:
		return BadRequestAt("Invalid google credentials", "googleCredentials")
	case r.Scope == "":
		return BadRequestAt("Invalid scope", "scope")
	}
	return nil
}

// ValidatePKCE kiểm tra code challenge (oauth/utils.ts:281). Client `pkceOptional` được bỏ qua. Chỉ chấp nhận plain và S256.
func ValidatePKCE(r LoginRequest, client *fhir.ClientApplication) error {
	if client != nil && IsTrue(client.PkceOptional) {
		return nil
	}
	if r.CodeChallenge == "" && r.CodeChallengeMethod != "" {
		return BadRequestAt("Invalid code challenge", "code_challenge")
	}
	if r.CodeChallenge != "" && r.CodeChallengeMethod == "" {
		return BadRequestAt("Invalid code challenge method", "code_challenge_method")
	}
	if r.CodeChallengeMethod != "" && r.CodeChallengeMethod != "plain" && r.CodeChallengeMethod != "S256" {
		return BadRequestAt("Invalid code challenge method", "code_challenge_method")
	}
	return nil
}

// OriginAllowed: client có allowedOrigin (kể cả rỗng, như Medplum) và yêu cầu có Origin thì Origin phải nằm trong danh sách.
func OriginAllowed(client *fhir.ClientApplication, origin string) bool {
	if client == nil || client.AllowedOrigin == nil || origin == "" {
		return true
	}
	for _, o := range client.AllowedOrigin {
		if o == origin {
			return true
		}
	}
	return false
}
