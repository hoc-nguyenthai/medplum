package dicomweb

import (
	"slices"
	"sort"
	"strings"

	"trivita-fhir/pkg/fhir"
)

// Số liệu tổng hợp (ModalitiesInStudy, NumberOfStudyRelatedSeries, NumberOfStudyRelatedInstances, NumberOfSeriesRelatedInstances) là khóa truy vấn
// cấp study và series mà kho lưu trữ tự tính (PS3.4 C.6.2.1.2); instance thật hầu như không mang chúng. Tính từ dữ liệu đang lưu (không cộng dồn theo
// instance vừa đến) nên idempotent: tải lại, thử lại hay tải tiếp sau một lần lỗi một phần đều hội tụ về cùng đáp án. Phần đọc-sửa-ghi có điều kiện
// (ifMatch, thử tối đa 3 lần) cần kho nên nằm ở D3; gói này chỉ tính và so sánh.

// StudyAggregates là số liệu tổng hợp của một study.
type StudyAggregates struct {
	Modalities []string // đã chuẩn hóa (cắt khoảng trắng, chữ hoa), không trùng, sắp xếp; nil khi không series nào có modality
	Series     int
	Instances  int
}

// ComputeStudyAggregates tính số liệu của study từ các series của nó và tổng số instance.
func ComputeStudyAggregates(series []*fhir.DicomSeries, instances int) StudyAggregates {
	set := map[string]bool{}
	for _, s := range series {
		if m := strings.ToUpper(strings.TrimSpace(s.Modality)); m != "" {
			set[m] = true
		}
	}
	agg := StudyAggregates{Series: len(series), Instances: instances}
	for m := range set {
		agg.Modalities = append(agg.Modalities, m)
	}
	sort.Strings(agg.Modalities)
	return agg
}

// ApplyStudyAggregates trả bản sao của study với số liệu mới và changed=true nếu có gì khác; nếu đã đúng thì trả (study, false) để không tạo phiên bản mới
// vô ích.
func ApplyStudyAggregates(study *fhir.DicomStudy, agg StudyAggregates) (updated *fhir.DicomStudy, changed bool) {
	if slices.Equal(study.ModalitiesInStudy, agg.Modalities) &&
		study.NumberOfStudyRelatedSeries != nil && *study.NumberOfStudyRelatedSeries == agg.Series &&
		study.NumberOfStudyRelatedInstances != nil && *study.NumberOfStudyRelatedInstances == agg.Instances {
		return study, false
	}
	cp := *study
	cp.ModalitiesInStudy = agg.Modalities
	cp.NumberOfStudyRelatedSeries = &agg.Series
	cp.NumberOfStudyRelatedInstances = &agg.Instances
	return &cp, true
}

// ApplySeriesAggregates như ApplyStudyAggregates cho NumberOfSeriesRelatedInstances của một series.
func ApplySeriesAggregates(series *fhir.DicomSeries, instances int) (updated *fhir.DicomSeries, changed bool) {
	if series.NumberOfSeriesRelatedInstances != nil && *series.NumberOfSeriesRelatedInstances == instances {
		return series, false
	}
	cp := *series
	cp.NumberOfSeriesRelatedInstances = &instances
	return &cp, true
}
