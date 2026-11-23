# Kế hoạch tách `scim` và `ws` thành service

Nguyên tắc chung về tách service và triển khai: [../../service-principles.md](../../service-principles.md). Trạng thái: **kế hoạch, chưa có code** (2026-11-21). Bổ sung cho [scim-ws-fhircast.md](scim-ws-fhircast.md): tài liệu đó trả lời "port hành vi nào của Medplum"; tài liệu này trả lời "chạy ở tiến trình nào, ranh giới dữ liệu ra sao, làm theo thứ tự nào". Mọi `file:dòng` là của repo này, đã đọc ngày viết.

## 0. Kết luận

| Module | Tách thành service riêng? | Đề xuất |
|---|---|---|
| `ws` (`subscriptions-r4`, sau này `fhircast`) | **Có**, nên tách | `apps/ws-gateway` (:8007). Lý do: kết nối dài, tốn bộ nhớ và scale theo số kết nối, khác hẳn fhir-server scale theo yêu cầu; hiện mỗi lần deploy fhir-server là ngắt mọi websocket |
| `scim` | **Không tách thành service có dữ liệu riêng** | Làm bên trong `apps/auth` sau cờ `AUTH_SCIM_ENABLED`; nếu cần cô lập khi vận hành thì chạy **cùng image `auth` với một vai trò riêng** (Pod riêng, ingress riêng, cùng DB) |

Lý do `scim` không tách (có bằng chứng trong code):
1. Dữ liệu và logic đều của `auth`: `User`, `ProjectMembership`, `Login`, `UserSecurityRequest`; cần `RevokeUserLogins` (`application/password.go:82`), retire security request (`application/securityrequest.go:65`), chính sách mặc định (`application/admin.go:99-108`), `Mutate` và `InTx` của kho. Kho `auth` ở database **riêng** (D-1..D-22), chứa `passwordHash`.
2. Tách ra thì chỉ có hai cách: dùng chung DB của auth (phá ranh giới dữ liệu, service SCIM giữ khóa đọc/ghi hash mật khẩu) hoặc auth phải mở API nội bộ. API đó chính là `/admin/*` HTTP đã **hoãn** (P7), tức là xây thêm một API lớn chỉ để SCIM gọi.
3. SCIM đăng nhập bằng Basic của `ClientApplication` (Q-SC-1) và kiểm `RequireProjectAdmin`: là mã xác thực của auth.
4. Không có nhu cầu scale riêng: Okta đồng bộ ngắt quãng, khoảng 700 dòng nguồn Medplum.

Hiệu chỉnh một điều tôi nói ở lượt trước: "ws cần fan-out nên bị `messaging` chặn" là **sai**. `pkg/messaging` đã có `Broadcaster` (`messaging.go:52`, Redis và memory), `fhirsub.Manager.RegisterBroadcast` dùng nó, và đã có test hai instance qua Redis (`ws_test.go`). Thứ thật sự chặn việc tách ws là mục 1.2 bên dưới.

## 1. `ws-gateway`

### 1.1 Hiện trạng cần biết

| Điều | Vị trí |
|---|---|
| `Hub` (kết nối, token bind HMAC, heartbeat) và `Manager` (nạp Subscription, khớp tiêu chí, webhook) nằm chung ở `pkg/fhirsub`; `fhir-server` gắn cả hai (`apps/fhir-server/internal/bootstrap/container.go:100-130`) | `pkg/fhirsub/ws.go`, `manager.go` |
| `Manager.dispatch` khớp **mọi** `ChangeEvent` với **mọi** Subscription `active` của project rồi mới gọi `hub.notify`; cache Subscription lấy từ repository gốc | `manager.go:288-335` |
| `Repo.Wrap` phát `ChangeEvent` (kèm toàn bộ tài nguyên) lên broker sau commit | `pkg/fhirsub/repo.go:45-70` |
| Token bind tự ký HMAC (`FHIR_WS_SIGNING_KEY`), đã chụp `Grants`/`Policy`/hạn của người xin (mục 1, 2 của B.1.2 đã vá 2026-11-21) nên Hub không cần `Principal` sống | `ws.go:173-220`, `ws_authz_test.go` |
| `$get-ws-binding-token` là method của `Hub` và đọc Subscription qua repository | `ws.go:220` |
| Chưa làm: ẩn trường theo AccessPolicy, `unbind-from-token`, hạn mức bind theo (user, project), chỉ khớp Subscription đã có socket bind | B.1.2 mục 1 (phần ẩn trường), 3, 5, 6 |

### 1.2 Hai chỗ phải quyết trước khi tách

**(a) Ai khớp tiêu chí.** Hai phương án:

| | A. "Bản sao mỏng" | B. "Khớp ở fhir-server, giao ở gateway" (đề xuất) |
|---|---|---|
| Luồng | Gateway nghe broadcast **mọi** event của mọi project, tự nạp Subscription và tự khớp | `fhir-server` khớp như hiện nay, chỉ phát event lên kênh **theo Subscription đang có socket bind**; gateway chỉ giữ socket, kiểm token, lọc theo người nhận, heartbeat |
| Gateway cần | Redis + nguồn Subscription (REST với token dịch vụ, hoặc đọc DB) | Redis + khóa ký token. **Không** DB, không token dịch vụ, không đọc Subscription |
| PHI | Mọi tài nguyên của mọi project chảy vào tiến trình quay ra Internet | Chỉ event của Subscription đang được nghe |
| Công thêm | Bộ nạp Subscription từ xa (S đến M), cờ vai trò cho `Manager` (S) | `pkg/messaging` cần subscribe kênh **động** (`Broadcaster` hiện đăng ký topic cố định trước `Start`, `messaging.go:49-54`) (M); sổ "Subscription đang bind" trong Redis có TTL (S); `Manager` kiểm sổ trước khi phát (S) |
| Hạn chế | Mỗi gateway tự khớp mọi event: tải nhân theo số gateway | Event bị mất khi gateway khởi động lại (Broadcaster vốn không bền, `messaging.go:46-51`); như A |

Đề xuất **B**: gateway là thành phần hướng Internet và giữ kết nối dài, nên nó nên có ít quyền và ít dữ liệu nhất. Nếu cần ra sớm, A làm nhanh hơn nhưng phải chấp nhận hai điểm đầu của cột A và sẽ phải làm lại thành B; tôi không khuyên làm hai lần.

**(b) Redis bắt buộc.** Tách tiến trình nghĩa là `BROKER_DRIVER=memory` không còn dùng được cho ws (đã đúng cho nhiều replica: `FHIR_WS_SIGNING_KEY` bắt buộc khi `BROKER_DRIVER=redis`, `config.go:344`). Redis cần mật khẩu/TLS và chính sách mạng ở mọi môi trường ngoài `local`.

### 1.3 Các bước

