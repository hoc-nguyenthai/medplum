package httpapi

// Tệp này chỉ chứa annotation swaggo để sinh OpenAPI (xem `make swagger APP=dicomweb`); các hàm rỗng không được gắn vào router. Sửa annotation xong
// phải chạy lại `make swagger APP=dicomweb` để cập nhật apps/dicomweb/docs. Ở D1 các route DICOMweb mới là khung (501); D3 và D4 bổ sung DTO và phản hồi thật.

// @title						Trivita DICOMweb API
// @version					1.0
// @description				DICOMweb (STOW-RS, QIDO-RS, WADO-RS) cho kho hình ảnh DICOM, dùng chung schema và kho Binary với fhir-server. Lỗi trả {"error": "..."}. STOW-RS (D3), QIDO-RS (tìm study, series) và WADO-RS (metadata series, frame) (D4) đã cài; các route DICOMweb còn lại trả 501.
// @BasePath					/
// @tag.name					dicomweb
// @tag.description			DICOMweb. Bốn tiền tố tương đương: /dicomweb, /api/dicomweb, /projects/{projectId}/dicomweb, /api/projects/{projectId}/dicomweb; với hai tiền tố có project, projectId phải khớp project của token (token thường), không thì 403. Spec này liệt kê tiền tố /dicomweb.
// @tag.name					health
// @tag.description			Probe liveness, startup và readiness cho orchestrator.
// @securityDefinitions.apikey	BearerAuth
// @in							header
// @name						Authorization
// @description				Dạng "Bearer <access_token>": JWT do auth cấp (kiểm bằng JWKS), có claim project. Đọc (QIDO, WADO) cần quyền đọc, ghi (STOW) cần quyền ghi.

// ErrorBody là lỗi JSON của mọi route.
type ErrorBody struct {
	Error string `json:"error" binding:"required" example:"Token không hợp lệ"` // thông điệp lỗi; lỗi 5xx chỉ là mô tả trạng thái chuẩn
}

// DicomJSONElement là một phần tử của DICOM JSON model (PS3.18 F.2).
type DicomJSONElement struct {
	VR           string `json:"vr" binding:"required" example:"UI" enums:"AE,AS,AT,CS,DA,DS,DT,FL,FD,IS,LO,LT,OB,OD,OF,OL,OV,OW,PN,SH,SL,SQ,SS,ST,SV,TM,UC,UI,UL,UN,UR,US,UT,UV"` // Value Representation của phần tử
	Value        []any  `json:"Value,omitempty"`                                                                                                                                  // danh sách giá trị: chuỗi, số, tên người ({"Alphabetic": ...}) hoặc dataset (phần tử của SQ)
	InlineBinary string `json:"InlineBinary,omitempty" example:"AQID"`                                                                                                            // dữ liệu nhị phân nhỏ dạng base64
	BulkDataURI  string `json:"BulkDataURI,omitempty"`                                                                                                                            // URI lấy dữ liệu nhị phân lớn
}

// StowResponse là phản hồi STOW-RS (PS3.18 10.5.3): dataset DICOM JSON, khóa là tag 8 chữ số hex. 00081190 là RetrieveURL cấp study (khi mọi instance thành công cùng một study), 00081199 là
// ReferencedSOPSequence (mỗi instance đã lưu: 00081150 ReferencedSOPClassUID, 00081155 ReferencedSOPInstanceUID, 00081190 RetrieveURL), 00081198 là FailedSOPSequence (mỗi instance thất bại, kèm
// 00081197 FailureReason: 272 = 0x0110 lỗi xử lý, 42752 = 0xA700 hết tài nguyên (quá lớn), 43264 = 0xA900 không thuộc study của yêu cầu, 49152 = 0xC000 không hiểu được tệp).
type StowResponse map[string]DicomJSONElement

// QueryResponse là phản hồi QIDO-RS và metadata series: mảng DICOM JSON, mỗi phần tử là một dataset (study, series kèm thuộc tính study, hoặc metadata của một instance:
// không có file meta, PixelData, overlay; nhị phân nhỏ là InlineBinary).
type QueryResponse []map[string]DicomJSONElement

