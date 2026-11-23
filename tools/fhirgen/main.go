// Command fhirgen sinh kiểu Go cho FHIR R4 (và các resource riêng của Medplum)
// từ các StructureDefinition trong thư mục definitions của Medplum.
//
// Chạy từ gốc repo:
//
//	go run ./tools/fhirgen -defs medplum/packages/definitions/src/fhir/r4 -out pkg/fhir
//
// Đầu ra (xem pkg/fhir):
//   - types_gen.go           kiểu phức hợp (Element, Coding, Reference, ...)
//   - resource_<tên>.go      mỗi resource một file
//   - registry_gen.go        bảng tên -> factory
//
// Quy ước kiểu:
//   - string, code, id, uri, date, dateTime, ... -> string (rỗng = vắng mặt, FHIR không cho chuỗi rỗng)
//   - decimal -> json.Number (giữ nguyên chữ số, ví dụ 1.0)
//   - boolean, integer, positiveInt, unsignedInt -> *bool, *int (để phân biệt false/0 với vắng mặt)
//   - kiểu phức hợp -> *T, mảng -> []T
//   - choice type value[x] -> một trường cho mỗi kiểu (ValueString, ValueQuantity, ...)
//   - mỗi trường nguyên thủy có thêm trường mở rộng "_name" (hậu tố Ext)
//   - Resource lồng (contained, Bundle.entry.resource) -> AnyResource
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type element struct {
	Path             string `json:"path"`
	Max              string `json:"max"`
	Min              int    `json:"min"`
	Short            string `json:"short"`
	ContentReference string `json:"contentReference"`
	Type             []struct {
		Code      string `json:"code"`
		Extension []struct {
			URL         string `json:"url"`
			ValueString string `json:"valueString"`
		} `json:"extension"`
	} `json:"type"`
	Constraint []struct {
		Key        string `json:"key"`
		Severity   string `json:"severity"`
		Human      string `json:"human"`
		Expression string `json:"expression"`
	} `json:"constraint"`
}

type structDef struct {
	ResourceType string `json:"resourceType"`
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	Derivation   string `json:"derivation"`
	Abstract     bool   `json:"abstract"`
	Snapshot     struct {
		Element []element `json:"element"`
	} `json:"snapshot"`
}

type bundle struct {
	Entry []struct {
		Resource json.RawMessage `json:"resource"`
	} `json:"entry"`
}

type node struct {
	el       element
	children []*node
}

// primitive: mã kiểu FHIR -> (kiểu Go khi vô hướng, có dùng con trỏ không)
var primitive = map[string]struct {
	goType  string
	pointer bool
}{
	"boolean":     {"bool", true},
	"integer":     {"int", true},
	"unsignedInt": {"int", true},
	"positiveInt": {"int", true},
	"decimal":     {"json.Number", false},
	"string":      {"string", false}, "code": {"string", false}, "id": {"string", false},
	"markdown": {"string", false}, "uri": {"string", false}, "url": {"string", false},
	"canonical": {"string", false}, "oid": {"string", false}, "uuid": {"string", false},
	"base64Binary": {"string", false}, "date": {"string", false}, "dateTime": {"string", false},
	"instant": {"string", false}, "time": {"string", false}, "xhtml": {"string", false},
	"http://hl7.org/fhirpath/System.String": {"string", false},
}

var skipTypes = map[string]bool{"Resource": true, "DomainResource": true, "BackboneElement": true}

type generator struct {
	complexTypes map[string]bool // tên kiểu phức hợp hợp lệ
	resources    map[string]bool
	structs      map[string]*bytes.Buffer // tên file -> nội dung
	errs         []string
	schema       map[string][]schemaEntry // tên kiểu (hoặc đường dẫn backbone) -> các phần tử con
	typeCons     map[string][]constraint  // ràng buộc ở gốc của kiểu
	regexes      map[string]string        // kiểu nguyên thủy -> regex
}

