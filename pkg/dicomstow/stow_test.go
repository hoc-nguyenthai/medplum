package dicomstow_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"strings"
	"sync"
	"testing"

	"trivita-fhir/pkg/binarystore"
	"trivita-fhir/pkg/dicomstow"
	"trivita-fhir/pkg/dicomtest"
	"trivita-fhir/pkg/dicomweb"
	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirauth"
	"trivita-fhir/pkg/fhirrouter"
	"trivita-fhir/pkg/fhirsearch"
)

// trackStore là kho nội dung trong bộ nhớ biết còn những khóa nào, để kiểm không có nội dung mồ côi.
type trackStore struct {
	binarystore.Store
	mu   sync.Mutex
	keys map[string]bool
	fail error // khác nil: Put lỗi sau khi đọc một ít
	puts int   // số lần Put được gọi
}

func newTrackStore() *trackStore {
	return &trackStore{Store: binarystore.NewMemory(), keys: map[string]bool{}}
}

func (t *trackStore) Put(ctx context.Context, key string, r io.Reader, o binarystore.PutOptions) (int64, error) {
	t.mu.Lock()
	t.puts++
	t.mu.Unlock()
	if t.fail != nil {
		_, _ = io.CopyN(io.Discard, r, 10)
		return 0, t.fail
	}
	n, err := t.Store.Put(ctx, key, r, o)
	if err == nil {
		t.mu.Lock()
		t.keys[key] = true
		t.mu.Unlock()
	}
	return n, err
}

func (t *trackStore) Delete(ctx context.Context, key string) error {
	t.mu.Lock()
	delete(t.keys, key)
	t.mu.Unlock()
	return t.Store.Delete(ctx, key)
}

func (t *trackStore) count() int { t.mu.Lock(); defer t.mu.Unlock(); return len(t.keys) }

type env struct {
	t     *testing.T
	repo  fhirrouter.Repository // kho gốc, không tách project (để kiểm đếm)
	scope fhirrouter.Repository
	blobs *trackStore
	cfg   dicomstow.Config
}

func principal(project string, role fhirauth.Role) context.Context {
	return fhirauth.WithPrincipal(context.Background(), &fhirauth.Principal{Subject: "u", Project: project, Role: role})
}

func newEnv(t *testing.T) *env {
	t.Helper()
	repo := fhirrouter.NewMemoryRepository()
	blobs := newTrackStore()
	scope := fhirauth.Scope(repo)
	return &env{t: t, repo: repo, scope: scope, blobs: blobs, cfg: dicomstow.Config{Repo: scope, Blobs: blobs, MaxHeaderBytes: 1 << 20}}
}

var ctxA = principal("A", fhirauth.RoleWrite)

type body struct {
	ct string
	r  io.Reader
}

// multi dựng thân multipart/related; mỗi phần là byte của một tệp (ct rỗng nghĩa là application/dicom).
func multi(parts ...[]byte) body {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for _, p := range parts {
		h := textproto.MIMEHeader{"Content-Type": {"application/dicom"}}
		pw, _ := w.CreatePart(h)
		pw.Write(p)
	}
	w.Close()
	return body{`multipart/related; type="application/dicom"; boundary="` + w.Boundary() + `"`, &buf}
}

func (e *env) store(ctx context.Context, b body, studyUID string) (*dicomstow.Outcome, error) {
	return dicomstow.Store(ctx, e.cfg, dicomstow.Request{ContentType: b.ct, Body: b.r, StudyUID: studyUID, RetrieveBase: "https://dicom.test/dicomweb"})
}

func (e *env) must(b body) *dicomstow.Outcome {
	e.t.Helper()
	out, err := e.store(ctxA, b, "")
	if err != nil {
		e.t.Fatal(err)
	}
	return out
}

func (e *env) count(rt string, q url.Values) int {
	e.t.Helper()
	req, err := fhirsearch.Parse(rt, q)
	if err != nil {
		e.t.Fatal(err)
	}
	req.Total, req.Count = "accurate", 1
	res, err := e.scope.Search(ctxA, req)
	if err != nil {
		e.t.Fatal(err)
	}
	return *res.Total
}

