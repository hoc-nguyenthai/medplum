package httpapi

import "trivita-fhir/pkg/fhir"

var _ = fhir.Bundle{} // để swag phân giải kiểu pkg/fhir

// Tệp này chỉ chứa annotation swaggo để sinh OpenAPI (xem `make swagger`). Các hàm rỗng không được gắn vào router:
// route FHIR thật nằm ở pkg/fhirrouter và đi qua một wildcard dưới Prefix, nên cần mô tả riêng ở đây.
// Sửa annotation xong phải chạy lại `make swagger` để cập nhật apps/fhir-server/docs.

// @title						Trivita FHIR Server API
// @version					1.0
// @description				FHIR R4 REST (CRUD, search, history, batch/transaction), operation, bulk export, terminology và GraphQL. Lỗi trả về dạng OperationOutcome (application/fhir+json).
// @BasePath					/
// @tag.name					fhir
// @tag.description			FHIR R4 REST: CRUD, search, history, batch/transaction, GraphQL, metadata (CapabilityStatement). Lỗi trả OperationOutcome (application/fhir+json).
// @tag.name					operation
// @tag.description			Operation FHIR ($validate, $everything, $reindex, $expunge, PackageRelease/$install, CodeSystem/$import, ConceptMap/$import) và trạng thái job nền. $reindex, $expunge, $install và $import cần quyền admin.
// @tag.name					terminology
// @tag.description			Terminology cơ bản: ValueSet/$expand và CodeSystem/$lookup.
// @tag.name					bulk
// @tag.description			Bulk Data Access: $export bất đồng bộ, theo dõi trạng thái và hủy; tệp kết quả là NDJSON tải qua Binary.
// @tag.name					health
// @tag.description			Probe liveness và readiness cho orchestrator (ngoài /fhir/R4, lỗi dạng JSON thường).
// @securityDefinitions.apikey	BearerAuth
// @in							header
// @name						Authorization
// @description				Dạng "Bearer <token>". Vai trò: read (GET, _search, operation không ghi), write (ghi, batch), admin ($reindex, $expunge, $install, $import). /fhir/R4/metadata công khai.

// FHIRResource là body FHIR JSON của một resource bất kỳ (luôn có resourceType); schema cụ thể xem pkg/fhir.
type FHIRResource map[string]any

// Các kiểu *Body dưới đây chỉ để Swagger UI hiển thị body mẫu (Example Value); server nhận mọi resource FHIR R4 hợp lệ,
// không giới hạn ở các trường này. Để gửi loại resource khác, đổi resourceType và nội dung tương ứng.

// HumanNameBody là HumanName mẫu.
type HumanNameBody struct {
	Use    string   `json:"use,omitempty" example:"official" enums:"usual,official,temp,nickname,anonymous,old,maiden"` // mục đích dùng tên
	Family string   `json:"family,omitempty" example:"Nguyen"`                                                          // họ
	Given  []string `json:"given,omitempty" example:"Van A"`                                                            // tên đệm và tên, mỗi phần tử một từ hoặc cụm
}

// IdentifierBody là Identifier mẫu.
type IdentifierBody struct {
	System string `json:"system,omitempty" example:"http://hospital.example.org/mrn"` // không gian tên của giá trị (URI)
	Value  string `json:"value,omitempty" example:"MRN-0001"`                         // giá trị định danh trong system
}

// ContactPointBody là ContactPoint mẫu.
type ContactPointBody struct {
	System string `json:"system,omitempty" example:"phone" enums:"phone,fax,email,pager,url,sms,other"` // kênh liên lạc
	Value  string `json:"value,omitempty" example:"0900000000"`                                         // số điện thoại, địa chỉ email...
	Use    string `json:"use,omitempty" example:"mobile" enums:"home,work,temp,old,mobile"`             // mục đích dùng
}

// PatientBody là body mẫu của create/update/validate (ví dụ Patient).
type PatientBody struct {
	ResourceType string             `json:"resourceType" binding:"required" example:"Patient"`                 // luôn "Patient" với body này
	ID           string             `json:"id,omitempty" example:"123"`                                        // id logic; khi tạo mới server cấp id (bỏ qua giá trị gửi lên, trừ update-as-create)
	Identifier   []IdentifierBody   `json:"identifier,omitempty"`                                              // định danh nghiệp vụ (mã bệnh nhân...)
	Active       bool               `json:"active,omitempty" example:"true"`                                   // hồ sơ còn dùng
	Name         []HumanNameBody    `json:"name,omitempty"`                                                    // họ tên
	Telecom      []ContactPointBody `json:"telecom,omitempty"`                                                 // phương thức liên lạc
	Gender       string             `json:"gender,omitempty" example:"male" enums:"male,female,other,unknown"` // giới tính hành chính
	BirthDate    string             `json:"birthDate,omitempty" example:"1990-05-20"`                          // ngày sinh, định dạng YYYY[-MM[-DD]]
}

// BundleRequestBody là entry.request của batch/transaction.
type BundleRequestBody struct {
	Method string `json:"method" binding:"required" example:"POST" enums:"GET,POST,PUT,PATCH,DELETE"` // phương thức HTTP của entry
	URL    string `json:"url" binding:"required" example:"Patient"`                                   // đường dẫn tương đối tới gốc FHIR, ví dụ Patient hoặc Patient/123
}

// BundleEntryBody là một entry mẫu của Bundle.
type BundleEntryBody struct {
	FullURL  string            `json:"fullUrl,omitempty" example:"urn:uuid:11111111-1111-1111-1111-111111111111"` // định danh tạm của entry, để entry khác tham chiếu trong transaction
	Resource PatientBody       `json:"resource" binding:"required"`                                               // resource của entry (bỏ qua với GET và DELETE); ví dụ này dùng Patient
	Request  BundleRequestBody `json:"request" binding:"required"`                                                // phương thức và URL của entry
}

