# Runbook vận hành `fhir-server`

**Triển khai cả cụm (`auth` + `fhir-server` + `ws-gateway`): thứ tự bước, cấu hình bắt buộc ở production, xoay khóa, nâng cấp: [runbook-deploy.md](runbook-deploy.md).**

Nguyên tắc tách service và hợp đồng triển khai chung của mọi service: [service-principles.md](service-principles.md). Vận hành riêng `auth`: [apps/auth/docs/runbook.md](../apps/auth/docs/runbook.md); riêng `fhir-server`: [apps/fhir-server/docs/runbook.md](../apps/fhir-server/docs/runbook.md) (bản tự chứa, gồm cả cơ sở dữ liệu và triển khai).

Dành cho người trực vận hành. Mọi tên biến, lệnh và giá trị mặc định dưới đây lấy từ code hiện tại
(`apps/fhir-server/internal/config`, `pkg/fhirstore`, `pkg/fhirrouter`, `pkg/fhirauth`); cấu hình đầy đủ ở
[README của app](../apps/fhir-server/README.md). Chưa có mục nào trong runbook này được diễn tập trên PostgreSQL thật
(xem [go-port-status.md](go-port-status.md), giai đoạn 19a): lần đầu áp dụng ở môi trường staging.

## 1. Kiểm tra sức khỏe

| Endpoint | Ý nghĩa |
|---|---|
| `GET /healthz` | process còn sống (luôn 200 khi server chạy). Dùng cho liveness. |
| `GET /readyz` | 200 khi nhận được lưu lượng; 503 khi đang tắt hoặc một phụ thuộc hỏng. Body `{"ready":bool,"checks":{...}}` gồm `accepting_traffic`, `database` và `broker` (chỉ khi bật `FHIR_SUBSCRIPTIONS`). Dùng cho readiness. |
| `fhir-server healthcheck` | gọi `/healthz`, dùng cho `HEALTHCHECK` của Docker (image distroless không có curl). |

`/readyz` kiểm DB trong `READINESS_TIMEOUT` (mặc định 2s). Nếu `database` là `false`: kiểm kết nối, `DATABASE_URL`, số kết nối
còn trống (`DATABASE_MAX_OPEN_CONNS`, mặc định 20). Nếu `broker` là `false`: kiểm Redis (`REDIS_URL`).

## 2. Khởi động, tắt, triển khai

- **Tắt êm** (SIGTERM/SIGINT): `/readyz` chuyển 503 ngay, chờ `SHUTDOWN_DRAIN_DELAY` (5s) để load balancer rút instance, rồi dừng nhận
  yêu cầu mới và chờ yêu cầu đang chạy tối đa `SHUTDOWN_TIMEOUT` (25s). Đặt `terminationGracePeriodSeconds` của Kubernetes lớn hơn tổng hai
  giá trị này.
- Khi tắt: job nền đang chạy được chờ xong; job chưa xong sẽ **nhả lease** để instance khác (hoặc lần khởi động sau) chạy lại, không đánh
  dấu lỗi. Kết nối websocket đóng bằng mã 1001 (GoingAway); client tự kết nối lại và bind lại bằng token mới.
- **Rolling update nhiều replica**: đặt `DATABASE_AUTO_MIGRATE=false` và chạy `fhir-server migrate up` ở một bước riêng trước khi
  triển khai (xem mục 3). Instance khởi động trên schema chưa migrate sẽ **từ chối chạy** (thông báo "schema chưa được migrate"), nên không
  có nguy cơ chạy trên schema cũ.
- Cấu hình sai được gom và báo một lượt khi khởi động (ví dụ thiếu `FHIR_JOB_SIGNING_KEY` khi dùng JWKS); sửa theo thông báo, không cần đoán.

## 3. Schema và migration

```bash
fhir-server migrate status   # in số migration chưa áp dụng
fhir-server migrate up       # áp dụng
fhir-server migrate down     # lùi MỘT bước (chỉ dùng khi chắc chắn; xem cảnh báo)
```

- Phiên bản schema nằm trong bảng `fhirstore_schema_version` (goose). Dữ liệu nằm ở `fhir_resource` (bản hiện tại), `fhir_history`
  (mọi phiên bản) và các bảng chỉ mục `fhir_idx_*` (string, token, reference, date, number, quantity, uri).
- `migrate down` xóa dữ liệu của các bảng bị gỡ (bước đầu tiên xóa toàn bộ bảng dữ liệu). **Không chạy `down` trên production.** Muốn
  quay lại phiên bản app cũ: khôi phục từ bản sao lưu (mục 4).
- Chỉ mục search là dữ liệu dẫn xuất từ `content`. Sau khi nâng cấp có thêm tham số search (ví dụ composite ở 12a) resource ghi trước đó chưa
  có chỉ mục mới: chạy `fhir-cli reindex <Type>` hoặc `POST [base]/<Type>/$reindex` (quyền admin) từng loại, chờ job xong
  (`GET [base]/job/{id}/status`).

## 4. Sao lưu và khôi phục

PostgreSQL là nguồn sự thật duy nhất; server không giữ trạng thái nào khác cần sao lưu (hàng đợi giao webhook ở bộ nhớ, sự kiện websocket
không bền). Redis chỉ là broker sự kiện: mất Redis không mất dữ liệu FHIR, chỉ mất sự kiện chưa giao.