func (e *env) one(rt, id string) fhir.Resource {
	e.t.Helper()
	r, err := e.scope.ReadResource(ctxA, rt, id)
	if err != nil {
		e.t.Fatalf("%s/%s: %v", rt, id, err)
	}
	return r
}

func file(study, series, sop string, mut ...func(*dicomtest.Meta)) []byte {
	m := dicomtest.Meta{StudyUID: dicomtest.UID(study), SeriesUID: dicomtest.UID(study, series), SOPUID: dicomtest.UID(study, series, sop), StudyDate: "20240102", StudyTime: "030405", ReferringPhysician: "Nguyen^Bac Si"}
	for _, f := range mut {
		f(&m)
	}
	return dicomtest.Build(m)
}

func TestStoreOneInstance(t *testing.T) {
	e := newEnv(t)
	data := file("1", "1", "1", func(m *dicomtest.Meta) { m.InstanceNumber = "5"; m.Rows, m.Cols = 32, 32 })
	out := e.must(multi(data))
	if out.Status != 200 || len(out.Stored) != 1 || len(out.Failed) != 0 {
		t.Fatalf("%+v", out)
	}
	st := out.Stored[0]
	study := e.one("DicomStudy", st.StudyID).(*fhir.DicomStudy)
	if study.StudyInstanceUid != dicomtest.UID("1") || study.PatientName != "Tran^Van A" || study.StudyDate != "2024-01-02" || study.StudyTime != "03:04:05" ||
		study.ReferringPhysiciansName != "Nguyen^Bac Si" || study.PatientBirthDate != "1980-01-01" {
		t.Errorf("study: %+v", study)
	}
	if study.NumberOfStudyRelatedSeries == nil || *study.NumberOfStudyRelatedSeries != 1 || *study.NumberOfStudyRelatedInstances != 1 || len(study.ModalitiesInStudy) != 1 || study.ModalitiesInStudy[0] != "CT" {
		t.Errorf("số liệu study: %+v", study)
	}
	series := e.one("DicomSeries", st.SeriesID).(*fhir.DicomSeries)
	if series.SeriesInstanceUid != dicomtest.UID("1", "1") || series.Modality != "CT" || series.Study.Reference != "DicomStudy/"+st.StudyID || *series.NumberOfSeriesRelatedInstances != 1 {
		t.Errorf("series: %+v", series)
	}
	inst := e.one("DicomInstance", st.InstanceID).(*fhir.DicomInstance)
	if inst.SopInstanceUid != dicomtest.UID("1", "1", "1") || inst.SopClassUid != dicomtest.CTImage || inst.InstanceNumber != "5" || *inst.Rows != 32 || *inst.Columns != 32 || *inst.BitsAllocated != 16 ||
		inst.Study.Reference != "DicomStudy/"+st.StudyID || inst.Series.Reference != "DicomSeries/"+st.SeriesID {
		t.Errorf("instance: %+v", inst)
	}
	if inst.NumberOfFrames != nil {
		t.Errorf("một frame không có NumberOfFrames: %d", *inst.NumberOfFrames)
	}
	// metadata đã làm sạch: không có nhóm 0002 và PixelData, có tên bệnh nhân
	if strings.Contains(inst.Metadata, "7FE00010") || strings.Contains(inst.Metadata, `"00020010"`) || !strings.Contains(inst.Metadata, `"Alphabetic":"Tran^Van A"`) {
		t.Errorf("metadata: %.300s", inst.Metadata)
	}
	// Binary: loại, ngữ cảnh bảo mật, nội dung đúng từng byte
	bin := e.one("Binary", strings.TrimPrefix(inst.Raw.Reference, "Binary/")).(*fhir.Binary)
	if bin.ContentType != "application/dicom" || bin.SecurityContext == nil || bin.SecurityContext.Reference != "DicomInstance/"+st.InstanceID || bin.Data != "" {
		t.Errorf("binary: %+v", bin)
	}
	key, _ := binarystore.Key(bin.ID, bin.Meta.VersionId)
	rc, err := e.blobs.Open(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(rc)
	rc.Close()
	if !bytes.Equal(got, data) {
		t.Errorf("nội dung lưu %d byte, gửi %d byte", len(got), len(data))
	}
	// phản hồi chuẩn
	sq := out.Response["00081199"]
	if sq.VR != "SQ" || len(sq.Value) != 1 {
		t.Fatalf("ReferencedSOPSequence: %+v", out.Response)
	}
	item := sq.Value[0].(dicomweb.Dataset)
	if item["00081155"].Value[0] != dicomtest.UID("1", "1", "1") || item["00081150"].Value[0] != dicomtest.CTImage ||
		item["00081190"].Value[0] != "https://dicom.test/dicomweb/studies/"+dicomtest.UID("1")+"/series/"+dicomtest.UID("1", "1")+"/instances/"+dicomtest.UID("1", "1", "1") {
		t.Errorf("item: %+v", item)
	}
	if out.Response["00081190"].Value[0] != "https://dicom.test/dicomweb/studies/"+dicomtest.UID("1") {
		t.Errorf("RetrieveURL cấp study: %+v", out.Response["00081190"])
	}
	if _, has := out.Response["00081198"]; has {
		t.Error("không có FailedSOPSequence khi mọi instance thành công")
	}
}

func TestStoreManyInstancesAggregates(t *testing.T) {
	e := newEnv(t)
	out := e.must(multi(
		file("1", "1", "1"), file("1", "1", "2"), file("1", "1", "3"),
		file("1", "2", "1", func(m *dicomtest.Meta) { m.Modality = "mr" }),
	))
	if out.Status != 200 || len(out.Stored) != 4 {
		t.Fatalf("%+v", out)
	}
	if n := e.count("DicomStudy", nil); n != 1 {
		t.Errorf("study: %d", n)
	}
	if n := e.count("DicomSeries", nil); n != 2 {
		t.Errorf("series: %d", n)
	}
	study := e.one("DicomStudy", out.Stored[0].StudyID).(*fhir.DicomStudy)
	if *study.NumberOfStudyRelatedSeries != 2 || *study.NumberOfStudyRelatedInstances != 4 || strings.Join(study.ModalitiesInStudy, ",") != "CT,MR" {
		t.Errorf("study: %v %v %v", *study.NumberOfStudyRelatedSeries, *study.NumberOfStudyRelatedInstances, study.ModalitiesInStudy)
	}
	s1 := e.one("DicomSeries", out.Stored[0].SeriesID).(*fhir.DicomSeries)
	s2 := e.one("DicomSeries", out.Stored[3].SeriesID).(*fhir.DicomSeries)
	if *s1.NumberOfSeriesRelatedInstances != 3 || *s2.NumberOfSeriesRelatedInstances != 1 || s2.Modality != "mr" {
		t.Errorf("series: %d %d %q", *s1.NumberOfSeriesRelatedInstances, *s2.NumberOfSeriesRelatedInstances, s2.Modality)
	}
	// request thứ hai bổ sung instance vào study đã có: số liệu cộng đúng (tính lại, không cộng dồn)
	e.must(multi(file("1", "1", "4")))
	study = e.one("DicomStudy", out.Stored[0].StudyID).(*fhir.DicomStudy)
	if *study.NumberOfStudyRelatedInstances != 5 || *study.NumberOfStudyRelatedSeries != 2 {
		t.Errorf("sau lần hai: %d instance, %d series", *study.NumberOfStudyRelatedInstances, *study.NumberOfStudyRelatedSeries)
	}
}

// Tải lại cùng instance là idempotent: không tạo bản trùng, không Binary hay nội dung mồ côi (Medplum để lại một Binary mỗi lần tải lại).
func TestReuploadIsIdempotent(t *testing.T) {
	e := newEnv(t)
	data := file("1", "1", "1")
	first := e.must(multi(data))
	blobs, bins, puts := e.blobs.count(), e.count("Binary", nil), e.blobs.puts
	for i := 0; i < 3; i++ {
		again := e.must(multi(data))
		if again.Status != 200 || len(again.Stored) != 1 || !again.Stored[0].AlreadyStored || again.Stored[0].InstanceID != first.Stored[0].InstanceID {
			t.Fatalf("lần %d: %+v", i, again)
		}
	}
	if e.blobs.puts != puts {
		t.Errorf("tải lại không được ghi nội dung nữa: %d lần Put thêm", e.blobs.puts-puts)
	}
	if e.count("DicomInstance", nil) != 1 || e.count("DicomStudy", nil) != 1 || e.count("Binary", nil) != bins || e.blobs.count() != blobs {
		t.Errorf("sau tải lại: instance %d, study %d, binary %d (muốn %d), nội dung %d (muốn %d)", e.count("DicomInstance", nil), e.count("DicomStudy", nil), e.count("Binary", nil), bins, e.blobs.count(), blobs)
	}
}

// Nhiều yêu cầu cùng lúc tải cùng một study (và cùng một instance): đúng một study, một series, một instance; nội dung của các bên thua bị dọn.
func TestConcurrentUploadsDoNotDuplicate(t *testing.T) {
	e := newEnv(t)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			out, err := e.store(ctxA, multi(file("1", "1", "900"), file("1", "1", fmt.Sprint(100+i))), "")
			if err != nil || out.Status != 200 {
				t.Errorf("goroutine %d: %v %+v", i, err, out)
			}
		}(i)
	}
	wg.Wait()
	if e.count("DicomStudy", nil) != 1 || e.count("DicomSeries", nil) != 1 || e.count("DicomInstance", nil) != 9 {
		t.Fatalf("study %d, series %d, instance %d (muốn 1, 1, 9)", e.count("DicomStudy", nil), e.count("DicomSeries", nil), e.count("DicomInstance", nil))
	}
	if e.count("Binary", nil) != 9 || e.blobs.count() != 9 {
		t.Errorf("Binary %d, nội dung %d (muốn 9): các bên thua phải dọn", e.count("Binary", nil), e.blobs.count())
	}
	req, _ := fhirsearch.Parse("DicomStudy", nil)
	res, _ := e.scope.Search(ctxA, req)
	study := res.Matches[0].(*fhir.DicomStudy)
	if *study.NumberOfStudyRelatedInstances != 9 {
		t.Errorf("số liệu sau tải song song: %d instance", *study.NumberOfStudyRelatedInstances)
	}
}

