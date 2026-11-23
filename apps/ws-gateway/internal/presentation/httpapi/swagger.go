package httpapi

// Tệp này chỉ chứa annotation swaggo để sinh OpenAPI (xem `make swagger APP=ws-gateway`); các hàm rỗng không được gắn vào router. Sửa annotation xong
// phải chạy lại `make swagger APP=ws-gateway` để cập nhật apps/ws-gateway/docs. OpenAPI 2.0 chỉ mô tả được phần HTTP: với hai kênh WebSocket nó chỉ ghi
// route nâng cấp (101) và lỗi trước khi nâng cấp; thông điệp trong kênh và mã đóng (1008, 1013...) nằm ở apps/ws-gateway/docs/api-contract.md.

// @title						Trivita WS Gateway API
// @version					1.0
// @description				Cổng WebSocket của Subscription R4 và Hub FHIRcast (tùy chọn). Spec này gồm probe, Hub FHIRcast (HTTP) và hai route nâng cấp WebSocket; giao thức thông điệp WebSocket nằm ở docs/api-contract.md. Lỗi của Hub trả OperationOutcome (application/fhir+json); lỗi ngoài Hub trả {"error": "..."}.
// @BasePath					/
// @tag.name					fhircast
// @tag.description			Hub FHIRcast đồng bộ ngữ cảnh giữa ứng dụng lâm sàng; có khi FHIRCAST_ENABLED=true. Ba tiền tố: /fhircast/STU2, /fhircast/STU3 và /hub (bí danh của STU3). Bearer JWT do auth cấp, cần claim project và scope fhircast/*.
// @tag.name					websocket
// @tag.description			Route nâng cấp lên WebSocket: phản hồi thành công là 101 Switching Protocols, sau đó là khung dữ liệu (xem docs/api-contract.md). Lỗi trước nâng cấp là HTTP thường.
// @tag.name					health
// @tag.description			Probe liveness và readiness cho orchestrator.
// @securityDefinitions.apikey	BearerAuth
// @in							header
// @name						Authorization
// @description				Dạng "Bearer <access_token>": JWT do auth cấp (kiểm bằng JWKS), có claim project và scope fhircast/*.

// OperationOutcome là lỗi của Hub FHIRcast theo định dạng của Medplum: thông điệp ở issue[0].details.text.
type OperationOutcome struct {
	ResourceType string                  `json:"resourceType" binding:"required" example:"OperationOutcome"` // luôn "OperationOutcome"
	Issue        []OperationOutcomeIssue `json:"issue" binding:"required"`                                   // danh sách vấn đề; Hub luôn trả đúng một phần tử
}

// OperationOutcomeIssue là một vấn đề trong OperationOutcome.
type OperationOutcomeIssue struct {
	Severity string                  `json:"severity" binding:"required" example:"error"`                                                             // mức độ; Hub luôn trả "error"
	Code     string                  `json:"code" binding:"required" example:"invalid" enums:"invalid,login,forbidden,conflict,too-costly,exception"` // mã vấn đề: invalid (400), login (401), forbidden (403), conflict (409), too-costly (413), exception (500)
	Details  OperationOutcomeDetails `json:"details" binding:"required"`                                                                              // chi tiết dạng văn bản
}

// OperationOutcomeDetails là phần chi tiết của một vấn đề.
type OperationOutcomeDetails struct {
	Text string `json:"text" binding:"required" example:"Missing hub.mode"` // thông điệp dành cho người dùng; lỗi hạ tầng chỉ là "Internal server error", không lộ chi tiết nội bộ
}

// ErrorBody là lỗi JSON thường của các route ngoài Hub (hạn mức, route không tồn tại, lỗi chưa phân loại).
type ErrorBody struct {
	Error string `json:"error" binding:"required" example:"quá nhiều yêu cầu, thử lại sau"` // thông điệp lỗi; lỗi 5xx chỉ là mô tả trạng thái chuẩn
}

// FhircastWellKnown là cấu hình Hub công bố ở .well-known/fhircast-configuration.
type FhircastWellKnown struct {
	EventsSupported   []string `json:"eventsSupported" binding:"required" example:"Patient-open,Patient-close"` // 14 sự kiện Hub chấp nhận, đúng thứ tự của Medplum
	WebsocketSupport  bool     `json:"websocketSupport" binding:"required" example:"true"`                      // luôn true: kênh duy nhất của Hub
	WebhookSupport    bool     `json:"webhookSupport" binding:"required" example:"false"`                       // luôn false: Hub không bao giờ gọi ra ngoài
	FhircastVersion   string   `json:"fhircastVersion" binding:"required" example:"STU3" enums:"STU2,STU3"`     // phiên bản của tiền tố đang gọi
	GetCurrentSupport bool     `json:"getCurrentSupport,omitempty" example:"true"`                              // chỉ có ở STU3: Hub hỗ trợ GET ngữ cảnh hiện tại
}

