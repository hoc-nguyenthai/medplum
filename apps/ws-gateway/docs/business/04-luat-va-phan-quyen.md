# 4. Luật, kịch bản chấp nhận, truy vết và điểm lệch

## 4.1 Luật nghiệp vụ

### Phần A

### Nâng cấp và giới hạn kết nối (BR-01..07)

| Mã | Luật | Loại | F |
|---|---|---|---|
| BR-01 | Khi gateway đang đóng, mọi upgrade mới bị từ chối bằng HTTP 503; kết nối lọt qua đúng lúc đóng bị đóng 1001. | OPS | F-01 |
| BR-02 | Khi số chỗ đã đặt đạt `FHIR_WS_MAX_CONNECTIONS` (mặc định 1000 mỗi instance), upgrade bị từ chối bằng 503 mà không gọi `Accept` và không tạo goroutine; chỗ được đặt trước `Accept` nên upgrade đồng thời không vượt trần, và chỗ được trả ở mọi đường thoát. | SEC | F-01 |
| BR-03 | Khi `FHIR_WS_MAX_CONNS_PER_IP` > 0, một IP không có quá số đó kết nối đang mở; vượt thì 429 kèm `Retry-After: 5`. IP lấy từ địa chỉ kết nối; `X-Forwarded-For` chỉ được tin khi proxy trực tiếp nằm trong `FHIR_WS_TRUSTED_PROXIES`. | SEC | F-01 |
| BR-04 | Yêu cầu có header `Origin` mà không khớp mẫu host của `FHIR_WS_ALLOWED_ORIGINS` (hoặc khác host khi danh sách rỗng) bị từ chối 403; yêu cầu không có `Origin` thì không bị kiểm (an toàn dựa vào token). | SEC | F-01 |
| BR-05 | `FHIR_WS_ALLOWED_ORIGINS` chỉ chứa mẫu host (không URL, không `/`), và không được chứa `*` ở staging/production; vi phạm thì gateway không khởi động. | SEC | F-01, F-07 |
| BR-06 | Khung nhận lớn hơn 64 KiB làm kết nối bị đóng 1009; handler panic làm kết nối đóng 1011 mà không làm sập tiến trình. | OPS | F-01 |
| BR-07 | Khi `FHIR_WS_RATE_LIMIT` > 0, mỗi IP chỉ mở được websocket theo xô token (`FHIR_WS_RATE_BURST`, mặc định gấp đôi tốc độ); hết token thì 429 `{"error":"quá nhiều yêu cầu, thử lại sau"}` kèm `Retry-After: 1`; không xác định được IP thì 403; health không bị giới hạn; staging/production không đặt biến thì mặc định 10, đặt rõ `0` mới tắt. | SEC | F-02 |

### Bind bằng token (BR-08..17)

| Mã | Luật | Loại | F |
|---|---|---|---|
| BR-08 | Kết nối không gửi `bind-with-token` thành công trong 10 giây bị đóng 1008 `cần bind-with-token`; `ping` không dừng đồng hồ; thông điệp khác bind gửi trước bind đầu tiên đóng ngay 1008. | SEC | F-03, F-04 |
| BR-09 | Chỉ token có chữ ký HMAC-SHA256 hợp lệ trên `"fhir-ws-binding|" + claims` với khóa hiện hành hoặc một trong tối đa 3 khóa cũ mới được nhận; mọi token khác (sai định dạng, sai chữ ký, thiếu tiền tố miền, thiếu `sub`) đóng 1008 `token không hợp lệ`. Gateway không có nguồn Subscription và không tự cấp token. | SEC | F-03 |
| BR-10 | Token đã quá hạn (`now >= exp`) bị đóng 1008 `token đã hết hạn` khi bind; hạn do bên cấp quy định (mặc định 5 phút). | SEC | F-03 |
| BR-11 | Token có phiên bản lớn hơn bản gateway hỗ trợ (hiện 1) bị đóng 1008; token không có `v` coi là phiên bản 1. | DATA | F-03 |
| BR-12 | Token mang ảnh chụp quyền hỏng, sai phiên bản, hoặc access token gốc đã hết hạn thì bị từ chối 1008 (`token không hợp lệ`, `access token đã hết hạn`). | SEC | F-03 |
| BR-13 | Một kết nối giữ tối đa 50 Subscription; bind thứ 51 (Subscription mới) đóng 1008 `quá nhiều Subscription trên một kết nối`. | SEC | F-03 |
| BR-14 | Một người (khóa `project\|subject` trong ảnh chụp quyền) có tối đa `FHIR_WS_MAX_BINDINGS_PER_USER` (20) lượt bind đồng thời trên mọi kết nối của một instance; vượt thì đóng 1008 `User has exceeded allotted maximum number of concurrent WebSocket subscriptions: <n>`; lượt bind được trả khi unbind, đóng kết nối hoặc binding bị gỡ; người khác không bị ảnh hưởng; token không có ảnh chụp hoặc không có `Subject` không bị đếm. | SEC | F-03 |
| BR-15 | Bind lại Subscription đã bind trên cùng kết nối không tốn hạn mức, thay ảnh chụp quyền bằng bản mới ngay lập tức, giữ bộ đếm sự kiện và gửi lại `handshake`. | DATA | F-03 |
| BR-16 | Gateway chỉ tin token (`TokenOnly`) vì token do `fhir-server` cấp sau khi kiểm Subscription đọc được, hoạt động và dùng kênh websocket; Hub không bật `TokenOnly` mà thiếu nguồn Subscription thì từ chối mọi bind (1008 `websocket chưa sẵn sàng`) thay vì tin token. | SEC | F-03 |
| BR-17 | Nếu Redis không đăng ký được kênh hoặc ghi được sổ hiện diện, bind thất bại bằng 1013 và binding vừa thêm bị hoàn tác (không để socket "bind nhưng điếc"). | DATA | F-03, F-06 |

### Điều khiển kết nối (BR-18..20)

| Mã | Luật | Loại | F |
|---|---|---|---|
| BR-18 | Kết nối chỉ nhận khung văn bản JSON: khung nhị phân đóng 1003 `chỉ nhận văn bản`, JSON hỏng đóng 1003 `JSON không hợp lệ`. | SEC | F-04 |
| BR-19 | `unbind-from-token` chỉ gỡ Subscription nêu trong token khỏi chính kết nối gửi nó; token phải có chữ ký và phiên bản hợp lệ nhưng không cần còn hạn; Subscription chưa bind thì bỏ qua; token sai hoặc gửi trước bind đầu tiên đóng 1008. | SEC | F-04 |
| BR-20 | `ping` được trả `{"type":"pong"}`; thông điệp `type` lạ sau khi đã bind bị bỏ qua. | OPS | F-04 |

### Phát thông báo và quyền người nhận (BR-21..26)

| Mã | Luật | Loại | F |
|---|---|---|---|
| BR-21 | Gateway gửi `heartbeat` mỗi `FHIR_WS_HEARTBEAT` cho mỗi Subscription còn bind và ping WebSocket; ghi hoặc ping quá `WriteTimeout` (10 giây) thì cắt kết nối. Heartbeat được sinh cục bộ, không chứng minh đường nhận sự kiện từ Redis còn sống. | OPS | F-05 |
| BR-22 | Mỗi sự kiện được kiểm quyền cho **từng người nhận** theo ảnh chụp trong token của họ: scope SMART phải có `r` cho loại resource, AccessPolicy phải liệt kê loại, cho phép đọc và resource phải thỏa tiêu chí; không đạt thì không gửi gì, không lộ tham chiếu (kể cả sự kiện xóa), chỉ `Denied` tăng. Token không có ảnh chụp thì không lọc. | SEC | F-05 |
| BR-23 | Access token gốc hết hạn thì binding bị gỡ khi sự kiện kế tiếp đến (không gửi tin nào); quyền đổi sau khi cấp token không có hiệu lực cho tới khi access token hết hạn. | SEC | F-05 |
| BR-24 | Trường `hiddenFields` của AccessPolicy được gỡ khỏi bản resource trong `event-notification` dành cho người đó, bản của người khác không đổi. | SEC | F-05 |
| BR-25 | `event-notification` của sự kiện xóa có `request.method` `DELETE`, `focus` và không có `resource`. | DATA | F-05 |
| BR-26 | `eventsSinceSubscriptionStart` và `eventNumber` là bộ đếm của **một binding trên một kết nối**: bắt đầu từ 1, chỉ tăng với sự kiện đã đạt kiểm quyền và được xếp hàng đợi, giữ khi bind lại, mất khi kết nối đóng; chỉ có trong `event-notification`. Người nhận quá chậm (hàng đợi 256 đầy) bị đóng 1013 `người nhận quá chậm`. | DATA | F-05 |

