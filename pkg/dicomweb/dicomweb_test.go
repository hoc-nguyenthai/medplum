package dicomweb_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"trivita-fhir/pkg/dicomweb"
	"trivita-fhir/pkg/fhir"
)

func el(vr string, v ...any) dicomweb.Element { return dicomweb.Element{VR: vr, Value: v} }

func pn(s string) dicomweb.PersonName { return dicomweb.PersonName{"Alphabetic": s} }

func ip(n int) *int { return &n }

// ---------------------------------------------------------------- study và series (utils.test.ts: 33-145)

func TestStudyFromDataset(t *testing.T) {
	got := dicomweb.StudyFromDataset(dicomweb.Dataset{
		dicomweb.TagStudyInstanceUID:              el("UI", "study-uid"),
		dicomweb.TagStudyID:                       el("SH", "study-id"),
		dicomweb.TagStudyDate:                     el("DA", "20240102"),
		dicomweb.TagStudyTime:                     el("TM", "030405"),
		dicomweb.TagAccessionNumber:               el("SH", "A123"),
		dicomweb.TagInstanceAvailability:          el("CS", "ONLINE"),
		dicomweb.TagModalitiesInStudy:             el("CS", "CT"),
		dicomweb.TagReferringPhysicianName:        el("PN", pn("Test^Dr")),
		dicomweb.TagTimezoneOffsetFromUTC:         el("SH", "-0700"),
		dicomweb.TagPatientName:                   el("PN", pn("TEST^PATIENT ")),
		dicomweb.TagPatientID:                     el("LO", "P123"),
		dicomweb.TagPatientBirthDate:              el("DA", "20000101"),
		dicomweb.TagPatientSex:                    el("CS", "O"),
		dicomweb.TagNumberOfStudyRelatedSeries:    el("IS", 2),
		dicomweb.TagNumberOfStudyRelatedInstances: el("IS", 3),
	})
	want := &fhir.DicomStudy{
		ResourceType: "DicomStudy", StudyInstanceUid: "study-uid", StudyId: "study-id", StudyDate: "2024-01-02", StudyTime: "03:04:05",
		AccessionNumber: "A123", InstanceAvailability: "ONLINE", ModalitiesInStudy: []string{"CT"}, ReferringPhysiciansName: "Test^Dr",
		TimezoneOffsetFromUtc: "-0700", PatientName: "TEST^PATIENT", PatientId: "P123", PatientBirthDate: "2000-01-01", PatientSex: "O",
		NumberOfStudyRelatedSeries: ip(2), NumberOfStudyRelatedInstances: ip(3),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("\n got %+v\nwant %+v", got, want)
	}
}

// Lỗi #2 của Medplum: dcmjsStudyToMedplumStudy đọc "ReferringPhysiciansName" (thừa chữ s) trong khi chiều ngược lại ghi "ReferringPhysicianName", nên bác sĩ
// chỉ định không bao giờ được đọc lại. Bản Go đọc và ghi cùng một tag 0008,0090 kiểu PN, và đi một vòng thì giữ nguyên.
func TestReferringPhysicianRoundTrips(t *testing.T) {
	study := &fhir.DicomStudy{StudyInstanceUid: "1.2", ReferringPhysiciansName: "Nguyen^Bac Si"}
	ds := dicomweb.StudyToDataset(study)
	el, ok := ds[dicomweb.TagReferringPhysicianName]
	if !ok || el.VR != "PN" || dicomweb.PersonNameToString(el.Value) != "Nguyen^Bac Si" {
		t.Fatalf("ghi: %+v", el)
	}
	// qua JSON và ngược lại, như metadata lưu rồi đọc ra
	raw, err := json.Marshal(ds)
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]struct {
		VR    string
		Value []any
	}
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if v := back["00080090"]; v.VR != "PN" || dicomweb.PersonNameToString(v.Value) != "Nguyen^Bac Si" {
		t.Errorf("sau JSON: %+v", v)
	}
	if got := dicomweb.StudyFromDataset(ds).ReferringPhysiciansName; got != "Nguyen^Bac Si" {
		t.Errorf("đọc lại: %q", got)
	}
}

