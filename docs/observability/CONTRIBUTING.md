# Đóng góp cho trivita-observability-go

Tài liệu dành cho người sửa code của library. Cách dùng xem [README](../../pkg/observability/README.md); vận hành Data Prepper xem [data-prepper.md](data-prepper.md). Library này là bản Go của `trivita-observability` (Python): yêu cầu chức năng và quyết định thiết kế gốc nằm trong `docs/SPEC.md` của repo Python. Khi đổi hành vi dùng chung (tên biến môi trường, field log JSON, resource attributes, endpoint mặc định), hãy đổi ở cả hai repo.

## Môi trường phát triển

```bash
go test -race ./...
go vet ./...
```

## Quy tắc bắt buộc

1. **Biến môi trường và field log JSON giống bản Python.** Service Go và Python chạy chung một cluster, chung dashboard. Chỉ khác ở chỗ bắt buộc phải khác: `OTEL_EXCLUDED_URLS` (Python dùng tên riêng của FastAPI) và `LOG_ACCESS` (Python để uvicorn lo access log). Đổi tên biến hay tên field là thay đổi phá vỡ tương thích.

2. **Endpoint mặc định là `grpc://`, mỗi tín hiệu một port** (trace 21890, metrics 21891, log 21892), và log/metrics **không** fallback về endpoint của trace. Data Prepper với cấu hình mặc định chỉ nhận gRPC, và mỗi source nghe một port riêng.

3. **Không ghi vào `os.Environ`.** Go SDK nhận cấu hình qua option, nên không cần mẹo `temporary_env` như bản Python. Để biến OTel chuẩn trong môi trường thật thắng settings (`OTEL_TRACES_SAMPLER*`, `OTEL_BSP_*`), chỉ truyền option khi biến đó **không có** (`os.LookupEnv`), vì option của SDK luôn thắng env.

4. **Không export log của chính pipeline export.** Lỗi của SDK và exporter đi qua `otel.SetErrorHandler`, được ghi bằng `pipelineLogger` (chỉ stdout). Không bao giờ chuyển chúng sang handler có OTLP: khi backend chết, log báo lỗi export sẽ tự sinh thêm lỗi export.

5. **Mỗi process chỉ có một provider cho mỗi tín hiệu, và `Setup` gọi lại được.** Trạng thái nằm trong `st` (khoá bởi `mu`); đường xử lý request chỉ đọc snapshot `rt` (atomic). Lần `Setup` sau dùng lại provider và chỉ thay handler stdout. `Shutdown` xoá trạng thái **trước** khi gọi `provider.Shutdown`, để một lần shutdown quá hạn không ảnh hưởng lần setup sau.

6. **Logger luôn trỏ tới handler hiện hành.** `Logger(name)` và logger mặc định của `slog` dùng `dynamicHandler`, đọc `currentHandler` mỗi lần ghi (có cache theo thế hệ). Vì vậy logger tạo ở cấp package, trước `Setup`, vẫn có định dạng JSON và OTLP sau đó. Không đặt `slog.SetDefault` khi chưa có `currentHandler`: handler mặc định của `slog` ghi qua package `log`, và bọc nó trong default mới sẽ tạo vòng lặp.

7. **Log, metrics và trace dùng chung một `Resource`** (`st.resource`), với `service.namespace`/`deployment.environment` từ settings, sau đó `OTEL_RESOURCE_ATTRIBUTES`, rồi `service.name`/`service.version` từ settings.

8. **Shutdown có giới hạn thời gian.** Ba provider tắt song song trong goroutine, chờ chung một `context.WithTimeout`. Hàm trả về khi hết hạn kể cả khi goroutine còn chạy.

9. **Instrument HTTP server lấy từ provider của library, không lấy từ meter toàn cục.** Meter toàn cục của OTel chỉ uỷ quyền cho provider **đầu tiên** của process; sau `Shutdown` + `Setup` mới, instrument cũ sẽ ghi vào provider đã tắt. `instruments()` tạo lại instrument khi provider đổi.

10. **Tên span không chứa URL thô.** Span SERVER tên `<METHOD> <route template>`; request không khớp route chỉ có `<METHOD>`. Adapter của từng framework chịu trách nhiệm nhận ra trường hợp không khớp route (`echo.NotFoundRouteName` ở v5, `echo.ErrNotFound` ở v4).

11. **Status code ghi lại là status client nhận được.** Adapter gọi error handler của framework ngay trong middleware rồi mới đọc status, và vẫn trả lỗi về cho middleware bên ngoài.

12. **Adapter framework phải mỏng.** Logic span, metric, semantic convention và access log nằm trong `httpserver.go` (`StartServerRequest` / `End`); adapter chỉ cung cấp route template, client IP, status, kích thước response và lỗi. Thêm framework mới (chi, gin, net/http `ServeMux`) bằng một package adapter tương tự `echo4obs`.

## Viết test

- Test không cần backend: truyền `WithSpanProcessor(tracetest.NewSpanRecorder())`, `WithMetricReader(sdkmetric.NewManualReader())`, `WithLogProcessor(...)`. Đặt endpoint thành `grpc://127.0.0.1:9` (không có gì nghe) để chắc chắn không gửi ra ngoài.
- Mỗi test gọi `Setup` và `Shutdown` trong `t.Cleanup`. Đừng chạy test song song (`t.Parallel`) trong package gốc: trạng thái là toàn cục.
- Hành vi phụ thuộc lần khởi tạo đầu tiên của process (meter toàn cục) phải test trong subprocess sạch, xem `TestMeterCreatedBeforeSetup`.
- Kiểm tra stdout bằng `captureStdout` (thay biến `stdout` của package), hoặc thay `os.Stdout` bằng pipe khi test từ package khác.
