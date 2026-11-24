// Package dicomstow là STOW-RS (PS3.18 10.5): nhận multipart/related chứa các tệp DICOM, lưu nội dung vào kho đối tượng và metadata thành resource FHIR
// (DicomStudy, DicomSeries, DicomInstance, Binary), rồi trả kết quả theo chuẩn. Không biết HTTP: nhận Content-Type và luồng thân yêu cầu, trả mã trạng thái
// và dataset phản hồi (DICOM JSON).
//
// Mỗi phần (một instance) đi qua các bước sau, và chỉ bước cuối mới làm dữ liệu xuất hiện với người dùng khác:
//
//  1. Đọc phần đầu (mọi thứ trước PixelData, tối đa MaxHeaderBytes) vào bộ nhớ và quét bằng dicomweb.Scan: từ chối tệp không phải DICOM, SQ lồng quá sâu (tệp 60 KB
//     làm thư viện DICOM dùng 2,2 GB), phần đầu quá lớn, transfer syntax không hỗ trợ (docs/plans/cds-dicom/d0-spike.md 3.1).
//  2. Đọc metadata bằng dicomread (SkipPixelData), kiểm các UID, kiểm study khi POST vào một study cụ thể.
//  3. Instance đã có (cùng SOPInstanceUID) thì coi là thành công mà không lưu lại nội dung: tải lại là idempotent, không tạo Binary mồ côi (Medplum tạo một Binary
//     mới rồi bỏ rơi).
//  4. Tạo Binary rồi ghi nội dung theo luồng (phần đầu đã đọc rồi phần còn lại) vào kho, đồng thời lập chỉ mục frame (ảnh không nén: từ phần đầu; ảnh đóng gói: đọc các
//     fragment song song với việc ghi, không đọc lại tệp); kiểm tệp không bị cắt giữa PixelData (không nén: so số byte nhận với độ dài khai báo; đóng gói: phải đọc
//     tới Sequence Delimitation; d0-spike.md 3.3); lỗi thì xóa Binary và nội dung. Chỉ mục lưu ở extension của DicomInstance.
//  5. Trong một transaction có khóa theo UID: tạo DicomStudy, DicomSeries, DicomInstance nếu chưa có (tải song song cùng study không tạo trùng).
//
// Sau khi xử lý hết các phần, tính lại số liệu tổng hợp của từng study và series chạm tới (idempotent, có điều kiện ifMatch, thử tối đa 3 lần; lỗi chỉ ghi log vì
// instance đã lưu và lần tải sau sẽ tính lại).
package dicomstow

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"trivita-fhir/pkg/binarystore"
	"trivita-fhir/pkg/dicomread"
	"trivita-fhir/pkg/dicomweb"
	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirrouter"
	"trivita-fhir/pkg/fhirsearch"
)

// Lý do thất bại của một instance (FailureReason (0008,1197), PS3.18 Table 10.5.3-1).
const (
	ReasonProcessingFailure uint16 = 0x0110
	ReasonOutOfResources    uint16 = 0xA700
	ReasonDataSetMismatch   uint16 = 0xA900
	ReasonCannotUnderstand  uint16 = 0xC000
)

// Config là phụ thuộc và giới hạn của dịch vụ.
type Config struct {
	// Repo là kho resource của người gọi (đã tách theo project và kiểm quyền: fhirauth.Scope).
	Repo fhirrouter.Repository
	// Blobs là kho nội dung (trùng với kho Binary của fhir-server).
	Blobs binarystore.Store
	// MaxInstanceBytes là trần một instance (vượt thì instance đó thất bại với ReasonOutOfResources). 0: không trần.
	MaxInstanceBytes int64
	// MaxHeaderBytes là trần phần đầu DICOM. Mặc định 1 MiB.
	MaxHeaderBytes int64
}

// Request là một yêu cầu STOW.
type Request struct {
	ContentType string    // header Content-Type của yêu cầu
	Body        io.Reader // thân yêu cầu
	StudyUID    string    // khác rỗng: mọi instance phải thuộc study này (POST /studies/{uid})
	// RetrieveBase là gốc URL WADO-RS kèm tiền tố, ví dụ https://dicom.example.org/dicomweb; dùng dựng RetrieveURL trong phản hồi.
	RetrieveBase string
}