### Feed và độ bền (BR-27..30)

| Mã | Luật | Loại | F |
|---|---|---|---|
| BR-27 | Gateway chỉ nghe kênh `fhirws.sub.<id>` của Subscription đang có ít nhất một socket bind; Subscription khác không đi qua gateway. | SEC | F-06 |
| BR-28 | Feed đăng ký kênh xong rồi mới ghi sổ hiện diện `fhirws.bound.<id>` (không bao giờ ngược lại), để bên phát thấy "có người nghe" thì người nghe đã sẵn sàng. | DATA | F-06 |
| BR-29 | Mục sổ hiện diện có TTL `FHIR_WS_PRESENCE_TTL` (30s), được gia hạn mỗi TTL/3 chừng nào còn socket; hết socket thì hủy đăng ký và gỡ mục ở vòng dọn kế tiếp; tắt êm gỡ mọi mục ngay; gateway chết thì mục tự hết hạn. | OPS | F-06, F-08 |
| BR-30 | Thông điệp kênh không phải JSON hoặc có `sub` lệch kênh bị bỏ và đếm `Malformed`; sự kiện không bền (at-most-once): mất khi gateway khởi động lại hoặc Redis đứt, không phát lại sự kiện cũ cho socket mới. | DATA | F-06 |

### Cấu hình, tắt êm và probe (BR-31..35)

| Mã | Luật | Loại | F |
|---|---|---|---|
| BR-31 | Gateway từ chối khởi động (thoát khác 0, nêu tên biến, gom hết lỗi một lượt) khi khóa ký thiếu hoặc ngắn hơn 32 ký tự, khóa cũ ngắn hoặc quá 3, `REDIS_URL` không tường minh ở staging/production, các giới hạn <= 0 hoặc âm, `FHIR_WS_PRESENCE_TTL` < 3s, `FHIR_WS_CLOSE_TIMEOUT` > `SHUTDOWN_TIMEOUT`, hoặc CIDR proxy sai. | SEC | F-07 |
| BR-32 | Khóa ký của gateway phải trùng khóa của bên cấp (không có giá trị ngẫu nhiên thay thế); xoay khóa theo thứ tự: đặt khóa mới ở mọi gateway với khóa cũ ở `FHIR_WS_SIGNING_KEY_PREVIOUS`, đổi bên cấp, bỏ khóa cũ sau một hạn token. | SEC | F-07, F-03 |
| BR-33 | Tắt êm theo thứ tự: `/health/ready` 503, chờ `SHUTDOWN_DRAIN_DELAY`, upgrade mới 503, mọi socket nhận 1001, Feed gỡ sổ hiện diện và hủy đăng ký, đóng Redis, thoát 0. | OPS | F-08 |
| BR-34 | Redis chết không làm gateway thoát và không ngắt socket đã bind: `/health/ready` 503 với `checks.redis=false`, `/health/live` 200, bind mới đóng 1013; Redis lên lại thì readiness tự về 200. | OPS | F-08, F-06 |
| BR-35 | `/healthz`, `/health/live`, `/health/startup` luôn trả 200 `{"status":"ok"}` và không bị hạn mức; `/health/ready` trả 200 chỉ khi Redis ping được trong `READINESS_TIMEOUT` và gateway chưa bắt đầu tắt. | OPS | F-08 |

---

### Phần B

### Kênh và đăng ký (BR-36..BR-45)

| Mã | Luật | Loại | F |
|---|---|---|---|
| BR-36 | `hub.channel.type` chỉ được là `websocket` (so khớp chính xác, phân biệt hoa thường): `webhook`, `rest-hook`, `email`, `WebSocket` bị 400 `Invalid hub.channel.type`; rỗng bị 400 `Missing hub.channel.type`. | BM | F-09 |
| BR-37 | Hub không bao giờ gọi mạng ra ngoài: `hub.callback` và `hub.secret` được bỏ qua, máy chủ callback của client nhận 0 yêu cầu kể cả khi kênh `websocket` hợp lệ đi kèm `hub.callback`. | SEC | F-09 |
| BR-38 | Đăng ký/hủy kiểm theo thứ tự: thiếu `hub.channel.type`, thiếu `hub.mode`, thiếu `hub.topic`; sau đó `hub.channel.type`, `hub.mode` (`subscribe` hoặc `unsubscribe`), `hub.topic` hợp lệ; chỉ sau đó mới tách nhánh hủy hoặc đăng ký. | BM | F-09, F-10 |
| BR-39 | `hub.events` tách theo dấu phẩy, bỏ khoảng trắng quanh tên, bỏ phần rỗng; mọi tên phải thuộc 14 sự kiện được hỗ trợ (so khớp không phân biệt hoa thường), một tên sai thì cả yêu cầu bị 400 `Invalid hub.events`; `Patient-open Patient-close` (cách bằng dấu cách) là MỘT tên nên bị từ chối. | BM | F-09 |
| BR-40 | Topic hợp lệ khi không rỗng, không quá 256 byte và không chứa ký tự điều khiển (dưới 0x20 hoặc 0x7f); topic do client tự chọn nên phải qua kiểm này trước khi vào khóa, kênh và log. | DATA | F-09, F-13 |
| BR-41 | Endpoint là UUID v4 sinh từ `crypto/rand` (122 bit ngẫu nhiên), mỗi lần đăng ký sinh endpoint mới và endpoint không chứa topic; quyền nối websocket chỉ dựa vào việc biết endpoint này. | SEC | F-09 |
| BR-42 | Đăng ký lưu ở Redis khóa theo endpoint với `{projectId, topic, events, version, userSubject, expiresAt}`, TTL bằng `FHIRCAST_LEASE` (mặc định 1 giờ, tối đa 24 giờ); việc đăng ký không có cách gia hạn, hết hạn thì phải đăng ký lại. | DATA | F-09 |
| BR-43 | Hủy chỉ thành công khi endpoint tồn tại, cùng project, cùng topic, và (khi đăng ký và token đều có subject) cùng người; mọi trường hợp còn lại, kể cả đã hủy, trả cùng một thông điệp 400 `Invalid endpoint` để không lộ endpoint có tồn tại hay của ai. | SEC | F-10 |
| BR-44 | Khi hủy, endpoint lấy từ `hub.channel.endpoint` (đoạn cuối của URL, bỏ query, fragment và dấu `/` thừa); chỉ khi trường đó rỗng mới dùng trường cũ `endpoint`; không có cả hai thì 400 `Missing endpoint`. | BM | F-10 |
| BR-45 | Hủy xóa đăng ký rồi phát `denied` (`Subscriber unsubscribed from topic`) gắn `target` là endpoint đó: chỉ socket đó nhận và bị đóng 1000; các đăng ký khác của cùng topic không bị ảnh hưởng; phản hồi là 202 gương lại URL đã cấp. | BM | F-10, F-15 |

### Xác thực và phân quyền (BR-46..BR-51)

| Mã | Luật | Loại | F |
|---|---|---|---|
| BR-46 | Mọi route Hub trừ `.well-known/fhircast-configuration` đòi Bearer token hợp lệ; thiếu, sai scheme, sai chữ ký, sai `iss` hoặc `aud`, hết hạn, thiếu `sub` đều là 401 `Unauthorized` (mã `login`) kèm `WWW-Authenticate: Bearer realm="fhircast"`. | SEC | F-14 |
| BR-47 | Token hợp lệ nhưng không có claim project (mặc định `fhir_project`) bị 403 `Forbidden`, kể cả khi `FHIRCAST_REQUIRE_SCOPE=false`; không có project thì không có cách tách dữ liệu giữa tenant. | SEC | F-14 |
| BR-48 | Khi `FHIRCAST_REQUIRE_SCOPE=true` (mặc định), token phải có ít nhất một scope bắt đầu bằng `fhircast/`; `fhircastx`, `xfhircast/*`, `fhir.write` hay `fhir.admin` không thay thế được; thiếu thì 403 `Forbidden`. | SEC | F-14 |
| BR-49 | Hub không đòi và không dùng quyền `fhir.*` vì không đụng dữ liệu FHIR; quyền duy nhất cần là scope `fhircast/*` (khi bắt buộc) cộng project. | SEC | F-14 |
| BR-50 | Khóa kiểm token là JWKS (RS256/384/512, PS256/384/512, ES256/384/512, EdDSA) hoặc một khóa HS256 chia sẻ, đúng một trong hai; ở staging/production chỉ JWKS qua https và `FHIRCAST_REQUIRE_SCOPE=false` bị cấm; thuật toán ngoài danh sách tương ứng bị từ chối. | SEC | F-14, F-16 |
| BR-51 | Lỗi xác thực không phải token sai và không phải thiếu quyền chỉ trả 500 `Internal server error` (mã `exception`), chi tiết nội bộ chỉ ở log. | SEC | F-14 |

