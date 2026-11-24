package fhirrouter

import (
	"context"

	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirsearch"
)

// Guard là decorator Repository giới hạn Binary kết quả của job (file export, đầu vào và kết quả của batch async) cho người tạo job: Binary do job
// tạo mang chủ sở hữu ở securityContext (xem BinaryOwner); mọi đường đọc (read, vread, history, tìm kiếm kể cả _include) và ghi (update, delete,
// expunge) của người gọi không được phép đều thấy 404 như Binary không tồn tại. Chỉ bọc repository phía người gọi: job nền giữ repository riêng nên
// không bị lọc. allowed(ctx, owner) quyết định (chủ sở hữu hoặc quản trị); Binary không có chủ (Binary thường) không bị ảnh hưởng.
type Guard struct {
	inner   Repository
	allowed func(ctx context.Context, owner string) bool
}

// NewGuard bọc inner. allowed nil: không lọc gì (trả inner).
func NewGuard(inner Repository, allowed func(ctx context.Context, owner string) bool) Repository {
	if allowed == nil {
		return inner
	}
	return &Guard{inner: inner, allowed: allowed}
}

var (
	_ Repository           = (*Guard)(nil)
	_ Transactional        = (*Guard)(nil)
	_ KeyLocker            = (*Guard)(nil)
	_ Reindexer            = (*Guard)(nil)
	_ Expunger             = (*Guard)(nil)
	_ ConceptStore         = (*Guard)(nil)
	_ MappingStore         = (*Guard)(nil)
	_ SubscriptionResender = (*Guard)(nil)
	_ AdminChecker         = (*Guard)(nil)
)

// hidden: resource là Binary của job mà người gọi không được thấy.
func (g *Guard) hidden(ctx context.Context, res fhir.Resource) bool {
	b, ok := res.(*fhir.Binary)
	if !ok {
		return false
	}
	owner := BinaryOwner(b)
	return owner != "" && !g.allowed(ctx, owner)
}

// hiddenStored: Binary đang lưu với id này là Binary của job mà người gọi không được thấy (đọc bản hiện tại bằng repository bên trong, không lọc).
// Lỗi đọc (không có, đã xóa) coi như không bị ẩn: để repository bên trong trả lỗi của nó.
func (g *Guard) hiddenStored(ctx context.Context, rt, id string) bool {
	if rt != "Binary" {
		return false
	}
	cur, err := g.inner.ReadResource(ctx, rt, id)
	return err == nil && g.hidden(ctx, cur)
}

func (g *Guard) CreateResource(ctx context.Context, r fhir.Resource) (fhir.Resource, error) {
	return g.inner.CreateResource(ctx, r)
}

func (g *Guard) ReadResource(ctx context.Context, rt, id string) (fhir.Resource, error) {
	res, err := g.inner.ReadResource(ctx, rt, id)
	if err != nil {
		return nil, err
	}
	if g.hidden(ctx, res) {
		return nil, NotFound()
	}
	return res, nil
}

func (g *Guard) ReadVersion(ctx context.Context, rt, id, vid string) (fhir.Resource, error) {
	res, err := g.inner.ReadVersion(ctx, rt, id, vid)
	if err != nil {
		return nil, err
	}
	if g.hidden(ctx, res) {
		return nil, NotFound()
	}
	return res, nil
}

func (g *Guard) ReadHistory(ctx context.Context, rt, id string) ([]HistoryEntry, error) {
	h, err := g.inner.ReadHistory(ctx, rt, id)
	if err != nil {
		return nil, err
	}
	if rt == "Binary" {
		for _, e := range h {
			if e.Resource != nil && g.hidden(ctx, e.Resource) {
				return nil, NotFound()
			}
		}
	}
	return h, nil
}

func (g *Guard) UpdateResource(ctx context.Context, r fhir.Resource, o UpdateOptions) (fhir.Resource, error) {
	if g.hiddenStored(ctx, r.GetResourceType(), r.GetID()) {
		return nil, NotFound()
	}
	return g.inner.UpdateResource(ctx, r, o)
}

func (g *Guard) DeleteResource(ctx context.Context, rt, id string) error {
	if g.hiddenStored(ctx, rt, id) {
		return NotFound()
	}
	return g.inner.DeleteResource(ctx, rt, id)
}

func (g *Guard) Search(ctx context.Context, req *fhirsearch.Request) (*SearchResult, error) {
	res, err := g.inner.Search(ctx, req)
	if err != nil || res == nil {
		return res, err
	}
	out := *res
	hid := 0
	filter := func(in []fhir.Resource, count bool) []fhir.Resource {
		var kept []fhir.Resource
		for _, x := range in {
			if g.hidden(ctx, x) {
				if count {
					hid++
				}
				continue
			}
			kept = append(kept, x)
		}
		return kept
	}
	if len(res.Matches) > 0 {
		out.Matches = filter(res.Matches, true)
	}
	if len(res.Included) > 0 {
		out.Included = filter(res.Included, false)
	}
	if hid > 0 && out.Total != nil {
		t := max(*out.Total-hid, 0)
		out.Total = &t
	}
	return &out, nil
}