// FhircastContextItem là một phần tử của event.context: một resource FHIR kèm khóa nghiệp vụ.
type FhircastContextItem struct {
	Key      string         `json:"key" binding:"required" example:"patient"` // vai trò của resource trong ngữ cảnh (patient, encounter, study, report...)
	Resource map[string]any `json:"resource" binding:"required"`              // resource FHIR dạng JSON (luôn có resourceType); với sự kiện update là Bundle
}

// FhircastEventPayload là phần event của một thông điệp đổi ngữ cảnh.
type FhircastEventPayload struct {
	HubTopic         string                `json:"hub.topic" binding:"required" example:"7f2c4e1a-9b3d-4c5e-8a1f-2d6b9e0c3a47"`                                                                                                                                                                                                                          // topic của phiên làm việc; không rỗng, tối đa 256 byte, không ký tự điều khiển
	HubEvent         string                `json:"hub.event" binding:"required" example:"Patient-open" enums:"Patient-open,Patient-close,ImagingStudy-open,ImagingStudy-close,Encounter-open,Encounter-close,DiagnosticReport-open,DiagnosticReport-close,DiagnosticReport-select,DiagnosticReport-update,userlogout,userhibernate,syncerror,heartbeat"` // tên sự kiện
	Context          []FhircastContextItem `json:"context" binding:"required"`                                                                                                                                                                                                                                                                           // ngữ cảnh của sự kiện; mảng (có thể rỗng) các phần tử {key, resource}
	ContextVersionID string                `json:"context.versionId,omitempty" example:"c8fa1e88-c131-4848-8467-9a0b2d1f5e33"`                                                                                                                                                                                                                           // phiên bản ngữ cảnh: Hub cấp khi mở; sự kiện DiagnosticReport-update phải gửi đúng phiên bản hiện tại
}

// FhircastEventBody là thông điệp đổi ngữ cảnh (FHIRcast event).
type FhircastEventBody struct {
	ID        string               `json:"id" binding:"required" example:"m-0192f3a4"`                                     // id thông điệp do bên gửi đặt, không rỗng
	Timestamp string               `json:"timestamp" binding:"required" example:"2026-11-23T08:30:00Z" format:"date-time"` // thời điểm phát sinh (RFC 3339), không rỗng
	Event     FhircastEventPayload `json:"event" binding:"required"`                                                       // nội dung sự kiện
}

// FhircastHubRequest là body của POST {v}/: Hub phân biệt hai dạng theo nội dung. Có trường id hoặc event thì là thông điệp đổi ngữ cảnh (dùng id, timestamp,
// event như FhircastEventBody); không thì là yêu cầu đăng ký hoặc hủy (dùng các trường hub.*). Cũng nhận application/x-www-form-urlencoded cho dạng đăng ký.
type FhircastHubRequest struct {
	HubChannelType     string                `json:"hub.channel.type,omitempty" example:"websocket" enums:"websocket"`                                                   // dạng đăng ký: kênh, chỉ "websocket" (webhook, rest-hook, email bị từ chối)
	HubMode            string                `json:"hub.mode,omitempty" example:"subscribe" enums:"subscribe,unsubscribe"`                                               // subscribe: tạo endpoint mới; unsubscribe: hủy endpoint đã cấp
	HubTopic           string                `json:"hub.topic,omitempty" example:"7f2c4e1a-9b3d-4c5e-8a1f-2d6b9e0c3a47"`                                                 // dạng đăng ký: topic muốn theo dõi
	HubEvents          string                `json:"hub.events,omitempty" example:"Patient-open,Patient-close"`                                                          // dạng đăng ký, bắt buộc khi subscribe: tên sự kiện cách nhau bằng dấu phẩy, mỗi tên phải thuộc 14 sự kiện được công bố
	HubChannelEndpoint string                `json:"hub.channel.endpoint,omitempty" example:"ws://gw.example.org:8007/ws/fhircast/baa8974b-807b-490d-9e87-334f99a72b6d"` // dạng hủy: URL hoặc endpoint đã được cấp lúc đăng ký
	Endpoint           string                `json:"endpoint,omitempty" example:"baa8974b-807b-490d-9e87-334f99a72b6d"`                                                  // dạng hủy, tên trường cũ của client trước @medplum/core 5.1.29; chỉ dùng khi thiếu hub.channel.endpoint
	ID                 string                `json:"id,omitempty" example:"m-0192f3a4"`                                                                                  // dạng đổi ngữ cảnh: id thông điệp
	Timestamp          string                `json:"timestamp,omitempty" example:"2026-11-23T08:30:00Z" format:"date-time"`                                              // dạng đổi ngữ cảnh: thời điểm phát sinh
	Event              *FhircastEventPayload `json:"event,omitempty"`                                                                                                    // dạng đổi ngữ cảnh: nội dung sự kiện
}

