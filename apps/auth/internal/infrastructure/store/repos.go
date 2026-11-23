package store

import (
	"context"
	"time"

	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirrouter"
	"trivita-fhir/pkg/fhirsearch"
)

// ---- User

type users struct{ s *Store }

func (r users) findOne(ctx context.Context, filters ...fhirsearch.Filter) (*fhir.User, error) {
	found, err := searchAll(ctx, r.s, "User", filters, 1)
	if err != nil || len(found) == 0 {
		return nil, err
	}
	u, _ := found[0].(*fhir.User)
	return u, nil
}

func (r users) FindInProject(ctx context.Context, email, projectID string) (*fhir.User, error) {
	return r.findOne(ctx, filter("User", "email", "exact", email), filter("User", "project", "", "Project/"+projectID))
}

func (r users) FindGlobal(ctx context.Context, email string) (*fhir.User, error) {
	return r.findOne(ctx, filter("User", "email", "exact", email), filter("User", "project", "missing", "true"))
}

func (r users) Get(ctx context.Context, id string) (*fhir.User, error) {
	return get[*fhir.User](ctx, r.s, "User", id)
}

func (r users) Mutate(ctx context.Context, id string, fn func(*fhir.User) error) (*fhir.User, error) {
	return mutate(ctx, r.s, "User", id, fn)
}

func (r users) Delete(ctx context.Context, id string) error {
	return mapErr(r.s.repo.DeleteResource(ctx, "User", id))
}

// ---- Login

type logins struct{ s *Store }

func (r logins) Create(ctx context.Context, l *fhir.Login) (*fhir.Login, error) {
	l.ResourceType = "Login"
	return create(ctx, r.s, l)
}

func (r logins) Get(ctx context.Context, id string) (*fhir.Login, error) {
	return get[*fhir.Login](ctx, r.s, "Login", id)
}

func (r logins) FindByPreAuthorizedCodeHash(ctx context.Context, hash string) (*fhir.Login, error) {
	found, err := searchAll(ctx, r.s, "Login", []fhirsearch.Filter{filter("Login", "pre-authorized-code-hash", "", hash)}, 1)
	if err != nil || len(found) == 0 {
		return nil, err
	}
	l, _ := found[0].(*fhir.Login)
	return l, nil
}

func (r logins) FindByCookie(ctx context.Context, cookieHash string) (*fhir.Login, error) {
	found, err := searchAll(ctx, r.s, "Login", []fhirsearch.Filter{filter("Login", "cookie", "", cookieHash)}, 1)
	if err != nil || len(found) == 0 {
		return nil, err
	}
	l, _ := found[0].(*fhir.Login)
	return l, nil
}

func (r logins) FindByCode(ctx context.Context, codeHash string) (*fhir.Login, error) {
	found, err := searchAll(ctx, r.s, "Login", []fhirsearch.Filter{filter("Login", "code", "", codeHash)}, 1)
	if err != nil || len(found) == 0 {
		return nil, err
	}
	l, _ := found[0].(*fhir.Login)
	return l, nil
}

func (r logins) UpdateIfMatch(ctx context.Context, l *fhir.Login, versionID string) (*fhir.Login, error) {
	return updateIfMatch(ctx, r.s, l, versionID)
}

func (r logins) Mutate(ctx context.Context, id string, fn func(*fhir.Login) error) (*fhir.Login, error) {
	return mutate(ctx, r.s, "Login", id, fn)
}

func (r logins) ListIDsByUser(ctx context.Context, userRef string) ([]string, error) {
	found, err := searchAll(ctx, r.s, "Login", []fhirsearch.Filter{filter("Login", "user", "", userRef)}, 0)
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(found))
	for i, f := range found {
		ids[i] = f.GetID()
	}
	return ids, nil
}

