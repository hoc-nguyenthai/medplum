package httpapi

// Tệp này chỉ chứa annotation swaggo để sinh OpenAPI (xem `make swagger APP=dicomweb`); các hàm rỗng không được gắn vào router. Sửa annotation xong
// phải chạy lại `make swagger APP=dicomweb` để cập nhật apps/dicomweb/docs. Ở D1 các route DICOMweb mới là khung (501); D3 và D4 bổ sung DTO và phản hồi thật.

// @title						Trivita DICOMweb API
// @version					1.0
// @description				DICOMweb (STOW-RS, QIDO-RS, WADO-RS) cho kho hình ảnh DICOM, dùng chung schema và kho Binary với fhir-server. Lỗi trả {"error": "..."}. STOW-RS, QIDO-RS (study, series, instance, có bộ lọc thuộc tính), WADO-RS (tải instance, series, study; metadata; frame) đã cài; bulkdata và WADO-URI không có (501).
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
//	@Description	Trả các study của project (mới cập nhật trước) dạng DICOM JSON. Bộ lọc theo thuộc tính cấp study: PatientID, PatientName (khớp đầu chuỗi, dấu * ở cuối), StudyDate (ngày hoặc khoảng YYYYMMDD-YYYYMMDD), StudyInstanceUID, AccessionNumber, ModalitiesInStudy, StudyDescription..., theo từ khóa hoặc tag 8 chữ số hex; nhiều giá trị ngăn bằng dấu \ hoặc dấu phẩy là OR. Tham số không hỗ trợ bị bỏ qua và báo bằng `Warning: 299`, giá trị sai (ngày hỏng, ký tự đại diện ở giữa) là 400. Khi còn kết quả có header `Warning: 299`. Không có kết quả thì 200 với []. Cần quyền đọc.
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

// qidoAllSeries godoc
//
//	@Summary		QIDO-RS: tìm series (cả project)
//	@Description	Như tìm series của một study nhưng không giới hạn study; lọc được theo thuộc tính cấp study và series (Modality, SeriesInstanceUID, SeriesNumber...). Cần quyền đọc.
//	@Tags			dicomweb
//	@Security		BearerAuth
//	@Produce		application/dicom+json,json
//	@Param			limit	query		string	false	"Cỡ trang (mặc định 100, tối đa 1000)"
//	@Param			offset	query		string	false	"Số kết quả bỏ qua"
//	@Param			attributes	query		string	false	"Thuộc tính cần lọc, theo từ khóa hoặc tag hex (ví dụ Modality=CT, PatientID=P1, 00080060=CT)"
//	@Success		200	{object}	QueryResponse	"Mảng DICOM JSON"
//	@Failure		400	{object}	ErrorBody	"Tham số không hợp lệ"
//	@Failure		401	{object}	ErrorBody	"Thiếu hoặc sai Bearer token"
//	@Failure		403	{object}	ErrorBody	"Token không đủ quyền hoặc chọn project khác"
//	@Failure		404	{object}	ErrorBody	"Không có"
//	@Failure		500	{object}	ErrorBody	"Lỗi xử lý (không lộ chi tiết nội bộ)"
//	@Router			/dicomweb/series [get]
func qidoAllSeries() {}

// qidoAllInstances godoc
//
//	@Summary		QIDO-RS: tìm instance (cả project)
//	@Description	Trả metadata đã lưu của các instance khớp (kèm thuộc tính study, series và instance); lọc theo thuộc tính cấp study, series và instance (SOPInstanceUID, SOPClassUID, InstanceNumber). Cần quyền đọc.
//	@Tags			dicomweb
//	@Security		BearerAuth
//	@Produce		application/dicom+json,json
//	@Param			limit	query		string	false	"Cỡ trang (mặc định 100, tối đa 1000)"
//	@Param			offset	query		string	false	"Số kết quả bỏ qua"
//	@Param			attributes	query		string	false	"Thuộc tính cần lọc, theo từ khóa hoặc tag hex (ví dụ Modality=CT, PatientID=P1, 00080060=CT)"
//	@Success		200	{object}	QueryResponse	"Mảng DICOM JSON"
//	@Failure		400	{object}	ErrorBody	"Tham số không hợp lệ"
//	@Failure		401	{object}	ErrorBody	"Thiếu hoặc sai Bearer token"
//	@Failure		403	{object}	ErrorBody	"Token không đủ quyền hoặc chọn project khác"
//	@Failure		404	{object}	ErrorBody	"Không có"
//	@Failure		500	{object}	ErrorBody	"Lỗi xử lý (không lộ chi tiết nội bộ)"
//	@Router			/dicomweb/instances [get]
func qidoAllInstances() {}