### Đổi ngữ cảnh và kiểm sự kiện (BR-52..BR-61)

| Mã | Luật | Loại | F |
|---|---|---|---|
| BR-52 | Thông điệp đổi ngữ cảnh kiểm theo thứ tự `id`, `timestamp`, `event`, `event["hub.topic"]`, `event["hub.event"]`, `context` (có mặt), `context` (mảng đối tượng), topic hợp lệ, tên sự kiện thuộc 14 sự kiện; thông điệp lỗi giữ đúng chuỗi của Medplum. | BM | F-11 |
| BR-53 | Ở `POST {v}` và `POST {v}/`: thân có khóa `id` hoặc `event` khác null là đổi ngữ cảnh, ngược lại là đăng ký/hủy; kiểu nội dung không phải JSON hoặc form coi như thân rỗng (rồi lỗi thiếu trường). | BM | F-09, F-11 |
| BR-54 | `POST {v}/{topic}` từ chối 400 `Topic mismatch` khi topic của event khác topic ở đường dẫn (đã giải mã đúng một lần, nên topic chứa `/`, `%`, dấu cách vẫn khớp); chỉ khi `FHIRCAST_ALLOW_TOPIC_MISMATCH=true` mới dùng topic của event. | SEC | F-11 |
| BR-55 | Hub không sửa payload của người gọi; thông điệp phát đi là bản sao có thêm `context.versionId` (open, update) và `context.priorVersionId` (update); phản hồi 202 là `{"success": true, "event": <thông điệp đã phát>}`. | DATA | F-11 |
| BR-56 | Phân loại sự kiện không phân biệt hoa thường: `*-open` là open, `*-close` là close, đúng `DiagnosticReport-update` là update; `*-select`, `userlogout`, `userhibernate`, `heartbeat`, `syncerror` chỉ được phát, không đổi trạng thái lưu. | BM | F-11 |
| BR-57 | Open ghi vô điều kiện ngữ cảnh mới có `context.versionId` mới, loại ngữ cảnh lấy từ phần trước dấu `-` đầu của tên sự kiện (Patient, ImagingStudy, Encounter, DiagnosticReport); `DiagnosticReport-open` thêm một Bundle `collection` rỗng ở khóa `content`. | BM | F-11, F-12 |
| BR-58 | Close luôn xóa ngữ cảnh hiện tại của topic mà không kiểm đúng tài nguyên; nếu context có tài nguyên `report` có `id` thì xóa luôn ngữ cảnh đã cất của báo cáo đó. | BM | F-11 |
| BR-59 | Khi ngữ cảnh hiện tại là DiagnosticReport, mọi open tiếp theo cất nó theo id báo cáo (tối đa 64 ngữ cảnh cất mỗi topic, quá thì 400 `Too many stored contexts for this topic`); mở lại báo cáo đã cất khôi phục nguyên ngữ cảnh và giữ `context.versionId` cũ. | DATA | F-11, F-12 |
| BR-60 | `DiagnosticReport-update` chỉ hợp lệ khi đang mở một DiagnosticReport và `event["context.versionId"]` bằng phiên bản hiện tại; update bundle chỉ nhận `PUT` và `DELETE`, tối đa `FHIRCAST_MAX_UPDATE_ENTRIES` (200) entry, và `DELETE` không được xóa tài nguyên thuộc ngữ cảnh mở ban đầu. | DATA | F-12 |
| BR-61 | Update ghi bằng so sánh-rồi-ghi (Redis WATCH/MULTI/EXEC, thử tối đa 8 lần): hai update đồng thời trên cùng một phiên bản thì đúng một bên thắng, bên kia nhận 409 `Context for this topic changed while the update was being applied, it is no longer at version '<prior>'`; lỗi giữa chừng khi áp bundle không lưu gì. | DATA | F-12 |

### Ngữ cảnh lưu, cô lập, hạn và kích thước (BR-62..BR-66)

| Mã | Luật | Loại | F |
|---|---|---|---|
| BR-62 | Ranh giới quyền duy nhất của Hub là project của token: project nằm trong khóa Redis của ngữ cảnh và trong tên kênh pub/sub (`fhircast:ch:<project>:<băm topic>`), nên cùng một chuỗi topic ở hai project là hai ngữ cảnh và hai kênh riêng; trong cùng project, mọi người có scope hợp lệ đều đọc, ghi và đăng ký được bất kỳ topic nào mà họ biết tên (Hub không kiểm topic thuộc về ai). | SEC | F-11, F-13, F-16 |
| BR-63 | Topic đi vào khóa Redis và tên kênh dưới dạng băm SHA-256 mã hóa base64url, nên topic chứa `:` hay rất dài không làm lệch khóa. | DATA | F-13, F-16 |
| BR-64 | Mọi khóa Redis của Hub có hạn: đăng ký theo lease, ngữ cảnh hiện tại và ngữ cảnh đã cất theo `FHIRCAST_CONTEXT_TTL` (24 giờ, phải lớn hơn 0), topic người dùng theo lease; ghi so sánh-rồi-ghi giữ nguyên hạn (không để dữ liệu y tế tồn vô hạn). | DATA | F-12, F-13, F-16 |
| BR-65 | Trần kích thước: thân HTTP tối đa `FHIRCAST_MAX_BODY_BYTES` (1 MiB) quá thì 413 `Request body too large`; một ngữ cảnh mã hóa tối đa `FHIRCAST_MAX_CONTEXT_BYTES` (256 KiB) quá thì 400 `Context exceeds the maximum size`; update bundle tối đa 200 entry; topic tối đa 256 byte. | DATA | F-09, F-11, F-12 |
| BR-66 | `GET {v}/{topic}` trả ngữ cảnh của project trong token: STU2 là mảng `context` (hoặc `[]`), STU3 là `{context.type, context, context.versionId}` (hoặc `{"context.type":"","context":[]}`); topic không hợp lệ 400 `Invalid hub.topic`; `.well-known` công khai, báo `webhookSupport: false`, `websocketSupport: true`, 14 sự kiện và `getCurrentSupport` chỉ ở STU3. | BM | F-13 |

### Websocket FHIRcast (BR-67..BR-74)

| Mã | Luật | Loại | F |
|---|---|---|---|
| BR-67 | Nối websocket không cần Bearer: quyền nối là biết endpoint (bí mật 122 bit, BR-41); endpoint không có, hết hạn, hỏng hay rỗng đều nhận cùng khung `denied` với `hub.reason` `invalid endpoint` rồi đóng 1000, không phân biệt nguyên nhân. | SEC | F-15 |
| BR-68 | Khung đầu tiên luôn là xác nhận (STU3: `hub.mode`, `hub.topic`, `hub.events`, `hub.lease_seconds`; STU2 thêm `hub.callback`, `hub.channel`, `hub.secret`, `hub.subscriber` rỗng); Hub nghe kênh của topic trước khi gửi xác nhận nên sự kiện phát ngay sau không lọt, và sự kiện tới trong lúc ghi xác nhận chỉ được gửi sau xác nhận. | BM | F-15 |
| BR-69 | Mỗi socket chỉ nhận sự kiện nằm trong `hub.events` đã đăng ký (không phân biệt hoa thường); `heartbeat` và `syncerror` luôn tới; thông điệp điều khiển Hub không có `hub.event` (như `denied`) luôn tới; thông điệp có `target` của endpoint khác bị bỏ, và `target` không bao giờ lên dây. | SEC | F-15 |
| BR-70 | Mỗi socket tự phát heartbeat cục bộ (không qua Redis nên không nhân đôi giữa các instance) mỗi `FHIRCAST_HEARTBEAT` (mặc định 10 giây) với `context` `[{"key":"period","decimal":"<giây>"}]` và `id` mới. | BM | F-15 |
| BR-71 | Lease là hạn thật của socket: tới `expiresAt` Hub gửi `denied` `lease expired` rồi đóng 1000; subscriber phải đăng ký lại; nối vào endpoint đã quá hạn bị coi là `invalid endpoint`. | BM | F-15 |
| BR-72 | Hạn mức nối (theo instance): tối đa `FHIRCAST_MAX_SOCKETS_PER_ENDPOINT` (4) socket cho một endpoint, socket vượt nhận `denied` `too many connections for this endpoint` rồi đóng 1008; vượt `FHIRCAST_MAX_CONNECTIONS` (1000) thì HTTP 503 trước khi nâng cấp; vượt `FHIR_WS_MAX_CONNS_PER_IP` thì HTTP 429 kèm `Retry-After: 5`; route chịu cùng giới hạn tốc độ `FHIR_WS_RATE_LIMIT` với websocket Subscription. | OPS | F-15 |
| BR-73 | Origin của yêu cầu nâng cấp được kiểm theo `FHIR_WS_ALLOWED_ORIGINS` (mặc định chỉ cùng host): Origin không được phép bị 403; yêu cầu không có Origin (client không phải trình duyệt) vẫn qua. | SEC | F-15 |
| BR-74 | Hàng đợi gửi mỗi socket có 64 khung, đầy thì đóng 1013 (client quá chậm); Redis lỗi khi đọc đăng ký hay nghe kênh thì đóng 1013 (client thử lại); tin client gửi lên không có tác dụng và tin hỏng không làm đóng socket; khung lớn hơn 64 KiB bị đóng 1009. | OPS | F-15 |