// RequestError là lỗi của cả yêu cầu (không phải của một instance), kèm mã HTTP.
type RequestError struct {
	Status int
	Msg    string
}

func (e *RequestError) Error() string { return e.Msg }

// Stored là một instance đã lưu (hoặc đã có sẵn).
type Stored struct {
	InstanceID, StudyID, SeriesID                    string
	StudyUID, SeriesUID, SOPClassUID, SOPInstanceUID string
	AlreadyStored                                    bool // đã có từ trước: không lưu lại nội dung
}

// Failed là một instance thất bại; UID có thể rỗng nếu chưa đọc được.
type Failed struct {
	SOPClassUID, SOPInstanceUID string
	Reason                      uint16
	Message                     string
	abort                       bool // luồng yêu cầu đã hỏng: không đọc tiếp các phần sau
}

// Outcome là kết quả của Store.
type Outcome struct {
	// Status: 200 mọi instance thành công, 202 một phần, 409 không instance nào thành công (PS3.18 10.5.3.2).
	Status   int
	Stored   []Stored
	Failed   []Failed
	Response dicomweb.Dataset // phản hồi DICOM JSON
}

var uidPattern = regexp.MustCompile(`^[0-9]+(\.[0-9]+)*$`)

// Store xử lý một yêu cầu STOW-RS.
func Store(ctx context.Context, cfg Config, req Request) (*Outcome, error) {
	if cfg.MaxHeaderBytes <= 0 {
		cfg.MaxHeaderBytes = 1 << 20
	}
	boundary, err := boundaryOf(req.ContentType)
	if err != nil {
		return nil, err
	}
	s := &service{cfg: cfg, req: req, touchedStudies: map[string]bool{}, touchedSeries: map[string]bool{}}
	mr := multipart.NewReader(req.Body, boundary)
	out := &Outcome{}
	parts := 0
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			if isTooLarge(err) {
				s.cleanupAggregates(ctx)
				return nil, &RequestError{http.StatusRequestEntityTooLarge, "Body quá lớn"}
			}
			if parts == 0 {
				return nil, &RequestError{http.StatusBadRequest, "Thân multipart không hợp lệ"}
			}
			out.Failed = append(out.Failed, Failed{Reason: ReasonProcessingFailure, Message: "Thân yêu cầu bị cắt hoặc hỏng giữa chừng"})
			break
		}
		parts++
		st, fail, fatal := s.storeOne(ctx, part)
		_ = part.Close()
		if fatal != nil {
			s.cleanupAggregates(ctx)
			return nil, fatal
		}
		if fail != nil {
			out.Failed = append(out.Failed, *fail)
			if fail.abort {
				break
			}
			continue
		}
		out.Stored = append(out.Stored, *st)
	}
	if parts == 0 {
		return nil, &RequestError{http.StatusBadRequest, "Yêu cầu không có instance nào"}
	}
	s.updateAggregates(ctx)
	out.Status = http.StatusOK
	switch {
	case len(out.Failed) > 0 && len(out.Stored) > 0:
		out.Status = http.StatusAccepted
	case len(out.Failed) > 0:
		out.Status = http.StatusConflict
	}
	out.Response = response(out, req)
	return out, nil
}

var boundaryRE = regexp.MustCompile(`(?i)boundary\s*=\s*"?([^";]+)"?`)

// boundaryOf lấy boundary của multipart/related. mime.ParseMediaType từ chối `type=application/dicom` không đặt trong nháy kép (PS3.18 bắt buộc nháy kép, nhưng
// có client gửi sai), nên khi nó lỗi thì tự rút boundary.
func boundaryOf(contentType string) (string, error) {
	mt, params, err := mime.ParseMediaType(contentType)
	if err == nil {
		if mt != "multipart/related" {
			return "", &RequestError{http.StatusUnsupportedMediaType, "Cần Content-Type multipart/related"}
		}
		if params["boundary"] == "" {
			return "", &RequestError{http.StatusBadRequest, "Thiếu boundary"}
		}
		return params["boundary"], nil
	}
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(contentType)), "multipart/related") {
		return "", &RequestError{http.StatusUnsupportedMediaType, "Cần Content-Type multipart/related"}
	}
	if m := boundaryRE.FindStringSubmatch(contentType); m != nil {
		return strings.TrimSpace(m[1]), nil
	}
	return "", &RequestError{http.StatusBadRequest, "Thiếu boundary"}
}

