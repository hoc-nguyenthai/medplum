package fhirrouter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strings"

	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirpath"
	"trivita-fhir/pkg/fhirsearch"
)

// Các operation phục vụ lâm sàng và tiện ích dữ liệu (#19), port từ Medplum: `$csv`, `$refresh-reference-display`, `StructureDefinition/$expand-profile`, `$resend`.

func (r *Router) registerClinical() {
	r.Add("GET", ":resourceType/$csv", r.csvOp)
	r.Add("POST", ":resourceType/:id/$refresh-reference-display", r.refreshReferenceDisplay)
	r.Add("GET", "StructureDefinition/$expand-profile", r.expandProfile)
	r.Add("POST", "StructureDefinition/$expand-profile", r.expandProfile)
	r.Add("POST", ":resourceType/:id/$resend", r.resend)
	r.Add("POST", ":resourceType/:id/_history/:vid/$resend", r.resend)
	r.registerGraph()
	r.registerPatientMatch()
	r.registerExtract()
	r.registerPatientSummary()
}

func knownType(rt string) bool {
	_, ok := fhir.NewResource(rt)
	return ok
}

// ---------------------------------------------------------------- $csv

// csvOp: GET /{type}/$csv?_fields=a,b,c&<tham số tìm kiếm>. Mỗi cột là mã search parameter (dùng biểu thức của nó) hoặc một biểu thức FHIRPath; giá trị đầu của mỗi biểu thức được định dạng
// (HumanName, Address, ContactPoint, Reference, CodeableConcept...). Tối đa 1000 dòng. Chống CSV injection: ô bắt đầu bằng = + - @ được thêm dấu nháy đơn (OWASP).
func (r *Router) csvOp(ctx context.Context, req *Request) (*Response, error) {
	rt := req.Params["resourceType"]
	fields := req.Query.Get("_fields")
	if fields == "" {
		return nil, BadRequest("Missing _fields parameter")
	}
	if !knownType(rt) {
		return nil, BadRequest("Unsupported resource type")
	}
	q := url.Values{}
	for k, v := range req.Query {
		if k != "_fields" {
			q[k] = v
		}
	}
	var columns []string
	var exprs []*fhirpath.Expression
	for _, f := range strings.Split(fields, ",") {
		columns = append(columns, f)
		src := f
		if def, ok := fhirsearch.Lookup(rt, f); ok && def.Expression != "" {
			src = def.Expression
		}
		e, err := fhirpath.Compile(src)
		if err != nil {
			return nil, BadRequest("Invalid FHIRPath expression")
		}
		exprs = append(exprs, e)
	}
	sr, err := fhirsearch.Parse(rt, q)
	if err != nil {
		return nil, err
	}
	sr.Count, sr.Total, sr.Offset = 1000, "none", 0
	res, err := r.searchRepo(ctx, r.repoOf(ctx), sr)
	if err != nil {
		return nil, err
	}
	var rows []string
	header := make([]string, len(columns))
	for i, c := range columns {
		header[i] = csvEscapeString(c)
	}
	rows = append(rows, strings.Join(header, ","))
	for _, m := range res.Matches {
		doc, err := toMap(m)
		if err != nil {
			return nil, Internal(err.Error())
		}
		cells := make([]string, len(exprs))
		for i, e := range exprs {
			vals, err := e.Eval(doc, nil)
			if err != nil {
				return nil, BadRequest("Invalid FHIRPath expression")
			}
			if len(vals) > 0 {
				cells[i] = csvEscape(vals[0].V)
			}
		}
		rows = append(rows, strings.Join(cells, ","))
	}
	// BOM UTF-8 để Excel nhận đúng bảng mã
	body := "\xEF\xBB\xBF" + strings.Join(rows, "\n")
	return &Response{Status: 200, Raw: []byte(body), ContentType: "text/csv", Header: map[string]string{"Content-Disposition": "attachment; filename=export.csv"}}, nil
}

func csvEscape(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		if x == "" {
			return ""
		}
		return csvEscapeString(x)
	case bool:
		return fmt.Sprint(x)
	case json.Number:
		return x.String()
	case float64, int, int64:
		return fmt.Sprint(x)
	case map[string]any:
		if _, ok := x["city"]; ok {
			return csvEscapeString(formatAddress(x))
		}
		if _, ok := x["family"]; ok {
			return csvEscapeString(formatHumanName(x))
		}
		if _, ok := x["value"]; ok {
			s, _ := x["value"].(string)
			return csvEscapeString(s)
		}
		if _, ok := x["reference"]; ok {
			s := strings.TrimSpace(dstr(x, "display"))
			if s == "" {
				s = dstr(x, "reference")
			}
			return csvEscapeString(s)
		}
		if _, ok := x["coding"]; ok {
			if cs := dlist(x, "coding"); len(cs) > 0 {
				cd := dobj(cs[0])
				if d := dstr(cd, "display"); d != "" {
					return csvEscapeString(d)
				}
				if c := dstr(cd, "code"); c != "" {
					return csvEscapeString(c)
				}
			}
		}
		if _, ok := x["text"]; ok {
			return csvEscapeString(dstr(x, "text"))
		}
	}
	return ""
}