### Cấu hình (BR-75)

| Mã | Luật | Loại | F |
|---|---|---|---|
| BR-75 | Hub tắt mặc định (`FHIRCAST_ENABLED`; tắt thì mọi route Hub và `/ws/fhircast` là 404); cấu hình sai bị từ chối khi khởi động: đặt `FHIRCAST_*` (public URL, issuer, audience, JWKS, HMAC) mà chưa bật; thiếu public URL; public URL không phải `ws://` hoặc `wss://` có host và không đường dẫn, hoặc không phải `wss://` ở staging/production; thiếu issuer hoặc audience; không đúng một trong JWKS và HMAC; HMAC ngắn hơn 32 ký tự; HMAC hoặc JWKS không https hoặc tắt scope ở staging/production; lease ngoài (0, 24h]; TTL ngữ cảnh không dương; các trần và hạn mức không dương. | OPS | F-16 |

## 4.2 Kịch bản chấp nhận

| Mã | Given When Then | BR |
|---|---|---|
| AT-01 | Given `FHIR_WS_ALLOWED_ORIGINS=app.example.org`, When mở kết nối với `Origin` `https://evil.example.com`, `https://app.example.org` và không có `Origin`, Then lạ nhận HTTP 403 còn hai trường hợp kia nâng cấp được. | BR-04 |
| AT-02 | Given `ENVIRONMENT=production` và `FHIR_WS_ALLOWED_ORIGINS=*` (hoặc giá trị là URL), When khởi động gateway, Then thoát khác 0 và thông báo nêu `FHIR_WS_ALLOWED_ORIGINS`. | BR-05, BR-31 |
| AT-03 | Given `FHIR_WS_MAX_CONNECTIONS=1` và một kết nối đang mở, When mở kết nối thứ hai (kể cả nhiều upgrade đồng thời), Then nhận 503 và số kết nối không vượt 1; đóng kết nối đầu thì mở lại được. | BR-02 |
| AT-04 | Given `FHIR_WS_MAX_CONNS_PER_IP=1`, When một IP mở kết nối thứ hai với `X-Forwarded-For` giả, Then nhận 429; chỉ khi `FHIR_WS_TRUSTED_PROXIES` khai proxy thì mỗi IP thật có bộ đếm riêng. | BR-03 |
| AT-05 | Given `FHIR_WS_RATE_LIMIT` nhỏ và burst 2, When mở websocket lần thứ ba liên tiếp từ cùng IP, Then nhận 429 kèm `Retry-After` còn `/healthz` và `/health/ready` không bị 429. | BR-07 |
| AT-06 | Given kết nối đang mở, When gửi một khung lớn hơn 64 KiB, Then kết nối bị đóng 1009. | BR-06 |
| AT-07 | Given kết nối mới, When không gửi bind trong 10 giây (hoặc chỉ gửi `ping`, hoặc gửi thông điệp khác bind), Then kết nối bị đóng 1008. | BR-08 |
| AT-08 | Given token ký bằng khóa lạ, token bị sửa chữ ký, hoặc token ký không có tiền tố miền, When bind, Then đóng 1008 và không có binding. | BR-09 |
| AT-09 | Given token đã quá hạn, hoặc token mang access token gốc đã hết hạn, When bind, Then đóng 1008 và không có binding. | BR-10, BR-12 |
| AT-10 | Given gateway đặt khóa mới cùng khóa cũ ở `FHIR_WS_SIGNING_KEY_PREVIOUS`, When bind bằng token ký khóa cũ rồi khóa mới, Then cả hai được nhận; bỏ khóa cũ thì token cũ bị 1008. | BR-09, BR-32 |
| AT-11 | Given token có `v` lớn hơn phiên bản hỗ trợ, When bind, Then đóng 1008; token không có `v` vẫn bind được. | BR-11 |
| AT-12 | Given hai kết nối cùng bind một Subscription, When kết nối A gửi unbind (token có thể đã hết hạn), Then chỉ A thôi nhận, B vẫn nhận; token sai chữ ký hoặc unbind trước bind bị đóng 1008. | BR-19 |
| AT-13 | Given `FHIR_WS_MAX_BINDINGS_PER_USER=2`, When cùng một người bind lần thứ ba trên kết nối khác, Then đóng 1008 `User has exceeded allotted maximum number of concurrent WebSocket subscriptions: 2`, người khác bind được, và sau unbind hoặc đóng kết nối thì bind lại được. | BR-14 |
| AT-14 | Given một kết nối đã đủ trần Subscription, When bind thêm Subscription mới, Then đóng 1008 `quá nhiều Subscription trên một kết nối`; bind lại Subscription đã có không bị từ chối. | BR-13, BR-15 |
| AT-15 | Given ba người bind cùng Subscription: một không có `r` cho Observation, một có, một AccessPolicy `status=final`, When ghi Observation `preliminary` rồi `final` và xóa nó, Then người không có `r` không nhận gì kể cả thông báo xóa, người có `r` nhận cả hai, người theo policy chỉ nhận cái `final`. | BR-22, BR-25 |
| AT-16 | Given binding có access token hết hạn sau 2 phút, When đồng hồ qua hạn rồi có sự kiện mới, Then binding bị gỡ, không gửi tin, `Denied` tăng. | BR-23 |
| AT-17 | Given Redis không dùng được, When bind bằng token hợp lệ, Then đóng 1013, không có binding và không có mục sổ hiện diện; khi Redis tốt, Feed đăng ký kênh trước rồi mới ghi sổ. | BR-17, BR-28, BR-34 |
| AT-18 | Given socket đã bind và sổ hiện diện có mục, When gateway nhận SIGTERM, Then `/health/ready` trả 503 ngay, socket nhận 1001, mục sổ được gỡ trước khi tiến trình thoát, mã thoát 0. | BR-33, BR-29 |

---