type service struct {
	cfg            Config
	req            Request
	touchedStudies map[string]bool // id DicomStudy
	touchedSeries  map[string]bool // id DicomSeries
}

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// sourceReader ghi lại lỗi đầu tiên của nguồn (thân yêu cầu), để phân biệt với lỗi của kho.
type sourceReader struct {
	r   io.Reader
	err error
}

func (s *sourceReader) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	if err != nil && err != io.EOF && s.err == nil {
		s.err = err
	}
	return n, err
}

// tailReader đếm số byte đã đi qua.
type tailReader struct {
	r io.Reader
	n int64
}

func (t *tailReader) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	t.n += int64(n)
	return n, err
}

// indexer lập chỉ mục frame của PixelData đóng gói ngay trong lúc nội dung được ghi vào kho: nhận cùng các byte (qua io.Pipe, nối bằng TeeReader), bỏ qua tới đầu PixelData
// rồi đọc các fragment tới Sequence Delimitation. Không đọc lại tệp và bộ nhớ hằng số. Luôn xả hết phần còn lại của luồng để không chặn đường ghi.
type indexer struct {
	pw   *io.PipeWriter
	done chan indexResult
}

type indexResult struct {
	frames []dicomweb.FrameRange
	end    int64 // vị trí ngay sau Sequence Delimitation (0 nếu cấu trúc hỏng hoặc bị cắt)
	err    error
}

func startIndexer(pixelOffset int64, frames int) *indexer {
	pr, pw := io.Pipe()
	ix := &indexer{pw: pw, done: make(chan indexResult, 1)}
	go func() {
		var res indexResult
		if _, err := io.CopyN(io.Discard, pr, pixelOffset); err != nil {
			res.err = err
		} else {
			res.frames, res.end, res.err = dicomweb.IndexEncapsulated(pr, pixelOffset, frames)
		}
		_, _ = io.Copy(io.Discard, pr)
		ix.done <- res
	}()
	return ix
}

// finish đóng luồng (kết thúc phía ghi) và chờ kết quả.
func (ix *indexer) finish(putErr error) indexResult {
	if putErr != nil {
		_ = ix.pw.CloseWithError(putErr)
	} else {
		_ = ix.pw.Close()
	}
	return <-ix.done
}

func fail(reason uint16, msg string, ds dicomweb.Dataset) *Failed {
	f := &Failed{Reason: reason, Message: msg}
	if ds != nil {
		f.SOPClassUID, f.SOPInstanceUID = ds.Str("00080016"), ds.Str("00080018")
	}
	return f
}

// fatalOf đổi lỗi của kho thành lỗi cả yêu cầu khi là lỗi quyền (401, 403): không phải lỗi của riêng instance.
func fatalOf(err error) *RequestError {
	var fe *fhirrouter.Error
	if errors.As(err, &fe) && (fe.Status == http.StatusUnauthorized || fe.Status == http.StatusForbidden) {
		msg := "Không đủ quyền"
		if fe.Status == http.StatusUnauthorized {
			msg = "Chưa xác thực"
		}
		return &RequestError{fe.Status, msg}
	}
	return nil
}

