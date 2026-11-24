# 3. Danh mục chức năng (F-01..F-16)

F-01..F-08: Subscription WebSocket và phần chung của gateway. F-09..F-16: Hub FHIRcast.

## Phần A: Subscription WebSocket và phần chung

Mảng (A): websocket của Subscription R4 và phần chung của gateway. Chức năng của Hub FHIRcast (F-09 trở đi) do mảnh B viết; ở đây chỉ nhắc chéo khi dùng chung `pkg/wsgroup`, hạn mức mở kết nối và probe. Bảng route, DTO và mã đóng đầy đủ ở [../api-contract.md](../api-contract.md), không chép lại. Các hằng số không có biến môi trường (hạn bind 10 giây, 50 Subscription mỗi kết nối, hàng đợi gửi 256, timeout ghi 10 giây, khung nhận tối đa 64 KiB) là hằng của `pkg/fhirsub` và `pkg/wsgroup`, gateway không cho đổi.

<a id="f-01"></a>
### F-01. Nâng cấp WebSocket, kiểm Origin và giới hạn kết nối

| Mục | Nội dung |
|---|---|
| Mục đích | Nhận yêu cầu nâng cấp `GET /ws/subscriptions-r4`, từ chối sớm (trước khi tốn tài nguyên) khi quá tải, đang tắt hoặc `Origin` lạ; theo dõi kết nối đang mở. Dùng chung cho kênh FHIRcast (mảnh B). |
| Tác nhân | Client của Subscription (ứng dụng, trình duyệt); hạ tầng (proxy, load balancer). |
| Tiền điều kiện | Gateway đang chạy và chưa tắt. Không cần xác thực HTTP: danh tính nằm trong token bind (F-03). |
| Cách gọi | `GET /ws/subscriptions-r4` với header nâng cấp WebSocket (route ở swagger, nhóm `websocket`). |
| Cấu hình | `FHIR_WS_MAX_CONNECTIONS` (1000 mỗi instance), `FHIR_WS_MAX_CONNS_PER_IP` (0: không giới hạn), `FHIR_WS_TRUSTED_PROXIES`, `FHIR_WS_ALLOWED_ORIGINS` (rỗng: chỉ cùng host), `FHIR_WS_CLOSE_TIMEOUT`. |

**Luồng chính:**
1. Group (`pkg/wsgroup`) xác định IP người gọi (chỉ khi `FHIR_WS_MAX_CONNS_PER_IP` > 0): mặc định là địa chỉ kết nối; gateway chỉ đọc `X-Forwarded-For` khi `FHIR_WS_TRUSTED_PROXIES` có CIDR khớp proxy trực tiếp.
2. Dưới một khóa, Group kiểm theo thứ tự: đang đóng, đủ `MaxConns`, đủ `MaxConnsPerIP`. Qua cả ba thì **đặt chỗ** (tổng và theo IP) rồi mới gọi `websocket.Accept`; nhờ đặt trước nên nhiều upgrade đồng thời không vượt được `MaxConns`.
3. `Accept` kiểm `Origin`: không có header `Origin` (client không phải trình duyệt) thì qua; có thì phải khớp một mẫu host của `FHIR_WS_ALLOWED_ORIGINS` (mẫu `path.Match`), hoặc khi danh sách rỗng thì phải cùng host với gateway.
4. Kết nối được ghi vào danh sách, đặt giới hạn khung nhận 64 KiB, chạy handler (F-03, F-04). Khi handler trả về, kết nối bị cắt, gỡ khỏi danh sách và chỗ đặt được trả (kể cả khi `Accept` thất bại).

**Thay thế / lỗi:**

| Tình huống | Kết quả |
|---|---|
| Group đang đóng | HTTP 503, body văn bản `đang tắt` |
| Đủ `MaxConns` | HTTP 503, `quá nhiều kết nối`; không tạo goroutine, không gọi `Accept` |
| Đủ `MaxConnsPerIP` | HTTP 429, `quá nhiều kết nối từ địa chỉ này`, header `Retry-After: 5` |
| `Origin` không được phép | HTTP 403 (do thư viện websocket, văn bản thường) |
| Kết nối lọt qua đúng lúc đang đóng | Đóng 1001 `server shutting down` |
| Khung nhận lớn hơn 64 KiB | Thư viện đóng 1009 |
| Handler panic | Đóng 1011 `internal error`, ghi log, tiến trình sống tiếp |

**Luật:** BR-01, BR-02, BR-03, BR-04, BR-05, BR-06.

<a id="f-02"></a>
### F-02. Hạn mức mở kết nối theo IP (tốc độ)

| Mục | Nội dung |
|---|---|
| Mục đích | Chặn bão upgrade và dò token bằng cách giới hạn số lần mở websocket mỗi giây của mỗi IP; bổ sung cho giới hạn số kết nối đồng thời của F-01. |
| Tác nhân | Client; hạ tầng. |
| Tiền điều kiện | `FHIR_WS_RATE_LIMIT` > 0 (hoặc staging/production không đặt biến). |
| Cách gọi | Middleware đứng trước handler của `/ws/subscriptions-r4` (và các route websocket FHIRcast, mảnh B). Không áp cho `/healthz` và `/health/*`. |
| Cấu hình | `FHIR_WS_RATE_LIMIT` (lần mở mỗi giây mỗi IP; không đặt: 0 ở local, 10 ở staging/production; đặt rõ `0` mới tắt), `FHIR_WS_RATE_BURST` (0: gấp đôi `FHIR_WS_RATE_LIMIT`, tối thiểu 1), `FHIR_WS_TRUSTED_PROXIES`. |

**Luồng chính:** middleware lấy IP bằng `echo.IPExtractor` của gateway (mặc định `ExtractIPDirect`, không tin header; có `FHIR_WS_TRUSTED_PROXIES` thì tin `X-Forwarded-For` của đúng các proxy đó); mỗi IP có một xô token trong bộ nhớ (hết hạn nhớ sau 3 phút không dùng); còn token thì cho đi tiếp F-01.

**Thay thế / lỗi:**

| Tình huống | Kết quả |
|---|---|
| Hết token của IP | HTTP 429, JSON `{"error":"quá nhiều yêu cầu, thử lại sau"}`, `Retry-After: 1` |
| Không xác định được IP | HTTP 403, `{"error":"không xác định được người gọi"}` |
| `FHIR_WS_RATE_LIMIT=0` ở staging/production | Cảnh báo khi khởi động (vẫn chạy) |

**Luật:** BR-07.

<a id="f-03"></a>
### F-03. Bind Subscription bằng token