// BundleBody là body mẫu của batch/transaction.
type BundleBody struct {
	ResourceType string            `json:"resourceType" binding:"required" example:"Bundle"`                        // luôn "Bundle"
	Type         string            `json:"type" binding:"required" example:"transaction" enums:"batch,transaction"` // batch: từng entry độc lập; transaction: tất cả hoặc không entry nào
	Entry        []BundleEntryBody `json:"entry" binding:"required"`                                                // danh sách entry, xử lý theo thứ tự quy tắc FHIR
}

// HealthResponse là body của /healthz.
type HealthResponse struct {
	Status string `json:"status" binding:"required" example:"ok"` // luôn "ok" khi tiến trình còn sống
}

// ReadyResponse là body của /readyz; Checks là kết quả từng phụ thuộc (true = sẵn sàng).
type ReadyResponse struct {
	Ready  bool            `json:"ready" binding:"required" example:"true"` // true khi nhận được lưu lượng (không đang tắt, mọi phụ thuộc sẵn sàng)
	Checks map[string]bool `json:"checks" binding:"required"`               // kết quả từng phụ thuộc: accepting_traffic, database, broker (chỉ khi bật Subscription)
}

// JSONPatchOp là một thao tác RFC 6902 trong PATCH.
type JSONPatchOp struct {
	Op    string `json:"op" binding:"required" enums:"add,remove,replace,move,copy,test" example:"replace"` // thao tác RFC 6902
	Path  string `json:"path" binding:"required" example:"/gender"`                                         // JSON Pointer tới vị trí cần sửa
	From  string `json:"from,omitempty" example:"/telecom/0"`                                               // nguồn, bắt buộc với move và copy
	Value any    `json:"value,omitempty"`                                                                   // giá trị mới, bắt buộc với add, replace và test
}

// GraphQLRequest là body của $graphql.
type GraphQLRequest struct {
	Query         string         `json:"query" binding:"required" example:"{ Patient(id: \"123\") { id name { family } } }"` // truy vấn GraphQL
	Variables     map[string]any `json:"variables,omitempty"`                                                                // giá trị biến của truy vấn
	OperationName string         `json:"operationName,omitempty" example:"GetPatient"`                                       // tên operation cần chạy khi query có nhiều operation
}

// GraphQLResponse là kết quả của $graphql.
type GraphQLResponse struct {
	Data   map[string]any `json:"data,omitempty"`   // kết quả truy vấn; vắng khi lỗi cú pháp
	Errors []GraphQLError `json:"errors,omitempty"` // lỗi GraphQL; vắng khi thành công hoàn toàn
}

// GraphQLError là một lỗi GraphQL.
type GraphQLError struct {
	Message string `json:"message" binding:"required" example:"Cannot query field \"foo\" on type \"Patient\""` // thông điệp lỗi
}

// ErrorBody là lỗi JSON thường (ngoài /fhir/R4).
type ErrorBody struct {
	Error string `json:"error" binding:"required" example:"Not Found"` // thông điệp lỗi
}

// healthz godoc
//
//	@Summary	Liveness probe
//	@Description	Tiến trình còn sống; luôn 200 khi server chạy. Không kiểm phụ thuộc nên orchestrator không khởi động lại tiến trình vì lỗi DB hay Redis.
//	@Tags		health
//	@Produce		application/json
//	@Success	200	{object}	HealthResponse
//	@Router		/healthz [get]
func healthz() {}

// readyz godoc
//
//	@Summary	Readiness probe (kiểm tra phụ thuộc)
//	@Description	200 khi nhận được lưu lượng; 503 khi đang tắt (drain) hoặc một phụ thuộc hỏng (database trong READINESS_TIMEOUT, broker khi bật Subscription). Body cho biết từng phụ thuộc.
//	@Tags		health
//	@Produce		application/json
//	@Success	200	{object}	ReadyResponse
//	@Failure	503	{object}	ReadyResponse
//	@Router		/readyz [get]
func readyz() {}

// ExportOutputFile là một tệp NDJSON trong manifest của bulk export.
type ExportOutputFile struct {
	Type  string `json:"type" binding:"required" example:"Patient"`                                    // loại resource trong tệp NDJSON
	URL   string `json:"url" binding:"required" example:"http://localhost:8005/fhir/R4/Binary/abc123"` // địa chỉ tải tệp (Binary)
	Count int    `json:"count,omitempty" example:"120"`                                                // số resource trong tệp
}

// ExportManifest là body 200 của bulkdata/{id}/status khi export đã xong (theo FHIR Bulk Data Access).
type ExportManifest struct {
	TransactionTime     string             `json:"transactionTime" binding:"required" example:"2026-11-21T10:00:00Z" format:"date-time"`     // thời điểm chụp dữ liệu của export
	Request             string             `json:"request" binding:"required" example:"http://localhost:8005/fhir/R4/$export?_type=Patient"` // URL yêu cầu export ban đầu
	RequiresAccessToken bool               `json:"requiresAccessToken" binding:"required" example:"true"`                                    // true khi tải tệp cần Bearer token
	Output              []ExportOutputFile `json:"output" binding:"required"`                                                                // các tệp kết quả
	Error               []any              `json:"error" binding:"required"`                                                                 // các lỗi từng phần (OperationOutcome); rỗng khi không có
}

// capabilities godoc
//
//	@Summary		CapabilityStatement
//	@Description	Mô tả server: các loại resource, interaction, search param và operation được hỗ trợ. Công khai, không cần token.
//	@Tags			fhir
//	@Produce		application/fhir+json
//	@Success		200	{object}	fhir.CapabilityStatement
//	@Router			/fhir/R4/metadata [get]
func capabilities() {}