func (s *service) storeOne(ctx context.Context, part *multipart.Part) (*Stored, *Failed, *RequestError) {
	if ct := part.Header.Get("Content-Type"); ct != "" {
		if mt, _, err := mime.ParseMediaType(ct); err != nil || (mt != "application/dicom" && mt != "application/octet-stream") {
			return nil, fail(ReasonCannotUnderstand, "Phần phải có Content-Type application/dicom", nil), nil
		}
	}
	src := &sourceReader{r: part}
	var head bytes.Buffer
	scan, err := dicomweb.Scan(io.TeeReader(src, &head), dicomweb.ScanOptions{HeaderOnly: true, MaxHeaderBytes: s.cfg.MaxHeaderBytes})
	if isTooLarge(src.err) {
		return nil, nil, &RequestError{http.StatusRequestEntityTooLarge, "Body quá lớn"}
	}
	if src.err != nil {
		f := fail(ReasonProcessingFailure, sourceMsg(src.err), nil)
		f.abort = true
		return nil, f, nil
	}
	if err != nil {
		reason := ReasonCannotUnderstand
		if errors.Is(err, dicomweb.ErrHeaderTooLarge) {
			reason = ReasonOutOfResources
		}
		return nil, fail(reason, "Tệp DICOM không hợp lệ: "+errKind(err), nil), nil
	}
	hb := head.Bytes()
	if scan.HasPixelData {
		hb = hb[:scan.PixelHeaderOffset]
	}
	ds, err := dicomread.ReadHeader(hb)
	if err != nil {
		return nil, fail(ReasonCannotUnderstand, "Không đọc được metadata DICOM", nil), nil
	}
	if ds.Str("00080016") == "" {
		ds["00080016"] = dicomweb.Element{VR: "UI", Value: []any{ds.Str("00020002")}}
	}
	for _, u := range []string{dicomweb.TagStudyInstanceUID, dicomweb.TagSeriesInstanceUID, "00080018", "00080016"} {
		if v := ds.Str(u); len(v) > 64 || !uidPattern.MatchString(v) {
			return nil, fail(ReasonCannotUnderstand, "Thiếu hoặc sai UID ("+u+")", ds), nil
		}
	}
	studyUID, seriesUID, sopUID, classUID := ds.Str(dicomweb.TagStudyInstanceUID), ds.Str(dicomweb.TagSeriesInstanceUID), ds.Str("00080018"), ds.Str("00080016")
	if s.req.StudyUID != "" && s.req.StudyUID != studyUID {
		return nil, fail(ReasonDataSetMismatch, "Instance không thuộc study của yêu cầu", ds), nil
	}
	cleaned := dicomweb.CleanDataset(ds)
	metadata, err := json.Marshal(cleaned)
	if err != nil {
		return nil, fail(ReasonProcessingFailure, "Không mã hóa được metadata", ds), nil
	}

	// instance đã có: idempotent
	if existing, err := findOne(ctx, s.cfg.Repo, "DicomInstance", "sop-instance-uid", sopUID); err != nil {
		if f := fatalOf(err); f != nil {
			return nil, nil, f
		}
		return nil, fail(ReasonProcessingFailure, "Không kiểm được instance đã có", ds), nil
	} else if inst, ok := existing.(*fhir.DicomInstance); ok {
		st := s.storedOf(inst, studyUID, seriesUID, classUID, sopUID, true)
		return st, nil, nil
	}

	instanceID := newID()
	bin, err := s.cfg.Repo.CreateResource(ctx, &fhir.Binary{ID: newID(), ContentType: "application/dicom", SecurityContext: &fhir.Reference{Reference: "DicomInstance/" + instanceID}})
	if err != nil {
		if f := fatalOf(err); f != nil {
			return nil, nil, f
		}
		slog.ErrorContext(ctx, "dicomstow: tạo Binary lỗi", "error", err)
		return nil, fail(ReasonProcessingFailure, "Không tạo được Binary", ds), nil
	}
	binID, ver := bin.GetID(), ""
	if m := bin.GetMeta(); m != nil {
		ver = m.VersionId
	}
	key, err := binarystore.Key(binID, ver)
	if err != nil {
		s.dropBinary(ctx, binID, "")
		return nil, fail(ReasonProcessingFailure, "Khóa kho không hợp lệ", ds), nil
	}
	tail := &tailReader{r: io.MultiReader(bytes.NewReader(head.Bytes()), src)}
	var upload io.Reader = tail
	var ix *indexer
	frames := 1
	if n := ds.Int("00280008"); n != nil && *n > 0 {
		frames = *n
	}
	if scan.HasPixelData && scan.Encapsulated {
		ix = startIndexer(scan.PixelOffset, frames)
		upload = io.TeeReader(tail, ix.pw)
	}
	_, putErr := s.cfg.Blobs.Put(ctx, key, upload, binarystore.PutOptions{ContentType: "application/dicom", MaxBytes: s.cfg.MaxInstanceBytes})
	var enc indexResult
	if ix != nil {
		enc = ix.finish(putErr)
	}
	if err := putErr; err != nil {
		s.dropBinary(ctx, binID, key)
		switch {
		case isTooLarge(src.err):
			return nil, nil, &RequestError{http.StatusRequestEntityTooLarge, "Body quá lớn"}
		case src.err != nil:
			f := fail(ReasonProcessingFailure, sourceMsg(src.err), ds)
			f.abort = true
			return nil, f, nil
		case errors.Is(err, binarystore.ErrTooLarge):
			return nil, fail(ReasonOutOfResources, "Instance vượt trần kích thước", ds), nil
		}
		slog.ErrorContext(ctx, "dicomstow: ghi nội dung lỗi", "binary", binID, "error", err)
		return nil, fail(ReasonProcessingFailure, "Không ghi được nội dung", ds), nil
	}
	index := dicomweb.FrameIndex{TransferSyntax: scan.TransferSyntax, Frames: scan.Frames}
	if scan.HasPixelData && scan.Encapsulated {
		// chỉ mục đọc được tới Sequence Delimitation nghĩa là tệp không bị cắt; không tách được frame (ErrUnsplittable) thì vẫn lưu, chỉ không có chỉ mục
		switch {
		case enc.err == nil:
			index.Frames = enc.frames
		case errors.Is(enc.err, dicomweb.ErrUnsplittable) && enc.end > 0 && tail.n >= enc.end:
			index.Frames = nil
		default:
			s.dropBinary(ctx, binID, key)
			return nil, fail(ReasonCannotUnderstand, "Tệp bị cắt giữa PixelData hoặc PixelData đóng gói không hợp lệ", ds), nil
		}
	} else if truncated(scan, tail) {
		s.dropBinary(ctx, binID, key)
		return nil, fail(ReasonCannotUnderstand, "Tệp bị cắt giữa PixelData", ds), nil
	}
	encoded, _ := index.Encode()

	inst, study, series, created, err := s.persist(ctx, ds, studyUID, seriesUID, sopUID, classUID, instanceID, binID, string(metadata), encoded)
	if err != nil {
		s.dropBinary(ctx, binID, key)
		if f := fatalOf(err); f != nil {
			return nil, nil, f
		}
		slog.ErrorContext(ctx, "dicomstow: lưu resource lỗi", "error", err)
		return nil, fail(ReasonProcessingFailure, "Không lưu được resource", ds), nil
	}
	if !created { // thua cuộc đua với một lần tải song song cùng instance: bỏ nội dung vừa ghi
		s.dropBinary(ctx, binID, key)
	}
	s.touchedStudies[study.ID] = true
	s.touchedSeries[series.ID] = true
	return &Stored{InstanceID: inst.ID, StudyID: study.ID, SeriesID: series.ID, StudyUID: studyUID, SeriesUID: seriesUID, SOPClassUID: classUID, SOPInstanceUID: sopUID, AlreadyStored: !created}, nil, nil
}