- **Sao lưu**: `pg_dump` toàn database (hoặc các bảng `fhir_*` và `fhirstore_schema_version`) theo lịch của đội DBA, hoặc snapshot/PITR của
  dịch vụ PostgreSQL. Cần nhất quán giữa `fhir_resource`, `fhir_history` và `fhir_idx_*`: dump trong **một** transaction (mặc định của `pg_dump`).
- **Khôi phục**: khôi phục vào database trống, chạy `fhir-server migrate status` (phải là 0 chưa áp dụng với cùng phiên bản app), khởi động.
  Nếu nghi chỉ mục lệch: `$reindex` từng loại (mục 3).
- **Xuất logic** (di chuyển dữ liệu giữa môi trường, không thay thế sao lưu): `fhir-cli export -o dir` rồi `fhir-cli import` vào máy chủ
  đích có `FHIR_UPDATE_AS_CREATE=true` để giữ nguyên id (xem [README của fhir-cli](../apps/fhir-cli/README.md)). Lưu ý bản export chỉ chứa
  phiên bản hiện tại, không có lịch sử, và không chứa `Binary`/`AsyncJob`.
- **Diễn tập** khôi phục định kỳ trên staging; chưa có kết quả diễn tập nào được ghi lại.

## 5. Xoay khóa và bí mật

| Bí mật | Cách xoay | Ảnh hưởng |
|---|---|---|
| Khóa JWT của identity provider (JWKS) | Phát hành khóa mới trên IdP (thêm trước, bỏ khóa cũ sau). Server tự nhận: cache JWKS 10 phút, gặp `kid` lạ thì làm mới (tối đa mỗi 30 giây). IdP sập thì dùng khóa đã cache. | Không cần khởi động lại. Token ký bằng khóa đã bị gỡ bị từ chối sau khi cache hết hạn. |
| Token tĩnh (`FHIR_READ_TOKENS`, `FHIR_WRITE_TOKENS`, `FHIR_ADMIN_TOKENS`) | Mỗi token dạng `tên:secret`, danh sách phân tách bằng dấu phẩy: thêm token mới cạnh token cũ, triển khai, đổi client sang token mới, rồi gỡ token cũ và triển khai lại. | Rolling update, không gián đoạn. Secret tối thiểu 24 ký tự. |
| `FHIR_JOB_SIGNING_KEY` (≥ 32 ký tự, mọi replica dùng chung) | Đặt **cùng giá trị mới trên mọi replica cùng lúc**. | Job nền đang chờ hoặc đang chạy tạo bằng khóa cũ sẽ **lỗi** ("sai chữ ký quyền") và phải gọi lại. Nên đổi khi không có export/reindex đang chạy (mục 6). |
| `FHIR_WS_SIGNING_KEY` (≥ 32 ký tự, mọi replica dùng chung) | **Xoay không gián đoạn** với `FHIR_WS_SIGNING_KEY_PREVIOUS`: (1) mọi replica đặt `FHIR_WS_SIGNING_KEY`=khóa mới và `FHIR_WS_SIGNING_KEY_PREVIOUS`=khóa cũ, triển khai cuốn chiếu (replica còn khóa cũ cấp token mà replica mới vẫn xác minh được); (2) sau ≥ 5 phút (hạn token) bỏ `_PREVIOUS`. Khi gateway tách riêng: đặt khóa mới ở gateway (cùng `_PREVIOUS`) TRƯỚC, rồi mới đổi bên cấp. Đổi đồng loạt không có `_PREVIOUS` vẫn được nhưng làm token chưa dùng mất hiệu lực. | Kết nối websocket đang mở không bị ảnh hưởng (khóa chỉ dùng lúc bind). Không có `_PREVIOUS`: binding token chưa dùng (hạn 5 phút) mất hiệu lực. |
| Secret của Subscription rest-hook (extension `subscription-secret`) | Sửa Subscription (PUT) với secret mới; báo bên nhận. | Có hiệu lực ngay trên instance sửa, các instance khác sau tối đa 30 giây (cache Subscription). |

## 6. Job nền (reindex, bulk export) bị kẹt hoặc lỗi

Trạng thái ở resource `AsyncJob` (status `accepted`, `completed`, `error`, `cancelled`). Với JWT, mỗi job mang chữ ký quyền; không có chữ ký hợp
lệ job báo `error`, không chạy.

- **Xem job đang chờ/chạy**: `GET [base]/AsyncJob?status=accepted` (token admin). Job đang chạy có lease ở `transactionTime` (RFC3339), được
  gia hạn định kỳ (lease `FHIR_JOB_LEASE`, mặc định 5 phút).
- **Process chết giữa chừng**: không cần can thiệp. Vòng bảo trì (`FHIR_JOB_MAINTENANCE_INTERVAL`, mặc định 30s; đặt `0` để tắt) đưa lại vào
  hàng đợi mọi job `accepted` đã hết lease và đủ cũ; nhiều replica cùng nhận thì chỉ một chạy (nhận job bằng If-Match). Job được chạy lại từ đầu,
  trừ batch async chạy tiếp từ checkpoint lưu trên `AsyncJob` (extension `https://trivita.ai/fhir/StructureDefinition/job-state`).