// qidoStudyInstances godoc
//
//	@Summary		QIDO-RS: tìm instance của study
//	@Description	Như tìm instance, giới hạn trong một study. Cần quyền đọc.
//	@Tags			dicomweb
//	@Security		BearerAuth
//	@Produce		application/dicom+json,json
//	@Param			studyUid	path		string	true	"UID của study (StudyInstanceUID)"
//	@Param			limit	query		string	false	"Cỡ trang (mặc định 100, tối đa 1000)"
//	@Param			offset	query		string	false	"Số kết quả bỏ qua"
//	@Param			attributes	query		string	false	"Thuộc tính cần lọc, theo từ khóa hoặc tag hex (ví dụ Modality=CT, PatientID=P1, 00080060=CT)"
//	@Success		200	{object}	QueryResponse	"Mảng DICOM JSON"
//	@Failure		400	{object}	ErrorBody	"Tham số không hợp lệ"
//	@Failure		401	{object}	ErrorBody	"Thiếu hoặc sai Bearer token"
//	@Failure		403	{object}	ErrorBody	"Token không đủ quyền hoặc chọn project khác"
//	@Failure		404	{object}	ErrorBody	"Không có"
//	@Failure		500	{object}	ErrorBody	"Lỗi xử lý (không lộ chi tiết nội bộ)"
//	@Router			/dicomweb/studies/{studyUid}/instances [get]
func qidoStudyInstances() {}

// qidoSeriesInstances godoc
//
//	@Summary		QIDO-RS: tìm instance của series
//	@Description	Như tìm instance, giới hạn trong một series. Cần quyền đọc.
//	@Tags			dicomweb
//	@Security		BearerAuth
//	@Produce		application/dicom+json,json
//	@Param			studyUid	path		string	true	"UID của study (StudyInstanceUID)"
//	@Param			seriesUid	path		string	true	"UID của series (SeriesInstanceUID)"
//	@Param			limit	query		string	false	"Cỡ trang (mặc định 100, tối đa 1000)"
//	@Param			offset	query		string	false	"Số kết quả bỏ qua"
//	@Param			attributes	query		string	false	"Thuộc tính cần lọc, theo từ khóa hoặc tag hex (ví dụ Modality=CT, PatientID=P1, 00080060=CT)"
//	@Success		200	{object}	QueryResponse	"Mảng DICOM JSON"
//	@Failure		400	{object}	ErrorBody	"Tham số không hợp lệ"
//	@Failure		401	{object}	ErrorBody	"Thiếu hoặc sai Bearer token"
//	@Failure		403	{object}	ErrorBody	"Token không đủ quyền hoặc chọn project khác"
//	@Failure		404	{object}	ErrorBody	"Không có"
//	@Failure		500	{object}	ErrorBody	"Lỗi xử lý (không lộ chi tiết nội bộ)"
//	@Router			/dicomweb/studies/{studyUid}/series/{seriesUid}/instances [get]
func qidoSeriesInstances() {}

// wadoStudyMetadata godoc
//
//	@Summary		WADO-RS: metadata của study
//	@Description	Mảng metadata DICOM JSON của mọi instance trong study, theo luồng. Study không có thì 404 trước khi ghi byte nào; lỗi giữa chừng thì ngắt kết nối. Cần quyền đọc.
//	@Tags			dicomweb
//	@Security		BearerAuth
//	@Produce		application/dicom+json,json
//	@Param			studyUid	path		string	true	"UID của study (StudyInstanceUID)"
//	@Success		200	{object}	QueryResponse	"Mảng DICOM JSON"
//	@Failure		400	{object}	ErrorBody	"Tham số không hợp lệ"
//	@Failure		401	{object}	ErrorBody	"Thiếu hoặc sai Bearer token"
//	@Failure		403	{object}	ErrorBody	"Token không đủ quyền hoặc chọn project khác"
//	@Failure		404	{object}	ErrorBody	"Không có"
//	@Failure		500	{object}	ErrorBody	"Lỗi xử lý (không lộ chi tiết nội bộ)"
//	@Router			/dicomweb/studies/{studyUid}/metadata [get]
func wadoStudyMetadata() {}

