package dicomweb

import (
	"encoding/json"
	"strconv"
	"strings"

	"trivita-fhir/pkg/fhir"
)

// ---- đọc giá trị từ Dataset ----

func (ds Dataset) first(tag string) any {
	if el, ok := ds[tag]; ok && len(el.Value) > 0 {
		return el.Value[0]
	}
	return nil
}

// str trả giá trị chuỗi đầu tiên của phần tử ("" nếu không có hoặc không phải chuỗi).
func (ds Dataset) str(tag string) string {
	s, _ := ds.first(tag).(string)
	return s
}

// strs trả mọi giá trị chuỗi của phần tử (bỏ giá trị không phải chuỗi); nil nếu không có.
func (ds Dataset) strs(tag string) []string {
	var out []string
	for _, v := range ds[tag].Value {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// Str trả giá trị chuỗi đầu tiên của phần tử ("" nếu không có hoặc không phải chuỗi).
func (ds Dataset) Str(tag string) string { return ds.str(tag) }

// Int trả giá trị số nguyên đầu tiên của phần tử (nil nếu không có hoặc không phải số nguyên).
func (ds Dataset) Int(tag string) *int { return ds.num(tag) }

// pn trả tên người của phần tử PN.
func (ds Dataset) pn(tag string) string { return PersonNameToString(ds[tag].Value) }

// num trả giá trị số nguyên đầu tiên (nhận int, int64, float64 nguyên, json.Number hoặc chuỗi toàn số như IS dạng chuỗi).
func (ds Dataset) num(tag string) *int {
	switch v := ds.first(tag).(type) {
	case int:
		return &v
	case int64:
		n := int(v)
		return &n
	case float64:
		if v == float64(int(v)) {
			n := int(v)
			return &n
		}
	case json.Number:
		if n, err := strconv.Atoi(v.String()); err == nil {
			return &n
		}
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return &n
		}
	}
	return nil
}

func set(ds Dataset, tag, vr string, values ...any) {
	if len(values) > 0 {
		ds[tag] = Element{VR: vr, Value: values}
	}
}

func setStr(ds Dataset, tag, vr, s string) {
	if s != "" {
		set(ds, tag, vr, s)
	}
}

func setNum(ds Dataset, tag string, n *int) {
	if n != nil {
		set(ds, tag, "IS", *n)
	}
}

// ---- study ----

// StudyFromDataset dựng DicomStudy từ DICOM JSON (không gán id và meta). Ngày và giờ không hợp lệ bị bỏ. ReferringPhysicianName được đọc đúng từ khóa DICOM
// (Medplum đọc nhầm "ReferringPhysiciansName" nên không bao giờ lấy được bác sĩ chỉ định) và đổi từ PN sang chuỗi.
func StudyFromDataset(ds Dataset) *fhir.DicomStudy {
	return &fhir.DicomStudy{
		ResourceType:                  "DicomStudy",
		StudyInstanceUid:              ds.str(TagStudyInstanceUID),
		StudyId:                       ds.str(TagStudyID),
		StudyDate:                     DateToFHIR(ds.str(TagStudyDate)),
		StudyTime:                     TimeToFHIR(ds.str(TagStudyTime)),
		AccessionNumber:               ds.str(TagAccessionNumber),
		InstanceAvailability:          ds.str(TagInstanceAvailability),
		ModalitiesInStudy:             ds.strs(TagModalitiesInStudy),
		ReferringPhysiciansName:       ds.pn(TagReferringPhysicianName),
		TimezoneOffsetFromUtc:         ds.str(TagTimezoneOffsetFromUTC),
		PatientName:                   ds.pn(TagPatientName),
		PatientId:                     ds.str(TagPatientID),
		PatientBirthDate:              DateToFHIR(ds.str(TagPatientBirthDate)),
		PatientSex:                    ds.str(TagPatientSex),
		NumberOfStudyRelatedSeries:    ds.num(TagNumberOfStudyRelatedSeries),
		NumberOfStudyRelatedInstances: ds.num(TagNumberOfStudyRelatedInstances),
	}
}

// StudyToDataset dựng DICOM JSON của một DicomStudy (phần tử rỗng bị bỏ).
func StudyToDataset(s *fhir.DicomStudy) Dataset {
	ds := Dataset{}
	setStr(ds, TagStudyInstanceUID, "UI", s.StudyInstanceUid)
	setStr(ds, TagStudyID, "SH", s.StudyId)
	setStr(ds, TagStudyDate, "DA", DateToDICOM(s.StudyDate))
	setStr(ds, TagStudyTime, "TM", TimeToDICOM(s.StudyTime))
	setStr(ds, TagAccessionNumber, "SH", s.AccessionNumber)
	setStr(ds, TagInstanceAvailability, "CS", s.InstanceAvailability)
	if len(s.ModalitiesInStudy) > 0 {
		vals := make([]any, len(s.ModalitiesInStudy))
		for i, m := range s.ModalitiesInStudy {
			vals[i] = m
		}
		set(ds, TagModalitiesInStudy, "CS", vals...)
	}
	set(ds, TagReferringPhysicianName, "PN", StringToPersonName(s.ReferringPhysiciansName)...)
	setStr(ds, TagTimezoneOffsetFromUTC, "SH", s.TimezoneOffsetFromUtc)
	set(ds, TagPatientName, "PN", StringToPersonName(s.PatientName)...)
	setStr(ds, TagPatientID, "LO", s.PatientId)
	setStr(ds, TagPatientBirthDate, "DA", DateToDICOM(s.PatientBirthDate))
	setStr(ds, TagPatientSex, "CS", s.PatientSex)
	setNum(ds, TagNumberOfStudyRelatedSeries, s.NumberOfStudyRelatedSeries)
	setNum(ds, TagNumberOfStudyRelatedInstances, s.NumberOfStudyRelatedInstances)
	return ds
}

// ---- series ----

// SeriesFromDataset dựng DicomSeries từ DICOM JSON; studyRef là tham chiếu tới DicomStudy chứa nó (dạng "DicomStudy/<id>").
func SeriesFromDataset(studyRef string, ds Dataset) *fhir.DicomSeries {
	s := &fhir.DicomSeries{
		ResourceType:                    "DicomSeries",
		SeriesInstanceUid:               ds.str(TagSeriesInstanceUID),
		Modality:                        ds.str(TagModality),
		SeriesDescription:               ds.str(TagSeriesDescription),
		TimezoneOffsetFromUtc:           ds.str(TagTimezoneOffsetFromUTC),
		NumberOfSeriesRelatedInstances:  ds.num(TagNumberOfSeriesRelatedInstances),
		PerformedProcedureStepStartDate: DateToFHIR(ds.str(TagPerformedProcedureStepStartDate)),
		PerformedProcedureStepStartTime: TimeToFHIR(ds.str(TagPerformedProcedureStepStartTime)),
	}
	if studyRef != "" {
		s.Study = &fhir.Reference{Reference: studyRef}
	}
	if n := ds.num(TagSeriesNumber); n != nil {
		s.SeriesNumber = strconv.Itoa(*n)
	}
	return s
}

// SeriesToDataset dựng DICOM JSON của một series, gồm cả thuộc tính của study chứa nó (kết quả QIDO-RS cấp series mang cả hai cấp, PS3.18 8.3.4).
// SeriesNumber không phải số nguyên thì bị bỏ.
func SeriesToDataset(study *fhir.DicomStudy, series *fhir.DicomSeries) Dataset {
	ds := StudyToDataset(study)
	setStr(ds, TagSeriesInstanceUID, "UI", series.SeriesInstanceUid)
	if n, err := strconv.Atoi(strings.TrimSpace(series.SeriesNumber)); err == nil {
		set(ds, TagSeriesNumber, "IS", n)
	}
	setStr(ds, TagModality, "CS", series.Modality)
	setStr(ds, TagSeriesDescription, "LO", series.SeriesDescription)
	setStr(ds, TagTimezoneOffsetFromUTC, "SH", series.TimezoneOffsetFromUtc)
	setNum(ds, TagNumberOfSeriesRelatedInstances, series.NumberOfSeriesRelatedInstances)
	setStr(ds, TagPerformedProcedureStepStartDate, "DA", DateToDICOM(series.PerformedProcedureStepStartDate))
	setStr(ds, TagPerformedProcedureStepStartTime, "TM", TimeToDICOM(series.PerformedProcedureStepStartTime))
	return ds
}
