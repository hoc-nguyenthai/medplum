package fhirrouter

import (
	"context"
	"fmt"
	"html"
	"net/url"
	"strings"
	"time"

	"trivita-fhir/pkg/fhir"
)

// `Patient/{id}/$summary` (International Patient Summary, port của patientsummary.ts): GET|POST, tham số `author` (Reference tới Practitioner, PractitionerRole hoặc Organization;
// bắt buộc vì router không biết hồ sơ người gọi như Medplum), `authoredOn`, `_since`. Gom resource của bệnh nhân bằng $everything (16 loại), chia vào các mục của Composition theo
// quy tắc của Medplum (loại resource, category, mã), mỗi mục có bảng HTML. Resource lồng (hasMember, DiagnosticReport.result, CarePlan.activity, Encounter.diagnosis, Condition.evidence,
// Account.coverage) không đứng riêng trong mục mà hiện trong bảng của cha. Trả Bundle `document`: Composition, Patient, tác giả, rồi các resource. Chưa làm: tham số `start`,
// `end`, `profile`, `identifier` và `Patient/$summary` mức loại. Độ đúng lâm sàng của cách chia mục là của Medplum ("không có quy tắc, làm hợp lý"), cần người có chuyên môn rà.

const (
	loincSystem          = "http://loinc.org"
	observationCategory  = "http://terminology.hl7.org/CodeSystem/observation-category"
	loincPatientSummary  = "60591-5"
	loincNoteDocument    = "34109-9"
	loincHealthConcerns  = "75310-3"
	loincDisabilityState = "89571-4"
)

var summaryTypes = []string{"Account", "AllergyIntolerance", "CarePlan", "ClinicalImpression", "Coverage", "Condition", "DeviceUseStatement", "DiagnosticReport", "Encounter", "Goal",
	"Immunization", "MedicationRequest", "Observation", "Procedure", "RelatedPerson", "ServiceRequest"}

func (r *Router) registerPatientSummary() {
	r.Add("GET", "Patient/:id/$summary", r.patientSummary)
	r.Add("POST", "Patient/:id/$summary", r.patientSummary)
}

type sumDoc = map[string]any

func (r *Router) patientSummary(ctx context.Context, req *Request) (*Response, error) {
	id := req.Params["id"]
	in, err := ParseInput(req)
	if err != nil {
		return nil, err
	}
	authorRef := in.String("author")
	if authorRef == "" && in.Body != nil { // Parameters {name: author, valueReference}
		for _, p := range dlist(in.Body, "parameter") {
			if pm := dobj(p); dstr(pm, "name") == "author" {
				authorRef = dstr(dobj(pm["valueReference"]), "reference")
			}
		}
	}
	if authorRef == "" {
		return nil, BadRequest("Thiếu tham số author (Reference tới Practitioner, PractitionerRole hoặc Organization)")
	}
	art, aid, ok := splitReference(authorRef)
	if !ok || (art != "Practitioner" && art != "PractitionerRole" && art != "Organization") {
		return nil, BadRequest("author phải là Reference tới Practitioner, PractitionerRole hoặc Organization")
	}
	repo := r.repoOf(ctx)
	author, err := repo.ReadResource(ctx, art, aid)
	if err != nil {
		return nil, err
	}
	patient, err := repo.ReadResource(ctx, "Patient", id)
	if err != nil {
		return nil, err
	}
	q := url.Values{"_type": {strings.Join(summaryTypes, ",")}}
	if since := in.String("_since"); since != "" {
		q.Set("_since", since)
	}
	ev, err := r.everything(ctx, &Request{Method: "GET", Params: map[string]string{"id": id}, Query: q})
	if err != nil {
		return nil, err
	}
	bundle, ok := ev.Resource.(*fhir.Bundle)
	if !ok {
		return nil, Internal("$everything không trả Bundle")
	}
	var everything []sumDoc
	for _, e := range bundle.Entry {
		if e.Search != nil && e.Search.Mode == "outcome" {
			continue
		}
		if e.Resource == nil {
			continue
		}
		res := e.Resource.Value
		if res == nil {
			continue
		}
		doc, err := toMap(res)
		if err != nil {
			return nil, Internal(err.Error())
		}
		everything = append(everything, doc)
	}
	patientDoc, err := toMap(patient)
	if err != nil {
		return nil, Internal(err.Error())
	}
	authorDoc, err := toMap(author)
	if err != nil {
		return nil, Internal(err.Error())
	}
	authoredOn := in.String("authoredOn")
	if authoredOn == "" {
		authoredOn = time.Now().UTC().Format(time.RFC3339)
	}
	b := &summaryBuilder{patient: patientDoc, author: authorDoc, everything: everything, authoredOn: authoredOn, nested: map[string]bool{}}
	out := b.build()
	res, err := toResource(out)
	if err != nil {
		return nil, Internal("Bundle summary không hợp lệ: " + err.Error())
	}
	return &Response{Status: 200, Resource: res}, nil
}

