package fhirrouter

import (
	"context"
	"encoding/json"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirsearch"
)

// `Patient/$match` (FHIR R4 patient-operation-match). Khác Medplum có chủ ý: Medplum đánh giá "Table 2" của đặc tả CMS Patient Match (SSN, ITIN, MBI của Hoa Kỳ, 22 tổ hợp có số
// thứ tự, nhật ký kiểm toán công bố dữ liệu); đó là quy định của Mỹ nên không port. Ở đây dùng quy tắc chung, không có mã tổ hợp:
//
//   - Ứng viên: tìm theo từng identifier (hệ thống|giá trị), theo từng điện thoại/email, và theo (ngày sinh ±1 ngày) kèm họ hoặc kèm tên đầu. Mỗi lần tìm tối đa 100; chạm trần nghĩa là
//     "bị cắt" và không thể chứng minh tính duy nhất.
//   - So khớp từng trường: tên đầu, họ, ngày sinh, dòng địa chỉ, mã bưu chính, điện thoại, email, giới tính, identifier. exact: bằng nhau sau chuẩn hóa NFC và bỏ khác biệt hoa thường;
//     fuzzy: bằng nhau sau khi bỏ dấu (Việt, đ → d), hoặc cách nhau tối đa 1 ký tự (tên từ 4 ký tự); ngày sinh lệch 1 ngày. Giới tính khác nhau và hậu tố thế hệ khác nhau (Jr, Sr, II...)
//     loại ứng viên (certainly-not).
//   - Điểm: min((exact + 0,5 fuzzy) / 6, 0,9) với các trường trên (identifier không tính điểm). Grade: certain (điểm 1) khi có identifier trùng cả hệ thống và giá trị, hoặc tên đầu, họ,
//     ngày sinh trùng exact cùng một trong (điện thoại, email, dòng địa chỉ) trùng exact; probable từ 0,65; possible từ 0,2; dưới đó certainly-not (bị loại).
//   - Chống nhầm anh em sinh đôi: nếu nhiều hơn một ứng viên cùng ngày sinh (±1 ngày) thì tên đầu phải khớp exact với mọi ứng viên.
//   - `onlyCertainMatches=true`: chỉ trả khi có ĐÚNG MỘT ứng viên certain và không bị cắt, còn không thì rỗng (không trả ứng viên mơ hồ).

const (
	matchCandidateCount  = 100
	matchProbable        = 0.65
	matchPossible        = 0.2
	matchFactor          = 6.0
	matchMaxScore        = 0.9
	matchGradeURL        = "http://hl7.org/fhir/StructureDefinition/match-grade"
	matchDefaultCount    = 20
	matchTelecomSystems  = "phone email"
	fuzzyMinNameLength   = 4
	matchOneDayTolerance = 24 * time.Hour
)

func (r *Router) registerPatientMatch() {
	r.Add("POST", "Patient/$match", r.patientMatch)
}

type matchFieldKind int

const (
	fieldNone matchFieldKind = iota
	fieldFuzzy
	fieldExact
)

type matchResult struct {
	patient   *fhir.Patient
	score     float64
	grade     string
	dobKind   matchFieldKind
	firstKind matchFieldKind
}