func TestInstanceKindsAreAccepted(t *testing.T) {
	e := newEnv(t)
	frames := [][]byte{bytes.Repeat([]byte{0xFF, 0xD8, 1}, 40), bytes.Repeat([]byte{0xFF, 0xD8, 2}, 40)}
	out := e.must(multi(
		file("2", "1", "11", func(m *dicomtest.Meta) { m.Frames = 3 }),
		file("2", "1", "12", func(m *dicomtest.Meta) { m.TransferSyntax = dicomtest.ImplicitLE }),
		file("2", "1", "13", func(m *dicomtest.Meta) {
			m.TransferSyntax, m.Bits, m.Frames, m.Fragments = dicomtest.JPEGBaseline, 8, 2, frames
		}),
		file("2", "1", "14", func(m *dicomtest.Meta) { m.NoPixelData = true; m.Modality = "SR" }),
		file("2", "1", "15", func(m *dicomtest.Meta) {
			m.Extra = []dicomtest.Elem{dicomtest.Sequence(true, 8, 0x1110, true, dicomtest.Item(true, dicomtest.E(true, 8, 0x1150, "UI", []byte("1.2.3"))))}
		}),
	))
	if out.Status != 200 || len(out.Stored) != 5 {
		t.Fatalf("%+v", out)
	}
	inst := e.one("DicomInstance", out.Stored[0].InstanceID).(*fhir.DicomInstance)
	if inst.NumberOfFrames == nil || *inst.NumberOfFrames != 3 {
		t.Errorf("NumberOfFrames: %v", inst.NumberOfFrames)
	}
	if !strings.Contains(e.one("DicomInstance", out.Stored[4].InstanceID).(*fhir.DicomInstance).Metadata, `"00081150":{"vr":"UI","Value":["1.2.3"]}`) {
		t.Error("SQ không rõ độ dài phải vào metadata")
	}
}

