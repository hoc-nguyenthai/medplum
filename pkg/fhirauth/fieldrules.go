package fhirauth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirrouter"
)

// Quy tắc trường và interaction của một rule chính sách (Medplum AccessPolicy.resource):
//
//   - hiddenFields: trường bị gỡ khỏi mọi resource trả về (đọc, vread, history, search, kết quả ghi) và không ghi được (bỏ khỏi input, update khôi phục từ bản đang lưu);
//   - readonlyFields: trường không ghi được (create: bỏ khỏi input; update: khôi phục từ bản đang lưu);
//   - interaction: tập con của read, vread, history, search, create, update, delete mà rule cho phép (không đặt: mọi interaction; `readonly` loại create/update/delete).
//
// Đường dẫn trường: tên thuộc tính, có thể lồng bằng dấu chấm (`name.family`, đi qua mảng) và hậu tố `[x]` (`value[x]` gỡ valueString, valueQuantity...). Chỉ trường cấp cao nhất mới
// khôi phục được từ bản cũ (như Medplum). Tìm kiếm theo một trường ẩn vẫn dùng được (như Medplum): chính sách không chặn việc suy ra giá trị bằng truy vấn.

var interactionNames = map[string]bool{"read": true, "vread": true, "history": true, "search": true, "create": true, "update": true, "delete": true}

var fieldPathRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\[x\])?(\.[A-Za-z_][A-Za-z0-9_]*(\[x\])?)*$`)

const maxFieldRules = 64

// choiceTypes là hậu tố kiểu hợp lệ của thuộc tính `[x]` (valueString, effectiveDateTime...).
var choiceTypes = map[string]bool{}

func init() {
	for _, t := range []string{"base64Binary", "boolean", "canonical", "code", "date", "dateTime", "decimal", "id", "instant", "integer", "markdown", "oid", "positiveInt", "string", "time",
		"unsignedInt", "uri", "url", "uuid", "Address", "Age", "Annotation", "Attachment", "CodeableConcept", "Coding", "ContactPoint", "Count", "Distance", "Duration", "HumanName",
		"Identifier", "Money", "Period", "Quantity", "Range", "Ratio", "Reference", "SampledData", "Signature", "Timing", "ContactDetail", "Contributor", "DataRequirement", "Expression",
		"ParameterDefinition", "RelatedArtifact", "TriggerDefinition", "UsageContext", "Dosage", "Meta"} {
		choiceTypes[t] = true
		choiceTypes[strings.ToUpper(t[:1])+t[1:]] = true
	}
}

// asAnyList nhận mảng đã giải mã từ JSON ([]any) hoặc mảng chuỗi Go ([]string, khi auth kiểm claim trước khi mã hóa).
func asAnyList(v any) ([]any, bool) {
	switch x := v.(type) {
	case []any:
		return x, true
	case []string:
		out := make([]any, len(x))
		for i, s := range x {
			out[i] = s
		}
		return out, true
	}
	return nil, false
}

// parseFieldList đọc một mảng chuỗi tên trường; lỗi (fail đóng) nếu không phải mảng chuỗi hoặc tên không hợp lệ.
func parseFieldList(v any, key, rt string) ([]string, error) {
	l, ok := asAnyList(v)
	if !ok {
		return nil, fmt.Errorf("%s của %s phải là mảng chuỗi", key, rt)
	}
	if len(l) > maxFieldRules {
		return nil, fmt.Errorf("%s của %s có %d phần tử, tối đa %d", key, rt, len(l), maxFieldRules)
	}
	var out []string
	for _, e := range l {
		s, ok := e.(string)
		if !ok || !fieldPathRE.MatchString(s) {
			return nil, fmt.Errorf("%s của %s có tên trường không hợp lệ: %v", key, rt, e)
		}
		out = append(out, s)
	}
	sort.Strings(out)
	return out, nil
}

func parseInteractions(v any, rt string) (map[string]bool, error) {
	l, ok := asAnyList(v)
	if !ok {
		return nil, fmt.Errorf("interaction của %s phải là mảng chuỗi", rt)
	}
	out := map[string]bool{}
	for _, e := range l {
		s, ok := e.(string)
		if !ok || !interactionNames[s] {
			return nil, fmt.Errorf("interaction của %s không hợp lệ: %v", rt, e)
		}
		out[s] = true
	}
	return out, nil
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sameSet(a, b map[string]bool) bool {
	if (a == nil) != (b == nil) || len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

// permits cho biết chính sách có cho interaction trên loại rt không (interaction: read, vread, history, search, create, update, delete). Không có chính sách: có.
func (p *Policy) permits(rt, interaction string) bool {
	if p == nil || rt == jobType || (p.jobBinary && rt == "Binary") {
		return true
	}
	r, ok := p.ruleFor(rt)
	if !ok {
		return false
	}
	if r.interactions != nil {
		return r.interactions[interaction]
	}
	return true
}

// fieldRule trả rule của loại rt khi nó có quy tắc trường; nil nếu không.
func (p *Policy) fieldRule(rt string) *rule {
	if p == nil {
		return nil
	}
	r, ok := p.ruleFor(rt)
	if !ok || (len(r.hidden) == 0 && len(r.readonlyFields) == 0) {
		return nil
	}
	return &r
}

func toDoc(res fhir.Resource) (map[string]any, error) {
	raw, err := json.Marshal(res)
	if err != nil {
		return nil, fhirrouter.Internal(err.Error())
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var doc map[string]any
	if err := d.Decode(&doc); err != nil {
		return nil, fhirrouter.Internal(err.Error())
	}
	return doc, nil
}

func fromDoc(doc map[string]any) (fhir.Resource, error) {
	raw, err := json.Marshal(doc)
	if err != nil {
		return nil, fhirrouter.Internal(err.Error())
	}
	res, err := fhir.UnmarshalResource(raw)
	if err != nil {
		return nil, fhirrouter.Internal(err.Error())
	}
	return res, nil
}

// removeField gỡ trường theo đường dẫn (đi qua mảng, hỗ trợ `[x]`).
func removeField(doc map[string]any, path string) {
	cur := []map[string]any{doc}
	parts := strings.Split(path, ".")
	for i, part := range parts {
		last := i == len(parts)-1
		var next []map[string]any
		for _, m := range cur {
			for _, key := range resolveKeys(m, part) {
				if last {
					delete(m, key)
					continue
				}
				switch v := m[key].(type) {
				case map[string]any:
					next = append(next, v)
				case []any:
					for _, e := range v {
						if em, ok := e.(map[string]any); ok {
							next = append(next, em)
						}
					}
				}
			}
		}
		cur = next
	}
}

func resolveKeys(m map[string]any, part string) []string {
	if !strings.HasSuffix(part, "[x]") {
		return []string{part}
	}
	base := strings.TrimSuffix(part, "[x]")
	var out []string
	for k := range m {
		if strings.HasPrefix(k, base) && len(k) > len(base) && choiceTypes[k[len(base):]] {
			out = append(out, k)
		}
	}
	return out
}

// redact gỡ các trường ẩn của rule khỏi resource (không đổi res gốc). Loại không có trường ẩn: trả nguyên res.
func (p *Policy) redact(res fhir.Resource) (fhir.Resource, error) {
	if res == nil {
		return res, nil
	}
	r := p.fieldRule(res.GetResourceType())
	if r == nil || len(r.hidden) == 0 {
		return res, nil
	}
	doc, err := toDoc(res)
	if err != nil {
		return nil, err
	}
	for _, f := range r.hidden {
		removeField(doc, f)
	}
	return fromDoc(doc)
}

func (p *Policy) redactAll(in []fhir.Resource) ([]fhir.Resource, error) {
	if p == nil || len(in) == 0 {
		return in, nil
	}
	out := make([]fhir.Resource, len(in))
	for i, x := range in {
		r, err := p.redact(x)
		if err != nil {
			return nil, err
		}
		out[i] = r
	}
	return out, nil
}

// protectWrite áp quy tắc ghi lên input: gỡ trường readonly và hidden; khi update (original khác nil) khôi phục các trường CẤP CAO NHẤT đó từ bản đang lưu, nên người gọi không thể
// sửa trường chỉ-đọc cũng không thể xóa trường ẩn họ không thấy.
func (p *Policy) protectWrite(res, original fhir.Resource) (fhir.Resource, error) {
	r := p.fieldRule(res.GetResourceType())
	if r == nil {
		return res, nil
	}
	doc, err := toDoc(res)
	if err != nil {
		return nil, err
	}
	var orig map[string]any
	if original != nil {
		if orig, err = toDoc(original); err != nil {
			return nil, err
		}
	}
	fields := append(append([]string(nil), r.readonlyFields...), r.hidden...)
	for _, f := range fields {
		removeField(doc, f)
		if orig != nil && !strings.Contains(f, ".") && !strings.HasSuffix(f, "[x]") {
			if v, ok := orig[f]; ok {
				doc[f] = v
			}
		}
	}
	return fromDoc(doc)
}
