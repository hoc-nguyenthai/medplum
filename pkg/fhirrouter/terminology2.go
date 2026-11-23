package fhirrouter

import (
	"context"
	"net/url"
	"sort"
	"strings"

	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirsearch"
)

// $subsumes và $translate trên CodeSystem/ConceptMap lưu trong server (cùng giới hạn với terminology.go: không có
// terminology ngoài như SNOMED hay LOINC).
//
//	CodeSystem/$subsumes?system=S&codeA=a&codeB=b      (hoặc codingA/codingB, CodeSystem/{id}/$subsumes)
//	  -> outcome: equivalent | subsumes (A là tổ tiên của B) | subsumed-by (A là con cháu của B) | not-subsumed
//	ConceptMap/$translate?url=U&system=S&code=c        (hoặc ConceptMap/{id}/$translate, coding, targetsystem, reverse)
//	  -> result và các match (equivalence, concept, source). Không có url/id thì tìm ConceptMap theo source-system
//	     (và target-system nếu có targetsystem).
func (r *Router) registerTerminology2() {
	for _, m := range []string{"GET", "POST"} {
		r.Add(m, "CodeSystem/$subsumes", r.subsumes)
		r.Add(m, "CodeSystem/:id/$subsumes", r.subsumes)
		r.Add(m, "ConceptMap/$translate", r.translate)
		r.Add(m, "ConceptMap/:id/$translate", r.translate)
	}
}

func (r *Router) subsumes(ctx context.Context, req *Request) (*Response, error) {
	in, err := ParseInput(req)
	if err != nil {
		return nil, err
	}
	repo := r.repoOf(ctx)
	a, b, system := in.String("codeA"), in.String("codeB"), in.String("system")
	if ca := codingParam(in, "codingA"); ca != nil && a == "" {
		a, system = ca.code, firstNonEmpty(ca.system, system)
	}
	if cb := codingParam(in, "codingB"); cb != nil && b == "" {
		b, system = cb.code, firstNonEmpty(cb.system, system)
	}
	if a == "" || b == "" {
		return nil, BadRequest("Thiếu codeA/codeB (hoặc codingA/codingB)")
	}
	cs, err := r.codeSystemFor(ctx, repo, req, &Input{values: map[string][]string{"system": {system}, "version": in.values["version"]}}, "system")
	if err != nil {
		return nil, err
	}
	ix := indexCodeSystem(cs)
	for _, c := range []string{a, b} {
		if _, ok := ix.byKey[c]; !ok {
			return nil, NotFoundMsg("Không có mã " + c + " trong " + cs.URL)
		}
	}
	outcome := "not-subsumed"
	switch {
	case a == b:
		outcome = "equivalent"
	case isAncestor(ix, a, b):
		outcome = "subsumes"
	case isAncestor(ix, b, a):
		outcome = "subsumed-by"
	}
	return &Response{Status: 200, Resource: &fhir.Parameters{Parameter: []fhir.ParametersParameter{{Name: "outcome", ValueCode: outcome}}}}, nil
}

