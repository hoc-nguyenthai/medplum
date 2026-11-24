package dicomretrieve

import (
	"net/http"
	"net/url"
	"sort"
	"strings"

	"trivita-fhir/pkg/dicomweb"
)

// Level là cấp truy vấn QIDO-RS (PS3.18 8.3.4): study, series hay instance. Truy vấn ở một cấp có thể kèm thuộc tính của cấp trên (ví dụ tìm series theo PatientID).
type Level int

const (
	LevelStudy Level = iota
	LevelSeries
	LevelInstance
)

type matchKind int

const (
	matchExact  matchKind = iota // so khớp nguyên chuỗi (UID, mã, số)
	matchPrefix                  // PatientName và tên người: khớp đầu chuỗi, không phân biệt hoa thường; dấu * ở cuối được bỏ
	matchDate                    // ngày hoặc khoảng ngày YYYYMMDD, YYYYMMDD-YYYYMMDD, -YYYYMMDD, YYYYMMDD-
	matchToken                   // mã (ModalitiesInStudy)
	matchPost                    // không có tham số tìm kiếm tương ứng ở kho: lọc sau khi đọc (Modality của series)
)

type attr struct {
	level Level
	code  string // tham số tìm kiếm FHIR (rỗng với matchPost)
	kind  matchKind
}

// Thuộc tính QIDO-RS được hỗ trợ, theo từ khóa DICOM và theo tag (8 chữ số hex, chữ hoa hay thường đều được).
var attrs = map[string]attr{
	"StudyInstanceUID": {LevelStudy, "study-instance-uid", matchExact}, "0020000D": {LevelStudy, "study-instance-uid", matchExact},
	"PatientID": {LevelStudy, "patient-id", matchExact}, "00100020": {LevelStudy, "patient-id", matchExact},
	"PatientName": {LevelStudy, "patient-name", matchPrefix}, "00100010": {LevelStudy, "patient-name", matchPrefix},
	"AccessionNumber": {LevelStudy, "accession-number", matchExact}, "00080050": {LevelStudy, "accession-number", matchExact},
	"StudyID": {LevelStudy, "study-id", matchExact}, "00200010": {LevelStudy, "study-id", matchExact},
	"ReferringPhysicianName": {LevelStudy, "referring-physicians-name", matchPrefix}, "00080090": {LevelStudy, "referring-physicians-name", matchPrefix},
	"StudyDate": {LevelStudy, "study-date", matchDate}, "00080020": {LevelStudy, "study-date", matchDate},
	"ModalitiesInStudy": {LevelStudy, "modalities", matchToken}, "00080061": {LevelStudy, "modalities", matchToken},
	"SeriesInstanceUID": {LevelSeries, "series-instance-uid", matchExact}, "0020000E": {LevelSeries, "series-instance-uid", matchExact},
	"SeriesNumber": {LevelSeries, "series-number", matchExact}, "00200011": {LevelSeries, "series-number", matchExact},
	"Modality": {LevelSeries, "", matchPost}, "00080060": {LevelSeries, "", matchPost},
	"SOPInstanceUID": {LevelInstance, "sop-instance-uid", matchExact}, "00080018": {LevelInstance, "sop-instance-uid", matchExact},
	"SOPClassUID": {LevelInstance, "sop-class-uid", matchExact}, "00080016": {LevelInstance, "sop-class-uid", matchExact},
	"InstanceNumber": {LevelInstance, "instance-number", matchExact}, "00200013": {LevelInstance, "instance-number", matchExact},
}

// Tham số điều khiển của QIDO-RS không phải thuộc tính: limit và offset được đọc; includefield (ta luôn trả mọi thuộc tính đã lưu) và fuzzymatching (không hỗ trợ so khớp
// mờ) được chấp nhận và bỏ qua mà không báo.
var controlParams = map[string]bool{"limit": true, "offset": true, "includefield": true, "fuzzymatching": true}