func (s *service) storedOf(inst *fhir.DicomInstance, studyUID, seriesUID, classUID, sopUID string, already bool) *Stored {
	st := &Stored{InstanceID: inst.ID, StudyUID: studyUID, SeriesUID: seriesUID, SOPClassUID: classUID, SOPInstanceUID: sopUID, AlreadyStored: already}
	if inst.Study != nil {
		st.StudyID = strings.TrimPrefix(inst.Study.Reference, "DicomStudy/")
	}
	if inst.Series != nil {
		st.SeriesID = strings.TrimPrefix(inst.Series.Reference, "DicomSeries/")
	}
	return st
}

// truncated cho biết tệp không nén đã nhận ít hơn PixelData khai báo (ảnh đóng gói được kiểm bằng chỉ mục frame: phải đọc tới Sequence Delimitation).
func truncated(scan *dicomweb.ScanResult, t *tailReader) bool {
	return scan.HasPixelData && scan.PixelLength >= 0 && t.n < scan.PixelOffset+scan.PixelLength
}

// isTooLarge: thân yêu cầu vượt trần kích thước của cả yêu cầu (http.MaxBytesReader). Là lỗi của cả yêu cầu (413), không phải của một instance; những instance
// đã lưu trước đó vẫn được giữ và tính số liệu.
func isTooLarge(err error) bool {
	var mbe *http.MaxBytesError
	return errors.As(err, &mbe)
}