| Mã | Kịch bản (Given When Then) | Luật |
|---|---|---|
| AT-19 | Given token hợp lệ. When `POST /fhircast/STU3` với `hub.channel.type` là `webhook`, `rest-hook`, `email` hoặc `WebSocket`. Then 400 `Invalid hub.channel.type` và không có đăng ký nào được tạo. | BR-36 |
| AT-20 | Given một máy chủ HTTP thử làm callback. When đăng ký với `hub.callback` trỏ vào nó (cả kênh sai lẫn kênh `websocket` đúng). Then máy chủ thử nhận 0 yêu cầu. | BR-37 |
| AT-21 | Given đăng ký với `hub.events=Patient-open,Sự-kiện-lạ`. When gửi. Then 400 `Invalid hub.events`; và `Patient-open Patient-close` (cách bằng dấu cách) cũng bị 400. | BR-39 |
| AT-22 | Given topic dài 257 byte hoặc chứa ký tự `\n`. When đăng ký, đọc ngữ cảnh. Then 400 `Invalid hub.topic`. | BR-40 |
| AT-23 | Given hai lần đăng ký liên tiếp cùng người, cùng topic. When so hai `hub.channel.endpoint`. Then khác nhau, đoạn cuối là UUID v4, không chứa topic. | BR-41 |
| AT-24 | Given một đăng ký của alice trong project p, topic t. When bob (cùng project) hủy; hoặc alice hủy với topic khác; hoặc người ở project khác hủy; hoặc hủy endpoint không tồn tại hoặc đã hủy. Then luôn 400 `Invalid endpoint` và đăng ký của alice còn nguyên. | BR-43 |
| AT-25 | Given hai đăng ký a và b cùng topic, cả hai đang nối. When alice hủy a. Then socket a nhận `denied` `Subscriber unsubscribed from topic` rồi đóng 1000; socket b không nhận gì và vẫn mở. | BR-45 |
| AT-26 | Given không có header `Authorization` hoặc token rác. When gọi `GET /hub/t`. Then 401 `Unauthorized` kèm `WWW-Authenticate: Bearer realm="fhircast"`; riêng `.well-known` vẫn 200. | BR-46 |
| AT-27 | Given token đúng chữ ký nhưng (a) `fhir_project` rỗng (kể cả khi `FHIRCAST_REQUIRE_SCOPE=false`) hoặc (b) có project nhưng chỉ scope `fhir.write`, `fhir.admin`, `fhircastx` hoặc `xfhircast/*`. When gọi `GET /hub/t`. Then 403 `Forbidden`; còn scope `fhircast/patient-open` hoặc `fhircast/*` thì 200. | BR-47, BR-48 |
| AT-28 | Given token ký bằng khóa khác khóa đã cấu hình. When gọi Hub. Then 401. | BR-50 |
| AT-29 | Given `POST /hub/path-topic` thân có `event["hub.topic"]` là `event-topic`. When gửi. Then 400 `Topic mismatch` và ngữ cảnh của `event-topic` không đổi; với `FHIRCAST_ALLOW_TOPIC_MISMATCH=true` thì 202 và ghi vào `event-topic`. | BR-54 |
| AT-30 | Given DiagnosticReport đang mở ở phiên bản v1. When gửi update với `context.versionId` sai hoặc rỗng. Then 400 `Expected event.context.versionId to be 'v1', received ...` và ngữ cảnh không đổi. | BR-60 |
| AT-31 | Given DiagnosticReport ở v1. When hai update khác nhau cùng mang v1 gửi đồng thời. Then đúng một cái 202, cái kia 409 `Context for this topic changed while the update was being applied, it is no longer at version 'v1'`. | BR-61 |
| AT-32 | Given project p có ngữ cảnh Patient ở topic t và một socket đang nghe. When token của project khác đọc topic t và phát sự kiện ở topic t. Then project khác thấy `context.type` rỗng; socket của p không nhận sự kiện đó, và dữ liệu của p không đổi. | BR-62 |
| AT-33 | Given `FHIRCAST_MAX_BODY_BYTES` nhỏ. When gửi thân vượt trần (JSON hoặc form); gửi context mã hóa lớn hơn 256 KiB. Then 413 `Request body too large`; 400 `Context exceeds the maximum size`. | BR-65 |
| AT-34 | Given topic có ngữ cảnh Patient và topic chưa có gì. When `GET /fhircast/STU2/{topic}` và `GET /fhircast/STU3/{topic}`. Then STU2 trả mảng (`[]` khi không có), STU3 trả đối tượng có `context.type`, `context`, `context.versionId` (hoặc `{"context.type":"","context":[]}`). | BR-66 |
| AT-35 | Given không có Bearer. When nối `GET /ws/fhircast/forged`. Then nhận `denied` `invalid endpoint` rồi đóng 1000; không có đường nào nối được với endpoint đã hủy hoặc hết hạn. | BR-67 |
| AT-36 | Given đăng ký chỉ `Patient-open`, socket đã nối. When phát `Encounter-open`, rồi `Patient-open`, rồi heartbeat. Then socket không nhận `Encounter-open`, nhận `Patient-open`; heartbeat vẫn tới; khung đầu tiên luôn là xác nhận. | BR-68, BR-69 |
| AT-37 | Given `FHIRCAST_LEASE` ngắn. When hết lease với socket đang nối; rồi nối lại vào endpoint đó. Then nhận `denied` `lease expired` rồi đóng 1000; lần nối lại nhận `invalid endpoint`. | BR-71 |
| AT-38 | Given `FHIRCAST_MAX_SOCKETS_PER_ENDPOINT=1` với một socket đang nối. When mở socket thứ hai vào cùng endpoint. Then nhận `denied` `too many connections for this endpoint` rồi đóng 1008; socket đầu không bị ảnh hưởng; với `FHIRCAST_MAX_CONNECTIONS=1` thì nâng cấp mới bị 503. | BR-72 |
| AT-39 | Given `FHIR_WS_ALLOWED_ORIGINS=app.example.org`. When nâng cấp với `Origin: https://evil.example.com`; rồi nâng cấp không có Origin. Then lần đầu 403, lần sau qua. | BR-73 |
| AT-40 | Given `FHIRCAST_ENABLED` tắt, hoặc bật nhưng thiếu `FHIRCAST_PUBLIC_URL`, hoặc đặt `FHIRCAST_JWKS_URL` mà chưa bật. When khởi động. Then tắt thì mọi route Hub là 404; hai trường hợp sau gateway từ chối khởi động với lỗi chứa `FHIRCAST_PUBLIC_URL: bắt buộc` hoặc `FHIRCAST_ENABLED chưa bật`. | BR-75 |

## 4.3 Truy vết luật tới test

