package fhirrouter

import (
	"context"
	"slices"
	"strings"
	"sync"
)

// MappingStore là tùy chọn của Repository: kho ánh xạ dẫn xuất của ConceptMap (không nằm trong JSON của ConceptMap), dùng cho
// bảng ánh xạ lớn (xem docs/plans/modules/definitions-import.md, D4). Khóa theo id của ConceptMap; việc kiểm project và quyền
// do repository bọc ngoài (fhirauth) và router làm trước khi gọi. PutMappings là upsert theo (nguồn, đích).
type MappingStore interface {
	PutMappings(ctx context.Context, mapID string, ms []StoredMapping) error
	// FindMappings trả các ánh xạ của một ConceptMap thỏa điều kiện (trường rỗng là không lọc), sắp theo nguồn rồi đích.
	FindMappings(ctx context.Context, mapID string, q MappingQuery) ([]StoredMapping, error)
	// DeleteMappings xóa mọi ánh xạ của một ConceptMap; trả số ánh xạ đã xóa.
	DeleteMappings(ctx context.Context, mapID string) (int, error)
}

// StoredMapping là một ánh xạ nguồn -> đích. Equivalence rỗng nghĩa là "equivalent". TargetCode rỗng là ánh xạ rỗng (null map).
// Attributes là JSON của các property, dependsOn, product (chỉ lưu, chưa dùng khi dịch).
type StoredMapping struct {
	SourceSystem, SourceCode, SourceDisplay string
	TargetSystem, TargetCode, TargetDisplay string
	Equivalence, Comment, Attributes        string
}

// MappingQuery lọc FindMappings; Limit 0 là không giới hạn.
type MappingQuery struct {
	SourceSystem, SourceCode string
	TargetSystem, TargetCode string
	Limit                    int
}

func (m StoredMapping) matches(q MappingQuery) bool {
	return (q.SourceSystem == "" || m.SourceSystem == q.SourceSystem) && (q.SourceCode == "" || m.SourceCode == q.SourceCode) &&
		(q.TargetSystem == "" || m.TargetSystem == q.TargetSystem) && (q.TargetCode == "" || m.TargetCode == q.TargetCode)
}

type mappingKey struct{ ss, sc, ts, tc string }

func (m StoredMapping) key() mappingKey {
	return mappingKey{m.SourceSystem, m.SourceCode, m.TargetSystem, m.TargetCode}
}

// memMappings là cài đặt trong bộ nhớ, nhúng vào MemoryRepository.
type memMappings struct {
	mmu  sync.RWMutex
	maps map[string]map[mappingKey]StoredMapping
}

func (c *memMappings) clone() map[string]map[mappingKey]StoredMapping {
	c.mmu.RLock()
	defer c.mmu.RUnlock()
	out := make(map[string]map[mappingKey]StoredMapping, len(c.maps))
	for id, m := range c.maps {
		out[id] = make(map[mappingKey]StoredMapping, len(m))
		for k, v := range m {
			out[id][k] = v
		}
	}
	return out
}

func (c *memMappings) restore(s map[string]map[mappingKey]StoredMapping) {
	c.mmu.Lock()
	c.maps = s
	c.mmu.Unlock()
}

func (c *memMappings) PutMappings(_ context.Context, id string, ms []StoredMapping) error {
	c.mmu.Lock()
	defer c.mmu.Unlock()
	if c.maps == nil {
		c.maps = map[string]map[mappingKey]StoredMapping{}
	}
	if c.maps[id] == nil {
		c.maps[id] = map[mappingKey]StoredMapping{}
	}
	for _, m := range ms {
		c.maps[id][m.key()] = m
	}
	return nil
}

func (c *memMappings) FindMappings(_ context.Context, id string, q MappingQuery) ([]StoredMapping, error) {
	c.mmu.RLock()
	defer c.mmu.RUnlock()
	var out []StoredMapping
	for _, m := range c.maps[id] {
		if m.matches(q) {
			out = append(out, m)
		}
	}
	slices.SortFunc(out, func(a, b StoredMapping) int {
		return strings.Compare(a.SourceSystem+"\x00"+a.SourceCode+"\x00"+a.TargetSystem+"\x00"+a.TargetCode,
			b.SourceSystem+"\x00"+b.SourceCode+"\x00"+b.TargetSystem+"\x00"+b.TargetCode)
	})
	if q.Limit > 0 && q.Limit < len(out) {
		out = out[:q.Limit]
	}
	return out, nil
}

func (c *memMappings) DeleteMappings(_ context.Context, id string) (int, error) {
	c.mmu.Lock()
	defer c.mmu.Unlock()
	n := len(c.maps[id])
	delete(c.maps, id)
	return n, nil
}

var _ MappingStore = (*MemoryRepository)(nil)