func (r logins) ListByUserSince(ctx context.Context, userRef string, since time.Time) ([]fhir.Login, error) {
	found, err := searchAll(ctx, r.s, "Login", []fhirsearch.Filter{
		filter("Login", "user", "", userRef), filter("Login", "_lastUpdated", "", timeFilterValue("gt", since)),
	}, 0)
	if err != nil {
		return nil, err
	}
	out := make([]fhir.Login, 0, len(found))
	for _, f := range found {
		if l, ok := f.(*fhir.Login); ok {
			out = append(out, *l)
		}
	}
	return out, nil
}

func (r logins) ListIDsUpdatedBefore(ctx context.Context, before time.Time) ([]string, error) {
	found, err := searchAll(ctx, r.s, "Login", []fhirsearch.Filter{filter("Login", "_lastUpdated", "", timeFilterValue("lt", before))}, 0)
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(found))
	for i, f := range found {
		ids[i] = f.GetID()
	}
	return ids, nil
}

// Expunge xóa vĩnh viễn một Login. Repository nền không hỗ trợ xóa vĩnh viễn (ví dụ bộ nhớ) thì lỗi, không âm thầm bỏ qua.
func (r logins) Expunge(ctx context.Context, id string) error {
	ex, ok := r.s.repo.(fhirrouter.Expunger)
	if !ok {
		return domain.Internal("kho dữ liệu không hỗ trợ xóa vĩnh viễn")
	}
	return mapErr(ex.Expunge(ctx, "Login", id))
}

// ---- tạo resource bất kỳ

type resources struct{ s *Store }

func (r resources) Create(ctx context.Context, res fhir.Resource) (fhir.Resource, error) {
	out, err := r.s.repo.CreateResource(ctx, res)
	if err != nil {
		return nil, mapErr(err)
	}
	return out, nil
}

func (r resources) Delete(ctx context.Context, resourceType, id string) error {
	return mapErr(r.s.repo.DeleteResource(ctx, resourceType, id))
}

func (r resources) Get(ctx context.Context, resourceType, id string) (fhir.Resource, error) {
	out, err := r.s.repo.ReadResource(ctx, resourceType, id)
	if err != nil {
		return nil, mapErr(err)
	}
	return out, nil
}

// ---- ProjectMembership

type memberships struct{ s *Store }

func (r memberships) FindByExternalID(ctx context.Context, externalID, projectRef string) (*fhir.ProjectMembership, error) {
	if externalID == "" || projectRef == "" {
		return nil, nil
	}
	found, err := searchAll(ctx, r.s, "ProjectMembership", []fhirsearch.Filter{
		filter("ProjectMembership", "external-id", "exact", externalID), filter("ProjectMembership", "project", "", projectRef),
	}, 0)
	if err != nil {
		return nil, err
	}
	for _, f := range found {
		if m, ok := f.(*fhir.ProjectMembership); ok && m.ExternalId == externalID && m.Project != nil && m.Project.Reference == projectRef {
			return m, nil
		}
	}
	return nil, nil
}

func (r memberships) FindByProfile(ctx context.Context, profileRef, projectRef string) (*fhir.ProjectMembership, error) {
	if profileRef == "" || projectRef == "" {
		return nil, nil
	}
	found, err := searchAll(ctx, r.s, "ProjectMembership", []fhirsearch.Filter{
		filter("ProjectMembership", "profile", "", profileRef), filter("ProjectMembership", "project", "", projectRef),
	}, 0)
	if err != nil {
		return nil, err
	}
	for _, f := range found {
		if m, ok := f.(*fhir.ProjectMembership); ok && m.Profile != nil && m.Profile.Reference == profileRef && m.Project != nil && m.Project.Reference == projectRef {
			return m, nil
		}
	}
	return nil, nil
}

func (r memberships) Mutate(ctx context.Context, id string, fn func(*fhir.ProjectMembership) error) (*fhir.ProjectMembership, error) {
	return mutate(ctx, r.s, "ProjectMembership", id, fn)
}

