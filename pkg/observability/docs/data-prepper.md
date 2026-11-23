# Data Prepper và OpenSearch

Tài liệu tham khảo khi vận hành Data Prepper cho các service dùng `trivita-observability-go` (và bản Python `trivita-observability`: hai library dùng chung port, endpoint, pipeline và cấu trúc document). Mọi kết quả ở đây đã được kiểm chứng với **Data Prepper 2.16.0** và **OpenSearch 2.19.1** bằng integration test của bản Python (xem phần [Kiểm tra lại](#kiểm-tra-lại)).

## Port và endpoint

| Tín hiệu | Source của Data Prepper | Port | Endpoint mặc định của library |
|---|---|---|---|
| Trace | `otlp_traces` | 21890 | `grpc://localhost:21890` |
| Log | `otlp_logs` | 21892 | `grpc://localhost:21892` |
| Metrics | `otlp_metrics` | 21891 | `grpc://localhost:21891` |

Scheme của endpoint quyết định giao thức, áp dụng giống nhau cho cả ba tín hiệu:

| Scheme | Giao thức |
|---|---|
| `grpc://host:port` | OTLP/gRPC, không TLS |
| `grpcs://host:port` | OTLP/gRPC có TLS |
| `http(s)://host:port/path` | OTLP/HTTP protobuf (ghi đầy đủ path) |

Data Prepper nhận mỗi loại tín hiệu ở một port riêng. Library có endpoint mặc định riêng cho từng loại, và **không bao giờ** dùng chung endpoint của trace cho log hay metrics.

## gRPC hay OTLP/HTTP

Kết quả gửi thử bằng exporter OTLP thật. "OK" nghĩa là exporter nhận phản hồi thành công **và** bản ghi thực sự xuất hiện ở sink.

| Cấu hình source | gRPC | OTLP/HTTP được nhận tại |
|---|---|---|
| Mặc định | OK | **Không path nào**: `/opentelemetry/v1/*` và `/v1/*` trả 404, `/...Service/Export` trả 415 |
| `unframed_requests: true` | OK | `/opentelemetry.proto.collector.trace.v1.TraceService/Export`, `/opentelemetry.proto.collector.logs.v1.LogsService/Export` |
| `unframed_requests: true` + `path: "/v1/traces"` | **Hỏng** | Chỉ path đã khai báo |
| `http_path` (của `otlp_logs`) | OK | Không mở thêm path nào |

Hệ quả:
- **Dùng `grpc://`** khi gửi tới Data Prepper tự host. Endpoint mặc định của SDK OpenSearch, `http://localhost:21890/opentelemetry/v1/traces`, **không chạy** với bất kỳ cấu hình nào ở trên.
- Chỉ dùng OTLP/HTTP khi giữa service và Data Prepper có proxy hoặc load balancer không chuyển tiếp được gRPC. Khi đó bật `unframed_requests: true` trên source, và dùng path dạng `...Service/Export`.
- `path:` thay luôn route của gRPC, nên chỉ đặt khi **mọi** client đều gửi qua HTTP.

## Ví dụ biến môi trường của service

```env
OTEL_ENABLED=true
OTEL_SERVICE_NAME=billing-api
OTEL_SERVICE_VERSION=1.4.2
OTEL_SERVICE_NAMESPACE=trivita
ENVIRONMENT=staging
OTEL_EXPORTER_OTLP_TRACES_ENDPOINT=grpc://data-prepper:21890
OTEL_TRACES_SAMPLE_RATIO=0.2
OTEL_LOGS_ENABLED=true
OTEL_EXPORTER_OTLP_LOGS_ENDPOINT=grpc://data-prepper:21892
OTEL_LOGS_LEVEL=INFO
OTEL_METRICS_ENABLED=true
OTEL_EXPORTER_OTLP_METRICS_ENDPOINT=grpc://data-prepper:21891
LOG_LEVEL=INFO
LOG_JSON=true
```

## `pipelines.yaml`

Dùng [`deploy/data-prepper/pipelines.yaml`](../deploy/data-prepper/pipelines.yaml). Integration test chạy chính file này với container thật, và yêu cầu span, log và metric cùng xuất hiện trong OpenSearch trong vòng 30 giây. Hãy sao chép file đó thay vì tự viết pipeline.

### Các lỗi hay gặp

Mỗi lỗi dưới đây hoặc làm Data Prepper không khởi động được, hoặc làm mất dữ liệu mà không báo rõ:

| Lỗi | Biểu hiện | Cách sửa |
|---|---|---|
| `password: "${OPENSEARCH_PASSWORD}"` | Sink retry mãi với `Unauthorized access`, source không bao giờ mở | Data Prepper 2.16 **không thay biến môi trường** trong pipeline, chỉ hỗ trợ `${{aws_secrets:<id>:<key>}}`. Hãy mount cả file dưới dạng secret |
| Sink https thiếu `cert:` (hoặc `insecure:`) | `PKIX path building failed`, source không bao giờ mở | `cert: <CA của chứng chỉ HTTP của OpenSearch>`. `insecure: true` chỉ dùng cho môi trường dev |
| Để `flush_timeout` của sink ở mặc định (60000 ms) | Mọi span, log, metric chỉ tìm thấy được sau khoảng 60 giây (đã đo; gọi `_refresh` không giúp gì) | `flush_timeout: 5000`, độ trễ còn khoảng 6–10 giây |
| Sink metrics dùng `index_type: metric-analytics` | Index được tạo nhưng luôn trống. Log Data Prepper: `Document failed to write … 400 … KeywordFieldMapper cannot be cast to ObjectMapper`. Exporter phía service vẫn báo thành công | `index_type: metric-analytics-plain` |
| Log ghi vào index tự đặt tên (`index:`) | Không có index template và ISM, không phải index mà Observability dùng | `index_type: log-analytics` (ghi vào `logs-otel-v1-*`) |
| Dùng `otel_trace_source` / `otel_logs_source` / `otel_metrics_source` | Cảnh báo deprecated trong 2.16 | `otlp_traces` / `otlp_logs` / `otlp_metrics` (cùng tuỳ chọn, cùng port) |

Những phần vẫn đúng như tài liệu cũ: processor `otel_traces`, `service_map`, `otel_metrics`; `index_type` `trace-analytics-raw` (ghi vào `otel-v1-apm-span-*`) và `trace-analytics-service-map` (ghi vào `otel-v1-apm-service-map`).

Các giá trị `index_type` hợp lệ trong 2.16: `trace-analytics-raw`, `trace-analytics-plain-raw`, `trace-analytics-service-map`, `otel-v2-apm-service-map`, `log-analytics`, `log-analytics-plain`, `metric-analytics`, `metric-analytics-plain`, `tsdb`, `custom`, `management_disabled`.

### Source chỉ mở sau khi mọi sink khởi tạo xong

Nếu một sink không kết nối hoặc không xác thực được với OpenSearch, Data Prepper **không mở port OTLP**, và phía service chỉ thấy lỗi gRPC `UNAVAILABLE`. Khi đó hãy xem log Data Prepper và tìm `Failed to initialize OpenSearch sink` trước.

## Cấu trúc document trong OpenSearch

Trong tên attribute, dấu `.` được đổi thành `@`.

**Span** (`otel-v1-apm-span-*`):
- `traceId`, `spanId`, `name` (ví dụ `GET /orders/:id` với Echo, `GET /orders/{order_id}` với FastAPI), `kind` (`SPAN_KIND_SERVER`), `serviceName`, `traceGroup`, `durationInNanos`.
- `resource.attributes.service@namespace`, `resource.attributes.deployment@environment`.
- Attribute HTTP (semantic convention ổn định, mặc định): `span.attributes.http@route`, `span.attributes.http@request@method`, `span.attributes.http@response@status_code`, `span.attributes.url@path`, `span.attributes.url@scheme`, `span.attributes.client@address`.

**Log** (`logs-otel-v1-*`):
- `traceId`, `spanId`, `severityText`, `body`, `serviceName`.
- Attribute truyền vào `slog` (ví dụ `"order_id", 123`) nằm ở `log.attributes.<key>`. Bản Python: field truyền qua `extra=`.

**Metric** (`metrics-otel-v1-*`, template `metric-analytics-plain`):
- `name`: field text, nên truy vấn bằng `match_phrase` chứ không dùng `term`.
- `kind` (`HISTOGRAM`, `SUM`, `GAUGE`), `serviceName`, `unit`, `aggregationTemporality` (cumulative).
- Histogram có `count`, `sum`, `bucketCounts`, `explicitBounds`; sum và gauge có `value`.
- `metric.attributes.<key>` (ví dụ `metric.attributes.http@route`), `resource.attributes.*`.
- `exemplars[].traceId` và `exemplars[].spanId` trỏ về trace đã tạo ra số đo.

## Xem dữ liệu

- **Trace**: OpenSearch Dashboards → Observability → Traces, nhóm theo `service.name`. Service map cần có `service-map-pipeline`.
- **Log**: lọc `logs-otel-v1-*` theo `traceId` lấy từ một span để xem toàn bộ log của một request.
- **Metrics**: lọc `metrics-otel-v1-*` theo `serviceName` và `name`.

Giao diện OpenSearch Dashboards chưa được kiểm chứng tự động; integration test chỉ kiểm tra dữ liệu trong index.

## Kiểm tra lại

Integration test với container Data Prepper và OpenSearch thật nằm ở repo Python (`RUN_INTEGRATION=1 uv run pytest tests/integration` trong `trivita-observability`), và chạy chính file `deploy/data-prepper/pipelines.yaml` giống hệt file ở repo này. Phía Go, `export_test.go` kiểm tra exporter gửi đúng OTLP/gRPC và OTLP/HTTP (đúng path `...Service/Export`, đúng `traceId` và resource) tới một receiver dựng trong process.

Khi nâng cấp Data Prepper hoặc OpenSearch, chạy lại integration test của repo Python, rồi cập nhật `pipelines.yaml` và tài liệu này ở **cả hai** repo.