func failedReasons(out *dicomstow.Outcome) []uint16 {
	var r []uint16
	for _, f := range out.Failed {
		r = append(r, f.Reason)
	}
	return r
}

// Instance hỏng bị loại với lý do theo chuẩn; không để lại resource hay nội dung nào cho chúng.
func TestBadInstancesAreRejectedCleanly(t *testing.T) {
	e := newEnv(t)
	good := file("3", "1", "1")
	cases := []struct {
		name   string
		data   []byte
		reason uint16
	}{
		{"không phải DICOM", bytes.Repeat([]byte("hello"), 200), dicomstow.ReasonCannotUnderstand},
		{"rỗng", nil, dicomstow.ReasonCannotUnderstand},
		{"cắt giữa PixelData", file("3", "1", "21", func(m *dicomtest.Meta) { m.Rows, m.Cols = 32, 32; m.TruncatePixel = 300 }), dicomstow.ReasonCannotUnderstand},
		{"đóng gói cắt giữa chừng", file("3", "1", "22", func(m *dicomtest.Meta) {
			m.TransferSyntax, m.Bits, m.Fragments = dicomtest.JPEGBaseline, 8, [][]byte{bytes.Repeat([]byte{1}, 200)}
			m.TruncatePixel = 40
		}), dicomstow.ReasonCannotUnderstand},
		{"SQ lồng sâu (bom bộ nhớ)", file("3", "1", "23", func(m *dicomtest.Meta) {
			m.Extra = []dicomtest.Elem{dicomtest.DeepUndefinedSQ(2000)}
			m.NoPixelData = true
		}), dicomstow.ReasonCannotUnderstand},
		{"Big Endian", file("3", "1", "24", func(m *dicomtest.Meta) { m.TransferSyntax = dicomtest.ExplicitBE }), dicomstow.ReasonCannotUnderstand},
		{"thiếu StudyInstanceUID", file("3", "1", "25", func(m *dicomtest.Meta) { m.StudyUID = "" }), dicomstow.ReasonCannotUnderstand},
		{"UID sai", file("3", "1", "26", func(m *dicomtest.Meta) { m.SOPUID = "abc.def" }), dicomstow.ReasonCannotUnderstand},
		{"UID quá dài", file("3", "1", "27", func(m *dicomtest.Meta) { m.SOPUID = "1." + strings.Repeat("2", 70) }), dicomstow.ReasonCannotUnderstand},
		{"header quá lớn", file("3", "1", "28", func(m *dicomtest.Meta) {
			m.Extra = []dicomtest.Elem{dicomtest.E(true, 9, 0x1001, "OB", make([]byte, 2<<20))}
		}), dicomstow.ReasonOutOfResources},
	}
	var all [][]byte
	for _, c := range cases {
		all = append(all, c.data)
	}
	// mỗi trường hợp một yêu cầu riêng để biết đích danh lý do
	for _, c := range cases {
		out := e.must(multi(c.data))
		if out.Status != 409 || len(out.Stored) != 0 || len(out.Failed) != 1 || out.Failed[0].Reason != c.reason {
			t.Errorf("%s: status %d, lý do %v (muốn 409 và 0x%X)", c.name, out.Status, failedReasons(out), c.reason)
		}
		if _, has := out.Response["00081199"]; has {
			t.Errorf("%s: không được có ReferencedSOPSequence", c.name)
		}
		if f := out.Response["00081198"]; f.VR != "SQ" || len(f.Value) != 1 || f.Value[0].(dicomweb.Dataset)["00081197"].Value[0] != int(c.reason) {
			t.Errorf("%s: FailedSOPSequence: %+v", c.name, out.Response)
		}
	}
	if e.count("DicomInstance", nil) != 0 || e.count("DicomStudy", nil) != 0 || e.count("Binary", nil) != 0 || e.blobs.count() != 0 {
		t.Errorf("instance hỏng để lại: study %d, instance %d, binary %d, nội dung %d", e.count("DicomStudy", nil), e.count("DicomInstance", nil), e.count("Binary", nil), e.blobs.count())
	}
	// lẫn tốt và hỏng: 202, chỉ cái tốt được lưu; UID của cái hỏng có trong FailedSOPSequence khi đọc được
	out := e.must(multi(good, bytes.Repeat([]byte("x"), 500), file("3", "1", "29", func(m *dicomtest.Meta) { m.TruncatePixel = 100 })))
	if out.Status != 202 || len(out.Stored) != 1 || len(out.Failed) != 2 {
		t.Fatalf("lẫn lộn: %d %+v", out.Status, out)
	}
	if out.Failed[1].SOPInstanceUID != dicomtest.UID("3", "1", "29") {
		t.Errorf("UID của instance hỏng: %q", out.Failed[1].SOPInstanceUID)
	}
	if e.count("DicomInstance", nil) != 1 || e.blobs.count() != 1 {
		t.Errorf("chỉ instance tốt được lưu: %d instance, %d nội dung", e.count("DicomInstance", nil), e.blobs.count())
	}
}