| Bước | Việc | Cỡ | Xong khi | Phụ thuộc |
|---|---|---|---|---|
| WS-0 | Chốt (a) A hay B, và Q-WS-4 (tiền tố URL `/ws/...`) | quyết định | ghi vào mục 4 | |
| WS-1 **[x] xong 2026-11-21** | `pkg/wsgroup` (nhóm kết nối, `Origin`, giới hạn, đóng êm 1001/503/1011) rồi `fhirsub.Hub` dùng lại: **= B-1, B-2** của scim-ws-fhircast.md | S + S | `go test ./pkg/wsgroup ./pkg/fhirsub` xanh, `architecture_test.go` xanh | |
| WS-2 **[x] xong 2026-11-21 (trừ ẩn trường, xem dưới)** | Hoàn tất B-3 còn thiếu: ẩn trường, `unbind-from-token`, hạn mức bind, chỉ khớp khi có bind. Làm **trong thư viện** để cả hai chế độ dùng chung | M | W-3, W-4, W-8; ca 13, 20, 21, 29-33 | WS-1 |
| WS-3 **[x] xong 2026-11-21** | Tách `fhirsub.Hub` thành `TokenIssuer` (ký và cấp token, ở fhir-server) và `Hub` (kết nối, ở gateway). Cùng khóa HMAC; hỗ trợ **hai khóa** (hiện hành + cũ) để xoay khóa không ngắt phiên | M | Token do issuer cấp được Hub ở tiến trình khác xác minh; test xoay khóa | WS-2 |
| WS-4 **[x] xong 2026-11-21** | (chỉ B) `pkg/messaging`: subscribe kênh động cho Redis và memory; sổ "bound" có TTL; `Manager` chỉ phát kênh theo Subscription có bind | M | Test hai tiến trình qua miniredis TCP; event không tới gateway không nghe | WS-0 |
| WS-5 **[x] xong 2026-11-21** | `apps/ws-gateway`: `cmd` (serve, healthcheck), `internal/{config,presentation,bootstrap}`, route `/ws/subscriptions-r4`, `/health/*`; readiness = Redis ping; luật trong `architecture_test.go`; target `run-ws-gateway`; compose; `--build-arg APP=ws-gateway` | M | e2e: token từ fhir-server, socket vào gateway, nhận event; đóng êm | WS-3, WS-4 |
| WS-6 **[x] xong 2026-11-21** | fhir-server: `FHIR_WS_MODE=off\|embedded\|issuer`. `issuer` chỉ cấp token và trả `websocket-url` của gateway (`FHIR_WS_URL`); `embedded` giữ nguyên cho dev/test | S | Chuyển chế độ không đổi giao thức client | WS-3 |
| WS-7 **[x] xong 2026-11-21** | e2e ba tiến trình (auth, fhir-server, ws-gateway + miniredis TCP) trong `make test-e2e`; một mục chạy Redis thật cho `make test-pg` | M | Cô lập project, token giả, thu hồi (xem R-2), đóng êm | WS-5, WS-6 |
| WS-8 **[x] xong 2026-11-22** | Vận hành: Pod riêng, HPA theo số kết nối, `terminationGracePeriodSeconds` > `SHUTDOWN_DRAIN_DELAY` + `SHUTDOWN_TIMEOUT` (5 + 25 giây, `pkg/httpserve`), timeout websocket ở ingress, PodDisruptionBudget, gauge số kết nối | S | `docker compose` chạy thử thật | WS-5 |
| WS-9 ****[xong 2026-11-22: FC-1..FC-7]**** | `fhircast` chuyển vào gateway (FC-1..FC-5 của scim-ws-fhircast.md) | M đến L | | WS-5, ưu tiên thấp |

Cấu hình gateway: `HTTP_ADDR`, `REDIS_URL` (bắt buộc), `FHIR_WS_SIGNING_KEY` (bắt buộc, ≥ 32 byte), `FHIR_WS_SIGNING_KEY_PREVIOUS`, `FHIR_WS_ALLOWED_ORIGINS` (cấm `*` ngoài local), `FHIR_WS_MAX_CONNECTIONS`, `FHIR_WS_MAX_CONNS_PER_IP`, `FHIR_WS_HEARTBEAT`, `FHIR_WS_CLOSE_TIMEOUT`. Tên giữ tiền tố `FHIR_WS_` cho dễ chuyển từ fhir-server.

**WS-1 đã làm:** `pkg/wsgroup` (`Group.Handler(ServeFunc)`, `Close`, `Conns`; luật `pkgRules` đã thêm; `fhirsub` được phép import nó) và `fhirsub.Hub` dùng lại (bỏ `conns/closed/ctx/wg`, giữ API và toàn bộ test cũ không sửa). Test mới: 9 ca (echo và theo dõi, 503 khi vượt `MaxConns` không rò goroutine, `Origin`, 1009, panic thành 1011, 1001 và 503 khi đóng, đóng nhóm rỗng, client im lặng, handler kẹt). Đã chạy đột biến: bỏ kiểm `MaxConns` hoặc kiểm `closed` thì test đỏ.
Phát hiện khi làm: `coder/websocket.CloseNow` **không ngắt được** một `Close` đang chờ bắt tay (nó chỉ chờ kết nối đóng, tối đa 5 giây của thư viện), nên không thể "cắt sớm hơn 5 giây" một client im lặng. Thiết kế: sau `CloseTimeout` `Group.Close` bỏ chờ và huỷ ngữ cảnh; phần dọn kết nối im lặng chạy nền, bị chặn bởi 5 giây của thư viện. Hệ quả: `Close` trả trong `CloseTimeout`, nhưng socket đó còn có thể tồn tại thêm tối đa khoảng 5 giây trước khi tiến trình thoát.

**WS-2 đã làm** (trong `pkg/fhirsub`, dùng chung cho chế độ nhúng và gateway):
- `unbind-from-token`: gỡ binding của chính kết nối gửi; kiểm chữ ký nhưng **không** kiểm hạn (chỉ làm giảm quyền); chưa bind mà gỡ, token sai hoặc rác thì đóng 1008; gỡ Subscription chưa bind thì bỏ qua; kết nối vẫn mở và bind lại được.
- Hạn mức bind theo (project, `Principal.Subject`): `WSConfig.MaxBindingsPerUser` (mặc định 20, `FHIR_WS_MAX_BINDINGS_PER_USER`), đếm trên mọi kết nối của instance, giảm khi unbind, khi quyền hết hạn và khi đóng kết nối; bind lại cùng Subscription trên cùng kết nối không tốn thêm; token không có ảnh chụp quyền thì không đếm. Vượt thì đóng 1008 với thông điệp như Medplum. Đếm theo instance (Q-WS-3): cả cụm cho tối đa N x số replica.
- `Manager.dispatch` bỏ qua Subscription websocket không có socket nào đang bind trước khi parse tài nguyên và khớp tiêu chí (B.1.2 mục 6). Đây cũng là hành vi mà phương án B (WS-4) sẽ đẩy lên tầng broker.
- 8 test mới (`ws_bind_test.go`); đã chạy đột biến 4 chỗ (bỏ tăng hạn mức, bỏ trả hạn mức, bỏ lọc `hasBinding`, bỏ xử lý unbind) đều làm test đỏ.

**WS-2 chưa làm: ẩn trường theo `hiddenFields`.** Đây không phải lỗ hổng riêng của ws: `fhirauth.Policy` chưa có `hiddenFields` ở bất kỳ đường nào (mục A-05 của `fhir-parity-audit.md`, thuộc WP-2b cỡ L), còn `auth` dịch mọi rule có `hiddenFields` thành `deny` cả loại (`domain/tokenscope.go:112`), nên token do auth cấp không bao giờ nhận thông báo của loại đó. Cài lọc trường chỉ ở ws sẽ lệch ngữ nghĩa với REST, nên để A-05 làm một lần cho cả hai; `TestWebSocketNeverDeliversDeniedTypeEvenWithWildcard` khoá hành vi an toàn hiện tại. Hệ quả cho W-4: điều kiện "chặn phát hành" của B.8 chỉ cần quyền theo loại và tiêu chí (đã có), ca 20 của Medplum (xoá trường) được thay bằng ca "loại bị deny".