func TestStudyToDataset(t *testing.T) {
	ds := dicomweb.StudyToDataset(&fhir.DicomStudy{
		StudyInstanceUid: "study-uid", StudyId: "study-id", StudyDate: "2024-01-02", StudyTime: "03:04:05", AccessionNumber: "A123",
		PatientName: "TEST^PATIENT", PatientBirthDate: "2000-01-01", ModalitiesInStudy: []string{"CT", "PT"},
		NumberOfStudyRelatedSeries: ip(2), NumberOfStudyRelatedInstances: ip(3),
	})
	b, _ := json.Marshal(ds)
	for _, frag := range []string{
		`"0020000D":{"vr":"UI","Value":["study-uid"]}`,
		`"00200010":{"vr":"SH","Value":["study-id"]}`,
		`"00080020":{"vr":"DA","Value":["20240102"]}`,
		`"00080030":{"vr":"TM","Value":["030405"]}`,
		`"00080050":{"vr":"SH","Value":["A123"]}`,
		`"00080061":{"vr":"CS","Value":["CT","PT"]}`,
		`"00100010":{"vr":"PN","Value":[{"Alphabetic":"TEST^PATIENT"}]}`,
		`"00100030":{"vr":"DA","Value":["20000101"]}`,
		`"00201206":{"vr":"IS","Value":[2]}`,
		`"00201208":{"vr":"IS","Value":[3]}`,
	} {
		if !strings.Contains(string(b), frag) {
			t.Errorf("thiếu %s trong %s", frag, b)
		}
	}
	// thuộc tính trống không sinh phần tử
	for _, tag := range []string{dicomweb.TagReferringPhysicianName, dicomweb.TagPatientID, dicomweb.TagPatientSex, dicomweb.TagInstanceAvailability} {
		if _, ok := ds[tag]; ok {
			t.Errorf("phần tử trống %s không được xuất hiện", tag)
		}
	}
}

func TestSeriesBothDirections(t *testing.T) {
	study := &fhir.DicomStudy{ID: "study-id", StudyInstanceUid: "study-uid"}
	got := dicomweb.SeriesFromDataset("DicomStudy/study-id", dicomweb.Dataset{
		dicomweb.TagSeriesInstanceUID:               el("UI", "series-uid"),
		dicomweb.TagSeriesNumber:                    el("IS", 7),
		dicomweb.TagModality:                        el("CS", "CT"),
		dicomweb.TagSeriesDescription:               el("LO", "Head CT"),
		dicomweb.TagTimezoneOffsetFromUTC:           el("SH", "-0700"),
		dicomweb.TagNumberOfSeriesRelatedInstances:  el("IS", 4),
		dicomweb.TagPerformedProcedureStepStartDate: el("DA", "20240102"),
		dicomweb.TagPerformedProcedureStepStartTime: el("TM", "030405"),
	})
	want := &fhir.DicomSeries{
		ResourceType: "DicomSeries", Study: &fhir.Reference{Reference: "DicomStudy/study-id"}, SeriesInstanceUid: "series-uid", SeriesNumber: "7",
		Modality: "CT", SeriesDescription: "Head CT", TimezoneOffsetFromUtc: "-0700", NumberOfSeriesRelatedInstances: ip(4),
		PerformedProcedureStepStartDate: "2024-01-02", PerformedProcedureStepStartTime: "03:04:05",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("\n got %+v\nwant %+v", got, want)
	}

	ds := dicomweb.SeriesToDataset(study, want)
	b, _ := json.Marshal(ds)
	for _, frag := range []string{
		`"0020000D":{"vr":"UI","Value":["study-uid"]}`, // gồm cả thuộc tính cấp study
		`"0020000E":{"vr":"UI","Value":["series-uid"]}`,
		`"00200011":{"vr":"IS","Value":[7]}`,
		`"00080060":{"vr":"CS","Value":["CT"]}`,
		`"00201209":{"vr":"IS","Value":[4]}`,
		`"00400244":{"vr":"DA","Value":["20240102"]}`,
		`"00400245":{"vr":"TM","Value":["030405"]}`,
	} {
		if !strings.Contains(string(b), frag) {
			t.Errorf("thiếu %s trong %s", frag, b)
		}
	}
	// SeriesNumber không phải số nguyên thì bỏ, không sinh số rác
	bad := *want
	bad.SeriesNumber = "abc"
	if _, ok := dicomweb.SeriesToDataset(study, &bad)[dicomweb.TagSeriesNumber]; ok {
		t.Error("SeriesNumber không phải số phải bị bỏ")
	}
}