// searchType godoc
//
//	@Summary		Tìm kiếm theo loại resource
//	@Description	Trả Bundle searchset. Ngoài các tham số điều khiển bên dưới, mọi search param của loại resource (ví dụ Patient: family, given, birthdate, identifier; Observation: code, subject, date) truyền qua query string theo chuẩn FHIR, kèm modifier (:exact, :missing...), prefix so sánh, chuỗi chained (subject.name=...) và _has. Danh sách đầy đủ theo từng loại ở GET /fhir/R4/metadata.
//	@Tags			fhir
//	@Security		BearerAuth
//	@Produce		application/fhir+json
//	@Param			resourceType	path		string	true	"Loại resource, ví dụ Patient"
//	@Param			_count			query		int		false	"Số kết quả mỗi trang. Ví dụ: 10"
//	@Param			_offset			query		int		false	"Vị trí bắt đầu (phân trang)"
//	@Param			_sort			query		string	false	"Sắp xếp, phân cách bằng dấu phẩy; tiền tố - để giảm dần. Ví dụ: -_lastUpdated,family"
//	@Param			_total			query		string	false	"Cách tính tổng"	Enums(none, estimate, accurate)
//	@Param			_summary		query		string	false	"Rút gọn kết quả"	Enums(true, text, data, count, false)
//	@Param			_elements		query		string	false	"Chỉ trả các phần tử này, phân cách bằng dấu phẩy"
//	@Param			_include		query		string	false	"Kèm resource được tham chiếu. Ví dụ: Observation:subject"
//	@Param			_revinclude		query		string	false	"Kèm resource tham chiếu tới kết quả. Ví dụ: Observation:subject"
//	@Param			_id				query		string	false	"Lọc theo id (nhiều giá trị cách nhau bằng dấu phẩy)"
//	@Param			_compartment	query		string	false	"Giới hạn trong compartment của một bệnh nhân. Dạng Patient/<id>, chỉ loại thuộc compartment Patient. Ví dụ: Patient/123"
//	@Param			_lastUpdated	query		string	false	"Lọc theo thời điểm cập nhật, hỗ trợ tiền tố ge/le/gt/lt. Ví dụ: ge2026-01-01"
//	@Param			_filter			query		string	false	"Biểu thức _filter của FHIR"
//	@Success		200				{object}	fhir.Bundle
//	@Failure		400				{object}	fhir.OperationOutcome	"Search param không hợp lệ hoặc không hỗ trợ"
//	@Failure		401				{object}	fhir.OperationOutcome	"Thiếu hoặc sai Bearer token"
//	@Failure		403				{object}	fhir.OperationOutcome	"Token không đủ quyền (cần quyền read)"
//	@Router			/fhir/R4/{resourceType} [get]
func searchType() {}

// searchPost godoc
//
//	@Summary		Tìm kiếm dạng form (POST _search)
//	@Description	Tương đương GET /fhir/R4/{resourceType} nhưng tham số đi trong body form-urlencoded, dùng khi query string quá dài. Cũng nhận mọi search param của loại resource.
//	@Tags			fhir
//	@Security		BearerAuth
//	@Accept			application/x-www-form-urlencoded
//	@Produce		application/fhir+json
//	@Param			resourceType	path		string	true	"Loại resource"
//	@Param			_count			formData	int		false	"Số kết quả mỗi trang. Ví dụ: 10"
//	@Param			_offset			formData	int		false	"Vị trí bắt đầu (phân trang)"
//	@Param			_sort			formData	string	false	"Sắp xếp, phân cách bằng dấu phẩy; tiền tố - để giảm dần. Ví dụ: -_lastUpdated,family"
//	@Param			_total			formData	string	false	"Cách tính tổng"	Enums(none, estimate, accurate)
//	@Param			_summary		formData	string	false	"Rút gọn kết quả"	Enums(true, text, data, count, false)
//	@Param			_elements		formData	string	false	"Chỉ trả các phần tử này, phân cách bằng dấu phẩy"
//	@Param			_include		formData	string	false	"Kèm resource được tham chiếu. Ví dụ: Observation:subject"
//	@Param			_revinclude		formData	string	false	"Kèm resource tham chiếu tới kết quả. Ví dụ: Observation:subject"
//	@Param			_id				formData	string	false	"Lọc theo id (nhiều giá trị cách nhau bằng dấu phẩy)"
//	@Param			_lastUpdated	formData	string	false	"Lọc theo thời điểm cập nhật, hỗ trợ tiền tố ge/le/gt/lt. Ví dụ: ge2026-01-01"
//	@Param			_filter			formData	string	false	"Biểu thức _filter của FHIR"
//	@Success		200				{object}	fhir.Bundle
//	@Failure		400				{object}	fhir.OperationOutcome	"Search param không hợp lệ"
//	@Failure		401				{object}	fhir.OperationOutcome	"Thiếu hoặc sai Bearer token"
//	@Failure		403				{object}	fhir.OperationOutcome	"Token không đủ quyền (cần quyền read)"
//	@Router			/fhir/R4/{resourceType}/_search [post]
func searchPost() {}

