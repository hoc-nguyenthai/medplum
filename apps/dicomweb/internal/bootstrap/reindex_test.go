package bootstrap_test

import (
	"bytes"
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"trivita-fhir/apps/dicomweb/internal/bootstrap"
	"trivita-fhir/pkg/binarystore"
	"trivita-fhir/pkg/dicomretrieve"
	"trivita-fhir/pkg/dicomtest"
	"trivita-fhir/pkg/dicomweb"
	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirrouter"
	"trivita-fhir/pkg/fhirsearch"
)

func allInstances(t *testing.T, c *bootstrap.Container) []*fhir.DicomInstance {
	t.Helper()
	req, err := fhirsearch.Parse("DicomInstance", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Count, req.Total = 1000, "none"
	res, err := c.Store.Search(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	var out []*fhir.DicomInstance
	for _, m := range res.Matches {
		out = append(out, m.(*fhir.DicomInstance))
	}
	return out
}

func hasIndex(i *fhir.DicomInstance) bool {
	for _, e := range i.Extension {
		if e.URL == dicomweb.FrameIndexURL {
			return true
		}
	}
	return false
}

func setIndex(t *testing.T, c *bootstrap.Container, i *fhir.DicomInstance, value string) {
	t.Helper()
	u := *i
	u.Extension = nil
	if value != "" {
		u.Extension = []fhir.Extension{{URL: dicomweb.FrameIndexURL, ValueString: value}}
	}
	if _, err := c.Store.UpdateResource(context.Background(), &u, fhirrouter.UpdateOptions{IfMatch: i.Meta.VersionId}); err != nil {
		t.Fatal(err)
	}
}

// Công cụ lập lại chỉ mục: instance cũ (không có chỉ mục) và chỉ mục sai số frame được lập lại, đúng mọi project, dry-run không ghi, chạy lại không làm gì, một instance mất nội dung
// không chặn các instance khác, và sau đó WADO-RS đọc frame theo khoảng (chỉ đúng byte của frame).
func TestReindexRepairsOldInstances(t *testing.T) {
	c, h := handler(t, settings(t, true))
	srv := httptest.NewServer(h)
	defer srv.Close()

	px := make([]byte, 64*64*2*3)
	for i := range px {
		px[i] = byte(i * 7)
	}
	jf := [][]byte{bytes.Repeat([]byte{0xFF, 0xD8, 1, 2}, 4096), bytes.Repeat([]byte{0xFF, 0xD8, 3, 4}, 4096)}
	stow := func(project string, files ...[]byte) {
		readers := make([]*bytes.Reader, len(files))
		for i, f := range files {
			readers[i] = bytes.NewReader(f)
		}
		ct, body := stowBody(toReaders(readers)...)
		if code, _, raw := send(t, srv, "/dicomweb/studies", tok(t, project, "fhir.write"), ct, body); code != 200 {
			t.Fatalf("STOW %s: %d %s", project, code, raw)
		}
	}
	stow("A",
		inst("1", "1", "1", func(m *dicomtest.Meta) { m.Rows, m.Cols, m.Bits, m.Frames, m.Pixel = 64, 64, 16, 3, px }),
		inst("1", "1", "2", func(m *dicomtest.Meta) {
			m.TransferSyntax, m.Rows, m.Cols, m.Bits, m.Frames, m.Fragments = dicomtest.JPEGBaseline, 8, 8, 8, 2, jf
		}),
		inst("1", "1", "3", func(m *dicomtest.Meta) { m.NoPixelData = true; m.Modality = "SR" }),
	)
	stow("B", inst("9", "1", "1", func(m *dicomtest.Meta) { m.Rows, m.Cols, m.Bits, m.Frames, m.Pixel = 64, 64, 16, 3, px }))

	insts := allInstances(t, c)
	if len(insts) != 4 {
		t.Fatalf("có %d instance", len(insts))
	}
	byUID := map[string]*fhir.DicomInstance{}
	withIndex := 0
	for _, i := range insts {
		byUID[i.SopInstanceUid] = i
		if hasIndex(i) {
			withIndex++
		}
	}
	if withIndex != 3 { // STOW lập chỉ mục cho 3 instance có ảnh
		t.Fatalf("STOW phải lập chỉ mục cho 3 instance, được %d", withIndex)
	}
	good := map[string]string{}
	for uid, i := range byUID {
		for _, e := range i.Extension {
			if e.URL == dicomweb.FrameIndexURL {
				good[uid] = e.ValueString
			}
		}
	}
	// giả lập dữ liệu cũ: bỏ chỉ mục của A/1/1/1 và instance B; chỉ mục của JPEG bị thay bằng chỉ mục sai số frame
	a1, jp, b1 := byUID[dicomtest.UID("1", "1", "1")], byUID[dicomtest.UID("1", "1", "2")], byUID[dicomtest.UID("9", "1", "1")]
	setIndex(t, c, a1, "")
	setIndex(t, c, b1, "")
	wrong, _ := dicomweb.FrameIndex{TransferSyntax: dicomtest.JPEGBaseline, Frames: []dicomweb.FrameRange{{Offset: 1, Length: 1}}}.Encode()
	setIndex(t, c, jp, wrong)

	ctx := context.Background()
	cfg := dicomretrieve.Config{Repo: c.Store, Blobs: c.Blobs}

	// dry-run: báo 3 cần lập, không ghi
	rep, err := dicomretrieve.Reindex(ctx, cfg, dicomretrieve.ReindexOptions{DryRun: true})
	if err != nil || rep.Scanned != 4 || rep.Indexed != 3 || rep.NoPixels != 1 || rep.UpToDate != 0 {
		t.Fatalf("dry-run: %+v %v", rep, err)
	}
	if hasIndex(byIDFresh(t, c, a1.ID)) {
		t.Error("dry-run đã ghi")
	}

	// một instance mất nội dung: Failed, không chặn cái khác
	lost := byUID[dicomtest.UID("9", "1", "1")]
	key := keyOf(t, c, lost)
	saved := readAll(t, c.Blobs, key)
	if err := c.Blobs.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	rep, err = dicomretrieve.Reindex(ctx, cfg, dicomretrieve.ReindexOptions{})
	if err != nil || rep.Indexed != 2 || rep.Failed != 1 {
		t.Fatalf("mất nội dung: %+v %v", rep, err)
	}
	// khôi phục nội dung rồi chạy lại: xong nốt
	if _, err := c.Blobs.Put(ctx, key, bytes.NewReader(saved), binarystore.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	rep, err = dicomretrieve.Reindex(ctx, cfg, dicomretrieve.ReindexOptions{})
	if err != nil || rep.Indexed != 1 || rep.Failed != 0 || rep.UpToDate != 2 || rep.NoPixels != 1 {
		t.Fatalf("sau khi khôi phục: %+v %v", rep, err)
	}
	// chạy lại: không còn gì để làm
	if rep, _ = dicomretrieve.Reindex(ctx, cfg, dicomretrieve.ReindexOptions{}); rep.Indexed != 0 || rep.UpToDate != 3 {
		t.Errorf("idempotent: %+v", rep)
	}
	// chỉ mục lập lại giống hệt chỉ mục STOW đã lập
	for _, i := range allInstances(t, c) {
		for _, e := range i.Extension {
			if e.URL == dicomweb.FrameIndexURL && e.ValueString != good[i.SopInstanceUid] {
				t.Errorf("%s: chỉ mục lập lại khác chỉ mục STOW:\n%s\n%s", i.SopInstanceUid, e.ValueString, good[i.SopInstanceUid])
			}
		}
	}
	// project được giữ nguyên: token B chỉ thấy instance của B, A chỉ thấy của A
	if code, _, raw := get(t, srv, "/dicomweb/studies", tok(t, "B", "fhir.read")); code != 200 || bytes.Count(raw, []byte(`"0020000D"`)) != 1 || !bytes.Contains(raw, []byte(dicomtest.UID("9"))) {
		t.Errorf("project B sau reindex: %d %s", code, raw)
	}
	if code, _, _ := get(t, srv, "/dicomweb/studies/"+dicomtest.UID("9"), tok(t, "A", "fhir.read")); code != 404 {
		t.Errorf("A thấy study của B sau reindex: %d", code)
	}
	// WADO-RS dùng chỉ mục mới: frame 3 đọc theo khoảng, đúng byte, chỉ đọc đúng frame
	cb := &countingBlobs{Store: c.Blobs}
	c.Blobs = cb
	path := "/dicomweb/studies/" + dicomtest.UID("1") + "/series/" + dicomtest.UID("1", "1") + "/instances/" + dicomtest.UID("1", "1", "1") + "/frames/3"
	code, hdr, raw := get(t, srv, path, tok(t, "A", "fhir.read"))
	_, parts, _ := multipartParts(t, hdr, raw)
	fs := 64 * 64 * 2
	if code != 200 || len(parts) != 1 || !bytes.Equal(parts[0], px[2*fs:]) {
		t.Fatalf("frame 3 sau reindex: %d", code)
	}
	if n := cb.n.Load(); n != int64(fs) {
		t.Errorf("đọc %d byte từ kho, muốn đúng %d (đọc theo khoảng nhờ chỉ mục)", n, fs)
	}
	// Force lập lại cả chỉ mục đã đủ; Limit giới hạn số lần lập
	if rep, _ = dicomretrieve.Reindex(ctx, cfg, dicomretrieve.ReindexOptions{Force: true, Limit: 2}); rep.Indexed != 2 {
		t.Errorf("force+limit: %+v", rep)
	}
}

func toReaders(rs []*bytes.Reader) []io.Reader {
	out := make([]io.Reader, len(rs))
	for i, r := range rs {
		out[i] = r
	}
	return out
}

func byIDFresh(t *testing.T, c *bootstrap.Container, id string) *fhir.DicomInstance {
	t.Helper()
	r, err := c.Store.ReadResource(context.Background(), "DicomInstance", id)
	if err != nil {
		t.Fatal(err)
	}
	return r.(*fhir.DicomInstance)
}

func keyOf(t *testing.T, c *bootstrap.Container, i *fhir.DicomInstance) string {
	t.Helper()
	r, err := c.Store.ReadResource(context.Background(), "Binary", strings.TrimPrefix(i.Raw.Reference, "Binary/"))
	if err != nil {
		t.Fatal(err)
	}
	b := r.(*fhir.Binary)
	k, err := binarystore.Key(b.ID, b.Meta.VersionId)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func readAll(t *testing.T, s binarystore.Store, key string) []byte {
	t.Helper()
	rc, err := s.Open(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	b, _ := io.ReadAll(rc)
	return b
}

// bumpingRepo thay đổi instance ngay trước khi ghi chỉ mục, như một ghi song song.
type bumpingRepo struct {
	fhirrouter.Repository
	bump func(fhir.Resource)
}

func (b bumpingRepo) UpdateResource(ctx context.Context, r fhir.Resource, o fhirrouter.UpdateOptions) (fhir.Resource, error) {
	b.bump(r)
	return b.Repository.UpdateResource(ctx, r, o)
}

// Số frame trong metadata khác số frame trong tệp: không ghi chỉ mục sai (Failed). Ghi song song làm đổi phiên bản: không ghi đè (If-Match), instance tính là Failed.
func TestReindexRefusesMismatchesAndConcurrentWrites(t *testing.T) {
	c, h := handler(t, settings(t, true))
	srv := httptest.NewServer(h)
	defer srv.Close()
	px := make([]byte, 8*8*2*3)
	ct, body := stowBody(bytes.NewReader(inst("1", "1", "1", func(m *dicomtest.Meta) { m.Rows, m.Cols, m.Bits, m.Frames, m.Pixel = 8, 8, 16, 3, px })))
	if code, _, raw := send(t, srv, "/dicomweb/studies", tok(t, "A", "fhir.write"), ct, body); code != 200 {
		t.Fatalf("STOW: %d %s", code, raw)
	}
	ctx := context.Background()
	i := allInstances(t, c)[0]
	setIndex(t, c, i, "")
	i = byIDFresh(t, c, i.ID)

	// metadata khai báo 5 frame, tệp có 3
	five := 5
	bad := *i
	bad.NumberOfFrames = &five
	if _, err := c.Store.UpdateResource(ctx, &bad, fhirrouter.UpdateOptions{IfMatch: i.Meta.VersionId}); err != nil {
		t.Fatal(err)
	}
	rep, _ := dicomretrieve.Reindex(ctx, dicomretrieve.Config{Repo: c.Store, Blobs: c.Blobs}, dicomretrieve.ReindexOptions{})
	if rep.Failed != 1 || rep.Indexed != 0 || hasIndex(byIDFresh(t, c, i.ID)) {
		t.Fatalf("sai số frame: %+v", rep)
	}
	// sửa lại metadata; lần này một ghi song song chen vào giữa
	i = byIDFresh(t, c, i.ID)
	three := 3
	fixed := *i
	fixed.NumberOfFrames = &three
	if _, err := c.Store.UpdateResource(ctx, &fixed, fhirrouter.UpdateOptions{IfMatch: i.Meta.VersionId}); err != nil {
		t.Fatal(err)
	}
	racing := bumpingRepo{Repository: c.Store, bump: func(r fhir.Resource) {
		cur := byIDFresh(t, c, i.ID)
		cur.Extension = append(cur.Extension, fhir.Extension{URL: "urn:test:concurrent", ValueString: "x"})
		if _, err := c.Store.UpdateResource(ctx, cur, fhirrouter.UpdateOptions{IfMatch: cur.Meta.VersionId}); err != nil {
			t.Error(err)
		}
	}}
	rep, _ = dicomretrieve.Reindex(ctx, dicomretrieve.Config{Repo: racing, Blobs: c.Blobs}, dicomretrieve.ReindexOptions{})
	got := byIDFresh(t, c, i.ID)
	if rep.Failed != 1 || rep.Indexed != 0 || hasIndex(got) {
		t.Fatalf("ghi song song: %+v", rep)
	}
	kept := false
	for _, e := range got.Extension {
		kept = kept || e.URL == "urn:test:concurrent"
	}
	if !kept {
		t.Error("thay đổi song song bị ghi đè")
	}
	// không còn tranh chấp: lập được
	if rep, _ = dicomretrieve.Reindex(ctx, dicomretrieve.Config{Repo: c.Store, Blobs: c.Blobs}, dicomretrieve.ReindexOptions{}); rep.Indexed != 1 || rep.Failed != 0 {
		t.Errorf("sau khi hết tranh chấp: %+v", rep)
	}
	if got = byIDFresh(t, c, i.ID); !hasIndex(got) {
		t.Error("thiếu chỉ mục")
	}
}