func main() {
	defs := flag.String("defs", "medplum/packages/definitions/src/fhir/r4", "thư mục chứa profiles-*.json")
	out := flag.String("out", "pkg/fhir", "thư mục đầu ra")
	pkgName := flag.String("pkg", "fhir", "tên package Go")
	flag.Parse()

	sds, err := load(*defs, "profiles-types.json", "profiles-resources.json", "profiles-medplum.json")
	if err != nil {
		fatal(err)
	}
	g := &generator{complexTypes: map[string]bool{}, resources: map[string]bool{}, structs: map[string]*bytes.Buffer{}, schema: map[string][]schemaEntry{}, typeCons: map[string][]constraint{}, regexes: map[string]string{}}
	var resources, complexes []structDef
	for _, sd := range sds {
		if sd.Derivation == "constraint" || skipTypes[sd.Name] {
			continue
		}
		switch sd.Kind {
		case "primitive-type":
			for _, el := range sd.Snapshot.Element {
				if strings.HasSuffix(el.Path, ".value") {
					for _, t := range el.Type {
						for _, x := range t.Extension {
							if strings.HasSuffix(x.URL, "/regex") {
								g.regexes[sd.Name] = x.ValueString
							}
						}
					}
				}
			}
		case "resource":
			g.resources[sd.Name] = true
			resources = append(resources, sd)
		case "complex-type":
			g.complexTypes[sd.Name] = true
			complexes = append(complexes, sd)
		}
	}
	sort.Slice(resources, func(i, j int) bool { return resources[i].Name < resources[j].Name })
	sort.Slice(complexes, func(i, j int) bool { return complexes[i].Name < complexes[j].Name })

	types := &bytes.Buffer{}
	for _, sd := range complexes {
		g.emit(types, sd, false)
	}
	g.write(*out, "types_gen.go", *pkgName, types)
	for _, sd := range resources {
		b := &bytes.Buffer{}
		g.emit(b, sd, true)
		g.write(*out, "resource_"+strings.ToLower(sd.Name)+".go", *pkgName, b)
	}
	reg := &bytes.Buffer{}
	fmt.Fprintf(reg, "// ResourceTypes liệt kê mọi resource được sinh (đã sắp xếp).\nvar ResourceTypes = []string{\n")
	for _, sd := range resources {
		fmt.Fprintf(reg, "\t%q,\n", sd.Name)
	}
	fmt.Fprintf(reg, "}\n\nvar resourceFactories = map[string]func() Resource{\n")
	for _, sd := range resources {
		fmt.Fprintf(reg, "\t%q: func() Resource { return &%s{} },\n", sd.Name, sd.Name)
	}
	fmt.Fprintf(reg, "}\n")
	g.write(*out, "registry_gen.go", *pkgName, reg)

	g.write(*out, "schema_gen.go", *pkgName, g.emitSchema())
	sp, nsp, err := emitSearchParams(*defs)
	if err != nil {
		fatal(err)
	}
	g.write(*out, "searchparams_gen.go", *pkgName, sp)
	comp, err := emitCompartment(*defs)
	if err != nil {
		fatal(err)
	}
	g.write(*out, "compartment_gen.go", *pkgName, comp)

	if len(g.errs) > 0 {
		sort.Strings(g.errs)
		fatal(fmt.Errorf("lỗi sinh mã:\n  %s", strings.Join(uniq(g.errs), "\n  ")))
	}
	fmt.Printf("fhirgen: %d resource, %d kiểu phức hợp, %d search parameter -> %s\n", len(resources), len(complexes), nsp, *out)
}

func load(dir string, files ...string) ([]structDef, error) {
	var all []structDef
	for _, f := range files {
		raw, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			return nil, err
		}
		var b bundle
		if err := json.Unmarshal(raw, &b); err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		for _, e := range b.Entry {
			var sd structDef
			if err := json.Unmarshal(e.Resource, &sd); err != nil {
				return nil, err
			}
			if sd.ResourceType == "StructureDefinition" {
				all = append(all, sd)
			}
		}
	}
	return all, nil
}

