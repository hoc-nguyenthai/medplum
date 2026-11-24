package dicomretrieve_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"trivita-fhir/pkg/binarystore"
	"trivita-fhir/pkg/dicomretrieve"
	"trivita-fhir/pkg/dicomstow"
	"trivita-fhir/pkg/dicomtest"
	"trivita-fhir/pkg/dicomweb"
	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirauth"
	"trivita-fhir/pkg/fhirrouter"
	"trivita-fhir/pkg/fhirsearch"
)

type env struct {
	t     *testing.T
	repo  fhirrouter.Repository
	blobs binarystore.Store
	ctx   context.Context
}

func principal(project string) context.Context {
	return fhirauth.WithPrincipal(context.Background(), &fhirauth.Principal{Subject: "u", Project: project, Role: fhirauth.RoleWrite})
}

func newEnv(t *testing.T) *env {
	return &env{t: t, repo: fhirauth.Scope(fhirrouter.NewMemoryRepository()), blobs: binarystore.NewMemory(), ctx: principal("A")}
}

func (e *env) cfg() dicomretrieve.Config { return dicomretrieve.Config{Repo: e.repo, Blobs: e.blobs} }

func (e *env) stow(ctx context.Context, files ...[]byte) *dicomstow.Outcome {
	e.t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for _, f := range files {
		pw, _ := w.CreatePart(textproto.MIMEHeader{"Content-Type": {"application/dicom"}})
		pw.Write(f)
	}
	w.Close()
	out, err := dicomstow.Store(ctx, dicomstow.Config{Repo: e.repo, Blobs: e.blobs}, dicomstow.Request{
		ContentType: `multipart/related; type="application/dicom"; boundary=` + w.Boundary(), Body: &buf, RetrieveBase: "https://x",
	})
	if err != nil || len(out.Failed) != 0 {
		e.t.Fatalf("stow: %v %+v", err, out)
	}
	return out
}

func meta(study, series, sop string, mut ...func(*dicomtest.Meta)) dicomtest.Meta {
	m := dicomtest.Meta{StudyUID: dicomtest.UID(study), SeriesUID: dicomtest.UID(study, series), SOPUID: dicomtest.UID(study, series, sop)}
	for _, f := range mut {
		f(&m)
	}
	return m
}

func file(study, series, sop string, mut ...func(*dicomtest.Meta)) []byte {
	return dicomtest.Build(meta(study, series, sop, mut...))
}

func status(err error) int {
	var re *dicomretrieve.Error
	if errors.As(err, &re) {
		return re.Status
	}
	if err != nil {
		return -1
	}
	return 0
}

func TestSearchStudiesAndPaging(t *testing.T) {
	e := newEnv(t)
	for i := 1; i <= 5; i++ {
		e.stow(e.ctx, file(fmt.Sprint(i), "1", "1", func(m *dicomtest.Meta) { m.PatientName = fmt.Sprint("Ten^", i) }))
	}
	p, err := dicomretrieve.SearchStudies(e.ctx, e.cfg(), 0, 0)
	if err != nil || len(p.Items) != 5 || p.More {
		t.Fatalf("mặc định: %v %+v", err, p)
	}
	// phân trang không trùng, không sót
	seen := map[string]bool{}
	for off := 0; off < 5; off += 2 {
		pg, err := dicomretrieve.SearchStudies(e.ctx, e.cfg(), 2, off)
		if err != nil {
			t.Fatal(err)
		}
		if want := off+2 < 5; pg.More != want {
			t.Errorf("offset %d: More = %v", off, pg.More)
		}
		for _, it := range pg.Items {
			uid := it["0020000D"].Value[0].(string)
			if seen[uid] {
				t.Errorf("trùng %s", uid)
			}
			seen[uid] = true
		}
	}
	if len(seen) != 5 {
		t.Errorf("phân trang thiếu: %d/5", len(seen))
	}
	// mỗi phần tử là DICOM JSON có tên bệnh nhân, số liệu tổng hợp
	it := p.Items[0]
	if it["00100010"].VR != "PN" || it["00201208"].Value[0] != 1 || it["00201206"].Value[0] != 1 {
		t.Errorf("study: %+v", it)
	}
	// limit vượt trần bị cắt về trần; offset âm coi như 0
	if pg, err := dicomretrieve.SearchStudies(e.ctx, e.cfg(), 100000, -5); err != nil || len(pg.Items) != 5 {
		t.Errorf("limit lớn: %v %v", pg, err)
	}
	// rỗng: không phải lỗi, danh sách rỗng (không phải nil để JSON ra [])
	empty, err := dicomretrieve.SearchStudies(principal("Z"), e.cfg(), 10, 0)
	if err != nil || empty.Items == nil || len(empty.Items) != 0 {
		t.Errorf("project rỗng: %+v %v", empty, err)
	}
}