// wadoInstanceMetadata godoc
//
//	@Summary		WADO-RS: metadata của instance
//	@Description	Mảng một phần tử: metadata DICOM JSON của instance. Cần quyền đọc.
//	@Tags			dicomweb
//	@Security		BearerAuth
//	@Produce		application/dicom+json,json
//	@Param			studyUid	path		string	true	"UID của study (StudyInstanceUID)"
//	@Param			seriesUid	path		string	true	"UID của series (SeriesInstanceUID)"
//	@Param			instanceUid	path		string	true	"UID của instance (SOPInstanceUID)"
//	@Success		200	{object}	QueryResponse	"Mảng DICOM JSON"
//	@Failure		400	{object}	ErrorBody	"Tham số không hợp lệ"
//	@Failure		401	{object}	ErrorBody	"Thiếu hoặc sai Bearer token"
//	@Failure		403	{object}	ErrorBody	"Token không đủ quyền hoặc chọn project khác"
//	@Failure		404	{object}	ErrorBody	"Không có"
//	@Failure		500	{object}	ErrorBody	"Lỗi xử lý (không lộ chi tiết nội bộ)"
//	@Router			/dicomweb/studies/{studyUid}/series/{seriesUid}/instances/{instanceUid}/metadata [get]
func wadoInstanceMetadata() {}

// wadoStudy godoc
//
//	@Summary		WADO-RS: tải study
//	@Description	Trả mọi instance của study dạng multipart/related; type="application/dicom", mỗi phần là tệp DICOM Part 10 nguyên văn như lúc STOW, theo luồng (chunked, bộ nhớ hằng số). Không có thì 404 trước khi ghi byte nào; lỗi giữa chừng thì ngắt kết nối. Cần quyền đọc, gồm Binary.
//	@Tags			dicomweb
//	@Security		BearerAuth
//	@Produce		multipart/related,json
//	@Param			studyUid	path		string	true	"UID của study (StudyInstanceUID)"
//	@Success		200	{file}	file	"multipart/related: mỗi phần là một tệp DICOM"
//	@Failure		400	{object}	ErrorBody	"Tham số không hợp lệ"
//	@Failure		401	{object}	ErrorBody	"Thiếu hoặc sai Bearer token"
//	@Failure		403	{object}	ErrorBody	"Token không đủ quyền hoặc chọn project khác"
//	@Failure		404	{object}	ErrorBody	"Không có"
//	@Failure		500	{object}	ErrorBody	"Lỗi xử lý (không lộ chi tiết nội bộ)"
//	@Router			/dicomweb/studies/{studyUid} [get]
func wadoStudy() {}

// wadoSeries godoc
//
//	@Summary		WADO-RS: tải series
//	@Description	Như tải study, cho các instance của một series.
//	@Tags			dicomweb
//	@Security		BearerAuth
//	@Produce		multipart/related,json
//	@Param			studyUid	path		string	true	"UID của study (StudyInstanceUID)"
//	@Param			seriesUid	path		string	true	"UID của series (SeriesInstanceUID)"
//	@Success		200	{file}	file	"multipart/related: mỗi phần là một tệp DICOM"
//	@Failure		400	{object}	ErrorBody	"Tham số không hợp lệ"
//	@Failure		401	{object}	ErrorBody	"Thiếu hoặc sai Bearer token"
//	@Failure		403	{object}	ErrorBody	"Token không đủ quyền hoặc chọn project khác"
//	@Failure		404	{object}	ErrorBody	"Không có"
//	@Failure		500	{object}	ErrorBody	"Lỗi xử lý (không lộ chi tiết nội bộ)"
//	@Router			/dicomweb/studies/{studyUid}/series/{seriesUid} [get]
func wadoSeries() {}

// wadoInstance godoc
//
//	@Summary		WADO-RS: tải instance
//	@Description	Như tải study, cho một instance (một phần duy nhất).
//	@Tags			dicomweb
//	@Security		BearerAuth
//	@Produce		multipart/related,json
//	@Param			studyUid	path		string	true	"UID của study (StudyInstanceUID)"
//	@Param			seriesUid	path		string	true	"UID của series (SeriesInstanceUID)"
//	@Param			instanceUid	path		string	true	"UID của instance (SOPInstanceUID)"
//	@Success		200	{file}	file	"multipart/related: một tệp DICOM"
//	@Failure		400	{object}	ErrorBody	"Tham số không hợp lệ"
//	@Failure		401	{object}	ErrorBody	"Thiếu hoặc sai Bearer token"
//	@Failure		403	{object}	ErrorBody	"Token không đủ quyền hoặc chọn project khác"
//	@Failure		404	{object}	ErrorBody	"Không có"
//	@Failure		500	{object}	ErrorBody	"Lỗi xử lý (không lộ chi tiết nội bộ)"
//	@Router			/dicomweb/studies/{studyUid}/series/{seriesUid}/instances/{instanceUid} [get]
func wadoInstance() {}