func (r memberships) Delete(ctx context.Context, id string) error {
	return mapErr(r.s.repo.DeleteResource(ctx, "ProjectMembership", id))
}

func (r memberships) collect(ctx context.Context, limit int, filters ...fhirsearch.Filter) ([]fhir.ProjectMembership, error) {
	found, err := searchAll(ctx, r.s, "ProjectMembership", filters, limit)
	if err != nil {
		return nil, err
	}
	out := make([]fhir.ProjectMembership, 0, len(found))
	for _, f := range found {
		if m, ok := f.(*fhir.ProjectMembership); ok {
			out = append(out, *m)
		}
	}
	return out, nil
}

func (r memberships) ListAllForUser(ctx context.Context, userRef string, limit int) ([]fhir.ProjectMembership, error) {
	return r.collect(ctx, limit, filter("ProjectMembership", "user", "", userRef))
}

func (r memberships) ListByProject(ctx context.Context, projectRef string) ([]fhir.ProjectMembership, error) {
	all, err := r.collect(ctx, 0, filter("ProjectMembership", "project", "", projectRef))
	if err != nil {
		return nil, err
	}
	out := all[:0]
	for _, m := range all {
		if m.Project != nil && m.Project.Reference == projectRef {
			out = append(out, m)
		}
	}
	return out, nil
}

func (r memberships) ListActiveAdmins(ctx context.Context, projectRef string) ([]fhir.ProjectMembership, error) {
	all, err := r.collect(ctx, 0, filter("ProjectMembership", "project", "", projectRef))
	if err != nil {
		return nil, err
	}
	var out []fhir.ProjectMembership
	for _, m := range all {
		if domain.IsTrue(m.Admin) && (m.Active == nil || *m.Active) && m.Project != nil && m.Project.Reference == projectRef {
			out = append(out, m)
		}
	}
	return out, nil
}

func (r memberships) Get(ctx context.Context, id string) (*fhir.ProjectMembership, error) {
	return get[*fhir.ProjectMembership](ctx, r.s, "ProjectMembership", id)
}

func (r memberships) ListForUser(ctx context.Context, userRef, projectRef string, limit int) ([]fhir.ProjectMembership, error) {
	filters := []fhirsearch.Filter{
		filter("ProjectMembership", "user", "", userRef),
		filter("ProjectMembership", "active", "not", "false"),
	}
	if projectRef != "" {
		filters = append(filters, filter("ProjectMembership", "project", "", projectRef))
	}
	found, err := searchAll(ctx, r.s, "ProjectMembership", filters, limit)
	if err != nil {
		return nil, err
	}
	out := make([]fhir.ProjectMembership, 0, len(found))
	for _, f := range found {
		if m, ok := f.(*fhir.ProjectMembership); ok {
			out = append(out, *m)
		}
	}
	return out, nil
}

// ---- đọc theo id

type projects struct{ s *Store }

func (r projects) Get(ctx context.Context, id string) (*fhir.Project, error) {
	return get[*fhir.Project](ctx, r.s, "Project", id)
}

func (r projects) Mutate(ctx context.Context, id string, fn func(*fhir.Project) error) (*fhir.Project, error) {
	return mutate(ctx, r.s, "Project", id, fn)
}

func (r projects) HasOwnedBy(ctx context.Context, userRef string) (bool, error) {
	found, err := searchAll(ctx, r.s, "Project", []fhirsearch.Filter{filter("Project", "owner", "", userRef)}, 1)
	return len(found) > 0, err
}

func (r projects) ByGoogleClientID(ctx context.Context, googleClientID, projectID string) ([]fhir.Project, error) {
	found, err := searchAll(ctx, r.s, "Project", []fhirsearch.Filter{filter("Project", "google-client-id", "", googleClientID)}, 0)
	if err != nil {
		return nil, err
	}
	var out []fhir.Project
	for _, f := range found {
		p, ok := f.(*fhir.Project)
		if !ok || (projectID != "" && p.ID != projectID) {
			continue
		}
		for _, site := range p.Site {
			if site.GoogleClientId == googleClientID {
				out = append(out, *p)
				break
			}
		}
	}
	return out, nil
}

