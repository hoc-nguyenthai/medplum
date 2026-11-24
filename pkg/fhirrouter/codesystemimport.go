package fhirrouter

import (
	"context"
	"fmt"
	"golang.org/x/text/unicode/norm"
	"strings"

	"trivita-fhir/pkg/fhir"
)

// CodeSystem/$import và CodeSystem/{id}/$import (Medplum: codesystemimport.ts). Nạp concept, thuộc tính và designation vào
// kho concept dẫn xuất (ConceptStore) thay vì sửa JSON của CodeSystem, để nạp được bảng mã lớn.
//
// Tham số (Parameters): system (uri, bắt buộc nếu không có id), concept (valueCoding: code, display),
// property (part: code, property, value), designation (part: code, language, value). Mỗi loại tối đa maxImportItems.
// Property phải được khai báo trong CodeSystem.property (hoặc là parent / hierarchyMeaning); code phải đã có hoặc có trong
// cùng lần gọi. Chỉ admin; trả Parameters với "return" là CodeSystem. Gọi lại với cùng dữ liệu là an toàn.

const maxImportItems = 10000

func (r *Router) registerCodeSystemImport() {
	r.Add("POST", "CodeSystem/$import", r.codeSystemImport)
	r.Add("POST", "CodeSystem/:id/$import", r.codeSystemImport)
}

func (r *Router) codeSystemImport(ctx context.Context, req *Request) (*Response, error) {
	if ac, ok := r.repo.(AdminChecker); ok {
		if err := ac.RequireAdmin(ctx); err != nil {
			return nil, err
		}
	}
	if _, ok := r.repo.(ConceptStore); !ok {
		return nil, NotImplemented("Repository không hỗ trợ kho concept")
	}
	in, err := ParseInput(req)
	if err != nil {
		return nil, err
	}
	concepts, props, desigs, err := parseImportBody(in.Body)
	if err != nil {
		return nil, err
	}
	repo := r.repoOf(ctx)
	if req.Params["id"] == "" && in.String("system") == "" {
		return nil, BadRequest("Chưa chỉ định CodeSystem (system hoặc id)")
	}
	cs, err := r.codeSystemFor(ctx, repo, req, in, "system")
	if err != nil {
		return nil, err
	}
	for i := range props {
		t, ok := propertyType(cs, props[i].Property)
		if !ok {
			return nil, BadRequest("Property không xác định: " + props[i].Property)
		}
		props[i].Type = t
	}

	apply := func(rp Repository) error {
		st, ok := rp.(ConceptStore)
		if !ok {
			return NotImplemented("Repository không hỗ trợ kho concept")
		}
		if err := st.PutConcepts(ctx, cs.ID, concepts); err != nil {
			return err
		}
		// PutConceptProperties và PutConceptDesignations tự kiểm mọi mã đã có (theo lô, báo mã thiếu đầu tiên): không tra từng mã ở đây (3 truy vấn mỗi mã, ở PostgreSQL 100 000 concept mất hàng chục giây vì độ trễ từng truy vấn).
		if err := st.PutConceptProperties(ctx, cs.ID, props); err != nil {
			return err
		}
		return st.PutConceptDesignations(ctx, cs.ID, desigs)
	}
	if tx, ok := repo.(Transactional); ok {
		err = tx.RunInTransaction(ctx, apply)
	} else {
		err = apply(repo)
	}
	if err != nil {
		return nil, err
	}
	return &Response{Status: 200, Resource: &fhir.Parameters{Parameter: []fhir.ParametersParameter{
		{Name: "return", Resource: &fhir.AnyResource{Value: cs}},
	}}}, nil
}

// propertyType trả kiểu của thuộc tính theo CodeSystem.property; parent (hoặc hierarchyMeaning) luôn hợp lệ, kiểu code.
func propertyType(cs *fhir.CodeSystem, code string) (string, bool) {
	for _, p := range cs.Property {
		if p.Code == code {
			return p.Type, true
		}
	}
	if code == cs.HierarchyMeaning || (code == "parent" && cs.HierarchyMeaning == "") {
		return "code", true
	}
	return "", false
}

func parseImportBody(body map[string]any) ([]Concept, []ConceptProperty, []ConceptDesignation, error) {
	var concepts []Concept
	var props []ConceptProperty
	var desigs []ConceptDesignation
	if body == nil || body["resourceType"] != "Parameters" {
		return nil, nil, nil, BadRequest("Body phải là Parameters")
	}
	list, _ := body["parameter"].([]any)
	for _, p := range list {
		pm, _ := p.(map[string]any)
		switch name, _ := pm["name"].(string); name {
		case "concept":
			cd, _ := pm["valueCoding"].(map[string]any)
			code, _ := cd["code"].(string)
			if code == "" {
				return nil, nil, nil, BadRequest("concept thiếu code")
			}
			disp, _ := cd["display"].(string)
			concepts = append(concepts, Concept{Code: code, Display: norm.NFC.String(disp)})
		case "property":
			parts := importParts(pm)
			if parts["code"] == "" || parts["property"] == "" || parts["value"] == "" {
				return nil, nil, nil, BadRequest("property cần code, property và value")
			}
			props = append(props, ConceptProperty{Code: parts["code"], Property: parts["property"], Value: parts["value"]})
		case "designation":
			parts := importParts(pm)
			if parts["code"] == "" || parts["value"] == "" {
				return nil, nil, nil, BadRequest("designation cần code và value")
			}
			desigs = append(desigs, ConceptDesignation{Code: parts["code"], Language: parts["language"], Value: norm.NFC.String(parts["value"])})
		}
	}
	if len(concepts) > maxImportItems || len(props) > maxImportItems || len(desigs) > maxImportItems {
		return nil, nil, nil, BadRequest(fmt.Sprintf("Mỗi loại tối đa %d phần tử mỗi lần gọi", maxImportItems))
	}
	return concepts, props, desigs, nil
}

func importParts(pm map[string]any) map[string]string {
	out := map[string]string{}
	parts, _ := pm["part"].([]any)
	for _, p := range parts {
		m, _ := p.(map[string]any)
		name, _ := m["name"].(string)
		for k, v := range m {
			if strings.HasPrefix(k, "value") {
				out[name] = fmt.Sprint(v)
			}
		}
	}
	return out
}