// emit viết struct gốc và mọi struct lồng (BackboneElement) của một StructureDefinition.
func (g *generator) emit(w *bytes.Buffer, sd structDef, isResource bool) {
	els := sd.Snapshot.Element
	if len(els) == 0 {
		g.errs = append(g.errs, sd.Name+": không có snapshot")
		return
	}
	nodes := map[string]*node{}
	var root *node
	for _, el := range els {
		n := &node{el: el}
		nodes[el.Path] = n
		if i := strings.LastIndex(el.Path, "."); i < 0 {
			root = n
		} else if p := nodes[el.Path[:i]]; p != nil {
			p.children = append(p.children, n)
		} else {
			g.errs = append(g.errs, el.Path+": thiếu phần tử cha")
		}
	}
	g.typeCons[sd.Name] = toConstraints(root.el, true)
	g.emitStruct(w, root, sd.Name, sd.Name, isResource, true)
	if isResource {
		fmt.Fprintf(w, "\n// GetResourceType trả về %q.\nfunc (r *%[1]s) GetResourceType() string { return %[1]q }\n", sd.Name)
		fmt.Fprintf(w, "\n// GetID trả về id logic của resource (rỗng nếu chưa có).\nfunc (r *%s) GetID() string { return r.ID }\n", sd.Name)
		fmt.Fprintf(w, "\n// GetMeta trả về meta của resource.\nfunc (r *%s) GetMeta() *Meta { return r.Meta }\n", sd.Name)
		fmt.Fprintf(w, "\n// MarshalJSON luôn ghi đúng resourceType.\nfunc (r %[1]s) MarshalJSON() ([]byte, error) {\n\ttype alias %[1]s\n\tr.ResourceType = %[1]q\n\treturn json.Marshal(alias(r))\n}\n", sd.Name)
	}
}

func (g *generator) emitStruct(w *bytes.Buffer, n *node, path, goName string, isResource, top bool) {
	var nested []*node
	fmt.Fprintf(w, "\n// %s %s\ntype %s struct {\n", goName, docLine(n.el.Short, path), goName)
	if isResource && top {
		fmt.Fprintf(w, "\tResourceType string `json:\"resourceType\"`\n")
	}
	seen := map[string]string{}
	add := func(goField, line, jsonName string) {
		if prev, dup := seen[goField]; dup {
			g.errs = append(g.errs, fmt.Sprintf("%s: trùng tên trường %s (%s và %s)", path, goField, prev, jsonName))
		}
		seen[goField] = jsonName
		w.WriteString(line)
	}
	for _, c := range n.children {
		base := c.el.Path[strings.LastIndex(c.el.Path, ".")+1:]
		array := c.el.Max == "*" || (c.el.Max != "" && c.el.Max != "0" && c.el.Max != "1")
		g.record(path, c, base, array)
		doc := docLine(c.el.Short, base)
		if c.el.Min >= 1 {
			doc += " (bắt buộc)"
		}
		// choice type
		if strings.HasSuffix(base, "[x]") {
			stem := strings.TrimSuffix(base, "[x]")
			for _, t := range c.el.Type {
				jn := stem + upperFirst(t.Code)
				g.field(add, jn, t.Code, array, doc, c, &nested, path)
			}
			continue
		}
		code := ""
		if len(c.el.Type) > 0 {
			code = c.el.Type[0].Code
		}
		g.field(add, base, code, array, doc, c, &nested, path)
	}
	fmt.Fprintf(w, "}\n")
	for _, c := range nested {
		g.emitStruct(w, c, c.el.Path, typeName(c.el.Path), false, false)
	}
}

