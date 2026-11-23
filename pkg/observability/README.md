# trivita-observability-go

Bản Go của [`trivita-observability`](../trivita-observability) (Python/FastAPI), dùng cho các service Go viết bằng [Echo](https://echo.labstack.com/). Cùng biến môi trường, cùng định dạng log, cùng resource attributes và cùng pipeline Data Prepper, nên service Go và Python hiển thị giống nhau trong OpenSearch.

- **Tracing**: mỗi request một span SERVER, gửi qua OTLP tới Data Prepper rồi vào OpenSearch Trace Analytics.
- **Logging**: log JSON ra stdout (luôn bật) qua `log/slog`, và đẩy log qua OTLP (tuỳ chọn). Mỗi dòng log ghi kèm context của request mang `trace_id`/`span_id`.
- **Metrics** (tuỳ chọn): `http.server.request.duration`, `http.server.active_requests`, `http.server.response.body.size`, metric HTTP client và metric do app tự định nghĩa, kèm exemplar trỏ về trace.
- **Worker / job queue**: trace của job nối tiếp trace của request đã enqueue nó (asynq, NATS, Kafka, cron, ...).

Mặc định library **không làm gì** ngoài cấu hình log stdout. Tracing chỉ chạy khi `OTEL_ENABLED=true`; OTLP log và metrics còn cần bật thêm `OTEL_LOGS_ENABLED` và `OTEL_METRICS_ENABLED`.

## Cài đặt

Yêu cầu Go ≥ 1.26 (theo `go.mod`).

```bash
go get github.com/TRIVITAAI/trivita-observability-go
```

| Package | Dùng cho |
|---|---|
| `github.com/TRIVITAAI/trivita-observability-go` | Lõi: `Setup`, `Shutdown`, `Logger`, `Tracer`, `Meter`, HTTP client, helper cho job |
| `.../echoobs` | Echo **v5** (`github.com/labstack/echo/v5`) |
| `.../echo4obs` | Echo **v4** (`github.com/labstack/echo/v4`) |

## Dùng với Echo

```go
package main

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/labstack/echo/v5"

	observability "github.com/TRIVITAAI/trivita-observability-go"
	"github.com/TRIVITAAI/trivita-observability-go/echoobs"
)

func main() {
	ctx := context.Background()
	e := echo.New()
	if err := echoobs.Setup(ctx, e, observability.FromEnv()); err != nil {
		slog.Error("observability setup failed", "error", err) // service vẫn chạy tiếp
	}
	defer observability.Shutdown(0) // flush span, log, metrics; tối đa OTEL_SHUTDOWN_TIMEOUT_MS

	e.GET("/orders/:id", getOrder)
	_ = http.ListenAndServe(":8080", e)
}
```

Với Echo v4 thay `echoobs` bằng `echo4obs`; cách dùng giống hệt. Ví dụ đầy đủ (graceful shutdown, HTTP client, metric tự định nghĩa): [`examples/echo`](examples/echo/main.go).

`echoobs.Setup` sẽ:

1. Gọi `observability.Setup`: cấu hình log stdout (JSON hoặc text, theo `LOG_JSON`) làm logger mặc định của `slog`. Log của package `log` chuẩn cũng đi qua đây.
2. Đặt `e.Logger` ghi qua cùng handler, nên log của Echo và các middleware (Recover, ...) cũng là JSON và được export.
3. Đăng ký `echoobs.Middleware()`. Hãy gọi `Setup` **trước** khi `e.Use(...)` các middleware khác, để span SERVER bao trùm chúng.
4. Nếu `OTEL_ENABLED=true`: tạo `TracerProvider` toàn cục, export span qua OTLP, đặt propagator W3C (`traceparent`, `baggage`).
5. Nếu `OTEL_LOGS_ENABLED=true`: thêm handler đẩy log qua OTLP, dùng chung resource với trace (`service.name`, `service.namespace`, `deployment.environment`).
6. Nếu `OTEL_METRICS_ENABLED=true`: tạo `MeterProvider` với cùng resource, gửi theo chu kỳ.

Middleware:

- Tạo một span SERVER cho mỗi request, tên `<METHOD> <route>` theo route template của Echo, ví dụ `GET /orders/:id`. Request không khớp route (404/405) chỉ có tên `GET`, để URL lạ không làm phình số tên span.
- Lỗi handler trả về được đưa qua `HTTPErrorHandler` của Echo ngay trong middleware, nên status code ghi lại là status client thật sự nhận. Status ≥ 500 và panic đánh dấu span `ERROR`; 4xx thì không.
- Request khớp `OTEL_EXCLUDED_URLS` (mặc định `health,metrics`) không có span, không có metric, không có access log.
- Ghi một dòng access log (`logger=http.access`) cho mỗi request, kèm `trace_id`. Tắt bằng `LOG_ACCESS=false`.

Gọi `Setup` nhiều lần vẫn an toàn: provider chỉ được tạo một lần cho mỗi process, lần sau chỉ thay handler stdout.

`observability.Shutdown(timeout)` flush log, metrics và trace **song song**, và luôn trả về trong `timeout` (`0` nghĩa là dùng `OTEL_SHUTDOWN_TIMEOUT_MS`, mặc định 5000 ms), kể cả khi Data Prepper không truy cập được. Phần chưa gửi kịp bị bỏ, hàm trả về `false` kèm log cảnh báo. Giữ giá trị này nhỏ hơn `terminationGracePeriodSeconds` của k8s trừ thời gian graceful shutdown của HTTP server.

### Cấu hình trong code

`FromEnv()` đọc biến môi trường; sửa field sau đó để ghi đè:

```go
s := observability.FromEnv()
s.ServiceName = "billing-api"
s.ExcludedURLs = []string{"health", "metrics", "swagger"}
err := echoobs.Setup(ctx, e, s)
```

Dùng `observability.DefaultSettings()` nếu không muốn đọc env. Field để trống (`""`, `0`) được thay bằng giá trị mặc định.

### Ghi log

Dùng `log/slog`. **Truyền context của request** (`InfoContext`, `ErrorContext`, ...) để dòng log có `trace_id`/`span_id`; trong Echo đó là `c.Request().Context()`:

```go
var logger = observability.Logger("app.orders") // tạo ở cấp package được, kể cả trước Setup

func getOrder(c *echo.Context) error {
	ctx := c.Request().Context()
	logger.InfoContext(ctx, "order created", "order_id", 123, "amount", 49.9)
	...
}
```

```json
{"timestamp":"2026-10-08T11:02:03.120+00:00","level":"INFO","message":"order created","service":"billing-api","environment":"staging","logger":"app.orders","order_id":123,"amount":49.9,"trace_id":"4bf92f3577b34da6a3ce929d0e0e4736","span_id":"00f067aa0ba902b7"}
```

Field giống bản Python: `timestamp` (UTC, ms), `level` (`DEBUG`/`INFO`/`WARNING`/`ERROR`/`CRITICAL`), `message`, `service`, `environment`, `logger` (khi dùng `observability.Logger(name)`), `trace_id`/`span_id`, cùng mọi attribute truyền vào. Ghi lỗi bằng `"error", err`.

### Middleware cho log (PII, context)

`slog.Handler` middleware truyền qua `WithLogMiddleware` được áp dụng cho **cả** stdout lẫn OTLP, theo thứ tự truyền vào:

```go
echoobs.Setup(ctx, e, s, observability.WithLogMiddleware(redactPII, addTenant))
// type LogMiddleware func(next slog.Handler) slog.Handler
```

### Tạo span thủ công

```go
ctx, span := observability.Tracer("app.invoices").Start(ctx, "calculate-invoice")
defer span.End()
span.SetAttributes(attribute.Int("invoice.items", 12))
```

### Gọi HTTP ra ngoài

```go
var client = observability.HTTPClient() // hoặc &http.Client{Transport: observability.HTTPTransport(base)}

req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil) // ctx của request
resp, err := client.Do(req)
```

Mỗi request có span CLIENT, metric HTTP client, và header `traceparent` để service phía sau nối tiếp trace.

### Database, Redis, gRPC

Go không có monkey-patching, nên instrumentation của thư viện client phải được gắn khi tạo client. `Setup` đặt `TracerProvider`, `MeterProvider` và propagator **toàn cục**, nên các instrumentation chính thức dùng được ngay, không cần truyền provider. Luôn truyền `ctx` của request vào các lời gọi.

| Thư viện | Instrumentation |
|---|---|
| `database/sql` | [`github.com/XSAM/otelsql`](https://github.com/XSAM/otelsql): `otelsql.Open(...)` |
| `pgx/v5` | [`github.com/exaring/otelpgx`](https://github.com/exaring/otelpgx): `cfg.ConnConfig.Tracer = otelpgx.NewTracer()` |
| `go-redis/v9` | `github.com/redis/go-redis/extra/redisotel/v9`: `redisotel.InstrumentTracing(rdb)` |
| gRPC | `go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc`: `grpc.WithStatsHandler(otelgrpc.NewClientHandler())` |

Ở worker, đừng gắn tracing cho client Redis mà queue dùng để polling: mỗi lần poll sinh một span và che mất trace của job.

### Metrics

Bật bằng `OTEL_METRICS_ENABLED=true`, cần có `OTEL_ENABLED=true`.

```go
var ordersCreated, _ = observability.Meter("app.orders").Int64Counter("orders.created",
	metric.WithUnit("1"), metric.WithDescription("Orders created"))

func createOrder(ctx context.Context) {
	ordersCreated.Add(ctx, 1, metric.WithAttributes(attribute.String("channel", "web")))
}
```

- Instrument tạo ở cấp package (trước `Setup`) vẫn hoạt động: khi setup bật metrics, chúng được tự gắn vào provider thật.
- Khi metrics tắt, `Meter` trả về meter không làm gì, nên code không cần kiểm tra.
- Truyền `ctx` của request khi ghi số đo để có exemplar trỏ về trace.
- Mặc định gửi mỗi 60 giây (`OTEL_METRIC_EXPORT_INTERVAL`), và gửi lần cuối khi gọi `Shutdown`.
- Không dùng giá trị không giới hạn (user id, order id, URL thô) làm attribute.

Semantic convention HTTP chọn bằng `OTEL_SEMCONV_STABILITY_OPT_IN` (hoặc `Settings.Semconv`), giống bản Python:

| Giá trị | Kết quả |
|---|---|
| `http` (mặc định) | Tên ổn định: `http.server.request.duration` (giây), `http.route`, `http.request.method`, `http.response.status_code`, `url.path` |
| `http/dup` | Cả tên cũ lẫn tên mới, để chuyển dần dashboard |
| chuỗi rỗng | Tên cũ: `http.server.duration` (ms), `http.target`, `http.method`, `http.status_code` |

Khác bản Python: route template của Echo dùng `:id` (`/orders/:id`), còn FastAPI dùng `{order_id}`.

## Dùng với worker và job queue

Worker không có Echo, chỉ cần `observability.Setup(ctx, observability.FromEnv())` và `defer observability.Shutdown(0)`.

Library không phụ thuộc vào queue nào. Hai helper sau nối trace từ API sang worker, tương đương `traced_enqueue` / `traced_jobs` của bản Python:

- `observability.Enqueue(ctx, system, queue, name, fn)`: tạo span `enqueue <name>` (PRODUCER) và đưa `traceparent` của span đó cho `fn` để lưu cùng job.
- `observability.RunJob(ctx, job, fn)`: chạy `fn` trong span `job <name>` (CONSUMER) là con của span enqueue, có `messaging.message.id` và `<system>.job.try`. Lỗi được ghi vào span (trạng thái `ERROR`) rồi trả về nguyên vẹn; panic được ghi rồi panic lại. Lỗi mà `Job.IsRetry` coi là yêu cầu retry chỉ thêm event `<system>.retry_requested`.

Ví dụ với [asynq](https://github.com/hibiken/asynq), lưu `traceparent` trong payload:

```go
type EmailPayload struct {
	UserID      int64  `json:"user_id"`
	Traceparent string `json:"traceparent,omitempty"`
}

// Phía API
err := observability.Enqueue(ctx, "asynq", "default", "send_email",
	func(ctx context.Context, traceparent string) (string, error) {
		payload, _ := json.Marshal(EmailPayload{UserID: userID, Traceparent: traceparent})
		info, err := client.EnqueueContext(ctx, asynq.NewTask("send_email", payload))
		if err != nil {
			return "", err
		}
		return info.ID, nil
	})

// Phía worker
mux.HandleFunc("send_email", func(ctx context.Context, t *asynq.Task) error {
	var p EmailPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return err
	}
	id, _ := asynq.GetTaskID(ctx)
	retried, _ := asynq.GetRetryCount(ctx)
	return observability.RunJob(ctx, observability.Job{
		System: "asynq", Name: t.Type(), ID: id, Try: retried + 1, Traceparent: p.Traceparent,
	}, func(ctx context.Context) error {
		return sendEmail(ctx, p.UserID) // ctx mang span của job
	})
})
```

Kết quả trên OpenSearch là một trace liền mạch: `POST /signup` → `enqueue send_email` → `job send_email` → các span DB/HTTP bên trong job. Job enqueue không qua `Enqueue` vẫn có span, nhưng bắt đầu trace mới.

Cách thủ công cho queue khác: `observability.Traceparent(ctx)` lấy chuỗi `traceparent` để gửi kèm, `observability.ContinueTrace(ctx, traceparent)` trả về context nối tiếp trace đó. Tên field quy ước là `observability.TraceparentKey` (`"traceparent"`).

## Biến môi trường

Giống bản Python, trừ `OTEL_EXCLUDED_URLS` và `LOG_ACCESS`.

| Biến | Mặc định | Ý nghĩa |
|---|---|---|
| `OTEL_ENABLED` | `false` | Công tắc chính cho tracing, OTLP log và metrics |
| `OTEL_SERVICE_NAME` | `unknown-service` | `service.name` |
| `OTEL_SERVICE_VERSION` | `0.0.0` | `service.version` |
| `OTEL_SERVICE_NAMESPACE` | `trivita` | `service.namespace` |
| `ENVIRONMENT` | `local` | `deployment.environment` |
| `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` | `grpc://localhost:21890` | Endpoint nhận trace |
| `OTEL_EXPORTER_OTLP_HEADERS` | — | Header gửi kèm, dạng `k1=v1,k2=v2` |
| `OTEL_TRACES_SAMPLE_RATIO` | `1.0` | Tỉ lệ lấy mẫu trace (parent-based), từ 0 đến 1 |
| `OTEL_BSP_SCHEDULE_DELAY` | `5000` | Chu kỳ gửi batch span và log, tính bằng ms |
| `OTEL_BSP_MAX_EXPORT_BATCH_SIZE` | `512` | Số span hoặc log tối đa mỗi batch |
| `OTEL_EXCLUDED_URLS` | `health,metrics` | Regex, khớp ở bất kỳ vị trí nào trong request URI. Bản Python dùng `OTEL_PYTHON_FASTAPI_EXCLUDED_URLS` |
| `OTEL_SEMCONV_STABILITY_OPT_IN` | `http` | `http` (ổn định), `http/dup` (cả hai), rỗng (cũ) |
| `OTEL_SHUTDOWN_TIMEOUT_MS` | `5000` | Thời gian tối đa của `Shutdown` |
| `OTEL_LOGS_ENABLED` | `false` | Bật đẩy log qua OTLP |
| `OTEL_EXPORTER_OTLP_LOGS_ENDPOINT` | `grpc://localhost:21892` | Endpoint nhận log (không bao giờ dùng chung endpoint của trace) |
| `OTEL_LOGS_LEVEL` | `INFO` | Level tối thiểu của log đẩy qua OTLP |
| `OTEL_METRICS_ENABLED` | `false` | Bật gửi metrics qua OTLP |
| `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` | `grpc://localhost:21891` | Endpoint nhận metrics |
| `OTEL_METRIC_EXPORT_INTERVAL` | `60000` | Chu kỳ gửi metrics, tính bằng ms |
| `LOG_LEVEL` | `INFO` | Level của log stdout (và cũng là sàn của OTLP) |
| `LOG_JSON` | `true` | `true` là JSON, `false` là text |
| `LOG_ACCESS` | `true` | Ghi access log cho mỗi request |

Biến OTel chuẩn đã có trong môi trường (`OTEL_TRACES_SAMPLER`, `OTEL_TRACES_SAMPLER_ARG`, `OTEL_BSP_*`, `OTEL_RESOURCE_ATTRIBUTES`) luôn được ưu tiên hơn giá trị của library. Library không ghi gì vào `os.Environ`.

Scheme của endpoint quyết định giao thức, áp dụng cho cả ba tín hiệu:

| Scheme | Giao thức |
|---|---|
| `grpc://host:port` | OTLP/gRPC, không TLS |
| `grpcs://host:port` | OTLP/gRPC có TLS |
| `http(s)://host:port/path` | OTLP/HTTP protobuf (ghi đầy đủ path) |

Với Data Prepper hãy dùng `grpc://`: với cấu hình mặc định, source chỉ nhận gRPC. Cấu hình Data Prepper đã kiểm chứng nằm ở [`deploy/data-prepper/pipelines.yaml`](deploy/data-prepper/pipelines.yaml) (giống hệt bản Python); các lỗi hay gặp và cấu trúc document trong OpenSearch: [docs/data-prepper.md](docs/data-prepper.md).

## Xử lý sự cố

| Triệu chứng | Nguyên nhân thường gặp |
|---|---|
| Không có dữ liệu nào | `OTEL_ENABLED` chưa bật; hoặc process thoát mà chưa gọi `observability.Shutdown` |
| Log không có `trace_id` | Ghi log bằng `slog.Info(...)` thay vì `slog.InfoContext(ctx, ...)`, hoặc dùng `context.Background()` thay vì `c.Request().Context()` |
| Span DB/HTTP/Redis không nằm trong trace của request | Chưa truyền `ctx` của request vào lời gọi, hoặc client chưa được gắn instrumentation (bảng "Database, Redis, gRPC") |
| Span SERVER không bao trùm các middleware khác | `e.Use(...)` được gọi trước `echoobs.Setup` |
| Log cảnh báo `Observability shutdown exceeded ... ms` | Backend chậm hoặc chết lúc tắt; phần dữ liệu cuối bị bỏ |
| Log `OpenTelemetry export pipeline error` ở stdout | Exporter không gửi được (sai host/port, sai scheme, sink Data Prepper chưa khởi tạo). Các log này **không** được export qua OTLP để tránh vòng lặp |
| Exporter báo HTTP 404 hoặc 415 | Dùng `http://` trong khi source Data Prepper chưa bật `unframed_requests`; chuyển sang `grpc://` |
| Có trace nhưng không có log | `OTEL_EXPORTER_OTLP_LOGS_ENDPOINT` trỏ sai port (log là 21892), hoặc `OTEL_LOGS_ENABLED` chưa bật |
| Metric tự định nghĩa không có dữ liệu | `OTEL_METRICS_ENABLED` chưa bật, hoặc chưa tới chu kỳ gửi (60 giây) và process chưa gọi `Shutdown` |

## Phát triển

```bash
go test -race ./...
```

Test không cần backend: span, log và metrics được ghi vào bộ nhớ qua `WithSpanProcessor`, `WithLogProcessor`, `WithMetricReader` (các option này cũng dùng được trong test của service). `export_test.go` dựng một OTLP receiver gRPC và HTTP ngay trong process để kiểm tra exporter thật.

```
.
├── observability.go   # Setup, Shutdown, Tracer, Meter, Option
├── config.go          # Settings, FromEnv, DefaultSettings
├── exporters.go       # chọn exporter gRPC/HTTP theo scheme của endpoint
├── tracing.go         # Resource, TracerProvider, sampler, batch
├── logging.go         # handler stdout JSON, handler OTLP, Logger(name)
├── httpserver.go      # span SERVER, metric HTTP server, access log (dùng chung cho mọi router)
├── httpclient.go      # HTTPTransport, HTTPClient
├── jobs.go            # Traceparent, ContinueTrace, Enqueue, RunJob
├── echoobs/           # Echo v5
├── echo4obs/          # Echo v4
└── examples/echo/     # service mẫu
```

Quy tắc khi sửa library: [docs/CONTRIBUTING.md](docs/CONTRIBUTING.md).