func TestSearchSeries(t *testing.T) {
	e := newEnv(t)
	e.stow(e.ctx, file("1", "1", "1"), file("1", "1", "2"), file("1", "2", "1", func(m *dicomtest.Meta) { m.Modality = "MR" }), file("2", "1", "1"))
	p, err := dicomretrieve.SearchSeries(e.ctx, e.cfg(), dicomtest.UID("1"), 0, 0)
	if err != nil || len(p.Items) != 2 {
		t.Fatalf("%v %+v", err, p)
	}
	mods := map[string]int{}
	for _, it := range p.Items {
		mods[it["00080060"].Value[0].(string)] = it["00201209"].Value[0].(int)
		if it["0020000D"].Value[0] != dicomtest.UID("1") { // gồm thuộc tính cấp study
			t.Errorf("thiếu thuộc tính study: %+v", it)
		}
	}
	if mods["CT"] != 2 || mods["MR"] != 1 {
		t.Errorf("số instance theo series: %v", mods)
	}
	if _, err := dicomretrieve.SearchSeries(e.ctx, e.cfg(), dicomtest.UID("99"), 0, 0); status(err) != 404 {
		t.Errorf("study không có: %v", err)
	}
	if pg, _ := dicomretrieve.SearchSeries(e.ctx, e.cfg(), dicomtest.UID("1"), 1, 0); len(pg.Items) != 1 || !pg.More {
		t.Errorf("trang: %+v", pg)
	}
}

func readAll(t *testing.T, write func(io.Writer) error) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := write(&b); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestSeriesMetadata(t *testing.T) {
	e := newEnv(t)
	e.stow(e.ctx, file("1", "1", "1"), file("1", "1", "2"), file("1", "1", "3", func(m *dicomtest.Meta) { m.NoPixelData = true }), file("1", "2", "1"))
	write, err := dicomretrieve.SeriesMetadata(e.ctx, e.cfg(), dicomtest.UID("1"), dicomtest.UID("1", "1"))
	if err != nil {
		t.Fatal(err)
	}
	raw := readAll(t, write)
	var list []map[string]struct {
		VR    string
		Value []any
	}
	if err := json.Unmarshal(raw, &list); err != nil || len(list) != 3 {
		t.Fatalf("%d phần tử, %v: %s", len(list), err, raw)
	}
	sops := map[any]bool{}
	for _, m := range list {
		sops[m["00080018"].Value[0]] = true
		if _, has := m["7FE00010"]; has {
			t.Error("metadata không được có PixelData")
		}
		if m["0020000E"].Value[0] != dicomtest.UID("1", "1") {
			t.Errorf("sai series: %v", m["0020000E"])
		}
	}
	if len(sops) != 3 {
		t.Errorf("SOPInstanceUID: %v", sops)
	}
	// 404 trước khi ghi gì
	for _, c := range [][2]string{{dicomtest.UID("9"), dicomtest.UID("1", "1")}, {dicomtest.UID("1"), dicomtest.UID("1", "9")}} {
		if _, err := dicomretrieve.SeriesMetadata(e.ctx, e.cfg(), c[0], c[1]); status(err) != 404 {
			t.Errorf("%v: %v", c, err)
		}
	}
	// series của study khác không lọt qua UID study sai (study 2 có thật)
	e.stow(e.ctx, file("2", "1", "1"))
	if _, err := dicomretrieve.SeriesMetadata(e.ctx, e.cfg(), dicomtest.UID("2"), dicomtest.UID("1", "1")); status(err) != 404 {
		t.Errorf("series không thuộc study: %v", err)
	}
}