| Nhóm luật | Test |
|---|---|
| BR-01, BR-06 (đóng, 1009, 1011) | `pkg/wsgroup/wsgroup_test.go`: `TestCloseSends1001AndRefusesNewUpgrades`, `TestOversizedFrameClosesWith1009`, `TestHandlerPanicClosesWith1011`; `pkg/fhirsub/ws_test.go`: `TestWebSocketLimitsAndShutdown` |
| BR-02 (tổng kết nối) | `pkg/wsgroup/wsgroup_test.go`: `TestMaxConnsRejectsWith503AndLeaksNothing`, `TestConcurrentUpgradesNeverExceedMaxConns`; `apps/ws-gateway/tests/integration/container_test.go`: `TestGatewayMaxConnectionsAnswers503` |
| BR-03 (theo IP) | `pkg/wsgroup/wsgroup_test.go`: `TestMaxConnsPerIP`, `TestMaxConnsPerIPDefaultsToRemoteAddr`; `apps/ws-gateway/tests/integration/container_test.go`: `TestGatewayPerIPLimitAndTrustedProxies` |
| BR-04, BR-05 (Origin) | `pkg/wsgroup/wsgroup_test.go`: `TestOriginPolicy`; `apps/ws-gateway/tests/integration/container_test.go`: `TestGatewayOriginPolicy`; `apps/ws-gateway/internal/config/config_test.go`: `TestValidationErrors` |
| BR-07 (hạn mức tốc độ) | `apps/ws-gateway/internal/presentation/httpapi/httpapi_test.go`: `TestRateLimitAppliesToWebSocketOnly`, `TestRateLimitIdentityFollowsIPExtractor`; `apps/ws-gateway/internal/config/config_test.go`: `TestRateLimitDefaults` |
| BR-08, BR-18 (bind trễ, khung lạ) | `pkg/fhirsub/ws_test.go`: `TestWebSocketRejectsBadBinding`; `pkg/fhirsub/ws_bind_test.go`: `TestWebSocketUnbindRejectsBadTokensAndEarlyUnbind` (chưa có test riêng cho khung nhị phân 1003 và cho `ping` không dừng đồng hồ bind) |
| BR-09..11, BR-32 (chữ ký, hạn, phiên bản, xoay khóa) | `pkg/fhirsub/ws_token_test.go`: `TestNewTokensValidatesKeys`, `TestIssuedTokenCarriesVersionAndExpiry`, `TestHubAcceptsTokensFromADifferentIssuerSharingTheKey`, `TestSigningKeyRotation`, `TestHubPreviousKeysViaConfig`, `TestTokenVersioning`, `TestTokenSignatureIsDomainSeparated`; `apps/ws-gateway/tests/integration/container_test.go`: `TestGatewayRejectsTokensFromAnotherKeyAndHonorsRotation`; `apps/ws-gateway/tests/e2e/gateway_test.go`: `TestSubscriptionSocketRejectsForgedToken` |
| BR-12, BR-23 (access token hết hạn) | `pkg/fhirsub/ws_authz_test.go`: `TestWebSocketRejectsBindWithExpiredAccessToken`, `TestWebSocketBindingStopsWhenAccessTokenExpires` |
| BR-13..15 (hạn mức bind, bind lại) | `pkg/fhirsub/ws_test.go`: `TestWebSocketLimitsAndShutdown`; `pkg/fhirsub/ws_bind_test.go`: `TestWebSocketBindQuotaPerUser`, `TestWebSocketBindQuotaDoesNotApplyWithoutPrincipal`; `pkg/fhirsub/ws_authz_test.go`: `TestWebSocketRebindReplacesPrincipal` |
| BR-16 (tin token) | `pkg/fhirsub/ws_remote_test.go`: `TestHubWithoutTokenOnlyStillRequiresLookup`, `TestRemoteSubscriptionDeletedAfterBindStopsDelivery` |
| BR-17, BR-28 (Redis lỗi lúc bind, thứ tự) | `pkg/fhirsub/ws_remote_test.go`: `TestRemoteFeedSubscribesBeforeItMarksPresence`, `TestRemoteBindFailsWithTryAgainWhenChannelSubscriptionFails`, `TestRemoteBindFailureAfterSubscribeCancelsTheSubscription`; `apps/ws-gateway/tests/integration/container_test.go`: `TestGatewayStartsWithoutRedisButRefusesBadConfig` |
| BR-19, BR-20 (unbind, ping) | `pkg/fhirsub/ws_bind_test.go`: `TestWebSocketUnbindFromToken`, `TestWebSocketUnbindIsScopedToTheConnectionAndAcceptsExpiredToken`, `TestWebSocketUnbindRejectsBadTokensAndEarlyUnbind` |
| BR-21, BR-26 (heartbeat, bộ đếm, chậm) | `pkg/fhirsub/ws_test.go`: `TestWebSocketHandshakeAndNotifications`, `TestWebSocketHeartbeat`, `TestWebSocketSlowConsumerIsDropped`, `TestWebSocketManyConnectionsAndSubscriptions` |
| BR-22, BR-25 (quyền từng người nhận) | `pkg/fhirsub/ws_authz_test.go`: `TestWebSocketDeliveryChecksReadPermissionPerRecipient`, `TestWebSocketDeliveryHonorsAccessPolicy`; `pkg/fhirsub/ws_bind_test.go`: `TestWebSocketNeverDeliversDeniedTypeEvenWithWildcard`; `pkg/fhirsub/ws_remote_test.go`: `TestRemoteDeliveryStillChecksEachRecipient`; `pkg/fhirauth/snapshot_test.go`: `TestCanReadResource`, `TestSnapshotRoundTrip` |
| BR-24 (hiddenFields) | Không có test nào gọi `RedactDoc` (xem `## Lệch`) |
| BR-27, BR-29, BR-30 (Feed, sổ, at-most-once) | `pkg/fhirsub/ws_remote_test.go`: `TestRemoteDeliveryEndToEnd`, `TestRemoteNothingIsPublishedWhileNobodyListens`, `TestRemoteGatewayOnlyListensToBoundSubscriptions`, `TestRemoteTwoSocketsShareOneChannelSubscription`, `TestRemoteFeedReleasesSubscriptionWhenLastSocketCloses`, `TestRemoteFeedStopUnmarksImmediately`, `TestRemoteFeedRenewsPresence`, `TestRemotePresenceExpiresWhenGatewayDiesSilently`, `TestRemoteMalformedMessagesAreDropped`, `TestAttachRemoteValidation`; `pkg/fhirsub/ws_test.go`: `TestWebSocketAcrossInstancesViaRedis`; `pkg/fhirsub/ws_bind_test.go`: `TestManagerMatchesWebSocketSubscriptionOnlyWhileBound`; `apps/ws-gateway/tests/integration/container_test.go`: `TestGatewayDeliversEventsMatchedByFhirServer`; `apps/ws-gateway/tests/e2e/gateway_test.go`: `TestSubscriptionSocketDeliversEventsPublishedViaRedis` |
| BR-31 (từ chối khởi động) | `apps/ws-gateway/internal/config/config_test.go`: `TestDefaultsAreValidForLocal`, `TestValidationErrors`, `TestListHelpers`; `apps/ws-gateway/internal/config/docs_test.go`: `TestConfigDocIsUpToDate`; `apps/ws-gateway/tests/e2e/gateway_test.go`: `TestRefusesToStartOnBadConfiguration`; `apps/ws-gateway/tests/integration/container_test.go`: `TestGatewayStartsWithoutRedisButRefusesBadConfig` |
| BR-33 (tắt êm) | `pkg/wsgroup/wsgroup_test.go`: `TestCloseIsPromptWithoutConnectionsAndIdempotent`, `TestSilentClientIsCutAfterCloseTimeout`, `TestCloseReturnsContextErrorWhenHandlerStuck`; `apps/ws-gateway/tests/integration/container_test.go`: `TestGatewayStopClosesSocketsAndReleasesPresence`, `TestServeEndToEnd`; `apps/ws-gateway/tests/e2e/gateway_test.go`: `TestGracefulShutdownSendsGoingAwayAndExitsZero` |
| BR-34, BR-35 (probe, Redis chết) | `apps/ws-gateway/internal/presentation/httpapi/httpapi_test.go`: `TestHealthEndpoints`, `TestSecurityHeadersAreSet`; `apps/ws-gateway/tests/integration/container_test.go`: `TestGatewayReadiness`, `TestGatewayDrainingReportsNotReady`; `apps/ws-gateway/tests/e2e/gateway_test.go`: `TestProbesAndRedisOutage`, `TestImageIsHardenedAndHealthcheckWorks` |
| Metric (F-08) | `apps/ws-gateway/tests/integration/metrics_test.go`: `TestGatewayMetricsReportConnectionsBindingsAndEvents`, `TestGatewayMetricRedisUpFollowsRedis` |
| Tải và định cỡ | `apps/ws-gateway/tests/load/load_test.go`: `TestWSLoadFanOutAndMemory` (đo, không kiểm đúng sai) |
| Cô lập project ở chế độ nhúng (không phải gateway) | `pkg/fhirsub/ws_test.go`: `TestWebSocketOnlySeesOwnProject` |

---

