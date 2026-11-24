package dicomstow_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/textproto"
	"os"
	"strings"
	"testing"

	"trivita-fhir/pkg/binarystore"
	"trivita-fhir/pkg/dicomstow"
	"trivita-fhir/pkg/dicomtest"
	"trivita-fhir/pkg/dicomweb"
	"trivita-fhir/pkg/fhir"
)

type bulkPart struct {
	location, id, contentType string
	data                      []byte
}

// metaBody dựng thân STOW dạng metadata: phần đầu là DICOM JSON, các phần sau là bulkdata.
func metaBody(metaJSON string, bulks ...bulkPart) body {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	pw, _ := w.CreatePart(textproto.MIMEHeader{"Content-Type": {"application/dicom+json"}})
	pw.Write([]byte(metaJSON))
	for _, b := range bulks {
		h := textproto.MIMEHeader{}
		if b.contentType != "" {
			h.Set("Content-Type", b.contentType)
		}
		if b.location != "" {
			h.Set("Content-Location", b.location)
		}
		if b.id != "" {
			h.Set("Content-ID", b.id)
		}
		bw, _ := w.CreatePart(h)
		bw.Write(b.data)
	}
	w.Close()
	return body{`multipart/related; type="application/dicom+json"; boundary="` + w.Boundary() + `"`, &buf}
}

func imageJSON(study, series, sop, bulkURI string, extra string) string {
	s := fmt.Sprintf(`{
	 "00080016":{"vr":"UI","Value":["%s"]},"00080018":{"vr":"UI","Value":["%s"]},"00080060":{"vr":"CS","Value":["CT"]},
	 "00080020":{"vr":"DA","Value":["20240102"]},"00100010":{"vr":"PN","Value":[{"Alphabetic":"Tran^Van B"}]},"00100020":{"vr":"LO","Value":["PID-77"]},
	 "0020000D":{"vr":"UI","Value":["%s"]},"0020000E":{"vr":"UI","Value":["%s"]},"00200013":{"vr":"IS","Value":[3]},
	 "00280002":{"vr":"US","Value":[1]},"00280004":{"vr":"CS","Value":["MONOCHROME2"]},"00280008":{"vr":"IS","Value":["2"]},
	 "00280010":{"vr":"US","Value":[4]},"00280011":{"vr":"US","Value":[4]},"00280100":{"vr":"US","Value":[16]},"00280101":{"vr":"US","Value":[16]},"00280102":{"vr":"US","Value":[15]},"00280103":{"vr":"US","Value":[0]},
	 "7FE00010":{"vr":"OW","BulkDataURI":"%s"}%s}`, dicomtest.CTImage, dicomtest.UID(study, series, sop), dicomtest.UID(study), dicomtest.UID(study, series), bulkURI, extra)
	return s
}

func (e *env) stored(inst *fhir.DicomInstance) []byte {
	e.t.Helper()
	bin := e.one("Binary", strings.TrimPrefix(inst.Raw.Reference, "Binary/")).(*fhir.Binary)
	key, _ := binarystore.Key(bin.ID, bin.Meta.VersionId)
	rc, err := e.blobs.Open(context.Background(), key)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rc.Close()
	b, _ := io.ReadAll(rc)
	return b
}