**WS-3 đã làm:** `pkg/fhirsub/ws_token.go` tách `Tokens` (cấp, xác minh, `TokenOperation`) khỏi `Hub`; `WSConfig.Tokens` cho Hub chỉ xác minh token của bộ khác (đường ws-gateway), còn cấu hình cũ (`SigningKey`) giữ nguyên nên chế độ nhúng không đổi. Định dạng dây được ghim bằng test dựng tay: `base64url(claims).base64url(HMAC-SHA256("fhir-ws-binding|"+claims))`, claims có `v` (hiện 1; bên nhận từ chối `v` lớn hơn, token không có `v` của bản trước được coi là 1). Xoay khóa: ký bằng khóa hiện hành, xác minh thử khóa hiện hành rồi tối đa 3 khóa trước (`FHIR_WS_SIGNING_KEY_PREVIOUS`, phân tách bằng dấu phẩy; quy trình ở `docs/runbook.md`); thử mọi khóa không thoát sớm. 10 test mới (`ws_token_test.go`) gồm hai đối tượng khác nhau chung khóa như hai tiến trình, xoay khóa ba bước, phiên bản, tách miền chữ ký; đột biến 4 chỗ đều bị bắt. Khi tách thật (WS-6) `websocket-url` do bên cấp trả về (`TokenConfig.URL`) sẽ là địa chỉ của gateway.

**Việc WS-3 để lộ cho WS-4/WS-5:** `Hub.bind` hiện gọi `h.lookup` do `Manager.AttachHub` gắn để kiểm Subscription còn `active` và cùng project (`ws.go`, `manager.go`). Ở gateway phương án B không có `Manager` (không có nguồn Subscription), nên Hub cần một chế độ "tin token": token đã được bên cấp kiểm (đọc được Subscription, `active`, `channel=websocket`) và ký, hạn 5 phút; việc Subscription bị xoá/tắt sau đó được xử lý bằng sổ "bound" của WS-4 (bên khớp ngừng phát khi Subscription không còn hoạt động, gateway đóng binding khi hết hạn quyền). Cần chốt khi làm WS-4: có cho Hub chạy không `lookup` ở chế độ gateway (đề xuất: có, chỉ khi `WSConfig.Tokens` được cấp và có cờ rõ ràng, còn chế độ nhúng vẫn bắt buộc `lookup`).

**WS-4 đã làm** (điểm mở của WS-3 về `lookup` cũng được giải ở đây):
- `pkg/messaging`: hai khả năng độc lập, cả Redis lẫn bộ nhớ. `Channels` (`PublishChannel`, `SubscribeChannel` trả `cancel`; kênh động theo tên, không bền, at-most-once; `SubscribeChannel` chỉ trả về khi Redis đã xác nhận đăng ký nên phát ngay sau đó không lọt; `cancel` chờ handler đang chạy; handler panic không giết đăng ký; `Stop` hủy hết). `Presence` (`Mark`/`Unmark`/`Present` với TTL): Redis dùng ZSET điểm là thời điểm hết hạn, tính bằng đồng hồ của Redis (`TIME` trong Lua, không phụ thuộc lệch giờ giữa máy), khóa chỉ hết hạn khi member sống lâu nhất hết hạn (member TTL ngắn ghi sau không cắt ngắn member dài).
- `fhirsub.Manager.AttachRemote(channels, presence)` (loại trừ `AttachHub`, không dùng `RegisterBroadcast`): Subscription websocket được khớp **một lần cho cả cụm** ở handler nhóm; khớp rồi mới hỏi sổ hiện diện (idle thì không tốn Redis), có người nghe thì `PublishChannel("fhirws.sub.<id>", WSMessage)`. `Stats` thêm `Published` và `Unheard`.
- `fhirsub.Feed` (phía gateway): đăng ký nghe kênh của Subscription khi socket đầu tiên bind **rồi mới** ghi sổ hiện diện (thứ tự được test khoá); vòng định kỳ (`PresenceTTL`/3) gia hạn sổ và bỏ đăng ký những Subscription hết socket; `Stop` gỡ sổ ngay để bên phát không chờ hết hạn. Redis hỏng lúc bind thì bind thất bại với **1013** (thử lại), hoàn tác binding và hạn mức, hủy đăng ký kênh đã tạo (không rò).
- `WSConfig.TokenOnly` cho Hub bỏ bước `lookup` với Manager (chế độ gateway): chỉ tin token đã ký. Mặc định tắt; Hub nhúng không bật vẫn bắt buộc `lookup` (có test).
- 16 test ở `ws_remote_test.go` (mô phỏng hai tiến trình chỉ chung khóa token và nơi pub/sub, chạy cả bộ nhớ lẫn Redis/miniredis) và 11 test ở `channels_test.go` (nhiều test chạy trên cả bộ nhớ lẫn Redis); đột biến 9 chỗ (đảo thứ tự subscribe/mark, bỏ kiểm sổ, bỏ dọn, bỏ gia hạn, bỏ xử lý lỗi ensure, đổi mã 1013, bỏ xác nhận đăng ký Redis, bỏ hết hạn, bỏ `PEXPIRE` có điều kiện) đều làm test đỏ.
- Đã chạy trên Redis THẬT 7.4 (Docker) ở WS-5: `messaging`, `fhirsub` và `ws-gateway` đều xanh (đặt `MESSAGING_TEST_REDIS_ADDR`; `make test-pg` đã gồm). Lua dùng `TIME` nên cần Redis >= 5.
- Giới hạn đã biết: (1) mỗi sự kiện khớp một Subscription websocket tốn một `ZCOUNT` Redis; (2) cache Subscription của Manager hết hạn theo `CacheTTL` ở các instance fhir-server khác (đã có từ trước, nay ảnh hưởng cả đường websocket); (3) Subscription websocket bị xoá vẫn giữ socket mở, chỉ thôi nhận (như bản nhúng).

**WS-5 đã làm: `apps/ws-gateway` (:8007)** — xem [apps/ws-gateway/README.md](../../../apps/ws-gateway/README.md).
- Cấu trúc như `fhir-server` (không domain/application riêng): `cmd/ws-gateway` (serve, healthcheck), `internal/config`, `internal/bootstrap` (Redis, Hub `TokenOnly`, Feed), `internal/presentation/httpapi` (Echo: `/ws/subscriptions-r4`, `/healthz`, `/health/{live,startup,ready}`). Không DB, không swagger (không có API REST nghiệp vụ). Luật phụ thuộc trong `architecture_test.go`; `make run-ws-gateway`; `docker-compose.yml` có `ws-gateway` và `redis`; `docker build --build-arg APP=ws-gateway` chạy được.
- Khóa `FHIR_WS_SIGNING_KEY` bắt buộc và KHÔNG có khóa ngẫu nhiên thay thế (phải trùng bên cấp); `REDIS_URL` bắt buộc đặt tường minh ở staging/production; `FHIR_WS_ALLOWED_ORIGINS` là mẫu host, cấm `*` ở staging/production; Redis chưa lên lúc khởi động không làm tiến trình thoát (readiness 503, bind trả 1013), cấu hình sai thì thoát ngay.
- `pkg/wsgroup` bổ sung: **giới hạn theo IP** (`MaxConnsPerIP`, 429, `ClientIP` tùy chỉnh để tin đúng proxy) và **đặt chỗ trước khi Accept**. Sửa một lỗi đua có từ WS-1: kiểm `MaxConns` bằng số kết nối đã đăng ký nên nhiều upgrade đồng thời vượt được hạn mức (test 120 upgrade đồng thời, đột biến về cách cũ làm test đỏ). `WSConfig` nhận `CloseTimeout`, `MaxConnsPerIP`, `ClientIP`.
- Hạn mức mở websocket mỗi IP (`FHIR_WS_RATE_LIMIT`, mặc định staging/production 10/s), `FHIR_WS_TRUSTED_PROXIES` để đếm đúng IP sau proxy.
- 20 test mới cho app (config 4, presentation 6, tích hợp 10 với phía fhir-server ở "tiến trình khác" chỉ chung Redis và khóa) + 3 test `wsgroup`; chạy cả trên miniredis lẫn Redis 7.4 thật (một ca chỉ chạy với miniredis vì tắt Redis giữa chừng); đột biến 7 chỗ ở container (bỏ `TokenOnly`, bỏ gỡ sổ khi Stop, readiness bỏ qua Redis, không truyền `ClientIP`, không truyền Origin, rơi khóa cũ, đảo thứ tự Stop) đều làm test đỏ; chạy thật bằng Docker: healthy, `101 Switching Protocols`, `/app healthcheck`.
- Chưa có: `fhir-server` ở chế độ chỉ cấp token (WS-6), nên chưa có luồng thật fhir-server -> Redis -> gateway ngoài test; e2e ba tiến trình thật (WS-7); K8s/HPA/PDB (WS-8); metrics số kết nối.

