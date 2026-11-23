package fhirrouter

import (
	"context"
	"sort"
	"sync"
	"time"

	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirsearch"
)

var (
	capOnce sync.Once
	capRest fhir.CapabilityStatementRest
)

func buildRest() {
	bt, bf := true, false
	capRest = fhir.CapabilityStatementRest{
		Mode: "server",
		Interaction: []fhir.CapabilityStatementRestInteraction{
			{Code: "transaction"}, {Code: "batch"}, {Code: "search-system"},
		},
	}
	for _, rt := range fhir.ResourceTypes {
		res := fhir.CapabilityStatementRestResource{
			Type:              rt,
			Versioning:        "versioned",
			ReadHistory:       &bt,
			UpdateCreate:      &bf,
			ConditionalCreate: &bt,
			ConditionalUpdate: &bt,
			ConditionalDelete: "single",
		}
		for _, code := range []string{"read", "vread", "update", "patch", "delete", "history-instance", "create", "search-type"} {
			res.Interaction = append(res.Interaction, fhir.CapabilityStatementRestResourceInteraction{Code: code})
		}
		seen := map[string]bool{}
		defs := fhir.SearchParams(rt)
		sort.SliceStable(defs, func(i, j int) bool { return defs[i].Code < defs[j].Code })
		for _, d := range defs {
			if seen[d.Code] {
				continue
			}
			seen[d.Code] = true
			if d.Type == "composite" || d.Type == "special" {
				continue // chưa hỗ trợ
			}
			if def, ok := fhirsearch.Lookup(rt, d.Code); ok {
				d = def
			}
			res.SearchParam = append(res.SearchParam, fhir.CapabilityStatementRestResourceSearchParam{Name: d.Code, Type: d.Type, Definition: d.URL})
		}
		capRest.Resource = append(capRest.Resource, res)
	}
}

func (r *Router) capabilities(context.Context, *Request) (*Response, error) {
	capOnce.Do(buildRest)
	bf := false
	cs := &fhir.CapabilityStatement{
		Status:       "active",
		Date:         time.Now().UTC().Format(time.RFC3339),
		Kind:         "instance",
		Experimental: &bf,
		FhirVersion:  "4.0.1",
		Format:       []string{"application/fhir+json"},
		PatchFormat:  []string{"application/json-patch+json"},
		Software:     &fhir.CapabilityStatementSoftware{Name: "trivita-fhir"},
		Rest:         []fhir.CapabilityStatementRest{capRest},
	}
	return &Response{Status: 200, Resource: cs}, nil
}