func TestMetadataStoreNativePixelData(t *testing.T) {
	e := newEnv(t)
	e.cfg.TempDir = t.TempDir()
	px := make([]byte, 4*4*2*2) // 2 frame
	for i := range px {
		px[i] = byte(i*3 + 1)
	}
	out := e.must(metaBody("["+imageJSON("1", "1", "1", "cid:px1", "")+"]", bulkPart{id: "<px1>", contentType: "application/octet-stream", data: px}))
	if out.Status != 200 || len(out.Stored) != 1 || len(out.Failed) != 0 {
		t.Fatalf("%+v", out)
	}
	st := out.Stored[0]
	inst := e.one("DicomInstance", st.InstanceID).(*fhir.DicomInstance)
	if inst.NumberOfFrames == nil || *inst.NumberOfFrames != 2 || *inst.Rows != 4 || inst.SopInstanceUid != dicomtest.UID("1", "1", "1") {
		t.Errorf("instance: %+v", inst)
	}
	study := e.one("DicomStudy", st.StudyID).(*fhir.DicomStudy)
	if study.PatientName != "Tran^Van B" || study.PatientId != "PID-77" || study.StudyDate != "2024-01-02" {
		t.Errorf("study: %+v", study)
	}
	// tệp lưu là Part 10 hợp lệ: quét ra hai frame đúng byte đã gửi
	file := e.stored(inst)
	scan, err := dicomweb.Scan(bytes.NewReader(file), dicomweb.ScanOptions{})
	if err != nil || !scan.HasPixelData || scan.TransferSyntax != "1.2.840.10008.1.2.1" || len(scan.Frames) != 2 || scan.Encapsulated {
		t.Fatalf("quét: %+v %v", scan, err)
	}
	for i, fr := range scan.Frames {
		if got := file[fr.Offset : fr.Offset+fr.Length]; !bytes.Equal(got, px[i*32:(i+1)*32]) {
			t.Errorf("frame %d sai nội dung", i+1)
		}
	}
	// chỉ mục frame lập lúc lưu có đủ hai frame
	idx := ""
	for _, x := range inst.Extension {
		if x.URL == dicomweb.FrameIndexURL {
			idx = x.ValueString
		}
	}
	if fi, err := dicomweb.ParseFrameIndex(idx); err != nil || len(fi.Frames) != 2 {
		t.Errorf("chỉ mục frame: %v %q", err, idx)
	}
	// tải lại là idempotent
	again := e.must(metaBody("["+imageJSON("1", "1", "1", "cid:px1", "")+"]", bulkPart{id: "<px1>", contentType: "application/octet-stream", data: px}))
	if again.Status != 200 || !again.Stored[0].AlreadyStored || e.blobs.count() != 1 {
		t.Errorf("idempotent: %+v blobs=%d", again, e.blobs.count())
	}
	if entries, _ := os.ReadDir(e.cfg.TempDir); len(entries) != 0 {
		t.Errorf("còn %d tệp tạm", len(entries))
	}
}

func TestMetadataStoreCompressedAndOddLength(t *testing.T) {
	e := newEnv(t)
	jpeg := append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, bytes.Repeat([]byte{7}, 101)...) // 105 byte, lẻ
	meta := strings.Replace(imageJSON("2", "1", "1", "http://example.org/bulk/pixel", ""), `"00280008":{"vr":"IS","Value":["2"]},`, "", 1)
	meta = strings.Replace(meta, `"vr":"OW"`, `"vr":"OB"`, 1)
	out := e.must(metaBody(meta, bulkPart{location: "http://example.org/bulk/pixel", contentType: "image/jpeg", data: jpeg}))
	if out.Status != 200 || len(out.Stored) != 1 {
		t.Fatalf("%+v", out)
	}
	file := e.stored(e.one("DicomInstance", out.Stored[0].InstanceID).(*fhir.DicomInstance))
	scan, err := dicomweb.Scan(bytes.NewReader(file), dicomweb.ScanOptions{})
	if err != nil || scan.TransferSyntax != "1.2.840.10008.1.2.4.50" || !scan.Encapsulated || len(scan.Frames) != 1 {
		t.Fatalf("quét: %+v %v", scan, err)
	}
	fr := scan.Frames[0]
	if fr.Size() != 106 || !bytes.Equal(file[fr.Offset:fr.Offset+int64(len(jpeg))], jpeg) {
		t.Errorf("frame đóng gói: %+v (muốn 106 byte, bắt đầu bằng JPEG gửi lên)", fr)
	}
	// tham số transfer-syntax tường minh thắng media type
	meta2 := strings.Replace(imageJSON("2", "1", "2", "cid:a", ""), `"00280008":{"vr":"IS","Value":["2"]},`, "", 1)
	out = e.must(metaBody(meta2, bulkPart{id: "<a>", contentType: "image/jp2; transfer-syntax=1.2.840.10008.1.2.4.91", data: []byte("JP2DATA!")}))
	if out.Status != 200 {
		t.Fatalf("%+v", out)
	}
	scan, _ = dicomweb.Scan(bytes.NewReader(e.stored(e.one("DicomInstance", out.Stored[0].InstanceID).(*fhir.DicomInstance))), dicomweb.ScanOptions{})
	if scan.TransferSyntax != "1.2.840.10008.1.2.4.91" {
		t.Errorf("transfer syntax: %s", scan.TransferSyntax)
	}
}

