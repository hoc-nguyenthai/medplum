package fhirsub

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"

	"trivita-fhir/pkg/core"
	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirrouter"
)

// Publisher là phần của messaging.Broker mà Repo cần.
type Publisher interface {
	Publish(ctx context.Context, e core.Event) error
}

// Repo bọc một fhirrouter.Repository: mỗi lần create, update, delete thành công thì phát ChangeEvent.
// Trong transaction, sự kiện được giữ lại và chỉ phát sau khi commit (rollback thì không phát gì).
// Lỗi khi phát chỉ được log, không làm hỏng việc ghi.
type Repo struct {
	fhirrouter.Repository
	pub Publisher
	buf *txBuffer // khác nil khi đang trong transaction
}

type txBuffer struct {
	mu     sync.Mutex
	events []ChangeEvent
}

// Wrap bọc repository. Repo thỏa Transactional (và chuyển tiếp Reindexer, Expunger) dựa trên repository bên trong.
func Wrap(inner fhirrouter.Repository, pub Publisher) *Repo {
	return &Repo{Repository: inner, pub: pub}
}

var (
	_ fhirrouter.Repository    = (*Repo)(nil)
	_ fhirrouter.Transactional = (*Repo)(nil)
	_ fhirrouter.Reindexer     = (*Repo)(nil)
	_ fhirrouter.Expunger      = (*Repo)(nil)
)

func (r *Repo) emit(ctx context.Context, interaction string, res fhir.Resource) {
	raw, err := json.Marshal(res)
	if err != nil {
		slog.ErrorContext(ctx, "fhirsub: không mã hóa được resource", "error", err)
		return
	}
	ev := ChangeEvent{BaseEvent: core.NewBaseEvent(), Interaction: interaction,
		ResourceType: res.GetResourceType(), ID: res.GetID(), Resource: raw}
	if m := res.GetMeta(); m != nil {
		ev.VersionID = m.VersionId
	}
	if r.buf != nil {
		r.buf.mu.Lock()
		r.buf.events = append(r.buf.events, ev)
		r.buf.mu.Unlock()
		return
	}
	r.publish(ctx, ev)
}

func (r *Repo) publish(ctx context.Context, ev ChangeEvent) {
	if err := r.pub.Publish(context.WithoutCancel(ctx), ev); err != nil {
		slog.ErrorContext(ctx, "fhirsub: phát sự kiện lỗi", "resource", ev.ResourceType+"/"+ev.ID, "error", err)
	}
}

func (r *Repo) CreateResource(ctx context.Context, res fhir.Resource) (fhir.Resource, error) {
	out, err := r.Repository.CreateResource(ctx, res)
	if err == nil {
		r.emit(ctx, Create, out)
	}
	return out, err
}

func (r *Repo) UpdateResource(ctx context.Context, res fhir.Resource, o fhirrouter.UpdateOptions) (fhir.Resource, error) {
	out, err := r.Repository.UpdateResource(ctx, res, o)
	if err == nil {
		r.emit(ctx, Update, out)
	}
	return out, err
}

func (r *Repo) DeleteResource(ctx context.Context, rt, id string) error {
	prev, _ := r.Repository.ReadResource(ctx, rt, id) // phiên bản cuối, để khớp tiêu chí khi xóa
	if err := r.Repository.DeleteResource(ctx, rt, id); err != nil {
		return err
	}
	if prev != nil {
		r.emit(ctx, Delete, prev)
	}
	return nil
}

// RunInTransaction chạy fn trong transaction của repository bên trong và phát sự kiện sau khi commit.
func (r *Repo) RunInTransaction(ctx context.Context, fn func(fhirrouter.Repository) error) error {
	if r.buf != nil { // đã trong transaction
		return fn(r)
	}
	tx, ok := r.Repository.(fhirrouter.Transactional)
	if !ok {
		return fhirrouter.NotImplemented("Repository không hỗ trợ transaction")
	}
	buf := &txBuffer{}
	err := tx.RunInTransaction(ctx, func(rp fhirrouter.Repository) error {
		return fn(&Repo{Repository: rp, pub: r.pub, buf: buf})
	})
	if err != nil {
		return err
	}
	for _, ev := range buf.events {
		r.publish(ctx, ev)
	}
	return nil
}