// create godoc
//
//	@Summary		Tạo resource
//	@Description	Hỗ trợ If-None-Exist (create có điều kiện). Trả 201 kèm Location và ETag. Sai cấu trúc/kiểu dữ liệu trả 400; giá trị code không thuộc ValueSet (ví dụ gender sai) hiện chưa bị từ chối.
//	@Tags			fhir
//	@Security		BearerAuth
//	@Accept			application/fhir+json
//	@Produce		application/json,application/fhir+json
//	@Param			resourceType	path		string			true	"Loại resource"
//	@Param			body			body		PatientBody	true	"Resource (mẫu là Patient)"
//	@Param			If-None-Exist	header		string			false	"Điều kiện search; nếu đã có resource khớp thì không tạo mới. Ví dụ: identifier=123"
//	@Success		201				{object}	FHIRResource	"Đã tạo; header Location và ETag"
//	@Success		200				{object}	FHIRResource	"If-None-Exist khớp đúng một resource, trả resource đó"
//	@Failure		400				{object}	fhir.OperationOutcome	"Sai cấu trúc, kiểu dữ liệu hoặc invariant"
//	@Failure		401				{object}	fhir.OperationOutcome	"Thiếu hoặc sai Bearer token"
//	@Failure		403				{object}	fhir.OperationOutcome	"Token không đủ quyền (cần quyền write)"
//	@Failure		412				{object}	fhir.OperationOutcome	"If-None-Exist khớp nhiều resource"
//	@Router			/fhir/R4/{resourceType} [post]
func create() {}

// read godoc
//
//	@Summary		Đọc resource
//	@Description	Hỗ trợ If-None-Match (ETag) và If-Modified-Since: trả 304 nếu không đổi.
//	@Tags			fhir
//	@Security		BearerAuth
//	@Produce		application/json,application/fhir+json
//	@Param			resourceType	path		string	true	"Loại resource"
//	@Param			id				path		string	true	"Id"
//	@Success		200				{object}	FHIRResource	"Header ETag = W/\"versionId\""
//	@Success		304				"Không đổi so với If-None-Match / If-Modified-Since"
//	@Failure		401				{object}	fhir.OperationOutcome	"Thiếu hoặc sai Bearer token"
//	@Failure		403				{object}	fhir.OperationOutcome	"Token không đủ quyền (cần quyền read)"
//	@Failure		404				{object}	fhir.OperationOutcome	"Không tồn tại"
//	@Failure		410				{object}	fhir.OperationOutcome	"Đã bị xóa"
//	@Router			/fhir/R4/{resourceType}/{id} [get]
func read() {}

// update godoc
//
//	@Summary		Cập nhật resource
//	@Description	Hỗ trợ If-Match (versionId) và update-as-create nếu bật. Body phải có id khớp với đường dẫn.
//	@Tags			fhir
//	@Security		BearerAuth
//	@Accept			application/fhir+json
//	@Produce		application/json,application/fhir+json
//	@Param			resourceType	path		string			true	"Loại resource"
//	@Param			id				path		string			true	"Id"
//	@Param			body			body		PatientBody	true	"Resource (mẫu là Patient)"
//	@Param			If-Match		header		string			false	"ETag phiên bản đang sửa, ví dụ W/\"2\""
//	@Success		200				{object}	FHIRResource	"Đã cập nhật; header ETag mới"
//	@Success		201				{object}	FHIRResource	"Tạo mới (update-as-create)"
//	@Failure		400				{object}	fhir.OperationOutcome	"Sai cấu trúc hoặc id không khớp"
//	@Failure		401				{object}	fhir.OperationOutcome	"Thiếu hoặc sai Bearer token"
//	@Failure		403				{object}	fhir.OperationOutcome	"Token không đủ quyền (cần quyền write)"
//	@Failure		404				{object}	fhir.OperationOutcome	"Không tồn tại và update-as-create đang tắt"
//	@Failure		412				{object}	fhir.OperationOutcome	"If-Match không khớp phiên bản hiện tại"
//	@Router			/fhir/R4/{resourceType}/{id} [put]
func update() {}

// patch godoc
//
//	@Summary		Patch resource (JSON Patch)
//	@Description	Body là mảng thao tác RFC 6902; Content-Type bắt buộc application/json-patch+json. Hỗ trợ If-Match.
//	@Tags			fhir
//	@Security		BearerAuth
//	@Accept			application/json-patch+json
//	@Produce		application/json,application/fhir+json
//	@Param			resourceType	path		string			true	"Loại resource"
//	@Param			id				path		string			true	"Id"
//	@Param			body			body		[]JSONPatchOp	true	"Các thao tác JSON Patch"
//	@Param			If-Match		header		string			false	"ETag phiên bản đang sửa"
//	@Success		200				{object}	FHIRResource	"Đã patch; header ETag mới"
//	@Failure		400				{object}	fhir.OperationOutcome	"Patch sai cú pháp hoặc kết quả không hợp lệ"
//	@Failure		401				{object}	fhir.OperationOutcome	"Thiếu hoặc sai Bearer token"
//	@Failure		403				{object}	fhir.OperationOutcome	"Token không đủ quyền (cần quyền write)"
//	@Failure		404				{object}	fhir.OperationOutcome	"Không tồn tại"
//	@Failure		412				{object}	fhir.OperationOutcome	"If-Match không khớp phiên bản hiện tại"
//	@Failure		415				{object}	fhir.OperationOutcome	"Content-Type không phải application/json-patch+json"
//	@Router			/fhir/R4/{resourceType}/{id} [patch]
func patch() {}

// deleteResource godoc
//
//	@Summary		Xóa resource
//	@Description	Xóa mềm: resource vẫn nằm trong _history, đọc lại trả 410. Xóa vĩnh viễn dùng $expunge.
//	@Tags			fhir
//	@Security		BearerAuth
//	@Produce		application/fhir+json
//	@Param			resourceType	path	string	true	"Loại resource"
//	@Param			id				path	string	true	"Id"
//	@Success		204
//	@Failure		401				{object}	fhir.OperationOutcome	"Thiếu hoặc sai Bearer token"
//	@Failure		403				{object}	fhir.OperationOutcome	"Token không đủ quyền (cần quyền write)"
//	@Failure		404				{object}	fhir.OperationOutcome	"Không tồn tại"
//	@Router			/fhir/R4/{resourceType}/{id} [delete]
func deleteResource() {}

