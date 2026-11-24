package dicomretrieve

import (
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"testing"
)

func TestParseQIDO(t *testing.T) {
	q, err := ParseQIDO(url.Values{
		"PatientID": {"P1"}, "00100010": {"ng*"}, "StudyDate": {"20240101-20240131"}, "Modality": {"ct\\mr"}, "SOPInstanceUID": {"1.2"}, "limit": {"5"}, "offset": {"2"},
		"Foo": {"x"}, "includefield": {"all"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if q.Limit != 5 || q.Offset != 2 {
		t.Errorf("phân trang: %d %d", q.Limit, q.Offset)
	}
	wantStudy := url.Values{"patient-id:exact": {"P1"}, "patient-name": {"ng"}, "study-date": {"ge2024-01-01", "le2024-01-31"}}
	if !reflect.DeepEqual(q.Study, wantStudy) {
		t.Errorf("điều kiện study: %v, muốn %v", q.Study, wantStudy)
	}
	if !reflect.DeepEqual(q.Ignored, []string{"Foo"}) {
		t.Errorf("Ignored: %v", q.Ignored)
	}
	if !reflect.DeepEqual(q.Modality, []string{"CT", "MR"}) {
		t.Errorf("Modality: %v", q.Modality)
	}
	if want := (url.Values{"sop-instance-uid:exact": {"1.2"}}); !reflect.DeepEqual(q.Instance, want) || len(q.Series) != 0 {
		t.Errorf("điều kiện instance và series: %v %v", q.Instance, q.Series)
	}
}

func TestParseQIDOErrors(t *testing.T) {
	for name, v := range map[string]url.Values{
		"ngày hỏng":     {"StudyDate": {"2024-01-02"}},
		"ngày không có": {"StudyDate": {"20240230"}},
		"khoảng rỗng":   {"StudyDate": {"-"}},
		"* ở giữa":      {"PatientName": {"a*b"}},
		"? đại diện":    {"PatientName": {"a?"}},
		"nhiều ngày":    {"StudyDate": {`20240101\20240102`}},
	} {
		_, err := ParseQIDO(v)
		var e *Error
		if !errors.As(err, &e) || e.Status != http.StatusBadRequest {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestDateConds(t *testing.T) {
	for in, want := range map[string][]string{
		"20240102":          {"eq2024-01-02"},
		"20240101-20240131": {"ge2024-01-01", "le2024-01-31"},
		"-20240131":         {"le2024-01-31"},
		"20240101-":         {"ge2024-01-01"},
	} {
		got, err := dateConds(in)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %v %v, muốn %v", in, got, err, want)
		}
	}
}