**WS-6 đã làm: `FHIR_WS_MODE=off|embedded|issuer` ở `fhir-server`.**
- `embedded` = hành vi cũ (Hub trong tiến trình); `FHIR_WEBSOCKET=true` giữ làm bí danh của `embedded` (đặt cùng `FHIR_WS_MODE` khác `embedded` là lỗi). Mặc định `off`. Đọc bằng `Settings.WSModeResolved()`.
- `issuer`: không có Hub và không có endpoint `/ws/subscriptions-r4` (404); `$get-ws-binding-token` vẫn ở fhir-server (kiểm quyền người xin, ký token chụp quyền) nhưng `websocket-url` là của gateway (`FHIR_WS_URL`); `Manager.AttachRemote` khớp một lần cho cả cụm và phát lên kênh của Subscription nếu có gateway nghe; `Container.Tokens` thay `Hub.Tokens()` để cấp token ở cả hai chế độ.
- Kiểm cấu hình lúc khởi động ở `issuer`: bắt buộc `FHIR_SUBSCRIPTIONS`, `BROKER_DRIVER=redis`, `FHIR_WS_SIGNING_KEY` (>= 32, không có khóa ngẫu nhiên thay thế), `FHIR_WS_URL` là `ws://`/`wss://` đầy đủ (staging/production chỉ `wss://`); **cấu hình của phía giữ kết nối** (`FHIR_WS_MAX_CONNECTIONS`, `FHIR_WS_MAX_BINDINGS_PER_USER`, `FHIR_WS_ALLOWED_ORIGINS`, `FHIR_WS_HEARTBEAT`) đặt ở fhir-server issuer bị từ chối và nhắc `ws-gateway` (tránh người vận hành tưởng giới hạn có hiệu lực).
- `docker-compose.ws.yml` (chồng lên `docker-compose.yml`) chạy cả bộ fhir-server issuer + ws-gateway + Redis + Postgres.
- Test: 7 test tích hợp ở `bootstrap/ws_issuer_test.go` (phía gateway dựng bằng `pkg/fhirsub`: Hub `TokenOnly` + `Feed` trên cùng Redis, chạy cả miniredis lẫn Redis thật) và `TestWSModeSettings` ở config (nhiều ca); đột biến 6 chỗ (bỏ `AttachRemote`, bỏ URL gateway trong token, chỉ đăng ký operation cho Hub, cho phép broker bộ nhớ, bỏ cấm cấu hình phía gateway, bỏ kiểm xung đột bí danh) đều làm test đỏ.
- **Đã kiểm bằng container thật** (compose 4 dịch vụ, Redis 7 + Postgres 16): tạo Subscription, lấy token (trả `ws://localhost:18007/...` của gateway), bind trên gateway, ghi Observation -> client nhận `event-notification`; sổ hiện diện có trong Redis khi có socket và **bị gỡ khi gateway dừng** (client nhận 1001, exit code 0); fhir-server issuer trả 404 ở endpoint websocket cũ; và **khởi động lại fhir-server khi socket đang mở không ngắt kết nối, sự kiện sau đó vẫn tới** (mục tiêu của việc tách).
- Không có đường chuyển dữ liệu đang mở từ `embedded` sang `issuer` (đổi chế độ thì client bind lại bằng token mới; tài liệu vận hành nên deploy gateway trước rồi mới đổi fhir-server).

**WS-7 đã làm: e2e ba binary thật trong `make test-e2e`** (`e2e/ws_split_test.go`; khung `e2e/authen_fhir_test.go` được mở rộng: build thêm `ws-gateway`, `newStackWS`, `proc.stop`, khởi động lại tiến trình). Redis là miniredis chạy trong tiến trình test với cổng TCP thật cho các binary, hoặc Redis thật nếu đặt `MESSAGING_TEST_REDIS_ADDR`; không cần Docker.
Bảy kịch bản, mỗi cái đi hết đường thật (đăng nhập ở auth -> tạo Subscription và lấy token bind ở fhir-server -> bind và nhận ở gateway):
1. người dùng đăng nhập nhận sự kiện đúng tài nguyên, không nhận sự kiện sai loại; `websocket-url` là địa chỉ gateway; fhir-server issuer trả 404 ở endpoint websocket cũ;
2. cách ly project xuyên cả chuỗi (B không lấy được token của Subscription của A: 404; sự kiện của A không tới socket của B và ngược lại);
3. quyền người nhận theo scope của token auth (token chỉ có `system/Subscription.rs` lấy được token bind nhưng không nhận sự kiện Observation);
4. gateway đóng 1008 với token rác, rỗng, ký bằng khóa khác, payload bị sửa giữ chữ ký, phiên bản lạ và hết hạn (khóa đúng), access token JWT và refresh token của auth dùng nhầm; token thật vẫn bind được sau đó;
5. **khởi động lại fhir-server khi socket đang mở không ngắt kết nối**, sự kiện sau đó vẫn tới, và bind mới sau restart cũng được;
6. gateway tắt êm: socket nhận 1001, sổ hiện diện trong Redis được gỡ, tiến trình thoát êm, fhir-server vẫn ghi bình thường; gateway lên lại thì bind lại được;
7. hết socket thì sổ hiện diện được gỡ (không chờ hết hạn) và bind lại vẫn nhận sự kiện.
Đã chạy đột biến 6 chỗ ở thư viện sản phẩm, mỗi chỗ làm đúng test tương ứng đỏ (gateway bỏ kiểm quyền người nhận, matcher bỏ lọc project - sự kiện project A tới socket project B, Feed không gỡ sổ khi dừng, Feed không bỏ đăng ký khi hết socket, token không kiểm chữ ký); chạy cả trên miniredis lẫn Redis 7.4 thật, chạy lặp 4 lần (28 lượt) không flaky.
Không kiểm ở e2e: token access hết hạn giữa chừng (cần chờ quá 30 giây lệch đồng hồ, đã có test đơn vị ở `pkg/fhirsub` và `pkg/fhirauth`); thu hồi login ở auth (cửa sổ thu hồi D-13 vẫn áp: socket đã bind chỉ ngừng nhận khi quyền chụp hết hạn); nhiều replica gateway/fhir-server cùng lúc.