- **Hủy/xóa batch async**: `DELETE [base]/job/{id}/status` (cần quyền ghi). Job đang chạy dừng ở entry kế tiếp và dọn Binary tạm; job đã xong
  bị xóa cùng Binary kết quả. Hết `FHIR_EXPORT_RETENTION` thì vòng bảo trì tự xóa như export.
- **Job `accepted` mãi không chạy**: kiểm (a) `FHIR_JOB_MAINTENANCE_INTERVAL` có bằng 0 không; (b) job tạo trước khi bật JWT/trước giai đoạn
  11c không có chữ ký quyền nên sẽ lỗi khi chạy lại, hãy hủy và tạo lại; (c) `FHIR_JOB_CONCURRENCY` (mặc định 2) có bị job dài chiếm hết không.
- **Hủy/xóa export**: `DELETE [base]/bulkdata/{id}/status` (hủy job đang chạy, hoặc xóa job đã xong cùng các file `Binary`); hoặc
  `fhir-cli export` tự dọn (không dùng `-keep`). Export cũ hơn `FHIR_EXPORT_RETENTION` (mặc định 24h; số âm là không bao giờ xóa) được vòng bảo trì tự xóa.
- **Job `error`**: `GET [base]/job/{id}/status` trả OperationOutcome nêu nguyên nhân (cũng có trong log mức ERROR, `job lỗi`). Sửa nguyên nhân rồi
  gọi lại; export lỗi tự dọn file đã ghi dở.
- **Export tốn dung lượng**: file export là `Binary` trong DB (mỗi file khoảng 8MiB). Giữ `FHIR_EXPORT_RETENTION` ngắn và đừng dùng `-keep`
  thường xuyên.

## 7. Subscription (rest-hook và websocket)

- Bật bằng `FHIR_SUBSCRIPTIONS=true`; nhiều replica cần `BROKER_DRIVER=redis` và `REDIS_URL`. Sự kiện đi qua Redis Streams: giao rest-hook là
  at-least-once theo nhóm; kênh websocket đọc stream trực tiếp (không bền, không replay).
- **Webhook không tới**: log mức WARN `fhirsub: giao thất bại` (có `subscription`, `resource`, `attempts`, `status`, `error`). Lỗi
  "địa chỉ đích bị chặn" là chống SSRF (đích nội bộ); chỉ dev mới đặt `FHIR_WEBHOOK_ALLOW_PRIVATE=true`. Thử lại tối đa
  `FHIR_WEBHOOK_MAX_ATTEMPTS` (5) với backoff gấp đôi; hàng đợi giao nằm ở bộ nhớ nên **mất khi restart** (sự kiện chưa xử lý vẫn còn trên
  Redis, nhưng lần giao đang thử lại thì không).
- **Subscription tự chuyển `off`**: khi đặt `FHIR_SUBSCRIPTION_AUTO_DISABLE`, log WARN `fhirsub: tự tắt Subscription vì giao thất bại lặp lại`
  (có `subscription`, `project`, `failureCount`, `timeWindowSeconds`) và `Subscription.error` ghi lý do. Sửa bên nhận rồi đặt lại
  `status=active` (PUT/PATCH Subscription); bộ đếm đã được xóa lúc tắt.
- **Hàng đợi đầy**: log ERROR `hàng đợi giao đầy, bỏ thông báo`; tăng `FHIR_WEBHOOK_WORKERS` hoặc xử lý bên nhận chậm.
- **Websocket tách riêng (`FHIR_WS_MODE=issuer`)**: kết nối do `ws-gateway` giữ (xem `apps/ws-gateway/README.md`); deploy hoặc khởi động lại fhir-server KHÔNG ngắt socket (đã kiểm bằng container thật), chỉ ngắt khi gateway khởi động lại (client nhận 1001 và bind lại bằng token mới). Giới hạn kết nối, hạn mức bind và Origin đặt ở gateway, không ở fhir-server. Redis là điểm phụ thuộc chung: khi Redis đứt, ghi vào fhir-server vẫn thành công (lỗi phát sự kiện chỉ được log) nhưng thông báo websocket (và webhook) bị mất, không bù lại; readiness của cả fhir-server lẫn ws-gateway báo 503 vì đã gồm kiểm Redis, nên pod bị rút khỏi load balancer.
- **Websocket**: số kết nối tối đa mỗi instance `FHIR_WS_MAX_CONNECTIONS` (1000; vượt thì 503); hạn mức bind theo người `FHIR_WS_MAX_BINDINGS_PER_USER` (20; vượt thì đóng 1008, đếm theo instance nên cả cụm có thể cho tối đa N x số replica); client chậm bị ngắt (mã 1013). Cần `Upgrade` đi
  xuyên qua reverse proxy và timeout đọc của proxy lớn hơn `FHIR_WS_HEARTBEAT` (30s). Trình duyệt chỉ được từ cùng host trừ khi đặt
  `FHIR_WS_ALLOWED_ORIGINS`.

## 8. Nhiều project (tenant), super-admin, dữ liệu cũ

- Mỗi yêu cầu JWT bị ràng buộc vào project trong claim `fhir_project` (đổi tên bằng `FHIR_JWT_PROJECT_CLAIM`). Dữ liệu không có `meta.project`
  không ai thấy. Sau khi bật JWT trên một database đã có dữ liệu: `fhir-server assign-project -project <id> -dry-run` rồi bỏ `-dry-run`, chạy khi
  không có ghi đồng thời (idempotent, chạy lại an toàn).