// conditional godoc
//
//	@Summary		Update/patch/delete có điều kiện
//	@Description	Dùng query search làm điều kiện: PUT, PATCH và DELETE trên /{resourceType}?... (cùng đường dẫn, khác method). Khớp 0 resource: PUT tạo mới, PATCH/DELETE trả 404; khớp nhiều resource trả 412.
//	@Tags			fhir
//	@Security		BearerAuth
//	@Accept			application/fhir+json
//	@Produce		application/json,application/fhir+json
//	@Param			resourceType	path		string			true	"Loại resource"
//	@Param			body			body		PatientBody	false	"Resource (PUT, mẫu là Patient) hoặc JSON Patch (PATCH)"
//	@Success		200				{object}	FHIRResource	"Đã cập nhật"
//	@Success		201				{object}	FHIRResource	"Không có resource khớp, đã tạo mới (PUT)"
//	@Failure		400				{object}	fhir.OperationOutcome	"Thiếu điều kiện search hoặc body sai"
//	@Failure		401				{object}	fhir.OperationOutcome	"Thiếu hoặc sai Bearer token"
//	@Failure		403				{object}	fhir.OperationOutcome	"Token không đủ quyền (cần quyền write)"
//	@Failure		412				{object}	fhir.OperationOutcome	"Điều kiện khớp nhiều resource"
//	@Router			/fhir/R4/{resourceType} [put]
func conditional() {}

// history godoc
//
//	@Summary		Lịch sử các phiên bản
//	@Description	Trả Bundle type history, phiên bản mới nhất trước. Gồm cả phiên bản đã xóa.
//	@Tags			fhir
//	@Security		BearerAuth
//	@Produce		application/fhir+json
//	@Param			resourceType	path		string	true	"Loại resource"
//	@Param			id				path		string	true	"Id"
//	@Param			_count			query		int		false	"Số phiên bản mỗi trang. Ví dụ: 10"
//	@Param			_offset			query		int		false	"Vị trí bắt đầu"
//	@Success		200				{object}	fhir.Bundle
//	@Failure		401				{object}	fhir.OperationOutcome	"Thiếu hoặc sai Bearer token"
//	@Failure		403				{object}	fhir.OperationOutcome	"Token không đủ quyền (cần quyền read)"
//	@Failure		404				{object}	fhir.OperationOutcome	"Không tồn tại"
//	@Router			/fhir/R4/{resourceType}/{id}/_history [get]
func history() {}

// vread godoc
//
//	@Summary		Đọc một phiên bản
//	@Description	Đọc đúng phiên bản vid của resource.
//	@Tags			fhir
//	@Security		BearerAuth
//	@Produce		application/json,application/fhir+json
//	@Param			resourceType	path		string	true	"Loại resource"
//	@Param			id				path		string	true	"Id"
//	@Param			vid				path		string	true	"versionId"
//	@Success		200				{object}	FHIRResource
//	@Failure		401				{object}	fhir.OperationOutcome	"Thiếu hoặc sai Bearer token"
//	@Failure		403				{object}	fhir.OperationOutcome	"Token không đủ quyền (cần quyền read)"
//	@Failure		404				{object}	fhir.OperationOutcome	"Không có phiên bản này"
//	@Failure		410				{object}	fhir.OperationOutcome	"Phiên bản là bản xóa"
//	@Router			/fhir/R4/{resourceType}/{id}/_history/{vid} [get]
func vread() {}

// batch godoc
//
//	@Summary		Batch / transaction
//	@Description	POST Bundle (type batch hoặc transaction) vào gốc. Cần quyền write. Transaction là tất cả hoặc không: một entry lỗi thì rollback toàn bộ. Batch xử lý từng entry độc lập, lỗi nằm trong response của entry đó.
//	@Tags			fhir
//	@Security		BearerAuth
//	@Accept			application/fhir+json
//	@Produce		application/fhir+json
//	@Param			body	body		BundleBody	true	"Bundle batch hoặc transaction"
//	@Success		200		{object}	fhir.Bundle	"Bundle batch-response hoặc transaction-response"
//	@Failure		400		{object}	fhir.OperationOutcome	"Bundle sai cấu trúc hoặc type không hỗ trợ"
//	@Failure		401		{object}	fhir.OperationOutcome	"Thiếu hoặc sai Bearer token"
//	@Failure		403		{object}	fhir.OperationOutcome	"Token không đủ quyền (cần quyền write)"
//	@Router			/fhir/R4 [post]
func batch() {}

// validate godoc
//
//	@Summary		$validate
//	@Description	Kiểm tra resource theo cấu trúc, kiểu, cardinality và invariant FHIRPath mà không lưu. Kết quả luôn là OperationOutcome; lỗi dữ liệu nằm trong issue, không phải mã HTTP 4xx.
//	@Tags			operation
//	@Security		BearerAuth
//	@Accept			application/fhir+json
//	@Produce		application/fhir+json
//	@Param			resourceType	path		string			true	"Loại resource"
//	@Param			body			body		PatientBody	true	"Resource cần kiểm tra (hoặc Parameters có parameter resource)"
//	@Success		200				{object}	fhir.OperationOutcome	"Các issue tìm thấy (nếu có)"
//	@Failure		400				{object}	fhir.OperationOutcome	"Body không phải JSON FHIR hợp lệ"
//	@Failure		401				{object}	fhir.OperationOutcome	"Thiếu hoặc sai Bearer token"
//	@Failure		403				{object}	fhir.OperationOutcome	"Token không đủ quyền (cần quyền read)"
//	@Router			/fhir/R4/{resourceType}/$validate [post]
func validate() {}