| Mục | Nội dung |
|---|---|
| Mục đích | Cho một kết nối nhận thông báo của một hoặc nhiều Subscription bằng cách trình token do `fhir-server` cấp; xác minh token, áp hạn mức, ghi nhận quyền của người bind. |
| Tác nhân | Client của Subscription (đã lấy token ở `fhir-server`); `fhir-server` (bên cấp). |
| Tiền điều kiện | Kết nối đã qua F-01. Token còn hạn (cấp với hạn 5 phút), ký bằng `FHIR_WS_SIGNING_KEY` hoặc một khóa trong `FHIR_WS_SIGNING_KEY_PREVIOUS`. Gateway không có database và không tra Subscription: chế độ `TokenOnly`. |
| Cách gọi | Thông điệp văn bản `{"type":"bind-with-token","payload":{"token":"..."}}`; có thể gửi nhiều lần trên một kết nối (mỗi Subscription một token). |
| Cấu hình | `FHIR_WS_SIGNING_KEY` (>= 32 ký tự, bắt buộc), `FHIR_WS_SIGNING_KEY_PREVIOUS` (tối đa 3 khóa, mỗi khóa >= 32 ký tự, cách nhau bằng dấu phẩy), `FHIR_WS_MAX_BINDINGS_PER_USER` (20 mỗi instance, phải > 0). |

**Luồng chính:**
1. Từ lúc mở kết nối, đồng hồ bind 10 giây chạy; thông điệp `bind-with-token` đầu tiên thành công thì dừng đồng hồ.
2. Gateway tách token `base64url(claims).base64url(chữ ký)`, kiểm chữ ký HMAC-SHA256 của chuỗi `"fhir-ws-binding|" + claims` với khóa hiện hành rồi các khóa cũ (so sánh thời gian không đổi, thử hết mọi khóa), kiểm phiên bản (thiếu `v` coi là 1; lớn hơn 1 bị từ chối), có `sub`, rồi kiểm hạn (`exp`).
3. Nếu token có ảnh chụp quyền (`snap`): giải mã (phiên bản ảnh chụp phải khớp) và kiểm access token gốc chưa hết hạn.
4. Dưới khóa của Hub: nếu kết nối đã bind Subscription này thì chỉ **thay ảnh chụp quyền** bằng bản mới (không tốn hạn mức, giữ bộ đếm sự kiện); nếu chưa thì kiểm trần 50 Subscription mỗi kết nối, kiểm hạn mức theo người (khóa `project|subject`), rồi ghi binding.
5. Hub nhờ Feed (F-06) đăng ký kênh Redis của Subscription và ghi sổ hiện diện; thất bại thì hoàn tác binding vừa thêm.
6. Gateway gửi `Bundle` `history` chứa `SubscriptionStatus` `type: "handshake"`.

**Thay thế / lỗi** (mọi lỗi đều đóng cả kết nối; mã và lý do nguyên văn từ mã):

| Tình huống | Mã đóng | Lý do |
|---|---|---|
| Không có bind hợp lệ trong 10 giây | 1008 | `cần bind-with-token` |
| Token sai định dạng, sai chữ ký (kể cả ký bằng khóa lạ hoặc thiếu tiền tố miền), thiếu `sub` | 1008 | `token không hợp lệ` |
| Token hết hạn (`now >= exp`) | 1008 | `token đã hết hạn` |
| Phiên bản token lớn hơn bản gateway hiểu | 1008 | `token có phiên bản không được hỗ trợ` |
| Ảnh chụp quyền hỏng hoặc phiên bản cũ | 1008 | `token không hợp lệ` |
| Access token gốc đã hết hạn | 1008 | `access token đã hết hạn` |
| Quá 50 Subscription trên kết nối | 1008 | `quá nhiều Subscription trên một kết nối` |
| Người đã đủ lượt bind đồng thời | 1008 | `User has exceeded allotted maximum number of concurrent WebSocket subscriptions: <n>` (n = `FHIR_WS_MAX_BINDINGS_PER_USER`) |
| Hub dựng không `TokenOnly` mà không có nguồn Subscription (không xảy ra ở gateway) | 1008 | `websocket chưa sẵn sàng` |
| Redis không đăng ký được kênh hoặc không ghi được sổ | 1013 | `tạm thời không nhận sự kiện được, thử lại sau` |

**Luật:** BR-08, BR-09, BR-10, BR-11, BR-12, BR-13, BR-14, BR-15, BR-16, BR-17.

<a id="f-04"></a>
### F-04. Điều khiển kết nối: unbind, ping/pong, vòng đời

| Mục | Nội dung |
|---|---|
| Mục đích | Cho client gỡ một Subscription khỏi kết nối đang mở, kiểm kết nối còn sống, và quy định cách gateway xử lý thông điệp lạ. |
| Tác nhân | Client của Subscription. |
| Tiền điều kiện | Đã bind ít nhất một Subscription (trừ `ping`, gửi lúc nào cũng được). |
| Cách gọi | `{"type":"unbind-from-token","payload":{"token":"..."}}`; `{"type":"ping"}`. |
| Cấu hình | Không (hằng: khung nhận 64 KiB). |

**Luồng chính:**
1. Gateway đọc từng khung. Khung phải là văn bản và JSON hợp lệ; `type` quyết định nhánh.
2. `unbind-from-token`: kiểm chữ ký và phiên bản của token nhưng **không kiểm hạn**; gỡ đúng Subscription nêu trong token khỏi **chính kết nối này** (trả hạn mức bind của người đó); Subscription chưa bind thì bỏ qua im lặng; kết nối và các binding khác giữ nguyên.
3. `ping`: gateway xếp `{"type":"pong"}` vào hàng đợi gửi. `ping` không dừng đồng hồ bind 10 giây.
4. `type` khác: trước khi bind thành công thì đóng 1008 `cần bind-with-token`; sau đó bị bỏ qua im lặng.
5. Kết nối thoát (client đóng, lỗi ghi, tắt gateway) thì mọi binding của nó bị gỡ và hạn mức được trả.

**Thay thế / lỗi:**

| Tình huống | Mã đóng | Lý do |
|---|---|---|
| Khung nhị phân | 1003 | `chỉ nhận văn bản` |
| JSON hỏng | 1003 | `JSON không hợp lệ` |
| `unbind-from-token` khi chưa bind nào | 1008 | `cần bind-with-token` |
| Token của unbind sai chữ ký, sai định dạng hoặc phiên bản lạ | 1008 | `token không hợp lệ` hoặc `token có phiên bản không được hỗ trợ` |
| Khung lớn hơn 64 KiB | 1009 | do thư viện |

**Luật:** BR-18, BR-19, BR-20.

<a id="f-05"></a>
### F-05. Phát thông báo tới từng người nhận có kiểm quyền

