// Package fhirrouter cài các interaction REST của FHIR R4 (create, read, vread, update, patch, delete,
// history, search, batch, transaction, capabilities) trên một interface Repository, không phụ thuộc
// framework HTTP. Có sẵn adapter net/http (HTTPHandler) và MemoryRepository để test và phát triển.
//
// Operation: $validate, Patient/$everything, $reindex (job nền AsyncJob), $expunge, cùng khung tham số Input
// và hạ tầng job (RegisterJob, StartJob, GET /job/{id}/status) để thêm operation khác. Bulk export ($export, xem bulk.go)
// và terminology ($expand, $lookup, $validate-code, xem terminology.go).
//
// Chained search và _has: xem chain.go. Phân tách theo project và JWT: xem pkg/fhirauth.
//
// GraphQL ($graphql): xem graphql.go. Update-as-create: Options.UpdateAsCreate. Phân quyền do tầng trên đảm nhiệm.
package fhirrouter

import (
	"context"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirsearch"
	"trivita-fhir/pkg/workflow"
)

// Request là yêu cầu đã tách khỏi tầng HTTP.
type Request struct {
	Method string
	Path   string            // đường dẫn tương đối gốc FHIR, ví dụ "Patient/123/_history" (không có "/" đầu)
	Params map[string]string // tham số của route: resourceType, id, vid
	Query  url.Values
	Body   any // JSON đã giải mã (UseNumber): map[string]any, hoặc []any với JSON Patch
	Header http.Header
}

// Response là kết quả của một interaction.
type Response struct {
	Status   int
	Resource fhir.Resource // nil nếu không có body
	Header   map[string]string
	// Raw (nếu khác nil) là body nguyên bản thay cho Resource, với Content-Type là ContentType
	// (đọc Binary dạng file, trạng thái bulk export).
	Raw         []byte
	ContentType string
}

// Handler xử lý một interaction.
type Handler func(ctx context.Context, req *Request) (*Response, error)

// HistoryEntry là một phiên bản trong lịch sử của resource.
type HistoryEntry struct {
	Resource     fhir.Resource // với phiên bản xóa: resource rỗng chỉ có id và resourceType
	Method       string        // POST, PUT hoặc DELETE
	ResourceType string
	ID           string
	VersionID    string
	LastUpdated  string
	Deleted      bool
}

// SearchResult là kết quả thô của Repository.Search; router dựng Bundle từ đây.
// JobSealURL là url của extension trên AsyncJob chứa chuỗi ký của JobSeal.
const JobSealURL = "https://trivita.ai/fhir/StructureDefinition/job-seal"

type SearchResult struct {
	Matches  []fhir.Resource
	Included []fhir.Resource // _include và _revinclude
	Total    *int            // nil nếu không tính
	More     bool            // còn kết quả sau trang này
}

// UpdateOptions điều chỉnh Repository.UpdateResource.
type UpdateOptions struct {
	IfMatch string // versionId mong đợi (đã bỏ W/"..."); rỗng nếu không kiểm tra
}

// Repository là kho lưu trữ resource. Các lỗi trả về nên là *Error (NotFound, Gone, ...).
type Repository interface {
	// CreateResource lưu resource mới. Router luôn cấp sẵn id; chỉ cần gán meta (versionId, lastUpdated).
	CreateResource(ctx context.Context, r fhir.Resource) (fhir.Resource, error)
	// ReadResource trả NotFound nếu chưa có, Gone nếu đã xóa.
	ReadResource(ctx context.Context, resourceType, id string) (fhir.Resource, error)
	ReadVersion(ctx context.Context, resourceType, id, versionID string) (fhir.Resource, error)
	// ReadHistory trả các phiên bản từ mới đến cũ (gồm cả phiên bản xóa).
	ReadHistory(ctx context.Context, resourceType, id string) ([]HistoryEntry, error)
	// UpdateResource lưu phiên bản mới của resource đã tồn tại (id lấy từ r). PreconditionFailed nếu IfMatch không khớp.
	UpdateResource(ctx context.Context, r fhir.Resource, opts UpdateOptions) (fhir.Resource, error)
	// DeleteResource đánh dấu xóa; NotFound nếu chưa từng tồn tại.
	DeleteResource(ctx context.Context, resourceType, id string) error
	Search(ctx context.Context, req *fhirsearch.Request) (*SearchResult, error)
}

