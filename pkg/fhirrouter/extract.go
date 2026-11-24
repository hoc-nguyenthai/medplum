package fhirrouter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirpath"
	"trivita-fhir/pkg/fhirsearch"
)

// `QuestionnaireResponse/$extract` (SDC template-based extraction, HL7 SDC "templateExtract"; port có chủ đích của extract.ts, viết lại trên JSON thay cho bộ duyệt kiểu và JSON Patch):
// POST /QuestionnaireResponse/{id}/$extract hoặc POST /QuestionnaireResponse/$extract với Parameters {questionnaire-response, questionnaire}. Trả Bundle `transaction` (không ghi gì).
//
// Questionnaire mang các extension SDC (URL cố định bên dưới) và `contained` chứa resource mẫu:
//   - `templateExtract` ở gốc Questionnaire hoặc ở item: trỏ tới mẫu `#id` (extension con `template`) và (tùy chọn) biểu thức resourceId, fullUrl, ifMatch, ifNoneMatch, ifNoneExist, ifModifiedSince.
//     Ngữ cảnh FHIRPath là QuestionnaireResponse (gốc) hoặc từng item của response có linkId đó (item); mỗi giá trị ngữ cảnh sinh một resource.
//   - `extractAllocateId` (valueString = tên): cấp biến %tên = urn:uuid mới (dùng chung cho các mẫu để tham chiếu chéo trong transaction).
//   - Trong mẫu: `templateExtractContext` (valueString hoặc valueExpression FHIRPath; language text/fhirpath) trên một phần tử lặp phần tử đó cho mỗi kết quả, và là ngữ cảnh của
//     phần tử con (valueExpression.name đặt biến %name); `templateExtractValue` (trong `_thuộcTính`) thay giá trị bằng kết quả biểu thức (không có kết quả: xóa thuộc tính; nhiều hơn một ở
//     thuộc tính đơn: 400; ở thuộc tính mảng: thay cả mảng bằng các kết quả).
//   - Mọi extension SDC bị gỡ khỏi resource đầu ra. Không hỗ trợ `application/x-fhir-query` trong context (400 rõ ràng).
const (
	sdcExtractTemplate = "http://hl7.org/fhir/uv/sdc/StructureDefinition/sdc-questionnaire-templateExtract"
	sdcAllocID         = "http://hl7.org/fhir/uv/sdc/StructureDefinition/sdc-questionnaire-extractAllocateId"
	sdcContext         = "http://hl7.org/fhir/uv/sdc/StructureDefinition/sdc-questionnaire-templateExtractContext"
	sdcValue           = "http://hl7.org/fhir/uv/sdc/StructureDefinition/sdc-questionnaire-templateExtractValue"
	maxExtractEntries  = 1000
	maxExtractDepth    = 64
)

func (r *Router) registerExtract() {
	r.Add("POST", "QuestionnaireResponse/:id/$extract", r.extractOp)
	r.Add("POST", "QuestionnaireResponse/$extract", r.extractOp)
}

type extractor struct {
	response  map[string]any
	templates map[string]map[string]any
	vars      map[string][]any
	entries   []fhir.BundleEntry
}

func (r *Router) extractOp(ctx context.Context, req *Request) (*Response, error) {
	in, err := ParseInput(req)
	if err != nil {
		return nil, err
	}
	repo := r.repoOf(ctx)
	var response map[string]any
	switch id := req.Params["id"]; {
	case id != "":
		res, err := repo.ReadResource(ctx, "QuestionnaireResponse", id)
		if err != nil {
			return nil, err
		}
		if response, err = toMap(res); err != nil {
			return nil, Internal(err.Error())
		}
	default:
		doc, ok := in.Resource("questionnaire-response")
		if !ok {
			return nil, BadRequest("QuestionnaireResponse to extract must be specified")
		}
		response = doc
	}
	var questionnaire map[string]any
	if doc, ok := in.Resource("questionnaire"); ok {
		questionnaire = doc
	} else if qurl := dstr(response, "questionnaire"); qurl != "" {
		sr, err := fhirsearch.Parse("Questionnaire", url.Values{"url": {qurl}, "_count": {"1"}})
		if err != nil {
			return nil, err
		}
		found, err := repo.Search(ctx, sr)
		if err != nil {
			return nil, err
		}
		if len(found.Matches) == 0 {
			return nil, BadRequest("Questionnaire with URL " + qurl + " not found")
		}
		if questionnaire, err = toMap(found.Matches[0]); err != nil {
			return nil, Internal(err.Error())
		}
	} else {
		return nil, BadRequest("Questionnaire associated with the response must be specified")
	}

	e := &extractor{response: response, templates: map[string]map[string]any{}, vars: map[string][]any{}}
	for _, c := range dlist(questionnaire, "contained") {
		cm := deepCopy(dobj(c)).(map[string]any)
		if id := dstr(cm, "id"); id != "" {
			delete(cm, "id") // mẫu không mang id (id do resourceId của extension quyết định)
			e.templates["#"+id] = cm
		}
	}
	if err := e.scan(questionnaire, "Questionnaire", 0); err != nil {
		return nil, err
	}
	return &Response{Status: 200, Resource: &fhir.Bundle{Type: "transaction", Entry: e.entries}}, nil
}

