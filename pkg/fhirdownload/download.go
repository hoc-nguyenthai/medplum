// Package fhirdownload tự tải attachment có URL NGOÀI về kho Binary, như worker `download` của Medplum: sau khi một resource được ghi (sự kiện fhirsub.ChangeEvent), mọi Attachment có
// `url` http(s) tuyệt đối trỏ ra ngoài server được tải về (qua pkg/safehttp, chặn SSRF), lưu thành Binary riêng của project chứa resource (securityContext trỏ về resource) rồi đổi `url`
// của attachment thành `Binary/<id>`.
//
// Đây là bề mặt SSRF và chi phí nên mặc định TẮT ở fhir-server (FHIR_AUTO_DOWNLOAD). Không ghi query string của URL vào log (có thể chứa token ký).
package fhirdownload

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"time"

	"trivita-fhir/pkg/binarystore"
	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirrouter"
	"trivita-fhir/pkg/fhirsub"
	"trivita-fhir/pkg/messaging"
	"trivita-fhir/pkg/safehttp"
)

// Config là phụ thuộc và giới hạn của worker.
type Config struct {
	// Repo đọc và cập nhật resource nguồn; nên là kho phát sự kiện (fhirsub.Wrap) để Subscription thấy thay đổi. Không tách project (job nền, không có người gọi).
	Repo fhirrouter.Repository
	// BinaryRepo tạo Binary (không kèm nội dung: nội dung ghi thẳng vào Blobs theo luồng). Nên là kho GỐC, không phát sự kiện.
	BinaryRepo fhirrouter.Repository
	Blobs      binarystore.Store
	// Client tải URL ngoài; mặc định safehttp.NewClient(Timeout, AllowPrivate).
	Client *http.Client
	// AllowPrivate tắt chống SSRF (cho phép tải từ địa chỉ nội bộ): CHỈ cho dev và test.
	AllowPrivate bool
	// Timeout của mỗi lần tải khi dùng Client mặc định (mặc định 30 giây).
	Timeout time.Duration
	// MaxBytes là trần kích thước một tệp tải về (bắt buộc > 0). Vượt thì bỏ, không tạo Binary.
	MaxBytes int64
	// OwnPrefixes là các gốc URL của chính server (FHIR base, /storage): không tải.
	OwnPrefixes []string
	// Allowed (nếu không rỗng) chỉ cho tải URL bắt đầu bằng một tiền tố trong danh sách; Ignored là danh sách bỏ qua (thắng Allowed).
	Allowed, Ignored []string
	// Async: Handle chỉ đưa việc vào hàng đợi trong tiến trình (Workers goroutine, thử lại 3 lần lùi 1, 2 giây) và trả ngay. Bắt buộc với broker trong bộ nhớ (nó giao ĐỒNG BỘ trong
	// yêu cầu ghi nên tải ngoài sẽ chặn client); với broker bền (Redis) để false: lỗi trả về cho broker thử lại và đưa vào dead-letter.
	Async   bool
	Workers int // mặc định 2
	// Backoff cho test; mặc định 1 giây nhân đôi mỗi lần.
	Backoff func(attempt int) time.Duration
}

// Worker tải attachment ngoài.
type Worker struct {
	cfg   Config
	queue chan job
	wg    sync.WaitGroup
	stop  chan struct{}
	once  sync.Once
}

type job struct {
	rt, id string
	urls   []string
}

const queueSize = 1024