// field viết một trường (và trường mở rộng _name nếu là nguyên thủy).
func (g *generator) field(add func(goField, line, jsonName string), jsonName, code string, array bool, doc string, c *node, nested *[]*node, parentPath string) {
	goName := fieldName(jsonName)
	tag := fmt.Sprintf("`json:\"%s,omitempty\"`", jsonName)
	switch {
	case c.el.ContentReference != "":
		ref := typeName(strings.TrimPrefix(c.el.ContentReference, "#"))
		add(goName, line(goName, wrap(ref, array, true), tag, doc), jsonName)
	case code == "BackboneElement" || code == "Element":
		if len(c.children) == 0 {
			g.errs = append(g.errs, c.el.Path+": Backbone không có phần tử con")
			return
		}
		*nested = append(*nested, c)
		add(goName, line(goName, wrap(typeName(c.el.Path), array, true), tag, doc), jsonName)
	case code == "Resource":
		add(goName, line(goName, wrap("AnyResource", array, true), tag, doc), jsonName)
	case primitive[code].goType != "":
		p := primitive[code]
		add(goName, line(goName, wrap(p.goType, array, p.pointer), tag, doc), jsonName)
		ext := goName + "Ext"
		etag := fmt.Sprintf("`json:\"_%s,omitempty\"`", jsonName)
		add(ext, line(ext, wrapExt(array), etag, "Phần mở rộng của "+jsonName), "_"+jsonName)
	case g.complexTypes[code] || g.resources[code]:
		add(goName, line(goName, wrap(code, array, true), tag, doc), jsonName)
	default:
		g.errs = append(g.errs, fmt.Sprintf("%s: kiểu không hỗ trợ %q", c.el.Path, code))
	}
}

func line(name, typ, tag, doc string) string {
	return fmt.Sprintf("\t// %s\n\t%s %s %s\n", doc, name, typ, tag)
}

func wrap(t string, array, pointer bool) string {
	switch {
	case array:
		return "[]" + t
	case pointer:
		return "*" + t
	}
	return t
}

func wrapExt(array bool) string {
	if array {
		return "[]*Element"
	}
	return "*Element"
}

// typeName: "Patient.contact.communication" -> "PatientContactCommunication".
func typeName(path string) string {
	var sb strings.Builder
	for _, s := range strings.Split(path, ".") {
		sb.WriteString(upperFirst(strings.TrimSuffix(s, "[x]")))
	}
	return sb.String()
}

func fieldName(json string) string {
	switch json {
	case "id":
		return "ID"
	case "url":
		return "URL"
	case "uri":
		return "URI"
	}
	return upperFirst(json)
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func docLine(short, fallback string) string {
	s := strings.Join(strings.Fields(short), " ")
	if s == "" {
		return fallback
	}
	return s
}

func (g *generator) write(dir, file, pkg string, body *bytes.Buffer) {
	var src bytes.Buffer
	fmt.Fprintf(&src, "// Code generated by tools/fhirgen. DO NOT EDIT.\n\npackage %s\n\n", pkg)
	text := body.String()
	if strings.Contains(text, "json.") {
		src.WriteString("import \"encoding/json\"\n\n")
	}
	src.WriteString(text)
	formatted, err := format.Source(src.Bytes())
	if err != nil {
		os.WriteFile(filepath.Join(dir, file+".broken"), src.Bytes(), 0o644)
		g.errs = append(g.errs, fmt.Sprintf("%s: gofmt: %v", file, err))
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, file), formatted, 0o644); err != nil {
		fatal(err)
	}
}

func uniq(s []string) []string {
	var out []string
	for i, v := range s {
		if i == 0 || v != s[i-1] {
			out = append(out, v)
		}
	}
	return out
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "fhirgen:", err)
	os.Exit(1)
}

// ---------------------------------------------------------------- schema

type constraint struct{ Key, Severity, Human, Expression string }

type schemaEntry struct {
	Name        string
	Types       []string
	Array       bool
	Min         int
	Constraints []constraint
}

// toConstraints lấy ràng buộc của một phần tử, bỏ ele-1 (đã có kiểm tra rỗng) và,
// ở phần tử con, ext-1 (đã có ở gốc kiểu Extension).
func toConstraints(el element, root bool) []constraint {
	var out []constraint
	for _, c := range el.Constraint {
		if c.Key == "ele-1" || (!root && c.Key == "ext-1") || c.Expression == "" {
			continue
		}
		out = append(out, constraint{c.Key, c.Severity, c.Human, c.Expression})
	}
	return out
}

