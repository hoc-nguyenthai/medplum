package bootstrap_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"trivita-fhir/apps/dicomweb/internal/bootstrap"
	"trivita-fhir/apps/dicomweb/internal/config"
	"trivita-fhir/pkg/binarystore"
	"trivita-fhir/pkg/dicomretrieve"
	"trivita-fhir/pkg/dicomtest"
	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirauth"
	"trivita-fhir/pkg/fhirrouter"
	"trivita-fhir/pkg/fhirsearch"
)

type pixelFixture struct {
	px     []byte
	jf     [][]byte
	native string // SOPInstanceUID
	jpeg   string
	sr     string
}

// stowFixtures lưu ba instance vào project A: ảnh không nén 3 frame, JPEG 2 frame (hai fragment), và một báo cáo không có ảnh.
func stowFixtures(t *testing.T, srv *httptest.Server) pixelFixture {
	t.Helper()
	f := pixelFixture{px: make([]byte, 16*16*2*3), jf: [][]byte{bytes.Repeat([]byte{0xFF, 0xD8, 1, 2}, 300), bytes.Repeat([]byte{0xFF, 0xD8, 3, 4}, 200)},
		native: dicomtest.UID("1", "1", "1"), jpeg: dicomtest.UID("1", "1", "2"), sr: dicomtest.UID("1", "1", "3")}
	for i := range f.px {
		f.px[i] = byte(i*11 + 5)
	}
	ct, body := stowBody(
		bytes.NewReader(inst("1", "1", "1", func(m *dicomtest.Meta) { m.Rows, m.Cols, m.Bits, m.Frames, m.Pixel = 16, 16, 16, 3, f.px })),
		bytes.NewReader(inst("1", "1", "2", func(m *dicomtest.Meta) {
			m.TransferSyntax, m.Rows, m.Cols, m.Bits, m.Frames, m.Fragments = dicomtest.JPEGBaseline, 8, 8, 8, 2, f.jf
		})),
		bytes.NewReader(inst("1", "1", "3", func(m *dicomtest.Meta) { m.NoPixelData = true; m.Modality = "SR" })),
	)
	if code, _, raw := send(t, srv, "/dicomweb/studies", tok(t, "A", "fhir.write"), ct, body); code != 200 {
		t.Fatalf("STOW: %d %s", code, raw)
	}
	return f
}

func principalA() context.Context {
	return fhirauth.WithPrincipal(context.Background(), &fhirauth.Principal{Subject: "u", Project: "A", Role: fhirauth.RoleWrite})
}

// frameBytes đọc nội dung của Binary frame qua kho resource (có tách project) và kho nội dung.
func frameBytes(t *testing.T, c *bootstrap.Container, ref string) (data []byte, contentType, security string) {
	t.Helper()
	r, err := c.Repo.ReadResource(principalA(), "Binary", strings.TrimPrefix(ref, "Binary/"))
	if err != nil {
		t.Fatalf("%s: %v", ref, err)
	}
	b := r.(*fhir.Binary)
	key, _ := binarystore.Key(b.ID, b.Meta.VersionId)
	rc, err := c.Blobs.Open(context.Background(), key)
	if err != nil {
		t.Fatalf("%s: nội dung: %v", ref, err)
	}
	defer rc.Close()
	data, _ = io.ReadAll(rc)
	if b.SecurityContext != nil {
		security = b.SecurityContext.Reference
	}
	return data, b.ContentType, security
}

func instanceByUID(t *testing.T, c *bootstrap.Container, uid string) *fhir.DicomInstance {
	t.Helper()
	for _, i := range allInstances(t, c) {
		if i.SopInstanceUid == uid {
			return i
		}
	}
	t.Fatalf("không có instance %s", uid)
	return nil
}