type summaryBuilder struct {
	patient, author sumDoc
	everything      []sumDoc
	authoredOn      string
	nested          map[string]bool

	lists map[string][]sumDoc // theo mục: allergies, medications, problems, results, social, vitals, procedures, encounters, assessments, plan, ...
}

func rtOf(d sumDoc) string { return dstr(d, "resourceType") }

func refID(ref any) string {
	m := dobj(ref)
	s := dstr(m, "reference")
	if s == "" {
		return ""
	}
	parts := strings.Split(s, "/")
	return parts[len(parts)-1]
}

func (b *summaryBuilder) buildNested() {
	for _, d := range b.everything {
		switch rtOf(d) {
		case "Observation":
			for _, m := range dlist(d, "hasMember") {
				if id := refID(m); id != "" {
					b.nested[id] = true
				}
			}
		case "DiagnosticReport":
			for _, m := range dlist(d, "result") {
				if id := refID(m); id != "" {
					b.nested[id] = true
				}
			}
		case "CarePlan":
			for _, a := range dlist(d, "activity") {
				if id := refID(dobj(a)["reference"]); id != "" {
					b.nested[id] = true
				}
			}
		case "Encounter":
			for _, a := range dlist(d, "diagnosis") {
				if id := refID(dobj(a)["condition"]); id != "" {
					b.nested[id] = true
				}
			}
		case "Condition":
			for _, ev := range dlist(d, "evidence") {
				for _, det := range dlist(dobj(ev), "detail") {
					if id := refID(det); id != "" {
						b.nested[id] = true
					}
				}
			}
		case "Account":
			for _, c := range dlist(d, "coverage") {
				if id := refID(dobj(c)["coverage"]); id != "" {
					b.nested[id] = true
				}
			}
		}
	}
}

func codeBySystem(cc any, system string) string {
	m := dobj(cc)
	if m == nil {
		if l, ok := cc.([]any); ok && len(l) > 0 {
			for _, c := range l {
				if s := codeBySystem(c, system); s != "" {
					return s
				}
			}
		}
		return ""
	}
	for _, cd := range dlist(m, "coding") {
		cm := dobj(cd)
		if dstr(cm, "system") == system && dstr(cm, "code") != "" {
			return dstr(cm, "code")
		}
	}
	return ""
}

func firstCoding(d sumDoc, key string) string {
	if cc := dobj(d[key]); cc != nil {
		if cs := dlist(cc, "coding"); len(cs) > 0 {
			return dstr(dobj(cs[0]), "code")
		}
	}
	return ""
}

func (b *summaryBuilder) choose(d sumDoc) {
	push := func(key string) { b.lists[key] = append(b.lists[key], d) }
	switch rtOf(d) {
	case "Practitioner", "RelatedPerson":
		push("participants")
	case "Account":
		push("insurance")
	case "AllergyIntolerance":
		push("allergies")
	case "CarePlan":
		push("plan")
	case "DeviceUseStatement":
		push("devices")
	case "DiagnosticReport":
		push("results")
	case "Encounter":
		push("encounters")
	case "Goal":
		push("goals")
	case "Immunization":
		push("immunizations")
	case "MedicationRequest":
		push("medications")
	case "Procedure":
		push("procedures")
	case "ClinicalImpression":
		if firstCoding(d, "code") == loincNoteDocument {
			push("notes")
		} else {
			push("assessments")
		}
	case "Condition":
		if codeBySystem(d["category"], loincSystem) == loincHealthConcerns {
			push("healthConcerns")
		} else {
			push("problems")
		}
	case "Observation":
		code := firstCoding(d, "code")
		switch codeBySystem(d["category"], observationCategory) {
		case "social-history":
			push("social")
		case "survey":
			if code == "d5" || code == loincDisabilityState {
				push("functional")
			} else {
				push("social")
			}
		case "vital-signs":
			push("vitals")
		default:
			push("results")
		}
	case "ServiceRequest":
		if firstCoding(d, "code") == "310449005" {
			push("referral")
		} else {
			push("plan")
		}
	}
}