// everything godoc
//
//	@Summary		Patient/$everything
//	@Description	Trả Bundle gồm hồ sơ Patient và các resource thuộc compartment của bệnh nhân đó.
//	@Tags			operation
//	@Security		BearerAuth
//	@Produce		application/fhir+json
//	@Param			id		path		string	true	"Patient id"
//	@Param			_count	query		int		false	"Số resource mỗi trang. Ví dụ: 10"
//	@Param			_since	query		string	false	"Chỉ lấy resource cập nhật từ thời điểm này"
//	@Param			_type	query		string	false	"Chỉ lấy các loại resource này, phân cách bằng dấu phẩy"
//	@Success		200		{object}	fhir.Bundle
//	@Failure		401		{object}	fhir.OperationOutcome	"Thiếu hoặc sai Bearer token"
//	@Failure		403		{object}	fhir.OperationOutcome	"Token không đủ quyền (cần quyền read)"
//	@Failure		404		{object}	fhir.OperationOutcome	"Patient không tồn tại"
//	@Router			/fhir/R4/Patient/{id}/$everything [get]
func everything() {}

// reindex godoc
//
//	@Summary		$reindex cả loại resource (job nền)
//	@Description	Cần quyền admin. Trả 202 kèm Content-Location tới /fhir/R4/job/{id}/status.
//	@Tags			operation
//	@Security		BearerAuth
//	@Produce		application/fhir+json
//	@Param			resourceType	path		string	true	"Loại resource"
//	@Success		202				{object}	fhir.OperationOutcome	"Job đã nhận; header Content-Location"
//	@Failure		401				{object}	fhir.OperationOutcome	"Thiếu hoặc sai Bearer token"
//	@Failure		403				{object}	fhir.OperationOutcome	"Token không đủ quyền (cần quyền admin)"
//	@Router			/fhir/R4/{resourceType}/$reindex [post]
func reindex() {}

// reindexOne godoc
//
//	@Summary		$reindex một resource (đồng bộ)
//	@Description	Cần quyền admin. Chạy ngay trong request, trả khi xong.
//	@Tags			operation
//	@Security		BearerAuth
//	@Produce		application/fhir+json
//	@Param			resourceType	path		string	true	"Loại resource"
//	@Param			id				path		string	true	"Id"
//	@Success		200				{object}	fhir.OperationOutcome
//	@Failure		401				{object}	fhir.OperationOutcome	"Thiếu hoặc sai Bearer token"
//	@Failure		403				{object}	fhir.OperationOutcome	"Token không đủ quyền (cần quyền admin)"
//	@Failure		404				{object}	fhir.OperationOutcome	"Không tồn tại"
//	@Router			/fhir/R4/{resourceType}/{id}/$reindex [post]
func reindexOne() {}

// expunge godoc
//
//	@Summary		$expunge (xóa vĩnh viễn)
//	@Description	Cần quyền admin. Xóa vĩnh viễn resource cùng mọi phiên bản, không hoàn tác.
//	@Tags			operation
//	@Security		BearerAuth
//	@Produce		application/fhir+json
//	@Param			resourceType	path		string	true	"Loại resource"
//	@Param			id				path		string	true	"Id"
//	@Success		200				{object}	fhir.OperationOutcome
//	@Failure		401				{object}	fhir.OperationOutcome	"Thiếu hoặc sai Bearer token"
//	@Failure		403				{object}	fhir.OperationOutcome	"Token không đủ quyền (cần quyền admin)"
//	@Failure		404				{object}	fhir.OperationOutcome	"Không tồn tại"
//	@Router			/fhir/R4/{resourceType}/{id}/$expunge [post]
func expunge() {}

// packageInstall godoc
//
//	@Summary		PackageRelease/$install (nạp gói định nghĩa)
//	@Description	Cần quyền admin project. Đọc Bundle (batch hoặc transaction) trong Binary mà PackageRelease.content.url trỏ tới (dạng Binary/<id>, tối đa 64 MiB) và chạy với quyền của người gọi. Ghi một PackageInstallation: installing rồi installed, hoặc error khi có entry lỗi (transaction được hoàn tác). Trả Bundle kết quả.
//	@Tags			operation
//	@Security		BearerAuth
//	@Produce		application/fhir+json
//	@Param			id	path		string	true	"Id của PackageRelease"
//	@Success		200	{object}	fhir.Bundle
//	@Failure		400	{object}	fhir.OperationOutcome	"Gói không phải Bundle JSON, content.url sai dạng hoặc có entry lỗi"
//	@Failure		401	{object}	fhir.OperationOutcome	"Thiếu hoặc sai Bearer token"
//	@Failure		403	{object}	fhir.OperationOutcome	"Token không đủ quyền (cần quyền admin)"
//	@Failure		404	{object}	fhir.OperationOutcome	"PackageRelease hoặc Binary không tồn tại"
//	@Router			/fhir/R4/PackageRelease/{id}/$install [post]
func packageInstall() {}

// codeSystemImport godoc
//
//	@Summary		CodeSystem/$import (nạp concept cho bảng mã lớn)
//	@Description	Cần quyền admin project. Body Parameters: system (uri; bỏ nếu dùng đường dẫn có id), concept (valueCoding: code, display), property (part code, property, value) và designation (part code, language, value); mỗi loại tối đa 10000 phần tử mỗi lần gọi. Concept lưu ở bảng dẫn xuất, không sửa JSON của CodeSystem. Property phải khai báo trong CodeSystem.property (hoặc là parent/hierarchyMeaning), code phải đã có hoặc có trong cùng lần gọi; lỗi thì hoàn tác cả lần gọi. Gọi lại với cùng dữ liệu là an toàn. Trả Parameters với return là CodeSystem.
//	@Tags			operation
//	@Security		BearerAuth
//	@Accept			application/fhir+json
//	@Produce		application/fhir+json
//	@Param			id		path		string	true	"Id của CodeSystem"
//	@Param			body	body		fhir.Parameters	true	"Parameters"
//	@Success		200		{object}	fhir.Parameters
//	@Failure		400		{object}	fhir.OperationOutcome	"Thiếu CodeSystem, property chưa khai báo hoặc code không xác định"
//	@Failure		401		{object}	fhir.OperationOutcome	"Thiếu hoặc sai Bearer token"
//	@Failure		403		{object}	fhir.OperationOutcome	"Token không đủ quyền (cần quyền admin)"
//	@Failure		404		{object}	fhir.OperationOutcome	"CodeSystem không tồn tại"
//	@Router			/fhir/R4/CodeSystem/{id}/$import [post]
func codeSystemImport() {}