// Transactional là tùy chọn của Repository để transaction Bundle có tính nguyên tử.
type Transactional interface {
	// RunInTransaction chạy fn với repository của giao dịch; lỗi từ fn hủy mọi thay đổi.
	RunInTransaction(ctx context.Context, fn func(Repository) error) error
}

// KeyLocker là tùy chọn của Repository để các thao tác "kiểm tra rồi tạo" (If-None-Exist) không tạo trùng khi chạy đồng
// thời. LockKey giữ khóa theo key (tư vấn, không gắn với dòng nào) cho tới hết transaction hiện tại; ngoài transaction thì
// không có tác dụng. Repository không cài đặt thì create có điều kiện chỉ an toàn khi có một writer.
type KeyLocker interface {
	LockKey(ctx context.Context, key string) error
}

// Options cấu hình Router.
type Options struct {
	// BaseURL dùng cho Location, fullUrl và link phân trang, ví dụ "https://host/fhir/R4". Có thể rỗng.
	BaseURL string
	// SkipValidation bỏ kiểm tra bằng fhirvalidate khi ghi.
	SkipValidation bool
	// SkipConstraints chỉ kiểm tra cấu trúc, bỏ invariant FHIRPath.
	SkipConstraints bool
	// ValidateProfiles bật kiểm resource theo từng meta.profile bằng StructureDefinition trong kho (cùng project với người gọi):
	// cardinality, fixed/pattern, slicing... (pkg/fhirvalidate/profile.go). Mỗi profile tốn một lần tra cứu kho khi ghi.
	// Profile không tìm thấy là cảnh báo, trừ khi ProfileUnknownError (khi đó là lỗi 400).
	ValidateProfiles    bool
	ProfileUnknownError bool
	// NewOrchestrator tạo orchestrator chạy job nền (AsyncJob) với runner do Router cung cấp.
	// Mặc định: workflow.Inline với 2 job đồng thời (trong process, mất job đang chạy khi restart);
	// dùng adapter asynq hoặc Temporal để bền vững và chạy ở tiến trình khác.
	NewOrchestrator func(run workflow.JobRunner) workflow.JobOrchestrator
	// BinaryContent (tùy chọn) trả nội dung của một Binary khi `data` không nằm trong resource (đã chuyển ra kho đối tượng). Không đặt
	// thì đọc `data` base64 trong resource. Dùng cho PackageRelease/$install.
	BinaryContent func(ctx context.Context, b *fhir.Binary) ([]byte, error)
	// JobRepository là repository mà job nền dùng để nạp/ghi AsyncJob và chạy hàm của job. Mặc định là repo của router.
	// Khi repo của router tách dữ liệu theo người gọi (pkg/fhirauth), job chạy ngoài yêu cầu HTTP nên không có
	// người gọi: truyền repository không tách (hoặc có quyền hệ thống) vào đây. Job như $reindex khi đó chạy trên mọi project.
	JobRepository Repository
	// ScopedJobRepository và JobContext dùng cho job phải chạy với quyền của người gọi (bulk export): job đọc dữ liệu
	// bằng ScopedJobRepository với context do JobContext dựng từ AsyncJob (ví dụ gắn lại project của người gọi). Thiếu một
	// trong hai thì các job loại này dùng JobRepository.
	ScopedJobRepository Repository
	JobContext          func(ctx context.Context, job *fhir.AsyncJob) context.Context
	// JobSeal và JobOpen bảo vệ quyền của job nền. JobSeal chạy khi tạo job (ctx là của người gọi) và trả chuỗi ký gắn vào
	// AsyncJob (extension JobSealURL); JobOpen chạy trước khi job chạy, kiểm chuỗi đó và trả context mang đúng quyền của
	// người gọi, hoặc lỗi (job bị đánh dấu error, không chạy). Khi JobOpen có, nó thay JobContext và áp cho mọi loại job.
	// UpdateAsCreate cho phép PUT Type/id tạo resource mới với id do client chọn khi chưa có (trả 201). Mặc định tắt (404).
	// id phải hợp lệ theo FHIR; resource đã xóa không được tạo lại; id đang bị project khác dùng trả 404.
	UpdateAsCreate bool
	// ResponseHook (tùy chọn) biến đổi resource trả về cho người gọi của mọi interaction thành công (kể cả Bundle) ngay trước khi gửi, ví dụ
	// đổi tham chiếu Binary thành URL ký. Phải trả nguyên đối tượng nếu không đổi gì và KHÔNG sửa đối tượng đầu vào.
	ResponseHook func(ctx context.Context, res fhir.Resource) fhir.Resource
	JobSeal      func(ctx context.Context, jobID, request string) (string, error)
	JobOpen      func(ctx context.Context, job *fhir.AsyncJob, sealed string) (context.Context, error)
	// JobLease là thời gian một replica giữ quyền chạy job trước khi replica khác được tiếp quản (mặc định 5 phút;
	// tự gia hạn mỗi JobLease/3 khi job còn chạy).
	JobLease time.Duration
	// JobCancelPoll là chu kỳ replica đang chạy job kiểm tra xem AsyncJob có bị đánh dấu cancelled (từ replica khác) không (mặc định 2 giây; không vượt JobLease/3).
	// Tách khỏi nhịp gia hạn lease để việc hủy có hiệu lực trong vài giây thay vì chờ JobLease/3 (mặc định 100 giây). Mỗi job đang chạy tốn một lần đọc mỗi chu kỳ.
	JobCancelPoll time.Duration
	// JobMaintenanceInterval > 0 bật vòng nền định kỳ: chạy lại job mồ côi (RecoverJobs) và xóa kết quả export hết hạn
	// (ExpireJobs). Mặc định 0 (tắt; gọi tay các hàm này). JobRecoveryGrace là tuổi tối thiểu của job mới được coi là
	// mồ côi (mặc định 30 giây, tránh giành job vừa tạo còn đang xếp hàng).
	JobMaintenanceInterval time.Duration
	JobRecoveryGrace       time.Duration
	// DisableJobExecution: router chỉ NHẬN và tạo AsyncJob (accepted), không đưa vào orchestrator và vòng bảo trì không tiếp quản job mồ côi
	// (RecoverJobs); một tiến trình khác (fhir-worker) chạy job qua RecoverJobs. Mặc định false: chạy tại chỗ như cũ.
	DisableJobExecution bool
	// ExportRetention là thời gian giữ kết quả bulk export và batch async (mặc định 24 giờ; âm = không bao giờ xóa).
	ExportRetention time.Duration
	// JobOwner (tùy chọn) trả định danh của người gọi, ghi vào AsyncJob.requester lúc tạo job; rỗng thì job không có chủ. JobAllowed
	// (tùy chọn) cho biết người gọi hiện tại có được xem trạng thái, hủy hoặc xóa job của owner đó không (chủ sở hữu hoặc quản trị).
	// Job bị từ chối trả 404 như không tồn tại (không lộ job của người khác). JobAllowed nil: mọi người gọi đều được (chỉ test hoặc dev).
	JobOwner   func(ctx context.Context) string
	JobAllowed func(ctx context.Context, owner string) bool
	// AuthorizeEntry (tùy chọn) kiểm quyền cho TỪNG entry của Bundle batch/transaction theo method và đường dẫn tương đối của entry
	// (ví dụ "Patient/$reindex"), bằng đúng quy tắc mà tầng HTTP áp cho yêu cầu ngoài. Cần vì cổng HTTP chỉ thấy `POST /` (quyền ghi):
	// nếu không kiểm, entry `$reindex`/`$expunge` lọt tới handler với quyền của Bundle. Lỗi trả về thành 403 của entry (batch) hoặc của
	// cả Bundle (transaction và batch async: không entry nào chạy). Kiểm ở lúc nhận Bundle, trong ngữ cảnh của người gọi thật (job nền
	// chạy sau với quyền thu hẹp của ảnh chụp, không có vai trò nên không kiểm lại ở đó). Nil: không kiểm (chỉ test hoặc dev).
	AuthorizeEntry func(ctx context.Context, method, path string) error
	// AsyncBatch cho POST Bundle batch kèm "Prefer: respond-async" chạy thành job nền (xem batch_async.go). Tắt thì header bị
	// bỏ qua và batch chạy đồng bộ. Transaction kèm respond-async luôn bị từ chối (400) khi bật.
	AsyncBatch bool
	// BatchCheckpointEntries và BatchCheckpointInterval: nhịp lưu checkpoint của batch async (mặc định 10 entry, 5 giây).
	BatchCheckpointEntries  int
	BatchCheckpointInterval time.Duration
}