- **Super-admin**: chỉ có tác dụng khi `FHIR_JWT_SUPER_ADMIN=true`; token có scope `fhir.super` chọn project bằng header `X-Fhir-Project`. Mọi yêu cầu
  của super-admin (kể cả bị từ chối) ghi một dòng log mức WARN `super-admin truy cập project` với `audit=true` (subject, home_project, target_project,
  method, path, status; không có nội dung dữ liệu hay token). **Thu các dòng này vào kho log không sửa được và đặt cảnh báo** khi có hoạt động bất thường.
- Điều tra rò rỉ chéo project: mọi truy cập đi qua `meta.project`; so sánh `project` của token với `meta.project` của resource, và kiểm các dòng `audit=true`.

## 9. Sự cố thường gặp

| Triệu chứng | Nguyên nhân thường gặp | Cách xử lý |
|---|---|---|
| Khởi động báo "schema chưa được migrate" | `DATABASE_AUTO_MIGRATE=false` và chưa chạy migrate | `fhir-server migrate up` |
| 401 `invalid_token` mọi yêu cầu sau khi đổi IdP | `FHIR_JWT_ISSUER`/`FHIR_JWT_AUDIENCE` không khớp claim `iss`/`aud`, hoặc IdP chưa phát `kid` mới | So khớp claim, kiểm `FHIR_JWKS_URL`; xem log WARN `token bị từ chối` (có lý do) |
| 403 với token hợp lệ | thiếu claim project, thiếu scope (`fhir.read`, `fhir.write`, `fhir.admin` hoặc SMART `system/<Type>.read`), hoặc AccessPolicy chặn loại/tiêu chí | log WARN `token không dùng được cho FHIR`; kiểm scope và claim `fhir_access_policy` |
| 404 với resource vừa ghi | đọc bằng token project khác | đúng thiết kế (tách project) |
| 410 khi đọc id của project khác đã bị xóa | giới hạn đã biết của tách project | xem go-port-status.md, 7a |
| Search composite hoặc tham số mới không ra kết quả với dữ liệu cũ | chưa lập chỉ mục sau nâng cấp | `$reindex` từng loại (mục 3) |
| Search chậm | thiếu chỉ mục cho kiểu truy vấn lạ, `_filter` nhiều nhánh OR, chained/`_has` khớp quá nhiều (giới hạn 5000, lỗi 400) | thu hẹp tiêu chí; xem `EXPLAIN` trên `fhir_idx_*`; chưa có bài kiểm tải |
| Cuộn trang thấy lặp/sót | phân trang bằng offset khi dữ liệu thay đổi giữa các trang | giới hạn đã biết; dùng `_since`/`_lastUpdated` cho đồng bộ tăng dần |
| `$export` lỗi 403 với token giới hạn | loại bị scope/AccessPolicy chặn (khi chỉ định `_type`) | bỏ `_type` để tự bỏ qua loại không đọc được, hoặc cấp quyền đọc `Binary` để tải file |

## 10. Cần làm trước khi coi là sẵn sàng production

Các việc còn mở (chi tiết ở [go-port-checklist.md](go-port-checklist.md)):

1. Chạy `make test-pg` trên máy có Docker và sửa mọi khác biệt SQL (giai đoạn 7 đến 21 chưa chạy trên PostgreSQL thật).
2. Kiểm tải: search, bulk export, nhiều replica tranh job, websocket nhiều kết nối. Chưa có số liệu.
3. CI chạy `make ci` (`gofmt`, `go vet`, `go test -race`) và `make test-pg`; repo chưa có file CI.
4. Diễn tập sao lưu/khôi phục (mục 4) và xoay khóa (mục 5) trên staging.
5. Kiểm toán bền: hiện chỉ có log `audit=true`, chưa ghi `AuditEvent` vào kho.

## 11. Vận hành `ws-gateway` (websocket của Subscription)

Chi tiết cấu hình và giao thức: [apps/ws-gateway/README.md](../apps/ws-gateway/README.md). Manifest ví dụ: [deploy/k8s/ws-gateway](../deploy/k8s/ws-gateway) (kustomize; **ví dụ**, mọi giá trị đánh dấu `ĐỔI` phải sửa theo môi trường).

### 11.1 Định cỡ (số đo thật, `make bench-ws`)

Đo bằng binary thật, miniredis, N socket bind vào 10 Subscription, mỗi vòng mỗi socket nhận một thông báo (Observation ~1 KB):

| N socket | RSS nền | RSS sau khi kết nối | RSS sau 3 vòng nhận tin | Mỗi socket | Fan-out p50 / p99 |
|---|---|---|---|---|---|
| 1.000 | 24 MB | 79 MB | 98 MB | 56 KB (kết nối), ~100 KB (đã nhận tin) | 7 ms / 15 ms |
| 4.000 | 24 MB | 230 MB | 272 MB | 53 KB (kết nối), ~68 KB (đã nhận tin) | 18 ms / 48 ms |

