package application

import (
	"context"
	"strings"

	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
)

// maxMemberships là giới hạn số membership đọc cho một login (Medplum: count=100, cắt im lặng).
const maxMemberships = 100

// GetClientApplication tìm client dựng sẵn trước, không có thì đọc ClientApplication theo id (không có thì NotFound).
func (s *Service) GetClientApplication(ctx context.Context, id string) (*fhir.ClientApplication, error) {
	for i := range s.StandardClients {
		if s.StandardClients[i].ID == id {
			c := s.StandardClients[i]
			return &c, nil
		}
	}
	return s.Clients.Get(ctx, id)
}

// ResolveProjectID: có clientId thì project là project của client, và nếu người gọi cũng gửi projectId thì phải khớp
// (không thì 400 "Invalid projectId"); không có clientId thì trả nguyên projectId (auth/utils.ts:586).
func (s *Service) ResolveProjectID(ctx context.Context, clientID, projectID string) (string, error) {
	if clientID == "" {
		return projectID, nil
	}
	client, err := s.GetClientApplication(ctx, clientID)
	if err != nil {
		return "", err
	}
	clientProject := ""
	if client.Meta != nil {
		clientProject = client.Meta.Project
	}
	if projectID != "" && projectID != clientProject {
		return "", domain.BadRequest("Invalid projectId")
	}
	return clientProject, nil
}

// FindUserByEmail dùng cho ĐĂNG NHẬP (oauth/utils.ts:908): có projectId (khác "new") thì thử user gắn project đó trước; không có thì
// RƠI VỀ user không gắn project nào (user toàn cục đăng nhập được qua client của project). Email được hạ chữ thường; nil nếu không có.
func (s *Service) FindUserByEmail(ctx context.Context, email, projectID string) (*fhir.User, error) {
	email = strings.ToLower(email)
	if projectID != "" && projectID != "new" {
		if u, err := s.Users.FindInProject(ctx, email, projectID); err != nil || u != nil {
			return u, err
		}
	}
	return s.Users.FindGlobal(ctx, email)
}

// FindUserInScope tra user CHỈ trong đúng phạm vi: có projectId (khác "new") thì chỉ user gắn project đó, không thì chỉ user toàn
// cục, không rơi về phạm vi khác. Dùng để kiểm tra trùng email khi đăng ký và cho các lệnh quản trị.
func (s *Service) FindUserInScope(ctx context.Context, email, projectID string) (*fhir.User, error) {
	email = strings.ToLower(email)
	if projectID != "" && projectID != "new" {
		return s.Users.FindInProject(ctx, email, projectID)
	}
	return s.Users.FindGlobal(ctx, email)
}

// LoginProject đọc project của login; nil nếu login chưa có project cụ thể ("Project/new" hoặc rỗng) hoặc đọc lỗi.
func (s *Service) LoginProject(ctx context.Context, login *fhir.Login) *fhir.Project {
	ref := refOf(login.Project)
	if ref == "" || ref == "Project/new" {
		return nil
	}
	p, err := s.Projects.Get(ctx, idOf(ref))
	if err != nil {
		return nil
	}
	return p
}

// MembershipsForLogin liệt kê membership mà login có thể chọn (oauth/utils.ts:423).
func (s *Service) MembershipsForLogin(ctx context.Context, login *fhir.Login) ([]fhir.ProjectMembership, error) {
	if refOf(login.Project) == "Project/new" {
		return nil, nil
	}
	userRef := refOf(login.User)
	if userRef == "" {
		return nil, domain.BadRequest("User reference is missing")
	}
	memberships, err := s.Memberships.ListForUser(ctx, userRef, refOf(login.Project), maxMemberships)
	if err != nil {
		return nil, err
	}
	if t := login.ProfileType; t != "" {
		var kept []fhir.ProjectMembership
		for _, m := range memberships {
			if strings.HasPrefix(refOf(m.Profile), t) {
				kept = append(kept, m)
			}
		}
		memberships = kept
	}
	var active []fhir.ProjectMembership
	for _, m := range memberships {
		project, perr := s.Projects.Get(ctx, idOf(refOf(m.Project)))
		// Membership được giữ khi project không đọc được (lỗi) hoặc đang hoạt động.
		if perr != nil || domain.InactiveReason(project, nil) == "" {
			active = append(active, m)
		}
	}
	if len(memberships) > 0 && len(active) == 0 {
		return nil, domain.BadRequest(domain.ProjectNotActive)
	}
	return active, nil
}

func refOf(r *fhir.Reference) string {
	if r == nil {
		return ""
	}
	return r.Reference
}

// idOf lấy phần id của tham chiếu "Type/id" (hoặc nguyên chuỗi nếu không có "/").
func idOf(ref string) string {
	if i := strings.LastIndex(ref, "/"); i >= 0 {
		return ref[i+1:]
	}
	return ref
}

// newRef dựng tham chiếu Type/id kèm display (createReference của Medplum đặt display theo resource).
func newRef(resourceType, id, display string) *fhir.Reference {
	return &fhir.Reference{Reference: resourceType + "/" + id, Display: display}
}