// Series lớn hơn một trang: trả đủ (Medplum cắt ở 1000 mà không báo), JSON hợp lệ.
func TestSeriesMetadataPagesThroughLargeSeries(t *testing.T) {
	e := newEnv(t)
	const n = 1230
	for i := 0; i < n; i += 100 {
		var files [][]byte
		for j := i; j < min(i+100, n); j++ {
			files = append(files, file("1", "1", fmt.Sprint(j+1), func(m *dicomtest.Meta) { m.Rows, m.Cols, m.Bits = 2, 2, 8 }))
		}
		e.stow(e.ctx, files...)
	}
	write, err := dicomretrieve.SeriesMetadata(e.ctx, e.cfg(), dicomtest.UID("1"), dicomtest.UID("1", "1"))
	if err != nil {
		t.Fatal(err)
	}
	var list []map[string]any
	if err := json.Unmarshal(readAll(t, write), &list); err != nil || len(list) != n {
		t.Fatalf("%d/%d instance: %v", len(list), n, err)
	}
	seen := map[string]bool{}
	for _, m := range list {
		seen[m["00080018"].(map[string]any)["Value"].([]any)[0].(string)] = true
	}
	if len(seen) != n {
		t.Errorf("trùng hoặc sót: %d khác biệt", len(seen))
	}
}

// parseMultipart tách phản hồi frame thành các phần.
func parseMultipart(t *testing.T, f *dicomretrieve.Frames) (partType string, parts [][]byte, contentTypes []string) {
	t.Helper()
	raw := readAll(t, f.Write)
	if int64(len(raw)) != f.Length {
		t.Errorf("Length khai báo %d, thực tế %d", f.Length, len(raw))
	}
	mt, params, err := mime.ParseMediaType(f.ContentType)
	if err != nil || mt != "multipart/related" || params["boundary"] == "" {
		t.Fatalf("Content-Type %q: %v", f.ContentType, err)
	}
	mr := multipart.NewReader(bytes.NewReader(raw), params["boundary"])
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(p)
		parts = append(parts, b)
		contentTypes = append(contentTypes, p.Header.Get("Content-Type"))
	}
	return params["type"], parts, contentTypes
}

func TestFramesNativeMatchPixelBytes(t *testing.T) {
	e := newEnv(t)
	px := make([]byte, 6*4*2*3)
	for i := range px {
		px[i] = byte(i*5 + 1)
	}
	e.stow(e.ctx, file("1", "1", "1", func(m *dicomtest.Meta) { m.Rows, m.Cols, m.Bits, m.Frames, m.Pixel = 4, 6, 16, 3, px }))
	for _, tc := range []struct{ frames []int }{{[]int{1}}, {[]int{3}}, {[]int{2}}, {[]int{3, 1}}, {[]int{1, 2, 3}}} {
		f, err := dicomretrieve.OpenFrames(e.ctx, e.cfg(), dicomtest.UID("1"), dicomtest.UID("1", "1"), dicomtest.UID("1", "1", "1"), tc.frames)
		if err != nil {
			t.Fatalf("%v: %v", tc.frames, err)
		}
		typ, parts, cts := parseMultipart(t, f)
		if typ != "application/octet-stream" || len(parts) != len(tc.frames) {
			t.Fatalf("%v: type %q, %d phần", tc.frames, typ, len(parts))
		}
		for i, fn := range tc.frames {
			if !bytes.Equal(parts[i], px[(fn-1)*48:fn*48]) || cts[i] != "application/octet-stream" {
				t.Errorf("%v: frame %d sai nội dung hoặc loại %q", tc.frames, fn, cts[i])
			}
		}
	}
}

func TestFramesEncapsulated(t *testing.T) {
	e := newEnv(t)
	frames := [][]byte{bytes.Repeat([]byte{0xFF, 0xD8, 1, 2}, 30), bytes.Repeat([]byte{0xFF, 0xD8, 3, 4}, 17)}
	e.stow(e.ctx, file("1", "1", "1", func(m *dicomtest.Meta) {
		m.TransferSyntax, m.Bits, m.Frames, m.Fragments = dicomtest.JPEGBaseline, 8, 2, frames
	}))
	for i, want := range frames {
		f, err := dicomretrieve.OpenFrames(e.ctx, e.cfg(), dicomtest.UID("1"), dicomtest.UID("1", "1"), dicomtest.UID("1", "1", "1"), []int{i + 1})
		if err != nil {
			t.Fatal(err)
		}
		typ, parts, cts := parseMultipart(t, f)
		if typ != "image/jpeg" || cts[0] != "image/jpeg; transfer-syntax="+dicomtest.JPEGBaseline || !bytes.Equal(parts[0], want) {
			t.Errorf("frame %d: type %q %q, %d byte", i+1, typ, cts[0], len(parts[0]))
		}
	}
	// một frame gồm nhiều fragment: nội dung là phần nối
	multi := [][]byte{bytes.Repeat([]byte{1}, 10), bytes.Repeat([]byte{2}, 6), bytes.Repeat([]byte{3}, 8)}
	e.stow(e.ctx, file("1", "1", "2", func(m *dicomtest.Meta) {
		m.TransferSyntax, m.Bits, m.Frames, m.Fragments = dicomtest.JPEGBaseline, 8, 1, multi
	}))
	f, err := dicomretrieve.OpenFrames(e.ctx, e.cfg(), dicomtest.UID("1"), dicomtest.UID("1", "1"), dicomtest.UID("1", "1", "2"), []int{1})
	if err != nil {
		t.Fatal(err)
	}
	_, parts, _ := parseMultipart(t, f)
	if want := bytes.Join(multi, nil); !bytes.Equal(parts[0], want) {
		t.Errorf("frame nhiều fragment: %v, muốn %v", parts[0], want)
	}
}

