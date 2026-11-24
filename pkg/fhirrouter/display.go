package fhirrouter

import (
	"fmt"
	"strings"
)

// Định dạng hiển thị theo `format.ts` và `getDisplayString` của @medplum/core (port bản trên dữ liệu JSON). Dùng cho $csv và $refresh-reference-display.

func dstr(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

func dlist(m map[string]any, k string) []any {
	l, _ := m[k].([]any)
	return l
}

func dobj(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

// formatHumanName: tiền tố, tên đệm và tên, họ, hậu tố cách nhau bằng khoảng trắng; không có thành phần nào thì dùng text.
func formatHumanName(n map[string]any) string {
	if n == nil {
		return ""
	}
	var b []string
	for _, k := range []string{"prefix", "given"} {
		for _, v := range dlist(n, k) {
			if s, ok := v.(string); ok {
				b = append(b, s)
			}
		}
	}
	if f := dstr(n, "family"); f != "" {
		b = append(b, f)
	}
	for _, v := range dlist(n, "suffix") {
		if s, ok := v.(string); ok {
			b = append(b, s)
		}
	}
	if len(b) == 0 {
		if t := dstr(n, "text"); t != "" {
			return t
		}
	}
	return strings.TrimSpace(strings.Join(b, " "))
}

// formatAddress: các dòng địa chỉ rồi "thành phố, bang, mã bưu chính", nối bằng ", ".
func formatAddress(a map[string]any) string {
	if a == nil {
		return ""
	}
	var b []string
	for _, v := range dlist(a, "line") {
		if s, ok := v.(string); ok {
			b = append(b, s)
		}
	}
	var csz []string
	for _, k := range []string{"city", "state", "postalCode"} {
		if s := dstr(a, k); s != "" {
			csz = append(csz, s)
		}
	}
	if len(csz) > 0 {
		b = append(b, strings.Join(csz, ", "))
	}
	return strings.TrimSpace(strings.Join(b, ", "))
}

// formatCodeableConcept: text, rồi display của coding đầu, rồi code của coding đầu.
func formatCodeableConcept(c map[string]any) string {
	if c == nil {
		return ""
	}
	if t := dstr(c, "text"); t != "" {
		return t
	}
	if cs := dlist(c, "coding"); len(cs) > 0 {
		cd := dobj(cs[0])
		if d := dstr(cd, "display"); d != "" {
			return d
		}
		return dstr(cd, "code")
	}
	return ""
}

var profileResources = map[string]bool{"Patient": true, "Practitioner": true, "RelatedPerson": true, "Person": true}

// displayString là getDisplayString của Medplum: tên người cho resource hồ sơ, vài quy tắc theo loại, rồi `name` (chuỗi), `code`, và cuối cùng "Loại/id".
func displayString(doc map[string]any) string {
	if doc == nil {
		return ""
	}
	rt := dstr(doc, "resourceType")
	if profileResources[rt] {
		if names := dlist(doc, "name"); len(names) > 0 {
			if s := formatHumanName(dobj(names[0])); s != "" {
				return s
			}
		}
	}
	switch rt {
	case "Appointment":
		if st := dlist(doc, "serviceType"); len(st) > 0 {
			return formatCodeableConcept(dobj(st[0]))
		}
	case "Device":
		if dn := dlist(doc, "deviceName"); len(dn) > 0 {
			if s := dstr(dobj(dn[0]), "name"); s != "" {
				return s
			}
		}
	case "DocumentReference":
		if d := dstr(doc, "description"); d != "" {
			return d
		}
		if t := dobj(doc["type"]); t != nil {
			return formatCodeableConcept(t)
		}
	case "Coverage":
		for _, p := range dlist(doc, "payor") {
			pm := dobj(p)
			ref := dstr(pm, "reference")
			if dstr(pm, "display") != "" && dstr(pm, "type") != "Patient" && dstr(pm, "type") != "RelatedPerson" &&
				!strings.HasPrefix(ref, "Patient/") && !strings.HasPrefix(ref, "RelatedPerson/") && !strings.Contains(ref, "/Patient/") && !strings.Contains(ref, "/RelatedPerson/") {
				return dstr(pm, "display")
			}
		}
		if s := formatCodeableConcept(dobj(doc["type"])); s != "" {
			return s
		}
	case "Immunization":
		if s := formatCodeableConcept(dobj(doc["vaccineCode"])); s != "" {
			return s
		}
	case "MedicationAdministration", "MedicationDispense", "MedicationRequest", "MedicationStatement":
		if mc := dobj(doc["medicationCodeableConcept"]); mc != nil {
			if s := formatCodeableConcept(mc); s != "" {
				return s
			}
		} else if mr := dobj(doc["medicationReference"]); mr != nil {
			if d := dstr(mr, "display"); d != "" {
				return d
			}
			if ref := dstr(mr, "reference"); strings.HasPrefix(ref, "#") {
				for _, c := range dlist(doc, "contained") {
					cm := dobj(c)
					if dstr(cm, "resourceType") == "Medication" && dstr(cm, "id") == ref[1:] {
						if s := formatCodeableConcept(dobj(cm["code"])); s != "" {
							return s
						}
					}
				}
			}
		}
	case "Slot":
		if dstr(doc, "start") != "" || dstr(doc, "end") != "" {
			return dstr(doc, "start") + " - " + dstr(doc, "end")
		}
	case "Subscription":
		if c := dstr(doc, "criteria"); c != "" {
			return c
		}
	case "User":
		if e := dstr(doc, "email"); e != "" {
			return e
		}
	}
	if n := dstr(doc, "name"); n != "" {
		return n
	}
	if c, ok := doc["code"]; ok && c != nil {
		if l, isList := c.([]any); isList {
			if len(l) > 0 {
				c = l[0]
			} else {
				c = nil
			}
		}
		if cm := dobj(c); cm != nil {
			if _, hasCoding := cm["coding"]; hasCoding {
				if s := formatCodeableConcept(cm); s != "" {
					return s
				}
			} else if t := dstr(cm, "text"); t != "" {
				return t
			}
		}
	}
	if id := dstr(doc, "id"); id != "" && rt != "" {
		return fmt.Sprintf("%s/%s", rt, id)
	}
	return ""
}