func sourceMsg(err error) string { return "đọc thân yêu cầu lỗi" }

func errKind(err error) string {
	switch {
	case errors.Is(err, dicomweb.ErrNotDICOM):
		return "không phải DICOM"
	case errors.Is(err, dicomweb.ErrTooDeep):
		return "SQ lồng quá sâu"
	case errors.Is(err, dicomweb.ErrHeaderTooLarge):
		return "phần đầu quá lớn"
	case errors.Is(err, dicomweb.ErrUnsupportedSyntax):
		return "transfer syntax không hỗ trợ"
	}
	return "cấu trúc hỏng"
}

// dropBinary xóa Binary vừa tạo và nội dung đã ghi (nếu có); lỗi chỉ ghi log: kho mồ côi do tiến trình chết cần công cụ quét (fhirbinary.Sweep).
func (s *service) dropBinary(ctx context.Context, binID, key string) {
	ctx = context.WithoutCancel(ctx)
	if key != "" {
		if err := s.cfg.Blobs.Delete(ctx, key); err != nil {
			slog.WarnContext(ctx, "dicomstow: không xóa được nội dung", "binary", binID, "error", err)
		}
	}
	// Việc dọn dẹp được thử lại vài lần: dưới tải song song, SQLite (chỉ dev) trả SQLITE_BUSY ngay khi nâng khóa đọc thành ghi, và Binary mồ côi là rác thật.
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		if err = s.cfg.Repo.DeleteResource(ctx, "Binary", binID); err == nil {
			return
		}
		var fe *fhirrouter.Error
		if errors.As(err, &fe) && (fe.Status == http.StatusNotFound || fe.Status == http.StatusGone || fe.Status == http.StatusForbidden) {
			break
		}
		time.Sleep(time.Duration(10<<attempt) * time.Millisecond)
	}
	slog.WarnContext(ctx, "dicomstow: không xóa được Binary", "binary", binID, "error", err)
}

// ---- resource ----

func uidSearch(rt, param, uid string) (*fhirsearch.Request, error) {
	r, err := fhirsearch.Parse(rt, url.Values{param + ":exact": {uid}, "_count": {"1"}})
	if err != nil {
		return nil, err
	}
	r.Total = "none"
	return r, nil
}

func findOne(ctx context.Context, repo fhirrouter.Repository, rt, param, uid string) (fhir.Resource, error) {
	req, err := uidSearch(rt, param, uid)
	if err != nil {
		return nil, err
	}
	res, err := repo.Search(ctx, req)
	if err != nil {
		return nil, err
	}
	if len(res.Matches) == 0 {
		return nil, nil
	}
	return res.Matches[0], nil
}

func lock(ctx context.Context, repo fhirrouter.Repository, key string) error {
	if kl, ok := repo.(fhirrouter.KeyLocker); ok {
		return kl.LockKey(ctx, key)
	}
	return nil
}