| Mục | Nội dung |
|---|---|
| Mục đích | Biến một sự kiện thay đổi resource (nhận từ F-06) thành `Bundle` `history` gửi tới mọi kết nối đã bind Subscription, **chỉ** tới người nhận được phép đọc resource đó; gửi heartbeat; ngắt người nhận quá chậm. |
| Tác nhân | Client của Subscription (người nhận); `fhir-server` (khớp tiêu chí, cô lập project). |
| Tiền điều kiện | Binding hiệu lực (F-03); sự kiện đã được `fhir-server` khớp với Subscription và phát lên kênh Redis. |
| Cách gọi | Không có lời gọi từ client; gateway đẩy. |
| Cấu hình | `FHIR_WS_HEARTBEAT` (30s, vừa là chu kỳ heartbeat vừa là chu kỳ ping WebSocket). Hằng: hàng đợi gửi 256, timeout ghi 10 giây. |

**Luồng chính:**
1. `handshake`: ngay sau bind (F-03), `SubscriptionStatus` `type: "handshake"`, không có resource và không có `eventsSinceSubscriptionStart`.
2. `event-notification`: với mỗi kết nối đang bind Subscription, theo ảnh chụp quyền của **người bind trên kết nối đó**: (a) access token gốc hết hạn thì gỡ binding đó và đếm `Denied`; (b) không đạt kiểm quyền đọc (scope SMART phải có `r` cho loại resource, AccessPolicy phải liệt kê loại, cho phép `read`, và resource phải thỏa tiêu chí `criteria` nếu có) thì bỏ im lặng, đếm `Denied`; (c) đạt thì tăng bộ đếm sự kiện của binding (`eventsSinceSubscriptionStart`, `eventNumber`), gỡ các trường `hiddenFields` của AccessPolicy khỏi **bản sao** resource dành cho người này, dựng Bundle (`entry[0]` là `SubscriptionStatus`, `entry[1]` có `fullUrl`, `request.method` POST/PUT/DELETE và `resource`) rồi xếp vào hàng đợi gửi của kết nối. Sự kiện xóa không kèm `resource`.
3. `heartbeat`: mỗi `FHIR_WS_HEARTBEAT`, writer gửi một `SubscriptionStatus` `heartbeat` cho **mỗi** Subscription còn bind, rồi ping WebSocket; ping hoặc ghi thất bại (quá 10 giây) thì cắt kết nối.
4. Token không có ảnh chụp quyền (bên cấp không xác thực) thì không lọc: mọi người bind đều nhận.

**Thay thế / lỗi:**

| Tình huống | Kết quả |
|---|---|
| Hàng đợi gửi (256) đầy | Kết nối đóng 1013 `người nhận quá chậm`; đếm `Slow` |
| Ghi hoặc ping quá `WriteTimeout` (10 giây) | Kết nối bị cắt (không có khung đóng) |
| Người nhận thiếu quyền | Không gửi gì, không báo (chỉ `Denied` tăng) |

**Luật:** BR-21, BR-22, BR-23, BR-24, BR-25, BR-26.

<a id="f-06"></a>
### F-06. Feed: nghe kênh Redis và sổ hiện diện

| Mục | Nội dung |
|---|---|
| Mục đích | Nối gateway với `fhir-server` qua Redis: chỉ nghe kênh của Subscription đang có socket, và báo cho bên phát biết "có gateway đang nghe" để họ khỏi phát vào hư không. |
| Tác nhân | `fhir-server` (bên khớp và phát); các gateway khác (cùng ghi sổ). |
| Tiền điều kiện | Redis dùng chung với `fhir-server` (`REDIS_URL`); cùng tiền tố khóa. |
| Cách gọi | Nội bộ: Hub gọi Feed khi bind (F-03) và Feed tự chạy vòng gia hạn. |
| Cấu hình | `FHIR_WS_PRESENCE_TTL` (30s, tối thiểu 3s), `FHIR_WS_INSTANCE_ID` (rỗng: ngẫu nhiên mỗi process), `REDIS_URL`. Hằng: timeout mỗi lời gọi Redis 2 giây, chu kỳ gia hạn = TTL/3. |

**Luồng chính:**
1. Lần đầu có socket bind một Subscription, Feed **đăng ký** kênh `fhirws.sub.<id>` (tên logic; khóa vật lý có tiền tố `trivita:events:ch:`), chờ Redis xác nhận, rồi **mới** ghi sổ hiện diện `fhirws.bound.<id>` (tiền tố `trivita:events:presence:`; ZSET, mục là `InstanceID`, điểm là thời điểm hết hạn theo đồng hồ Redis). Thứ tự này bảo đảm bên phát thấy "có người nghe" thì người nghe đã sẵn sàng.
2. Bên phát (fhir-server) chỉ `PUBLISH` lên kênh khi sổ có mục chưa hết hạn; không thì đếm `Unheard` và bỏ.
3. Thông điệp trên kênh là `{"sub": "<id>", "event": ChangeEvent}`; Feed kiểm `sub` khớp kênh, rồi giao cho F-05. Hai socket cùng Subscription trên một gateway dùng chung một đăng ký kênh.
4. Mỗi `TTL/3`, vòng dọn gia hạn mục sổ của mọi Subscription còn socket, và với Subscription không còn socket nào thì hủy đăng ký kênh và gỡ mục của gateway này khỏi sổ.
5. Khi tắt, `Feed.Stop` hủy mọi đăng ký và gỡ mọi mục ngay, không chờ hết hạn.

**Thay thế / lỗi:**

| Tình huống | Kết quả |
|---|---|
| Redis lỗi lúc đăng ký hoặc ghi sổ | Bind thất bại 1013 (F-03); nếu đã đăng ký thì hủy lại |
| Thông điệp kênh không phải JSON hoặc `sub` lệch kênh | Bỏ, log cảnh báo, đếm `Malformed` |
| Gia hạn sổ lỗi (Redis chập chờn) | Log cảnh báo, thử lại ở vòng sau; mục hết hạn sau TTL nếu Redis vắng lâu |
| Gateway chết im lặng | Mục tự hết hạn sau tối đa `FHIR_WS_PRESENCE_TTL`; bên phát ngừng gửi |
| Sự kiện phát lúc gateway khởi động lại hoặc Redis đứt | Mất (không bền, không phát lại) |

**Luật:** BR-27, BR-28, BR-29, BR-30.

<a id="f-07"></a>
### F-07. Khởi động và cấu hình (từ chối khởi động khi cấu hình sai)

| Mục | Nội dung |
|---|---|
| Mục đích | Đọc cấu hình, kiểm hợp lệ, lắp Redis, Hub, Feed; từ chối chạy khi cấu hình làm hỏng bảo mật hoặc không thể hoạt động. |
| Tác nhân | Vận hành; orchestrator. |
| Tiền điều kiện | Biến môi trường (và `.env` nếu có, không ghi đè biến thật). |
| Cách gọi | `ws-gateway` (chạy server); `ws-gateway healthcheck` (gọi `/healthz` của chính tiến trình). |
| Cấu hình | Toàn bộ `FHIR_WS_*`, `REDIS_URL`, `ENVIRONMENT`, `SHUTDOWN_*`, `READINESS_TIMEOUT`, `READ_HEADER_TIMEOUT`; chi tiết ở [../config.md](../config.md). |