// HealthResponse là phản hồi của probe liveness và startup.
type HealthResponse struct {
	Status string `json:"status" binding:"required" example:"ok"` // luôn "ok" khi tiến trình còn sống
}

// ReadyResponse là phản hồi của probe readiness.
type ReadyResponse struct {
	Ready  bool            `json:"ready" binding:"required" example:"true"` // true khi mọi phụ thuộc chính sẵn sàng và không đang tắt
	Checks map[string]bool `json:"checks" binding:"required"`               // kết quả từng phụ thuộc (database)
}

// healthLive godoc
//
//	@Summary		Liveness probe
//	@Description	Tiến trình còn sống; không kiểm phụ thuộc ngoài để orchestrator không restart pod vì lỗi của cơ sở dữ liệu.
//	@Tags			health
//	@Produce		json
//	@Success		200	{object}	HealthResponse
//	@Router			/health/live [get]
func healthLive() {}

// healthStartup godoc
//
//	@Summary		Startup probe
//	@Description	Đã khởi động xong (route đã sẵn sàng); không kiểm phụ thuộc ngoài.
//	@Tags			health
//	@Produce		json
//	@Success		200	{object}	HealthResponse
//	@Router			/health/startup [get]
func healthStartup() {}

// healthReady godoc
//
//	@Summary		Readiness probe
//	@Description	Sẵn sàng nhận yêu cầu: cơ sở dữ liệu trả lời trong READINESS_TIMEOUT. 503 khi đang tắt (SIGTERM) hoặc phụ thuộc lỗi.
//	@Tags			health
//	@Produce		json
//	@Success		200	{object}	ReadyResponse
//	@Failure		503	{object}	ReadyResponse	"Chưa sẵn sàng hoặc đang tắt"
//	@Router			/health/ready [get]
func healthReady() {}

// healthz godoc
//
//	@Summary		Healthcheck của Docker
//	@Description	Giống liveness; dùng cho HEALTHCHECK của image (`/app healthcheck`).
//	@Tags			health
//	@Produce		json
//	@Success		200	{object}	HealthResponse
//	@Router			/healthz [get]
func healthz() {}

// stowStudies godoc
//
//	@Summary		STOW-RS: lưu instance
//	@Description	Thân là multipart/related; type="application/dicom" (mỗi phần là một tệp DICOM Part 10), truyền luồng: phần đầu (tối đa DICOMWEB_MAX_HEADER_BYTES) được kiểm tra, nội dung ghi vào kho Binary dùng chung với fhir-server, metadata thành DicomStudy, DicomSeries, DicomInstance (tìm được qua API FHIR). Tải lại cùng SOPInstanceUID là idempotent. Từng instance được xử lý độc lập: 200 khi mọi instance thành công, 202 khi một phần (kèm FailedSOPSequence), 409 khi không instance nào thành công. Instance hỏng, bị cắt giữa PixelData, SQ lồng quá sâu, vượt DICOMWEB_MAX_INSTANCE_BYTES bị từ chối riêng và không để lại gì. Cần quyền ghi.
//	@Tags			dicomweb
//	@Security		BearerAuth
//	@Accept			multipart/related
//	@Produce		application/dicom+json,json
//	@Success		200	{object}	StowResponse	"Mọi instance đã lưu"
//	@Success		202	{object}	StowResponse	"Một phần: có cả ReferencedSOPSequence và FailedSOPSequence"
//	@Failure		400	{object}	ErrorBody		"Thân multipart sai, thiếu boundary hoặc không có phần nào"
//	@Failure		401	{object}	ErrorBody		"Thiếu hoặc sai Bearer token"
//	@Failure		403	{object}	ErrorBody		"Token không đủ quyền hoặc chọn project khác"
//	@Failure		409	{object}	StowResponse	"Không instance nào được lưu (FailedSOPSequence nêu lý do từng instance)"
//	@Failure		413	{object}	ErrorBody		"Body vượt DICOMWEB_MAX_REQUEST_BYTES"
//	@Failure		415	{object}	ErrorBody		"Content-Type không phải multipart/related"
//	@Router			/dicomweb/studies [post]
func stowStudies() {}

