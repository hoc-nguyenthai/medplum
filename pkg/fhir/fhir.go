// Package fhir chứa kiểu dữ liệu FHIR R4 (kèm các resource riêng của Medplum).
//
// Hầu hết mã trong package được sinh bởi tools/fhirgen từ StructureDefinition
// (xem doc.go); file này chỉ chứa phần viết tay: giao diện Resource và AnyResource.
package fhir

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Resource là giao diện chung của mọi resource FHIR được sinh ra.
type Resource interface {
	GetResourceType() string
	GetID() string
	GetMeta() *Meta
}

// NewResource tạo resource rỗng theo tên kiểu, ví dụ "Patient".
func NewResource(resourceType string) (Resource, bool) {
	f, ok := resourceFactories[resourceType]
	if !ok {
		return nil, false
	}
	return f(), true
}

// IsResourceType cho biết tên có phải resource được hỗ trợ không.
func IsResourceType(name string) bool {
	_, ok := resourceFactories[name]
	return ok
}

// UnmarshalResource giải mã JSON thành đúng kiểu resource theo trường resourceType.
func UnmarshalResource(data []byte) (Resource, error) {
	var probe struct {
		ResourceType string `json:"resourceType"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, err
	}
	if probe.ResourceType == "" {
		return nil, errors.New("fhir: thiếu resourceType")
	}
	r, ok := NewResource(probe.ResourceType)
	if !ok {
		return nil, fmt.Errorf("fhir: resourceType không hỗ trợ: %q", probe.ResourceType)
	}
	if err := json.Unmarshal(data, r); err != nil {
		return nil, err
	}
	return r, nil
}

// AnyResource chứa một resource bất kỳ; dùng cho Bundle.entry.resource, contained, Parameters...
type AnyResource struct {
	Value Resource
}

// MarshalJSON ghi resource bên trong (hoặc null nếu rỗng).
func (a AnyResource) MarshalJSON() ([]byte, error) {
	if a.Value == nil {
		return []byte("null"), nil
	}
	return json.Marshal(a.Value)
}

// UnmarshalJSON chọn kiểu theo resourceType.
func (a *AnyResource) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		a.Value = nil
		return nil
	}
	r, err := UnmarshalResource(data)
	if err != nil {
		return err
	}
	a.Value = r
	return nil
}

// SearchParamComponent là một thành phần của search parameter kiểu composite.
type SearchParamComponent struct {
	Definition string
	Expression string
}

// SearchParamDef là một SearchParameter rút gọn, sinh từ định nghĩa của Medplum.
type SearchParamDef struct {
	URL        string
	Code       string
	Type       string // number, date, string, token, reference, composite, quantity, uri, special
	Base       []string
	Target     []string
	Expression string
	Comparator []string
	Modifier   []string
	Component  []SearchParamComponent
}

// ProjectParam là search parameter "_project" (Meta.project), không có trong đặc tả R4 mà thuộc mở rộng Medplum.
// pkg/fhirauth dùng nó để tách dữ liệu theo project.
var ProjectParam = SearchParamDef{
	URL: "https://medplum.com/fhir/SearchParameter/Resource-project", Code: "_project", Type: "token",
	Base: []string{"Resource"}, Expression: "Resource.meta.project",
}

// SearchParams trả về các search parameter áp dụng cho một resource type
// (gồm cả tham số khai báo trên Resource và DomainResource).
func SearchParams(resourceType string) []SearchParamDef {
	out := []SearchParamDef{ProjectParam}
	for _, sp := range searchParamData {
		for _, b := range sp.Base {
			if b == resourceType || b == "Resource" || (b == "DomainResource" && IsSubtype(resourceType, "DomainResource")) {
				out = append(out, sp)
				break
			}
		}
	}
	return out
}