**Luồng chính:**
1. `config.Load` đọc mọi biến và **gom toàn bộ lỗi** thành một lượt báo, nêu tên biến.
2. Dựng Redis và ping với `READINESS_TIMEOUT`: Redis chưa lên chỉ cảnh báo, tiến trình **không** thoát; `REDIS_URL` sai định dạng thì thoát.
3. Dựng `Tokens` (kiểm độ dài khóa), Hub (`TokenOnly`), Feed; đăng ký hàm dọn theo thứ tự ngược (Hub đóng, Feed gỡ sổ, Redis đóng).
4. Ở staging/production ghi cảnh báo (không thoát) khi `FHIR_WS_RATE_LIMIT=0`, `FHIR_WS_MAX_CONNS_PER_IP=0` hoặc `FHIR_WS_ALLOWED_ORIGINS` rỗng.
5. Nghe cổng (`HTTP_ADDR`, mặc định `:8007`) qua `pkg/httpserve`.

**Thay thế / lỗi:** thoát mã khác 0, in `lỗi: ...` ra stderr, không nghe cổng, khi có bất kỳ điều nào sau (thông điệp nguyên văn từ `internal/config`):

| Điều kiện | Thông điệp |
|---|---|
| Thiếu khóa | `FHIR_WS_SIGNING_KEY: bắt buộc (phải giống khóa của fhir-server, tối thiểu 32 ký tự)` |
| Khóa ngắn | `FHIR_WS_SIGNING_KEY: tối thiểu 32 ký tự` |
| Khóa cũ ngắn / quá 3 khóa | `FHIR_WS_SIGNING_KEY_PREVIOUS: mỗi khóa tối thiểu 32 ký tự` / `FHIR_WS_SIGNING_KEY_PREVIOUS: tối đa 3 khóa` |
| Staging/production mà `REDIS_URL` rỗng hoặc là mặc định | `REDIS_URL: bắt buộc đặt tường minh ở staging/production (Redis dùng chung với fhir-server)` |
| `FHIR_WS_MAX_CONNECTIONS`, `FHIR_WS_MAX_BINDINGS_PER_USER`, `FHIR_WS_HEARTBEAT`, `FHIR_WS_CLOSE_TIMEOUT` <= 0 | `... phải > 0` |
| `FHIR_WS_MAX_CONNS_PER_IP` âm | `FHIR_WS_MAX_CONNS_PER_IP: không được âm` |
| `FHIR_WS_PRESENCE_TTL` < 3s | `FHIR_WS_PRESENCE_TTL: tối thiểu 3s (gia hạn mỗi một phần ba TTL)` |
| `FHIR_WS_CLOSE_TIMEOUT` > `SHUTDOWN_TIMEOUT` | `FHIR_WS_CLOSE_TIMEOUT không được lớn hơn SHUTDOWN_TIMEOUT` |
| Origin là URL hoặc có `/` | `FHIR_WS_ALLOWED_ORIGINS: "..." phải là mẫu host (...), không phải URL` |
| Origin `*` ở staging/production | `FHIR_WS_ALLOWED_ORIGINS: "*" không được dùng ở staging/production` |
| CIDR proxy sai | `FHIR_WS_TRUSTED_PROXIES: "..." không phải CIDR` |
| Hạn mức âm | `FHIR_WS_RATE_LIMIT và FHIR_WS_RATE_BURST: không được âm` |
| Biến FHIRCAST_* sai hoặc đặt khi chưa bật | thuộc mảnh B (F-09 trở đi) |

**Luật:** BR-31, BR-32.

<a id="f-08"></a>
### F-08. Probe, tắt êm và quan sát

| Mục | Nội dung |
|---|---|
| Mục đích | Cho orchestrator biết gateway sống hay sẵn sàng; tắt không làm client mất kết nối đột ngột; xuất số đo vận hành. |
| Tác nhân | Orchestrator, load balancer, vận hành, hệ thống giám sát. |
| Tiền điều kiện | Gateway đang chạy. |
| Cách gọi | `GET /healthz`, `/health/live`, `/health/startup` (sống); `GET /health/ready` (sẵn sàng); SIGTERM hoặc SIGINT. |
| Cấu hình | `READINESS_TIMEOUT` (2s), `SHUTDOWN_DRAIN_DELAY` (5s), `SHUTDOWN_TIMEOUT` (25s), `FHIR_WS_CLOSE_TIMEOUT` (5s), `OTEL_*` (metric chỉ ghi khi `OTEL_METRICS_ENABLED=true`). |

**Luồng chính (probe):** các probe sống luôn trả 200 `{"status":"ok"}`, không phụ thuộc Redis. `/health/ready` ping Redis (hạn `READINESS_TIMEOUT`) và đọc cờ đang tắt; trả `{"ready":bool,"checks":{"accepting_traffic":bool,"redis":bool}}`, 200 khi cả hai đúng, 503 khi một trong hai sai.

**Luồng chính (tắt êm):**
1. Nhận SIGTERM/SIGINT: đặt cờ đang tắt, `/health/ready` trả 503 ngay.
2. Chờ `SHUTDOWN_DRAIN_DELAY` để load balancer rút instance.
3. Đóng HTTP server (kết nối websocket đã hijack không bị server HTTP đóng).
4. Chạy các hàm dọn theo thứ tự ngược: Hub đóng (upgrade mới 503, mọi socket nhận 1001 `server shutting down`, chờ client đáp tối đa `FHIR_WS_CLOSE_TIMEOUT`, rồi cắt), Feed gỡ mọi mục sổ và hủy đăng ký, đóng Redis. Tổng thời gian bị chặn bởi `SHUTDOWN_TIMEOUT`.
5. Thoát mã 0.

**Metric** (đăng ký khi khởi động): `ws_gateway.connections`, `.bindings`, `.subscriptions`, `.messages.sent`, `.connections.slow`, `.messages.denied`, `.events.received`, `.events.malformed`, `.redis.up`.

**Thay thế / lỗi:**

| Tình huống | Kết quả |
|---|---|
| Redis chết | `/health/ready` 503 (`checks.redis=false`), `/health/live` vẫn 200; socket đã bind **không** bị ngắt; bind mới đóng 1013; tự hồi phục khi Redis lên |
| Client không đáp khung đóng | Không giữ quá `FHIR_WS_CLOSE_TIMEOUT`; việc dọn nền có thể kéo thêm tối đa khoảng 5 giây (giới hạn của thư viện) |
| Handler không thoát trước `SHUTDOWN_TIMEOUT` | Hub trả lỗi ngữ cảnh, Serve trả lỗi |
| Không nghe được cổng | Lỗi `nghe <addr>`, thoát |

