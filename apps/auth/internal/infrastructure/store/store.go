// Package store cài đặt các kho của application trên fhirrouter.Repository (pkg/fhirstore cho SQL, MemoryRepository cho test).
// Dữ liệu của auth (User, Login, UserSecurityRequest... chứa bí mật) phải nằm trong SCHEMA RIÊNG (mô hình một database nhiều schema, role riêng không có quyền trên schema khác; docs/plans/microservices-shared-db.md) hoặc database riêng, không lẫn với
// kho lâm sàng của fhir-server.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"trivita-fhir/apps/auth/internal/application"
	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirrouter"
	"trivita-fhir/pkg/fhirsearch"
)

// maxMutateAttempts là số lần thử lại tối đa của Mutate khi gặp xung đột phiên bản.
const maxMutateAttempts = 8

// pageSize là số kết quả mỗi trang khi duyệt hết một truy vấn.
var pageSize = fhirsearch.MaxCount

// Store là lớp dùng chung của các kho: giữ repository nền và các hàm hỗ trợ.
type Store struct{ repo fhirrouter.Repository }

// New dựng Store trên repository nền.
func New(repo fhirrouter.Repository) *Store { return &Store{repo: repo} }

// Repos gom mọi kho của application, để lắp vào application.Deps.
type Repos struct {
	Users            application.UserRepository
	Logins           application.LoginRepository
	Memberships      application.MembershipRepository
	Projects         application.ProjectRepository
	Clients          application.ClientRepository
	Launches         application.LaunchRepository
	AccessPolicies   application.AccessPolicyRepository
	SecurityRequests application.SecurityRequestRepository
	DomainConfigs    application.DomainConfigRepository
	UserConfigs      application.UserConfigurationRepository
	Resources        application.ResourceWriter
	Tx               application.Transactor
}

// Repos trả mọi kho gắn với Store.
func (s *Store) Repos() Repos {
	return Repos{
		Users: users{s}, Logins: logins{s}, Memberships: memberships{s}, Projects: projects{s}, Clients: clients{s},
		Launches: launches{s}, AccessPolicies: accessPolicies{s}, SecurityRequests: securityRequests{s}, DomainConfigs: domainConfigs{s}, UserConfigs: userConfigs{s}, Resources: resources{s}, Tx: s,
	}
}

// InTx chạy fn trong transaction nếu repository nền hỗ trợ; nếu không thì chạy thẳng (bộ nhớ một writer).
func (s *Store) InTx(ctx context.Context, fn func(application.TxRepos) error) error {
	run := func(st *Store) error {
		return fn(application.TxRepos{Users: users{st}, SecurityRequests: securityRequests{st}, Resources: resources{st}, Locker: locker{st}, Memberships: memberships{st}, Projects: projects{st}, Logins: logins{st}})
	}
	tr, ok := s.repo.(fhirrouter.Transactional)
	if !ok {
		return run(s)
	}
	return tr.RunInTransaction(ctx, func(r fhirrouter.Repository) error { return run(New(r)) })
}

// mapErr đổi lỗi của fhirrouter thành lỗi miền: 404/410 là NotFound, 412 là ErrVersionConflict, còn lại là Internal
// (không lộ chi tiết; ghi log ở nơi gọi). Lỗi miền đi qua nguyên vẹn.
func mapErr(err error) error {
	if err == nil {
		return nil
	}
	var de *domain.Error
	if errors.As(err, &de) || errors.Is(err, domain.ErrVersionConflict) || errors.Is(err, application.ErrNoChange) {
		return err
	}
	var fe *fhirrouter.Error
	if errors.As(err, &fe) {
		switch fe.Status {
		case 404, 410:
			return domain.NotFound()
		case 412:
			return domain.ErrVersionConflict
		}
		return domain.InternalErr("kho dữ liệu", fe)
	}
	return domain.InternalErr("kho dữ liệu", err)
}

func versionOf(r fhir.Resource) string {
	if m := r.GetMeta(); m != nil {
		return m.VersionId
	}
	return ""
}

// get đọc một resource và ép về kiểu T; kiểu khác (không xảy ra với id đúng loại) là lỗi nội bộ.
func get[T fhir.Resource](ctx context.Context, s *Store, resourceType, id string) (T, error) {
	var zero T
	r, err := s.repo.ReadResource(ctx, resourceType, id)
	if err != nil {
		return zero, mapErr(err)
	}
	t, ok := r.(T)
	if !ok {
		return zero, domain.Internal(fmt.Sprintf("kho trả %T cho %s/%s", r, resourceType, id))
	}
	return t, nil
}