// codeSystemImportByURL godoc
//
//	@Summary		CodeSystem/$import theo system (không cần id)
//	@Description	Như CodeSystem/{id}/$import nhưng chọn CodeSystem bằng tham số system (url canonical) trong body.
//	@Tags			operation
//	@Security		BearerAuth
//	@Accept			application/fhir+json
//	@Produce		application/fhir+json
//	@Param			body	body		fhir.Parameters	true	"Parameters có system"
//	@Success		200		{object}	fhir.Parameters
//	@Failure		400		{object}	fhir.OperationOutcome	"Thiếu system, property chưa khai báo hoặc code không xác định"
//	@Failure		401		{object}	fhir.OperationOutcome	"Thiếu hoặc sai Bearer token"
//	@Failure		403		{object}	fhir.OperationOutcome	"Token không đủ quyền (cần quyền admin)"
//	@Failure		404		{object}	fhir.OperationOutcome	"CodeSystem không tồn tại"
//	@Router			/fhir/R4/CodeSystem/$import [post]
func codeSystemImportByURL() {}

// conceptMapImport godoc
//
//	@Summary		ConceptMap/$import (nạp ánh xạ cho bảng ánh xạ lớn)
//	@Description	Cần quyền admin project. Body Parameters: mapping (lặp, tối đa 10000 mỗi lần gọi; part source và target kiểu Coding, target.system bắt buộc, để trống target.code cho ánh xạ rỗng; relationship mặc định equivalent; comment; property, dependsOn, product với part code và value[x]). Ánh xạ lưu ở bảng dẫn xuất, không sửa JSON của ConceptMap, và ConceptMap/$translate đọc từ đó. Nạp lại cùng cặp nguồn và đích là ghi đè. Trả ConceptMap.
//	@Tags			operation
//	@Security		BearerAuth
//	@Accept			application/fhir+json
//	@Produce		application/fhir+json
//	@Param			id		path		string	true	"Id của ConceptMap"
//	@Param			body	body		fhir.Parameters	true	"Parameters"
//	@Success		200		{object}	fhir.ConceptMap
//	@Failure		400		{object}	fhir.OperationOutcome	"Thiếu mapping, source.code hoặc target.system"
//	@Failure		401		{object}	fhir.OperationOutcome	"Thiếu hoặc sai Bearer token"
//	@Failure		403		{object}	fhir.OperationOutcome	"Token không đủ quyền (cần quyền admin)"
//	@Failure		404		{object}	fhir.OperationOutcome	"ConceptMap không tồn tại"
//	@Router			/fhir/R4/ConceptMap/{id}/$import [post]
func conceptMapImport() {}

// conceptMapImportByURL godoc
//
//	@Summary		ConceptMap/$import theo url (không cần id)
//	@Description	Như ConceptMap/{id}/$import nhưng chọn ConceptMap bằng tham số url (canonical) trong body.
//	@Tags			operation
//	@Security		BearerAuth
//	@Accept			application/fhir+json
//	@Produce		application/fhir+json
//	@Param			body	body		fhir.Parameters	true	"Parameters có url"
//	@Success		200		{object}	fhir.ConceptMap
//	@Failure		400		{object}	fhir.OperationOutcome	"Thiếu url, mapping, source.code hoặc target.system"
//	@Failure		401		{object}	fhir.OperationOutcome	"Thiếu hoặc sai Bearer token"
//	@Failure		403		{object}	fhir.OperationOutcome	"Token không đủ quyền (cần quyền admin)"
//	@Failure		404		{object}	fhir.OperationOutcome	"ConceptMap không tồn tại"
//	@Router			/fhir/R4/ConceptMap/$import [post]
func conceptMapImportByURL() {}

// jobStatus godoc
//
//	@Summary		Trạng thái job nền
//	@Description	Trạng thái của job tạo bởi $reindex (và các job nền khác): accepted, in-progress, completed hoặc error, kèm kết quả trong Parameters.
//	@Tags			operation
//	@Security		BearerAuth
//	@Produce		application/fhir+json
//	@Param			id	path		string	true	"Job id"
//	@Success		200	{object}	fhir.Parameters
//	@Failure		401		{object}	fhir.OperationOutcome	"Thiếu hoặc sai Bearer token"
//	@Failure		403		{object}	fhir.OperationOutcome	"Token không đủ quyền (cần quyền read)"
//	@Failure		404	{object}	fhir.OperationOutcome	"Job không tồn tại"
//	@Router			/fhir/R4/job/{id}/status [get]
func jobStatus() {}