func (r *Router) patientMatch(ctx context.Context, req *Request) (*Response, error) {
	in, err := ParseInput(req)
	if err != nil {
		return nil, err
	}
	doc, ok := in.Resource("resource")
	if !ok {
		if in.Body != nil && dstr(in.Body, "resourceType") == "Patient" {
			doc, ok = in.Body, true
		}
	}
	if !ok || dstr(doc, "resourceType") != "Patient" {
		return nil, BadRequest(`Input parameter "resource" must be a Patient resource`)
	}
	res, err := toResource(doc)
	if err != nil {
		return nil, err
	}
	input := res.(*fhir.Patient)
	if !hasMatchableField(input) {
		return nil, BadRequest("Input Patient must include at least one of: identifier, name, birthDate, or telecom")
	}
	count, err := in.Int("count", matchDefaultCount)
	if err != nil || count <= 0 {
		return nil, BadRequest("count phải là số nguyên dương")
	}
	onlyCertain := strings.EqualFold(in.String("onlyCertainMatches"), "true")

	repo := r.repoOf(ctx)
	cands, truncated, err := gatherCandidates(ctx, repo, input)
	if err != nil {
		return nil, err
	}
	scored := make([]matchResult, len(cands))
	for i, c := range cands {
		scored[i] = scoreCandidate(c, input, false)
	}
	withDOB := 0
	for _, s := range scored {
		if s.dobKind != fieldNone {
			withDOB++
		}
	}
	if withDOB > 1 { // anh em sinh đôi: tên đầu phải khớp exact
		for i, c := range cands {
			scored[i] = scoreCandidate(c, input, true)
		}
	}
	var relevant, certain []matchResult
	for _, s := range scored {
		if s.grade != "certainly-not" {
			relevant = append(relevant, s)
		}
		if s.grade == "certain" {
			certain = append(certain, s)
		}
	}
	out := relevant
	if onlyCertain {
		out = nil
		if !truncated && len(certain) == 1 {
			out = certain
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].score > out[j].score })
	if len(out) > count {
		out = out[:count]
	}
	total := len(out)
	b := &fhir.Bundle{Type: "searchset", Total: &total}
	for _, m := range out {
		b.Entry = append(b.Entry, fhir.BundleEntry{
			Resource: &fhir.AnyResource{Value: m.patient},
			Search:   &fhir.BundleEntrySearch{Mode: "match", Score: json.Number(strconv.FormatFloat(m.score, 'f', -1, 64)), Extension: []fhir.Extension{{URL: matchGradeURL, ValueCode: m.grade}}},
		})
	}
	return &Response{Status: 200, Resource: b}, nil
}

func hasMatchableField(p *fhir.Patient) bool {
	for _, id := range p.Identifier {
		if id.Value != "" {
			return true
		}
	}
	for _, n := range p.Name {
		if n.Family != "" || len(n.Given) > 0 {
			return true
		}
	}
	if p.BirthDate != "" {
		return true
	}
	for _, t := range p.Telecom {
		if (t.System == "phone" || t.System == "email") && t.Value != "" {
			return true
		}
	}
	return false
}

func gatherCandidates(ctx context.Context, repo Repository, in *fhir.Patient) ([]*fhir.Patient, bool, error) {
	seen := map[string]*fhir.Patient{}
	var order []string
	truncated := false
	run := func(q url.Values) error {
		sr, err := fhirsearch.Parse("Patient", q)
		if err != nil {
			return BadRequest("Error searching for patient candidates: " + err.Error())
		}
		sr.Count, sr.Total = matchCandidateCount, "none"
		res, err := repo.Search(ctx, sr)
		if err != nil {
			return err
		}
		if len(res.Matches) >= matchCandidateCount || res.More {
			truncated = true
		}
		for _, m := range res.Matches {
			if p, ok := m.(*fhir.Patient); ok {
				if _, dup := seen[p.ID]; !dup {
					seen[p.ID] = p
					order = append(order, p.ID)
				}
			}
		}
		return nil
	}
	for _, id := range in.Identifier {
		if id.Value == "" {
			continue
		}
		v := id.Value
		if id.System != "" {
			v = id.System + "|" + id.Value
		}
		if err := run(url.Values{"identifier": {v}}); err != nil {
			return nil, false, err
		}
	}
	telecoms := map[string]bool{}
	for _, t := range in.Telecom {
		if (t.System == "phone" || t.System == "email") && t.Value != "" && !telecoms[t.Value] {
			telecoms[t.Value] = true
			if err := run(url.Values{"telecom": {t.Value}}); err != nil {
				return nil, false, err
			}
		}
	}
	if dob := dobFilters(in.BirthDate); dob != nil {
		family, given := "", ""
		for _, n := range in.Name {
			if family == "" && n.Family != "" {
				family = n.Family
			}
			if given == "" && len(n.Given) > 0 {
				given = n.Given[0]
			}
		}
		for _, extra := range []url.Values{{"family:exact": {family}}, {"given:exact": {given}}} {
			for k, v := range extra {
				if v[0] == "" {
					continue
				}
				q := url.Values{"birthdate": dob}
				q[k] = v
				if err := run(q); err != nil {
					return nil, false, err
				}
			}
		}
	}
	out := make([]*fhir.Patient, 0, len(order))
	for _, id := range order {
		out = append(out, seen[id])
	}
	return out, truncated, nil
}