// stowStudy godoc
//
//	@Summary		STOW-RS: lưu instance vào một study
//	@Description	Như STOW-RS ở /studies nhưng mọi instance phải thuộc study chỉ định; instance của study khác bị từ chối riêng với FailureReason 0xA900. Cần quyền ghi.
//	@Tags			dicomweb
//	@Security		BearerAuth
//	@Accept			multipart/related
//	@Produce		application/dicom+json,json
//	@Param			studyUid	path		string			true	"UID của study (StudyInstanceUID)"
//	@Success		200			{object}	StowResponse	"Mọi instance đã lưu"
//	@Success		202			{object}	StowResponse	"Một phần: có cả ReferencedSOPSequence và FailedSOPSequence"
//	@Failure		400			{object}	ErrorBody		"Thân multipart sai, thiếu boundary hoặc không có phần nào"
//	@Failure		401			{object}	ErrorBody		"Thiếu hoặc sai Bearer token"
//	@Failure		403			{object}	ErrorBody		"Token không đủ quyền hoặc chọn project khác"
//	@Failure		409			{object}	StowResponse	"Không instance nào được lưu"
//	@Failure		413			{object}	ErrorBody		"Body vượt DICOMWEB_MAX_REQUEST_BYTES"
//	@Failure		415			{object}	ErrorBody		"Content-Type không phải multipart/related"
//	@Router			/dicomweb/studies/{studyUid} [post]
func stowStudy() {}

// qidoStudies godoc
//
//	@Summary		QIDO-RS: tìm study
//	@Description	Trả các study của project (mới cập nhật trước) dạng DICOM JSON. Hiện chỉ hỗ trợ phân trang (limit, offset); bộ lọc theo thuộc tính (PatientID, StudyDate...) chưa có và các tham số khác bị bỏ qua. Khi còn kết quả có header `Warning: 299`. Không có kết quả thì 200 với []. Cần quyền đọc.
//	@Tags			dicomweb
//	@Security		BearerAuth
//	@Produce		application/dicom+json,json
//	@Param			limit	query		string	false	"Cỡ trang (mặc định 100, tối đa 1000); giá trị hỏng dùng mặc định"
//	@Param			offset	query		string	false	"Số kết quả bỏ qua; giá trị hỏng coi là 0"
//	@Success		200	{object}	QueryResponse	"Mảng DICOM JSON"
//	@Failure		400	{object}	ErrorBody	"Tham số không hợp lệ"
//	@Failure		401	{object}	ErrorBody	"Thiếu hoặc sai Bearer token"
//	@Failure		403	{object}	ErrorBody	"Token không đủ quyền hoặc chọn project khác"
//	@Failure		500	{object}	ErrorBody	"Lỗi xử lý (không lộ chi tiết nội bộ)"
//	@Router			/dicomweb/studies [get]
func qidoStudies() {}

// qidoSeries godoc
//
//	@Summary		QIDO-RS: tìm series của study
//	@Description	Trả các series của study dạng DICOM JSON, mỗi phần tử gồm cả thuộc tính của study. Cần quyền đọc.
//	@Tags			dicomweb
//	@Security		BearerAuth
//	@Produce		application/dicom+json,json
//	@Param			studyUid	path		string	true	"UID của study (StudyInstanceUID)"
//	@Param			limit	query		string	false	"Cỡ trang (mặc định 100, tối đa 1000); giá trị hỏng dùng mặc định"
//	@Param			offset	query		string	false	"Số kết quả bỏ qua; giá trị hỏng coi là 0"
//	@Success		200	{object}	QueryResponse	"Mảng DICOM JSON"
//	@Failure		400	{object}	ErrorBody	"Tham số không hợp lệ"
//	@Failure		401	{object}	ErrorBody	"Thiếu hoặc sai Bearer token"
//	@Failure		403	{object}	ErrorBody	"Token không đủ quyền hoặc chọn project khác"
//	@Failure		404	{object}	ErrorBody	"Study hoặc series không có"
//	@Failure		500	{object}	ErrorBody	"Lỗi xử lý (không lộ chi tiết nội bộ)"
//	@Router			/dicomweb/studies/{studyUid}/series [get]
func qidoSeries() {}