type segment struct {
	literal string
	param   string
}

type route struct {
	method string
	segs   []segment
	h      Handler
	score  int
}

// Router định tuyến yêu cầu FHIR tới handler.
type Router struct {
	repo   Repository
	opts   Options
	routes []route
	jobs   *jobs
}

// New tạo Router đã đăng ký đủ các interaction chuẩn.
func New(repo Repository, opts Options) *Router {
	r := &Router{repo: repo, opts: opts, jobs: &jobs{funcs: map[string]JobFunc{}, scope: map[string]bool{}, cancels: map[string]context.CancelFunc{}, stop: make(chan struct{})}}
	if opts.JobRepository == nil {
		r.opts.JobRepository = repo
	}
	if r.opts.JobLease <= 0 {
		r.opts.JobLease = 5 * time.Minute
	}
	if r.opts.JobCancelPoll <= 0 {
		r.opts.JobCancelPoll = 2 * time.Second
	}
	if r.opts.JobRecoveryGrace <= 0 {
		r.opts.JobRecoveryGrace = 30 * time.Second
	}
	if r.opts.ExportRetention == 0 {
		r.opts.ExportRetention = 24 * time.Hour
	}
	if r.opts.BatchCheckpointEntries <= 0 {
		r.opts.BatchCheckpointEntries = 10
	}
	if r.opts.BatchCheckpointInterval <= 0 {
		r.opts.BatchCheckpointInterval = 5 * time.Second
	}
	r.registerStandard()
	r.registerOperations()
	r.startOrchestrator()
	r.startMaintenance()
	return r
}