| Nhóm luật | Test |
|---|---|
| BR-36, BR-37 (chỉ websocket, không gọi ra) | `apps/ws-gateway/internal/presentation/httpapi/fhircast_test.go`: `TestFhircastWebhookNeverCallsOut`, `TestFhircastSubscribeErrors`; `pkg/fhircast/hub_test.go`: `TestHubSubscribe` |
| BR-38..BR-42 (đăng ký, topic, endpoint, lease) | `pkg/fhircast/hub_test.go`: `TestHubSubscribe`, `TestHubSubscribeStoreFailure`; `pkg/fhircast/fhircast_test.go`: `TestParseEvents`, `TestSupportedEventsAreTheFourteen`, `TestTopicHashAndValidation`, `TestNewIDIsUUIDv4AndUnique`, `TestSubscriptionSetGetDelete`, `TestCorruptSubscriptionReadsAsNone`; `apps/ws-gateway/internal/presentation/httpapi/fhircast_test.go`: `TestFhircastSubscribeEveryPathAndEncoding`, `TestFhircastSubscribeStoreFailureIs500` |
| BR-43..BR-45 (hủy) | `pkg/fhircast/hub_test.go`: `TestHubUnsubscribe`; `pkg/fhircast/fhircast_test.go`: `TestExtractEndpoint`; `apps/ws-gateway/internal/presentation/httpapi/fhircast_test.go`: `TestFhircastUnsubscribeOverHTTP`; `pkg/fhircast/ws/ws_test.go`: `TestUnsubscribeDeniesOnlyThatSocket` |
| BR-46..BR-51 (xác thực) | `apps/ws-gateway/internal/bootstrap/fhircast_test.go`: `TestAuthenticator`; `apps/ws-gateway/internal/presentation/httpapi/fhircast_test.go`: `TestFhircastRequiresAuth`, `TestFhircastWellKnownIsPublic`; `apps/ws-gateway/tests/integration/fhircast_container_test.go`: `TestGatewayFhircastAuthorization`, `TestGatewayFhircastEndToEndOverRealJWT` |
| BR-52, BR-53, BR-55 (kiểm thông điệp, phân nhánh POST) | `pkg/fhircast/hub_test.go`: `TestHubChangeContextValidation`, `TestHubDoesNotMutateCallerPayload`; `apps/ws-gateway/internal/presentation/httpapi/fhircast_test.go`: `TestFhircastEventValidationOverHTTP`, `TestFhircastContextChangeAndGet` |
| BR-54 (topic ở đường dẫn) | `apps/ws-gateway/internal/presentation/httpapi/fhircast_test.go`: `TestFhircastTopicMismatchOnPath`, `TestFhircastTopicsWithSpecialCharacters` |
| BR-56..BR-59 (open, close, cất, khôi phục) | `pkg/fhircast/fhircast_test.go`: `TestEventCategory`, `TestExtractAnchorType`, `TestReportContexts`; `pkg/fhircast/hub_test.go`: `TestHubOpenCloseGet`, `TestHubDiagnosticReportOpenHasEmptyContentBundle`, `TestHubSuspendResume`, `TestHubOpenFailsWhenStoredDiagnosticReportIsBroken` |
| BR-60, BR-61 (update, phiên bản, tranh chấp) | `pkg/fhircast/hub_test.go`: `TestHubUpdate`, `TestHubUpdateNeedsOpenDiagnosticReport`, `TestHubUpdateEntryLimit`, `TestHubConcurrentUpdatesExactlyOneWins`; `pkg/fhircast/fhircast_test.go`: `TestCompareAndSetContext`, `TestCompareAndSetRaceExactlyOneWins` |
| BR-62, BR-63 (cô lập project, băm topic) | `pkg/fhircast/fhircast_test.go`: `TestContextIsolatedByProjectAndTopic`, `TestTopicHashAndValidation`; `pkg/fhircast/ws/ws_test.go`: `TestProjectIsolation`; `apps/ws-gateway/tests/e2e/gateway_test.go`: `TestFhircastOverHTTPAndWebSocket`; `apps/ws-gateway/tests/integration/fhircast_container_test.go`: `TestGatewayFhircastEndToEndOverRealJWT` |
| BR-64 (mọi khóa có hạn) | `pkg/fhircast/redis_test.go`: `TestRedisStoreEveryKeyHasTTL`, `TestRedisStoreCASKeepsTTL`, `TestRedisStoreExpiry`; `pkg/fhircast/fhircast_test.go`: `TestContextExpiresAndClose`, `TestUserTopicStableAndRefreshed` |
| BR-65 (trần kích thước) | `apps/ws-gateway/internal/presentation/httpapi/fhircast_test.go`: `TestFhircastBodyLimit`; `pkg/fhircast/fhircast_test.go`: `TestContextSizeLimit`; `pkg/fhircast/hub_test.go`: `TestHubTooLargeContextIs400`, `TestHubUpdateEntryLimit` |
| BR-66 (GET ngữ cảnh, well-known) | `apps/ws-gateway/internal/presentation/httpapi/fhircast_test.go`: `TestFhircastWellKnownIsPublic`, `TestFhircastContextChangeAndGet`; `pkg/fhircast/fhircast_test.go`: `TestEmptyContextEncodesAsArray` |
| BR-67..BR-69 (websocket: endpoint, xác nhận, lọc) | `pkg/fhircast/ws/ws_test.go`: `TestInvalidEndpoint`, `TestConfirmationAndDelivery`, `TestConfirmationIsAlwaysFirst`, `TestOnlySubscribedEventsAndCaseInsensitive`; `pkg/fhircast/fhircast_test.go`: `TestMessageForSubscriber`, `TestTargetNeverReachesTheWire`, `TestConfirmationShapes`; `apps/ws-gateway/tests/integration/fhircast_container_test.go`: `TestGatewayFhircastWebSocketNeedsNoBearerButNeedsValidEndpoint`, `TestGatewayFhircastWebSocketAcrossInstances` |
| BR-70, BR-71 (heartbeat, lease) | `pkg/fhircast/ws/ws_test.go`: `TestHeartbeat`, `TestLeaseExpiryDeniesAndCloses`; `pkg/fhircast/fhircast_test.go`: `TestHeartbeatShape` |
| BR-72, BR-73 (hạn mức, Origin) | `pkg/fhircast/ws/ws_test.go`: `TestMaxSocketsPerEndpoint`, `TestOriginPolicy`; `apps/ws-gateway/tests/integration/fhircast_container_test.go`: `TestGatewayFhircastWebSocketHonorsSettings`; `apps/ws-gateway/internal/presentation/httpapi/fhircast_test.go`: `TestFhircastWebSocketIsRateLimited`, `TestFhircastWebSocketRoute` |
| BR-74 (độ bền, client chậm, Redis lỗi) | `pkg/fhircast/ws/ws_test.go`: `TestSlowClientIsClosed`, `TestInfrastructureFailuresCloseWith1013`, `TestMalformedMessagesAreIgnored`, `TestEverySocketReleasesItsBusSubscription`, `TestGracefulCloseSends1001`; `pkg/fhircast/redis_test.go`: `TestRedisBusCrossInstance`, `TestRedisBusSubscribeAfterCloseAndFailure` |
| BR-75 (cấu hình, bật/tắt) | `apps/ws-gateway/internal/config/fhircast_config_test.go`: `TestFhircastDisabledByDefaultAndDefaults`, `TestFhircastValidation`; `apps/ws-gateway/internal/presentation/httpapi/fhircast_test.go`: `TestFhircastDisabledAndMisconfigured`; `apps/ws-gateway/tests/integration/fhircast_container_test.go`: `TestGatewayFhircastDisabledByDefault`, `TestGatewayFhircastStopIsClean`, `TestGatewayFhircastSocketsAreClosedGracefullyOnStop` |
| Nhiều instance dùng chung trạng thái | `apps/ws-gateway/tests/integration/fhircast_container_test.go`: `TestGatewayFhircastTwoInstancesShareState`, `TestGatewayFhircastWebSocketAcrossInstances` |

## 4.4 Điểm lệch và rủi ro

### Phần A

Các mục đã kiểm trong mã trừ khi ghi "(chưa kiểm)".

1. **`eventsSinceSubscriptionStart` không phát hiện được mất sự kiện, và heartbeat không chứng minh đường nhận còn sống (mất dữ liệu, trung bình-cao).** Bộ đếm (`binding.events`) do Hub tăng cho sự kiện đã tới gateway và đạt kiểm quyền; sự kiện mất ở Redis, ở lúc gateway khởi động lại hay trước khi sổ gia hạn không để lại khoảng trống số. Bộ đếm còn về 1 khi kết nối mới. Heartbeat thì không có số và do writer cục bộ sinh ra, vẫn chạy khi Redis chết. Vì vậy câu "client dựa vào `heartbeat` và `eventsSinceSubscriptionStart` để phát hiện thiếu" ở `apps/ws-gateway/docs/api-contract.md` (mục 3), `runbook.md` (dòng 175) và chú thích `pkg/fhirsub/ws_remote.go` **lệch** với hành vi thật; client phải tự đối soát (đọc lại theo `_lastUpdated`).
2. **Redis chết không ngắt socket đã bind** (đúng thiết kế nhưng ảnh hưởng người dùng): socket vẫn nhận heartbeat bình thường, không có tín hiệu báo; sau khi Redis lên, sổ chỉ được ghi lại ở vòng dọn kế (tối đa 10 giây) và go-redis tự đăng ký lại kênh; việc giao sự kiện sau hồi phục chưa có test (`TestProbesAndRedisOutage` chỉ kiểm readiness) (chưa kiểm).
3. **Hết hạn access token chỉ được phát hiện khi có sự kiện** (`Hub.notify`): binding rảnh, đã quá hạn, vẫn ở trong `bySub`, chiếm hạn mức bind và đăng ký kênh, client không được báo; chỉ heartbeat của Subscription đó dừng sau khi gỡ. Không lộ dữ liệu vì gỡ trước khi gửi.
4. **Quyền chụp một lần và token là bearer** (thiết kế, ghi trong `pkg/fhirsub/ws.go`): thu hồi quyền hoặc xóa Subscription không có hiệu lực trên binding đã có; token dùng lại được trong 5 phút từ kết nối khác; xóa hoặc tắt Subscription không ngắt kết nối đang mở (chỉ thôi nhận).
5. **Gateway không đối chiếu project** (`claims.Project`) với project trong ảnh chụp quyền hay trong resource, và tin mọi thông điệp trên kênh Redis của Subscription (không ký): cách ly project dựa hoàn toàn vào `fhir-server` khớp và Redis là biên tin cậy. Chỉ có test cách ly ở chế độ nhúng (`TestWebSocketOnlySeesOwnProject`), không có test ở mức gateway (chưa kiểm).
6. **`hiddenFields` không có test** (bảo mật, trung bình): `RedactDoc` được gọi trong `Hub.notify` nhưng không test nào (`pkg/fhirauth`, `pkg/fhirsub`, gateway) phủ nó; chú thích `TestWebSocketNeverDeliversDeniedTypeEvenWithWildcard` nói `auth` dịch rule `hiddenFields` thành `deny` cả loại, nghĩa là lọc từng trường có thể chưa được dùng trong thực tế. Cũng không áp cho sự kiện xóa (vì không kèm resource) (chưa kiểm hành vi đầu-cuối).
7. **Mô tả "tin đầu tiên phải là bind, nếu không bị đóng 1008"** ở `api-contract.md` chưa chính xác: `ping` trước bind được trả `pong` và không bị đóng ngay, nhưng đồng hồ 10 giây không dừng nên kết nối vẫn bị đóng 1008 sau 10 giây; thông điệp lạ khác đóng ngay.
8. **`Feed.ensure` giữ khóa trong lúc gọi Redis** (tối đa 2 giây mỗi lần): mọi bind (kể cả Subscription đã có đăng ký) và vòng dọn bị xếp hàng sau một lời gọi Redis chậm; Redis chậm làm bind chậm tuần tự (chưa kiểm).
9. **Cuộc đua nhỏ giữa `handshake` và sự kiện đầu** (chưa kiểm): binding vào `bySub` và kênh đã đăng ký trước khi `handshake` được xếp hàng, nên một sự kiện tới đúng khoảng đó có thể đứng trước `handshake`.
10. **Hạn mức theo người, theo IP, tốc độ mở và trần kết nối đều tính theo instance**: cụm N replica cho N lần hạn mức. Hạn mức tốc độ chạy trước kiểm `Origin` nên yêu cầu bị 403 vẫn tiêu token.
11. **Hằng không cấu hình được ở gateway**: 50 Subscription mỗi kết nối, hạn bind 10 giây, timeout ghi 10 giây, hàng đợi 256, khung 64 KiB; `FHIR_WS_MAX_BINDINGS_PER_USER` bị chặn phải > 0 nên không thể tắt như `WSConfig` của Hub (âm: không giới hạn).
12. **Kết nối không còn binding nào** (đã unbind hết) sống mãi, vẫn chiếm trần kết nối và trần IP; không có hạn rảnh.
13. **Tiền tố khóa Redis cố định `trivita:events`** (gateway không cho cấu hình): bên phát dùng tiền tố khác thì không gặp gateway (phía `fhir-server` chưa kiểm).
14. **`FHIR_WS_MAX_CONNS_PER_IP` mà `FHIR_WS_TRUSTED_PROXIES` sai**: mọi client sau proxy bị coi là một IP và bị chặn nhầm (runbook đã cảnh báo; cảnh báo khởi động chỉ nhắc khi giá trị bằng 0).
15. **Không có `Origin` thì không kiểm** (client không phải trình duyệt): đúng thiết kế, an toàn dựa vào token; nhưng nghĩa là `FHIR_WS_ALLOWED_ORIGINS` không chặn được client tự viết.
16. **`api-contract/common.md`**: dòng 34 nói token bind gửi "trong khung đầu tiên" (chính xác hơn: bind phải tới trong 10 giây, `ping` đứng trước vẫn được; xem mục 7) và bảng lỗi HTTP (dòng 43-47) không liệt kê 403 `{"error":"không xác định được người gọi"}` của hạn mức tốc độ khi không xác định được IP.