// persist tạo (nếu chưa có) study, series, instance trong một transaction; khóa theo UID theo thứ tự cố định (study, series, instance) nên tải song song
// không tạo trùng và không bế tắc. created=false nghĩa là instance đã được một yêu cầu khác tạo trước.
func (s *service) persist(ctx context.Context, ds dicomweb.Dataset, studyUID, seriesUID, sopUID, classUID, instanceID, binID, metadata, frameIndex string) (inst *fhir.DicomInstance, study *fhir.DicomStudy, series *fhir.DicomSeries, created bool, err error) {
	run := func(rp fhirrouter.Repository) error {
		if err := lock(ctx, rp, "dicomweb|study|"+studyUID); err != nil {
			return err
		}
		r, err := findOne(ctx, rp, "DicomStudy", "study-instance-uid", studyUID)
		if err != nil {
			return err
		}
		if study, _ = r.(*fhir.DicomStudy); study == nil {
			ns := dicomweb.StudyFromDataset(ds)
			ns.ID = newID()
			c, err := rp.CreateResource(ctx, ns)
			if err != nil {
				return err
			}
			study = c.(*fhir.DicomStudy)
		}
		if err := lock(ctx, rp, "dicomweb|series|"+seriesUID); err != nil {
			return err
		}
		r, err = findOne(ctx, rp, "DicomSeries", "series-instance-uid", seriesUID)
		if err != nil {
			return err
		}
		if series, _ = r.(*fhir.DicomSeries); series == nil {
			nse := dicomweb.SeriesFromDataset("DicomStudy/"+study.ID, ds)
			nse.ID = newID()
			c, err := rp.CreateResource(ctx, nse)
			if err != nil {
				return err
			}
			series = c.(*fhir.DicomSeries)
		}
		if err := lock(ctx, rp, "dicomweb|instance|"+sopUID); err != nil {
			return err
		}
		r, err = findOne(ctx, rp, "DicomInstance", "sop-instance-uid", sopUID)
		if err != nil {
			return err
		}
		if inst, _ = r.(*fhir.DicomInstance); inst != nil {
			created = false
			return nil
		}
		ni := &fhir.DicomInstance{
			ID: instanceID, Study: &fhir.Reference{Reference: "DicomStudy/" + study.ID}, Series: &fhir.Reference{Reference: "DicomSeries/" + series.ID},
			Raw: &fhir.Reference{Reference: "Binary/" + binID}, Metadata: metadata, SopClassUid: classUID, SopInstanceUid: sopUID,
			InstanceAvailability: ds.Str("00080056"), TimezoneOffsetFromUtc: ds.Str(dicomweb.TagTimezoneOffsetFromUTC),
			InstanceNumber: "1", Rows: ds.Int("00280010"), Columns: ds.Int("00280011"), BitsAllocated: ds.Int("00280100"), NumberOfFrames: ds.Int("00280008"),
		}
		if n := ds.Int("00200013"); n != nil {
			ni.InstanceNumber = fmt.Sprint(*n)
		}
		if frameIndex != "" { // chỉ mục frame (D5): WADO-RS đọc frame theo khoảng mà không quét tệp
			ni.Extension = []fhir.Extension{{URL: dicomweb.FrameIndexURL, ValueString: frameIndex}}
		}
		c, err := rp.CreateResource(ctx, ni)
		if err != nil {
			return err
		}
		inst, created = c.(*fhir.DicomInstance), true
		return nil
	}
	if tx, ok := s.cfg.Repo.(fhirrouter.Transactional); ok {
		err = tx.RunInTransaction(ctx, run)
	} else {
		err = run(s.cfg.Repo)
	}
	return inst, study, series, created, err
}

// ---- số liệu tổng hợp ----

const maxUpdateAttempts = 3

func (s *service) updateAggregates(ctx context.Context) {
	for id := range s.touchedStudies {
		if err := updateStudy(ctx, s.cfg.Repo, id); err != nil {
			slog.ErrorContext(ctx, "dicomstow: không cập nhật được số liệu tổng hợp của study", "study", id, "error", err)
		}
	}
	for id := range s.touchedSeries {
		if err := updateSeries(ctx, s.cfg.Repo, id); err != nil {
			slog.ErrorContext(ctx, "dicomstow: không cập nhật được số liệu tổng hợp của series", "series", id, "error", err)
		}
	}
}

// cleanupAggregates vẫn cập nhật số liệu cho những gì đã lưu trước khi yêu cầu bị dừng giữa chừng.
func (s *service) cleanupAggregates(ctx context.Context) {
	s.updateAggregates(context.WithoutCancel(ctx))
}

func countInstances(ctx context.Context, repo fhirrouter.Repository, param, ref string) (int, error) {
	req, err := fhirsearch.Parse("DicomInstance", url.Values{param: {ref}, "_count": {"1"}})
	if err != nil {
		return 0, err
	}
	req.Total = "accurate"
	res, err := repo.Search(ctx, req)
	if err != nil {
		return 0, err
	}
	if res.Total == nil {
		return 0, errors.New("dicomstow: kho không trả tổng số")
	}
	return *res.Total, nil
}

func isConflict(err error) bool {
	var fe *fhirrouter.Error
	return errors.As(err, &fe) && fe.Status == http.StatusPreconditionFailed
}