**Luật:** BR-33, BR-34, BR-35.

---

## Phần B: Hub FHIRcast

Mảnh B gồm tám chức năng của Hub FHIRcast (F-09..F-16). Các chức năng F-01..F-08 (websocket Subscription R4, nền dùng chung `wsgroup`, khởi động, probe, tắt êm) thuộc mảnh A. Bảng route và DTO nằm ở [../api-contract.md](../api-contract.md), không chép lại ở đây. Toàn bộ Hub chỉ có kênh `websocket`; không có webhook, không có `hub.callback`, không có lời gọi mạng ra ngoài (BR-36, BR-37).

<a id="f-09"></a>
### F-09. Đăng ký nhận sự kiện (hub.mode=subscribe)

| Mục | Nội dung |
|---|---|
| Mục đích | Cấp cho ứng dụng lâm sàng một URL websocket bí mật để nhận các sự kiện đổi ngữ cảnh của một `hub.topic` mà nó chọn. |
| Tác nhân | Ứng dụng lâm sàng (subscriber) có Bearer JWT hợp lệ (F-14). |
| Tiền điều kiện | `FHIRCAST_ENABLED=true`; Redis chạy; token có `fhir_project` và (mặc định) scope `fhircast/*`. |
| Cách gọi | `POST /fhircast/STU2`, `POST /fhircast/STU3` hoặc `POST /hub` (bí danh STU3), có hoặc không dấu `/` cuối; thân JSON hoặc `application/x-www-form-urlencoded`. Chi tiết trường: [../api-contract.md](../api-contract.md). |
| Cấu hình | `FHIRCAST_PUBLIC_URL` (dựng URL trả về, không suy từ header `Host`), `FHIRCAST_LEASE` (mặc định 1 giờ), `FHIRCAST_MAX_BODY_BYTES`. |

**Luồng chính**
1. Gateway xác thực Bearer (F-14), lấy `project` và `subject` từ token.
2. Thân không có khóa `id` và không có `event` khác null nên được hiểu là đăng ký/hủy (BR-53).
3. Hub kiểm theo thứ tự: thiếu `hub.channel.type`, thiếu `hub.mode`, thiếu `hub.topic`; rồi `hub.channel.type` phải đúng `websocket`; `hub.mode` phải là `subscribe` hoặc `unsubscribe`; `hub.topic` hợp lệ (BR-38, BR-40).
4. Với `subscribe`: `hub.events` bắt buộc, tách theo dấu phẩy, mọi tên phải thuộc 14 sự kiện được hỗ trợ (BR-39).
5. Hub sinh `endpoint` mới (UUID v4 từ `crypto/rand`, BR-41) và ghi vào Redis `{projectId, topic, events, version, userSubject, expiresAt}` với TTL bằng lease (BR-42).
6. Phản hồi `202` `{"hub.channel.endpoint": "<FHIRCAST_PUBLIC_URL>/ws/fhircast/<endpoint>"}`. Mỗi lần đăng ký là một endpoint mới, kể cả cùng người, cùng topic.

**Thay thế / lỗi** (OperationOutcome, thông điệp ở `issue[0].details.text`)

| Điều kiện | HTTP | Thông điệp |
|---|---|---|
| Thiếu `hub.channel.type` | 400 | `Missing hub.channel.type` |
| Thiếu `hub.mode` | 400 | `Missing hub.mode` |
| Thiếu `hub.topic` | 400 | `Missing hub.topic` |
| `hub.channel.type` khác `websocket` (webhook, rest-hook, email, `WebSocket` viết hoa) | 400 | `Invalid hub.channel.type` |
| `hub.mode` lạ | 400 | `Invalid hub.mode` |
| `hub.topic` rỗng sau kiểm, quá 256 byte, có ký tự điều khiển | 400 | `Invalid hub.topic` |
| Thiếu `hub.events` | 400 | `Missing hub.events` |
| `hub.events` rỗng sau tách hoặc có một tên không được hỗ trợ | 400 | `Invalid hub.events` |
| Redis không ghi được đăng ký | 500 | `Failed to create subscription for topic` |
| Thân quá `FHIRCAST_MAX_BODY_BYTES` | 413 | `Request body too large` |
| Thân JSON không phải đối tượng hoặc form hỏng | 400 | `Invalid request body` |

**Luật:** BR-36, BR-37, BR-38, BR-39, BR-40, BR-41, BR-42, BR-53, BR-65

<a id="f-10"></a>
### F-10. Hủy đăng ký (hub.mode=unsubscribe)

| Mục | Nội dung |
|---|---|
| Mục đích | Cho phép subscriber chấm dứt một đăng ký của chính nó; socket đang nối được báo `denied` và đóng. |
| Tác nhân | Đúng người đã tạo đăng ký (cùng `subject`, cùng project). |
| Tiền điều kiện | Bearer hợp lệ; biết URL `hub.channel.endpoint` đã được cấp. |
| Cách gọi | Cùng route với F-09: `POST {v}` với `hub.mode=unsubscribe`, `hub.topic`, `hub.channel.endpoint` (hoặc trường cũ `endpoint` của client trước `@medplum/core` 5.1.29). |
| Cấu hình | `FHIRCAST_PUBLIC_URL` (để gương URL trong phản hồi). |

**Luồng chính**
1. Các kiểm tra chung của F-09 (kênh, mode, topic).
2. Hub lấy endpoint từ `hub.channel.endpoint` (bỏ query, fragment, dấu `/` thừa, lấy đoạn cuối); nếu rỗng thì thử trường `endpoint` cũ (BR-44).
3. Hub đọc đăng ký; chỉ chấp nhận khi tồn tại, cùng project, cùng topic và (nếu đăng ký và token cùng có subject) cùng người (BR-43).
4. Hub xóa đăng ký, rồi phát lên kênh topic thông điệp `denied` với `hub.reason` `Subscriber unsubscribed from topic`, gắn `target` là endpoint đó, nên chỉ socket đó nhận (BR-45).
5. Phản hồi `202` `{"hub.channel.endpoint": "<URL đã hủy>"}`.

**Thay thế / lỗi**

| Điều kiện | HTTP | Thông điệp |
|---|---|---|
| Không có endpoint nào ở hai trường | 400 | `Missing endpoint` |
| Không tồn tại, hết hạn, khác project, khác topic, của người khác, đã hủy rồi | 400 | `Invalid endpoint` (một thông điệp cho mọi trường hợp) |
| Redis không đọc hoặc không xóa được | 500 | `Internal server error` |
| Xóa xong nhưng không phát được `denied` | 202 | Đăng ký đã mất; socket đang nối vẫn mở đến hết lease (chỉ ghi log mức cảnh báo) |