// dobFilters: birthdate trong ±1 ngày (ngày đầy đủ), hoặc đúng chuỗi nhập (ngày không đầy đủ).
func dobFilters(birth string) []string {
	if birth == "" {
		return nil
	}
	t, err := time.Parse("2006-01-02", birth)
	if err != nil || len(birth) != 10 {
		return []string{birth}
	}
	return []string{"ge" + t.Add(-matchOneDayTolerance).Format("2006-01-02"), "le" + t.Add(matchOneDayTolerance).Format("2006-01-02")}
}

// fold bỏ dấu và hoa thường: NFD rồi bỏ dấu kết hợp, đ → d.
func fold(s string) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "đ", "d"), "Đ", "D")
	var b strings.Builder
	for _, r := range norm.NFD.String(strings.TrimSpace(s)) {
		if !unicode.Is(unicode.Mn, r) {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

func nfcLower(s string) string { return strings.ToLower(norm.NFC.String(strings.TrimSpace(s))) }

func levenshteinAtMostOne(a, b string) bool {
	ra, rb := []rune(a), []rune(b)
	if d := len(ra) - len(rb); d > 1 || d < -1 {
		return false
	}
	i, j, diff := 0, 0, 0
	for i < len(ra) && j < len(rb) {
		if ra[i] == rb[j] {
			i++
			j++
			continue
		}
		diff++
		if diff > 1 {
			return false
		}
		switch {
		case len(ra) > len(rb):
			i++
		case len(ra) < len(rb):
			j++
		default:
			i++
			j++
		}
	}
	return diff+(len(ra)-i)+(len(rb)-j) <= 1
}

func compareName(a, b string, exactOnly bool) matchFieldKind {
	if a == "" || b == "" {
		return fieldNone
	}
	if nfcLower(a) == nfcLower(b) {
		return fieldExact
	}
	if exactOnly {
		return fieldNone
	}
	fa, fb := fold(a), fold(b)
	if fa == fb || (len([]rune(fa)) >= fuzzyMinNameLength && len([]rune(fb)) >= fuzzyMinNameLength && levenshteinAtMostOne(fa, fb)) {
		return fieldFuzzy
	}
	return fieldNone
}

func firstGiven(p *fhir.Patient) string {
	for _, n := range p.Name {
		if len(n.Given) > 0 {
			return n.Given[0]
		}
	}
	return ""
}

func family(p *fhir.Patient) string {
	for _, n := range p.Name {
		if n.Family != "" {
			return n.Family
		}
	}
	return ""
}

func compareDOB(a, b string) matchFieldKind {
	if a == "" || b == "" {
		return fieldNone
	}
	if a == b {
		return fieldExact
	}
	ta, e1 := time.Parse("2006-01-02", a)
	tb, e2 := time.Parse("2006-01-02", b)
	if e1 == nil && e2 == nil && len(a) == 10 && len(b) == 10 {
		if d := ta.Sub(tb); d <= matchOneDayTolerance && d >= -matchOneDayTolerance {
			return fieldFuzzy
		}
	}
	return fieldNone
}

func digits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func telecomSet(p *fhir.Patient, system string) map[string]bool {
	out := map[string]bool{}
	for _, t := range p.Telecom {
		if t.System != system || t.Value == "" {
			continue
		}
		if system == "phone" {
			if d := digits(t.Value); d != "" {
				out[strings.TrimLeft(d, "0")] = true // 090.. và +84 90.. (đã bỏ số 0 đầu) khác nhau; so theo chữ số, tên miền nội bộ
			}
		} else {
			out[strings.ToLower(strings.TrimSpace(t.Value))] = true
		}
	}
	return out
}

func intersects(a, b map[string]bool) bool {
	for k := range a {
		if b[k] {
			return true
		}
	}
	return false
}

func addressLines(p *fhir.Patient) map[string]bool {
	out := map[string]bool{}
	for _, a := range p.Address {
		for _, l := range a.Line {
			if l = fold(l); l != "" {
				out[l] = true
			}
		}
	}
	return out
}

func postalCodes(p *fhir.Patient) map[string]bool {
	out := map[string]bool{}
	for _, a := range p.Address {
		if a.PostalCode != "" {
			out[strings.ToLower(strings.ReplaceAll(a.PostalCode, " ", ""))] = true
		}
	}
	return out
}

func identifierKeys(p *fhir.Patient) map[string]bool {
	out := map[string]bool{}
	for _, id := range p.Identifier {
		if id.Value != "" && id.System != "" {
			out[id.System+"|"+id.Value] = true
		}
	}
	return out
}

var generationalSuffixes = map[string]string{"jr": "jr", "jr.": "jr", "sr": "sr", "sr.": "sr", "ii": "ii", "iii": "iii", "iv": "iv"}

func suffixes(p *fhir.Patient) map[string]bool {
	out := map[string]bool{}
	for _, n := range p.Name {
		for _, s := range n.Suffix {
			if g, ok := generationalSuffixes[strings.ToLower(strings.TrimSpace(s))]; ok {
				out[g] = true
			}
		}
	}
	return out
}

func scoreCandidate(c, in *fhir.Patient, exactFirstName bool) matchResult {
	res := matchResult{patient: c}
	// hậu tố thế hệ khác nhau (khi cả hai bên đều khai) và giới tính khác nhau: loại ngay
	if a, b := suffixes(in), suffixes(c); len(a) > 0 && len(b) > 0 && !intersects(a, b) {
		res.grade = "certainly-not"
		return res
	}
	if in.Gender != "" && c.Gender != "" && in.Gender != c.Gender && in.Gender != "unknown" && c.Gender != "unknown" && in.Gender != "other" && c.Gender != "other" {
		res.grade = "certainly-not"
		return res
	}
	res.firstKind = compareName(firstGiven(in), firstGiven(c), exactFirstName)
	lastKind := compareName(family(in), family(c), false)
	res.dobKind = compareDOB(in.BirthDate, c.BirthDate)
	exact, fuzzy := 0, 0
	count := func(k matchFieldKind) {
		switch k {
		case fieldExact:
			exact++
		case fieldFuzzy:
			fuzzy++
		}
	}
	count(res.firstKind)
	count(lastKind)
	count(res.dobKind)
	phone := intersects(telecomSet(in, "phone"), telecomSet(c, "phone"))
	email := intersects(telecomSet(in, "email"), telecomSet(c, "email"))
	street := intersects(addressLines(in), addressLines(c))
	postal := intersects(postalCodes(in), postalCodes(c))
	for _, ok := range []bool{phone, email, street, postal} {
		if ok {
			exact++
		}
	}
	if in.Gender != "" && in.Gender == c.Gender {
		exact++
	}
	identifier := intersects(identifierKeys(in), identifierKeys(c))
	res.score = min((float64(exact)+0.5*float64(fuzzy))/matchFactor, matchMaxScore)
	switch {
	case identifier || (res.firstKind == fieldExact && lastKind == fieldExact && res.dobKind == fieldExact && (phone || email || street)):
		res.score, res.grade = 1, "certain"
	case res.score >= matchProbable:
		res.grade = "probable"
	case res.score >= matchPossible:
		res.grade = "possible"
	default:
		res.grade = "certainly-not"
	}
	return res
}