func (b *summaryBuilder) byRef(ref any) sumDoc {
	id := refID(ref)
	if id == "" {
		return nil
	}
	for _, d := range b.everything {
		if dstr(d, "id") == id {
			return d
		}
	}
	return nil
}

func formatDate(s string) string {
	if len(s) >= 10 {
		return s[:10]
	}
	return s
}

func formatObservationValue(o sumDoc) string {
	switch {
	case o["valueQuantity"] != nil:
		q := dobj(o["valueQuantity"])
		return strings.TrimSpace(fmt.Sprint(q["value"]) + " " + dstr(q, "unit"))
	case o["valueCodeableConcept"] != nil:
		return formatCodeableConcept(dobj(o["valueCodeableConcept"]))
	case o["valueString"] != nil:
		return dstr(o, "valueString")
	case o["valueBoolean"] != nil:
		return fmt.Sprint(o["valueBoolean"])
	case o["valueInteger"] != nil:
		return fmt.Sprint(o["valueInteger"])
	case o["valueDateTime"] != nil:
		return dstr(o, "valueDateTime")
	}
	var parts []string
	for _, c := range dlist(o, "component") {
		cm := dobj(c)
		v := formatObservationValue(cm)
		if v != "" {
			parts = append(parts, formatCodeableConcept(dobj(cm["code"]))+" "+v)
		}
	}
	return strings.Join(parts, "; ")
}

func table(headings []string, rows [][]string) string {
	if len(rows) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(`<table border="1" width="100%"><thead><tr>`)
	for _, h := range headings {
		sb.WriteString("<th>" + html.EscapeString(h) + "</th>")
	}
	sb.WriteString("</tr></thead><tbody>")
	for _, row := range rows {
		sb.WriteString("<tr>")
		for _, c := range row {
			sb.WriteString("<td>")
			if c != "" {
				sb.WriteString(html.EscapeString(c))
			}
			sb.WriteString("</td>")
		}
		sb.WriteString("</tr>")
	}
	sb.WriteString("</tbody></table>")
	return sb.String()
}

func section(code, title, htmlBody string, entries []sumDoc) sumDoc {
	refs := make([]any, 0, len(entries))
	for _, e := range entries {
		refs = append(refs, sumDoc{"reference": rtOf(e) + "/" + dstr(e, "id")})
	}
	return sumDoc{
		"title": title, "code": sumDoc{"coding": []any{sumDoc{"system": loincSystem, "code": code}}},
		"text":  sumDoc{"status": "generated", "div": `<div xmlns="http://www.w3.org/1999/xhtml">` + htmlBody + `</div>`},
		"entry": refs,
	}
}

func (b *summaryBuilder) resultRows(rows *[][]string, d sumDoc) {
	switch rtOf(d) {
	case "DiagnosticReport":
		*rows = append(*rows, []string{formatCodeableConcept(dobj(d["code"])), "", formatDate(dstr(d, "effectiveDateTime"))})
		for _, res := range dlist(d, "result") {
			if o := b.byRef(res); o != nil && rtOf(o) == "Observation" {
				b.resultRows(rows, o)
			}
		}
	case "Observation":
		*rows = append(*rows, []string{formatCodeableConcept(dobj(d["code"])), formatObservationValue(d), formatDate(dstr(d, "effectiveDateTime"))})
		for _, m := range dlist(d, "hasMember") {
			if o := b.byRef(m); o != nil && rtOf(o) == "Observation" {
				b.resultRows(rows, o)
			}
		}
	}
}

func (b *summaryBuilder) resultTable(list []sumDoc) string {
	var rows [][]string
	for _, d := range list {
		b.resultRows(&rows, d)
	}
	return table([]string{"Name", "Result", "Date"}, rows)
}