**Luật:** BR-43, BR-44, BR-45, BR-38

<a id="f-11"></a>
### F-11. Đổi ngữ cảnh (POST {v} và POST {v}/{topic})

| Mục | Nội dung |
|---|---|
| Mục đích | Một ứng dụng thông báo cho mọi ứng dụng cùng topic rằng bệnh nhân, ca khám, ảnh hoặc báo cáo đang làm việc đã đổi (open, close, select, userlogout, userhibernate, heartbeat, syncerror). |
| Tác nhân | Ứng dụng lâm sàng (publisher) có Bearer hợp lệ trong cùng project với các subscriber. |
| Tiền điều kiện | Bearer hợp lệ (F-14); Redis chạy. |
| Cách gọi | `POST {v}` hoặc `POST {v}/` với thân có `id`/`event`; hoặc `POST {v}/{topic}` (topic ở đường dẫn, giải mã đúng một lần). |
| Cấu hình | `FHIRCAST_ALLOW_TOPIC_MISMATCH` (mặc định tắt), `FHIRCAST_MAX_BODY_BYTES`, `FHIRCAST_MAX_CONTEXT_BYTES`. |

**Luồng chính**
1. Gateway xác thực và đọc thân (JSON hoặc form; kiểu khác coi như thân rỗng).
2. Ở `POST {v}/{topic}`, nếu thân đủ hợp lệ để biết topic mà topic của event khác topic ở đường dẫn thì từ chối `Topic mismatch`, trừ khi `FHIRCAST_ALLOW_TOPIC_MISMATCH=true` (BR-54).
3. Hub sao chép payload (không sửa bản của người gọi), kiểm hình dạng theo thứ tự cố định (BR-52).
4. Theo loại sự kiện (BR-56): `*-open` ghi ngữ cảnh mới (BR-57); `*-close` xóa ngữ cảnh hiện tại (BR-58); `DiagnosticReport-update` xử lý ở F-12; các sự kiện khác chỉ phát.
5. Hub phát thông điệp (đã có `context.versionId` nếu là open/update) lên kênh `fhircast:ch:<project>:<băm topic>` (BR-62).
6. Phản hồi `202` `{"success": true, "event": <thông điệp đã phát>}`.

**Thay thế / lỗi**

| Điều kiện | HTTP | Thông điệp |
|---|---|---|
| Thiếu `id` | 400 | `Missing event ID` |
| Thiếu `timestamp` | 400 | `Missing event timestamp` |
| `event` không phải đối tượng | 400 | `Missing event payload` |
| Thiếu `event["hub.topic"]` | 400 | `Missing event["hub.topic"]` |
| Thiếu `event["hub.event"]` | 400 | `Missing event["hub.event"]` |
| `context` không có | 400 | `Missing event.context` |
| `context` không phải mảng đối tượng | 400 | `Invalid event.context` |
| Topic không hợp lệ | 400 | `Invalid event["hub.topic"]` |
| Tên sự kiện ngoài 14 sự kiện | 400 | `Invalid event["hub.event"]` |
| Topic ở đường dẫn khác topic của event | 400 | `Topic mismatch` |
| Payload không thành JSON hợp lệ khi sao chép | 400 | `Invalid event payload` |
| Ngữ cảnh mã hóa vượt `FHIRCAST_MAX_CONTEXT_BYTES` | 400 | `Context exceeds the maximum size` |
| Quá 64 ngữ cảnh đã lưu của một topic | 400 | `Too many stored contexts for this topic` |
| Redis hoặc Bus lỗi | 500 | `Internal server error` (chi tiết chỉ ở log) |

**Luật:** BR-52, BR-53, BR-54, BR-55, BR-56, BR-57, BR-58, BR-59, BR-62, BR-65

<a id="f-12"></a>
### F-12. Báo cáo chẩn đoán: cập nhật nội dung, lưu và khôi phục (DiagnosticReport)

| Mục | Nội dung |
|---|---|
| Mục đích | Cho nhiều ứng dụng cùng chỉnh nội dung một báo cáo qua sự kiện `DiagnosticReport-update`, với phiên bản (`context.versionId`) để không ghi đè nhau; cất và khôi phục ngữ cảnh khi chuyển qua lại giữa các báo cáo. |
| Tác nhân | Ứng dụng soạn báo cáo (publisher) và các ứng dụng khác theo dõi cùng topic. |
| Tiền điều kiện | Đã có `DiagnosticReport-open` trên topic (ngữ cảnh hiện tại có `context.type = DiagnosticReport`). |
| Cách gọi | Cùng route F-11, `hub.event` là `DiagnosticReport-open` hoặc `DiagnosticReport-update`. |
| Cấu hình | `FHIRCAST_MAX_UPDATE_ENTRIES` (mặc định 200), `FHIRCAST_CONTEXT_TTL`, `FHIRCAST_MAX_CONTEXT_BYTES`. |

**Luồng chính**
1. `DiagnosticReport-open`: Hub tạo ngữ cảnh mới, tự thêm một Bundle `collection` rỗng ở khóa `content`, sinh `context.versionId` (BR-57).
2. Nếu ngữ cảnh hiện tại đang là DiagnosticReport, Hub cất nó theo id báo cáo trước khi ghi đè; mở lại một báo cáo đã cất thì khôi phục đúng ngữ cảnh và `versionId` cũ (BR-59).
3. `DiagnosticReport-update`: kiểm có DiagnosticReport đang mở; so `event["context.versionId"]` với phiên bản hiện tại (BR-60).
4. Hub áp từng entry của update bundle lên Bundle `content` trên một bản sao: `PUT` thêm hoặc thay theo `id`, `DELETE` xóa theo `fullUrl` (`Loại/id`). Lỗi giữa chừng thì không lưu gì.
5. Hub sinh `versionId` mới, gắn `context.priorVersionId`, ghi bằng so sánh-rồi-ghi (BR-61) và phát sự kiện.

**Thay thế / lỗi** (HTTP 400 trừ khi ghi khác)

| Điều kiện | Thông điệp |
|---|---|
| Không có DiagnosticReport đang mở (kể cả ngữ cảnh hiện tại lỗi, không có `report`) | `No DiagnosticReport currently open for this topic` |
| `versionId` sai | `Expected event.context.versionId to be '<hiện tại>', received '<nhận>'` |
| `versionId` rỗng | `Expected event.context.versionId to be '<hiện tại>', received empty event.context.versionId` |
| Thiếu tài nguyên `updates` trong context | `Update event requires an update bundle in the context` |
| Quá số entry | `Update bundle has too many entries (maximum 200)` |
| Entry thiếu `request.method` | `Update bundle contains entry with a missing request.method` |
| `PUT` thiếu `resource` | `Missing resource in a update bundle PUT entry` |
| `DELETE` thiếu `fullUrl` | `DELETE entry in update bundle missing fullUrl` |
| `fullUrl` không dạng `Loại/id` | `fullUrl in DELETE entry is not a resolvable reference string` |
| Xóa tài nguyên thuộc ngữ cảnh mở ban đầu | `Cannot delete a resource that is part of the original open context` |
| Xóa tài nguyên không có trong `content` | `Cannot delete resource not currently in the content bundle` |
| Phương thức khác `PUT`, `DELETE` | `Update bundle contains bad entry with request.method = '<m>', only 'PUT' and 'DELETE' are supported by an update event` |
| Hai update tranh nhau cùng phiên bản (HTTP 409) | `Context for this topic changed while the update was being applied, it is no longer at version '<prior>'` |