func TestStudyMismatchAndSizeLimit(t *testing.T) {
	e := newEnv(t)
	// POST vào một study cụ thể: instance của study khác bị từ chối (A900), instance đúng study thì nhận
	out, err := e.store(ctxA, multi(file("4", "1", "1"), file("5", "1", "1")), dicomtest.UID("4"))
	if err != nil || out.Status != 202 || len(out.Stored) != 1 || len(out.Failed) != 1 || out.Failed[0].Reason != dicomstow.ReasonDataSetMismatch {
		t.Fatalf("%v %+v", err, out)
	}
	// trần kích thước instance
	e.cfg.MaxInstanceBytes = 2000
	big := file("6", "1", "31", func(m *dicomtest.Meta) { m.Rows, m.Cols = 64, 64 })
	out = e.must(multi(big, file("6", "1", "32", func(m *dicomtest.Meta) { m.Rows, m.Cols, m.Bits = 4, 4, 8 })))
	if out.Status != 202 || len(out.Stored) != 1 || out.Failed[0].Reason != dicomstow.ReasonOutOfResources || out.Stored[0].SOPInstanceUID != dicomtest.UID("6", "1", "32") {
		t.Fatalf("%+v", out)
	}
	if e.count("DicomInstance", nil) != 2 || e.blobs.count() != 2 {
		t.Errorf("instance quá lớn không được để lại gì: %d instance, %d nội dung", e.count("DicomInstance", nil), e.blobs.count())
	}
}

