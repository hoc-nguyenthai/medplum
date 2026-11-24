package bootstrap_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"

	"trivita-fhir/pkg/dicomtest"
)

type dicomObj map[string]struct{ Value []any }

func uidsOf(t *testing.T, raw []byte, tag string) []string {
	t.Helper()
	var arr []dicomObj
	if err := json.Unmarshal(raw, &arr); err != nil {
		t.Fatalf("không phải mảng DICOM JSON: %v: %s", err, raw)
	}
	var out []string
	for _, o := range arr {
		if v := o[tag].Value; len(v) > 0 {
			out = append(out, v[0].(string))
		}
	}
	sort.Strings(out)
	return out
}

func q(path string, kv ...string) string {
	v := url.Values{}
	for i := 0; i < len(kv); i += 2 {
		v.Add(kv[i], kv[i+1])
	}
	return path + "?" + v.Encode()
}

// D7: bộ lọc QIDO ở ba cấp, QIDO cấp instance, tải instance, series, study byte-exact, metadata study và instance.
func TestD7FiltersInstancesAndDownloads(t *testing.T) {
	_, h := handler(t, settings(t, true))
	srv := httptest.NewServer(h)
	defer srv.Close()
	wr, rd := tok(t, "A", "fhir.write"), tok(t, "A", "fhir.read")

	files := map[string][]byte{
		"111": inst("1", "1", "1", func(m *dicomtest.Meta) {
			m.PatientID, m.PatientName, m.StudyDate, m.InstanceNumber = "P1", "Nguyen^An", "20240102", "1"
		}),
		"112": inst("1", "1", "2", func(m *dicomtest.Meta) {
			m.PatientID, m.PatientName, m.StudyDate, m.InstanceNumber = "P1", "Nguyen^An", "20240102", "2"
		}),
		"121": inst("1", "2", "1", func(m *dicomtest.Meta) {
			m.PatientID, m.PatientName, m.StudyDate, m.Modality = "P1", "Nguyen^An", "20240102", "MR"
		}),
		"211": inst("2", "1", "1", func(m *dicomtest.Meta) { m.PatientID, m.PatientName, m.StudyDate = "P2", "Le^Binh", "20231215" }),
	}
	order := []string{"111", "112", "121", "211"}
	var readers []io.Reader
	for _, k := range order {
		readers = append(readers, bytes.NewReader(files[k]))
	}
	ct, body := stowBody(readers...)
	if code, _, raw := send(t, srv, "/dicomweb/studies", wr, ct, body); code != 200 {
		t.Fatalf("STOW: %d %s", code, raw)
	}
	s1, s2 := dicomtest.UID("1"), dicomtest.UID("2")
	const studyUID, seriesUID, sopUID = "0020000D", "0020000E", "00080018"

	// bộ lọc cấp study
	studyCases := []struct {
		name   string
		kv     []string
		expect []string
	}{
		{"PatientID", []string{"PatientID", "P1"}, []string{s1}},
		{"tag hex", []string{"00100020", "P2"}, []string{s2}},
		{"PatientName tiền tố không phân biệt hoa thường", []string{"PatientName", "nguy*"}, []string{s1}},
		{"StudyDate chính xác", []string{"StudyDate", "20231215"}, []string{s2}},
		{"StudyDate khoảng", []string{"StudyDate", "20240101-20240131"}, []string{s1}},
		{"StudyDate mở đầu", []string{"StudyDate", "-20231231"}, []string{s2}},
		{"StudyDate mở cuối", []string{"StudyDate", "20240101-"}, []string{s1}},
		{"StudyInstanceUID", []string{"StudyInstanceUID", s2}, []string{s2}},
		{"OR bằng dấu \\", []string{"PatientID", `P1\P2`}, []string{s1, s2}},
		{"không khớp", []string{"PatientID", "KHONG"}, nil},
		{"hai điều kiện (AND)", []string{"PatientID", "P1", "StudyDate", "20231215"}, nil},
		{"lọc theo Modality ở cấp study qua ModalitiesInStudy", []string{"ModalitiesInStudy", "MR"}, []string{s1}},
	}
	for _, c := range studyCases {
		code, _, raw := get(t, srv, q("/dicomweb/studies", c.kv...), rd)
		got := uidsOf(t, raw, studyUID)
		want := append([]string(nil), c.expect...)
		sort.Strings(want)
		if code != 200 || strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("study/%s: %d %v, muốn %v", c.name, code, got, want)
		}
	}

	// series: lọc cấp series, lọc kèm cấp study (hai cấp), cả project và theo study
	code, _, raw := get(t, srv, q("/dicomweb/series", "Modality", "MR"), rd)
	if got := uidsOf(t, raw, seriesUID); code != 200 || len(got) != 1 || got[0] != dicomtest.UID("1", "2") {
		t.Errorf("series Modality=MR: %d %v", code, got)
	}
	code, _, raw = get(t, srv, q("/dicomweb/series", "PatientID", "P1"), rd)
	if got := uidsOf(t, raw, seriesUID); code != 200 || len(got) != 2 {
		t.Errorf("series theo PatientID (hai cấp): %d %v", code, got)
	}
	code, _, raw = get(t, srv, q("/dicomweb/series", "PatientID", "P1", "Modality", "CT"), rd)
	if got := uidsOf(t, raw, seriesUID); code != 200 || len(got) != 1 || got[0] != dicomtest.UID("1", "1") {
		t.Errorf("series PatientID + Modality: %d %v", code, got)
	}
	code, _, raw = get(t, srv, q("/dicomweb/studies/"+s1+"/series", "Modality", "MR"), rd)
	if got := uidsOf(t, raw, seriesUID); code != 200 || len(got) != 1 {
		t.Errorf("series của study theo Modality: %d %v", code, got)
	}

	// instance: cả project, theo study, theo series, lọc
	for path, want := range map[string]int{
		"/dicomweb/instances":                    4,
		"/dicomweb/studies/" + s1 + "/instances": 3,
		"/dicomweb/studies/" + s1 + "/series/" + dicomtest.UID("1", "1") + "/instances": 2,
		"/dicomweb/studies/" + s2 + "/instances":                                        1,
	} {
		code, hdr, raw := get(t, srv, path, rd)
		if got := uidsOf(t, raw, sopUID); code != 200 || len(got) != want || hdr.Get("Content-Type") != "application/dicom+json" {
			t.Errorf("%s: %d %d instance, muốn %d", path, code, len(got), want)
		}
	}
	code, _, raw = get(t, srv, q("/dicomweb/instances", "SOPInstanceUID", dicomtest.UID("1", "1", "2")), rd)
	if got := uidsOf(t, raw, sopUID); code != 200 || len(got) != 1 || got[0] != dicomtest.UID("1", "1", "2") {
		t.Errorf("instance theo SOPInstanceUID: %d %v", code, got)
	}
	code, _, raw = get(t, srv, q("/dicomweb/instances", "PatientID", "P2", "Modality", "CT"), rd)
	if got := uidsOf(t, raw, sopUID); code != 200 || len(got) != 1 {
		t.Errorf("instance lọc ba cấp: %d %v", code, got)
	}
	// phân trang instance: 4 instance, limit 3 -> Warning, offset 3 -> 1 phần tử
	code, hdr, raw := get(t, srv, "/dicomweb/instances?limit=3", rd)
	if got := uidsOf(t, raw, sopUID); code != 200 || len(got) != 3 || !strings.HasPrefix(hdr.Get("Warning"), "299 ") {
		t.Errorf("limit=3: %d %d %q", code, len(got), hdr.Get("Warning"))
	}
	if code, _, raw := get(t, srv, "/dicomweb/instances?limit=3&offset=3", rd); len(uidsOf(t, raw, sopUID)) != 1 || code != 200 {
		t.Errorf("offset=3: %d %s", code, raw)
	}
	// tham số không hỗ trợ: bỏ qua nhưng báo; giá trị sai: 400
	if code, hdr, raw := get(t, srv, "/dicomweb/studies?Foo=bar", rd); code != 200 || !strings.Contains(hdr.Get("Warning"), "Foo") || len(uidsOf(t, raw, studyUID)) != 2 {
		t.Errorf("tham số lạ: %d %q", code, hdr.Get("Warning"))
	}
	for name, p := range map[string]string{
		"ngày hỏng":             q("/dicomweb/studies", "StudyDate", "2024-01-02"),
		"ký tự đại diện ở giữa": q("/dicomweb/studies", "PatientName", "ng*en"),
		"nhiều khoảng ngày":     q("/dicomweb/studies", "StudyDate", `20240101-20240102\20240201-`),
	} {
		if code, _, _ := get(t, srv, p, rd); code != 400 {
			t.Errorf("%s: %d, muốn 400", name, code)
		}
	}
	// study không có ở đường dẫn: 404, không phải [] rỗng
	if code, _, _ := get(t, srv, "/dicomweb/studies/"+dicomtest.UID("9")+"/instances", rd); code != 404 {
		t.Errorf("study không có: %d", code)
	}

	// tải instance byte-exact
	i111 := "/dicomweb/studies/" + s1 + "/series/" + dicomtest.UID("1", "1") + "/instances/" + dicomtest.UID("1", "1", "1")
	code, hdr, raw = get(t, srv, i111, rd)
	typ, parts, types := multipartParts(t, hdr, raw)
	if code != 200 || typ != "application/dicom" || len(parts) != 1 || !bytes.Equal(parts[0], files["111"]) || types[0] != "application/dicom" {
		t.Fatalf("tải instance: %d type=%q %d phần", code, typ, len(parts))
	}
	// series: hai instance; study: ba instance; đủ và khớp byte
	code, hdr, raw = get(t, srv, "/dicomweb/studies/"+s1+"/series/"+dicomtest.UID("1", "1"), rd)
	_, parts, _ = multipartParts(t, hdr, raw)
	if code != 200 || len(parts) != 2 || !hasPart(parts, files["111"]) || !hasPart(parts, files["112"]) {
		t.Errorf("tải series: %d %d phần", code, len(parts))
	}
	code, hdr, raw = get(t, srv, "/dicomweb/studies/"+s1, rd)
	_, parts, _ = multipartParts(t, hdr, raw)
	if code != 200 || len(parts) != 3 || !hasPart(parts, files["111"]) || !hasPart(parts, files["112"]) || !hasPart(parts, files["121"]) || hasPart(parts, files["211"]) {
		t.Errorf("tải study: %d %d phần", code, len(parts))
	}
	// lỗi tải
	for name, p := range map[string]string{
		"study không có":        "/dicomweb/studies/" + dicomtest.UID("9"),
		"series không có":       "/dicomweb/studies/" + s1 + "/series/" + dicomtest.UID("1", "9"),
		"instance không có":     "/dicomweb/studies/" + s1 + "/series/" + dicomtest.UID("1", "1") + "/instances/" + dicomtest.UID("1", "1", "9"),
		"series của study khác": "/dicomweb/studies/" + s2 + "/series/" + dicomtest.UID("1", "1"),
	} {
		if code, _, _ := get(t, srv, p, rd); code != 404 {
			t.Errorf("%s: %d", name, code)
		}
	}
	// token khác project không thấy gì
	other := tok(t, "B", "fhir.read")
	if code, _, raw := get(t, srv, "/dicomweb/studies", other); code != 200 || len(uidsOf(t, raw, studyUID)) != 0 {
		t.Errorf("project khác thấy study: %d %s", code, raw)
	}
	if code, _, _ := get(t, srv, "/dicomweb/studies/"+s1, other); code != 404 {
		t.Errorf("project khác tải được study: %d", code)
	}

	// metadata study và instance
	code, _, raw = get(t, srv, "/dicomweb/studies/"+s1+"/metadata", rd)
	if got := uidsOf(t, raw, sopUID); code != 200 || len(got) != 3 {
		t.Errorf("metadata study: %d %v", code, got)
	}
	code, _, raw = get(t, srv, i111+"/metadata", rd)
	if got := uidsOf(t, raw, sopUID); code != 200 || len(got) != 1 || got[0] != dicomtest.UID("1", "1", "1") {
		t.Errorf("metadata instance: %d %v", code, got)
	}
	if bytes.Contains(raw, []byte("7FE00010")) {
		t.Error("metadata instance có PixelData")
	}
	if code, _, _ := get(t, srv, "/dicomweb/studies/"+dicomtest.UID("9")+"/metadata", rd); code != 404 {
		t.Errorf("metadata study không có: %d", code)
	}
}

func hasPart(parts [][]byte, want []byte) bool {
	for _, p := range parts {
		if bytes.Equal(p, want) {
			return true
		}
	}
	return false
}