**WS-8 đã làm: vận hành.**
- **Metric**: `apps/ws-gateway/internal/bootstrap/metrics.go` đăng ký 9 instrument OTLP qua `observability.Meter` (tạo SAU khi Setup observability chạy): gauge `ws_gateway.connections/bindings/subscriptions/redis.up`, counter `messages.sent`, `connections.slow`, `messages.denied`, `events.received`, `events.malformed`. Đọc `Stats()` của Hub và Feed lúc thu thập nên không làm chậm đường nóng. 2 test với SDK reader thủ công; đột biến 8 chỗ (đổi các gauge cho nhau, đặt giá trị 0, `redis.up` luôn 1) đều làm test đỏ (hai lần đầu lọt vì các con số trùng nhau, đã sửa kịch bản để mọi số khác nhau). `go.mod`: `otel/metric` chuyển thành phụ thuộc trực tiếp.
- **Đo tải thật** (`make bench-ws`, `apps/ws-gateway/tests/load`, cần `TRIVITA_E2E_LOAD=1` và Docker cho Redis): N socket vào binary thật; 1.000 socket ≈ 56 KB/socket khi kết nối và ~100 KB sau khi nhận tin, 4.000 socket ≈ 53 và ~68 KB; fan-out p99 15 ms (1.000) và 48 ms (4.000). Quy ước 100 KB/socket + 24 MB nền; số liệu và lưu ý ở `docs/runbook.md` mục 11.1.
- **Manifest K8s ví dụ** `deploy/k8s/ws-gateway`: Deployment (rolling `maxUnavailable: 0`, `minReadySeconds`, `terminationGracePeriodSeconds: 45`, 3 probe, securityContext chặt, `GOMEMLIMIT`), Service, HPA (bộ nhớ và CPU, scale xuống rất chậm), PDB, ConfigMap, Secret ví dụ, ingress-nginx, Istio (VirtualService + DestinationRule), NetworkPolicy tùy chọn. Kiểm bằng `kubectl kustomize`, dry-run phía server (bắt được: Istio của cụm từ chối `timeout: 0s`) và **triển khai thật** vào namespace tạm trên cụm OrbStack (production env, Redis nội bộ): bắt được lỗi `runAsNonRoot` không qua được với `USER nonroot` dạng tên, đã sửa ở `Dockerfile` (UID 65532) và manifest.
- **Hành vi đã kiểm trên cụm thật**: xoá pod -> client nhận 1001 sau ~5s, pod biến mất < 12s; Redis chết -> pod NotReady sau ~13s, không restart, Ready lại sau ~3-6s khi Redis lên; rolling update giữ >= 2 endpoint sẵn sàng, 2 replica thay xong ~26s.
- **Chưa kiểm**: HPA scale thật (cụm thử không có metrics-server), thực thi NetworkPolicy, định tuyến qua ingress-nginx/Istio (chỉ dry-run), nhiều node/zone, metric OTLP tới collector thật, scale theo số kết nối (cần Prometheus/KEDA).
- **Dọn dẹp**: namespace thử `ws-gw-ws8-test` đã được yêu cầu xoá nhưng API cụm không phản hồi lúc dọn (xem ghi chú cuối; kiểm tra lại bằng `kubectl get ns ws-gw-ws8-test`).

### 1.4 Rủi ro

| Mã | Rủi ro | Cách xử lý |
|---|---|---|
| R-1 | Mất event khi gateway khởi động lại hoặc Redis đứt (`Broadcaster` không bền) | Chấp nhận (at-most-once, như Medplum); client phát hiện qua `eventsSinceSubscriptionStart` và `heartbeat`; ghi vào `docs/api-contract` |
| R-2 | Thu hồi quyền: token bind chụp quyền lúc xin; login bị thu hồi sau đó vẫn nhận tới khi socket đóng (cửa sổ ≤ 5 phút để bind, nhưng socket đã bind sống lâu hơn) | `Hub.notify` đã gỡ binding khi quyền hết hạn (`b.who.Expired`, `ws.go:~492`); giữ nguyên ở gateway và thêm test hai tiến trình (WS-7); cửa sổ thu hồi giống D-13 giữa auth và fhir-server, ghi nhận |
| R-3 | Gateway hướng Internet giữ PHI trong bộ nhớ | Phương án B; chạy `nonroot`, không DB, mạng chỉ tới Redis; `Origin` chặt; giới hạn theo IP |
| R-4 | Hai phiên bản thư viện (fhir-server và gateway) lệch định dạng token khi deploy cuốn chiếu | Trường `v` trong token; gateway từ chối phiên bản lạ; deploy gateway trước, issuer sau |
| R-5 | Redis là điểm lỗi chung của ws và webhook | Readiness gateway phụ thuộc Redis; fhir-server `issuer` vẫn phục vụ REST khi Redis chết (ws suy giảm) |

## 2. `scim`

### 2.1 Ba phương án

| | Mô tả | Đánh giá |
|---|---|---|
| **A. Trong `apps/auth` (đề xuất)** | Route `/scim/v2/*` trong nhóm riêng, cờ `AUTH_SCIM_ENABLED=false` mặc định (404 khi tắt), middleware riêng (Basic/Bearer của client admin, `RequireProjectAdmin`, giới hạn IP `AUTH_SCIM_ALLOWED_IPS`, hạn mức riêng) | Không phá ranh giới dữ liệu, ít code, đúng kế hoạch A.3 của scim-ws-fhircast.md |
| **B. Cùng image, vai trò riêng** | `auth serve -role=scim` chỉ đăng ký `/scim/v2/*` và `/health/*`, dùng **cùng DB** và cùng use case; Deployment/HPA/ingress riêng (Okta chỉ vào ingress này, IP allowlist ở ingress) | Cô lập khi vận hành mà không tách dữ liệu; rẻ (S), đảo ngược được. Làm **sau A** khi cần |
| C. Service riêng, DB riêng hoặc gọi API | Cần `/admin/*` HTTP của auth (đã hoãn) hoặc dùng chung DB | Không khuyên: xem mục 0 |

Để A có thể đổi thành B hoặc C về sau: đặt use case ở `application/scim.go` không import Echo, nhóm route ở `presentation/httpapi/scim.go`, mọi cấu hình có tiền tố `AUTH_SCIM_`. Khi đó vai trò B chỉ là một `switch` ở `cmd/auth`.

### 2.2 Các bước (bám SC-1..SC-7 của scim-ws-fhircast.md)

| Bước | Việc | Chặn bởi |
|---|---|---|
| SC-0 | **Chốt Q-SC-1** (Basic cho `/scim/v2`), Q-SC-2 (`ProfileWriter`, D-10), Q-SC-3, Q-SC-4, Q-SC-5, Q-SC-7 | quyết định của bạn |
| SC-1, SC-2 | `domain/scim` (kiểu, filter, patch); cổng kho `MembershipRepository.ListByProject/Mutate/Delete`, `TxRepos` thêm `Memberships`, `Logins` | không |
| SC-3 | Middleware xác thực và `RequireProjectAdmin` | P7.1 |
| SC-4 | Đọc, tìm, PUT, PATCH + route; đổi email trong một transaction | SC-1..3 |
| SC-5 | `InviteUser` lõi (P7.4a) và `CreateScimUser`, `ProfileWriter` | P4 (security request), D-10 |
| SC-6 | `DeleteScimUser` dùng use case P7.3 | P7.3 |
| SC-7 | e2e kịch bản Okta, tài liệu, swagger | SC-5, SC-6 |
| SC-R | (tùy chọn) vai trò B | SC-4 |

SCIM nằm sau P4/P5 và P7 của `auth`, nên **không nên bắt đầu trước khi P4 xong**; nếu cần ra sớm, làm SC-1, SC-2, SC-4 (chỉ đọc, PUT, PATCH, DELETE) trước, tạo user (SC-5) sau, đúng "Rủi ro" ở A.10.