Mở 1.000 socket mất ~0,1 giây (≈13.000 socket/giây). **Quy ước định cỡ: 100 KB mỗi socket + 24 MB nền**, tức mặc định `FHIR_WS_MAX_CONNECTIONS=1000` cần ~125 MB; manifest ví dụ đặt request 192 Mi, limit 384 Mi (dư gấp ba cho tin lớn hơn). Số đo từ máy dev (macOS arm64), tin nhỏ, client cùng máy: **đo lại trên cấu hình triển khai thật** (`WS_LOAD_CONNS=5000 make bench-ws`) trước khi chốt requests/limits và `FHIR_WS_MAX_CONNECTIONS`. Tin lớn (nhiều trường, tài nguyên lớn) làm tăng bộ nhớ hàng đợi gửi của mỗi socket chậm.

### 11.2 Triển khai K8s

- **Tắt êm**: `terminationGracePeriodSeconds` PHẢI lớn hơn `SHUTDOWN_DRAIN_DELAY` (5s) + `SHUTDOWN_TIMEOUT` (25s) + `OTEL_SHUTDOWN_TIMEOUT_MS` (5s). Ví dụ đặt 45s. Đã kiểm trên cụm thật: khi pod bị xoá, readiness báo 503 ngay, client nhận `1001 server shutting down` sau ~5 giây (drain delay), pod biến mất trước 12 giây.
- **Rolling update**: `maxSurge: 1`, `maxUnavailable: 0`, `minReadySeconds: 10`. Mỗi pod cũ đóng 1001 cho socket của nó; client phải **bind lại có backoff ngẫu nhiên** (lấy token mới nếu token cũ quá 5 phút) để không dồn cả đàn một lúc. Đã kiểm: số endpoint sẵn sàng không xuống dưới 2 (với 2 replica), thay xong trong ~26 giây.
- **PodDisruptionBudget** `maxUnavailable: 1`: node drain gỡ từng pod một.
- **HPA** (cần metrics-server): bộ nhớ 70% và CPU 65% của request; scale XUỐNG rất thận trọng (cửa sổ 900s, mỗi 300s một pod) vì mỗi pod bị gỡ làm đứt socket của nó. Scale theo **số kết nối thật** cần đưa `ws_gateway.connections` vào Prometheus/KEDA: thư viện observability chỉ đẩy OTLP, không có endpoint scrape.
- **Bảo mật pod**: chạy UID 65532 (số; `USER nonroot` dạng tên không qua được `runAsNonRoot` của kubelet, đã sửa trong `Dockerfile`), `readOnlyRootFilesystem`, bỏ mọi capability, `seccomp RuntimeDefault`, không mount token ServiceAccount. `networkpolicy.yaml` (tùy chọn): chỉ cho vào từ ingress và ra tới DNS, Redis (cần CNI hỗ trợ).
- **Ingress**: cần `Upgrade` và timeout đọc/ghi dài hơn `FHIR_WS_HEARTBEAT` (ingress-nginx mặc định 60s sẽ cắt socket im lặng: đặt 3600s, xem `ingress-nginx.yaml`). Istio: **không** đặt `timeout` trên route (mặc định không giới hạn; Istio từ chối `0s`), `idleTimeout` upstream mặc định 1h, xem `istio.yaml`. Đặt `FHIR_WS_TRUSTED_PROXIES` đúng CIDR của pod ingress, nếu sai thì `FHIR_WS_MAX_CONNS_PER_IP` coi mọi client là một IP và chặn nhầm tất cả.

### 11.3 Metric (OTLP)

Bật `OTEL_METRICS_ENABLED=true` (mặc định thư viện observability tắt metric) và `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`; chu kỳ `OTEL_METRIC_EXPORT_INTERVAL` mặc định 60s. Instrument: `ws_gateway.connections`, `ws_gateway.bindings`, `ws_gateway.subscriptions` (gauge); `ws_gateway.messages.sent`, `ws_gateway.connections.slow` (người nhận chậm bị ngắt), `ws_gateway.messages.denied` (bỏ vì thiếu quyền hoặc hết hạn), `ws_gateway.events.received`, `ws_gateway.events.malformed` (tích lũy); `ws_gateway.redis.up` (1/0). Gợi ý cảnh báo: `redis.up == 0`; `connections` tiến gần `FHIR_WS_MAX_CONNECTIONS` (vượt thì 503); `connections.slow` tăng đều (client chậm hoặc tin quá lớn); `events.malformed > 0` (lệch phiên bản giữa fhir-server và gateway).

### 11.4 Hành vi khi sự cố (đã kiểm trên cụm thật)

| Sự cố | Hành vi |
|---|---|
| Redis chết | Mọi pod gateway chuyển NotReady sau ~13 giây (readiness kiểm Redis), **không restart** (liveness độc lập), mất khỏi Service nên không nhận kết nối mới; socket đang mở không bị cắt nhưng không nhận thêm sự kiện (sự kiện không bền, mất không bù). Redis lên lại thì pod Ready trong ~3-6 giây |
| Pod gateway bị xoá hoặc nâng cấp | Socket của pod đó nhận 1001; client bind lại bằng token mới (xem 11.2) |
| fhir-server restart/deploy | **Không ảnh hưởng socket**; sự kiện tiếp tục tới sau khi fhir-server lên lại |
| Gateway chết đột ngột (kill -9, node chết) | Mục hiện diện hết hạn sau `FHIR_WS_PRESENCE_TTL` (30s); fhir-server ngừng phát cho Subscription đó trong lúc chờ |
| Hết `FHIR_WS_MAX_CONNECTIONS` hoặc quá `FHIR_WS_MAX_CONNS_PER_IP` | 503 / 429; client thử lại có backoff |