**Luật:** BR-57, BR-59, BR-60, BR-61, BR-64, BR-65

<a id="f-13"></a>
### F-13. Đọc ngữ cảnh hiện tại và cấu hình Hub (GET)

| Mục | Nội dung |
|---|---|
| Mục đích | Cho ứng dụng vừa vào topic biết ngữ cảnh đang mở (đồng bộ lúc bắt đầu), và cho client dò Hub hỗ trợ gì. |
| Tác nhân | Ứng dụng lâm sàng (đọc ngữ cảnh, cần Bearer); bất kỳ ai (cấu hình, không cần token). |
| Tiền điều kiện | Đọc ngữ cảnh: Bearer hợp lệ. Cấu hình: không. |
| Cách gọi | `GET {v}/{topic}` và `GET {v}/.well-known/fhircast-configuration` cho `{v}` là `/fhircast/STU2`, `/fhircast/STU3`, `/hub`. |
| Cấu hình | Không có riêng (dùng chung `FHIRCAST_CONTEXT_TTL` làm hạn dữ liệu). |

**Luồng chính**
1. `.well-known`: trả `{eventsSupported: <14 sự kiện>, websocketSupport: true, webhookSupport: false, fhircastVersion: "STU2"|"STU3"}`; chỉ STU3 có thêm `getCurrentSupport: true`. Không cần token.
2. `GET {v}/{topic}`: xác thực, giải mã topic đúng một lần, kiểm `ValidTopic`, đọc khóa Redis theo `(project của token, topic)`.
3. STU2 trả mảng `context` (hoặc `[]` khi không có); STU3 trả `{"context.type","context","context.versionId"}` (hoặc `{"context.type":"","context":[]}`) (BR-66).

**Thay thế / lỗi**

| Điều kiện | HTTP | Thông điệp |
|---|---|---|
| Topic không hợp lệ | 400 | `Invalid hub.topic` |
| Redis không đọc được | 500 | `Internal server error` |
| Topic không có ngữ cảnh (hoặc dữ liệu hỏng, hết hạn, thuộc project khác) | 200 | Dạng rỗng như trên, không phân biệt các nguyên nhân |

**Luật:** BR-62, BR-63, BR-64, BR-66

<a id="f-14"></a>
### F-14. Xác thực Bearer JWT cho Hub

| Mục | Nội dung |
|---|---|
| Mục đích | Chỉ người gọi có token do auth cấp, thuộc một project, và (mặc định) có scope `fhircast/*`, mới dùng được Hub; project của token là ranh giới cô lập dữ liệu. |
| Tác nhân | Mọi client gọi route Hub trừ `.well-known`. |
| Tiền điều kiện | Cấu hình khóa xác thực (đúng một trong `FHIRCAST_JWKS_URL`, `FHIRCAST_JWT_HMAC_SECRET`), `FHIRCAST_JWT_ISSUER`, `FHIRCAST_JWT_AUDIENCE`. |
| Cách gọi | Header `Authorization: Bearer <token>` (scheme không phân biệt hoa thường). |
| Cấu hình | `FHIRCAST_JWT_ISSUER`, `FHIRCAST_JWT_AUDIENCE`, `FHIRCAST_JWKS_URL`, `FHIRCAST_JWT_HMAC_SECRET`, `FHIRCAST_REQUIRE_SCOPE` (mặc định true). |

**Luồng chính**
1. Lấy token từ header; thiếu hoặc scheme khác `Bearer` thì 401.
2. Verifier kiểm chữ ký (JWKS với RS/PS/ES/EdDSA, hoặc HS256 chia sẻ nếu dùng khóa HMAC), `iss`, `aud`, `exp` (bắt buộc), `sub`.
3. Claim project (mặc định `fhir_project`) phải khác rỗng, nếu không 403 (BR-47).
4. Nếu `FHIRCAST_REQUIRE_SCOPE=true`, phải có ít nhất một scope bắt đầu bằng `fhircast/` (BR-48). Không cần quyền `fhir.*` (BR-49).
5. Người gọi (subject, project) được đặt vào ngữ cảnh yêu cầu; project dùng cho mọi khóa Redis và tên kênh.

**Thay thế / lỗi**

| Điều kiện | HTTP | Mã OperationOutcome | Thông điệp |
|---|---|---|---|
| Thiếu token, scheme sai, token sai chữ ký, sai `iss`/`aud`, hết hạn, thiếu `sub`, JWKS không tải được hoặc thiếu `kid` | 401 + `WWW-Authenticate: Bearer realm="fhircast"` | `login` | `Unauthorized` |
| Thiếu project; hoặc thiếu scope `fhircast/*` khi bắt buộc | 403 | `forbidden` | `Forbidden` |
| Lỗi xác thực không phân loại được | 500 | `exception` | `Internal server error` |

**Luật:** BR-46, BR-47, BR-48, BR-49, BR-50, BR-51

<a id="f-15"></a>
### F-15. Kênh websocket FHIRcast (/ws/fhircast/{endpoint})

| Mục | Nội dung |
|---|---|
| Mục đích | Chuyển sự kiện đổi ngữ cảnh theo thời gian thực tới đúng những subscriber đã đăng ký, kèm heartbeat và hạn lease. |
| Tác nhân | Ứng dụng lâm sàng đã có URL từ F-09. |
| Tiền điều kiện | Endpoint còn sống trong Redis. Không cần Bearer (BR-67). |
| Cách gọi | `GET /ws/fhircast/{endpoint}` nâng cấp lên WebSocket; đường dẫn nhiều đoạn cũng vào cùng handler, endpoint là đoạn cuối. Chung hạn mức mở kết nối và `wsgroup` với websocket Subscription (mảnh A). |
| Cấu hình | `FHIRCAST_MAX_CONNECTIONS` (1000), `FHIRCAST_MAX_SOCKETS_PER_ENDPOINT` (4), `FHIRCAST_HEARTBEAT` (10s), `FHIR_WS_MAX_CONNS_PER_IP`, `FHIR_WS_ALLOWED_ORIGINS`, `FHIR_WS_CLOSE_TIMEOUT`, `FHIR_WS_RATE_LIMIT`. |

