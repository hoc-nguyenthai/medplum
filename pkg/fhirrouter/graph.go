package fhirrouter

import (
	"context"
	"net/url"
	"strconv"
	"strings"

	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirpath"
	"trivita-fhir/pkg/fhirsearch"
)

// `$graph` (port của resourcegraph.ts): GET|POST /{type}/{id}/$graph?graph=<tên GraphDefinition>. Đi theo các link của GraphDefinition từ resource gốc: link có `path` là biểu thức
// FHIRPath trả Reference (hoặc URL canonical, tìm theo `url`), link không có `path` mà target có `params` là tìm kiếm với `{ref}` thay bằng tham chiếu của resource hiện tại. Trả Bundle
// `collection` không trùng lặp. Chặn vòng lặp: tối đa 1000 resource và 5 tầng.

const (
	graphMaxResources = 1000
	graphMaxDepth     = 5
	graphDefaultCount = 20
	graphMaxCount     = 5000
)

func (r *Router) registerGraph() {
	r.Add("GET", ":resourceType/:id/$graph", r.graphOp)
	r.Add("POST", ":resourceType/:id/$graph", r.graphOp)
}

type graphWalk struct {
	noJobs  bool // người gọi không được xem mọi job: không đi vào AsyncJob
	repo    Repository
	ctx     context.Context
	cache   map[string]fhir.Resource // theo "Loại/id" và theo canonical url
	results []fhir.Resource
}

func refKey(res fhir.Resource) string { return res.GetResourceType() + "/" + res.GetID() }

func (g *graphWalk) remember(res fhir.Resource) {
	g.cache[refKey(res)] = res
	if doc, err := toMap(res); err == nil {
		if u := dstr(doc, "url"); u != "" {
			g.cache[u] = res
		}
	}
}

func dedupe(rs []fhir.Resource) []fhir.Resource {
	seen := map[string]bool{}
	var out []fhir.Resource
	for _, r := range rs {
		k := refKey(r)
		if !seen[k] {
			seen[k] = true
			out = append(out, r)
		}
	}
	return out
}

func (r *Router) graphOp(ctx context.Context, req *Request) (*Response, error) {
	rt, id := req.Params["resourceType"], req.Params["id"]
	name := req.Query.Get("graph")
	if name == "" {
		if in, err := ParseInput(req); err == nil {
			name = in.String("graph")
		}
	}
	if name == "" {
		return nil, BadRequest("Missing required parameter: graph")
	}
	repo := r.repoOf(ctx)
	sr, err := fhirsearch.Parse("GraphDefinition", url.Values{"name": {name}, "_count": {"1"}})
	if err != nil {
		return nil, err
	}
	found, err := repo.Search(ctx, sr)
	if err != nil {
		return nil, err
	}
	if len(found.Matches) == 0 {
		return nil, NotFound()
	}
	def := found.Matches[0].(*fhir.GraphDefinition)
	if def.Start == "" || def.Start != rt {
		return nil, BadRequest("Missing or incorrect `start` type")
	}
	root, err := repo.ReadResource(ctx, rt, id)
	if err != nil {
		return nil, err
	}
	g := &graphWalk{repo: repo, ctx: ctx, noJobs: !r.jobsAll(ctx), cache: map[string]fhir.Resource{}, results: []fhir.Resource{root}}
	g.remember(root)
	if err := g.follow(root, def.Link, 0); err != nil {
		return nil, err
	}
	b := &fhir.Bundle{Type: "collection"}
	for _, res := range dedupe(g.results) {
		b.Entry = append(b.Entry, fhir.BundleEntry{Resource: &fhir.AnyResource{Value: res}})
	}
	return &Response{Status: 200, Resource: b}, nil
}

// checkTarget từ chối đích liên kết là AsyncJob với người không xem được mọi job: GraphDefinition do quản trị soạn không được dùng làm đường vòng qua
// kiểm chủ sở hữu job. Kiểm cả hai kiểu liên kết (path và tìm kiếm), kể cả đích khác nằm cùng một link.
func (g *graphWalk) checkTarget(rt string) error {
	if g.noJobs && rt == "AsyncJob" {
		return Forbidden("AsyncJob không truy vấn được bằng $graph")
	}
	return nil
}