// Nhiều loại VR, SQ, nhị phân inline và ký tự ngoài ASCII đi một vòng: dựng DICOM rồi đọc lại bằng bộ đọc của STOW thấy đúng giá trị.
func TestMetadataStoreVariousVRs(t *testing.T) {
	e := newEnv(t)
	extra := `,"00081140":{"vr":"SQ","Value":[{"00081150":{"vr":"UI","Value":["1.2.3"]},"00081155":{"vr":"UI","Value":["1.2.3.4"]}}]},
	 "00181063":{"vr":"DS","Value":[1.5,"2.25"]},"00200032":{"vr":"DS","Value":[-10.5,0,3]},"00280030":{"vr":"DS","Value":[0.7,0.7]},
	 "00091001":{"vr":"LO","Value":["private"]},"00540010":{"vr":"US","Value":[1,2,3]},"00209165":{"vr":"AT","Value":["00100020"]},
	 "00181030":{"vr":"LO","Value":["Chụp phổi"]},"00081030":{"vr":"LO","Value":["Phổi thẳng"]},"00420011":{"vr":"OB","InlineBinary":"AQID"}`
	out := e.must(metaBody("["+imageJSON("3", "1", "1", "cid:p", extra)+"]", bulkPart{id: "p", contentType: "application/octet-stream", data: make([]byte, 64)}))
	if out.Status != 200 || len(out.Stored) != 1 {
		t.Fatalf("%+v %+v", out.Stored, out.Failed)
	}
	inst := e.one("DicomInstance", out.Stored[0].InstanceID).(*fhir.DicomInstance)
	for _, want := range []string{`"1.2.3.4"`, `"00081155"`, "Chụp phổi", `"InlineBinary":"AQIDAA=="`, `1.5`, `"00200032"`} {
		if !strings.Contains(inst.Metadata, want) {
			t.Errorf("metadata thiếu %q:\n%.600s", want, inst.Metadata)
		}
	}
	// ISO_IR 192 được tự thêm khi có ký tự ngoài ASCII
	if !strings.Contains(inst.Metadata, `"00080005"`) || !strings.Contains(inst.Metadata, "ISO_IR 192") {
		t.Errorf("thiếu Specific Character Set: %.400s", inst.Metadata)
	}
}

func TestMetadataStoreFailures(t *testing.T) {
	e := newEnv(t)
	good := imageJSON("4", "1", "1", "cid:ok", "")
	px := bulkPart{id: "ok", contentType: "application/octet-stream", data: make([]byte, 64)}
	noUID := strings.Replace(imageJSON("4", "1", "2", "cid:ok", ""), `"00080018":{"vr":"UI","Value":["`+dicomtest.UID("4", "1", "2")+`"]},`, "", 1)
	// một instance hỏng không làm hỏng instance khác
	out := e.must(metaBody("["+good+","+imageJSON("4", "1", "3", "cid:thieu", "")+","+noUID+"]", px))
	if out.Status != 202 || len(out.Stored) != 1 || len(out.Failed) != 2 {
		t.Fatalf("%d stored %d failed status %d", len(out.Stored), len(out.Failed), out.Status)
	}
	for _, f := range out.Failed {
		if f.Reason != dicomstow.ReasonCannotUnderstand {
			t.Errorf("lý do: %x %s", f.Reason, f.Message)
		}
	}
	if e.count("DicomInstance", nil) != 1 || e.blobs.count() != 1 {
		t.Errorf("không để lại gì cho instance hỏng: %d resource, %d nội dung", e.count("DicomInstance", nil), e.blobs.count())
	}
	// không instance nào thành công: 409
	if out := e.must(metaBody("[" + imageJSON("4", "1", "9", "cid:none", "") + "]")); out.Status != 409 || len(out.Failed) != 1 {
		t.Errorf("409: %+v", out)
	}
	// sai study của yêu cầu: 0xA900
	other, err := e.store(ctxA, metaBody("["+imageJSON("5", "1", "1", "cid:ok", "")+"]", px), dicomtest.UID("4"))
	if err != nil || other.Status != 409 || other.Failed[0].Reason != dicomstow.ReasonDataSetMismatch {
		t.Errorf("sai study: %+v %v", other, err)
	}
	// VR sai kiểu, giá trị ngoài miền
	for name, extra := range map[string]string{
		"US âm":                 `,"00540010":{"vr":"US","Value":[-1]}`,
		"US chuỗi":              `,"00540010":{"vr":"US","Value":["a"]}`,
		"VR lạ":                 `,"00540010":{"vr":"XX","Value":[1]}`,
		"base64 hỏng":           `,"00880200":{"vr":"OB","InlineBinary":"***"}`,
		"tag không hex":         `,"ZZZZ0010":{"vr":"US","Value":[1]}`,
		"IS không nguyên":       `,"00200013":{"vr":"IS","Value":[1.5]}`,
		"UI sai kiểu":           `,"00081140":{"vr":"UI","Value":[5]}`,
		"BulkDataURI lạ":        `,"00880200":{"vr":"OB","BulkDataURI":"cid:khong-co"}`,
		"phần tử sau PixelData": `,"7FE10010":{"vr":"LO","Value":["x"]}`,
		"PixelData trong SQ":    `,"00081140":{"vr":"SQ","Value":[{"7FE00010":{"vr":"OW","InlineBinary":"AAAA"}}]}`,
	} {
		o := e.must(metaBody("["+strings.Replace(imageJSON("6", "1", "1", "cid:ok", extra), `"00200013":{"vr":"IS","Value":[3]},`, ifIS(name), 1)+"]", px))
		if o.Status != 409 || len(o.Failed) != 1 || o.Failed[0].Reason != dicomstow.ReasonCannotUnderstand {
			t.Errorf("%s: %d %+v", name, o.Status, o.Failed)
		}
	}
	// bulkdata ngắn hơn số frame khai báo: từ chối, không để lại gì
	if o := e.must(metaBody("["+imageJSON("6", "2", "1", "cid:short", "")+"]", bulkPart{id: "short", contentType: "application/octet-stream", data: make([]byte, 10)})); o.Status != 409 {
		t.Errorf("bulkdata ngắn: %d %+v", o.Status, o.Failed)
	}
	if e.count("DicomInstance", nil) != 1 || e.blobs.count() != 1 {
		t.Errorf("instance hỏng để lại resource: %d", e.count("DicomInstance", nil))
	}
}