Chưa kiểm thật: scale tự động của HPA (cụm thử không có metrics-server), thực thi NetworkPolicy, định tuyến qua ingress-nginx và Istio (manifest chỉ qua dry-run phía server), nhiều node.

### 11.5 FHIRcast (Hub đồng bộ ngữ cảnh giữa ứng dụng lâm sàng)

Chạy trong `ws-gateway` khi `FHIRCAST_ENABLED=true`; token do `auth` cấp. Chi tiết giao thức và cấu hình: [apps/ws-gateway/README.md](../apps/ws-gateway/README.md), [apps/ws-gateway/docs/config.md](../apps/ws-gateway/docs/config.md), [oauth.md](../apps/auth/docs/api-contract/oauth.md).

**Cấu hình hai phía (phải khớp):**

| `auth` | `ws-gateway` | Ghi chú |
|---|---|---|
| `AUTH_FHIRCAST_HUB_URL=https://<gateway>/fhircast/STU3` | `FHIRCAST_PUBLIC_URL=wss://<gateway>` | địa chỉ công khai: `hub.url` ở token và `hub.channel.endpoint` ở đăng ký |
| `AUTH_ISSUER` (mặc định `AUTH_BASE_URL`) | `FHIRCAST_JWT_ISSUER` | phải giống từng ký tự |
| audience của access token (`AUTH_AUDIENCE`, mặc định = issuer) | `FHIRCAST_JWT_AUDIENCE` | |
| `/.well-known/jwks.json` | `FHIRCAST_JWKS_URL` | https ở staging/production; **không** dùng `FHIRCAST_JWT_HMAC_SECRET` ngoài dev |
| `AUTH_REDIS_URL` (bắt buộc ở staging/production) | `REDIS_URL` | **hai Redis không cần chung**: `auth` chỉ nhớ topic của người dùng, gateway chỉ nhớ đăng ký và ngữ cảnh |

**Kiểm nhanh:**

```
# 1. đăng nhập xin scope fhircast/*: phản hồi token phải có hub.url và hub.topic
curl -s -X POST https://<auth>/oauth2/token -d grant_type=client_credentials -d client_id=... -d client_secret=... -d 'scope=fhircast/*' | jq '."hub.url", ."hub.topic"'
# 2. Hub công khai
curl -s https://<gateway>/fhircast/STU3/.well-known/fhircast-configuration
# 3. đăng ký (Bearer là access_token của bước 1)
curl -s -X POST https://<gateway>/fhircast/STU3 -H "Authorization: Bearer $TOKEN" -d hub.channel.type=websocket -d hub.mode=subscribe -d hub.topic=$TOPIC -d hub.events=Patient-open
```

**Sự cố thường gặp:**

| Triệu chứng | Nguyên nhân hay gặp | Cách xử lý |
|---|---|---|
| Token không có `hub.url`/`hub.topic` | scope đã cấp không có term `fhircast/`; hoặc `AUTH_FHIRCAST_HUB_URL` chưa đặt | xin scope `fhircast/*`; kiểm cấu hình `auth` |
| `auth` trả `500 server_error` khi xin scope `fhircast/*` | `auth` không lấy được topic (Redis của `auth` lỗi hoặc treo quá 2 giây); token thường vẫn cấp được | kiểm `AUTH_REDIS_URL`; log `không lấy được topic FHIRcast` |
| Hub trả `401` | token sai chữ ký/iss/aud/hết hạn; gateway không tải được JWKS lần đầu | kiểm issuer/audience hai phía và `FHIRCAST_JWKS_URL` truy cập được từ gateway |
| Hub trả `403` | token đúng nhưng thiếu scope `fhircast/*` hoặc không có claim project | xin lại scope; `FHIRCAST_REQUIRE_SCOPE=false` chỉ để chuyển tiếp ở local |
| Hai ứng dụng cùng người dùng nhận topic khác nhau | `auth` chạy nhiều instance mà không chung `AUTH_REDIS_URL`; hoặc quá 1 giờ không xin token (topic hết hạn) | đặt Redis chung (production đã bắt buộc); xin token lại |
| Socket tự đóng sau khoảng `FHIRCAST_LEASE` (1 giờ) | hết lease: nhận `denied` "lease expired" rồi đóng | đăng ký lại (`POST hub.url` với `hub.mode=subscribe`) |
| `denied` "invalid endpoint" ngay khi nối | endpoint không tồn tại, đã hủy hoặc đã hết hạn | đăng ký lại; endpoint là bí mật dùng một lần cho mỗi đăng ký |
| Socket bị đóng 1008 "too many connections for this endpoint" | quá `FHIRCAST_MAX_SOCKETS_PER_ENDPOINT` (4) socket cùng một endpoint trên một instance | mỗi ứng dụng nên đăng ký endpoint riêng |
| Socket bị đóng 1013 | client đọc quá chậm (hàng đợi đầy) hoặc Redis lỗi lúc đăng ký | client đọc nhanh hơn / thử lại có backoff |

Điều cần nhớ khi chạy nhiều replica: ngữ cảnh và đăng ký ở Redis nên mọi gateway thấy như nhau; hạn mức socket mỗi endpoint đếm **theo instance**. Chưa có số liệu OTLP riêng cho FHIRcast (`ws_gateway.*` chỉ tính websocket Subscription).