// record ghi lại kiểu của một phần tử con để sinh schema_gen.go.
func (g *generator) record(parent string, c *node, base string, array bool) {
	norm := func(code string) string {
		if strings.HasPrefix(code, "http://hl7.org/fhirpath/System.") {
			if base == "id" && g.resources[parent] {
				return "id" // Resource.id có kiểu FHIR id (regex [A-Za-z0-9\-\.]{1,64})
			}
			return "string"
		}
		return code
	}
	e := schemaEntry{Array: array, Min: c.el.Min, Constraints: toConstraints(c.el, false)}
	switch {
	case strings.HasSuffix(base, "[x]"):
		e.Name = strings.TrimSuffix(base, "[x]")
		for _, t := range c.el.Type {
			e.Types = append(e.Types, norm(t.Code))
		}
	case c.el.ContentReference != "":
		e.Name = base
		e.Types = []string{strings.TrimPrefix(c.el.ContentReference, "#")}
	default:
		e.Name = base
		if len(c.el.Type) > 0 {
			code := c.el.Type[0].Code
			if code == "BackboneElement" || code == "Element" {
				e.Types = []string{c.el.Path}
			} else {
				e.Types = []string{norm(code)}
			}
		}
	}
	g.schema[parent] = append(g.schema[parent], e)
}

func (g *generator) emitSchema() *bytes.Buffer {
	b := &bytes.Buffer{}
	names := make([]string, 0, len(g.schema))
	for k := range g.schema {
		names = append(names, k)
	}
	sort.Strings(names)
	fmt.Fprintf(b, "// schemaData: kiểu -> phần tử con. Tên kiểu là tên FHIR (Patient, HumanName) hoặc đường dẫn\n// của backbone element (Patient.contact).\nvar schemaData = map[string][]ElementInfo{\n")
	for _, n := range names {
		fmt.Fprintf(b, "\t%q: {\n", n)
		for _, e := range g.schema[n] {
			fmt.Fprintf(b, "\t\t{Name: %q, Types: []string{", e.Name)
			for i, t := range e.Types {
				if i > 0 {
					b.WriteString(", ")
				}
				fmt.Fprintf(b, "%q", t)
			}
			fmt.Fprintf(b, "}, Array: %v", e.Array)
			if e.Min > 0 {
				fmt.Fprintf(b, ", Min: %d", e.Min)
			}
			writeConstraints(b, e.Constraints)
			b.WriteString("},\n")
		}
		b.WriteString("\t},\n")
	}
	b.WriteString("}\n\n// typeConstraintData: ràng buộc ở gốc của từng kiểu (dom-2, qty-3, ...).\nvar typeConstraintData = map[string][]Constraint{\n")
	var cn []string
	for k, v := range g.typeCons {
		if len(v) > 0 {
			cn = append(cn, k)
		}
	}
	sort.Strings(cn)
	for _, k := range cn {
		fmt.Fprintf(b, "\t%q: {", k)
		for _, c := range g.typeCons[k] {
			fmt.Fprintf(b, "{%q, %q, %q, %q}, ", c.Key, c.Severity, c.Human, c.Expression)
		}
		b.WriteString("},\n")
	}
	b.WriteString("}\n\n// primitiveRegexData: regex hợp lệ của kiểu nguyên thủy (chưa neo đầu cuối).\nvar primitiveRegexData = map[string]string{\n")
	var rn []string
	for k := range g.regexes {
		rn = append(rn, k)
	}
	sort.Strings(rn)
	for _, k := range rn {
		fmt.Fprintf(b, "\t%q: %q,\n", k, g.regexes[k])
	}
	b.WriteString("}\n")
	return b
}

func writeConstraints(b *bytes.Buffer, cs []constraint) {
	if len(cs) == 0 {
		return
	}
	b.WriteString(", Constraints: []Constraint{")
	for _, c := range cs {
		fmt.Fprintf(b, "{%q, %q, %q, %q}, ", c.Key, c.Severity, c.Human, c.Expression)
	}
	b.WriteString("}")
}

// ---------------------------------------------------------------- search parameters