func updateStudy(ctx context.Context, repo fhirrouter.Repository, id string) error {
	for attempt := 0; attempt < maxUpdateAttempts; attempt++ {
		r, err := repo.ReadResource(ctx, "DicomStudy", id)
		if err != nil {
			return err
		}
		study := r.(*fhir.DicomStudy)
		var series []*fhir.DicomSeries
		for offset := 0; ; offset += 1000 {
			req, err := fhirsearch.Parse("DicomSeries", url.Values{"study": {"DicomStudy/" + id}})
			if err != nil {
				return err
			}
			req.Count, req.Offset, req.Total = 1000, offset, "none"
			res, err := repo.Search(ctx, req)
			if err != nil {
				return err
			}
			for _, m := range res.Matches {
				if se, ok := m.(*fhir.DicomSeries); ok {
					series = append(series, se)
				}
			}
			if !res.More {
				break
			}
		}
		instances, err := countInstances(ctx, repo, "study", "DicomStudy/"+id)
		if err != nil {
			return err
		}
		updated, changed := dicomweb.ApplyStudyAggregates(study, dicomweb.ComputeStudyAggregates(series, instances))
		if !changed {
			return nil
		}
		ver := ""
		if m := study.Meta; m != nil {
			ver = m.VersionId
		}
		if _, err := repo.UpdateResource(ctx, updated, fhirrouter.UpdateOptions{IfMatch: ver}); err == nil {
			return nil
		} else if !isConflict(err) {
			return err
		}
		// người ghi khác thắng: đọc lại và tính lại từ dữ liệu mới hơn
	}
	return errors.New("dicomstow: quá số lần thử do xung đột phiên bản")
}

func updateSeries(ctx context.Context, repo fhirrouter.Repository, id string) error {
	for attempt := 0; attempt < maxUpdateAttempts; attempt++ {
		r, err := repo.ReadResource(ctx, "DicomSeries", id)
		if err != nil {
			return err
		}
		series := r.(*fhir.DicomSeries)
		n, err := countInstances(ctx, repo, "series", "DicomSeries/"+id)
		if err != nil {
			return err
		}
		updated, changed := dicomweb.ApplySeriesAggregates(series, n)
		if !changed {
			return nil
		}
		ver := ""
		if m := series.Meta; m != nil {
			ver = m.VersionId
		}
		if _, err := repo.UpdateResource(ctx, updated, fhirrouter.UpdateOptions{IfMatch: ver}); err == nil {
			return nil
		} else if !isConflict(err) {
			return err
		}
	}
	return errors.New("dicomstow: quá số lần thử do xung đột phiên bản")
}

// ---- phản hồi ----

func response(out *Outcome, req Request) dicomweb.Dataset {
	str := func(vr, v string) dicomweb.Element { return dicomweb.Element{VR: vr, Value: []any{v}} }
	ds := dicomweb.Dataset{}
	var ok, bad []any
	studies := map[string]bool{}
	for _, st := range out.Stored {
		studies[st.StudyUID] = true
		ok = append(ok, dicomweb.Dataset{
			"00081150": str("UI", st.SOPClassUID), "00081155": str("UI", st.SOPInstanceUID),
			"00081190": str("UR", req.RetrieveBase+"/studies/"+st.StudyUID+"/series/"+st.SeriesUID+"/instances/"+st.SOPInstanceUID),
		})
	}
	for _, f := range out.Failed {
		item := dicomweb.Dataset{"00081197": {VR: "US", Value: []any{int(f.Reason)}}}
		if f.SOPClassUID != "" {
			item["00081150"] = str("UI", f.SOPClassUID)
		}
		if f.SOPInstanceUID != "" {
			item["00081155"] = str("UI", f.SOPInstanceUID)
		}
		bad = append(bad, item)
	}
	// RetrieveURL cấp study khi mọi instance thành công thuộc cùng một study (PS3.18 Table 10.5.3-1)
	if len(studies) == 1 {
		for u := range studies {
			ds["00081190"] = str("UR", req.RetrieveBase+"/studies/"+u)
		}
	}
	if len(bad) > 0 {
		ds["00081198"] = dicomweb.Element{VR: "SQ", Value: bad}
	}
	if len(ok) > 0 {
		ds["00081199"] = dicomweb.Element{VR: "SQ", Value: ok}
	}
	return ds
}