// RunInTransaction chạy fn với repository của giao dịch cũng được bọc (GET entry trong Bundle đọc Binary của job cũng bị lọc).
func (g *Guard) RunInTransaction(ctx context.Context, fn func(Repository) error) error {
	tx, ok := g.inner.(Transactional)
	if !ok {
		return NotImplemented("Repository không hỗ trợ transaction")
	}
	return tx.RunInTransaction(ctx, func(rp Repository) error { return fn(&Guard{inner: rp, allowed: g.allowed}) })
}

// LockKey: không có repository tùy chọn thì không khóa (như router khi repository không cài KeyLocker).
func (g *Guard) LockKey(ctx context.Context, key string) error {
	if kl, ok := g.inner.(KeyLocker); ok {
		return kl.LockKey(ctx, key)
	}
	return nil
}

func (g *Guard) Reindex(ctx context.Context, rt, id string, progress func(int)) (int, error) {
	ri, ok := g.inner.(Reindexer)
	if !ok {
		return 0, NotImplemented("Repository không hỗ trợ reindex")
	}
	return ri.Reindex(ctx, rt, id, progress)
}

func (g *Guard) Expunge(ctx context.Context, rt, id string) error {
	ex, ok := g.inner.(Expunger)
	if !ok {
		return NotImplemented("Repository không hỗ trợ expunge")
	}
	if g.hiddenStored(ctx, rt, id) {
		return NotFound()
	}
	return ex.Expunge(ctx, rt, id)
}

func (g *Guard) ResendSubscriptions(ctx context.Context, rt, id, versionID, interaction string) error {
	rs, ok := g.inner.(SubscriptionResender)
	if !ok {
		return NotImplemented("Repository không hỗ trợ gửi lại sự kiện Subscription")
	}
	return rs.ResendSubscriptions(ctx, rt, id, versionID, interaction)
}

// RequireAdmin: repository bên trong không cài AdminChecker thì cho qua, như router khi không có AdminChecker (chế độ không xác thực).
func (g *Guard) RequireAdmin(ctx context.Context) error {
	if ac, ok := g.inner.(AdminChecker); ok {
		return ac.RequireAdmin(ctx)
	}
	return nil
}

func (g *Guard) cs() (ConceptStore, error) {
	cs, ok := g.inner.(ConceptStore)
	if !ok {
		return nil, NotImplemented("Repository không hỗ trợ kho concept")
	}
	return cs, nil
}

func (g *Guard) PutConcepts(ctx context.Context, sid string, c []Concept) error {
	cs, err := g.cs()
	if err != nil {
		return err
	}
	return cs.PutConcepts(ctx, sid, c)
}

func (g *Guard) PutConceptProperties(ctx context.Context, sid string, p []ConceptProperty) error {
	cs, err := g.cs()
	if err != nil {
		return err
	}
	return cs.PutConceptProperties(ctx, sid, p)
}

func (g *Guard) PutConceptDesignations(ctx context.Context, sid string, d []ConceptDesignation) error {
	cs, err := g.cs()
	if err != nil {
		return err
	}
	return cs.PutConceptDesignations(ctx, sid, d)
}

func (g *Guard) GetConcept(ctx context.Context, sid, code string) (*StoredConcept, error) {
	cs, err := g.cs()
	if err != nil {
		return nil, err
	}
	return cs.GetConcept(ctx, sid, code)
}

func (g *Guard) QueryConcepts(ctx context.Context, sid string, q ConceptQuery) ([]Concept, int, error) {
	cs, err := g.cs()
	if err != nil {
		return nil, 0, err
	}
	return cs.QueryConcepts(ctx, sid, q)
}

func (g *Guard) DeleteConcepts(ctx context.Context, sid string) (int, error) {
	cs, err := g.cs()
	if err != nil {
		return 0, err
	}
	return cs.DeleteConcepts(ctx, sid)
}

func (g *Guard) ms() (MappingStore, error) {
	ms, ok := g.inner.(MappingStore)
	if !ok {
		return nil, NotImplemented("Repository không hỗ trợ kho ánh xạ")
	}
	return ms, nil
}

func (g *Guard) PutMappings(ctx context.Context, id string, m []StoredMapping) error {
	ms, err := g.ms()
	if err != nil {
		return err
	}
	return ms.PutMappings(ctx, id, m)
}

func (g *Guard) FindMappings(ctx context.Context, id string, q MappingQuery) ([]StoredMapping, error) {
	ms, err := g.ms()
	if err != nil {
		return nil, err
	}
	return ms.FindMappings(ctx, id, q)
}

func (g *Guard) DeleteMappings(ctx context.Context, id string) (int, error) {
	ms, err := g.ms()
	if err != nil {
		return 0, err
	}
	return ms.DeleteMappings(ctx, id)
}