## 12. Cơ sở dữ liệu dùng chung: một database, mỗi service một schema

Kế hoạch và lý do: [docs/plans/microservices-shared-db.md](plans/microservices-shared-db.md) (D-DB-1, D-DB-2). `auth` và `fhir-server` dùng **cùng một database** PostgreSQL nhưng **schema và role riêng**; role này không có quyền gì trên schema của role kia (kể cả đọc) và không ai tạo được bảng ở `public`.

| Service | Role | Schema | `DATABASE_URL` (dev) |
|---|---|---|---|
| `auth` | `identity_app` | `identity` | `postgres://identity_app:<mk>@postgres:5432/trivita` |
| `fhir-server` | `fhir_app` | `fhir` | `postgres://fhir_app:<mk>@postgres:5432/trivita` |

`ws-gateway` không có DB. Hai app đều dùng cùng tên bảng (`fhir_resource`, `fhir_history`, `fhir_idx_*`) và cùng tên bảng phiên bản (`fhirstore_schema_version`): schema riêng là điều bắt buộc, không phải tùy chọn.

### 12.1 Khởi tạo

`deploy/postgres-init/01-schemas.sh` (chạy một lần khi volume Postgres khởi tạo lần đầu, qua `docker-entrypoint-initdb.d`) tạo hai role, hai schema, thu quyền của `PUBLIC` và đặt `search_path` ở mức **role** (không phụ thuộc DSN, đúng cả sau PgBouncer). Mật khẩu lấy từ `IDENTITY_DB_PASSWORD`, `FHIR_DB_PASSWORD` (mặc định chỉ để dev: `identity`, `fhir`; **đổi ở môi trường thật**, và mã hóa URL nếu có ký tự đặc biệt).

```
# volume đã có sẵn (script chạy lại được, không mất dữ liệu):
docker compose exec postgres sh /docker-entrypoint-initdb.d/01-schemas.sh
# dev muốn bắt đầu lại từ đầu:
docker compose down --volumes && docker compose up --build
```

**Dữ liệu cũ không tự chuyển.** Nếu trước đây `auth` dùng database `auth` hoặc cả hai app dùng schema `public`, dữ liệu đó vẫn nằm đó và app mới (schema riêng) sẽ thấy rỗng. Chuyển bằng quy trình ở mục 6 của kế hoạch (`COPY` từng bảng, kiểm số dòng và tổng kiểm, giữ bản cũ 7 ngày).

### 12.2 `DATABASE_REQUIRE_SCHEMA`

`DATABASE_REQUIRE_SCHEMA=true` (chỉ PostgreSQL; compose đã bật) làm app **từ chối khởi động** và từ chối cả `migrate` nếu `search_path` của kết nối không phải **đúng một** schema riêng đã tồn tại. Mặc định tắt để không phá cách chạy cũ (database riêng, schema `public`); ở staging/production không bật thì app ghi cảnh báo lúc khởi động. Bật cờ với SQLite hoặc MySQL là lỗi cấu hình (không có schema để kiểm).

| Thông báo lúc khởi động | Nguyên nhân | Cách xử lý |
|---|---|---|
| `search_path "\"$user\", public" có nhiều schema` | role chưa đặt `search_path` (mặc định) hoặc đăng nhập bằng superuser | dùng role của service; hoặc `ALTER ROLE <role> SET search_path = <schema>` |
| `search_path "identity, public" có nhiều schema` | còn `public` trong đường tìm kiếm: một bảng cùng tên ở `public` sẽ che bảng của service | bỏ `public` khỏi `search_path` |
| `search_path "public" không phải schema riêng` | trỏ thẳng vào `public` | đặt schema riêng |
| `schema "identity" ... chưa tồn tại (hoặc role không có quyền dùng nó)` | chưa chạy init, hoặc role không sở hữu/không có `USAGE` | chạy `01-schemas.sh`; kiểm quyền |
| `RequireSchema chỉ áp dụng cho PostgreSQL` | bật cờ với SQLite/MySQL | tắt cờ hoặc dùng PostgreSQL |

### 12.3 Kiểm tra trạng thái (không cần mã)

```
# mỗi schema phải có đủ bộ bảng và bảng phiên bản riêng; public không có bảng nào của service
psql -d trivita -c "select table_schema, count(*) from information_schema.tables where table_schema in ('identity','fhir','public') group by 1"
# role không thấy schema kia (kết quả mong đợi: permission denied)
PGPASSWORD=<mk> psql -U fhir_app -h <host> -d trivita -c "select 1 from identity.fhir_resource"
```

### 12.4 Số kết nối

Mỗi app mở tối đa `DATABASE_MAX_OPEN_CONNS` (mặc định 20) kết nối **tới cùng một máy chủ**: tổng của mọi replica của mọi service phải nhỏ hơn `max_connections` của PostgreSQL (mặc định 100) trừ phần dự phòng. Ví dụ 4 replica `fhir-server` + 2 replica `auth` ở mặc định là 120 kết nối, vượt giới hạn: hạ `DATABASE_MAX_OPEN_CONNS` hoặc đặt PgBouncer. **Chưa kiểm** hành vi của PgBouncer với `search_path`; vì vậy `search_path` được đặt ở mức role chứ không ở DSN.

