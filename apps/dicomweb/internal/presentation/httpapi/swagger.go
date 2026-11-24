package httpapi

// Tệp này chỉ chứa annotation swaggo để sinh OpenAPI (xem `make swagger APP=dicomweb`); các hàm rỗng không được gắn vào router. Sửa annotation xong
// phải chạy lại `make swagger APP=dicomweb` để cập nhật apps/dicomweb/docs. Ở D1 các route DICOMweb mới là khung (501); D3 và D4 bổ sung DTO và phản hồi thật.

// @title						Trivita DICOMweb API
// @version					1.0
// @description				DICOMweb (STOW-RS, QIDO-RS, WADO-RS) cho kho hình ảnh DICOM, dùng chung schema và kho Binary với fhir-server. Lỗi trả {"error": "..."}. Hiện chỉ có khung: mọi route DICOMweb trả 501 cho tới khi STOW-RS (D3) và QIDO-RS, WADO-RS (D4) được cài.
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
//	@Summary		STOW-RS: lưu instance (chưa cài)
//	@Description	Sẽ nhận multipart/related (application/dicom) và lưu từng instance (D3). Cần quyền ghi. Mọi route DICOMweb khác không liệt kê ở đây (instances, rendered, bulkdata, xóa...) cũng trả 501 sau xác thực.
//	@Tags			dicomweb
//	@Security		BearerAuth
//	@Produce		json
//	@Success		200	{object}	ErrorBody	"Chưa dùng: route này hiện luôn trả 501"
//	@Failure		401	{object}	ErrorBody	"Thiếu hoặc sai Bearer token"
//	@Failure		403	{object}	ErrorBody	"Token không đủ quyền (cần quyền ghi) hoặc chọn project khác"
//	@Failure		413	{object}	ErrorBody	"Body vượt DICOMWEB_MAX_REQUEST_BYTES"
//	@Failure		501	{object}	ErrorBody	"Chưa cài đặt"
//	@Router			/dicomweb/studies [post]
func stowStudies() {}

// stowStudy godoc
//
//	@Summary		STOW-RS: lưu instance vào một study (chưa cài)
//	@Description	Như STOW-RS ở /studies nhưng ràng buộc mọi instance vào study chỉ định (D3). Cần quyền ghi.
//	@Tags			dicomweb
//	@Security		BearerAuth
//	@Produce		json
//	@Param			studyUid	path		string	true	"UID của study (DICOM StudyInstanceUID)"
//	@Success		200	{object}	ErrorBody	"Chưa dùng: route này hiện luôn trả 501"
//	@Failure		401	{object}	ErrorBody	"Thiếu hoặc sai Bearer token"
//	@Failure		403	{object}	ErrorBody	"Token không đủ quyền (cần quyền ghi) hoặc chọn project khác"
//	@Failure		413	{object}	ErrorBody	"Body vượt DICOMWEB_MAX_REQUEST_BYTES"
//	@Failure		501	{object}	ErrorBody	"Chưa cài đặt"
//	@Router			/dicomweb/studies/{studyUid} [post]
func stowStudy() {}

// qidoStudies godoc
//
//	@Summary		QIDO-RS: tìm study (chưa cài)
//	@Description	Sẽ trả danh sách study dạng DICOM JSON (D4). Cần quyền đọc.
//	@Tags			dicomweb
//	@Security		BearerAuth
//	@Produce		json
//	@Success		200	{object}	ErrorBody	"Chưa dùng: route này hiện luôn trả 501"
//	@Failure		401	{object}	ErrorBody	"Thiếu hoặc sai Bearer token"
//	@Failure		403	{object}	ErrorBody	"Token không đủ quyền (cần quyền đọc) hoặc chọn project khác"
//	@Failure		501	{object}	ErrorBody	"Chưa cài đặt"
//	@Router			/dicomweb/studies [get]
func qidoStudies() {}

// qidoSeries godoc
//
//	@Summary		QIDO-RS: tìm series của study (chưa cài)
//	@Description	Sẽ trả danh sách series của một study dạng DICOM JSON (D4). Cần quyền đọc.
//	@Tags			dicomweb
//	@Security		BearerAuth
//	@Produce		json
//	@Param			studyUid	path		string	true	"UID của study (DICOM StudyInstanceUID)"
//	@Success		200	{object}	ErrorBody	"Chưa dùng: route này hiện luôn trả 501"
//	@Failure		401	{object}	ErrorBody	"Thiếu hoặc sai Bearer token"
//	@Failure		403	{object}	ErrorBody	"Token không đủ quyền (cần quyền đọc) hoặc chọn project khác"
//	@Failure		501	{object}	ErrorBody	"Chưa cài đặt"
//	@Router			/dicomweb/studies/{studyUid}/series [get]
func qidoSeries() {}

// wadoSeriesMetadata godoc
//
//	@Summary		WADO-RS: metadata của series (chưa cài)
//	@Description	Sẽ trả metadata mọi instance của series dạng DICOM JSON (D4). Cần quyền đọc.
//	@Tags			dicomweb
//	@Security		BearerAuth
//	@Produce		json
//	@Param			studyUid	path		string	true	"UID của study (DICOM StudyInstanceUID)"
//	@Param			seriesUid	path		string	true	"UID của series (SeriesInstanceUID)"
//	@Success		200	{object}	ErrorBody	"Chưa dùng: route này hiện luôn trả 501"
//	@Failure		401	{object}	ErrorBody	"Thiếu hoặc sai Bearer token"
//	@Failure		403	{object}	ErrorBody	"Token không đủ quyền (cần quyền đọc) hoặc chọn project khác"
//	@Failure		501	{object}	ErrorBody	"Chưa cài đặt"
//	@Router			/dicomweb/studies/{studyUid}/series/{seriesUid}/metadata [get]
func wadoSeriesMetadata() {}

// wadoFrame godoc
//
//	@Summary		WADO-RS: lấy frame (chưa cài)
//	@Description	Sẽ trả một frame dạng multipart/related, truyền luồng từ kho (D4). Cần quyền đọc.
//	@Tags			dicomweb
//	@Security		BearerAuth
//	@Produce		json
//	@Param			studyUid	path		string	true	"UID của study (DICOM StudyInstanceUID)"
//	@Param			seriesUid	path		string	true	"UID của series (SeriesInstanceUID)"
//	@Param			instanceUid	path		string	true	"UID của instance (SOPInstanceUID)"
//	@Param			frame	path		string	true	"Số thứ tự frame, bắt đầu từ 1"
//	@Success		200	{object}	ErrorBody	"Chưa dùng: route này hiện luôn trả 501"
//	@Failure		401	{object}	ErrorBody	"Thiếu hoặc sai Bearer token"
//	@Failure		403	{object}	ErrorBody	"Token không đủ quyền (cần quyền đọc) hoặc chọn project khác"
//	@Failure		501	{object}	ErrorBody	"Chưa cài đặt"
//	@Router			/dicomweb/studies/{studyUid}/series/{seriesUid}/instances/{instanceUid}/frames/{frame} [get]
func wadoFrame() {}