func countBinaries(t *testing.T, c *bootstrap.Container) int {
	t.Helper()
	req, _ := fhirsearch.Parse("Binary", nil)
	req.Count, req.Total = 1000, "none"
	res, err := c.Store.Search(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return len(res.Matches)
}

// Quét bù: dựng pixelData cho instance đã lưu từ trước (tính năng đang tắt), đúng nội dung, đúng Content-Type, tách project, idempotent, Force dựng lại và xóa bản cũ.
func TestPixelDataSweep(t *testing.T) {
	c, h := handler(t, settings(t, true))
	srv := httptest.NewServer(h)
	defer srv.Close()
	f := stowFixtures(t, srv)
	if bootstrap.PixelsEnabled(c) {
		t.Fatal("tính năng phải tắt mặc định")
	}
	if n := countBinaries(t, c); n != 3 {
		t.Fatalf("%d Binary sau STOW", n)
	}
	for _, i := range allInstances(t, c) {
		if len(i.PixelData) != 0 {
			t.Fatalf("instance có pixelData khi tính năng tắt")
		}
	}
	ctx := context.Background()
	cfg := dicomretrieve.Config{Repo: c.Store, Blobs: c.Blobs}

	rep, err := dicomretrieve.SweepPixelData(ctx, cfg, dicomretrieve.PixelDataSweepOptions{DryRun: true})
	if err != nil || rep.Scanned != 3 || rep.Built != 2 || rep.NoPixels != 1 || countBinaries(t, c) != 3 {
		t.Fatalf("dry-run: %+v %v", rep, err)
	}
	rep, err = dicomretrieve.SweepPixelData(ctx, cfg, dicomretrieve.PixelDataSweepOptions{})
	if err != nil || rep.Built != 2 || rep.Frames != 5 || rep.Failed != 0 || countBinaries(t, c) != 8 {
		t.Fatalf("quét: %+v %v binaries=%d", rep, err, countBinaries(t, c))
	}

	n := instanceByUID(t, c, f.native)
	if len(n.PixelData) != 3 {
		t.Fatalf("pixelData không nén: %+v", n.PixelData)
	}
	for i, ref := range n.PixelData {
		data, ct, sec := frameBytes(t, c, ref.Reference)
		if !bytes.Equal(data, f.px[i*512:(i+1)*512]) || ct != "application/octet-stream" || sec != "DicomInstance/"+n.ID {
			t.Errorf("frame %d: %d byte, %q, %q", i+1, len(data), ct, sec)
		}
	}
	j := instanceByUID(t, c, f.jpeg)
	for i, ref := range j.PixelData {
		data, ct, _ := frameBytes(t, c, ref.Reference)
		if !bytes.Equal(data, f.jf[i]) || ct != "image/jpeg" {
			t.Errorf("JPEG frame %d: %d byte, %q", i+1, len(data), ct)
		}
	}
	if len(j.PixelData) != 2 || len(instanceByUID(t, c, f.sr).PixelData) != 0 {
		t.Errorf("số pixelData: %d, SR: %d", len(j.PixelData), len(instanceByUID(t, c, f.sr).PixelData))
	}
	// project khác không đọc được Binary frame
	other := fhirauth.WithPrincipal(ctx, &fhirauth.Principal{Subject: "o", Project: "B", Role: fhirauth.RoleWrite})
	if _, err := c.Repo.ReadResource(other, "Binary", strings.TrimPrefix(n.PixelData[0].Reference, "Binary/")); err == nil {
		t.Error("project B đọc được frame của project A")
	}
	// WADO-RS không đổi: vẫn đọc từ tệp gốc
	code, hdr, raw := get(t, srv, "/dicomweb/studies/"+dicomtest.UID("1")+"/series/"+dicomtest.UID("1", "1")+"/instances/"+f.native+"/frames/3", tok(t, "A", "fhir.read"))
	if _, parts, _ := multipartParts(t, hdr, raw); code != 200 || !bytes.Equal(parts[0], f.px[1024:]) {
		t.Errorf("WADO frame 3: %d", code)
	}

	// BuildPixelData trên instance đã đủ không làm gì (không tạo Binary)
	if made, err := dicomretrieve.BuildPixelData(ctx, cfg, instanceByUID(t, c, f.native), dicomretrieve.PixelDataOptions{}); err != nil || made != 0 || countBinaries(t, c) != 8 {
		t.Errorf("đã đủ: %d %v binaries=%d", made, err, countBinaries(t, c))
	}
	if _, err := dicomretrieve.BuildPixelData(ctx, cfg, instanceByUID(t, c, f.sr), dicomretrieve.PixelDataOptions{}); !errors.Is(err, dicomretrieve.ErrNoPixels) {
		t.Errorf("không có ảnh: %v", err)
	}
	// idempotent
	rep, _ = dicomretrieve.SweepPixelData(ctx, cfg, dicomretrieve.PixelDataSweepOptions{})
	if rep.Built != 0 || rep.UpToDate != 2 || countBinaries(t, c) != 8 {
		t.Errorf("chạy lại: %+v binaries=%d", rep, countBinaries(t, c))
	}
	// Force: Binary mới, Binary cũ bị xóa (cả nội dung)
	oldRef := n.PixelData[0].Reference
	oldBin, _ := c.Store.ReadResource(ctx, "Binary", strings.TrimPrefix(oldRef, "Binary/"))
	oldKey, _ := binarystore.Key(oldBin.GetID(), oldBin.GetMeta().VersionId)
	rep, err = dicomretrieve.SweepPixelData(ctx, cfg, dicomretrieve.PixelDataSweepOptions{PixelDataOptions: dicomretrieve.PixelDataOptions{Force: true}, Limit: 1})
	if err != nil || rep.Built != 1 || countBinaries(t, c) != 8 {
		t.Fatalf("force: %+v %v binaries=%d", rep, err, countBinaries(t, c))
	}
	rebuilt := 0
	for _, uid := range []string{f.native, f.jpeg} {
		i := instanceByUID(t, c, uid)
		if len(i.PixelData) > 0 && (uid != f.native || i.PixelData[0].Reference != oldRef) {
			rebuilt++
		}
	}
	if rebuilt == 0 {
		t.Error("force không dựng lại")
	}
	if i := instanceByUID(t, c, f.native); i.PixelData[0].Reference != oldRef {
		if _, err := c.Blobs.Open(ctx, oldKey); !errors.Is(err, binarystore.ErrNotFound) {
			t.Errorf("nội dung frame cũ phải bị xóa: %v", err)
		}
	}
}

// flakyBlobs làm Put của frame lỗi từ lần thứ failAt (đếm từ 1) trong khi failing.
type flakyBlobs struct {
	binarystore.Store
	puts    atomic.Int32
	first   atomic.Int64 // thời điểm (UnixNano) của lần lỗi đầu tiên
	failAt  int32
	failing atomic.Bool
}

func (f *flakyBlobs) Put(ctx context.Context, key string, r io.Reader, o binarystore.PutOptions) (int64, error) {
	if f.failing.Load() && o.ContentType != "application/dicom" && f.puts.Add(1) >= f.failAt { // chỉ làm lỗi việc ghi frame, không đụng nội dung gốc của STOW
		f.first.CompareAndSwap(0, time.Now().UnixNano())
		_, _ = io.CopyN(io.Discard, r, 3)
		return 0, errors.New("kho lỗi")
	}
	return f.Store.Put(ctx, key, r, o)
}

// Lỗi giữa chừng (frame thứ hai không ghi được) không để lại Binary hay nội dung mồ côi và không đổi pixelData; một ghi song song làm đổi phiên bản cũng không để lại gì.
func TestPixelDataFailuresLeaveNothingBehind(t *testing.T) {
	c, h := handler(t, settings(t, true))
	srv := httptest.NewServer(h)
	defer srv.Close()
	f := stowFixtures(t, srv)
	ctx := context.Background()
	inner := c.Blobs
	flaky := &flakyBlobs{Store: inner, failAt: 2}
	c.Blobs = flaky
	listKeys := func() int {
		l, ok := inner.(binarystore.Lister)
		if !ok {
			return -1
		}
		n := 0
		_ = l.List(ctx, func(binarystore.Entry) error { n++; return nil })
		return n
	}
	before, beforeKeys := countBinaries(t, c), listKeys()
	flaky.failing.Store(true)
	cfg := dicomretrieve.Config{Repo: c.Store, Blobs: c.Blobs}
	rep, _ := dicomretrieve.SweepPixelData(ctx, cfg, dicomretrieve.PixelDataSweepOptions{})
	if rep.Failed != 2 || rep.Built != 0 {
		t.Fatalf("lỗi kho: %+v", rep)
	}
	if countBinaries(t, c) != before || listKeys() != beforeKeys {
		t.Errorf("còn rác: Binary %d→%d, nội dung %d→%d", before, countBinaries(t, c), beforeKeys, listKeys())
	}
	if len(instanceByUID(t, c, f.native).PixelData) != 0 {
		t.Error("pixelData bị đổi khi lỗi")
	}
	flaky.failing.Store(false)

	// ghi song song chen vào giữa lúc cập nhật instance
	racing := bumpRepo{Repository: c.Store, bump: func(r fhir.Resource) {
		cur, _ := c.Store.ReadResource(ctx, "DicomInstance", r.GetID())
		ci := cur.(*fhir.DicomInstance)
		ci.Extension = append(ci.Extension, fhir.Extension{URL: "urn:test:race", ValueString: "x"})
		if _, err := c.Store.UpdateResource(ctx, ci, fhirrouter.UpdateOptions{IfMatch: ci.Meta.VersionId}); err != nil {
			t.Error(err)
		}
	}}
	rep, _ = dicomretrieve.SweepPixelData(ctx, dicomretrieve.Config{Repo: racing, Blobs: c.Blobs}, dicomretrieve.PixelDataSweepOptions{})
	if rep.Failed != 2 || countBinaries(t, c) != before || listKeys() != beforeKeys {
		t.Errorf("tranh chấp: %+v Binary %d nội dung %d (muốn %d, %d)", rep, countBinaries(t, c), listKeys(), before, beforeKeys)
	}
	// hết lỗi thì dựng được
	if rep, _ = dicomretrieve.SweepPixelData(ctx, cfg, dicomretrieve.PixelDataSweepOptions{}); rep.Built != 2 || rep.Failed != 0 {
		t.Errorf("sau khi hết lỗi: %+v", rep)
	}
}

type bumpRepo struct {
	fhirrouter.Repository
	bump func(fhir.Resource)
}

func (b bumpRepo) UpdateResource(ctx context.Context, r fhir.Resource, o fhirrouter.UpdateOptions) (fhir.Resource, error) {
	if _, ok := r.(*fhir.DicomInstance); ok {
		b.bump(r)
	}
	return b.Repository.UpdateResource(ctx, r, o)
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("hết hạn chờ: %s", what)
}

// Bật DICOMWEB_PIXELDATA_FRAMES: STOW xong thì worker nền dựng pixelData bằng quyền của người STOW; tải lại cùng instance không dựng lại; lỗi tạm thời được thử lại; tắt êm chờ việc xong.
func TestPixelDataWorkerAfterStow(t *testing.T) {
	c, h := handler(t, settings(t, true, func(s *config.Settings) { s.PixelDataFrames, s.PixelDataWorkers = true, 2 }))
	srv := httptest.NewServer(h)
	defer srv.Close()
	if !bootstrap.PixelsEnabled(c) {
		t.Fatal("worker phải bật")
	}
	bootstrap.SetPixelBackoff(c, func(int) time.Duration { return 60 * time.Millisecond })
	flaky := &flakyBlobs{Store: c.Blobs, failAt: 1}
	c.Blobs = flaky

	// STOW bình thường: worker dựng đủ cho cả hai instance có ảnh, không dựng cho SR
	f := stowFixtures(t, srv)
	waitFor(t, "pixelData của hai instance", func() bool {
		return len(instanceByUID(t, c, f.native).PixelData) == 3 && len(instanceByUID(t, c, f.jpeg).PixelData) == 2
	})
	n := instanceByUID(t, c, f.native)
	if data, ct, sec := frameBytes(t, c, n.PixelData[2].Reference); !bytes.Equal(data, f.px[1024:]) || ct != "application/octet-stream" || sec != "DicomInstance/"+n.ID {
		t.Errorf("frame 3: %d byte %q %q", len(data), ct, sec)
	}
	if len(instanceByUID(t, c, f.sr).PixelData) != 0 {
		t.Error("SR không có ảnh")
	}
	total := countBinaries(t, c)
	// tải lại: instance đã có nên không dựng lại
	stowFixtures(t, srv)
	time.Sleep(200 * time.Millisecond)
	if countBinaries(t, c) != total {
		t.Errorf("tải lại tạo thêm Binary: %d → %d", total, countBinaries(t, c))
	}

	// lỗi tạm thời: Put đầu tiên lỗi, lần thử lại thành công
	flaky.failing.Store(true)
	flaky.failAt = 1
	go func() {
		for flaky.puts.Load() < 1 { // chờ thấy lần lỗi đầu rồi mới hết lỗi: bảo đảm lần thử lại thật sự xảy ra
			time.Sleep(time.Millisecond)
		}
		flaky.failing.Store(false)
	}()
	ct, body := stowBody(bytes.NewReader(inst("2", "1", "1", func(m *dicomtest.Meta) { m.Rows, m.Cols, m.Bits, m.Pixel = 8, 8, 8, make([]byte, 64) })))
	if code, _, raw := send(t, srv, "/dicomweb/studies", tok(t, "A", "fhir.write"), ct, body); code != 200 {
		t.Fatalf("STOW: %d %s", code, raw)
	}
	waitFor(t, "instance mới có pixelData sau lần thử lại", func() bool {
		for _, i := range allInstances(t, c) {
			if i.SopInstanceUid == dicomtest.UID("2", "1", "1") {
				return len(i.PixelData) == 1
			}
		}
		return false
	})
	// lần thử lại phải chờ theo hạn lùi (60 ms), không dồn dập
	if gap := time.Since(time.Unix(0, flaky.first.Load())); gap < 55*time.Millisecond {
		t.Errorf("thử lại sau %v, muốn chờ ít nhất 60 ms", gap)
	}
	// tắt êm
	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Stop(sctx); err != nil {
		t.Errorf("Stop: %v", err)
	}
}