func TestFrameErrors(t *testing.T) {
	e := newEnv(t)
	e.stow(e.ctx, file("1", "1", "1", func(m *dicomtest.Meta) { m.Frames = 2 }), file("1", "1", "2", func(m *dicomtest.Meta) { m.NoPixelData = true }))
	study, series, inst := dicomtest.UID("1"), dicomtest.UID("1", "1"), dicomtest.UID("1", "1", "1")
	for name, c := range map[string]struct {
		study, series, inst string
		frames              []int
		want                int
	}{
		"hợp lệ":               {study, series, inst, []int{2}, 0},
		"frame 0":              {study, series, inst, []int{0}, 400},
		"danh sách rỗng":       {study, series, inst, nil, 400},
		"vượt số frame":        {study, series, inst, []int{3}, 416},
		"vượt trong danh sách": {study, series, inst, []int{1, 3}, 416},
		"study không có":       {dicomtest.UID("9"), series, inst, []int{1}, 404},
		"series không có":      {study, dicomtest.UID("1", "9"), inst, []int{1}, 404},
		"instance không có":    {study, series, dicomtest.UID("1", "1", "9"), []int{1}, 404},
		"không có ảnh":         {study, series, dicomtest.UID("1", "1", "2"), []int{1}, 404},
	} {
		_, err := dicomretrieve.OpenFrames(e.ctx, e.cfg(), c.study, c.series, c.inst, c.frames)
		if got := status(err); got != c.want {
			t.Errorf("%s: %d, muốn %d (%v)", name, got, c.want, err)
		}
	}
	// instance thuộc series khác không truy cập được qua series sai
	e.stow(e.ctx, file("1", "2", "1"))
	if _, err := dicomretrieve.OpenFrames(e.ctx, e.cfg(), study, dicomtest.UID("1", "2"), inst, []int{1}); status(err) != 404 {
		t.Errorf("instance qua series sai: %v", err)
	}
}

func TestParseFrames(t *testing.T) {
	for in, want := range map[string][]int{"1": {1}, "12": {12}, "1,2,3": {1, 2, 3}, "3,1": {3, 1}} {
		if got, err := dicomretrieve.ParseFrames(in); err != nil || fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("%q: %v %v", in, got, err)
		}
	}
	for _, in := range []string{"", "0", "-1", "a", "1,", ",1", "1,,2", "1.5", "1 2", "1234567890", strings.Repeat("1,", 60) + "1"} {
		if got, err := dicomretrieve.ParseFrames(in); status(err) != 400 {
			t.Errorf("%q: muốn 400, được %v %v", in, got, err)
		}
	}
}

