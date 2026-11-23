package fhir

import (
	"slices"
	"strings"
	"sync"
)

// ElementInfo mô tả một phần tử con của một kiểu FHIR.
//
// Với choice type (value[x]) Name là phần gốc ("value") và Types liệt kê mọi kiểu cho phép;
// tên thuộc tính JSON là Name + tên kiểu viết hoa đầu (valueQuantity).
// Với backbone element, Types chứa đường dẫn kiểu (ví dụ "Patient.contact").
type ElementInfo struct {
	Name        string
	Types       []string
	Array       bool
	Min         int          // 1 nếu bắt buộc
	Constraints []Constraint // ràng buộc FHIRPath áp dụng cho giá trị của phần tử (không gồm ele-1)
}

// Constraint là một invariant FHIRPath (dom-2, obs-6, ...). Severity là "error" hoặc "warning".
type Constraint struct {
	Key, Severity, Human, Expression string
}

// TypeConstraints trả về ràng buộc ở gốc của một kiểu (ví dụ dom-2 của mọi DomainResource).
func TypeConstraints(typeName string) []Constraint { return typeConstraintData[typeName] }

// PrimitiveRegex trả về regex hợp lệ của kiểu nguyên thủy FHIR (chưa neo), nếu có.
func PrimitiveRegex(typeName string) (string, bool) {
	r, ok := primitiveRegexData[typeName]
	return r, ok
}

// IsChoice cho biết phần tử là choice type.
func (e ElementInfo) IsChoice() bool { return len(e.Types) > 1 }

var (
	schemaOnce  sync.Once
	schemaIndex map[string]map[string]ElementInfo
)

func buildSchema() {
	schemaIndex = make(map[string]map[string]ElementInfo, len(schemaData))
	for t, els := range schemaData {
		m := make(map[string]ElementInfo, len(els))
		for _, e := range els {
			m[e.Name] = e
		}
		schemaIndex[t] = m
	}
}

// LookupElement tra cứu phần tử con theo tên (không kèm hậu tố kiểu của choice type).
func LookupElement(typeName, name string) (ElementInfo, bool) {
	schemaOnce.Do(buildSchema)
	e, ok := schemaIndex[typeName][name]
	return e, ok
}

// HasType cho biết typeName có được định nghĩa trong schema không.
func HasType(typeName string) bool {
	schemaOnce.Do(buildSchema)
	_, ok := schemaIndex[typeName]
	return ok
}

// Elements trả về các phần tử con của một kiểu theo thứ tự định nghĩa.
func Elements(typeName string) []ElementInfo { return schemaData[typeName] }

// ChoiceProperty trả về tên thuộc tính JSON của một biến thể choice type: ("value","Quantity") -> "valueQuantity".
func ChoiceProperty(name, typ string) string {
	if typ == "" {
		return name
	}
	return name + strings.ToUpper(typ[:1]) + typ[1:]
}

var primitiveParents = map[string]string{
	"code": "string", "id": "string", "markdown": "string",
	"uri": "string", "url": "uri", "canonical": "uri", "oid": "uri", "uuid": "uri",
	"positiveInt": "integer", "unsignedInt": "integer",
}

var noDomainResource = []string{"Bundle", "Binary", "Parameters"}

// IsSubtype cho biết t có bằng base hoặc kế thừa base (ví dụ code -> string, Age -> Quantity, Patient -> DomainResource -> Resource).
func IsSubtype(t, base string) bool {
	if t == base {
		return true
	}
	if p, ok := primitiveParents[t]; ok {
		return IsSubtype(p, base)
	}
	switch t {
	case "Age", "Count", "Distance", "Duration", "Money", "SimpleQuantity", "MoneyQuantity":
		return base == "Quantity" || base == "Element"
	}
	if IsResourceType(t) {
		if base == "Resource" {
			return true
		}
		return base == "DomainResource" && !slices.Contains(noDomainResource, t)
	}
	if base == "Element" {
		if _, ok := schemaData[t]; ok {
			return true
		}
	}
	return false
}