func csvEscapeString(s string) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), `"`, `""`)
	if s != "" && strings.ContainsRune("=+-@", rune(s[0])) {
		s = "'" + s
	}
	return `"` + s + `"`
}

// ---------------------------------------------------------------- $refresh-reference-display

// references trả con trỏ tới mọi fhir.Reference trong resource (theo KIỂU, đi qua struct, slice và con trỏ).
func references(v any) []*fhir.Reference {
	var out []*fhir.Reference
	refType := reflect.TypeOf(fhir.Reference{})
	var walk func(rv reflect.Value, depth int)
	walk = func(rv reflect.Value, depth int) {
		if depth > 64 {
			return
		}
		switch rv.Kind() {
		case reflect.Pointer, reflect.Interface:
			if !rv.IsNil() {
				walk(rv.Elem(), depth+1)
			}
		case reflect.Struct:
			if rv.Type() == refType {
				if rv.CanAddr() {
					out = append(out, rv.Addr().Interface().(*fhir.Reference))
				}
				return
			}
			for i := 0; i < rv.NumField(); i++ {
				if rv.Type().Field(i).IsExported() {
					walk(rv.Field(i), depth+1)
				}
			}
		case reflect.Slice:
			for i := 0; i < rv.Len(); i++ {
				walk(rv.Index(i), depth+1)
			}
		}
	}
	if rv := reflect.ValueOf(v); rv.Kind() == reflect.Pointer && !rv.IsNil() {
		walk(rv.Elem(), 0)
	}
	return out
}

// splitReference tách "Loại/id" tương đối (hoặc URL tuyệt đối kết thúc bằng Loại/id); ok=false với tham chiếu nội bộ (#), urn:, hay dạng khác.
func splitReference(ref string) (rt, id string, ok bool) {
	if ref == "" || strings.HasPrefix(ref, "#") || strings.HasPrefix(ref, "urn:") {
		return "", "", false
	}
	parts := strings.Split(strings.TrimRight(ref, "/"), "/")
	if len(parts) < 2 {
		return "", "", false
	}
	rt, id = parts[len(parts)-2], parts[len(parts)-1]
	if i := strings.Index(id, "?"); i >= 0 || id == "" || !knownType(rt) {
		return "", "", false
	}
	return rt, id, true
}

// refreshReferenceDisplay: POST /{type}/{id}/$refresh-reference-display. Đặt lại `display` của mọi Reference trong resource theo resource được tham chiếu (getDisplayString); Reference không
// đọc được (không có, đã xóa, không quyền) được giữ nguyên. Cập nhật bằng If-Match theo phiên bản đã đọc.
func (r *Router) refreshReferenceDisplay(ctx context.Context, req *Request) (*Response, error) {
	rt, id := req.Params["resourceType"], req.Params["id"]
	if !knownType(rt) {
		return nil, BadRequest("Invalid resource type")
	}
	repo := r.repoOf(ctx)
	res, err := repo.ReadResource(ctx, rt, id)
	if err != nil {
		return nil, err
	}
	cache := map[string]string{}
	changed := false
	for _, ref := range references(res) {
		trt, tid, ok := splitReference(ref.Reference)
		if !ok {
			continue
		}
		key := trt + "/" + tid
		disp, seen := cache[key]
		if !seen {
			target, err := repo.ReadResource(ctx, trt, tid)
			if err != nil {
				cache[key] = "\x00" // không đọc được: bỏ qua
				continue
			}
			doc, err := toMap(target)
			if err != nil {
				return nil, Internal(err.Error())
			}
			disp = displayString(doc)
			cache[key] = disp
		}
		if disp == "\x00" || disp == "" {
			continue
		}
		if ref.Display != disp {
			ref.Display = disp
			changed = true
		}
	}
	if !changed {
		return &Response{Status: 200, Resource: res}, nil
	}
	opts := UpdateOptions{}
	if m := res.GetMeta(); m != nil {
		opts.IfMatch = m.VersionId
	}
	updated, err := repo.UpdateResource(ctx, res, opts)
	if err != nil {
		return nil, err
	}
	return &Response{Status: 200, Resource: updated}, nil
}

