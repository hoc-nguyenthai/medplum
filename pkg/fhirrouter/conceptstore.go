package fhirrouter

import (
	"context"
	"slices"
	"strings"
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
	// QueryConcepts trả các concept (sắp theo code) thỏa điều kiện, và tổng số trước khi phân trang.
	QueryConcepts(ctx context.Context, systemID string, q ConceptQuery) ([]Concept, int, error)
	// DeleteConcepts xóa mọi concept, thuộc tính và designation của một CodeSystem; trả số concept đã xóa.
	DeleteConcepts(ctx context.Context, systemID string) (int, error)
}

// ConceptQuery là điều kiện của QueryConcepts; các điều kiện kết hợp bằng AND.
type ConceptQuery struct {
	Codes             []string        // nếu không rỗng, chỉ xét các code này
	Filters           []ConceptFilter // theo ValueSet.compose.include.filter
	HierarchyProperty string          // thuộc tính chứa code cha (hierarchyMeaning, hoặc "parent")
	Text              string          // chứa trong code hoặc display, không phân biệt hoa thường
	Offset, Limit     int             // Limit 0 là không giới hạn
}

// ConceptFilter là một filter của compose: Op là "is-a", "descendent-of" hoặc "=" (Property "code"/"concept" so với code).
type ConceptFilter struct{ Op, Property, Value string }

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

func (c *memConcepts) QueryConcepts(_ context.Context, sid string, q ConceptQuery) ([]Concept, int, error) {
	c.cmu.RLock()
	defer c.cmu.RUnlock()
	all := c.sets[sid]
	kids := map[string][]string{}
	if q.HierarchyProperty != "" {
		for code, sc := range all {
			for _, p := range sc.Properties {
				if p.Property == q.HierarchyProperty {
					kids[p.Value] = append(kids[p.Value], code)
				}
			}
		}
	}
	allowed := func(code string) bool { return true }
	restrict := func(set map[string]bool) {
		prev := allowed
		allowed = func(code string) bool { return set[code] && prev(code) }
	}
	if len(q.Codes) > 0 {
		set := map[string]bool{}
		for _, code := range q.Codes {
			set[code] = true
		}
		restrict(set)
	}
	for _, f := range q.Filters {
		set := map[string]bool{}
		switch {
		case f.Op == "is-a" || f.Op == "descendent-of":
			var rec func(string)
			rec = func(code string) {
				for _, k := range kids[code] {
					if !set[k] {
						set[k] = true
						rec(k)
					}
				}
			}
			rec(f.Value)
			if f.Op == "is-a" {
				set[f.Value] = true
			}
		case f.Op == "=" && (f.Property == "code" || f.Property == "concept"):
			set[f.Value] = true
		case f.Op == "=":
			for code, sc := range all {
				for _, p := range sc.Properties {
					if p.Property == f.Property && p.Value == f.Value {
						set[code] = true
					}
				}
			}
		default:
			return nil, 0, Unprocessable("filter không hỗ trợ trên kho concept: " + f.Property + " " + f.Op)
		}
		restrict(set)
	}
	text := strings.ToLower(q.Text)
	var out []Concept
	for code, sc := range all {
		if !allowed(code) {
			continue
		}
		if text != "" && !strings.Contains(strings.ToLower(code), text) && !strings.Contains(strings.ToLower(sc.Display), text) {
			continue
		}
		out = append(out, Concept{Code: code, Display: sc.Display})
	}
	slices.SortFunc(out, func(a, b Concept) int { return strings.Compare(a.Code, b.Code) })
	total := len(out)
	if q.Offset > 0 {
		out = out[min(q.Offset, len(out)):]
	}
	if q.Limit > 0 && q.Limit < len(out) {
		out = out[:q.Limit]
	}
	return out, total, nil
}