// LockKey chuyển cho repository bên trong (xem fhirrouter.KeyLocker).
func (r *Repo) LockKey(ctx context.Context, key string) error {
	if kl, ok := r.Repository.(fhirrouter.KeyLocker); ok {
		return kl.LockKey(ctx, key)
	}
	return nil
}

func (r *Repo) Reindex(ctx context.Context, rt, id string, progress func(int)) (int, error) {
	ri, ok := r.Repository.(fhirrouter.Reindexer)
	if !ok {
		return 0, fhirrouter.NotImplemented("Repository không hỗ trợ reindex")
	}
	return ri.Reindex(ctx, rt, id, progress)
}

func (r *Repo) Expunge(ctx context.Context, rt, id string) error {
	ex, ok := r.Repository.(fhirrouter.Expunger)
	if !ok {
		return fhirrouter.NotImplemented("Repository không hỗ trợ expunge")
	}
	return ex.Expunge(ctx, rt, id)
}

// ConceptStore: chuyển cho repository bên trong (xem fhirrouter.ConceptStore).
func (r *Repo) cs() (fhirrouter.ConceptStore, error) {
	cs, ok := r.Repository.(fhirrouter.ConceptStore)
	if !ok {
		return nil, fhirrouter.NotImplemented("Repository không hỗ trợ kho concept")
	}
	return cs, nil
}

func (r *Repo) PutConcepts(ctx context.Context, sid string, c []fhirrouter.Concept) error {
	cs, err := r.cs()
	if err != nil {
		return err
	}
	return cs.PutConcepts(ctx, sid, c)
}

func (r *Repo) PutConceptProperties(ctx context.Context, sid string, p []fhirrouter.ConceptProperty) error {
	cs, err := r.cs()
	if err != nil {
		return err
	}
	return cs.PutConceptProperties(ctx, sid, p)
}

func (r *Repo) PutConceptDesignations(ctx context.Context, sid string, d []fhirrouter.ConceptDesignation) error {
	cs, err := r.cs()
	if err != nil {
		return err
	}
	return cs.PutConceptDesignations(ctx, sid, d)
}

func (r *Repo) GetConcept(ctx context.Context, sid, code string) (*fhirrouter.StoredConcept, error) {
	cs, err := r.cs()
	if err != nil {
		return nil, err
	}
	return cs.GetConcept(ctx, sid, code)
}

func (r *Repo) DeleteConcepts(ctx context.Context, sid string) (int, error) {
	cs, err := r.cs()
	if err != nil {
		return 0, err
	}
	return cs.DeleteConcepts(ctx, sid)
}

func (r *Repo) QueryConcepts(ctx context.Context, sid string, q fhirrouter.ConceptQuery) ([]fhirrouter.Concept, int, error) {
	cs, err := r.cs()
	if err != nil {
		return nil, 0, err
	}
	return cs.QueryConcepts(ctx, sid, q)
}

var _ fhirrouter.ConceptStore = (*Repo)(nil)

// MappingStore: chuyển cho repository bên trong (xem fhirrouter.MappingStore).
func (r *Repo) ms() (fhirrouter.MappingStore, error) {
	ms, ok := r.Repository.(fhirrouter.MappingStore)
	if !ok {
		return nil, fhirrouter.NotImplemented("Repository không hỗ trợ kho ánh xạ")
	}
	return ms, nil
}

func (r *Repo) PutMappings(ctx context.Context, id string, m []fhirrouter.StoredMapping) error {
	ms, err := r.ms()
	if err != nil {
		return err
	}
	return ms.PutMappings(ctx, id, m)
}

func (r *Repo) FindMappings(ctx context.Context, id string, q fhirrouter.MappingQuery) ([]fhirrouter.StoredMapping, error) {
	ms, err := r.ms()
	if err != nil {
		return nil, err
	}
	return ms.FindMappings(ctx, id, q)
}

func (r *Repo) DeleteMappings(ctx context.Context, id string) (int, error) {
	ms, err := r.ms()
	if err != nil {
		return 0, err
	}
	return ms.DeleteMappings(ctx, id)
}

var _ fhirrouter.MappingStore = (*Repo)(nil)
