package application

import (
	"context"
	"net/url"
	"regexp"
	"strings"

	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
)

// maxLaunchContext là số tham chiếu fhirContext tối đa của một launch.
const maxLaunchContext = 20

var launchRef = regexp.MustCompile(`^([A-Z][A-Za-z]{1,63})/([A-Za-z0-9\-.]{1,64})$`)

// SmartLaunchRequest là ngữ cảnh gắn vào launch (ClientApplication/$smart-launch).
type SmartLaunchRequest struct {
	Patient     string   // "Patient/<id>"
	Encounter   string   // "Encounter/<id>"
	FhirContext []string // tham chiếu "Type/<id>" khác
}

// SmartLaunchResult là kết quả tạo launch.
type SmartLaunchResult struct {
	LaunchID  string
	LaunchURL string // launchUri của client kèm `iss` và `launch`; ứng dụng mở URL này để bắt đầu EHR launch
}

func checkLaunchRef(ref, wantType, field string) error {
	m := launchRef.FindStringSubmatch(ref)
	if m == nil || (wantType != "" && m[1] != wantType) {
		msg := "Invalid reference"
		if wantType != "" {
			msg = field + " must be " + wantType + "/<id>"
		}
		return domain.BadRequestAt(msg, field)
	}
	return nil
}

// AdminSmartLaunch tạo SmartAppLaunch (bệnh nhân, lượt khám, ngữ cảnh FHIR khác) cho một client trong project của người gọi và trả URL khởi chạy (EHR launch): launchUri của
// client kèm `iss` (AUTH_FHIR_BASE_URL) và `launch` (id launch). Ứng dụng mở URL đó, rồi gọi /oauth2/authorize với `launch`; auth đọc lại launch khi phát token. Client ngoài
// project hoặc không tồn tại là 403 chung. Client phải có launchUri; phải có AUTH_FHIR_BASE_URL; phải có ít nhất một ngữ cảnh. Tham chiếu chỉ được kiểm DẠNG (Patient/<id>, Encounter/<id>,
// Type/<id>): auth không có đường đọc resource lâm sàng nên không kiểm resource tồn tại hay thuộc project. Launch không gắn client, không hết hạn và không bị dọn (SmartAppLaunch
// không có trường đó): id là ngẫu nhiên khó đoán, nhưng ai có id đều dùng được. Ghi log kiểm toán.
func (s *Service) AdminSmartLaunch(ctx context.Context, a *AuthState, projectID, clientID string, req SmartLaunchRequest) (*SmartLaunchResult, error) {
	p, err := adminProject(a, projectID)
	if err != nil {
		return nil, err
	}
	client, err := s.Clients.Get(ctx, clientID)
	if err != nil {
		if domain.KindOf(err) == domain.KindNotFound {
			return nil, errAdminForbidden
		}
		return nil, err
	}
	if client.Meta == nil || client.Meta.Project != p.ID {
		return nil, errAdminForbidden
	}
	if client.LaunchUri == "" {
		return nil, domain.BadRequestAt("Client application does not have a launch URI", "clientId")
	}
	base := strings.TrimRight(s.Tokens.FHIRBaseURL, "/")
	if base == "" {
		return nil, domain.BadRequest("AUTH_FHIR_BASE_URL is not configured")
	}
	if req.Patient == "" && req.Encounter == "" && len(req.FhirContext) == 0 {
		return nil, domain.BadRequest("patient, encounter or fhirContext is required")
	}
	if len(req.FhirContext) > maxLaunchContext {
		return nil, domain.BadRequestAt("Too many fhirContext references", "fhirContext")
	}
	l := &fhir.SmartAppLaunch{ResourceType: "SmartAppLaunch", ID: domain.NewID(), Meta: &fhir.Meta{Project: p.ID}}
	if req.Patient != "" {
		if err := checkLaunchRef(req.Patient, "Patient", "patient"); err != nil {
			return nil, err
		}
		l.Patient = &fhir.Reference{Reference: req.Patient}
	}
	if req.Encounter != "" {
		if err := checkLaunchRef(req.Encounter, "Encounter", "encounter"); err != nil {
			return nil, err
		}
		l.Encounter = &fhir.Reference{Reference: req.Encounter}
	}
	for _, r := range req.FhirContext {
		if err := checkLaunchRef(r, "", "fhirContext"); err != nil {
			return nil, err
		}
		l.FhirContext = append(l.FhirContext, fhir.Reference{Reference: r})
	}
	u, err := url.Parse(client.LaunchUri)
	if err != nil || !u.IsAbs() || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, domain.BadRequestAt("Client launch URI is invalid", "clientId")
	}
	if _, err := s.Resources.Create(ctx, l); err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("iss", base)
	q.Set("launch", l.ID)
	u.RawQuery = q.Encode()
	s.Logger.WarnContext(ctx, "tạo SmartAppLaunch", "audit", true, "actor", refOf(a.Membership.User), "project_id", p.ID, "client_id", clientID, "launch_id", l.ID)
	return &SmartLaunchResult{LaunchID: l.ID, LaunchURL: u.String()}, nil
}