func extOrder(url string) int {
	switch url {
	case sdcAllocID:
		return 1
	case sdcContext:
		return 2
	case sdcValue:
		return 3
	case sdcExtractTemplate:
		return 4
	}
	return 5
}

// scan duyệt Questionnaire (bỏ `contained`) theo thứ tự tài liệu: xử lý extension SDC của node rồi đệ quy vào item.
func (e *extractor) scan(node map[string]any, kind string, depth int) error {
	if depth > maxExtractDepth {
		return BadRequest("Questionnaire lồng quá sâu")
	}
	exts := dlist(node, "extension")
	sorted := append([]any(nil), exts...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return extOrder(dstr(dobj(sorted[i]), "url")) < extOrder(dstr(dobj(sorted[j]), "url"))
	})
	for _, x := range sorted {
		ext := dobj(x)
		switch dstr(ext, "url") {
		case sdcAllocID:
			if name := dstr(ext, "valueString"); name != "" {
				e.vars[name] = []any{"urn:uuid:" + newID()}
			}
		case sdcExtractTemplate:
			if err := e.extractInto(ext, kind, node); err != nil {
				return err
			}
		}
	}
	for _, it := range dlist(node, "item") {
		if im := dobj(it); im != nil {
			if err := e.scan(im, "QuestionnaireItem", depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

func (e *extractor) contextValues(kind string, node map[string]any) ([]any, error) {
	switch kind {
	case "Questionnaire":
		return []any{e.response}, nil
	case "QuestionnaireItem":
		var out []any
		var find func(items []any)
		find = func(items []any) {
			for _, it := range items {
				im := dobj(it)
				if dstr(im, "linkId") == dstr(node, "linkId") {
					out = append(out, im)
				} else {
					find(dlist(im, "item"))
				}
			}
		}
		find(dlist(e.response, "item"))
		return out, nil
	}
	return nil, BadRequest("Extraction cannot begin on element of type " + kind)
}

func subExt(ext map[string]any, url string) map[string]any {
	for _, s := range dlist(ext, "extension") {
		if sm := dobj(s); dstr(sm, "url") == url {
			return sm
		}
	}
	return nil
}

func (e *extractor) extractInto(ext map[string]any, kind string, node map[string]any) error {
	ref := ""
	if t := subExt(ext, "template"); t != nil {
		ref = dstr(dobj(t["valueReference"]), "reference")
	}
	tmpl, ok := e.templates[ref]
	if !ok {
		return BadRequest("Missing template resource " + ref)
	}
	values, err := e.contextValues(kind, node)
	if err != nil {
		return err
	}
	for _, v := range values {
		if len(e.entries) >= maxExtractEntries {
			return BadRequest("Quá nhiều resource được trích xuất")
		}
		out, err := e.expandObject(deepCopy(tmpl).(map[string]any), v, 0)
		if err != nil {
			return err
		}
		res, err := toResource(out)
		if err != nil {
			return BadRequest("Template sinh ra resource không hợp lệ: " + err.Error())
		}
		entry, err := e.entry(res, ext, v)
		if err != nil {
			return err
		}
		e.entries = append(e.entries, entry)
	}
	return nil
}

func (e *extractor) entry(res fhir.Resource, ext map[string]any, focus any) (fhir.BundleEntry, error) {
	field := func(name string) (string, error) {
		s := subExt(ext, name)
		if s == nil {
			return "", nil
		}
		vals, err := e.eval(dstr(s, "valueString"), focus)
		if err != nil || len(vals) == 0 {
			return "", err
		}
		return fmt.Sprint(vals[0]), nil
	}
	var vals [6]string
	for i, name := range []string{"resourceId", "fullUrl", "ifMatch", "ifNoneMatch", "ifNoneExist", "ifModifiedSince"} {
		v, err := field(name)
		if err != nil {
			return fhir.BundleEntry{}, err
		}
		vals[i] = v
	}
	rt := res.GetResourceType()
	req := &fhir.BundleEntryRequest{Method: "POST", URL: rt, IfMatch: vals[2], IfNoneMatch: vals[3], IfNoneExist: vals[4], IfModifiedSince: vals[5]}
	if vals[0] != "" {
		setID(res, vals[0])
		req.Method, req.URL = "PUT", rt+"/"+vals[0]
	}
	full := vals[1]
	if full == "" {
		full = "urn:uuid:" + newID()
	}
	return fhir.BundleEntry{FullUrl: full, Resource: &fhir.AnyResource{Value: res}, Request: req}, nil
}

func setID(res fhir.Resource, id string) {
	raw, _ := json.Marshal(res)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	m["id"] = id
	if r2, err := toResource(m); err == nil {
		// sao chép lại vào res (cùng kiểu) qua JSON
		b, _ := json.Marshal(r2)
		_ = json.Unmarshal(b, res)
	}
}

func (e *extractor) eval(expr string, focus any) ([]any, error) {
	if expr == "" {
		return nil, nil
	}
	ex, err := fhirpath.Compile(expr)
	if err != nil {
		return nil, BadRequest("FHIRPath không hợp lệ: " + expr)
	}
	vals, err := ex.EvalAt(focus, "", e.response, nil, &fhirpath.Options{Vars: e.vars})
	if err != nil {
		return nil, BadRequest("Lỗi đánh giá FHIRPath " + expr + ": " + err.Error())
	}
	out := make([]any, len(vals))
	for i, v := range vals {
		out[i] = v.V
	}
	return out, nil
}

// splitExt tách extension SDC khỏi danh sách extension của một node: trả (extension SDC theo url, phần còn lại).
func splitExt(m map[string]any) (sdc map[string]map[string]any, rest []any) {
	sdc = map[string]map[string]any{}
	for _, x := range dlist(m, "extension") {
		xm := dobj(x)
		switch u := dstr(xm, "url"); u {
		case sdcContext, sdcValue, sdcAllocID, sdcExtractTemplate:
			sdc[u] = xm
		default:
			rest = append(rest, x)
		}
	}
	return sdc, rest
}

// contextResults tính kết quả của extension context (valueString hoặc valueExpression) trên focus; trả (kết quả, tên biến nếu có).
func (e *extractor) contextResults(ctxExt map[string]any, focus any) ([]any, string, error) {
	if s := dstr(ctxExt, "valueString"); s != "" {
		r, err := e.eval(s, focus)
		return r, "", err
	}
	ve := dobj(ctxExt["valueExpression"])
	if ve == nil {
		return nil, "", BadRequest("Invalid extraction context extension")
	}
	expr := dstr(ve, "expression")
	if expr == "" {
		return nil, "", BadRequest("Questionnaire extraction context missing expression")
	}
	switch lang := dstr(ve, "language"); lang {
	case "text/fhirpath":
		r, err := e.eval(expr, focus)
		return r, dstr(ve, "name"), err
	default:
		return nil, "", BadRequest("Questionnaire extraction context expression uses unsupported language: " + lang)
	}
}

// expandObject điền mẫu: gỡ extension SDC, thay giá trị theo templateExtractValue, lặp phần tử theo templateExtractContext.
func (e *extractor) expandObject(m map[string]any, focus any, depth int) (map[string]any, error) {
	if depth > maxExtractDepth {
		return nil, BadRequest("Template lồng quá sâu")
	}
	sdc, rest := splitExt(m)
	if x := sdc[sdcAllocID]; x != nil {
		if name := dstr(x, "valueString"); name != "" {
			e.vars[name] = []any{"urn:uuid:" + newID()}
		}
	}
	if len(rest) > 0 {
		m["extension"] = rest
	} else {
		delete(m, "extension")
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v, ok := m[k]
		if !ok {
			continue
		}
		if strings.HasPrefix(k, "_") {
			if err := e.primitiveValue(m, k, v, focus); err != nil {
				return nil, err
			}
			continue
		}
		switch x := v.(type) {
		case map[string]any:
			nv, keep, err := e.expandChild(x, focus, depth)
			if err != nil {
				return nil, err
			}
			if keep {
				m[k] = nv
			} else {
				delete(m, k)
			}
		case []any:
			var out []any
			for _, el := range x {
				em, isMap := el.(map[string]any)
				if !isMap {
					out = append(out, el)
					continue
				}
				items, err := e.expandArrayElement(em, focus, depth)
				if err != nil {
					return nil, err
				}
				out = append(out, items...)
			}
			if len(out) == 0 {
				delete(m, k)
			} else {
				m[k] = out
			}
		}
	}
	return m, nil
}

// expandChild xử lý giá trị đối tượng của một thuộc tính đơn: có context thì dùng kết quả đầu làm ngữ cảnh (không có kết quả: xóa thuộc tính).
func (e *extractor) expandChild(x map[string]any, focus any, depth int) (map[string]any, bool, error) {
	sdc, _ := splitExt(x)
	f := focus
	if c := sdc[sdcContext]; c != nil {
		res, name, err := e.contextResults(c, focus)
		if err != nil {
			return nil, false, err
		}
		if name != "" {
			if len(res) > 0 {
				e.vars[name] = res[:1]
			} else {
				e.vars[name] = nil
			}
		}
		if len(res) == 0 {
			return nil, false, nil
		}
		f = res[0]
	}
	out, err := e.expandObject(x, f, depth+1)
	return out, true, err
}

// expandArrayElement: phần tử mảng có context được lặp cho mỗi kết quả (ngữ cảnh của bản sao là kết quả đó); không có context thì giữ một bản.
func (e *extractor) expandArrayElement(el map[string]any, focus any, depth int) ([]any, error) {
	sdc, _ := splitExt(el)
	c := sdc[sdcContext]
	if c == nil {
		out, err := e.expandObject(el, focus, depth+1)
		if err != nil {
			return nil, err
		}
		return []any{out}, nil
	}
	res, name, err := e.contextResults(c, focus)
	if err != nil {
		return nil, err
	}
	var out []any
	for _, r := range res {
		if name != "" {
			e.vars[name] = []any{r}
		}
		clone, err := e.expandObject(deepCopy(el).(map[string]any), r, depth+1)
		if err != nil {
			return nil, err
		}
		out = append(out, clone)
	}
	return out, nil
}

// primitiveValue xử lý `_thuộcTính` có templateExtractValue: thay giá trị của `thuộcTính`.
func (e *extractor) primitiveValue(m map[string]any, key string, holder any, focus any) error {
	base := key[1:]
	switch h := holder.(type) {
	case map[string]any:
		sdc, rest := splitExt(h)
		vx := sdc[sdcValue]
		if vx == nil {
			return nil
		}
		res, err := e.eval(dstr(vx, "valueString"), focus)
		if err != nil {
			return err
		}
		switch {
		case len(res) == 0:
			delete(m, base)
		case len(res) == 1:
			m[base] = res[0]
		default:
			return BadRequest("Error inserting template value: array not allowed at singleton path " + base)
		}
		if len(rest) > 0 {
			h["extension"] = rest
		} else {
			delete(h, "extension")
		}
		if len(h) == 0 {
			delete(m, key)
		}
	case []any:
		var all []any
		found := false
		for _, el := range h {
			em := dobj(el)
			if em == nil {
				continue
			}
			if vx := func() map[string]any { s, _ := splitExt(em); return s[sdcValue] }(); vx != nil {
				found = true
				res, err := e.eval(dstr(vx, "valueString"), focus)
				if err != nil {
					return err
				}
				all = append(all, res...)
			}
		}
		if found {
			m[base] = all
			delete(m, key)
		}
	}
	return nil
}