type searchParam struct {
	ResourceType string   `json:"resourceType"`
	ID           string   `json:"id"`
	URL          string   `json:"url"`
	Code         string   `json:"code"`
	Type         string   `json:"type"`
	Base         []string `json:"base"`
	Target       []string `json:"target"`
	Expression   string   `json:"expression"`
	Comparator   []string `json:"comparator"`
	Modifier     []string `json:"modifier"`
	Component    []struct {
		Definition string `json:"definition"`
		Expression string `json:"expression"`
	} `json:"component"`
}

func emitSearchParams(dir string) (*bytes.Buffer, int, error) {
	var all []searchParam
	seen := map[string]bool{}
	for _, f := range []string{"search-parameters.json", "search-parameters-medplum.json", "search-parameters-uscore.json"} {
		raw, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			return nil, 0, err
		}
		var b bundle
		if err := json.Unmarshal(raw, &b); err != nil {
			return nil, 0, fmt.Errorf("%s: %w", f, err)
		}
		for _, e := range b.Entry {
			var sp searchParam
			if err := json.Unmarshal(e.Resource, &sp); err != nil {
				return nil, 0, err
			}
			if sp.ResourceType != "SearchParameter" || seen[sp.URL] {
				continue
			}
			seen[sp.URL] = true
			all = append(all, sp)
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].Code != all[j].Code {
			return all[i].Code < all[j].Code
		}
		return all[i].URL < all[j].URL
	})
	b := &bytes.Buffer{}
	b.WriteString("// searchParamData: mọi SearchParameter (R4, Medplum, US Core), sắp xếp theo code rồi url.\nvar searchParamData = []SearchParamDef{\n")
	strs := func(x []string) string {
		if len(x) == 0 {
			return "nil"
		}
		q := make([]string, len(x))
		for i, v := range x {
			q[i] = fmt.Sprintf("%q", v)
		}
		return "[]string{" + strings.Join(q, ", ") + "}"
	}
	for _, sp := range all {
		fmt.Fprintf(b, "\t{URL: %q, Code: %q, Type: %q, Base: %s, Target: %s, Expression: %q, Comparator: %s, Modifier: %s",
			sp.URL, sp.Code, sp.Type, strs(sp.Base), strs(sp.Target), sp.Expression, strs(sp.Comparator), strs(sp.Modifier))
		if len(sp.Component) > 0 {
			b.WriteString(", Component: []SearchParamComponent{")
			for _, c := range sp.Component {
				fmt.Fprintf(b, "{Definition: %q, Expression: %q}, ", c.Definition, c.Expression)
			}
			b.WriteString("}")
		}
		b.WriteString("},\n")
	}
	b.WriteString("}\n")
	return b, len(all), nil
}

// ---------------------------------------------------------------- compartment

// emitCompartment sinh bảng compartment Patient: resource type -> các search parameter trỏ về Patient.
func emitCompartment(dir string) (*bytes.Buffer, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "compartmentdefinition-patient.json"))
	if err != nil {
		return nil, err
	}
	var cd struct {
		Resource []struct {
			Code  string   `json:"code"`
			Param []string `json:"param"`
		} `json:"resource"`
	}
	if err := json.Unmarshal(raw, &cd); err != nil {
		return nil, err
	}
	b := &bytes.Buffer{}
	b.WriteString("// patientCompartmentData: resource type -> search parameter liên kết resource đó với Patient (CompartmentDefinition Patient).\nvar patientCompartmentData = map[string][]string{\n")
	sort.Slice(cd.Resource, func(i, j int) bool { return cd.Resource[i].Code < cd.Resource[j].Code })
	for _, r := range cd.Resource {
		if len(r.Param) == 0 {
			continue
		}
		q := make([]string, len(r.Param))
		for i, p := range r.Param {
			q[i] = fmt.Sprintf("%q", p)
		}
		fmt.Fprintf(b, "\t%q: {%s},\n", r.Code, strings.Join(q, ", "))
	}
	b.WriteString("}\n")
	return b, nil
}