func (b *summaryBuilder) planTable(list []sumDoc) string {
	var rows [][]string
	for _, d := range list {
		switch rtOf(d) {
		case "CarePlan":
			var cat map[string]any
			if cs := dlist(d, "category"); len(cs) > 0 {
				cat = dobj(cs[0])
			}
			rows = append(rows, []string{formatCodeableConcept(cat), formatDate(dstr(dobj(d["period"]), "start"))})
			for _, a := range dlist(d, "activity") {
				if sr := b.byRef(dobj(a)["reference"]); sr != nil && rtOf(sr) == "ServiceRequest" {
					rows = append(rows, []string{formatCodeableConcept(dobj(sr["code"])), formatDate(dstr(sr, "authoredOn"))})
				}
			}
		case "Goal":
			due := ""
			if ts := dlist(d, "target"); len(ts) > 0 {
				due = dstr(dobj(ts[0]), "dueDate")
			}
			rows = append(rows, []string{formatCodeableConcept(dobj(d["description"])), formatDate(due)})
		case "ServiceRequest":
			rows = append(rows, []string{formatCodeableConcept(dobj(d["code"])), formatDate(dstr(d, "authoredOn"))})
		}
	}
	return table([]string{"Planned Care", "Start Date"}, rows)
}

func first(d sumDoc, key string) sumDoc {
	if l := dlist(d, key); len(l) > 0 {
		return dobj(l[0])
	}
	return nil
}

