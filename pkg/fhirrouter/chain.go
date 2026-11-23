package fhirrouter

import (
	"context"
	"encoding/json"
	"strings"

	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirsearch"
)

// Chained search và _has được thực thi bằng sub-search rồi đổi thành điều kiện thường, nên Repository (bộ nhớ, SQL)
// không cần biết tới chúng và hành xử giống nhau:
//
//	Observation?subject:Patient.name=smith    -> tìm Patient name=smith, rồi Observation?subject=Patient/<id>,...
//	Patient?_has:Observation:subject:code=x   -> tìm Observation code=x, rồi Patient?_id=<id từ subject>,...
//
// Sub-search đi qua cùng Repository (nên giữ nguyên phân tách theo project) và cùng cơ chế chained (lồng nhiều cấp).
// Giới hạn: một sub-search khớp quá maxChainMatches resource thì báo lỗi 400 (thu hẹp tiêu chí); kết quả sub-search
// là ảnh chụp tại thời điểm tìm, không nhất quán với ghi đồng thời.
const (
	maxChainMatches = 5000
	chainPage       = 1000
	maxChainDepth   = 4
	noMatch         = "__no-match__"
)

// searchRepo chạy search sau khi đã giải các điều kiện chained và _has.
func (r *Router) searchRepo(ctx context.Context, repo Repository, sr *fhirsearch.Request) (*SearchResult, error) {
	return searchResolved(ctx, repo, sr, 0)
}

func searchResolved(ctx context.Context, repo Repository, sr *fhirsearch.Request, depth int) (*SearchResult, error) {
	if !fhirsearch.NeedsResolution(sr) {
		return repo.Search(ctx, sr)
	}
	sr, err := resolveTerminology(ctx, repo, sr)
	if err != nil {
		return nil, err
	}
	if len(sr.Chained) == 0 {
		return repo.Search(ctx, sr)
	}
	resolved, err := resolveChains(ctx, repo, sr, depth)
	if err != nil {
		return nil, err
	}
	return repo.Search(ctx, resolved)
}

func resolveChains(ctx context.Context, repo Repository, sr *fhirsearch.Request, depth int) (*fhirsearch.Request, error) {
	if depth >= maxChainDepth {
		return nil, BadRequest("chained search lồng quá sâu")
	}
	out := *sr
	out.Filters = append([]fhirsearch.Filter(nil), sr.Filters...)
	out.Chained = nil
	for _, c := range sr.Chained {
		if !c.Reverse {
			var refs []string
			for _, t := range c.Types {
				ms, err := collect(ctx, repo, t, c, depth)
				if err != nil {
					return nil, err
				}
				for _, m := range ms {
					refs = append(refs, t+"/"+m.GetID())
				}
			}
			if len(refs) == 0 {
				refs = []string{c.Types[0] + "/" + noMatch}
			}
			out.Filters = append(out.Filters, fhirsearch.Filter{Code: c.Code, Def: c.Def, Values: refs})
			continue
		}
		base := sr.ResourceType
		if base == "" && len(sr.Types) == 1 {
			base = sr.Types[0]
		}
		ms, err := collect(ctx, repo, c.Types[0], c, depth)
		if err != nil {
			return nil, err
		}
		seen := map[string]bool{}
		var ids []string
		for _, m := range ms {
			raw, err := json.Marshal(m)
			if err != nil {
				return nil, Internal(err.Error())
			}
			var doc map[string]any
			if err := json.Unmarshal(raw, &doc); err != nil {
				return nil, Internal(err.Error())
			}
			refs, err := fhirsearch.References(doc, c.Def)
			if err != nil {
				return nil, Internal(err.Error())
			}
			for _, ref := range refs {
				if id, ok := strings.CutPrefix(ref, base+"/"); ok && id != "" && !strings.Contains(id, "/") && !seen[id] {
					seen[id] = true
					ids = append(ids, id)
				}
			}
		}
		if len(ids) == 0 {
			ids = []string{noMatch}
		}
		idDef, ok := fhirsearch.Lookup(base, "_id")
		if !ok {
			return nil, Internal("không có tham số _id")
		}
		out.Filters = append(out.Filters, fhirsearch.Filter{Code: "_id", Def: idDef, Values: ids})
	}
	return &out, nil
}

// collect chạy sub-search của một điều kiện chained trên kiểu t và trả về mọi resource khớp (tối đa maxChainMatches).
func collect(ctx context.Context, repo Repository, t string, c fhirsearch.Chained, depth int) ([]fhir.Resource, error) {
	sub, err := fhirsearch.Parse(t, c.Query)
	if err != nil {
		return nil, BadRequest(err.Error())
	}
	sub.Count, sub.Total, sub.Sort = chainPage, "none", nil
	var all []fhir.Resource
	for offset := 0; ; offset += chainPage {
		sub.Offset = offset
		res, err := searchResolved(ctx, repo, sub, depth+1)
		if err != nil {
			return nil, err
		}
		all = append(all, res.Matches...)
		if len(all) > maxChainMatches {
			return nil, BadRequest("chained search/_has khớp quá nhiều resource " + t + " (> " + itoa(maxChainMatches) + "); thu hẹp tiêu chí")
		}
		if !res.More {
			return all, nil
		}
	}
}

// SearchResolved chạy search trên repo sau khi giải chained search và _has. Dùng cho test và mã không đi qua Router.
func SearchResolved(ctx context.Context, repo Repository, sr *fhirsearch.Request) (*SearchResult, error) {
	return searchResolved(ctx, repo, sr, 0)
}