func (r projects) FindByRecaptchaSiteKey(ctx context.Context, siteKey, projectID string) (*fhir.Project, error) {
	found, err := searchAll(ctx, r.s, "Project", []fhirsearch.Filter{filter("Project", "recaptcha-site-key", "", siteKey)}, 0)
	if err != nil {
		return nil, err
	}
	for _, f := range found {
		if p, ok := f.(*fhir.Project); ok && (projectID == "" || p.ID == projectID) {
			return p, nil
		}
	}
	return nil, nil
}

type clients struct{ s *Store }

func (r clients) Get(ctx context.Context, id string) (*fhir.ClientApplication, error) {
	return get[*fhir.ClientApplication](ctx, r.s, "ClientApplication", id)
}

type launches struct{ s *Store }

func (r launches) Get(ctx context.Context, id string) (*fhir.SmartAppLaunch, error) {
	return get[*fhir.SmartAppLaunch](ctx, r.s, "SmartAppLaunch", id)
}

type accessPolicies struct{ s *Store }

func (r accessPolicies) Get(ctx context.Context, id string) (*fhir.AccessPolicy, error) {
	return get[*fhir.AccessPolicy](ctx, r.s, "AccessPolicy", id)
}

// ---- UserSecurityRequest

type securityRequests struct{ s *Store }

func (r securityRequests) Get(ctx context.Context, id string) (*fhir.UserSecurityRequest, error) {
	return get[*fhir.UserSecurityRequest](ctx, r.s, "UserSecurityRequest", id)
}

func (r securityRequests) MarkUsedIfMatch(ctx context.Context, sr *fhir.UserSecurityRequest, versionID string) error {
	if versionID == "" {
		return domain.Internal("UserSecurityRequest thiếu versionId, không thể tiêu thụ có điều kiện")
	}
	updated := *sr
	t := true
	updated.Used = &t
	_, err := updateIfMatch(ctx, r.s, &updated, versionID)
	return err
}

func (r securityRequests) MarkUsed(ctx context.Context, id string) error {
	_, err := mutate(ctx, r.s, "UserSecurityRequest", id, func(sr *fhir.UserSecurityRequest) error {
		t := true
		sr.Used = &t
		return nil
	})
	return err
}

func (r securityRequests) ListSince(ctx context.Context, userRef string, since time.Time) ([]fhir.UserSecurityRequest, error) {
	found, err := searchAll(ctx, r.s, "UserSecurityRequest", []fhirsearch.Filter{
		filter("UserSecurityRequest", "user", "", userRef),
		filter("UserSecurityRequest", "_lastUpdated", "", timeFilterValue("ge", since)),
	}, 0)
	if err != nil {
		return nil, err
	}
	out := make([]fhir.UserSecurityRequest, 0, len(found))
	for _, f := range found {
		if sr, ok := f.(*fhir.UserSecurityRequest); ok {
			out = append(out, *sr)
		}
	}
	return out, nil
}

// ---- DomainConfiguration, UserConfiguration

type domainConfigs struct{ s *Store }

func (r domainConfigs) FindByDomain(ctx context.Context, domainName string) (*fhir.DomainConfiguration, error) {
	found, err := searchAll(ctx, r.s, "DomainConfiguration", []fhirsearch.Filter{filter("DomainConfiguration", "domain", "", domainName)}, 1)
	if err != nil || len(found) == 0 {
		return nil, err
	}
	c, _ := found[0].(*fhir.DomainConfiguration)
	return c, nil
}

type userConfigs struct{ s *Store }

func (r userConfigs) Get(ctx context.Context, id string) (*fhir.UserConfiguration, error) {
	return get[*fhir.UserConfiguration](ctx, r.s, "UserConfiguration", id)
}