func TestNumbersFromEveryJSONRepresentation(t *testing.T) {
	var ds dicomweb.Dataset
	// dạng sau khi giải mã JSON chung: float64; và dạng IS gửi bằng chuỗi
	if err := json.Unmarshal([]byte(`{"00201206":{"vr":"IS","Value":[2]},"00201208":{"vr":"IS","Value":["3"]},"00201209":{"vr":"IS","Value":[2.5]}}`), &ds); err != nil {
		t.Fatal(err)
	}
	s := dicomweb.StudyFromDataset(ds)
	if s.NumberOfStudyRelatedSeries == nil || *s.NumberOfStudyRelatedSeries != 2 || s.NumberOfStudyRelatedInstances == nil || *s.NumberOfStudyRelatedInstances != 3 {
		t.Errorf("số: %v %v", s.NumberOfStudyRelatedSeries, s.NumberOfStudyRelatedInstances)
	}
	if got := dicomweb.SeriesFromDataset("", ds).NumberOfSeriesRelatedInstances; got != nil {
		t.Errorf("số không nguyên phải bị bỏ: %d", *got)
	}
}

// ---------------------------------------------------------------- làm sạch (utils.test.ts: 146-181)

func TestCleanDataset(t *testing.T) {
	small, large := []byte{1, 2, 3}, make([]byte, 11*1024)
	in := dicomweb.Dataset{
		"00020010": el("UI", "1.2.840.10008.1.2.1"),
		"7FE00010": el("OB", small),
		"60003000": el("OB", small),
		"60FF3000": el("OW", small),
		"00100010": el("PN", pn("TEST^PATIENT")),
		"00081110": el("SQ",
			dicomweb.Dataset{"00020010": el("UI", "removed"), "00081155": el("UI", "kept")},
			"unchanged"),
		"00290010": el("OB", small),
		"00290012": el("OB", large),
		"00290013": el("OB", map[string]any{}),
		"00290014": el("OB", dicomweb.Dataset{}),
	}
	want := dicomweb.Dataset{
		"00100010": el("PN", pn("TEST^PATIENT")),
		"00081110": el("SQ", dicomweb.Dataset{"00081155": el("UI", "kept")}, "unchanged"),
		"00290010": {VR: "OB", InlineBinary: "AQID"},
		"00290013": {VR: "OB"},
		"00290014": {VR: "OB"},
	}
	got := dicomweb.CleanDataset(in)
	if !reflect.DeepEqual(got, want) {
		gb, _ := json.Marshal(got)
		wb, _ := json.Marshal(want)
		t.Errorf("\n got %s\nwant %s", gb, wb)
	}
	// không sửa đầu vào
	if _, ok := in["7FE00010"]; !ok || in["00290010"].InlineBinary != "" || len(in["00290010"].Value) != 1 {
		t.Error("CleanDataset đã sửa dataset đầu vào")
	}
}

func TestCleanDatasetIsCaseInsensitiveAndBoundsDepth(t *testing.T) {
	got := dicomweb.CleanDataset(dicomweb.Dataset{"7fe00010": el("OB", []byte{1}), "60ab3000": el("OB", []byte{1}), "0002000a": el("UI", "x"), "00100020": el("LO", "id")})
	if len(got) != 1 || got["00100020"].VR != "LO" {
		t.Errorf("tag chữ thường: %v", got)
	}
	// SQ lồng 100 lớp: không đệ quy vô hạn, phần quá sâu bị cắt
	deep := dicomweb.Dataset{"00100020": el("LO", "leaf")}
	for i := 0; i < 100; i++ {
		deep = dicomweb.Dataset{"00081110": el("SQ", deep)}
	}
	out := dicomweb.CleanDataset(deep)
	depth := 0
	for cur := out; ; depth++ {
		e, ok := cur["00081110"]
		if !ok {
			break
		}
		cur = e.Value[0].(dicomweb.Dataset)
	}
	if depth == 0 || depth > 33 {
		t.Errorf("độ sâu sau khi làm sạch: %d", depth)
	}
}