## 3. Thứ tự đề xuất

1. WS-0 (quyết định), rồi WS-1, WS-2: sửa lỗ hổng còn lại trong code hiện có, không phụ thuộc gì.
2. WS-3, WS-4, WS-5, WS-6: tách thật.
3. WS-7, WS-8: kiểm tra đầu-cuối và vận hành.
4. SC-0 (quyết định) song song bất cứ lúc nào; SC-1, SC-2 làm xen kẽ trong khi chờ P4; còn lại theo `auth` P4/P5/P7.
5. Việc #4 của task-list (`pkg/mailer`, `pkg/binarystore`, `SessionBus`) **không chặn** ws; `SessionBus` chỉ cần nếu sau này muốn đóng socket ngay khi auth thu hồi login (R-2).

## 4. Quyết định cần bạn chốt

| Mã | Câu hỏi | Đề xuất |
|---|---|---|
| D-WS-1 | Phương án A hay B (mục 1.2) | **B** |
| D-WS-2 | Tiền tố URL websocket (Q-WS-4): `/ws/subscriptions-r4` hay có `/api`, `/projects/{id}` | Giữ `/ws/subscriptions-r4` |
| D-WS-3 | Redis bắt buộc cho mọi môi trường có gateway tách | Có |
| D-SC-1 | `scim` ở `auth` (A), có thể thêm vai trò riêng (B) sau; không tách service có dữ liệu riêng | **A rồi B** |
| D-SC-2 | Q-SC-1: Basic cho `/scim/v2` | Basic của `ClientApplication` có `admin`, bắt buộc `AUTH_SCIM_ALLOWED_IPS` ở staging/production |

**WS-9 / FC-1 đã làm** (`pkg/fhircast`, chỉ stdlib; Q-FC-7 đã giải bằng cách đọc `@medplum/core`):
- `events.go`: `SupportedEvents` (đúng 14 sự kiện của Medplum), `ParseEvents`, `ExtractEndpoint`, `EventCategory`, `ExtractAnchorType`, `ValidTopic` (≤256 byte, không ký tự điều khiển), `TopicHash` (SHA-256 base64url cho khóa và tên kênh), `NewID` (UUID v4 từ `crypto/rand`).
- `message.go`: `Subscription` (kèm `userSubject`, `expiresAt`), `ChannelMessage` + `MessageForSubscriber` (bỏ khi hỏng, `target` của người khác, sự kiện chưa đăng ký; `heartbeat`/`syncerror` và thông điệp điều khiển luôn tới; `target` không lên dây), `Confirmation` STU2/STU3, `Denied`, `InvalidEndpoint`, `Heartbeat` (cục bộ).
- `store.go`: cổng `Store` (đăng ký, ngữ cảnh hiện tại, CAS theo `context.versionId`, ngữ cảnh đã lưu theo báo cáo, topic người dùng) và `MemoryStore` có đồng hồ giả; mọi khóa có hạn (đăng ký bị kẹp ở lease, ngữ cảnh 24 giờ làm mới mỗi lần ghi), trần kích thước ngữ cảnh và số trường `ctxs`; dữ liệu hỏng đọc ra "không có" (`DecodeSubscription`/`DecodeContext` dùng chung cho adapter Redis ở FC-2).
- `architecture_test.go`: `pkgRules["pkg/fhircast"]={}` và `TestFhircastNeverDialsOut` (cấm import `net`, `net/http`: không có đường gọi ra ngoài, F-1 ở mức kiến trúc).
- 19 test (ca 36, 38, 39, 40, 41, 42 không Lua, 37 dạng bộ nhớ; F-3, F-6, F-7, F-9 với `-race`, F-10). Đột biến 14 chỗ (chạy lại bằng bộ chạy đúng, xem "Đính chính" bên dưới) đều làm test đỏ.
- Còn lại: FC-2 (Redis `Store`/`Bus`), FC-3 (máy trạng thái ngữ cảnh), FC-4 (HTTP, cần quyết định nơi xác thực Bearer ở gateway), FC-5 (kênh websocket).

**Quyết định D-WS-9-1 (2026-11-22, user chọn A):** HTTP của fhircast (`/fhircast/STU2|STU3`, `/hub`) nằm trong gateway; gateway thêm Verifier JWT dùng JWKS của auth (không DB). Hệ quả cho FC-4: cần cấu hình issuer/JWKS cho gateway, `GuardFor` theo scope `fhircast/*`, và readiness không phụ thuộc auth (JWKS được cache, xác minh vẫn chạy khi auth tạm tắt).

**WS-9 / FC-2 đã làm** (`pkg/fhircast`, thêm `go-redis` là phụ thuộc bên thứ ba duy nhất; vẫn không import `net`):
- `RedisStore` (cùng hợp đồng với `MemoryStore`): đăng ký `SET EX` (kẹp ở lease), ngữ cảnh `SET EX` 24 giờ, CAS bằng `WATCH/MULTI/EXEC` (thử lại ≤8 lần, không Lua), hash `ctxs` có trần số trường và `EXPIRE`, topic người dùng `SET NX`+`GET`+`EXPIRE` trong một MULTI. Khóa chứa project và `TopicHash`, không chứa topic thô.
- `Bus` (+ `MemoryBus`, `RedisBus`): **một** `PubSub` (một kết nối) cho cả instance, đăng ký/hủy kênh Redis theo đếm tham chiếu, `Subscribe` chờ Redis xác nhận mới trả về (phát ngay sau đó không lọt; 20 lần liên tiếp), mỗi đăng ký có hàng đợi riêng (người chậm không chặn người khác, đầy thì bỏ), panic handler không giết đăng ký, `cancel` chờ handler đang chạy. `Channel(project, topic)` = `fhircast:ch:{project}:{topicHash}`.
- Test: hợp đồng `Store` chạy trên cả bộ nhớ và Redis, 6 test Bus x 2 backend, hai instance chung Redis, mọi khóa có TTL > 0, hết hạn bằng miniredis `FastForward` (F-10), dữ liệu hỏng, lỗi Redis được trả lên. Chạy cả trên Redis 7.4 THẬT (`FHIRCAST_TEST_REDIS_ADDR`, đã vào `make test-pg`), `-race -count=3` xanh.
- Đột biến 10 chỗ (bỏ so phiên bản CAS, bỏ kẹp lease, CAS/Set/hash ghi không TTL, bỏ trần `ctxs`, không Unsubscribe khi hết ref, luôn Subscribe lại, không chờ xác nhận, không gỡ đăng ký cuối, không giao tin) đều làm test đỏ; hai đột biến sống sót nên đã thêm `TestRedisStoreCASKeepsTTL` (CAS ghi mất TTL) và `TestRedisBusSubscribeWaitsForRedisConfirmation` (dùng proxy TCP làm chậm chiều client->server để kiểm `Subscribe` chỉ trả về sau khi Redis xác nhận; miniredis quá nhanh để phân biệt), và một test treo khi không có tin nên đã thêm timeout.