func ifIS(name string) string {
	if name == "IS không nguyên" {
		return ""
	}
	return `"00200013":{"vr":"IS","Value":[3]},`
}

func TestMetadataStoreRequestErrors(t *testing.T) {
	e := newEnv(t)
	px := bulkPart{id: "ok", contentType: "application/octet-stream", data: make([]byte, 64)}
	for name, c := range map[string]struct {
		b    body
		want int
	}{
		"JSON hỏng":             {metaBody(`[{"00080016":`), 400},
		"mảng rỗng":             {metaBody(`[]`), 400},
		"không phải dataset":    {metaBody(`"x"`), 400},
		"bulkdata thiếu vị trí": {metaBody("["+imageJSON("7", "1", "1", "cid:ok", "")+"]", bulkPart{contentType: "application/octet-stream", data: []byte{1, 2}}), 400},
		"metadata quá lớn":      {metaBody(`[{"00081030":{"vr":"LO","Value":["` + strings.Repeat("a", 9<<20) + `"]}}]`), 413},
	} {
		_, err := e.store(ctxA, c.b, "")
		var re *dicomstow.RequestError
		if !errors.As(err, &re) || re.Status != c.want {
			t.Errorf("%s: %v, muốn %d", name, err, c.want)
		}
	}
	_ = px
	// bulkdata vượt trần instance: instance đó thất bại 0xA700, instance khác vẫn lưu
	e.cfg.MaxInstanceBytes = 4096
	small := bulkPart{id: "small", contentType: "application/octet-stream", data: make([]byte, 64)}
	big := bulkPart{id: "big", contentType: "application/octet-stream", data: make([]byte, 8192)}
	out := e.must(metaBody("["+imageJSON("8", "1", "1", "cid:big", "")+","+imageJSON("8", "1", "2", "cid:small", "")+"]", big, small))
	if out.Status != 202 || len(out.Stored) != 1 || len(out.Failed) != 1 || out.Failed[0].Reason != dicomstow.ReasonOutOfResources {
		t.Errorf("vượt trần: %d %+v %+v", out.Status, out.Stored, out.Failed)
	}
}

// STOW tệp thường vẫn hoạt động y nguyên sau khi thêm dạng metadata (phần đầu application/dicom).
func TestMetadataModeDoesNotAffectPlainStow(t *testing.T) {
	e := newEnv(t)
	if out := e.must(multi(file("9", "1", "1"))); out.Status != 200 || len(out.Stored) != 1 {
		t.Fatalf("%+v", out)
	}
}