func TestRequestLevelErrors(t *testing.T) {
	e := newEnv(t)
	statusOf := func(b body) int {
		_, err := e.store(ctxA, b, "")
		var re *dicomstow.RequestError
		if !errors.As(err, &re) {
			t.Fatalf("muốn RequestError, được %v", err)
		}
		return re.Status
	}
	if got := statusOf(body{"application/dicom", strings.NewReader("x")}); got != http.StatusUnsupportedMediaType {
		t.Errorf("Content-Type sai: %d", got)
	}
	if got := statusOf(body{"", strings.NewReader("x")}); got != http.StatusUnsupportedMediaType {
		t.Errorf("không Content-Type: %d", got)
	}
	if got := statusOf(body{"multipart/related", strings.NewReader("x")}); got != http.StatusBadRequest {
		t.Errorf("thiếu boundary: %d", got)
	}
	if got := statusOf(body{`multipart/related; boundary=abc`, strings.NewReader("không phải multipart")}); got != http.StatusBadRequest {
		t.Errorf("thân sai: %d", got)
	}
	if got := statusOf(body{`multipart/related; boundary=abc`, strings.NewReader("--abc--\r\n")}); got != http.StatusBadRequest {
		t.Errorf("không có phần nào: %d", got)
	}
	// type không nháy kép (client sai chuẩn): vẫn rút được boundary
	b := multi(file("7", "1", "1"))
	b.ct = strings.Replace(b.ct, `type="application/dicom"`, `type=application/dicom`, 1)
	b.ct = strings.ReplaceAll(b.ct, `"`, ``)
	if out := e.must(b); out.Status != 200 || len(out.Stored) != 1 {
		t.Errorf("type không nháy kép: %+v", out)
	}
	// phần không phải application/dicom
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	pw, _ := w.CreatePart(textproto.MIMEHeader{"Content-Type": {"application/json"}})
	pw.Write([]byte("{}"))
	w.Close()
	out := e.must(body{`multipart/related; type="application/dicom"; boundary="` + w.Boundary() + `"`, &buf})
	if out.Status != 409 || out.Failed[0].Reason != dicomstow.ReasonCannotUnderstand {
		t.Errorf("phần JSON: %+v", out)
	}
}

