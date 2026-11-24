package dicomread_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"trivita-fhir/pkg/dicomread"
	"trivita-fhir/pkg/dicomtest"
	"trivita-fhir/pkg/dicomweb"
)

func header(t *testing.T, m dicomtest.Meta) dicomweb.Dataset {
	t.Helper()
	b, _ := dicomtest.Header(m)
	ds, err := dicomread.ReadHeader(b)
	if err != nil {
		t.Fatal(err)
	}
	return ds
}

var base = dicomtest.Meta{StudyUID: dicomtest.UID("1"), SeriesUID: dicomtest.UID("1", "1"), SOPUID: dicomtest.UID("1", "1", "1")}

func TestReadHeaderMapsTheCommonVRs(t *testing.T) {
	for _, ts := range []string{dicomtest.ExplicitLE, dicomtest.ImplicitLE} {
		m := base
		m.TransferSyntax, m.StudyDate, m.StudyTime, m.InstanceNumber = ts, "20240102", "030405", "7"
		m.ReferringPhysician, m.PatientName = "Nguyen^Bac Si", "Tran^Van A=Ideo^Name"
		ds := header(t, m)
		if got := ds.Str(dicomweb.TagStudyInstanceUID); got != base.StudyUID {
			t.Errorf("%s: StudyInstanceUID %q", ts, got)
		}
		s := dicomweb.StudyFromDataset(ds)
		if s.StudyInstanceUid != base.StudyUID || s.StudyDate != "2024-01-02" || s.StudyTime != "03:04:05" || s.PatientName != "Tran^Van A" || s.ReferringPhysiciansName != "Nguyen^Bac Si" || s.PatientBirthDate != "1980-01-01" {
			t.Errorf("%s: %+v", ts, s)
		}
		se := dicomweb.SeriesFromDataset("", ds)
		if se.SeriesInstanceUid != base.SeriesUID || se.Modality != "CT" {
			t.Errorf("%s: %+v", ts, se)
		}
		// PN nhiều thành phần được tách theo "="
		if pn, _ := ds["00100010"].Value[0].(dicomweb.PersonName); pn["Alphabetic"] != "Tran^Van A" || pn["Ideographic"] != "Ideo^Name" {
			t.Errorf("%s: PN %v", ts, ds["00100010"].Value)
		}
		// số: US thành int, IS thành int
		if v := ds["00280010"].Value; len(v) != 1 || v[0] != 8 {
			t.Errorf("%s: Rows %v", ts, v)
		}
		if v := ds["00200013"].Value; len(v) != 1 || v[0] != 7 {
			t.Errorf("%s: InstanceNumber %v", ts, v)
		}
		if _, ok := ds["7FE00010"]; ok {
			t.Errorf("%s: không được có PixelData", ts)
		}
	}
}

func TestReadHeaderSequencesAndCleaning(t *testing.T) {
	m := base
	m.Extra = []dicomtest.Elem{
		dicomtest.Sequence(true, 8, 0x1110, true, dicomtest.Item(true, dicomtest.E(true, 8, 0x1150, "UI", []byte("1.2.3")), dicomtest.E(true, 8, 0x1155, "UI", []byte("4.5.6")))),
		dicomtest.E(true, 9, 0x1001, "OB", make([]byte, 20000)),
		dicomtest.E(true, 9, 0x1002, "OB", []byte{1, 2, 3, 4}),
	}
	ds := header(t, m)
	sq := ds["00081110"]
	if sq.VR != "SQ" || len(sq.Value) != 1 {
		t.Fatalf("SQ: %+v", sq)
	}
	if inner := sq.Value[0].(dicomweb.Dataset); inner["00081150"].Value[0] != "1.2.3" || inner["00081155"].Value[0] != "4.5.6" {
		t.Errorf("item: %+v", inner)
	}
	clean := dicomweb.CleanDataset(ds)
	if _, ok := clean["00020010"]; ok {
		t.Error("nhóm 0002 phải bị bỏ")
	}
	if _, ok := clean["00091001"]; ok {
		t.Error("OB 20 KB phải bị bỏ")
	}
	if clean["00091002"].InlineBinary != "AQIDBA==" {
		t.Errorf("OB nhỏ: %+v", clean["00091002"])
	}
	// kết quả marshal được thành JSON hợp lệ
	raw, err := json.Marshal(clean)
	if err != nil || !strings.Contains(string(raw), `"00081150":{"vr":"UI","Value":["1.2.3"]}`) {
		t.Errorf("JSON: %s %v", raw, err)
	}
}

func TestReadHeaderRejectsGarbage(t *testing.T) {
	good, _ := dicomtest.Header(base)
	for name, b := range map[string][]byte{"rỗng": nil, "rác": make([]byte, 400), "cắt giữa": good[:len(good)-5]} {
		if _, err := dicomread.ReadHeader(b); !errors.Is(err, dicomread.ErrUnreadable) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
