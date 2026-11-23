package fhirrouter

import (
	"context"
	"slices"
	"sync"
)

// ConceptStore là tùy chọn của Repository: kho concept dẫn xuất của CodeSystem (không nằm trong JSON của CodeSystem), dùng
// cho bảng mã lớn như SNOMED, LOINC (xem docs/plans/modules/definitions-import.md, Q-D1 = A). Khóa theo id của CodeSystem;
// việc kiểm project và quyền do repository bọc ngoài (fhirauth) và router làm trước khi gọi. Mọi hàm Put là upsert theo
// khóa tự nhiên, nên nạp lại cùng dữ liệu là an toàn.
type ConceptStore interface {
	PutConcepts(ctx context.Context, systemID string, concepts []Concept) error
	PutConceptProperties(ctx context.Context, systemID string, props []ConceptProperty) error
	PutConceptDesignations(ctx context.Context, systemID string, ds []ConceptDesignation) error
	// GetConcept trả nil, nil nếu không có concept.
	GetConcept(ctx context.Context, systemID, code string) (*StoredConcept, error)
	// DeleteConcepts xóa mọi concept, thuộc tính và designation của một CodeSystem; trả số concept đã xóa.
	DeleteConcepts(ctx context.Context, systemID string) (int, error)
}

// Concept là một dòng concept.
type Concept struct{ Code, Display string }

// ConceptProperty là một thuộc tính của concept. Value luôn là chuỗi; Type là kiểu khai báo ở CodeSystem.property
// ("code", "string", "boolean", ...), với "code" thì Value là mã của một concept khác (cha, con).
type ConceptProperty struct{ Code, Property, Type, Value string }

// ConceptDesignation là một tên gọi khác của concept.
type ConceptDesignation struct{ Code, Language, Value string }

// StoredConcept là kết quả của GetConcept.
type StoredConcept struct {
	Code         string
	Display      string
	Properties   []ConceptProperty
	Designations []ConceptDesignation
}

// memConcepts là cài đặt trong bộ nhớ, nhúng vào MemoryRepository.
type memConcepts struct {
	cmu  sync.RWMutex
	sets map[string]map[string]*StoredConcept // systemID -> code
}

func (c *memConcepts) clone() map[string]map[string]*StoredConcept {
	c.cmu.RLock()
	defer c.cmu.RUnlock()
	out := make(map[string]map[string]*StoredConcept, len(c.sets))
	for sid, m := range c.sets {
		mm := make(map[string]*StoredConcept, len(m))
		for code, sc := range m {
			cp := *sc
			cp.Properties = slices.Clone(sc.Properties)
			cp.Designations = slices.Clone(sc.Designations)
			mm[code] = &cp
		}
		out[sid] = mm
	}
	return out
}

func (c *memConcepts) restore(s map[string]map[string]*StoredConcept) {
	c.cmu.Lock()
	c.sets = s
	c.cmu.Unlock()
}

func (c *memConcepts) PutConcepts(_ context.Context, sid string, cs []Concept) error {
	c.cmu.Lock()
	defer c.cmu.Unlock()
	m := c.set(sid)
	for _, x := range cs {
		if cur := m[x.Code]; cur != nil {
			cur.Display = x.Display
		} else {
			m[x.Code] = &StoredConcept{Code: x.Code, Display: x.Display}
		}
	}
	return nil
}

func (c *memConcepts) set(sid string) map[string]*StoredConcept {
	if c.sets == nil {
		c.sets = map[string]map[string]*StoredConcept{}
	}
	if c.sets[sid] == nil {
		c.sets[sid] = map[string]*StoredConcept{}
	}
	return c.sets[sid]
}

func (c *memConcepts) PutConceptProperties(_ context.Context, sid string, ps []ConceptProperty) error {
	c.cmu.Lock()
	defer c.cmu.Unlock()
	m := c.set(sid)
	for _, p := range ps {
		sc := m[p.Code]
		if sc == nil {
			return BadRequest("Concept chưa có: " + p.Code)
		}
		if !slices.Contains(sc.Properties, p) {
			sc.Properties = append(sc.Properties, p)
		}
	}
	return nil
}

func (c *memConcepts) PutConceptDesignations(_ context.Context, sid string, ds []ConceptDesignation) error {
	c.cmu.Lock()
	defer c.cmu.Unlock()
	m := c.set(sid)
	for _, d := range ds {
		sc := m[d.Code]
		if sc == nil {
			return BadRequest("Concept chưa có: " + d.Code)
		}
		if !slices.Contains(sc.Designations, d) {
			sc.Designations = append(sc.Designations, d)
		}
	}
	return nil
}

func (c *memConcepts) GetConcept(_ context.Context, sid, code string) (*StoredConcept, error) {
	c.cmu.RLock()
	defer c.cmu.RUnlock()
	sc := c.sets[sid][code]
	if sc == nil {
		return nil, nil
	}
	cp := *sc
	cp.Properties = slices.Clone(sc.Properties)
	cp.Designations = slices.Clone(sc.Designations)
	return &cp, nil
}

func (c *memConcepts) DeleteConcepts(_ context.Context, sid string) (int, error) {
	c.cmu.Lock()
	defer c.cmu.Unlock()
	n := len(c.sets[sid])
	delete(c.sets, sid)
	return n, nil
}

var _ ConceptStore = (*MemoryRepository)(nil)