### Phần B

Các điểm dưới đây đã đối chiếu mã; mục có "(chưa kiểm)" suy từ mã mà không có test.

1. **`hub.lease_seconds` luôn là 3600, không theo `FHIRCAST_LEASE`.** `Confirmation` dùng hằng `DefaultLease` (`pkg/fhircast/message.go`) chứ không dùng lease đã cấu hình. Đặt `FHIRCAST_LEASE=10m` thì socket vẫn bị đóng đúng 10 phút nhưng xác nhận báo 3600 giây; client dựa vào số này để gia hạn sẽ tính sai. Test e2e cố định giá trị 3600 ở cấu hình mặc định nên không bắt được. Không có cách gia hạn lease nào (không có API làm mới) nên giá trị chỉ mang tính thông tin.
2. **Thu hồi token không cắt socket đã nối.** Websocket không kiểm Bearer sau khi đăng ký; người bị thu hồi quyền ở auth vẫn nhận sự kiện (chứa tham chiếu bệnh nhân, đôi khi cả nội dung báo cáo) tới hết lease (mặc định 1 giờ, tối đa 24 giờ). Đây là đánh đổi của mô hình endpoint bí mật (BR-41, BR-67); không có cơ chế thu hồi theo người dùng (chưa kiểm).
3. **Cô lập chỉ theo project, không theo topic hay người dùng.** Trong cùng project, ai có scope `fhircast/*` và biết một topic đều đọc được ngữ cảnh, đăng ký nhận và phát sự kiện giả vào topic đó, kể cả `heartbeat`, `syncerror`, `userlogout` (các sự kiện này nằm trong 14 sự kiện được phép gửi và `heartbeat`/`syncerror` luôn tới mọi subscriber, BR-69). Bí mật thực tế là topic UUID do auth cấp theo người dùng (BF-4); nếu ứng dụng dùng topic đoán được (ví dụ mã bệnh nhân) thì mọi người trong project dùng chung được. Có `AllowTopicMismatch=true` thì còn cho phép ghi vào topic khác với đường dẫn.
4. **Không có hạn mức theo IP hoặc tốc độ cho các route HTTP của Hub.** `FHIR_WS_RATE_LIMIT` chỉ gắn vào `/ws/subscriptions-r4` và `/ws/fhircast/*`; `POST /fhircast/*`, `GET .../{topic}` chỉ bị giới hạn kích thước thân và cần token. Mỗi người có token tạo được số đăng ký không giới hạn (mỗi cái chiếm một khóa Redis tới 1 giờ) và không có trần số đăng ký mỗi người hoặc mỗi topic (chưa kiểm khả năng gây áp lực Redis).
5. **Lỗi hạ tầng của JWKS thành 401, không phải 503.** `parse` trong `pkg/fhirauth/jwt.go` bọc mọi lỗi (kể cả tải JWKS thất bại, không có `kid`) vào `ErrInvalidToken`; nhánh 500 của `authenticator` chỉ chạy với verifier giả trong test. Khi JWKS của auth sập (và không còn bản cache), client bị 401 và có thể đi xin token mới trong vô ích.
6. **Hủy mà không phát được `denied` thì socket còn sống tới hết lease.** `unsubscribe` xóa đăng ký rồi mới phát; nếu Bus lỗi chỉ ghi log. Socket giữ bản `Subscription` trong bộ nhớ nên vẫn nhận sự kiện cho tới khi hết lease (chưa kiểm bằng test).
7. **Bus không bền.** Sự kiện phát khi Redis pub/sub đứt, khi hàng đợi 256 của đăng ký đầy (bị bỏ, chỉ log cảnh báo), hoặc khi socket chưa nghe sẽ mất, không phát lại; socket không biết đã mất. Redis đứt không đóng socket.
8. **Giới hạn socket mỗi endpoint chỉ trên từng instance.** Với N replica, một endpoint có thể có tới 4N socket; tương tự `FHIRCAST_MAX_CONNECTIONS` và tốc độ nâng cấp tính từng instance (đã ghi ở `wsgroup`).
9. **Bất đối xứng hủy cho đăng ký không có subject.** `unsubscribe` bỏ qua kiểm người khi `UserSubject` của đăng ký rỗng; token luôn có `sub` nên chỉ xảy ra với dữ liệu cũ trong Redis (không có đường tạo mới).
10. **Nhận xét mã lệch test.** Chú thích đầu `pkg/fhircast/ws/ws.go` nhắc `TestFhircastNeverDialsOut` nhưng test đó không tồn tại; test thật cho "không gọi ra ngoài" là `TestFhircastWebhookNeverCallsOut` ở `apps/ws-gateway/internal/presentation/httpapi/fhircast_test.go`. `pkg/fhircast/ws/env_test.go` chỉ có 5 dòng (chuẩn bị môi trường).
11. **Dữ liệu y tế trên kênh Redis pub/sub.** Thông điệp đổi ngữ cảnh (kể cả bundle nội dung báo cáo) đi qua Redis pub/sub dạng văn bản; ai đọc được Redis dùng chung đều thấy. Kiểm soát bằng mạng và ACL Redis, không ở tầng Hub (chưa kiểm cấu hình Redis).
12. **Tên sự kiện giữ nguyên hoa/thường người gửi.** So khớp lọc không phân biệt hoa thường nhưng thông điệp tới subscriber giữ nguyên chữ của publisher (ví dụ `patient-open`); client so sánh phân biệt hoa thường có thể bỏ sót.
13. **`api-contract/common.md`** (dòng về Hub và route chỉ có khi bật): các câu đã đối chiếu (route, 404 khi tắt, mã 401 có `WWW-Authenticate: Bearer realm="fhircast"`, mã `login`/`forbidden`/`invalid`/`conflict`/`too-costly`/`exception`) khớp mã; phần nâng cấp WebSocket và Origin (mục 5) chỉ đối chiếu với `wsgroup` ở mức mô tả, chưa đọc từng dòng.
14. **Chưa kiểm:** hành vi khi `FHIRCAST_PUBLIC_URL` có cổng khác với địa chỉ client thật thấy (Hub tin cấu hình, không suy từ `Host`); xử lý đồng hồ lệch giữa các instance đối với `expiresAt` (instance giữ socket so với đồng hồ của instance tạo đăng ký); kích thước `X-Request-Id` và tải thực của `/ws/fhircast` (có `apps/ws-gateway/tests/load/load_test.go` nhưng không đọc trong mảnh này).