// Quyền theo project: project khác không thấy study, series, instance hay frame của nhau; thiếu quyền đọc Binary thì không lấy được frame.
func TestRetrieveRespectsProjectAndPermissions(t *testing.T) {
	e := newEnv(t)
	e.stow(e.ctx, file("1", "1", "1"))
	other := principal("B")
	if p, _ := dicomretrieve.SearchStudies(other, e.cfg(), 0, 0); len(p.Items) != 0 {
		t.Error("project B thấy study của A")
	}
	if _, err := dicomretrieve.SearchSeries(other, e.cfg(), dicomtest.UID("1"), 0, 0); status(err) != 404 {
		t.Errorf("series của A qua B: %v", err)
	}
	if _, err := dicomretrieve.OpenFrames(other, e.cfg(), dicomtest.UID("1"), dicomtest.UID("1", "1"), dicomtest.UID("1", "1", "1"), []int{1}); status(err) != 404 {
		t.Errorf("frame của A qua B: %v", err)
	}
	// không có quyền đọc Binary (SMART): 403 thay vì 500
	noBinary := fhirauth.WithPrincipal(context.Background(), &fhirauth.Principal{Subject: "u", Project: "A", Role: fhirauth.RoleRead,
		Grants: fhirauth.Grants{"DicomStudy": fhirauth.PermRead | fhirauth.PermSearch, "DicomSeries": fhirauth.PermRead | fhirauth.PermSearch, "DicomInstance": fhirauth.PermRead | fhirauth.PermSearch}})
	if _, err := dicomretrieve.OpenFrames(noBinary, e.cfg(), dicomtest.UID("1"), dicomtest.UID("1", "1"), dicomtest.UID("1", "1", "1"), []int{1}); status(err) != 403 {
		t.Errorf("thiếu quyền đọc Binary: %v", err)
	}
	if _, err := dicomretrieve.SearchStudies(context.Background(), e.cfg(), 0, 0); status(err) != 403 {
		t.Errorf("không có người gọi: %v", err)
	}
}

// Nội dung kho hỏng hoặc mất: lỗi 500 không lộ chi tiết, xảy ra TRƯỚC khi ghi byte nào.
func TestFramesWithBrokenStorage(t *testing.T) {
	e := newEnv(t)
	out := e.stow(e.ctx, file("1", "1", "1", func(m *dicomtest.Meta) { m.Rows, m.Cols = 16, 16 }))
	_ = out
	cfg := e.cfg()
	cfg.Blobs = binarystore.NewMemory() // kho trống: nội dung mất
	_, err := dicomretrieve.OpenFrames(e.ctx, cfg, dicomtest.UID("1"), dicomtest.UID("1", "1"), dicomtest.UID("1", "1", "1"), []int{1})
	// số frame sai được báo (416) trước khi chạm vào kho, kể cả khi kho hỏng
	if _, err := dicomretrieve.OpenFrames(e.ctx, cfg, dicomtest.UID("1"), dicomtest.UID("1", "1"), dicomtest.UID("1", "1", "1"), []int{99}); status(err) != 416 {
		t.Errorf("frame vượt số: %v", err)
	}
	var re *dicomretrieve.Error
	if !errors.As(err, &re) || re.Status != http.StatusInternalServerError || strings.Contains(re.Msg, "memory") || strings.Contains(re.Msg, "key") {
		t.Errorf("nội dung mất: %v", err)
	}
}

// ---------------------------------------------------------------- chỉ mục frame và đọc theo khoảng (D5)

// countingStore đếm số byte đọc ra từ kho và số lần mở cả nội dung; hideRange giấu RangeOpener để thử đường quay về.
type countingStore struct {
	binarystore.Store
	bytes     int64
	fullOpens int
	ranges    int
}

func (c *countingStore) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	c.fullOpens++
	rc, err := c.Store.Open(ctx, key)
	if err != nil {
		return nil, err
	}
	return &countReader{rc, &c.bytes}, nil
}

func (c *countingStore) OpenRange(ctx context.Context, key string, off, n int64) (io.ReadCloser, error) {
	c.ranges++
	rc, err := c.Store.(binarystore.RangeOpener).OpenRange(ctx, key, off, n)
	if err != nil {
		return nil, err
	}
	return &countReader{rc, &c.bytes}, nil
}

type countReader struct {
	io.ReadCloser
	n *int64
}

func (c *countReader) Read(p []byte) (int, error) {
	n, err := c.ReadCloser.Read(p)
	*c.n += int64(n)
	return n, err
}

// plainStore chỉ cài Store (không RangeOpener), để thử đường mở và bỏ qua byte.
type plainStore struct{ binarystore.Store }

func frameBytes(t *testing.T, cfg dicomretrieve.Config, study, series, inst string, frames []int) [][]byte {
	t.Helper()
	f, err := dicomretrieve.OpenFrames(principal("A"), cfg, dicomtest.UID(study), dicomtest.UID(study, series), dicomtest.UID(study, series, inst), frames)
	if err != nil {
		t.Fatal(err)
	}
	_, parts, _ := parseMultipart(t, f)
	return parts
}