// FhircastHubResponse là body 202 của POST {v}/: dạng đăng ký hoặc hủy trả hub.channel.endpoint; dạng đổi ngữ cảnh trả success và event.
type FhircastHubResponse struct {
	HubChannelEndpoint string             `json:"hub.channel.endpoint,omitempty" example:"ws://gw.example.org:8007/ws/fhircast/baa8974b-807b-490d-9e87-334f99a72b6d"` // dạng đăng ký/hủy: URL WebSocket để nhận sự kiện (mỗi lần đăng ký sinh endpoint mới); với hủy là URL đã hủy
	Success            bool               `json:"success,omitempty" example:"true"`                                                                                   // dạng đổi ngữ cảnh: true khi sự kiện đã được chấp nhận và phát cho người đăng ký
	Event              *FhircastEventBody `json:"event,omitempty"`                                                                                                    // dạng đổi ngữ cảnh: thông điệp đã phát (đã bổ sung context.versionId)
}

// FhircastChangeResponse là body 202 của POST {v}/{topic}.
type FhircastChangeResponse struct {
	Success bool              `json:"success" binding:"required" example:"true"` // true khi sự kiện đã được chấp nhận và phát cho người đăng ký
	Event   FhircastEventBody `json:"event" binding:"required"`                  // thông điệp đã phát (đã bổ sung context.versionId)
}

// FhircastContextSTU3 là ngữ cảnh hiện tại ở STU3.
type FhircastContextSTU3 struct {
	ContextType      string                `json:"context.type" binding:"required" example:"Patient"`                          // loại resource của ngữ cảnh đang mở; rỗng khi chưa có ngữ cảnh
	Context          []FhircastContextItem `json:"context" binding:"required"`                                                 // các phần tử của ngữ cảnh; mảng rỗng khi chưa có
	ContextVersionID string                `json:"context.versionId,omitempty" example:"c8fa1e88-c131-4848-8467-9a0b2d1f5e33"` // phiên bản ngữ cảnh; vắng khi chưa có ngữ cảnh
}

// healthLive godoc
//
//	@Summary		Liveness probe
//	@Description	Tiến trình còn sống. Không kiểm Redis nên orchestrator không khởi động lại tiến trình vì lỗi Redis. /healthz dùng cho HEALTHCHECK của Docker; /health/startup trả như /health/live.
//	@Tags			health
//	@Produce		json
//	@Success		200	{object}	HealthResponse
//	@Router			/health/live [get]
//	@Router			/health/startup [get]
//	@Router			/healthz [get]
func healthLive() {}

// healthReady godoc
//
//	@Summary		Readiness probe
//	@Description	200 khi nhận được lưu lượng; 503 khi đang tắt (drain) hoặc Redis chưa kết nối được (Redis chưa lên lúc khởi động không làm tiến trình thoát, chỉ giữ readiness 503). Body cho biết từng phụ thuộc.
//	@Tags			health
//	@Produce		json
//	@Success		200	{object}	ReadyResponse
//	@Failure		503	{object}	ReadyResponse
//	@Router			/health/ready [get]
func healthReady() {}

// subscriptionsWebSocket godoc
//
//	@Summary		WebSocket Subscription R4
//	@Description	Nâng cấp lên WebSocket (Subscriptions R5 Backport, như Medplum). Không dùng header xác thực: client gửi {"type":"bind-with-token","payload":{"token":"..."}} trong khung đầu tiên với token do fhir-server cấp ($get-ws-binding-token), trong 10 giây. Thông điệp, mã đóng và đảm bảo giao hàng: docs/api-contract.md mục 3. Chịu hạn mức mở kết nối theo IP (FHIR_WS_RATE_LIMIT, FHIR_WS_MAX_CONNS_PER_IP) và giới hạn FHIR_WS_MAX_CONNECTIONS.
//	@Tags			websocket
//	@Produce		plain
//	@Success		101	"Switching Protocols: kênh đã mở"
//	@Failure		403	{string}	string	"Origin không nằm trong FHIR_WS_ALLOWED_ORIGINS"
//	@Failure		429	{string}	string	"Quá FHIR_WS_MAX_CONNS_PER_IP (text, Retry-After: 5) hoặc quá FHIR_WS_RATE_LIMIT (JSON {error}, Retry-After: 1)"
//	@Failure		503	{string}	string	"Đang tắt hoặc hết FHIR_WS_MAX_CONNECTIONS"
//	@Router			/ws/subscriptions-r4 [get]
func subscriptionsWebSocket() {}