func (b *summaryBuilder) build() sumDoc {
	b.lists = map[string][]sumDoc{}
	b.buildNested()
	for _, d := range b.everything {
		if !b.nested[dstr(d, "id")] {
			b.choose(d)
		}
	}
	L := b.lists
	rowsOf := func(list []sumDoc, f func(sumDoc) []string) [][]string {
		var rows [][]string
		for _, d := range list {
			rows = append(rows, f(d))
		}
		return rows
	}
	cc := func(m any) string { return formatCodeableConcept(dobj(m)) }
	sections := []sumDoc{
		section("48765-2", "Allergies", table([]string{"Substance", "Reaction", "Severity", "Status"}, rowsOf(L["allergies"], func(a sumDoc) []string {
			var man map[string]any
			if r := first(a, "reaction"); r != nil {
				if ms := dlist(r, "manifestation"); len(ms) > 0 {
					man = dobj(ms[0])
				}
			}
			sev := ""
			if r := first(a, "reaction"); r != nil {
				sev = dstr(r, "severity")
			}
			return []string{cc(a["code"]), formatCodeableConcept(man), sev, cc(a["clinicalStatus"])}
		})), L["allergies"]),
		section("11369-6", "Immunizations", table([]string{"Vaccine", "Date", "Status"}, rowsOf(L["immunizations"], func(i sumDoc) []string {
			return []string{cc(i["vaccineCode"]), formatDate(dstr(i, "occurrenceDateTime")), dstr(i, "status")}
		})), L["immunizations"]),
		section("10160-0", "Medications", table([]string{"Medication", "Directions", "Start Date", "End Date"}, rowsOf(L["medications"], func(m sumDoc) []string {
			dir := ""
			if di := first(m, "dosageInstruction"); di != nil {
				dir = dstr(di, "text")
			}
			vp := dobj(dobj(m["dispenseRequest"])["validityPeriod"])
			return []string{cc(m["medicationCodeableConcept"]), dir, formatDate(dstr(vp, "start")), formatDate(dstr(vp, "end"))}
		})), L["medications"]),
		section("11450-4", "Problem List", table([]string{"Problem", "Start Date", "Status"}, rowsOf(L["problems"], func(p sumDoc) []string {
			return []string{cc(p["code"]), formatDate(dstr(p, "onsetDateTime")), cc(p["clinicalStatus"])}
		})), L["problems"]),
		section("30954-2", "Results", b.resultTable(L["results"]), L["results"]),
		section("29762-2", "Social History", b.resultTable(L["social"]), L["social"]),
		section("8716-3", "Vital Signs", b.resultTable(L["vitals"]), L["vitals"]),
		section("47519-4", "Procedures", table([]string{"Procedure", "Date", "Target Site", "Status"}, rowsOf(L["procedures"], func(p sumDoc) []string {
			var site map[string]any
			if bs := dlist(p, "bodySite"); len(bs) > 0 {
				site = dobj(bs[0])
			}
			return []string{cc(p["code"]), formatDate(dstr(p, "performedDateTime")), formatCodeableConcept(site), dstr(p, "status")}
		})), L["procedures"]),
		section("46240-8", "Encounters", table([]string{"Encounter", "Date", "Type", "Status"}, rowsOf(L["encounters"], func(e sumDoc) []string {
			var typ, reason map[string]any
			if t := dlist(e, "type"); len(t) > 0 {
				typ = dobj(t[0])
			}
			if t := dlist(e, "reasonCode"); len(t) > 0 {
				reason = dobj(t[0])
			}
			return []string{formatCodeableConcept(typ), formatDate(dstr(dobj(e["period"]), "start")), formatCodeableConcept(reason), dstr(e, "status")}
		})), L["encounters"]),
		section("46264-8", "Devices", table([]string{"Device", "Status"}, rowsOf(L["devices"], func(u sumDoc) []string {
			dev := b.byRef(u["device"])
			return []string{cc(dev["type"]), dstr(u, "status")}
		})), L["devices"]),
		section("51848-0", "Assessments", table([]string{"Summary", "Date"}, rowsOf(L["assessments"], func(a sumDoc) []string {
			return []string{dstr(a, "summary"), formatDate(dstr(a, "date"))}
		})), L["assessments"]),
		section("18776-5", "Plan of Treatment", b.planTable(L["plan"]), L["plan"]),
	}
	optional := func(key, code, title, body string) {
		if len(L[key]) > 0 {
			sections = append(sections, section(code, title, body, L[key]))
		}
	}
	optional("goals", "61146-7", "Goals", table([]string{"Goal", "Date"}, rowsOf(L["goals"], func(g sumDoc) []string {
		return []string{cc(g["description"]), formatDate(dstr(g, "startDate"))}
	})))
	optional("healthConcerns", "75310-3", "Health Concerns", table([]string{"Concern", "Start Date", "Status"}, rowsOf(L["healthConcerns"], func(p sumDoc) []string {
		return []string{cc(p["code"]), formatDate(dstr(p, "onsetDateTime")), cc(p["clinicalStatus"])}
	})))
	optional("functional", "47420-5", "Functional Status", b.resultTable(L["functional"]))
	optional("notes", "11488-4", "Notes", table([]string{"Note", "Date"}, rowsOf(L["notes"], func(n sumDoc) []string {
		return []string{dstr(n, "summary"), formatDate(dstr(n, "date"))}
	})))
	optional("referral", "42349-1", "Reason for Referral", b.planTable(L["referral"]))
	optional("insurance", "48768-6", "Insurance", table([]string{"Coverage", "Status", "Type"}, rowsOf(L["insurance"], func(a sumDoc) []string {
		return []string{dstr(a, "name"), dstr(a, "status"), cc(a["type"])}
	})))

	comp := sumDoc{
		"resourceType": "Composition", "id": newID(), "status": "final",
		"type":    sumDoc{"coding": []any{sumDoc{"system": loincSystem, "code": loincPatientSummary, "display": "Patient Summary"}}},
		"subject": sumDoc{"reference": "Patient/" + dstr(b.patient, "id")}, "date": b.authoredOn,
		"author": []any{sumDoc{"reference": rtOf(b.author) + "/" + dstr(b.author, "id")}},
		"title":  "Medical Summary", "confidentiality": "N",
		"section": func() []any {
			out := make([]any, len(sections))
			for i, s := range sections {
				out[i] = s
			}
			return out
		}(),
	}
	if mo := b.patient["managingOrganization"]; mo != nil {
		comp["custodian"] = mo
	}
	var start, end string
	for _, d := range b.everything {
		if lu := dstr(dobj(d["meta"]), "lastUpdated"); lu != "" {
			if start == "" || lu < start {
				start = lu
			}
			if lu > end {
				end = lu
			}
		}
	}
	if start != "" || end != "" {
		comp["event"] = []any{sumDoc{"period": sumDoc{"start": start, "end": end}}}
	}
	all := []any{comp, b.patient, b.author}
	for _, d := range b.everything {
		all = append(all, d)
	}
	entries := make([]any, len(all))
	for i, a := range all {
		entries[i] = sumDoc{"resource": a}
	}
	return sumDoc{"resourceType": "Bundle", "type": "document", "timestamp": time.Now().UTC().Format(time.RFC3339), "entry": entries}
}