// Query là điều kiện QIDO-RS đã phân tích: tham số tìm kiếm FHIR theo từng cấp. Ignored là các tham số không hỗ trợ (bị bỏ qua, nên báo cho client bằng header Warning).
type Query struct {
	Limit, Offset           int
	Study, Series, Instance url.Values
	Modality                []string // lọc sau theo Modality của series (kho không có tham số tìm kiếm này)
	Ignored                 []string
}

// ParseQIDO phân tích các tham số truy vấn của một yêu cầu QIDO-RS. Giá trị nhiều phần tử (ngăn bằng dấu \ theo DICOM, hoặc dấu phẩy) là OR. Lỗi 400 với tên thuộc tính hoặc
// giá trị sai (ngày không hợp lệ, ký tự đại diện ở giữa chuỗi).
func ParseQIDO(params url.Values) (*Query, error) {
	q := &Query{Study: url.Values{}, Series: url.Values{}, Instance: url.Values{}}
	if v := params.Get("limit"); v != "" {
		q.Limit, _ = dicomweb.ParseQueryInt(v)
	}
	if v := params.Get("offset"); v != "" {
		q.Offset, _ = dicomweb.ParseQueryInt(v)
	}
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if controlParams[strings.ToLower(k)] {
			continue
		}
		a, ok := attrs[k]
		if !ok {
			a, ok = attrs[strings.ToUpper(k)] // tag viết thường
		}
		if !ok {
			q.Ignored = append(q.Ignored, k)
			continue
		}
		vals := splitValues(params[k])
		if len(vals) == 0 {
			continue // tham số rỗng: không lọc
		}
		target := q.Study
		switch a.level {
		case LevelSeries:
			target = q.Series
		case LevelInstance:
			target = q.Instance
		}
		bad := func(msg string) error { return &Error{http.StatusBadRequest, k + ": " + msg} }
		switch a.kind {
		case matchExact:
			target.Set(a.code+":exact", strings.Join(vals, ","))
		case matchToken:
			target.Set(a.code, strings.Join(vals, ","))
		case matchPrefix:
			var out []string
			for _, v := range vals {
				v = strings.TrimSuffix(v, "*")
				if strings.ContainsAny(v, "*?") {
					return nil, bad("ký tự đại diện chỉ được dùng ở cuối giá trị")
				}
				out = append(out, v)
			}
			target.Set(a.code, strings.Join(out, ","))
		case matchDate:
			if len(vals) != 1 {
				return nil, bad("chỉ nhận một ngày hoặc một khoảng ngày")
			}
			conds, err := dateConds(vals[0])
			if err != nil {
				return nil, bad(err.Error())
			}
			for _, c := range conds {
				target.Add(a.code, c)
			}
		case matchPost:
			for _, v := range vals {
				q.Modality = append(q.Modality, strings.ToUpper(strings.TrimSpace(v)))
			}
		}
	}
	return q, nil
}

func splitValues(raw []string) []string {
	var out []string
	for _, r := range raw {
		for _, p := range strings.FieldsFunc(r, func(c rune) bool { return c == '\\' || c == ',' }) {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

// dateConds đổi giá trị ngày của QIDO-RS sang điều kiện của tham số date FHIR: một ngày là "eq", khoảng là "ge" và "le" (các điều kiện của cùng một tham số kết hợp bằng AND).
func dateConds(v string) ([]string, error) {
	conv := func(d string) (string, error) {
		f := dicomweb.DateToFHIR(d)
		if f == "" {
			return "", &Error{http.StatusBadRequest, "ngày phải là YYYYMMDD hợp lệ"}
		}
		return f, nil
	}
	from, to, isRange := strings.Cut(v, "-")
	if !isRange {
		d, err := conv(v)
		if err != nil {
			return nil, err
		}
		return []string{"eq" + d}, nil
	}
	var out []string
	if from != "" {
		d, err := conv(from)
		if err != nil {
			return nil, err
		}
		out = append(out, "ge"+d)
	}
	if to != "" {
		d, err := conv(to)
		if err != nil {
			return nil, err
		}
		out = append(out, "le"+d)
	}
	if len(out) == 0 {
		return nil, &Error{http.StatusBadRequest, "khoảng ngày rỗng"}
	}
	return out, nil
}