type brokenReader struct {
	r   io.Reader
	cut int
	n   int
}

func (b *brokenReader) Read(p []byte) (int, error) {
	if b.n >= b.cut {
		return 0, errors.New("connection reset")
	}
	if len(p) > b.cut-b.n {
		p = p[:b.cut-b.n]
	}
	n, err := b.r.Read(p)
	b.n += n
	return n, err
}

// Thân yêu cầu đứt giữa chừng: phần đã xong vẫn được lưu và tính số liệu; phần dở bị bỏ và dọn sạch.
func TestBrokenRequestKeepsCompletedInstances(t *testing.T) {
	e := newEnv(t)
	first, second := file("8", "1", "1"), file("8", "1", "2", func(m *dicomtest.Meta) { m.Rows, m.Cols = 64, 64 })
	b := multi(first, second)
	raw, _ := io.ReadAll(b.r)
	cut := len(first) + len(second)/2 + 400
	out, err := e.store(ctxA, body{b.ct, &brokenReader{r: bytes.NewReader(raw), cut: cut}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Stored) != 1 || len(out.Failed) == 0 || out.Status != 202 {
		t.Fatalf("%d %+v", out.Status, out)
	}
	if e.count("DicomInstance", nil) != 1 || e.blobs.count() != 1 || e.count("Binary", nil) != 1 {
		t.Errorf("phần dở phải được dọn: %d instance, %d nội dung, %d binary", e.count("DicomInstance", nil), e.blobs.count(), e.count("Binary", nil))
	}
	study := e.one("DicomStudy", out.Stored[0].StudyID).(*fhir.DicomStudy)
	if *study.NumberOfStudyRelatedInstances != 1 {
		t.Errorf("số liệu: %d", *study.NumberOfStudyRelatedInstances)
	}
}

func TestStorageFailureLeavesNothing(t *testing.T) {
	e := newEnv(t)
	e.blobs.fail = errors.New("disk full")
	out := e.must(multi(file("9", "1", "1")))
	if out.Status != 409 || out.Failed[0].Reason != dicomstow.ReasonProcessingFailure {
		t.Fatalf("%+v", out)
	}
	if e.count("Binary", nil) != 0 || e.count("DicomInstance", nil) != 0 || e.count("DicomStudy", nil) != 0 {
		t.Error("lỗi kho không được để lại resource")
	}
	if strings.Contains(out.Failed[0].Message, "disk full") {
		t.Errorf("không lộ chi tiết nội bộ: %q", out.Failed[0].Message)
	}
}

// Quyền: token chỉ đọc không STOW được (lỗi cả yêu cầu, không phải từng instance); project khác không thấy và không đụng dữ liệu của nhau.
func TestPermissionsAndProjectIsolation(t *testing.T) {
	e := newEnv(t)
	// quyền theo loại resource (scope SMART) do fhirauth.Scope kiểm ở kho; vai trò đọc/ghi do tầng HTTP kiểm
	readOnly := fhirauth.WithPrincipal(context.Background(), &fhirauth.Principal{Subject: "u", Project: "A", Role: fhirauth.RoleWrite, Grants: fhirauth.Grants{"*": fhirauth.PermRead | fhirauth.PermSearch}})
	_, err := e.store(readOnly, multi(file("10", "1", "1")), "")
	var re *dicomstow.RequestError
	if !errors.As(err, &re) || re.Status != http.StatusForbidden {
		t.Fatalf("token chỉ đọc: %v", err)
	}
	if e.count("Binary", nil) != 0 || e.blobs.count() != 0 {
		t.Error("bị từ chối mà vẫn để lại dữ liệu")
	}
	if _, err := e.store(context.Background(), multi(file("10", "1", "1")), ""); !errors.As(err, &re) || re.Status != http.StatusForbidden && re.Status != http.StatusUnauthorized {
		t.Errorf("không có người gọi: %v", err)
	}

	same := file("10", "1", "1")
	a := e.must(multi(same))
	outB, err := e.store(principal("B", fhirauth.RoleWrite), multi(same), "")
	if err != nil || outB.Status != 200 || outB.Stored[0].AlreadyStored || outB.Stored[0].InstanceID == a.Stored[0].InstanceID {
		t.Fatalf("project B cùng UID phải có bản riêng: %v %+v", err, outB)
	}
	if e.count("DicomInstance", nil) != 1 { // đếm theo project A
		t.Errorf("project A thấy %d instance", e.count("DicomInstance", nil))
	}
	if _, err := e.scope.ReadResource(ctxA, "DicomInstance", outB.Stored[0].InstanceID); err == nil {
		t.Error("project A đọc được instance của project B")
	}
	if e.blobs.count() != 2 {
		t.Errorf("mỗi project một nội dung: %d", e.blobs.count())
	}
}

// Thân yêu cầu vượt trần cả yêu cầu (http.MaxBytesReader) giữa chừng: 413 cho cả yêu cầu, nhưng instance đã xong trước đó được giữ và tính số liệu.
func TestRequestTooLargeKeepsCompletedInstances(t *testing.T) {
	e := newEnv(t)
	first, second := file("12", "1", "1"), file("12", "1", "2", func(m *dicomtest.Meta) { m.Rows, m.Cols = 64, 64 })
	b := multi(first, second)
	raw, _ := io.ReadAll(b.r)
	limit := int64(len(first) + len(second)/2 + 400)
	rec := httptestRecorder()
	body := http.MaxBytesReader(rec, io.NopCloser(bytes.NewReader(raw)), limit)
	_, err := e.store(ctxA, dicomBody(b.ct, body), "")
	var re *dicomstow.RequestError
	if !errors.As(err, &re) || re.Status != http.StatusRequestEntityTooLarge {
		t.Fatalf("muốn 413, được %v", err)
	}
	if e.count("DicomInstance", nil) != 1 || e.blobs.count() != 1 || e.count("Binary", nil) != 1 {
		t.Errorf("instance đã xong phải được giữ, phần dở phải được dọn: %d instance, %d nội dung, %d binary", e.count("DicomInstance", nil), e.blobs.count(), e.count("Binary", nil))
	}
	req, _ := fhirsearch.Parse("DicomStudy", nil)
	res, _ := e.scope.Search(ctxA, req)
	if study := res.Matches[0].(*fhir.DicomStudy); *study.NumberOfStudyRelatedInstances != 1 {
		t.Errorf("số liệu: %d", *study.NumberOfStudyRelatedInstances)
	}
}

func dicomBody(ct string, r io.Reader) body { return body{ct, r} }

func httptestRecorder() http.ResponseWriter { return httptest.NewRecorder() }