// ---------------------------------------------------------------- StructureDefinition/$expand-profile

// expandProfile: GET|POST StructureDefinition/$expand-profile?url=<profile>. Trả Bundle searchset gồm profile và mọi profile mà `snapshot.element.type.profile` của nó tham chiếu
// (đệ quy tối đa 10 tầng, mỗi URL một lần). Mỗi URL lấy bản `version` mới nhất.
func (r *Router) expandProfile(ctx context.Context, req *Request) (*Response, error) {
	u := req.Query.Get("url")
	if u == "" {
		if in, err := ParseInput(req); err == nil {
			u = in.String("url")
		}
	}
	if u == "" {
		return nil, BadRequest("StructureDefinition profile url not specified")
	}
	repo := r.repoOf(ctx)
	profile, err := r.profileByURL(ctx, repo, u)
	if err != nil {
		return nil, err
	}
	if profile == nil {
		return nil, BadRequest("StructureDefinition profile with URL " + u + " not found")
	}
	seen := map[string]bool{u: true}
	all := []*fhir.StructureDefinition{profile}
	var load func(p *fhir.StructureDefinition, depth int) error
	load = func(p *fhir.StructureDefinition, depth int) error {
		if depth > 10 || p.Snapshot == nil {
			return nil
		}
		for _, el := range p.Snapshot.Element {
			for _, t := range el.Type {
				for _, pu := range t.Profile {
					if seen[pu] {
						continue
					}
					seen[pu] = true
					sd, err := r.profileByURL(ctx, repo, pu)
					if err != nil {
						return err
					}
					if sd == nil {
						continue
					}
					all = append(all, sd)
					if err := load(sd, depth+1); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}
	if err := load(profile, 1); err != nil {
		return nil, err
	}
	b := &fhir.Bundle{Type: "searchset"}
	base := strings.TrimRight(r.opts.BaseURL, "/")
	for _, sd := range all {
		if sd.ID == "" {
			continue
		}
		b.Entry = append(b.Entry, fhir.BundleEntry{FullUrl: base + "/StructureDefinition/" + sd.ID, Resource: &fhir.AnyResource{Value: sd}})
	}
	return &Response{Status: http.StatusOK, Resource: b}, nil
}

func (r *Router) profileByURL(ctx context.Context, repo Repository, u string) (*fhir.StructureDefinition, error) {
	sr, err := fhirsearch.Parse("StructureDefinition", url.Values{"url": {u}, "_sort": {"-version"}, "_count": {"1"}})
	if err != nil {
		return nil, err
	}
	res, err := repo.Search(ctx, sr)
	if err != nil {
		return nil, err
	}
	if len(res.Matches) == 0 {
		return nil, nil
	}
	sd, _ := res.Matches[0].(*fhir.StructureDefinition)
	return sd, nil
}

// ---------------------------------------------------------------- $resend

// SubscriptionResender là tùy chọn của Repository: phát lại sự kiện ghi của một resource (bản hiện tại hoặc một phiên bản lịch sử) để Subscription được xử lý lại. interaction là
// "create", "update" (mặc định) hoặc "delete".
type SubscriptionResender interface {
	ResendSubscriptions(ctx context.Context, resourceType, id, versionID, interaction string) error
}

// resend: POST /{type}/{id}/$resend và /{type}/{id}/_history/{vid}/$resend, body (tùy chọn) {"interaction": "create|update|delete"}. Chỉ quản trị project (kiểm ở repository đã tách project).
func (r *Router) resend(ctx context.Context, req *Request) (*Response, error) {
	rt, id, vid := req.Params["resourceType"], req.Params["id"], req.Params["vid"]
	if !knownType(rt) {
		return nil, BadRequest("Invalid resource type")
	}
	interaction := "update"
	if m, ok := req.Body.(map[string]any); ok {
		if v, ok := m["interaction"].(string); ok && v != "" {
			interaction = v
		}
	}
	switch interaction {
	case "create", "update", "delete":
	default:
		return nil, BadRequest("interaction phải là create, update hoặc delete")
	}
	rs, ok := r.repoOf(ctx).(SubscriptionResender)
	if !ok {
		return nil, NotImplemented("Repository không hỗ trợ gửi lại Subscription (cần bật FHIR_SUBSCRIPTIONS)")
	}
	if err := rs.ResendSubscriptions(ctx, rt, id, vid, interaction); err != nil {
		return nil, err
	}
	return &Response{Status: 200, Resource: informational("Resent " + interaction + " of " + rt + "/" + id)}, nil
}