// ---------------------------------------------------------------- tên người, ngày, giờ (utils.test.ts: 182-221)

func TestPersonNameDateAndTime(t *testing.T) {
	if got := dicomweb.PersonNameToString([]any{pn(" TEST^PATIENT ")}); got != "TEST^PATIENT" {
		t.Errorf("PN: %q", got)
	}
	if got := dicomweb.PersonNameToString([]any{dicomweb.PersonName{"Ideographic": "ignored"}}); got != "" {
		t.Errorf("PN không có Alphabetic: %q", got)
	}
	if got := dicomweb.PersonNameToString([]any{map[string]any{"Alphabetic": " A^B "}}); got != "A^B" {
		t.Errorf("PN từ JSON chung: %q", got)
	}
	if dicomweb.PersonNameToString(nil) != "" || dicomweb.PersonNameToString([]any{42}) != "" {
		t.Error("PN rỗng hoặc sai kiểu phải trả rỗng")
	}
	if got := dicomweb.StringToPersonName("TEST^PATIENT"); !reflect.DeepEqual(got, []any{pn("TEST^PATIENT")}) {
		t.Errorf("StringToPersonName: %v", got)
	}
	if dicomweb.StringToPersonName("") != nil {
		t.Error("tên rỗng phải trả nil")
	}

	for in, want := range map[string]string{"20240102": "2024-01-02", "202401": ""} {
		if got := dicomweb.DateToFHIR(in); got != want {
			t.Errorf("DateToFHIR(%q) = %q, muốn %q", in, got, want)
		}
	}
	if dicomweb.DateToDICOM("2024-01-02") != "20240102" || dicomweb.DateToDICOM("") != "" {
		t.Error("DateToDICOM")
	}
	for in, want := range map[string]string{"030405.123": "03:04:05", "0304": ""} {
		if got := dicomweb.TimeToFHIR(in); got != want {
			t.Errorf("TimeToFHIR(%q) = %q, muốn %q", in, got, want)
		}
	}
	if dicomweb.TimeToDICOM("03:04:05") != "030405" || dicomweb.TimeToDICOM("") != "" {
		t.Error("TimeToDICOM")
	}
}

// Mỗi giá trị dưới đây đủ dài để bị định dạng lại, nên chỉ kiểm hợp lệ kết quả mới loại được; truyền "asdf-as-df" đi tiếp sẽ làm cả yêu cầu bị từ chối.
func TestInvalidDatesAndTimesAreDropped(t *testing.T) {
	for _, in := range []string{"asdfasdf", "99999999", "20241301", "20240132", "2024-01-02", "00000101", "20240230", "20230229", ""} {
		if got := dicomweb.DateToFHIR(in); got != "" {
			t.Errorf("DateToFHIR(%q) = %q, muốn rỗng", in, got)
		}
	}
	for _, in := range []string{"asdfas", "999999", "250405", "036005", "asdfasdfasdf", "30405", ""} {
		if got := dicomweb.TimeToFHIR(in); got != "" {
			t.Errorf("TimeToFHIR(%q) = %q, muốn rỗng", in, got)
		}
	}
	// ngày nhuận có thật; giây nhuận và nửa đêm hợp lệ và không bị coi là "rỗng"
	if got := dicomweb.DateToFHIR("20240229"); got != "2024-02-29" {
		t.Errorf("29/2/2024: %q", got)
	}
	if got := dicomweb.TimeToFHIR("235960"); got != "23:59:60" {
		t.Errorf("giây nhuận: %q", got)
	}
	if got := dicomweb.TimeToFHIR("000000"); got != "00:00:00" {
		t.Errorf("nửa đêm: %q", got)
	}
	// study có ngày hỏng vẫn lưu được, chỉ mất thuộc tính hỏng
	s := dicomweb.StudyFromDataset(dicomweb.Dataset{dicomweb.TagStudyInstanceUID: el("UI", "1.2"), dicomweb.TagStudyDate: el("DA", "asdfasdf"), dicomweb.TagStudyTime: el("TM", "999999")})
	if s.StudyInstanceUid != "1.2" || s.StudyDate != "" || s.StudyTime != "" {
		t.Errorf("study với ngày giờ hỏng: %+v", s)
	}
}