func (g *graphWalk) follow(res fhir.Resource, links []fhir.GraphDefinitionLink, depth int) error {
	if len(g.results) > graphMaxResources || depth >= graphMaxDepth || len(links) == 0 {
		return nil
	}
	for _, link := range links {
		for _, target := range link.Target {
			if err := g.checkTarget(target.Type); err != nil {
				return err
			}
			var linked []fhir.Resource
			var err error
			switch {
			case link.Path != "":
				linked, err = g.followPath(link, target, res)
			case target.Type != "" && target.Params != "":
				linked, err = g.followSearch(res, link)
			default:
				err = BadRequest("Invalid link: thiếu path hoặc target.type và target.params")
			}
			if err != nil {
				return err
			}
			linked = dedupe(linked)
			g.results = append(g.results, linked...)
			for _, l := range linked {
				if err := g.follow(l, target.Link, depth+1); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (g *graphWalk) followPath(link fhir.GraphDefinitionLink, target fhir.GraphDefinitionLinkTarget, res fhir.Resource) ([]fhir.Resource, error) {
	doc, err := toMap(res)
	if err != nil {
		return nil, Internal(err.Error())
	}
	vals, err := fhirpath.Evaluate(link.Path, doc, nil)
	if err != nil {
		return nil, BadRequest("Invalid link path: " + err.Error())
	}
	var out []fhir.Resource
	for _, v := range vals {
		switch x := v.V.(type) {
		case map[string]any:
			if v.T != "Reference" {
				return nil, BadRequest("Invalid link path. Must return a path to a Reference type")
			}
			ref := dstr(x, "reference")
			if ref == "" || strings.SplitN(ref, "/", 2)[0] != target.Type {
				continue
			}
			if c, ok := g.cache[ref]; ok {
				out = append(out, c)
				continue
			}
			trt, tid, ok := splitReference(ref)
			if !ok {
				continue
			}
			got, err := g.repo.ReadResource(g.ctx, trt, tid)
			if err != nil {
				return nil, err
			}
			g.remember(got)
			out = append(out, got)
		case string: // canonical: tìm theo url
			if c, ok := g.cache[x]; ok {
				out = append(out, c)
				continue
			}
			sr, err := fhirsearch.Parse(target.Type, url.Values{"url": {x}})
			if err != nil {
				return nil, err
			}
			res, err := g.repo.Search(g.ctx, sr)
			if err != nil {
				return nil, err
			}
			for _, m := range res.Matches {
				g.remember(m)
				out = append(out, m)
			}
		default:
			return nil, BadRequest("Invalid link path. Must return a path to a Reference type")
		}
	}
	return out, nil
}

func (g *graphWalk) followSearch(res fhir.Resource, link fhir.GraphDefinitionLink) ([]fhir.Resource, error) {
	var out []fhir.Resource
	for _, t := range link.Target {
		if err := g.checkTarget(t.Type); err != nil {
			return nil, err
		}
		if !strings.Contains(t.Params, "{ref}") {
			return nil, BadRequest("Link target search params must include {ref}")
		}
		q, err := url.ParseQuery(strings.ReplaceAll(t.Params, "{ref}", url.QueryEscape(refKey(res))))
		if err != nil {
			return nil, BadRequest("Link target params không hợp lệ")
		}
		sr, err := fhirsearch.Parse(t.Type, q)
		if err != nil {
			return nil, err
		}
		count := graphDefaultCount
		switch link.Max {
		case "":
		case "*":
			count = graphMaxCount
		default:
			if n, err := strconv.Atoi(link.Max); err == nil && n > 0 {
				count = min(n, graphMaxCount)
			}
		}
		sr.Count, sr.Total = count, "none"
		found, err := g.repo.Search(g.ctx, sr)
		if err != nil {
			return nil, err
		}
		for _, m := range found.Matches {
			g.remember(m)
			out = append(out, m)
		}
	}
	return out, nil
}