**Luồng chính**
1. `wsgroup` kiểm: đang tắt (503), quá `MaxConns` (503), quá số kết nối mỗi IP (429 + `Retry-After: 5`), `Origin` không được phép (403) (BR-72, BR-73).
2. Hub đọc đăng ký của endpoint; không có, hỏng, hoặc đã quá `expiresAt` thì gửi `{"hub.mode":"denied","hub.reason":"invalid endpoint",...}` rồi đóng 1000 (BR-67).
3. Kiểm số socket đồng thời của endpoint trên instance này; vượt thì gửi `denied` `too many connections for this endpoint` rồi đóng 1008 (BR-72).
4. Hub nghe kênh Redis của `(project, topic)` TRƯỚC khi gửi xác nhận.
5. Khung đầu tiên luôn là xác nhận: STU3 có bốn trường `hub.mode`, `hub.topic`, `hub.events`, `hub.lease_seconds`; STU2 thêm bốn trường rỗng `hub.callback`, `hub.channel`, `hub.secret`, `hub.subscriber` (BR-68).
6. Mỗi thông điệp từ kênh qua bộ lọc người nhận (BR-69) rồi xếp vào hàng đợi riêng của socket (64 khung); heartbeat cục bộ sinh theo `FHIRCAST_HEARTBEAT` (BR-70).
7. Hết lease: gửi `denied` `lease expired` rồi đóng 1000; nhận `denied` do hủy: gửi rồi đóng 1000 (BR-71, BR-45).
8. Tin client gửi lên chỉ được đếm và (nếu không phải JSON) ghi log cảnh báo; không có tác dụng và không làm đóng socket (BR-74).

**Thay thế / lỗi và mã đóng**

| Điều kiện | Kết quả quan sát được |
|---|---|
| Endpoint không có, hết hạn, hỏng, rỗng | Khung `denied` (`hub.reason`=`invalid endpoint`), đóng 1000 |
| Quá `FHIRCAST_MAX_SOCKETS_PER_ENDPOINT` | Khung `denied` (`too many connections for this endpoint`), đóng 1008 |
| Redis lỗi khi đọc đăng ký hoặc nghe kênh | Đóng 1013 (`dịch vụ tạm thời không sẵn sàng` hoặc `không nghe được kênh`) |
| Hàng đợi 64 khung đầy (client chậm) | Đóng 1013 (`client quá chậm`) |
| Hết lease | Khung `denied` (`lease expired`), đóng 1000 |
| Subscriber bị hủy | Khung `denied` (`Subscriber unsubscribed from topic`), đóng 1000 |
| Khung client lớn hơn 64 KiB | Thư viện đóng 1009 |
| Tắt êm | 1001 (do `wsgroup`, xem mảnh A) |

**Luật:** BR-67, BR-68, BR-69, BR-70, BR-71, BR-72, BR-73, BR-74

<a id="f-16"></a>
### F-16. Bật, cấu hình và hạ tầng Redis của Hub

| Mục | Nội dung |
|---|---|
| Mục đích | Bật Hub chỉ khi cấu hình đầy đủ và an toàn; dùng Redis dùng chung cho kho trạng thái (khóa) và Bus (pub/sub); nêu ranh giới với auth, nơi cấp token và topic. |
| Tác nhân | Vận hành; auth (bên cấp token). |
| Tiền điều kiện | Redis dùng chung của gateway; auth phát hành token ký bằng khóa mà gateway xác thực được. |
| Cách gọi | Biến môi trường `FHIRCAST_*` lúc khởi động. Bảng đầy đủ: [../config.md](../config.md). |
| Cấu hình | Xem bảng dưới. |

**Luồng chính**
1. Khởi động đọc `FHIRCAST_*`; kiểm hợp lệ (BR-75). Tắt mà vẫn đặt cấu hình thì từ chối khởi động.
2. Khi bật, bootstrap dựng Verifier JWT, `RedisStore` (tiền tố khóa `fhircast`), `RedisBus` (một kết nối pub/sub cho cả instance), máy chủ socket, rồi đăng ký route Hub (khi tắt các route không tồn tại: 404).
3. Thứ tự tắt: đóng socket (1001) trước, rồi đóng bus, rồi Redis.
4. Ranh giới với auth (chỉ mô tả): auth cấp access token có scope `fhircast/...` và (khi cấu hình `hub.url`) kèm `hub.url` và `hub.topic` trong phản hồi token; `hub.topic` là topic ổn định theo người dùng. Không lấy được topic thì auth không cấp token. Gateway KHÔNG cấp token, KHÔNG biết topic thuộc về ai, chỉ kiểm project và scope (BR-62).

| Biến | Mặc định | Ý nghĩa |
|---|---|---|
| `FHIRCAST_ENABLED` | tắt | Bật Hub |
| `FHIRCAST_PUBLIC_URL` | (bắt buộc khi bật) | `ws://` hoặc `wss://host[:port]`, không đường dẫn; `wss://` ở staging/production |
| `FHIRCAST_JWT_ISSUER`, `FHIRCAST_JWT_AUDIENCE` | (bắt buộc khi bật) | `iss`, `aud` mà token phải có |
| `FHIRCAST_JWKS_URL` hoặc `FHIRCAST_JWT_HMAC_SECRET` | đúng một trong hai | JWKS (https ở staging/production) hoặc HS256 từ 32 ký tự (chỉ dev/test) |
| `FHIRCAST_REQUIRE_SCOPE` | true | Đòi scope `fhircast/*`; `false` bị cấm ở staging/production |
| `FHIRCAST_ALLOW_TOPIC_MISMATCH` | false | Cho `POST {v}/{topic}` dùng topic của event |
| `FHIRCAST_LEASE` | 1h | Trong (0, 24h] |
| `FHIRCAST_CONTEXT_TTL` | 24h | Lớn hơn 0 |
| `FHIRCAST_MAX_CONTEXT_BYTES`, `FHIRCAST_MAX_UPDATE_ENTRIES`, `FHIRCAST_MAX_BODY_BYTES` | 256 KiB, 200, 1 MiB | Lớn hơn 0 |
| `FHIRCAST_MAX_CONNECTIONS`, `FHIRCAST_MAX_SOCKETS_PER_ENDPOINT`, `FHIRCAST_HEARTBEAT` | 1000, 4, 10s | Lớn hơn 0 |

**Thay thế / lỗi:** khi cấu hình sai, tiến trình không khởi động và liệt kê lỗi (thông điệp nguyên văn ở BR-75). Khi Redis chết lúc chạy: đăng ký 500, đổi ngữ cảnh 500, đọc ngữ cảnh 500, socket mới 1013 (BF-6).

**Luật:** BR-62, BR-63, BR-64, BR-75