// isAncestor cho biết anc là tổ tiên thật sự của code.
func isAncestor(ix *csIndex, anc, code string) bool {
	for p := ix.byKey[code].parent; p != ""; p = ix.byKey[p].parent {
		if p == anc {
			return true
		}
	}
	return false
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

type translation struct {
	equivalence, system, code, display, source string
}

func (r *Router) translate(ctx context.Context, req *Request) (*Response, error) {
	in, err := ParseInput(req)
	if err != nil {
		return nil, err
	}
	repo := r.repoOf(ctx)
	code, system := in.String("code"), in.String("system")
	if code == "" {
		if cd := codingParam(in, "coding"); cd != nil {
			code, system = cd.code, cd.system
		}
	}
	if code == "" {
		if cd := codingParam(in, "codeableConcept"); cd != nil {
			code, system = cd.code, cd.system
		}
	}
	if code == "" {
		return nil, BadRequest("Thiếu tham số code (hoặc coding)")
	}
	target := in.String("targetsystem")
	reverse := in.String("reverse") == "true"

	var maps []*fhir.ConceptMap
	switch {
	case req.Params["id"] != "":
		res, err := repo.ReadResource(ctx, "ConceptMap", req.Params["id"])
		if err != nil {
			return nil, err
		}
		cm, ok := res.(*fhir.ConceptMap)
		if !ok {
			return nil, NotFound()
		}
		maps = append(maps, cm)
	case in.String("url") != "":
		u := in.String("url")
		if v := in.String("conceptMapVersion"); v != "" && !strings.Contains(u, "|") {
			u += "|" + v
		}
		res, err := r.findByURL(ctx, repo, "ConceptMap", u)
		if err != nil {
			return nil, err
		}
		maps = append(maps, res.(*fhir.ConceptMap))
	default:
		if system == "" {
			return nil, BadRequest("Thiếu url/id của ConceptMap hoặc system của mã nguồn")
		}
		q := url.Values{"_count": {"50"}}
		from, to := "source-system", "target-system"
		if reverse {
			from, to = to, from
		}
		q.Set(from, system)
		if target != "" {
			q.Set(to, target)
		}
		sr, err := fhirsearch.Parse("ConceptMap", q)
		if err != nil {
			return nil, BadRequest(err.Error())
		}
		sr.Total = "none"
		res, err := repo.Search(ctx, sr)
		if err != nil {
			return nil, err
		}
		for _, m := range res.Matches {
			if cm, ok := m.(*fhir.ConceptMap); ok {
				maps = append(maps, cm)
			}
		}
	}

	var found []translation
	seen := map[string]bool{}
	add := func(t translation) {
		k := t.equivalence + "|" + t.system + "|" + t.code + "|" + t.source
		if !seen[k] {
			seen[k] = true
			found = append(found, t)
		}
	}
	for _, cm := range maps {
		src := cm.URL
		for _, g := range cm.Group {
			from, to := g.Source, g.Target
			if reverse {
				from, to = to, from
			}
			if system != "" && from != "" && from != system {
				continue
			}
			if target != "" && to != target {
				continue
			}
			matched := false
			for _, el := range g.Element {
				if !reverse {
					if el.Code != code {
						continue
					}
					for _, t := range el.Target {
						if t.Code == "" && t.Equivalence != "unmatched" {
							continue
						}
						matched = true
						add(translation{t.Equivalence, g.Target, t.Code, t.Display, src})
					}
					continue
				}
				for _, t := range el.Target { // dịch ngược: tìm phần tử có đích là mã đã cho, trả về mã nguồn của nó
					if t.Code == code {
						matched = true
						add(translation{reverseEquivalence(t.Equivalence), g.Source, el.Code, el.Display, src})
					}
				}
			}
			if !matched && g.Unmapped != nil && !reverse {
				switch g.Unmapped.Mode {
				case "provided":
					add(translation{"equivalent", g.Target, code, "", src})
				case "fixed":
					add(translation{"equivalent", g.Target, g.Unmapped.Code, g.Unmapped.Display, src})
				}
			}
		}
	}
	sort.SliceStable(found, func(i, j int) bool {
		return equivalenceRank(found[i].equivalence) < equivalenceRank(found[j].equivalence)
	})

	ok := false
	out := &fhir.Parameters{}
	var matches []fhir.ParametersParameter
	for _, t := range found {
		if t.equivalence == "unmatched" || t.equivalence == "disjoint" {
			continue // không phải kết quả dịch
		}
		ok = true
		part := []fhir.ParametersParameter{{Name: "equivalence", ValueCode: t.equivalence}}
		if t.code != "" {
			part = append(part, fhir.ParametersParameter{Name: "concept", ValueCoding: &fhir.Coding{System: t.system, Code: t.code, Display: t.display}})
		}
		if t.source != "" {
			part = append(part, fhir.ParametersParameter{Name: "source", ValueUri: t.source})
		}
		matches = append(matches, fhir.ParametersParameter{Name: "match", Part: part})
	}
	out.Parameter = append(out.Parameter, fhir.ParametersParameter{Name: "result", ValueBoolean: &ok})
	if !ok {
		out.Parameter = append(out.Parameter, fhir.ParametersParameter{Name: "message", ValueString: "Không có bản dịch cho " + code})
	}
	out.Parameter = append(out.Parameter, matches...)
	return &Response{Status: 200, Resource: out}, nil
}

// reverseEquivalence đảo chiều quan hệ khi dịch ngược.
func reverseEquivalence(e string) string {
	switch e {
	case "wider":
		return "narrower"
	case "narrower":
		return "wider"
	case "subsumes":
		return "specializes"
	case "specializes":
		return "subsumes"
	}
	return e
}

// equivalenceRank xếp kết quả mạnh trước: tương đương rồi đến rộng/hẹp hơn.
func equivalenceRank(e string) int {
	for i, x := range []string{"equal", "equivalent", "relatedto", "wider", "subsumes", "narrower", "specializes", "inexact", "unmatched", "disjoint"} {
		if x == e {
			return i
		}
	}
	return 99
}