// fhircastWebSocket godoc
//
//	@Summary		WebSocket FHIRcast
//	@Description	Chỉ có khi FHIRCAST_ENABLED=true. Nâng cấp lên WebSocket để nhận sự kiện đã đăng ký. endpoint là bí mật 122 bit do POST {v}/ cấp (không cần Bearer khi nối); đường dẫn có nhiều đoạn sau /ws/fhircast/ cũng vào cùng handler và endpoint sai bị đóng với "invalid endpoint". Khung đầu tiên luôn là xác nhận, sau đó là sự kiện đã đăng ký, heartbeat cục bộ mỗi FHIRCAST_HEARTBEAT. Tối đa FHIRCAST_MAX_SOCKETS_PER_ENDPOINT socket mỗi endpoint mỗi instance.
//	@Tags			websocket
//	@Produce		plain
//	@Param			endpoint	path	string	true	"Endpoint bí mật do đăng ký cấp (phần cuối của hub.channel.endpoint)"
//	@Success		101	"Switching Protocols: kênh đã mở"
//	@Failure		403	{string}	string	"Origin không nằm trong FHIR_WS_ALLOWED_ORIGINS"
//	@Failure		429	{string}	string	"Quá hạn mức mở kết nối (text hoặc JSON {error}, như WebSocket Subscription)"
//	@Failure		503	{string}	string	"Đang tắt hoặc hết hạn mức kết nối"
//	@Router			/ws/fhircast/{endpoint} [get]
func fhircastWebSocket() {}

// fhircastWellKnown godoc
//
//	@Summary		Cấu hình Hub FHIRcast
//	@Description	Công khai, không cần token. Hub chỉ hỗ trợ kênh websocket (webhookSupport luôn false). STU3 thêm getCurrentSupport.
//	@Tags			fhircast
//	@Produce		json
//	@Success		200	{object}	FhircastWellKnown
//	@Router			/fhircast/STU2/.well-known/fhircast-configuration [get]
//	@Router			/fhircast/STU3/.well-known/fhircast-configuration [get]
//	@Router			/hub/.well-known/fhircast-configuration [get]
func fhircastWellKnown() {}

// fhircastHubPost godoc
//
//	@Summary		Đăng ký, hủy đăng ký hoặc đổi ngữ cảnh
//	@Description	Một route, hai dạng theo nội dung body. (1) Có trường id hoặc event: đổi ngữ cảnh như POST {v}/{topic} (không kiểm khớp topic đường dẫn); phản hồi 202 có success và event. (2) Không có: đăng ký (hub.mode=subscribe) hoặc hủy (hub.mode=unsubscribe) kênh websocket; phản hồi 202 có hub.channel.endpoint. Đăng ký cần hub.channel.type=websocket, hub.mode, hub.topic, hub.events (14 sự kiện được công bố); hủy chỉ cho đúng người, project và topic đã đăng ký, sai bất kỳ điều nào cũng là 400 "Invalid endpoint". Nhận application/json hoặc application/x-www-form-urlencoded (dạng đăng ký). Body vượt FHIRCAST_MAX_BODY_BYTES là 413.
//	@Tags			fhircast
//	@Security		BearerAuth
//	@Accept			json,x-www-form-urlencoded
//	@Produce		json,application/fhir+json
//	@Param			body	body		FhircastHubRequest	true	"Yêu cầu đăng ký/hủy hoặc thông điệp đổi ngữ cảnh"
//	@Success		202		{object}	FhircastHubResponse
//	@Failure		400		{object}	OperationOutcome	"Thiếu hoặc sai trường (ví dụ Missing hub.mode, Invalid hub.channel.type, Invalid hub.events, Invalid endpoint, Missing event ID)"
//	@Failure		401		{object}	OperationOutcome	"Thiếu hoặc sai Bearer token (kèm WWW-Authenticate: Bearer realm=\"fhircast\")"
//	@Failure		403		{object}	OperationOutcome	"Token thiếu project hoặc scope fhircast/*"
//	@Failure		413		{object}	OperationOutcome	"Body vượt FHIRCAST_MAX_BODY_BYTES"
//	@Failure		500		{object}	OperationOutcome	"Lỗi hạ tầng (Redis); thông điệp chung, không lộ chi tiết"
//	@Router			/fhircast/STU2 [post]
//	@Router			/fhircast/STU3 [post]
//	@Router			/hub [post]
func fhircastHubPost() {}