**WS-9 / FC-3 đã làm** (`pkg/fhircast/hub.go`; đọc lại `fhircast/routes.ts` để giữ đúng thông điệp lỗi):
- `Hub{Store, Bus}`: `Subscribe` (đăng ký và hủy, trả URL websocket hoặc endpoint đã hủy), `ChangeContext` (`open`/`close`/`update`/khác rồi phát lên `Channel(project, topic)`), `CurrentContext` + `ContextResponse` (STU2 trả mảng, STU3 trả đối tượng), `ValidateEvent`. Lỗi là `*Error{Status, Message}` để FC-4 đổi thành OperationOutcome; lỗi hạ tầng luôn là 500 `Internal server error` (không lộ chi tiết Redis).
- Giữ hành vi Medplum: `open` cất ngữ cảnh DiagnosticReport đang mở rồi khôi phục (giữ `context.versionId`) khi mở lại báo cáo đó, DiagnosticReport thêm Bundle `content` rỗng, `close` luôn xóa ngữ cảnh hiện tại (và bản đã lưu của báo cáo), update bundle PUT/DELETE với đúng 11 chuỗi lỗi, `priorVersionId`, ghi có điều kiện (409 khi bị chen).
- Siết thêm so với Medplum (theo C.8/C.9): `hub.events` và `hub.event` phải thuộc danh sách 14 sự kiện (F-8), topic hợp lệ (≤256 byte, không ký tự điều khiển), trần số entry của update bundle (200), hủy chỉ bởi đúng người tạo (cùng project, topic, `userSubject`), ngữ cảnh quá lớn là 400, update lỗi giữa chừng không để lại dấu, payload của người gọi không bị sửa. Không có tham số callback nào: kênh khác `websocket` bị từ chối (F-1).
- Test: 20 test chạy trên cả bộ nhớ và Redis (ca 2-35, 51, F-1/F-3/F-7/F-8/F-9); 20 chuỗi đua update đồng thời cho đúng một người thắng. Đột biến 22 chỗ đều làm test đỏ; `-race -count=15` xanh; Redis 7.4 thật xanh.
- Chưa làm ở FC-3: kiểm tra ý nghĩa tài nguyên (chỉ cần là đối tượng JSON; không validate `resourceType` ở context ngoài DiagnosticReport), và Q-FC-6 (topic đường dẫn khác `hub.topic`) thuộc FC-4.

**WS-9 / FC-4 đã làm** (lớp HTTP của Hub trong `apps/ws-gateway`, theo D-WS-9-1=A):
- `pkg/fhirauth`: `Verifier.VerifyClaims` (chữ ký, iss, aud, exp, sub; KHÔNG đòi project hay scope fhir.*) và kiểu `Claims`; `Verify` giữ nguyên hành vi (cùng đường `parse`). `messaging.Redis.Client()` để dùng chung pool.
- Cấu hình `FHIRCAST_*` (tắt mặc định): `PUBLIC_URL` (ws/wss, không suy từ Host), `JWT_ISSUER/AUDIENCE`, `JWKS_URL` hoặc `JWT_HMAC_SECRET` (đúng một; HMAC và `REQUIRE_SCOPE=false` và `ws://` bị cấm ở staging/production, JWKS phải https), `LEASE`, `CONTEXT_TTL`, `MAX_CONTEXT_BYTES`, `MAX_UPDATE_ENTRIES`, `MAX_BODY_BYTES`, `ALLOW_TOPIC_MISMATCH`; đặt cấu hình mà chưa bật là lỗi.
- `httpapi/fhircast.go`: `.well-known` công khai, `POST {v}/`, `POST {v}/{topic}`, `GET {v}/{topic}` cho STU2, STU3 và `/hub`; đọc JSON và form; OperationOutcome với đúng chuỗi lỗi ở `issue[0].details.text`; 401 kèm `WWW-Authenticate`, 403, 413 khi quá `MAX_BODY_BYTES`; lỗi lạ là 500 chung. Q-FC-6 chốt: mặc định từ chối topic lệch (`400 Topic mismatch`), có cờ nới như Medplum.
- `bootstrap/fhircast.go`: `authenticator` (token sai 401; thiếu project hoặc scope `fhircast/*` là 403; lỗi hạ tầng không bị đổi thành 401), dựng `RedisStore` + `RedisBus` trên client của `messaging.Redis`, bus đóng TRƯỚC Redis khi tắt.
- Lỗi thật tìm ra khi làm: (1) `readBody` ghi lỗi rồi trả nil nên handler chạy tiếp và ghi phản hồi thứ hai; (2) `c.Param` của echo trả topic đã giải mã khi đường dẫn không có `%2F` nhưng giữ nguyên dạng mã hóa khi có, nên topic chứa `/` hoặc `%` đọc sai: đã tự giải mã đúng một lần từ `EscapedPath`.
- Test: httpapi 14 test (ca 1-12, 15, 17-18, 20-21, 23-31, F-1 với httptest callback nhận 0 request, F-2, F-3, F-7), config 2 test x 20 ca, bootstrap 6 test (authenticator và 5 test container: JWT HS256 thật, hai instance chung Redis đọc cùng ngữ cảnh và nhận sự kiện của nhau, hủy ở instance khác, Stop sạch), `VerifyClaims` 1 test. Đột biến 25 chỗ (bỏ kiểm scope/project, scope khớp lỏng, GET/POST không auth, bỏ kiểm topic lệch, bỏ giới hạn body, `c.Param`, ba chỗ "lỗi mà vẫn đi tiếp" của `readBody`, thiếu `WWW-Authenticate`, lộ chi tiết lỗi, các điều kiện production của config...) đều làm test đỏ; một đột biến sống sót (nhánh form của `readBody`) nên test 413 giờ đòi thân phản hồi là MỘT OperationOutcome; chạy lại trên Redis 7.4 thật xanh.
- Chưa làm ở FC-4: hạn mức tốc độ theo IP cho các route HTTP của FHIRcast; `docker-compose`/K8s chưa có biến FHIRCAST_*; chưa kiểm với JWKS thật của auth (chỉ HMAC và test bộ `fhirauth` đã có cho JWKS).

**WS-9 / FC-5 đã làm** (kênh WebSocket `/ws/fhircast/{endpoint}`; gói con `pkg/fhircast/ws` vì cần `net/http` và `pkg/wsgroup`):
- `ws.Server` (trên `wsgroup.Group`): đọc đăng ký của endpoint (không có, hoặc quá `ExpiresAt` thì gửi `denied` "invalid endpoint" rồi đóng 1000), nghe kênh của (project, topic) qua `Bus` TRƯỚC khi gửi xác nhận STU2/STU3 (xác nhận luôn là khung đầu tiên), lọc theo người nhận bằng `MessageForSubscriber`, `denied` thì gửi xong đóng 1000, heartbeat cục bộ mỗi socket (không qua Bus, không trùng giữa instance), **hết lease thì `denied` "lease expired" rồi đóng** (Q-FC-4: chốt gửi `denied` trước), tối đa N socket mỗi endpoint trên một instance (mặc định 4, vượt thì `denied` rồi đóng 1008), hàng đợi gửi riêng mỗi socket (đầy thì đóng 1013), tin client hỏng bị bỏ qua, Bus hoặc Store lỗi thì đóng 1013, tắt êm 1001, đếm `Stats`.
- Gateway: route `/ws/fhircast`, `/ws/fhircast/*` (cùng hạn mức tốc độ theo IP, Origin, `FHIR_WS_MAX_CONNS_PER_IP`, `FHIR_WS_CLOSE_TIMEOUT`, `FHIR_WS_TRUSTED_PROXIES` với websocket Subscription; KHÔNG cần Bearer vì endpoint là bí mật 122 bit gắn với một đăng ký đã xác thực lúc cấp); biến mới `FHIRCAST_MAX_CONNECTIONS` (1000), `FHIRCAST_MAX_SOCKETS_PER_ENDPOINT` (4), `FHIRCAST_HEARTBEAT` (10s); thứ tự tắt: socket (1001) -> bus -> Redis.
- `architecture_test.go`: `pkg/fhircast` được import `pkg/wsgroup`; `TestFhircastNeverDialsOut` chuyển sang kiểm bằng AST: gói gốc cấm `net` và `net/http`, gói con cấm `net` và cấm `http.Client/Get/Post/PostForm/Head/DefaultClient/NewRequest*/Transport/RoundTripper` (đã thử chèn vi phạm giả lập, test đỏ).
- Test: `pkg/fhircast/ws` 17 test chạy trên cả bộ nhớ lẫn Redis (ca 43-50, 52-58, F-3 đến F-7: Origin, lease, giới hạn endpoint, client chậm, tắt êm, đăng ký bus được trả lại); bootstrap thêm 6 test (hai gateway chung Redis: đăng ký ở A, socket ở B, sự kiện đổi ở A tới B, hủy ở A đóng socket ở B; Stop gửi 1001; cấu hình thật sự tới được kênh: giới hạn endpoint, MaxConns, Origin được phép lẫn bị cấm, giới hạn theo IP, heartbeat, lease); httpapi thêm 2 test (route, hạn mức tốc độ). Đột biến 30 chỗ ở `ws.go`, bootstrap và route đều làm test đỏ; 1 đột biến tương đương (bỏ `Lease` ở `StoreOptions` của gateway, vì Hub đã tự truyền lease khi lưu nên không đổi hành vi). Chạy lại trên Redis 7.4 thật, `-race -count=5`.
- Chưa làm ở FC-5: số liệu OTLP của kênh (`medplum.fhircast.*`; Stats đã có sẵn để nối), giới hạn socket theo project (`FHIR_FHIRCAST_MAX_SOCKETS_PER_PROJECT` trong kế hoạch), giới hạn socket mỗi endpoint đếm theo instance chứ chưa theo cụm, kiểm tra thực tế với ingress/Istio (timeout websocket), e2e với binary thật (FC-7).