// ---------------------------------------------------------------- số liệu tổng hợp (utils.test.ts: 258-439, phần tính toán)

func TestStudyAggregates(t *testing.T) {
	mk := func(mods ...string) []*fhir.DicomSeries {
		var out []*fhir.DicomSeries
		for _, m := range mods {
			out = append(out, &fhir.DicomSeries{Modality: m})
		}
		return out
	}
	// hợp modality của study nhiều loại: chuẩn hóa, không trùng, có thứ tự
	agg := dicomweb.ComputeStudyAggregates(mk("PT", " ct ", "CT", "", "US", "MR", "DX"), 9)
	if !reflect.DeepEqual(agg.Modalities, []string{"CT", "DX", "MR", "PT", "US"}) || agg.Series != 7 || agg.Instances != 9 {
		t.Errorf("hợp modality: %+v", agg)
	}
	// không series nào có modality thì để trống, không phải mảng rỗng
	if none := dicomweb.ComputeStudyAggregates(mk("", " "), 0); none.Modalities != nil || none.Series != 2 {
		t.Errorf("không modality: %+v", none)
	}

	study := &fhir.DicomStudy{StudyInstanceUid: "1.2", ModalitiesInStudy: []string{"MR"}, NumberOfStudyRelatedSeries: ip(99)}
	updated, changed := dicomweb.ApplyStudyAggregates(study, agg)
	if !changed || !reflect.DeepEqual(updated.ModalitiesInStudy, []string{"CT", "DX", "MR", "PT", "US"}) || *updated.NumberOfStudyRelatedSeries != 7 || *updated.NumberOfStudyRelatedInstances != 9 {
		t.Fatalf("áp dụng: %+v changed=%v", updated, changed)
	}
	if study.ModalitiesInStudy[0] != "MR" || *study.NumberOfStudyRelatedSeries != 99 {
		t.Error("ApplyStudyAggregates đã sửa study đầu vào")
	}
	// đã đúng thì không tạo phiên bản mới (idempotent: chạy lại cho cùng đáp án)
	if again, changed := dicomweb.ApplyStudyAggregates(updated, agg); changed || again != updated {
		t.Errorf("study đã đúng không được báo thay đổi")
	}
	// study chưa có số liệu (nil) là khác
	if _, changed := dicomweb.ApplyStudyAggregates(&fhir.DicomStudy{}, dicomweb.StudyAggregates{}); !changed {
		t.Error("study chưa có số đếm phải được cập nhật dù đáp án là 0")
	}
}

func TestSeriesAggregates(t *testing.T) {
	s := &fhir.DicomSeries{SeriesInstanceUid: "1.2.3"}
	updated, changed := dicomweb.ApplySeriesAggregates(s, 5)
	if !changed || *updated.NumberOfSeriesRelatedInstances != 5 || s.NumberOfSeriesRelatedInstances != nil {
		t.Fatalf("áp dụng: %+v changed=%v (đầu vào %+v)", updated, changed, s)
	}
	if again, changed := dicomweb.ApplySeriesAggregates(updated, 5); changed || again != updated {
		t.Error("series đã đúng không được báo thay đổi")
	}
	if z, changed := dicomweb.ApplySeriesAggregates(&fhir.DicomSeries{}, 0); !changed || *z.NumberOfSeriesRelatedInstances != 0 {
		t.Errorf("0 instance vẫn phải được ghi: %+v %v", z, changed)
	}
}

// ---------------------------------------------------------------- parseQueryInt (utils.test.ts: 440-451)

func TestParseQueryInt(t *testing.T) {
	for in, want := range map[string]int{"123": 123, "0": 0, "007": 7} {
		if got, ok := dicomweb.ParseQueryInt(in); !ok || got != want {
			t.Errorf("ParseQueryInt(%q) = %d,%v", in, got, ok)
		}
	}
	for _, in := range []string{"", "abc", "-1", "+1", "1.5", "12abc", " 1", "99999999999999999999"} {
		if got, ok := dicomweb.ParseQueryInt(in); ok {
			t.Errorf("ParseQueryInt(%q) = %d, muốn không hợp lệ", in, got)
		}
	}
}