// wadoSeriesMetadata godoc
//
//	@Summary		WADO-RS: metadata của series
//	@Description	Trả mảng metadata DICOM JSON của mọi instance trong series, theo luồng (không bị cắt ở 1000 instance như Medplum). Study hoặc series không có thì 404 trước khi ghi byte nào; lỗi giữa chừng thì ngắt kết nối. Cần quyền đọc.
//	@Tags			dicomweb
//	@Security		BearerAuth
//	@Produce		application/dicom+json,json
//	@Param			studyUid	path		string	true	"UID của study (StudyInstanceUID)"
//	@Param			seriesUid	path		string	true	"UID của series (SeriesInstanceUID)"
//	@Success		200	{object}	QueryResponse	"Mảng DICOM JSON"
//	@Failure		400	{object}	ErrorBody	"Tham số không hợp lệ"
//	@Failure		401	{object}	ErrorBody	"Thiếu hoặc sai Bearer token"
//	@Failure		403	{object}	ErrorBody	"Token không đủ quyền hoặc chọn project khác"
//	@Failure		404	{object}	ErrorBody	"Study hoặc series không có"
//	@Failure		500	{object}	ErrorBody	"Lỗi xử lý (không lộ chi tiết nội bộ)"
//	@Router			/dicomweb/studies/{studyUid}/series/{seriesUid}/metadata [get]
func wadoSeriesMetadata() {}

// wadoFrames godoc
//
//	@Summary		WADO-RS: lấy frame
//	@Description	Trả một hoặc nhiều frame dạng multipart/related (có Content-Length), chép theo luồng từ nội dung đã lưu (bộ nhớ hằng số). Content-Type của phần: application/octet-stream với dữ liệu không nén, image/jpeg, image/jls, image/jp2, image/jpx, image/jphc kèm transfer-syntax với dữ liệu nén. Frame đóng gói nhiều fragment được nối lại. Vị trí frame lấy từ chỉ mục lập lúc STOW và đọc theo khoảng nên chi phí tỉ lệ với kích thước frame (instance cũ chưa có chỉ mục hoặc kho không đọc theo khoảng thì quay về quét tệp). Cần quyền đọc, gồm cả quyền đọc Binary.
//	@Tags			dicomweb
//	@Security		BearerAuth
//	@Produce		multipart/related,json
//	@Param			studyUid	path		string	true	"UID của study (StudyInstanceUID)"
//	@Param			seriesUid	path		string	true	"UID của series (SeriesInstanceUID)"
//	@Param			instanceUid	path		string	true	"UID của instance (SOPInstanceUID)"
//	@Param			frame	path		string	true	"Số thứ tự frame bắt đầu từ 1, hoặc danh sách cách nhau bằng dấu phẩy (1,2,3; tối đa 50)"
//	@Success		200	{file}	file	"multipart/related: mỗi phần là một frame"
//	@Failure		400	{object}	ErrorBody	"Tham số không hợp lệ"
//	@Failure		401	{object}	ErrorBody	"Thiếu hoặc sai Bearer token"
//	@Failure		403	{object}	ErrorBody	"Token không đủ quyền hoặc chọn project khác"
//	@Failure		404	{object}	ErrorBody	"Study, series, instance không có, hoặc instance không có ảnh"
//	@Failure		416	{object}	ErrorBody	"Số frame vượt tổng số frame của instance"
//	@Failure		500	{object}	ErrorBody	"Lỗi xử lý (không lộ chi tiết nội bộ)"
//	@Router			/dicomweb/studies/{studyUid}/series/{seriesUid}/instances/{instanceUid}/frames/{frame} [get]
func wadoFrames() {}