**Đính chính (2026-11-22, khi làm FC-5):** các lần chạy đột biến ở FC-2 đến FC-4 dùng lệnh `timeout`, KHÔNG tồn tại trên máy macOS này, nên các lần chạy qua nó luôn báo "bắt" dù test vẫn xanh; ngoài ra bộ chạy cũ tính cả lỗi biên dịch là "bắt". Toàn bộ đột biến của FC-1 đến FC-5 đã được chạy lại bằng bộ chạy mới (`go test -timeout`, tách riêng lỗi biên dịch và test đỏ) và các con số ở trên là kết quả của lần chạy lại. Lần chạy lại tìm ra 4 lỗ hổng test thật (đã vá): `Subscribe` của bus Redis không chờ xác nhận, nhánh form của `readBody` ghi đôi phản hồi, `Origin` và `MaxConnsPerIP` ở bootstrap không được kiểm, khung websocket nhị phân và đăng ký bus rò khi socket đóng.

**WS-9 / FC-6 đã làm** (`apps/auth`: token phát `hub.url` và `hub.topic`):
- `application/fhircast_hub.go`: khi **scope đã cấp** có term bắt đầu bằng `fhircast/` và `AUTH_FHIRCAST_HUB_URL` được đặt, phản hồi token có `hub.url` (nguyên văn, bỏ `/` cuối) và `hub.topic` (UUID ổn định theo chủ thể token, user hoặc ClientApplication). Không lấy được topic thì **không cấp token** (`500 server_error`, như Medplum); token không có `fhircast/` không hỏi kho (không tốn Redis).
- Cổng `application.HubTopics` + `HubConfig`; `infrastructure/hubtopic` cài bằng `fhircast.Store.UserTopic` (Redis `SET NX`+`EXPIRE` 1 giờ, mỗi lần hỏi gia hạn; hoặc bộ nhớ cho một instance). Chọn khác kế hoạch ban đầu (C.3): **auth và gateway không cần chung Redis** vì gateway không bao giờ đọc khóa topic người dùng.
- Cấu hình: `AUTH_FHIRCAST_HUB_URL` (http(s), không query/fragment; https ở staging/production) dùng `AUTH_REDIS_URL`, **bắt buộc có Redis ở staging/production** (nhiều instance mà không chung kho thì cùng một người nhận topic khác nhau).
- Lỗi thật tìm ra khi làm: deadline của context **không áp** cho bước khởi tạo kết nối của go-redis (đo được: cuộc gọi tới một Redis treo mất **5 giây** dù context 2 giây); đã đặt `DialTimeout/ReadTimeout/WriteTimeout` ngay trên client (đo lại: 2,0 giây) và không thử lại. Ghi nhận cho phần còn lại: các lời gọi Redis của route HTTP FHIRcast ở gateway dùng ngữ cảnh của yêu cầu (không có hạn riêng) nên một Redis treo làm yêu cầu đó chờ theo timeout mặc định của client.
- Test: `application/fhircast_hub_test.go` (4 test: scope, cấu hình tắt, fail đóng), `hubtopic` (5 test: ổn định theo chủ thể, chia sẻ giữa hai instance, hạn và gia hạn, Redis chết, Redis **treo**), `bootstrap/oauth_hub_test.go` (5 test qua HTTP thật: token mang hub.*, topic ổn định, access token đủ claim project/scope để gateway chấp nhận, mặc định tắt, fail đóng, topic nằm ở Redis dùng chung), cấu hình 1 test (3 ca hợp lệ, 6 ca lỗi). Đột biến 17 chỗ (bỏ kiểm scope, scope khớp lỏng, fail mở, chấp nhận topic rỗng, sai chủ thể, không nối hub, bỏ trim, bỏ điều kiện production, bỏ hạn Redis...) đều làm test đỏ, kèm 1 đột biến tương đương (dùng `login.Scope` thay `scopeClaim`: `scopeClaim` là tập cha của `login.Scope` nên không đổi hành vi).

**WS-9 / FC-7 đã làm** (`e2e/fhircast_test.go`, `make test-e2e`; hai tiến trình thật `auth` + `ws-gateway`, hai "ứng dụng lâm sàng" giả):
- `TestFhircastTwoClinicalAppsStayInSync`: hai ứng dụng của **cùng một người dùng** đăng nhập thật (PKCE), nhận **cùng `hub.topic`** và `hub.url` của gateway; viewer đăng ký và mở websocket, EHR mở bệnh nhân (`POST hub.url`), viewer nhận `Patient-open` kèm `context.versionId`, đọc được ngữ cảnh hiện tại, EHR đóng bệnh nhân (`POST hub.url/{topic}`), viewer nhận `Patient-close` và ngữ cảnh trống.
- `TestFhircastTokenWithoutScopeIsRefusedByGateway`: token thường không có `hub.*`, gateway trả 403 (thiếu scope), 401 (token giả), `.well-known` công khai.
- `TestFhircastProjectsAreIsolated`: hai người dùng ở hai project nhận topic khác nhau; người này biết topic của người kia vẫn không nhận sự kiện và không đọc được ngữ cảnh.
- Đây là lần **đầu tiên đường JWKS thật giữa auth và gateway** được chạy (gateway xác minh ES256 bằng `/.well-known/jwks.json` của auth). Đột biến 4 chỗ ở `auth` và gateway (không phát `hub.*`, mọi người chung topic, bỏ kiểm scope, gộp project) đều làm e2e đỏ.
- **WS-9 hoàn tất.** Còn lại (ngoài WS-9): số liệu OTLP của kênh FHIRcast, hạn mức tốc độ theo IP cho route HTTP FHIRcast, giới hạn socket theo project và theo cả cụm, kiểm qua ingress/Istio.