// create ghi resource mới.
func create[T fhir.Resource](ctx context.Context, s *Store, r T) (T, error) {
	var zero T
	out, err := s.repo.CreateResource(ctx, r)
	if err != nil {
		return zero, mapErr(err)
	}
	t, ok := out.(T)
	if !ok {
		return zero, domain.Internal(fmt.Sprintf("kho trả %T khi tạo %s", out, r.GetResourceType()))
	}
	return t, nil
}

// updateIfMatch ghi r kèm điều kiện phiên bản. versionID rỗng thì ghi không điều kiện.
func updateIfMatch[T fhir.Resource](ctx context.Context, s *Store, r T, versionID string) (T, error) {
	var zero T
	out, err := s.repo.UpdateResource(ctx, r, fhirrouter.UpdateOptions{IfMatch: versionID})
	if err != nil {
		return zero, mapErr(err)
	}
	t, ok := out.(T)
	if !ok {
		return zero, domain.Internal(fmt.Sprintf("kho trả %T khi cập nhật %s", out, r.GetResourceType()))
	}
	return t, nil
}

// mutate đọc bản mới nhất, áp fn rồi ghi kèm IfMatch; gặp xung đột thì đọc lại và thử lại. fn trả application.ErrNoChange
// để không ghi. Không bao giờ ghi lại một bản cũ của người gọi (thay cho patchResource của Medplum).
func mutate[T fhir.Resource](ctx context.Context, s *Store, resourceType, id string, fn func(T) error) (T, error) {
	var zero T
	for range maxMutateAttempts {
		cur, err := get[T](ctx, s, resourceType, id)
		if err != nil {
			return zero, err
		}
		if err := fn(cur); err != nil {
			if errors.Is(err, application.ErrNoChange) {
				return cur, nil
			}
			return zero, err
		}
		out, err := updateIfMatch(ctx, s, cur, versionOf(cur))
		if errors.Is(err, domain.ErrVersionConflict) {
			continue
		}
		return out, err
	}
	return zero, domain.Conflict("Resource đang bị sửa đồng thời, thử lại sau")
}

// ---- truy vấn

// filter dựng thẳng fhirsearch.Filter (không đi qua cú pháp query) nên giá trị chứa `,` `|` `\` `$` không bị hiểu là cú
// pháp search. Panic nếu tham số không tồn tại (lỗi lập trình, được test bắt).
func filter(resourceType, code, modifier string, values ...string) fhirsearch.Filter {
	for _, d := range fhir.SearchParams(resourceType) {
		if d.Code == code {
			return fhirsearch.Filter{Code: code, Def: d, Modifier: modifier, Values: values}
		}
	}
	panic("store: không có search parameter " + resourceType + "." + code)
}

// searchAll duyệt hết các trang của một truy vấn.
func searchAll(ctx context.Context, s *Store, resourceType string, filters []fhirsearch.Filter, limit int) ([]fhir.Resource, error) {
	var out []fhir.Resource
	for offset := 0; ; offset += pageSize {
		count := pageSize
		if limit > 0 && limit-len(out) < count {
			count = limit - len(out)
		}
		res, err := s.repo.Search(ctx, &fhirsearch.Request{
			ResourceType: resourceType, Filters: filters, Count: count, Offset: offset, Total: "none",
		})
		if err != nil {
			return nil, mapErr(err)
		}
		out = append(out, res.Matches...)
		if !res.More || (limit > 0 && len(out) >= limit) || len(res.Matches) == 0 {
			return out, nil
		}
	}
}

func timeFilterValue(prefix string, t time.Time) string {
	return prefix + t.UTC().Format("2006-01-02T15:04:05.000Z")
}

// locker khóa theo key bằng KeyLocker của repository (PostgreSQL: advisory lock tới hết transaction). Repository không hỗ trợ thì lỗi,
// không bỏ qua: bỏ qua âm thầm làm "kiểm tra trùng rồi tạo" đua nhau.
type locker struct{ s *Store }

func (l locker) Lock(ctx context.Context, key string) error {
	kl, ok := l.s.repo.(fhirrouter.KeyLocker)
	if !ok {
		return domain.Internal("kho dữ liệu không hỗ trợ khóa theo key")
	}
	return mapErr(kl.LockKey(ctx, key))
}
