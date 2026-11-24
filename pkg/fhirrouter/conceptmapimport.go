package fhirrouter

import (
	"context"
	"encoding/json"
	"fmt"
	"golang.org/x/text/unicode/norm"
	"strings"

	"trivita-fhir/pkg/fhir"
)

// ConceptMap/$import và ConceptMap/{id}/$import (Medplum: conceptmapimport.ts). Nạp ánh xạ vào kho ánh xạ dẫn xuất
// (MappingStore) thay vì sửa JSON của ConceptMap, để nạp được bảng ánh xạ lớn; ConceptMap/$translate đọc lại từ đó.
//
// Tham số (Parameters): url (uri; chỉ với đường dẫn không có id), mapping (lặp; part: source, target (valueCoding; target.system
// bắt buộc, để trống target.code cho ánh xạ rỗng), relationship (code, mặc định equivalent), comment, property, dependsOn, product
// (mỗi cái lặp; part: code, value[x])). Tối đa maxImportItems mapping mỗi lần gọi. Chỉ admin; trả ConceptMap. Nạp lại cùng
// (nguồn, đích) là ghi đè nên an toàn. Khác Medplum: ánh xạ trùng nguồn và đích gộp thành một dòng.

func (r *Router) registerConceptMapImport() {
	r.Add("POST", "ConceptMap/$import", r.conceptMapImport)
	r.Add("POST", "ConceptMap/:id/$import", r.conceptMapImport)
}

func (r *Router) conceptMapImport(ctx context.Context, req *Request) (*Response, error) {
	if ac, ok := r.repo.(AdminChecker); ok {
		if err := ac.RequireAdmin(ctx); err != nil {
			return nil, err
		}
	}
	if _, ok := r.repo.(MappingStore); !ok {
		return nil, NotImplemented("Repository không hỗ trợ kho ánh xạ")
	}
	in, err := ParseInput(req)
	if err != nil {
		return nil, err
	}
	id, u := req.Params["id"], in.String("url")
	switch {
	case id != "" && u != "":
		return nil, BadRequest("Tham số url không dùng được với operation trên một ConceptMap cụ thể")
	case id == "" && u == "":
		return nil, BadRequest("Chưa chỉ định ConceptMap (url hoặc id)")
	}
	ms, err := parseMappings(in.Body)
	if err != nil {
		return nil, err
	}
	repo := r.repoOf(ctx)
	var cm *fhir.ConceptMap
	if id != "" {
		res, err := repo.ReadResource(ctx, "ConceptMap", id)
		if err != nil {
			return nil, err
		}
		cm, _ = res.(*fhir.ConceptMap)
	} else {
		if v := in.String("conceptMapVersion"); v != "" && !strings.Contains(u, "|") {
			u += "|" + v
		}
		res, err := r.findByURL(ctx, repo, "ConceptMap", u)
		if err != nil {
			return nil, err
		}
		cm, _ = res.(*fhir.ConceptMap)
	}
	if cm == nil {
		return nil, NotFound()
	}
	put := func(rp Repository) error {
		st, ok := rp.(MappingStore)
		if !ok {
			return NotImplemented("Repository không hỗ trợ kho ánh xạ")
		}
		return st.PutMappings(ctx, cm.ID, ms)
	}
	if tx, ok := repo.(Transactional); ok {
		err = tx.RunInTransaction(ctx, put)
	} else {
		err = put(repo)
	}
	if err != nil {
		return nil, err
	}
	return &Response{Status: 200, Resource: cm}, nil
}

func parseMappings(body map[string]any) ([]StoredMapping, error) {
	if body == nil || body["resourceType"] != "Parameters" {
		return nil, BadRequest("Body phải là Parameters")
	}
	var out []StoredMapping
	list, _ := body["parameter"].([]any)
	for _, p := range list {
		pm, _ := p.(map[string]any)
		if pm["name"] != "mapping" {
			continue
		}
		var m StoredMapping
		var attrs []mappingAttr
		parts, _ := pm["part"].([]any)
		for _, pt := range parts {
			x, _ := pt.(map[string]any)
			switch name, _ := x["name"].(string); name {
			case "source", "target":
				cd, _ := x["valueCoding"].(map[string]any)
				sys, code, disp := str(cd["system"]), str(cd["code"]), norm.NFC.String(str(cd["display"]))
				if name == "source" {
					m.SourceSystem, m.SourceCode, m.SourceDisplay = sys, code, disp
				} else {
					m.TargetSystem, m.TargetCode, m.TargetDisplay = sys, code, disp
				}
			case "relationship":
				m.Equivalence = str(x["valueCode"])
				if m.Equivalence == "equivalent" {
					m.Equivalence = ""
				}
			case "comment":
				m.Comment = str(x["valueString"])
			case "property", "dependsOn", "product":
				a := mappingAttr{Kind: name}
				sub, _ := x["part"].([]any)
				for _, sp := range sub {
					sm, _ := sp.(map[string]any)
					switch sn, _ := sm["name"].(string); sn {
					case "code":
						a.Code = str(sm["valueCode"])
					case "value":
						for k, v := range sm {
							if strings.HasPrefix(k, "value") {
								a.Type, a.Value = strings.TrimPrefix(k, "value"), v
							}
						}
					}
				}
				if a.Code == "" {
					return nil, BadRequest(name + " thiếu code")
				}
				attrs = append(attrs, a)
			}
		}
		if m.SourceCode == "" {
			return nil, BadRequest("mapping thiếu source.code")
		}
		if m.TargetSystem == "" {
			return nil, BadRequest("mapping thiếu target.system")
		}
		if len(attrs) > 0 {
			raw, err := json.Marshal(attrs)
			if err != nil {
				return nil, BadRequest(err.Error())
			}
			m.Attributes = string(raw)
		}
		out = append(out, m)
	}
	if len(out) == 0 {
		return nil, BadRequest("Cần ít nhất một mapping")
	}
	if len(out) > maxImportItems {
		return nil, BadRequest(fmt.Sprintf("Tối đa %d mapping mỗi lần gọi", maxImportItems))
	}
	return out, nil
}

type mappingAttr struct {
	Kind  string `json:"kind"`
	Code  string `json:"code"`
	Type  string `json:"type,omitempty"`
	Value any    `json:"value,omitempty"`
}