// stripIndex xóa extension chỉ mục frame của một instance (giả lập instance cũ chưa có chỉ mục).
func (e *env) stripIndex(sop string) {
	e.t.Helper()
	e.editIndex(sop, func(ex *fhir.Extension) bool { return false })
}

func (e *env) editIndex(sop string, edit func(*fhir.Extension) bool) {
	e.t.Helper()
	req, _ := fhirsearch.Parse("DicomInstance", url.Values{"sop-instance-uid:exact": {sop}})
	res, err := e.repo.Search(e.ctx, req)
	if err != nil || len(res.Matches) != 1 {
		e.t.Fatalf("tìm instance: %v", err)
	}
	inst := res.Matches[0].(*fhir.DicomInstance)
	var keep []fhir.Extension
	for _, ex := range inst.Extension {
		if ex.URL == dicomweb.FrameIndexURL && !edit(&ex) {
			continue
		}
		keep = append(keep, ex)
	}
	inst.Extension = keep
	if _, err := e.repo.UpdateResource(e.ctx, inst, fhirrouter.UpdateOptions{}); err != nil {
		e.t.Fatal(err)
	}
}

// Đường chỉ mục + đọc theo khoảng và đường quay về (quét + bỏ qua byte) phải cho cùng từng byte, ở mọi dạng ảnh và mọi frame.
func TestIndexedAndFallbackPathsReturnTheSameBytes(t *testing.T) {
	e := newEnv(t)
	jf := [][]byte{bytes.Repeat([]byte{0xFF, 0xD8, 1}, 40), bytes.Repeat([]byte{0xFF, 0xD8, 2}, 25), bytes.Repeat([]byte{0xFF, 0xD8, 3}, 9)}
	e.stow(e.ctx,
		file("1", "1", "1", func(m *dicomtest.Meta) { m.Rows, m.Cols, m.Bits, m.Frames = 8, 8, 16, 4 }),
		file("1", "1", "2", func(m *dicomtest.Meta) {
			m.TransferSyntax, m.Bits, m.Frames, m.Fragments = dicomtest.JPEGBaseline, 8, 3, jf
		}),
		file("1", "1", "3", func(m *dicomtest.Meta) {
			m.TransferSyntax, m.Bits, m.Frames, m.Fragments = dicomtest.JPEGBaseline, 8, 1, jf
		}),
		file("1", "1", "4", func(m *dicomtest.Meta) { m.TransferSyntax = dicomtest.ImplicitLE; m.Rows, m.Cols = 5, 5 }))
	indexed := e.cfg()
	cs := &countingStore{Store: e.blobs}
	indexed.Blobs = cs
	plain := e.cfg()
	plain.Blobs = plainStore{e.blobs}
	for _, c := range []struct {
		sop    string
		frames []int
	}{{"1", []int{1}}, {"1", []int{4, 2}}, {"2", []int{3}}, {"2", []int{1, 2, 3}}, {"3", []int{1}}, {"4", []int{1}}} {
		a := frameBytes(t, indexed, "1", "1", c.sop, c.frames)
		b := frameBytes(t, plain, "1", "1", c.sop, c.frames)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("instance %s frames %v: đường chỉ mục khác đường quay về", c.sop, c.frames)
		}
	}
	if cs.fullOpens != 0 || cs.ranges == 0 {
		t.Errorf("đường chỉ mục không được mở cả nội dung: %d lần mở cả nội dung, %d lần đọc khoảng", cs.fullOpens, cs.ranges)
	}
	// xóa chỉ mục (instance cũ): quay về quét, vẫn đúng
	for _, sop := range []string{"1", "2", "3", "4"} {
		e.stripIndex(dicomtest.UID("1", "1", sop))
	}
	cs2 := &countingStore{Store: e.blobs}
	noIdx := e.cfg()
	noIdx.Blobs = cs2
	for _, c := range []struct {
		sop    string
		frames []int
	}{{"1", []int{3}}, {"2", []int{2}}, {"3", []int{1}}} {
		if a, b := frameBytes(t, noIdx, "1", "1", c.sop, c.frames), frameBytes(t, plain, "1", "1", c.sop, c.frames); !reflect.DeepEqual(a, b) || len(a[0]) == 0 {
			t.Errorf("không chỉ mục, instance %s: kết quả khác hoặc rỗng", c.sop)
		}
	}
	if cs2.fullOpens == 0 {
		t.Error("không có chỉ mục thì phải quét nội dung")
	}
}

