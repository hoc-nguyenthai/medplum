package fhirrouter

import (
	"context"
	"slices"
	"sync"
	"time"

	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirsearch"
)

// MemoryRepository là Repository trong bộ nhớ, có lịch sử phiên bản, search đầy đủ qua fhirsearch
// và transaction (snapshot rồi khôi phục khi lỗi). Dùng cho test và phát triển, không dành cho production.
//
// Lưu ý: transaction chỉ nguyên tử với người gọi; ghi đồng thời từ goroutine khác trong lúc transaction
// đang chạy sẽ bị mất nếu transaction bị hủy.
type MemoryRepository struct {
	memConcepts
	memMappings
	mu   sync.RWMutex
	txMu sync.Mutex // tuần tự hóa các transaction
	data map[string]map[string]*record
	now  func() time.Time
}

type version struct {
	doc         map[string]any
	vid         string
	lastUpdated string
	method      string
	deleted     bool
}

type record struct{ versions []version } // cũ nhất trước

// NewMemoryRepository tạo kho rỗng.
func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{data: map[string]map[string]*record{}, now: time.Now}
}

func (m *MemoryRepository) stamp() string {
	return m.now().UTC().Format("2006-01-02T15:04:05.000Z")
}

func fromDoc(doc map[string]any) (fhir.Resource, error) { return toResource(doc) }

func (rec *record) current() *version { return &rec.versions[len(rec.versions)-1] }