// wadoInstanceRendered godoc
//
//	@Summary		WADO-RS: ảnh dựng của instance
//	@Description	Trả frame đầu tiên của instance dưới dạng ảnh image/jpeg (mặc định) hoặc image/png theo header Accept. Dựng được: JPEG baseline (trả nguyên khi cần JPEG, giải mã và mã hóa PNG khi cần PNG), dữ liệu không nén xám 8 hoặc 16 bit (window/level từ metadata, hoặc từ tham số `window`; MONOCHROME1 được đảo) và RGB 8 bit. Mọi định dạng khác (JPEG 2000, JPEG-LS, RLE, YBR, bảng màu...) trả 406. Bộ nhớ một frame tối đa 64 MiB (vượt thì 413). Tham số viewport, annotation, iccprofile bị bỏ qua. Cần quyền đọc, gồm Binary.
//	@Tags			dicomweb
//	@Security		BearerAuth
//	@Produce		image/jpeg,image/png,json
//	@Param			studyUid	path		string	true	"UID của study (StudyInstanceUID)"
//	@Param			seriesUid	path		string	true	"UID của series (SeriesInstanceUID)"
//	@Param			instanceUid	path		string	true	"UID của instance (SOPInstanceUID)"
//	@Param			window	query		string	false	"center,width[,linear]: cửa sổ ghi đè (width > 0)"
//	@Param			quality	query		int		false	"Chất lượng JPEG 1..100 (mặc định 90)"
//	@Success		200	{file}	file	"Ảnh JPEG hoặc PNG"
//	@Failure		400	{object}	ErrorBody	"Tham số không hợp lệ"
//	@Failure		401	{object}	ErrorBody	"Thiếu hoặc sai Bearer token"
//	@Failure		403	{object}	ErrorBody	"Token không đủ quyền hoặc chọn project khác"
//	@Failure		404	{object}	ErrorBody	"Không có instance hoặc ảnh"
//	@Failure		406	{object}	ErrorBody	"Accept không có định dạng hỗ trợ, hoặc dữ liệu điểm ảnh chưa dựng được"
//	@Failure		413	{object}	ErrorBody	"Frame quá lớn để dựng"
//	@Failure		500	{object}	ErrorBody	"Lỗi xử lý (không lộ chi tiết nội bộ)"
//	@Router			/dicomweb/studies/{studyUid}/series/{seriesUid}/instances/{instanceUid}/rendered [get]
func wadoInstanceRendered() {}

// wadoFrameRendered godoc
//
//	@Summary		WADO-RS: ảnh dựng của một frame
//	@Description	Như ảnh dựng của instance nhưng cho frame được chọn (một số, từ 1). Frame vượt tổng số frame là 416.
//	@Tags			dicomweb
//	@Security		BearerAuth
//	@Produce		image/jpeg,image/png,json
//	@Param			studyUid	path		string	true	"UID của study (StudyInstanceUID)"
//	@Param			seriesUid	path		string	true	"UID của series (SeriesInstanceUID)"
//	@Param			instanceUid	path		string	true	"UID của instance (SOPInstanceUID)"
//	@Param			frame	path		int		true	"Số thứ tự frame bắt đầu từ 1"
//	@Param			window	query		string	false	"center,width[,linear]: cửa sổ ghi đè (width > 0)"
//	@Param			quality	query		int		false	"Chất lượng JPEG 1..100 (mặc định 90)"
//	@Success		200	{file}	file	"Ảnh JPEG hoặc PNG"
//	@Failure		400	{object}	ErrorBody	"Tham số không hợp lệ"
//	@Failure		401	{object}	ErrorBody	"Thiếu hoặc sai Bearer token"
//	@Failure		403	{object}	ErrorBody	"Token không đủ quyền hoặc chọn project khác"
//	@Failure		404	{object}	ErrorBody	"Không có instance hoặc ảnh"
//	@Failure		406	{object}	ErrorBody	"Accept không có định dạng hỗ trợ, hoặc dữ liệu điểm ảnh chưa dựng được"
//	@Failure		413	{object}	ErrorBody	"Frame quá lớn để dựng"
//	@Failure		416	{object}	ErrorBody	"Số frame vượt tổng số frame của instance"
//	@Failure		500	{object}	ErrorBody	"Lỗi xử lý (không lộ chi tiết nội bộ)"
//	@Router			/dicomweb/studies/{studyUid}/series/{seriesUid}/instances/{instanceUid}/frames/{frame}/rendered [get]
func wadoFrameRendered() {}