### 12.5 Role migrate riêng (production, D-DB-4)

Mặc định (dev) mỗi service dùng **một** role sở hữu schema nên có cả DDL và tự migrate. Ở staging/production nên tách: một lỗ hổng SQL injection ở app không được phép `DROP`/`ALTER` bảng.

| Role | Quyền | Dùng để |
|---|---|---|
| `identity_migrate`, `fhir_migrate` | **sở hữu** schema và mọi bảng; chạy DDL | chỉ lệnh `migrate` ở bước deploy |
| `identity_app`, `fhir_app` | `USAGE` schema; `SELECT/INSERT/UPDATE/DELETE` trên mọi bảng và `USAGE/SELECT` trên sequence, **kể cả bảng và sequence do migration tương lai tạo** (`ALTER DEFAULT PRIVILEGES`); không DDL, không `TRUNCATE` | app đang chạy |

**Bật:** chạy `deploy/postgres-init/01-schemas.sh` với `DB_SEPARATE_MIGRATION_ROLES=true` và **đủ bốn mật khẩu** (`IDENTITY_DB_PASSWORD`, `FHIR_DB_PASSWORD`, `IDENTITY_MIGRATE_DB_PASSWORD`, `FHIR_MIGRATE_DB_PASSWORD`; chế độ này không có mật khẩu mặc định, thiếu là script dừng). Script chạy lại được trên database đã có: chuyển chủ sở hữu bảng sang role migrate bằng `REASSIGN OWNED` và giữ nguyên dữ liệu (đã kiểm). **Không** hỗ trợ chiều ngược lại (từ chế độ tách về một role).

**Cấu hình app:** `DATABASE_URL` là role `*_app`; `DATABASE_MIGRATE_URL` là role `*_migrate` (chỉ lệnh `migrate` dùng); `DATABASE_AUTO_MIGRATE=false` (**bắt buộc**: đặt `DATABASE_MIGRATE_URL` mà để tự migrate là lỗi cấu hình); `DATABASE_REQUIRE_SCHEMA=true`. Mỗi lần deploy: chạy `<app> migrate up` (bằng role migrate) **trước** khi rollout app mới.

```
# bước deploy (ví dụ), role migrate lấy từ DATABASE_MIGRATE_URL:
auth migrate up && fhir-server migrate up
# thử trên máy: docker-compose.db-roles.yml (hai tác vụ auth-migrate, fhir-migrate chạy trước app)
IDENTITY_DB_PASSWORD=... FHIR_DB_PASSWORD=... IDENTITY_MIGRATE_DB_PASSWORD=... FHIR_MIGRATE_DB_PASSWORD=... FHIR_JOB_SIGNING_KEY=... \
docker compose -f docker-compose.yml -f docker-compose.auth.yml -f docker-compose.db-roles.yml up --build fhir-server auth postgres
```

Trên K8s, `migrate up` chạy như một `Job` (hoặc `initContainer` chỉ với một replica) dùng cùng image với `DATABASE_MIGRATE_URL` lấy từ Secret của role migrate; Deployment của app **không** mount Secret đó. (Chưa kiểm trên cụm thật; chỉ kiểm bằng compose và test.)

| Triệu chứng | Nguyên nhân | Xử lý |
|---|---|---|
| App báo `schema chưa được migrate` | chưa chạy `migrate up`, hoặc chạy trước khi script init chuyển quyền | chạy `<app> migrate up`; thông báo này là đúng ở chế độ tách (trước đây là `permission denied` khó hiểu) |
| `failed to create version table ... permission denied for schema` khi khởi động | app đang để `DATABASE_AUTO_MIGRATE=true` nhưng role app không có DDL | đặt `DATABASE_AUTO_MIGRATE=false` và migrate bằng role migrate |
| `DATABASE_MIGRATE_URL: đã dùng role migrate riêng thì app không tự migrate` | đặt `DATABASE_MIGRATE_URL` mà `DATABASE_AUTO_MIGRATE` còn `true` | đặt `DATABASE_AUTO_MIGRATE=false` |
| Migration tương lai tạo bảng mà app báo `permission denied for table` | bảng không do role migrate tạo (ví dụ tạo bằng superuser) nên không được cấp quyền mặc định | tạo bảng bằng role migrate, hoặc `GRANT SELECT, INSERT, UPDATE, DELETE ON <bảng> TO <role>_app` |

### 12.6 `statement_timeout` theo role

Script init chỉ đặt khi có biến `IDENTITY_STATEMENT_TIMEOUT` hoặc `FHIR_STATEMENT_TIMEOUT` (ví dụ `30s`); để trống thì **không** đặt. Cố ý không có giá trị mặc định: `$reindex` và bulk export của `fhir-server` chạy dưới role `fhir_app` và có thể chạy lâu, một timeout chưa đo sẽ làm chúng hỏng. Đo thời gian truy vấn thật rồi mới đặt (nên đặt cho `identity_app` trước vì truy vấn của `auth` ngắn); role migrate không bao giờ bị đặt timeout.

### 12.7 Còn lại

Chưa kiểm PgBouncer; chưa diễn tập khôi phục từng schema từ backup; chưa đo dung lượng dữ liệu thật để ước lượng thời gian dừng khi chuyển dữ liệu từ database cũ (P4); chưa chạy `migrate up` bằng `Job` trên K8s thật.