// Lấy một frame của instance lớn chỉ đọc đúng byte của frame (đo ở mức kho), không đọc cả tệp.
func TestFrameReadsOnlyItsOwnBytes(t *testing.T) {
	e := newEnv(t)
	e.stow(e.ctx, file("1", "1", "1", func(m *dicomtest.Meta) { m.Rows, m.Cols, m.Bits, m.Frames = 256, 256, 16, 20 })) // 20 frame x 128 KiB
	cs := &countingStore{Store: e.blobs}
	cfg := e.cfg()
	cfg.Blobs = cs
	parts := frameBytes(t, cfg, "1", "1", "1", []int{20})
	if len(parts[0]) != 256*256*2 {
		t.Fatalf("frame %d byte", len(parts[0]))
	}
	if cs.bytes != int64(len(parts[0])) || cs.fullOpens != 0 || cs.ranges != 1 {
		t.Errorf("đọc %d byte (muốn %d), %d lần mở cả nội dung, %d lần đọc khoảng", cs.bytes, len(parts[0]), cs.fullOpens, cs.ranges)
	}
}

// Chỉ mục hỏng, sai số frame, hoặc trỏ ra ngoài nội dung không được làm sai dữ liệu: hỏng và sai số thì quay về quét; trỏ ra ngoài thì 500 trước khi ghi byte nào.
func TestBadFrameIndexNeverReturnsWrongData(t *testing.T) {
	e := newEnv(t)
	px := make([]byte, 8*8*2*3)
	for i := range px {
		px[i] = byte(i + 7)
	}
	e.stow(e.ctx, file("1", "1", "1", func(m *dicomtest.Meta) { m.Rows, m.Cols, m.Bits, m.Frames, m.Pixel = 8, 8, 16, 3, px }))
	sop := dicomtest.UID("1", "1", "1")
	want := frameBytes(t, e.cfg(), "1", "1", "1", []int{2})
	if !bytes.Equal(want[0], px[128:256]) {
		t.Fatal("dữ liệu gốc sai")
	}
	set := func(v string) { e.editIndex(sop, func(ex *fhir.Extension) bool { ex.ValueString = v; return true }) }

	set(`{"v":1,"ts":"x","f":[[1,0]]}`) // không hợp lệ
	if got := frameBytes(t, e.cfg(), "1", "1", "1", []int{2}); !bytes.Equal(got[0], want[0]) {
		t.Error("chỉ mục hỏng: phải quay về quét")
	}
	set(`{"v":1,"ts":"1.2.840.10008.1.2.1","f":[[100,128],[228,128]]}`) // 2 frame trong khi instance có 3
	if got := frameBytes(t, e.cfg(), "1", "1", "1", []int{2}); !bytes.Equal(got[0], want[0]) {
		t.Error("sai số frame: phải quay về quét")
	}
	set(`{"v":1,"ts":"1.2.840.10008.1.2.1","f":[[9000000,128],[9000200,128],[9000400,128]]}`) // trỏ ra ngoài nội dung
	_, err := dicomretrieve.OpenFrames(e.ctx, e.cfg(), dicomtest.UID("1"), dicomtest.UID("1", "1"), sop, []int{2})
	var re *dicomretrieve.Error
	if !errors.As(err, &re) || re.Status != http.StatusInternalServerError {
		t.Errorf("chỉ mục trỏ ra ngoài: %v", err)
	}
	// khoảng bắt đầu trong nội dung nhưng kéo dài quá cuối tệp: nội dung ngắn hơn khai báo không được trả như dữ liệu hợp lệ (lỗi giữa chừng, để handler ngắt kết nối)
	cut := `{"v":1,"ts":"1.2.840.10008.1.2.1","f":[[100,10],[100,10],[100,1000000]]}` // bắt đầu trong tệp, dài hơn tệp
	e.editIndex(sop, func(ex *fhir.Extension) bool { ex.ValueString = cut; return true })
	g, err := dicomretrieve.OpenFrames(e.ctx, e.cfg(), dicomtest.UID("1"), dicomtest.UID("1", "1"), sop, []int{3})
	if err != nil {
		t.Fatalf("OpenFrames: %v", err)
	}
	if err := g.Write(io.Discard); err == nil {
		t.Error("khoảng dài hơn nội dung phải báo lỗi khi ghi, không trả thiếu byte như hợp lệ")
	}
}