// Add đăng ký thêm một route (ví dụ operation "$validate"). Mẫu dùng ":name" cho tham số,
// ví dụ "Patient/:id/$everything". Route cụ thể hơn được ưu tiên.
func (r *Router) Add(method, pattern string, h Handler) {
	var segs []segment
	score := 0
	if pattern != "" {
		parts := strings.Split(pattern, "/")
		for i, p := range parts {
			if strings.HasPrefix(p, ":") {
				segs = append(segs, segment{param: p[1:]})
			} else {
				segs = append(segs, segment{literal: p})
				score += 100 - i // literal sớm hơn có điểm cao hơn
			}
		}
	}
	r.routes = append(r.routes, route{method: method, segs: segs, h: h, score: score})
	sort.SliceStable(r.routes, func(i, j int) bool {
		if len(r.routes[i].segs) != len(r.routes[j].segs) {
			return len(r.routes[i].segs) > len(r.routes[j].segs)
		}
		return r.routes[i].score > r.routes[j].score
	})
}

// match tìm route; pathExists cho biết đường dẫn khớp với route của method khác (để trả 405).
func (r *Router) match(method, path string) (h Handler, params map[string]string, pathExists bool) {
	path = strings.Trim(path, "/")
	var parts []string
	if path != "" {
		parts = strings.Split(path, "/")
	}
	for _, rt := range r.routes {
		if len(rt.segs) != len(parts) {
			continue
		}
		p := map[string]string{}
		ok := true
		for i, s := range rt.segs {
			if s.param != "" {
				if parts[i] == "" || (s.param == "resourceType" && !fhir.IsResourceType(parts[i])) {
					ok = false // resourceType chỉ nhận tên resource thật
					break
				}
				p[s.param] = parts[i]
			} else if s.literal != parts[i] {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		if rt.method == method {
			return rt.h, p, true
		}
		pathExists = true
	}
	return nil, nil, pathExists
}

// Handle xử lý một yêu cầu; lỗi trả về luôn là *Error.
func (r *Router) Handle(ctx context.Context, req *Request) (*Response, error) {
	h, params, exists := r.match(req.Method, req.Path)
	if h == nil {
		if exists {
			return nil, MethodNotAllowed(req.Method + " không được hỗ trợ cho " + req.Path)
		}
		return nil, NotFoundMsg("Không có route cho " + req.Method + " " + req.Path)
	}
	req.Params = params
	res, err := h(ctx, req)
	if err != nil {
		return nil, asError(err)
	}
	r.scrubJobs(ctx, res)
	if hook := r.opts.ResponseHook; hook != nil && res != nil && res.Resource != nil && res.Status < 300 {
		out := *res // không sửa Response gốc (handler có thể giữ lại)
		out.Resource = hook(ctx, res.Resource)
		return &out, nil
	}
	return res, nil
}

func (r *Router) registerStandard() {
	r.Add("GET", "metadata", r.capabilities)
	r.Add("GET", "", r.searchSystem)
	r.Add("POST", "", r.batch)
	r.Add("GET", ":resourceType", r.searchType)
	r.Add("POST", ":resourceType/_search", r.searchType)
	r.Add("POST", ":resourceType", r.create)
	r.Add("GET", ":resourceType/:id", r.read)
	r.Add("GET", ":resourceType/:id/_history", r.history)
	r.Add("GET", ":resourceType/:id/_history/:vid", r.vread)
	r.Add("PUT", ":resourceType/:id", r.update)
	r.Add("PUT", ":resourceType", r.conditionalUpdate)
	r.Add("PATCH", ":resourceType/:id", r.patch)
	r.Add("PATCH", ":resourceType", r.conditionalPatch)
	r.Add("DELETE", ":resourceType/:id", r.delete)
	r.Add("DELETE", ":resourceType", r.conditionalDelete)
}

// ---------------------------------------------------------------- lỗi

// Error là lỗi FHIR có mã HTTP và OperationOutcome.
type Error struct {
	Status  int
	Outcome *fhir.OperationOutcome
}

func (e *Error) Error() string {
	if e.Outcome != nil && len(e.Outcome.Issue) > 0 {
		return e.Outcome.Issue[0].Diagnostics
	}
	return http.StatusText(e.Status)
}

func newError(status int, code, msg string) *Error {
	return &Error{Status: status, Outcome: &fhir.OperationOutcome{Issue: []fhir.OperationOutcomeIssue{{
		Severity: "error", Code: code, Diagnostics: msg, Details: &fhir.CodeableConcept{Text: msg},
	}}}}
}

// Các hàm dựng lỗi thông dụng.
func BadRequest(msg string) *Error         { return newError(400, "invalid", msg) }
func Unauthorized(msg string) *Error       { return newError(401, "login", msg) }
func Forbidden(msg string) *Error          { return newError(403, "forbidden", msg) }
func NotFound() *Error                     { return newError(404, "not-found", "Not found") }
func NotFoundMsg(msg string) *Error        { return newError(404, "not-found", msg) }
func MethodNotAllowed(msg string) *Error   { return newError(405, "not-supported", msg) }
func Conflict(msg string) *Error           { return newError(409, "conflict", msg) }
func Gone() *Error                         { return newError(410, "deleted", "Gone") }
func PreconditionFailed(msg string) *Error { return newError(412, "conflict", msg) }
func Unprocessable(msg string) *Error      { return newError(422, "processing", msg) }
func NotImplemented(msg string) *Error     { return newError(501, "not-supported", msg) }
func Internal(msg string) *Error           { return newError(500, "exception", msg) }

func asError(err error) *Error {
	switch e := err.(type) {
	case *Error:
		return e
	case *fhirsearch.Error:
		return BadRequest(e.Msg)
	}
	return Internal(err.Error())
}