// fhircastTopicPost godoc
//
//	@Summary		Đổi ngữ cảnh của một topic
//	@Description	Gửi thông điệp đổi ngữ cảnh (sự kiện open, close, select hoặc update) cho topic ở đường dẫn. event["hub.topic"] phải trùng topic đường dẫn, nếu không 400 "Topic mismatch" (đặt FHIRCAST_ALLOW_TOPIC_MISMATCH=true để như Medplum). Sự kiện update phải gửi đúng context.versionId hiện tại; ngữ cảnh đổi trong lúc cập nhật là 409. Ngữ cảnh vượt FHIRCAST_MAX_CONTEXT_BYTES hoặc quá nhiều ngữ cảnh đã lưu là 400. Hub phát sự kiện cho mọi người đăng ký của topic trong cùng project.
//	@Tags			fhircast
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json,application/fhir+json
//	@Param			topic	path		string				true	"Topic (đã mã hóa URL, chứa được dấu / hoặc :)"
//	@Param			body	body		FhircastEventBody	true	"Thông điệp đổi ngữ cảnh"
//	@Success		202		{object}	FhircastChangeResponse
//	@Failure		400		{object}	OperationOutcome	"Sự kiện sai (Missing event ID, Invalid event[\"hub.event\"], Topic mismatch, No DiagnosticReport currently open for this topic...)"
//	@Failure		401		{object}	OperationOutcome	"Thiếu hoặc sai Bearer token"
//	@Failure		403		{object}	OperationOutcome	"Token thiếu project hoặc scope fhircast/*"
//	@Failure		409		{object}	OperationOutcome	"Ngữ cảnh đổi trong lúc cập nhật, không còn ở phiên bản đã gửi"
//	@Failure		413		{object}	OperationOutcome	"Body vượt FHIRCAST_MAX_BODY_BYTES"
//	@Failure		500		{object}	OperationOutcome	"Lỗi hạ tầng (Redis)"
//	@Router			/fhircast/STU2/{topic} [post]
//	@Router			/fhircast/STU3/{topic} [post]
//	@Router			/hub/{topic} [post]
func fhircastTopicPost() {}

// fhircastContextSTU3 godoc
//
//	@Summary		Ngữ cảnh hiện tại (STU3)
//	@Description	Ngữ cảnh đang mở của topic trong project của token. Project khác không thấy. Chưa có ngữ cảnh: context.type rỗng và context là mảng rỗng.
//	@Tags			fhircast
//	@Security		BearerAuth
//	@Produce		json,application/fhir+json
//	@Param			topic	path		string	true	"Topic (đã mã hóa URL)"
//	@Success		200		{object}	FhircastContextSTU3
//	@Failure		401		{object}	OperationOutcome	"Thiếu hoặc sai Bearer token"
//	@Failure		403		{object}	OperationOutcome	"Token thiếu project hoặc scope fhircast/*"
//	@Failure		500		{object}	OperationOutcome	"Lỗi hạ tầng (Redis)"
//	@Router			/fhircast/STU3/{topic} [get]
//	@Router			/hub/{topic} [get]
func fhircastContextSTU3() {}

// fhircastContextSTU2 godoc
//
//	@Summary		Ngữ cảnh hiện tại (STU2)
//	@Description	Mảng các phần tử context của topic trong project của token; chưa có ngữ cảnh thì là mảng rỗng (mở rộng không chuẩn cho Nuance PowerCast Hub). Project khác không thấy.
//	@Tags			fhircast
//	@Security		BearerAuth
//	@Produce		json,application/fhir+json
//	@Param			topic	path		string	true	"Topic (đã mã hóa URL)"
//	@Success		200		{array}		FhircastContextItem
//	@Failure		401		{object}	OperationOutcome	"Thiếu hoặc sai Bearer token"
//	@Failure		403		{object}	OperationOutcome	"Token thiếu project hoặc scope fhircast/*"
//	@Failure		500		{object}	OperationOutcome	"Lỗi hạ tầng (Redis)"
//	@Router			/fhircast/STU2/{topic} [get]
func fhircastContextSTU2() {}
