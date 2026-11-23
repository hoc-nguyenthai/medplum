# pkg/workflow/asynq

Adapter `workflow.JobOrchestrator` bền vững bằng [asynq](https://github.com/hibiken/asynq) — hàng đợi task trên **Redis**. Dùng khi đã có Redis và job chỉ là "chạy hàm này cho job ID này" (không cần workflow nhiều bước, timer, signal như Temporal). Đối chiếu: [`../temporal`](../temporal) cho workflow bền vững đầy đủ, `workflow.NewInline` cho goroutine trong process (mất job khi restart).

## Mô hình

```
API process                                   Redis                      Worker process (cùng hoặc khác API)
Submit(jobID) ──► task "<TaskType>:<jobID>" ──► queue <Queue> ──► handle ──► JobRunner(ctx, jobID)
                  {job_id, traceparent}                              │
                                                                     └─ lỗi ⇒ retry / archive; ctx bị hủy ⇒ trả về hàng đợi
```

- Mỗi job là **một task**. Các bước bên trong job (OCR, kiểm tra, lưu...) do `JobRunner` của app quyết định và chạy tuần tự trong lần chạy đó (thường qua `workflow.InlineStepExecutor`).
- API và worker có thể là **cùng process** (`StartWorker`) hoặc **hai process** dùng chung `RedisURL` và `Queue`. API-only chỉ cần `Submit`.
- `traceparent` của request được lưu trong task, nên span `job …` nối tiếp trace của request đã xếp job (qua `trivita-observability-go`).

## Dùng

```go
import (
	"trivita-fhir/pkg/workflow"
	wfasynq "trivita-fhir/pkg/workflow/asynq"
)

// run phải idempotent: có thể bị chạy lại từ đầu cho cùng jobID.
run := func(ctx context.Context, jobID string) error { return pipeline.Run(ctx, jobID) }

o, err := wfasynq.New(run, wfasynq.Config{
	RedisURL: "redis://localhost:6379/0",
	Queue:    "pdf-ocr",
	TaskType: "ocr_pipeline",
})
if err != nil { /* RedisURL thiếu hoặc sai */ }
defer o.Shutdown(ctx)          // đặt trong composition root, theo thứ tự tắt

if err := o.StartWorker(); err != nil { /* bỏ qua ở pod chỉ-API */ }

var _ workflow.JobOrchestrator = o
err = o.Submit(ctx, jobID)     // lỗi ⇒ job CHƯA được xếp hàng: báo lỗi cho client, đừng nói "đã nhận"
```

`New` nối **lazy**: không lỗi khi Redis chưa chạy lúc khởi động; `Ping`/`Submit` mới báo lỗi. Dùng `Ping(ctx)` làm check `/readyz`. Muốn dùng client Redis có sẵn (vd gắn `redisotel`) thì `NewWithClient(rdb, run, cfg)` — orchestrator sở hữu client và đóng nó ở `Shutdown`.

## Cấu hình (`Config`)

| Trường | Mặc định | Ý nghĩa |
|---|---|---|
| `RedisURL` | — (bắt buộc với `New`) | `redis://host:6379/0` |
| `Queue` | `default` | hàng đợi; **API và worker phải trùng** |
| `TaskType` | `workflow:run_job` | loại task; cũng là tiền tố ID task (`<TaskType>:<jobID>`) |
| `MaxRetry` | `3` | số lần chạy **lại** sau lần đầu khi `JobRunner` trả lỗi; số âm = không retry |
| `Timeout` | `30m` | thời gian tối đa của MỘT lần chạy job |
| `Retention` | `24h` | giữ task đã xong trong Redis để `Submit` lặp lại là no-op |
| `RetryInterval` | `5s` | chờ trước lần chạy lại đầu, gấp đôi mỗi lần |
| `WorkerConcurrency` | `2` | số job đồng thời mỗi worker |
| `WorkerStopTimeout` | `20s` | chờ job đang chạy khi tắt worker; quá hạn thì hủy `ctx` và trả task về hàng đợi |
| `PollInterval` | `1s` | chu kỳ hỏi Redis khi hàng đợi rỗng |

Đặt `WorkerStopTimeout` nhỏ hơn thời gian tắt của process (vd `SHUTDOWN_TIMEOUT − 5s` như pdf-ocr) để kịp trả task về hàng đợi trước khi bị SIGKILL.

## Đảm bảo và hành vi

- **At-least-once, chạy lại từ đầu.** Task nằm trong Redis nên job còn nguyên khi API restart và khi chưa có worker. Worker tắt giữa chừng: quá `WorkerStopTimeout` thì `ctx` bị hủy và task về hàng đợi cho worker khác. Worker **chết cứng**: asynq giao lại sau khi hết lease (~30s). Hệ quả: `JobRunner` phải idempotent, và tác dụng phụ ra ngoài (webhook, email) có thể xảy ra **nhiều hơn một lần**.
- **`ctx` bị hủy khi worker tắt phải được tôn trọng.** `JobRunner` nên trả lỗi `ctx` mà **không** ghi trạng thái "failed" (pdf-ocr làm vậy bằng `LeaveRunningOnCancel`); nếu không, job được giao lại sẽ chạy trên bản ghi đã bị đánh dấu thất bại.
- **Idempotent khi xếp hàng.** ID task là `<TaskType>:<jobID>`; `Submit` lặp lại cho cùng job (API retry) là no-op, kể cả khi job đã xong, **trong `Retention`**. Sau thời gian đó cùng `jobID` sẽ chạy lại — job ID nên là duy nhất lâu dài.
- **Retry.** `JobRunner` trả lỗi ⇒ chạy lại tối đa `MaxRetry` lần (backoff gấp đôi từ `RetryInterval`). Asynq chuyển task retry về hàng đợi theo chu kỳ ~5s nên độ trễ thực tế có thể vài giây. Lỗi nghiệp vụ không nên retry thì để `JobRunner` ghi kết quả và trả `nil`.
- **Archive, không mất.** Hết số lần retry thì task vào **archive** của asynq (xem/chạy lại bằng `asynq` CLI hoặc asynqmon). Payload hỏng (không phải JSON hợp lệ, thiếu `job_id`) vào archive ngay (`SkipRetry`), không retry vô ích.
- **Shutdown** idempotent; sau `Shutdown` thì `Submit` và `StartWorker` trả lỗi.
- Log của asynq đi qua `slog` (trường `logger=asynq`), nên theo cấu hình JSON/OTLP của service.

## Bảo mật và vận hành

- Task chỉ chứa `job_id` và `traceparent` — **không** đưa dữ liệu nhạy cảm vào payload; dữ liệu thật nằm ở storage mà `JobRunner` đọc theo `jobID`.
- Redis là điểm phụ thuộc duy nhất: Redis chết thì `Submit` lỗi (client nhận 5xx, không bị báo nhầm là đã xếp hàng) và worker ngừng nhận job; job đã xếp vẫn còn khi Redis phục hồi nếu bật persistence (AOF/RDB). Hãy cấu hình persistence cho Redis dùng làm hàng đợi.
- Nhiều app dùng chung một Redis thì đặt `Queue` khác nhau (và `TaskType` khác nhau cho mỗi loại job).
- Chưa kiểm với Redis Cluster/Sentinel.

## Test

```bash
go test ./pkg/workflow/asynq        # cần redis-server trong PATH; không có thì các test tích hợp tự bỏ qua
go test -short ./pkg/workflow/asynq # chỉ test không cần Redis
```

Test tích hợp khởi động **`redis-server` thật** trên cổng ngẫu nhiên (asynq dùng nhiều script Lua nên không giả lập bằng miniredis) và kiểm: chạy đúng một lần và `Submit` lặp là no-op, job chờ tới khi có worker, retry, vào archive sau `MaxRetry`, giao lại khi worker tắt giữa chừng, payload hỏng không retry, vòng đời `Shutdown`/`StartWorker`, kết nối lazy khi Redis chết. Retry và archive mất vài giây mỗi test do chu kỳ chuyển task của asynq.

Dùng trong repo: `apps/pdf-ocr` (`WORKFLOW_DRIVER=asynq`) — xem [README của pdf-ocr](../../../apps/pdf-ocr/README.md).