func (m *MemoryRepository) CreateResource(_ context.Context, r fhir.Resource) (fhir.Resource, error) {
	doc, err := toMap(r)
	if err != nil {
		return nil, Internal(err.Error())
	}
	rt := r.GetResourceType()
	id := r.GetID()
	if id == "" {
		id = newID()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.data[rt] == nil {
		m.data[rt] = map[string]*record{}
	}
	if _, exists := m.data[rt][id]; exists {
		return nil, Conflict("Resource đã tồn tại: " + rt + "/" + id)
	}
	v := m.newVersion(doc, id, "POST")
	m.data[rt][id] = &record{versions: []version{v}}
	return fromDoc(v.doc)
}

// newVersion gán id và meta cho doc và trả về phiên bản mới (khóa ghi phải đang được giữ).
func (m *MemoryRepository) newVersion(doc map[string]any, id, method string) version {
	doc["id"] = id
	meta, _ := doc["meta"].(map[string]any)
	if meta == nil {
		meta = map[string]any{}
	}
	vid, lu := newID(), m.stamp()
	meta["versionId"], meta["lastUpdated"] = vid, lu
	doc["meta"] = meta
	return version{doc: doc, vid: vid, lastUpdated: lu, method: method}
}

func (m *MemoryRepository) lookup(rt, id string) (*record, error) {
	rec := m.data[rt][id]
	if rec == nil {
		return nil, NotFound()
	}
	if rec.current().deleted {
		return nil, Gone()
	}
	return rec, nil
}

func (m *MemoryRepository) ReadResource(_ context.Context, rt, id string) (fhir.Resource, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	rec, err := m.lookup(rt, id)
	if err != nil {
		return nil, err
	}
	return fromDoc(rec.current().doc)
}

func (m *MemoryRepository) ReadVersion(_ context.Context, rt, id, vid string) (fhir.Resource, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	rec := m.data[rt][id]
	if rec == nil {
		return nil, NotFound()
	}
	for _, v := range rec.versions {
		if v.vid == vid {
			if v.deleted {
				return nil, Gone()
			}
			return fromDoc(v.doc)
		}
	}
	return nil, NotFound()
}

func (m *MemoryRepository) ReadHistory(_ context.Context, rt, id string) ([]HistoryEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	rec := m.data[rt][id]
	if rec == nil {
		return nil, NotFound()
	}
	var out []HistoryEntry
	for i := len(rec.versions) - 1; i >= 0; i-- {
		v := rec.versions[i]
		res, err := fromDoc(v.doc)
		if err != nil {
			return nil, Internal(err.Error())
		}
		out = append(out, HistoryEntry{Resource: res, Method: v.method, ResourceType: rt, ID: id,
			VersionID: v.vid, LastUpdated: v.lastUpdated, Deleted: v.deleted})
	}
	return out, nil
}

func (m *MemoryRepository) UpdateResource(_ context.Context, r fhir.Resource, opts UpdateOptions) (fhir.Resource, error) {
	doc, err := toMap(r)
	if err != nil {
		return nil, Internal(err.Error())
	}
	rt, id := r.GetResourceType(), r.GetID()
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, err := m.lookup(rt, id)
	if err != nil {
		return nil, err
	}
	cur := rec.current()
	if opts.IfMatch != "" && opts.IfMatch != cur.vid {
		return nil, PreconditionFailed("Phiên bản không khớp: hiện tại " + cur.vid)
	}
	// meta do client gửi (versionId, lastUpdated) bị bỏ qua
	if meta, ok := doc["meta"].(map[string]any); ok {
		delete(meta, "versionId")
		delete(meta, "lastUpdated")
	}
	v := m.newVersion(doc, id, "PUT")
	rec.versions = append(rec.versions, v)
	return fromDoc(v.doc)
}

func (m *MemoryRepository) DeleteResource(_ context.Context, rt, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, err := m.lookup(rt, id)
	if err != nil {
		return err
	}
	_ = rec
	doc := map[string]any{"resourceType": rt}
	v := m.newVersion(doc, id, "DELETE")
	v.deleted = true
	m.data[rt][id].versions = append(m.data[rt][id].versions, v)
	return nil
}

// ---------------------------------------------------------------- search

func (m *MemoryRepository) Search(_ context.Context, req *fhirsearch.Request) (*SearchResult, error) {
	if fhirsearch.NeedsResolution(req) {
		return nil, NotImplemented("chained search, _has và modifier token :in/:not-in/:above/:below phải được giải trước khi gọi Repository (dùng Router)")
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	types := req.Types
	if req.ResourceType != "" {
		types = []string{req.ResourceType}
	}
	if len(types) == 0 {
		for t := range m.data {
			types = append(types, t)
		}
		slices.Sort(types)
	}
	var matches []map[string]any
	for _, t := range types {
		ids := make([]string, 0, len(m.data[t]))
		for id := range m.data[t] {
			ids = append(ids, id)
		}
		slices.Sort(ids) // thứ tự ổn định khi không có _sort
		for _, id := range ids {
			cur := m.data[t][id].current()
			if cur.deleted {
				continue
			}
			ok, err := fhirsearch.Match(cur.doc, req)
			if err != nil {
				return nil, err
			}
			if ok {
				matches = append(matches, cur.doc)
			}
		}
	}
	fhirsearch.Sort(matches, req.Sort)
	res := &SearchResult{}
	if req.Total != "none" {
		n := len(matches)
		res.Total = &n
	}
	end := min(req.Offset+req.Count, len(matches))
	page := []map[string]any{}
	if req.Offset < len(matches) {
		page = matches[req.Offset:end]
	}
	res.More = end < len(matches) && req.Count > 0
	for _, d := range page {
		r, err := fromDoc(d)
		if err != nil {
			return nil, Internal(err.Error())
		}
		res.Matches = append(res.Matches, r)
	}
	inc, err := m.includes(page, req)
	if err != nil {
		return nil, err
	}
	res.Included = inc
	return res, nil
}

// includes xử lý _include và _revinclude (một mức, kể cả :iterate chỉ lặp một lần).
func (m *MemoryRepository) includes(page []map[string]any, req *fhirsearch.Request) ([]fhir.Resource, error) {
	if len(req.Include) == 0 && len(req.Revinclude) == 0 {
		return nil, nil
	}
	seen := map[string]bool{}
	for _, d := range page {
		seen[d["resourceType"].(string)+"/"+d["id"].(string)] = true
	}
	var out []fhir.Resource
	add := func(d map[string]any) error {
		key := d["resourceType"].(string) + "/" + d["id"].(string)
		if seen[key] {
			return nil
		}
		seen[key] = true
		r, err := fromDoc(d)
		if err != nil {
			return err
		}
		out = append(out, r)
		return nil
	}
	for _, inc := range req.Include {
		for _, d := range page {
			rt := d["resourceType"].(string)
			if inc.ResourceType != "*" && inc.ResourceType != rt {
				continue
			}
			def, ok := fhirsearch.Lookup(rt, inc.Param)
			if !ok {
				return nil, &fhirsearch.Error{Msg: "_include: tham số không tồn tại: " + inc.Param}
			}
			refs, err := fhirsearch.References(d, def)
			if err != nil {
				return nil, err
			}
			for _, ref := range refs {
				t, id := splitRef(ref)
				if t == "" || (inc.Target != "" && inc.Target != t) {
					continue
				}
				if rec := m.data[t][id]; rec != nil && !rec.current().deleted {
					if err := add(rec.current().doc); err != nil {
						return nil, err
					}
				}
			}
		}
	}
	for _, rev := range req.Revinclude {
		def, ok := fhirsearch.Lookup(rev.ResourceType, rev.Param)
		if !ok {
			return nil, &fhirsearch.Error{Msg: "_revinclude: tham số không tồn tại: " + rev.Param}
		}
		keys := map[string]bool{}
		for _, d := range page {
			keys[d["resourceType"].(string)+"/"+d["id"].(string)] = true
		}
		ids := make([]string, 0)
		for id := range m.data[rev.ResourceType] {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		for _, id := range ids {
			cur := m.data[rev.ResourceType][id].current()
			if cur.deleted {
				continue
			}
			refs, err := fhirsearch.References(cur.doc, def)
			if err != nil {
				return nil, err
			}
			for _, ref := range refs {
				if keys[ref] {
					if err := add(cur.doc); err != nil {
						return nil, err
					}
					break
				}
			}
		}
	}
	return out, nil
}

func splitRef(ref string) (string, string) {
	for i := len(ref) - 1; i > 0; i-- {
		if ref[i] == '/' {
			if fhir.IsResourceType(ref[:i]) {
				return ref[:i], ref[i+1:]
			}
			break
		}
	}
	return "", ""
}

// ---------------------------------------------------------------- transaction

func (m *MemoryRepository) snapshot() map[string]map[string]*record {
	snap := make(map[string]map[string]*record, len(m.data))
	for t, recs := range m.data {
		c := make(map[string]*record, len(recs))
		for id, rec := range recs {
			c[id] = &record{versions: slices.Clone(rec.versions)}
		}
		snap[t] = c
	}
	return snap
}

// RunInTransaction chạy fn rồi giữ thay đổi nếu fn trả nil; ngược lại khôi phục trạng thái ban đầu.
func (m *MemoryRepository) RunInTransaction(_ context.Context, fn func(Repository) error) error {
	m.txMu.Lock()
	defer m.txMu.Unlock()
	m.mu.RLock()
	snap := m.snapshot()
	m.mu.RUnlock()
	csnap := m.memConcepts.clone()
	msnap := m.memMappings.clone()
	if err := fn(m); err != nil {
		m.mu.Lock()
		m.data = snap
		m.mu.Unlock()
		m.memConcepts.restore(csnap)
		m.memMappings.restore(msnap)
		return err
	}
	return nil
}

// LockKey không cần làm gì: RunInTransaction đã tuần tự hóa mọi transaction bằng txMu.
func (m *MemoryRepository) LockKey(context.Context, string) error { return nil }

var (
	_ Repository    = (*MemoryRepository)(nil)
	_ Transactional = (*MemoryRepository)(nil)
	_ KeyLocker     = (*MemoryRepository)(nil)
)

// ---------------------------------------------------------------- quản trị

// Reindex đếm các resource sẽ được lập lại chỉ mục. MemoryRepository đối sánh trực tiếp trên nội dung
// nên không có chỉ mục riêng cần dựng lại.
func (m *MemoryRepository) Reindex(_ context.Context, rt, id string, progress func(int)) (int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	n := 0
	for rid, rec := range m.data[rt] {
		if (id == "" || id == rid) && !rec.current().deleted {
			n++
		}
	}
	if progress != nil {
		progress(n)
	}
	return n, nil
}

// Expunge xóa vĩnh viễn resource và toàn bộ lịch sử.
func (m *MemoryRepository) Expunge(_ context.Context, rt, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.data[rt][id] == nil {
		return NotFound()
	}
	delete(m.data[rt], id)
	if rt == "CodeSystem" {
		_, _ = m.DeleteConcepts(context.Background(), id)
	}
	if rt == "ConceptMap" {
		_, _ = m.DeleteMappings(context.Background(), id)
	}
	return nil
}

var (
	_ Reindexer = (*MemoryRepository)(nil)
	_ Expunger  = (*MemoryRepository)(nil)
)