// exportSystem godoc
//
//	@Summary		Bulk export ($export)
//	@Description	Cũng có Patient/$export và Group/{id}/$export. Bắt buộc header Prefer: respond-async. Trả 202 kèm Content-Location trỏ tới bulkdata/{id}/status; gọi URL đó (poll) cho tới khi nhận 200 và manifest.
//	@Tags			bulk
//	@Security		BearerAuth
//	@Produce		application/fhir+json
//	@Param			Prefer			header	string	true	"Bắt buộc: respond-async"	default(respond-async)
//	@Param			_type			query	string	false	"Chỉ export các loại resource này, phân cách bằng dấu phẩy"
//	@Param			_since			query	string	false	"Chỉ export resource cập nhật từ thời điểm này"
//	@Param			_outputFormat	query	string	false	"Định dạng tệp; chỉ hỗ trợ application/fhir+ndjson"
//	@Success		202				{object}	fhir.OperationOutcome	"Job đã nhận; header Content-Location tới bulkdata/{id}/status"
//	@Failure		400				{object}	fhir.OperationOutcome	"Thiếu Prefer: respond-async hoặc tham số sai"
//	@Failure		401				{object}	fhir.OperationOutcome	"Thiếu hoặc sai Bearer token"
//	@Failure		403				{object}	fhir.OperationOutcome	"Token không đủ quyền (cần quyền read)"
//	@Router			/fhir/R4/$export [get]
func exportSystem() {}

// exportStatus godoc
//
//	@Summary		Trạng thái bulk export
//	@Description	Poll sau khi $export. 202 là đang chạy (header X-Progress, Retry-After); 200 là đã xong kèm manifest các tệp NDJSON; mỗi url trong output tải qua GET (cần token).
//	@Tags			bulk
//	@Security		BearerAuth
//	@Produce		application/json,application/fhir+json
//	@Param			id	path		string	true	"Export id"
//	@Success		200	{object}	ExportManifest	"Đã xong"
//	@Success		202	"Đang chạy; header X-Progress: in-progress và Retry-After (giây)"
//	@Failure		401		{object}	fhir.OperationOutcome	"Thiếu hoặc sai Bearer token"
//	@Failure		403		{object}	fhir.OperationOutcome	"Token không đủ quyền (cần quyền read)"
//	@Failure		404	{object}	fhir.OperationOutcome	"Export không tồn tại"
//	@Router			/fhir/R4/bulkdata/{id}/status [get]
func exportStatus() {}

// exportCancel godoc
//
//	@Summary		Hủy bulk export
//	@Description	Hủy job export đang chạy.
//	@Tags			bulk
//	@Security		BearerAuth
//	@Produce		application/fhir+json
//	@Param			id	path		string	true	"Export id"
//	@Success		202	{object}	fhir.OperationOutcome	"Đã nhận yêu cầu hủy"
//	@Failure		401		{object}	fhir.OperationOutcome	"Thiếu hoặc sai Bearer token"
//	@Failure		403		{object}	fhir.OperationOutcome	"Token không đủ quyền (cần quyền read)"
//	@Failure		404	{object}	fhir.OperationOutcome	"Export không tồn tại"
//	@Router			/fhir/R4/bulkdata/{id}/status [delete]
func exportCancel() {}

// graphql godoc
//
//	@Summary		GraphQL
//	@Description	Truy vấn FHIR bằng GraphQL. Lỗi cú pháp hoặc truy vấn nằm trong trường errors của body, HTTP vẫn 200.
//	@Tags			fhir
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		application/json,application/fhir+json
//	@Param			body	body		GraphQLRequest	true	"{query, variables, operationName}"
//	@Success		200		{object}	GraphQLResponse
//	@Failure		401		{object}	fhir.OperationOutcome	"Thiếu hoặc sai Bearer token"
//	@Failure		403		{object}	fhir.OperationOutcome	"Token không đủ quyền (cần quyền read)"
//	@Router			/fhir/R4/$graphql [post]
func graphql() {}

// terminologyExpand godoc
//
//	@Summary		ValueSet/$expand
//	@Description	Mở rộng ValueSet thành danh sách code. Terminology còn có $lookup, $validate-code, $subsumes, $translate (GET và POST) với cùng dạng đường dẫn.
//	@Tags			terminology
//	@Security		BearerAuth
//	@Produce		application/fhir+json
//	@Param			url				query		string	true	"Canonical URL của ValueSet, có thể kèm |version"
//	@Param			valueSetVersion	query		string	false	"Phiên bản ValueSet (nếu url không có |version)"
//	@Param			filter			query		string	false	"Lọc code/display chứa chuỗi này"
//	@Param			count			query		int		false	"Số code tối đa"
//	@Param			offset			query		int		false	"Vị trí bắt đầu"
//	@Success		200				{object}	fhir.ValueSet
//	@Failure		400				{object}	fhir.OperationOutcome	"Thiếu url hoặc tham số sai"
//	@Failure		401				{object}	fhir.OperationOutcome	"Thiếu hoặc sai Bearer token"
//	@Failure		403				{object}	fhir.OperationOutcome	"Token không đủ quyền (cần quyền read)"
//	@Failure		404				{object}	fhir.OperationOutcome	"Không tìm thấy ValueSet"
//	@Router			/fhir/R4/ValueSet/$expand [get]
func terminologyExpand() {}

// terminologyLookup godoc
//
//	@Summary		CodeSystem/$lookup
//	@Description	Tra cứu thông tin một code trong CodeSystem: display, definition, property.
//	@Tags			terminology
//	@Security		BearerAuth
//	@Produce		application/fhir+json
//	@Param			system		query		string	true	"URL của CodeSystem"
//	@Param			code		query		string	true	"Code cần tra"
//	@Param			version		query		string	false	"Phiên bản CodeSystem"
//	@Param			property	query		string	false	"Property cần trả, lặp tham số để chọn nhiều"
//	@Success		200			{object}	fhir.Parameters
//	@Failure		400			{object}	fhir.OperationOutcome	"Thiếu system hoặc code"
//	@Failure		401			{object}	fhir.OperationOutcome	"Thiếu hoặc sai Bearer token"
//	@Failure		403			{object}	fhir.OperationOutcome	"Token không đủ quyền (cần quyền read)"
//	@Failure		404			{object}	fhir.OperationOutcome	"Không tìm thấy code"
//	@Router			/fhir/R4/CodeSystem/$lookup [get]
func terminologyLookup() {}