// New dựng worker (và chạy goroutine nếu Async).
func New(cfg Config) (*Worker, error) {
	if cfg.Repo == nil || cfg.BinaryRepo == nil || cfg.Blobs == nil {
		return nil, errors.New("fhirdownload: thiếu Repo, BinaryRepo hoặc Blobs")
	}
	if cfg.MaxBytes <= 0 {
		return nil, errors.New("fhirdownload: MaxBytes phải > 0")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.Client == nil {
		cfg.Client = safehttp.NewClient(cfg.Timeout, cfg.AllowPrivate)
	}
	if cfg.Workers <= 0 {
		cfg.Workers = 2
	}
	if cfg.Backoff == nil {
		cfg.Backoff = func(n int) time.Duration { return time.Duration(1<<(n-1)) * time.Second }
	}
	w := &Worker{cfg: cfg, queue: make(chan job, queueSize), stop: make(chan struct{})}
	if cfg.Async {
		for i := 0; i < cfg.Workers; i++ {
			w.wg.Add(1)
			go w.run()
		}
	}
	return w, nil
}

// Register đăng ký worker nhận sự kiện ghi resource (trước khi Start broker).
func (w *Worker) Register(b interface {
	Subscribe(topic string, h messaging.Handler)
}) {
	b.Subscribe(fhirsub.EventType, w.Handle)
}

// Shutdown ngừng nhận việc và chờ các việc đang chạy xong hoặc hết hạn ctx (việc còn trong hàng đợi bị bỏ).
func (w *Worker) Shutdown(ctx context.Context) error {
	w.once.Do(func() { close(w.stop) })
	done := make(chan struct{})
	go func() { w.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// skipTypes là loại resource không bao giờ chứa attachment cần tải (và là nguồn của nhiều sự kiện).
var skipTypes = map[string]bool{"Binary": true, "AsyncJob": true, "AuditEvent": true, "Subscription": true}

// Handle xử lý một sự kiện ghi resource.
func (w *Worker) Handle(ctx context.Context, env messaging.Envelope) error {
	var ev fhirsub.ChangeEvent
	if err := env.Decode(&ev); err != nil {
		return err
	}
	if ev.Interaction == "delete" || skipTypes[ev.ResourceType] || len(ev.Resource) == 0 || ev.ID == "" {
		return nil
	}
	res, err := fhir.UnmarshalResource(ev.Resource)
	if err != nil || res == nil {
		return nil
	}
	urls := w.externalURLs(res)
	if len(urls) == 0 {
		return nil
	}
	j := job{rt: ev.ResourceType, id: ev.ID, urls: urls}
	if w.cfg.Async {
		select {
		case <-w.stop:
		case w.queue <- j:
		default:
			slog.WarnContext(ctx, "fhirdownload: hàng đợi đầy, bỏ qua", "resource", j.rt+"/"+j.id)
		}
		return nil
	}
	return w.process(ctx, j)
}

func (w *Worker) run() {
	defer w.wg.Done()
	for {
		select {
		case <-w.stop:
			return
		case j := <-w.queue:
			var err error
			for attempt := 1; attempt <= 3; attempt++ {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
				err = w.process(ctx, j)
				cancel()
				if err == nil {
					break
				}
				if attempt < 3 {
					select {
					case <-time.After(w.cfg.Backoff(attempt)):
					case <-w.stop:
						return
					}
				}
			}
			if err != nil {
				slog.Error("fhirdownload: bỏ cuộc sau 3 lần", "resource", j.rt+"/"+j.id, "error", err.Error())
			}
		}
	}
}

// attachments trả mọi Attachment trong resource (theo KIỂU, không theo tên khóa `url`, để không tải nhầm Extension.url hay Reference).
func attachments(v any) []*fhir.Attachment {
	var out []*fhir.Attachment
	var walk func(rv reflect.Value, depth int)
	attType := reflect.TypeOf(fhir.Attachment{})
	walk = func(rv reflect.Value, depth int) {
		if depth > 64 {
			return
		}
		switch rv.Kind() {
		case reflect.Pointer, reflect.Interface:
			if !rv.IsNil() {
				walk(rv.Elem(), depth+1)
			}
		case reflect.Struct:
			if rv.Type() == attType {
				if rv.CanAddr() {
					out = append(out, rv.Addr().Interface().(*fhir.Attachment))
				}
				return
			}
			for i := 0; i < rv.NumField(); i++ {
				if rv.Type().Field(i).IsExported() {
					walk(rv.Field(i), depth+1)
				}
			}
		case reflect.Slice:
			for i := 0; i < rv.Len(); i++ {
				walk(rv.Index(i), depth+1)
			}
		}
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Pointer && !rv.IsNil() {
		walk(rv.Elem(), 0)
	}
	return out
}

// wanted: URL http(s) tuyệt đối, không phải của chính server, qua danh sách cho phép và bỏ qua.
func (w *Worker) wanted(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return false
	}
	for _, p := range w.cfg.OwnPrefixes {
		if p != "" && strings.HasPrefix(raw, p) {
			return false
		}
	}
	for _, p := range w.cfg.Ignored {
		if p != "" && strings.HasPrefix(raw, p) {
			return false
		}
	}
	if len(w.cfg.Allowed) == 0 {
		return true
	}
	for _, p := range w.cfg.Allowed {
		if p != "" && strings.HasPrefix(raw, p) {
			return true
		}
	}
	return false
}

func (w *Worker) externalURLs(res fhir.Resource) []string {
	seen := map[string]bool{}
	var out []string
	for _, a := range attachments(res) {
		if a.URL != "" && !seen[a.URL] && w.wanted(a.URL) {
			seen[a.URL] = true
			out = append(out, a.URL)
		}
	}
	return out
}

func hostOf(raw string) string {
	if u, err := url.Parse(raw); err == nil {
		return u.Host
	}
	return "?"
}

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func gone(err error) bool {
	var fe *fhirrouter.Error
	return errors.As(err, &fe) && (fe.Status == http.StatusNotFound || fe.Status == http.StatusGone)
}

func (w *Worker) process(ctx context.Context, j job) error {
	for _, u := range j.urls {
		if err := w.downloadOne(ctx, j.rt, j.id, u); err != nil {
			return err
		}
	}
	return nil
}

// downloadOne tải một URL của một resource. Lỗi trả về là lỗi tạm (thử lại được); lỗi vĩnh viễn (URL bị chặn, quá lớn, loại nội dung bị cấm, resource đã mất hoặc đã đổi URL) chỉ ghi log và trả nil.
func (w *Worker) downloadOne(ctx context.Context, rt, id, raw string) error {
	log := slog.With("resource", rt+"/"+id, "host", hostOf(raw))
	cur, err := w.cfg.Repo.ReadResource(ctx, rt, id)
	if err != nil {
		if gone(err) {
			return nil
		}
		return err
	}
	if !hasURL(cur, raw) {
		return nil // đã đổi hoặc đã được tải
	}
	project := ""
	if m := cur.GetMeta(); m != nil {
		project = m.Project
	}
	if !w.cfg.AllowPrivate {
		if _, err := safehttp.CheckURL(raw, false); err != nil {
			log.Warn("fhirdownload: URL bị chặn", "error", err.Error())
			return nil
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil
	}
	resp, err := w.cfg.Client.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err // url.Error chứa cả URL (có thể kèm token ký trong query): chỉ giữ lỗi gốc
		}
		if errors.Is(err, safehttp.ErrBlockedAddress) || strings.Contains(err.Error(), safehttp.ErrBlockedAddress.Error()) {
			log.Warn("fhirdownload: địa chỉ bị chặn")
			return nil
		}
		return fmt.Errorf("tải %s: %v", hostOf(raw), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("tải %s: HTTP %d", hostOf(raw), resp.StatusCode)
	}
	if resp.ContentLength > w.cfg.MaxBytes {
		log.Warn("fhirdownload: quá lớn", "bytes", resp.ContentLength, "max", w.cfg.MaxBytes)
		return nil
	}
	contentType := resp.Header.Get("Content-Type")
	if mt, _, err := mime.ParseMediaType(contentType); err == nil {
		contentType = mt
	} else {
		contentType = ""
	}
	if contentType == "" {
		for _, a := range attachments(cur) {
			if a.URL == raw && a.ContentType != "" {
				contentType = a.ContentType
				break
			}
		}
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if err := binarystore.CheckMetadata("", contentType); err != nil {
		log.Warn("fhirdownload: loại nội dung bị cấm", "content_type", contentType)
		return nil
	}

	bin := &fhir.Binary{ID: newID(), ContentType: contentType, SecurityContext: &fhir.Reference{Reference: rt + "/" + id}}
	if project != "" {
		bin.Meta = &fhir.Meta{Project: project} // kho không tách project không tự gán: Binary phải thuộc project của resource
	}
	created, err := w.cfg.BinaryRepo.CreateResource(ctx, bin)
	if err != nil {
		return err
	}
	ver := ""
	if m := created.GetMeta(); m != nil {
		ver = m.VersionId
	}
	key, err := binarystore.Key(created.GetID(), ver)
	if err != nil {
		w.drop(ctx, created.GetID(), "")
		return err
	}
	if _, err := w.cfg.Blobs.Put(ctx, key, resp.Body, binarystore.PutOptions{ContentType: contentType, MaxBytes: w.cfg.MaxBytes}); err != nil {
		w.drop(ctx, created.GetID(), key)
		if errors.Is(err, binarystore.ErrTooLarge) {
			log.Warn("fhirdownload: quá lớn", "max", w.cfg.MaxBytes)
			return nil
		}
		return fmt.Errorf("ghi nội dung: %w", err)
	}

	// đọc lại resource rồi đổi URL của mọi attachment khớp; ghi bằng If-Match nên thua ghi song song thì dọn và trả lỗi để thử lại
	fresh, err := w.cfg.Repo.ReadResource(ctx, rt, id)
	if err != nil {
		w.drop(ctx, created.GetID(), key)
		if gone(err) {
			return nil
		}
		return err
	}
	n := 0
	for _, a := range attachments(fresh) {
		if a.URL == raw {
			a.URL = "Binary/" + created.GetID()
			if a.ContentType == "" {
				a.ContentType = contentType
			}
			n++
		}
	}
	if n == 0 {
		w.drop(ctx, created.GetID(), key)
		return nil
	}
	opts := fhirrouter.UpdateOptions{}
	if m := fresh.GetMeta(); m != nil {
		opts.IfMatch = m.VersionId
	}
	if _, err := w.cfg.Repo.UpdateResource(ctx, fresh, opts); err != nil {
		w.drop(ctx, created.GetID(), key)
		return err
	}
	return nil
}

func hasURL(res fhir.Resource, raw string) bool {
	for _, a := range attachments(res) {
		if a.URL == raw {
			return true
		}
	}
	return false
}

// drop xóa nội dung và Binary vừa tạo (cố gắng hết sức; không để mồ côi như Medplum).
func (w *Worker) drop(ctx context.Context, binID, key string) {
	ctx = context.WithoutCancel(ctx)
	if key != "" {
		if err := w.cfg.Blobs.Delete(ctx, key); err != nil {
			slog.WarnContext(ctx, "fhirdownload: không xóa được nội dung", "binary", binID, "error", err.Error())
		}
	}
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		if err = w.cfg.BinaryRepo.DeleteResource(ctx, "Binary", binID); err == nil || gone(err) {
			return
		}
		time.Sleep(time.Duration(10<<attempt) * time.Millisecond)
	}
	slog.WarnContext(ctx, "fhirdownload: không xóa được Binary", "binary", binID, "error", err.Error())
}
