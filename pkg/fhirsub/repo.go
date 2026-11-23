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
